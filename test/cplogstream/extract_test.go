package cplogstream

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeCP(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	full := filepath.Join(root, filepath.FromSlash(SourcePath))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	return root
}

func TestExtractReadsTheModuleLevelConstant(t *testing.T) {
	v, err := Extract(fakeCP(t, "# the platform's stream\r\nSYSTEM_LOG_STREAM = \"system\"\r\n"+
		"LOG_STREAMS = (\"stdout\", \"stderr\", SYSTEM_LOG_STREAM)\r\n"+
		"    SYSTEM_LOG_STREAM = \"indented, not module level\"\r\n"))
	require.NoError(t, err)
	assert.Equal(t, "system", v)
}

func TestExtractRefusesWhatCouldAgreeWithAnything(t *testing.T) {
	_, err := Extract(fakeCP(t, "LOG_STREAMS = (\"stdout\",)\n"))
	assert.Error(t, err, "no declaration")
	_, err = Extract(fakeCP(t, "SYSTEM_LOG_STREAM = \"\"\n"))
	assert.Error(t, err, "an empty value")
	_, err = Extract(fakeCP(t, "SYSTEM_LOG_STREAM = \"a\"\nSYSTEM_LOG_STREAM = \"b\"\n"))
	assert.Error(t, err, "two declarations")
}
