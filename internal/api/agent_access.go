package api

import (
	"fmt"
	"net/url"
)

// AgentAccess is which workspaces an agent's key reaches on the vector API,
// the level it holds in each, and whether its own key may write its own
// secrets (the control plane's GET /agents/{agent}/access; the design is
// aetherfy-control-plane docs/AGENT_ACCESS_LEVELS.md). Changed is set on a
// change, nil on a read.
//
// Every workspace comes by name, and again as a ref in the *Refs fields: the
// names for people, the refs for a script that keeps a reference (a
// workspace deleted and recreated under the same name is a different
// workspace, with a different id).
type AgentAccess struct {
	Agent           string       `json:"agent"`
	OwnWorkspace    string       `json:"own_workspace"`
	OwnWorkspaceRef WorkspaceRef `json:"own_workspace_ref"`
	// OwnWorkspaceLevel is the level it holds at home: its own setting
	// (OwnWorkspaceLevelSetting, nil when it follows the workspace), else
	// the workspace's default (WorkspaceDefaultLevel).
	OwnWorkspaceLevel        string                `json:"own_workspace_level"`
	OwnWorkspaceLevelSetting *string               `json:"own_workspace_level_setting"`
	WorkspaceDefaultLevel    string                `json:"workspace_default_level"`
	GrantedWorkspaces        []string              `json:"granted_workspaces"`
	GrantedWorkspaceRefs     []LeveledWorkspaceRef `json:"granted_workspace_refs"`
	AllowedWorkspaces        []string              `json:"allowed_workspaces"`
	AllowedWorkspaceRefs     []LeveledWorkspaceRef `json:"allowed_workspace_refs"`
	SecretsAccess            bool                  `json:"secrets_access"`
	Changed                  *bool                 `json:"changed"`
}

// LeveledWorkspaceRef is a workspace the agent reaches, with its level there:
// read, read_write or full.
type LeveledWorkspaceRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Level string `json:"level"`
}

// AccessLevels are the three levels, lowest first: the control plane's
// shared/agent_grants.py ACCESS_LEVELS.
var AccessLevels = []string{"read", "read_write", "full"}

func accessPath(agent string) string {
	return fmt.Sprintf("/agents/%s/access", url.PathEscape(agent))
}

// GetAgentAccess reads an agent's access. An account key may read any of its
// agents'; an agent's own key may read only its own.
func (c *Client) GetAgentAccess(agent string) (*AgentAccess, error) {
	var out AgentAccess
	if err := c.Get(accessPath(agent), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GrantWorkspace grants an agent a workspace of the account at level ("" =
// not given: a new grant is read, an existing one keeps its level). Already
// so: Changed false. The agent's own workspace: 422
// AGENT_WORKSPACE_GRANT_REDUNDANT.
func (c *Client) GrantWorkspace(agent, workspace, level string) (*AgentAccess, error) {
	var out AgentAccess
	body := map[string]string{"workspace": workspace}
	if level != "" {
		body["level"] = level
	}
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

// SetOwnWorkspaceLevel sets the agent's level in its own workspace; nil
// returns it to the workspace's default.
func (c *Client) SetOwnWorkspaceLevel(agent string, level *string) (*AgentAccess, error) {
	var out AgentAccess
	body := map[string]*string{"level": level}
	if err := c.Put(accessPath(agent)+"/own-workspace", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetSecretsAccess turns the agent's secrets switch on or off.
func (c *Client) SetSecretsAccess(agent string, enabled bool) (*AgentAccess, error) {
	var out AgentAccess
	body := map[string]bool{"enabled": enabled}
	if err := c.Put(accessPath(agent)+"/secrets", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
