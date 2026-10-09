package api

import (
	"fmt"
	"net/url"
)

// AgentAccess is which workspaces an agent's key reaches on the vector API:
// its own, plus the ones the account owner granted it (the control plane's
// GET /agents/{agent}/access). Changed is set on a change, nil on a read.
type AgentAccess struct {
	Agent                string   `json:"agent"`
	OwnWorkspace         *string  `json:"own_workspace"`
	GrantedWorkspaces    []string `json:"granted_workspaces"`
	WorkspacelessGranted bool     `json:"workspaceless_granted"`
	AllowedWorkspaces    []string `json:"allowed_workspaces"`
	WorkspacelessAllowed bool     `json:"workspaceless_allowed"`
	Changed              *bool    `json:"changed"`
}

func accessPath(agent string) string {
	return fmt.Sprintf("/agents/%s/access", url.PathEscape(agent))
}

// GetAgentAccess reads an agent's workspace access. An agent's own key is
// refused it (403 AUTH_AGENT_KEY_OUT_OF_SCOPE): only an account key may.
func (c *Client) GetAgentAccess(agent string) (*AgentAccess, error) {
	var out AgentAccess
	if err := c.Get(accessPath(agent), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GrantWorkspace grants an agent a workspace of the account. Already granted:
// Changed false. The agent's own workspace: 422 AGENT_WORKSPACE_GRANT_REDUNDANT.
func (c *Client) GrantWorkspace(agent, workspace string) (*AgentAccess, error) {
	var out AgentAccess
	body := map[string]string{"workspace": workspace}
	if err := c.Post(accessPath(agent)+"/workspaces", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeWorkspace revokes a granted workspace. Not granted: Changed false.
func (c *Client) RevokeWorkspace(agent, workspace string) (*AgentAccess, error) {
	var out AgentAccess
	if err := c.DeleteWithResult(accessPath(agent)+"/workspaces/"+url.PathEscape(workspace), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GrantWorkspaceless grants the workspaceless collections, on their own path:
// "workspaceless" is a valid workspace NAME and must not mean two things.
func (c *Client) GrantWorkspaceless(agent string) (*AgentAccess, error) {
	var out AgentAccess
	if err := c.Put(accessPath(agent)+"/workspaceless", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeWorkspaceless revokes the workspaceless collections.
func (c *Client) RevokeWorkspaceless(agent string) (*AgentAccess, error) {
	var out AgentAccess
	if err := c.DeleteWithResult(accessPath(agent)+"/workspaceless", &out); err != nil {
		return nil, err
	}
	return &out, nil
}
