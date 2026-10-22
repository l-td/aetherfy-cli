package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// afy access levels and afy access --secrets (grants round 1; the design is
// aetherfy-control-plane docs/AGENT_ACCESS_LEVELS.md), against a fake control
// plane: what each flag sends, which changes ask first (the ones that let the
// agent's code do more), and which refuse before anything is sent.

const (
	accessOwnPath     = accessPath + "/own-workspace"
	accessSecretsPath = accessPath + "/secrets"
)

// botAccess is bot's access: home alpha (its own setting `own`, "" = follows
// the default `def`), beta granted at `beta`, and the secrets switch.
func botAccess(own, def, beta string, secrets bool) string {
	ownLevel, setting := def, "null"
	if own != "" {
		ownLevel, setting = own, `"`+own+`"`
	}
	sec := "false"
	if secrets {
		sec = "true"
	}
	return `{"agent":"bot","own_workspace":"alpha","own_workspace_level":"` + ownLevel + `",` +
		`"own_workspace_level_setting":` + setting + `,"workspace_default_level":"` + def + `",` +
		`"granted_workspaces":["beta"],"granted_workspace_refs":[{"id":"b","name":"beta","level":"` + beta + `"}],` +
		`"allowed_workspaces":["alpha","beta"],"secrets_access":` + sec + `,"changed":true}`
}

// levelCP answers bot's access as `read`, and every change with `after`.
func levelCP(t *testing.T, read, after string) *vecServer {
	ok := func(body string) func(vecRequest) (int, string) {
		return func(vecRequest) (int, string) { return 200, body }
	}
	return cpFake(t, map[string]func(vecRequest) (int, string){
		"GET " + accessPath:        ok(read),
		"POST " + accessWsPath:     ok(after),
		"PUT " + accessOwnPath:     ok(after),
		"PUT " + accessSecretsPath: ok(after),
	})
}

func runLevels(t *testing.T, read, after string, asJSON bool) (*vecRun, *vecServer, *strings.Builder, *strings.Builder) {
	cp := levelCP(t, read, after)
	r, out, errOut := runWithCP(noVectors(t), cp, asJSON)
	return r, cp, out, errOut
}

func TestLevelUsageErrorsSendNothing(t *testing.T) {
	for _, c := range []struct {
		name   string
		change accessChange
		says   string
	}{
		{"--level without --add", accessChange{level: "full", levelSet: true, yes: true}, "no --add was given"},
		{"--level outside the three", accessChange{add: []string{"gamma"}, level: "admin", levelSet: true, yes: true}, "--level must be one of read, read_write, full"},
		{"--own-level outside the three", accessChange{ownLevel: "owner", ownLevelSet: true, yes: true}, "--own-level must be one of read, read_write, full, or default"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, cp, _, errOut := runLevels(t, botAccess("", "full", "read", false), "{}", false)
			if code := agentsAccess(r, "bot", c.change, strings.NewReader(""), true); code != exitInputRefused {
				t.Fatalf("exit %d, want %d", code, exitInputRefused)
			}
			if len(cp.got()) != 0 {
				t.Errorf("sent %v", cp.got())
			}
			if !strings.Contains(errOut.String(), c.says) {
				t.Errorf("stderr %q", errOut.String())
			}
		})
	}
}

func TestLevelANewGrantAtALevelSaysWhatItReachesAndSendsIt(t *testing.T) {
	r, cp, out, errOut := runLevels(t, botAccess("", "full", "read", false), botAccess("", "full", "read", false), false)
	change := accessChange{add: []string{"gamma"}, level: "read_write", levelSet: true}
	if code := agentsAccess(r, "bot", change, strings.NewReader("y\n"), true); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	// The canonical sentences, word for word (cmd/access_words.go; the design doc).
	if !strings.Contains(out.String(), "Adds workspace 'gamma' at read and write access") ||
		!strings.Contains(out.String(), "This lets the agent's code do more in gamma. Read and write access: everything read "+
			"access can do, plus add, change and delete points, and create collections and indexes.") {
		t.Errorf("preview:\n%s", out.String())
	}
	reqs := requestsTo(cp, "POST", accessWsPath)
	if len(reqs) != 1 || reqs[0].Body["workspace"] != "gamma" || reqs[0].Body["level"] != "read_write" {
		t.Fatalf("grant = %v", reqs)
	}
}

