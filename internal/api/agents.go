package api

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// SpawnableBy returns the names of SERVICE agents whose allowed_workers
// list includes jobName — i.e. the services that could spawn this JOB.
// Computed client-side from a previously-fetched agent list (no extra API
// call, no new endpoint).
func SpawnableBy(jobName string, agents []Agent) []string {
	var out []string
	for i := range agents {
		a := &agents[i]
		if !strings.EqualFold(a.AgentType, "service") {
			continue
		}
		for _, w := range a.AllowedWorkers {
			if w == jobName {
				out = append(out, a.Name)
				break
			}
		}
	}
	return out
}

// AgentNameByID resolves an agent ID to its name using a previously-fetched
// agent list. Returns "" if no agent in the list has that ID.
func AgentNameByID(id string, agents []Agent) string {
	for i := range agents {
		if agents[i].ID == id {
			return agents[i].Name
		}
	}
	return ""
}

// ListAgents returns all agents for the authenticated user
func (c *Client) ListAgents() ([]Agent, error) {
	var agents []Agent
	err := c.Get("/agents", &agents)
	if err != nil {
		return nil, err
	}
	return agents, nil
}

// GetAgent returns a single agent by ID or name
func (c *Client) GetAgent(idOrName string) (*Agent, error) {
	var agent Agent
	err := c.Get("/agents/"+idOrName, &agent)
	if err != nil {
		return nil, err
	}
	return &agent, nil
}

// GetAgentYAML returns the agent's current configuration serialized as
// aetherfy.yaml (the control-plane GET /agents/{name}/yaml endpoint). The
// serialization is owned server-side and shared with the dashboard "Download
// YAML" button — the CLI never re-implements it (§64 PR 3). Returns the raw
// YAML bytes.
func (c *Client) GetAgentYAML(idOrName string) ([]byte, error) {
	return c.GetRaw("/agents/" + idOrName + "/yaml")
}

// CreateAgent creates a new agent
func (c *Client) CreateAgent(req *AgentCreateRequest) (*Agent, error) {
	var agent Agent
	err := c.Post("/agents", req, &agent)
	if err != nil {
		return nil, err
	}
	return &agent, nil
}

// UpdateAgent updates an agent by ID or name
func (c *Client) UpdateAgent(idOrName string, req *AgentUpdateRequest) (*Agent, error) {
	var agent Agent
	err := c.Patch("/agents/"+idOrName, req, &agent)
	if err != nil {
		return nil, err
	}
	return &agent, nil
}

// DeleteAgent deletes an agent by ID or name
func (c *Client) DeleteAgent(idOrName string) error {
	return c.Delete("/agents/" + idOrName)
}

// Bounded retry for the agent lifecycle verbs on the control-plane's two
// explicit "this row is busy right now, the request never ran" answers.
// Delays double from 2s (2+4+8+16 = 30s across 5 attempts), matching the
// envelope the server's own messages suggest ("Retry in a few seconds").
const lifecycleRetryAttempts = 5

// var, not const, purely so tests can shrink the backoff. Read inside the retry
// loop (not captured at init), so an override takes effect at call time.
var lifecycleRetryBaseWait = 2 * time.Second

// contendedLifecycleCodes are the control-plane responses that mean a worker
// held the agent row and OUR REQUEST NEVER EXECUTED — so re-sending cannot
// double-apply anything.
//
//	409 AGENT_OPERATION_IN_PROGRESS — the lifecycle handler's bounded 3s
//	    agent-row wait expired (api/routes/agents.py _lock_agent_row_or_409).
//	503 RESOURCE_BUSY — the global 5s request-path lock bound expired
//	    (shared/error_codes.py). Contended lifecycle calls answer this
//	    routinely now that the bound is actually armed for the whole
//	    transaction; before, they blocked the server's event loop instead.
//
// Deliberately NOT retried: every other 409 (AGENT_ALREADY_PAUSED,
// AGENT_NOT_PAUSED, AGENT_ALREADY_ARCHIVED, AGENT_HAS_PENDING_DEPLOYMENTS) is a
// contract answer about state, and any other 5xx may have half-applied.
var contendedLifecycleCodes = map[int]string{
	409: "AGENT_OPERATION_IN_PROGRESS",
	503: "RESOURCE_BUSY",
}

// postLifecycle issues an agent-lifecycle POST (stop/start/archive/restore)
// with a bounded retry on server-declared contention.
//
// Retrying is safe precisely BECAUSE it is keyed on those two codes: both are
// raised from a BOUNDED lock wait before the handler does any work, so they
// mean "not started", never "half-done". A verb that already took effect
// answers with a different code entirely, so a completed operation is never
// re-sent. Everything else fails fast on the first attempt.
//
// This is the deliberate, response-code-aware retry that the transport-level
// policy in client.go points at: a mutating verb earns a retry at its own call
// site, where idempotency is known — not blindly, in the HTTP layer.
func (c *Client) postLifecycle(path string) error {
	var err error
	for attempt := 1; ; attempt++ {
		err = c.Post(path, nil, nil)
		if !isContendedLifecycle(err) || attempt == lifecycleRetryAttempts {
			return err
		}
		time.Sleep(lifecycleRetryBaseWait << (attempt - 1))
	}
}

