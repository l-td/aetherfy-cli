package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aetherfy/cli/internal/api"
)

// These tests mirror the existing agents_test.go pattern (httptest.Server +
// api.NewClientWithURL) and exercise the archive/restore lifecycle at the HTTP
// call-shape + error-envelope seam — the same seam runAgentsArchive /
// runAgentsRestore drive. They assert the method + path the CLI hits and that
// the canonical control-plane error envelope
// ({"detail":{"code":"...","message":"..."}}) is surfaced with its code intact
// (the restore command switches on Code == "PLAN_LIMIT_EXCEEDED" for its hint).

// captureServer records the last method+path it saw, then replies with the
// given status and body. Used to assert the CLI issues exactly the archive /
// restore POST the locked server contract expects.
func captureServer(t *testing.T, status int, body string, gotMethod, gotPath *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotMethod = r.Method
		*gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestArchiveAgentPostsToArchiveEndpoint(t *testing.T) {
	var method, path string
	srv := captureServer(t, http.StatusAccepted,
		`{"status":"archiving","agent_id":"a1"}`, &method, &path)
	defer srv.Close()
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	if err := client.ArchiveAgent("billing"); err != nil {
		t.Fatalf("ArchiveAgent: unexpected error: %v", err)
	}
	if method != http.MethodPost {
		t.Errorf("archive method: want POST, got %s", method)
	}
	if path != "/agents/billing/archive" {
		t.Errorf("archive path: want /agents/billing/archive, got %s", path)
	}
}

func TestRestoreAgentPostsToRestoreEndpoint(t *testing.T) {
	var method, path string
	srv := captureServer(t, http.StatusAccepted,
		`{"status":"restoring","agent_id":"a1"}`, &method, &path)
	defer srv.Close()
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	if err := client.RestoreAgent("billing"); err != nil {
		t.Fatalf("RestoreAgent: unexpected error: %v", err)
	}
	if method != http.MethodPost {
		t.Errorf("restore method: want POST, got %s", method)
	}
	if path != "/agents/billing/restore" {
		t.Errorf("restore path: want /agents/billing/restore, got %s", path)
	}
}

// AGENT_ALREADY_ARCHIVED (409) must surface the server message + code so the
// archive command can pass it through unchanged.
func TestArchiveAlreadyArchivedSurfacesEnvelope(t *testing.T) {
	var method, path string
	srv := captureServer(t, http.StatusConflict,
		`{"detail":{"code":"AGENT_ALREADY_ARCHIVED","message":"Agent is already archived."}}`,
		&method, &path)
	defer srv.Close()
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	err := client.ArchiveAgent("billing")
	if err == nil {
		t.Fatal("expected an error for already-archived agent, got nil")
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("want *api.APIError, got %T", err)
	}
	if apiErr.Code != "AGENT_ALREADY_ARCHIVED" {
		t.Errorf("code: want AGENT_ALREADY_ARCHIVED, got %q", apiErr.Code)
	}
	if !strings.Contains(apiErr.Message, "already archived") {
		t.Errorf("message not passed through: %q", apiErr.Message)
	}
}

// The restore command adds a "free a slot" hint only when the code is
// PLAN_LIMIT_EXCEEDED — assert the envelope carries that code (403) so the
// command's switch fires.
func TestRestorePlanLimitExceededCarriesCode(t *testing.T) {
	var method, path string
	srv := captureServer(t, http.StatusForbidden,
		`{"detail":{"code":"PLAN_LIMIT_EXCEEDED","message":"Plan limit reached."}}`,
		&method, &path)
	defer srv.Close()
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	err := client.RestoreAgent("billing")
	if err == nil {
		t.Fatal("expected an error at plan limit, got nil")
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("want *api.APIError, got %T", err)
	}
	if apiErr.Code != "PLAN_LIMIT_EXCEEDED" {
		t.Errorf("code: want PLAN_LIMIT_EXCEEDED, got %q", apiErr.Code)
	}
}

// AGENT_NOT_ARCHIVED (409) passes through with its code — restore must NOT add
// the plan-limit hint for this one.
func TestRestoreNotArchivedSurfacesEnvelope(t *testing.T) {
	var method, path string
	srv := captureServer(t, http.StatusConflict,
		`{"detail":{"code":"AGENT_NOT_ARCHIVED","message":"Agent is not archived."}}`,
		&method, &path)
	defer srv.Close()
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	err := client.RestoreAgent("billing")
	if err == nil {
		t.Fatal("expected an error for a non-archived agent, got nil")
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("want *api.APIError, got %T", err)
	}
	if apiErr.Code != "AGENT_NOT_ARCHIVED" {
		t.Errorf("code: want AGENT_NOT_ARCHIVED, got %q", apiErr.Code)
	}
	if apiErr.Code == "PLAN_LIMIT_EXCEEDED" {
		t.Error("AGENT_NOT_ARCHIVED must not be treated as a plan-limit error")
	}
}
