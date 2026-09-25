package archive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/test/cperrors"
	"github.com/l-td/aetherfy-cli/test/cpyaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const base = "name: typo-agent\nruntime: python3.11\n"

func refusal(t *testing.T, yamlText string) string {
	t.Helper()
	err := CheckUnknownFields([]byte(yamlText))
	require.Error(t, err, "expected a refusal for:\n%s", yamlText)
	var uf *UnknownFieldsError
	require.True(t, errors.As(err, &uf), "not an UnknownFieldsError: %T %v", err, err)
	return err.Error()
}

// The sentences below are the control plane's, clause for clause (see
// tests/unit/shared/test_config_parser.py::TestUnknownFieldsRefused there).
func TestAMisspeltTopLevelFieldIsRefusedWithASuggestion(t *testing.T) {
	assert.Equal(t, "aetherfy.yaml: unknown field `schedul`, did you mean `schedule`?",
		refusal(t, base+"schedul: '0 * * * *'\n"))
}

func TestAMisspeltNestedFieldIsRefusedWithItsPath(t *testing.T) {
	assert.Equal(t, "aetherfy.yaml: unknown field `spawn.workres`, did you mean `spawn.workers`?",
		refusal(t, base+"spawn:\n  enabled: true\n  workres: [child]\n"))
}

func TestANestedUnknownIsNotSuggestedATopLevelField(t *testing.T) {
	assert.Equal(t, "aetherfy.yaml: unknown field `spawn.schedule`",
		refusal(t, base+"spawn:\n  enabled: true\n  schedule: x\n"))
}

func TestNoSuggestionWhenNothingIsClose(t *testing.T) {
	assert.Equal(t, "aetherfy.yaml: unknown field `zzzzzz`", refusal(t, base+"zzzzzz: 1\n"))
}

func TestEveryUnknownFieldIsReportedInFileOrder(t *testing.T) {
	assert.Equal(t,
		"aetherfy.yaml: unknown field `schedul`, did you mean `schedule`?; "+
			"unknown field `memry_mb`, did you mean `memory_mb`?",
		refusal(t, base+"schedul: x\nmemry_mb: 1\n"))
}

// Every field the server accepts is accepted here, nested ones included. The
// file names every KnownFields path; if a field is added to the struct and not
// here, the loop below says which.
func TestAFileWithEveryFieldIsAccepted(t *testing.T) {
	full := "name: full\nruntime: python3.11\ntype: service\ndescription: d\n" +
		"tier: starter\nworkspace: ws\nspawn:\n  enabled: true\n  workers: [a]\n" +
		"regions: [us-east-1]\nmemory_mb: 512\nidle_timeout_minutes: 10\n" +
		"keep_alive: false\nentrypoint: main.py\nschedule: '0 3 * * *'\n" +
		"github_dependencies: ['o/r@v1']\n"
	require.NoError(t, CheckUnknownFields([]byte(full)))
	for _, p := range KnownFields().Paths() {
		leaf := p[strings.LastIndex(p, ".")+1:]
		assert.Contains(t, full, leaf+":", "the every-field file does not declare %s", p)
	}
}

// Merge-patch is the server's: a field left out is not an error here.
func TestAnAbsentFieldIsNotAnError(t *testing.T) {
	require.NoError(t, CheckUnknownFields([]byte(base)))
}

func TestAnAliasedBlockIsCheckedAndAMergeKeyIsLeftToTheServer(t *testing.T) {
	// The anchor sits on a leaf field, which is not walked; the alias under
	// `spawn` is, and must be followed into the block it names.
	assert.Equal(t, "aetherfy.yaml: unknown field `spawn.workres`, did you mean `spawn.workers`?",
		refusal(t, base+"description: &s {enabled: true, workres: [a]}\nspawn: *s\n"))
	require.NoError(t, CheckUnknownFields([]byte(base+"spawn:\n  <<: {enabled: true}\n")))
}

func TestParseAetherfyConfigRefusesBeforeAnyUpload(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "aetherfy.yaml"), []byte(base+"schedul: '0 * * * *'\n"), 0o644))

	_, err := ParseAetherfyConfig(dir)
	require.Error(t, err)
	assert.Equal(t, "aetherfy.yaml: unknown field `schedul`, did you mean `schedule`?", err.Error())

	// CreateTarballWithValidation is what `afy deploy` builds the upload
	// with: it must not produce bytes for this file.
	data, err := CreateTarballWithValidation(dir)
	assert.Nil(t, data)
	var uf *UnknownFieldsError
	assert.True(t, errors.As(err, &uf), "tarball built for a refused file: %v", err)
}

