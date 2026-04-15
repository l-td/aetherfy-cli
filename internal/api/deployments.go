package api

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/url"
)

// Deploy uploads code and triggers a deployment
func (c *Client) Deploy(agentID string, zipData []byte) (*DeployResponse, error) {
	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add the file - backend expects "code_archive" field with .tar.gz extension
	part, err := writer.CreateFormFile("code_archive", "code.tar.gz")
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := part.Write(zipData); err != nil {
		return nil, fmt.Errorf("failed to write file data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Make the request
	var resp DeployResponse
	httpResp, err := c.http.R().
		SetHeader("Content-Type", writer.FormDataContentType()).
		SetBody(body.Bytes()).
		SetResult(&resp).
		Post(c.url(fmt.Sprintf("/agents/%s/deploy", agentID)))

	if err != nil {
		return nil, fmt.Errorf("deploy request failed: %w", err)
	}

	if httpResp.IsError() {
		return nil, parseAPIError(httpResp)
	}

	return &resp, nil
}

// GetDeployment returns deployment details
func (c *Client) GetDeployment(deploymentID string) (*Deployment, error) {
	var deployment Deployment
	err := c.Get("/deployments/"+deploymentID, &deployment)
	if err != nil {
		return nil, err
	}
	return &deployment, nil
}

// ListDeployments returns all deployments for an agent
func (c *Client) ListDeployments(agentID string) ([]Deployment, error) {
	var deployments []Deployment
	err := c.Get(fmt.Sprintf("/agents/%s/deployments", agentID), &deployments)
	if err != nil {
		return nil, err
	}
	return deployments, nil
}

// Rollback rolls an agent back to a specific deployment version.
// Returns the new deployment created for the rollback.
func (c *Client) Rollback(agentID string, version int) (*RollbackResponse, error) {
	var resp RollbackResponse
	path := fmt.Sprintf("/agents/%s/deployments/%d/rollback", agentID, version)
	httpResp, err := c.http.R().
		SetResult(&resp).
		Post(c.url(path))
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if httpResp.IsError() {
		return nil, parseAPIError(httpResp)
	}
	return &resp, nil
}

// GetDeploymentLogs returns logs for a deployment. Results are DESC by id
// (newest first). Pass tail=0 to omit the tail parameter.
func (c *Client) GetDeploymentLogs(agentID string, tail int) ([]LogEntry, error) {
	return c.GetAgentLogs(agentID, LogQuery{Tail: tail})
}

// LogQuery bundles the query parameters the server understands.
type LogQuery struct {
	Tail    int    // default 50, max 1000 when > 0
	Since   string // e.g. "1h", "30m", "45s"
	Search  string // ILIKE match on message
	AfterID int64  // forward pagination: return logs with id > AfterID, ASC order
}

// GetAgentLogs is the filter-aware variant. When AfterID is set, the server
// returns logs in ASC order so a follower can page forward without gaps.
func (c *Client) GetAgentLogs(agentID string, q LogQuery) ([]LogEntry, error) {
	path := fmt.Sprintf("/agents/%s/logs", agentID)
	params := url.Values{}
	if q.Tail > 0 {
		params.Set("tail", fmt.Sprintf("%d", q.Tail))
	}
	if q.Since != "" {
		params.Set("since", q.Since)
	}
	if q.Search != "" {
		params.Set("search", q.Search)
	}
	if q.AfterID > 0 {
		params.Set("after_id", fmt.Sprintf("%d", q.AfterID))
	}
	if encoded := params.Encode(); encoded != "" {
		path = path + "?" + encoded
	}

	var logs []LogEntry
	if err := c.Get(path, &logs); err != nil {
		return nil, err
	}
	return logs, nil
}
