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
