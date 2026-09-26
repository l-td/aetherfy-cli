package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// `afy start` WHILE THE PREVIOUS STOP IS STILL FINISHING.
//
// The control plane ACCEPTS that start (202, resume_pending): it keeps the
// request and starts the agent itself once the stop completes. So the CLI
// sends /start ONCE and says so; with --wait it follows the agent's STATUS
// until it runs -- it never asks for the start again. The server here is a
// stand-in answering the control plane's bodies field for field
// (api/routes/agents.py start_agent, AgentResponse.resume_pending); the waits
// between reads are recorded, not spent.

// The control plane's 202 for a start accepted during a stop.
const acceptedDuringAStop = `{"status":"paused","agent_id":"a","readiness":null,"resume_pending":true,"stop_bound_seconds":155}`

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

func agentRead(status string, resumePending bool) string {
	return fmt.Sprintf(`{"id":"a","name":"api","status":%q,"agent_type":"service","resume_pending":%v}`,
		status, resumePending)
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
	// The server's own bound, so a CLI printing a constant cannot pass.
	if !strings.Contains(out, "will start once its previous stop finishes (up to 155s)") {
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
		agentRead("paused", true), agentRead("paused", true), agentRead("running", false),
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

func TestWaitSaysSoWhenTheAcceptedStartWasDropped(t *testing.T) {
	srv, s := newStartServer(t, acceptedDuringAStop, []string{
		agentRead("paused", true), agentRead("paused", false),
	})
	recordStartWaits(t)

	out, err := runStart(t, srv, true)

	if err == nil {
		t.Fatalf("a dropped start exited 0:\n%s", out)
	}
	if s.starts != 1 {
		t.Fatalf("the start was sent %d times", s.starts)
	}
	if !strings.Contains(out, "was not started") || !strings.Contains(out, "afy start api") {
		t.Fatalf("the dropped start is not explained, with what to do:\n%s", out)
	}
}

func TestWaitGivesUpPastTheBoundAndSaysTheStartIsStillPending(t *testing.T) {
	srv, s := newStartServer(t,
		`{"status":"paused","agent_id":"a","readiness":null,"resume_pending":true,"stop_bound_seconds":10}`,
		[]string{agentRead("paused", true)})
	waits := recordStartWaits(t)
	old := startWaitBeyondTheStop
	startWaitBeyondTheStop = 10 * time.Second
	t.Cleanup(func() { startWaitBeyondTheStop = old })

	out, err := runStart(t, srv, true)

	if err == nil {
		t.Fatalf("a start still pending past the bound exited 0:\n%s", out)
	}
	// 10s of stop + 10s beyond it, at the 5s poll: four reads.
	if s.starts != 1 || s.reads != 4 || len(*waits) != 4 {
		t.Fatalf("%d start(s), %d read(s), %d wait(s); want 1, 4, 4", s.starts, s.reads, len(*waits))
	}
	if !strings.Contains(out, "still pending") {
		t.Fatalf("giving up must not read as the start failing -- it is still pending:\n%s", out)
	}
}

func TestAnOrdinaryStartIsUnchangedByWait(t *testing.T) {
	srv, s := newStartServer(t,
		`{"status":"running","agent_id":"a","readiness":"serving","resume_pending":false}`, nil)
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
