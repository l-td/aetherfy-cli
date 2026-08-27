package test

// Ties the Go floor in go.mod to every toolchain pin in .github/workflows, and
// pins that floor at or above the version the build-stamp feature needs.
//
// This exists because of a real defect. pkg/version's build-info fallback reads
// info.Main.Version, which `go build` only stamps from Go 1.24 onward — below
// that it is "(devel)", which the fallback correctly refuses. The workflows
// pinned 1.21 while development happened on 1.25, so the feature worked
// locally, TestVersionReportsTheEmbeddedBuildStamp passed locally, and the
// commit was red the moment CI ran it. Nothing connected the version the
// FEATURE needs to the version CI actually installs.
//
// Two assertions, deliberately independent:
//
//   - agreement: go.mod and every workflow pin name the same version. Catches
//     a pin that drifts, or a new workflow added with a stale copy.
//   - floor: that agreed version is >= 1.24. Agreement alone is satisfied by
//     moving everything DOWN in lockstep, which is exactly the state that
//     shipped the defect.
//
// Neither implies the other, and the falsifiability controls prove it: setting
// one pin back reds only the first, and lowering everything together reds only
// the second.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lowest Go that stamps info.Main.Version for a plain `go build`.
const (
	buildInfoStampMajor = 1
	buildInfoStampMinor = 24
)

var (
	goModGoLine   = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+(?:\.\d+)?)\s*$`)
	goVersionPin  = regexp.MustCompile(`(?m)^\s*go-version:\s*['"]?([^'"\s]+)['"]?\s*$`)
	goVersionOnly = regexp.MustCompile(`^(\d+)\.(\d+)`)
)

// parseGoVersion turns "1.24", "1.24.3" or "1.24.x" into (1, 24).
//
// Floating pins ("stable", "oldstable") are rejected rather than skipped: they
// name no floor at all, so accepting one would let the agreement assertion
// below compare against nothing while still reading as green.
func parseGoVersion(t *testing.T, where, raw string) (int, int) {
	t.Helper()
	m := goVersionOnly.FindStringSubmatch(raw)
	require.NotNil(t, m,
		"%s: %q is not a pinned Go version. Floating pins like \"stable\" name no floor, "+
			"so this guard could not tell whether CI runs a toolchain new enough for the "+
			"build-stamp feature. Pin an explicit major.minor.", where, raw)
	major, err := strconv.Atoi(m[1])
	require.NoError(t, err)
	minor, err := strconv.Atoi(m[2])
	require.NoError(t, err)
	return major, minor
}

// goModFloor returns the major/minor on go.mod's `go` line.
func goModFloor(t *testing.T) (int, int) {
	t.Helper()
	raw := mustMatch(t, readSuggestionSource(t, "go.mod"), "go.mod `go` directive", goModGoLine)
	return parseGoVersion(t, "go.mod", raw)
}

// workflowGoPins returns every `go-version:` in .github/workflows, labelled by
// file and line so a failure names the one that drifted.
func workflowGoPins(t *testing.T) map[string]string {
	t.Helper()
	const dir = ".github/workflows"

	entries, err := os.ReadDir(filepath.Join("..", filepath.FromSlash(dir)))
	require.NoError(t, err, "cannot read %s — has it moved? This guard scans it by directory, not by a list.", dir)

	pins := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !(strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml")) {
			continue
		}
		rel := dir + "/" + e.Name()
		for i, line := range strings.Split(readSuggestionSource(t, rel), "\n") {
			if m := goVersionPin.FindStringSubmatch(line); m != nil {
				pins[rel+":"+strconv.Itoa(i+1)] = m[1]
			}
		}
	}
	return pins
}

func TestWorkflowGoPinsAgreeWithTheModuleFloor(t *testing.T) {
	major, minor := goModFloor(t)
	floor := strconv.Itoa(major) + "." + strconv.Itoa(minor)

	pins := workflowGoPins(t)

	// Anti-vacuity. Every assertion below runs inside the loop, so an
	// extraction that found nothing would agree with itself and report green
	// while checking not one pin.
	require.NotEmpty(t, pins,
		"found ZERO go-version pins under .github/workflows — the scan is dead and this "+
			"test asserts nothing. Either the pins moved (setup-go's go-version-file?) or "+
			"the regex no longer matches their shape.")

	for where, raw := range pins {
		pinMajor, pinMinor := parseGoVersion(t, where, raw)
		assert.Equal(t, [2]int{major, minor}, [2]int{pinMajor, pinMinor},
			"TOOLCHAIN PIN DRIFT.\n"+
				"  go.mod declares the floor: %s\n"+
				"  %s pins:                   %s\n"+
				"CI would run a different toolchain than the module targets. When these "+
				"disagreed before, the build-stamp feature worked locally and the suite was "+
				"red in CI. Change both or neither.",
			floor, where, raw)
	}
}

// The floor itself. Separate from agreement above: moving go.mod AND every pin
// down together keeps them agreeing, and that is precisely the configuration
// that shipped the defect this guard exists for.
func TestModuleFloorSupportsBuildInfoStamping(t *testing.T) {
	major, minor := goModFloor(t)

	ok := major > buildInfoStampMajor || (major == buildInfoStampMajor && minor >= buildInfoStampMinor)
	assert.True(t, ok,
		"GO FLOOR TOO LOW: go.mod declares %d.%d, but this module needs >= %d.%d.\n\n"+
			"pkg/version's build-info fallback fills Version from info.Main.Version, which "+
			"`go build` only stamps from Go %d.%d onward. Below that floor the toolchain "+
			"reports \"(devel)\", the fallback correctly refuses it, and:\n"+
			"  - TestVersionReportsTheEmbeddedBuildStamp is RED, and\n"+
			"  - every source build silently reports \"dev\" again, which is the whole bug "+
			"the fallback was written to fix.\n\n"+
			"Raising this floor is a supported-versions decision. Do not lower it to go green.",
		major, minor, buildInfoStampMajor, buildInfoStampMinor,
		buildInfoStampMajor, buildInfoStampMinor)
}
