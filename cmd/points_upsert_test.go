package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/vectors"
)

// afy points upsert: what is refused before any request, the request the API
// gets, and how a failure is reported, including one after earlier requests
// of the same command were written.

const upsertOK = `{"result":{"operation_id":1,"status":"completed"},"status":"ok","time":0.001}`

func upsertServer(t *testing.T) *vecServer {
	return newVecServer(t, func(vecRequest) (int, string) { return 200, upsertOK })
}

// One point, inline: one PUT to the SDKs' upsert path, the point sent as
// typed inside {"points": [...]}, and the one-line summary.
func TestPointsUpsertOnePointInline(t *testing.T) {
	s := upsertServer(t)
	r, stdout, stderr, _ := s.run("", false)
	in := `{"id": 1, "vector": [0.5, -0.25], "payload": {"lang": "en"}}`
	if code := pointsUpsert(r, "articles", in); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	req := only(t, s.got())
	if req.Method != "PUT" || req.Path != "/api/v1/collections/articles/points" {
		t.Errorf("sent %s %s", req.Method, req.Path)
	}
	if len(req.Body) != 1 {
		t.Errorf("body = %v; want only points", req.Body)
	}
	points := req.Body["points"].([]interface{})
	got := fmt.Sprint(points)
	if len(points) != 1 || got != "[map[id:1 payload:map[lang:en] vector:[0.5 -0.25]]]" {
		t.Errorf("points = %s", got)
	}
	if stdout.String() != "Upserted 1 point(s) into collection 'articles'.\n" || stderr.Len() != 0 {
		t.Errorf("stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}

// An array, from a file, in a workspace, with --json: the workspace's nested
// path and the --json object the other points commands print.
func TestPointsUpsertAnArrayFromAFileAsJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "points.json")
	body := `[
  {"id": "5c56c793-69f3-4fbf-87e6-c4bf54c28c26", "vector": [1, 0]},
  {"id": 2, "vector": [0, 1], "payload": {}}
]`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s := upsertServer(t)
	r, stdout, _, _ := s.run("team", true)
	if code := pointsUpsert(r, "articles", "@"+path); code != 0 {
		t.Fatalf("exit %d", code)
	}
	req := only(t, s.got())
	if req.Path != "/api/v1/workspaces/team/collections/articles/points" {
		t.Errorf("path %s", req.Path)
	}
	points := req.Body["points"].([]interface{})
	if len(points) != 2 || points[0].(map[string]interface{})["id"] != "5c56c793-69f3-4fbf-87e6-c4bf54c28c26" {
		t.Errorf("points = %v", points)
	}
	out := decode(t, stdout.String())
	if out["collection"] != "articles" || out["upserted"] != 2.0 || out["workspace"] != "team" || out["endpoint"] != s.srv.URL {
		t.Errorf("--json = %v", out)
	}
}

// Refused before any request, exit 2: nothing resolved, nothing sent.
func TestPointsUpsertRefusesBadInputBeforeAnyRequest(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.json")
	cases := map[string]struct{ in, want string }{
		"no --points":       {"", "--points is required: a point object or a JSON array of them, or @path to them"},
		"blank":             {"  \n", "--points is required: a point object or a JSON array of them, or @path to them"},
		"not JSON":          {`{"id": 1, "vector": [1,}`, "--points is not valid JSON: "},
		"array not JSON":    {`[{"id": 1, "vector": [1]}`, "--points is not valid JSON: "},
		"empty array":       {`[]`, "--points is an empty array: give at least one point"},
		"not an object":     {`[{"id": 1, "vector": [1]}, 7]`, "--points: point 2 is not a JSON object"},
		"bare number":       {`7`, "--points: point 1 is not a JSON object"},
		"no id":             {`{"vector": [1]}`, "--points: point 1 has no id"},
		"null id":           {`{"id": null, "vector": [1]}`, "--points: point 1 has no id"},
		"no vector":         {`[{"id": 1, "vector": [1]}, {"id": 2}]`, "--points: point 2 has no vector"},
		"null vector":       {`{"id": 1, "vector": null}`, "--points: point 1 has no vector"},
		"file that is gone": {"@" + missing, "--points @" + missing + ": "},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := upsertServer(t)
			r, stdout, stderr, connects := s.run("", true)
			if code := pointsUpsert(r, "articles", tc.in); code != exitInputRefused {
				t.Errorf("exit %d, want %d", code, exitInputRefused)
			}
			if len(s.got()) != 0 || *connects != 0 || stdout.Len() != 0 {
				t.Errorf("a refused upsert resolved (%d), sent (%d) or printed %q", *connects, len(s.got()), stdout.String())
			}
			msg, _ := decode(t, stderr.String())["error"].(map[string]interface{})["message"].(string)
			if !strings.HasPrefix(msg, tc.want) {
				t.Errorf("error = %q\nwant prefix %q", msg, tc.want)
			}
		})
	}
}

