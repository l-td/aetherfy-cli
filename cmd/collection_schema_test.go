package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/vectors"
)

// afy collections schema get/set/delete/analyze, through the real root
// command, against a fake vectors API that keeps one schema the way vectordb's
// routes/schema.js does (sheet 1.5): a PUT with a stale If-Match is refused 412
// and changes nothing; a PUT without If-Match overwrites.

const schemaPath = "/api/v1/workspaces/research/collections/articles/schema"

// schemaStore is the fake's one schema. etag "" means none is defined.
type schemaStore struct {
	mu     sync.Mutex
	etag   string
	schema string
	puts   int
}

func (st *schemaStore) handle(r vecRequest) (int, string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	switch {
	case r.Method == "GET" && r.Path == schemaPath:
		if st.etag == "" {
			return 404, `{"error":{"code":"SCHEMA_NOT_DEFINED","message":"No schema defined for collection 'articles'"}}`
		}
		return 200, `{"schema":` + st.schema + `,"enforcement_mode":"strict","description":"News","etag":"` + st.etag + `","processing_time_ms":1}`
	case r.Method == "PUT" && r.Path == schemaPath:
		st.puts++
		if r.IfMatch != "" && st.etag != "" && strings.Trim(r.IfMatch, `"`) != st.etag {
			return 412, `{"error":{"code":"SCHEMA_VERSION_MISMATCH","message":"Schema has been modified since you last fetched it",` +
				`"current_etag":"` + st.etag + `","provided_etag":"` + strings.Trim(r.IfMatch, `"`) + `"}}`
		}
		st.etag = "v" + strconv.Itoa(st.puts+2)
		st.schema = `{"fields":{"written":{"type":"string"}}}`
		return 200, `{"success":true,"etag":"` + st.etag + `","processing_time_ms":1}`
	case r.Method == "DELETE" && r.Path == schemaPath:
		st.etag = ""
		return 200, `{"success":true}`
	case r.Method == "POST" && r.Path == schemaPath+"/analyze":
		return 200, `{"collection":"articles","sample_size":4,"total_points":10,"fields":{` +
			`"title":{"presence":1,"types":{"string":1},"element_types":{},"warnings":[]},` +
			`"tags":{"presence":0.25,"types":{"array":1},"element_types":{"string":1},"warnings":["SPARSE_FIELD"]}},` +
			`"suggested_schema":{"fields":{"title":{"type":"string","required":true}}},"processing_time_ms":3}`
	}
	return 599, `{"error":{"code":"stand-in-unrouted","message":"the fake has no answer"}}`
}

// schemaFake starts the fake with a schema at etag (none when ""), and points
// the vector commands at it.
func schemaFake(t *testing.T, etag string) (*vecServer, *schemaStore) {
	t.Helper()
	st := &schemaStore{etag: etag, schema: `{"fields":{"title":{"type":"string"}}}`}
	s := newVecServer(t, st.handle)
	t.Setenv(vectors.EnvVectorsURL, s.srv.URL)
	t.Setenv(vectors.EnvAPIRegion, "")
	return s, st
}

func schemaEnv(s *vecServer) []string {
	return []string{vectors.EnvVectorsURL + "=" + s.srv.URL, vectors.EnvAPIRegion + "="}
}

func writeSchemaFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func methods(reqs []vecRequest) string {
	var m []string
	for _, r := range reqs {
		m = append(m, r.Method)
	}
	return strings.Join(m, ",")
}

