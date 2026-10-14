package link

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAFolderWithoutALinkIsNotLinked(t *testing.T) {
	_, err := Read(t.TempDir())
	assert.True(t, errors.Is(err, ErrNotLinked), "got %v", err)
}

func TestWriteAddsATargetAndKeepsTheOthers(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Write(dir, DefaultTarget, "id-prod"))
	require.NoError(t, Write(dir, "staging", "id-staging"))
	require.NoError(t, Write(dir, DefaultTarget, "id-prod-2"))

	f, err := Read(dir)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"default": "id-prod-2", "staging": "id-staging"}, f.Targets)
	assert.Equal(t, []string{"default", "staging"}, f.Names())

	raw, err := os.ReadFile(filepath.Join(dir, ".aetherfy", "agent.json"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"targets":{"default":"id-prod-2","staging":"id-staging"}}`, string(raw))
}

func TestAFileThatIsNotALinkIsRefusedAndNeverOverwritten(t *testing.T) {
	for _, body := range []string{`{`, `{"targets":{}}`, `{"targets":{"default":""}}`, `{"agent":"x"}`} {
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, Dir), 0o755))
		require.NoError(t, os.WriteFile(Path(dir), []byte(body), 0o644))

		_, err := Read(dir)
		assert.Error(t, err, body)
		assert.False(t, errors.Is(err, ErrNotLinked), "%s: a broken link is not 'no link'", body)

		assert.Error(t, Write(dir, DefaultTarget, "id"), body)
		raw, _ := os.ReadFile(Path(dir))
		assert.Equal(t, body, string(raw), "a file Write cannot read must be left as it was")
	}
}

func TestGitSaysWhetherTheLinkTravels(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH")
	}
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	outside := t.TempDir()
	require.NoError(t, Write(outside, DefaultTarget, "id"))
	assert.Equal(t, NotInGit, Git(outside))

	repo := t.TempDir()
	run(repo, "init", "-q")
	require.NoError(t, Write(repo, DefaultTarget, "id"))
	assert.Equal(t, Untracked, Git(repo))

	run(repo, "add", RelPath())
	assert.Equal(t, Tracked, Git(repo))

	ignored := t.TempDir()
	run(ignored, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(ignored, ".gitignore"), []byte(".aetherfy/\n"), 0o644))
	require.NoError(t, Write(ignored, DefaultTarget, "id"))
	assert.Equal(t, Ignored, Git(ignored))
}
