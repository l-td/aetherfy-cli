package cmd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func datasetFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const casesJSONL = `{"key": "q1", "input": {"question": "Capital of France?"}, "expected": {"answer": "Paris"}}

{"input": {"question": "Capital of Italy?"}, "expected": {"answer": "Rome"}}
`

const gradersYAML = `- type: exact
  path: /answer
  expected_path: /answer
- type: llm_judge
  format: anthropic
  model: claude
  api_key_secret: ANTHROPIC_API_KEY
  rubric: Is the answer the capital?
`

func versionReply(version int, graders int) map[string]interface{} {
	g := make([]interface{}, graders)
	for i := range g {
		g[i] = map[string]interface{}{"name": "exact", "type": "exact"}
	}
	return map[string]interface{}{
		"dataset": "qa", "version": version, "case_count": 2, "graders": g,
		"graders_digest": strings.Repeat("a", 64), "cases_digest": strings.Repeat("b", 64),
		"created_at": "2026-09-29T10:00:00Z",
	}
}

func TestDatasetsPushCreatesAMissingDatasetAndSaysSo(t *testing.T) {
	f, srv := newFakeCP(t)
	f.reply("GET "+A+"/eval-datasets/qa", 404, apiError("EVAL_DATASET_NOT_FOUND", "no dataset", nil))
	f.reply("POST "+A+"/eval-datasets", 201, map[string]interface{}{"id": "d1", "name": "qa"})
	f.reply("POST "+A+"/eval-datasets/qa/versions", 201, versionReply(1, 2))
	h := harness(srv, false, "")

	err := datasetsPush(h.io, "bot", "qa", datasetsPushFlags{
		cases: datasetFile(t, "cases.jsonl", casesJSONL), graders: datasetFile(t, "graders.yaml", gradersYAML),
		description: "capitals",
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, h.stderr)
	}
	var created map[string]interface{}
	_ = json.Unmarshal(f.bodies["POST "+A+"/eval-datasets"], &created)
	if created["name"] != "qa" || created["description"] != "capitals" {
		t.Errorf("create body %v", created)
	}
	var version struct {
		Cases   []map[string]interface{} `json:"cases"`
		Graders []map[string]interface{} `json:"graders"`
	}
	_ = json.Unmarshal(f.bodies["POST "+A+"/eval-datasets/qa/versions"], &version)
	if len(version.Cases) != 2 || version.Cases[0]["key"] != "q1" {
		t.Errorf("cases sent: %v", version.Cases)
	}
	if len(version.Graders) != 2 || version.Graders[1]["api_key_secret"] != "ANTHROPIC_API_KEY" {
		t.Errorf("graders sent: %v", version.Graders)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "Created dataset 'qa' on 'bot'.") || !strings.Contains(out, "Pushed qa@v1: 2 cases, 2 graders.") {
		t.Errorf("output:\n%s", out)
	}
}

func TestDatasetsPushToAnExistingDatasetKeepsItsGraders(t *testing.T) {
	f, srv := newFakeCP(t)
	f.reply("GET "+A+"/eval-datasets/qa", 200, map[string]interface{}{"id": "d1", "name": "qa", "latest_version": 1})
	f.reply("POST "+A+"/eval-datasets/qa/versions", 201, versionReply(2, 1))
	h := harness(srv, true, "")
	if err := datasetsPush(h.io, "bot", "qa", datasetsPushFlags{cases: datasetFile(t, "c.jsonl", casesJSONL)}); err != nil {
		t.Fatal(err)
	}
	if f.count("POST "+A+"/eval-datasets?") != 0 {
		t.Errorf("an existing dataset was created again")
	}
	if strings.Contains(string(f.bodies["POST "+A+"/eval-datasets/qa/versions"]), "graders") {
		t.Errorf("a push without --graders sent a graders field: %s", f.bodies["POST "+A+"/eval-datasets/qa/versions"])
	}
	var result pushResult
	if err := json.Unmarshal(h.stdout.Bytes(), &result); err != nil || result.DatasetCreated || result.Version.Version != 2 {
		t.Errorf("-o json stdout = %q", h.stdout)
	}
}

