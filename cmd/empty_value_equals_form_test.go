package cmd

import (
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/vectors"
	"github.com/spf13/cobra"
)

// WINDOWS POWERSHELL 5.1 DROPS AN EMPTY ARGUMENT. `afy access bot --add ""`
// reaches afy as `access bot --add`: the "" (or '') never arrives, measured on
// 5.1.19041 with a program that prints its argv. The flag then takes the NEXT
// argument as its value, or fails for want of one. `--add=` arrives whole, and
// means the empty value. So every flag whose "" means "no workspace" is driven
// here in its = form, through the root command's real flag parsing, as a 5.1
// user has to write it -- and the help says so.

// resetFlags puts a command's parsed values and Changed marks back, as a fresh
// process starts: cobra keeps both between executions of the same tree.
func resetFlags(t *testing.T) {
	t.Helper()
	agentsAccessAdd, agentsAccessRemove, agentsAccessYes = nil, nil, false
	collectionMoveTo, collectionMoveWait = "", false
	vecWorkspace, vecJSON = "", false
	for _, c := range []*cobra.Command{agentsAccessCmd, collectionsMoveCmd, collectionsListCmd} {
		// Flags() holds the inherited persistent flags too, once parsed.
		for _, name := range []string{"add", "remove", "yes", "to", "wait", "workspace", "json"} {
			if f := c.Flags().Lookup(name); f != nil {
				f.Changed = false
			}
		}
	}
}

