package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// BENCHMARK RUN 4, N1, IN THE LIST. `afy list`'s Last Run column held the
// schedule's decision, so a run that exited 1 read "fired" under a header that
// says run. The column is the run's outcome now, and the schedule's decision
// appears only when it did not start a run, labelled as a tick.
func listRows(t *testing.T, agents string) map[string]string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(agents))
	}))
	t.Cleanup(srv.Close)

	var err error
	out := captureStdout(t, func() {
		err = printAgentList(api.NewClientWithURL(srv.URL, "afy_test_key"))
	})
	if err != nil {
		t.Fatalf("afy list failed: %v", err)
	}
	// A row is its line plus the continuation lines a multi-line cell adds, up
	// to the next agent's row or the end of the table.
	rows := map[string]string{}
	current := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" || strings.Contains(line, "Total:") {
			current = ""
			continue
		}
		for _, name := range []string{"ran-failed", "tick-skipped", "never-ran"} {
			if strings.Contains(line, name) {
				current = name
			}
		}
		if current != "" {
			rows[current] += line + "\n"
		}
	}
	return rows
}

func listAgent(name, extra string) string {
	return `{"id":"id-` + name + `","user_id":"u1","name":"` + name + `","status":"running",` +
		`"agent_type":"job","spawn_enabled":false,"deployed":true,"cron_schedule":"*/10 * * * *",` +
		`"created_at":"2026-09-01T10:00:00Z","updated_at":"2026-09-01T10:00:00Z"` + extra + `}`
}

func TestListLastRunIsTheRunsOutcomeAndATickOnlyWhenItDidNotFire(t *testing.T) {
	ago := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }
	rows := listRows(t, `[`+
		listAgent("ran-failed", `,"cron_last_status":"fired","cron_last_run_at":"`+ago(2*time.Minute)+`"`+
			`,"last_run":{"id":"r1","trigger_source":"cron","state":"failed","created_at":"`+ago(2*time.Minute)+`",`+
			`"error_message":"run exited with code 1","release_version":3}`)+`,`+
		listAgent("tick-skipped", `,"cron_last_status":"skipped","cron_last_run_at":"`+ago(5*time.Minute)+`"`+
			`,"last_run":{"id":"r2","trigger_source":"cron","state":"completed","created_at":"`+ago(time.Hour)+`"}`)+`,`+
		listAgent("never-ran", ``)+
		`]`)

	failed := rows["ran-failed"]
	if !strings.Contains(failed, "failed, 2m ago") {
		t.Errorf("the failed run's outcome is not in its row: %q", failed)
	}
	if strings.Contains(failed, "fired") || strings.Contains(failed, "tick") {
		t.Errorf("a tick that fired is printed as the run's outcome or as a tick: %q", failed)
	}
	skipped := rows["tick-skipped"]
	if !strings.Contains(skipped, "completed, 1h ago") || !strings.Contains(skipped, "tick skipped (5m ago)") {
		t.Errorf("the skipped tick must follow the last run's outcome, labelled a tick: %q", skipped)
	}
	if never := rows["never-ran"]; !strings.Contains(never, "never") {
		t.Errorf("a scheduled agent that never ran must say never: %q", never)
	}
}
