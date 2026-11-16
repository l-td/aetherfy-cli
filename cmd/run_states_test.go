package cmd

import (
	"strings"
	"testing"

	"github.com/fatih/color"
	"github.com/l-td/aetherfy-cli/internal/output"
)

// THE TWO RUN STATES THE RUN QUEUE ADDED. A run over the account's limit WAITS in
// a line instead of being refused (state `waiting`, with a 1-based `position`),
// and a run stopped from outside ends `cancelled`, with the reason's sentence in
// error_message. Every place a run's state is shown says both, and none of them
// keeps a table of the reasons: the sentence is the server's.

func intp(n int) *int { return &n }

func TestFormatRunStateSaysAWaitingRunsPlaceAndACancelledRun(t *testing.T) {
	cases := []struct {
		state    string
		position *int
		want     string
	}{
		{"waiting", intp(3), "waiting (#3 in line)"},
		{"waiting", intp(1), "waiting (#1 in line)"},
		{"waiting", nil, "waiting"},
		{"cancelled", nil, "cancelled"},
		// A place on a state that is not waiting is not printed: the line is the
		// waiting runs' alone.
		{"active", intp(2), "active"},
		{"completed", intp(2), "completed"},
	}
	for _, c := range cases {
		if got := formatRunState(c.state, c.position); got != c.want {
			t.Errorf("formatRunState(%q, %v) = %q, want %q", c.state, c.position, got, c.want)
		}
	}
}

// THE COLOURS, which the plain-text test above cannot see: waiting is yellow like
// every run in flight, cancelled is dim (an outside stop, like a superseded
// release), and failed stays the only red.
func TestFormatRunStateColours(t *testing.T) {
	color.NoColor = false
	t.Cleanup(output.DisableColors)

	const (
		yellow = "\x1b[33m"
		dim    = "\x1b[90m"
		red    = "\x1b[31m"
	)
	for state, want := range map[string]string{"waiting": yellow, "cancelled": dim, "failed": red, "queued": yellow} {
		if got := formatRunState(state, nil); !strings.HasPrefix(got, want) {
			t.Errorf("formatRunState(%q) = %q, want it to start with the colour %q", state, got, want)
		}
	}
}

func TestRunsTableShowsAWaitingRunsPlaceAndACancelledRun(t *testing.T) {
	out := runsTable(t, `[`+
		`{"id":"run-wait","trigger_source":"manual","state":"waiting","created_at":"2026-09-16T03:00:00Z","position":3,"cancellation_reason":null},`+
		`{"id":"run-cancelled","trigger_source":"cron","state":"cancelled","created_at":"2026-09-16T02:00:00Z","position":null,"cancellation_reason":"user_cancel","error_message":"Cancelled by its owner."}`+
		`]`)

	if line := firstLineWith(out, "run-wait"); !strings.Contains(line, "waiting (#3 in line)") {
		t.Errorf("the waiting run's row does not say its place: %q\n%s", line, out)
	}
	if line := firstLineWith(out, "run-cancelled"); !strings.Contains(line, "cancelled") {
		t.Errorf("the cancelled run's row does not say cancelled: %q\n%s", line, out)
	}
}

// --json carries what the server sent about the line and the cancellation, as the
// server sent it: a script reads `position` and `cancellation_reason`, never the
// table's sentence.
func TestRunsJSONCarriesPositionAndCancellationReason(t *testing.T) {
	jsonRun(t, true)
	out := runsTable(t, `[`+
		`{"id":"run-wait","trigger_source":"manual","state":"waiting","created_at":"2026-09-16T03:00:00Z","position":3,"cancellation_reason":null},`+
		`{"id":"run-cancelled","trigger_source":"cron","state":"cancelled","created_at":"2026-09-16T02:00:00Z","position":null,"cancellation_reason":"parent_run_cancelled"}`+
		`]`)

	doc := oneJSONDocument(t, "afy runs --json", out).([]interface{})
	waiting, cancelled := doc[0].(map[string]interface{}), doc[1].(map[string]interface{})
	if waiting["position"] != 3.0 || waiting["cancellation_reason"] != nil {
		t.Errorf("the waiting run's row = %v, want position 3 and a null reason", waiting)
	}
	if cancelled["position"] != nil || cancelled["cancellation_reason"] != "parent_run_cancelled" {
		t.Errorf("the cancelled run's row = %v, want a null position and the server's reason code", cancelled)
	}
}

// `afy status` Last run: a cancelled run is followed by the server's sentence,
// as a failed one is; a waiting one says it waits.
func TestStatusLastRunSaysWhyARunWasCancelled(t *testing.T) {
	const sentence = "a sentence this test made up about why it ended"
	out := statusWithSchedule(t,
		`,"last_run":{"id":"r1","trigger_source":"cron","state":"cancelled","created_at":"2026-09-16T12:30:01Z",`+
			`"error_message":"`+sentence+`","release_version":3}`)
	if line := lineWith(out, "Last run"); !strings.Contains(line, "cancelled ("+sentence+")") {
		t.Errorf("the Last run line does not carry the cancelled run's sentence: %q\n%s", line, out)
	}

	// THE CONTROL: a run that completed prints no sentence, so the line above is
	// the cancelled state's and not every state's.
	out = statusWithSchedule(t,
		`,"last_run":{"id":"r1","trigger_source":"cron","state":"completed","created_at":"2026-09-16T12:30:01Z",`+
			`"error_message":"`+sentence+`","release_version":3}`)
	if line := lineWith(out, "Last run"); strings.Contains(line, sentence) {
		t.Errorf("a completed run's Last run line carries a sentence: %q", line)
	}

	out = statusWithSchedule(t,
		`,"last_run":{"id":"r1","trigger_source":"manual","state":"waiting","created_at":"2026-09-16T12:30:01Z","release_version":null}`)
	if line := lineWith(out, "Last run"); !strings.Contains(line, "waiting,") {
		t.Errorf("the Last run line does not say the run waits: %q\n%s", line, out)
	}
}

