package changelog

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sample = `# Changelog

intro

## v0.4.2 (not tagged yet)

### Breaking

body of 0.4.2

` + "```" + `
## not a heading, it is code
` + "```" + `

## v0.4.1

old notes

## v0.4.2-rc1

rc notes
`

func TestTheSectionOfATagIsItsBodyAndNothingElse(t *testing.T) {
	got, err := Section(sample, "v0.4.2")
	require.NoError(t, err)
	assert.Contains(t, got, "### Breaking")
	assert.Contains(t, got, "body of 0.4.2")
	assert.Contains(t, got, "## not a heading, it is code", "a ## inside a code fence is code")
	assert.NotContains(t, got, "old notes", "the next section is not part of it")
	assert.NotContains(t, got, "rc notes")
	assert.False(t, strings.HasPrefix(got, "\n") || strings.HasPrefix(got, "## "), "no section heading line, no leading blank: %q", got)

	old, err := Section(sample, "v0.4.1")
	require.NoError(t, err)
	assert.Equal(t, "old notes\n", old)
}

func TestACarriageReturnedFileReadsTheSame(t *testing.T) {
	got, err := Section(strings.ReplaceAll(sample, "\n", "\r\n"), "v0.4.1")
	require.NoError(t, err)
	assert.Equal(t, "old notes\n", got)
}

func TestATagWithoutASectionFailsAndNamesTheSectionsThereAre(t *testing.T) {
	for _, tag := range []string{"v0.4.3", "v0.4", "0.4.2", "v0.4.2-rc", ""} {
		got, err := Section(sample, tag)
		require.Error(t, err, "tag %q", tag)
		assert.Empty(t, got, "a failure never returns a body")
	}
	_, err := Section(sample, "v0.9.0")
	assert.Contains(t, err.Error(), `add a "## v0.9.0" heading`)
	assert.Contains(t, err.Error(), "v0.4.2 (not tagged yet)")
}

func TestAPrereleaseTagIsNotTheReleasesSection(t *testing.T) {
	got, err := Section(sample, "v0.4.2-rc1")
	require.NoError(t, err)
	assert.Equal(t, "rc notes\n", got)
}

func TestAnEmptySectionFailsAsDoesADuplicate(t *testing.T) {
	_, err := Section("## v1.0.0\n\n   \n## v0.9.0\nx\n", "v1.0.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is empty")
	_, err = Section("## v1.0.0\na\n## v1.0.0\nb\n", "v1.0.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 sections")
}

// The repository's own CHANGELOG.md: its newest section parses, so a malformed
// edit (a lost "## " or an emptied section) is caught before a tag is pushed.
func TestTheRepositorysChangelogHasAUsableNewestSection(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	require.NoError(t, err)
	tag := regexp.MustCompile(`(?m)^## (v\d+\.\d+\.\d+)\b`).FindStringSubmatch(string(raw))
	require.NotNil(t, tag, "CHANGELOG.md has no \"## vX.Y.Z\" heading")
	got, err := Section(string(raw), tag[1])
	require.NoError(t, err)
	assert.Contains(t, got, "Breaking", "the newest section names its breaking changes")
}

// THE WIRING. The extraction is worth nothing if the workflow does not run it
// before goreleaser and hand its output over, so this reads the workflow: the notes
// step comes first and its failure fails the job (no `|| true`, no
// continue-on-error), and goreleaser is given the file.
func TestTheReleaseWorkflowPublishesTheChangelogSection(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	require.NoError(t, err)
	wf := strings.ReplaceAll(string(raw), "\r\n", "\n")

	notes := strings.Index(wf, "go run ./scripts/release-notes")
	goreleaser := strings.Index(wf, "goreleaser/goreleaser-action")
	require.True(t, notes > 0, "the workflow does not extract the release notes")
	require.True(t, goreleaser > 0)
	assert.Less(t, notes, goreleaser, "the notes are extracted BEFORE goreleaser publishes")

	step := wf[strings.LastIndex(wf[:notes], "- name:"):goreleaser]
	assert.Contains(t, step, `"${GITHUB_REF_NAME}"`, "it extracts the tag being released")
	assert.NotContains(t, step, "|| true")
	assert.NotContains(t, step, "continue-on-error")
	assert.Regexp(t, `--release-notes=[^\n]*release-notes\.md`, wf[goreleaser:], "goreleaser is given the extracted file")
}

// The command, end to end: stdout is the section, and a tag without one exits 1
// with nothing on stdout.
func TestTheCommandPrintsTheSectionOrFailsEmptyHanded(t *testing.T) {
	file := filepath.Join(t.TempDir(), "CHANGELOG.md")
	require.NoError(t, os.WriteFile(file, []byte(sample), 0o644))
	run := func(tag string) (string, string, error) {
		cmd := exec.Command("go", "run", "./scripts/release-notes", tag, file)
		cmd.Dir = filepath.Join("..", "..")
		var out, errOut strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errOut
		err := cmd.Run()
		return out.String(), errOut.String(), err
	}
	out, _, err := run("v0.4.1")
	require.NoError(t, err)
	assert.Equal(t, "old notes\n", out)

	out, stderr, err := run("v9.9.9")
	require.Error(t, err)
	assert.Empty(t, out, "a missing section prints nothing for the release body")
	assert.Contains(t, stderr, `no section for v9.9.9`)
}
