package cpbudgets

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeCP(t *testing.T, path, body string) string {
	t.Helper()
	root := t.TempDir()
	full := filepath.Join(root, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	return root
}

func TestReadsTheModuleLevelInteger(t *testing.T) {
	root := fakeCP(t, HealthGate.Path, "# budget\r\n"+
		"DEPLOY_HEALTH_GATE_TIMEOUT_SECONDS = 300  # the customer's init\r\n"+
		"DEPLOY_HEALTH_GATE_POLL_SECONDS = 5.0\r\n")
	got, err := Read(root, HealthGate)
	require.NoError(t, err)
	assert.Equal(t, 300*time.Second, got)
}

// Negative controls: every shape that is not one plain module-level integer is
// an error, because a zero would make any CLI budget look long enough.
func TestRefusesWhatCouldAgreeWithAnything(t *testing.T) {
	for name, body := range map[string]string{
		"missing":   "OTHER = 300\n",
		"indented":  "    BUILD_TIMEOUT_SECONDS = 300\n",
		"twice":     "BUILD_TIMEOUT_SECONDS = 300\nBUILD_TIMEOUT_SECONDS = 600\n",
		"not a int": "BUILD_TIMEOUT_SECONDS = settings.build_timeout\n",
		"zero":      "BUILD_TIMEOUT_SECONDS = 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Read(fakeCP(t, Build.Path, body), Build)
			assert.Error(t, err)
		})
	}
}
