package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// `afy start` WHILE THE PREVIOUS STOP IS STILL SETTLING.
//
// A stop gives the agent's server its shutdown grace, and a resume sent
// meanwhile is answered 409 AGENT_STILL_STOPPING, with how long to wait
// (Retry-After, carried in the body as retry_after_seconds) and the longest a
// stop can take (stop_bound_seconds). The CLI waits as told, retries, says so
// once, and gives up past the bound. The server here is a stand-in that
// answers exactly that envelope, then 202; the waits are recorded, not spent.

// stillStoppingServer answers the first len(retryAfters) starts 409
// AGENT_STILL_STOPPING, each with its own Retry-After, then 202 -- or forever
// 409 when forever is set.
func stillStoppingServer(t *testing.T, retryAfters []int, bound int, forever bool) (*httptest.Server, *int) {
	t.Helper()
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/agents/api/start") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		mu.Lock()
		n := hits
		hits++
		mu.Unlock()
		if forever || n < len(retryAfters) {
			after := retryAfters[len(retryAfters)-1]
			if n < len(retryAfters) {
				after = retryAfters[n]
			}
			w.Header().Set("Retry-After", fmt.Sprint(after))
			w.WriteHeader(http.StatusConflict)
			_, _ = fmt.Fprintf(w, `{"detail":{"code":%q,"message":"Agent 'api' is still finishing its previous stop.","agent":"api","retry_after_seconds":%d,"stop_bound_seconds":%d}}`,
				codeAgentStillStopping, after, bound)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"running","agent_id":"a","readiness":"serving"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// recordWaits replaces the wait between attempts with a recorder.
func recordWaits(t *testing.T) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	old := stillStoppingSleep
	stillStoppingSleep = func(d time.Duration) { waits = append(waits, d) }
	t.Cleanup(func() { stillStoppingSleep = old })
	return &waits
}

func TestStartWaitsOutAStopStillSettlingAsTheServerSays(t *testing.T) {
	// Two DIFFERENT waits, so a CLI that waited a constant of its own instead
	// of the server's Retry-After cannot pass.
	srv, hits := stillStoppingServer(t, []int{2, 3}, 150, false)
	waits := recordWaits(t)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	var err error
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() { err = startAgent(client, "api") })
	})
	out := strings.ToLower(stdout + stderr)

	if err != nil {
		t.Fatalf("a start that succeeded on its third attempt returned an error: %v\n%s", err, out)
	}
	if *hits != 3 {
		t.Fatalf("the start was sent %d time(s), not 3 (two 409s, then the 202)", *hits)
	}
	if want := []time.Duration{2 * time.Second, 3 * time.Second}; fmt.Sprint(*waits) != fmt.Sprint(want) {
		t.Fatalf("waited %v between attempts, not the server's Retry-After %v", *waits, want)
	}
	if n := strings.Count(out, "previous stop still finishing"); n != 1 {
		t.Fatalf("the progress line was printed %d time(s), not once:\n%s", n, out)
	}
	if !strings.Contains(out, "retrying in 2s") {
		t.Fatalf("the progress line does not say how long it waits:\n%s", out)
	}
	if !strings.Contains(out, "serving requests") {
		t.Fatalf("the eventual 202 was not reported as usual:\n%s", out)
	}
}

func TestStartGivesUpClearlyPastTheStopBound(t *testing.T) {
	// A stop that never settles: 5s waits against a 12s bound -- two waits
	// fit (10s), a third would pass the bound, so the third answer ends it.
	srv, hits := stillStoppingServer(t, []int{5}, 12, true)
	waits := recordWaits(t)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	var err error
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() { err = startAgent(client, "api") })
	})
	out := strings.ToLower(stdout + stderr)

	apiErr, ok := err.(*api.APIError)
	if !ok || apiErr.Code != codeAgentStillStopping {
		t.Fatalf("past the bound the start must fail with the server's answer, got %v", err)
	}
	if *hits != 3 || len(*waits) != 2 {
		t.Fatalf("%d attempt(s) and %d wait(s) against a 12s bound of 5s waits; want 3 and 2", *hits, len(*waits))
	}
	if !strings.Contains(out, "still finishing its previous stop after 10s") {
		t.Fatalf("the failure does not say why, or after how long:\n%s", out)
	}
}

func TestAnEnvelopeWithoutItsNumbersIsNotWaitedOn(t *testing.T) {
	// No retry_after_seconds / stop_bound_seconds: nothing to wait BY, so the
	// answer fails as it arrived instead of being retried on a guess.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprintf(w, `{"detail":{"code":%q,"message":"still stopping"}}`, codeAgentStillStopping)
	}))
	t.Cleanup(srv.Close)
	waits := recordWaits(t)

	var err error
	_ = captureStdout(t, func() {
		_ = captureStderr(t, func() { err = startAgent(api.NewClientWithURL(srv.URL, "afy_test_key"), "api") })
	})
	if err == nil || len(*waits) != 0 {
		t.Fatalf("an envelope without its numbers was waited on (%v) or succeeded (%v)", *waits, err)
	}
}
