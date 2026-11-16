package cmd

import (
	"strconv"
	"strings"
	"testing"
)

// `afy cancel <agent> --run <run-id>` cancels a RUN. Without --run the command is
// what it was: it cancels the agent's pending release.

const cancelRunAgent = `{"id":"a1","name":"bot","status":"running","agent_type":"job","created_at":"2026-09-01T10:00:00Z","updated_at":"2026-09-01T10:00:00Z"}`

// runRow is a run of agent agentID, as GET /deployments/{id} answers it.
func runRow(agentID string) string {
	return `{"id":"run-1","agent_id":"` + agentID + `","version":7,"state":"waiting","position":2,"created_at":"2026-09-01T10:00:00Z"}`
}

// cancelAnswer is the cancel route's 200 body.
func cancelAnswer(outcome, message string, count int) string {
	return `{"run":{"id":"run-1","agent_id":"a1","version":7,"state":"cancelled","cancellation_reason":"user_cancel",` +
		`"created_at":"2026-09-01T10:00:00Z"},"outcome":"` + outcome + `","message":"` + message + `","cancelled_count":` +
		strconv.Itoa(count) + `}`
}

// cancelRunCP is a control plane where the agent `bot` (id a1) exists, run-1 is a
// run of agent runAgentID, and cancelling it answers `cancel`.
func cancelRunCP(t *testing.T, runAgentID, cancel string) *fakeCP {
	cp := newFakeCP(t)
	cp.on("GET", "/agents/bot", 200, cancelRunAgent)
	cp.on("GET", "/deployments/run-1", 200, runRow(runAgentID))
	cp.on("POST", "/deployments/run-1/cancel", 200, cancel)
	return cp
}

