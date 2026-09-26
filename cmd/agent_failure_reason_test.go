package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// An agent that cannot start reads `failed` and nothing else in the terminal
// unless the recorded reason is printed. The control plane stores one
// (agents.failure_code) and composes its sentence; on 2026-09-14 a task agent
// whose image the registry had lost was exactly this case, and the explanation
// reached the API and the dashboard while `afy status` showed a bare state.
func statusWithFailure(t *testing.T, extra string) string {
	t.Helper()
	return statusWith(t, "failed", extra)
}

func statusWith(t *testing.T, status, extra string) string {
	t.Helper()
	body := `{"id":"a1","user_id":"u1","name":"tick","status":"` + status + `",` +
		`"agent_type":"service","spawn_enabled":false,"deployed":false,` +
		`"created_at":"2026-09-01T10:00:00Z","updated_at":"2026-09-01T10:00:00Z"` + extra + `}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	var err error
	out := captureStdout(t, func() {
		err = showAgentStatus(api.NewClientWithURL(srv.URL, "afy_test_key"), "tick")
	})
	if err != nil {
		t.Fatalf("afy status failed: %v", err)
	}
	return out
}

func TestStatusPrintsTheRecordedFailureReason(t *testing.T) {
	out := statusWithFailure(t, `,"failure_code":"image_lost_on_provider",`+
		`"failure_message":"The image this agent runs is no longer available on the compute plane, so it cannot start. Deploy again to rebuild the image and bring it back."`)

	if !strings.Contains(out, "Reason") {
		t.Errorf("status printed no Reason line for a failed agent:\n%s", out)
	}
	if !strings.Contains(out, "no longer available") || !strings.Contains(out, "Deploy again") {
		t.Errorf("status did not print the server's own wording:\n%s", out)
	}
}

// A newer control plane can send a code this binary predates. Printing the code
// beats printing nothing, as long as it says how to get the wording.
func TestStatusPrintsACodeItHasNoMessageFor(t *testing.T) {
	out := statusWithFailure(t, `,"failure_code":"some_future_code"`)

	if !strings.Contains(out, "some_future_code") || !strings.Contains(out, "afy upgrade") {
		t.Errorf("status swallowed a failure code it has no message for:\n%s", out)
	}
}

// THE CONTROL. Both tests above would pass against a Reason line that is always
// printed; an agent with no recorded failure must have none.
func TestStatusOfAnAgentWithNoRecordedFailureHasNoReasonLine(t *testing.T) {
	out := statusWithFailure(t, "")

	if strings.Contains(out, "Reason") {
		t.Errorf("a status with no recorded failure printed a Reason line:\n%s", out)
	}
}

// A RESTORE THE PLATFORM REFUSED leaves the agent `archived` -- correctly, it
// still is -- with the reason on the record. The Reason line is printed on any
// status: gated on `failed`, the one answer the customer is waiting for after
// `afy restore` would reach the API and never the terminal.
func TestStatusPrintsARefusedRestoresReasonOnAnArchivedAgent(t *testing.T) {
	out := statusWith(t, "archived", `,"failure_code":"restore_refused_agent_limit",`+
		`"failure_message":"This agent was not restored because your plan's agent limit is reached. It is still archived, with its code, secrets and settings kept. Archive or delete another agent, or upgrade your plan, then restore it again."`)

	if !strings.Contains(out, "Reason") || !strings.Contains(out, "agent limit is reached") {
		t.Errorf("status of an archived agent dropped the refused restore's reason:\n%s", out)
	}
	if !strings.Contains(out, "restore it again") {
		t.Errorf("status did not print the action that fixes the refusal:\n%s", out)
	}
}
