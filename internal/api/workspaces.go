package api

import (
	"fmt"
	"net/url"
	"time"
)

// WorkspaceAgent is one agent that LIVES in a workspace, as the workspace's
// agent list answers it (the control plane's WorkspaceAgentResponse): a summary,
// narrower than an Agent. It has its own struct so that `afy workspaces agents
// --json` prints what the server sent and nothing it did not.
type WorkspaceAgent struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	AgentType      string  `json:"agent_type"`
	Status         string  `json:"status"`
	Deployed       bool    `json:"deployed"`
	Awake          bool    `json:"awake"`
	URL            *string `json:"url"`
	IsDegraded     bool    `json:"is_degraded"`
	RegionsTotal   int     `json:"regions_total"`
	RegionsReady   int     `json:"regions_ready"`
	DegradedReason *string `json:"degraded_reason"`
	FailureCode    *string `json:"failure_code"`
	FailureMessage *string `json:"failure_message"`
	// AccessLevel is the agent's level in its own workspace: its own setting
	// (AccessLevelSetting) or, when that is null, the workspace's default.
	AccessLevel        *string   `json:"access_level"`
	AccessLevelSetting *string   `json:"access_level_setting"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// ListWorkspaceAgents returns the agents that LIVE in a workspace.
func (c *Client) ListWorkspaceAgents(workspaceName string) ([]WorkspaceAgent, error) {
	var agents []WorkspaceAgent
	err := c.Get("/workspaces/"+url.PathEscape(workspaceName)+"/agents", &agents)
	if err != nil {
		return nil, err
	}
	return agents, nil
}

// GrantedAgent is an agent of another workspace of the account that has been
// granted access to this one (contract sheet 2.12). A grant gives access; it
// does not move the agent.
type GrantedAgent struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	GrantedAt time.Time `json:"granted_at"`
	// Level is the agent's level in this workspace, by its grant.
	Level string `json:"level"`
}

// ListWorkspaceGrantedAgents returns the agents granted access to a workspace,
// ordered by name (GET /workspaces/{workspace_name}/granted-agents). An unknown
// workspace is 404 WORKSPACE_NOT_FOUND, like the living list.
func (c *Client) ListWorkspaceGrantedAgents(workspaceName string) ([]GrantedAgent, error) {
	var granted []GrantedAgent
	err := c.Get("/workspaces/"+url.PathEscape(workspaceName)+"/granted-agents", &granted)
	if err != nil {
		return nil, err
	}
	return granted, nil
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

// UpdateWorkspace updates a workspace's mutable fields. Only description
// is supported by the control plane; name is structurally immutable.
func (c *Client) UpdateWorkspace(name string, req *WorkspaceUpdateRequest) (*Workspace, error) {
	var workspace Workspace
	err := c.Patch(fmt.Sprintf("/workspaces/%s", name), req, &workspace)
	if err != nil {
		return nil, err
	}
	return &workspace, nil
}
