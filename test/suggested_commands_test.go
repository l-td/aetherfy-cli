package test

// Guards the commands the CLI SUGGESTS to users.
//
// Three wrong suggestions shipped at once, and none was reachable by any test
// because they are `output.Println` strings on success/empty paths that no test
// asserts against:
//
//   - `afy agents create my-agent --workspace X`  (workspaces create success)
//   - `afy deploy --workspace X`                  (workspaces agents, empty)
//     Neither command has a --workspace flag. A user following either gets
//     "unknown flag".
//   - `Run 'aetherfy deploy' to try again.`       (deploy failure path)
//     Wrong binary name — it is `afy`.
//
// A suggestion is a promise. Printing one that errors is worse than printing
// nothing, because it arrives exactly when the user is already stuck.
//
// SCOPE, stated honestly: the binary-name check below is general. The phantom-
// flag check is a REGRESSION PIN over known-bad strings, not a flag validator —
// validating an arbitrary `afy a b --c` suggestion means resolving cobra's
// command tree (parent linkage via AddCommand, flags via the command's own
// var), which is more machinery than this earns. If a fourth bad suggestion
// appears in a new shape, add it here.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Files that print suggestions to the user.
var suggestionSources = []string{
	"cmd/agents.go",
	"cmd/deploy.go",
	"cmd/deployments.go",
	"cmd/github.go",
	"cmd/init.go",
	"cmd/login.go",
	"cmd/logs.go",
	"cmd/rollback.go",
	"cmd/root.go",
	"cmd/secrets.go",
	"cmd/spawn.go",
	"cmd/workspaces.go",
}

func readSuggestionSource(t *testing.T, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(rel)))
	assert.NoError(t, err, "cannot read %s — has it moved? Update suggestionSources.", rel)
	return strings.ReplaceAll(string(body), "\r\n", "\n")
}

// The binary is `afy`. `aetherfy` as a command name is always wrong.
func TestSuggestionsUseTheRealBinaryName(t *testing.T) {
	// Matches `aetherfy <subcommand>` — the shape of an invocation. Does not
	// match aetherfy.yaml, aetherfy.com, or AETHERFY_ env vars.
	invocation := regexp.MustCompile(`\baetherfy \w`)

	for _, rel := range suggestionSources {
		body := readSuggestionSource(t, rel)
		for i, line := range strings.Split(body, "\n") {
			if m := invocation.FindString(line); m != "" {
				assert.Fail(t,
					"wrong binary name in a user-facing string",
					"%s:%d suggests %q — the binary is 'afy'", rel, i+1, m)
			}
		}
	}
}

// Regression pin: flags that do not exist on the command they were suggested for.
func TestSuggestionsDoNotNamePhantomFlags(t *testing.T) {
	phantom := []struct{ fragment, why string }{
		{"afy agents create my-agent --workspace", "`agents create` has no --workspace flag; use `agents update --workspace`"},
		{"afy agents create <agent> --workspace", "same as above"},
		{"afy deploy --workspace", "`deploy` has no --workspace flag; set `workspace:` in aetherfy.yaml or use `agents update`"},
	}

	for _, rel := range suggestionSources {
		body := readSuggestionSource(t, rel)
		// Scan line by line rather than asserting NotContains over the whole
		// file: a whole-file assertion dumps the entire source into the failure
		// output, which buries the one line you need to fix.
		for i, line := range strings.Split(body, "\n") {
			for _, p := range phantom {
				if strings.Contains(line, p.fragment) {
					assert.Fail(t,
						"suggested a flag that does not exist",
						"%s:%d — %s", rel, i+1, p.why)
				}
			}
		}
	}
}

// Anti-no-op: both tests above assert ABSENCE. Prove the corpus is real and
// that the readers are actually seeing suggestion text.
func TestSuggestionSourcesAreActuallyScanned(t *testing.T) {
	joined := ""
	for _, rel := range suggestionSources {
		body := readSuggestionSource(t, rel)
		assert.NotEmpty(t, body, "%s is empty — the guard would pass vacuously", rel)
		joined += body
	}

	// Real suggestions that must still be present. If these vanish, the scan is
	// looking at the wrong files and the absence assertions mean nothing.
	for _, canary := range []string{
		"afy agents update my-agent --workspace ",
		"afy secrets set --workspace ",
		"Run 'afy deploy' to try again.",
	} {
		assert.Contains(t, joined, canary,
			"expected suggestion %q not found — suggestionSources is stale and the guards are vacuous", canary)
	}
}