func TestCancelRunCancelsTheRunAndSaysSo(t *testing.T) {
	cp := cancelRunCP(t, "a1", cancelAnswer("cancelled", "Run cancelled.", 1))
	stdout, stderr, err := runAfy(t, cp, "cancel", "bot", "--run", "run-1")
	if err != nil {
		t.Fatalf("cancel --run: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "✓ Run run-1 cancelled.") {
		t.Errorf("the cancel is not said:\n%s", stdout)
	}
	if strings.Contains(stdout, "below it") {
		t.Errorf("a cancel that took no other run says it did:\n%s", stdout)
	}
	// The agent is resolved and the run read BEFORE the cancel is sent, and the
	// release's cancel route is never touched.
	var seq []string
	for _, c := range cp.all() {
		seq = append(seq, c.Method+" "+c.Path)
	}
	want := "GET /agents/bot,GET /deployments/run-1,POST /deployments/run-1/cancel"
	if strings.Join(seq, ",") != want {
		t.Errorf("requests = %v, want %s", seq, want)
	}
}

func TestCancelRunSaysHowManyRunsBelowItWentToo(t *testing.T) {
	for count, want := range map[int]string{
		2: "1 run below it was cancelled too.",
		4: "3 runs below it were cancelled too.",
	} {
		cp := cancelRunCP(t, "a1", cancelAnswer("cancelled", "Run cancelled.", count))
		stdout, stderr, err := runAfy(t, cp, "cancel", "bot", "--run", "run-1")
		if err != nil {
			t.Fatalf("cancel --run: %v\n%s", err, stderr)
		}
		if !strings.Contains(stdout, "✓ Run run-1 cancelled.") || !strings.Contains(stdout, want) {
			t.Errorf("cancelled_count %d: want the cancel and %q:\n%s", count, want, stdout)
		}
	}
}

func TestCancelRunOfARunInProgressSaysItIsBeingStopped(t *testing.T) {
	cp := cancelRunCP(t, "a1", cancelAnswer("stopping", "The run is being stopped.", 0))
	stdout, stderr, err := runAfy(t, cp, "cancel", "bot", "--run", "run-1")
	if err != nil {
		t.Fatalf("cancel --run: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "Run run-1 is being stopped; it ends cancelled.") {
		t.Errorf("a run being stopped is not said to be:\n%s", stdout)
	}
	if strings.Contains(stdout, "✓ Run run-1 cancelled.") {
		t.Errorf("a run that is still being stopped is said to be cancelled:\n%s", stdout)
	}
}

// A run that had already ended is not an error: the server's sentence, exit 0.
func TestCancelRunOfAFinishedRunPrintsTheServersMessageAndSucceeds(t *testing.T) {
	const message = "a sentence this test made up about a run that had ended"
	cp := cancelRunCP(t, "a1", cancelAnswer("already_finished", message, 0))
	stdout, stderr, err := runAfy(t, cp, "cancel", "bot", "--run", "run-1")
	if err != nil {
		t.Fatalf("an already finished run must exit 0: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, message) {
		t.Errorf("the server's message is not printed:\n%s", stdout)
	}
	if strings.Contains(stdout, "✓ Run run-1 cancelled.") || strings.Contains(stderr, "Error") {
		t.Errorf("a run that had ended is reported as cancelled or as an error:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

// THE RUN MUST BE THE AGENT'S. A run of another agent is refused as input (exit
// 2) before the cancel is sent: nothing is cancelled.
func TestCancelRunOfAnotherAgentsRunIsRefusedAndCancelsNothing(t *testing.T) {
	cp := cancelRunCP(t, "a2", cancelAnswer("cancelled", "Run cancelled.", 1))
	stdout, stderr, err := runAfy(t, cp, "cancel", "bot", "--run", "run-1")
	if err == nil || ExitCode(err) != exitInputRefused {
		t.Fatalf("want exit %d, got %v\n%s%s", exitInputRefused, err, stdout, stderr)
	}
	if !strings.Contains(stderr, "run run-1 is not a run of 'bot'") || !strings.Contains(stderr, "Nothing was cancelled") {
		t.Errorf("the refusal does not say whose run it is not:\n%s", stderr)
	}
	if n := len(cp.called("POST", "/deployments/run-1/cancel")); n != 0 {
		t.Errorf("the cancel was sent %d time(s) for a run of another agent", n)
	}

	// THE CONTROL: the same run, of the agent named, is cancelled.
	cp = cancelRunCP(t, "a1", cancelAnswer("cancelled", "Run cancelled.", 1))
	if _, stderr, err := runAfy(t, cp, "cancel", "bot", "--run", "run-1"); err != nil {
		t.Fatalf("cancel --run: %v\n%s", err, stderr)
	}
	if n := len(cp.called("POST", "/deployments/run-1/cancel")); n != 1 {
		t.Errorf("the run of the agent named was not cancelled (%d request(s))", n)
	}
}

// A refusal of the cancel is the server's sentence, as it wrote it, exit 1: a
// release id answers 409, and the message says what to run instead.
func TestCancelRunPrintsTheServersRefusalVerbatim(t *testing.T) {
	const message = "the stand-in says to cancel a release another way"
	cp := cancelRunCP(t, "a1", "")
	cp.on("POST", "/deployments/run-1/cancel", 409, `{"detail":{"code":"stand-in-code","message":"`+message+`"}}`)
	stdout, stderr, err := runAfy(t, cp, "cancel", "bot", "--run", "run-1")
	if err == nil || ExitCode(err) != exitRequestFailed {
		t.Fatalf("want exit %d, got %v\n%s%s", exitRequestFailed, err, stdout, stderr)
	}
	if strings.Count(stderr, message) != 1 {
		t.Errorf("the server's sentence must be printed once, as written:\n%s", stderr)
	}
}

// --json prints the server's answer as it came.
func TestCancelRunJSONPrintsTheAnswer(t *testing.T) {
	cp := cancelRunCP(t, "a1", cancelAnswer("stopping", "The run is being stopped.", 2))
	stdout, stderr, err := runAfy(t, cp, "cancel", "bot", "--run", "run-1", "--json")
	if err != nil {
		t.Fatalf("cancel --run --json: %v\n%s", err, stderr)
	}
	doc, _ := oneJSONDocument(t, "afy cancel --run --json", stdout).(map[string]interface{})
	run, _ := doc["run"].(map[string]interface{})
	if doc["outcome"] != "stopping" || doc["cancelled_count"] != 2.0 || doc["message"] != "The run is being stopped." ||
		run["id"] != "run-1" || run["cancellation_reason"] != "user_cancel" {
		t.Errorf("cancel --run --json = %v", doc)
	}
}

// A --run with no run id, or with the next flag taken for it, is refused before
// anything is sent.
func TestCancelRunRefusesAValueThatIsNoRunID(t *testing.T) {
	for _, args := range [][]string{
		{"cancel", "bot", "--run", ""},
		{"cancel", "bot", "--run="},
		{"cancel", "bot", "--run", "--json"},
	} {
		cp := cancelRunCP(t, "a1", cancelAnswer("cancelled", "Run cancelled.", 1))
		_, stderr, err := runAfy(t, cp, args...)
		if err == nil || ExitCode(err) != exitInputRefused {
			t.Errorf("afy %s: want exit %d, got %v\n%s", strings.Join(args, " "), exitInputRefused, err, stderr)
		}
		if n := len(cp.all()); n != 0 {
			t.Errorf("afy %s sent %d request(s)", strings.Join(args, " "), n)
		}
	}
}

// Without --run the command cancels the agent's pending release and never touches
// a run's route.
func TestCancelWithoutRunStillCancelsTheRelease(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/agents/bot/deployments", 200,
		deploymentsPage(`[{"id":"d-3","agent_id":"a1","version":3,"state":"queued","created_at":"2026-09-01T10:00:00Z"}]`))
	cp.on("POST", "/agents/bot/deployments/3/cancel", 200,
		`{"id":"d-3","agent_id":"a1","version":3,"state":"failed","created_at":"2026-09-01T10:00:00Z"}`)
	stdout, stderr, err := runAfy(t, cp, "cancel", "bot")
	if err != nil {
		t.Fatalf("cancel: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "Deployment v3 cancelled.") {
		t.Errorf("the release was not cancelled:\n%s", stdout)
	}
	for _, c := range cp.all() {
		if strings.HasPrefix(c.Path, "/deployments/") {
			t.Errorf("the release cancel touched a run route: %s %s", c.Method, c.Path)
		}
	}
}

// The help says what the command does now: it cancels a pending release, which a
// building or deploying one is asked to stop, and with --run a run.
func TestCancelHelpDescribesBothTargets(t *testing.T) {
	long := agentsCancelCmd.Long
	for _, stale := range []string{"Phase 1", "only QUEUED", "cooperative-cancellation"} {
		if strings.Contains(long, stale) {
			t.Errorf("the help still carries %q:\n%s", stale, long)
		}
	}
	for _, want := range []string{"--run", "waiting", "afy runs"} {
		if !strings.Contains(long, want) {
			t.Errorf("the help does not mention %q:\n%s", want, long)
		}
	}
}
