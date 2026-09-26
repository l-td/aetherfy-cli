package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/test/cperrors"
	"github.com/l-td/aetherfy-cli/test/cpresume"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `afy start` WHILE THE PREVIOUS STOP IS STILL FINISHING.
//
// The control plane ACCEPTS that start (202, `resume` pending): it keeps the
// request and starts the agent itself once the stop completes. So the CLI
// sends /start ONCE and says so; with --wait it follows the agent's STATUS
// until it runs -- it never asks for the start again. The server here is a
// stand-in answering the control plane's bodies field for field
// (api/routes/agents.py start_agent, AgentResponse.resume); the waits
// between reads are recorded, not spent.

// The control plane's 202 for a start accepted during a stop.
const acceptedDuringAStop = `{"status":"paused","agent_id":"a","readiness":null,` +
	`"resume":{"state":"pending","reason":null,"requested_at":"2026-09-26T10:00:00Z"},` +
	`"stop_bound_seconds":155,"wait_bound_seconds":4100}`

type startServer struct {
	mu     sync.Mutex
	starts int
	reads  int
}

// newStartServer answers POST /agents/api/start with startBody, and the
// n-th GET /agents/api with reads[n] (the last one repeating).
func newStartServer(t *testing.T, startBody string, reads []string) (*httptest.Server, *startServer) {
	t.Helper()
	s := &startServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/agents/api/start"):
			s.starts++
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(startBody))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/agents/api") && len(reads) > 0:
			n := s.reads
			if n >= len(reads) {
				n = len(reads) - 1
			}
			s.reads++
			_, _ = w.Write([]byte(reads[n]))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

// agentRead is GET /agents/api with `resume` in the given state (""  = null);
// reason only for "dropped".
func agentRead(status, resumeState, reason string) string {
	resume := "null"
	switch {
	case resumeState == "dropped":
		resume = fmt.Sprintf(`{"state":"dropped","reason":%q,"requested_at":"2026-09-26T10:00:00Z"}`, reason)
	case resumeState != "":
		resume = fmt.Sprintf(`{"state":%q,"reason":null,"requested_at":"2026-09-26T10:00:00Z"}`, resumeState)
	}
	return fmt.Sprintf(`{"id":"a","name":"api","status":%q,"agent_type":"service","resume":%s}`,
		status, resume)
}

// recordStartWaits replaces the wait between reads with a recorder.
func recordStartWaits(t *testing.T) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	old := startWaitSleep
	startWaitSleep = func(d time.Duration) { waits = append(waits, d) }
	t.Cleanup(func() { startWaitSleep = old })
	return &waits
}

func runStart(t *testing.T, srv *httptest.Server, wait bool) (string, error) {
	t.Helper()
	client := api.NewClientWithURL(srv.URL, "afy_test_key")
	var err error
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() { err = startAgent(client, "api", wait) })
	})
	return strings.ToLower(stdout + stderr), err
}

func TestAStartAcceptedDuringAStopIsOneRequestAndSaysSo(t *testing.T) {
	srv, s := newStartServer(t, acceptedDuringAStop, nil)
	waits := recordStartWaits(t)

	out, err := runStart(t, srv, false)

	if err != nil {
		t.Fatalf("an accepted start returned an error: %v\n%s", err, out)
	}
	if s.starts != 1 || s.reads != 0 || len(*waits) != 0 {
		t.Fatalf("%d start(s), %d read(s), %d wait(s): an accepted start is one request and no waiting",
			s.starts, s.reads, len(*waits))
	}
	// The server's WHOLE wait, so a CLI printing a constant -- or the stop's
	// part of it -- cannot pass.
	if !strings.Contains(out, "will start once its previous stop finishes (at most 4100s)") {
		t.Fatalf("the accepted start is not reported with the server's bound:\n%s", out)
	}
	if !strings.Contains(out, "afy status api") {
		t.Fatalf("the output does not say how to follow it:\n%s", out)
	}
	if strings.Contains(out, "resumed") || strings.Contains(out, "serving requests") {
		t.Fatalf("an accepted start was reported as a started agent:\n%s", out)
	}
}

func TestWaitFollowsTheAgentsStatusAndNeverSendsTheStartAgain(t *testing.T) {
	srv, s := newStartServer(t, acceptedDuringAStop, []string{
		agentRead("paused", "pending", ""), agentRead("paused", "queued", ""), agentRead("running", "", ""),
	})
	waits := recordStartWaits(t)

	out, err := runStart(t, srv, true)

	if err != nil {
		t.Fatalf("a start that the platform carried out returned an error: %v\n%s", err, out)
	}
	if s.starts != 1 {
		t.Fatalf("the start was sent %d times; --wait follows the status, it never re-sends", s.starts)
	}
	if s.reads != 3 || len(*waits) != 3 {
		t.Fatalf("%d read(s) and %d wait(s); want 3 of each (paused, paused, running)", s.reads, len(*waits))
	}
	if !strings.Contains(out, "agent 'api' resumed") {
		t.Fatalf("the start the platform carried out is not reported:\n%s", out)
	}
}

