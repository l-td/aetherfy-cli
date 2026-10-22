package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// THE PLAN'S LOG BOUNDS, END TO END (the shipped command against a stand-in
// control plane). The control plane never returns a log older than the plan's
// retention, and an account over its stored-log size has its oldest lines
// removed first. A `--since` that reaches past the retention is NOT an error: the
// kept logs are shown, a sentence on stderr names the plan's retention, and
// `--json` carries the retention. Every failure of `afy logs` goes through the
// CLI's one error path: a non-zero exit, the human line (or the --json error
// document with its code) on stderr, nothing on stdout.

const oneLog = `[{"id":7,"timestamp":"2026-10-20T10:00:00Z","stream":"stdout","level":"INFO","message":"kept"}]`

func TestASinceBeyondTheRetentionShowsWhatIsKeptAndNamesTheRetentionOnStderr(t *testing.T) {
	cp := newFakeCP(t)
	cp.onWithHeaders("GET", "/agents/bot/logs", 200, oneLog,
		map[string]string{"X-Log-Retention-Days": "3", "X-Log-Since-Clamped": "true"})

	stdout, stderr, err := runAfy(t, cp, "logs", "bot", "--since", "30d")
	if err != nil {
		t.Fatalf("a --since past the retention is not an error: %v", err)
	}
	if !strings.Contains(stdout, "kept") {
		t.Errorf("the kept logs are shown on stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "--since 30d reaches past your plan's log retention (3 days)") {
		t.Errorf("stderr must name the plan's retention, got %q", stderr)
	}
	if strings.Contains(stdout, "retention") {
		t.Errorf("the note is not part of the result on stdout: %q", stdout)
	}
}

func TestASinceInsideTheRetentionSaysNothing(t *testing.T) {
	cp := newFakeCP(t)
	cp.onWithHeaders("GET", "/agents/bot/logs", 200, oneLog, map[string]string{"X-Log-Retention-Days": "3"})

	_, stderr, err := runAfy(t, cp, "logs", "bot", "--since", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "retention") {
		t.Errorf("no note when the window was not cut, got %q", stderr)
	}
}

func TestWhenTheSizeCapRolledLinesOutTheReadSaysWhereTheLogsNowStart(t *testing.T) {
	cp := newFakeCP(t)
	cp.onWithHeaders("GET", "/agents/bot/logs", 200, oneLog, map[string]string{
		"X-Log-Retention-Days":         "14",
		"X-Log-Max-Bytes-Stored":       "262144000",
		"X-Log-Cap-Rolled-Out-Through": "2026-10-20T10:00:00+00:00",
	})

	stdout, stderr, err := runAfy(t, cp, "logs", "bot")
	if err != nil {
		t.Fatalf("a size-cap rollout is not an error: %v", err)
	}
	want := "older lines were removed because your account is over its stored-log size (250.0 MB on your plan); nothing before 2026-10-20T10:00:00Z is left."
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr must say where the logs now start, want %q in %q", want, stderr)
	}
	if !strings.Contains(stdout, "kept") || strings.Contains(stdout, "older lines") {
		t.Errorf("the lines stay on stdout and the note stays off it: %q", stdout)
	}
	// without the header, no note
	cp.onWithHeaders("GET", "/agents/bot/logs", 200, oneLog, map[string]string{"X-Log-Retention-Days": "14"})
	_, stderr, _ = runAfy(t, cp, "logs", "bot")
	if strings.Contains(stderr, "older lines") {
		t.Errorf("no rollout, no note: %q", stderr)
	}
}