func TestLevelANewGrantWithoutALevelSendsNoneSoTheControlPlaneDefaultHolds(t *testing.T) {
	r, cp, _, errOut := runLevels(t, botAccess("", "full", "read", false), botAccess("", "full", "read", false), false)
	if code := agentsAccess(r, "bot", accessChange{add: []string{"gamma"}, yes: true}, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	reqs := requestsTo(cp, "POST", accessWsPath)
	if _, sent := reqs[0].Body["level"]; len(reqs) != 1 || sent {
		t.Fatalf("grant = %v", reqs)
	}
}

func TestLevelRaisingAGrantAsksAndLoweringOneDoesNot(t *testing.T) {
	r, cp, out, errOut := runLevels(t, botAccess("", "full", "read", false), botAccess("", "full", "full", false), false)
	if code := agentsAccess(r, "bot", accessChange{add: []string{"beta"}, level: "full", levelSet: true},
		strings.NewReader("y\n"), true); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Changes its level in workspace 'beta' from read access to full access") ||
		!strings.Contains(out.String(), "Full access: everything read and write access can do, plus delete collections, "+
			"delete indexes and change their structure (schema, aliases), which changes them for everyone who uses them.") ||
		!strings.Contains(errOut.String(), "Proceed?") {
		t.Errorf("out:\n%s\nerr: %s", out.String(), errOut.String())
	}
	if reqs := requestsTo(cp, "POST", accessWsPath); len(reqs) != 1 || reqs[0].Body["level"] != "full" {
		t.Fatalf("grant = %v", reqs)
	}

	// Lowering it, from a script without --yes: a narrowing asks nothing, and
	// says when it takes effect.
	r2, cp2, out2, errOut2 := runLevels(t, botAccess("", "full", "full", false), botAccess("", "full", "read", false), false)
	if code := agentsAccess(r2, "bot", accessChange{add: []string{"beta"}, level: "read", levelSet: true},
		strings.NewReader(""), true); code != 0 {
		t.Fatalf("lowering: exit %d: %s", code, errOut2.String())
	}
	if strings.Contains(errOut2.String(), "Proceed?") {
		t.Error("lowering a grant asked")
	}
	if !strings.Contains(out2.String(), "Takes effect within 60 seconds; requests above the new level are refused.") {
		t.Errorf("the note under a lowered level is missing:\n%s", out2.String())
	}
	if reqs := requestsTo(cp2, "POST", accessWsPath); len(reqs) != 1 || reqs[0].Body["level"] != "read" {
		t.Fatalf("lowering grant = %v", reqs)
	}
}

func TestLevelTheSameLevelAgainIsNoChange(t *testing.T) {
	r, cp, out, _ := runLevels(t, botAccess("", "full", "read", false), "{}", false)
	if code := agentsAccess(r, "bot", accessChange{add: []string{"beta"}, level: "read", levelSet: true, yes: true},
		strings.NewReader(""), true); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "No change") || len(cp.got()) != 1 {
		t.Errorf("out %q, %d requests", out.String(), len(cp.got()))
	}
}

