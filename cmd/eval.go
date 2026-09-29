package cmd

// `afy eval`: run an agent's dataset against its current release, and read,
// compare and cancel the results. An AGENT VERB GROUP, like `afy schedule`: its
// subcommands take the agent as their first argument. It is never
// `afy eval <agent>`, because an agent may be named `list`.
//
// BUILT FOR CI. `afy eval run` waits for the eval by default and exits with a
// code a pipeline can gate on:
//
//	0  the eval completed and every threshold held
//	4  --fail-under or --max-drop was breached (exitThresholdBreached)
//	1  the eval ended errored or cancelled, or a request failed
//	2  the input was refused before any request (a bad flag or file)
//	3  not logged in
//
// With `-o json` stdout holds exactly one JSON value and nothing else; progress
// lines and errors go to stderr.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

// evalPollInterval is how often `afy eval run` asks for progress. A variable so
// the tests do not wait.
var evalPollInterval = 5 * time.Second

// thresholdBreached is an eval that completed but missed --fail-under or
// --max-drop: exit 4, through ExitCode.
type thresholdBreached struct{ msg string }

func (e *thresholdBreached) Error() string { return e.msg }

// reported is an error the command has already printed; SilenceErrors keeps
// cobra from printing it a second time, and errors.As still finds its cause
// for the exit code.
type reported struct{ err error }

func (r *reported) Error() string { return r.err.Error() }
func (r *reported) Unwrap() error { return r.err }

// evalIO is one eval or datasets command's context. Built by newEvalIO from
// the process; a test builds its own around an httptest server.
type evalIO struct {
	client *api.Client
	stdout io.Writer
	stderr io.Writer
	stdin  io.Reader
	json   bool
	sleep  func(time.Duration)
}

func newEvalIO() *evalIO {
	return &evalIO{
		client: api.NewClient(),
		stdout: os.Stdout,
		stderr: os.Stderr,
		stdin:  os.Stdin,
		json:   config.Get().OutputFormat == "json",
		sleep:  time.Sleep,
	}
}

