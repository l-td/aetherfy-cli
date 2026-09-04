package api

import "fmt"

// GitHubConnectURL begins the App install and returns the github.com URL the
// user should open. The call is authenticated: the server mints a CSRF state
// token and builds the install URL. The user must NOT be sent to the control
// plane endpoint itself — a browser carries no API key there.
func (c *Client) GitHubConnectURL() (string, error) {
	var resp GitHubInstallURL
	if err := c.Get("/auth/github", &resp); err != nil {
		return "", err
	}
	return resp.InstallURL, nil
}

// GitHubStatus returns the current GitHub connection status for the authenticated user.
func (c *Client) GitHubStatus() (*GitHubStatus, error) {
	var status GitHubStatus
	if err := c.Get("/auth/github/status", &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// GitHubDisconnect revokes the stored GitHub OAuth token (idempotent — 204 even if not connected).
func (c *Client) GitHubDisconnect() error {
	return c.Delete("/auth/github")
}

// GitHubLinkAgent links an agent to a GitHub repository and registers a push webhook.
// branch defaults to "main" when empty; rootDir empty means the repository root.
func (c *Client) GitHubLinkAgent(agentID, repo, branch, rootDir string) (*GitHubLinkResponse, error) {
	var resp GitHubLinkResponse
	req := &GitHubLinkRequest{Repo: repo, Branch: branch, RootDir: rootDir}
	if err := c.Post(fmt.Sprintf("/agents/%s/github", agentID), req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GitHubUnlinkAgent removes the GitHub link from an agent and deletes its webhook (idempotent).
func (c *Client) GitHubUnlinkAgent(agentID string) error {
	return c.Delete(fmt.Sprintf("/agents/%s/github", agentID))
}
