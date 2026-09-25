package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/test/cperrors"
	"github.com/l-td/aetherfy-cli/test/cpbudgets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A deployment REPLACED before it went live -- a newer one of the same agent
// passed its health gate first, or a rollback replaced it -- ends in a state
// that never changes again. The watch used to treat it as still in progress and
// poll it until the whole wait ran out, then report a timeout.
func TestAReplacedDeploymentEndsTheWatchAtOnceAndNonZero(t *testing.T) {
	for _, state := range []string{"superseded", "rolled_back"} {
		t.Run(state, func(t *testing.T) {
			client := deploymentServer(t, state, "", false)

			var code int
			started := time.Now()
			stderr := captureStderr(t, func() {
				code = rollbackAgent(client, []string{"ranker", "2"}, false, fastPoll, 30*time.Second)
			})

			if code == 0 {
				t.Fatalf("a deployment that never went live exited 0; a script would read "+
					"its version as serving\nstderr:\n%s", stderr)
			}
			if elapsed := time.Since(started); elapsed > 5*time.Second {
				t.Errorf("the watch kept polling a terminal state for %s", elapsed)
			}
			if strings.Contains(stderr, "timed out") {
				t.Errorf("a replaced deployment was reported as a timeout:\n%s", stderr)
			}
			if !strings.Contains(stderr, "replaced before it went live") {
				t.Errorf("the reason is not on stderr:\n%s", stderr)
			}
		})
	}
}

// THE BUDGET COVERS WHAT IT WAITS ON. The control plane holds a release in
// `deploying` through its health gate, after its build; each budget the watch
// is built from must be the control plane's own number, and the watch must be
// at least their sum. Live against the sibling checkout: skipped where there is
// none, FAILED where cperrors.RequireEnv says there must be (the e2e nightly).
func TestTheDeployWatchCoversTheControlPlanesStageBudgets(t *testing.T) {
	cpRoot := cperrors.Root("..")
	if !cperrors.RootExists(cpRoot) {
		cperrors.SkipUnlessRequired(t, "SKIPPED: no control-plane checkout at %s (set %s to point elsewhere)",
			cpRoot, cperrors.RootEnv)
	}
	build, err := cpbudgets.Read(cpRoot, cpbudgets.Build)
	require.NoError(t, err)
	gate, err := cpbudgets.Read(cpRoot, cpbudgets.HealthGate)
	require.NoError(t, err)

	assert.Equal(t, build, cpBuildTimeout,
		"cpBuildTimeout mirrors %s in %s, which is %s", cpbudgets.Build.Name, cpbudgets.Build.Path, build)
	assert.Equal(t, gate, cpHealthGateTimeout,
		"cpHealthGateTimeout mirrors %s in %s, which is %s", cpbudgets.HealthGate.Name, cpbudgets.HealthGate.Path, gate)
	assert.GreaterOrEqual(t, deploymentWatchTimeout, build+gate,
		"the deploy watch (%s) is shorter than the build (%s) plus the health gate (%s) it waits on",
		deploymentWatchTimeout, build, gate)
}