func TestDatasetsPushNamesTheBadLineAndSendsNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		cases, graders, want string
	}{
		"bad json":         {"{\"input\": {}}\n{not json}\n", "", "c.jsonl:2: not valid JSON"},
		"not an object":    {"{\"input\": {}}\n\n[1, 2]\n", "", "c.jsonl:3: each line must be a JSON object"},
		"empty":            {"\n\n", "", "holds no cases"},
		"graders not list": {"{\"input\": {}}\n", "type: exact\n", "must hold a non-empty list"},
		"graders ext":      {"{\"input\": {}}\n", "[]", "must be a .yaml, .yml or .json file"},
	} {
		t.Run(name, func(t *testing.T) {
			f, srv := newFakeCP(t)
			h := harness(srv, false, "")
			flags := datasetsPushFlags{cases: datasetFile(t, "c.jsonl", tc.cases)}
			if tc.graders != "" {
				ext := "g.yaml"
				if name == "graders ext" {
					ext = "g.txt"
				}
				flags.graders = datasetFile(t, ext, tc.graders)
			}
			if got := exitOf(datasetsPush(h.io, "bot", "qa", flags)); got != exitInputRefused {
				t.Errorf("exit %d, want 2", got)
			}
			if !strings.Contains(h.stderr.String(), tc.want) {
				t.Errorf("stderr %q lacks %q", h.stderr, tc.want)
			}
			if len(f.requests) != 0 {
				t.Errorf("refused input sent %v", f.requests)
			}
		})
	}
}

func TestDatasetsPushPrintsEveryViolation(t *testing.T) {
	f, srv := newFakeCP(t)
	f.reply("GET "+A+"/eval-datasets/qa", 200, map[string]interface{}{"id": "d1", "name": "qa"})
	f.reply("POST "+A+"/eval-datasets/qa/versions", 422, apiError("EVAL_DATASET_INVALID", "The dataset version has 2 problem(s).",
		map[string]interface{}{"violations": []interface{}{
			map[string]interface{}{"case": "q1", "field": "expected", "message": "grader 'exact' reads expected/answer: no key"},
			map[string]interface{}{"case": 2, "field": "input", "message": "is required"},
		}}))
	for _, jsonOut := range []bool{false, true} {
		h := harness(srv, jsonOut, "")
		err := datasetsPush(h.io, "bot", "qa", datasetsPushFlags{cases: datasetFile(t, "c.jsonl", casesJSONL)})
		if exitOf(err) != exitRequestFailed {
			t.Errorf("exit %d", exitOf(err))
		}
		stderr := h.stderr.String()
		if jsonOut {
			var failure evalErrorJSON
			if json.Unmarshal([]byte(stderr), &failure) != nil || len(failure.Error.Violations) != 2 || failure.Error.Code != "EVAL_DATASET_INVALID" {
				t.Errorf("json stderr %q", stderr)
			}
			continue
		}
		for _, want := range []string{"case q1, field expected: grader 'exact'", "case 2, field input: is required", "EVAL_DATASET_INVALID"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr lacks %q:\n%s", want, stderr)
			}
		}
	}
}

func TestDatasetsPushReportsTheOtherEnvelopes(t *testing.T) {
	for code, status := range map[string]int{
		"EVAL_DATASET_TOO_LARGE": 413, "EVAL_GRADER_INVALID": 422, "EVAL_JUDGE_HOST_NOT_ALLOWED": 422,
		"EVAL_DATASET_EXISTS": 409, "EVALS_NOT_ENABLED": 503,
	} {
		f, srv := newFakeCP(t)
		f.reply("GET "+A+"/eval-datasets/qa", 404, apiError("EVAL_DATASET_NOT_FOUND", "no", nil))
		f.reply("POST "+A+"/eval-datasets", 201, map[string]interface{}{"id": "d1", "name": "qa"})
		f.reply("POST "+A+"/eval-datasets/qa/versions", status, apiError(code, "refused", nil))
		if code == "EVAL_DATASET_EXISTS" {
			f.reply("POST "+A+"/eval-datasets", status, apiError(code, "exists", nil))
		}
		h := harness(srv, false, "")
		err := datasetsPush(h.io, "bot", "qa", datasetsPushFlags{cases: datasetFile(t, "c.jsonl", casesJSONL)})
		if exitOf(err) != exitRequestFailed || !strings.Contains(h.stderr.String(), code) {
			t.Errorf("%s: exit %d, stderr %q", code, exitOf(err), h.stderr)
		}
	}
}

