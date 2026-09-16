package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// A schedule whose ticks are being skipped read as healthy in the terminal: the
// benchmark of 2026-09-16 watched every tick skip behind a hung run while
// `afy status` printed `skipped` with no reason. The reason is the server's, so
// these tests send one of their own making and expect it back verbatim: the CLI
// must not know the list of reasons.
func statusWithSchedule(t *testing.T, extra string) string {
	t.Helper()
	body := `{"id":"a1","user_id":"u1","name":"tick","status":"running",` +
		`"agent_type":"job","spawn_enabled":false,"deployed":true,` +
		`"cron_schedule":"*/10 * * * *",` +
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

func lastRunLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Last run") {
			return line
		}
	}
	t.Fatalf("status printed no Last run line:\n%s", out)
	return ""
}

func TestStatusPrintsTheReasonTheLastTickRecorded(t *testing.T) {
	const reason = "a reason this test made up"
	out := statusWithSchedule(t,
		`,"cron_last_status":"skipped","cron_last_reason":"`+reason+`","cron_last_run_at":"2026-09-16T12:30:01Z"`)

	line := lastRunLine(t, out)
	if !strings.Contains(line, "skipped") || !strings.Contains(line, reason) {
		t.Errorf("the Last run line did not carry both the status and the server's reason: %q", line)
	}
}

// THE CONTROL. The test above passes against a line that prints any reason
// field unconditionally; a tick with no recorded reason must print none.
func TestStatusOfATickWithNoReasonPrintsNoReason(t *testing.T) {
	out := statusWithSchedule(t,
		`,"cron_last_status":"fired","cron_last_run_at":"2026-09-16T12:10:01Z"`)

	line := lastRunLine(t, out)
	if !strings.Contains(line, "fired") {
		t.Errorf("the Last run line lost the status: %q", line)
	}
	if strings.Contains(line, "—") {
		t.Errorf("a tick with no recorded reason printed a reason separator: %q", line)
	}
}