func TestJSONIsTheLogsRetentionAndSinceClampedAndKeepsStdoutOnlyJSON(t *testing.T) {
	cp := newFakeCP(t)
	cp.onWithHeaders("GET", "/agents/bot/logs", 200, oneLog,
		map[string]string{"X-Log-Retention-Days": "14", "X-Log-Since-Clamped": "true"})

	stdout, stderr, err := runAfy(t, cp, "logs", "bot", "--since", "90d", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	if len(keys) != 3 || doc["logs"] == nil || doc["retention_days"] == nil || doc["since_clamped"] == nil {
		t.Errorf("the document is exactly {logs, retention_days, since_clamped}, got keys %v", keys)
	}
	if string(doc["retention_days"]) != "14" || string(doc["since_clamped"]) != "true" {
		t.Errorf("document = %s", stdout)
	}
	if !strings.Contains(stderr, "plan's log retention (14 days)") {
		t.Errorf("the note is on stderr in JSON mode too, got %q", stderr)
	}
}

func TestJSONRetentionIsNullWhereThePlanNegotiatesIt(t *testing.T) {
	cp := newFakeCP(t)
	cp.onWithHeaders("GET", "/agents/bot/logs", 200, `[]`, map[string]string{"X-Log-Retention-Days": "negotiated"})

	stdout, _, err := runAfy(t, cp, "logs", "bot", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"retention_days": null`) || !strings.Contains(stdout, `"logs": []`) {
		t.Errorf("negotiated is null and an empty read is [], got %s", stdout)
	}
}

// ---------------------------------------------------- the one error path

type logsFailure struct {
	name    string
	args    []string
	answer  func(cp *fakeCP)
	code    string // the --json error code
	message string // what the text error contains
}

func logsFailures() []logsFailure {
	refuse := func(cp *fakeCP) {
		cp.on("GET", "/agents/bot/logs", 404, `{"detail":{"code":"AGENT_NOT_FOUND","message":"Agent 'bot' not found"}}`)
	}
	unstated := func(cp *fakeCP) {
		cp.onWithHeaders("GET", "/agents/bot/logs", 200, `[]`, map[string]string{"X-Log-Retention-Days": "soon"})
	}
	return []logsFailure{
		{"a refused read", []string{"logs", "bot"}, refuse, "AGENT_NOT_FOUND", "Agent 'bot' not found"},
		{"an answer without the plan's retention", []string{"logs", "bot"}, unstated, "CLI_RESPONSE_INVALID", "did not state the plan's log retention"},
		{"a refused first read of --follow", []string{"logs", "bot", "--follow"}, refuse, "AGENT_NOT_FOUND", "Agent 'bot' not found"},
		{"--follow without the plan's retention", []string{"logs", "bot", "--follow"}, unstated, "CLI_RESPONSE_INVALID", "did not state the plan's log retention"},
	}
}

func TestEveryFailureOfLogsIsTheOneErrorPathNonZeroNothingOnStdout(t *testing.T) {
	for _, f := range logsFailures() {
		f := f
		t.Run(f.name+"/text", func(t *testing.T) {
			cp := newFakeCP(t)
			f.answer(cp)
			stdout, stderr, code := runAfyProcess(t, cp, nil, f.args...)
			if code != 1 {
				t.Errorf("exit %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("a failure wrote to stdout:\n%s", stdout)
			}
			if !strings.HasPrefix(stderr, "Error: ") || strings.Count(stderr, "Error:") != 1 || !strings.Contains(stderr, f.message) {
				t.Errorf("stderr must be the one `Error: ...` line containing %q, got %q", f.message, stderr)
			}
		})
		t.Run(f.name+"/json", func(t *testing.T) {
			cp := newFakeCP(t)
			f.answer(cp)
			stdout, stderr, code := runAfyProcess(t, cp, nil, append(append([]string{}, f.args...), "--json")...)
			if code != 1 {
				t.Errorf("exit %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("a failure wrote to stdout:\n%s", stdout)
			}
			e := jsonErrorOnStderr(t, strings.Join(f.args, " "), stderr)
			if e["code"] != f.code || !strings.Contains(e["message"].(string), f.message) {
				t.Errorf("error document = %v, want code %q and a message containing %q", e, f.code, f.message)
			}
		})
	}
}

func TestAFollowThatIsRefusedAfterItStartedEndsThroughTheSameErrorPath(t *testing.T) {
	cp := newFakeCP(t)
	// the seed read is answered, the first poll (after_id set) is refused
	cp.onWithHeaders("GET", "/agents/bot/logs", 200, oneLog, map[string]string{"X-Log-Retention-Days": "3"})
	f := &follower{client: api.NewClientWithURL(cp.srv.URL+"/api/v1", "afy_test_key"), agentID: "bot", afterID: 7}
	cp.on("GET", "/agents/bot/logs", 403, `{"detail":{"code":"ACCOUNT_SUSPENDED","message":"suspended"}}`)

	if err := f.tick(); err == nil {
		t.Fatal("a refused poll must end the follow")
	}
}

func TestATransientFollowFailureIsSaidOncePerOutageAndFollowingGoesOn(t *testing.T) {
	cp := newFakeCP(t)
	f := &follower{client: api.NewClientWithURL(cp.srv.URL+"/api/v1", "afy_test_key"), agentID: "bot"}
	t.Setenv("AETHERFY_CONFIG_DIR", t.TempDir())
	say := func() string {
		return captureStdout(t, func() {
			if err := f.tick(); err != nil {
				t.Fatalf("a transient failure must not end the follow: %v", err)
			}
		})
	}

	cp.on("GET", "/agents/bot/logs", 503, `{"detail":{"code":"SERVICE_UNAVAILABLE","message":"try again"}}`)
	first := say()
	second := say()
	if !strings.Contains(first, "Failed to fetch logs") || second != "" {
		t.Errorf("the outage is said once: first %q, second %q", first, second)
	}

	cp.onWithHeaders("GET", "/agents/bot/logs", 200, oneLog, map[string]string{"X-Log-Retention-Days": "3"})
	if out := say(); !strings.Contains(out, "kept") {
		t.Errorf("following resumes and prints what arrived: %q", out)
	}
	cp.on("GET", "/agents/bot/logs", 503, `{"detail":{"code":"SERVICE_UNAVAILABLE","message":"try again"}}`)
	if again := say(); !strings.Contains(again, "Failed to fetch logs") {
		t.Errorf("a second outage is said again: %q", again)
	}
}

func TestFollowRefusedClassifiesWhatRetryingCannotFix(t *testing.T) {
	apiErr := func(status int) error { return &api.APIError{StatusCode: status, Message: "x", Code: "X"} }
	for status, want := range map[int]bool{400: true, 401: true, 403: true, 404: true, 429: false, 500: false, 503: false} {
		if got := followRefused(apiErr(status)); got != want {
			t.Errorf("status %d: followRefused = %v, want %v", status, got, want)
		}
	}
	if !followRefused(&api.ResponseInvalidError{Message: "x"}) {
		t.Error("an answer the CLI cannot use is never retried into working")
	}
	if followRefused(os.ErrDeadlineExceeded) {
		t.Error("a transport failure is retried")
	}
}

var handBuiltError = regexp.MustCompile(`(?m)fmt\.Errorf\(|fmt\.Fprint\w*\(os\.Std(err|out)|PrintWarning\("Failed|"Error: `)

func TestLogsGoCarriesNoHandBuiltErrorText(t *testing.T) {
	// Every failure goes through printRefusal / printUnprinted (the one error path);
	// the only warning is the transient-outage line (follower.warnOnce), built from a
	// format and not from error text, and that is not an error.
	raw, err := os.ReadFile("logs.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if handBuiltError.MatchString(line) {
			t.Errorf("cmd/logs.go builds error text by hand: %s", strings.TrimSpace(line))
		}
	}
	// control: the checker sees what it is meant to catch
	if !handBuiltError.MatchString(`return fmt.Errorf("x")`) || !handBuiltError.MatchString(`fmt.Fprintln(os.Stderr, "Error: x")`) {
		t.Fatal("the checker misses a hand-built error")
	}
}

// THE FIRST READ OF --follow is a read like every poll after it: a refusal ends the
// command, a 5xx or a network failure is said once and tried again.

func shortFollowInterval(t *testing.T) {
	t.Helper()
	old := followInterval
	followInterval = 10 * time.Millisecond
	t.Cleanup(func() { followInterval = old })
}

func TestTheFirstReadOfAFollowRetriesAnOutageAndStartsWhenTheControlPlaneAnswers(t *testing.T) {
	shortFollowInterval(t)
	cp := newFakeCP(t)
	t.Setenv("AETHERFY_CONFIG_DIR", t.TempDir())
	f := &follower{client: api.NewClientWithURL(cp.srv.URL+"/api/v1", "afy_test_key"), agentID: "bot"}
	cp.on("GET", "/agents/bot/logs", 503, `{"detail":{"code":"SERVICE_UNAVAILABLE","message":"deploying"}}`)

	done := make(chan error, 1)
	said := captureStdout(t, func() {
		go func() { done <- f.seed() }()
		deadline := time.Now().Add(5 * time.Second)
		for len(cp.called("GET", "/agents/bot/logs")) < 3 {
			if time.Now().After(deadline) {
				t.Fatal("the first read was not retried")
			}
			time.Sleep(5 * time.Millisecond)
		}
		select {
		case err := <-done:
			t.Fatalf("an outage must not end the follow: %v", err)
		default:
		}
		cp.onWithHeaders("GET", "/agents/bot/logs", 200, oneLog, map[string]string{"X-Log-Retention-Days": "3"})
		if err := <-done; err != nil {
			t.Fatalf("seed after the outage: %v", err)
		}
	})
	if f.afterID != 7 {
		t.Errorf("the cursor is the newest line (7), got %d", f.afterID)
	}
	if strings.Count(said, "Failed to start following logs") != 1 {
		t.Errorf("the outage is said exactly once, got %q", said)
	}
}

func TestTheFirstReadOfAFollowEndsOnARefusalWithoutRetrying(t *testing.T) {
	shortFollowInterval(t)
	for name, answer := range map[string][2]interface{}{
		"404": {404, `{"detail":{"code":"AGENT_NOT_FOUND","message":"no such agent"}}`},
		"403": {403, `{"detail":{"code":"ACCOUNT_SUSPENDED","message":"suspended"}}`},
	} {
		cp := newFakeCP(t)
		cp.on("GET", "/agents/bot/logs", answer[0].(int), answer[1].(string))
		f := &follower{client: api.NewClientWithURL(cp.srv.URL+"/api/v1", "afy_test_key"), agentID: "bot"}
		mustEndWithin(t, name, cp, f)
		if n := len(cp.called("GET", "/agents/bot/logs")); n != 1 {
			t.Errorf("%s: a refusal is not retried, the control plane saw %d reads", name, n)
		}
	}
}

// mustEndWithin runs f.seed() in a goroutine and fails BY ASSERTION, not by the test
// binary's timeout, when it does not end with an error: a seed that retries a refusal
// never ends. The reads it made are in the message.
func mustEndWithin(t *testing.T, name string, cp *fakeCP, f *follower) {
	t.Helper()
	done := make(chan error, 1)
	cancel := make(chan struct{})
	f.cancel = cancel
	go func() { done <- f.seed() }()
	select {
	case err := <-done:
		if err == nil {
			t.Errorf("%s: the refused first read must end the follow with an error", name)
		}
	case <-time.After(2 * time.Second):
		close(cancel) // stop the goroutine before failing
		t.Fatalf("%s: a refusal was retried: %d reads and the first read had not ended", name, len(cp.called("GET", "/agents/bot/logs")))
	}
}

func TestTheFirstReadOfAFollowRetriesANetworkErrorAndStartsWhenItAnswers(t *testing.T) {
	shortFollowInterval(t)
	t.Setenv("AETHERFY_CONFIG_DIR", t.TempDir())
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n <= 4 {
			// a network failure: the connection is closed with no answer (the client itself retries a
			// call twice, so four failures outlast one whole first read)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		w.Header().Set("X-Log-Retention-Days", "3")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(oneLog))
	}))
	t.Cleanup(srv.Close)
	f := &follower{client: api.NewClientWithURL(srv.URL+"/api/v1", "afy_test_key"), agentID: "bot"}

	var said string
	var err error
	said = captureStdout(t, func() {
		done := make(chan error, 1)
		cancel := make(chan struct{})
		f.cancel = cancel
		go func() { done <- f.seed() }()
		select {
		case err = <-done:
		case <-time.After(10 * time.Second):
			close(cancel)
			mu.Lock()
			n := calls
			mu.Unlock()
			t.Fatalf("the first read was never retried to success: %d reads", n)
		}
	})
	if err != nil {
		t.Fatalf("a network failure on the first read must be retried, not end the follow: %v", err)
	}
	if f.afterID != 7 {
		t.Errorf("the cursor is the newest line (7), got %d", f.afterID)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls < 5 {
		t.Errorf("expected at least 5 reads (4 failures, then an answer), got %d", calls)
	}
	if strings.Count(said, "Failed to start following logs") != 1 {
		t.Errorf("the outage is said exactly once, got %q", said)
	}
}

func TestTheFirstReadOfAFollowEndsOnAnAnswerTheCLICannotUse(t *testing.T) {
	shortFollowInterval(t)
	calls := 0
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		// 200 without X-Log-Retention-Days: the control plane's contract is broken, and
		// retrying it would give the same answer forever
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	f := &follower{client: api.NewClientWithURL(srv.URL+"/api/v1", "afy_test_key"), agentID: "bot"}

	done := make(chan error, 1)
	cancel := make(chan struct{})
	f.cancel = cancel
	go func() { done <- f.seed() }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("an invalid answer must end the follow with an error")
		}
	case <-time.After(2 * time.Second):
		close(cancel)
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("an invalid answer was retried: %d reads and the first read had not ended", calls)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("an invalid answer is not retried, the server saw %d reads", calls)
	}
}
