package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// afy access, against a fake control plane (the real client, pointed at it):
// the same conventions as afy collections regions -- a preview, "Proceed?
// [y/N]" before widening, --yes, and --json or no terminal requiring --yes.

const (
	accessPath        = "/api/v1/agents/bot/access"
	accessWsPath      = accessPath + "/workspaces"
	botInAlphaGranted = `{"agent":"bot","own_workspace":"alpha","own_workspace_level":"full",` +
		`"own_workspace_level_setting":null,"workspace_default_level":"full","granted_workspaces":["beta"],` +
		`"granted_workspace_refs":[{"id":"b","name":"beta","level":"read"}],` +
		`"allowed_workspaces":["alpha","beta"],"secrets_access":false,"changed":null}`
	botAfterGamma = `{"agent":"bot","own_workspace":"alpha","own_workspace_level":"full",` +
		`"own_workspace_level_setting":null,"workspace_default_level":"full","granted_workspaces":["beta","gamma"],` +
		`"granted_workspace_refs":[{"id":"b","name":"beta","level":"read"},{"id":"g","name":"gamma","level":"read"}],` +
		`"allowed_workspaces":["alpha","beta","gamma"],"secrets_access":false,"changed":true}`
)

// accessCP answers the access routes; every call is recorded by the server.
func accessCP(t *testing.T) *vecServer {
	ok := func(body string) func(vecRequest) (int, string) {
		return func(vecRequest) (int, string) { return 200, body }
	}
	return cpFake(t, map[string]func(vecRequest) (int, string){
		"GET " + accessPath:                ok(botInAlphaGranted),
		"POST " + accessWsPath:             ok(botAfterGamma),
		"DELETE " + accessWsPath + "/beta": ok(botInAlphaGranted),
	})
}

// noVectors fails the test if the vectors API is asked anything: access is a
// control-plane command.
func noVectors(t *testing.T) *vecServer {
	return newVecServer(t, func(r vecRequest) (int, string) {
		t.Errorf("unexpected vectors request %s %s", r.Method, r.Path)
		return 500, `{}`
	})
}

func runAccess(t *testing.T, asJSON bool) (*vecRun, *vecServer, *strings.Builder, *strings.Builder) {
	cp := accessCP(t)
	r, out, errOut := runWithCP(noVectors(t), cp, asJSON)
	return r, cp, out, errOut
}

func TestAccessListsOwnThenGranted(t *testing.T) {
	r, cp, out, errOut := runAccess(t, false)
	if code := agentsAccess(r, "bot", accessChange{add: nil, remove: nil, yes: false}, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for _, want := range []string{"Agent 'bot':", "Lives in: alpha  (full access, the workspace's default)",
		"Can access:", "beta  (read access)", "Secrets: off"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q:\n%s", want, out.String())
		}
	}
	if len(cp.got()) != 1 {
		t.Errorf("a list sent %d requests", len(cp.got()))
	}
}

