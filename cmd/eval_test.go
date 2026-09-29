package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// fakeCP is a control plane for the eval and datasets commands: routes answer
// from handlers keyed by "METHOD path", and every request is recorded.
type fakeCP struct {
	t        *testing.T
	mu       sync.Mutex
	routes   map[string]func(r *http.Request, body []byte) (int, interface{})
	requests []string
	bodies   map[string][]byte
}

func newFakeCP(t *testing.T) (*fakeCP, *httptest.Server) {
	f := &fakeCP{t: t, routes: map[string]func(*http.Request, []byte) (int, interface{}){}, bodies: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.requests = append(f.requests, key+"?"+r.URL.RawQuery)
		f.bodies[key] = body
		handler, ok := f.routes[key]
		f.mu.Unlock()
		if !ok {
			t.Errorf("unexpected request %s", key)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		status, out := handler(r, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if out != nil {
			_ = json.NewEncoder(w).Encode(out)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeCP) on(key string, handler func(r *http.Request, body []byte) (int, interface{})) {
	f.routes[key] = handler
}

func (f *fakeCP) reply(key string, status int, out interface{}) {
	f.on(key, func(*http.Request, []byte) (int, interface{}) { return status, out })
}

func (f *fakeCP) count(prefix string) int {
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func apiError(code, message string, extras map[string]interface{}) map[string]interface{} {
	detail := map[string]interface{}{"code": code, "message": message}
	for k, v := range extras {
		detail[k] = v
	}
	return map[string]interface{}{"detail": detail}
}

type evalHarness struct {
	io     *evalIO
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	sleeps []time.Duration
}

func harness(srv *httptest.Server, jsonOut bool, stdin string) *evalHarness {
	h := &evalHarness{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	h.io = &evalIO{
		client: api.NewClientWithURL(srv.URL, "afy_test_key"),
		stdout: h.stdout,
		stderr: h.stderr,
		stdin:  strings.NewReader(stdin),
		json:   jsonOut,
		sleep:  func(d time.Duration) { h.sleeps = append(h.sleeps, d) },
	}
	return h
}

// exitOf is what main would exit with for err.
func exitOf(err error) int {
	if err == nil {
		return 0
	}
	return ExitCode(err)
}

const A = "/agents/bot"

func summary(passRate float64) map[string]interface{} {
	return map[string]interface{}{
		"cases_total": 10, "cases_graded": 10, "cases_passed": int(passRate * 10), "cases_failed": 10 - int(passRate*10),
		"cases_errored": 0, "cases_skipped": 0, "pass_rate": passRate, "mean_score": passRate, "runs_failed": 0,
		"duration_ms": map[string]interface{}{"p50": 1200, "p95": 2400, "max": 3000},
		"graders":     []interface{}{map[string]interface{}{"name": "exact", "type": "exact", "graded": 10, "passed": int(passRate * 10), "errored": 0, "mean_score": passRate}},
		"judge":       map[string]interface{}{"calls": 0, "input_tokens": 0, "output_tokens": 0},
	}
}

func evalBody(id, status string, version, release int, extra map[string]interface{}) map[string]interface{} {
	body := map[string]interface{}{
		"id": id, "agent": "bot", "dataset": "qa", "dataset_version": version, "release_version": release,
		"release_code_digest": "abc", "origin": "manual", "status": status, "status_reason": nil,
		"concurrency": 2, "cancel_requested": false, "created_at": "2026-09-29T10:00:00Z",
		"started_at": nil, "finished_at": nil, "summary": nil,
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func progress(graded, dispatched int) map[string]interface{} {
	return map[string]interface{}{"total": 10, "pending": 10 - graded - dispatched, "dispatched": dispatched, "ran": 0, "graded": graded, "errored": 0, "skipped": 0}
}

// startable wires the dataset lookup and the POST, and serves GET /evals/e1 as
// the given sequence of bodies (the last one repeats).
func startable(f *fakeCP, polls ...map[string]interface{}) {
	f.reply("GET "+A+"/eval-datasets/qa", 200, map[string]interface{}{
		"id": "d1", "name": "qa", "description": nil, "latest_version": 2, "case_count": 10,
		"created_at": "2026-09-29T10:00:00Z", "updated_at": "2026-09-29T10:00:00Z", "versions": []interface{}{},
	})
	f.reply("POST "+A+"/evals", 202, evalBody("e1", "queued", 2, 7, nil))
	i := 0
	f.on("GET "+A+"/evals/e1", func(*http.Request, []byte) (int, interface{}) {
		body := polls[i]
		if i < len(polls)-1 {
			i++
		}
		return 200, body
	})
}

func completed(passRate float64) map[string]interface{} {
	return evalBody("e1", "completed", 2, 7, map[string]interface{}{"summary": summary(passRate), "progress": progress(10, 0)})
}

func runFlags(overrides func(*evalRunFlags)) evalRunFlags {
	f := evalRunFlags{dataset: "qa", failUnder: thresholdUnset, maxDrop: thresholdUnset}
	if overrides != nil {
		overrides(&f)
	}
	return f
}

// ---------------------------------------------------------------------------
// afy eval run: polling, output, and the exit-code matrix.
// ---------------------------------------------------------------------------

func TestEvalRunPollsPrintsEachChangeOnceAndThePassingScorecard(t *testing.T) {
	f, srv := newFakeCP(t)
	running := evalBody("e1", "running", 2, 7, map[string]interface{}{"progress": progress(3, 2)})
	startable(f, evalBody("e1", "queued", 2, 7, map[string]interface{}{"progress": progress(0, 0)}), running, running, completed(0.9))
	h := harness(srv, false, "")

	err := evalRun(h.io, "bot", runFlags(func(r *evalRunFlags) { r.failUnder = 0.8 }))
	if exitOf(err) != 0 {
		t.Fatalf("exit %d: %v\n%s", exitOf(err), err, h.stderr)
	}
	var body map[string]interface{}
	_ = json.Unmarshal(f.bodies["POST "+A+"/evals"], &body)
	if body["dataset"] != "qa" || body["dataset_version"] != float64(2) {
		t.Errorf("POST body = %v; want the dataset's latest version pinned", body)
	}
	out := h.stdout.String()
	if n := strings.Count(out, "running: 3/10 graded, 2 running"); n != 1 {
		t.Errorf("the unchanged running line printed %d times, want 1:\n%s", n, out)
	}
	for _, want := range []string{"queued: 0/10 graded", "completed: 10/10 graded", "Pass rate:   90.0%", "exact"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(h.sleeps) != 3 || h.sleeps[0] != 5*time.Second {
		t.Errorf("slept %v; want 3 polls 5s apart", h.sleeps)
	}
}

func TestEvalRunExitCodeMatrix(t *testing.T) {
	reason := "release_moved"
	cases := []struct {
		name  string
		final map[string]interface{}
		flags func(*evalRunFlags)
		want  int
	}{
		{"completed, no threshold", completed(0.5), nil, 0},
		{"completed, over --fail-under", completed(0.9), func(r *evalRunFlags) { r.failUnder = 0.9 }, 0},
		{"completed, under --fail-under", completed(0.7), func(r *evalRunFlags) { r.failUnder = 0.8 }, exitThresholdBreached},
		{"errored", evalBody("e1", "errored", 2, 7, map[string]interface{}{"status_reason": reason, "summary": summary(1)}), func(r *evalRunFlags) { r.failUnder = 0.1 }, exitRequestFailed},
		{"cancelled", evalBody("e1", "cancelled", 2, 7, map[string]interface{}{"summary": summary(1)}), nil, exitRequestFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFakeCP(t)
			startable(f, tc.final)
			h := harness(srv, false, "")
			err := evalRun(h.io, "bot", runFlags(tc.flags))
			if got := exitOf(err); got != tc.want {
				t.Errorf("exit %d, want %d (%v)\n%s", got, tc.want, err, h.stderr)
			}
		})
	}
}

func TestEvalRunErroredNamesTheReason(t *testing.T) {
	f, srv := newFakeCP(t)
	startable(f, evalBody("e1", "errored", 2, 7, map[string]interface{}{"status_reason": "SOFT_CAP_EXCEEDED"}))
	h := harness(srv, false, "")
	_ = evalRun(h.io, "bot", runFlags(nil))
	if !strings.Contains(h.stderr.String(), "ended errored (SOFT_CAP_EXCEEDED)") {
		t.Errorf("stderr = %q", h.stderr)
	}
}

func TestEvalRunMaxDropAgainstABaselineRelease(t *testing.T) {
	for _, tc := range []struct {
		delta float64
		want  int
	}{{-0.05, 0}, {-0.2, exitThresholdBreached}, {0.1, 0}} {
		t.Run(fmt.Sprint(tc.delta), func(t *testing.T) {
			f, srv := newFakeCP(t)
			startable(f, completed(0.8))
			f.on("GET "+A+"/evals", func(r *http.Request, _ []byte) (int, interface{}) {
				q := r.URL.Query()
				if q.Get("release_version") != "5" || q.Get("status") != "completed" || q.Get("dataset") != "qa" {
					t.Errorf("baseline query = %v", q)
				}
				// Newest first: an eval of another version, then the one to use.
				return 200, []interface{}{evalBody("other", "completed", 1, 5, nil), evalBody("base", "completed", 2, 5, nil)}
			})
			f.on("GET "+A+"/eval-comparisons", func(r *http.Request, _ []byte) (int, interface{}) {
				if r.URL.Query().Get("base") != "base" || r.URL.Query().Get("head") != "e1" {
					t.Errorf("compared %v", r.URL.Query())
				}
				return 200, map[string]interface{}{
					"dataset":     map[string]interface{}{"name": "qa", "version": 2},
					"base":        map[string]interface{}{"eval_id": "base", "release_version": 5},
					"head":        map[string]interface{}{"eval_id": "e1", "release_version": 7},
					"delta":       map[string]interface{}{"pass_rate": tc.delta, "mean_score": 0},
					"regressions": []interface{}{map[string]interface{}{"case_key": "q1", "base_score": 1, "head_score": 0}},
					"fixes":       []interface{}{}, "graders": []interface{}{}, "not_comparable": []interface{}{},
				}
			})
			h := harness(srv, false, "")
			err := evalRun(h.io, "bot", runFlags(func(r *evalRunFlags) { r.maxDrop = 0.1; r.baselineRelease = 5 }))
			if got := exitOf(err); got != tc.want {
				t.Errorf("exit %d, want %d (%v)", got, tc.want, err)
			}
			if !strings.Contains(h.stdout.String(), "Regressions (pass -> fail)") {
				t.Errorf("the comparison was not printed:\n%s", h.stdout)
			}
		})
	}
}

func TestEvalRunRefusesAMissingBaselineBeforeStartingAnything(t *testing.T) {
	f, srv := newFakeCP(t)
	startable(f, completed(1))
	f.reply("GET "+A+"/evals", 200, []interface{}{evalBody("other", "completed", 1, 5, nil)})
	h := harness(srv, false, "")
	err := evalRun(h.io, "bot", runFlags(func(r *evalRunFlags) { r.maxDrop = 0.1; r.baselineRelease = 5 }))
	if exitOf(err) != exitInputRefused {
		t.Fatalf("exit %d, want 2 (%v)", exitOf(err), err)
	}
	if f.count("POST") != 0 {
		t.Errorf("an eval was started although it had nothing to compare against")
	}
	if !strings.Contains(h.stderr.String(), "no completed eval of release v5 on qa@v2") {
		t.Errorf("stderr = %q", h.stderr)
	}
}

func TestEvalRunChecksAnExplicitBaseline(t *testing.T) {
	f, srv := newFakeCP(t)
	startable(f, completed(1))
	f.reply("GET "+A+"/evals/base", 200, evalBody("base", "running", 2, 5, nil))
	h := harness(srv, false, "")
	err := evalRun(h.io, "bot", runFlags(func(r *evalRunFlags) { r.maxDrop = 0.1; r.baseline = "base" }))
	if exitOf(err) != exitInputRefused || f.count("POST") != 0 {
		t.Errorf("exit %d, posts %d; want a refusal before the start", exitOf(err), f.count("POST"))
	}
}

func TestEvalRunFlagRefusals(t *testing.T) {
	for name, flags := range map[string]func(*evalRunFlags){
		"no dataset":                func(r *evalRunFlags) { r.dataset = "" },
		"fail-under out of range":   func(r *evalRunFlags) { r.failUnder = 1.5 },
		"max-drop without baseline": func(r *evalRunFlags) { r.maxDrop = 0.1 },
		"baseline without max-drop": func(r *evalRunFlags) { r.baseline = "x" },
		"both baselines":            func(r *evalRunFlags) { r.maxDrop = 0.1; r.baseline = "x"; r.baselineRelease = 3 },
		"detach with a threshold":   func(r *evalRunFlags) { r.detach = true; r.failUnder = 0.5 },
	} {
		t.Run(name, func(t *testing.T) {
			f, srv := newFakeCP(t)
			h := harness(srv, false, "")
			if got := exitOf(evalRun(h.io, "bot", runFlags(flags))); got != exitInputRefused {
				t.Errorf("exit %d, want 2", got)
			}
			if len(f.requests) != 0 {
				t.Errorf("a refused input sent %v", f.requests)
			}
		})
	}
}

func TestEvalRunDetach(t *testing.T) {
	f, srv := newFakeCP(t)
	startable(f, completed(1))
	h := harness(srv, false, "")
	err := evalRun(h.io, "bot", runFlags(func(r *evalRunFlags) { r.detach = true; r.datasetVersion = 2 }))
	if exitOf(err) != 0 || strings.TrimSpace(h.stdout.String()) != "e1" {
		t.Errorf("exit %d, stdout %q; want the eval id alone", exitOf(err), h.stdout)
	}
	if f.count("GET "+A+"/evals/e1") != 0 || f.count("GET "+A+"/eval-datasets") != 0 {
		t.Errorf("--detach polled or looked up a version it was given: %v", f.requests)
	}
}

func TestEvalRunJSONPutsOnlyOneObjectOnStdout(t *testing.T) {
	f, srv := newFakeCP(t)
	startable(f, evalBody("e1", "running", 2, 7, map[string]interface{}{"progress": progress(1, 1)}), completed(0.7))
	h := harness(srv, true, "")
	err := evalRun(h.io, "bot", runFlags(func(r *evalRunFlags) { r.failUnder = 0.8 }))
	if exitOf(err) != exitThresholdBreached {
		t.Fatalf("exit %d", exitOf(err))
	}
	dec := json.NewDecoder(h.stdout)
	var result struct {
		Eval       api.EvalRun `json:"eval"`
		Thresholds struct {
			FailUnder *float64 `json:"fail_under"`
			Breached  []string `json:"breached"`
		} `json:"thresholds"`
	}
	if err := dec.Decode(&result); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout)
	}
	if dec.More() {
		t.Errorf("stdout holds more than one JSON value")
	}
	if result.Eval.ID != "e1" || *result.Thresholds.FailUnder != 0.8 || strings.Join(result.Thresholds.Breached, ",") != "fail_under" {
		t.Errorf("result = %+v", result)
	}
	if !strings.Contains(h.stderr.String(), "running: 1/10 graded") {
		t.Errorf("progress did not go to stderr: %q", h.stderr)
	}
	var failure evalErrorJSON
	lines := strings.Split(strings.TrimSpace(h.stderr.String()), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &failure); err != nil || !strings.Contains(failure.Error.Message, "threshold breached") {
		t.Errorf("the breach is not a JSON error on stderr: %q", lines[len(lines)-1])
	}
}

// Every EVAL_* envelope POST /evals can answer (and the dark-launch 503) is
// reported with its code, exits 1, and under -o json lands on stderr as the
// API's status and code.
func TestEvalRunReportsEveryEnvelope(t *testing.T) {
	codes := map[string]int{
		"EVALS_NOT_ENABLED": 503, "EVAL_DATASET_NOT_FOUND": 404, "EVAL_DATASET_VERSION_NOT_FOUND": 404,
		"EVAL_RELEASE_NOT_CURRENT": 409, "EVAL_TOO_MANY_ACTIVE": 429, "EVAL_JUDGE_SECRET_MISSING": 422,
		"EVAL_GRADER_INVALID": 422, "AGENT_NOT_DEPLOYED": 422, "AGENT_RUN_INELIGIBLE_STATE": 409,
		"SOFT_CAP_EXCEEDED": 403,
	}
	for code, status := range codes {
		for _, jsonOut := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s json=%v", code, jsonOut), func(t *testing.T) {
				f, srv := newFakeCP(t)
				f.reply("POST "+A+"/evals", status, apiError(code, "refused: "+code, nil))
				h := harness(srv, jsonOut, "")
				err := evalRun(h.io, "bot", runFlags(func(r *evalRunFlags) { r.datasetVersion = 2 }))
				if exitOf(err) != exitRequestFailed {
					t.Errorf("exit %d, want 1", exitOf(err))
				}
				if h.stdout.Len() != 0 {
					t.Errorf("stdout carries %q; a failure belongs on stderr", h.stdout)
				}
				if jsonOut {
					var failure evalErrorJSON
					if err := json.Unmarshal(h.stderr.Bytes(), &failure); err != nil || failure.Error.Code != code || failure.Error.Status != status {
						t.Errorf("stderr = %q", h.stderr)
					}
				} else if !strings.Contains(h.stderr.String(), code) {
					t.Errorf("stderr lacks the code: %q", h.stderr)
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// list / show / compare / cancel.
// ---------------------------------------------------------------------------

func TestEvalListAndItsFilters(t *testing.T) {
	f, srv := newFakeCP(t)
	f.on("GET "+A+"/evals", func(r *http.Request, _ []byte) (int, interface{}) {
		if got := r.URL.RawQuery; got != "dataset=qa&limit=20&release_version=7&status=completed" {
			t.Errorf("query %q", got)
		}
		return 200, []interface{}{completed(0.9)}
	})
	h := harness(srv, false, "")
	if err := evalList(h.io, "bot", evalListFlags{dataset: "qa", release: 7, status: "completed", limit: 20}); err != nil {
		t.Fatal(err)
	}
	if out := h.stdout.String(); !strings.Contains(out, "qa@v2") || !strings.Contains(out, "90.0%") || !strings.Contains(out, "9/10") {
		t.Errorf("table:\n%s", out)
	}
}

func TestEvalShowPagesThroughTheCases(t *testing.T) {
	f, srv := newFakeCP(t)
	f.reply("GET "+A+"/evals/e1", 200, completed(0.5))
	f.on("GET "+A+"/evals/e1/cases", func(r *http.Request, _ []byte) (int, interface{}) {
		if r.URL.Query().Get("verdict") != "fail" {
			t.Errorf("verdict filter %q", r.URL.Query().Get("verdict"))
		}
		failed := map[string]interface{}{"ordinal": 1, "key": "q1", "status": "graded", "verdict": "fail", "score": 0,
			"grades": []interface{}{map[string]interface{}{"grader": "exact", "passed": false, "reason": "expected Paris, got Nice"}}}
		if r.URL.Query().Get("after_ordinal") == "0" {
			return 200, map[string]interface{}{"cases": []interface{}{failed}, "next_after_ordinal": 1}
		}
		failed["ordinal"], failed["key"] = 5, "q5"
		failed["status_reason"] = "run_failed"
		return 200, map[string]interface{}{"cases": []interface{}{failed}, "next_after_ordinal": nil}
	})
	h := harness(srv, false, "")
	if err := evalShow(h.io, "bot", "e1", "fail"); err != nil {
		t.Fatal(err)
	}
	out := h.stdout.String()
	for _, want := range []string{"q1", "exact: expected Paris", "q5", "run_failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if got := exitOf(evalShow(h.io, "bot", "e1", "some")); got != exitInputRefused {
		t.Errorf("--cases some exits %d", got)
	}
}

func TestEvalCompareAndCancel(t *testing.T) {
	f, srv := newFakeCP(t)
	f.reply("GET "+A+"/eval-comparisons", 409, apiError("EVAL_COMPARE_INCOMPATIBLE", "The base eval is running", nil))
	f.reply("POST "+A+"/evals/e1/cancel", 202, evalBody("e1", "running", 2, 7, map[string]interface{}{"cancel_requested": true}))
	f.reply("POST "+A+"/evals/e2/cancel", 409, apiError("EVAL_NOT_CANCELLABLE", "already completed", nil))
	h := harness(srv, false, "")
	if got := exitOf(evalCompare(h.io, "bot", "a", "b")); got != exitRequestFailed || !strings.Contains(h.stderr.String(), "EVAL_COMPARE_INCOMPATIBLE") {
		t.Errorf("compare exit %d, stderr %q", got, h.stderr)
	}
	if err := evalCancel(h.io, "bot", "e1"); err != nil || !strings.Contains(h.stdout.String(), "Cancelling eval e1") {
		t.Errorf("cancel: %v %q", err, h.stdout)
	}
	if got := exitOf(evalCancel(h.io, "bot", "e2")); got != exitRequestFailed {
		t.Errorf("cancel of a finished eval exits %d", got)
	}
}

func TestExitThresholdBreachedIsFour(t *testing.T) {
	if exitThresholdBreached != 4 {
		t.Fatalf("exitThresholdBreached = %d; CI pipelines gate on 4", exitThresholdBreached)
	}
	wrapped := &reported{&thresholdBreached{msg: "x"}}
	if ExitCode(wrapped) != 4 || ExitCode(&reported{refuse("x")}) != 2 || ExitCode(&reported{fmt.Errorf("x")}) != 1 {
		t.Errorf("ExitCode does not see through reported")
	}
}
