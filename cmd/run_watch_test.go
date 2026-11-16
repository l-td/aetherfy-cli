package cmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// `afy run --wait` FOLLOWS THE RUN TO THE STATE IT ENDS IN, and says how that
// went in its exit code: 0 completed, 1 failed or cancelled (with the reason), 4
// when its bound passes with the run still going. It used to exit 0 on its
// 30-minute timeout and poll forever on a state it did not know.
//
// These tests drive followRun by EVENTS: a stand-in control plane answers each
// read of the run with the next state of a script, and the clock is a script too
// (one more read per tick, then the bound). Nothing sleeps and nothing measures
// time.

// scriptedClock gives followRun `ticks` more reads after its first, then reports
// the bound. stopped says followRun released the clock.
type scriptedClock struct {
	ticks   int
	stopped bool
}

func (c *scriptedClock) next() bool {
	if c.ticks == 0 {
		return false
	}
	c.ticks--
	return true
}

func (c *scriptedClock) stop() { c.stopped = true }

// aRun is the run `run-1` as GET /deployments/run-1 answers it in a state.
func aRun(state string, extra string) cpAnswer {
	return cpAnswer{status: 200, body: `{"id":"run-1","agent_id":"a1","version":7,"state":"` + state +
		`","created_at":"2026-09-01T10:00:00Z"` + extra + `}`}
}

func waitingAt(place string) cpAnswer { return aRun("waiting", `,"position":`+place) }

// followScript runs followRun against a control plane that answers the run's reads
// with `answers` in order (the last one repeating), through a clock that allows
// `ticks` reads after the first, and returns what it printed and how it ended.
func followScript(t *testing.T, ticks int, answers ...cpAnswer) (stdout, stderr string, err error, reads int, clock *scriptedClock) {
	t.Helper()
	cp := newFakeCP(t)
	cp.onSequence("GET", "/deployments/run-1", answers...)
	client := api.NewClientWithURL(cp.srv.URL+"/api/v1", "afy_test_key")
	clock = &scriptedClock{ticks: ticks}
	stderr = captureStderr(t, func() {
		stdout = captureStdout(t, func() { err = followRun(client, "bot", "run-1", clock) })
	})
	return stdout, stderr, err, len(cp.called("GET", "/deployments/run-1")), clock
}

func TestFollowRunExitsZeroWhenTheRunCompletes(t *testing.T) {
	stdout, stderr, err, reads, clock := followScript(t, 10, aRun("active", ""), aRun("completed", ""))
	if err != nil {
		t.Fatalf("a run that completed returned %v\n%s%s", err, stdout, stderr)
	}
	if reads != 2 {
		t.Errorf("the run was read %d times, want 2 (one per state of the script)", reads)
	}
	for _, want := range []string{"Status: active", "Status: completed", "✓ Run completed."} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if !clock.stopped {
		t.Error("the clock was not released")
	}
}

func TestFollowRunExitsOneWithTheReasonWhenTheRunFails(t *testing.T) {
	for _, state := range []string{"failed", "error"} {
		stdout, stderr, err, _, _ := followScript(t, 10, aRun("active", ""),
			aRun(state, `,"error_message":"the stand-in says the run exited with code 7"`))
		if err == nil || ExitCode(err) != exitRequestFailed || !alreadyPrinted(err) {
			t.Errorf("%s: err = %v (exit %d, printed %v), want exit 1, printed once by followRun", state, err, ExitCode(err), alreadyPrinted(err))
		}
		if !strings.Contains(stderr, "Error: Run failed.") || !strings.Contains(stdout, "Reason: the stand-in says the run exited with code 7") {
			t.Errorf("%s: the failure and its reason are not printed:\nstdout:\n%s\nstderr:\n%s", state, stdout, stderr)
		}
	}
}

