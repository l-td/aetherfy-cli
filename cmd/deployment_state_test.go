package cmd

import (
	"strings"
	"testing"
)

// formatDeploymentState backs the State column in `afy rollback <agent>`'s
// deployment-history table. Only the live-serving (active) row gets the
// "(current)" suffix — surface parity with the dashboard "→ CURRENT" marker.
func TestFormatDeploymentState(t *testing.T) {
	cases := []struct {
		state string
		want  string
	}{
		{"active", "active (current)"},
		{"superseded", "superseded"},
		{"failed", "failed"},
		{"rolled_back", "rolled_back"},
		{"queued", "queued"},
	}
	for _, tc := range cases {
		if got := formatDeploymentState(tc.state); got != tc.want {
			t.Errorf("formatDeploymentState(%q) = %q, want %q", tc.state, got, tc.want)
		}
	}
}

// formatDeployState is the deployments table's COLORIZER, and it is a different
// function from formatDeploymentState above — which only appends "(current)".
// It had no test at all, and that is exactly how it came to be missing
// "completed": CP migration 093 added that state and nothing here noticed, so
// it printed bare among decorated siblings for months. "archived" (migration
// 107) would have been the second silent omission.
//
// THE LIST BELOW IS THE GATE. It must name every DeploymentStateEnum value the
// control plane can send. A state absent from both the switch and this list is
// invisible to this test, so adding one to CP means adding it here — that is
// the cost of the enum living in another repository.
func TestFormatDeployStateDecoratesEveryKnownState(t *testing.T) {
	states := []string{
		"queued", "building", "deploying", "active",
		"failed", "rolled_back", "superseded", "completed", "archived",
	}
	for _, s := range states {
		got := formatDeployState(s)
		if got == s {
			t.Errorf("formatDeployState(%q) returned the bare state — it fell through to default", s)
		}
		if !strings.Contains(got, s) {
			t.Errorf("formatDeployState(%q) = %q, which no longer contains the state name", s, got)
		}
	}
}

// The default branch must still pass an unknown state through untouched rather
// than dropping it: a state this CLI has not learned yet should still be
// readable, not blank.
func TestFormatDeployStatePassesUnknownStatesThrough(t *testing.T) {
	if got := formatDeployState("some_future_state"); got != "some_future_state" {
		t.Errorf("unknown state should pass through verbatim, got %q", got)
	}
}