func TestSchemaGetShowsTheSchemaAndItsVersion(t *testing.T) {
	s, _ := schemaFake(t, "v2")
	stdout, stderr, err := runAfy(t, nil, "collections", "schema", "get", "articles", "--workspace", "research")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	for _, want := range []string{"workspace 'research'", "Version:     v2", "Enforcement: strict", "Description: News", `"title"`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if req := sentOne(t, s.got()); req.Method != "GET" || req.Path != schemaPath {
		t.Errorf("sent %s %s", req.Method, req.Path)
	}

	stdout, _, err = runAfy(t, nil, "collections", "schema", "get", "articles", "--workspace", "research", "--json")
	if err != nil {
		t.Fatal(err)
	}
	out := decode(t, stdout)
	if out["etag"] != "v2" || out["enforcement_mode"] != "strict" || out["collection"] != "articles" || out["workspace"] != "research" {
		t.Errorf("--json = %v", out)
	}
}

// get --save writes the file set reads back, version included: the read ->
// edit -> set round trip.
func TestSchemaGetSaveThenSetSendsTheSavedVersion(t *testing.T) {
	s, st := schemaFake(t, "v2")
	file := filepath.Join(t.TempDir(), "schema.json")
	stdout, stderr, err := runAfy(t, nil, "collections", "schema", "get", "articles", "--workspace", "research", "--save", file)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "afy collections schema set articles --file "+file) {
		t.Errorf("stdout does not say how to set it back:\n%s", stdout)
	}
	saved := decode(t, readFile(t, file))
	if saved["etag"] != "v2" || saved["enforcement_mode"] != "strict" || saved["description"] != "News" || saved["schema"] == nil {
		t.Fatalf("saved file = %v", saved)
	}

	stdout, stderr, err = runAfy(t, nil, "collections", "schema", "set", "articles", "--workspace", "research", "--file", file)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	reqs := s.got()
	if methods(reqs) != "GET,PUT" {
		t.Fatalf("requests = %s, want the get's GET then one PUT (no read before a versioned set)", methods(reqs))
	}
	put := reqs[1]
	if put.IfMatch != `"v2"` {
		t.Errorf("If-Match = %q, want the saved version \"v2\"", put.IfMatch)
	}
	if put.Body["enforcement_mode"] != "strict" || put.Body["description"] != "News" || put.Body["schema"] == nil {
		t.Errorf("PUT body = %v, want the file's schema, mode and description", put.Body)
	}
	if _, sent := put.Body["etag"]; sent {
		t.Errorf("the etag went in the body: %v", put.Body)
	}
	if !strings.Contains(stdout, "(version v3)") || st.etag != "v3" {
		t.Errorf("stdout = %q, store at %s", stdout, st.etag)
	}
}

