package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// These drive the connect flow itself rather than the API client, so they live
// in package cmd: the wait loop and the baseline read are unexported, and the
// properties worth pinning are about WHICH requests the flow makes — that the
// begin endpoint is hit exactly once, and that an already-connected account
// never reaches it at all. Client-level shape lives in test/github_test.go.

// connectServer answers the two endpoints connect uses and counts the hits.
// statuses is consumed one per /auth/github/status call; the last entry
// repeats once the slice runs out.
type connectServer struct {
	mu           sync.Mutex
	statusHits   int
	beginHits    int
	statusTimes  []time.Time
	statuses     []interface{}
	expiresAt    time.Time
	rateLimitOn  int // 1-based status call that answers 429; 0 = never
	retrySeconds int
}

func (c *connectServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/auth/github/status":
			c.statusHits++
			c.statusTimes = append(c.statusTimes, time.Now())
			if c.rateLimitOn != 0 && c.statusHits == c.rateLimitOn {
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"detail": map[string]interface{}{
						"code":                "RATE_LIMIT_EXCEEDED",
						"message":             "Rate limit exceeded.",
						"retry_after_seconds": c.retrySeconds,
					},
				})
				return
			}
			idx := c.statusHits - 1
			if idx >= len(c.statuses) {
				idx = len(c.statuses) - 1
			}
			_ = json.NewEncoder(w).Encode(c.statuses[idx])

		case "/auth/github":
			c.beginHits++
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"install_url": "https://github.com/apps/aetherfy-bot/installations/new?state=abc",
				"expires_at":  c.expiresAt.Format(time.RFC3339Nano),
			})

		default:
			http.NotFound(w, r)
		}
	}
}

func (c *connectServer) counts() (status, begin int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusHits, c.beginHits
}

func notConnected() map[string]interface{} {
	return map[string]interface{}{"connected": false}
}

func connected() map[string]interface{} {
	return map[string]interface{}{
		"connected":       true,
		"installation_id": 4242,
		"connected_at":    "2026-09-04T10:00:00Z",
		"manage_url":      "https://github.com/apps/aetherfy-bot/installations/new",
	}
}

// stubBrowser replaces the real opener for the duration of a test so a run
// does not put a window on the developer's screen.
func stubBrowser(t *testing.T) *int {
	t.Helper()
	opened := 0
	prev := githubOpenBrowser
	githubOpenBrowser = func(string) error { opened++; return nil }
	t.Cleanup(func() { githubOpenBrowser = prev })
	return &opened
}

// b1: the ordinary case. Not connected for a few ticks, then connected.
func TestConnectWaitsThenReportsTheConnection(t *testing.T) {
	cs := &connectServer{
		statuses: []interface{}{notConnected(), notConnected(), notConnected(), connected()},
		// Generous against a 1ms tick, but short enough that a flow which
		// never recognises the connection fails in seconds instead of
		// leaning on the package timeout.
		expiresAt: time.Now().Add(5 * time.Second),
	}
	srv := httptest.NewServer(cs.handler(t))
	defer srv.Close()
	stubBrowser(t)

	code := runGitHubConnect(api.NewClientWithURL(srv.URL, "afy_test_key"), time.Millisecond)

	if code != githubConnectOK {
		t.Fatalf("exit code: want %d, got %d", githubConnectOK, code)
	}
	statusHits, beginHits := cs.counts()
	if beginHits != 1 {
		t.Errorf("begin endpoint hits: want exactly 1, got %d", beginHits)
	}
	// One baseline read plus the polls that found it connected.
	if statusHits < 2 {
		t.Errorf("status hits: want a baseline plus at least one poll, got %d", statusHits)
	}
}

// b2: an already-connected account is not re-run through GitHub.
func TestConnectOnAnAlreadyConnectedAccountNeverBegins(t *testing.T) {
	cs := &connectServer{
		statuses:  []interface{}{connected()},
		expiresAt: time.Now().Add(30 * time.Second),
	}
	srv := httptest.NewServer(cs.handler(t))
	defer srv.Close()
	opened := stubBrowser(t)

	code := runGitHubConnect(api.NewClientWithURL(srv.URL, "afy_test_key"), time.Millisecond)

	if code != githubConnectOK {
		t.Fatalf("exit code: want %d, got %d", githubConnectOK, code)
	}
	statusHits, beginHits := cs.counts()
	if beginHits != 0 {
		t.Errorf("/auth/github was requested %d time(s); an already-connected "+
			"account must not be sent back through GitHub", beginHits)
	}
	if statusHits != 1 {
		t.Errorf("status hits: want exactly the baseline read, got %d", statusHits)
	}
	if *opened != 0 {
		t.Errorf("a browser was opened %d time(s) for an account that is already connected", *opened)
	}
}

