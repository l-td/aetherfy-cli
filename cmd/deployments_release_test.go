package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// A RUN IS NOT A DEPLOYMENT (benchmark run 5, AFY-8). The server's history holds
// both: a run is a row of its own, with a version number in the same sequence.
// `afy deployments` lists the releases; a failed run used to be "the latest
// deployment" and the hint beneath the table then offered a rollback to the
// release the run had executed.

// A serving release and a completed run of it: the plain history.
const deploymentsBody = `[` +
	`{"id":"run-4","agent_id":"a1","version":4,"release_version":1,"state":"completed","is_ephemeral":true,"created_at":"2026-09-17T12:20:09Z"},` +
	`{"id":"rel-1","agent_id":"a1","version":1,"release_version":null,"state":"active","created_at":"2026-09-17T12:00:00Z"}]`

// A broken release v7 (it went active; its runs fail), two failed runs of it,
// and the releases before it: v6 failed to build (and kept its archive, so the
// server would accept a rollback to it), v5 is the last good one.
const afterAFailedRunBody = `[` +
	`{"id":"run-9","agent_id":"a1","version":9,"release_version":7,"state":"failed","is_ephemeral":true,"created_at":"2026-09-17T12:30:00Z","error_message":"run failed"},` +
	`{"id":"run-8","agent_id":"a1","version":8,"release_version":7,"state":"failed","is_ephemeral":true,"created_at":"2026-09-17T12:20:00Z","error_message":"run failed"},` +
	`{"id":"rel-7","agent_id":"a1","version":7,"release_version":null,"state":"active","can_rollback":false,"created_at":"2026-09-17T12:10:00Z"},` +
	`{"id":"rel-6","agent_id":"a1","version":6,"release_version":null,"state":"failed","can_rollback":true,"created_at":"2026-09-17T12:05:00Z"},` +
	`{"id":"rel-5","agent_id":"a1","version":5,"release_version":null,"state":"superseded","can_rollback":true,"created_at":"2026-09-17T12:00:00Z"}]`

// The same, after v8 -- a RELEASE -- failed to deploy.
const afterAFailedReleaseBody = `[` +
	`{"id":"rel-8","agent_id":"a1","version":8,"release_version":null,"state":"failed","can_rollback":true,"created_at":"2026-09-17T12:20:00Z","error_message":"build failed"},` +
	`{"id":"run-7","agent_id":"a1","version":7,"release_version":6,"state":"completed","is_ephemeral":true,"created_at":"2026-09-17T12:10:00Z"},` +
	`{"id":"rel-6","agent_id":"a1","version":6,"release_version":null,"state":"failed","can_rollback":true,"created_at":"2026-09-17T12:05:00Z"},` +
	`{"id":"rel-5","agent_id":"a1","version":5,"release_version":null,"state":"superseded","can_rollback":true,"created_at":"2026-09-17T12:00:00Z"}]`

// A failed release with nothing good to go back to.
const noGoodReleaseBody = `[` +
	`{"id":"rel-2","agent_id":"a1","version":2,"release_version":null,"state":"failed","can_rollback":true,"created_at":"2026-09-17T12:20:00Z","error_message":"build failed"},` +
	`{"id":"rel-1","agent_id":"a1","version":1,"release_version":null,"state":"failed","can_rollback":true,"created_at":"2026-09-17T12:00:00Z"}]`

func deploymentsOutput(t *testing.T, body string) string {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		// The history is an array; the agent read that follows it (for the push
		// waiting on the deploy) is one object. A body of the wrong shape fails
		// to decode and is retried with real waits.
		if !strings.HasSuffix(r.URL.Path, "/deployments") {
			_, _ = w.Write([]byte(`{"id":"a1","user_id":"u1","name":"ticker","status":"running","agent_type":"job"}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	var err error
	out := captureStdout(t, func() {
		err = printDeployments(api.NewClientWithURL(srv.URL, "afy_test_key"), "ticker")
	})
	if err != nil {
		t.Fatalf("afy deployments failed: %v", err)
	}
	// No read is made twice: a retried read is an answer the client could not
	// decode, and costs real seconds of waiting.
	mu.Lock()
	defer mu.Unlock()
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			t.Errorf("%s was requested more than once (%v): an answer the client could not decode is retried", p, paths)
		}
		seen[p] = true
	}
	return out
}

// rowVersions: the first cell of each table row ("v7"), in order.
func rowVersions(out string) []string {
	var versions []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		first := fields[0]
		if len(first) >= 2 && first[0] == 'v' && first[1] >= '0' && first[1] <= '9' && !strings.HasSuffix(first, ":") {
			versions = append(versions, first)
		}
	}
	return versions
}

func TestAFailedRunIsNotListedAsADeploymentAndSuggestsNoRollback(t *testing.T) {
	out := deploymentsOutput(t, afterAFailedRunBody)

	if got := strings.Join(rowVersions(out), " "); got != "v7 v6 v5" {
		t.Errorf("the table lists %q, want the releases v7 v6 v5 and no run:\n%s", got, out)
	}
	if !strings.Contains(out, "Total: 3 deployment(s)") {
		t.Errorf("the total counts the runs:\n%s", out)
	}
	// The newest RELEASE is serving: nothing failed to deploy, so nothing to
	// roll back from. The run's failure is `afy runs`'s to show.
	for _, unwanted := range []string{"Latest deployment failed", "Roll back to"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a failed RUN produced %q:\n%s", unwanted, out)
		}
	}
}

func TestTheSuggestionAfterAFailedReleaseIsTheLastGoodRelease(t *testing.T) {
	out := deploymentsOutput(t, afterAFailedReleaseBody)

	if !strings.Contains(out, "Roll back to v5: afy rollback ticker 5") {
		t.Errorf("want the last good release, v5:\n%s", out)
	}
	// v6 failed to deploy: the server would accept it (it kept its archive), and
	// it is not a release to go back to.
	if strings.Contains(out, "Roll back to v6") || strings.Contains(out, "Roll back to v8") {
		t.Errorf("the suggestion names a release that failed:\n%s", out)
	}
	if got := strings.Join(rowVersions(out), " "); got != "v8 v6 v5" {
		t.Errorf("the table lists %q, want v8 v6 v5 (the run v7 is not a deployment):\n%s", got, out)
	}
}

func TestNothingIsSuggestedWhenNoReleaseWentLive(t *testing.T) {
	out := deploymentsOutput(t, noGoodReleaseBody)
	if !strings.Contains(out, "Latest deployment failed") {
		t.Fatalf("the failed release must still be called out:\n%s", out)
	}
	if strings.Contains(out, "Roll back to") {
		t.Errorf("a rollback is suggested with no good release to go to:\n%s", out)
	}
}

func TestDeploymentsJSONHoldsReleasesOnly(t *testing.T) {
	jsonRun(t, true)

	var rows []map[string]any
	if err := json.Unmarshal([]byte(deploymentsOutput(t, afterAFailedRunBody)), &rows); err != nil {
		t.Fatalf("afy deployments --json printed no JSON: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want the 3 releases, got %d rows: %v", len(rows), rows)
	}
	for _, row := range rows {
		if row["is_ephemeral"] == true {
			t.Errorf("a run is in the deployment history: %v", row)
		}
	}
}