func TestLevelLoweringItsOwnLevelAsksNothingEvenFromAScript(t *testing.T) {
	r, cp, out, errOut := runLevels(t, botAccess("", "full", "read", false), botAccess("read", "full", "read", false), false)
	if code := agentsAccess(r, "bot", accessChange{ownLevel: "read", ownLevelSet: true}, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	reqs := requestsTo(cp, "PUT", accessOwnPath)
	if len(reqs) != 1 || reqs[0].Body["level"] != "read" {
		t.Fatalf("own level = %v", reqs)
	}
	if !strings.Contains(out.String(), "Sets its level where it lives, workspace 'alpha', to read access (set for this agent)") ||
		!strings.Contains(out.String(), "Takes effect within 60 seconds; requests above the new level are refused.") {
		t.Errorf("out:\n%s", out.String())
	}
}

func TestLevelDefaultSendsNullAndRaisingToItNeedsAYes(t *testing.T) {
	// Set to read; the default is full: "default" raises it.
	read := botAccess("read", "full", "read", false)
	r, cp, _, errOut := runLevels(t, read, botAccess("", "full", "read", false), false)
	if code := agentsAccess(r, "bot", accessChange{ownLevel: followDefault, ownLevelSet: true}, strings.NewReader(""), false); code != exitInputRefused {
		t.Fatalf("exit %d, want %d", code, exitInputRefused)
	}
	if !strings.Contains(errOut.String(), "This grants access to agent 'bot'; pass --yes to confirm without a prompt.") {
		t.Errorf("stderr %q", errOut.String())
	}
	if n := len(requestsTo(cp, "PUT", accessOwnPath)); n != 0 {
		t.Errorf("sent %d own-level changes without a yes", n)
	}

	r2, cp2, _, errOut2 := runLevels(t, read, botAccess("", "full", "read", false), false)
	if code := agentsAccess(r2, "bot", accessChange{ownLevel: followDefault, ownLevelSet: true, yes: true}, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut2.String())
	}
	reqs := requestsTo(cp2, "PUT", accessOwnPath)
	if len(reqs) != 1 {
		t.Fatalf("own level = %v", reqs)
	}
	if v, sent := reqs[0].Body["level"]; !sent || v != nil {
		t.Errorf("default is sent as null, got %v (sent %v)", v, sent)
	}
}

