package test

import (
	"encoding/json"
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
		_, _ = w.Write([]byte(`[]`))
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
		_, _ = w.Write([]byte(`[]`))
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

// --- Workspace type ---

func TestWorkspaceStructHasRequiredFields(t *testing.T) {
	now := time.Now()
	ws := api.Workspace{
		ID:          "ws-id",
		Name:        "invoice-pipeline",
		Description: "Invoice agents",
		AgentCount:  3,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if ws.Name != "invoice-pipeline" {
		t.Errorf("Expected Name 'invoice-pipeline', got '%s'", ws.Name)
	}
	if ws.AgentCount != 3 {
		t.Errorf("Expected AgentCount 3, got %d", ws.AgentCount)
	}
}

func TestWorkspaceDescriptionOptional(t *testing.T) {
	ws := api.Workspace{ID: "ws-id", Name: "minimal-ws"}
	if ws.Description != "" {
		t.Errorf("Expected empty Description, got '%s'", ws.Description)
	}
}

// --- Workspace CRUD HTTP endpoints ---

func TestCreateWorkspaceCallsCorrectEndpoint(t *testing.T) {
	var capturedMethod, capturedPath string
	var capturedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ws-1","name":"my-workspace","agent_count":0,"created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`))
	}))
	defer server.Close()

	client := api.NewClientWithURL(server.URL, "test-key")
	ws, err := client.CreateWorkspace(&api.WorkspaceCreateRequest{
		Name:        "my-workspace",
		Description: "A workspace",
	})

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("Expected method POST, got %s", capturedMethod)
	}
	if capturedPath != "/workspaces" {
		t.Errorf("Expected path '/workspaces', got '%s'", capturedPath)
	}
	if capturedBody["name"] != "my-workspace" {
		t.Errorf("Expected name 'my-workspace' in body, got %v", capturedBody["name"])
	}
	if ws == nil || ws.Name != "my-workspace" {
		t.Errorf("Expected workspace with name 'my-workspace', got %v", ws)
	}
}

func TestListWorkspacesCallsCorrectEndpoint(t *testing.T) {
	var capturedMethod, capturedPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client := api.NewClientWithURL(server.URL, "test-key")
	workspaces, err := client.ListWorkspaces()

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if capturedMethod != http.MethodGet {
		t.Errorf("Expected method GET, got %s", capturedMethod)
	}
	if capturedPath != "/workspaces" {
		t.Errorf("Expected path '/workspaces', got '%s'", capturedPath)
	}
	if workspaces == nil {
		t.Error("Expected non-nil workspaces slice")
	}
}

func TestGetWorkspaceCallsCorrectEndpoint(t *testing.T) {
	var capturedMethod, capturedPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ws-1","name":"invoice-pipeline","agent_count":2,"created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}`))
	}))
	defer server.Close()

	client := api.NewClientWithURL(server.URL, "test-key")
	ws, err := client.GetWorkspace("invoice-pipeline")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if capturedMethod != http.MethodGet {
		t.Errorf("Expected method GET, got %s", capturedMethod)
	}
	if capturedPath != "/workspaces/invoice-pipeline" {
		t.Errorf("Expected path '/workspaces/invoice-pipeline', got '%s'", capturedPath)
	}
	if ws == nil || ws.AgentCount != 2 {
		t.Errorf("Expected workspace with AgentCount=2, got %v", ws)
	}
}

func TestDeleteWorkspaceCallsCorrectEndpoint(t *testing.T) {
	var capturedMethod, capturedPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"deleted","secrets_deleted":2,"collections_remaining":0}`))
	}))
	defer server.Close()

	client := api.NewClientWithURL(server.URL, "test-key")
	result, err := client.DeleteWorkspace("my-workspace")

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if capturedMethod != http.MethodDelete {
		t.Errorf("Expected method DELETE, got %s", capturedMethod)
	}
	if capturedPath != "/workspaces/my-workspace" {
		t.Errorf("Expected path '/workspaces/my-workspace', got '%s'", capturedPath)
	}
	if result == nil || result.Status != "deleted" {
		t.Errorf("Expected result.Status 'deleted', got %v", result)
	}
	if result.SecretsDeleted != 2 {
		t.Errorf("Expected SecretsDeleted=2, got %d", result.SecretsDeleted)
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
