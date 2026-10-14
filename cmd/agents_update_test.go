package cmd

import (
	"strings"
	"testing"
)

// afy update <agent>: the platform's fields. A rename and a description are
// PATCH /agents/{agent} (name and description only: the control plane refuses
// every yaml-owned field there); a workspace is POST /agents/{agent}/move,
// which redeploys the agent.

const updatedAgent = `{"id":"7d1c2f4e-0000-4000-8000-000000000001","name":"invoice-parser","description":"","workspace_id":"w1","workspace_name":"default","url":"https://bot-k3m7x2.aetherfy.dev"}`

func TestUpdateNameSendsOnlyTheName(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("PATCH", "/agents/bot", 200, updatedAgent)

	stdout, _, err := runAfy(t, cp, "update", "bot", "--name", "invoice-parser")
	if err != nil {
		t.Fatalf("update --name: %v", err)
	}
	calls := cp.called("PATCH", "/agents/bot")
	if len(calls) != 1 {
		t.Fatalf("want one PATCH /agents/bot, got %+v", cp.all())
	}
	if len(calls[0].Body) != 1 || calls[0].Body["name"] != "invoice-parser" {
		t.Errorf("the PATCH must carry exactly {name}; got %v", calls[0].Body)
	}
	if len(cp.called("POST", "/agents/bot/move")) != 0 {
		t.Error("a rename must not move the agent")
	}
	if !strings.Contains(stdout, "does not move it") {
		t.Errorf("a rename must say the address stays; got:\n%s", stdout)
	}
}

func TestUpdateWorkspaceMovesThroughTheMoveRoute(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("POST", "/agents/bot/move", 200, `{"agent_id":"a1","agent":"bot",
		"workspace":{"id":"w2","name":"acme"},"previous_workspace":{"id":"w1","name":"default"},
		"changed":true,"redeployed":true,"deployment":{"id":"d9","agent_id":"a1","version":4,"state":"pending"}}`)

	stdout, _, err := runAfy(t, cp, "update", "bot", "--workspace", "acme")
	if err != nil {
		t.Fatalf("update --workspace: %v", err)
	}
	calls := cp.called("POST", "/agents/bot/move")
	if len(calls) != 1 {
		t.Fatalf("want one POST /agents/bot/move, got %+v", cp.all())
	}
	if len(calls[0].Body) != 1 || calls[0].Body["workspace_name"] != "acme" {
		t.Errorf("the move must carry exactly {workspace_name}; got %v", calls[0].Body)
	}
	if len(cp.called("PATCH", "/agents/bot")) != 0 {
		t.Error("a move must not PATCH the agent: the control plane refuses workspace_name there")
	}
	for _, want := range []string{"moved from workspace 'default' to 'acme'", "version 4", "conversation history stays in workspace 'default'"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

func TestUpdateRenameThenMoveAddressesTheAgentByID(t *testing.T) {
	// After a rename in the same command the old name is gone: the move must
	// address the agent by the id the rename answered with.
	cp := newFakeCP(t)
	cp.on("PATCH", "/agents/bot", 200, updatedAgent)
	cp.on("POST", "/agents/7d1c2f4e-0000-4000-8000-000000000001/move", 200, `{"agent_id":"a1","agent":"invoice-parser",
		"workspace":{"id":"w2","name":"acme"},"previous_workspace":{"id":"w1","name":"default"},
		"changed":true,"redeployed":false,"deployment":null}`)

	if _, _, err := runAfy(t, cp, "update", "bot", "--name", "invoice-parser", "--workspace", "acme"); err != nil {
		t.Fatalf("update --name --workspace: %v", err)
	}
	if len(cp.called("POST", "/agents/bot/move")) != 0 {
		t.Error("the move went to the OLD name")
	}
	if len(cp.called("POST", "/agents/7d1c2f4e-0000-4000-8000-000000000001/move")) != 1 {
		t.Errorf("the move did not go to the agent's id; calls: %+v", cp.all())
	}
}

func TestUpdateMoveRefusalPrintsTheServersSentenceUnaltered(t *testing.T) {
	// The frozen-account refusal and RELEASE_CONFIG_NOT_ON_RECORD are written
	// for the reader; the CLI prints them as the server wrote them.
	for _, tc := range []struct {
		status        int
		code, message string
	}{
		{403, "SOFT_CAP_EXCEEDED", "Your account is over its spend cap, so this agent can't be redeployed. Reduce always-on, memory or regions, or raise the cap."},
		{422, "RELEASE_CONFIG_NOT_ON_RECORD", "Version 3's configuration isn't on record; deploy it again from its source."},
		{409, "AGENT_PAUSED_CANNOT_MOVE", "Agent 'bot' is paused; resume it before moving it."},
	} {
		t.Run(tc.code, func(t *testing.T) {
			cp := newFakeCP(t)
			cp.on("POST", "/agents/bot/move", tc.status, `{"detail":{"code":"`+tc.code+`","message":"`+tc.message+`"}}`)
			_, stderr, err := runAfy(t, cp, "update", "bot", "--workspace", "acme")
			if err == nil {
				t.Fatal("a refused move must fail the command")
			}
			if !strings.Contains(stderr, "Error: "+tc.message+"\n") {
				t.Errorf("the server's sentence must be printed on its own, unaltered; stderr:\n%s", stderr)
			}
		})
	}
}

func TestUpdateHasNoNoWorkspace(t *testing.T) {
	cp := newFakeCP(t)
	_, _, err := runAfy(t, cp, "update", "bot", "--no-workspace")
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --no-workspace") {
		t.Fatalf("--no-workspace must be an unknown flag, got %v", err)
	}
	if len(cp.all()) != 0 {
		t.Errorf("nothing may be sent; got %+v", cp.all())
	}
}

func TestRenameAndPullAreGone(t *testing.T) {
	// `afy rename` is `afy update --name`; `afy pull` is `afy init --agent`.
	for _, name := range []string{"rename", "pull"} {
		for _, c := range rootCmd.Commands() {
			if c.Name() == name {
				t.Errorf("`afy %s` is still registered", name)
			}
		}
	}
}
