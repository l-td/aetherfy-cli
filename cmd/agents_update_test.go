package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
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

	stdout, _, err := runAfy(t, cp, "update", "bot", "--workspace", "acme", "--yes")
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
	cp.on("GET", "/agents/bot/access", 200, botAccessJSON)
	cp.on("POST", "/agents/bot/move", 200, movedBot)
	cp.on("PATCH", "/agents/7d1c2f4e-0000-4000-8000-000000000001", 200, updatedAgent)

	if _, stderr, err := runAfy(t, cp, "update", "bot", "--name", "invoice-parser", "--workspace", "acme", "--yes"); err != nil {
		t.Fatalf("update --name --workspace: %v\n%s", err, stderr)
	}
	calls := writesOf(cp)
	if len(calls) != 2 || calls[0].Path != "/agents/bot/move" || calls[1].Path != "/agents/7d1c2f4e-0000-4000-8000-000000000001" {
		t.Errorf("want the move, then the rename by id; calls: %+v", calls)
	}
}

// A refused move applies NOTHING: the rename is never sent.
func TestUpdateARefusedMoveAppliesNothing(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/agents/bot/access", 200, botAccessJSON)
	cp.on("POST", "/agents/bot/move", 409, `{"detail":{"code":"AGENT_PAUSED_CANNOT_MOVE","message":"Agent 'bot' is paused; resume it before moving it."}}`)

	_, stderr, err := runAfy(t, cp, "update", "bot", "--name", "invoice-parser", "--workspace", "acme", "--yes")
	if err == nil {
		t.Fatal("a refused move must fail the command")
	}
	if len(cp.called("PATCH", "/agents/bot")) != 0 || len(writesOf(cp)) != 1 {
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

	_, stderr, err := runAfy(t, cp, "update", "bot", "--name", "invoice-parser", "--workspace", "acme", "--yes")
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
			_, stderr, err := runAfy(t, cp, "update", "bot", "--workspace", "acme", "--yes")
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

// botAccessJSON is an agent in 'default' with everything a move touches: its own
// level set, the secrets switch on, a grant to the target ('acme') and two others.
const botAccessJSON = `{"agent":"bot","own_workspace":"default","own_workspace_ref":{"id":"w1","name":"default"},
	"own_workspace_level":"full","own_workspace_level_setting":"full","workspace_default_level":"read_write",
	"granted_workspaces":["acme","research","archive"],
	"granted_workspace_refs":[{"id":"w2","name":"acme","level":"read"},{"id":"w3","name":"research","level":"read_write"},{"id":"w4","name":"archive","level":"full"}],
	"allowed_workspaces":["default","acme","research","archive"],"allowed_workspace_refs":[],"secrets_access":true}`

// writesOf is every request that is not a read: what the command changed.
func writesOf(cp *fakeCP) []cpCall {
	var out []cpCall
	for _, c := range cp.all() {
		if c.Method != "GET" {
			out = append(out, c)
		}
	}
	return out
}

func accessOf(t *testing.T, body string) *api.AgentAccess {
	t.Helper()
	a := &api.AgentAccess{}
	if err := json.Unmarshal([]byte(body), a); err != nil {
		t.Fatal(err)
	}
	return a
}

// A move lists what it resets, what it keeps (by workspace and level) and that the
// old home is no longer reached: the sentences the dashboard's dialog and the docs
// page use word for word.
func TestMoveConsequencesNameTheResetsTheKeptGrantsAndTheOldHome(t *testing.T) {
	got := strings.Join(moveConsequences(accessOf(t, botAccessJSON), "acme"), "\n")
	for _, want := range []string{
		"Its own level in 'default' (full access) is cleared; in 'acme' it holds that workspace's default.",
		"Its secrets switch is turned off.",
		"Its grant to 'acme' is dropped: it is its home now.",
		"It keeps its other grants: 'research' (read and write access), 'archive' (full access).",
		"It no longer reaches 'default' unless it is granted there.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
}

func TestMoveConsequencesSayNothingAboutWhatIsNotSet(t *testing.T) {
	plain := `{"agent":"bot","own_workspace":"default","own_workspace_ref":{"id":"w1","name":"default"},
		"own_workspace_level":"read_write","own_workspace_level_setting":null,"workspace_default_level":"read_write",
		"granted_workspaces":[],"granted_workspace_refs":[],"allowed_workspaces":[],"allowed_workspace_refs":[],"secrets_access":false}`
	got := strings.Join(moveConsequences(accessOf(t, plain), "acme"), "\n")
	for _, not := range []string{"is cleared", "secrets switch", "is dropped"} {
		if strings.Contains(got, not) {
			t.Errorf("says %q about something that is not set:\n%s", not, got)
		}
	}
	if !strings.Contains(got, "It has no other grants.") || !strings.Contains(got, "no longer reaches 'default'") {
		t.Errorf("want the no-grants line and the old-home line:\n%s", got)
	}
}

func moveCP(t *testing.T) *fakeCP {
	cp := newFakeCP(t)
	cp.on("GET", "/agents/bot/access", 200, botAccessJSON)
	cp.on("POST", "/agents/bot/move", 200, movedBot)
	return cp
}

// Without --yes and without a terminal nobody can answer: refused, exit 2's rule,
// BEFORE anything is read or sent.
func TestUpdateMoveWithoutYesOrTerminalIsRefusedBeforeAnythingIsSent(t *testing.T) {
	onTerminal(t, false, "")
	cp := moveCP(t)
	_, stderr, err := runAfy(t, cp, "update", "bot", "--workspace", "acme")
	if err == nil {
		t.Fatal("a move nobody can confirm must be refused")
	}
	if len(cp.all()) != 0 {
		t.Errorf("sent requests: %+v", cp.all())
	}
	if !strings.Contains(stderr, "This moves agent 'bot' to workspace 'acme'; pass --yes to confirm without a prompt.") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

// --yes skips the question, not the list: it goes to stderr, the move goes ahead.
func TestUpdateMoveWithYesStillPrintsWhatItChanges(t *testing.T) {
	onTerminal(t, false, "")
	cp := moveCP(t)
	_, stderr, err := runAfy(t, cp, "update", "bot", "--workspace", "acme", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	for _, want := range []string{"Its secrets switch is turned off.", "It keeps its other grants:", "It no longer reaches 'default'"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "Move the agent?") {
		t.Errorf("--yes asked:\n%s", stderr)
	}
	if len(cp.called("POST", "/agents/bot/move")) != 1 {
		t.Error("the move was not sent")
	}
}

// A person at a terminal sees the list, then the question: "n" moves nothing.
func TestUpdateMoveAskedAtATerminalHonoursTheAnswer(t *testing.T) {
	for _, tc := range []struct {
		typed string
		moves bool
	}{{"y\n", true}, {"n\n", false}} {
		onTerminal(t, true, tc.typed)
		cp := moveCP(t)
		_, _, err := runAfy(t, cp, "update", "bot", "--workspace", "acme")
		sent := len(cp.called("POST", "/agents/bot/move")) == 1
		if sent != tc.moves || (err == nil) != tc.moves {
			t.Errorf("typed %q: moved=%v err=%v, want moved=%v", tc.typed, sent, err, tc.moves)
		}
	}
}

// The question carries the list, in order, before the prompt.
func TestUpdateMoveShowsTheListBeforeTheQuestion(t *testing.T) {
	onTerminal(t, true, "n\n")
	cp := moveCP(t)
	stdout, stderr, err := runAfy(t, cp, "update", "bot", "--workspace", "acme")
	if err == nil {
		t.Fatal("n must cancel")
	}
	text := stdout + stderr
	list, question := strings.Index(text, "It keeps its other grants"), strings.Index(text, "Move the agent?")
	if list < 0 || question < list {
		t.Errorf("want the list, then the question:\n%s", text)
	}
}

// An agent already in the target workspace has nothing to confirm: no question
// (the move answers "nothing changed").
func TestUpdateMoveAsksNothingWhenTheAgentAlreadyLivesThere(t *testing.T) {
	onTerminal(t, true, "")
	cp := newFakeCP(t)
	cp.on("GET", "/agents/bot/access", 200, botAccessJSON)
	cp.on("POST", "/agents/bot/move", 200, `{"agent_id":"a1","agent":"bot","workspace":{"id":"w1","name":"default"},
		"previous_workspace":{"id":"w1","name":"default"},"changed":false,"redeployed":false,"deployment":null}`)
	stdout, stderr, err := runAfy(t, cp, "update", "bot", "--workspace", "default")
	if err != nil {
		t.Fatalf("asked or failed: %v\n%s", err, stderr)
	}
	if strings.Contains(stdout+stderr, "Move the agent?") || !strings.Contains(stdout, "Nothing changed") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

// A list that cannot be read is said and does not block the move.
func TestUpdateMoveSaysWhenTheListCannotBeRead(t *testing.T) {
	onTerminal(t, false, "")
	cp := newFakeCP(t)
	cp.on("GET", "/agents/bot/access", 503, `{"detail":{"code":"SERVICE_UNAVAILABLE","message":"down"}}`)
	cp.on("POST", "/agents/bot/move", 200, movedBot)
	_, stderr, err := runAfy(t, cp, "update", "bot", "--workspace", "acme", "--yes")
	if err != nil {
		t.Fatalf("a list that cannot be read blocked the move: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "Could not read what the move changes") {
		t.Errorf("stderr %q", stderr)
	}
	if len(cp.called("POST", "/agents/bot/move")) != 1 {
		t.Error("the move was not sent")
	}
}