func TestWaitPrintsTheReasonTheStartWasDropped(t *testing.T) {
	// Every published reason, each with its OWN sentence: a CLI printing one
	// fixed explanation, or the list of possibilities, cannot pass.
	seen := map[string]bool{}
	for reason, sentence := range resumeDropSentences {
		srv, s := newStartServer(t, acceptedDuringAStop, []string{
			agentRead("paused", "pending", ""), agentRead("paused", "dropped", reason),
		})
		recordStartWaits(t)

		out, err := runStart(t, srv, true)

		if err == nil {
			t.Fatalf("%s: a dropped start exited 0:\n%s", reason, out)
		}
		if s.starts != 1 {
			t.Fatalf("%s: the start was sent %d times", reason, s.starts)
		}
		if !strings.Contains(out, "was not started: "+sentence) || !strings.Contains(out, "afy start api") {
			t.Fatalf("%s: the drop is not explained by its reason (%q), with what to do:\n%s",
				reason, sentence, out)
		}
		seen[sentence] = true
	}
	if len(seen) != len(resumeDropSentences) {
		t.Fatalf("two reasons share a sentence: %v", resumeDropSentences)
	}
}

func TestAReasonThisBinaryDoesNotKnowStillReadsAsASentence(t *testing.T) {
	srv, _ := newStartServer(t, acceptedDuringAStop, []string{agentRead("paused", "dropped", "new_reason")})
	recordStartWaits(t)
	out, err := runStart(t, srv, true)
	if err == nil || !strings.Contains(out, "was not started: the platform could not carry it out") ||
		strings.Contains(out, "new_reason") {
		t.Fatalf("an unknown reason must read as a sentence, never a raw code:\n%s", out)
	}
}

func TestWaitGivesUpAtTheServersBoundAndSaysTheStartIsStillPending(t *testing.T) {
	// The bound is the server's wait_bound_seconds, and nothing else: 20s, at
	// the 5s poll, is four reads. (The stop's 155s must not be what is used.)
	srv, s := newStartServer(t,
		`{"status":"paused","agent_id":"a","readiness":null,`+
			`"resume":{"state":"pending","reason":null,"requested_at":"2026-09-26T10:00:00Z"},`+
			`"stop_bound_seconds":155,"wait_bound_seconds":20}`,
		[]string{agentRead("paused", "pending", "")})
	waits := recordStartWaits(t)

	out, err := runStart(t, srv, true)

	if err == nil {
		t.Fatalf("a start still pending past the bound exited 0:\n%s", out)
	}
	if s.starts != 1 || s.reads != 4 || len(*waits) != 4 {
		t.Fatalf("%d start(s), %d read(s), %d wait(s); want 1, 4, 4", s.starts, s.reads, len(*waits))
	}
	if !strings.Contains(out, "still pending") {
		t.Fatalf("giving up must not read as the start failing -- it is still pending:\n%s", out)
	}
}

func TestAnOrdinaryStartIsUnchangedByWait(t *testing.T) {
	srv, s := newStartServer(t,
		`{"status":"running","agent_id":"a","readiness":"serving","resume":null}`, nil)
	waits := recordStartWaits(t)

	out, err := runStart(t, srv, true)

	if err != nil {
		t.Fatalf("an ordinary start returned an error: %v", err)
	}
	if s.starts != 1 || s.reads != 0 || len(*waits) != 0 {
		t.Fatalf("%d start(s), %d read(s), %d wait(s): a started agent has nothing to wait for",
			s.starts, s.reads, len(*waits))
	}
	if !strings.Contains(out, "serving requests") {
		t.Fatalf("the ordinary start is not reported as usual:\n%s", out)
	}
}

func TestWaitRefusesToGuessWhenTheServerStatesNoBound(t *testing.T) {
	srv, s := newStartServer(t,
		`{"status":"paused","agent_id":"a","readiness":null,`+
			`"resume":{"state":"pending","reason":null,"requested_at":"2026-09-26T10:00:00Z"}}`,
		[]string{agentRead("paused", "pending", "")})
	waits := recordStartWaits(t)

	out, err := runStart(t, srv, true)

	if err == nil || s.reads != 0 || len(*waits) != 0 {
		t.Fatalf("with no wait_bound_seconds, --wait must not invent one (err=%v, %d reads):\n%s",
			err, s.reads, out)
	}
}

// THE PIN. The keys of resumeDropSentences must be exactly the control plane's
// RESUME_DROP_REASONS. Live against the sibling checkout, like the readiness
// pin: skipped where there is none, FAILED where cperrors.RequireEnv says there
// must be (the e2e nightly).
func TestStartDescribesExactlyTheControlPlanesDropReasons(t *testing.T) {
	cpRoot := cperrors.Root("..")
	if !cperrors.RootExists(cpRoot) {
		cperrors.SkipUnlessRequired(t, "SKIPPED: no control-plane checkout at %s (set %s to point elsewhere)",
			cpRoot, cperrors.RootEnv)
	}
	reasons, err := cpresume.Extract(cpRoot)
	require.NoError(t, err, "reading RESUME_DROP_REASONS from %s", cpRoot)
	require.NoError(t, cpresume.Validate(reasons))

	var known []string
	for k := range resumeDropSentences {
		known = append(known, k)
	}
	sort.Strings(known)
	assert.Equal(t, cpresume.Set(reasons), known,
		"`afy start --wait` and `afy status` describe %v, and the control plane publishes %v (%s in %s). "+
			"Add or rename the entry in resumeDropSentences -- and the table in docs-site's "+
			"agents/api-lifecycle.mdx, and the dashboard's RESUME_DROP_SENTENCES.",
		known, cpresume.Set(reasons), cpresume.SourcePath, cpRoot)
}
