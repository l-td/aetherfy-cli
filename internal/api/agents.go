package api

import (
	"fmt"
	"strings"
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

// StopAgent pauses an agent (user-invoked). The server flips Fly's auto-start
// off and stops every machine. Reversible via StartAgent.
func (c *Client) StopAgent(idOrName string) error {
	return c.Post(fmt.Sprintf("/agents/%s/stop", idOrName), nil, nil)
}

// StartAgent resumes a paused agent.
func (c *Client) StartAgent(idOrName string) error {
	return c.Post(fmt.Sprintf("/agents/%s/start", idOrName), nil, nil)
}

// ArchiveAgent archives an agent: the server destroys its Fly app (freeing the
// plan quota slot) while preserving all config and the S3 code bundle.
// Reversible via RestoreAgent. Returns 202 on success.
func (c *Client) ArchiveAgent(idOrName string) error {
	return c.Post(fmt.Sprintf("/agents/%s/archive", idOrName), nil, nil)
}

// RestoreAgent re-provisions an archived agent from its preserved code bundle.
// Quota is re-checked server-side (may 403 PLAN_LIMIT_EXCEEDED). Returns 202 on
// success; the deploy then runs asynchronously.
func (c *Client) RestoreAgent(idOrName string) error {
	return c.Post(fmt.Sprintf("/agents/%s/restore", idOrName), nil, nil)
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