func TestFollowRunExitsOneWithTheSentenceWhenTheRunIsCancelled(t *testing.T) {
	const sentence = "a sentence this test made up about who cancelled the run"
	stdout, stderr, err, reads, _ := followScript(t, 10, waitingAt("2"),
		aRun("cancelled", `,"error_message":"`+sentence+`","cancellation_reason":"user_cancel"`))
	if err == nil || ExitCode(err) != exitRequestFailed || !alreadyPrinted(err) {
		t.Fatalf("err = %v (exit %d, printed %v), want exit 1, printed once by followRun", err, ExitCode(err), alreadyPrinted(err))
	}
	if reads != 2 {
		t.Errorf("a cancelled run was read %d times, want 2: the follow ends on it", reads)
	}
	if !strings.Contains(stderr, "Error: Run cancelled.") || !strings.Contains(stdout, "Reason: "+sentence) {
		t.Errorf("the cancellation and its sentence are not printed:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stdout, "Status: cancelled") {
		t.Errorf("the state cancelled is not said:\n%s", stdout)
	}
}

// A run waiting in the line is followed: its place is said when it changes (and
// only then), and the follow goes on until the run ends.
func TestFollowRunSaysAWaitingRunsPlaceWhenItChangesAndKeepsWaiting(t *testing.T) {
	stdout, stderr, err, reads, _ := followScript(t, 20,
		waitingAt("3"), waitingAt("3"), waitingAt("2"), waitingAt("1"), aRun("queued", ""), aRun("active", ""), aRun("completed", ""))
	if err != nil {
		t.Fatalf("followRun: %v\n%s%s", err, stdout, stderr)
	}
	if reads != 7 {
		t.Errorf("read %d times, want 7: a waiting run is followed, not given up on", reads)
	}
	if n := strings.Count(stdout, "Status: waiting (#3 in line)"); n != 1 {
		t.Errorf("the place #3 is said %d times, want once (it did not change between the two reads):\n%s", n, stdout)
	}
	// In the order the places came, ending with the states after the line.
	last := -1
	for _, want := range []string{"waiting (#3 in line)", "waiting (#2 in line)", "waiting (#1 in line)", "Status: queued", "Status: active", "Status: completed"} {
		i := strings.Index(stdout, want)
		if i < 0 || i < last {
			t.Fatalf("%q is missing or out of order in:\n%s", want, stdout)
		}
		last = i
	}
}

// A state this CLI does not know does not end the follow and is not a hang: the
// run is followed to its end, and the state is said once, raw.
func TestFollowRunKeepsFollowingAStateItDoesNotKnowAndSaysItOnce(t *testing.T) {
	stdout, stderr, err, reads, _ := followScript(t, 20,
		aRun("mystery", ""), aRun("mystery", ""), aRun("mystery", ""), aRun("completed", ""))
	if err != nil {
		t.Fatalf("followRun: %v\n%s%s", err, stdout, stderr)
	}
	if reads != 4 {
		t.Errorf("read %d times, want 4: the unknown state is followed through", reads)
	}
	if n := strings.Count(stdout, "Status: mystery"); n != 1 {
		t.Errorf("the unknown state is said %d times, want once:\n%s", n, stdout)
	}
	if !strings.Contains(stdout, "✓ Run completed.") {
		t.Errorf("the run's end is not said:\n%s", stdout)
	}
}

// THE BOUND IS A FAILURE. The wait that gives up leaves the run going, exits 4 (as
// `afy start --wait` does at its bound), and says so and how to follow the run.
func TestFollowRunNonZeroWhenTheBoundPassesWithTheRunStillGoing(t *testing.T) {
	stdout, stderr, err, reads, clock := followScript(t, 3, waitingAt("3"))
	if err == nil {
		t.Fatalf("a wait that gave up on a run still waiting returned nil\n%s%s", stdout, stderr)
	}
	var pending *stillPendingError
	if !errors.As(err, &pending) || ExitCode(err) != exitStillPending {
		t.Fatalf("err = %v (exit %d), want the still-pending error, exit %d", err, ExitCode(err), exitStillPending)
	}
	if reads != 4 {
		t.Errorf("read %d times, want 4 (the first read and one per tick)", reads)
	}
	for _, want := range []string{
		"stopped waiting after 30 minutes", "run run-1 is still waiting (#3 in line)",
		"does not stop the run", "'afy runs bot'", "'afy logs bot --run run-1'",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message lacks %q: %s", want, err.Error())
		}
	}
	if !clock.stopped {
		t.Error("the clock was not released")
	}
}

