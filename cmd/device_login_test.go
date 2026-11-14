package cmd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/config"
)

// THE DEVICE SIGN-IN, through the real root command against a stand-in control
// plane. Time is never waited for: the flow asks deviceWait to let an interval
// pass, and these tests record what it asked (an EVENT per wait) instead of
// sleeping. What each test asserts is the requests the flow made, the waits it
// asked for, and what it printed and stored.

const (
	deviceCodeSecret = "dc-secret-0123456789"
	approvedKey      = "afy_live_abcdefghijklmnopqrstuvwxyz012345"
	approvedKeyID    = "11111111-1111-1111-1111-111111111111"
	pageComplete     = "https://app.example.test/cli-login?code=ABCD-EFGH"
)

const deviceStartBody = `{"device_code":"` + deviceCodeSecret + `","user_code":"ABCD-EFGH",` +
	`"verification_uri":"https://app.example.test/cli-login","verification_uri_complete":"` + pageComplete + `",` +
	`"expires_in":600,"interval":5}`

const approvedKeyRow = `{"id":"` + approvedKeyID + `","name":"test-host","key_prefix":"afy_live_ab...",` +
	`"created_at":"2026-11-12T10:00:00Z","last_used_at":null,"is_system_managed":false}`

var (
	pendingAnswer  = cpAnswer{status: 202, body: `{"status":"pending","interval":5}`}
	approvedAnswer = cpAnswer{status: 200, body: `{"status":"approved","api_key":"` + approvedKey + `","key":` + approvedKeyRow + `}`}
)

func errAnswer(status int, code, message string, headers map[string]string) cpAnswer {
	return cpAnswer{status: status, headers: headers,
		body: `{"detail":{"code":"` + code + `","message":"` + message + `"}}`}
}

// deviceCP is a control plane that starts a device sign-in and, once the key is
// approved, knows the account behind it.
func deviceCP(t *testing.T, poll ...cpAnswer) *fakeCP {
	t.Helper()
	cp := newFakeCP(t)
	cp.on("POST", "/auth/device", 200, deviceStartBody)
	cp.onSequence("POST", "/auth/device/token", poll...)
	cp.on("GET", "/auth/me", 200, `{"user_id":"u-1","email":"dev@example.com","tier":"starter"}`)
	return cp
}

// waitLog records every wait the flow asked for.
type waitLog struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (w *waitLog) all() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Duration(nil), w.waits...)
}

// recordWaits replaces deviceWait with a recorder that lets no time pass. stopAt
// (1-based, 0 for never) makes that wait fail as an interrupted one does.
func recordWaits(t *testing.T, stopAt int) *waitLog {
	t.Helper()
	log := &waitLog{}
	old := deviceWait
	deviceWait = func(ctx context.Context, d time.Duration) error {
		log.mu.Lock()
		log.waits = append(log.waits, d)
		n := len(log.waits)
		log.mu.Unlock()
		if stopAt != 0 && n == stopAt {
			return context.Canceled
		}
		return ctx.Err()
	}
	t.Cleanup(func() { deviceWait = old })
	return log
}

// recordBrowser replaces deviceOpenBrowser with a recorder that answers err.
func recordBrowser(t *testing.T, err error) *[]string {
	t.Helper()
	var opened []string
	old := deviceOpenBrowser
	deviceOpenBrowser = func(url string) error { opened = append(opened, url); return err }
	t.Cleanup(func() { deviceOpenBrowser = old })
	return &opened
}

// pipeStdin makes the run a script whose stdin carries s.
func pipeStdin(t *testing.T, s string) {
	t.Helper()
	oldIn, oldTerm := loginStdin, stdinIsTerminal
	loginStdin, stdinIsTerminal = strings.NewReader(s), func() bool { return false }
	t.Cleanup(func() { loginStdin, stdinIsTerminal = oldIn, oldTerm })
}

func durations(secs ...int) []time.Duration {
	var out []time.Duration
	for _, s := range secs {
		out = append(out, time.Duration(s)*time.Second)
	}
	return out
}

func sameWaits(got, want []time.Duration) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func credentialsStored() bool {
	_, err := os.Stat(config.CredentialsPath())
	return err == nil
}

