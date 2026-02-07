package api

import "fmt"

// ListWorkspaceAgents returns all agents in a workspace
func (c *Client) ListWorkspaceAgents(workspaceName string) ([]Agent, error) {
	var agents []Agent
	err := c.Get(fmt.Sprintf("/workspaces/%s/agents", workspaceName), &agents)
	if err != nil {
		return nil, err
	}
	return agents, nil
}
