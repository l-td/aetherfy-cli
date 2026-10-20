package cmd

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// afy create <name> [--workspace <ws>] [-d]: POST /agents with the name, the
// description and the workspace BY ID, nothing else.

const (
	oneWorkspace  = `[{"id":"w-1","name":"default","regions":["us-east-1"]}]`
	twoWorkspaces = `[{"id":"w-2","name":"acme","regions":["us-east-1"]},{"id":"w-1","name":"default","regions":["us-east-1"]}]`
	createdAgent  = `{"id":"a-1","name":"invoice-parser","workspace_id":"w-1","workspace_name":"default"}`
)

func TestCreateUsesTheOnlyWorkspaceAndSendsItsID(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/workspaces", 200, oneWorkspace)
	cp.on("POST", "/agents", 201, createdAgent)

	stdout, _, err := runAfy(t, cp, "create", "invoice-parser", "-d", "Parses invoices")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	posts := cp.called("POST", "/agents")
	if len(posts) != 1 {
		t.Fatalf("want one POST /agents, got %+v", cp.all())
	}
	b := posts[0].Body
	if len(b) != 3 || b["name"] != "invoice-parser" || b["description"] != "Parses invoices" || b["workspace_id"] != "w-1" {
		t.Errorf("body = %v, want exactly {name, description, workspace_id: w-1}", b)
	}
	if !strings.Contains(stdout, "afy init --agent invoice-parser") {
		t.Errorf("the next step must be linking a folder; stdout:\n%s", stdout)
	}
}

func TestCreateWithWorkspaceSendsThatWorkspacesID(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/workspaces/acme", 200, `{"id":"w-2","name":"acme","regions":["us-east-1"]}`)
	cp.on("POST", "/agents", 201, createdAgent)

	if _, _, err := runAfy(t, cp, "create", "invoice-parser", "--workspace", "acme"); err != nil {
		t.Fatalf("create: %v", err)
	}
	posts := cp.called("POST", "/agents")
	if len(posts) != 1 || posts[0].Body["workspace_id"] != "w-2" {
		t.Errorf("POST /agents = %+v, want workspace_id w-2", posts)
	}
	if _, sent := posts[0].Body["workspace_name"]; sent {
		t.Error("the workspace goes by id, never by name")
	}
}

func TestCreateWithSeveralWorkspacesAndNoTerminalIsRefused(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/workspaces", 200, twoWorkspaces)

	_, stderr, err := runAfy(t, cp, "create", "invoice-parser")
	if err == nil || ExitCode(err) != exitInputRefused {
		t.Fatalf("want exit %d, got %v", exitInputRefused, err)
	}
	if !strings.Contains(stderr, "pass --workspace <name>") || !strings.Contains(stderr, "acme, default") {
		t.Errorf("the refusal must name the flag and the workspaces; stderr:\n%s", stderr)
	}
	if len(cp.called("POST", "/agents")) != 0 {
		t.Error("an agent was created")
	}
}

func TestCreateNoLongerTakesTheYamlOwnedFlags(t *testing.T) {
	for _, flag := range []string{"--type", "--runtime", "--spawn-enabled", "--spawn-workers"} {
		cp := newFakeCP(t)
		_, _, err := runAfy(t, cp, "create", "bot", flag, "x")
		if err == nil || !strings.Contains(err.Error(), "unknown flag: "+flag) {
			t.Errorf("%s: want unknown flag, got %v", flag, err)
		}
	}
}

func TestPickWorkspaceAsksOnATerminal(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/workspaces", 200, twoWorkspaces)
	client := api.NewClientWithURL(cp.srv.URL+"/api/v1", "afy_test_key")

	for answer, want := range map[string]string{"2\n": "default", "acme\n": "acme", "\n": "acme"} {
		var out bytes.Buffer
		ws, err := pickWorkspace(client, "", false, true, &prompter{in: bufio.NewReader(strings.NewReader(answer)), out: &out})
		if err != nil || ws.Name != want {
			t.Errorf("answer %q: got %v, %v; want %s", answer, ws, err, want)
		}
		if !strings.Contains(out.String(), "1) acme") || !strings.Contains(out.String(), "2) default") {
			t.Errorf("the question must list the workspaces by number:\n%s", out.String())
		}
	}
	for _, answer := range []string{"9\n", ""} {
		_, err := pickWorkspace(client, "", false, true, &prompter{in: bufio.NewReader(strings.NewReader(answer)), out: &bytes.Buffer{}})
		if ExitCode(err) != exitInputRefused {
			t.Errorf("answer %q (outside the list, or no input) must be refused, got %v", answer, err)
		}
	}
}
