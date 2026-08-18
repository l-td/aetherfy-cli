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

// The repository this code actually lives in (`git remote get-url origin`).
// A URL a reader is told to clone, browse, or file an issue against must
// resolve to it.
const realRepoURL = "https://github.com/l-td/aetherfy-cli"

// A github.com URL in prose, as opposed to a Go import path. Import paths have
// no scheme, so requiring `https://` separates the two: the module is still
// github.com/aetherfy/cli in every source file's import block, and renaming
// that is a different change from telling a reader where the code is.
var proseGitHubURL = regexp.MustCompile(`https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+`)

// README URLs must point at the repository that exists.
//
// The README told readers to `git clone https://github.com/aetherfy/cli.git`,
// to download releases from `aetherfy/cli/releases`, and to file issues at
// `aetherfy/cli/issues`. That org and repo do not exist — every one of those
// links 404s, and the clone is the FIRST thing a reader does. The docs site now
// points at this repository, so the repository must not point at a fiction.
func TestReadmeGitHubURLsPointAtTheRealRepo(t *testing.T) {
	readme := readSuggestionSource(t, "README.md")

	found := 0
	for i, line := range strings.Split(readme, "\n") {
		for _, u := range proseGitHubURL.FindAllString(line, -1) {
			found++
			assert.True(t, strings.HasPrefix(u, realRepoURL),
				"README.md:%d links %q — the repository is %s (git remote origin). "+
					"github.com/aetherfy/* does not exist.", i+1, u, realRepoURL)
		}
	}

	// Boundary check, not a truthy one: the loop above asserts nothing if the
	// regex stops matching. The README carries at least the clone URL and the
	// issues URL.
	assert.GreaterOrEqual(t, found, 2,
		"expected at least 2 github.com URLs in README.md, found %d — "+
			"the URL scan is dead and the assertion above is vacuous", found)

	// The clone command specifically: the shape a reader copies. Scanned line
	// by line and reported with Fail rather than asserted with Contains over
	// the whole file — Contains prints the entire README on failure, burying
	// the one line you need to fix.
	cloneOK := false
	for _, line := range strings.Split(readme, "\n") {
		if strings.Contains(line, "git clone "+realRepoURL+".git") {
			cloneOK = true
			break
		}
	}
	if !cloneOK {
		assert.Fail(t, "README.md shows no working clone command",
			"expected a line containing `git clone %s.git`", realRepoURL)
	}
}

// The installer script downloads from a GitHub repo; same rule.
func TestInstallScriptTargetsTheRealRepo(t *testing.T) {
	script := readSuggestionSource(t, "scripts/install.sh")
	assert.Contains(t, script, `GITHUB_REPO="l-td/aetherfy-cli"`,
		"scripts/install.sh must fetch releases from the repository that exists")
}

// The release target is the third place this repository names itself, and the
// one nobody reads until a release is being cut — which is the worst moment to
// discover it points at `aetherfy/cli`, an org that does not exist.
//
// NOT pinned here, deliberately: the Go MODULE path, `github.com/aetherfy/cli`.
// It appears in every import block and in the ldflags below, it does not have
// to match the release target for goreleaser, and renaming it is an owner
// decision that only matters if `go install` support is ever wanted. A guard
// that failed on it would fail on ~60 correct lines. So this scan skips
// comments and looks only at the release block's own owner/name keys.
func TestReleaseConfigTargetsTheRealRepo(t *testing.T) {
	cfg := readSuggestionSource(t, ".goreleaser.yaml")

	sawOwner, sawName := false, false
	for i, line := range strings.Split(cfg, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue // commented-out blocks are inert; the cask is one
		}
		switch {
		case strings.HasPrefix(trimmed, "owner:"):
			sawOwner = true
			assert.Equal(t, "owner: l-td", trimmed,
				".goreleaser.yaml:%d — release owner must be the real org (l-td)", i+1)
		case strings.HasPrefix(trimmed, "name: aetherfy-cli"), strings.HasPrefix(trimmed, "name: cli"):
			sawName = true
			assert.Equal(t, "name: aetherfy-cli", trimmed,
				".goreleaser.yaml:%d — release repo must be aetherfy-cli, not cli", i+1)
		}
	}

	// Anti-vacuity: both assertions live inside a conditional, so an edit that
	// renamed or removed the keys would leave this test asserting nothing.
	assert.True(t, sawOwner && sawName,
		"no active release owner/name found in .goreleaser.yaml — the scan is dead "+
			"and TestReleaseConfigTargetsTheRealRepo asserts nothing")
}

// Install paths that do not exist must not be advertised as if they did.
//
// `curl -fsSL https://aetherfy.com/install.sh | bash` — that URL is a 404;
// the landing site serves no install.sh. `brew install aetherfy/tap/afy` —
// there is no tap, and no tagged release for one to carry. Both were printed
// as ready-to-run commands under "Installation". Build-from-source is the
// only path today; when a release exists, document these again then.
func TestReadmeDoesNotAdvertiseUnshippedInstallPaths(t *testing.T) {
	readme := readSuggestionSource(t, "README.md")

	unshipped := []struct{ fragment, why string }{
		{"aetherfy.com/install.sh", "aetherfy.com serves no install.sh — the URL 404s"},
		{"brew install", "no Homebrew tap exists and there are no releases to package"},
		{"github.com/l-td/aetherfy-cli/releases", "this repository has no tagged releases"},
	}

	for i, line := range strings.Split(readme, "\n") {
		// Only executable-looking lines: a sentence explaining that these are
		// not published yet is exactly what should be there instead.
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "curl ") && !strings.HasPrefix(trimmed, "brew ") &&
			!strings.Contains(trimmed, "](https://github.com/") {
			continue
		}
		for _, u := range unshipped {
			if strings.Contains(line, u.fragment) {
				assert.Fail(t, "README advertises an install path that does not work",
					"README.md:%d — %s\n\n"+
						"IF YOU JUST CUT THE FIRST RELEASE: this guard is the thing that is "+
						"out of date, not your README. The rule it encodes — \"no release "+
						"exists, so no download path can be documented\" — expires the moment "+
						"one does. Delete the matching entry from `unshipped` in %s, re-point "+
						"scripts/install.sh's header at the now-live URL, and restore the "+
						"homebrew_casks block in .goreleaser.yaml with a tap repository that "+
						"exists. Do not silence this by rewording the README.",
					i+1, u.why, "test/suggested_commands_test.go")
			}
		}
	}
}
