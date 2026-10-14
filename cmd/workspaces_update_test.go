package cmd

import (
	"strings"
	"testing"
)

// afy workspaces update <name> --name / --description, and the last-workspace
// refusal on delete: the shipped commands against a stand-in control plane.

const renamedWorkspace = `{"id":"w-1","name":"invoices","description":null,"regions":["us-east-1"],"agent_count":2,"created_at":"2026-10-01T00:00:00Z","updated_at":"2026-10-14T00:00:00Z"}`

func TestWorkspacesUpdateNameRenames(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("PATCH", "/workspaces/invoice-pipeline", 200, renamedWorkspace)

	stdout, stderr, err := runAfy(t, cp, "workspaces", "update", "invoice-pipeline", "--name", "invoices")
	if err != nil {
		t.Fatalf("rename: %v\n%s", err, stderr)
	}
	calls := cp.called("PATCH", "/workspaces/invoice-pipeline")
	if len(calls) != 1 || len(calls[0].Body) != 1 || calls[0].Body["name"] != "invoices" {
		t.Fatalf("want one PATCH carrying exactly {name}, got %+v", cp.all())
	}
	for _, want := range []string{"renamed to 'invoices'", "Its agents keep running", "must use 'invoices' now"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestWorkspacesUpdateDescriptionOnlyAndClearing(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("PATCH", "/workspaces/invoices", 200, renamedWorkspace)

	if _, _, err := runAfy(t, cp, "workspaces", "update", "invoices", "--description", ""); err != nil {
		t.Fatalf("clear description: %v", err)
	}
	calls := cp.called("PATCH", "/workspaces/invoices")
	if len(calls) != 1 || len(calls[0].Body) != 1 {
		t.Fatalf("want one PATCH carrying exactly {description}, got %+v", cp.all())
	}
	if v, ok := calls[0].Body["description"]; !ok || v != "" {
		t.Errorf(`--description "" must send an empty description, got %v`, calls[0].Body)
	}
}

func TestWorkspacesUpdateNeedsAFlag(t *testing.T) {
	cp := newFakeCP(t)
	_, stderr, err := runAfy(t, cp, "workspaces", "update", "invoices")
	if err == nil || !strings.Contains(stderr, "--name <new> and/or --description <text>") {
		t.Errorf("want the no-flag refusal, got %v\n%s", err, stderr)
	}
	if len(cp.all()) != 0 {
		t.Errorf("nothing may be sent: %+v", cp.all())
	}
}

func TestWorkspaceRefusalsPrintTheServersSentence(t *testing.T) {
	for _, c := range []struct {
		name, method, path string
		status             int
		code, message      string
		args               []string
	}{
		{"name taken", "PATCH", "/workspaces/invoices", 409, "WORKSPACE_NAME_TAKEN",
			"A workspace named 'acme' already exists.", []string{"workspaces", "update", "invoices", "--name", "acme"}},
		{"the last workspace", "DELETE", "/workspaces/default", 409, "WORKSPACE_IS_LAST",
			"'default' is your only workspace; every agent and collection lives in one, so it can't be deleted.",
			[]string{"workspaces", "delete", "default", "--force"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cp := newFakeCP(t)
			cp.on(c.method, c.path, c.status, `{"detail":{"code":"`+c.code+`","message":"`+c.message+`"}}`)
			_, stderr, err := runAfy(t, cp, c.args...)
			if err == nil {
				t.Fatal("a refusal must fail the command")
			}
			if !strings.Contains(stderr, "Error: "+c.message+"\n") {
				t.Errorf("the server's sentence must be printed unaltered:\n%s", stderr)
			}
		})
	}
}
