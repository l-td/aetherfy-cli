package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// A PUSH WAITING FOR THE DEPLOY IN PROGRESS HAS NO DEPLOYMENT ROW, so the agent's
// link (`github.waiting_push`) is the only thing that says it exists. These pin
// that `afy status` and `afy deployments` print it, and print nothing when no
// push waits.

const waitingLink = `{"linked":true,"repo":"l-td/templates","branch":"main","root_dir":null,` +
	`"webhook_id":"1","account_connected":true,"branch_deleted_at":null,` +
	`"waiting_push":{"sha":"9f8e7d6c5b4a39281706f5e4d3c2b1a098765432","received_at":"2026-09-26T12:00:00Z"}}`

const idleLink = `{"linked":true,"repo":"l-td/templates","branch":"main","root_dir":null,` +
	`"webhook_id":"1","account_connected":true,"branch_deleted_at":null,"waiting_push":null}`

const waitingLine = "9f8e7d6, received 2026-09-26 12:00 UTC — deploys when the deploy in progress ends"

func TestStatusShowsTheWaitingPush(t *testing.T) {
	out, _ := runStatus(t, "text", waitingLink)
	if !strings.Contains(out, "Push waiting") || !strings.Contains(out, waitingLine) {
		t.Fatalf("afy status did not show the waiting push:\n%s", out)
	}
}

func TestStatusSaysNothingWhenNoPushWaits(t *testing.T) {
	out, _ := runStatus(t, "text", idleLink)
	if strings.Contains(out, "Push waiting") {
		t.Fatalf("afy status invented a waiting push:\n%s", out)
	}
}

func TestStatusJSONCarriesTheWaitingPush(t *testing.T) {
	out, _ := runStatus(t, "json", waitingLink)
	if !strings.Contains(out, `"waiting_push"`) || !strings.Contains(out, "9f8e7d6c5b4a39281706f5e4d3c2b1a098765432") {
		t.Fatalf("afy status -o json dropped waiting_push:\n%s", out)
	}
}

// deploymentsWithAgent answers the deployments list with deploymentsBody and the
// agent read with an agent carrying `link`.
func deploymentsWithAgent(t *testing.T, link string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/deployments") {
			_, _ = w.Write([]byte(deploymentsBody))
			return
		}
		_, _ = w.Write([]byte(statusAgentJSON(link)))
	}))
	t.Cleanup(srv.Close)
	var err error
	out := captureStdout(t, func() {
		err = printDeployments(api.NewClientWithURL(srv.URL, "afy_test_key"), "reporter")
	})
	if err != nil {
		t.Fatalf("afy deployments failed: %v", err)
	}
	return out
}

func TestDeploymentsNameTheWaitingPushAfterTheHistory(t *testing.T) {
	out := deploymentsWithAgent(t, waitingLink)
	i := strings.Index(out, "Push waiting")
	if i < 0 || !strings.Contains(out, waitingLine) {
		t.Fatalf("afy deployments did not show the waiting push:\n%s", out)
	}
	if j := strings.Index(out, "Total:"); j < 0 || j > i {
		t.Fatalf("the waiting push must follow the history it continues:\n%s", out)
	}
}

func TestDeploymentsSayNothingWhenNoPushWaits(t *testing.T) {
	out := deploymentsWithAgent(t, idleLink)
	if strings.Contains(out, "Push waiting") {
		t.Fatalf("afy deployments invented a waiting push:\n%s", out)
	}
}
