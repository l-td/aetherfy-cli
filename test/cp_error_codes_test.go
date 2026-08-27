package test

// Guards every control-plane error code this CLI pins.
//
// The CLI switches on error-code string literals the control plane sends —
// `deploy` keys on AGENT_NOT_FOUND to offer create-on-deploy, on
// OVERAGE_CONFIRM_REQUIRED for the cost prompt and on SOFT_CAP_EXCEEDED /
// DUNNING_FROZEN for the freeze paths; the lifecycle retry keys on
// AGENT_OPERATION_IN_PROGRESS and RESOURCE_BUSY; `agents run` picks its hint
// from AGENT_NOT_DEPLOYED / AGENT_SCHEDULE_NOT_SET / AGENT_RUN_REQUIRES_JOB_TYPE;
// `github` keys on GITHUB_NOT_CONNECTED.
//
// (Those spellings are safe to name HERE, unlike prose elsewhere: this file is
// excluded from the scan, but every one of them is also a canary below, so a
// rename reds TestTheScanReachesTheRealPins rather than quietly outdating this
// paragraph.)
//
// Rename one on the control-plane side and the matching branch silently stops
// firing. Nothing crashes, no test goes red: the CLI's tests mock the server
// and agree with themselves about the spelling, so a fixture and a branch drift
// together and stay green forever while the feature is dead in production.
// Commit bf93cd1 is that exact bug — the auth code had become INVALID_API_KEY
// while this repo still said AUTH_INVALID_API_KEY — and it was found by reading,
// not by CI.
//
// So the check runs the other way: extract the control plane's registry, and
// require every code this repo pins to be either a code that registry really
// holds or an entry on the allowlist below with a stated reason. Fail-closed on
// purpose. A new SCREAMING_SNAKE literal must be classified by a human once; the
// alternative is a guard that quietly stops covering whatever was added last.
//
// A pin is either shape the repo actually uses: a whole string literal
// ("AGENT_NOT_FOUND"), or a code inside an error envelope a mocked server
// writes (`{"detail":{"code":"AGENT_NOT_FOUND",...}}`). Test fixtures count —
// a mock answering with a code the control plane no longer emits is precisely
// the drift being guarded against, and the bf93cd1 case had one of each on the
// same test case.
//
// WHAT IT DOES NOT SEE, stated plainly: a code assembled at runtime (concatenated,
// or formatted in through %s) is invisible to a source scan. Nothing in this repo
// does that today, and the codes here should stay literal so they stay checkable.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/test/cperrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests run with the working directory set to their own package directory.
const repoRoot = ".."

// This file is the one file excluded from the repo-wide scan, because it is the
// only file whose code-shaped literals are ABOUT codes rather than uses of them:
// the allowlist below names literals precisely because they are not codes, and
// the negative control names one that exists nowhere. Scanning it would make the
// guard report on its own prose.
//
// The exclusion is exactly one path and TestSkipListIsExactlyThisFile keeps it
// that way, so it cannot quietly widen into "and also the file where the next
// drift happens to live".
const guardFile = "test/cp_error_codes_test.go"

func skipList() map[string]bool { return map[string]bool{guardFile: true} }