func (e *evalIO) printJSON(v interface{}) error {
	enc := json.NewEncoder(e.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// note is a human line: stdout normally, stderr under -o json.
func (e *evalIO) note(format string, args ...interface{}) {
	w := e.stdout
	if e.json {
		w = e.stderr
	}
	fmt.Fprintf(w, format+"\n", args...)
}

// evalErrorJSON is what -o json prints on stderr for a failure: the API's
// status, code, message and violations when the API answered.
type evalErrorJSON struct {
	Error struct {
		Status     int                      `json:"status,omitempty"`
		Code       string                   `json:"code,omitempty"`
		Message    string                   `json:"message"`
		Violations []map[string]interface{} `json:"violations,omitempty"`
	} `json:"error"`
}

// fail prints err once, with the API's violations and a hint for the codes a
// customer can act on, and returns it marked as reported.
func (e *evalIO) fail(err error) error {
	var apiErr *api.APIError
	isAPI := errors.As(err, &apiErr)
	if e.json {
		var out evalErrorJSON
		out.Error.Message = err.Error()
		if isAPI {
			out.Error.Status, out.Error.Code, out.Error.Message = apiErr.StatusCode, apiErr.Code, apiErr.Message
			out.Error.Violations = apiErr.Violations
		}
		data, _ := json.Marshal(out)
		fmt.Fprintln(e.stderr, string(data))
		return &reported{err}
	}
	fmt.Fprintln(e.stderr, "Error: "+err.Error())
	if isAPI {
		for _, v := range apiErr.Violations {
			fmt.Fprintln(e.stderr, "  "+formatViolation(v))
		}
		if hint := evalHint(apiErr.Code); hint != "" {
			fmt.Fprintln(e.stderr, hint)
		}
	}
	return &reported{err}
}

func formatViolation(v map[string]interface{}) string {
	var where []string
	for _, key := range []string{"case", "grader"} {
		if value, ok := v[key]; ok && value != nil {
			where = append(where, fmt.Sprintf("%s %v", key, value))
		}
	}
	if field, ok := v["field"].(string); ok && field != "" {
		where = append(where, "field "+field)
	}
	message, _ := v["message"].(string)
	if len(where) == 0 {
		return message
	}
	return strings.Join(where, ", ") + ": " + message
}

func evalHint(code string) string {
	switch code {
	case "EVALS_NOT_ENABLED":
		return "Evals are not enabled on this account yet."
	case "EVAL_DATASET_NOT_FOUND":
		return "Push one first: afy datasets push <agent> <name> --cases cases.jsonl --graders graders.yaml"
	case "EVAL_RELEASE_NOT_CURRENT":
		return "Only the agent's current release can be evaluated."
	case "EVAL_JUDGE_SECRET_MISSING":
		return "Set the judge's key as a secret: afy secrets set <agent> NAME=value"
	}
	return ""
}

// --- the command tree --------------------------------------------------------

var evalCmd = &cobra.Command{
	Use:   "eval",
	Short: "Evaluate an agent on a dataset of test cases",
	Long: `Run a dataset of test cases against an agent's current release and grade
every result. Each case is an ordinary run of the agent, billed as a run.

  afy datasets push my-agent qa --cases cases.jsonl --graders graders.yaml
  afy eval run my-agent --dataset qa --fail-under 0.9

Exit codes of 'afy eval run': 0 completed and every threshold held, 4 a
threshold was breached, 1 the eval errored or was cancelled (or a request
failed), 2 the input was refused, 3 not logged in.`,
}

var evalRunCmd = &cobra.Command{
	Use:   "run <agent>",
	Short: "Evaluate the agent's current release on a dataset",
	Long: `Start an eval of the agent's current release on a dataset version, wait for
it, and print its scorecard.

--fail-under RATE exits 4 when the pass rate is below RATE (0 to 1).
--max-drop DELTA exits 4 when the pass rate fell by more than DELTA against a
baseline: an eval id (--baseline) or the newest completed eval of a release on
the same dataset version (--baseline-release).`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		return evalRun(newEvalIO(), args[0], evalRunFlags{
			dataset: evalRunDataset, datasetVersion: evalRunDatasetVersion, release: evalRunRelease,
			concurrency: evalRunConcurrency, detach: evalRunDetach, failUnder: evalRunFailUnder,
			maxDrop: evalRunMaxDrop, baseline: evalRunBaseline, baselineRelease: evalRunBaselineRelease,
		})
	},
}

var evalListCmd = &cobra.Command{
	Use:   "list <agent>",
	Short: "List an agent's evals, newest first",
	Args:  refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		return evalList(newEvalIO(), args[0], evalListFlags{
			dataset: evalListDataset, release: evalListRelease, status: evalListStatus, limit: evalListLimit,
		})
	},
}

var evalShowCmd = &cobra.Command{
	Use:   "show <agent> <eval-id>",
	Short: "Show an eval's progress and scorecard",
	Args:  refuseArgs(cobra.ExactArgs(2)),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		return evalShow(newEvalIO(), args[0], args[1], evalShowCases)
	},
}

var evalCompareCmd = &cobra.Command{
	Use:   "compare <agent> <base-eval-id> <head-eval-id>",
	Short: "Compare two evals of the same dataset version, case by case",
	Args:  refuseArgs(cobra.ExactArgs(3)),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		return evalCompare(newEvalIO(), args[0], args[1], args[2])
	},
}

var evalCancelCmd = &cobra.Command{
	Use:   "cancel <agent> <eval-id>",
	Short: "Stop an eval: pending cases are skipped, runs in flight finish",
	Args:  refuseArgs(cobra.ExactArgs(2)),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		return evalCancel(newEvalIO(), args[0], args[1])
	},
}

