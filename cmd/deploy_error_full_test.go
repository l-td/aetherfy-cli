package cmd

import (
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// A failed deploy's sentence is readable in full in the CLI: `afy status`
// shows the newest release deploy's error_message whole, and `afy
// deployments` prints whole every error its table cut.

// pushWithoutYAML is the shape of the control plane's push refusal
// (shared/github_webhook_processor.py): repo, commit, branch and path named,
// far past the table's width, with a multi-byte character inside.
const pushWithoutYAML = "No aetherfy.yaml in myorg/agents at 3f9c2e1 (branch production), looked in services/scraper/aetherfy.yaml — add it beside your code with at least `runtime:`."

func statusAgainst(t *testing.T, deployments string) string {
	t.Helper()
	cp := newFakeCP(t)
	cp.on("GET", "/agents/bot", 200, `{"id":"a1","name":"bot","status":"running","agent_type":"service","created_at":"2026-09-01T10:00:00Z","updated_at":"2026-09-01T10:00:00Z"}`)
	cp.on("GET", "/agents/a1/deployments", 200, deployments)
	var err error
	out := captureStdout(t, func() {
		err = showAgentStatus(api.NewClientWithURL(cp.srv.URL+"/api/v1", "afy_test_key"), "bot")
	})
	if err != nil {
		t.Fatalf("afy status: %v", err)
	}
	return out
}

func TestStatusShowsAPushDeployFailureInFull(t *testing.T) {
	out := statusAgainst(t, `[
		{"id":"d3","agent_id":"a1","version":3,"state":"failed","error_message":"`+pushWithoutYAML+`","created_at":"2026-10-14T10:00:00Z"},
		{"id":"d2","agent_id":"a1","version":2,"state":"superseded","created_at":"2026-10-13T10:00:00Z"}]`)
	if !strings.Contains(out, "v3 failed") || !strings.Contains(out, pushWithoutYAML) {
		t.Errorf("afy status must show the failed deploy's sentence whole:\n%s", out)
	}
}

func TestStatusSaysNothingWhenTheNewestReleaseDeploySucceeded(t *testing.T) {
	// A failed RUN (ephemeral) newer than a good release is `Last run`'s to
	// report, not a failed deploy.
	out := statusAgainst(t, `[
		{"id":"r9","agent_id":"a1","version":9,"state":"failed","error_message":"run blew up","is_ephemeral":true,"created_at":"2026-10-14T11:00:00Z"},
		{"id":"d3","agent_id":"a1","version":3,"state":"active","created_at":"2026-10-14T10:00:00Z"}]`)
	if strings.Contains(out, "Last deploy") || strings.Contains(out, "Deploy error") {
		t.Errorf("no failed deploy to report:\n%s", out)
	}
}

func TestDeploymentsPrintsEveryCutErrorInFull(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/agents/bot/deployments", 200, `[
		{"id":"d3","agent_id":"a1","version":3,"state":"failed","error_message":"`+pushWithoutYAML+`","created_at":"2026-10-14T10:00:00Z"},
		{"id":"d2","agent_id":"a1","version":2,"state":"failed","error_message":"short reason","created_at":"2026-10-13T10:00:00Z"}]`)
	stdout, _, err := runAfy(t, cp, "deployments", "bot")
	if err != nil {
		t.Fatalf("deployments: %v", err)
	}
	if !strings.Contains(stdout, "v3 error: "+pushWithoutYAML+"\n") {
		t.Errorf("the cut sentence must be printed whole below the table:\n%s", stdout)
	}
	if strings.Contains(stdout, "v2 error:") {
		t.Errorf("an error the table showed whole is not repeated:\n%s", stdout)
	}
}

func TestShortenErrorCutsOnCharacters(t *testing.T) {
	long := strings.Repeat("—", 80)
	got, cut := shortenError(long)
	if !cut || got != strings.Repeat("—", errorColumnWidth-3)+"..." {
		t.Errorf("shortenError cut %q (cut=%v), want %d dashes and an ellipsis", got, cut, errorColumnWidth-3)
	}
	if got, cut := shortenError("short"); cut || got != "short" {
		t.Errorf("a short error must pass unchanged, got %q %v", got, cut)
	}
}
