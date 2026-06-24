package cmd

import "testing"

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
