package cmd

// `afy datasets`: the test cases `afy eval` runs. A NOUN GROUP beside secrets
// and workspaces (see the rule in cmd/root.go): a dataset belongs to an agent,
// and every subcommand takes the agent first.
//
// A dataset has immutable, numbered versions. `push` creates the dataset when
// it is missing (and says so), then a new version from a JSONL file of cases --
// one {"key"?, "input", "expected"?, "metadata"?} object per line -- and a
// graders file (YAML or JSON). Without --graders the new version keeps the
// previous version's graders.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// A line of cases.jsonl may be a whole run payload (256 KiB) plus its
// expected value; the scanner's default 64 KiB line would refuse it.
const casesLineMaxBytes = 8 << 20

var datasetsCmd = &cobra.Command{
	Use:   "datasets",
	Short: "Manage an agent's eval datasets",
	Long: `Datasets of test cases for 'afy eval'. Each push creates a new, immutable version.

  afy datasets push my-agent qa --cases cases.jsonl --graders graders.yaml
  afy datasets show my-agent qa --version 2 --cases`,
}

var datasetsListCmd = &cobra.Command{
	Use:   "list <agent>",
	Short: "List an agent's datasets",
	Args:  refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		return datasetsList(newEvalIO(), args[0])
	},
}

var datasetsPushCmd = &cobra.Command{
	Use:   "push <agent> <name>",
	Short: "Push a new version of a dataset (creating the dataset if needed)",
	Long: `Push a new version of a dataset from a JSONL file of cases, one JSON object per
line: {"key": "q1", "input": {...}, "expected": ..., "metadata": {...}}.
"input" is required and is sent verbatim as the run's payload.

--graders names a YAML or JSON file with the list of graders. Without it the new
version keeps the previous version's graders; the first version needs them.`,
	Args: refuseArgs(cobra.ExactArgs(2)),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		return datasetsPush(newEvalIO(), args[0], args[1], datasetsPushOpts)
	},
}

var datasetsShowCmd = &cobra.Command{
	Use:   "show <agent> <name>",
	Short: "Show a dataset's versions, or one version's graders and cases",
	Args:  refuseArgs(cobra.ExactArgs(2)),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		return datasetsShow(newEvalIO(), args[0], args[1], datasetsShowVersion, datasetsShowCases)
	},
}

var datasetsDeleteCmd = &cobra.Command{
	Use:   "delete <agent> <name>",
	Short: "Delete a dataset with its versions, evals and results",
	Long: `Delete a dataset with every version, eval and result. The runs its evals fired
stay in the agent's deployments. Refused while an eval of it is still running.`,
	Args: refuseArgs(cobra.ExactArgs(2)),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkAuth(); err != nil {
			return err
		}
		return datasetsDelete(newEvalIO(), args[0], args[1], datasetsDeleteYes)
	},
}

type datasetsPushFlags struct {
	cases       string
	graders     string
	description string
}

var (
	datasetsPushOpts    datasetsPushFlags
	datasetsShowVersion int
	datasetsShowCases   bool
	datasetsDeleteYes   bool
)

func init() {
	datasetsPushCmd.Flags().StringVar(&datasetsPushOpts.cases, "cases", "", "JSONL file of cases, one per line (required)")
	datasetsPushCmd.Flags().StringVar(&datasetsPushOpts.graders, "graders", "", "YAML or JSON file with the graders (default: the previous version's)")
	datasetsPushCmd.Flags().StringVar(&datasetsPushOpts.description, "description", "", "Description, when this push creates the dataset")

	datasetsShowCmd.Flags().IntVar(&datasetsShowVersion, "version", 0, "Show this version's graders (default: the dataset and its versions)")
	datasetsShowCmd.Flags().BoolVar(&datasetsShowCases, "cases", false, "Also list the version's cases")

	datasetsDeleteCmd.Flags().BoolVar(&datasetsDeleteYes, "yes", false, "Delete without asking")

	for _, c := range []*cobra.Command{datasetsListCmd, datasetsPushCmd, datasetsShowCmd, datasetsDeleteCmd} {
		c.SilenceErrors = true
		c.SetFlagErrorFunc(refuseFlag)
	}
	datasetsCmd.AddCommand(datasetsListCmd)
	datasetsCmd.AddCommand(datasetsPushCmd)
	datasetsCmd.AddCommand(datasetsShowCmd)
	datasetsCmd.AddCommand(datasetsDeleteCmd)
}

