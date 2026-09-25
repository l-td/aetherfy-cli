package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/vectors"
)

// The vector commands, each driven against a fake vectors API: what it sends,
// what --json prints, and the exit code a script sees when the API refuses.
// --json is the contract a coding agent reads, so it is decoded and compared
// field by field rather than grepped.

type vecRequest struct {
	Method string
	Path   string // escaped, as sent
	Auth   string
	Body   map[string]interface{}
}

// vecServer answers each request with handler, and records it. RUNAWAY GUARD:
// past maxRequests it fails the test, so a wait that stopped ending is red,
// not a hang.
type vecServer struct {
	t           *testing.T
	srv         *httptest.Server
	mu          sync.Mutex
	requests    []vecRequest
	maxRequests int
}

func newVecServer(t *testing.T, handler func(r vecRequest) (int, string)) *vecServer {
	t.Helper()
	s := &vecServer{t: t, maxRequests: 50}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		r := vecRequest{Method: req.Method, Path: req.URL.EscapedPath(), Auth: req.Header.Get("Authorization")}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &r.Body); err != nil {
				t.Errorf("request body is not a JSON object: %s", raw)
			}
		}
		s.mu.Lock()
		s.requests = append(s.requests, r)
		n := len(s.requests)
		s.mu.Unlock()
		if n > s.maxRequests {
			t.Errorf("RUNAWAY: %d requests; the command no longer stops", n)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		status, body := handler(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *vecServer) got() []vecRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]vecRequest(nil), s.requests...)
}

// run is a vecRun pointed at the fake server, as if resolution had picked it.
// connects counts resolutions, so a test can assert a refused input resolved
// nothing (region discovery is a request too).
func (s *vecServer) run(workspace string, asJSON bool) (*vecRun, *bytes.Buffer, *bytes.Buffer, *int) {
	var stdout, stderr bytes.Buffer
	connects := 0
	r := &vecRun{json: asJSON, stdout: &stdout, stderr: &stderr}
	r.connect = func() (*vectors.Client, error) {
		connects++
		return vectors.New(s.srv.URL, "afy_test_key", workspace), nil
	}
	return r, &stdout, &stderr, &connects
}

func decode(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("--json printed something that is not one JSON object: %v\n%s", err, raw)
	}
	return out
}

func only(t *testing.T, reqs []vecRequest) vecRequest {
	t.Helper()
	if len(reqs) != 1 {
		t.Fatalf("requests = %+v, want exactly one", reqs)
	}
	if reqs[0].Auth != "Bearer afy_test_key" {
		t.Errorf("sent Authorization %q, want the stored key", reqs[0].Auth)
	}
	return reqs[0]
}

func strPtr(s string) *string { return &s }

const collectionRow = `{"name":"articles","description":null,"config":{"params":{"vectors":{"size":1536,"distance":"Cosine"}}},"status":"green","points_count":120,"created_at":"2026-09-01T10:00:00Z"}`

func TestCollectionsListScopesToTheWorkspaceAndSaysWhichOne(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, `{"collections":[` + collectionRow + `]}`
	})

	r, stdout, _, _ := s.run("research", true)
	if code := collectionsList(r); code != 0 {
		t.Fatalf("exit %d", code)
	}
	req := only(t, s.got())
	if req.Method != "GET" || req.Path != "/api/v1/workspaces/research/collections" {
		t.Errorf("sent %s %s", req.Method, req.Path)
	}
	out := decode(t, stdout.String())
	if out["endpoint"] != s.srv.URL || out["workspace"] != "research" {
		t.Errorf("envelope = endpoint %v, workspace %v", out["endpoint"], out["workspace"])
	}
	cols := out["collections"].([]interface{})
	c := cols[0].(map[string]interface{})
	if c["name"] != "articles" || c["size"] != 1536.0 || c["distance"] != "Cosine" ||
		c["points_count"] != 120.0 || c["status"] != "green" || c["description"] != nil {
		t.Errorf("collection = %v", c)
	}

	// No workspace: the flat route, and the envelope says so with a null
	// rather than leaving the key out.
	s = newVecServer(t, func(vecRequest) (int, string) { return 200, `{"collections":[]}` })
	r, stdout, _, _ = s.run("", true)
	if code := collectionsList(r); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if req := only(t, s.got()); req.Path != "/api/v1/collections" {
		t.Errorf("workspaceless list went to %s", req.Path)
	}
	out = decode(t, stdout.String())
	if ws, present := out["workspace"]; !present || ws != nil {
		t.Errorf("workspace = %v (present %v), want null", ws, present)
	}
	if cols, ok := out["collections"].([]interface{}); !ok || len(cols) != 0 {
		t.Errorf("an empty list must print [], got %v", out["collections"])
	}
}

