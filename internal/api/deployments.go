package api

import (
	"bytes"
	"fmt"
	"mime/multipart"
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

// GetDeploymentLogs returns logs for a deployment
func (c *Client) GetDeploymentLogs(agentID string, tail int) ([]LogEntry, error) {
	path := fmt.Sprintf("/agents/%s/logs", agentID)
	if tail > 0 {
		path = fmt.Sprintf("%s?tail=%d", path, tail)
	}

	var logs []LogEntry
	err := c.Get(path, &logs)
	if err != nil {
		return nil, err
	}
	return logs, nil
}
