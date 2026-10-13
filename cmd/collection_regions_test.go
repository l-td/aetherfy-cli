package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// afy collections regions / move, against a fake vectors API (the name -> id
// lookup) and a fake control plane (preview, change, operation). The CP
// client is the real one, pointed at the fake.

const articlesGet = `{"result":{"id":"c-1","name":"articles","config":{"params":{"vectors":{"size":4,"distance":"Cosine"}}},` +
	`"points_count":3,"status":"green","regions":["us-east-1"]}}`

func previewBody(extra string) string {
	return `{"collection_id":"c-1","from_regions":["us-east-1"],"to_regions":["us-east-1","eu-central-1"],` +
		`"regions_to_add":["eu-central-1"],"regions_to_remove":[],"no_op":false,"copy_bytes":3221225472,` +
		`"data_deleted_from":[],"limits":{"counted":true,"window_days":30,"count":{"limit":10,"used":3,"this_change":4},` +
		`"size":{"limit_bytes":429496729600,"used_bytes":53687091200,"left_after_bytes":372588412928}}` + extra + `}`
}

// cpFake answers the control plane's routes from a table: "METHOD path" ->
// a function of the request, so a test can count and inspect.
func cpFake(t *testing.T, routes map[string]func(vecRequest) (int, string)) *vecServer {
	return newVecServer(t, func(r vecRequest) (int, string) {
		if h, ok := routes[r.Method+" "+r.Path]; ok {
			return h(r)
		}
		t.Errorf("unexpected control-plane request %s %s", r.Method, r.Path)
		return 404, `{"detail":{"code":"NOT_FOUND","message":"no"}}`
	})
}

func runWithCP(vec *vecServer, cp *vecServer, asJSON bool) (*vecRun, *strings.Builder, *strings.Builder) {
	r, stdout, stderr, _ := vec.run("", asJSON)
	r.cp = api.NewClientWithURL(cp.srv.URL+"/api/v1", "afy_test_key")
	out, errOut := &strings.Builder{}, &strings.Builder{}
	r.stdout, r.stderr = out, errOut
	_, _ = stdout, stderr
	return r, out, errOut
}

