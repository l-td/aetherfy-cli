package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
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

// The new "archived" lifecycle state must render cleanly (the status string is
// preserved verbatim, only wrapped in color codes) so the agents-list Status
// column shows it. Colors are disabled here so we can assert the plain text.
func TestFormatStatusArchived(t *testing.T) {
	// TestMain-free package: disable color so Sprint returns the raw string.
	// (colors.go honors NO_COLOR; set it for the assertion.)
	t.Setenv("NO_COLOR", "1")

	for _, s := range []string{"archived", "ARCHIVED"} {
		if got := formatStatus(s); got != s {
			t.Errorf("formatStatus(%q) = %q, want the status string preserved", s, got)
		}
	}
}

// Command wiring: archive/restore are registered under `agents`, take exactly
// one arg, and do not collide with stop/start (which stay pause/resume).
func TestArchiveRestoreCommandsWired(t *testing.T) {
	var archive, restore, stop, start *cobra.Command
	for _, c := range agentsCmd.Commands() {
		switch c.Name() {
		case "archive":
			archive = c
		case "restore":
			restore = c
		case "stop":
			stop = c
		case "start":
			start = c
		}
	}
	if archive == nil {
		t.Fatal("`agents archive` not registered")
	}
	if restore == nil {
		t.Fatal("`agents restore` not registered")
	}
	if archive.RunE == nil || restore.RunE == nil {
		t.Error("archive/restore must have a RunE")
	}
	// Both take exactly one <name> arg.
	if err := archive.Args(archive, []string{"foo"}); err != nil {
		t.Errorf("archive should accept one arg: %v", err)
	}
	if err := archive.Args(archive, []string{}); err == nil {
		t.Error("archive should reject zero args")
	}
	if err := restore.Args(restore, []string{"foo"}); err != nil {
		t.Errorf("restore should accept one arg: %v", err)
	}
	// stop/start remain the pause/resume commands — archive/restore are separate.
	if stop == nil || start == nil {
		t.Fatal("stop/start must still exist alongside archive/restore")
	}
	if strings.Contains(strings.ToLower(stop.Short), "archive") {
		t.Error("`stop` short help must not mention archive (stays pause)")
	}
	if strings.Contains(strings.ToLower(start.Short), "archive") {
		t.Error("`start` short help must not mention archive (stays resume)")
	}
}
