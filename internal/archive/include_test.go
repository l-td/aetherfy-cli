package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The include rules and sentences are the control plane's (shared/config_parser.include_problem,
// shared/code_tree); `afy deploy` refuses before it uploads with the same words.

func TestAnIncludeLeavingTheAgentsFolderIsAccepted(t *testing.T) {
	for _, e := range []string{"../shared", "../shared/", "../../shared", "../../libs/client", "../../tsconfig.base.json", "../a_b-c.d"} {
		assert.Empty(t, IncludeProblem(e), e)
	}
}

func TestTheGrammarRefusesWithTheControlPlanesSentence(t *testing.T) {
	inside := "It names a path inside the agent's own folder, which is always deployed: an include starts " +
		"with '../' and names a path outside it."
	contains := "It names a folder that contains the agent's own folder: include only the folders the agent " +
		"shares, not one above it."
	names := "After the leading '../' steps, use folder and file names made of letters, digits, '.', '_' " +
		"and '-', separated by '/'."
	cases := map[string]string{
		"shared":                         inside,
		"./shared":                       inside,
		"/srv/shared":                    "An include is relative to the agent's folder, not an absolute path.",
		"C:/shared":                      "An include is relative to the agent's folder, not an absolute path.",
		`..\shared`:                      "Use '/' as the path separator.",
		"..":                             contains,
		"../..":                          contains,
		"../shared/../other":             names,
		"../shared/./x":                  names,
		"../sha red":                     names,
		"../" + strings.Repeat("a", 600): "An include is at most 512 characters.",
	}
	for entry, want := range cases {
		assert.Equal(t, want, IncludeProblem(entry), entry)
	}
}

// A name a deploy always leaves out is refused, naming it: the built-in exclusions are matched below a packed
// tree, never on its own name, so `../../.git` would otherwise upload the repository's history.
func TestAPathADeployAlwaysLeavesOutIsRefusedNamingIt(t *testing.T) {
	excluded := "It names '%s', which a deploy always leaves out (like .git, .env and node_modules): include " +
		"only paths a deploy carries."
	cases := map[string]string{
		"../../.git": ".git", "../../.git/config": ".git", "../../.env": ".env",
		"../../.env.production": ".env.production", "../../shared/.env": ".env", "../../node_modules": "node_modules",
		"../../.venv/lib": ".venv", "../../logs/build.log": "build.log", "../../shared/__pycache__": "__pycache__",
		"../.afyignore": ".afyignore",
	}
	for entry, name := range cases {
		assert.Equal(t, fmt.Sprintf(excluded, name), IncludeProblem(entry), entry)
	}
	// CONTROL: names that only resemble an exclusion are accepted.
	for _, entry := range []string{"../../environment", "../../.envrc", "../../git-tools", "../../logs",
		"../../shared/env_config.py", "../../node_modules_shared"} {
		assert.Empty(t, IncludeProblem(entry), entry)
	}
}

func refusalOf(t *testing.T, value any) string {
	t.Helper()
	_, err := Includes(value)
	require.Error(t, err)
	var inc *IncludeError
	require.True(t, errors.As(err, &inc))
	return inc.Message
}

func TestTheListIsRefusedAsTheControlPlaneRefusesIt(t *testing.T) {
	p := "Config validation failed: include: "
	assert.Equal(t, p+"include is a list of paths, one per line, each starting with '../'.", refusalOf(t, "../shared"))
	assert.Equal(t, p+"Invalid include entry '7'. Each include is a path, written as a string.", refusalOf(t, []any{7}))
	assert.Equal(t, p+"Invalid include entry 'shared'. It names a path inside the agent's own folder, which is "+
		"always deployed: an include starts with '../' and names a path outside it.", refusalOf(t, []any{"shared"}))
	assert.Equal(t, p+"include lists '../shared' twice.", refusalOf(t, []any{"../shared", "../shared/"}))
	assert.Equal(t, p+"include lists '../a/b', which is inside '../a', also listed: list only '../a'.",
		refusalOf(t, []any{"../a", "../a/b"}))
	many := make([]any, IncludeMax+1)
	for i := range many {
		many[i] = "../p" + strings.Repeat("x", i+1)
	}
	assert.Equal(t, p+"include lists 21 paths; the most it takes is 20.", refusalOf(t, many))
	entries, err := Includes(many[:IncludeMax])
	require.NoError(t, err)
	assert.Len(t, entries, IncludeMax)
	none, err := Includes(nil)
	require.NoError(t, err)
	assert.Empty(t, none)
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var inc *IncludeError
	require.True(t, errors.As(err, &inc), "%v", err)
	return inc.Code
}

