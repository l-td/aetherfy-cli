package cmd

import (
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// The rename note is CONDITIONAL, and a conditional nobody tests can invert
// silently. It printed unconditionally before the flip, which is the bug these
// pin: an agent with no address was told its address had not changed.
//
// captureStdout (cmd/deploy_url_test.go) redirects BOTH writers this package
// uses — fmt to os.Stdout and fatih/color to its package-level color.Output,
// bound to the real stdout at init. Capturing one proves nothing about the
// other, which is how the first version of these helpers passed while printing
// nothing anyone could see.

func TestRenameNoteNamesTheAddressWhenThereIsOne(t *testing.T) {
	out := captureStdout(t, func() {
		printRenameNote(&api.Agent{Name: "reporter", URL: "https://reporter-k3m7x2.aetherfy.dev"})
	})

	if !strings.Contains(out, "https://reporter-k3m7x2.aetherfy.dev") {
		t.Errorf("the note did not name the address; a note about an address that does not show it\n"+
			"leaves the reader to go and look it up. got:\n%s", out)
	}
	if !strings.Contains(out, "does not move it") {
		t.Errorf("the note did not say the rename leaves the address alone. got:\n%s", out)
	}
}

func TestRenameNoteIsSilentForAnAgentWithNoAddress(t *testing.T) {
	// THE REGRESSION. An agent that has never deployed has no public_label and
	// therefore no URL. Reassuring someone that their URL is unchanged, when
	// they do not have one, invents a resource.
	out := captureStdout(t, func() {
		printRenameNote(&api.Agent{Name: "fresh", URL: ""})
	})

	if strings.TrimSpace(out) != "" {
		t.Errorf("the note printed for an agent with no address:\n%s", out)
	}
}

func TestRenameNoteSurvivesANilAgent(t *testing.T) {
	// UpdateAgent returns (*Agent, error); a caller that stops checking err
	// would reach here with nil. Cheap to survive, and a panic in a success
	// path reads as a failed rename that actually succeeded.
	out := captureStdout(t, func() { printRenameNote(nil) })

	if strings.TrimSpace(out) != "" {
		t.Errorf("expected silence for a nil agent, got:\n%s", out)
	}
}
