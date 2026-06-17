package test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aetherfy/cli/internal/api"
)

// TestAgentCreateRequestWithRuntime tests that AgentCreateRequest includes runtime field
func TestAgentCreateRequestWithRuntime(t *testing.T) {
	req := &api.AgentCreateRequest{
		Name:         "test-agent",
		Description:  "Test agent",
		AgentType:    "SERVICE",
		Runtime:      "python3.11",
		SpawnEnabled: false,
	}

	if req.Runtime != "python3.11" {
		t.Errorf("Expected runtime to be 'python3.11', got '%s'", req.Runtime)
	}
}

// TestAgentCreateRequestRuntimeOptional tests that runtime is optional
func TestAgentCreateRequestRuntimeOptional(t *testing.T) {
	req := &api.AgentCreateRequest{
		Name:      "test-agent",
		AgentType: "SERVICE",
	}

	// Runtime should be empty string when not set (omitempty)
	if req.Runtime != "" {
		t.Errorf("Expected runtime to be empty, got '%s'", req.Runtime)
	}
}

// TestAgentIncludesRuntime tests that Agent type includes runtime field
func TestAgentIncludesRuntime(t *testing.T) {
	agent := &api.Agent{
		ID:           "test-id",
		Name:         "test-agent",
		AgentType:    "SERVICE",
		Runtime:      "node20",
		SpawnEnabled: false,
	}

	if agent.Runtime != "node20" {
		t.Errorf("Expected runtime to be 'node20', got '%s'", agent.Runtime)
	}
}

// TestAgentRuntimeOptional tests that runtime is optional on Agent
func TestAgentRuntimeOptional(t *testing.T) {
	agent := &api.Agent{
		ID:        "test-id",
		Name:      "test-agent",
		AgentType: "SERVICE",
	}

	// Runtime should be empty string when not set (omitempty)
	if agent.Runtime != "" {
		t.Errorf("Expected runtime to be empty, got '%s'", agent.Runtime)
	}
}

// TestAgentUpdateRequestWithName tests that AgentUpdateRequest can set name
func TestAgentUpdateRequestWithName(t *testing.T) {
	newName := "new-agent-name"
	req := &api.AgentUpdateRequest{
		Name: &newName,
	}

	if req.Name == nil {
		t.Error("Expected Name to be set, got nil")
	}
	if *req.Name != "new-agent-name" {
		t.Errorf("Expected name to be 'new-agent-name', got '%s'", *req.Name)
	}
}

// TestAgentUpdateRequestOptionalFields tests that all fields are optional
func TestAgentUpdateRequestOptionalFields(t *testing.T) {
	req := &api.AgentUpdateRequest{}

	if req.Name != nil {
		t.Error("Expected Name to be nil when not set")
	}
	if req.Description != nil {
		t.Error("Expected Description to be nil when not set")
	}
	if req.Tier != nil {
		t.Error("Expected Tier to be nil when not set")
	}
	if req.MemoryMB != nil {
		t.Error("Expected MemoryMB to be nil when not set")
	}
	if req.KeepAlive != nil {
		t.Error("Expected KeepAlive to be nil when not set")
	}
}

// TestAgentUpdateRequestPartialUpdate tests updating only some fields
func TestAgentUpdateRequestPartialUpdate(t *testing.T) {
	newName := "renamed-agent"
	memoryMB := 512

	req := &api.AgentUpdateRequest{
		Name:     &newName,
		MemoryMB: &memoryMB,
	}

	// Set fields should have values
	if req.Name == nil || *req.Name != "renamed-agent" {
		t.Error("Expected Name to be set to 'renamed-agent'")
	}
	if req.MemoryMB == nil || *req.MemoryMB != 512 {
		t.Error("Expected MemoryMB to be set to 512")
	}

	// Unset fields should be nil
	if req.Description != nil {
		t.Error("Expected Description to be nil")
	}
	if req.Tier != nil {
		t.Error("Expected Tier to be nil")
	}
}

// --- workspace_name three-way (omitted / null / string) on PATCH /agents ---
//
// AgentUpdateRequest.WorkspaceName is json.RawMessage so the CLI can express
// the backend's three-way semantic. These tests pin the wire bytes for each
// case via a capturing test server.