// A read of the run that fails is said and the follow goes on to the run's end.
func TestFollowRunSaysAFailedReadAndGoesOn(t *testing.T) {
	stdout, stderr, err, reads, _ := followScript(t, 10,
		cpAnswer{status: 404, body: `{"detail":{"code":"stand-in-code","message":"the stand-in lost the run for a moment"}}`},
		aRun("completed", ""))
	if err != nil {
		t.Fatalf("followRun: %v\n%s", err, stderr)
	}
	if reads != 2 || !strings.Contains(stdout, "Failed to get run status") {
		t.Errorf("reads %d; stdout %q: want the failed read said and a second read made", reads, stdout)
	}
}

// THE REAL CLOCK, both of its answers, without waiting for either to be late: a
// bound that has passed ends the follow, and a tick that is due reads again.
func TestTheRunWatchClockAnswersTheBoundAndTheTick(t *testing.T) {
	passed := newTimerRunWatchClock(time.Hour, time.Nanosecond)
	defer passed.stop()
	if passed.next() {
		t.Error("a bound that passed must end the follow")
	}
	due := newTimerRunWatchClock(time.Millisecond, time.Hour)
	defer due.stop()
	if !due.next() {
		t.Error("a tick that came due must read the run again")
	}
}

func TestTheShippedRunWatchBounds(t *testing.T) {
	if runWatchPoll != 5*time.Second || runWatchTimeout != 30*time.Minute {
		t.Errorf("the shipped watch reads every %s for %s, want 5s and 30m", runWatchPoll, runWatchTimeout)
	}
	if got := formatWaitBound(30 * time.Minute); got != "30 minutes" {
		t.Errorf("formatWaitBound = %q", got)
	}
}

// ── through the shipped command ─────────────────────────────────────────────────────────

// runWait runs `afy run bot --wait` against a control plane that accepts the run as
// `accepted` and answers its reads with `answers`, through a clock allowing `ticks`
// reads after the first.
func runWaitWith(t *testing.T, ticks int, accepted string, extra []string, answers ...cpAnswer) (stdout, stderr string, err error) {
	t.Helper()
	cp := newFakeCP(t)
	cp.on("POST", "/agents/bot/run", 202, accepted)
	cp.onSequence("GET", "/deployments/d-1", answers...)
	previous := newRunWatchClock
	newRunWatchClock = func() runWatchClock { return &scriptedClock{ticks: ticks} }
	t.Cleanup(func() { newRunWatchClock = previous })
	return runAfy(t, cp, append([]string{"run", "bot", "--wait"}, extra...)...)
}

const acceptedWaiting = `{"deployment_id":"d-1","version":4,"job_id":null,"status":"waiting","position":2}`

func dRun(state, extra string) cpAnswer {
	return cpAnswer{status: 200, body: `{"id":"d-1","agent_id":"a1","version":4,"state":"` + state +
		`","created_at":"2026-09-01T10:00:00Z"` + extra + `}`}
}