type evalRunFlags struct {
	dataset         string
	datasetVersion  int
	release         int
	concurrency     int
	detach          bool
	failUnder       float64
	maxDrop         float64
	baseline        string
	baselineRelease int
}

type evalListFlags struct {
	dataset string
	release int
	status  string
	limit   int
}

// One variable per flag, each bound by a literal `Flags().XxxVar(&v, ...)` call
// that docs-site's surface extractor reads (see cmd/vectors.go); RunE gathers
// them into the flags struct the command function takes.
var (
	evalRunDataset         string
	evalRunDatasetVersion  int
	evalRunRelease         int
	evalRunConcurrency     int
	evalRunDetach          bool
	evalRunFailUnder       float64
	evalRunMaxDrop         float64
	evalRunBaseline        string
	evalRunBaselineRelease int
	evalListDataset        string
	evalListRelease        int
	evalListStatus         string
	evalListLimit          int
	evalShowCases          string
)

// Unset thresholds read as negative, so 0 stays a real value for both.
const thresholdUnset = -1

func init() {
	evalRunCmd.Flags().StringVar(&evalRunDataset, "dataset", "", "Dataset to evaluate (required)")
	evalRunCmd.Flags().IntVar(&evalRunDatasetVersion, "dataset-version", 0, "Dataset version (default: the latest)")
	evalRunCmd.Flags().IntVar(&evalRunRelease, "release", 0, "Refuse unless this is the agent's current release")
	evalRunCmd.Flags().IntVar(&evalRunConcurrency, "concurrency", 0, "Case runs in flight at once (default 2, at most 10)")
	evalRunCmd.Flags().BoolVar(&evalRunDetach, "detach", false, "Print the eval id and exit without waiting")
	evalRunCmd.Flags().Float64Var(&evalRunFailUnder, "fail-under", thresholdUnset, "Exit 4 when the pass rate is below this (0 to 1)")
	evalRunCmd.Flags().Float64Var(&evalRunMaxDrop, "max-drop", thresholdUnset, "Exit 4 when the pass rate fell by more than this against the baseline (0 to 1)")
	evalRunCmd.Flags().StringVar(&evalRunBaseline, "baseline", "", "Baseline eval id for --max-drop")
	evalRunCmd.Flags().IntVar(&evalRunBaselineRelease, "baseline-release", 0, "Baseline for --max-drop: the newest completed eval of this release on the same dataset version")

	evalListCmd.Flags().StringVar(&evalListDataset, "dataset", "", "Only evals of this dataset")
	evalListCmd.Flags().IntVar(&evalListRelease, "release", 0, "Only evals of this release")
	evalListCmd.Flags().StringVar(&evalListStatus, "status", "", "Only evals in this status: queued, running, grading, completed, errored, cancelled")
	evalListCmd.Flags().IntVar(&evalListLimit, "limit", 20, "Maximum number of evals (max 100)")

	evalShowCmd.Flags().StringVar(&evalShowCases, "cases", "", "Also list cases: all, fail or errored")

	for _, c := range []*cobra.Command{evalRunCmd, evalListCmd, evalShowCmd, evalCompareCmd, evalCancelCmd} {
		c.SilenceErrors = true
		c.SetFlagErrorFunc(refuseFlag)
	}
	evalCmd.AddCommand(evalRunCmd)
	evalCmd.AddCommand(evalListCmd)
	evalCmd.AddCommand(evalShowCmd)
	evalCmd.AddCommand(evalCompareCmd)
	evalCmd.AddCommand(evalCancelCmd)
}

// --- afy eval run ------------------------------------------------------------

