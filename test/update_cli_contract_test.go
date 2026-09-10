package test

// Pins `afy upgrade` at the binary level: the two things no in-process test can
// see, which are the exit code and whether anything was written to disk.
//
// --check is the flag people reach for precisely because they do not want to be
// updated yet — in a script, in CI, on a machine mid-deploy. "Changes nothing"
// has to be a property of the command, not a claim in its help text, and the
// only way to know is to run it and look at the filesystem afterwards.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/release"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runCLIIn is runCLI (whoami_exit_contract_test.go) with a working directory,
// so "did anything appear on disk?" can be asked of a directory nothing else
// touches. That file is left alone deliberately.
func runCLIIn(t *testing.T, workDir, bin string, env []string, args ...string) (string, string, int) {
	t.Helper()

	clean := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "AETHERFY_") {
			continue
		}
		clean = append(clean, kv)
	}

	run := exec.Command(bin, args...)
	run.Dir = workDir
	run.Env = append(clean, env...)

	var stdout, stderr bytes.Buffer
	run.Stdout = &stdout
	run.Stderr = &stderr
	err := run.Run()

	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "could not run %s", bin)
	return stdout.String(), stderr.String(), exitErr.ExitCode()
}

func hashFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func entryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// The pseudo-version this test's probe binary reports. Shaped like the one the
// toolchain embeds into a build from a working tree: v0.0.0, a UTC timestamp, a
// twelve-character commit.
//
// WHY IT IS STAMPED RATHER THAN INHERITED. This test used to build with plain
// `go build` and assert whatever version came out, on the reasoning that a
// source build always reports a pseudo-version. That holds only while HEAD
// carries no tag. From Go 1.24 the toolchain fills info.Main.Version from the
// VCS, so on a TAGGED commit the same `go build` reports the tag, the binary
// is indistinguishable from a release, and `afy upgrade` correctly declines to
// refuse it. release.yml runs `go test ./...` ON the tag, so the test could
// never pass there and the first release is where that surfaced.
//
// The rule under test is about the version a binary REPORTS. So the test says
// what that version is instead of borrowing the checkout's.
const sourceBuildTestVersion = "v0.0.0-20260101120000-0123456789ab"

// The refusal, end to end: `afy upgrade` must not replace a binary that was
// built rather than downloaded, because a release archive cannot put it back.
func TestUpdateRefusesToReplaceABuildFromSource(t *testing.T) {
	// The fixture really is in the population the rule targets, checked with the
	// product's own predicate. A literal that drifted into looking like a release
	// would otherwise leave this asserting only that `afy upgrade` reaches for
	// the network.
	require.False(t, release.IsReleaseBuild(sourceBuildTestVersion),
		"%s must be a version afy upgrade refuses, or this test says nothing about the refusal",
		sourceBuildTestVersion)

	// Stamped through the symbol .goreleaser.yaml uses, resolved from that file:
	// -X against a symbol that does not exist is ignored by the linker, so a
	// hardcoded path would silently build an UNSTAMPED binary.
	bin := buildCLIWithLdflags(t, "-X "+releaseVersionSymbol(t)+"="+sourceBuildTestVersion)
	workDir := t.TempDir()
	configDir := t.TempDir()

	before := hashFile(t, bin)

	stdout, stderr, code := runCLIIn(t, workDir, bin,
		[]string{"AETHERFY_CONFIG_DIR=" + configDir}, "upgrade", "--check")

	assert.Equal(t, 1, code,
		"a refused update must exit non-zero so a script notices.\nstdout: %q\nstderr: %q", stdout, stderr)

	// The message has to be actionable, not just a no: it names the version it
	// found, how that version got there, and the override.
	assert.Contains(t, stderr, "--force", "the refusal must name the override")
	assert.Contains(t, stderr, sourceBuildTestVersion,
		"the refusal must quote the version it detected, or the user cannot tell which "+
			"build it is talking about. Seeing a DIFFERENT version here also means the -X "+
			"stamp above did not take, so the binary under test is not the one intended.")

	assert.Equal(t, before, hashFile(t, bin),
		"the refused update replaced the binary anyway — this is the failure the refusal exists to prevent")
}

