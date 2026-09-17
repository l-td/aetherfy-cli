package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// A schedule whose ticks are being skipped read as healthy in the terminal: the
// benchmark of 2026-09-16 watched every tick skip behind a hung run while
// `afy status` printed `skipped` with no reason. The reason is the server's, so
// these tests send one of their own making and expect it back verbatim: the CLI
// must not know the list of reasons.
func statusWithSchedule(t *testing.T, extra string) string {
	t.Helper()
	return statusOf(t, `,"cron_schedule":"*/10 * * * *"`+extra)
}

// statusOf runs `afy status` against a server answering with one agent, whose
// JSON is the base record plus `extra`.
func statusOf(t *testing.T, extra string) string {
	t.Helper()
	body := `{"id":"a1","user_id":"u1","name":"tick","status":"running",` +
		`"agent_type":"job","spawn_enabled":false,"deployed":true,` +
		`"created_at":"2026-09-01T10:00:00Z","updated_at":"2026-09-01T10:00:00Z"` + extra + `}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	var err error
	out := captureStdout(t, func() {
		err = showAgentStatus(api.NewClientWithURL(srv.URL, "afy_test_key"), "tick")
	})
	if err != nil {
		t.Fatalf("afy status failed: %v", err)
	}
	return out
}

// lineWith returns the status line labelled `key`, or "" when there is none.
func lineWith(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, key+":") {
			return line
		}
	}
	return ""
}

func TestStatusPrintsTheReasonTheLastTickRecorded(t *testing.T) {
	const reason = "a reason this test made up"
	out := statusWithSchedule(t,
		`,"cron_last_status":"skipped","cron_last_reason":"`+reason+`","cron_last_run_at":"2026-09-16T12:30:01Z"`)

	line := lineWith(out, "Last tick")
	if !strings.Contains(line, "skipped") || !strings.Contains(line, reason) {
		t.Errorf("the Last tick line did not carry both the decision and the server's reason: %q\n%s", line, out)
	}
}

// THE CONTROL. The test above passes against a line that prints any reason
// field unconditionally; a tick with no recorded reason must print none.
func TestStatusOfATickWithNoReasonPrintsNoReason(t *testing.T) {
	out := statusWithSchedule(t, `,"cron_last_status":"missed","cron_last_run_at":"2026-09-16T12:10:01Z"`)

	line := lineWith(out, "Last tick")
	if !strings.Contains(line, "missed") {
		t.Errorf("the Last tick line lost the decision: %q", line)
	}
	if strings.Contains(line, "—") {
		t.Errorf("a tick with no recorded reason printed a reason separator: %q", line)
	}
}

// BENCHMARK RUN 4, N1. After a run failed, `afy status` printed
// "Last run: fired (just now)": fired is the schedule's decision, and the run's
// reason was only in `afy runs`. The Last run line is the RUN's outcome now.
func TestStatusPrintsTheLastRunsFailureAndItsReason(t *testing.T) {
	started := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	out := statusWithSchedule(t,
		`,"cron_last_status":"fired","cron_last_run_at":"`+started+`"`+
			`,"last_run":{"id":"r1","trigger_source":"cron","state":"failed",`+
			`"created_at":"`+started+`","error_message":"run exited with code 1","release_version":3}`)

	line := lineWith(out, "Last run")
	if !strings.Contains(line, "failed (run exited with code 1), 2m ago") {
		t.Errorf("the Last run line does not say the run failed and why: %q\n%s", line, out)
	}
	if strings.Contains(line, "fired") {
		t.Errorf("the Last run line still reports the schedule's decision: %q", line)
	}
	// A tick that fired is not news: the run's own line says what happened.
	if tick := lineWith(out, "Last tick"); tick != "" {
		t.Errorf("a fired tick printed its own line: %q", tick)
	}
}

// THE CONTROL: a run that completed carries no reason, so the parenthesis above
// is the failure's message arriving, not a decoration every state gets.
func TestStatusOfACompletedRunPrintsNoReason(t *testing.T) {
	started := time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339)
	out := statusOf(t,
		`,"last_run":{"id":"r1","trigger_source":"manual","state":"completed","created_at":"`+started+`"}`)

	line := lineWith(out, "Last run")
	if !strings.Contains(line, "completed, 5m ago") || strings.Contains(line, "(") {
		t.Errorf("a completed run's line is wrong: %q\n%s", line, out)
	}
}

func TestStatusOfAScheduleThatNeverRanSaysNever(t *testing.T) {
	out := statusWithSchedule(t, ``)
	if line := lineWith(out, "Last run"); !strings.Contains(line, "never") {
		t.Errorf("a schedule with no run did not say never: %q\n%s", line, out)
	}
}

func TestStatusOfAnAgentWithNoScheduleAndNoRunPrintsNoLastRun(t *testing.T) {
	out := statusOf(t, ``)
	if line := lineWith(out, "Last run"); line != "" {
		t.Errorf("an agent that has never run and has no schedule printed %q", line)
	}
}
