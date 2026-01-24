package test

import (
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
