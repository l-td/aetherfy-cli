package cmd

import (
	"strings"
	"testing"
)

// afy workspaces update --agent-access-level and the levels in afy workspaces
// agents (grants round 1; aetherfy-control-plane docs/AGENT_ACCESS_LEVELS.md),
// against a stand-in control plane.

func workspaceAt(level string) string {
	return `{"id":"w-1","name":"invoices","description":null,"regions":["us-east-1"],"agent_count":2,` +
		`"agent_access_level":"` + level + `","created_at":"2026-10-01T00:00:00Z","updated_at":"2026-10-14T00:00:00Z"}`
}

func TestWorkspacesUpdateLoweringTheAgentLevelAsksNothing(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/workspaces/invoices", 200, workspaceAt("full"))
	cp.on("PATCH", "/workspaces/invoices", 200, workspaceAt("read"))
	stdout, stderr, err := runAfy(t, cp, "workspaces", "update", "invoices", "--agent-access-level", "read")
	if err != nil {
		t.Fatalf("lower: %v\n%s", err, stderr)
	}
	calls := cp.called("PATCH", "/workspaces/invoices")
	if len(calls) != 1 || len(calls[0].Body) != 1 || calls[0].Body["agent_access_level"] != "read" {
		t.Fatalf("want one PATCH carrying exactly {agent_access_level}, got %+v", cp.all())
	}
	if strings.Contains(stderr, "Proceed?") || !strings.Contains(stdout, "Agent access level") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}

func TestWorkspacesUpdateRaisingTheAgentLevelNeedsYesWhereNobodyCanAnswer(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/workspaces/invoices", 200, workspaceAt("read"))
	_, stderr, err := runAfy(t, cp, "workspaces", "update", "invoices", "--agent-access-level", "full")
	if err == nil || !strings.Contains(stderr, "raising its agents' level from read to full") {
		t.Fatalf("want the refusal, got %v\n%s", err, stderr)
	}
	if n := len(cp.called("PATCH", "/workspaces/invoices")); n != 0 {
		t.Errorf("raised without a yes: %+v", cp.all())
	}

	cp2 := newFakeCP(t)
	cp2.on("GET", "/workspaces/invoices", 200, workspaceAt("read"))
	cp2.on("PATCH", "/workspaces/invoices", 200, workspaceAt("full"))
	if _, stderr2, err := runAfy(t, cp2, "workspaces", "update", "invoices", "--agent-access-level", "full", "--yes"); err != nil {
		t.Fatalf("raise --yes: %v\n%s", err, stderr2)
	}
	if calls := cp2.called("PATCH", "/workspaces/invoices"); len(calls) != 1 || calls[0].Body["agent_access_level"] != "full" {
		t.Fatalf("PATCH = %+v", cp2.all())
	}
}

func TestWorkspacesUpdateALevelOutsideTheThreeIsRefusedBeforeAnyRequest(t *testing.T) {
	cp := newFakeCP(t)
	_, stderr, err := runAfy(t, cp, "workspaces", "update", "invoices", "--agent-access-level", "admin")
	if err == nil || !strings.Contains(stderr, "--agent-access-level must be one of read, read_write, full") {
		t.Fatalf("want the refusal, got %v\n%s", err, stderr)
	}
	if len(cp.all()) != 0 {
		t.Errorf("nothing may be sent: %+v", cp.all())
	}
}

func TestWorkspacesAgentsShowsEachAgentsLevel(t *testing.T) {
	living := `[
		{"id":"a-1","name":"invoice-parser","agent_type":"service","status":"running","access_level":"full","created_at":"2026-10-01T10:00:00Z"},
		{"id":"a-2","name":"nightly-report","agent_type":"job","status":"stopped","access_level":"read","access_level_setting":"read","created_at":"2026-10-02T11:30:00Z"}]`
	granted := `[{"id":"a-9","name":"support-bot","granted_at":"2026-10-13T09:30:00+00:00","level":"read_write"}]`
	cp := agentsCP(t, living, granted)
	stdout, stderr, err := runAfy(t, cp, "workspaces", "agents", "acme")
	if err != nil {
		t.Fatalf("workspaces agents: %v\n%s", err, stderr)
	}
	for _, want := range []string{"full (default)", "read_write"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "read (default)") {
		t.Errorf("an agent set to read does not follow the default:\n%s", stdout)
	}
}