func TestCollectionsGet(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, `{"result":{"name":"articles","description":"news","config":{"params":{"vectors":{"size":768,"distance":"Dot"}}},` +
			`"points_count":3,"status":"green","regions":["us-east-1","eu-central-1"],"created_at":"c","updated_at":"u"},"schema_version":"v1"}`
	})
	r, stdout, _, _ := s.run("", true)
	if code := collectionsGet(r, "my articles"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if req := only(t, s.got()); req.Method != "GET" || req.Path != "/api/v1/collections/my%20articles" {
		t.Errorf("sent %s %s", req.Method, req.Path)
	}
	c := decode(t, stdout.String())["collection"].(map[string]interface{})
	if c["size"] != 768.0 || c["distance"] != "Dot" || c["description"] != "news" || c["updated_at"] != "u" {
		t.Errorf("collection = %v", c)
	}
	if regions := c["regions"].([]interface{}); len(regions) != 2 || regions[1] != "eu-central-1" {
		t.Errorf("regions = %v", regions)
	}
}

func TestCollectionsCreateSendsTheSDKsBody(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, `{"result":true,"status":"ok","regions":["us-east-1"]}`
	})
	r, stdout, _, _ := s.run("research", true)
	if code := collectionsCreate(r, "articles", 1536, "euclid", []string{"us-east-1"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	req := only(t, s.got())
	if req.Method != "POST" || req.Path != "/api/v1/workspaces/research/collections" {
		t.Errorf("sent %s %s", req.Method, req.Path)
	}
	vec := req.Body["vectors"].(map[string]interface{})
	if req.Body["name"] != "articles" || vec["size"] != 1536.0 || vec["distance"] != "Euclid" {
		t.Errorf("body = %v", req.Body)
	}
	if regions := req.Body["regions"].([]interface{}); len(regions) != 1 || regions[0] != "us-east-1" {
		t.Errorf("regions = %v", req.Body["regions"])
	}
	c := decode(t, stdout.String())["collection"].(map[string]interface{})
	if c["name"] != "articles" || c["distance"] != "Euclid" || len(c) != 4 {
		t.Errorf("collection = %v; want name, size, distance, regions only", c)
	}

	// Without --regions the key is not sent: the server places the collection
	// in the full scope, while an explicit [] is a different request.
	s = newVecServer(t, func(vecRequest) (int, string) { return 200, `{"result":true,"regions":["us-east-1","eu-central-1"]}` })
	r, _, _, _ = s.run("", false)
	if code := collectionsCreate(r, "articles", 8, "cosine", nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, sent := only(t, s.got()).Body["regions"]; sent {
		t.Error("regions was sent although --regions was not given")
	}
}

func TestCollectionsCreateRefusesBadInputBeforeAnyRequest(t *testing.T) {
	for name, tc := range map[string]struct {
		size     int
		distance string
	}{
		"no size":          {0, "cosine"},
		"unknown distance": {8, "hamming"},
		"no distance":      {8, ""},
	} {
		t.Run(name, func(t *testing.T) {
			s := newVecServer(t, func(vecRequest) (int, string) { return 200, `{}` })
			r, _, stderr, connects := s.run("", false)
			if code := collectionsCreate(r, "articles", tc.size, tc.distance, nil); code != exitInputRefused {
				t.Errorf("exit %d, want %d", code, exitInputRefused)
			}
			if len(s.got()) != 0 || *connects != 0 {
				t.Errorf("a refused input still resolved (%d) or sent (%d)", *connects, len(s.got()))
			}
			if !strings.HasPrefix(stderr.String(), "Error: --") {
				t.Errorf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestCollectionsDeleteNeedsAConfirmation(t *testing.T) {
	deleted := func(t *testing.T, s *vecServer) {
		t.Helper()
		if req := only(t, s.got()); req.Method != "DELETE" || req.Path != "/api/v1/collections/articles" {
			t.Errorf("sent %s %s", req.Method, req.Path)
		}
	}
	ok := func(vecRequest) (int, string) { return 200, `{"result":true}` }

	t.Run("--yes deletes", func(t *testing.T) {
		s := newVecServer(t, ok)
		r, stdout, _, _ := s.run("", true)
		if code := collectionsDelete(r, "articles", true, strings.NewReader(""), false); code != 0 {
			t.Fatalf("exit %d", code)
		}
		deleted(t, s)
		out := decode(t, stdout.String())
		if out["collection"] != "articles" || out["deleted"] != true {
			t.Errorf("output = %v", out)
		}
	})
	t.Run("no terminal and no --yes deletes nothing", func(t *testing.T) {
		s := newVecServer(t, ok)
		r, _, stderr, connects := s.run("", false)
		if code := collectionsDelete(r, "articles", false, strings.NewReader("articles\n"), false); code != exitInputRefused {
			t.Errorf("exit %d, want %d", code, exitInputRefused)
		}
		if len(s.got()) != 0 || *connects != 0 {
			t.Error("deleted without a confirmation")
		}
		if !strings.Contains(stderr.String(), "--yes") {
			t.Errorf("the refusal should say how to proceed: %q", stderr.String())
		}
	})
	t.Run("typing the name deletes", func(t *testing.T) {
		s := newVecServer(t, ok)
		r, _, _, _ := s.run("", false)
		if code := collectionsDelete(r, "articles", false, strings.NewReader("articles\n"), true); code != 0 {
			t.Fatalf("exit %d", code)
		}
		deleted(t, s)
	})
	t.Run("typing another name deletes nothing", func(t *testing.T) {
		s := newVecServer(t, ok)
		r, _, _, _ := s.run("", false)
		if code := collectionsDelete(r, "articles", false, strings.NewReader("article\n"), true); code != exitInputRefused {
			t.Errorf("exit %d", code)
		}
		if len(s.got()) != 0 {
			t.Error("deleted on a mismatched name")
		}
	})
}

const (
	indexCompleted    = `{"result":{"operation_id":7,"status":"completed"},"status":"ok"}`
	indexAcknowledged = `{"result":{"operation_id":null,"status":"acknowledged"},"status":"ok"}`
)

func TestIndexCreateWaitsForCompleted(t *testing.T) {
	// The server did not hold the "acknowledged" (it answered at once), so the
	// second create waits the 1 s floor first. That second is real: the fake
	// clock tests in internal/vectors cover the timing; this one covers the
	// command.
	answers := []string{indexAcknowledged, indexCompleted}
	s := newVecServer(t, func(vecRequest) (int, string) {
		a := answers[0]
		if len(answers) > 1 {
			answers = answers[1:]
		}
		return 200, a
	})
	r, stdout, _, _ := s.run("research", true)
	if code := indexCreate(r, "articles", "thread_id", "keyword", nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	reqs := s.got()
	if len(reqs) != 2 {
		t.Fatalf("creates = %d, want 2 (acknowledged, then completed)", len(reqs))
	}
	for _, req := range reqs {
		if req.Method != "PUT" || req.Path != "/api/v1/workspaces/research/collections/articles/index" ||
			req.Body["field_name"] != "thread_id" || req.Body["field_schema"] != "keyword" {
			t.Errorf("sent %s %s %v", req.Method, req.Path, req.Body)
		}
	}
	out := decode(t, stdout.String())
	if out["status"] != "completed" || out["field"] != "thread_id" || out["type"] != "keyword" || out["workspace"] != "research" {
		t.Errorf("output = %v", out)
	}
}

func TestIndexCreateForwardsAParameterisedTypeAsAnObject(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 200, indexCompleted })
	r, _, _, _ := s.run("", false)
	if code := indexCreate(r, "articles", "body", `{"type":"text","lowercase":true}`, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	schema, ok := only(t, s.got()).Body["field_schema"].(map[string]interface{})
	if !ok || schema["type"] != "text" || schema["lowercase"] != true {
		t.Errorf("field_schema = %v, want the object as given", only(t, s.got()).Body["field_schema"])
	}
}

func TestIndexCreateStopsAtItsDeadlineWithTheStillBuildingMessage(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 200, indexAcknowledged })
	r, stdout, stderr, _ := s.run("", false)
	if code := indexCreate(r, "articles", "ts", "integer", strPtr("0.3")); code != exitRequestFailed {
		t.Errorf("exit %d, want %d", code, exitRequestFailed)
	}
	if stdout.Len() != 0 {
		t.Errorf("a failure printed to stdout: %q", stdout.String())
	}
	want := "Error: The payload index on 'ts' in collection 'articles' is still building after the 0.3 s deadline."
	if !strings.HasPrefix(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to start %q", stderr.String(), want)
	}
}

func TestIndexCreateRefusesATimeoutBeforeAnyRequest(t *testing.T) {
	for _, raw := range []string{"0", "-5", "NaN", "Inf", "ten", ""} {
		s := newVecServer(t, func(vecRequest) (int, string) { return 200, indexCompleted })
		r, _, stderr, connects := s.run("", true)
		if code := indexCreate(r, "articles", "ts", "integer", strPtr(raw)); code != exitInputRefused {
			t.Errorf("--timeout %q: exit %d, want %d", raw, code, exitInputRefused)
		}
		if len(s.got()) != 0 || *connects != 0 {
			t.Errorf("--timeout %q still resolved (%d) or sent (%d)", raw, *connects, len(s.got()))
		}
		e := decode(t, stderr.String())["error"].(map[string]interface{})
		if e["message"] != "--timeout must be a finite number of seconds above 0, got '"+raw+"'" {
			t.Errorf("--timeout %q: error = %v", raw, e)
		}
	}
}

func TestIndexDelete(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 200, indexCompleted })
	r, stdout, _, _ := s.run("", true)
	if code := indexDelete(r, "articles", "meta/tag"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if req := only(t, s.got()); req.Method != "DELETE" || req.Path != "/api/v1/collections/articles/index/meta%2Ftag" {
		t.Errorf("sent %s %s", req.Method, req.Path)
	}
	if out := decode(t, stdout.String()); out["deleted"] != true || out["field"] != "meta/tag" {
		t.Errorf("output = %v", out)
	}
}

const langFilter = `{"must":[{"key":"lang","match":{"value":"en"}}]}`

func TestPointsCount(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 200, `{"result":{"count":42},"status":"ok"}` })
	r, stdout, _, _ := s.run("", true)
	if code := pointsCount(r, "articles", langFilter); code != 0 {
		t.Fatalf("exit %d", code)
	}
	req := only(t, s.got())
	if req.Method != "POST" || req.Path != "/api/v1/collections/articles/points/count" || req.Body["exact"] != true {
		t.Errorf("sent %s %s %v", req.Method, req.Path, req.Body)
	}
	if f, ok := req.Body["filter"].(map[string]interface{}); !ok || f["must"] == nil {
		t.Errorf("filter = %v, want the JSON object as given", req.Body["filter"])
	}
	if out := decode(t, stdout.String()); out["count"] != 42.0 || out["collection"] != "articles" {
		t.Errorf("output = %v", out)
	}

	// Human output is the bare number, for $(afy points count ...).
	s = newVecServer(t, func(vecRequest) (int, string) { return 200, `{"result":{"count":7}}` })
	r, stdout, _, _ = s.run("", false)
	if code := pointsCount(r, "articles", ""); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, sent := only(t, s.got()).Body["filter"]; sent || stdout.String() != "7\n" {
		t.Errorf("filter sent %v, stdout %q", sent, stdout.String())
	}
}

func TestPointsGetSendsDigitsAsNumbersAndTheRestAsStrings(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, `{"result":[{"id":17,"payload":{"title":"a"}},{"id":"5c56c793-69f3-4fbf-87e6-c4bf54c28c26","payload":null}]}`
	})
	r, stdout, _, _ := s.run("", true)
	if code := pointsGet(r, "articles", []string{"17", "5c56c793-69f3-4fbf-87e6-c4bf54c28c26"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	req := only(t, s.got())
	ids := req.Body["ids"].([]interface{})
	if req.Path != "/api/v1/collections/articles/points/retrieve" || ids[0] != 17.0 ||
		ids[1] != "5c56c793-69f3-4fbf-87e6-c4bf54c28c26" || req.Body["with_payload"] != true || req.Body["with_vector"] != false {
		t.Errorf("sent %s %v", req.Path, req.Body)
	}
	points := decode(t, stdout.String())["points"].([]interface{})
	p := points[0].(map[string]interface{})
	if p["id"] != 17.0 || p["payload"].(map[string]interface{})["title"] != "a" {
		t.Errorf("point = %v", p)
	}
	if _, scored := p["score"]; scored {
		t.Error("a retrieved point has no score to print")
	}
}

func TestPointsSearch(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 200, `{"result":[{"id":3,"score":0.91,"payload":{"lang":"en"},"version":1}]}`
	})
	vectorFile := filepath.Join(t.TempDir(), "q.json")
	if err := os.WriteFile(vectorFile, []byte("[0.5, -0.25]"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, stdout, _, _ := s.run("", true)
	if code := pointsSearch(r, "articles", "@"+vectorFile, 5, langFilter); code != 0 {
		t.Fatalf("exit %d", code)
	}
	req := only(t, s.got())
	vec := req.Body["vector"].([]interface{})
	if req.Path != "/api/v1/collections/articles/points/search" || len(vec) != 2 || vec[1] != -0.25 ||
		req.Body["limit"] != 5.0 || req.Body["filter"] == nil || req.Body["with_payload"] != true {
		t.Errorf("sent %s %v", req.Path, req.Body)
	}
	hit := decode(t, stdout.String())["points"].([]interface{})[0].(map[string]interface{})
	if hit["id"] != 3.0 || hit["score"] != 0.91 || len(hit) != 3 {
		t.Errorf("hit = %v; want id, score, payload", hit)
	}
}

func TestPointsRefuseBadInputBeforeAnyRequest(t *testing.T) {
	cases := map[string]func(r *vecRun) int{
		"count, filter not an object": func(r *vecRun) int { return pointsCount(r, "a", `[1]`) },
		"search, no vector":           func(r *vecRun) int { return pointsSearch(r, "a", "", 10, "") },
		"search, vector not numbers":  func(r *vecRun) int { return pointsSearch(r, "a", `["x"]`, 10, "") },
		"search, empty vector":        func(r *vecRun) int { return pointsSearch(r, "a", `[]`, 10, "") },
		"search, missing file":        func(r *vecRun) int { return pointsSearch(r, "a", "@/nonexistent/q.json", 10, "") },
		"search, limit 0":             func(r *vecRun) int { return pointsSearch(r, "a", `[1]`, 0, "") },
		"search, filter not JSON":     func(r *vecRun) int { return pointsSearch(r, "a", `[1]`, 10, `{lang}`) },
		"index, no type":              func(r *vecRun) int { return indexCreate(r, "a", "f", "", nil) },
		"index, broken type object":   func(r *vecRun) int { return indexCreate(r, "a", "f", `{"type":`, nil) },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			s := newVecServer(t, func(vecRequest) (int, string) { return 200, `{}` })
			r, _, _, connects := s.run("", false)
			if code := run(r); code != exitInputRefused {
				t.Errorf("exit %d, want %d", code, exitInputRefused)
			}
			if len(s.got()) != 0 || *connects != 0 {
				t.Errorf("a refused input still resolved (%d) or sent (%d)", *connects, len(s.got()))
			}
		})
	}
}