func checkRunFlags(f evalRunFlags) error {
	if f.dataset == "" {
		return refuse("--dataset is required")
	}
	if f.failUnder != thresholdUnset && (f.failUnder < 0 || f.failUnder > 1) {
		return refuse("--fail-under must be from 0 to 1, got %g", f.failUnder)
	}
	hasBaseline := f.baseline != "" || f.baselineRelease > 0
	if f.baseline != "" && f.baselineRelease > 0 {
		return refuse("give --baseline or --baseline-release, not both")
	}
	if f.maxDrop != thresholdUnset {
		if f.maxDrop < 0 || f.maxDrop > 1 {
			return refuse("--max-drop must be from 0 to 1, got %g", f.maxDrop)
		}
		if !hasBaseline {
			return refuse("--max-drop needs a baseline: --baseline <eval-id> or --baseline-release N")
		}
	} else if hasBaseline {
		return refuse("--baseline and --baseline-release only apply with --max-drop")
	}
	if f.detach && (f.failUnder != thresholdUnset || f.maxDrop != thresholdUnset) {
		return refuse("--detach does not wait, so it cannot check --fail-under or --max-drop")
	}
	return nil
}

// findBaseline is the eval --max-drop compares against, checked BEFORE the new
// eval starts, so a missing baseline costs no runs.
func findBaseline(e *evalIO, agent string, f evalRunFlags, version int) (*api.EvalRun, error) {
	if f.baseline != "" {
		base, err := e.client.GetEval(agent, f.baseline)
		if err != nil {
			return nil, err
		}
		if base.Status != "completed" || base.Dataset != f.dataset || base.DatasetVersion != version {
			return nil, refuse("baseline eval %s is %s on %s@v%d; it must be a completed eval of %s@v%d",
				f.baseline, base.Status, base.Dataset, base.DatasetVersion, f.dataset, version)
		}
		return base, nil
	}
	evals, err := e.client.ListEvals(agent, api.EvalsQuery{
		Dataset: f.dataset, ReleaseVersion: f.baselineRelease, Status: "completed", Limit: 100,
	})
	if err != nil {
		return nil, err
	}
	for i := range evals {
		if evals[i].DatasetVersion == version {
			return &evals[i], nil
		}
	}
	return nil, refuse("no completed eval of release v%d on %s@v%d to compare against; run one first",
		f.baselineRelease, f.dataset, version)
}

func progressLine(ev *api.EvalRun) string {
	line := ev.Status
	if p := ev.Progress; p != nil {
		line += fmt.Sprintf(": %d/%d graded, %d running", p.Graded, p.Total, p.Dispatched)
		if p.Errored+p.Skipped > 0 {
			line += fmt.Sprintf(", %d errored, %d skipped", p.Errored, p.Skipped)
		}
	}
	return line
}

func terminal(status string) bool {
	return status == "completed" || status == "errored" || status == "cancelled"
}

// evalRunResult is `afy eval run -o json`'s one object.
type evalRunResult struct {
	Eval       *api.EvalRun        `json:"eval"`
	Comparison *api.EvalComparison `json:"comparison"`
	Thresholds struct {
		FailUnder *float64 `json:"fail_under"`
		MaxDrop   *float64 `json:"max_drop"`
		Breached  []string `json:"breached"`
	} `json:"thresholds"`
}