// CloseMatch against answers produced by Python's own difflib, with the
// control plane's call: get_close_matches(word, fields, n=1, cutoff=0.6).
// Generated 2026-09-25 on CPython 3.12. The tie rows are the ones an
// approximation gets wrong: equal scores go to the larger string.
func TestCloseMatchAgreesWithPythonDifflib(t *testing.T) {
	top := []string{"name", "runtime", "type", "description", "tier", "workspace", "spawn", "regions",
		"memory_mb", "idle_timeout_minutes", "keep_alive", "entrypoint", "schedule", "github_dependencies"}
	spawn := []string{"enabled", "workers"}
	cases := []struct {
		cands []string
		word  string
		want  string
	}{
		{top, "schedul", "schedule"}, {top, "shedule", "schedule"}, {top, "scheduel", "schedule"},
		{top, "memry_mb", "memory_mb"}, {top, "memory", "memory_mb"}, {top, "mem", ""},
		{top, "memorymb", "memory_mb"}, {top, "idle_timeout", "idle_timeout_minutes"},
		{top, "idle_timeout_minute", "idle_timeout_minutes"}, {top, "timeout", ""},
		{top, "keepalive", "keep_alive"}, {top, "keep-alive", "keep_alive"}, {top, "alive", "keep_alive"},
		{top, "entrypiont", "entrypoint"}, {top, "entry", "entrypoint"}, {top, "entry_point", "entrypoint"},
		{top, "region", "regions"}, {top, "regoins", "regions"}, {top, "runtme", "runtime"},
		{top, "nme", "name"}, {top, "names", "name"}, {top, "typ", "type"}, {top, "types", "type"},
		{top, "descripton", "description"}, {top, "desc", ""}, {top, "teir", "tier"}, {top, "tiers", "tier"},
		{top, "workspce", "workspace"}, {top, "work_space", "workspace"}, {top, "spwan", "spawn"},
		{top, "spawns", "spawn"}, {top, "github_dependency", "github_dependencies"},
		{top, "dependencies", "github_dependencies"}, {top, "deps", ""}, {top, "zzzzzz", ""},
		{top, "x", ""}, {top, "", ""}, {top, "schedule_", "schedule"}, {top, "SCHEDULE", ""},
		{top, "Schedule", "schedule"}, {top, "rum", "runtime"}, {top, "ab", ""}, {top, "tipe", "type"},
		{top, "naem", "name"},
		{spawn, "workres", "workers"}, {spawn, "enable", "enabled"}, {spawn, "enabld", "enabled"},
		{spawn, "worker", "workers"}, {spawn, "schedule", ""}, {spawn, "workspace", "workers"},
		{spawn, "wrkers", "workers"}, {spawn, "enabeld", "enabled"}, {spawn, "x", ""},
		{[]string{"ac", "ad"}, "ab", ""},
		{[]string{"abd", "abe", "xbc"}, "abc", "xbc"},
		{[]string{"aab", "baa"}, "aaa", "baa"},
		{[]string{"hallo", "hello"}, "héllo", "hello"},
		{[]string{"ab", "ba"}, "ba", "ba"},
		{[]string{"bcda", "dabc"}, "abcd", "dabc"},
	}
	for _, c := range cases {
		got, _ := CloseMatch(c.word, c.cands, UnknownFieldSuggestionCutoff)
		assert.Equal(t, c.want, got, "CloseMatch(%q, %v)", c.word, c.cands)
	}
}

// THE PAIR. The CLI's field set must equal the control plane's model, nested
// models included, and every model it reaches must forbid extras -- otherwise
// one side accepts what the other refuses. Live against the sibling checkout
// like the other control-plane guards: skipped where there is none, FAILED
// where cperrors.RequireEnv says there must be (the e2e nightly).
func TestKnownFieldsEqualTheControlPlaneModel(t *testing.T) {
	src := controlPlaneSource(t)
	assert.Equal(t, cpyaml.Paths(src), KnownFields().Paths(),
		"archive.AetherfyConfig's yaml tags and the control plane's %s (%s) disagree. Add the field to the "+
			"struct (typed `any` if the CLI does not read it) -- and to docs-site's aetherfy-yaml schema.",
		cpyaml.RootModel, cpyaml.SourcePath)
	assert.Empty(t, cpyaml.NotForbidding(src),
		"these control-plane models accept unknown fields, which the CLI refuses: set "+
			"model_config = ConfigDict(extra=\"forbid\") on them")
}

func TestUnknownFieldWordingEqualsTheControlPlanes(t *testing.T) {
	src := controlPlaneSource(t)
	py := func(s string) string {
		return strings.NewReplacer("{path}", "%s", "{suggestion}", "%s").Replace(s)
	}
	assert.Equal(t, py(src.Message), UnknownFieldMessage, "the unknown-field sentence")
	assert.Equal(t, py(src.Suggestion), UnknownFieldSuggestion, "the did-you-mean clause")
	assert.Equal(t, src.SuggestionCutoff, UnknownFieldSuggestionCutoff, "the suggestion cutoff")
}

func controlPlaneSource(t *testing.T) *cpyaml.Source {
	t.Helper()
	cpRoot := cperrors.Root(filepath.Join("..", ".."))
	if !cperrors.RootExists(cpRoot) {
		cperrors.SkipUnlessRequired(t, "SKIPPED: no control-plane checkout at %s (set %s to point elsewhere)",
			cpRoot, cperrors.RootEnv)
	}
	src, err := cpyaml.Extract(cpRoot)
	require.NoError(t, err, "reading %s from %s", cpyaml.SourcePath, cpRoot)
	require.NoError(t, cpyaml.Validate(src))
	fmt.Printf("COMPARED aetherfy.yaml fields against %s (%d paths)\n", cpRoot, len(cpyaml.Paths(src)))
	return src
}
