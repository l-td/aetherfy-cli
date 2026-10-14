package cmd

import (
	"strings"
	"testing"
)

// A VALUE THAT LOOKS LIKE A FLAG is refused before anything is sent, exit 2:
// no workspace name starts with "-", and the usual cause is a value left out
// (`--to --wait` gives --to the value "--wait"). Parsed by the command's own
// flags.
func TestAFlagLikeValueIsRefusedAndSendsNothing(t *testing.T) {
	t.Cleanup(resetAllFlags)
	resetAllFlags()
	if err := agentsAccessCmd.ParseFlags([]string{"--add", "--yes"}); err != nil {
		t.Fatal(err)
	}
	refused := func(t *testing.T, code int, stderr, flag string) {
		t.Helper()
		if code != exitInputRefused {
			t.Errorf("exit %d, want %d", code, exitInputRefused)
		}
		for _, want := range []string{"no workspace name starts with", "after --" + flag, "Nothing was sent"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr lacks %q: %s", want, stderr)
			}
		}
	}

	t.Run("afy access bot --add --yes", func(t *testing.T) {
		r, cp, _, errOut := runAccess(t, false)
		code := agentsAccess(r, "bot", agentsAccessAdd, agentsAccessRemove, agentsAccessYes, strings.NewReader("y\n"), true)
		refused(t, code, errOut.String(), "add")
		if len(cp.got()) != 0 {
			t.Errorf("sent %v", cp.got())
		}
	})

	t.Run("CONTROL: without the check, the same values go through", func(t *testing.T) {
		r, cp, _, errOut := runAccess(t, false)
		code := changeAgentAccess(r, "bot", agentsAccessAdd, agentsAccessRemove, agentsAccessYes, strings.NewReader("y\n"), true)
		reqs := requestsTo(cp, "POST", accessWsPath)
		if code != 0 || len(reqs) != 1 || reqs[0].Body["workspace"] != "--yes" {
			t.Errorf("exit %d, grants %v (%s); want the workspace \"--yes\" granted", code, reqs, errOut.String())
		}
	})

	t.Run("afy access --remove --json", func(t *testing.T) {
		r, cp, _, errOut := runAccess(t, true)
		code := agentsAccess(r, "bot", nil, []string{"--json"}, false, strings.NewReader(""), false)
		refused(t, code, errOut.String(), "remove")
		if len(cp.got()) != 0 {
			t.Errorf("sent %v", cp.got())
		}
	})

	t.Run("afy collections move --to --wait", func(t *testing.T) {
		vec, cp := vecWithArticles(t), cpFake(t, nil)
		r, _, errOut := runWithCP(vec, cp, false)
		refused(t, collectionsMove(r, "articles", "--wait", true, false), errOut.String(), "to")
		if len(vec.got())+len(cp.got()) != 0 {
			t.Error("sent requests before refusing")
		}
	})

	t.Run("--workspace --json on a vector command", func(t *testing.T) {
		resetAllFlags()
		if err := collectionsListCmd.ParseFlags([]string{"--workspace", "--json"}); err != nil {
			t.Fatal(err)
		}
		if err := workspaceFlagRefusal(collectionsListCmd); err == nil || !strings.Contains(err.Error(), "after --workspace") {
			t.Fatalf("refusal = %v", err)
		}
	})
}

// THE EMPTY FORMS ARE GONE. `--add ""`, `--add=`, `--remove=` and `--to=` used
// to mean "the collections in no workspace"; every collection now lives in a
// named workspace, so each is refused, through the real command line, with
// nothing sent.
func TestTheEmptyWorkspaceFormsAreRefused(t *testing.T) {
	for _, args := range [][]string{
		{"access", "bot", "--add=", "--yes"},
		{"access", "bot", "--add", "", "--yes"},
		{"access", "bot", "--remove="},
		{"collections", "move", "articles", "--workspace", "research", "--to="},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cp := newFakeCP(t)
			_, stderr, code := runAfyProcess(t, cp, nil, args...)
			if code != exitInputRefused {
				t.Fatalf("exit %d, want %d; stderr %q", code, exitInputRefused, stderr)
			}
			if !strings.Contains(stderr, "empty workspace name") {
				t.Errorf("stderr %q", stderr)
			}
			if len(cp.calls) != 0 {
				t.Errorf("sent %+v", cp.calls)
			}
		})
	}
}