func evalRun(e *evalIO, agent string, f evalRunFlags) error {
	if err := checkRunFlags(f); err != nil {
		return e.fail(err)
	}
	version := f.datasetVersion
	if version == 0 {
		dataset, err := e.client.GetEvalDataset(agent, f.dataset)
		if err != nil {
			return e.fail(err)
		}
		if dataset.LatestVersion == nil {
			return e.fail(refuse("dataset %q has no version yet: push one with 'afy datasets push'", f.dataset))
		}
		version = *dataset.LatestVersion
	}
	var baseline *api.EvalRun
	if f.maxDrop != thresholdUnset {
		var err error
		if baseline, err = findBaseline(e, agent, f, version); err != nil {
			return e.fail(err)
		}
	}

	body := &api.EvalRunCreate{Dataset: f.dataset, DatasetVersion: &version}
	if f.release > 0 {
		body.ReleaseVersion = &f.release
	}
	if f.concurrency > 0 {
		body.Concurrency = &f.concurrency
	}
	started, err := e.client.StartEval(agent, body)
	if err != nil {
		return e.fail(err)
	}
	if f.detach {
		if e.json {
			return e.printJSON(started)
		}
		fmt.Fprintln(e.stdout, started.ID)
		return nil
	}
	e.note("Evaluating %s v%d on %s@v%d: eval %s", agent, started.ReleaseVersion, f.dataset, version, started.ID)

	var ev *api.EvalRun
	last := ""
	for {
		if ev, err = e.client.GetEval(agent, started.ID); err != nil {
			return e.fail(err)
		}
		if line := progressLine(ev); line != last {
			e.note("%s", line)
			last = line
		}
		if terminal(ev.Status) {
			break
		}
		e.sleep(evalPollInterval)
	}

	var result evalRunResult
	result.Eval = ev
	result.Thresholds.Breached = []string{}
	if ev.Status == "completed" {
		if f.failUnder != thresholdUnset {
			result.Thresholds.FailUnder = &f.failUnder
			if ev.Summary == nil || ev.Summary.PassRate == nil || *ev.Summary.PassRate < f.failUnder {
				result.Thresholds.Breached = append(result.Thresholds.Breached, "fail_under")
			}
		}
		if baseline != nil {
			result.Thresholds.MaxDrop = &f.maxDrop
			comparison, err := e.client.CompareEvals(agent, baseline.ID, ev.ID)
			if err != nil {
				return e.fail(err)
			}
			result.Comparison = comparison
			if comparison.Delta.PassRate == nil || -*comparison.Delta.PassRate > f.maxDrop {
				result.Thresholds.Breached = append(result.Thresholds.Breached, "max_drop")
			}
		}
	}

	if e.json {
		if err := e.printJSON(result); err != nil {
			return err
		}
	} else {
		printScorecard(e.stdout, ev)
		if result.Comparison != nil {
			printComparison(e.stdout, result.Comparison)
		}
	}

	switch {
	case ev.Status != "completed":
		reason := ""
		if ev.StatusReason != nil {
			reason = " (" + *ev.StatusReason + ")"
		}
		return e.fail(fmt.Errorf("eval %s ended %s%s", ev.ID, ev.Status, reason))
	case len(result.Thresholds.Breached) > 0:
		return e.fail(&thresholdBreached{msg: breachMessage(ev, result.Comparison, f)})
	}
	return nil
}

func breachMessage(ev *api.EvalRun, comparison *api.EvalComparison, f evalRunFlags) string {
	var parts []string
	if f.failUnder != thresholdUnset && (ev.Summary == nil || ev.Summary.PassRate == nil || *ev.Summary.PassRate < f.failUnder) {
		parts = append(parts, fmt.Sprintf("pass rate %s is under --fail-under %g", formatRate(passRate(ev)), f.failUnder))
	}
	if comparison != nil && (comparison.Delta.PassRate == nil || -*comparison.Delta.PassRate > f.maxDrop) {
		parts = append(parts, fmt.Sprintf("pass rate moved %s against the baseline, more than --max-drop %g", formatDelta(comparison.Delta.PassRate), f.maxDrop))
	}
	return "threshold breached: " + strings.Join(parts, "; ")
}

// --- rendering ---------------------------------------------------------------

func passRate(ev *api.EvalRun) *float64 {
	if ev.Summary == nil {
		return nil
	}
	return ev.Summary.PassRate
}

func formatRate(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", *v*100)
}

func formatScore(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.3f", *v)
}

func formatDelta(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%+.1f pts", *v*100)
}

