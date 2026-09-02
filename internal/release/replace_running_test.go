package release

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureSource is a stand-in binary. It prints a marker so a test can tell
// WHICH build is currently in place, exits 0 for any argument (so it satisfies
// the `version` check ReplaceExecutable runs), and blocks on stdin when asked —
// which is how a test gets a genuinely RUNNING executable.
const fixtureSource = `package main

import (
	"bufio"
	"fmt"
	"os"
)

func main() {
	fmt.Println("__MARKER__")
	if len(os.Args) > 1 && os.Args[1] == "block" {
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
}
`

// buildFixture compiles a real executable that identifies itself as marker.
func buildFixture(t *testing.T, out, marker string) string {
	t.Helper()
	src := t.TempDir()
	body := strings.Replace(fixtureSource, "__MARKER__", marker, 1)
	require.NoError(t, os.WriteFile(filepath.Join(src, "main.go"), []byte(body), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "go.mod"), []byte("module fixture\n\ngo 1.24\n"), 0o644))

	build := exec.Command("go", "build", "-o", out, ".")
	build.Dir = src
	combined, err := build.CombinedOutput()
	require.NoError(t, err, "go build failed:\n%s", combined)
	return out
}

// whichBuildIsInPlace runs the binary at path and returns its marker. Reading
// bytes would only prove a file was copied; running it proves the thing that
// matters, which is that the user has a CLI that starts.
func whichBuildIsInPlace(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command(path).CombinedOutput()
	require.NoError(t, err, "the binary at %s does not run:\n%s", path, out)
	return strings.TrimSpace(string(out))
}

func exeSuffix() string {
	if os.PathSeparator == '\\' {
		return ".exe"
	}
	return ""
}

// installedFixture lays out a fake install directory holding a working binary.
func installedFixture(t *testing.T, marker string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), BinaryName+exeSuffix())
	return buildFixture(t, target, marker)
}

func TestReplaceExecutableSwapsOneWorkingBinaryForAnother(t *testing.T) {
	target := installedFixture(t, "original")
	replacement := buildFixture(t, filepath.Join(t.TempDir(), "new"+exeSuffix()), "replacement")

	require.NoError(t, ReplaceExecutable(target, replacement))

	assert.Equal(t, "replacement", whichBuildIsInPlace(t, target),
		"the update did not take effect")

	if os.PathSeparator != '\\' {
		info, err := os.Stat(target)
		require.NoError(t, err)
		// os.CreateTemp makes the staged file 0600. Forgetting the chmod leaves
		// the user with correct bytes they cannot execute.
		assert.NotZero(t, info.Mode().Perm()&0o111, "the replacement is not executable (mode %v)", info.Mode().Perm())
	}

	// Nothing staged may survive beside the target.
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers, "a staged temp file was left in the install directory")
}

// THE POINT OF THE WHOLE ROLLBACK PATH. A release can be bad — a corrupt but
// checksum-valid upload, a binary built against a newer libc than this machine
// has. install.sh runs the installed binary and fails loudly; before this, the
// updater replaced and reported success without ever executing anything, so a
// bad release left the user with no working CLI and no way back.
func TestReplaceExecutableRollsBackWhenTheReplacementDoesNotRun(t *testing.T) {
	target := installedFixture(t, "original")

	// A file that is not an executable at all: the simplest replacement that
	// passes a checksum and cannot possibly run.
	broken := filepath.Join(t.TempDir(), "broken"+exeSuffix())
	require.NoError(t, os.WriteFile(broken, []byte("this is not a binary\n"), 0o755))

	err := ReplaceExecutable(target, broken)

	require.Error(t, err, "replacing a working binary with one that cannot run must FAIL, not report success")
	assert.Contains(t, err.Error(), "does not run",
		"the message must say the download is the problem: %v", err)
	assert.Contains(t, err.Error(), "restored",
		"the message must tell the user their binary is back: %v", err)

	assert.Equal(t, "original", whichBuildIsInPlace(t, target),
		"THE USER HAS BEEN LEFT WITH A BROKEN CLI — the rollback did not happen")

	leftovers, err2 := filepath.Glob(filepath.Join(filepath.Dir(target), "*"))
	require.NoError(t, err2)
	assert.Len(t, leftovers, 1, "rollback left debris beside the binary: %v", leftovers)
}

