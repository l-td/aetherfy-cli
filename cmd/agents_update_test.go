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

const movedBot = `{"agent_id":"7d1c2f4e-0000-4000-8000-000000000001","agent":"bot",
	"workspace":{"id":"w2","name":"acme"},"previous_workspace":{"id":"w1","name":"default"},
	"changed":true,"redeployed":false,"deployment":null}`

// --name with --workspace: the MOVE first (the step most likely refused), then
// the rename, by the id the move answered with.
func TestUpdateMovesFirstThenRenamesByID(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("POST", "/agents/bot/move", 200, movedBot)
	cp.on("PATCH", "/agents/7d1c2f4e-0000-4000-8000-000000000001", 200, updatedAgent)

	if _, stderr, err := runAfy(t, cp, "update", "bot", "--name", "invoice-parser", "--workspace", "acme"); err != nil {
		t.Fatalf("update --name --workspace: %v\n%s", err, stderr)
	}
	calls := cp.all()
	if len(calls) != 2 || calls[0].Path != "/agents/bot/move" || calls[1].Path != "/agents/7d1c2f4e-0000-4000-8000-000000000001" {
		t.Errorf("want the move, then the rename by id; calls: %+v", calls)
	}
}

// A refused move applies NOTHING: the rename is never sent.
func TestUpdateARefusedMoveAppliesNothing(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("POST", "/agents/bot/move", 409, `{"detail":{"code":"AGENT_PAUSED_CANNOT_MOVE","message":"Agent 'bot' is paused; resume it before moving it."}}`)

	_, stderr, err := runAfy(t, cp, "update", "bot", "--name", "invoice-parser", "--workspace", "acme")
	if err == nil {
		t.Fatal("a refused move must fail the command")
	}
	if len(cp.called("PATCH", "/agents/bot")) != 0 || len(cp.all()) != 1 {
		t.Errorf("nothing may be applied after a refused move; calls: %+v", cp.all())
	}
	if !strings.Contains(stderr, "Agent 'bot' is paused; resume it before moving it.") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

// A rename refused after a move that succeeded says exactly that, once.
func TestUpdateARenameRefusedAfterTheMoveSaysWhatHappened(t *testing.T) {
	const taken = "An agent named 'invoice-parser' already exists."
	cp := newFakeCP(t)
	cp.on("POST", "/agents/bot/move", 200, movedBot)
	cp.on("PATCH", "/agents/7d1c2f4e-0000-4000-8000-000000000001", 409, `{"detail":{"code":"AGENT_NAME_TAKEN","message":"`+taken+`"}}`)

	_, stderr, err := runAfy(t, cp, "update", "bot", "--name", "invoice-parser", "--workspace", "acme")
	if err == nil {
		t.Fatal("a refused rename must fail the command")
	}
	want := "Agent 'bot' moved to workspace 'acme'; rename refused: " + taken
	if !strings.Contains(stderr, want) || strings.Count(stderr, taken) != 1 {
		t.Errorf("want %q, once:\n%s", want, stderr)
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
			if n := strings.Count(stderr, tc.message); n != 1 {
				t.Errorf("the sentence must be shown once, got %d times:\n%s", n, stderr)
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
