package test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// CancelDeployment is the API-client primitive; the `afy cancel`
// command composes ListDeployments → CancelDeployment. These tests cover
// the client-side primitive only; the command-level "find pending deploy"
// logic is composition of well-tested primitives.

func TestCancelDeployment_QueuedSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/agents/my-agent/deployments/3/cancel" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(api.Deployment{
			ID:           "dep-123",
			AgentID:      "agent-1",
			Version:      3,
			Status:       "failed",
			ErrorMessage: "Cancelled by user before build started.",
			CreatedAt:    time.Now(),
		})
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	resp, err := client.CancelDeployment("my-agent", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Version != 3 {
		t.Errorf("expected version 3, got %d", resp.Version)
	}
	if resp.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", resp.Status)
	}
}

func TestCancelDeployment_VersionNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"detail": "Deployment version 99 not found for this agent.",
		})
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	_, err := client.CancelDeployment("my-agent", 99)
	if err == nil {
		t.Fatal("expected error for missing version, got nil")
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("expected *api.APIError, got %T", err)
	}
	if apiErr.StatusCode != 404 {
		t.Errorf("expected 404, got %d", apiErr.StatusCode)
	}
}

// In-flight builds (BUILDING/DEPLOYING) return 202 with state unchanged
// and cancellation_requested=true. The CLI uses CancellationRequested to
// distinguish the in-flight async path from the QUEUED sync path.
func TestCancelDeployment_BuildingReturnsInFlightShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":                      "dep-123",
			"agent_id":                "agent-1",
			"version":                 3,
			"state":                   "building", // unchanged — worker will transition
			"created_at":              time.Now().Format(time.RFC3339),
			"cancellation_requested":  true,
			"cancellation_reason":     "user_cancel",
		})
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	resp, err := client.CancelDeployment("my-agent", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "building" {
		t.Errorf("expected state='building' (unchanged), got %q", resp.Status)
	}
	if !resp.CancellationRequested {
		t.Error("expected CancellationRequested=true on in-flight cancel response")
	}
	if resp.CancellationReason != "user_cancel" {
		t.Errorf("expected CancellationReason='user_cancel', got %q", resp.CancellationReason)
	}
}

// Terminal states (active/failed) return 409. Distinct error message from
// the in-flight path.
func TestCancelDeployment_ActiveReturns409(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"detail": "Deployment v3 is in terminal state 'active' and cannot be cancelled.",
		})
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	_, err := client.CancelDeployment("my-agent", 3)
	if err == nil {
		t.Fatal("expected error for terminal-state deployment, got nil")
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("expected *api.APIError, got %T", err)
	}
	if apiErr.StatusCode != 409 {
		t.Errorf("expected 409, got %d", apiErr.StatusCode)
	}
}
