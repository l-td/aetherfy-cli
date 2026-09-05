package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

// A begin response with no expires_at is a MALFORMED answer, not an expired
// link. Decoded, it is the zero time; used as a deadline it is already long
// past, so the wait would end on its first turn and announce that the link had
// expired — on a link that is perfectly good and a browser that is already on
// GitHub. The command must not poll and must not claim an expiry that did not
// happen.
func TestConnectRefusesAResponseWithNoExpiry(t *testing.T) {
	statusHits := 0
	beginHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/github/status":
			statusHits++
			_ = json.NewEncoder(w).Encode(notConnected())
		case "/auth/github":
			beginHits++
			// No expires_at, deliberately.
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"install_url": "https://github.com/apps/aetherfy-bot/installations/new?state=abc",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	stubBrowser(t)

	// THE MESSAGE IS THE ASSERTION, not the exit code. Without the guard this
	// flow ALSO exits 1 and ALSO polls exactly once — the zero deadline fires
	// on the first turn of the select — so an exit-code check passes whether
	// the guard is there or not. What separates them is what the user is told:
	// a true statement about a malformed response, or a false one about an
	// expiry that never happened.
	var code int
	stderr := captureStderr(t, func() {
		done := make(chan int, 1)
		go func() {
			done <- runGitHubConnect(api.NewClientWithURL(srv.URL, "afy_test_key"), time.Millisecond)
		}()
		select {
		case code = <-done:
		case <-time.After(5 * time.Second):
			t.Error("connect neither refused nor returned on a response with no expiry")
		}
	})

	if code != githubConnectFailed {
		t.Fatalf("exit code: want %d, got %d", githubConnectFailed, code)
	}
	if !strings.Contains(stderr, "did not say when this installation link expires") {
		t.Errorf("the user was not told the response was malformed; stderr was: %q", stderr)
	}
	if strings.Contains(stderr, "expired") && !strings.Contains(stderr, "did not say when") {
		t.Errorf("the user was told the link expired, which is false; stderr was: %q", stderr)
	}
	if strings.Contains(stderr, "has expired without a connection being recorded") {
		t.Errorf("the expiry message fired on a link that never had an expiry; stderr was: %q", stderr)
	}

	if beginHits != 1 {
		t.Errorf("begin endpoint hits: want exactly 1, got %d", beginHits)
	}
	// Exactly the baseline read. Polling against a deadline we do not have
	// would be waiting for something with no end.
	if statusHits != 1 {
		t.Errorf("status was requested %d time(s); with no expiry there is "+
			"nothing to poll until, so only the baseline read may happen", statusHits)
	}
}

// captureStderr swaps os.Stderr for a pipe while fn runs. output.PrintError
// resolves os.Stderr at call time, so this sees what the user would.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prev := os.Stderr
	os.Stderr = w

	collected := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		collected <- buf.String()
	}()

	fn()

	os.Stderr = prev
	_ = w.Close()
	out := <-collected
	_ = r.Close()
	return out
}

// `status` prints where to change repository access. The CLI cannot build that
// URL — it does not know the App's name — so it either shows the server's or
// shows nothing, and showing nothing leaves a connected user with no route to
// the one thing they are most likely to want next.
func TestStatusShowsWhereToManageRepositoryAccess(t *testing.T) {
	const manageURL = "https://github.com/apps/aetherfy-bot/installations/new"

	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	body = connected()
	status, err := client.GitHubStatus()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.ManageURL != manageURL {
		t.Errorf("manage_url: want %s, got %q", manageURL, status.ManageURL)
	}

	// Absent on a server with no GitHub App configured. Empty, so the caller
	// can tell "nowhere to send them" from a URL and print neither a blank
	// line nor a broken link.
	body = map[string]interface{}{"connected": true, "installation_id": 4242}
	status, err = client.GitHubStatus()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.ManageURL != "" {
		t.Errorf("manage_url: want empty, got %q", status.ManageURL)
	}
}

// The command gates the line on a non-empty value rather than printing it
// unconditionally, so an unconfigured server produces no dangling sentence.
func TestStatusGatesTheManageLineOnAValue(t *testing.T) {
	src, err := os.ReadFile("github.go")
	if err != nil {
		t.Fatalf("cannot read github.go: %v", err)
	}
	if !strings.Contains(string(src), `if status.ManageURL != "" {`) {
		t.Error("status must print the manage line only when the server sent one")
	}
}