// Code-shaped literals in this repo that are NOT control-plane error codes.
// Each needs a reason, because "it isn't a code" is a claim about intent that
// nothing else in the tree records.
var notControlPlaneCodes = map[string]string{
	// Environment variables the CLI reads.
	"AETHERFY_API_KEY":    "env var: the API key, read by internal/config/credentials.go",
	"AETHERFY_CONFIG_DIR": "env var: overrides the credentials directory",
	"NO_COLOR":            "env var: the no-color convention, honoured by internal/output",
	"XDG_CONFIG_HOME":     "env var: XDG base-directory lookup on unix",
	"AETHERFY_CP_ROOT":    "env var: points this guard at a control-plane checkout (cperrors.RootEnv)",

	"AETHERFY_DASHBOARD_ROOT": "env var: points the landing-redirect guard at an aetherfy-dashboard checkout (dashboardRootEnv)",

	// Environment variables the CLI REFUSES to let a user set as an agent
	// secret — reserved names, asserted in test/secrets_test.go.
	"AETHERFY_AGENT_ID":        "reserved agent env var, not settable as a secret",
	"AETHERFY_DATABASE_URL":    "reserved agent env var, not settable as a secret",
	"AETHERFY_INTERNAL_SECRET": "reserved agent env var, not settable as a secret",

	// Secret and env-var NAMES in test fixtures. They travel in request bodies
	// as data; nothing branches on them.
	"API_KEY":          "fixture secret name (test/workspaces_test.go)",
	"DATABASE_URL":     "fixture secret name (test/secrets_test.go)",
	"MY_AETHERFY_KEY":  "fixture secret name (test/secrets_test.go)",
	"MY_API_KEY":       "fixture secret name (test/workspaces_test.go)",
	"MY_KEY":           "fixture secret name (test/secrets_test.go)",
	"OPENAI_API_KEY":   "fixture secret name (test/secrets_test.go)",
	"SENSITIVE_KEY":    "fixture secret name (test/workspaces_test.go)",
	"SHARED_API_KEY":   "fixture secret name (test/workspaces_test.go)",
	"SHARED_CACHE_URL": "fixture secret name (test/workspaces_test.go)",
	"SHARED_DB_URL":    "fixture secret name (test/workspaces_test.go)",

	// Shell variable names read out of scripts/install.sh by the install
	// pair gate. They are substitution keys, not anything a server sends.
	"BINARY_NAME": "shell variable name in scripts/install.sh, pinned by test/install_contract_test.go",

	// Fixture constants in test/cperrors/extract_test.go, which builds a fake
	// control-plane tree to pin the extractor's form rule. They are there
	// precisely BECAUSE they are not error codes: the first four are the real
	// non-self-named constants sitting beside the one code plan_validator.py
	// owns, and the fifth is an indented assignment. That this guard demanded
	// they be classified is the mechanism working — a file full of things that
	// look like codes is exactly what must not slip in unexamined.
	"UPGRADE_URL":               "extractor-test fixture: plan_validator.py's `UPGRADE_URL = \"/billing/upgrade\"`, not self-named",
	"SUPPORT_CONTACT":           "extractor-test fixture: bound to a mailto:, not self-named",
	"FREEZE_REASON_CEILING":     "extractor-test fixture: bound to \"ceiling\", not self-named",
	"FREEZE_REASON_DUNNING":     "extractor-test fixture: bound to \"dunning\", not self-named",
	"INDENTED_NOT_MODULE_LEVEL": "extractor-test fixture: an indented assignment, not a module-level registry entry",

	// Deliberately not a control-plane code — the only literal in shipped code
	// that LOOKS like drift and is not.
	//
	// Two fixtures answer with it: internal/api/lifecycle_retry_test.go asserts a
	// 500 carrying a code the retry does not recognise fails fast, and
	// cmd/deploy_create_test.go's loop-bound server answers the second deploy
	// with it. Both need a code that means "something the client has no branch
	// for", and the control plane publishes no generic 500 code to borrow —
	// main.py wraps only RequestValidationError and OperationalError — so an
	// unrecognised string is the honest fixture. Pinning a REAL code here would
	// be the lie: it would claim the server sends that code in this situation.
	"INTERNAL_ERROR": "deliberately-unrecognised 500 body in internal/api/lifecycle_retry_test.go " +
		"and cmd/deploy_create_test.go; the control plane publishes no generic 500 code",
}

func loadSnapshot(t *testing.T) *cperrors.Snapshot {
	t.Helper()
	path := filepath.Join(repoRoot, filepath.FromSlash(cperrors.SnapshotPath))
	snap, err := cperrors.Load(path)
	require.NoError(t, err, "cannot read %s — regenerate with `go run ./%s`",
		cperrors.SnapshotPath, cperrors.GeneratorPath)
	return snap
}

func scanRepo(t *testing.T) []cperrors.Literal {
	t.Helper()
	lits, err := cperrors.ScanTree(repoRoot, skipList())
	require.NoError(t, err)
	return lits
}

// THE GUARD. Every code-shaped literal in the repo is either a real
// control-plane code or an allowlisted non-code.
func TestEveryPinnedCodeExistsInTheControlPlaneRegistry(t *testing.T) {
	snap := loadSnapshot(t)
	require.NoError(t, cperrors.Validate(snap.Registry()),
		"the committed snapshot is not trustworthy, so nothing below means anything")

	for _, lit := range scanRepo(t) {
		if _, ok := snap.Codes[lit.Value]; ok {
			continue
		}
		if _, ok := notControlPlaneCodes[lit.Value]; ok {
			continue
		}
		assert.Fail(t, "unknown control-plane error code",
			"%s:%d pins %q, which the control plane does not publish.\n\n"+
				"Two things this can be, and they need different fixes:\n"+
				"  1. DRIFT — the control plane renamed or removed the code. The branch or\n"+
				"     fixture here is dead: it will never match again. Fix the literal, and\n"+
				"     check whether the code was renamed rather than dropped.\n"+
				"  2. NOT A CODE — it is an env var, a header, a fixture value. Add it to\n"+
				"     notControlPlaneCodes in %s with a one-line reason.\n\n"+
				"If the code IS new on the control-plane side, regenerate the snapshot:\n"+
				"  go run ./%s",
			lit.File, lit.Line, lit.Value, guardFile, cperrors.GeneratorPath)
	}
}

