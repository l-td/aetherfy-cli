package cpresume

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

// The real shape: CRLF, a comment on every entry, and other tuples around it.
const realShape = "IN_FLIGHT = (JobStatusEnum.PENDING,)\r\n" +
	"RESUME_DROP_REASONS = (\r\n" +
	"    \"paused_again\",   # the customer paused the agent again\r\n" +
	"    \"archived\",       # the agent was archived\r\n" +
	"    \"stop_failed\",    # its stop never completed\r\n" +
	")\r\n" +
	"_OTHER = (\r\n    \"not_a_reason\",\r\n)\r\n"

func TestExtractReadsTheTupleAndNothingAroundIt(t *testing.T) {
	reasons, err := Extract(fakeCP(t, realShape))
	require.NoError(t, err)
	require.NoError(t, Validate(reasons))
	assert.Equal(t, []string{"archived", "paused_again", "stop_failed"}, Set(reasons))
}

func TestAnEntryTheReaderCannotVouchForIsAnError(t *testing.T) {
	_, err := Extract(fakeCP(t, "RESUME_DROP_REASONS = (\n    SOME_CONSTANT,\n)\n"))
	require.Error(t, err)
}

func TestNoTupleIsAnErrorNotAnEmptyAgreement(t *testing.T) {
	_, err := Extract(fakeCP(t, "RESUME_DROP_REASONS = frozenset()\n"))
	require.Error(t, err)
	assert.Error(t, Validate(nil))
	assert.Error(t, Validate([]string{"a", "a"}))
}
