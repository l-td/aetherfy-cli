package cmd

// WHILE THE PLATFORM RETRIES, A WAIT SAYS SO (the record's `retry`: the attempt made next and when). `afy deploy`
// (watchDeployment, which redeploy and rollback share) and `afy run --wait` (followRun) print ONE line each time
// the retry changes, never one per poll. These tests assert EVENTS, not time: the reads are scripted, and the
// instant the line counts from is fixed (retryNow), so nothing sleeps and no elapsed time is measured.
//
// The retry's code is a stand-in spelling here on purpose: the CLI says the same line for every code and branches
// on none, so no control-plane code is pinned by these fixtures.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// retryT0 is the instant every line below counts from.
var retryT0 = time.Date(2026, time.November, 18, 3, 12, 40, 0, time.UTC)

const (
	in5s  = "2026-11-18T03:12:45Z" // 5 s after retryT0
	in10s = "2026-11-18T03:12:50Z"
	in20s = "2026-11-18T03:13:00Z"
	ago3s = "2026-11-18T03:12:37Z" // 3 s before retryT0
	atT0  = "2026-11-18T03:12:40Z"

	retryIn5Attempt2  = "The platform was temporarily unavailable: retrying in 5 s (attempt 2 of 4)."
	retryIn10Attempt2 = "The platform was temporarily unavailable: retrying in 10 s (attempt 2 of 4)."
	retryIn20Attempt3 = "The platform was temporarily unavailable: retrying in 20 s (attempt 3 of 4)."
	retryNowAttempt2  = "The platform was temporarily unavailable: retrying now (attempt 2 of 4)."
	retryNowAttempt3  = "The platform was temporarily unavailable: retrying now (attempt 3 of 4)."

	// What no run or deployment without a retry may ever print.
	retrySentence = "temporarily unavailable"
)

// atRetryT0 fixes the instant the retry line counts its seconds from, for the test's length.
func atRetryT0(t *testing.T) {
	t.Helper()
	previous := retryNow
	retryNow = func() time.Time { return retryT0 }
	t.Cleanup(func() { retryNow = previous })
}

// retryField is the `retry` member of a run or deployment record: the attempt made next, of 4, due at `at`.
func retryField(attempt, maxAttempts int, at, code string) string {
	b, _ := json.Marshal(map[string]interface{}{
		"code": code, "attempt": attempt, "max_attempts": maxAttempts, "next_attempt_at": at,
	})
	return `,"retry":` + string(b)
}

func retryAt(attempt int, at string) string { return retryField(attempt, 4, at, "stand-in-code") }

// ── the sentence ────────────────────────────────────────────────────────────────────────────

// THE WAIT IS ROUNDED UP, and a time that has come says "now". Each row is a boundary: exactly due, a moment
// before it, a nanosecond ahead, between two seconds, on a whole second, and long past.
func TestTheRetryLineRoundsTheWaitUpAndSaysNowWhenItIsDue(t *testing.T) {
	const prefix = "The platform was temporarily unavailable: "
	for _, c := range []struct {
		name string
		due  time.Duration // from the instant the line counts from
		want string
	}{
		{"due exactly now", 0, prefix + "retrying now (attempt 2 of 4)."},
		{"a nanosecond past", -time.Nanosecond, prefix + "retrying now (attempt 2 of 4)."},
		{"long past", -30 * time.Second, prefix + "retrying now (attempt 2 of 4)."},
		{"a nanosecond ahead", time.Nanosecond, prefix + "retrying in 1 s (attempt 2 of 4)."},
		{"a second and a half ahead", 1500 * time.Millisecond, prefix + "retrying in 2 s (attempt 2 of 4)."},
		{"two whole seconds ahead", 2 * time.Second, prefix + "retrying in 2 s (attempt 2 of 4)."},
		{"just over two seconds ahead", 2*time.Second + time.Nanosecond, prefix + "retrying in 3 s (attempt 2 of 4)."},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := &api.RetryState{Code: "stand-in-code", Attempt: 2, MaxAttempts: 4, NextAttemptAt: retryT0.Add(c.due)}
			if got := retryLine(r, retryT0); got != c.want {
				t.Errorf("retryLine = %q, want %q", got, c.want)
			}
		})
	}
}

// The line is the same for every code: the CLI branches on none.
func TestTheRetryLineIsTheSameForEveryCode(t *testing.T) {
	due := retryT0.Add(5 * time.Second)
	a := retryLine(&api.RetryState{Code: "stand-in-code", Attempt: 2, MaxAttempts: 4, NextAttemptAt: due}, retryT0)
	b := retryLine(&api.RetryState{Code: "another-stand-in", Attempt: 2, MaxAttempts: 4, NextAttemptAt: due}, retryT0)
	if a != b || a != retryIn5Attempt2 {
		t.Errorf("the line depends on the code: %q, %q", a, b)
	}
}

