package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/config"
)

// A RUN READ ON ITS OWN SAYS WHICH RELEASE IT RAN (benchmark run 4, N3).
// `afy deployments` lists runs next to releases, and a run's Version is its
// place in the sequence, not the release it executed.
const deploymentsBody = `[` +
	`{"id":"run-4","agent_id":"a1","version":4,"release_version":1,"state":"completed","is_ephemeral":true,"created_at":"2026-09-17T12:20:09Z"},` +
	`{"id":"rel-1","agent_id":"a1","version":1,"release_version":null,"state":"active","created_at":"2026-09-17T12:00:00Z"}]`

func deploymentsOutput(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(deploymentsBody))
	}))
	t.Cleanup(srv.Close)
	var err error
	out := captureStdout(t, func() {
		err = printDeployments(api.NewClientWithURL(srv.URL, "afy_test_key"), "ticker")
	})
	if err != nil {
		t.Fatalf("afy deployments failed: %v", err)
	}
	return out
}

func TestDeploymentsNameTheReleaseARunExecuted(t *testing.T) {
	out := deploymentsOutput(t)
	var run, release string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		switch fields[0] {
		case "v4":
			run = line
		case "v1":
			release = line
		}
	}
	// A row is Version, the state cell (its mark and its word), then Release.
	if f := strings.Fields(run); len(f) < 4 || f[3] != "v1" {
		t.Errorf("the run's Release cell is not v1: %q\n%s", run, out)
	}
	if f := strings.Fields(release); len(f) < 4 || f[3] != "-" {
		t.Errorf("a release row must print a dash for Release: %q\n%s", release, out)
	}
}

func TestDeploymentsJSONCarriesReleaseVersionNullIncluded(t *testing.T) {
	previous := config.Get().OutputFormat
	config.SetOutputFormat("json")
	t.Cleanup(func() { config.SetOutputFormat(previous) })

	var rows []map[string]any
	if err := json.Unmarshal([]byte(deploymentsOutput(t)), &rows); err != nil {
		t.Fatalf("afy deployments -o json printed no JSON: %v", err)
	}
	if rows[0]["release_version"] != float64(1) {
		t.Errorf("the run's release_version is missing: %v", rows[0])
	}
	if value, present := rows[1]["release_version"]; !present || value != nil {
		t.Errorf("a release row must say release_version null: %v", rows[1])
	}
}