// b3: a 429 mid-poll is honoured, measured rather than assumed.
func TestConnectHonoursRetryAfterOnA429(t *testing.T) {
	const retrySeconds = 1
	cs := &connectServer{
		// call 1 = baseline (not connected), call 2 = 429, call 3 = connected
		statuses:     []interface{}{notConnected(), notConnected(), connected()},
		expiresAt:    time.Now().Add(30 * time.Second),
		rateLimitOn:  2,
		retrySeconds: retrySeconds,
	}
	srv := httptest.NewServer(cs.handler(t))
	defer srv.Close()
	stubBrowser(t)

	code := runGitHubConnect(api.NewClientWithURL(srv.URL, "afy_test_key"), time.Millisecond)
	if code != githubConnectOK {
		t.Fatalf("exit code: want %d, got %d", githubConnectOK, code)
	}

	cs.mu.Lock()
	times := append([]time.Time(nil), cs.statusTimes...)
	cs.mu.Unlock()

	if len(times) < 3 {
		t.Fatalf("want at least 3 status calls (baseline, 429, success), got %d", len(times))
	}
	// The tick is a millisecond, so without honouring the body's delay the
	// next call lands almost immediately. Measure the gap, do not assume it.
	gap := times[2].Sub(times[1])
	if gap < retrySeconds*time.Second {
		t.Errorf("the call after the 429 came %v later; retry_after_seconds=%d was ignored",
			gap, retrySeconds)
	}
}

// b4: a link that is already dead ends in the expiry message, not a hang and
// not the word "failed".
func TestConnectStopsWhenTheLinkHasAlreadyExpired(t *testing.T) {
	cs := &connectServer{
		statuses:  []interface{}{notConnected()},
		expiresAt: time.Now().Add(-time.Minute),
	}
	srv := httptest.NewServer(cs.handler(t))
	defer srv.Close()
	stubBrowser(t)

	done := make(chan int, 1)
	go func() {
		done <- runGitHubConnect(api.NewClientWithURL(srv.URL, "afy_test_key"), time.Millisecond)
	}()

	select {
	case code := <-done:
		if code != githubConnectFailed {
			t.Fatalf("exit code: want %d, got %d", githubConnectFailed, code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("connect did not stop at an expiry that had already passed")
	}

	statusHits, beginHits := cs.counts()
	if beginHits != 1 {
		t.Errorf("begin endpoint hits: want exactly 1, got %d", beginHits)
	}
	// The baseline read, and at most one poll before the deadline fires.
	if statusHits > 2 {
		t.Errorf("status polled %d times against an already-dead link", statusHits)
	}
}

// Ctrl-C leaves the link alive, and says so rather than implying the attempt
// is lost.
func TestWaitReportsInterruptionSeparatelyFromExpiry(t *testing.T) {
	cs := &connectServer{
		statuses:  []interface{}{notConnected()},
		expiresAt: time.Now().Add(time.Hour),
	}
	srv := httptest.NewServer(cs.handler(t))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	code := waitForGitHubConnection(ctx, api.NewClientWithURL(srv.URL, "afy_test_key"),
		cs.expiresAt, time.Millisecond)

	if code != githubConnectInterrupt {
		t.Errorf("exit code: want %d (interrupted), got %d", githubConnectInterrupt, code)
	}
}

// The deadline comes from the server's expires_at. A wait that outlived it
// would be waiting on a token the callback has already stopped accepting.
func TestWaitIsBoundedByTheServersExpiry(t *testing.T) {
	cs := &connectServer{
		statuses:  []interface{}{notConnected()},
		expiresAt: time.Now().Add(120 * time.Millisecond),
	}
	srv := httptest.NewServer(cs.handler(t))
	defer srv.Close()

	// The wait is bounded in its own goroutine. A regression here is a wait
	// that does NOT end, so a test that simply called it would hang until the
	// whole package timed out and report nothing useful about which property
	// broke. Failing fast at 3s is the assertion.
	done := make(chan int, 1)
	go func() {
		done <- waitForGitHubConnection(context.Background(),
			api.NewClientWithURL(srv.URL, "afy_test_key"), cs.expiresAt, time.Millisecond)
	}()

	select {
	case code := <-done:
		if code != githubConnectFailed {
			t.Errorf("exit code: want %d (expired), got %d", githubConnectFailed, code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait ran well past a 120ms expiry; it is not bound by expires_at")
	}
}
