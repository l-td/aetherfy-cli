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

// CreateWorkspace creates a new workspace
func (c *Client) CreateWorkspace(req *WorkspaceCreateRequest) (*Workspace, error) {
	var workspace Workspace
	err := c.Post("/workspaces", req, &workspace)
	if err != nil {
		return nil, err
	}
	return &workspace, nil
}

// ListWorkspaces returns all workspaces for the authenticated user
func (c *Client) ListWorkspaces() ([]Workspace, error) {
	var workspaces []Workspace
	err := c.Get("/workspaces", &workspaces)
	if err != nil {
		return nil, err
	}
	return workspaces, nil
}

// GetWorkspace returns a single workspace by name
func (c *Client) GetWorkspace(name string) (*Workspace, error) {
	var workspace Workspace
	err := c.Get(fmt.Sprintf("/workspaces/%s", name), &workspace)
	if err != nil {
		return nil, err
	}
	return &workspace, nil
}

// DeleteWorkspace deletes a workspace by name
func (c *Client) DeleteWorkspace(name string) (*WorkspaceDeleteResponse, error) {
	var result WorkspaceDeleteResponse
	err := c.DeleteWithResult(fmt.Sprintf("/workspaces/%s", name), &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