// What the CLI leaves to the server reaches it: a wrong-sized vector, a
// reserved payload key, an id of the wrong type are sent, and the server's
// refusal is printed with its code, exit 1.
func TestPointsUpsertLeavesTheServersRulesToTheServer(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 400, `{"error":{"code":"VALIDATION_ERROR","message":"Vector dimension mismatch: expected 3, got 1"}}`
	})
	r, stdout, stderr, _ := s.run("", false)
	in := `{"id": "not-a-uuid", "vector": [1], "payload": {"__aetherfy_agent_id": "x"}}`
	if code := pointsUpsert(r, "articles", in); code != exitRequestFailed {
		t.Errorf("exit %d", code)
	}
	only(t, s.got())
	if stdout.Len() != 0 || stderr.String() != "Error: Vector dimension mismatch: expected 3, got 1 (VALIDATION_ERROR)\n" {
		t.Errorf("stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}

func manyPoints(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf(`{"id":%d,"vector":[1]}`, i)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// More than one request's worth goes as sequential requests of at most
// vectors.UpsertPointsMax, in order.
func TestPointsUpsertSplitsAtTheAPIsPointCap(t *testing.T) {
	total := 2*vectors.UpsertPointsMax + 1
	s := upsertServer(t)
	r, stdout, _, _ := s.run("", true)
	if code := pointsUpsert(r, "articles", manyPoints(total)); code != 0 {
		t.Fatalf("exit %d", code)
	}
	reqs := s.got()
	if len(reqs) != 3 {
		t.Fatalf("%d requests, want 3", len(reqs))
	}
	next := 0.0
	for i, want := range []int{vectors.UpsertPointsMax, vectors.UpsertPointsMax, 1} {
		points := reqs[i].Body["points"].([]interface{})
		if len(points) != want {
			t.Errorf("request %d carried %d points, want %d", i+1, len(points), want)
		}
		for _, p := range points {
			if id := p.(map[string]interface{})["id"].(float64); id != next {
				t.Fatalf("request %d: point %v out of order, want %v", i+1, id, next)
			}
			next++
		}
	}
	if out := decode(t, stdout.String()); out["upserted"] != float64(total) {
		t.Errorf("--json = %v", out)
	}
}

// A request that fails after earlier ones were written: it stops there, exit 1,
// and says how many points were written, in --json as error.written beside the
// server's own status, code and message.
func TestPointsUpsertReportsWhatWasWrittenBeforeAFailure(t *testing.T) {
	total := 2*vectors.UpsertPointsMax + 5
	failing := func() *vecServer {
		n := 0
		return newVecServer(t, func(vecRequest) (int, string) {
			n++
			if n == 2 {
				return 413, `{"error":{"code":"PAYLOAD_TOO_LARGE","message":"Upsert body too large"}}`
			}
			return 200, upsertOK
		})
	}

	s := failing()
	r, stdout, stderr, _ := s.run("", true)
	if code := pointsUpsert(r, "articles", manyPoints(total)); code != exitRequestFailed {
		t.Errorf("exit %d", code)
	}
	if len(s.got()) != 2 || stdout.Len() != 0 {
		t.Errorf("%d requests (want 2: none after the failure), stdout %q", len(s.got()), stdout.String())
	}
	e := decode(t, stderr.String())["error"].(map[string]interface{})
	if e["status"] != 413.0 || e["code"] != "PAYLOAD_TOO_LARGE" || e["message"] != "Upsert body too large" ||
		e["written"] != float64(vectors.UpsertPointsMax) {
		t.Errorf("--json error = %v", e)
	}
	// A refusal of a request no larger than one vectordb chunk wrote nothing
	// of it: no unconfirmed points.
	if _, has := e["unconfirmed"]; has {
		t.Errorf("--json error = %v; a refused one-chunk request is not unconfirmed", e)
	}

	s = failing()
	r, _, stderr, _ = s.run("", false)
	pointsUpsert(r, "articles", manyPoints(total))
	want := fmt.Sprintf("Error: Upsert body too large (PAYLOAD_TOO_LARGE); %d of %d point(s) were written before it\n", vectors.UpsertPointsMax, total)
	if stderr.String() != want {
		t.Errorf("stderr %q\nwant   %q", stderr.String(), want)
	}
}

// A first request that fails wrote nothing, so there is nothing to add: the
// error is the server's, as for every other command.
func TestPointsUpsertFailingFirstRequestReportsNoWrittenCount(t *testing.T) {
	s := newVecServer(t, func(vecRequest) (int, string) {
		return 404, `{"error":{"code":"NOT_FOUND","message":"Collection 'articles' not found"}}`
	})
	r, _, stderr, _ := s.run("", true)
	pointsUpsert(r, "articles", manyPoints(vectors.UpsertPointsMax+1))
	e := decode(t, stderr.String())["error"].(map[string]interface{})
	if _, has := e["written"]; has || len(s.got()) != 1 {
		t.Errorf("error = %v after %d requests", e, len(s.got()))
	}
}

// A request that gets no answer has an unknown outcome. Here the connection
// is dropped on the second request: the first request's points are reported
// written, the second's as unconfirmed (never as not written), with the
// advice that re-running is safe. The same classification covers a timeout
// (internal/vectors TestUpsertTimeoutOnTheSecondRequestIsUnconfirmed drives a
// real one; the client's timeout is not settable from here).
func TestPointsUpsertReportsTheLostRequestAsUnconfirmed(t *testing.T) {
	total := vectors.UpsertPointsMax + 5
	dropping := func() (*vecServer, *int) {
		n := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			n++
			if n == 2 {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					conn.Close()
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(upsertOK))
		}))
		t.Cleanup(srv.Close)
		return &vecServer{t: t, srv: srv}, &n
	}

	s, n := dropping()
	r, stdout, stderr, _ := s.run("", true)
	if code := pointsUpsert(r, "articles", manyPoints(total)); code != exitRequestFailed {
		t.Errorf("exit %d", code)
	}
	if *n != 2 || stdout.Len() != 0 {
		t.Errorf("%d requests, stdout %q", *n, stdout.String())
	}
	e := decode(t, stderr.String())["error"].(map[string]interface{})
	if e["written"] != float64(vectors.UpsertPointsMax) || e["unconfirmed"] != 5.0 {
		t.Errorf("--json error = %v; want written %d, unconfirmed 5", e, vectors.UpsertPointsMax)
	}
	if _, has := e["status"]; has {
		t.Errorf("--json error = %v; no answer means no status", e)
	}
	msg, _ := e["message"].(string)
	if !strings.Contains(msg, "Re-running the same upsert is safe") {
		t.Errorf("message %q does not say re-running is safe", msg)
	}

	s, _ = dropping()
	r, _, stderr, _ = s.run("", false)
	pointsUpsert(r, "articles", manyPoints(total))
	want := fmt.Sprintf("; %d of %d point(s) were written before it, and the outcome of the 5 point(s) in the request "+
		"that failed is unknown: they may or may not have been written. Re-running the same upsert is safe: points are "+
		"replaced by id\n", vectors.UpsertPointsMax, total)
	if got := stderr.String(); !strings.HasPrefix(got, "Error: Network connection failed: ") || !strings.HasSuffix(got, want) {
		t.Errorf("stderr %q\nwant suffix %q", got, want)
	}
}
