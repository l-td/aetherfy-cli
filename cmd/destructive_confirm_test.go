package cmd

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/output"
)

// Every destructive command (afy delete, afy workspaces delete, afy secrets
// delete) confirms the same way, through the real root command:
//
//   - no terminal and no --yes: refused BEFORE asking, exit 2, CLI_USAGE_ERROR,
//     and nothing is deleted;
//   - --yes: deletes, without asking;
//   - a person answers no (or types the wrong name) at the prompt: exit 1 and
//     "Cancelled", nothing is deleted. A cancelled delete never exits 0.

// onTerminal makes the run a person at a terminal typing stdin, or (interactive
// false) a script with none. Restored when the test ends.
func onTerminal(t *testing.T, interactive bool, stdin string) {
	t.Helper()
	oldTerm, oldPrompter := stdinIsTerminal, newPrompter
	stdinIsTerminal = func() bool { return interactive }
	newPrompter = func() *prompter {
		return &prompter{in: bufio.NewReader(strings.NewReader(stdin)), out: output.Out()}
	}
	t.Cleanup(func() { stdinIsTerminal, newPrompter = oldTerm, oldPrompter })
}

type destructiveCase struct {
	name     string
	args     []string
	method   string
	path     string
	reply    string
	confirms string // what a person types to confirm
	deletes  string // the sentence the refusal names
	// also are the reads the command makes before it asks (afy keys revoke names the key).
	also []route
}

func destructiveCases() []destructiveCase {
	return []destructiveCase{
		{"afy delete", []string{"delete", "bot"}, "DELETE", "/agents/bot", `{}`, "bot",
			"This deletes agent 'bot' and all its deployments; pass --yes to confirm without a prompt.", nil},
		{"afy workspaces delete", []string{"workspaces", "delete", "acme"}, "DELETE", "/workspaces/acme", `{"secrets_deleted":0}`, "acme",
			"This deletes workspace 'acme' and all its secrets; pass --yes to confirm without a prompt.", nil},
		{"afy secrets delete (agent)", []string{"secrets", "delete", "bot", "K"}, "DELETE", "/agents/bot/secrets/K", `{}`, "y",
			"This deletes secret 'K' from agent 'bot'; pass --yes to confirm without a prompt.", nil},
		{"afy secrets delete (workspace)", []string{"secrets", "delete", "--workspace", "acme", "K"}, "DELETE", "/workspaces/acme/secrets/K", `{}`, "y",
			"This deletes secret 'K' from workspace 'acme'; pass --yes to confirm without a prompt.", nil},
		{"afy keys revoke", []string{"keys", "revoke", "k-1"}, "DELETE", "/keys/k-1", `{"revoked":true,"id":"k-1"}`, "y",
			"This revokes API key 'k-1'; pass --yes to confirm without a prompt.",
			[]route{{"GET", "/keys", 200, `{"keys":[{"id":"k-1","name":"ci","key_prefix":"afy_live_zz...","created_at":"2026-11-01T00:00:00Z","last_used_at":null,"is_system_managed":false}]}`}}},
	}
}

func (c destructiveCase) cp(t *testing.T) *fakeCP {
	cp := newFakeCP(t)
	cp.on(c.method, c.path, 200, c.reply)
	for _, r := range c.also {
		cp.on(r.method, r.path, r.status, r.body)
	}
	return cp
}

func TestADestructiveCommandWithoutATerminalOrYesIsRefusedAndDeletesNothing(t *testing.T) {
	for _, c := range destructiveCases() {
		t.Run(c.name, func(t *testing.T) {
			onTerminal(t, false, "")
			cp := c.cp(t)
			stdout, stderr, err := runAfy(t, cp, c.args...)
			if err == nil || ExitCode(err) != exitInputRefused {
				t.Fatalf("want exit %d, got err=%v (exit %d)\n%s", exitInputRefused, err, ExitCode(err), stderr)
			}
			if !strings.Contains(stderr, c.deletes) {
				t.Errorf("stderr lacks %q:\n%s", c.deletes, stderr)
			}
			if strings.Contains(stdout+stderr, "[y/N]") || strings.Contains(stdout+stderr, "to confirm:") {
				t.Errorf("it must refuse BEFORE prompting:\n%s%s", stdout, stderr)
			}
			if len(cp.called(c.method, c.path)) != 0 {
				t.Errorf("nothing may be deleted: %+v", cp.all())
			}

			// --json: the same refusal as the usage-error document, nothing on stdout.
			onTerminal(t, false, "")
			stdout, stderr, err = runAfy(t, cp, append(append([]string{}, c.args...), "--json")...)
			if ExitCode(err) != exitInputRefused || strings.TrimSpace(stdout) != "" {
				t.Fatalf("--json: exit %d, stdout %q", ExitCode(err), stdout)
			}
			doc := jsonErrorOnStderr(t, c.name, stderr)
			if doc["code"] != usageErrorCode {
				t.Errorf("code = %v, want %s", doc["code"], usageErrorCode)
			}
		})
	}
}

