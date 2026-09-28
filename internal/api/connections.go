package api

import (
	"fmt"
	"net/url"
)

// ConnectionTarget is where a connection lives: one agent (by UUID or name)
// or one workspace. Exactly one field is set; there is no account-wide
// connection.
type ConnectionTarget struct {
	Agent     string
	Workspace string
}

// path is the collection route for the target.
func (t ConnectionTarget) path() string {
	if t.Workspace != "" {
		return fmt.Sprintf("/workspaces/%s/connections", url.PathEscape(t.Workspace))
	}
	return fmt.Sprintf("/agents/%s/connections", url.PathEscape(t.Agent))
}

// ConnectionProviders lists the services a connection can be made to.
func (c *Client) ConnectionProviders() ([]ConnectionProvider, error) {
	var providers []ConnectionProvider
	if err := c.Get("/connections/providers", &providers); err != nil {
		return nil, err
	}
	return providers, nil
}

// ListConnections lists every connection on the account.
func (c *Client) ListConnections() ([]Connection, error) {
	var conns []Connection
	if err := c.Get("/connections", &conns); err != nil {
		return nil, err
	}
	return conns, nil
}

// ListTargetConnections lists what a target holds. For an agent that is its
// own connections followed by its workspace's (each carries its Scope); for
// a workspace, the workspace's.
func (c *Client) ListTargetConnections(target ConnectionTarget) ([]Connection, error) {
	var conns []Connection
	if err := c.Get(target.path(), &conns); err != nil {
		return nil, err
	}
	return conns, nil
}

// BeginConnection starts a connection and returns the provider's consent URL.
// The user opens it in a browser; the CLI never handles the grant itself.
func (c *Client) BeginConnection(target ConnectionTarget, req ConnectionCreateRequest) (*ConnectionConnectURL, error) {
	var resp ConnectionConnectURL
	if err := c.Post(target.path(), &req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteConnection removes one of the target's own connections. The server
// asks the provider to revoke the grant first and reports whether it confirmed.
func (c *Client) DeleteConnection(target ConnectionTarget, name string) (*ConnectionDeleted, error) {
	var resp ConnectionDeleted
	if err := c.DeleteWithResult(fmt.Sprintf("%s/%s", target.path(), url.PathEscape(name)), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
