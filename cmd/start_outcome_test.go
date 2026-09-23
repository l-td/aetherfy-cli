package cmd

import (
	"strings"
	"testing"
)

// `afy start` may say an agent is serving ONLY when the control plane's
// readiness says so. Every other answer -- including no answer at all, from an
// older control plane or for a task agent -- must not read as serving.
func startOutcome(t *testing.T, readiness *string) string {
	t.Helper()
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			printStartOutcome("api", readiness)
		})
	})
	return strings.ToLower(stdout + stderr)
}

func ptr(s string) *string { return &s }

func TestStartSaysServingOnlyWhenTheAgentSaidSo(t *testing.T) {
	if out := startOutcome(t, ptr("serving")); !strings.Contains(out, "serving requests") {
		t.Errorf("a serving agent was not reported as serving:\n%s", out)
	}
}

func TestStartClaimsNothingItDidNotObserve(t *testing.T) {
	cases := map[string]*string{
		"absent":      nil,
		"starting":    ptr("starting"),
		"load_failed": ptr("load_failed"),
		"unconfirmed": ptr("unconfirmed"),
		"unknown":     ptr("some-future-value"),
	}
	for name, readiness := range cases {
		t.Run(name, func(t *testing.T) {
			out := startOutcome(t, readiness)
			for _, claim := range []string{"serving requests", "machines are running", "is running"} {
				if strings.Contains(out, claim) {
					t.Errorf("readiness %s printed %q:\n%s", name, claim, out)
				}
			}
			if !strings.Contains(out, "resumed") {
				t.Errorf("readiness %s did not say the agent resumed:\n%s", name, out)
			}
		})
	}
}

// Each non-serving value names what it saw, so a reader can tell them apart.
func TestStartNamesWhatItSaw(t *testing.T) {
	want := map[string]string{
		"starting":    "still starting",
		"load_failed": "failed to load",
		"unconfirmed": "did not confirm",
	}
	for readiness, phrase := range want {
		if out := startOutcome(t, ptr(readiness)); !strings.Contains(out, phrase) {
			t.Errorf("readiness %s did not say %q:\n%s", readiness, phrase, out)
		}
	}
}
