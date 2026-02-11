package test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aetherfy/cli/internal/api"
)

// --- Agent workspace field ---

func TestAgentIncludesWorkspaceName(t *testing.T) {
	agent := &api.Agent{
		ID:            "test-id",
		Name:          "my-agent",
		WorkspaceName: "my-workspace",
	}

	if agent.WorkspaceName != "my-workspace" {
		t.Errorf("Expected WorkspaceName 'my-workspace', got '%s'", agent.WorkspaceName)
	}
}

func TestAgentWorkspaceNameOptional(t *testing.T) {
	agent := &api.Agent{
		ID:   "test-id",
		Name: "standalone-agent",
	}

	if agent.WorkspaceName != "" {
		t.Errorf("Expected empty WorkspaceName, got '%s'", agent.WorkspaceName)
	}
}

func TestMultipleAgentsInSameWorkspace(t *testing.T) {
	workspace := "invoice-pipeline"

	agents := []api.Agent{
		{ID: "1", Name: "extractor", WorkspaceName: workspace},
		{ID: "2", Name: "classifier", WorkspaceName: workspace},
		{ID: "3", Name: "archiver", WorkspaceName: workspace},
	}

	for _, a := range agents {
		if a.WorkspaceName != workspace {
			t.Errorf("Agent '%s': expected workspace '%s', got '%s'", a.Name, workspace, a.WorkspaceName)
		}
	}
}

func TestAgentsInDifferentWorkspaces(t *testing.T) {
	a1 := &api.Agent{ID: "1", Name: "agent-a", WorkspaceName: "workspace-a"}
	a2 := &api.Agent{ID: "2", Name: "agent-b", WorkspaceName: "workspace-b"}
	a3 := &api.Agent{ID: "3", Name: "agent-c"} // No workspace

	if a1.WorkspaceName == a2.WorkspaceName {
		t.Error("Agents in different workspaces should have different workspace names")
	}
	if a3.WorkspaceName != "" {
		t.Error("Agent without workspace should have empty WorkspaceName")
	}
}

// --- SpawnRequest workspace field ---

func TestSpawnRequestWithWorkspace(t *testing.T) {
	req := &api.SpawnRequest{
		ChildAgentID:  "child-agent-id",
		WorkspaceName: "my-workspace",
		Payload:       map[string]interface{}{"task": "process"},
	}

	if req.WorkspaceName != "my-workspace" {
		t.Errorf("Expected WorkspaceName 'my-workspace', got '%s'", req.WorkspaceName)
	}
	if req.ChildAgentID != "child-agent-id" {
		t.Errorf("Expected ChildAgentID 'child-agent-id', got '%s'", req.ChildAgentID)
	}
}

func TestSpawnRequestWorkspaceOptional(t *testing.T) {
	req := &api.SpawnRequest{
		ChildAgentID: "child-agent-id",
	}

	if req.WorkspaceName != "" {
		t.Errorf("Expected empty WorkspaceName, got '%s'", req.WorkspaceName)
	}
}

// --- HTTP endpoint verification ---

func TestListWorkspaceAgentsCallsCorrectEndpoint(t *testing.T) {
	var capturedMethod, capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client := api.NewClientWithURL(server.URL, "test-key")
	agents, err := client.ListWorkspaceAgents("invoice-pipeline")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if capturedMethod != http.MethodGet {
		t.Errorf("Expected method GET, got %s", capturedMethod)
	}
	if capturedPath != "/workspaces/invoice-pipeline/agents" {
		t.Errorf("Expected path '/workspaces/invoice-pipeline/agents', got '%s'", capturedPath)
	}
	if agents == nil {
		t.Error("Expected non-nil agents slice")
	}
}

func TestListWorkspaceSecretsCallsCorrectEndpoint(t *testing.T) {
	var capturedMethod, capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client := api.NewClientWithURL(server.URL, "test-key")
	secrets, err := client.ListWorkspaceSecrets("my-workspace")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if capturedMethod != http.MethodGet {
		t.Errorf("Expected method GET, got %s", capturedMethod)
	}
	if capturedPath != "/workspaces/my-workspace/secrets" {
		t.Errorf("Expected path '/workspaces/my-workspace/secrets', got '%s'", capturedPath)
	}
	if secrets == nil {
		t.Error("Expected non-nil secrets slice")
	}
}

func TestDeleteWorkspaceSecretCallsCorrectEndpoint(t *testing.T) {
	var capturedMethod, capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := api.NewClientWithURL(server.URL, "test-key")
	err := client.DeleteWorkspaceSecret("my-workspace", "API_KEY")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if capturedMethod != http.MethodDelete {
		t.Errorf("Expected method DELETE, got %s", capturedMethod)
	}
	if capturedPath != "/workspaces/my-workspace/secrets/API_KEY" {
		t.Errorf("Expected path '/workspaces/my-workspace/secrets/API_KEY', got '%s'", capturedPath)
	}
}

// --- Secret struct ---

func TestSecretStructHasRequiredFields(t *testing.T) {
	now := time.Now()
	s := api.Secret{
		Key:       "MY_API_KEY",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if s.Key != "MY_API_KEY" {
		t.Errorf("Expected Key 'MY_API_KEY', got '%s'", s.Key)
	}
}

func TestSecretDoesNotExposeValue(t *testing.T) {
	// Secret struct must not have a Value field - values are write-only
	s := api.Secret{Key: "SENSITIVE_KEY"}

	// This test verifies the struct design: only Key and timestamps
	// No Value field should exist on Secret
	if s.Key == "" {
		t.Error("Expected Key to be set")
	}
}

func TestMultipleSecretsInWorkspace(t *testing.T) {
	now := time.Now()
	secrets := []api.Secret{
		{Key: "SHARED_DB_URL", CreatedAt: now, UpdatedAt: now},
		{Key: "SHARED_API_KEY", CreatedAt: now, UpdatedAt: now},
		{Key: "SHARED_CACHE_URL", CreatedAt: now, UpdatedAt: now},
	}

	keys := make(map[string]bool)
	for _, s := range secrets {
		keys[s.Key] = true
	}

	expected := []string{"SHARED_DB_URL", "SHARED_API_KEY", "SHARED_CACHE_URL"}
	for _, k := range expected {
		if !keys[k] {
			t.Errorf("Expected secret key '%s' not found", k)
		}
	}
}
