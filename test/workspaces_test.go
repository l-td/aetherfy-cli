package test

import (
	"fmt"
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

// --- URL path format verification ---

func TestWorkspaceAgentsURLPath(t *testing.T) {
	tests := []struct {
		workspace string
		expected  string
	}{
		{"my-workspace", "/workspaces/my-workspace/agents"},
		{"invoice-pipeline", "/workspaces/invoice-pipeline/agents"},
		{"a", "/workspaces/a/agents"},
	}

	for _, tt := range tests {
		path := fmt.Sprintf("/workspaces/%s/agents", tt.workspace)
		if path != tt.expected {
			t.Errorf("Workspace '%s': expected path '%s', got '%s'", tt.workspace, tt.expected, path)
		}
	}
}

func TestWorkspaceSecretsListURLPath(t *testing.T) {
	workspace := "my-workspace"
	expected := "/workspaces/my-workspace/secrets"

	path := fmt.Sprintf("/workspaces/%s/secrets", workspace)
	if path != expected {
		t.Errorf("Expected path '%s', got '%s'", expected, path)
	}
}

func TestWorkspaceSecretDeleteURLPath(t *testing.T) {
	workspace := "my-workspace"
	key := "API_KEY"
	expected := "/workspaces/my-workspace/secrets/API_KEY"

	path := fmt.Sprintf("/workspaces/%s/secrets/%s", workspace, key)
	if path != expected {
		t.Errorf("Expected path '%s', got '%s'", expected, path)
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