func TestLoginWithNoKeyApprovesACodeAndStoresTheNewKey(t *testing.T) {
	pipeStdin(t, "")
	waits := recordWaits(t, 0)
	opened := recordBrowser(t, nil)
	cp := deviceCP(t, pendingAnswer, pendingAnswer, approvedAnswer)

	stdout, stderr, err := runAfy(t, cp, "login")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, stdout, stderr)
	}

	// The two device calls carry no credentials, though a key is in the environment.
	start, polls := cp.called("POST", "/auth/device"), cp.called("POST", "/auth/device/token")
	if len(start) != 1 || len(polls) != 3 {
		t.Fatalf("want 1 start and 3 polls, got %d and %d: %+v", len(start), len(polls), cp.all())
	}
	for _, c := range append(append([]cpCall{}, start...), polls...) {
		if c.Authorization != "" {
			t.Errorf("%s %s carried credentials (%q): a key must never mint a key", c.Method, c.Path, c.Authorization)
		}
	}
	if start[0].Body["device_name"] != "test-host" {
		t.Errorf("the request names the machine %v, want its hostname", start[0].Body["device_name"])
	}
	if _, has := start[0].Body["key_name"]; has {
		t.Errorf("afy login names no key, but sent key_name: %v", start[0].Body)
	}
	for _, p := range polls {
		if p.Body["device_code"] != deviceCodeSecret {
			t.Errorf("a poll sent %v, want the device code", p.Body)
		}
	}
	if !sameWaits(waits.all(), durations(5, 5, 5)) {
		t.Errorf("waits = %v, want one interval before each of the three polls", waits.all())
	}

	// The person is told the code and the page, and the page is opened.
	for _, want := range []string{"ABCD-EFGH", pageComplete} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if len(*opened) != 1 || (*opened)[0] != pageComplete {
		t.Errorf("browser opened %v, want the page with the code in it", *opened)
	}
	if strings.Contains(stdout+stderr, deviceCodeSecret) {
		t.Errorf("the device code (a secret) was printed:\n%s%s", stdout, stderr)
	}
	if strings.Contains(stdout+stderr, approvedKey) {
		t.Errorf("login printed the key it stored:\n%s%s", stdout, stderr)
	}

	// The key is validated with itself, then stored where afy login stores a key.
	me := cp.called("GET", "/auth/me")
	if len(me) != 1 || me[0].Authorization != "Bearer "+approvedKey {
		t.Errorf("/auth/me calls = %+v, want one with the new key", me)
	}
	raw, err := os.ReadFile(config.CredentialsPath())
	if err != nil || !strings.Contains(string(raw), approvedKey) || !strings.Contains(string(raw), "dev@example.com") {
		t.Errorf("credentials file = %q (%v), want the new key and the account", raw, err)
	}
	if !strings.Contains(stdout, "Logged in successfully!") {
		t.Errorf("no success line:\n%s", stdout)
	}
}

func TestLoginNoBrowserDoesNotOpenOneAndAFailureToOpenIsNotAnError(t *testing.T) {
	pipeStdin(t, "")
	recordWaits(t, 0)

	opened := recordBrowser(t, nil)
	stdout, _, err := runAfy(t, deviceCP(t, approvedAnswer), "login", "--no-browser")
	if err != nil {
		t.Fatal(err)
	}
	if len(*opened) != 0 {
		t.Errorf("--no-browser opened %v", *opened)
	}
	if !strings.Contains(stdout, pageComplete) {
		t.Errorf("the page must still be printed:\n%s", stdout)
	}

	opened = recordBrowser(t, errors.New("no display"))
	stdout, _, err = runAfy(t, deviceCP(t, approvedAnswer), "login")
	if err != nil {
		t.Fatalf("a browser that will not open failed the login: %v", err)
	}
	if len(*opened) != 1 || !strings.Contains(stdout, "Open the page above in your browser") {
		t.Errorf("opened %v; stdout:\n%s", *opened, stdout)
	}
}