func TestListLastRunSaysAWaitingAndACancelledRun(t *testing.T) {
	rows := listRows(t, `[`+
		listAgent("ran-failed", `,"last_run":{"id":"r1","trigger_source":"manual","state":"waiting","created_at":"2026-09-16T12:30:01Z"}`)+`,`+
		listAgent("tick-skipped", `,"last_run":{"id":"r2","trigger_source":"cron","state":"cancelled","created_at":"2026-09-16T12:30:01Z","error_message":"x"}`)+
		`]`)
	if !strings.Contains(rows["ran-failed"], "waiting,") {
		t.Errorf("the waiting last run is not in its row: %q", rows["ran-failed"])
	}
	if !strings.Contains(rows["tick-skipped"], "cancelled,") {
		t.Errorf("the cancelled last run is not in its row: %q", rows["tick-skipped"])
	}
}

// A schedule's deadline is a status line when the agent has one, and no line when
// it has none.
func TestStatusPrintsTheSchedulesDeadlineOnlyWhenItHasOne(t *testing.T) {
	out := statusWithSchedule(t, `,"cron_deadline_minutes":45`)
	if line := lineWith(out, "Deadline"); !strings.Contains(line, "45 min") {
		t.Errorf("the status of a schedule with a deadline has no `Deadline: 45 min` line:\n%s", out)
	}
	out = statusWithSchedule(t, `,"cron_deadline_minutes":null`)
	if line := lineWith(out, "Deadline"); line != "" {
		t.Errorf("a schedule without a deadline prints one: %q", line)
	}
}

func TestStatusJSONCarriesTheDeadline(t *testing.T) {
	for body, want := range map[string]interface{}{
		`"cron_schedule":"*/10 * * * *","cron_deadline_minutes":45`:   45.0,
		`"cron_schedule":"*/10 * * * *","cron_deadline_minutes":null`: nil,
	} {
		cp := newFakeCP(t)
		cp.on("GET", "/agents/bot", 200, `{"id":"a1","name":"bot","status":"running","agent_type":"job",`+
			`"created_at":"2026-09-01T10:00:00Z","updated_at":"2026-09-01T10:00:00Z",`+body+`}`)
		stdout, stderr, err := runAfy(t, cp, "status", "bot", "--json")
		if err != nil {
			t.Fatalf("status: %v\n%s", err, stderr)
		}
		doc, _ := oneJSONDocument(t, "afy status --json", stdout).(map[string]interface{})
		if v, present := doc["cron_deadline_minutes"]; !present || v != want {
			t.Errorf("status --json cron_deadline_minutes = %v (present %v), want %v", v, present, want)
		}
	}
}

// `afy run` says what became of the run the server accepted.
func TestRunSaysARunThatWaitsInTheLine(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("POST", "/agents/bot/run", 202, `{"deployment_id":"d-1","version":4,"job_id":null,"status":"waiting","position":3}`)
	stdout, stderr, err := runAfy(t, cp, "run", "bot")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "✓ Run accepted; waiting for a slot (#3 in line).") {
		t.Errorf("a waiting run is not said to wait, at its place:\n%s", stdout)
	}
	if strings.Contains(stdout, "Run started.") {
		t.Errorf("a waiting run is said to have started:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Follow it with: afy logs bot --run d-1") {
		t.Errorf("the follow-up line is gone:\n%s", stdout)
	}

	// THE CONTROL: a run that starts says so, with no place in any line.
	cp = newFakeCP(t)
	cp.on("POST", "/agents/bot/run", 202, `{"deployment_id":"d-1","version":4,"job_id":"j-1","status":"queued","position":null}`)
	stdout, stderr, err = runAfy(t, cp, "run", "bot")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "✓ Run started.") || strings.Contains(stdout, "in line") {
		t.Errorf("a run that starts is not said to start:\n%s", stdout)
	}
}

func TestRunJSONCarriesTheStatusPositionAndJob(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("POST", "/agents/bot/run", 202, `{"deployment_id":"d-1","version":4,"job_id":null,"status":"waiting","position":2}`)
	stdout, stderr, err := runAfy(t, cp, "run", "bot", "--json")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	doc, _ := oneJSONDocument(t, "afy run --json", stdout).(map[string]interface{})
	if doc["status"] != "waiting" || doc["position"] != 2.0 {
		t.Errorf("run --json = %v, want status waiting and position 2", doc)
	}
	if v, present := doc["job_id"]; !present || v != nil {
		t.Errorf("run --json job_id = %v (present %v), want an explicit null", v, present)
	}
}

// firstLineWith is the first line of out that contains s.
func firstLineWith(out, s string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, s) {
			return line
		}
	}
	return ""
}