func requestsTo(s *vecServer, method, path string) []vecRequest {
	var out []vecRequest
	for _, r := range s.got() {
		if r.Method == method && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

const (
	previewPath = "/api/v1/collections/c-1/regions/preview"
	changePath  = "/api/v1/collections/c-1"
)

func standardCP(t *testing.T, preview string) *vecServer {
	return cpFake(t, map[string]func(vecRequest) (int, string){
		"POST " + previewPath: func(vecRequest) (int, string) { return 200, preview },
		"PATCH " + changePath: func(vecRequest) (int, string) {
			return 202, `{"operation_id":"op-1","collection_id":"c-1","regions":["us-east-1","eu-central-1"]}`
		},
		"GET /api/v1/operations/op-1": func(vecRequest) (int, string) {
			return 200, `{"operation_id":"op-1","status":"succeeded"}`
		},
	})
}

func vecWithArticles(t *testing.T) *vecServer {
	return newVecServer(t, func(vecRequest) (int, string) { return 200, articlesGet })
}

var twoRegions = []string{"us-east-1", "eu-central-1"}

func TestCollectionsRegionsShowsThePreviewAndAsksBeforeChanging(t *testing.T) {
	vec, cp := vecWithArticles(t), standardCP(t, previewBody(`,"refusal":null`))
	r, stdout, stderr := runWithCP(vec, cp, false)

	if code := collectionsRegions(r, "articles", twoRegions, false, false, strings.NewReader("y\n"), true); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"us-east-1 -> us-east-1, eu-central-1",
		"Adds eu-central-1: copies 3 GB of data into it.",
		"Region change 4 of 10 that copy data in the last 30 days.",
		"50 GB of 400 GB copied between regions in the last 30 days; 347 GB left after this change.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(stderr.String(), "Proceed? [y/N]") {
		t.Errorf("the question goes to stderr: %q", stderr.String())
	}
	reqs := requestsTo(cp, "PATCH", changePath)
	if len(reqs) != 1 {
		t.Fatalf("PATCH sent %d times", len(reqs))
	}
	if got := reqs[0].Body["regions"].([]interface{}); len(got) != 2 || got[1] != "eu-central-1" {
		t.Errorf("PATCH body = %v", reqs[0].Body)
	}
	// The collection was found by name through the vectors API.
	if req := only(t, vec.got()); req.Path != "/api/v1/collections/articles" {
		t.Errorf("lookup = %s", req.Path)
	}
}

func TestCollectionsRegionsChangesNothingUnlessConfirmed(t *testing.T) {
	for _, answer := range []string{"\n", "n\n", "no\n", "maybe\n", ""} {
		vec, cp := vecWithArticles(t), standardCP(t, previewBody(`,"refusal":null`))
		r, _, stderr := runWithCP(vec, cp, false)
		if code := collectionsRegions(r, "articles", twoRegions, false, false, strings.NewReader(answer), true); code != exitInputRefused {
			t.Errorf("answer %q: exit %d, want %d", answer, code, exitInputRefused)
		}
		if n := len(requestsTo(cp, "PATCH", changePath)); n != 0 {
			t.Errorf("answer %q: changed without a yes", answer)
		}
		if !strings.Contains(stderr.String(), "not confirmed") {
			t.Errorf("answer %q: stderr %q", answer, stderr.String())
		}
	}
}

func TestCollectionsRegionsARefusedPreviewSendsNothing(t *testing.T) {
	refusal := `,"refusal":{"status":422,"code":"REGION_CHANGE_COUNT_LIMIT_EXCEEDED",` +
		`"message":"Your plan allows 10 region changes that copy data in 30 days, and 10 have been made."}`
	vec, cp := vecWithArticles(t), standardCP(t, previewBody(refusal))

	r, stdout, stderr := runWithCP(vec, cp, false)
	if code := collectionsRegions(r, "articles", twoRegions, true, false, strings.NewReader(""), true); code != exitRequestFailed {
		t.Fatalf("exit %d, want %d", code, exitRequestFailed)
	}
	if n := len(requestsTo(cp, "PATCH", changePath)); n != 0 {
		t.Error("a refused change was sent")
	}
	// The refusal is said once, on stderr with its code; the preview does not
	// describe the window for a change that will not be made.
	if !strings.Contains(stderr.String(), "Your plan allows 10") || !strings.Contains(stderr.String(), "REGION_CHANGE_COUNT_LIMIT_EXCEEDED") {
		t.Errorf("stderr %q", stderr.String())
	}
	if strings.Contains(stdout.String(), "Your plan allows") || strings.Contains(stdout.String(), "of 10 that copy data") {
		t.Errorf("stdout repeats the refusal or describes the window: %q", stdout.String())
	}

	// --json: the refusal is the one JSON object on stderr, with its status and code.
	vec, cp = vecWithArticles(t), standardCP(t, previewBody(refusal))
	r, _, stderr = runWithCP(vec, cp, true)
	if code := collectionsRegions(r, "articles", twoRegions, true, false, strings.NewReader(""), false); code != exitRequestFailed {
		t.Fatalf("--json exit %d", code)
	}
	e := decode(t, stderr.String())["error"].(map[string]interface{})
	if e["status"] != 422.0 || e["code"] != "REGION_CHANGE_COUNT_LIMIT_EXCEEDED" {
		t.Errorf("error = %v", e)
	}
}

func TestCollectionsRegionsRefusesBeforeAnyRequestWhenNobodyCanConfirm(t *testing.T) {
	cases := []struct {
		name         string
		asJSON       bool
		interactive  bool
		regions      []string
		wantInStderr string
	}{
		{"--json without --yes", true, true, twoRegions, "--yes"},
		{"no terminal without --yes", false, false, twoRegions, "--yes"},
		{"no regions", false, true, nil, "--regions is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vec, cp := vecWithArticles(t), standardCP(t, previewBody(`,"refusal":null`))
			r, _, stderr := runWithCP(vec, cp, c.asJSON)
			if code := collectionsRegions(r, "articles", c.regions, false, false, strings.NewReader("y\n"), c.interactive); code != exitInputRefused {
				t.Errorf("exit %d, want %d", code, exitInputRefused)
			}
			if len(vec.got())+len(cp.got()) != 0 {
				t.Errorf("sent %d request(s) before refusing", len(vec.got())+len(cp.got()))
			}
			if !strings.Contains(stderr.String(), c.wantInStderr) {
				t.Errorf("stderr %q", stderr.String())
			}
		})
	}
}

