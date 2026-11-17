package main

// The generator refuses a control-plane checkout nobody else can reach before it extracts or writes anything
// (cperrors.DescribeCheckout / Unshareable), on a real pushed checkout holding the registries it reads.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/l-td/aetherfy-cli/test/cperrors"
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

// aPushedControlPlane holds an empty file at every registry path the extractor reads, committed and pushed.
func aPushedControlPlane(t *testing.T) (work string, registry string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH")
	}
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitT(t, filepath.Dir(origin), "init", "-q", "--bare", "-b", "main", origin)
	work = filepath.Join(t.TempDir(), "aetherfy-control-plane")
	gitT(t, filepath.Dir(work), "clone", "-q", origin, work)
	gitT(t, work, "symbolic-ref", "HEAD", "refs/heads/main")
	paths := cperrors.MissingSources(t.TempDir()) // every registry path, read from the extractor's own list
	require.NotEmpty(t, paths)
	for _, p := range paths {
		full := filepath.Join(work, filepath.FromSlash(p))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte("\n"), 0o644))
	}
	gitT(t, work, "add", "-A")
	gitT(t, work, "commit", "-qm", "registries")
	gitT(t, work, "push", "-q", "-u", "origin", "main")
	return work, paths[0]
}

func TestTheGeneratorRefusesUncommittedPythonAndWritesNothing(t *testing.T) {
	cp, registry := aPushedControlPlane(t)
	repo := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "test"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cp, filepath.FromSlash(registry)), []byte("X = 'X'\n"), 0o644))

	err := run(repo, cp)

	require.Error(t, err)
	assert.Equal(t, "the control-plane checkout has uncommitted Python: "+registry+".\n"+
		"  Commit and push the control-plane change first, then regenerate. The snapshot is what\n"+
		"  CI reads, so it must describe a surface CI could also reach.", err.Error())
	_, statErr := os.Stat(filepath.Join(repo, filepath.FromSlash(cperrors.SnapshotPath)))
	assert.True(t, os.IsNotExist(statErr), "a snapshot was written from an unpushed control plane")
}

func TestCONTROLAPushedControlPlaneIsNotRefusedForWhereItIs(t *testing.T) {
	// The same checkout, clean and pushed, goes past the provenance check: it fails later, on its empty
	// registries, which is a different refusal. So the one above is the provenance check's, not the extraction's.
	cp, _ := aPushedControlPlane(t)
	repo := t.TempDir()

	err := run(repo, cp)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "Commit and push the control-plane change first")
	assert.Contains(t, err.Error(), "is not trustworthy")
}
