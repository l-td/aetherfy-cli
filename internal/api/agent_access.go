package api

import (
	"fmt"
	"net/url"
)

// AgentAccess is which workspaces an agent's key reaches on the vector API:
// its own, plus the ones the account owner granted it (the control plane's
// GET /agents/{agent}/access). Changed is set on a change, nil on a read.
//
// Every workspace comes by name, and again as an {id, name} pair in the *Ref
// fields: the names for people, the pairs for a script that keeps a
// reference (a workspace deleted and recreated under the same name is a
// different workspace, with a different id).
type AgentAccess struct {
	Agent                string         `json:"agent"`
	OwnWorkspace         string         `json:"own_workspace"`
	OwnWorkspaceRef      WorkspaceRef   `json:"own_workspace_ref"`
	GrantedWorkspaces    []string       `json:"granted_workspaces"`
	GrantedWorkspaceRefs []WorkspaceRef `json:"granted_workspace_refs"`
	AllowedWorkspaces    []string       `json:"allowed_workspaces"`
	AllowedWorkspaceRefs []WorkspaceRef `json:"allowed_workspace_refs"`
	// ConnectionsWorkspaces: the granted workspaces whose grant also covers
	// connections (the grant's connections switch is on), by name and as pairs.
	ConnectionsWorkspaces    []string       `json:"connections_workspaces"`
	ConnectionsWorkspaceRefs []WorkspaceRef `json:"connections_workspace_refs"`
	Changed                  *bool          `json:"changed"`
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

// SetConnectionsSwitch turns a grant's connections switch on or off: may the
// agent's key also use the granted workspace's connections. Off when a grant is
// made. A workspace the agent is not granted: 404 AGENT_WORKSPACE_NOT_GRANTED.
func (c *Client) SetConnectionsSwitch(agent, workspace string, enabled bool) (*AgentAccess, error) {
	var out AgentAccess
	body := map[string]bool{"enabled": enabled}
	if err := c.Put(accessPath(agent)+"/workspaces/"+url.PathEscape(workspace)+"/connections", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
