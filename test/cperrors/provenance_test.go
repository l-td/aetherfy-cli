package cperrors

// The generator's refusal of a control plane nobody else can reach (provenance.go), on real repositories:
// a bare origin and a checkout pushed to it, then each state the docs generators refuse or accept.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitT runs git for a test, isolated from the machine's own git config, and fails the test when git does.
func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{"-C", dir, "-c", "core.autocrlf=false", "-c", "commit.gpgsign=false",
		"-c", "user.name=provenance-test", "-c", "user.email=provenance-test@example.invalid"}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
}

// pushedCheckout is a checkout whose branch `main` is pushed to its origin, holding a.py and README.md.
func pushedCheckout(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH")
	}
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitT(t, filepath.Dir(origin), "init", "-q", "--bare", "-b", "main", origin)
	work := filepath.Join(t.TempDir(), "cp")
	gitT(t, filepath.Dir(work), "clone", "-q", origin, work)
	gitT(t, work, "symbolic-ref", "HEAD", "refs/heads/main")
	write(t, work, "a.py", "A = 1\n")
	write(t, work, "README.md", "readme\n")
	gitT(t, work, "add", "-A")
	gitT(t, work, "commit", "-qm", "one")
	gitT(t, work, "push", "-q", "-u", "origin", "main")
	return work
}

func TestAPushedCleanCheckoutIsShareable(t *testing.T) {
	assert.Equal(t, "", Unshareable(DescribeCheckout(pushedCheckout(t))))
}

func TestUncommittedPythonIsRefusedNamingTheFiles(t *testing.T) {
	work := pushedCheckout(t)
	write(t, work, "a.py", "A = 2\n")
	write(t, work, "b.py", "B = 1\n")
	gitT(t, work, "add", "b.py") // staged, not committed: dirty as well

	assert.Equal(t, "the control-plane checkout has uncommitted Python: a.py, b.py",
		Unshareable(DescribeCheckout(work)))
}

func TestOnlyTrackedPythonCountsAsDirty(t *testing.T) {
	work := pushedCheckout(t)
	write(t, work, "README.md", "changed\n") // not Python: the extraction cannot read it
	write(t, work, "scratch.py", "X = 1\n")  // untracked: not part of the extraction
	assert.Equal(t, "", Unshareable(DescribeCheckout(work)))
}

func TestACommitNotOnTheUpstreamIsRefused(t *testing.T) {
	work := pushedCheckout(t)
	write(t, work, "a.py", "A = 3\n")
	gitT(t, work, "commit", "-qam", "two")

	assert.Equal(t, "the control-plane checkout is 1 commit(s) ahead of its upstream, so the surface it describes is not pushed",
		Unshareable(DescribeCheckout(work)))
}

func TestABranchWithoutAnUpstreamIsRefused(t *testing.T) {
	work := pushedCheckout(t)
	gitT(t, work, "checkout", "-q", "-b", "side")

	assert.Equal(t, `branch "side" has no upstream, so its commits are on no shared branch`,
		Unshareable(DescribeCheckout(work)))
}

func TestADetachedHeadIsShareableOnlyWhereARemoteBranchPointsAtIt(t *testing.T) {
	work := pushedCheckout(t)
	gitT(t, work, "checkout", "-q", "--detach", "origin/main")
	assert.Equal(t, "", Unshareable(DescribeCheckout(work)), "a pushed commit, detached")

	write(t, work, "a.py", "A = 4\n")
	gitT(t, work, "commit", "-qam", "local only")
	assert.Equal(t, "the control-plane checkout is on a detached HEAD that no remote branch points at, so it is not pushed",
		Unshareable(DescribeCheckout(work)))
}

func TestAFolderThatIsNotACheckoutIsRefused(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on PATH")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output(); err == nil &&
		string(out) == "true\n" {
		t.Skip("the temporary folder is itself inside a git repository on this host")
	}
	assert.Equal(t, "aetherfy-control-plane is not a git checkout, so its provenance cannot be established",
		Unshareable(DescribeCheckout(dir)))
}