func TestAccessAddShowsWhatBecomesReachableAndAsks(t *testing.T) {
	r, cp, out, errOut := runAccess(t, false)
	if code := agentsAccess(r, "bot", accessChange{add: []string{"gamma"}, remove: nil, yes: false}, strings.NewReader("y\n"), true); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Adds workspace 'gamma' at read access, every collection in it, including ones added later.") ||
		!strings.Contains(out.String(), "This lets the agent's code do more in gamma. Read access: can search, query, retrieve "+
			"and count points, and read a collection's information and schema.") {
		t.Errorf("preview:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "Proceed? [y/N]") {
		t.Errorf("the question goes to stderr: %q", errOut.String())
	}
	reqs := requestsTo(cp, "POST", accessWsPath)
	if len(reqs) != 1 || reqs[0].Body["workspace"] != "gamma" {
		t.Fatalf("grant requests = %v", reqs)
	}
	if !strings.Contains(out.String(), "takes effect within "+accessChangeWindow) {
		t.Errorf("result:\n%s", out.String())
	}
}

// One rule for every confirmation: a "no" is exit 1 with "Cancelled" (never 0),
// input that ends before an answer is exit 2, no terminal and no --yes is exit 2
// before asking.
func TestAccessAddChangesNothingUnlessConfirmed(t *testing.T) {
	for _, c := range []struct {
		answer string
		code   int
		says   string
	}{
		{"\n", exitRequestFailed, "Cancelled: agent 'bot' was not changed"},
		{"n\n", exitRequestFailed, "Cancelled: agent 'bot' was not changed"},
		{"no\n", exitRequestFailed, "Cancelled: agent 'bot' was not changed"},
		{"", exitInputRefused, "no answer"},
	} {
		r, cp, _, errOut := runAccess(t, false)
		if code := agentsAccess(r, "bot", accessChange{add: []string{"gamma"}, remove: nil, yes: false}, strings.NewReader(c.answer), true); code != c.code {
			t.Errorf("answer %q: exit %d, want %d", c.answer, code, c.code)
		}
		if n := len(requestsTo(cp, "POST", accessWsPath)); n != 0 {
			t.Errorf("answer %q: granted without a yes", c.answer)
		}
		if !strings.Contains(errOut.String(), c.says) {
			t.Errorf("answer %q: stderr %q", c.answer, errOut.String())
		}
	}
}

// --json no longer refuses a terminal: the question goes to stderr, so stdout
// stays one object, and a "no" is an error document, not a result.
func TestAccessAddUnderJSONAsksOnATerminalAndACancelIsAnErrorDocument(t *testing.T) {
	r, cp, out, errOut := runAccess(t, true)
	if code := agentsAccess(r, "bot", accessChange{add: []string{"gamma"}, remove: nil, yes: false}, strings.NewReader("n\n"), true); code != exitRequestFailed {
		t.Fatalf("exit %d, want %d", code, exitRequestFailed)
	}
	if n := len(requestsTo(cp, "POST", accessWsPath)); n != 0 || out.Len() != 0 {
		t.Errorf("granted %d time(s), stdout %q", n, out.String())
	}
	if !strings.Contains(errOut.String(), `"message":"Cancelled: agent 'bot' was not changed"`) || !strings.Contains(errOut.String(), `"code":"CLI_CANCELLED"`) {
		t.Errorf("stderr %q", errOut.String())
	}

	r, cp, _, errOut = runAccess(t, true)
	if code := agentsAccess(r, "bot", accessChange{add: []string{"gamma"}, remove: nil, yes: false}, strings.NewReader("y\n"), true); code != 0 {
		t.Fatalf("a yes under --json must grant: exit %d, %s", code, errOut.String())
	}
	if n := len(requestsTo(cp, "POST", accessWsPath)); n != 1 {
		t.Errorf("granted %d time(s)", n)
	}
}

func TestAccessAddWithNoTerminalNeedsYesAndSendsNothing(t *testing.T) {
	const sentence = "This grants access to agent 'bot'; pass --yes to confirm without a prompt."
	for _, tc := range []struct {
		asJSON, interactive bool
		says                string
	}{{true, false, sentence}, {false, false, sentence}} {
		r, cp, _, errOut := runAccess(t, tc.asJSON)
		if code := agentsAccess(r, "bot", accessChange{add: []string{"gamma"}, remove: nil, yes: false}, strings.NewReader("y\n"), tc.interactive); code != exitInputRefused {
			t.Errorf("%+v: exit %d, want %d", tc, code, exitInputRefused)
		}
		// The current access is read first (a GET changes nothing); nothing is written.
		for _, call := range cp.got() {
			if call.Method != "GET" {
				t.Errorf("%+v: sent %s %s, want no write", tc, call.Method, call.Path)
			}
		}
		if !strings.Contains(errOut.String(), tc.says) {
			t.Errorf("%+v: stderr %q", tc, errOut.String())
		}
	}
}

// An --add of what is already granted changes nothing, so it needs no --yes: not
// under --json, not without a terminal. Only a change that would really widen
// asks (the access is read first).
func TestAccessAddOfAnAlreadyGrantedWorkspaceNeedsNoYesUnderJSON(t *testing.T) {
	r, cp, out, errOut := runAccess(t, true)
	if code := agentsAccess(r, "bot", accessChange{add: []string{"beta"}}, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var got struct {
		Added []string `json:"added"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil || len(got.Added) != 0 {
		t.Errorf("stdout %q (%v)", out.String(), err)
	}
	for _, call := range cp.got() {
		if call.Method != "GET" {
			t.Errorf("sent %s %s for a no-op", call.Method, call.Path)
		}
	}
	if strings.Contains(errOut.String(), "pass --yes") {
		t.Errorf("a no-op asked for --yes: %q", errOut.String())
	}
}

func TestAccessRemoveAsksNothing(t *testing.T) {
	r, cp, _, errOut := runAccess(t, false)
	// No terminal and no --yes: a removal narrows access, so it needs neither.
	if code := agentsAccess(r, "bot", accessChange{add: nil, remove: []string{"beta"}, yes: false}, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if n := len(requestsTo(cp, "DELETE", accessWsPath+"/beta")); n != 1 {
		t.Errorf("revoke sent %d times", n)
	}
	if strings.Contains(errOut.String(), "Proceed?") {
		t.Error("a removal asked")
	}
}

func TestAccessAlreadyGrantedIsNoChangeAndAsksNothing(t *testing.T) {
	r, cp, out, _ := runAccess(t, false)
	if code := agentsAccess(r, "bot", accessChange{add: []string{"beta"}, remove: []string{"gamma"}, yes: false}, strings.NewReader(""), true); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "No change") {
		t.Errorf("out:\n%s", out.String())
	}
	if len(cp.got()) != 1 { // the read only
		t.Errorf("%d requests, want only the read", len(cp.got()))
	}
}

func TestAccessOwnWorkspaceIsRefusedBeforeAnythingChanges(t *testing.T) {
	r, cp, _, errOut := runAccess(t, false)
	if code := agentsAccess(r, "bot", accessChange{add: []string{"alpha"}, remove: nil, yes: true}, strings.NewReader(""), true); code != exitInputRefused {
		t.Fatalf("exit %d, want %d", code, exitInputRefused)
	}
	if !strings.Contains(errOut.String(), "agent 'bot''s own") {
		t.Errorf("stderr %q", errOut.String())
	}
	if n := len(requestsTo(cp, "POST", accessWsPath)); n != 0 {
		t.Error("the own workspace was sent")
	}
}

func TestAccessAnEmptyWorkspaceNameIsRefusedBeforeAnyRequest(t *testing.T) {
	// There is no "no workspace" scope any more: an empty name is an input
	// error, for --add and --remove alike, alone or inside a list.
	for _, c := range []struct {
		name        string
		add, remove []string
	}{
		{"--add empty", []string{""}, nil},
		{"--remove empty", nil, []string{""}},
		{"empty inside a list", []string{"beta,"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, cp, _, errOut := runAccess(t, false)
			if code := agentsAccess(r, "bot", accessChange{add: c.add, remove: c.remove, yes: true}, strings.NewReader(""), true); code != exitInputRefused {
				t.Fatalf("exit %d, want %d", code, exitInputRefused)
			}
			if len(cp.got()) != 0 {
				t.Errorf("requests were sent: %v", cp.got())
			}
			if !strings.Contains(errOut.String(), "empty workspace name") {
				t.Errorf("stderr %q", errOut.String())
			}
		})
	}
}

func TestAccessJSONWithYesPrintsTheFinalAccess(t *testing.T) {
	r, _, out, errOut := runAccess(t, true)
	if code := agentsAccess(r, "bot", accessChange{add: []string{"gamma"}, remove: nil, yes: true}, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var got struct {
		Access struct {
			AllowedWorkspaces []string `json:"allowed_workspaces"`
		} `json:"access"`
		Added   []string `json:"added"`
		Removed []string `json:"removed"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, out.String())
	}
	if strings.Join(got.Access.AllowedWorkspaces, ",") != "alpha,beta,gamma" || len(got.Added) != 1 || len(got.Removed) != 0 {
		t.Errorf("json = %+v", got)
	}
}

func TestAccessTheSameScopeAddedAndRemovedIsRefused(t *testing.T) {
	r, cp, _, _ := runAccess(t, false)
	if code := agentsAccess(r, "bot", accessChange{add: []string{"beta"}, remove: []string{"beta"}, yes: true}, strings.NewReader(""), true); code != exitInputRefused {
		t.Fatalf("exit %d", code)
	}
	if len(cp.got()) != 0 {
		t.Error("requests were sent")
	}
}

func TestAccessFlagValuesSplitOnCommasAndDropDuplicates(t *testing.T) {
	if got := strings.Join(scopes([]string{"a,b", "b", "c"}), "|"); got != "a|b|c" {
		t.Errorf("scopes = %q", got)
	}
}
