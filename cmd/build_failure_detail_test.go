package cmd

import (
	"strings"
	"testing"
)

// The server sends the build output only for a deployment that failed in the
// BUILD stage. Everything else -- a region that never came up, a cancellation,
// a success -- carries none, and a heading printed over nothing would claim the
// build printed nothing, which is a different and untrue statement.
func TestBuildFailureDetailPrintsNothingWhenThereIsNone(t *testing.T) {
	for _, empty := range []string{"", "   ", "\n\n", "\t\n "} {
		out := captureStdout(t, func() { printBuildFailureDetail(empty) })
		if strings.TrimSpace(out) != "" {
			t.Errorf("printed a section for %q:\n%s", empty, out)
		}
	}
}

func TestBuildFailureDetailPrintsEveryLineOfWhatTheBuildSaid(t *testing.T) {
	detail := "ERROR: failed to solve: process \"/bin/sh -c uv pip install -r requirements.txt\"\n" +
		"  x No solution found when resolving dependencies\n" +
		"  help: pin a version that exists"

	out := captureStdout(t, func() { printBuildFailureDetail(detail) })

	if !strings.Contains(out, "Build output:") {
		t.Errorf("no heading, so the quoted text is not attributed:\n%s", out)
	}
	// EVERY line, not just the first. A detail cut short at the newline would
	// drop the resolver's actual complaint and keep only the shell invocation,
	// which is the half that says nothing.
	for _, line := range strings.Split(detail, "\n") {
		if !strings.Contains(out, strings.TrimSpace(line)) {
			t.Errorf("dropped a line of the build output: %q\n%s", line, out)
		}
	}
}

// THE NEGATIVE CONTROL on the test above. Both tests would pass against a
// function that printed its argument and nothing else, and the indentation is
// what separates the server's words from the CLI's on a terminal.
func TestBuildFailureDetailIndentsWhatTheServerSaid(t *testing.T) {
	out := captureStdout(t, func() { printBuildFailureDetail("a build error") })

	if !strings.Contains(out, "  a build error") {
		t.Errorf("the quoted output is not indented, so it reads as the CLI's own:\n%s", out)
	}
}
