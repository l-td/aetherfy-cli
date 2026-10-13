package cmd

import (
	"testing"

	"github.com/l-td/aetherfy-cli/test/cperrors"
	"github.com/l-td/aetherfy-cli/test/cplogstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE PIN. `afy logs` tells a platform line from the customer's own by its
// stream alone (logTag), so the CLI's systemStream must be the control plane's
// SYSTEM_LOG_STREAM. Renamed there, a platform ERROR would print as a plain
// "[ERROR]" with every other test in this repo green.
//
// Live against the sibling checkout, as the other control-plane guards are:
// skipped where there is none, FAILED where cperrors.RequireEnv says there must
// be (the e2e nightly).
func TestTheSystemStreamIsTheControlPlanes(t *testing.T) {
	cpRoot := cperrors.Root("..")
	if !cperrors.RootExists(cpRoot) {
		cperrors.SkipUnlessRequired(t, "SKIPPED: no control-plane checkout at %s (set %s to point elsewhere)",
			cpRoot, cperrors.RootEnv)
	}
	cp, err := cplogstream.Extract(cpRoot)
	require.NoError(t, err, "reading SYSTEM_LOG_STREAM from %s", cpRoot)
	assert.Equal(t, cp, systemStream,
		"`afy logs` tags platform lines by the stream %q, and the control plane files them under %q "+
			"(%s in %s). Change cmd/logs.go systemStream.", systemStream, cp, cplogstream.SourcePath, cpRoot)
}
