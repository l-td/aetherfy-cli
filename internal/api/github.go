package api

import (
	"fmt"
	"time"
)

// GitHubConnectURL begins the connection and returns the github.com URL the
// user should open, together with the instant that URL stops working. The call
// is authenticated: the server mints a CSRF state token and builds GitHub's
// authorize URL. The user must NOT be sent to the control plane endpoint itself
// — a browser carries no API key there.
//
// The URL is an AUTHORIZE url, not the App's install page. Authorizing always
// redirects back, so an installation that already exists can be attached; the
// install page does nothing at all on an account where the App is already
// installed, which is how an existing installation became unattachable.
//
// The expiry is returned rather than assumed. It is the lifetime of the state
// token the callback will check, so it is the only honest answer to "how long
// can I keep waiting", and holding a second copy of that number here would
// drift from the server's without anything noticing.
func (c *Client) GitHubConnectURL() (string, time.Time, error) {
	var resp GitHubConnectURL
	if err := c.Get("/auth/github", &resp); err != nil {
		return "", time.Time{}, err
	}
	return resp.ConnectURL, resp.ExpiresAt, nil
}

// GitHubStatus returns the current GitHub connection status for the authenticated user.
func (c *Client) GitHubStatus() (*GitHubStatus, error) {
	var status GitHubStatus
	if err := c.Get("/auth/github/status", &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// GitHubRepositories lists the repositories the account's App installations can
// reach — the domain of GitHubLinkAgent's repo argument, unioned across every
// GitHub account connected.
//
// Linking registers a webhook ON the repository, so an installation has to be
// able to reach it. A repository outside this list cannot be linked at all,
// which is why this is the set to show someone rather than a search over
// everything they own.
func (c *Client) GitHubRepositories() (*GitHubRepoList, error) {
	var list GitHubRepoList
	if err := c.Get("/auth/github/repositories", &list); err != nil {
		return nil, err
	}
	return &list, nil
}

// GitHubDisconnect removes EVERY GitHub installation this account holds
// (idempotent — 204 even if not connected).
func (c *Client) GitHubDisconnect() error {
	return c.Delete("/auth/github")
}

// GitHubDisconnectInstallation removes ONE of the account's GitHub
// installations, leaving the others deploying. 404 GITHUB_INSTALLATION_NOT_FOUND
// when the account does not hold it — deliberately not an idempotent 204, so an
// id belonging to somebody else cannot be probed for through this endpoint.
func (c *Client) GitHubDisconnectInstallation(installationID int64) error {
	return c.Delete(fmt.Sprintf("/auth/github/installations/%d", installationID))
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
