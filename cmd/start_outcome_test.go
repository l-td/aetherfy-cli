package cmd

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/test/cperrors"
	"github.com/l-td/aetherfy-cli/test/cpreadiness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE PIN. The keys of startReadinessMessages must be exactly the control
// plane's READINESS_* values. A value renamed there, or a new one added, would
// otherwise print the catch-all "does not describe" line forever with every
// other test in this file green -- they feed the CLI values it already knows.
//
// Live against the sibling checkout, as the other control-plane guards are:
// skipped where there is none, FAILED where cperrors.RequireEnv says there must
// be (the e2e nightly).
func TestStartDescribesExactlyTheControlPlanesReadinessValues(t *testing.T) {
	cpRoot := cperrors.Root("..")
	if !cperrors.RootExists(cpRoot) {
		cperrors.SkipUnlessRequired(t, "SKIPPED: no control-plane checkout at %s (set %s to point elsewhere)",
			cpRoot, cperrors.RootEnv)
	}
	vals, err := cpreadiness.Extract(cpRoot)
	require.NoError(t, err, "reading the readiness constants from %s", cpRoot)
	require.NoError(t, cpreadiness.Validate(vals))

	var known []string
	for k := range startReadinessMessages {
		known = append(known, k)
	}
	sort.Strings(known)
	assert.Equal(t, cpreadiness.Set(vals), known,
		"`afy start` describes %v, and the control plane answers %v (%s in %s). Add or rename the "+
			"entry in startReadinessMessages -- and the table in docs-site's agents/api-lifecycle.mdx.",
		known, cpreadiness.Set(vals), cpreadiness.SourcePath, cpRoot)
}

// A value this binary does not know is a gap in the CLI, not news about the
// agent: it must not borrow "did not confirm", which is a claim.
func TestAnUnknownReadinessIsNotReportedAsAClaimAboutTheAgent(t *testing.T) {
	out := startOutcome(t, ptr("some-future-value"))
	if !strings.Contains(out, "does not describe") || !strings.Contains(out, "some-future-value") {
		t.Errorf("an unknown readiness did not say the CLI lacks a description for it:\n%s", out)
	}
	if strings.Contains(out, "did not confirm") {
		t.Errorf("an unknown readiness was reported as the agent not confirming:\n%s", out)
	}
}

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

// THE WHOLE PATH, not just the printer: the control plane's 202 body is
// decoded and ITS readiness decides the message. Without this, a command that
// ignored the server and printed "serving" regardless would keep every test
// above green -- they call the printer directly.
func startAgainst(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/agents/api/start") {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	var err error
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			err = startAgent(client, "api")
		})
	})
	if err != nil {
		t.Fatalf("a 202 resume returned an error: %v", err)
	}
	return strings.ToLower(stdout + stderr)
}

func TestStartReportsTheServersReadiness(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"serving":     {`{"status":"running","agent_id":"a","readiness":"serving"}`, "serving requests"},
		"starting":    {`{"status":"running","agent_id":"a","readiness":"starting"}`, "still starting"},
		"load_failed": {`{"status":"running","agent_id":"a","readiness":"load_failed"}`, "failed to load"},
		"unconfirmed": {`{"status":"running","agent_id":"a","readiness":"unconfirmed"}`, "did not confirm"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if out := startAgainst(t, tc.body); !strings.Contains(out, tc.want) {
				t.Errorf("readiness %s: want %q in:\n%s", name, tc.want, out)
			}
		})
	}
}

// An older control plane sends no readiness, and a job agent sends null. Both
// must decode to "claims nothing", never to serving.
func TestStartWithoutReadinessClaimsNothing(t *testing.T) {
	for name, body := range map[string]string{
		"absent": `{"status":"running","agent_id":"a"}`,
		"null":   `{"status":"running","agent_id":"a","readiness":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			out := startAgainst(t, body)
			if strings.Contains(out, "serving requests") || !strings.Contains(out, "resumed") {
				t.Errorf("readiness %s printed:\n%s", name, out)
			}
		})
	}
}
