package vectors

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Every body `afy collections|index|points` builds for the vectors API, driven
// through the validator vectordb runs on each request (backend/utils/
// filterValidation.js).
//
// Part 1 records the bodies the real client methods send, through the sender
// seam, and holds them to testdata/request-bodies.json: a method that gains a
// member changes the file, so the change is seen. Regenerate with
// AFY_WRITE_BODIES=1 go test ./internal/vectors -run TestRequestBodies.
//
// Part 2 runs the file through the validator itself (node, vectordb's own
// code, not a copy of its rules) when a vectordb checkout that has it is
// beside this one (AFY_VECTORDB_DIR names another). Without one it is SKIPPED
// with the reason, and the same file is driven through the validator by
// vectordb's own unit lane (tests/fixtures/filters/cli-request-bodies.json).

type recordedRequest struct {
	Method string          `json:"method"`
	Route  string          `json:"route"`
	Body   json.RawMessage `json:"body"`
}

// workspacePrefix is what the edge rewrites away before vectordb sees a path:
// /workspaces/{ws}/collections/{name} becomes /collections/{name}.
var workspacePrefix = regexp.MustCompile(`^workspaces/[^/]+/`)

const testEndpoint = "https://vectors.test"

func recordBodies(t *testing.T) []recordedRequest {
	t.Helper()
	var got []recordedRequest
	c := New(testEndpoint, "afy_test_key", "default")
	c.send = func(method, target string, body []byte, _ http.Header, _ time.Duration) (int, []byte, error) {
		route := "/" + workspacePrefix.ReplaceAllString(target[len(testEndpoint+"/api/v1/"):], "")
		var raw json.RawMessage
		if body != nil {
			raw = json.RawMessage(append([]byte(nil), body...))
		}
		got = append(got, recordedRequest{Method: method, Route: route, Body: raw})
		// An answer each method can read: no points, no next page, regions placed.
		if strings.HasSuffix(route, "/points/retrieve") {
			return 200, []byte(`{"result":[]}`), nil
		}
		return 200, []byte(`{"result":{"count":0,"points":[],"next_page_offset":null,"status":"completed"},"status":"ok","regions":["us-east-1"]}`), nil
	}
	c.sleep = func(time.Duration) {}

	filters := []json.RawMessage{
		nil,
		json.RawMessage(`{"must":[{"key":"lang","match":{"value":"en"}}]}`),
		json.RawMessage(`{"must":[{"key":"year","range":{"gte":2020,"lt":2025}}],"should":[{"key":"tags","match":{"any":["a","b"]}},{"is_empty":{"key":"__aetherfy_agent_id"}}],"must_not":[{"has_id":[1,2,3]},{"key":"loc","geo_radius":{"center":{"lat":52.5,"lon":13.4},"radius":1000}}]}`),
		json.RawMessage(`{"must":[{"nested":{"key":"items","filter":{"must":[{"key":"n","match":{"value":1}}]}}}],"min_should":{"conditions":[{"key":"a","match":{"value":1}},{"key":"b","match":{"value":2}}],"min_count":1}}`),
	}
	point := json.RawMessage(`{"id":1,"vector":[0.1,0.2],"payload":{"lang":"en"}}`)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range filters {
		_, err := c.CountPoints("articles", f)
		must(err)
		_, err = c.Search("articles", []float64{0.1, 0.2}, 5, f)
		must(err)
		_, err = c.Scroll("articles", 10, nil, f, false)
		must(err)
		_, err = c.Scroll("articles", 10, float64(7), f, true)
		must(err)
		if f != nil {
			must(c.DeletePoints("articles", nil, f))
		}
	}
	must(c.DeletePoints("articles", []json.RawMessage{json.RawMessage("1"), json.RawMessage(`"b3d1"`)}, nil))
	_, err := c.GetPoints("articles", []interface{}{1, "b3d1"})
	must(err)
	_, err = c.ExistingPointIDs("articles", []interface{}{1, "b3d1"})
	must(err)
	_, err = c.Upsert("articles", []json.RawMessage{point})
	must(err)
	must(c.CreateFieldIndex("articles", "lang", "keyword", 0))
	must(c.CreateFieldIndex("articles", "body", map[string]interface{}{"type": "text", "tokenizer": "word"}, 0))
	_, err = c.AnalyzeSchema("articles", 500)
	must(err)
	_, err = c.CreateCollection(CreateCollectionRequest{Name: "articles", Vectors: VectorParams{Size: 2, Distance: "Cosine"}, Regions: []string{"us-east-1"}})
	must(err)
	name, desc := "renamed", "about articles"
	_, err = c.UpdateCollection("articles", UpdateCollectionRequest{Name: &name, Description: &desc})
	must(err)
	return got
}

