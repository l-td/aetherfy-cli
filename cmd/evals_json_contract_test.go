package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// The evals commands under the CLI's one JSON contract (json_contract_test.go):
// stdout is one JSON document on success and empty on a refusal, whose stderr
// carries the server's code and status. `afy datasets push` reads two files, so
// it has its own test below; the other leaves are rows of the shared table.

func jsonText(t interface{}) string {
	data, err := json.Marshal(t)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func evalsJSONCases() []jsonCase {
	eval := jsonText(evalBody("e1", "completed", 2, 7, map[string]interface{}{"summary": summary(1)}))
	dataset := jsonText(map[string]interface{}{
		"id": "d1", "name": "qa", "latest_version": 3, "case_count": 40,
		"created_at": "2026-09-29T10:00:00Z", "updated_at": "2026-09-29T11:00:00Z",
	})
	comparison := `{"dataset":{"name":"qa","version":2},"base":{"eval_id":"e1","release_version":6},"head":{"eval_id":"e2","release_version":7},"same_code":false,"delta":{},"graders":[],"regressions":[],"fixes":[],"unchanged_pass":10,"unchanged_fail":0,"not_comparable":[]}`
	return []jsonCase{
		{class: "list", args: []string{"eval", "list", "bot"}, ok: []route{{"GET", "/agents/bot/evals", 200, `[` + eval + `]`}}, refuse: []string{"GET /agents/bot/evals"}},
		{class: "list", args: []string{"datasets", "list", "bot"}, ok: []route{{"GET", "/agents/bot/eval-datasets", 200, `[` + dataset + `]`}}, refuse: []string{"GET /agents/bot/eval-datasets"}},
		{class: "show", args: []string{"eval", "show", "bot", "e1"}, ok: []route{{"GET", "/agents/bot/evals/e1", 200, eval}}, refuse: []string{"GET /agents/bot/evals/e1"}},
		{class: "show", args: []string{"eval", "compare", "bot", "e1", "e2"}, ok: []route{{"GET", "/agents/bot/eval-comparisons", 200, comparison}}, refuse: []string{"GET /agents/bot/eval-comparisons"}},
		{class: "show", args: []string{"datasets", "show", "bot", "qa"}, ok: []route{{"GET", "/agents/bot/eval-datasets/qa", 200, `{"id":"d1","name":"qa","latest_version":3,"versions":[]}`}}, refuse: []string{"GET /agents/bot/eval-datasets/qa"}},
		{class: "mutate", args: []string{"eval", "cancel", "bot", "e1"}, ok: []route{{"POST", "/agents/bot/evals/e1/cancel", 202, eval}}, refuse: []string{"POST /agents/bot/evals/e1/cancel"}},
		{class: "mutate", args: []string{"eval", "run", "bot", "--dataset", "qa", "--dataset-version", "2", "--detach"}, ok: []route{{"POST", "/agents/bot/evals", 202, eval}}, refuse: []string{"POST /agents/bot/evals"}},
		{class: "mutate", args: []string{"datasets", "delete", "bot", "qa", "--yes"}, ok: []route{{"DELETE", "/agents/bot/eval-datasets/qa", 204, ``}}, refuse: []string{"DELETE /agents/bot/eval-datasets/qa"}},
	}
}

func TestJSONContractDatasetsPush(t *testing.T) {
	cases := datasetFile(t, "cases.jsonl", casesJSONL)
	graders := datasetFile(t, "graders.yaml", gradersYAML)
	version := jsonText(versionReply(1, 1))
	ok := []route{
		{"GET", "/agents/bot/eval-datasets/qa", 200, `{"id":"d1","name":"qa","latest_version":1,"versions":[]}`},
		{"POST", "/agents/bot/eval-datasets/qa/versions", 201, version},
	}
	c := jsonCase{
		class: "mutate",
		args:  []string{"datasets", "push", "bot", "qa", "--cases", filepath.ToSlash(cases), "--graders", filepath.ToSlash(graders)},
		ok:    ok, refuse: []string{"POST /agents/bot/eval-datasets/qa/versions"},
	}
	stdout, stderr, code := runCase(t, c, true, false)
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	oneJSONDocument(t, strings.Join(c.args, " "), stdout)

	stdout, stderr, code = runCase(t, c, true, true)
	if code != 1 || strings.TrimSpace(stdout) != "" {
		t.Errorf("a refused push: exit %d, stdout %q; want 1 and nothing on stdout", code, stdout)
	}
	if e := jsonErrorOnStderr(t, "datasets push", stderr); e["code"] != refusalCode || e["status"] != 409.0 {
		t.Errorf("the refusal carries %v, want the server's code %q and status 409", e, refusalCode)
	}
}