func TestRunWaitExitCodes(t *testing.T) {
	cases := []struct {
		name    string
		ticks   int
		answers []cpAnswer
		want    int
		in      string // what the output must carry
	}{
		{"completed", 10, []cpAnswer{dRun("waiting", `,"position":2`), dRun("completed", "")}, 0, "✓ Run completed."},
		{"failed", 10, []cpAnswer{dRun("failed", `,"error_message":"exited 7"`)}, exitRequestFailed, "Reason: exited 7"},
		{"cancelled", 10, []cpAnswer{dRun("waiting", `,"position":1`), dRun("cancelled", `,"error_message":"stopped by its owner"`)}, exitRequestFailed, "Reason: stopped by its owner"},
		{"the wait gives up", 2, []cpAnswer{dRun("waiting", `,"position":5`)}, exitStillPending, "stopped waiting after 30 minutes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stdout, stderr, err := runWaitWith(t, c.ticks, acceptedWaiting, nil, c.answers...)
			if got := exitCodeOf(err); got != c.want {
				t.Errorf("exit %d, want %d\n%s%s", got, c.want, stdout, stderr)
			}
			if !strings.Contains(stdout+stderr, c.in) {
				t.Errorf("the output lacks %q:\n%s%s", c.in, stdout, stderr)
			}
			if !strings.Contains(stdout, "✓ Run accepted; waiting for a slot (#2 in line).") {
				t.Errorf("the accepted line is gone:\n%s", stdout)
			}
		})
	}
}

// exitCodeOf is the exit code of the process for what Execute returned.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	return ExitCode(err)
}

// A failure with --json is the one error object, the reason inside its message.
func TestRunWaitFailureIsOneJSONErrorWithTheReason(t *testing.T) {
	stdout, stderr, err := runWaitWith(t, 10, acceptedWaiting, []string{"--json"},
		dRun("cancelled", `,"error_message":"stopped by its owner"`))
	if exitCodeOf(err) != exitRequestFailed {
		t.Fatalf("exit %d, want 1\n%s%s", exitCodeOf(err), stdout, stderr)
	}
	// stdout holds the run that was accepted, and nothing after it.
	doc, _ := oneJSONDocument(t, "afy run --wait --json", stdout).(map[string]interface{})
	if doc["status"] != "waiting" {
		t.Errorf("stdout = %v, want the accepted run", doc)
	}
	e := jsonErrorOnStderr(t, "afy run --wait --json", stderr)
	if msg, _ := e["message"].(string); !strings.Contains(msg, "Run cancelled.") || !strings.Contains(msg, "stopped by its owner") {
		t.Errorf("the error does not carry the cancellation and its reason: %v", e)
	}
}

// The help says what the wait does.
func TestRunHelpSaysWhatWaitDoes(t *testing.T) {
	long, usage := agentsRunCmd.Long, agentsRunCmd.Flags().Lookup("wait").Usage
	for _, want := range []string{"waits its turn", "exits 0", "1 if it failed or was cancelled", "exits 4"} {
		if !strings.Contains(long, want) {
			t.Errorf("the help lacks %q:\n%s", want, long)
		}
	}
	if !strings.Contains(usage, "cancelled") {
		t.Errorf("the --wait flag says %q: it must say a cancelled run exits 1 too", usage)
	}
}

// THE SHIPPED PROCESS: the exit code a script sees. followRun returns its ending and
// the process exits with it (the re-executed test binary reads its runs every
// 10 ms, set in TestMain).
func TestRunWaitExitCodeAsAProcess(t *testing.T) {
	for _, c := range []struct {
		name    string
		answers []cpAnswer
		want    int
	}{
		{"completed", []cpAnswer{dRun("waiting", `,"position":1`), dRun("completed", "")}, 0},
		{"failed", []cpAnswer{dRun("active", ""), dRun("failed", `,"error_message":"exited 7"`)}, 1},
		{"cancelled", []cpAnswer{dRun("waiting", `,"position":1`), dRun("cancelled", `,"error_message":"stopped by its owner"`)}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			cp := newFakeCP(t)
			cp.on("POST", "/agents/bot/run", 202, acceptedWaiting)
			cp.onSequence("GET", "/deployments/d-1", c.answers...)
			stdout, stderr, code := runAfyProcess(t, cp, nil, "run", "bot", "--wait")
			if code != c.want {
				t.Errorf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", code, c.want, stdout, stderr)
			}
		})
	}
}