func TestCollectionsRegionsYesJSONPrintsThePreviewAndTheOperation(t *testing.T) {
	vec, cp := vecWithArticles(t), standardCP(t, previewBody(`,"refusal":null`))
	r, stdout, stderr := runWithCP(vec, cp, true)
	if code := collectionsRegions(r, "articles", twoRegions, true, false, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "Proceed") {
		t.Error("--yes asked anyway")
	}
	out := decode(t, stdout.String())
	if out["collection"] != "articles" || out["operation_id"] != "op-1" {
		t.Errorf("output = %v", out)
	}
	p := out["preview"].(map[string]interface{})
	if p["copy_bytes"] != 3221225472.0 {
		t.Errorf("preview = %v", p)
	}
}

func TestCollectionsRegionsANoOpSendsNoChange(t *testing.T) {
	noop := `{"collection_id":"c-1","from_regions":["us-east-1"],"to_regions":["us-east-1"],"regions_to_add":[],` +
		`"regions_to_remove":[],"no_op":true,"copy_bytes":0,"data_deleted_from":[],"limits":null,"refusal":null}`
	vec, cp := vecWithArticles(t), standardCP(t, noop)
	r, stdout, _ := runWithCP(vec, cp, false)
	if code := collectionsRegions(r, "articles", []string{"us-east-1"}, false, false, strings.NewReader(""), true); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if n := len(requestsTo(cp, "PATCH", changePath)); n != 0 {
		t.Error("a no-op was sent")
	}
	if !strings.Contains(stdout.String(), "No change") {
		t.Errorf("stdout %q", stdout.String())
	}
}

func shortWait(t *testing.T, bound time.Duration) {
	t.Helper()
	b, i, s := operationWaitBound, operationPollInterval, operationSleep
	operationWaitBound, operationPollInterval, operationSleep = bound, time.Second, func(time.Duration) {}
	t.Cleanup(func() { operationWaitBound, operationPollInterval, operationSleep = b, i, s })
}

func TestCollectionsRegionsWaitFollowsTheOperationToItsEnd(t *testing.T) {
	statuses := func(seq ...string) func(vecRequest) (int, string) {
		n := 0
		return func(vecRequest) (int, string) {
			s := seq[len(seq)-1]
			if n < len(seq) {
				s = seq[n]
			}
			n++
			body := `{"operation_id":"op-1","status":"` + s + `"`
			if s == "failed" {
				body += `,"failures":[{"message":"a region could not be reached"}]`
			}
			return 200, body + `}`
		}
	}
	cases := []struct {
		name  string
		seq   []string
		bound time.Duration
		want  int
		in    string
	}{
		{"succeeds", []string{"pending", "in_progress", "succeeded"}, time.Minute, 0, "now in us-east-1, eu-central-1"},
		{"fails: exit 1 with the failure", []string{"in_progress", "failed"}, time.Minute, exitRequestFailed, "a region could not be reached"},
		{"still running at the bound: exit 4", []string{"in_progress"}, 3 * time.Second, exitStillPending, "still in_progress"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			shortWait(t, c.bound)
			cp := cpFake(t, map[string]func(vecRequest) (int, string){
				"POST " + previewPath:         func(vecRequest) (int, string) { return 200, previewBody(`,"refusal":null`) },
				"PATCH " + changePath:         func(vecRequest) (int, string) { return 202, `{"operation_id":"op-1"}` },
				"GET /api/v1/operations/op-1": statuses(c.seq...),
			})
			r, stdout, stderr := runWithCP(vecWithArticles(t), cp, false)
			if code := collectionsRegions(r, "articles", twoRegions, true, true, strings.NewReader(""), true); code != c.want {
				t.Fatalf("exit %d, want %d\n%s%s", code, c.want, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String()+stderr.String(), c.in) {
				t.Errorf("output lacks %q:\n%s%s", c.in, stdout.String(), stderr.String())
			}
		})
	}
}