func TestAnIncludeResolvesFromTheAgentsFolder(t *testing.T) {
	r, err := ResolveIncludes("agents/bot", []string{"../../shared", "../common/", "../../tsconfig.base.json"})
	require.NoError(t, err)
	assert.Equal(t, []ResolvedInclude{{"../../shared", "shared"}, {"../common/", "agents/common"},
		{"../../tsconfig.base.json", "tsconfig.base.json"}}, r)
}

func TestAnIncludeAboveTheRepositoryIsRefusedAsOutside(t *testing.T) {
	_, err := ResolveIncludes("agents/bot", []string{"../../../outside"})
	assert.Equal(t, CodeIncludeOutsideRepository, codeOf(t, err))
	_, err = ResolveIncludes("", []string{"../shared"})
	assert.Equal(t, CodeIncludeOutsideRepository, codeOf(t, err))
	assert.Equal(t, "include '../shared' leaves the repository: from the repository root it reaches above the "+
		"repository root. Include only paths inside the repository.", err.Error())
}

func TestAnIncludeOnTheAgentsOwnFolderIsRefusedAsOverlapping(t *testing.T) {
	for _, e := range []string{"../bot", "../bot/lib", "../../agents"} {
		_, err := ResolveIncludes("agents/bot", []string{e})
		assert.Equal(t, CodeIncludeOverlapsFolder, codeOf(t, err), e)
	}
}

func TestTwoSpellingsOfOnePathAreOnePathListedTwice(t *testing.T) {
	_, err := ResolveIncludes("agents/bot", []string{"../shared", "../../agents/shared"})
	assert.Equal(t, CodeConfigParseError, codeOf(t, err))
	assert.Equal(t, "include lists 'agents/shared' twice.", err.Error())
}

// Nested only once resolved: from agents/bot, `../x` is agents/x and `../../agents/x/lib` is inside it. The text
// alone does not show it (the grammar takes both), so resolution refuses it, never keeping one of the two, with
// the grammar's sentence, word for word the control plane's (shared/code_tree.resolve_includes).
func TestAPathInsideAnotherOnceResolvedIsRefusedWithTheGrammarsSentence(t *testing.T) {
	entries := []string{"../x", "../../agents/x/lib"}
	for _, e := range entries {
		assert.Equal(t, "", IncludeProblem(e), e)
	}
	_, err := ResolveIncludes("agents/bot", entries)
	assert.Equal(t, CodeConfigParseError, codeOf(t, err))
	assert.Equal(t, "include lists 'agents/x/lib', which is inside 'agents/x', also listed: list only 'agents/x'.",
		err.Error())
}

// --- the repository and its files -------------------------------------------------------------

func write(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func repository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	write(t, filepath.Join(root, "agents", "bot", "aetherfy.yaml"), "runtime: python3.12\ninclude:\n  - ../../shared\n")
	write(t, filepath.Join(root, "agents", "bot", "main.py"), "from shared.util import X\n")
	write(t, filepath.Join(root, "shared", "util.py"), "X = 1\n")
	write(t, filepath.Join(root, "agents", "other", "secret.py"), "NOT = 'shipped'\n")
	return root
}

func symlinkOrSkip(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
}

func TestTheRepositoryRootIsTheNearestFolderHoldingGit(t *testing.T) {
	root := repository(t)
	got, err := RepositoryRoot(filepath.Join(root, "agents", "bot"))
	require.NoError(t, err)
	want, _ := filepath.Abs(root)
	assert.Equal(t, want, got)

	worktree := t.TempDir()
	write(t, filepath.Join(worktree, ".git"), "gitdir: /elsewhere\n") // a worktree's .git is a file
	write(t, filepath.Join(worktree, "a", "aetherfy.yaml"), "runtime: python3.12\n")
	got, err = RepositoryRoot(filepath.Join(worktree, "a"))
	require.NoError(t, err)
	want, _ = filepath.Abs(worktree)
	assert.Equal(t, want, got)
}

func TestOutsideAGitRepositoryThereIsNoRootToIncludeFrom(t *testing.T) {
	plain := t.TempDir()
	_, err := RepositoryRoot(plain)
	if err == nil {
		t.Skip("the temporary folder is itself inside a git repository on this host")
	}
	assert.ErrorIs(t, err, ErrNotInRepository)
}

func TestAMissingIncludeIsRefusedAsNotFound(t *testing.T) {
	root := repository(t)
	resolved, err := ResolveIncludes("agents/bot", []string{"../../libs"})
	require.NoError(t, err)
	err = CheckIncludesOnDisk(root, "agents/bot", resolved)
	assert.Equal(t, CodeIncludeNotFound, codeOf(t, err))
	assert.Equal(t, "include '../../libs' names 'libs', which is not in this repository.", err.Error())
}