// Anti-vacuity. Every assertion above lives inside a loop, so a scan that
// reaches nothing passes silently. These are literals the CLI genuinely
// branches on, in three different packages.
func TestTheScanReachesTheRealPins(t *testing.T) {
	seen := map[string]string{}
	for _, lit := range scanRepo(t) {
		seen[lit.Value] = lit.File
	}

	for _, canary := range []struct{ code, where string }{
		{"AGENT_NOT_FOUND", "cmd/deploy.go — the create-on-deploy branch"},
		{"OVERAGE_CONFIRM_REQUIRED", "cmd/deploy.go — the cost prompt"},
		{"SOFT_CAP_EXCEEDED", "cmd/deploy.go — the freeze path"},
		{"DUNNING_FROZEN", "cmd/deploy.go — the freeze path"},
		{"AGENT_OPERATION_IN_PROGRESS", "internal/api/agents.go — the lifecycle retry"},
		{"RESOURCE_BUSY", "internal/api/agents.go — the lifecycle retry"},
		{"GITHUB_NOT_CONNECTED", "cmd/github.go"},
		{"PLAN_LIMIT_EXCEEDED", "cmd/agents.go"},
		{"AGENT_RUN_REQUIRES_JOB_TYPE", "cmd/agents.go — run-now"},
		{"AGENT_NOT_DEPLOYED", "cmd/agents.go — run-now hint"},
		{"AGENT_SCHEDULE_NOT_SET", "cmd/agents.go — run-now hint"},
	} {
		assert.Contains(t, seen, canary.code,
			"the scan did not find %s (%s) — it is not reaching the sources, and every "+
				"assertion in TestEveryPinnedCodeExistsInTheControlPlaneRegistry is vacuous",
			canary.code, canary.where)
	}

	assert.GreaterOrEqual(t, len(seen), 20,
		"only %d distinct code-shaped literals in the whole repo — the walk is broken", len(seen))
}

// Negative control. A zero-violations result is worthless until the detector is
// shown to fire, so run the scanner over source it MUST flag, and over source it
// must not.
func TestTheScannerFiresAndIgnoresComments(t *testing.T) {
	const planted = "package p\n\n" +
		"// A doc comment naming AGENT_NOT_FOUND and \"STABLE_CODE\" as prose.\n" +
		"const real = \"AGENT_NOT_FOUND\"\n" +
		"const bogus = \"AGENT_NOT_FOUND_XYZZY\"\n" +
		"const notShaped = \"lower_case_thing\"\n" +
		"const body = `{\"detail\":{\"code\":\"EMBEDDED_BOGUS_CODE\",\"message\":\"x\"}}`\n" +
		"const help = \"set OPENAI_API_KEY in your shell\"\n"

	lits, err := cperrors.ScanGoSource("planted.go", planted)
	require.NoError(t, err)

	found := map[string]bool{}
	for _, l := range lits {
		found[l.Value] = true
	}

	assert.True(t, found["AGENT_NOT_FOUND_XYZZY"],
		"the scanner missed a planted bogus code — every clean run it reports is meaningless")
	assert.True(t, found["AGENT_NOT_FOUND"], "the scanner missed a real pin")
	assert.True(t, found["EMBEDDED_BOGUS_CODE"],
		"the scanner missed a code embedded in an error envelope — the mocked-server fixtures "+
			"write codes that way, and a stale one there is the same drift")
	assert.False(t, found["STABLE_CODE"],
		"the scanner picked a literal out of a COMMENT — internal/api/errors.go documents the "+
			"envelope with exactly that placeholder, and prose about the contract is not a pin on it")
	assert.False(t, found["lower_case_thing"], "the shape filter is not filtering")
	assert.False(t, found["OPENAI_API_KEY"],
		"the envelope matcher fired on capitals inside ordinary prose — it must match the "+
			"`\"code\": \"...\"` shape only, or every env var named in help text becomes a candidate")

	// And the planted bogus code must be one the guard would reject.
	snap := loadSnapshot(t)
	_, inSnapshot := snap.Codes["AGENT_NOT_FOUND_XYZZY"]
	assert.False(t, inSnapshot, "the planted control is a real code; pick a different one")
}

// The exclusion covers this file and nothing else.
func TestSkipListIsExactlyThisFile(t *testing.T) {
	skip := skipList()
	assert.Len(t, skip, 1, "the scan may exclude exactly one file — its own")

	_, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(guardFile)))
	assert.NoError(t, err,
		"%s does not exist — the exclusion matches nothing, and this file is being scanned "+
			"under some other name", guardFile)
}