func TestDatasetsDeleteConfirms(t *testing.T) {
	f, srv := newFakeCP(t)
	f.reply("DELETE "+A+"/eval-datasets/qa", 204, nil)

	h := harness(srv, false, "qb\n")
	if err := datasetsDelete(h.io, "bot", "qa", false); err != nil || f.count("DELETE") != 0 {
		t.Errorf("a wrong confirmation deleted: %v %v", err, f.requests)
	}
	h = harness(srv, false, "qa\n")
	if err := datasetsDelete(h.io, "bot", "qa", false); err != nil || f.count("DELETE") != 1 {
		t.Errorf("the right confirmation did not delete: %v %v", err, f.requests)
	}
	h = harness(srv, true, "")
	if err := datasetsDelete(h.io, "bot", "qa", true); err != nil || f.count("DELETE") != 2 {
		t.Errorf("--yes still asked: %v", err)
	}
	if strings.TrimSpace(h.stdout.String()) != "{\n  \"deleted\": \"qa\"\n}" {
		t.Errorf("-o json stdout %q", h.stdout)
	}
	f.reply("DELETE "+A+"/eval-datasets/qa", 409, apiError("EVAL_DATASET_IN_USE", "1 eval running", nil))
	h = harness(srv, false, "")
	if got := exitOf(datasetsDelete(h.io, "bot", "qa", true)); got != exitRequestFailed || !strings.Contains(h.stderr.String(), "EVAL_DATASET_IN_USE") {
		t.Errorf("in use: exit %d, %q", got, h.stderr)
	}
}

func TestDatasetsListAndShow(t *testing.T) {
	f, srv := newFakeCP(t)
	f.reply("GET "+A+"/eval-datasets", 200, []interface{}{map[string]interface{}{
		"id": "d1", "name": "qa", "latest_version": 3, "case_count": 40,
		"created_at": "2026-09-29T10:00:00Z", "updated_at": "2026-09-29T11:00:00Z",
	}})
	f.reply("GET "+A+"/eval-datasets/qa", 200, map[string]interface{}{
		"id": "d1", "name": "qa", "latest_version": 3,
		"versions": []interface{}{map[string]interface{}{"version": 3, "case_count": 40, "graders_digest": strings.Repeat("c", 64), "cases_digest": strings.Repeat("d", 64), "created_at": "2026-09-29T11:00:00Z"}},
	})
	f.on("GET "+A+"/eval-datasets/qa/versions/3", func(r *http.Request, _ []byte) (int, interface{}) {
		page := versionReply(3, 1)
		if r.URL.Query().Get("after_ordinal") == "0" {
			page["cases"] = []interface{}{map[string]interface{}{"ordinal": 1, "key": "q1", "input": map[string]interface{}{"q": 1}}}
			page["next_after_ordinal"] = 1
		} else {
			page["cases"] = []interface{}{map[string]interface{}{"ordinal": 2, "key": "q2", "input": map[string]interface{}{"q": 2}}}
		}
		return 200, page
	})
	h := harness(srv, false, "")
	if err := datasetsList(h.io, "bot"); err != nil || !strings.Contains(h.stdout.String(), "v3") || !strings.Contains(h.stdout.String(), "40") {
		t.Errorf("list: %v\n%s", err, h.stdout)
	}
	h = harness(srv, false, "")
	if err := datasetsShow(h.io, "bot", "qa", 0, false); err != nil || !strings.Contains(h.stdout.String(), "cccccccc") {
		t.Errorf("show: %v\n%s", err, h.stdout)
	}
	h = harness(srv, true, "")
	if err := datasetsShow(h.io, "bot", "qa", 0, true); err != nil {
		t.Fatal(err)
	}
	var version struct {
		Version int `json:"version"`
		Cases   []struct {
			Key string `json:"key"`
		} `json:"cases"`
	}
	if json.Unmarshal(h.stdout.Bytes(), &version) != nil || version.Version != 3 || len(version.Cases) != 2 {
		t.Errorf("show --cases -o json paged %q", h.stdout)
	}
}