// isContendedLifecycle reports whether err is one of the control-plane's
// retryable row-contention answers. Matched on the stable code, not the
// message — a 409/503 carrying any other code must still fail fast.
func isContendedLifecycle(err error) bool {
	apiErr, ok := err.(*APIError)
	if !ok {
		return false
	}
	want, ok := contendedLifecycleCodes[apiErr.StatusCode]
	return ok && apiErr.Code == want
}

// StopAgent pauses an agent (user-invoked). The server flips Fly's auto-start
// off and stops every machine. Reversible via StartAgent.
func (c *Client) StopAgent(idOrName string) error {
	return c.postLifecycle(fmt.Sprintf("/agents/%s/stop", idOrName))
}

// StartAgent resumes a paused agent.
func (c *Client) StartAgent(idOrName string) error {
	return c.postLifecycle(fmt.Sprintf("/agents/%s/start", idOrName))
}

// ArchiveAgent archives an agent: the server destroys its Fly app (freeing the
// plan quota slot) while preserving all config and the S3 code bundle.
// Reversible via RestoreAgent. Returns 202 on success.
func (c *Client) ArchiveAgent(idOrName string) error {
	return c.postLifecycle(fmt.Sprintf("/agents/%s/archive", idOrName))
}

// RestoreAgent re-provisions an archived agent from its preserved code bundle.
// Quota is re-checked server-side (may 403 PLAN_LIMIT_EXCEEDED). Returns 202 on
// success; the deploy then runs asynchronously.
func (c *Client) RestoreAgent(idOrName string) error {
	return c.postLifecycle(fmt.Sprintf("/agents/%s/restore", idOrName))
}

// GetAgentStatus returns detailed status for an agent
func (c *Client) GetAgentStatus(idOrName string) (*Agent, error) {
	return c.GetAgent(idOrName)
}

// SpawnAgent spawns a JOB agent from a SERVICE agent
func (c *Client) SpawnAgent(agentID string, req *SpawnRequest) (*SpawnResponse, error) {
	var resp SpawnResponse
	err := c.Post(fmt.Sprintf("/agents/%s/spawn", agentID), req, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// RunAgent triggers a manual "run now" of a deployed JOB agent (POST
// /agents/{id}/run). The run is a ROOT run: no parent, trigger_source=manual.
// payload is optional (nil omits the body field). Errors carry the CP-4 code
// taxonomy — AGENT_RUN_REQUIRES_JOB_TYPE / AGENT_NOT_DEPLOYED (422),
// AGENT_RUN_IN_PROGRESS / AGENT_RUN_INELIGIBLE_STATE / AGENT_OPERATION_IN_PROGRESS
// (409), the billing-gate 403s — for callers to switch on.
func (c *Client) RunAgent(idOrName string, payload map[string]interface{}) (*RunAgentResponse, error) {
	var resp RunAgentResponse
	err := c.Post(fmt.Sprintf("/agents/%s/run", idOrName), &RunAgentRequest{Payload: payload}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListAgentRuns returns the run history (GET /agents/{id}/runs). Spawn rows are
// excluded server-side; only cron/manual runs are returned. Empty query returns
// the server defaults (both trigger sources, newest first).
func (c *Client) ListAgentRuns(idOrName string, q RunsQuery) ([]AgentRun, error) {
	path := fmt.Sprintf("/agents/%s/runs", idOrName)
	params := url.Values{}
	if q.TriggerSource != "" {
		params.Set("trigger_source", q.TriggerSource)
	}
	if q.Limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", q.Limit))
	}
	if q.Before != "" {
		params.Set("before", q.Before)
	}
	if encoded := params.Encode(); encoded != "" {
		path = path + "?" + encoded
	}

	var runs []AgentRun
	if err := c.Get(path, &runs); err != nil {
		return nil, err
	}
	return runs, nil
}

// PauseSchedule pauses an agent's cron schedule (POST
// /agents/{id}/schedule/pause). Idempotent: Changed is false when it was
// already paused. 422 AGENT_SCHEDULE_NOT_SET when the agent has no schedule.
func (c *Client) PauseSchedule(idOrName string) (*ScheduleStateResponse, error) {
	var resp ScheduleStateResponse
	err := c.Post(fmt.Sprintf("/agents/%s/schedule/pause", idOrName), nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// ResumeSchedule resumes a paused cron schedule (POST
// /agents/{id}/schedule/resume). The server recomputes cron_next_run_at from
// now (skip-to-future; elapsed occurrences are never backfired). Idempotent:
// Changed is false when it was already live.
func (c *Client) ResumeSchedule(idOrName string) (*ScheduleStateResponse, error) {
	var resp ScheduleStateResponse
	err := c.Post(fmt.Sprintf("/agents/%s/schedule/resume", idOrName), nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
