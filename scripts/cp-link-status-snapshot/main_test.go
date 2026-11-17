package main

// The link-status generator asks the one provenance rule (cperrors.DescribeCheckout / Unshareable) before it
// extracts or writes anything, on a real pushed checkout. The case that shows the rule is the stricter one: the
// uncommitted Python is NOT the link model's file, which the per-file rule this replaced let through.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/l-td/aetherfy-cli/test/cplink"
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

// aPushedControlPlane holds the link model's file and one other Python file, committed and pushed.
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
	write(t, work, cplink.SourcePath, "\n")
	write(t, work, "shared/elsewhere.py", "X = 1\n")
	gitT(t, work, "add", "-A")
	gitT(t, work, "commit", "-qm", "model")
	gitT(t, work, "push", "-q", "-u", "origin", "main")
	return work
}

func TestTheGeneratorRefusesUncommittedPythonOutsideTheModelAndWritesNothing(t *testing.T) {
	cp := aPushedControlPlane(t)
	repo := t.TempDir()
	write(t, cp, "shared/elsewhere.py", "X = 2\n") // not the link model's file

	err := run(repo, cp)

	require.Error(t, err)
	assert.Equal(t, "the control-plane checkout has uncommitted Python: shared/elsewhere.py.\n"+
		"  Commit and push the control-plane change first, then regenerate. The snapshot is what\n"+
		"  CI reads, so it must describe a surface CI could also reach.", err.Error())
	_, statErr := os.Stat(filepath.Join(repo, filepath.FromSlash(cplink.SnapshotPath)))
	assert.True(t, os.IsNotExist(statErr), "a snapshot was written from an unpushed control plane")
}

func TestCONTROLAPushedControlPlaneIsNotRefusedForWhereItIs(t *testing.T) {
	// The same checkout, clean and pushed, goes past the provenance check and fails later, on its empty model:
	// a different refusal, so the one above is the provenance check's.
	cp := aPushedControlPlane(t)

	err := run(t.TempDir(), cp)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "Commit and push the control-plane change first")
}
