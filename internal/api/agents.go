package api

import "fmt"

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
