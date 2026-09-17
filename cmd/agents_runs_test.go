package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/config"
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

// BENCHMARK RUNS 3 AND 4. The table printed local time with no zone while
// `-o json` printed UTC, so the same run read two hours apart. The table prints
// the JSON's instant, in UTC, marked.
func TestRunsTablePrintsTheJSONsInstantInUTC(t *testing.T) {
	// The terminal's zone is not UTC here, as it was not for the benchmark: on a
	// UTC machine local time and UTC print the same, and this could not fail.
	previous := time.Local
	time.Local = time.FixedZone("CEST", 2*3600)
	t.Cleanup(func() { time.Local = previous })

	out := runsTable(t, `[{"id":"run-utc","trigger_source":"cron","state":"completed",`+
		`"created_at":"2026-09-17T12:00:21Z","duration_seconds":6}]`)

	if !strings.Contains(out, "2026-09-17 12:00 UTC") {
		t.Errorf("the run's time is not the UTC instant, marked:\n%s", out)
	}
}

// BENCHMARK RUN 4, N3. Which release a run executed was inferred from
// timestamps; each row now names it, and a run the server recorded none for
// says so rather than borrowing a number.
func TestRunsTableNamesTheReleaseEachRunExecuted(t *testing.T) {
	out := runsTable(t, `[`+
		`{"id":"run-new","trigger_source":"cron","state":"failed","created_at":"2026-09-17T12:20:09Z","release_version":3},`+
		`{"id":"run-old","trigger_source":"cron","state":"completed","created_at":"2026-09-17T12:00:21Z","release_version":null}]`)

	rows := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		for _, id := range []string{"run-new", "run-old"} {
			if strings.Contains(line, id) {
				rows[id] = line
			}
		}
	}
	if !strings.Contains(rows["run-new"], "v3") {
		t.Errorf("the run that executed v3 does not say so: %q", rows["run-new"])
	}
	if strings.Contains(rows["run-old"], "v3") || !strings.Contains(rows["run-old"], " - ") {
		t.Errorf("a run with no recorded release must print a dash: %q", rows["run-old"])
	}
}

func TestRunsJSONCarriesTheReleaseAndSaysNullWhenThereIsNone(t *testing.T) {
	previous := config.Get().OutputFormat
	config.SetOutputFormat("json")
	t.Cleanup(func() { config.SetOutputFormat(previous) })

	out := runsTable(t, `[`+
		`{"id":"run-new","trigger_source":"cron","state":"failed","created_at":"2026-09-17T12:20:09Z","release_version":3},`+
		`{"id":"run-old","trigger_source":"cron","state":"completed","created_at":"2026-09-17T12:00:21Z","release_version":null}]`)

	var runs []map[string]any
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		t.Fatalf("afy runs -o json printed no JSON: %v\n%s", err, out)
	}
	if len(runs) != 2 || runs[0]["release_version"] != float64(3) {
		t.Fatalf("the first run's release is missing: %v", runs)
	}
	if value, present := runs[1]["release_version"]; !present || value != nil {
		t.Errorf("a run with no recorded release must say null, not drop the key: %v", runs[1])
	}
}