func TestLoginDeviceFlowUnderJSONPrintsTheLoginResultAndKeepsTheKeyOffStdout(t *testing.T) {
	pipeStdin(t, "")
	recordWaits(t, 0)
	recordBrowser(t, nil)
	cp := deviceCP(t, pendingAnswer, approvedAnswer)

	stdout, stderr, err := runAfy(t, cp, "login", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	doc, ok := oneJSONDocument(t, "login", stdout).(map[string]interface{})
	if !ok || doc["logged_in"] != true || doc["email"] != "dev@example.com" || doc["tier"] != "starter" || doc["key_type"] != "live" {
		t.Errorf("login --json = %v, want the result login prints today", doc)
	}
	if strings.Contains(stdout, approvedKey) {
		t.Errorf("the key is on stdout: %s", stdout)
	}
	// The code and the page are for the person: stderr.
	if !strings.Contains(stderr, "ABCD-EFGH") || strings.Contains(stdout, "ABCD-EFGH") {
		t.Errorf("the code must be on stderr only.\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

func TestLoginHonoursTheIntervalTheServerChangesAndTheRetryAfterItAsks(t *testing.T) {
	pipeStdin(t, "")
	recordBrowser(t, nil)

	t.Run("a pending answer that changes the interval", func(t *testing.T) {
		waits := recordWaits(t, 0)
		cp := deviceCP(t, cpAnswer{status: 202, body: `{"status":"pending","interval":2}`}, approvedAnswer)
		if _, stderr, err := runAfy(t, cp, "login"); err != nil {
			t.Fatalf("%v\n%s", err, stderr)
		}
		if !sameWaits(waits.all(), durations(5, 2)) {
			t.Errorf("waits = %v, want the start's interval, then the one the poll set", waits.all())
		}
	})

	t.Run("polling too fast waits Retry-After and goes on", func(t *testing.T) {
		waits := recordWaits(t, 0)
		cp := deviceCP(t, pendingAnswer,
			errAnswer(429, "DEVICE_POLLING_TOO_FAST", "Slow down.", map[string]string{"Retry-After": "9"}),
			approvedAnswer)
		if _, stderr, err := runAfy(t, cp, "login"); err != nil {
			t.Fatalf("%v\n%s", err, stderr)
		}
		if !sameWaits(waits.all(), durations(5, 5, 9)) {
			t.Errorf("waits = %v, want 5, 5, then the 9 s the server asked for", waits.all())
		}
		if n := len(cp.called("POST", "/auth/device/token")); n != 3 {
			t.Errorf("%d polls, want 3: a 429 is waited out, not fatal", n)
		}
	})

	t.Run("a 503 waits Retry-After and goes on", func(t *testing.T) {
		waits := recordWaits(t, 0)
		cp := deviceCP(t,
			errAnswer(503, "SERVICE_UNAVAILABLE", "Try again shortly.", map[string]string{"Retry-After": "3"}),
			approvedAnswer)
		if _, stderr, err := runAfy(t, cp, "login"); err != nil {
			t.Fatalf("%v\n%s", err, stderr)
		}
		if !sameWaits(waits.all(), durations(5, 3)) {
			t.Errorf("waits = %v, want 5, then the 3 s the server asked for", waits.all())
		}
	})
}

func TestLoginGivesUpOnAServerThatStaysUnavailable(t *testing.T) {
	pipeStdin(t, "")
	recordBrowser(t, nil)
	recordWaits(t, 0)
	cp := deviceCP(t, errAnswer(503, "SERVICE_UNAVAILABLE", "The service is unavailable.", map[string]string{"Retry-After": "1"}))

	_, stderr, err := runAfy(t, cp, "login", "--json")
	if err == nil || ExitCode(err) != exitRequestFailed {
		t.Fatalf("want exit %d, got %v", exitRequestFailed, err)
	}
	if n := len(cp.called("POST", "/auth/device/token")); n != deviceMaxUnavailable+1 {
		t.Errorf("%d polls, want %d: the first plus %d waited out", n, deviceMaxUnavailable+1, deviceMaxUnavailable)
	}
	e := jsonErrorOnStderr(t, "login", stderr)
	if e["code"] != "SERVICE_UNAVAILABLE" || e["status"] != 503.0 {
		t.Errorf("error = %v, want the server's", e)
	}
	if credentialsStored() {
		t.Error("credentials were stored")
	}
}

func TestLoginStopsWhenTheCodesLifeIsUsedUp(t *testing.T) {
	pipeStdin(t, "")
	recordBrowser(t, nil)
	waits := recordWaits(t, 0)
	cp := deviceCP(t, pendingAnswer)
	cp.on("POST", "/auth/device", 200, strings.Replace(deviceStartBody, `"expires_in":600`, `"expires_in":20`, 1))

	_, stderr, err := runAfy(t, cp, "login", "--json")
	if err == nil || ExitCode(err) != exitRequestFailed {
		t.Fatalf("want exit %d, got %v", exitRequestFailed, err)
	}
	if n := len(cp.called("POST", "/auth/device/token")); n != 4 {
		t.Errorf("%d polls, want 4 (20 s at 5 s)", n)
	}
	if !sameWaits(waits.all(), durations(5, 5, 5, 5)) {
		t.Errorf("waits = %v", waits.all())
	}
	e := jsonErrorOnStderr(t, "login", stderr)
	if e["code"] != "DEVICE_CODE_EXPIRED" || !strings.Contains(e["message"].(string), "afy login") {
		t.Errorf("error = %v, want the expiry, naming the command to run again", e)
	}
	if _, has := e["status"]; has {
		t.Errorf("no server said it: the error must carry no status, got %v", e)
	}
	if credentialsStored() {
		t.Error("credentials were stored")
	}
}

func TestLoginSaysWhatToDoForEachWayTheServerEndsTheSignIn(t *testing.T) {
	pipeStdin(t, "")
	recordBrowser(t, nil)
	recordWaits(t, 0)
	for _, tc := range []struct {
		status int
		code   string
		want   []string
	}{
		{410, "DEVICE_CODE_EXPIRED", []string{"expired", "'afy login' again"}},
		{403, "DEVICE_ACCESS_DENIED", []string{"denied", "no key was created", "'afy login' again"}},
		{409, "DEVICE_CODE_ALREADY_USED", []string{"already used", "afy keys list", "'afy login' again"}},
		{404, "DEVICE_CODE_NOT_FOUND", []string{"does not know this code", "'afy login' again"}},
		{400, "API_KEY_LIMIT_REACHED", []string{"limit on API keys", "afy keys revoke", "settings/api-keys", "'afy login' again"}},
	} {
		tc := tc
		t.Run(tc.code, func(t *testing.T) {
			cp := deviceCP(t, errAnswer(tc.status, tc.code, "the server's words", nil))
			stdout, stderr, err := runAfy(t, cp, "login", "--json")
			if err == nil || ExitCode(err) != exitRequestFailed {
				t.Fatalf("want exit %d, got %v", exitRequestFailed, err)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("a refusal wrote to stdout: %q", stdout)
			}
			e := jsonErrorOnStderr(t, tc.code, stderr)
			if e["code"] != tc.code || e["status"] != float64(tc.status) {
				t.Errorf("error = %v, want the server's code and status", e)
			}
			for _, w := range tc.want {
				if !strings.Contains(e["message"].(string), w) {
					t.Errorf("message %q lacks %q", e["message"], w)
				}
			}
			if n := len(cp.called("POST", "/auth/device/token")); n != 1 {
				t.Errorf("%d polls, want 1: a final answer is not polled past", n)
			}
			if len(cp.called("GET", "/auth/me")) != 0 || credentialsStored() {
				t.Error("nothing may be validated or stored when no key was made")
			}
		})
	}

	t.Run("the key limit at the start of the sign-in", func(t *testing.T) {
		cp := deviceCP(t, approvedAnswer)
		cp.on("POST", "/auth/device", 400, errAnswer(400, "API_KEY_LIMIT_REACHED", "x", nil).body)
		_, stderr, err := runAfy(t, cp, "login", "--json")
		if err == nil {
			t.Fatal("want a refusal")
		}
		e := jsonErrorOnStderr(t, "start", stderr)
		if e["code"] != "API_KEY_LIMIT_REACHED" || !strings.Contains(e["message"].(string), "afy keys revoke") {
			t.Errorf("error = %v", e)
		}
		if len(cp.called("POST", "/auth/device/token")) != 0 {
			t.Error("polled a sign-in that never started")
		}
	})
}

func TestLoginInterruptedStopsWithoutPollingOrStoring(t *testing.T) {
	pipeStdin(t, "")
	recordBrowser(t, nil)
	recordWaits(t, 2) // Ctrl-C during the second wait
	cp := deviceCP(t, pendingAnswer)

	_, stderr, err := runAfy(t, cp, "login", "--json")
	if err == nil || ExitCode(err) != exitRequestFailed {
		t.Fatalf("want exit %d, got %v", exitRequestFailed, err)
	}
	if n := len(cp.called("POST", "/auth/device/token")); n != 1 {
		t.Errorf("%d polls, want 1 (the one before the interrupted wait)", n)
	}
	if e := jsonErrorOnStderr(t, "interrupt", stderr); !strings.Contains(e["message"].(string), "afy login") {
		t.Errorf("error = %v, want the command to run again", e)
	}
	if credentialsStored() {
		t.Error("credentials were stored")
	}
}

func TestLoginKeepsAskingThroughAnAnswerThatNeverCame(t *testing.T) {
	pipeStdin(t, "")
	recordBrowser(t, nil)
	waits := recordWaits(t, 0)

	// Poll 1 is cut off mid-connection, polls 2 and 3 are answered by a captive
	// portal's HTML (the same failure twice), poll 4 is the approval.
	var mu sync.Mutex
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch strings.TrimPrefix(r.URL.Path, "/api/v1") {
		case "/auth/device":
			_, _ = io.WriteString(w, deviceStartBody)
		case "/auth/device/token":
			mu.Lock()
			polls++
			n := polls
			mu.Unlock()
			switch {
			case n == 1:
				conn, _, _ := w.(http.Hijacker).Hijack()
				_ = conn.Close()
			case n <= 3:
				w.Header().Set("Content-Type", "text/html")
				_, _ = io.WriteString(w, "<html>Sign in to the Wi-Fi</html>")
			default:
				_, _ = io.WriteString(w, approvedAnswer.body)
			}
		case "/auth/me":
			_, _ = io.WriteString(w, `{"user_id":"u-1","email":"dev@example.com","tier":"starter"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AETHERFY_API_URL", srv.URL+"/api/v1")

	stdout, stderr, err := runAfy(t, nil, "login")
	if err != nil {
		t.Fatalf("a dropped connection ended the sign-in: %v\n%s%s", err, stdout, stderr)
	}
	if n := len(waits.all()); n != 4 {
		t.Errorf("%d waits, want 4: a poll that got no answer is asked again", n)
	}
	// Two different failures, one of them twice: said once per distinct message.
	if n := strings.Count(stdout+stderr, "Still waiting"); n != 2 {
		t.Errorf("%d Still-waiting lines, want 2: %q %q", n, stdout, stderr)
	}
	if n := strings.Count(stdout+stderr, "did not come from the Aetherfy API"); n != 1 {
		t.Errorf("the repeated failure was said %d times, want once: %q %q", n, stdout, stderr)
	}
	if !credentialsStored() {
		t.Error("the key was not stored after the fourth poll")
	}
}

func TestLoginRefusesAnAnswerThatLacksTheCodeOrTheKey(t *testing.T) {
	pipeStdin(t, "")
	recordBrowser(t, nil)
	recordWaits(t, 0)

	t.Run("a start with no user code", func(t *testing.T) {
		cp := deviceCP(t, approvedAnswer)
		cp.on("POST", "/auth/device", 200, `{"device_code":"x","expires_in":600,"interval":5}`)
		_, stderr, err := runAfy(t, cp, "login", "--json")
		if err == nil {
			t.Fatal("want a refusal")
		}
		if e := jsonErrorOnStderr(t, "start", stderr); e["code"] != responseInvalidCode {
			t.Errorf("code = %v, want %s", e["code"], responseInvalidCode)
		}
		if len(cp.called("POST", "/auth/device/token")) != 0 {
			t.Error("polled with a start it could not show")
		}
	})

	t.Run("an approval with no key", func(t *testing.T) {
		cp := deviceCP(t, cpAnswer{status: 200, body: `{"status":"approved"}`})
		_, stderr, err := runAfy(t, cp, "login", "--json")
		if err == nil {
			t.Fatal("want a refusal")
		}
		if e := jsonErrorOnStderr(t, "poll", stderr); e["code"] != responseInvalidCode {
			t.Errorf("code = %v, want %s", e["code"], responseInvalidCode)
		}
		if credentialsStored() {
			t.Error("credentials were stored")
		}
	})

	t.Run("a status this afy does not know", func(t *testing.T) {
		cp := deviceCP(t, cpAnswer{status: 200, body: `{"status":"escalated"}`})
		_, stderr, err := runAfy(t, cp, "login", "--json")
		if err == nil {
			t.Fatal("want a refusal")
		}
		if e := jsonErrorOnStderr(t, "poll", stderr); e["code"] != responseInvalidCode {
			t.Errorf("code = %v, want %s", e["code"], responseInvalidCode)
		}
	})
}

func TestLoginWithAKeyDoesNotStartADeviceSignIn(t *testing.T) {
	recordBrowser(t, nil)
	recordWaits(t, 0)
	const given = "afy_live_0123456789abcdef0123456789abcdef"

	check := func(t *testing.T, cp *fakeCP, stdout, stderr string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%v\n%s%s", err, stdout, stderr)
		}
		if n := len(cp.called("POST", "/auth/device")); n != 0 {
			t.Errorf("a key was given, yet %d device sign-in(s) started", n)
		}
		me := cp.called("GET", "/auth/me")
		if len(me) != 1 || me[0].Authorization != "Bearer "+given {
			t.Errorf("/auth/me = %+v, want one with the given key", me)
		}
		raw, rerr := os.ReadFile(config.CredentialsPath())
		if rerr != nil || !strings.Contains(string(raw), given) {
			t.Errorf("credentials = %q (%v), want the given key", raw, rerr)
		}
	}

	t.Run("--api-key", func(t *testing.T) {
		pipeStdin(t, "")
		cp := deviceCP(t)
		stdout, stderr, err := runAfy(t, cp, "login", "--api-key", given)
		check(t, cp, stdout, stderr, err)
	})

	t.Run("a key piped on stdin", func(t *testing.T) {
		pipeStdin(t, given+"\n")
		cp := deviceCP(t)
		stdout, stderr, err := runAfy(t, cp, "login")
		check(t, cp, stdout, stderr, err)
	})

	t.Run("a malformed key is refused before anything is sent", func(t *testing.T) {
		pipeStdin(t, "")
		cp := deviceCP(t)
		_, _, err := runAfy(t, cp, "login", "--api-key", "not-a-key")
		if err == nil || ExitCode(err) != exitInputRefused {
			t.Fatalf("want exit %d, got %v", exitInputRefused, err)
		}
		if n := len(cp.all()); n != 0 {
			t.Errorf("%d request(s) sent", n)
		}
	})
}

func TestLoginAtATerminalDoesNotPromptForAKey(t *testing.T) {
	recordBrowser(t, nil)
	recordWaits(t, 0)
	read := false
	oldIn, oldTerm := loginStdin, stdinIsTerminal
	loginStdin = readerFunc(func([]byte) (int, error) { read = true; return 0, io.EOF })
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { loginStdin, stdinIsTerminal = oldIn, oldTerm })

	stdout, stderr, err := runAfy(t, deviceCP(t, approvedAnswer), "login")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, stdout, stderr)
	}
	if read {
		t.Error("afy login read stdin at a terminal: the paste prompt is gone")
	}
	if strings.Contains(stdout+stderr, "Enter your API key") {
		t.Errorf("the paste prompt is still printed:\n%s%s", stdout, stderr)
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestLoginNamesTheKeyItCouldNotStore(t *testing.T) {
	pipeStdin(t, "")
	recordBrowser(t, nil)
	recordWaits(t, 0)
	cp := deviceCP(t, approvedAnswer)
	cp.on("GET", "/auth/me", 401, errAnswer(401, "INVALID_API_KEY", "That key was refused.", nil).body)

	_, stderr, err := runAfy(t, cp, "login")
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !strings.Contains(stderr, "afy keys revoke "+approvedKeyID) {
		t.Errorf("stderr must say how to revoke the key it made and lost:\n%s", stderr)
	}
	if strings.Contains(stderr, approvedKey) {
		t.Errorf("stderr printed the key:\n%s", stderr)
	}
	if credentialsStored() {
		t.Error("credentials were stored for a key that did not validate")
	}
}

// waitFor is the shipped wait the tests above replace: it returns as soon as the
// context ends, with the context's error, and does not sit out the duration.
func TestTheShippedWaitStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitFor(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("waitFor on a cancelled context = %v, want context.Canceled", err)
	}
	if err := waitFor(context.Background(), 0); err != nil {
		t.Errorf("waitFor(0) = %v", err)
	}
}

func TestAHostNameLongerThanTheSignInAcceptsIsCutToIt(t *testing.T) {
	long := strings.Repeat("build-box-", 20) + ".internal.example.com" // 221 characters, a legal FQDN
	multibyte := strings.Repeat("é", 100)                           // 100 characters, 200 bytes
	for name, host := range map[string]string{"an fqdn": long, "multibyte characters": multibyte} {
		t.Run(name, func(t *testing.T) {
			pipeStdin(t, "")
			recordBrowser(t, nil)
			recordWaits(t, 0)
			saved := deviceHostname
			deviceHostname = func() (string, error) { return host, nil }
			t.Cleanup(func() { deviceHostname = saved })
			cp := deviceCP(t, approvedAnswer)

			if _, _, err := runAfy(t, cp, "login"); err != nil {
				t.Fatal(err)
			}

			sent, _ := cp.called("POST", "/auth/device")[0].Body["device_name"].(string)
			if len([]rune(sent)) != deviceNameMaxRunes || !strings.HasPrefix(host, sent) {
				t.Errorf("sent %d characters (%q), want the first %d of the host name", len([]rune(sent)), sent, deviceNameMaxRunes)
			}
		})
	}
}

func TestAHostNameWithinTheLimitIsSentAsItIs(t *testing.T) {
	exact := strings.Repeat("h", deviceNameMaxRunes)
	pipeStdin(t, "")
	recordBrowser(t, nil)
	recordWaits(t, 0)
	saved := deviceHostname
	deviceHostname = func() (string, error) { return exact, nil }
	t.Cleanup(func() { deviceHostname = saved })
	cp := deviceCP(t, approvedAnswer)

	if _, _, err := runAfy(t, cp, "login"); err != nil {
		t.Fatal(err)
	}

	if sent := cp.called("POST", "/auth/device")[0].Body["device_name"]; sent != exact {
		t.Errorf("sent %v, want the host name untouched", sent)
	}
}

func TestTheStartRequestNamesTheMachineAndNothingElse(t *testing.T) {
	pipeStdin(t, "")
	recordBrowser(t, nil)
	recordWaits(t, 0)
	cp := deviceCP(t, approvedAnswer)
	if _, _, err := runAfy(t, cp, "login"); err != nil {
		t.Fatal(err)
	}
	got := cp.called("POST", "/auth/device")[0].Body
	if len(got) != 1 || got["device_name"] != "test-host" {
		t.Errorf("start body = %v, want exactly {device_name: test-host}", got)
	}
}

// THE JSON CONTRACT of the two commands that run the device sign-in, as a script
// sees them (own process): stdout is one document on success and empty on a
// refusal, the person's words are on stderr, and a refusal is the error object
// with the server's code.
func TestJSONContractDeviceCommands(t *testing.T) {
	for _, args := range [][]string{{"login"}, {"keys", "create"}} {
		name := strings.Join(args, " ")
		t.Run(name+" succeeds", func(t *testing.T) {
			cp := deviceCP(t, pendingAnswer, approvedAnswer)
			stdout, stderr, code := runAfyProcess(t, cp, nil, append(append([]string{}, args...), "--json")...)
			if code != 0 {
				t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			oneJSONDocument(t, name, stdout)
			if !strings.Contains(stderr, "ABCD-EFGH") {
				t.Errorf("the code belongs on stderr with --json:\n%s", stderr)
			}
			if strings.Contains(stdout, "ABCD-EFGH") {
				t.Errorf("the code leaked to stdout:\n%s", stdout)
			}
		})
		t.Run(name+" is refused", func(t *testing.T) {
			cp := deviceCP(t, errAnswer(403, "DEVICE_ACCESS_DENIED", "Denied.", nil))
			stdout, stderr, code := runAfyProcess(t, cp, nil, append(append([]string{}, args...), "--json")...)
			if code != 1 {
				t.Errorf("exit %d, want 1", code)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("a refusal wrote to stdout: %q", stdout)
			}
			if e := jsonErrorOnStderr(t, name, stderr); e["code"] != "DEVICE_ACCESS_DENIED" || e["status"] != 403.0 {
				t.Errorf("error = %v, want the server's code and status", e)
			}
		})
	}
}