// The real wiring, flags to request: `afy collections list` run through the
// root command, with its endpoint and workspace coming from the environment
// or the flags exactly as a user's shell would supply them.
func TestTheFlagsAndTheEnvironmentReachTheRequest(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) { return 200, `{"collections":[]}` })
	t.Setenv("AETHERFY_CONFIG_DIR", t.TempDir())
	t.Setenv("AETHERFY_API_KEY", "afy_test_0123456789abcdef0123456789abcdef")
	t.Setenv(vectors.EnvVectorsURL, s.srv.URL)
	t.Setenv(vectors.EnvAPIRegion, "")
	t.Setenv(vectors.EnvWorkspace, "from-env")

	run := func(args ...string) map[string]interface{} {
		t.Helper()
		// Cobra keeps parsed values and Changed marks between executions of the
		// same tree; put these back so each run starts as a fresh process would.
		defer func() {
			for _, name := range []string{"json", "workspace", "vectors-url", "api-region"} {
				f := collectionsListCmd.Flag(name)
				_ = f.Value.Set(f.DefValue)
				f.Changed = false
			}
		}()
		rootCmd.SetArgs(append([]string{"collections", "list"}, args...))
		var err error
		out := captureStdout(t, func() { err = rootCmd.Execute() })
		if err != nil {
			t.Fatalf("afy collections list %v: %v", args, err)
		}
		return decode(t, out)
	}

	out := run("--json")
	if out["workspace"] != "from-env" || out["endpoint"] != s.srv.URL {
		t.Errorf("env: envelope = %v", out)
	}
	out = run("--json", "--workspace", "from-flag")
	if out["workspace"] != "from-flag" {
		t.Errorf("--workspace: envelope = %v", out)
	}
	out = run("--json", "--workspace", "")
	if out["workspace"] != nil {
		t.Errorf(`--workspace "": envelope = %v, want no workspace`, out)
	}

	paths := []string{}
	for _, r := range s.got() {
		paths = append(paths, r.Path)
		if r.Auth != "Bearer afy_test_0123456789abcdef0123456789abcdef" {
			t.Errorf("sent Authorization %q, want AETHERFY_API_KEY", r.Auth)
		}
	}
	want := "/api/v1/workspaces/from-env/collections,/api/v1/workspaces/from-flag/collections,/api/v1/collections"
	if strings.Join(paths, ",") != want {
		t.Errorf("paths = %v, want %s", paths, want)
	}
}

