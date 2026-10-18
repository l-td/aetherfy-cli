package cmd

import (
	"strings"
	"testing"
)

// A usage error is input refused before anything is sent, so it exits 2 like
// every other such refusal: cobra's own (an unknown flag or command, a flag
// without its value, too few or too many arguments, a required flag not set)
// and the update commands' "say what to change" checks. With --json it is the
// same {"error":{...}} line on stderr, carrying the CLI's own code.

var usageCases = []struct {
	name string
	args []string
}{
	{"an unknown flag", []string{"list", "--nope"}},
	{"an unknown shorthand", []string{"list", "-Z"}},
	{"an unknown command", []string{"frobnicate"}},
	{"an unknown subcommand of a group", []string{"secrets", "frobnicate"}},
	{"a missing argument", []string{"stop"}},
	{"an extra argument", []string{"stop", "bot", "extra"}},
	{"a flag without its value", []string{"create", "newbot", "--workspace"}},
	{"a flag with a value of the wrong type", []string{"logs", "bot", "--tail", "many"}},
	{"a required flag not set", []string{"workspaces", "regions", "acme"}},
	{"update with nothing to change", []string{"update", "bot"}},
	{"update with an empty name", []string{"update", "bot", "--name", ""}},
	{"update with an empty workspace", []string{"update", "bot", "--workspace", ""}},
	{"workspaces update with nothing to change", []string{"workspaces", "update", "acme"}},
	{"workspaces update with an empty name", []string{"workspaces", "update", "acme", "--name", ""}},
}

func TestUsageErrorsExit2(t *testing.T) {
	for _, tc := range usageCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cp := newFakeCP(t)
			stdout, stderr, code := runAfyProcess(t, cp, nil, tc.args...)
			if code != exitInputRefused {
				t.Errorf("exit %d, want %d\nstderr:\n%s", code, exitInputRefused, stderr)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("stdout carries %q on a usage error", stdout)
			}
			if !strings.HasPrefix(strings.TrimSpace(stderr), "Error: ") || strings.Count(stderr, "Error: ") != 1 {
				t.Errorf("want exactly one text %q line:\n%s", "Error: ...", stderr)
			}
			if n := len(cp.all()); n != 0 {
				t.Errorf("a usage error sent %d request(s)", n)
			}
		})
	}
}

func TestUsageErrorsUnderJSONCarryTheCLIsCode(t *testing.T) {
	for _, tc := range usageCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// --json FIRST: after a flag missing its value it would be taken as the value.
			stdout, stderr, code := runAfyProcess(t, newFakeCP(t), nil, append([]string{"--json"}, tc.args...)...)
			if code != exitInputRefused {
				t.Errorf("exit %d, want %d\nstderr:\n%s", code, exitInputRefused, stderr)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("stdout carries %q on a usage error", stdout)
			}
			e := jsonErrorOnStderr(t, tc.name, stderr)
			if e["code"] != usageErrorCode {
				t.Errorf("code = %v, want %s: %v", e["code"], usageErrorCode, e)
			}
			if _, has := e["status"]; has {
				t.Errorf("a usage error has no HTTP status: %v", e)
			}
		})
	}
}

func TestUsageExitDoesNotSwallowOtherExitCodes(t *testing.T) {
	// The control: what is NOT a usage error keeps its code. A server refusal
	// is 1, no credentials is 3, and a failure inside the command (after
	// cobra handed it over) is not turned into a usage error.
	cp := newCasesCP(t, []route{{"POST", "/agents/bot/stop", 409, refusalBody}})
	if _, _, code := runAfyProcess(t, cp, nil, "stop", "bot"); code != exitRequestFailed {
		t.Errorf("a server refusal exits %d, want %d", code, exitRequestFailed)
	}
	if _, _, code := runAfyProcess(t, newFakeCP(t), []string{"AETHERFY_API_KEY="}, "list"); code != exitNotLoggedIn {
		t.Errorf("no credentials exits %d, want %d", code, exitNotLoggedIn)
	}
	if _, _, code := runAfyProcess(t, newFakeCP(t), nil, "list"); code != exitRequestFailed {
		t.Errorf("an unanswered request exits %d, want %d", code, exitRequestFailed)
	}
}

func TestUsageErrorOfAVectorCommandCarriesTheCLIsCodeToo(t *testing.T) {
	// The vector commands write their own error line (vecRun.fail); the
	// input they refuse before sending is the same exit 2 and the same code.
	cp := newFakeCP(t)
	env := []string{"AETHERFY_VECTORS_URL=" + cp.srv.URL}
	stdout, stderr, code := runAfyProcess(t, cp, env, "collections", "list", "--workspace", "", "--json")
	if code != exitInputRefused || strings.TrimSpace(stdout) != "" {
		t.Fatalf("exit %d, stdout %q\n%s", code, stdout, stderr)
	}
	if e := jsonErrorOnStderr(t, "collections list", stderr); e["code"] != usageErrorCode {
		t.Errorf("code = %v, want %s: %v", e["code"], usageErrorCode, e)
	}
	if n := len(cp.all()); n != 0 {
		t.Errorf("a refused input sent %d request(s)", n)
	}
}

func TestBareGroupStillShowsItsHelp(t *testing.T) {
	// A group with no subcommand named is a request for its help, not an error.
	stdout, _, code := runAfyProcess(t, newFakeCP(t), nil, "secrets")
	if code != 0 || !strings.Contains(stdout, "Available Commands") {
		t.Errorf("exit %d, stdout:\n%s", code, stdout)
	}
}
