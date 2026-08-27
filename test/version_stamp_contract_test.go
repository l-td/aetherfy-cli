package test

// Pins the build-stamp fallback in pkg/version.
//
// Version/Commit/BuildDate were only ever filled by goreleaser's ldflags, so
// every `go build` and every `go install github.com/l-td/aetherfy-cli@latest`
// reported "dev" forever — and "what version are you on?" had no answer for
// that entire population, which is everyone who did not install from a release
// archive. The data was already in the binary (`go version -m afy` shows the
// pseudo-version and the vcs.* settings); nothing read it.
//
// Build stamping is unobservable from inside the process: the values are fixed
// at link time, so a test in the same binary can only see its own stamp, never
// the one a different build would have produced. This therefore builds real
// binaries and runs them, the same shape as whoami_exit_contract_test.go.

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The version the ldflags control stamps. Implausible as a real tag on purpose,
// so it cannot be mistaken for something the toolchain embedded on its own.
const stampedTestVersion = "v9.9.9-test"

// The Version field of `afy version`'s output block (pkg/version.Full()).
// Requiring a non-space value is deliberate: a fallback that BLANKED the field
// would leave "Version:" with nothing after it, and a laxer pattern would match
// that and compare an empty string against an empty expectation.
var reportedVersionField = regexp.MustCompile(`(?m)^Version:\s*(\S+)\s*$`)

// buildCLIWithLdflags compiles the real binary with the given -ldflags and
// returns its path. buildCLI (whoami_exit_contract_test.go) covers the plain
// build; this is its stamped twin, kept separate so that file is untouched.
func buildCLIWithLdflags(t *testing.T, ldflags string) string {
	t.Helper()
	name := "afy"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)

	build := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, "./cmd/afy")
	build.Dir = ".." // test/ sits one level under the repo root
	out, err := build.CombinedOutput()
	require.NoError(t, err, "go build failed:\n%s", out)
	return bin
}

// releaseVersionSymbol returns the linker symbol .goreleaser.yaml stamps the
// version into, e.g. github.com/l-td/aetherfy-cli/pkg/version.Version.
//
// Read out of the release config rather than written down a second time here.
// `-X` against a symbol that does not exist is IGNORED by the linker — it does
// not fail the build — so a module-path rename that updated .goreleaser.yaml
// and not this file would leave the control below building an UNSTAMPED binary
// and asserting nothing, in green. Two copies of one string are zero gates.
func releaseVersionSymbol(t *testing.T) string {
	t.Helper()
	cfg := dropComments(readSuggestionSource(t, ".goreleaser.yaml"))
	return mustMatch(t, cfg, ".goreleaser.yaml builds.ldflags -X ...pkg/version.Version",
		regexp.MustCompile(`-X\s+(\S+/pkg/version\.Version)=`))
}

func TestVersionReportsTheEmbeddedBuildStamp(t *testing.T) {
	// The regression this change exists to prevent. An unstamped `go build` must
	// report the version the toolchain embedded, not the "dev" sentinel.
	t.Run("plain build reports the embedded version", func(t *testing.T) {
		bin := buildCLI(t)

		stdout, stderr, code := runCLI(t, bin, nil, "version")
		require.Equal(t, 0, code,
			"`afy version` must exit 0.\nstdout: %q\nstderr: %q", stdout, stderr)

		reported := mustMatch(t, stdout, "`afy version` Version field", reportedVersionField)

		assert.NotContains(t, reported, "dev",
			"a plain `go build` reports Version %q. The build info the toolchain embeds "+
				"(info.Main.Version plus the vcs.* settings) is supposed to fill the sentinel — "+
				"see fillFromBuildInfo in pkg/version/version.go.\n\n"+
				"MOST LIKELY CAUSE: the toolchain is older than Go 1.24. `go build` only "+
				"stamps info.Main.Version with the module pseudo-version from 1.24 onward; "+
				"before that it is \"(devel)\", which the fallback correctly refuses, so there "+
				"is nothing left to recover. Check `go version` against go.mod's floor — "+
				"TestWorkflowGoPinsAgreeWithTheModuleFloor guards that pair.\n\n"+
				"LESS LIKELY: the build ran outside a VCS working tree, or with -buildvcs=false, "+
				"which removes the same data by a different route.", reported)

		assert.True(t, strings.HasPrefix(reported, "v"),
			"a plain `go build` reports Version %q, which is not version-shaped; expected the "+
				"module pseudo-version, e.g. v0.0.0-<utc-date>-<12-char-sha>", reported)
	})

	// The positive control. Without it the subtest above passes even if the
	// fallback OVERWRITES ldflags — the failure mode that would make every
	// released binary report a pseudo-version instead of its tag, while the
	// regression test above stayed green because both values start with "v".
	t.Run("ldflags win over the fallback", func(t *testing.T) {
		symbol := releaseVersionSymbol(t)
		bin := buildCLIWithLdflags(t, "-X "+symbol+"="+stampedTestVersion)

		stdout, stderr, code := runCLI(t, bin, nil, "version")
		require.Equal(t, 0, code,
			"`afy version` must exit 0.\nstdout: %q\nstderr: %q", stdout, stderr)

		reported := mustMatch(t, stdout, "`afy version` Version field", reportedVersionField)

		assert.Equal(t, stampedTestVersion, reported,
			"a build stamped with -X %s=%s reports Version %q instead. The build-info fallback "+
				"must fill a field only while it still holds its sentinel; here it has taken "+
				"precedence over an explicit stamp, so every RELEASED binary would report a "+
				"pseudo-version rather than its tag.", symbol, stampedTestVersion, reported)

		assert.NotContains(t, reported, "v0.0.0-",
			"the stamped build reports the module pseudo-version %q — the fallback overwrote "+
				"the ldflag rather than deferring to it", reported)
	})
}
