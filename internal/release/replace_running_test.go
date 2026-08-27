package release

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockerSource is a program that reports it is up and then waits. It exists to
// be a REAL running executable for the test below.
const blockerSource = `package main

import (
	"bufio"
	"fmt"
	"os"
)

func main() {
	fmt.Println("ready")
	// Blocks until stdin closes. On Windows this keeps the image mapped, which
	// is the whole point.
	bufio.NewReader(os.Stdin).ReadString('\n')
}
`

// buildBlocker compiles blockerSource to the given path.
func buildBlocker(t *testing.T, out string) {
	t.Helper()
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "main.go"), []byte(blockerSource), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "go.mod"), []byte("module blocker\n\ngo 1.24\n"), 0o644))

	build := exec.Command("go", "build", "-o", out, ".")
	build.Dir = src
	combined, err := build.CombinedOutput()
	require.NoError(t, err, "go build failed:\n%s", combined)
}

// The claim the whole Windows strategy rests on: a running executable cannot be
// deleted or written over, but it CAN be renamed out of the way.
//
// It is asserted against a genuinely running process rather than a file with an
// os.Open handle on it. Those are not the same thing and the difference bites in
// both directions: Go's os.Open takes FILE_SHARE_READ|FILE_SHARE_WRITE and NOT
// FILE_SHARE_DELETE, so an os.Open'd file refuses the rename that a running
// image — which the loader maps with share-delete — permits. A test built on the
// os.Open stand-in fails on correct code, and would have been "fixed" by making
// the code worse.
func TestReplaceExecutableReplacesAnExecutableThatIsRunning(t *testing.T) {
	installDir := t.TempDir()
	target := filepath.Join(installDir, "blocker"+exeSuffix())
	buildBlocker(t, target)

	src := filepath.Join(t.TempDir(), "replacement"+exeSuffix())
	require.NoError(t, os.WriteFile(src, []byte(newBytes), 0o755))

	proc := exec.Command(target)
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
	require.NoError(t, err, "the blocker never started")
	require.Contains(t, line, "ready")

	require.NoError(t, ReplaceExecutable(target, src),
		"replacing a RUNNING executable is the only case `afy update` ever has; if this fails, "+
			"the command cannot update anything")

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, newBytes, string(got), "the running binary's path still holds the old bytes")

	// Informational, not asserted: on Windows the .old is expected to survive
	// until the process exits, which is exactly the case replaceByRenamingAside
	// tolerates. Asserting either way would pin an OS detail, not our contract.
	if _, statErr := os.Stat(BackupPath(target)); statErr == nil {
		t.Logf("%s survived the update, as expected while the old image is still mapped", BackupPath(target))
	}
}

func exeSuffix() string {
	if os.PathSeparator == '\\' {
		return ".exe"
	}
	return ""
}