// The allowlist is an exemption list, and exemption lists rot in two directions.
func TestAllowlistIsNotRotten(t *testing.T) {
	snap := loadSnapshot(t)

	present := map[string]bool{}
	for _, lit := range scanRepo(t) {
		present[lit.Value] = true
	}

	for value, why := range notControlPlaneCodes {
		// Membership checked by hand rather than with assert.NotContains: that
		// helper prints the map it searched, and this map is the entire 123-code
		// registry — the one name you need to fix would be buried in it.
		if _, isRealCode := snap.Codes[value]; isRealCode {
			assert.Fail(t, "an allowlisted literal is a real error code",
				"%q is allowlisted as %q, but the control plane publishes it as a real error "+
					"code. Drop the allowlist entry so the pin is checked like every other.", value, why)
		}

		assert.True(t, present[value],
			"%q is allowlisted but appears nowhere in the repo any more. Delete the entry — a "+
				"stale exemption is an exemption waiting to cover something it was never "+
				"reviewed for.", value)
	}
}

// The committed snapshot must be believable on its own terms, since CI has no
// control-plane checkout to compare it against.
func TestCommittedSnapshotIsTrustworthy(t *testing.T) {
	snap := loadSnapshot(t)

	assert.Equal(t, cperrors.SchemaVersion, snap.SchemaVersion,
		"snapshot schemaVersion %d, this guard reads %d — regenerate with `go run ./%s`",
		snap.SchemaVersion, cperrors.SchemaVersion, cperrors.GeneratorPath)
	assert.Equal(t, cperrors.GeneratorPath, snap.Generator)
	assert.NoError(t, cperrors.Validate(snap.Registry()))
}

// Where the control plane IS checked out, re-extract and red on any difference,
// so a stale snapshot cannot survive a dev build. CI checks out no siblings and
// skips this — same trust model as docs-site's cli-surface-snapshot.
func TestSnapshotMatchesTheLiveControlPlane(t *testing.T) {
	cpRoot := cperrors.Root(repoRoot)
	if !cperrors.RootExists(cpRoot) {
		t.Skipf("SKIPPED the live-drift check: no control-plane checkout at %s "+
			"(set %s to point elsewhere). The committed snapshot was checked instead.",
			cpRoot, cperrors.RootEnv)
	}

	// A checkout IS here. From this point absence is a failure, never a skip.
	//
	// These were one question once — "is the control plane present?" answered by
	// stat-ing all three registries — and that is how a guard stops guarding
	// without saying so: move shared/error_codes.py and every dev machine reports
	// "no control-plane checkout", skips, and goes green, while CI keeps validating
	// a snapshot that has quietly become fiction. The control plane is refactored
	// often and that file is exactly the kind that gets folded into a package.
	if missing := cperrors.MissingSources(cpRoot); len(missing) > 0 {
		t.Fatalf("the control plane IS checked out at %s, but %d of its error-code "+
			"registries are not where this guard looks:\n    %s\n\n"+
			"That is a move or a rename, not a missing checkout — do not read this as "+
			"'skip'. Update `sources` in test/cperrors/extract.go to the new paths, then "+
			"regenerate: go run ./%s",
			cpRoot, len(missing), strings.Join(missing, "\n    "), cperrors.GeneratorPath)
	}

	live, err := cperrors.Extract(cpRoot)
	require.NoError(t, err)
	require.NoError(t, cperrors.ValidateExtraction(live),
		"the extraction from %s is not trustworthy — refusing to compare against it, because "+
			"an empty extraction agrees with everything", cpRoot)

	snap := loadSnapshot(t)

	var missing, added, moved []string
	for name, want := range live.Codes {
		got, ok := snap.Codes[name]
		switch {
		case !ok:
			missing = append(missing, name)
		case got != want:
			moved = append(moved, name+" ("+got.Source+" → "+want.Source+")")
		}
	}
	for name := range snap.Codes {
		if _, ok := live.Codes[name]; !ok {
			added = append(added, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(added)
	sort.Strings(moved)

	if len(missing) > 0 || len(added) > 0 || len(moved) > 0 {
		assert.Fail(t, "the committed snapshot no longer matches the control plane",
			"extracted from %s\n"+
				"  new in the control plane, absent from the snapshot: %s\n"+
				"  in the snapshot, GONE from the control plane:        %s\n"+
				"  moved between registries:                            %s\n\n"+
				"Regenerate: go run ./%s\n"+
				"If a code went GONE, that is a rename or a removal — check what in this repo "+
				"pinned it before you regenerate, because the pin is now dead code.",
			cpRoot, list(missing), list(added), list(moved), cperrors.GeneratorPath)
	}

	assert.Equal(t, len(live.Sources), len(snap.Sources), "source list drifted")
}

func list(v []string) string {
	if len(v) == 0 {
		return "(none)"
	}
	return strings.Join(v, ", ")
}