// The claim the Windows strategy rests on: a running executable cannot be
// deleted or written over, but it CAN be renamed.
//
// Asserted against a genuinely running process, not a file with an os.Open
// handle on it. Those are not the same and the difference bites in both
// directions: Go's os.Open takes FILE_SHARE_READ|FILE_SHARE_WRITE and NOT
// FILE_SHARE_DELETE, so an os.Open'd file refuses the rename a running image —
// which the loader maps with share-delete — permits. A test built on that
// stand-in fails on correct code and would have been "fixed" by making the code
// worse.
func TestReplaceExecutableReplacesAnExecutableThatIsRunning(t *testing.T) {
	target := installedFixture(t, "original")
	replacement := buildFixture(t, filepath.Join(t.TempDir(), "new"+exeSuffix()), "replacement")

	proc := exec.Command(target, "block")
	stdin, err := proc.StdinPipe()
	require.NoError(t, err)
	stdout, err := proc.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, proc.Start())
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = proc.Process.Kill()
		_ = proc.Wait()
	})

	// Do not race the replacement against process startup: on Windows the image
	// is not mapped until the loader has run, and replacing the file a moment
	// too early would test nothing while looking like it passed.
	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err, "the fixture never started")
	require.Contains(t, line, "original")

	require.NoError(t, ReplaceExecutable(target, replacement),
		"replacing a RUNNING executable is the only case `afy upgrade` ever has; if this fails, "+
			"the command cannot update anything")

	assert.Equal(t, "replacement", whichBuildIsInPlace(t, target))

	// Informational, not asserted: on Windows the .old is expected to survive
	// until the process exits, which is the case ReplaceExecutable tolerates.
	if _, statErr := os.Stat(BackupPath(target)); statErr == nil {
		t.Logf("%s survived the update, as expected while the old image is still mapped", BackupPath(target))
	}
}

// The tolerance branch. On Windows the .old may still be held open by this very
// process, so removing it can fail — and an update that has ALREADY SUCCEEDED
// and been verified must not report failure over a leftover file the next run
// will clear.
func TestReplaceExecutableToleratesABackupItCannotRemove(t *testing.T) {
	target := installedFixture(t, "original")
	replacement := buildFixture(t, filepath.Join(t.TempDir(), "new"+exeSuffix()), "replacement")

	original := removeBackup
	t.Cleanup(func() { removeBackup = original })
	called := 0
	removeBackup = func(string) error {
		called++
		return os.ErrPermission
	}

	require.NoError(t, ReplaceExecutable(target, replacement),
		"the swap succeeded and was verified; only the .old cleanup failed. Reporting that as a "+
			"failed update would send the user chasing a problem that does not exist")
	assert.Equal(t, 1, called, "the seam was never reached — this test proved nothing")

	assert.Equal(t, "replacement", whichBuildIsInPlace(t, target))
	assert.FileExists(t, BackupPath(target), "the .old is left for the next run to clear")
}

// A leftover .old from a previous update must not block the next one.
func TestReplaceExecutableClearsAStaleBackupFirst(t *testing.T) {
	target := installedFixture(t, "original")
	replacement := buildFixture(t, filepath.Join(t.TempDir(), "new"+exeSuffix()), "replacement")
	require.NoError(t, os.WriteFile(BackupPath(target), []byte("left by a previous run"), 0o644))

	require.NoError(t, ReplaceExecutable(target, replacement))

	assert.Equal(t, "replacement", whichBuildIsInPlace(t, target))
	assert.NoFileExists(t, BackupPath(target), "the stale backup should have been cleared")
}

// The pre-download permission probe must leave nothing behind — it runs on the
// real update path, in the user's install directory.
func TestEnsureReplaceableWritesNothingDurable(t *testing.T) {
	target := filepath.Join(t.TempDir(), BinaryName+exeSuffix())
	require.NoError(t, os.WriteFile(target, []byte("binary"), 0o755))

	require.NoError(t, EnsureReplaceable(target))

	entries, err := os.ReadDir(filepath.Dir(target))
	require.NoError(t, err)
	require.Len(t, entries, 1, "the probe left a file behind: %v", entries)
	assert.Equal(t, filepath.Base(target), entries[0].Name())
}
