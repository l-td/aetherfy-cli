package main

// The response-schemas generator asks the one provenance rule (cperrors.DescribeCheckout / Unshareable) before it
// imports or writes anything, on a real pushed checkout; and a checkout without the app's module is a move, never
// an absence.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/l-td/aetherfy-cli/test/cpschemas"
)

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{"-C", dir, "-c", "core.autocrlf=false", "-c", "commit.gpgsign=false",
		"-c", "user.name=provenance-test", "-c", "user.email=provenance-test@example.invalid"}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
}

// aPushedControlPlane holds a main.py with no app in it, and one other Python file, committed and pushed.
func aPushedControlPlane(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH")
	}
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitT(t, filepath.Dir(origin), "init", "-q", "--bare", "-b", "main", origin)
	work := filepath.Join(t.TempDir(), "aetherfy-control-plane")
	gitT(t, filepath.Dir(work), "clone", "-q", origin, work)
	gitT(t, work, "symbolic-ref", "HEAD", "refs/heads/main")
	write(t, work, "main.py", "# no app here\n")
	write(t, work, "shared/elsewhere.py", "X = 1\n")
	gitT(t, work, "add", "-A")
	gitT(t, work, "commit", "-qm", "app")
	gitT(t, work, "push", "-q", "-u", "origin", "main")
	return work
}

func TestTheGeneratorRefusesUncommittedPythonAndWritesNothing(t *testing.T) {
	cp := aPushedControlPlane(t)
	repo := t.TempDir()
	write(t, cp, "shared/elsewhere.py", "X = 2\n")

	err := run(repo, cp)

	require.Error(t, err)
	assert.Equal(t, "the control-plane checkout has uncommitted Python: shared/elsewhere.py.\n"+
		"  Commit and push the control-plane change first, then regenerate. The snapshot is what\n"+
		"  CI reads, so it must describe answers CI could also get.", err.Error())
	_, statErr := os.Stat(filepath.Join(repo, filepath.FromSlash(cpschemas.SnapshotPath)))
	assert.True(t, os.IsNotExist(statErr), "a snapshot was written from an unpushed control plane")
}

func TestCONTROLAPushedControlPlaneIsNotRefusedForWhereItIs(t *testing.T) {
	// The same checkout, clean and pushed, goes past the provenance check and fails later, importing an app that
	// is not there: a different refusal, so the one above is the provenance check's.
	cp := aPushedControlPlane(t)
	repo := t.TempDir()

	err := run(repo, cp)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "Commit and push the control-plane change first")
	_, statErr := os.Stat(filepath.Join(repo, filepath.FromSlash(cpschemas.SnapshotPath)))
	assert.True(t, os.IsNotExist(statErr), "a snapshot was written from an extraction that failed")
}

func TestACheckoutWithoutTheAppsModuleIsAMoveNotAnAbsence(t *testing.T) {
	cp := aPushedControlPlane(t)
	require.NoError(t, os.Remove(filepath.Join(cp, "main.py")))

	err := run(t.TempDir(), cp)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no main.py")
	assert.NotContains(t, err.Error(), "is not checked out")
}
