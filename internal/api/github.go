package api

import "fmt"

// GitHubConnectURL returns the URL the user should open in a browser to connect GitHub.
// The server generates a CSRF state token on GET, so the user must visit this URL directly.
func (c *Client) GitHubConnectURL() string {
	return c.baseURL + "/auth/github"
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
