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

// A github.com URL in prose, as opposed to a Go import path. Import paths carry
// no scheme, so requiring `https://` keeps this check on reader-facing links
// only; the module path is pinned separately, by its own rule below.
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
// This scan skips comment lines, so the commented-out cask block stays inert
// and prose that quotes the old path (as the comments above do, describing what
// was fixed) does not trip it.
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

// The module path is the fourth place this repository names itself, and it used
// to name someone else's: `github.com/aetherfy/cli`. The owner does not own the
// `aetherfy` GitHub org (they hold aetherfy-hq; this CLI lives in a personal
// account), so every import in this repo resolved into a namespace a stranger
// could claim and publish under. That is a supply-chain problem, not a cosmetic
// one, which is why it is pinned rather than left to judgement.
//
// Pinning the module path also makes the ldflags checkable, and they are the
// quiet failure here: `-X` against a symbol path that does not exist is
// silently ignored by the linker, so a half-done rename does not break the
// build — it just makes `afy version` report "dev" forever.
const realModulePath = "github.com/l-td/aetherfy-cli"

func TestModulePathIsOwnedByUs(t *testing.T) {
	// go.mod is the declaration; everything else follows it.
	gomod := readSuggestionSource(t, "go.mod")
	assert.Contains(t, gomod, "module "+realModulePath,
		"go.mod must declare the module under a namespace we control")

	// The two places the path is written by hand rather than by the compiler.
	// A stale one here does not fail the build, it fails `afy version`.
	//
	// Every -X flag is extracted and checked INDIVIDUALLY. Checking the line
	// instead lets a stale flag hide behind its correct neighbours: the
	// Makefile carries all three (-X Version, -X Commit, -X BuildDate) on one
	// line, so a line-level "contains the right path" passes while one of the
	// three silently injects nothing.
	flags := 0
	for _, rel := range []string{"Makefile", ".goreleaser.yaml"} {
		body := readSuggestionSource(t, rel)
		for i, line := range strings.Split(body, "\n") {
			for _, m := range ldflagX.FindAllStringSubmatch(line, -1) {
				flags++
				assert.Equal(t, realModulePath, m[1],
					"%s:%d — ldflag -X names %q; we own %q. `-X` against a symbol "+
						"that does not exist is IGNORED by the linker, so this does not "+
						"break the build — it ships a binary reporting version \"dev\".",
					rel, i+1, m[1], realModulePath)
			}
		}
	}
	assert.Equal(t, 6, flags,
		"expected 6 -X ldflags (3 in the Makefile, 3 in .goreleaser.yaml), found %d — "+
			"the extractor is stale and the assertions above are vacuous", flags)

	// Nothing anywhere may still IMPORT through the foreign namespace. Scanned
	// across the tracked Go sources rather than a hand-listed few, because an
	// import that survives a rename is exactly the one nobody looked at.
	//
	// Anchored to import-line shape rather than a substring search. A substring
	// search matches this file's own comments and its own detector string, so
	// the first version of this guard failed on the prose describing the bug it
	// guards against — a self-referential scan reports on itself, not on the
	// code. Import lines are a bare (optionally aliased) quoted path.
	scanned, foreign := 0, 0
	for _, dir := range []string{"cmd", "internal", "test", "pkg"} {
		for _, f := range goFilesUnder(t, dir) {
			body := readSuggestionSource(t, f)
			for i, line := range strings.Split(body, "\n") {
				if !goImportLine.MatchString(line) {
					continue
				}
				scanned++
				if strings.Contains(line, `"github.com/aetherfy/`) {
					foreign++
					assert.Fail(t, "import path in a namespace we do not own",
						"%s:%d — %s", f, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
	assert.Equal(t, 0, foreign)

	// Negative control: a zero above is worthless unless the detector fires.
	assert.Greater(t, scanned, 50,
		"only %d import lines seen — the scan is not reaching the sources", scanned)
	assert.True(t, goImportLine.MatchString(`	"github.com/aetherfy/cli/internal/api"`),
		"the import-line matcher no longer matches an import line; the zero above means nothing")
	assert.False(t, goImportLine.MatchString(`// github.com/aetherfy/cli in a comment`),
		"the import-line matcher matches comments again — it will report on its own prose")
}

// An import line: optional alias, then a quoted path. Deliberately not a
// substring search — see TestModulePathIsOwnedByUs.
var goImportLine = regexp.MustCompile(`^\s*(?:[\w.]+\s+)?"[^"]+"\s*$`)

// One `-X <module>/pkg/version.<Symbol>=...` ldflag, capturing the module path
// so each flag can be checked on its own rather than by line.
var ldflagX = regexp.MustCompile(`-X\s+(\S+?)/pkg/version\.\w+=`)

// goFilesUnder lists .go files in a repo-relative directory, so the import scan
// covers what is actually there instead of a list that rots.
func goFilesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	root := filepath.Join("..", filepath.FromSlash(dir))
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil // pkg/ may not exist; absence is not a failure
	}
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, goFilesUnder(t, dir+"/"+e.Name())...)
			continue
		}
		if strings.HasSuffix(e.Name(), ".go") {
			out = append(out, dir+"/"+e.Name())
		}
	}
	return out
}

