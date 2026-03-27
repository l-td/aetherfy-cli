package test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aetherfy/cli/internal/api"
)

func TestRollback_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/agents/my-agent/deployments/3/rollback" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(api.RollbackResponse{
			ID:        "dep-123",
			AgentID:   "agent-1",
			Version:   5,
			State:     "deploying",
			CreatedAt: time.Now(),
		})
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	resp, err := client.Rollback("my-agent", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "dep-123" {
		t.Errorf("expected deployment ID dep-123, got %s", resp.ID)
	}
	if resp.Version != 5 {
		t.Errorf("expected version 5, got %d", resp.Version)
	}
}

func TestRollback_VersionNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"detail": "Deployment version 99 not found for this agent.",
		})
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	_, err := client.Rollback("my-agent", 99)
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

func TestRollback_ConflictInProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"detail": "Agent already has a deployment in progress (status: building).",
		})
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	_, err := client.Rollback("my-agent", 3)
	if err == nil {
		t.Fatal("expected error for conflict, got nil")
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("expected *api.APIError, got %T", err)
	}
	if apiErr.StatusCode != 409 {
		t.Errorf("expected 409, got %d", apiErr.StatusCode)
	}
}

func TestRollback_NoImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"detail": "Version 2 has no built image and cannot be used as a rollback target.",
		})
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	_, err := client.Rollback("my-agent", 2)
	if err == nil {
		t.Fatal("expected error for no image, got nil")
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("expected *api.APIError, got %T", err)
	}
	if apiErr.StatusCode != 422 {
		t.Errorf("expected 422, got %d", apiErr.StatusCode)
	}
}
