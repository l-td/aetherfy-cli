package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// codeServer answers the first `failures` requests with `status`/`code`, then
// 202. Counts every request it received.
func codeServer(t *testing.T, failures int32, status int, code string) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		if n <= failures {
			w.WriteHeader(status)
			fmt.Fprintf(w, `{"detail":{"code":%q,"message":"busy, retry in a few seconds"}}`, code)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"status":"paused"}`)
	}))

	return srv, &hits
}

// Shrink the 2s/4s/8s/16s backoff so the suite stays fast. The policy under
// test is WHICH responses retry, not the exact sleep.
func withFastBackoff(t *testing.T) {
	t.Helper()
	orig := lifecycleRetryBaseWait
	lifecycleRetryBaseWait = time.Millisecond
	t.Cleanup(func() { lifecycleRetryBaseWait = orig })
}

func TestLifecycleRetriesContendedCodes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
	}{
		{"409 agent-row held", http.StatusConflict, "AGENT_OPERATION_IN_PROGRESS"},
		{"503 request-path lock bound", http.StatusServiceUnavailable, "RESOURCE_BUSY"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withFastBackoff(t)
			// Busy twice, then succeed.
			srv, hits := codeServer(t, 2, tc.status, tc.code)
			defer srv.Close()

			c := NewClientWithURL(srv.URL, "afy_test_key")
			if err := c.StopAgent("some-agent"); err != nil {
				t.Fatalf("stop should have succeeded after retrying: %v", err)
			}
			if got := atomic.LoadInt32(hits); got != 3 {
				t.Errorf("server saw %d request(s); want 3 (2 contended + 1 success)", got)
			}
		})
	}
}

func TestLifecycleRetryIsBounded(t *testing.T) {
	withFastBackoff(t)
	// Never clears.
	srv, hits := codeServer(t, 1<<30, http.StatusConflict, "AGENT_OPERATION_IN_PROGRESS")
	defer srv.Close()

	c := NewClientWithURL(srv.URL, "afy_test_key")
	err := c.StopAgent("some-agent")
	if err == nil {
		t.Fatal("expected the contended error to surface once attempts are exhausted")
	}
	if got := atomic.LoadInt32(hits); got != int32(lifecycleRetryAttempts) {
		t.Errorf("server saw %d request(s); want exactly %d", got, lifecycleRetryAttempts)
	}
}

// The whole safety argument is that we retry ONLY codes meaning "never ran".
// A state answer must fail on the first response — retrying AGENT_ALREADY_PAUSED
// would spin for 30s before reporting something already true.
func TestLifecycleDoesNotRetryStateAnswers(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
	}{
		{"already paused", http.StatusConflict, "AGENT_ALREADY_PAUSED"},
		{"pending deployments", http.StatusConflict, "AGENT_HAS_PENDING_DEPLOYMENTS"},
		{"already archived", http.StatusConflict, "AGENT_ALREADY_ARCHIVED"},
		{"not found", http.StatusNotFound, "AGENT_NOT_FOUND"},
		{"plan limit", http.StatusForbidden, "PLAN_LIMIT_EXCEEDED"},
		{"other 503", http.StatusServiceUnavailable, "AGENT_PAUSE_FAILED"},
		{"server error", http.StatusInternalServerError, "INTERNAL_ERROR"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withFastBackoff(t)
			srv, hits := codeServer(t, 1<<30, tc.status, tc.code)
			defer srv.Close()

			c := NewClientWithURL(srv.URL, "afy_test_key")
			if err := c.StopAgent("some-agent"); err == nil {
				t.Fatal("expected an error")
			}
			if got := atomic.LoadInt32(hits); got != 1 {
				t.Errorf("server saw %d request(s); want exactly 1 — %s is a state "+
					"answer and must fail fast", got, tc.code)
			}
		})
	}
}

// All four lifecycle verbs go through the retry, not just stop.
func TestEveryLifecycleVerbRetries(t *testing.T) {
	verbs := map[string]func(*Client) error{
		"stop":    func(c *Client) error { return c.StopAgent("a") },
		"start":   func(c *Client) error { return c.StartAgent("a") },
		"archive": func(c *Client) error { return c.ArchiveAgent("a") },
		"restore": func(c *Client) error { return c.RestoreAgent("a") },
	}

	for name, call := range verbs {
		t.Run(name, func(t *testing.T) {
			withFastBackoff(t)
			srv, hits := codeServer(t, 1, http.StatusConflict, "AGENT_OPERATION_IN_PROGRESS")
			defer srv.Close()

			c := NewClientWithURL(srv.URL, "afy_test_key")
			if err := call(c); err != nil {
				t.Fatalf("%s should have succeeded after one retry: %v", name, err)
			}
			if got := atomic.LoadInt32(hits); got != 2 {
				t.Errorf("%s: server saw %d request(s); want 2", name, got)
			}
		})
	}
}