func datasetsList(e *evalIO, agent string) error {
	datasets, err := e.client.ListEvalDatasets(agent)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		return e.printJSON(datasets)
	}
	if len(datasets) == 0 {
		fmt.Fprintf(e.stdout, "No datasets on '%s'. Push one with 'afy datasets push %s <name> --cases cases.jsonl --graders graders.yaml'.\n", agent, agent)
		return nil
	}
	rows := make([][]string, 0, len(datasets))
	for _, d := range datasets {
		latest, cases := "-", "-"
		if d.LatestVersion != nil {
			latest = fmt.Sprintf("v%d", *d.LatestVersion)
		}
		if d.CaseCount != nil {
			cases = fmt.Sprint(*d.CaseCount)
		}
		rows = append(rows, []string{d.Name, latest, cases, formatUTCTime(&d.UpdatedAt)})
	}
	output.RenderTable(e.stdout, []string{"Dataset", "Latest", "Cases", "Updated"}, rows)
	return nil
}

// readCases parses a JSONL file: one JSON object per non-blank line. A bad
// line is refused with its number, before anything is sent.
func readCases(path string) ([]interface{}, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, refuse("cannot read --cases: %v", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), casesLineMaxBytes)
	var cases []interface{}
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var value interface{}
		if err := json.Unmarshal([]byte(text), &value); err != nil {
			return nil, refuse("%s:%d: not valid JSON: %v", path, line, err)
		}
		if _, ok := value.(map[string]interface{}); !ok {
			return nil, refuse("%s:%d: each line must be a JSON object with an \"input\"", path, line)
		}
		cases = append(cases, value)
	}
	if err := scanner.Err(); err != nil {
		return nil, refuse("%s: %v", path, err)
	}
	if len(cases) == 0 {
		return nil, refuse("%s holds no cases", path)
	}
	return cases, nil
}

// readGraders parses a YAML or JSON list of grader specs.
func readGraders(path string) ([]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, refuse("cannot read --graders: %v", err)
	}
	var value interface{}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		err = yaml.Unmarshal(data, &value)
	case ".json":
		err = json.Unmarshal(data, &value)
	default:
		return nil, refuse("--graders must be a .yaml, .yml or .json file, got %s", path)
	}
	if err != nil {
		return nil, refuse("%s: %v", path, err)
	}
	list, ok := value.([]interface{})
	if !ok || len(list) == 0 {
		return nil, refuse("%s must hold a non-empty list of graders", path)
	}
	return list, nil
}

// pushResult is `afy datasets push -o json`'s object.
type pushResult struct {
	DatasetCreated bool                    `json:"dataset_created"`
	Version        *api.EvalDatasetVersion `json:"version"`
}

func datasetsPush(e *evalIO, agent, name string, f datasetsPushFlags) error {
	if f.cases == "" {
		return e.fail(refuse("--cases is required"))
	}
	cases, err := readCases(f.cases)
	if err != nil {
		return e.fail(err)
	}
	body := &api.EvalDatasetVersionCreate{Cases: cases}
	if f.graders != "" {
		if body.Graders, err = readGraders(f.graders); err != nil {
			return e.fail(err)
		}
	}

	created := false
	if _, err := e.client.GetEvalDataset(agent, name); err != nil {
		var apiErr *api.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != "EVAL_DATASET_NOT_FOUND" {
			return e.fail(err)
		}
		if _, err := e.client.CreateEvalDataset(agent, name, f.description); err != nil {
			return e.fail(err)
		}
		created = true
		e.note("Created dataset '%s' on '%s'.", name, agent)
	}

	version, err := e.client.PushEvalDatasetVersion(agent, name, body)
	if err != nil {
		return e.fail(err)
	}
	if e.json {
		return e.printJSON(pushResult{DatasetCreated: created, Version: version})
	}
	fmt.Fprintf(e.stdout, "Pushed %s@v%d: %d cases, %d graders.\n", name, version.Version, version.CaseCount, len(version.Graders))
	if f.graders == "" && version.Version > 1 {
		fmt.Fprintln(e.stdout, "The graders are the previous version's.")
	}
	fmt.Fprintf(e.stdout, "Run it: afy eval run %s --dataset %s\n", agent, name)
	return nil
}