func TestTheEqualsFormMeansNoWorkspace(t *testing.T) {
	t.Setenv("AETHERFY_CONFIG_DIR", t.TempDir())
	t.Setenv("AETHERFY_API_KEY", "afy_test_0123456789abcdef0123456789abcdef")
	t.Setenv(vectors.EnvAPIRegion, "")
	t.Cleanup(func() { resetFlags(t); rootCmd.SetArgs(nil) })

	execute := func(t *testing.T, args ...string) (string, error) {
		t.Helper()
		resetFlags(t)
		rootCmd.SetArgs(args)
		var err error
		out := captureStdout(t, func() { err = rootCmd.Execute() })
		return out, err
	}
	controlPlane := func(t *testing.T, cp *vecServer) {
		t.Setenv("AETHERFY_API_URL", cp.srv.URL+"/api/v1")
	}

	t.Run("afy access --add= grants the collections in no workspace", func(t *testing.T) {
		cp := accessCP(t)
		controlPlane(t, cp)
		if _, err := execute(t, "access", "bot", "--add=", "--yes", "--json"); err != nil {
			t.Fatalf("exit %d: %v", ExitCode(err), err)
		}
		if len(requestsTo(cp, "PUT", accessNonePath)) != 1 || len(requestsTo(cp, "POST", accessWsPath)) != 0 {
			t.Errorf("requests = %v, want one PUT %s", cp.got(), accessNonePath)
		}
	})

	t.Run("afy access --remove= revokes the collections in no workspace", func(t *testing.T) {
		granted := strings.Replace(botInAlphaGranted, `"workspaceless_granted":false`, `"workspaceless_granted":true`, 1)
		cp := cpFake(t, map[string]func(vecRequest) (int, string){
			"GET " + accessPath:        func(vecRequest) (int, string) { return 200, granted },
			"DELETE " + accessNonePath: func(vecRequest) (int, string) { return 200, botInAlphaGranted },
		})
		controlPlane(t, cp)
		if _, err := execute(t, "access", "bot", "--remove=", "--json"); err != nil {
			t.Fatalf("exit %d: %v", ExitCode(err), err)
		}
		if len(requestsTo(cp, "DELETE", accessNonePath)) != 1 {
			t.Errorf("requests = %v, want one DELETE %s", cp.got(), accessNonePath)
		}
	})

	t.Run("afy collections move --to= moves out of any workspace", func(t *testing.T) {
		moved := func(vecRequest) (int, string) {
			return 202, `{"operation_id":"op-2","collection_id":"c-1","regions":["us-east-1"]}`
		}
		cp := cpFake(t, map[string]func(vecRequest) (int, string){"PATCH " + changePath: moved})
		controlPlane(t, cp)
		t.Setenv(vectors.EnvVectorsURL, vecWithArticles(t).srv.URL)
		t.Setenv(vectors.EnvWorkspace, "research")
		if _, err := execute(t, "collections", "move", "articles", "--to=", "--json"); err != nil {
			t.Fatalf("exit %d: %v", ExitCode(err), err)
		}
		reqs := requestsTo(cp, "PATCH", changePath)
		if len(reqs) != 1 {
			t.Fatalf("PATCH = %v", cp.got())
		}
		if v, ok := reqs[0].Body["workspace_id"]; !ok || v != nil {
			t.Errorf("PATCH body = %v, want workspace_id null", reqs[0].Body)
		}
	})

	t.Run("--workspace= forces no workspace over the environment's", func(t *testing.T) {
		vec := newVecServer(t, func(vecRequest) (int, string) { return 200, `{"collections":[]}` })
		t.Setenv(vectors.EnvVectorsURL, vec.srv.URL)
		t.Setenv(vectors.EnvWorkspace, "from-env")
		out, err := execute(t, "collections", "list", "--workspace=", "--json")
		if err != nil {
			t.Fatalf("exit %d: %v", ExitCode(err), err)
		}
		if ws, present := decode(t, out)["workspace"]; !present || ws != nil {
			t.Errorf("envelope workspace = %v, want null", ws)
		}
		if got := vec.got(); len(got) != 1 || got[0].Path != "/api/v1/collections" {
			t.Errorf("requests = %v, want GET /api/v1/collections", got)
		}
	})

	// CONTROL: what 5.1 delivers for `--add "" --yes --json` is `--add --yes
	// --json`. That is NOT "no workspace": --add takes "--yes" as its value and
	// --yes is never set. Parsed only: a run would end in exitWith's os.Exit.
	t.Run("CONTROL: the plain form as 5.1 delivers it does not mean no workspace", func(t *testing.T) {
		resetFlags(t)
		if err := agentsAccessCmd.ParseFlags([]string{"--add", "--yes", "--json"}); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(agentsAccessAdd, ","); got != "--yes" || agentsAccessYes {
			t.Errorf("--add parsed as %q (--yes %v), want the next argument \"--yes\" and --yes unset", got, agentsAccessYes)
		}
		resetFlags(t)
		if err := agentsAccessCmd.ParseFlags([]string{"--add=", "--yes", "--json"}); err != nil {
			t.Fatal(err)
		}
		if len(agentsAccessAdd) != 1 || agentsAccessAdd[0] != "" || !agentsAccessYes {
			t.Errorf("--add= parsed as %q (--yes %v), want one empty value and --yes set", agentsAccessAdd, agentsAccessYes)
		}
	})
}

// The help names the = form wherever "" means no workspace.
func TestTheHelpNamesTheEqualsFormForPowerShell51(t *testing.T) {
	for name, text := range map[string]string{
		"access (long)":           agentsAccessCmd.Long,
		"access --add":            agentsAccessCmd.Flags().Lookup("add").Usage,
		"access --remove":         agentsAccessCmd.Flags().Lookup("remove").Usage,
		"collections move (long)": collectionsMoveCmd.Long,
		"collections move --to":   collectionsMoveCmd.Flags().Lookup("to").Usage,
		"--workspace":             vecWorkspaceHelp,
	} {
		if !strings.Contains(text, "Windows PowerShell 5.1") || !strings.Contains(text, "=") {
			t.Errorf("%s does not name the = form for Windows PowerShell 5.1:\n%s", name, text)
		}
	}
	for flag, text := range map[string]string{
		"--add=":       agentsAccessCmd.Long,
		"--remove=":    agentsAccessCmd.Long,
		"--to=":        collectionsMoveCmd.Long,
		"--workspace=": vecWorkspaceHelp,
	} {
		if !strings.Contains(text, flag) {
			t.Errorf("help lacks %s:\n%s", flag, text)
		}
	}
}