func TestADestructiveCommandWithYesDeletesWithoutAsking(t *testing.T) {
	for _, c := range destructiveCases() {
		t.Run(c.name, func(t *testing.T) {
			onTerminal(t, false, "")
			cp := c.cp(t)
			stdout, stderr, err := runAfy(t, cp, append(append([]string{}, c.args...), "--yes")...)
			if err != nil {
				t.Fatalf("%v\n%s", err, stderr)
			}
			if n := len(cp.called(c.method, c.path)); n != 1 {
				t.Errorf("want one %s %s, got %d", c.method, c.path, n)
			}
			if strings.Contains(stdout+stderr, "[y/N]") || strings.Contains(stdout+stderr, "to confirm:") {
				t.Errorf("--yes must not ask:\n%s%s", stdout, stderr)
			}
		})
	}
}

func TestADestructiveCommandConfirmedAtAPromptDeletes(t *testing.T) {
	for _, c := range destructiveCases() {
		t.Run(c.name, func(t *testing.T) {
			onTerminal(t, true, c.confirms+"\n")
			cp := c.cp(t)
			_, stderr, err := runAfy(t, cp, c.args...)
			if err != nil {
				t.Fatalf("%v\n%s", err, stderr)
			}
			if n := len(cp.called(c.method, c.path)); n != 1 {
				t.Errorf("want one %s %s, got %d", c.method, c.path, n)
			}
		})
	}
}

func TestDecliningAtThePromptExitsOneAndNeverZero(t *testing.T) {
	for _, c := range destructiveCases() {
		for _, answer := range []string{"n", "", "no", "wrong-name"} {
			t.Run(c.name+" answer "+answer, func(t *testing.T) {
				onTerminal(t, true, answer+"\n")
				cp := c.cp(t)
				stdout, stderr, err := runAfy(t, cp, c.args...)
				if err == nil {
					t.Fatalf("a cancelled delete must fail, not exit 0\n%s%s", stdout, stderr)
				}
				if ExitCode(err) != exitRequestFailed {
					t.Errorf("exit %d, want %d", ExitCode(err), exitRequestFailed)
				}
				if !strings.Contains(stderr, "Cancelled") {
					t.Errorf("stderr must say Cancelled:\n%s", stderr)
				}
				if len(cp.called(c.method, c.path)) != 0 {
					t.Errorf("nothing may be deleted: %+v", cp.all())
				}
			})
		}
	}
}

func TestACancelledDeleteUnderJSONIsAnErrorDocumentNotAResult(t *testing.T) {
	for _, c := range destructiveCases() {
		t.Run(c.name, func(t *testing.T) {
			onTerminal(t, true, "n\n")
			cp := c.cp(t)
			stdout, stderr, err := runAfy(t, cp, append(append([]string{}, c.args...), "--json")...)
			if err == nil || ExitCode(err) != exitRequestFailed {
				t.Fatalf("exit %d, err %v", ExitCode(err), err)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("stdout must stay empty (no success-shaped result): %q", stdout)
			}
			doc := jsonErrorOnStderr(t, c.name, stderr)
			if msg, _ := doc["message"].(string); !strings.HasPrefix(msg, "Cancelled") {
				t.Errorf("message = %q", msg)
			}
			if doc["code"] != cancelledCode {
				t.Errorf("code = %v, want %s", doc["code"], cancelledCode)
			}
			if _, has := doc["status"]; has {
				t.Errorf("no server wrote this: no status, got %v", doc["status"])
			}
			// stdout must not carry {"deleted":false} either.
			var v interface{}
			if json.Unmarshal([]byte(stdout), &v) == nil && strings.TrimSpace(stdout) != "" {
				t.Errorf("stdout is JSON: %s", stdout)
			}
		})
	}
}

func TestTheOldForceFlagIsGoneFromEveryDestructiveCommand(t *testing.T) {
	for _, c := range destructiveCases() {
		t.Run(c.name, func(t *testing.T) {
			onTerminal(t, false, "")
			cp := c.cp(t)
			_, _, err := runAfy(t, cp, append(append([]string{}, c.args...), "--force")...)
			if err == nil || ExitCode(err) != exitInputRefused {
				t.Fatalf("--force must be an unknown flag (exit %d), got %v", exitInputRefused, err)
			}
			if len(cp.called(c.method, c.path)) != 0 {
				t.Errorf("nothing may be deleted: %+v", cp.all())
			}
		})
	}
}