// ── afy run --wait ──────────────────────────────────────────────────────────────────────────

// ONE LINE PER CHANGE, NOT PER POLL. The first retry is read three times (said once), then the same attempt with a
// new time (said again), then the next attempt, read twice (said once).
func TestFollowRunSaysEachRetryOnceNotOncePerPoll(t *testing.T) {
	atRetryT0(t)
	stdout, stderr, err, reads, _ := followScript(t, 20,
		aRun("queued", retryAt(2, in5s)), aRun("queued", retryAt(2, in5s)), aRun("queued", retryAt(2, in5s)),
		aRun("queued", retryAt(2, in10s)),
		aRun("queued", retryAt(3, in20s)), aRun("queued", retryAt(3, in20s)),
		aRun("completed", ""))
	if err != nil {
		t.Fatalf("followRun: %v\n%s%s", err, stdout, stderr)
	}
	if reads != 7 {
		t.Errorf("read %d times, want 7: the retries are followed, not given up on", reads)
	}
	last := -1
	for _, want := range []string{retryIn5Attempt2, retryIn10Attempt2, retryIn20Attempt3} {
		if n := strings.Count(stdout, want); n != 1 {
			t.Errorf("%q is said %d times, want once:\n%s", want, n, stdout)
		}
		i := strings.Index(stdout, want)
		if i < last {
			t.Errorf("%q is out of order in:\n%s", want, stdout)
		}
		last = i
	}
	if n := strings.Count(stdout, retrySentence); n != 3 {
		t.Errorf("the retry is said %d times in all, want 3 (one per change):\n%s", n, stdout)
	}
	if !strings.Contains(stdout, "✓ Run completed.") {
		t.Errorf("the run's end is not said:\n%s", stdout)
	}
}

// A retry that is gone and then comes back is a new retry: nothing was being retried in between.
func TestFollowRunSaysARetryThatComesBackAfterNone(t *testing.T) {
	atRetryT0(t)
	stdout, stderr, err, _, _ := followScript(t, 20,
		aRun("queued", retryAt(3, in20s)), aRun("building", ""), aRun("queued", retryAt(3, in20s)), aRun("completed", ""))
	if err != nil {
		t.Fatalf("followRun: %v\n%s%s", err, stdout, stderr)
	}
	if n := strings.Count(stdout, retryIn20Attempt3); n != 2 {
		t.Errorf("a retry that returned after a read with none is said %d times, want 2:\n%s", n, stdout)
	}
}

// A time that has come, or has gone, says "retrying now": never a zero or a negative number of seconds.
func TestFollowRunSaysRetryingNowWhenTheTimeHasComeOrGone(t *testing.T) {
	atRetryT0(t)
	stdout, stderr, err, _, _ := followScript(t, 20,
		aRun("queued", retryAt(2, ago3s)), aRun("queued", retryAt(3, atT0)), aRun("completed", ""))
	if err != nil {
		t.Fatalf("followRun: %v\n%s%s", err, stdout, stderr)
	}
	for _, want := range []string{retryNowAttempt2, retryNowAttempt3} {
		if n := strings.Count(stdout, want); n != 1 {
			t.Errorf("%q is said %d times, want once:\n%s", want, n, stdout)
		}
	}
	if strings.Contains(stdout, "retrying in") {
		t.Errorf("a time that has come must not say a number of seconds:\n%s", stdout)
	}
}

// NULL, AND ABSENT, SAY NOTHING: the records the control plane writes when nothing is being retried.
func TestFollowRunSaysNothingWhenThereIsNoRetry(t *testing.T) {
	atRetryT0(t)
	stdout, stderr, err, _, _ := followScript(t, 20,
		aRun("queued", `,"retry":null`), aRun("building", ""), aRun("active", `,"retry":null`), aRun("completed", ""))
	if err != nil {
		t.Fatalf("followRun: %v\n%s%s", err, stdout, stderr)
	}
	if out := stdout + stderr; strings.Contains(out, retrySentence) || strings.Contains(out, "retrying") {
		t.Errorf("a run with no retry says one:\n%s", out)
	}
}

