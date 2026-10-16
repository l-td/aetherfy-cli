package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// afy workspaces agents <workspace>: "Lives here" (GET /workspaces/{name}/agents)
// and "Granted access" (GET /workspaces/{name}/granted-agents, sheet 2.12),
// against a stand-in control plane.

const (
	livingTwo = `[
		{"id":"a-1","name":"invoice-parser","agent_type":"service","status":"running","created_at":"2026-10-01T10:00:00Z"},
		{"id":"a-2","name":"nightly-report","agent_type":"job","status":"stopped","created_at":"2026-10-02T11:30:00Z"}]`
	grantedOne = `[{"id":"a-9","name":"support-bot","granted_at":"2026-10-13T09:30:00.123456+00:00"}]`
)

func agentsCP(t *testing.T, living, granted string) *fakeCP {
	cp := newFakeCP(t)
	cp.on("GET", "/workspaces/acme/agents", 200, living)
	cp.on("GET", "/workspaces/acme/granted-agents", 200, granted)
	return cp
}

func TestWorkspacesAgentsShowsBothSections(t *testing.T) {
	cp := agentsCP(t, livingTwo, grantedOne)
	stdout, stderr, err := runAfy(t, cp, "workspaces", "agents", "acme")
	if err != nil {
		t.Fatalf("workspaces agents: %v\n%s", err, stderr)
	}
	for _, want := range []string{
		"Lives here (2)", "invoice-parser", "nightly-report",
		"Granted access (1)", "support-bot", "2026-10-13 09:30 UTC",
		"it does not move it",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if lives, granted := strings.Index(stdout, "Lives here"), strings.Index(stdout, "Granted access"); lives < 0 || granted < lives {
		t.Errorf("Lives here must come before Granted access:\n%s", stdout)
	}
	// support-bot is a GRANTED agent: it must not be listed among those that live here.
	if strings.Index(stdout, "support-bot") < strings.Index(stdout, "Granted access") {
		t.Errorf("a granted agent appears under Lives here:\n%s", stdout)
	}
	if len(cp.called("GET", "/workspaces/acme/agents")) != 1 || len(cp.called("GET", "/workspaces/acme/granted-agents")) != 1 {
		t.Errorf("each list is read once; calls: %+v", cp.all())
	}
}

// An empty section says so in ONE line, and never hides the other section.
func TestWorkspacesAgentsEmptySectionsSaySoInOneLine(t *testing.T) {
	t.Run("no granted agents", func(t *testing.T) {
		stdout, _, err := runAfy(t, agentsCP(t, livingTwo, `[]`), "workspaces", "agents", "acme")
		if err != nil {
			t.Fatal(err)
		}
		const line = "Granted access: no agents from other workspaces have access to this one"
		if strings.Count(stdout, line) != 1 || !strings.Contains(stdout, "Lives here (2)") {
			t.Errorf("want the one-line empty granted section and the living table:\n%s", stdout)
		}
	})
	t.Run("nothing lives here, agents are granted in", func(t *testing.T) {
		stdout, _, err := runAfy(t, agentsCP(t, `[]`, grantedOne), "workspaces", "agents", "acme")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, "Lives here: no agents (move one in with: afy update <agent> --workspace acme)") {
			t.Errorf("want the one-line empty living section:\n%s", stdout)
		}
		if !strings.Contains(stdout, "Granted access (1)") || !strings.Contains(stdout, "support-bot") {
			t.Errorf("an empty living list must not hide the granted agents:\n%s", stdout)
		}
		if strings.Contains(stdout, "workspace: acme") {
			t.Errorf("the hint must not advise the yaml `workspace:` key, which a deploy refuses:\n%s", stdout)
		}
	})
}

func TestWorkspacesAgentsJSONShape(t *testing.T) {
	stdout, stderr, err := runAfy(t, agentsCP(t, livingTwo, grantedOne), "workspaces", "agents", "acme", "--json")
	if err != nil {
		t.Fatalf("json: %v\n%s", err, stderr)
	}
	var got struct {
		Living  []map[string]interface{} `json:"living"`
		Granted []map[string]interface{} `json:"granted"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, stdout)
	}
	if len(got.Living) != 2 || got.Living[0]["name"] != "invoice-parser" {
		t.Errorf("living = %v", got.Living)
	}
	if len(got.Granted) != 1 || got.Granted[0]["id"] != "a-9" || got.Granted[0]["name"] != "support-bot" ||
		got.Granted[0]["granted_at"] != "2026-10-13T09:30:00.123456Z" {
		t.Errorf("granted = %v, want {id, name, granted_at}", got.Granted)
	}
}

// Both lists empty: still the same object, with arrays, never null and never
// the text message.
func TestWorkspacesAgentsJSONEmptyListsAreArrays(t *testing.T) {
	// `[]` and `null` are both an empty list to a client; only the second
	// decodes to a nil slice, which would print as null without the guard.
	for _, body := range []string{`[]`, `null`} {
		stdout, _, err := runAfy(t, agentsCP(t, body, body), "workspaces", "agents", "acme", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, stdout)
		}
		for _, key := range []string{"living", "granted"} {
			if strings.TrimSpace(string(raw[key])) != "[]" {
				t.Errorf("server said %s: %s = %s, want []", body, key, raw[key])
			}
		}
	}
}

// An unknown workspace: the server's sentence, once, a failing exit, and no
// "success" output from the other list.
func TestWorkspacesAgentsUnknownWorkspaceSaysSo(t *testing.T) {
	const sentence = "Workspace 'nope' not found"
	notFound := `{"detail":{"code":"WORKSPACE_NOT_FOUND","message":"` + sentence + `","workspace":"nope"}}`
	for name, routes := range map[string]map[string]string{
		"the living list refuses":  {"/workspaces/nope/agents": notFound},
		"the granted list refuses": {"/workspaces/nope/agents": `[]`, "/workspaces/nope/granted-agents": notFound},
	} {
		t.Run(name, func(t *testing.T) {
			cp := newFakeCP(t)
			for path, body := range routes {
				status := 200
				if body == notFound {
					status = 404
				}
				cp.on("GET", path, status, body)
			}
			stdout, stderr, err := runAfy(t, cp, "workspaces", "agents", "nope")
			if err == nil {
				t.Fatal("an unknown workspace must fail the command")
			}
			if !strings.Contains(stderr, "Error: "+sentence+"\n") || strings.Count(stderr, sentence) != 1 {
				t.Errorf("want the server's sentence, once:\n%s", stderr)
			}
			if strings.Contains(stdout, "Lives here") || strings.Contains(stdout, "Granted access") {
				t.Errorf("no section may be printed for a workspace that was refused:\n%s", stdout)
			}
		})
	}
}
