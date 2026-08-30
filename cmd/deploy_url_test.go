package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/fatih/color"
	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/output"
)

// printAgentURL is FAIL-SOFT: any error and it prints nothing, because a deploy
// that worked must not be reported as a failure because a follow-up read was
// not. That property is also what makes it dangerous to leave unexercised — a
// wrong path, a wrong field name, a client that never reaches the server all
// look identical to "the agent has no URL yet", forever, in silence.
//
// So the happy path is RUN here, not read. `agentID` at the call site is the
// agent NAME (from --agent or aetherfy.yaml), and GetAgent takes id-or-name;
// the server assertion below is what pins that they agree.

// agentURLServer answers GET /agents/{idOrName} and records what was asked for.
func agentURLServer(t *testing.T, body string) (*httptest.Server, *string) {
	t.Helper()
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		if body == "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":{"code":"AGENT_NOT_FOUND","message":"nope"}}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

// captureStdout runs fn with the CLI's output redirected and returns what it
// wrote.
//
// TWO WRITERS, NOT ONE, and this test found that out the hard way. Plain
// output.Println/Printf go through fmt to os.Stdout, but anything coloured —
// output.KeyValue's bold key, output.Dim.Printf — goes through fatih/color's
// package-level color.Output, which is bound to the REAL stdout at init and is
// not affected by swapping os.Stdout. Redirect only one and the capture comes
// back interleaved and half-empty, which reads exactly like the code not
// printing at all.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	oldStdout, oldColor := os.Stdout, color.Output
	os.Stdout, color.Output = w, w
	fn()
	w.Close()
	os.Stdout, color.Output = oldStdout, oldColor

	out, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(out)
}

func TestCaptureStdoutSeesBothWriters(t *testing.T) {
	// Positive control on the harness itself, in both lanes — the silence
	// assertions below are worthless if the capture simply misses output.
	out := captureStdout(t, func() {
		output.Println("plain-lane")
		output.Dim.Printf("colour-lane\n")
	})
	for _, want := range []string{"plain-lane", "colour-lane"} {
		if !strings.Contains(out, want) {
			t.Errorf("capture missed %q; got %q", want, out)
		}
	}
}

func TestPrintAgentURLPrintsTheURLAndACurl(t *testing.T) {
	srv, asked := agentURLServer(t, `{"id":"a1","name":"reporter","agent_type":"service","status":"running","deployed":true,"url":"https://reporter-k3m7x2.aetherfy.dev"}`)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	out := captureStdout(t, func() { printAgentURL(client, "reporter") })

	if !strings.Contains(out, "https://reporter-k3m7x2.aetherfy.dev") {
		t.Errorf("deploy did not print the agent URL; got:\n%s", out)
	}
	// A plain GET, not a POST. Aetherfy adds no routing of its own, so we
	// cannot know which paths exist; a GET to the root is what an arbitrary
	// service is likeliest to answer, and a POST to / would 405 against the
	// quickstart's own sample app (which declares GET / and POST /echo).
	if !strings.Contains(out, "curl https://reporter-k3m7x2.aetherfy.dev") {
		t.Errorf("deploy did not print a ready-to-paste curl; got:\n%s", out)
	}
	// The name-or-id agreement between the deploy call site and GetAgent. A
	// mismatch here would 404 and, being fail-soft, print nothing at all.
	if *asked != "/agents/reporter" {
		t.Errorf("GetAgent asked for %q, want /agents/reporter", *asked)
	}
}

func TestPrintAgentURLStaysSilentWhenThereIsNoURL(t *testing.T) {
	// A draft or a still-building agent has no address. Printing an empty
	// "URL:" line, or a bare "https://.aetherfy.dev", would be worse than
	// printing nothing.
	srv, _ := agentURLServer(t, `{"id":"a1","name":"reporter","agent_type":"service","status":"pending"}`)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	if out := captureStdout(t, func() { printAgentURL(client, "reporter") }); strings.TrimSpace(out) != "" {
		t.Errorf("printed something for an agent with no URL:\n%s", out)
	}
}

func TestPrintAgentURLStaysSilentForADeployedJobAgent(t *testing.T) {
	// A job runs once and exits behind NO HTTP server (the control plane's
	// image generator ships no uvicorn/express and no EXPOSE for JOB mode), so
	// there is nowhere to send a request. The server says that with two fields
	// answering two questions — `deployed: true` and NO url — and the CLI must
	// not invent an address out of the first.
	//
	// The rule itself lives server-side (models.Agent.url). A copy here would be
	// a second answer waiting to disagree with the first, which is exactly what
	// an agent_type check in this function used to be.
	srv, _ := agentURLServer(t, `{"id":"a1","name":"nightly","agent_type":"job","status":"running","deployed":true}`)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	if out := captureStdout(t, func() { printAgentURL(client, "nightly") }); strings.TrimSpace(out) != "" {
		t.Errorf("printed a request target for a deployed job agent, which serves none:\n%s", out)
	}
}

func TestPrintAgentURLStaysSilentWhenTheReadFails(t *testing.T) {
	// The fail-soft contract itself: a successful deploy must not grow a
	// warning because the follow-up read 404'd or the network blipped.
	srv, _ := agentURLServer(t, "")
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	if out := captureStdout(t, func() { printAgentURL(client, "reporter") }); strings.TrimSpace(out) != "" {
		t.Errorf("a failed read printed output:\n%s", out)
	}
}