// A stale version: the plain sentence, exit 1, one PUT, and the schema as it
// was. CONTROL: with If-Match not sent, the fake overwrites, so this goes red.
func TestSchemaSetWithAStaleVersionIsRefusedAndWritesNothing(t *testing.T) {
	s, st := schemaFake(t, "v2")
	file := writeSchemaFile(t, `{"schema":{"fields":{}},"enforcement_mode":"warn","etag":"v1"}`)
	stdout, stderr, code := runAfyProcess(t, nil, schemaEnv(s), "collections", "schema", "set", "articles", "--workspace", "research", "--file", file)
	if code != exitRequestFailed {
		t.Errorf("exit %d, want %d; stderr %q", code, exitRequestFailed, stderr)
	}
	if st.etag != "v2" || !strings.Contains(st.schema, "title") {
		t.Errorf("a stale set overwrote the schema: now at %s, %s", st.etag, st.schema)
	}
	if methods(s.got()) != "PUT" {
		t.Errorf("requests = %s, want one PUT, never retried without If-Match", methods(s.got()))
	}
	sentence := "the schema of collection 'articles' changed since you read it (your file has version v1), so nothing was written"
	if n := strings.Count(stderr, sentence); n != 1 {
		t.Errorf("want the sentence once, got %d:\n%s", n, stderr)
	}
	if !strings.Contains(stderr, "afy collections schema get articles --save <file>") || strings.Contains(stderr, "modified since you last fetched") {
		t.Errorf("stderr = %q, want the plain sentence naming the fix, not the server's", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q", stdout)
	}

	_, stderr, code = runAfyProcess(t, nil, schemaEnv(s), "collections", "schema", "set", "articles", "--workspace", "research", "--file", file, "--json")
	e := decode(t, stderr)["error"].(map[string]interface{})
	if code != exitRequestFailed || e["status"] != 412.0 || e["code"] != "SCHEMA_VERSION_MISMATCH" || !strings.Contains(e["message"].(string), "changed since you read it") {
		t.Errorf("--json: exit %d, error %v", code, e)
	}
}

// A file with no version and a schema already there: refused, nothing sent
// past the read. CONTROL: with the refusal removed, the PUT overwrites, red.
func TestSchemaSetWithoutAVersionOverAnExistingSchemaIsRefused(t *testing.T) {
	s, st := schemaFake(t, "v2")
	file := writeSchemaFile(t, `{"schema":{"fields":{}}}`)
	_, stderr, code := runAfyProcess(t, nil, schemaEnv(s), "collections", "schema", "set", "articles", "--workspace", "research", "--file", file)
	if code != exitRequestFailed {
		t.Errorf("exit %d, want %d; stderr %q", code, exitRequestFailed, stderr)
	}
	if methods(s.got()) != "GET" || st.puts != 0 || st.etag != "v2" {
		t.Errorf("requests = %s, puts %d, store at %s: want the GET only and the schema untouched", methods(s.got()), st.puts, st.etag)
	}
	sentence := "collection 'articles' already has a schema (version v2)"
	if strings.Count(stderr, sentence) != 1 || !strings.Contains(stderr, "run `afy collections schema get articles --save <file>`, edit it, and set it from that file") {
		t.Errorf("stderr = %q, want the refusal once, naming the fix", stderr)
	}
}

// No version and no schema yet: the first schema, set without If-Match.
func TestSchemaSetWithoutAVersionIsTheFirstSchema(t *testing.T) {
	s, st := schemaFake(t, "")
	file := writeSchemaFile(t, `{"schema":{"fields":{"title":{"type":"string"}}},"description":"first"}`)
	stdout, stderr, err := runAfy(t, nil, "collections", "schema", "set", "articles", "--workspace", "research", "--file", file, "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	reqs := s.got()
	if methods(reqs) != "GET,PUT" || reqs[1].IfMatch != "" {
		t.Fatalf("requests = %s, If-Match %q: want GET (none) then PUT without If-Match", methods(reqs), reqs[len(reqs)-1].IfMatch)
	}
	if _, sent := reqs[1].Body["enforcement_mode"]; sent {
		t.Errorf("an absent mode was sent (the server's default is off): %v", reqs[1].Body)
	}
	if out := decode(t, stdout); out["etag"] != st.etag || out["collection"] != "articles" {
		t.Errorf("--json = %v", out)
	}
}

// Input refused before any request: exit 2, nothing sent.
func TestSchemaSetRefusesABadFileBeforeAnyRequest(t *testing.T) {
	s, _ := schemaFake(t, "v2")
	for name, args := range map[string][]string{
		"no --file":     {},
		"missing file":  {"--file", filepath.Join(t.TempDir(), "nope.json")},
		"not JSON":      {"--file", writeSchemaFile(t, `schema: {}`)},
		"unknown field": {"--file", writeSchemaFile(t, `{"schema":{},"enforcment_mode":"strict"}`)},
		"no schema":     {"--file", writeSchemaFile(t, `{"etag":"v2"}`)},
		"a null schema": {"--file", writeSchemaFile(t, `{"schema":null,"etag":"v2"}`)},
	} {
		t.Run(name, func(t *testing.T) {
			before := len(s.got())
			argv := append([]string{"collections", "schema", "set", "articles", "--workspace", "research"}, args...)
			_, stderr, code := runAfyProcess(t, nil, schemaEnv(s), argv...)
			if code != exitInputRefused || len(s.got()) != before {
				t.Errorf("exit %d, %d requests; want exit %d and nothing sent. stderr %q", code, len(s.got())-before, exitInputRefused, stderr)
			}
		})
	}
}

func TestSchemaDelete(t *testing.T) {
	s, st := schemaFake(t, "v2")
	_, stderr, code := runAfyProcess(t, nil, schemaEnv(s), "collections", "schema", "delete", "articles", "--workspace", "research")
	if code != exitInputRefused || len(s.got()) != 0 || !strings.Contains(stderr, "Pass --yes") {
		t.Errorf("no terminal, no --yes: exit %d, %d requests, stderr %q", code, len(s.got()), stderr)
	}
	stdout, stderr, err := runAfy(t, nil, "collections", "schema", "delete", "articles", "--workspace", "research", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if req := sentOne(t, s.got()); req.Method != "DELETE" || req.Path != schemaPath {
		t.Errorf("sent %s %s", req.Method, req.Path)
	}
	if st.etag != "" || !strings.Contains(stdout, "Schema of collection 'articles' deleted in workspace 'research'") {
		t.Errorf("stdout %q, store at %q", stdout, st.etag)
	}
}

func TestSchemaAnalyze(t *testing.T) {
	s, _ := schemaFake(t, "v2")
	stdout, stderr, err := runAfy(t, nil, "collections", "schema", "analyze", "articles", "--workspace", "research", "--sample-size", "500")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	req := sentOne(t, s.got())
	if req.Method != "POST" || req.Path != schemaPath+"/analyze" || req.Body["sample_size"] != 500.0 {
		t.Errorf("sent %s %s %v", req.Method, req.Path, req.Body)
	}
	for _, want := range []string{
		"Analyzed 4 of 10 points of collection 'articles'",
		"title", "100.0%", "string 100.0%",
		"tags", "25.0%", "array 100.0% (elements: string 100.0%)", "SPARSE_FIELD",
		"Suggested schema:", `"required": true`,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	// No --sample-size: the server's default, so none is sent.
	_, _, _ = runAfy(t, nil, "collections", "schema", "analyze", "articles", "--workspace", "research")
	if last := s.got()[len(s.got())-1]; len(last.Body) != 0 {
		t.Errorf("without --sample-size the body is %v, want {}", last.Body)
	}
}

// Through an alias (sheet 1.6): writes are refused 409, reads 404. Each is the
// server's sentence, printed once.
func TestSchemaThroughAnAliasPrintsTheServersRefusalOnce(t *testing.T) {
	const alias409 = `"articles" is an alias, not a collection: a schema is changed on the collection itself, never through one of its aliases. Address the collection by its own name.`
	const notFound = "Collection 'articles' not found"
	s := newVecServer(t, func(r vecRequest) (int, string) {
		if r.Method == "PUT" || r.Method == "DELETE" {
			return 409, `{"error":{"code":"ADDRESSED_AN_ALIAS","message":` + jsonString(alias409) + `,"alias":"articles"}}`
		}
		return 404, `{"error":{"code":"COLLECTION_NOT_FOUND","message":"` + notFound + `"}}`
	})
	file := writeSchemaFile(t, `{"schema":{"fields":{}},"etag":"v2"}`)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"set", "articles", "--file", file}, alias409 + " (ADDRESSED_AN_ALIAS)"},
		{[]string{"delete", "articles", "--yes"}, alias409 + " (ADDRESSED_AN_ALIAS)"},
		{[]string{"get", "articles"}, notFound + " (COLLECTION_NOT_FOUND)"},
		{[]string{"analyze", "articles"}, notFound + " (COLLECTION_NOT_FOUND)"},
	} {
		t.Run(tc.args[0], func(t *testing.T) {
			argv := append([]string{"collections", "schema"}, append(tc.args, "--workspace", "research")...)
			stdout, stderr, code := runAfyProcess(t, nil, schemaEnv(s), argv...)
			if code != exitRequestFailed || stdout != "" {
				t.Errorf("exit %d, stdout %q", code, stdout)
			}
			if stderr != "Error: "+tc.want+"\n" {
				t.Errorf("stderr = %q, want the server's sentence once", stderr)
			}
		})
	}
}

func jsonString(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// sentOne is the one request a root-driven command sent. (only pins the
// in-process key; through the root command the key is AETHERFY_API_KEY.)
func sentOne(t *testing.T, reqs []vecRequest) vecRequest {
	t.Helper()
	if len(reqs) != 1 {
		t.Fatalf("requests = %+v, want exactly one", reqs)
	}
	if reqs[0].Auth != "Bearer afy_test_0123456789abcdef0123456789abcdef" {
		t.Errorf("sent Authorization %q, want AETHERFY_API_KEY", reqs[0].Auth)
	}
	return reqs[0]
}
