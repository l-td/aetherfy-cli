package test

// Gates the one cross-repo claim this repository makes about another.
//
// scripts/install.sh's header states that https://aetherfy.com/install.sh is
// served — a 307 to this repo's raw scripts/install.sh — and the README
// documents `curl -fsSL https://aetherfy.com/install.sh | bash` on the strength
// of it. That redirect lives entirely in aetherfy-dashboard's
// landing/next.config.js. Nothing here could see it, so both files asserted a
// fact about a repository they cannot read: delete the redirect and this side
// keeps promising a URL that 404s, discovered by a user running the one command
// the README puts first.
//
// LIMIT, deliberately stated rather than hidden: this reads the sibling's
// WORKING TREE. Green here proves a developer has the redirect on disk. It does
// NOT prove the redirect is committed, merged, or deployed — landing ships from
// master, so there is a real window in which this test is green and the URL
// still 404s. It catches deletion and retargeting, which is what it is for; it
// is not a production probe.
//
// SECOND LIMIT, found by mutating the sibling and watching this stay green:
// `go test` CACHES this result. The cache keys on inputs it can attribute, and
// the sibling's next.config.js lives outside this module, so editing or
// deleting the redirect does NOT invalidate a previous pass — the run reports
// "ok (cached)" against a file that no longer says what it did. Re-run with
// -count=1 to make this guard authoritative after touching the sibling. There
// is no in-test fix; the cache cannot be told about a file it does not track.
//
// CI checks out only this repository, so an absent sibling logs and passes.
// That is the same shape docs-site uses for its `sources:` existence checks.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Points this guard at an aetherfy-dashboard checkout that is not the default
// sibling location. Set but wrong is an error, not a skip — see dashboardRoot.
const dashboardRootEnv = "AETHERFY_DASHBOARD_ROOT"

// Where the sibling normally sits, relative to this repo's root.
const dashboardSibling = "../aetherfy-dashboard"

// The redirect object, tolerating the line break prettier puts before a long
// destination. Anchored on `source` so it cannot match one of the /docs entries.
//
// The destination may be written either way, and both are normal in that file:
// a quoted literal (group 1), or a const identifier (group 2) — the convention
// its /docs redirects already follow with DOCS_ORIGIN. Accepting only literals
// would red the moment someone hoisted the URL to a const, which is a tidy-up,
// not a broken contract.
var installRedirect = regexp.MustCompile(
	`source:\s*['"]/install\.sh['"]\s*,\s*destination:\s*(?:['"]([^'"]+)['"]|([A-Za-z_$][\w$]*))`)

// A top-level `const NAME = '...'`, for resolving an identifier destination.
func constAssignment(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*const\s+` + regexp.QuoteMeta(name) + `\s*=\s*['"]([^'"]+)['"]`)
}

// dashboardRoot resolves the sibling checkout, reporting whether one was found.
//
// An env override that points at nothing FAILS rather than skipping: someone
// who set it meant to run this check, and silently downgrading that to a pass
// is how a guard ends up never running anywhere.
func dashboardRoot(t *testing.T) (string, bool) {
	t.Helper()

	if override := os.Getenv(dashboardRootEnv); override != "" {
		info, err := os.Stat(override)
		require.NoError(t, err,
			"%s=%q but that path does not exist. Unset it to fall back to %s, or point it "+
				"at a real aetherfy-dashboard checkout.", dashboardRootEnv, override, dashboardSibling)
		require.True(t, info.IsDir(), "%s=%q is not a directory", dashboardRootEnv, override)
		return override, true
	}

	// test/ sits one level under the repo root, so the sibling is two up.
	path := filepath.Join("..", filepath.FromSlash(dashboardSibling))
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path, true
	}
	return "", false
}

func TestLandingRedirectPointsAtThisRepositoriesInstaller(t *testing.T) {
	root, found := dashboardRoot(t)
	if !found {
		t.Logf("SKIPPED: no aetherfy-dashboard checkout at %s and %s is unset. "+
			"The redirect backing scripts/install.sh's header is therefore UNVERIFIED here — "+
			"this is expected in CI, which checks out only this repository.",
			dashboardSibling, dashboardRootEnv)
		return
	}

	rel := filepath.Join("landing", "next.config.js")
	body, err := os.ReadFile(filepath.Join(root, rel))
	require.NoError(t, err,
		"found an aetherfy-dashboard checkout at %s but could not read %s. If the landing "+
			"config moved, this guard needs re-pointing — it must not silently stop checking.",
		root, rel)
	config := strings.ReplaceAll(string(body), "\r\n", "\n")

	// Refuse to vacuum. A config with no redirects() at all is a restructure,
	// not a pass: the assertion below would find nothing and agree with itself.
	require.Contains(t, config, "redirects(",
		"%s has no redirects() — the scan is dead. scripts/install.sh's header claims "+
			"aetherfy.com/install.sh is served from there; if redirects moved to middleware "+
			"or a rewrite, re-point this guard and that header together.", rel)

	m := installRedirect.FindStringSubmatch(config)
	require.NotNil(t, m,
		"%s has no redirect whose source is '/install.sh'.\n\n"+
			"scripts/install.sh's header and the README both tell users to "+
			"`curl -fsSL https://aetherfy.com/install.sh | bash`. Without that redirect the "+
			"URL 404s and every one of those installs fails. Restore it, or change this "+
			"repository's header and README in the same breath.\n\n"+
			"(If the redirect is present but shaped differently — keys reordered, a computed "+
			"destination — teach this pattern the new shape rather than deleting the guard.)",
		rel)

	destination := m[1]
	if destination == "" {
		// An identifier destination — resolve it to the string it names, and
		// fail loudly if it cannot be resolved rather than comparing "" against
		// the expected prefix and reporting a confusing mismatch.
		name := m[2]
		dm := constAssignment(name).FindStringSubmatch(config)
		require.NotNil(t, dm,
			"%s sends /install.sh to `%s`, but no top-level `const %s = '...'` string could be "+
				"found in that file to resolve it. If it is imported, computed, or built from "+
				"an env var, this guard can no longer read the destination statically — teach "+
				"it the new shape rather than deleting it.", rel, name, name)
		destination = dm[1]
		t.Logf("destination resolved via `const %s`", name)
	}

	// Derived from the module path, not written out again: a repository rename
	// that updated go.mod would otherwise leave this pinning the old slug and
	// still passing against a stale literal.
	rawPrefix := "https://raw.githubusercontent.com/" + strings.TrimPrefix(realModulePath, "github.com/") + "/"

	assert.True(t, strings.HasPrefix(destination, rawPrefix),
		"CROSS-REPO REDIRECT RETARGETED.\n"+
			"  %s sends /install.sh to: %s\n"+
			"  this repository is:      %s\n"+
			"That URL no longer serves THIS repo's installer, but scripts/install.sh's header "+
			"still claims it does.", rel, destination, realModulePath)

	assert.True(t, strings.HasSuffix(destination, "/scripts/install.sh"),
		"%s sends /install.sh to %q, which is not scripts/install.sh. The header in that "+
			"file names this redirect as the other end of a single source of truth; if the "+
			"script moved, both sides move together.", rel, destination)
}
