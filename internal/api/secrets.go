package api

import "fmt"

// ListSecrets returns all secrets for an agent (keys only, not values)
func (c *Client) ListSecrets(agentID string) ([]Secret, error) {
	var secrets []Secret
	err := c.Get(fmt.Sprintf("/agents/%s/secrets", agentID), &secrets)
	if err != nil {
		return nil, err
	}
	return secrets, nil
}

// SetSecret creates or updates a secret
func (c *Client) SetSecret(agentID, key, value string) error {
	req := map[string]string{
		"key":   key,
		"value": value,
	}
	var result map[string]interface{}
	return c.Post(fmt.Sprintf("/agents/%s/secrets", agentID), req, &result)
}

// DeleteSecret removes a secret
func (c *Client) DeleteSecret(agentID, key string) error {
	return c.Delete(fmt.Sprintf("/agents/%s/secrets/%s", agentID, key))
}

// Workspace Secrets

// ListWorkspaceSecrets returns all secrets for a workspace (keys only, not values)
func (c *Client) ListWorkspaceSecrets(workspaceName string) ([]Secret, error) {
	var secrets []Secret
	err := c.Get(fmt.Sprintf("/workspaces/%s/secrets", workspaceName), &secrets)
	if err != nil {
		return nil, err
	}
	return secrets, nil
}

// SetWorkspaceSecret creates or updates a workspace secret
func (c *Client) SetWorkspaceSecret(workspaceName, key, value string) error {
	req := map[string]string{
		"key":   key,
		"value": value,
	}
	var result map[string]interface{}
	return c.Post(fmt.Sprintf("/workspaces/%s/secrets", workspaceName), req, &result)
}

// DeleteWorkspaceSecret removes a workspace secret
func (c *Client) DeleteWorkspaceSecret(workspaceName, key string) error {
	return c.Delete(fmt.Sprintf("/workspaces/%s/secrets/%s", workspaceName, key))
}
