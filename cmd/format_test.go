package cmd

import (
	"strings"
	"testing"
)

// formatDegradedTag is the single source for the inline DEGRADED marker shown
// in `afy agents list` and `afy deployments` (REVIEW_FAQ §63). It must stay
// empty unless degraded (no spurious marker), and carry both the DEGRADED term
// and the N/M region readiness when degraded.
func TestFormatDegradedTag(t *testing.T) {
	if got := formatDegradedTag(false, 0, 0); got != "" {
		t.Errorf("not degraded → want empty, got %q", got)
	}
	if got := formatDegradedTag(false, 2, 3); got != "" {
		t.Errorf("not degraded (even with counts) → want empty, got %q", got)
	}

	got := formatDegradedTag(true, 2, 3)
	if !strings.Contains(got, "DEGRADED") {
		t.Errorf("degraded tag missing DEGRADED term: %q", got)
	}
	if !strings.Contains(got, "2/3") {
		t.Errorf("degraded tag missing N/M readiness: %q", got)
	}
}