func captureUpdateAgentBody(t *testing.T, req *api.AgentUpdateRequest) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"a1","name":"my-agent"}`))
	}))
	defer server.Close()

	client := api.NewClientWithURL(server.URL, "test-key")
	if _, err := client.UpdateAgent("my-agent", req); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	return body
}

func TestUpdateAgentWorkspaceSetSerializesString(t *testing.T) {
	encoded, _ := json.Marshal("my-ws")
	body := captureUpdateAgentBody(t, &api.AgentUpdateRequest{
		WorkspaceName: json.RawMessage(encoded),
	})

	v, present := body["workspace_name"]
	if !present {
		t.Fatal("Expected workspace_name present in body")
	}
	if v != "my-ws" {
		t.Errorf("Expected workspace_name 'my-ws', got %v", v)
	}
}

func TestUpdateAgentNoWorkspaceSerializesNull(t *testing.T) {
	body := captureUpdateAgentBody(t, &api.AgentUpdateRequest{
		WorkspaceName: json.RawMessage("null"),
	})

	v, present := body["workspace_name"]
	if !present {
		t.Fatal("Explicit null must reach the wire, not be omitted — otherwise the agent can't be cleared to workspaceless")
	}
	if v != nil {
		t.Errorf("Expected workspace_name null, got %v", v)
	}
}

func TestUpdateAgentOmitsWorkspaceWhenUnset(t *testing.T) {
	// A rename-style update (workspace_name unset) MUST omit the field —
	// otherwise every rename/other PATCH would send "workspace_name": null
	// and silently clear the agent's workspace.
	newName := "renamed-agent"
	body := captureUpdateAgentBody(t, &api.AgentUpdateRequest{Name: &newName})

	if _, present := body["workspace_name"]; present {
		t.Errorf("Unset workspace_name must be omitted (no-change semantic), got body %v", body)
	}
}

// --- spawn relationship visibility (PR 4) ---

// Agent struct pulls allowed_workers / parent_agent_id through from the
// server's AgentResponse.
func TestAgentDeserializesSpawnFields(t *testing.T) {
	parentID := "parent-id"
	agent := api.Agent{
		ID:             "job-1",
		Name:           "my-job",
		AgentType:      "job",
		AllowedWorkers: []string{"a", "b"},
		ParentAgentID:  &parentID,
	}
	if len(agent.AllowedWorkers) != 2 {
		t.Errorf("Expected 2 allowed workers, got %d", len(agent.AllowedWorkers))
	}
	if agent.ParentAgentID == nil || *agent.ParentAgentID != "parent-id" {
		t.Errorf("Expected ParentAgentID 'parent-id', got %v", agent.ParentAgentID)
	}
}

// Case 1: SERVICE → its allowed_workers are surfaced directly on the agent.
func TestServiceAllowedWorkers(t *testing.T) {
	svc := api.Agent{ID: "s1", Name: "my-service", AgentType: "service", AllowedWorkers: []string{"my-job"}}
	if len(svc.AllowedWorkers) != 1 || svc.AllowedWorkers[0] != "my-job" {
		t.Errorf("Expected allowed_workers [my-job], got %v", svc.AllowedWorkers)
	}
}

// Case 2: JOB → "spawnable by" is the reverse client-side filter over the
// agent list (services whose allowed_workers include this job's name).
func TestSpawnableByComputesReverseView(t *testing.T) {
	all := []api.Agent{
		{ID: "s1", Name: "svc-a", AgentType: "service", AllowedWorkers: []string{"my-job", "other"}},
		{ID: "s2", Name: "svc-b", AgentType: "service", AllowedWorkers: []string{"my-job"}},
		{ID: "s3", Name: "svc-c", AgentType: "service", AllowedWorkers: []string{"unrelated"}},
		{ID: "j1", Name: "my-job", AgentType: "job"}, // a JOB is never a spawner
	}
	got := api.SpawnableBy("my-job", all)
	if len(got) != 2 {
		t.Fatalf("Expected 2 spawners, got %v", got)
	}
	want := map[string]bool{"svc-a": true, "svc-b": true}
	for _, n := range got {
		if !want[n] {
			t.Errorf("Unexpected spawner %q in %v", n, got)
		}
	}
}

func TestSpawnableByEmptyWhenNoSpawner(t *testing.T) {
	all := []api.Agent{
		{ID: "s1", Name: "svc-a", AgentType: "service", AllowedWorkers: []string{"other"}},
	}
	if got := api.SpawnableBy("my-job", all); len(got) != 0 {
		t.Errorf("Expected no spawners, got %v", got)
	}
}

// Case 3: JOB instance with a parent → resolve parent_agent_id to the
// SERVICE's name from the already-fetched agent list.
func TestAgentNameByIDResolvesParent(t *testing.T) {
	all := []api.Agent{
		{ID: "svc-id", Name: "my-service", AgentType: "service"},
		{ID: "job-id", Name: "my-job", AgentType: "job"},
	}
	if name := api.AgentNameByID("svc-id", all); name != "my-service" {
		t.Errorf("Expected 'my-service', got %q", name)
	}
	if name := api.AgentNameByID("missing", all); name != "" {
		t.Errorf("Expected empty string for unknown id, got %q", name)
	}
}
