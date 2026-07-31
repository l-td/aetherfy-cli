package api

import (
	"encoding/json"
	"time"
)

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
	// AllowedWorkers / ParentAgentID pull through from the server's
	// AgentResponse (no new server work). AllowedWorkers lists the JOB
	// names a SERVICE may spawn; ParentAgentID is the SERVICE that spawned
	// this JOB instance (nil for non-spawned / standalone agents).
	AllowedWorkers []string `json:"allowed_workers,omitempty"`
	ParentAgentID  *string  `json:"parent_agent_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	// Derived resource health from the server's AgentResponse (control-plane
	// REVIEW_FAQ §63) — computed from the agent's CURRENT deployment, not a
	// stored status. IsDegraded = a partial multi-region deploy still
	// converging; DegradedReason is a human summary. Surfaced on
	// `afy agents list` / `afy agents status`. No omitempty on IsDegraded:
	// health must serialize explicitly so `-o json` health-check scripts can
	// read it. (No is_serving — agent.Status answers "is it up"; the server
	// keeps is_serving on the deployment, where it's well-defined.)
	IsDegraded     bool   `json:"is_degraded"`
	RegionsTotal   int    `json:"regions_total"`
	RegionsReady   int    `json:"regions_ready"`
	DegradedReason string `json:"degraded_reason,omitempty"`
	// Cron scheduling (CP-4). Carried on the agent list + detail responses.
	// CronSchedule is the 5-field UTC expression (empty = no schedule); the
	// rest is scheduler-written fire-time telemetry feeding the CLI badges.
	// CronLastStatus is one of 'fired'|'skipped'|'missed'; CronLastReason is
	// the reason/error code when a fire did not happen.
	CronSchedule   string     `json:"cron_schedule,omitempty"`
	CronNextRunAt  *time.Time `json:"cron_next_run_at,omitempty"`
	CronPaused     bool       `json:"cron_paused,omitempty"`
	CronLastRunAt  *time.Time `json:"cron_last_run_at,omitempty"`
	CronLastStatus string     `json:"cron_last_status,omitempty"`
	CronLastReason string     `json:"cron_last_reason,omitempty"`
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
	// WorkspaceName is tri-state to match the backend PATCH /agents
	// three-way semantic (omitted / null / string):
	//   - nil (unset)                  → field omitted → no change
	//   - json.RawMessage("null")      → "workspace_name": null → clear
	//   - json.RawMessage(`"my-ws"`)   → "workspace_name": "my-ws" → set
	// json.RawMessage (not *string) is required: *string+omitempty cannot
	// emit an explicit JSON null while still omitting when unset, and a
	// non-omitempty *string would make every other update (e.g. rename)
	// send "workspace_name": null and silently clear the workspace.
	WorkspaceName json.RawMessage `json:"workspace_name,omitempty"`
}

// Deployment represents a deployment
type Deployment struct {
	ID           string    `json:"id"`
	AgentID      string    `json:"agent_id"`
	Version      int       `json:"version"`
	Status       string    `json:"state"`
	Regions      []string  `json:"regions,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	DeployedAt   *time.Time `json:"deployed_at,omitempty"`
	// True for spawn invocations (one Deployment row per spawn() call).
	// CLI cancel filters these out — only user-initiated deploys
	// (is_ephemeral=false) are user-cancellable.
	IsEphemeral bool `json:"is_ephemeral,omitempty"`
	// Set by the cancel route. For QUEUED the route returns state="failed"
	// and CancellationRequested=true (synchronous path). For
	// BUILDING/DEPLOYING the route returns the current in-flight state
	// with CancellationRequested=true — the worker will transition state
	// to failed at its next checkpoint. The CLI uses this to disambiguate
	// "cancelled" from "cancellation requested, worker cleaning up".
	CancellationRequested bool   `json:"cancellation_requested,omitempty"`
	CancellationReason    string `json:"cancellation_reason,omitempty"`
	// Partial-deploy contract (server DeploymentResponse; REVIEW_FAQ §63). A
	// degraded deployment stays state=ACTIVE and converges in the background;
	// RegionsReady/RegionsTotal is the N/M progress. No omitempty on IsDegraded:
	// it must serialize explicitly for `-o json` scripting. (is_serving exists
	// on the API but isn't modeled here — the State field already conveys
	// active; we only surface the degraded delta.)
	IsDegraded              bool   `json:"is_degraded"`
	RegionsTotal            int    `json:"regions_total"`
	RegionsReady            int    `json:"regions_ready"`
	PendingRegionAlertStage string `json:"pending_region_alert_stage,omitempty"`
}

// DeployRequest is the request body for deploying
type DeployRequest struct {
	AgentID string `json:"agent_id"`
	Region  string `json:"region,omitempty"`
}

// DeployResponse is the response from a deploy request (maps to DeploymentResponse on the server).
type DeployResponse struct {
	DeploymentID string `json:"id"`
	AgentID      string `json:"agent_id"`
	Version      int    `json:"version"`
	Status       string `json:"state"`
	QueuePosition *int  `json:"queue_position,omitempty"`
}