func formatMs(v *int) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.1fs", float64(*v)/1000)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func printScorecard(w io.Writer, ev *api.EvalRun) {
	fmt.Fprintf(w, "\nEval %s  %s@v%d  release v%d  %s", ev.ID, ev.Dataset, ev.DatasetVersion, ev.ReleaseVersion, ev.Status)
	if ev.StatusReason != nil {
		fmt.Fprintf(w, " (%s)", *ev.StatusReason)
	}
	fmt.Fprintln(w)
	s := ev.Summary
	if s == nil {
		return
	}
	fmt.Fprintf(w, "  Pass rate:   %s  (%d/%d graded passed)\n", formatRate(s.PassRate), s.CasesPassed, s.CasesGraded)
	fmt.Fprintf(w, "  Mean score:  %s\n", formatScore(s.MeanScore))
	fmt.Fprintf(w, "  Cases:       %d total, %d failed, %d errored, %d skipped, %d run failures\n",
		s.CasesTotal, s.CasesFailed, s.CasesErrored, s.CasesSkipped, s.RunsFailed)
	fmt.Fprintf(w, "  Duration:    p50 %s, p95 %s, max %s\n", formatMs(s.DurationMs.P50), formatMs(s.DurationMs.P95), formatMs(s.DurationMs.Max))
	if s.Judge.Calls > 0 {
		fmt.Fprintf(w, "  Judge:       %d calls, %d input / %d output tokens\n", s.Judge.Calls, s.Judge.InputTokens, s.Judge.OutputTokens)
	}
	if len(s.Graders) == 0 {
		return
	}
	rows := make([][]string, 0, len(s.Graders))
	for _, g := range s.Graders {
		rows = append(rows, []string{g.Name, g.Type, fmt.Sprintf("%d/%d", g.Passed, g.Graded), fmt.Sprint(g.Errored), formatScore(g.MeanScore)})
	}
	fmt.Fprintln(w)
	output.RenderTable(w, []string{"Grader", "Type", "Passed", "Errored", "Mean score"}, rows)
}

func printComparison(w io.Writer, c *api.EvalComparison) {
	fmt.Fprintf(w, "\nCompared with eval %s (release v%d) on %s@v%d:\n", c.Base.EvalID, c.Base.ReleaseVersion, c.Dataset.Name, c.Dataset.Version)
	fmt.Fprintf(w, "  Pass rate:   %s -> %s  (%s)\n", formatRate(c.Base.PassRate), formatRate(c.Head.PassRate), formatDelta(c.Delta.PassRate))
	fmt.Fprintf(w, "  Mean score:  %s -> %s\n", formatScore(c.Base.MeanScore), formatScore(c.Head.MeanScore))
	if c.SameCode {
		fmt.Fprintln(w, "  Same code on both sides: differences are the agent's own variance.")
	}
	fmt.Fprintf(w, "  %d regressions, %d fixes, %d unchanged passing, %d unchanged failing, %d not comparable\n",
		len(c.Regressions), len(c.Fixes), c.UnchangedPass, c.UnchangedFail, len(c.NotComparable))
	for _, group := range []struct {
		title string
		cases []api.EvalComparisonCase
	}{{"Regressions (pass -> fail)", c.Regressions}, {"Fixes (fail -> pass)", c.Fixes}} {
		if len(group.cases) == 0 {
			continue
		}
		rows := make([][]string, 0, len(group.cases))
		for _, cs := range group.cases {
			rows = append(rows, []string{cs.CaseKey, formatScore(cs.BaseScore), formatScore(cs.HeadScore)})
		}
		fmt.Fprintf(w, "\n%s\n", group.title)
		output.RenderTable(w, []string{"Case", "Base score", "Head score"}, rows)
	}
}

// --- list / show / compare / cancel -------------------------------------------