// The marker the README carries while paths that need a tag are documented but
// cannot yet work. Its presence is what makes documenting them honest; its
// absence turns the same prose into a promise the repository cannot keep.
const removeOnFirstReleaseMarker = "<!-- remove-on-first-release -->"

// Install paths that do not exist must not be advertised as if they did.
//
// `curl -fsSL https://aetherfy.com/install.sh | bash` — aetherfy.com now 307s
// that URL to this repository's raw scripts/install.sh, configured in
// aetherfy-dashboard:landing/next.config.js. The URL resolves; the DOWNLOAD it
// performs still 404s, because no tag exists for it to fetch. `brew install
// aetherfy/tap/afy` — there is no tap, and no tagged release for one to carry.
// All three were once printed as ready-to-run commands under "Installation".
//
// The two failure modes are NOT the same, which is what okWithMarker encodes:
//
//   - The install script and the release downloads are WIRED AND WAITING. Every
//     piece exists — the redirect, the script, the release workflow, the asset
//     names — and they begin working the moment a tag is pushed, with no edit
//     to anything. Documenting them behind the marker is accurate: the reader
//     is told, in the same section, that the tag is what is missing.
//   - Homebrew has NO OTHER END. There is no tap repository, the homebrew_casks
//     block in .goreleaser.yaml is commented out, and a tag changes none of
//     that. No marker makes `brew install` true, so none is accepted for it.
//
// So this guard is now the release-day checklist rather than a blanket ban: it
// holds the marker and the documented paths together until the tag lands.
func TestReadmeDoesNotAdvertiseUnshippedInstallPaths(t *testing.T) {
	readme := readSuggestionSource(t, "README.md")

	// okWithMarker: true for paths that a tag alone turns real, and which the
	// remove-on-first-release marker therefore licenses documenting today.
	unshipped := []struct {
		fragment, why string
		okWithMarker  bool
	}{
		{"aetherfy.com/install.sh", "the URL 307s to this repo's scripts/install.sh, but the download it runs has no tagged release to fetch", true},
		{"brew install", "no Homebrew tap exists and there are no releases to package", false},
		{"github.com/l-td/aetherfy-cli/releases", "this repository has no tagged releases", true},
	}

	marked := strings.Contains(readme, removeOnFirstReleaseMarker)

	for i, line := range strings.Split(readme, "\n") {
		// Only executable-looking lines: a sentence explaining that these are
		// not published yet is exactly what should be there instead.
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "curl ") && !strings.HasPrefix(trimmed, "brew ") &&
			!strings.Contains(trimmed, "](https://github.com/") {
			continue
		}
		for _, u := range unshipped {
			if !strings.Contains(line, u.fragment) {
				continue
			}
			if u.okWithMarker {
				assert.True(t, marked,
					"the README documents an unshipped path and the remove-on-first-release "+
						"marker is gone.\n\n"+
						"README.md:%d — %s\n\n"+
						"Either restore the marker, or — if you just cut the first release — "+
						"delete these entries from `unshipped` in %s, re-point "+
						"scripts/install.sh's header, and restore homebrew_casks in "+
						".goreleaser.yaml with a tap repository that exists.\n\n"+
						"The marker is %q and belongs on the sentence in the Installation "+
						"section that names the tag as the missing piece.",
					i+1, u.why, "test/suggested_commands_test.go", removeOnFirstReleaseMarker)
				continue
			}
			// No marker licenses this one — nothing on the other end exists.
			assert.Fail(t, "README advertises an install path that does not work",
				"README.md:%d — %s\n\n"+
					"The remove-on-first-release marker does NOT cover this path: unlike the "+
					"install script and the release downloads, it does not start working when "+
					"a tag is pushed. Restore the homebrew_casks block in .goreleaser.yaml "+
					"with a tap repository that exists first, then document it. Do not "+
					"silence this by rewording the README.",
				i+1, u.why)
		}
	}
}