// RollbackResponse is the response from a rollback request (same shape as DeploymentResponse).
type RollbackResponse struct {
	ID           string    `json:"id"`
	AgentID      string    `json:"agent_id"`
	Version      int       `json:"version"`
	State        string    `json:"state"`
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
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
	ChildAgentID string                 `json:"child_agent_id"`
	Payload      map[string]interface{} `json:"payload,omitempty"`
}

// SpawnResponse is the response from a spawn request
type SpawnResponse struct {
	SpawnID      string `json:"spawn_id"`
	DeploymentID string `json:"deployment_id"`
	MachineID    string `json:"machine_id,omitempty"`
	Status       string `json:"status"`
	Message      string `json:"message"`
}

// RunAgentRequest is the optional body for POST /agents/{id}/run. Payload is
// passed to the ephemeral run the same way spawn's payload is (omitted when
// nil). Manual runs carry no client_operation_key — they are intentional
// repeats — mirroring spawn's request contract.
type RunAgentRequest struct {
	Payload map[string]interface{} `json:"payload,omitempty"`
}

// RunAgentResponse is the 202 body from POST /agents/{id}/run — the ephemeral
// (root) run's deployment id, version, and the DEPLOY job id.
type RunAgentResponse struct {
	DeploymentID string `json:"deployment_id"`
	Version      int    `json:"version"`
	JobID        string `json:"job_id"`
}

// AgentRun is one row from GET /agents/{id}/runs — the cron/manual run history.
// State includes the terminal COMPLETED / FAILED outcomes (real results, not
// just "started"). DurationSeconds is stopped-started when both machine
// timestamps exist, else nil.
type AgentRun struct {
	ID               string     `json:"id"`
	TriggerSource    string     `json:"trigger_source"`
	State            string     `json:"state"`
	CreatedAt        time.Time  `json:"created_at"`
	ErrorMessage     string     `json:"error_message,omitempty"`
	MachineStartedAt *time.Time `json:"machine_started_at,omitempty"`
	MachineStoppedAt *time.Time `json:"machine_stopped_at,omitempty"`
	DurationSeconds  *float64   `json:"duration_seconds,omitempty"`
}

// RunsQuery bundles the GET /agents/{id}/runs query parameters. TriggerSource
// narrows to 'cron' or 'manual' (empty = both); Before is an ISO cursor
// (created_at < before).
type RunsQuery struct {
	TriggerSource string
	Limit         int
	Before        string
}

// ScheduleStateResponse is the body from POST /agents/{id}/schedule/pause and
// /schedule/resume. Changed is false when the schedule was already in the
// requested pause state (idempotent no-op).
type ScheduleStateResponse struct {
	CronPaused    bool       `json:"cron_paused"`
	CronNextRunAt *time.Time `json:"cron_next_run_at,omitempty"`
	Changed       bool       `json:"changed"`
}

// UserInfo represents the authenticated user
type UserInfo struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Tier   string `json:"tier"`
}

// LogEntry represents a log line
type LogEntry struct {
	ID        int64     `json:"id,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Stream    string    `json:"stream,omitempty"`
	Level     string    `json:"level,omitempty"`
	Message   string    `json:"message"`
}

// Workspace represents a workspace namespace for multi-agent coordination
type Workspace struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Description         string    `json:"description,omitempty"`
	AgentCount          int       `json:"agent_count"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// WorkspaceCreateRequest is the request body for creating a workspace
type WorkspaceCreateRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// WorkspaceUpdateRequest is the request body for PATCH /workspaces/{name}.
// Only description is mutable; name is intentionally not included here —
// the API rejects rename attempts with 400 WORKSPACE_NAME_IMMUTABLE
// (see docs/REVIEW_FAQ.md §53 in the control-plane repo).
type WorkspaceUpdateRequest struct {
	Description string `json:"description"`
}

// WorkspaceDeleteResponse is the response from deleting a workspace
type WorkspaceDeleteResponse struct {
	Status         string `json:"status"`
	SecretsDeleted int    `json:"secrets_deleted"`
}

// HealthResponse is the response from health check
type HealthResponse struct {
	Status    string `json:"status"`
	Version   string `json:"version,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// GitHubStatus represents the GitHub connection status for the authenticated user
type GitHubStatus struct {
	Connected      bool       `json:"connected"`
	InstallationID *int64     `json:"installation_id,omitempty"`
	ConnectedAt    *time.Time `json:"connected_at,omitempty"`
}

// GitHubLinkRequest is the request body for linking an agent to a GitHub repo.
// RootDir is the repo-relative folder holding this agent's code and
// aetherfy.yaml; omitted means the repository root.
type GitHubLinkRequest struct {
	Repo    string `json:"repo"`
	Branch  string `json:"branch,omitempty"`
	RootDir string `json:"root_dir,omitempty"`
}

// GitHubLinkResponse is the response from linking an agent to a GitHub repo.
// WebhookSecret is returned ONCE, at link time — there is no fetch endpoint,
// and `afy github link`'s help promises we print it, so it must be decoded.
type GitHubLinkResponse struct {
	Repo          string `json:"repo"`
	Branch        string `json:"branch"`
	RootDir       string `json:"root_dir"`
	WebhookID     string `json:"webhook_id"`
	WebhookSecret string `json:"webhook_secret"`
}