// The sentinel path, end to end, WITHOUT this test ever spelling the sentinel.
//
// `go build -buildvcs=false` embeds no VCS data, so info.Main.Version is
// "(devel)", the fallback in pkg/version correctly refuses it, and Version is
// left holding its actual default. The binary below therefore reports whatever
// pkg/version defaults to — the real value, not a literal copied into a test —
// and `afy upgrade` must refuse it.
//
// Rename that sentinel and this test follows automatically. A test that pinned
// the string "dev" would go green through the very rename that breaks the rule.
func TestUpdateRefusesAnUnstampedBuild(t *testing.T) {
	name := "afy"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)

	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin, "./cmd/afy")
	build.Dir = ".."
	out, err := build.CombinedOutput()
	require.NoError(t, err, "go build failed:\n%s", out)

	// Positive control on the FIXTURE, not on the rule: if -buildvcs=false ever
	// stops producing an unstamped build, the refusal below would be asserting
	// something about a pseudo-version instead and would pass for the wrong
	// reason.
	stdout, stderr, code := runCLIIn(t, t.TempDir(), bin, []string{"AETHERFY_CONFIG_DIR=" + t.TempDir()}, "version")
	require.Equal(t, 0, code, "`afy version` must work.\nstdout: %q\nstderr: %q", stdout, stderr)
	reported := mustMatch(t, stdout, "`afy version` Version field", reportedVersionField)
	require.NotRegexp(t, `-\d{14}-[0-9a-f]{12}`, reported,
		"-buildvcs=false produced a pseudo-version (%q), so this test is no longer exercising the "+
			"unstamped-build path at all", reported)

	before := hashFile(t, bin)
	stdout, stderr, code = runCLIIn(t, t.TempDir(), bin,
		[]string{"AETHERFY_CONFIG_DIR=" + t.TempDir()}, "upgrade", "--check")

	assert.Equal(t, 1, code,
		"a build reporting %q — pkg/version's default, i.e. nothing stamped it — must be refused.\n"+
			"stdout: %q\nstderr: %q", reported, stdout, stderr)
	assert.Contains(t, stderr, reported,
		"the refusal must quote the version it detected")
	assert.Equal(t, before, hashFile(t, bin),
		"the refused update replaced the binary anyway")
}

// --check writes nothing. Asserted against a working directory, a config
// directory and the binary's own directory, because a self-updater has three
// plausible places to leave something behind: where it was run, where it keeps
// state, and beside the binary it stages a replacement next to.
func TestUpdateCheckWritesNothing(t *testing.T) {
	bin := buildCLI(t)
	binDir := filepath.Dir(bin)

	cases := []struct {
		name string
		args []string
	}{
		{
			name: "on this build, which is refused before any network call",
			args: []string{"upgrade", "--check"},
		},
		{
			// --force gets past the refusal and reaches the network, which is
			// the branch that downloads. Whether it finds a release or fails to
			// is not asserted — the point is that neither outcome writes.
			name: "with --force, which reaches the resolve-latest path",
			args: []string{"upgrade", "--check", "--force"},
		},
	}
	require.NotEmpty(t, cases, "the table is empty — this test asserts nothing")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if testing.Short() && strings.Contains(tc.name, "--force") {
				t.Skip("reaches github.com; skipped under -short")
			}

			workDir := t.TempDir()
			configDir := t.TempDir()
			before := hashFile(t, bin)

			stdout, stderr, code := runCLIIn(t, workDir, bin,
				[]string{"AETHERFY_CONFIG_DIR=" + configDir}, tc.args...)
			t.Logf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)

			assert.Empty(t, entryNames(t, workDir),
				"`afy %s` left a file in the working directory", strings.Join(tc.args, " "))
			assert.Empty(t, entryNames(t, configDir),
				"`afy %s` wrote into the config directory", strings.Join(tc.args, " "))
			assert.Equal(t, []string{filepath.Base(bin)}, entryNames(t, binDir),
				"`afy %s` left something beside the binary — a staged replacement or a .old backup",
				strings.Join(tc.args, " "))
			assert.Equal(t, before, hashFile(t, bin),
				"`afy %s` modified the binary it was asked only to ask about",
				strings.Join(tc.args, " "))
		})
	}
}

// Positive control for the two tests above. Without it they would both pass
// against an `update` command that did nothing at all, or did not exist: a
// command that never runs writes nothing and exits non-zero.
func TestUpdateCommandIsWiredIntoTheCLI(t *testing.T) {
	bin := buildCLI(t)

	stdout, stderr, code := runCLIIn(t, t.TempDir(), bin,
		[]string{"AETHERFY_CONFIG_DIR=" + t.TempDir()}, "upgrade", "--help")

	require.Equal(t, 0, code, "`afy upgrade --help` must work.\nstdout: %q\nstderr: %q", stdout, stderr)
	for _, flag := range []string{"--check", "--force", "--version"} {
		assert.Contains(t, stdout, flag, "`afy upgrade` must offer %s", flag)
	}

	// It must not be behind checkAuth(): updating the CLI needs no account, and
	// a logged-out user on an old build is exactly who needs it. checkAuth
	// exits 3 with this line.
	assert.NotContains(t, stderr, notLoggedInHint,
		"`afy upgrade` asked for credentials; replacing the binary requires no Aetherfy account")
}