// A command line cobra cannot parse is refused input, exit 2, on the vector
// commands. Measured before this existed: `--size abc`, a missing <name> and an
// unknown flag each exited 1, the code a script reads as "the request failed".
// Other commands keep their 1.
func TestAnUnparseableVectorCommandLineExitsTwo(t *testing.T) {
	t.Setenv("AETHERFY_CONFIG_DIR", t.TempDir())
	t.Setenv("AETHERFY_API_KEY", "afy_test_0123456789abcdef0123456789abcdef")
	cases := [][]string{
		{"collections", "create", "a", "--size", "abc", "--distance", "cosine"},
		{"points", "search", "a", "--vector", "[1]", "--limit", "x"},
		{"collections", "get"},
		{"index", "create", "a"},
		{"points", "get", "a"},
		{"collections", "list", "--bogus"},
	}
	for _, args := range cases {
		rootCmd.SetArgs(args)
		var err error
		captureStderr(t, func() { err = rootCmd.Execute() })
		if err == nil || ExitCode(err) != exitInputRefused {
			t.Errorf("afy %s: err %v, exit %d; want exit %d", strings.Join(args, " "), err, ExitCode(err), exitInputRefused)
		}
	}
	rootCmd.SetArgs([]string{"list", "--bogus"})
	var err error
	captureStderr(t, func() { err = rootCmd.Execute() })
	if err == nil || ExitCode(err) != 1 {
		t.Errorf("afy list --bogus: exit %d; a non-vector command keeps 1", ExitCode(err))
	}
	rootCmd.SetArgs(nil)
}