// With --json the line is human text: it goes to stderr and stdout stays the one JSON document.
func TestRunWaitJSONSaysTheRetryOnStderrAndKeepsStdoutOneDocument(t *testing.T) {
	atRetryT0(t)
	stdout, stderr, err := runWaitWith(t, 10, acceptedWaiting, []string{"--json"},
		dRun("queued", retryAt(2, in5s)), dRun("queued", retryAt(2, in5s)), dRun("completed", ""))
	if exitCodeOf(err) != 0 {
		t.Fatalf("exit %d, want 0\n%s%s", exitCodeOf(err), stdout, stderr)
	}
	oneJSONDocument(t, "afy run --wait --json", stdout)
	if strings.Contains(stdout, retrySentence) {
		t.Errorf("the retry line reached stdout in JSON mode:\n%s", stdout)
	}
	if n := strings.Count(stderr, retryIn5Attempt2); n != 1 {
		t.Errorf("the retry is said %d times on stderr, want once:\n%s", n, stderr)
	}
}

// ── afy deploy (and redeploy, rollback) ─────────────────────────────────────────────────────

// deploymentSequence answers the reads of deployment d-4 with `answers` in order (the last repeating), and returns
// a client on it.
func deploymentSequence(t *testing.T, answers ...cpAnswer) *api.Client {
	t.Helper()
	cp := newFakeCP(t)
	cp.onSequence("GET", "/deployments/d-4", answers...)
	return api.NewClientWithURL(cp.srv.URL+"/api/v1", "afy_test_key")
}

func aDeployment(state, extra string) cpAnswer {
	return cpAnswer{status: 200, body: `{"id":"d-4","agent_id":"a1","version":4,"state":"` + state +
		`","created_at":"2026-11-18T03:00:00Z"` + extra + `}`}
}

func TestWatchDeploymentSaysEachRetryOnceNotOncePerPoll(t *testing.T) {
	atRetryT0(t)
	client := deploymentSequence(t,
		aDeployment("queued", retryField(2, 4, in5s, "another-stand-in")), aDeployment("queued", retryField(2, 4, in5s, "another-stand-in")),
		aDeployment("building", retryField(2, 4, in10s, "another-stand-in")),
		aDeployment("building", retryField(3, 4, in20s, "another-stand-in")), aDeployment("building", retryField(3, 4, in20s, "another-stand-in")),
		aDeployment("failed", `,"error_message":"the stand-in gave up"`))

	var err error
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() { err = watchDeployment(client, "tick", "d-4", fastPoll, time.Minute) })
	})
	if err == nil {
		t.Fatalf("the script ends in a failed deployment\n%s%s", stdout, stderr)
	}
	for _, want := range []string{retryIn5Attempt2, retryIn10Attempt2, retryIn20Attempt3} {
		if n := strings.Count(stdout, want); n != 1 {
			t.Errorf("%q is said %d times, want once:\n%s", want, n, stdout)
		}
	}
	if n := strings.Count(stdout, retrySentence); n != 3 {
		t.Errorf("the retry is said %d times in all, want 3 (one per change):\n%s", n, stdout)
	}
}

func TestWatchDeploymentSaysRetryingNowAndSaysNothingWhenThereIsNoRetry(t *testing.T) {
	atRetryT0(t)
	client := deploymentSequence(t,
		aDeployment("queued", retryAt(2, ago3s)),
		aDeployment("building", `,"retry":null`),
		aDeployment("failed", `,"error_message":"the stand-in gave up"`))

	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() { _ = watchDeployment(client, "tick", "d-4", fastPoll, time.Minute) })
	})
	if strings.Count(stdout, retryNowAttempt2) != 1 || strings.Count(stdout, retrySentence) != 1 {
		t.Errorf("a retry already due is said once, as now:\n%s%s", stdout, stderr)
	}

	// CONTROL: a deployment that never had a retry says nothing about one.
	client = deploymentSequence(t, aDeployment("queued", ""), aDeployment("building", `,"retry":null`),
		aDeployment("failed", `,"error_message":"the stand-in gave up"`))
	stderr = captureStderr(t, func() {
		stdout = captureStdout(t, func() { _ = watchDeployment(client, "tick", "d-4", fastPoll, time.Minute) })
	})
	if out := stdout + stderr; strings.Contains(out, retrySentence) || strings.Contains(out, "retrying") {
		t.Errorf("a deployment with no retry says one:\n%s", out)
	}
}

// ── --json carries the field as the server sent it ──────────────────────────────────────────

