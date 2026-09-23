package cpreadiness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCP writes a fly_manager.py with the shapes the real one has: plain
// literals, an alias to a private string constant, a trailing comment, and the
// private rank table that must NOT be read as a value.
func fakeCP(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	full := filepath.Join(root, filepath.FromSlash(SourcePath))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	return root
}

const realShape = "_SUPERVISOR_STARTING_STATUS = \"starting\"\r\n" +
	"READINESS_SERVING = \"serving\"            # every machine answered 200\r\n" +
	"READINESS_STARTING = _SUPERVISOR_STARTING_STATUS\r\n" +
	"READINESS_UNCONFIRMED = \"unconfirmed\"\r\n" +
	"READINESS_LOAD_FAILED = \"load_failed\"\r\n" +
	"_READINESS_RANK = {\r\n    READINESS_SERVING: 0,\r\n}\r\n" +
	"    READINESS_INDENTED = \"not_module_level\"\r\n"

func TestExtractReadsLiteralsAndResolvesTheAlias(t *testing.T) {
	vals, err := Extract(fakeCP(t, realShape))
	require.NoError(t, err)
	require.NoError(t, Validate(vals))
	assert.Equal(t, []string{"load_failed", "serving", "starting", "unconfirmed"}, Set(vals),
		"the alias must resolve to the supervisor's word, and neither the rank table nor an "+
			"indented assignment may be read as a value")
}

func TestAnAliasToNothingIsAnErrorNotADroppedValue(t *testing.T) {
	_, err := Extract(fakeCP(t, "READINESS_SERVING = \"serving\"\nREADINESS_STARTING = _GONE\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "READINESS_STARTING")
}

func TestValidateRefusesWhatCouldAgreeWithAnything(t *testing.T) {
	assert.Error(t, Validate(nil), "an empty extraction agrees with everything")
	assert.Error(t, Validate([]Value{{"READINESS_A", "x"}, {"READINESS_B", "x"}}), "two names, one value")
	assert.Error(t, Validate([]Value{{"READINESS_A", ""}, {"READINESS_B", "y"}}), "an empty value")
}
