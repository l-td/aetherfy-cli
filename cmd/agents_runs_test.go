package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// A SERVICE RUN HAS A DURATION TOO. It holds no machine of its own, so it has
// no machine timestamps; the control plane measures it from the dispatch being
// accepted to the outcome being reported and sends that as duration_seconds.
// The table must print it rather than read the absent machine fields as "no
// duration".
func runsTable(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/agents/catalog-api/runs") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	var err error
	out := captureStdout(t, func() {
		err = printAgentRuns(api.NewClientWithURL(srv.URL, "afy_test_key"), "catalog-api")
	})
	if err != nil {
		t.Fatalf("afy runs failed: %v", err)
	}
	return out
}

func TestRunsPrintsAServiceRunsDurationWithNoMachineTimestamps(t *testing.T) {
	out := runsTable(t, `[{"id":"run-svc","trigger_source":"cron","state":"completed",`+
		`"created_at":"2026-09-16T03:00:00Z","machine_started_at":null,`+
		`"machine_stopped_at":null,"duration_seconds":42}]`)

	if !strings.Contains(out, "42s") {
		t.Errorf("the service run's duration was not printed:\n%s", out)
	}
}

// THE CONTROL: a run the server gives no duration shows the placeholder, so the
// assertion above is about the value arriving, not about the column existing.
func TestRunsPrintsNoDurationWhenTheServerSendsNone(t *testing.T) {
	out := runsTable(t, `[{"id":"run-svc","trigger_source":"manual","state":"active",`+
		`"created_at":"2026-09-16T03:00:00Z","duration_seconds":null}]`)

	if strings.Contains(out, "42s") {
		t.Errorf("a run with no duration printed one:\n%s", out)
	}
	if !strings.Contains(out, "run-svc") {
		t.Errorf("the run row is missing:\n%s", out)
	}
}