func evalList(e *evalIO, agent string, f evalListFlags) error {
	evals, err := e.client.ListEvals(agent, api.EvalsQuery{
		Dataset: f.dataset, ReleaseVersion: f.release, Status: f.status, Limit: f.limit,
	})
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		return e.printJSON(evals)
	}
	if len(evals) == 0 {
		fmt.Fprintf(e.stdout, "No evals for '%s'.\n", agent)
		return nil
	}
	rows := make([][]string, 0, len(evals))
	for i := range evals {
		ev := &evals[i]
		cases := "-"
		if ev.Summary != nil {
			cases = fmt.Sprintf("%d/%d", ev.Summary.CasesPassed, ev.Summary.CasesTotal)
		}
		rows = append(rows, []string{
			shortID(ev.ID), fmt.Sprintf("%s@v%d", ev.Dataset, ev.DatasetVersion),
			fmt.Sprintf("v%d", ev.ReleaseVersion), ev.Status, formatRate(passRate(ev)), cases,
			formatUTCTime(&ev.CreatedAt),
		})
	}
	output.RenderTable(e.stdout, []string{"Eval", "Dataset", "Release", "Status", "Pass rate", "Passed", "Created"}, rows)
	return nil
}

var caseFilters = map[string]string{"all": "", "fail": "fail", "errored": "errored"}

// allCases pages through an eval's case results.
func allCases(e *evalIO, agent, evalID, verdict string) ([]api.EvalCaseResult, error) {
	var out []api.EvalCaseResult
	after := 0
	for {
		page, err := e.client.ListEvalCases(agent, evalID, verdict, 100, after)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Cases...)
		if page.NextAfterOrdinal == nil {
			return out, nil
		}
		after = *page.NextAfterOrdinal
	}
}

func caseReason(c api.EvalCaseResult) string {
	if c.StatusReason != nil {
		return *c.StatusReason
	}
	for _, g := range c.Grades {
		if passed, _ := g["passed"].(bool); !passed {
			reason, _ := g["reason"].(string)
			if errText, _ := g["error"].(string); errText != "" {
				reason = errText
			}
			return fmt.Sprintf("%v: %s", g["grader"], reason)
		}
	}
	return ""
}

func evalShow(e *evalIO, agent, evalID, cases string) error {
	verdict, ok := caseFilters[cases]
	if cases != "" && !ok {
		return e.fail(refuse("--cases must be all, fail or errored, got %q", cases))
	}
	ev, err := e.client.GetEval(agent, evalID)
	if err != nil {
		return e.fail(err)
	}
	var results []api.EvalCaseResult
	if cases != "" {
		if results, err = allCases(e, agent, evalID, verdict); err != nil {
			return e.fail(err)
		}
	}
	if e.json {
		return e.printJSON(struct {
			Eval  *api.EvalRun         `json:"eval"`
			Cases []api.EvalCaseResult `json:"cases,omitempty"`
		}{ev, results})
	}
	printScorecard(e.stdout, ev)
	if p := ev.Progress; p != nil && !terminal(ev.Status) {
		fmt.Fprintf(e.stdout, "  Progress:    %s\n", progressLine(ev))
	}
	if cases != "" {
		rows := make([][]string, 0, len(results))
		for _, c := range results {
			verdictText := "-"
			if c.Verdict != nil {
				verdictText = *c.Verdict
			}
			rows = append(rows, []string{c.Key, c.Status, verdictText, formatScore(c.Score), caseReason(c)})
		}
		fmt.Fprintln(e.stdout)
		output.RenderTable(e.stdout, []string{"Case", "Status", "Verdict", "Score", "Why"}, rows)
	}
	return nil
}

func evalCompare(e *evalIO, agent, base, head string) error {
	comparison, err := e.client.CompareEvals(agent, base, head)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		return e.printJSON(comparison)
	}
	printComparison(e.stdout, comparison)
	return nil
}

func evalCancel(e *evalIO, agent, evalID string) error {
	ev, err := e.client.CancelEval(agent, evalID)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		return e.printJSON(ev)
	}
	fmt.Fprintf(e.stdout, "Cancelling eval %s: pending cases are skipped, runs in flight finish.\n", ev.ID)
	return nil
}