// wantRetryAsSent fails unless got is the retry object of retryAt(2, in5s) with code "stand-in-code".
func wantRetryAsSent(t *testing.T, label string, got interface{}) {
	t.Helper()
	r, ok := got.(map[string]interface{})
	if !ok {
		t.Errorf("%s: retry = %v, want the object the server sent", label, got)
		return
	}
	if r["code"] != "stand-in-code" || r["attempt"] != float64(2) || r["max_attempts"] != float64(4) ||
		r["next_attempt_at"] != in5s || len(r) != 4 {
		t.Errorf("%s: retry = %v, want code, attempt 2, max_attempts 4, next_attempt_at %s and nothing else", label, r, in5s)
	}
}

// wantRetryNull fails unless the key is there and says null: not dropped, not an empty object.
func wantRetryNull(t *testing.T, label string, record map[string]interface{}) {
	t.Helper()
	if value, present := record["retry"]; !present || value != nil {
		t.Errorf("%s: a record with no retry must say null, not drop the key: %v", label, record)
	}
}

// wantRetryInJSON checks one record of a --json answer: the retry object as sent when `sent` holds one, null when
// it says `"retry":null`.
func wantRetryInJSON(t *testing.T, label string, record map[string]interface{}, sent bool) {
	t.Helper()
	if sent {
		wantRetryAsSent(t, label, record["retry"])
	} else {
		wantRetryNull(t, label, record)
	}
}

// afy runs --json: each run carries its retry.
func TestRunsJSONCarriesTheRetryAsSentAndSaysNullWhenThereIsNone(t *testing.T) {
	jsonRun(t, true)
	out := runsTable(t, `[`+
		`{"id":"run-retrying","trigger_source":"manual","state":"queued","created_at":"2026-11-18T03:00:00Z"`+retryAt(2, in5s)+`},`+
		`{"id":"run-plain","trigger_source":"manual","state":"completed","created_at":"2026-11-18T02:00:00Z","retry":null}]`)
	var runs []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &runs); err != nil || len(runs) != 2 {
		t.Fatalf("afy runs --json: %v\n%s", err, out)
	}
	wantRetryInJSON(t, "afy runs --json (retrying run)", runs[0], true)
	wantRetryInJSON(t, "afy runs --json (plain run)", runs[1], false)
}

// afy status --json: the agent's last run carries its retry.
func TestStatusJSONCarriesTheLastRunsRetryAsSentAndSaysNullWhenThereIsNone(t *testing.T) {
	jsonRun(t, true)
	for _, c := range []struct {
		name  string
		field string
		sent  bool
	}{
		{"a retrying last run", retryAt(2, in5s), true},
		{"a last run with none", `,"retry":null`, false},
	} {
		cp := newFakeCP(t)
		cp.on("GET", "/agents/bot", 200, `{"id":"a1","name":"bot","status":"running","agent_type":"task",`+
			`"created_at":"2026-09-01T10:00:00Z","updated_at":"2026-09-01T10:00:00Z",`+
			`"last_run":{"id":"r1","trigger_source":"manual","state":"queued","created_at":"2026-11-18T03:00:00Z"`+c.field+`}}`)
		var err error
		status := captureStdout(t, func() {
			err = showAgentStatus(api.NewClientWithURL(cp.srv.URL+"/api/v1", "afy_test_key"), "bot")
		})
		if err != nil {
			t.Fatalf("%s: afy status --json: %v", c.name, err)
		}
		var agent map[string]interface{}
		if err := json.Unmarshal([]byte(status), &agent); err != nil {
			t.Fatalf("%s: afy status --json printed no JSON object: %v\n%s", c.name, err, status)
		}
		lastRun, _ := agent["last_run"].(map[string]interface{})
		if lastRun == nil {
			t.Fatalf("%s: afy status --json carries no last_run: %v", c.name, agent)
		}
		wantRetryInJSON(t, "afy status --json: "+c.name, lastRun, c.sent)
	}
}

// A deployment, as `afy deploy --json` ends with it and `afy deployments --json` lists it: the record the server
// sent, marshalled as the commands write it.
func TestADeploymentCarriesItsRetryAsSentAndSaysNullWhenThereIsNone(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		sent bool
	}{
		{"a deployment with a retry", `{"id":"d-4","version":4,"state":"queued"` + retryAt(2, in5s) + `}`, true},
		{"a deployment with none", `{"id":"d-4","version":4,"state":"queued","retry":null}`, false},
	} {
		cp := newFakeCP(t)
		cp.on("GET", "/deployments/d-4", 200, c.body)
		d, err := api.NewClientWithURL(cp.srv.URL+"/api/v1", "afy_test_key").GetDeployment("d-4")
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		raw, _ := json.Marshal(d)
		var record map[string]interface{}
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		wantRetryInJSON(t, c.name, record, c.sent)
	}
}