func TestNotLoggedInIsExitThreeAndJSONUnderJSON(t *testing.T) {
	t.Setenv("AETHERFY_CONFIG_DIR", t.TempDir())
	t.Setenv("AETHERFY_API_KEY", "")
	if _, err := config.LoadCredentials(); err != nil {
		t.Fatal(err)
	}
	s := newVecServer(t, func(vecRequest) (int, string) { return 200, `{"collections":[]}` })
	r, stdout, stderr, connects := s.run("", true)
	if code := vecMain(r, collectionsList); code != exitNotLoggedIn {
		t.Errorf("exit %d, want %d", code, exitNotLoggedIn)
	}
	if e := decode(t, stderr.String())["error"].(map[string]interface{}); e["message"] != "Not logged in. Run 'afy login' first." {
		t.Errorf("stderr error = %v", e)
	}
	if stdout.Len() != 0 || *connects != 0 || len(s.got()) != 0 {
		t.Error("a logged-out command printed, resolved or sent something")
	}
}

// Every command, against an API that refuses it: exit 1, nothing on stdout,
// and the API's status, code and message on stderr, unchanged.
func TestEveryVectorCommandReportsTheAPIsErrorAndExitsNonZero(t *testing.T) {
	const message = "Collection 'articles' not found"
	commands := map[string]func(r *vecRun) int{
		"collections list":   func(r *vecRun) int { return collectionsList(r) },
		"collections get":    func(r *vecRun) int { return collectionsGet(r, "articles") },
		"collections create": func(r *vecRun) int { return collectionsCreate(r, "articles", 8, "cosine", nil) },
		"collections delete": func(r *vecRun) int {
			return collectionsDelete(r, "articles", true, strings.NewReader(""), false)
		},
		"index create":  func(r *vecRun) int { return indexCreate(r, "articles", "f", "keyword", nil) },
		"index delete":  func(r *vecRun) int { return indexDelete(r, "articles", "f") },
		"points count":  func(r *vecRun) int { return pointsCount(r, "articles", "") },
		"points get":    func(r *vecRun) int { return pointsGet(r, "articles", []string{"1"}) },
		"points search": func(r *vecRun) int { return pointsSearch(r, "articles", "[1]", 10, "") },
	}
	if len(commands) != len(vectorLeaves()) {
		t.Fatalf("%d commands here, %d vector commands registered: cover the new one", len(commands), len(vectorLeaves()))
	}
	refuse := func(vecRequest) (int, string) {
		return 404, `{"error":{"code":"NOT_FOUND","message":"` + message + `"}}`
	}
	for name, run := range commands {
		t.Run(name+" --json", func(t *testing.T) {
			s := newVecServer(t, refuse)
			r, stdout, stderr, _ := s.run("", true)
			if code := run(r); code != exitRequestFailed {
				t.Errorf("exit %d, want %d", code, exitRequestFailed)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", stdout.String())
			}
			e := decode(t, stderr.String())["error"].(map[string]interface{})
			if e["status"] != 404.0 || e["code"] != "NOT_FOUND" || e["message"] != message {
				t.Errorf("stderr error = %v", e)
			}
			if len(s.got()) != 1 {
				t.Errorf("requests = %d; a 404 is not retried", len(s.got()))
			}
		})
		t.Run(name+" text", func(t *testing.T) {
			s := newVecServer(t, refuse)
			r, _, stderr, _ := s.run("", false)
			if code := run(r); code != exitRequestFailed {
				t.Errorf("exit %d, want %d", code, exitRequestFailed)
			}
			if stderr.String() != "Error: "+message+" (NOT_FOUND)\n" {
				t.Errorf("stderr = %q", stderr.String())
			}
		})
	}
}