func TestLevelJSONSaysWhichLevelsChanged(t *testing.T) {
	r, _, out, errOut := runLevels(t, botAccess("", "full", "read", false), botAccess("read", "full", "full", false), true)
	change := accessChange{add: []string{"beta"}, level: "full", levelSet: true, ownLevel: "read", ownLevelSet: true, yes: true}
	if code := agentsAccess(r, "bot", change, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var got struct {
		LevelsChanged []struct {
			Workspace, From, To string
		} `json:"levels_changed"`
		OwnLevelChanged bool `json:"own_level_changed"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, out.String())
	}
	if len(got.LevelsChanged) != 1 || got.LevelsChanged[0].To != "full" || !got.OwnLevelChanged {
		t.Errorf("json = %+v", got)
	}
}

func TestSecretsOnAsksFirstAndSendsTheSwitch(t *testing.T) {
	r, cp, out, errOut := runLevels(t, botAccess("", "full", "read", false), botAccess("", "full", "read", true), false)
	if code := agentsAccessSecrets(r, "bot", "on", false, strings.NewReader("y\n"), true); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	// Its OWN secrets only: never its workspace's, never another agent's.
	if !strings.Contains(errOut.String(), "Proceed?") ||
		!strings.Contains(errOut.String(), "create, update and delete its own secrets") ||
		!strings.Contains(errOut.String(), "It never reaches its workspace's secrets or another agent's.") ||
		strings.Contains(out.String()+errOut.String(), "its own workspace 'alpha''s secrets") {
		t.Errorf("out:\n%s\nerr: %s", out.String(), errOut.String())
	}
	if reqs := requestsTo(cp, "PUT", accessSecretsPath); len(reqs) != 1 || reqs[0].Body["enabled"] != true {
		t.Fatalf("switch = %v", reqs)
	}
	if !strings.Contains(out.String(), "a service sees it after its next deployment, a task on its next run") {
		t.Errorf("out:\n%s", out.String())
	}
}

func TestSecretsOnUnconfirmedOrUnaskableSendsNothing(t *testing.T) {
	// A "no" is a cancelled change: exit 1 (never read as done), nothing sent.
	r, cp, _, errOut := runLevels(t, botAccess("", "full", "read", false), "{}", false)
	if code := agentsAccessSecrets(r, "bot", "on", false, strings.NewReader("n\n"), true); code != exitRequestFailed {
		t.Fatalf("not confirmed: exit %d, want %d", code, exitRequestFailed)
	}
	if !strings.Contains(errOut.String(), "Cancelled: agent 'bot' was not changed") {
		t.Errorf("stderr %q", errOut.String())
	}
	if n := len(requestsTo(cp, "PUT", accessSecretsPath)); n != 0 {
		t.Errorf("turned on without a yes")
	}
	// Nobody can answer: exit 2 before asking; only the read was sent.
	for _, asJSON := range []bool{true, false} {
		r2, cp2, _, errOut2 := runLevels(t, botAccess("", "full", "read", false), "{}", asJSON)
		if code := agentsAccessSecrets(r2, "bot", "on", false, strings.NewReader(""), false); code != exitInputRefused {
			t.Fatalf("json=%v: exit %d", asJSON, code)
		}
		if n := len(cp2.got()); n != 1 || cp2.got()[0].Method != "GET" {
			t.Errorf("json=%v: sent %v, want the one read", asJSON, cp2.got())
		}
		if !strings.Contains(errOut2.String(), "This turns on secrets access for agent 'bot'; pass --yes to confirm without a prompt.") {
			t.Errorf("json=%v: stderr %q", asJSON, errOut2.String())
		}
	}
}

func TestSecretsOffAsksNothingAndAnUnknownStateIsAUsageError(t *testing.T) {
	r, cp, _, errOut := runLevels(t, botAccess("", "full", "read", true), botAccess("", "full", "read", false), false)
	if code := agentsAccessSecrets(r, "bot", "off", false, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if reqs := requestsTo(cp, "PUT", accessSecretsPath); len(reqs) != 1 || reqs[0].Body["enabled"] != false {
		t.Fatalf("switch = %v", reqs)
	}
	r2, cp2, _, errOut2 := runLevels(t, botAccess("", "full", "read", true), "{}", false)
	if code := agentsAccessSecrets(r2, "bot", "yes", true, strings.NewReader(""), true); code != exitInputRefused {
		t.Fatalf("exit %d", code)
	}
	if len(cp2.got()) != 0 || !strings.Contains(errOut2.String(), "on or off") {
		t.Errorf("sent %v, stderr %q", cp2.got(), errOut2.String())
	}
}

// A state already set changes nothing, so it asks for nothing: not a --yes under
// --json, not a terminal.
func TestSecretsAlreadySoIsNoChange(t *testing.T) {
	r, cp, out, _ := runLevels(t, botAccess("", "full", "read", true), "{}", false)
	if code := agentsAccessSecrets(r, "bot", "on", false, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "No change") || len(cp.got()) != 1 {
		t.Errorf("out %q, %d requests", out.String(), len(cp.got()))
	}
}

func TestSecretsAlreadySoUnderJSONWithoutYesIsNoChange(t *testing.T) {
	r, cp, out, errOut := runLevels(t, botAccess("", "full", "read", true), "{}", true)
	if code := agentsAccessSecrets(r, "bot", "on", false, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil || got["changed"] != false {
		t.Errorf("stdout %q (%v)", out.String(), err)
	}
	if len(cp.got()) != 1 {
		t.Errorf("sent %v, want the one read", cp.got())
	}
}

func TestSecretsJSONShape(t *testing.T) {
	r, _, out, errOut := runLevels(t, botAccess("", "full", "read", false), botAccess("", "full", "read", true), true)
	if code := agentsAccessSecrets(r, "bot", "on", true, strings.NewReader(""), false); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, out.String())
	}
	if len(got) != 3 || got["agent"] != "bot" || got["secrets_access"] != true || got["changed"] != true {
		t.Errorf("json = %v", got)
	}
}

func TestSecretsFlagIsACommandOfItsOwn(t *testing.T) {
	for _, extra := range [][]string{
		{"--add", "gamma"}, {"--remove", "beta"}, {"--level", "full"}, {"--own-level", "read"},
	} {
		cp := newFakeCP(t)
		args := append([]string{"access", "bot", "--secrets", "on"}, extra...)
		_, stderr, err := runAfy(t, cp, args...)
		if err == nil || !strings.Contains(stderr, "--secrets is a command of its own") {
			t.Errorf("%v: want the refusal, got %v\n%s", extra, err, stderr)
		}
		if len(cp.all()) != 0 {
			t.Errorf("%v: nothing may be sent: %+v", extra, cp.all())
		}
	}
}