func datasetsShow(e *evalIO, agent, name string, version int, withCases bool) error {
	if version == 0 {
		dataset, err := e.client.GetEvalDataset(agent, name)
		if err != nil {
			return e.fail(err)
		}
		if !withCases {
			return printDataset(e, agent, dataset)
		}
		if dataset.LatestVersion == nil {
			return e.fail(refuse("dataset %q has no version yet", name))
		}
		version = *dataset.LatestVersion
	}

	first, err := e.client.GetEvalDatasetVersion(agent, name, version, 100, 0)
	if err != nil {
		return e.fail(err)
	}
	cases := first.Cases
	for next := first.NextAfterOrdinal; withCases && next != nil; {
		page, err := e.client.GetEvalDatasetVersion(agent, name, version, 100, *next)
		if err != nil {
			return e.fail(err)
		}
		cases = append(cases, page.Cases...)
		next = page.NextAfterOrdinal
	}
	first.NextAfterOrdinal = nil
	first.Cases = nil
	if withCases {
		first.Cases = cases
	}
	if e.json {
		return e.printJSON(first)
	}
	fmt.Fprintf(e.stdout, "%s@v%d: %d cases\n\nGraders:\n", name, first.Version, first.CaseCount)
	for _, g := range first.Graders {
		fmt.Fprintf(e.stdout, "  %v (%v)\n", g["name"], g["type"])
	}
	if withCases {
		rows := make([][]string, 0, len(first.Cases))
		for _, c := range first.Cases {
			input, _ := json.Marshal(c.Input)
			expected, _ := json.Marshal(c.Expected)
			rows = append(rows, []string{fmt.Sprint(c.Ordinal), c.Key, truncate(string(input), 60), truncate(string(expected), 40)})
		}
		fmt.Fprintln(e.stdout)
		output.RenderTable(e.stdout, []string{"#", "Key", "Input", "Expected"}, rows)
	}
	return nil
}

func printDataset(e *evalIO, agent string, dataset *api.EvalDataset) error {
	if e.json {
		return e.printJSON(dataset)
	}
	fmt.Fprintf(e.stdout, "Dataset %s on %s\n", dataset.Name, agent)
	if dataset.Description != nil {
		fmt.Fprintf(e.stdout, "  %s\n", *dataset.Description)
	}
	rows := make([][]string, 0, len(dataset.Versions))
	for _, v := range dataset.Versions {
		rows = append(rows, []string{
			fmt.Sprintf("v%d", v.Version), fmt.Sprint(v.CaseCount),
			truncate(v.GradersDigest, 12), truncate(v.CasesDigest, 12), formatUTCTime(&v.CreatedAt),
		})
	}
	output.RenderTable(e.stdout, []string{"Version", "Cases", "Graders digest", "Cases digest", "Created"}, rows)
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func datasetsDelete(e *evalIO, agent, name string, yes bool) error {
	if !yes {
		fmt.Fprintf(e.stderr, "This deletes dataset '%s' with every version, eval and result.\nType the dataset name to confirm: ", name)
		var confirm string
		_, _ = fmt.Fscanln(e.stdin, &confirm)
		if confirm != name {
			e.note("Deletion cancelled.")
			return nil
		}
	}
	if err := e.client.DeleteEvalDataset(agent, name); err != nil {
		return e.fail(err)
	}
	if e.json {
		return e.printJSON(map[string]string{"deleted": name})
	}
	fmt.Fprintf(e.stdout, "Deleted dataset '%s'.\n", name)
	return nil
}