func TestCollectionsMoveAsksNothingAndSendsTheTargetsID(t *testing.T) {
	moved := func(vecRequest) (int, string) {
		return 202, `{"operation_id":"op-2","collection_id":"c-1","regions":["us-east-1"]}`
	}

	t.Run("into a workspace: its id, from its name", func(t *testing.T) {
		cp := cpFake(t, map[string]func(vecRequest) (int, string){
			"GET /api/v1/workspaces/research": func(vecRequest) (int, string) { return 200, `{"id":"w-9","name":"research"}` },
			"PATCH " + changePath:             moved,
		})
		r, stdout, stderr := runWithCP(vecWithArticles(t), cp, false)
		if code := collectionsMove(r, "articles", "research", true, false); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr.String())
		}
		reqs := requestsTo(cp, "PATCH", changePath)
		if len(reqs) != 1 || reqs[0].Body["workspace_id"] != "w-9" {
			t.Fatalf("PATCH = %v", reqs)
		}
		if _, sent := reqs[0].Body["regions"]; sent {
			t.Error("a move must not send regions")
		}
		if !strings.Contains(stdout.String(), "moving to workspace 'research'") {
			t.Errorf("stdout %q", stdout.String())
		}
	})
	t.Run(`--to "" moves out of any workspace: workspace_id null`, func(t *testing.T) {
		cp := cpFake(t, map[string]func(vecRequest) (int, string){"PATCH " + changePath: moved})
		r, _, stderr := runWithCP(vecWithArticles(t), cp, true)
		if code := collectionsMove(r, "articles", "", true, false); code != 0 {
			t.Fatalf("exit %d: %s", code, stderr.String())
		}
		reqs := requestsTo(cp, "PATCH", changePath)
		if v, ok := reqs[0].Body["workspace_id"]; !ok || v != nil {
			t.Errorf("PATCH body = %v, want workspace_id null", reqs[0].Body)
		}
	})
	t.Run("--to missing: refused before any request", func(t *testing.T) {
		vec := vecWithArticles(t)
		cp := cpFake(t, nil)
		r, _, stderr := runWithCP(vec, cp, false)
		if code := collectionsMove(r, "articles", "", false, false); code != exitInputRefused {
			t.Errorf("exit %d", code)
		}
		if len(vec.got())+len(cp.got()) != 0 {
			t.Error("sent requests before refusing")
		}
		if !strings.Contains(stderr.String(), "--to is required") {
			t.Errorf("stderr %q", stderr.String())
		}
	})
	t.Run("the move's refusal is reported with its code", func(t *testing.T) {
		cp := cpFake(t, map[string]func(vecRequest) (int, string){
			"GET /api/v1/workspaces/research": func(vecRequest) (int, string) { return 200, `{"id":"w-9","name":"research"}` },
			"PATCH " + changePath: func(vecRequest) (int, string) {
				return 422, `{"detail":{"code":"COLLECTION_MOVE_OUTSIDE_TARGET_REGIONS","message":"placed in eu-central-1"}}`
			},
		})
		r, _, stderr := runWithCP(vecWithArticles(t), cp, true)
		if code := collectionsMove(r, "articles", "research", true, false); code != exitRequestFailed {
			t.Errorf("exit %d", code)
		}
		e := decode(t, stderr.String())["error"].(map[string]interface{})
		if e["status"] != 422.0 || e["code"] != "COLLECTION_MOVE_OUTSIDE_TARGET_REGIONS" {
			t.Errorf("error = %v", e)
		}
	})
}

func TestHumanBytesMatchesTheDashboard(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 3221225472: "3 GB", 429496729600: "400 GB", 1536: "1.5 KB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