func TestRequestBodiesAreWhatTheFileSays(t *testing.T) {
	got, err := json.MarshalIndent(recordBodies(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	file := filepath.Join("testdata", "request-bodies.json")
	if os.Getenv("AFY_WRITE_BODIES") == "1" {
		if err := os.WriteFile(file, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatalf("the requests the client builds changed; check them against the vectors API's validation, then regenerate %s with AFY_WRITE_BODIES=1", file)
	}
}

// validatorScript loads vectordb's validator and prints which requests it
// refused, with the code and message.
const validatorScript = `
const { validateFilters } = require(process.argv[1]);
const rows = JSON.parse(require('fs').readFileSync(process.argv[2], 'utf8'));
const refused = [];
rows.forEach((r, i) => {
  const bad = validateFilters(r.method, r.route, r.body === null ? undefined : r.body);
  if (bad) refused.push({ i, code: bad.error.code, message: bad.error.message });
});
console.log(JSON.stringify({ checked: rows.length, refused }));
`

type refusal struct {
	I       int    `json:"i"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func vectordbValidator(t *testing.T) string {
	t.Helper()
	for _, dir := range []string{os.Getenv("AFY_VECTORDB_DIR"), filepath.Join("..", "..", "..", "vectordb")} {
		if dir == "" {
			continue
		}
		p, _ := filepath.Abs(filepath.Join(dir, "backend", "utils", "filterValidation.js"))
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("no vectordb checkout with backend/utils/filterValidation.js beside this one (set AFY_VECTORDB_DIR); vectordb's own unit lane drives the same file through the validator")
	return ""
}

func runValidator(t *testing.T, validator string, rows []recordedRequest) (int, []refusal) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	data, _ := json.Marshal(rows)
	in := filepath.Join(t.TempDir(), "rows.json")
	if err := os.WriteFile(in, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, "-e", validatorScript, validator, in).CombinedOutput()
	if err != nil {
		t.Fatalf("the validator did not run: %v\n%s", err, out)
	}
	var res struct {
		Checked int       `json:"checked"`
		Refused []refusal `json:"refused"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("the validator printed %q: %v", out, err)
	}
	return res.Checked, res.Refused
}

func TestEveryRequestTheClientBuildsPassesVectordbsValidation(t *testing.T) {
	validator := vectordbValidator(t)
	rows := recordBodies(t)
	checked, refused := runValidator(t, validator, rows)
	if checked != len(rows) {
		t.Fatalf("the validator checked %d of %d requests", checked, len(rows))
	}
	for _, r := range refused {
		t.Errorf("%s %s: refused with %s: %s\n  body: %s", rows[r.I].Method, rows[r.I].Route, r.Code, r.Message, rows[r.I].Body)
	}
}

// The run above proves nothing if the validator accepts everything: these
// bodies, a misspelt filter key and a misspelt member and a delete naming both selectors
// on three routes (an upsert's extra members are refused by vectordb's streaming
// parser, not by this validation; the CLI sends `points` only), must be refused with the codes the CLI relays.
func TestTheValidatorRunHereRefusesWhatItShould(t *testing.T) {
	validator := vectordbValidator(t)
	rows := []recordedRequest{
		{Method: "POST", Route: "/collections/articles/points/scroll", Body: json.RawMessage(`{"limit":10,"filter":{"must":[{"key":"y","range":{"gte":1}}],"shuold":[]}}`)},
		{Method: "POST", Route: "/collections/articles/points/query", Body: json.RawMessage(`{"query":[0.1],"limitt":5}`)},
		{Method: "POST", Route: "/collections/articles/points/delete", Body: json.RawMessage(`{"points":[1],"filter":{"must":[]}}`)},
	}
	_, refused := runValidator(t, validator, rows)
	want := []string{"INVALID_FILTER", "INVALID_REQUEST_FIELD", "INVALID_REQUEST_FIELD"}
	if len(refused) != len(rows) {
		t.Fatalf("refused %d of %d: %+v", len(refused), len(rows), refused)
	}
	for i, r := range refused {
		if r.Code != want[i] {
			t.Errorf("row %d: %s, want %s (%s)", i, r.Code, want[i], r.Message)
		}
	}
}