func TestAnIncludeThatIsALinkOutOfTheRepositoryIsRefused(t *testing.T) {
	root := repository(t)
	elsewhere := t.TempDir()
	symlinkOrSkip(t, elsewhere, filepath.Join(root, "libs"))
	resolved, _ := ResolveIncludes("agents/bot", []string{"../../libs"})
	assert.Equal(t, CodeIncludeOutsideRepository, codeOf(t, CheckIncludesOnDisk(root, "agents/bot", resolved)))
}

func TestAnIncludeThatIsALinkToTheAgentsOwnFolderIsRefusedAsOverlapping(t *testing.T) {
	root := repository(t)
	symlinkOrSkip(t, filepath.Join(root, "agents", "bot"), filepath.Join(root, "libs"))
	resolved, err := ResolveIncludes("agents/bot", []string{"../../libs"})
	require.NoError(t, err) // by its spelling, 'libs' is another folder: only the disk shows where it leads
	err = CheckIncludesOnDisk(root, "agents/bot", resolved)
	assert.Equal(t, CodeIncludeOverlapsFolder, codeOf(t, err))
	assert.Equal(t, "include '../../libs' leads through a link to this agent's own folder ('agents/bot'), which is "+
		"always deployed: include only the folders it shares.", err.Error())
}

// --- the archives -------------------------------------------------------------------------------

func members(t *testing.T, data []byte) map[string]*tar.Header {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	out := map[string]*tar.Header{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
		out[h.Name] = h
	}
}

func names(m map[string]*tar.Header) []string {
	var out []string
	for n := range m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func TestTheRepositoryArchiveHoldsTheFolderAndItsIncludesAtTheirPaths(t *testing.T) {
	root := repository(t)
	data, err := CreateRepositoryTarball(root, "agents/bot", []string{"shared"}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"agents/bot", "agents/bot/aetherfy.yaml", "agents/bot/main.py", "shared", "shared/util.py"},
		names(members(t, data)))
}

func TestTheAgentsAfyignoreAppliesBelowEachIncludeNeverToItsOwnName(t *testing.T) {
	root := repository(t)
	write(t, filepath.Join(root, "shared", "data.csv"), "a,b\n")
	write(t, filepath.Join(root, "shared", "node_modules", "x.js"), "")
	// ".*" matches "." itself, the name a walk gives the packed tree's own root: it must not take the tree.
	data, err := CreateRepositoryTarball(root, "agents/bot", []string{"shared"}, []string{"shared", "*.csv", ".*"})
	require.NoError(t, err)
	got := names(members(t, data))
	assert.Contains(t, got, "agents/bot/main.py")
	assert.Contains(t, got, "shared/util.py")
	assert.NotContains(t, got, "shared/data.csv")
	assert.NotContains(t, got, "shared/node_modules")
}

func TestARelativeLinkInsideTheFolderIsPackedAsALink(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "real.py"), "print(1)\n")
	symlinkOrSkip(t, "real.py", filepath.Join(dir, "alias.py"))
	data, err := CreateTarball(dir, nil)
	require.NoError(t, err, "a symlink used to fail the archive with `write too long`")
	alias := members(t, data)["alias.py"]
	require.NotNil(t, alias)
	assert.Equal(t, byte(tar.TypeSymlink), alias.Typeflag)
	assert.Equal(t, "real.py", alias.Linkname)
}

func TestALinkLeavingTheFolderIsRefusedNamingInclude(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "bot")
	write(t, filepath.Join(dir, "main.py"), "")
	write(t, filepath.Join(parent, "secret.txt"), "do not ship me\n")
	symlinkOrSkip(t, filepath.Join("..", "secret.txt"), filepath.Join(dir, "leak.txt"))
	_, err := CreateTarball(dir, nil)
	var linkErr *LinkError
	require.True(t, errors.As(err, &linkErr), "%v", err)
	assert.Contains(t, err.Error(), "The link 'leak.txt' points to '../secret.txt', outside the code")
	assert.Contains(t, err.Error(), "`include:`")
}

func TestAnAbsoluteLinkIsRefusedEvenInsideTheFolder(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "real.py"), "")
	symlinkOrSkip(t, filepath.Join(dir, "real.py"), filepath.Join(dir, "alias.py"))
	_, err := CreateTarball(dir, nil)
	var linkErr *LinkError
	assert.True(t, errors.As(err, &linkErr), "%v", err)
}

func TestALinkFromTheFolderIntoAnIncludeIsKept(t *testing.T) {
	root := repository(t)
	symlinkOrSkip(t, filepath.Join("..", "..", "shared", "util.py"), filepath.Join(root, "agents", "bot", "util.py"))
	data, err := CreateRepositoryTarball(root, "agents/bot", []string{"shared"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "../../shared/util.py", members(t, data)["agents/bot/util.py"].Linkname)
	_, err = CreateTarball(filepath.Join(root, "agents", "bot"), nil)
	var linkErr *LinkError
	assert.True(t, errors.As(err, &linkErr), "without the include the same link leaves the packed code")
}
