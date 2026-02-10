package api

import "time"

// Agent represents an agent in the system
type Agent struct {
	ID            string    `json:"id"`
	UserID        string    `json:"user_id"`
	Name          string    `json:"name"`
	Description   string    `json:"description,omitempty"`
	Status        string    `json:"status"`
	AgentType     string    `json:"agent_type"`
	Runtime       string    `json:"runtime,omitempty"`
	Region        string    `json:"region,omitempty"`
	WorkspaceName string    `json:"workspace_name,omitempty"`
	SpawnEnabled  bool      `json:"spawn_enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// AgentCreateRequest is the request body for creating an agent
type AgentCreateRequest struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	AgentType    string `json:"agent_type,omitempty"`
	Runtime      string `json:"runtime,omitempty"`
	SpawnEnabled bool   `json:"spawn_enabled,omitempty"`
}

// AgentUpdateRequest is the request body for updating an agent
type AgentUpdateRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Tier        *string `json:"tier,omitempty"`
	MemoryMB    *int    `json:"memory_mb,omitempty"`
	KeepAlive   *bool   `json:"keep_alive,omitempty"`
}

// Deployment represents a deployment
type Deployment struct {
	ID          string    `json:"id"`
	AgentID     string    `json:"agent_id"`
	Version     int       `json:"version"`
	Status      string    `json:"status"`
	Region      string    `json:"region"`
	ImageTag    string    `json:"image_tag,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

// DeployRequest is the request body for deploying
type DeployRequest struct {
	AgentID string `json:"agent_id"`
	Region  string `json:"region,omitempty"`
}

// DeployResponse is the response from a deploy request
type DeployResponse struct {
	DeploymentID string `json:"deployment_id"`
	JobID        string `json:"job_id"`
	Status       string `json:"status"`
	Message      string `json:"message"`
}

// Secret represents a secret (without the value)
type Secret struct {
	Key       string    `json:"key"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SecretSetRequest is the request body for setting a secret
type SecretSetRequest struct {
	AgentID string `json:"agent_id"`
	Key     string `json:"key"`
	Value   string `json:"value"`
}

// SpawnRequest is the request body for spawning a JOB agent
type SpawnRequest struct {
	ChildAgentID  string                 `json:"child_agent_id"`
	Payload       map[string]interface{} `json:"payload,omitempty"`
	WorkspaceName string                 `json:"workspace_name,omitempty"`
}

// SpawnResponse is the response from a spawn request
type SpawnResponse struct {
	SpawnID      string `json:"spawn_id"`
	DeploymentID string `json:"deployment_id"`
	MachineID    string `json:"machine_id,omitempty"`
	Status       string `json:"status"`
	Message      string `json:"message"`
}

// UserInfo represents the authenticated user
type UserInfo struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Tier   string `json:"tier"`
}

// LogEntry represents a log line
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
	Level     string    `json:"level,omitempty"`
}

// HealthResponse is the response from health check
type HealthResponse struct {
	Status    string `json:"status"`
	Version   string `json:"version,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}
