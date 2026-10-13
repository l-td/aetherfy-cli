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
	accessNonePath    = accessPath + "/workspaceless"
	botInAlphaGranted = `{"agent":"bot","own_workspace":"alpha","granted_workspaces":["beta"],` +
		`"workspaceless_granted":false,"allowed_workspaces":["alpha","beta"],"workspaceless_allowed":false,"changed":null}`
	botAfterGamma = `{"agent":"bot","own_workspace":"alpha","granted_workspaces":["beta","gamma"],` +
		`"workspaceless_granted":false,"allowed_workspaces":["alpha","beta","gamma"],"workspaceless_allowed":false,"changed":true}`
)

// accessCP answers the access routes; every call is recorded by the server.
func accessCP(t *testing.T) *vecServer {
	ok := func(body string) func(vecRequest) (int, string) {
		return func(vecRequest) (int, string) { return 200, body }
	}
	return cpFake(t, map[string]func(vecRequest) (int, string){
		"GET " + accessPath:                         ok(botInAlphaGranted),
		"POST " + accessWsPath:                      ok(botAfterGamma),
		"DELETE " + accessWsPath + "/beta":          ok(botInAlphaGranted),
		"DELETE " + accessWsPath + "/workspaceless": ok(botInAlphaGranted),
		"PUT " + accessNonePath:                     ok(botInAlphaGranted),
		"DELETE " + accessNonePath:                  ok(botInAlphaGranted),
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
	if code := agentsAccess(r, "bot", nil, nil, false, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for _, want := range []string{"Agent 'bot' can use:", "alpha  (its own workspace)", "beta  (granted)"} {
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
	if code := agentsAccess(r, "bot", []string{"gamma"}, nil, false, strings.NewReader("y\n"), true); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Adds workspace 'gamma': every collection in workspace 'gamma', including ones added later, becomes readable and writable") {
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

func TestAccessAddChangesNothingUnlessConfirmed(t *testing.T) {
	for _, answer := range []string{"\n", "n\n", "no\n", ""} {
		r, cp, _, errOut := runAccess(t, false)
		if code := agentsAccess(r, "bot", []string{"gamma"}, nil, false, strings.NewReader(answer), true); code != exitInputRefused {
			t.Errorf("answer %q: exit %d, want %d", answer, code, exitInputRefused)
		}
		if n := len(requestsTo(cp, "POST", accessWsPath)); n != 0 {
			t.Errorf("answer %q: granted without a yes", answer)
		}
		if !strings.Contains(errOut.String(), "not confirmed") {
			t.Errorf("answer %q: stderr %q", answer, errOut.String())
		}
	}
}

func TestAccessAddWithJSONOrNoTerminalNeedsYesAndSendsNothing(t *testing.T) {
	for _, tc := range []struct {
		asJSON, interactive bool
		says                string
	}{{true, true, "--json cannot answer"}, {false, false, "stdin is not a terminal"}} {
		r, cp, _, errOut := runAccess(t, tc.asJSON)
		if code := agentsAccess(r, "bot", []string{"gamma"}, nil, false, strings.NewReader("y\n"), tc.interactive); code != exitInputRefused {
			t.Errorf("%+v: exit %d, want %d", tc, code, exitInputRefused)
		}
		if n := len(cp.got()); n != 0 {
			t.Errorf("%+v: %d requests sent, want none", tc, n)
		}
		if !strings.Contains(errOut.String(), tc.says) {
			t.Errorf("%+v: stderr %q", tc, errOut.String())
		}
	}
}

func TestAccessRemoveAsksNothing(t *testing.T) {
	r, cp, _, errOut := runAccess(t, false)
	// No terminal and no --yes: a removal narrows access, so it needs neither.
	if code := agentsAccess(r, "bot", nil, []string{"beta"}, false, strings.NewReader(""), false); code != 0 {
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
	if code := agentsAccess(r, "bot", []string{"beta"}, []string{"gamma"}, false, strings.NewReader(""), true); code != 0 {
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
	if code := agentsAccess(r, "bot", []string{"alpha"}, nil, true, strings.NewReader(""), true); code != exitInputRefused {
		t.Fatalf("exit %d, want %d", code, exitInputRefused)
	}
	if !strings.Contains(errOut.String(), "agent 'bot''s own") {
		t.Errorf("stderr %q", errOut.String())
	}
	if n := len(requestsTo(cp, "POST", accessWsPath)); n != 0 {
		t.Error("the own workspace was sent")
	}
}

func TestAccessEmptyNameIsTheWorkspacelessScopeAndANamedWorkspaceIsNot(t *testing.T) {
	r, cp, _, errOut := runAccess(t, false)
	if code := agentsAccess(r, "bot", []string{""}, []string{"workspaceless"}, true, strings.NewReader(""), true); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if n := len(requestsTo(cp, "PUT", accessNonePath)); n != 1 {
		t.Errorf(`--add "" sent %d workspaceless grants`, n)
	}
	// "workspaceless" is a workspace NAME; not granted here, so nothing to revoke.
	if n := len(requestsTo(cp, "DELETE", accessNonePath)); n != 0 {
		t.Error(`--remove workspaceless revoked the workspaceless scope`)
	}
}

func TestAccessJSONWithYesPrintsTheFinalAccess(t *testing.T) {
	r, _, out, errOut := runAccess(t, true)
	if code := agentsAccess(r, "bot", []string{"gamma"}, nil, true, strings.NewReader(""), false); code != 0 {
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
	if code := agentsAccess(r, "bot", []string{"beta"}, []string{"beta"}, true, strings.NewReader(""), true); code != exitInputRefused {
		t.Fatalf("exit %d", code)
	}
	if len(cp.got()) != 0 {
		t.Error("requests were sent")
	}
}

func TestAccessFlagValuesSplitOnCommasAndKeepALoneEmpty(t *testing.T) {
	if got := strings.Join(scopes([]string{"a,b", "b", ""}), "|"); got != "a|b|" {
		t.Errorf("scopes = %q", got)
	}
}
