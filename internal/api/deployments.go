package api

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/url"
	"sort"
	"strconv"
)

// Deploy uploads code and triggers a deployment. confirmOverage=true accepts a
// deploy that tips the account into overage (D2 Part 6) — sent as the
// confirm_overage query param so the control-plane skips the 402 confirm gate.
func (c *Client) Deploy(agentID string, zipData []byte, confirmOverage bool) (*DeployResponse, error) {
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
	req := c.http.R().
		SetHeader("Content-Type", writer.FormDataContentType()).
		SetBody(body.Bytes()).
		SetResult(&resp)
	if confirmOverage {
		req = req.SetQueryParam("confirm_overage", "true")
	}
	httpResp, err := req.Post(c.url(fmt.Sprintf("/agents/%s/deploy", agentID)))

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
//
// NEWEST FIRST, BY VERSION, sorted here: every caller reads [0] as the newest
// (afy status's failed deploy, the deployments table, redeploy's active
// release, rollback's history), and the order the server happens to answer
// in is not a contract. Versions are unique per agent; created_at breaks a
// tie, should one ever occur.
func (c *Client) ListDeployments(agentID string) ([]Deployment, error) {
	var deployments []Deployment
	err := c.Get(fmt.Sprintf("/agents/%s/deployments", agentID), &deployments)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(deployments, func(i, j int) bool {
		if deployments[i].Version != deployments[j].Version {
			return deployments[i].Version > deployments[j].Version
		}
		return deployments[i].CreatedAt.After(deployments[j].CreatedAt)
	})
	return deployments, nil
}

// CancelDeployment cancels an in-flight deployment by version.
// Phase 1: only QUEUED deployments are cancellable; BUILDING/DEPLOYING
// return 409 ("not yet cancellable") until cooperative-cancel
// checkpoints land in BuildWorker / DeployWorker. Terminal states
// (active, failed, rolled_back, superseded) return 409 ("nothing to
// cancel"). Returns the FAILED deployment row on success.
func (c *Client) CancelDeployment(agentID string, version int) (*Deployment, error) {
	var resp Deployment
	path := fmt.Sprintf("/agents/%s/deployments/%d/cancel", agentID, version)
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

// Redeploy re-runs a version's BUILD from its stored source archive, injecting
// current secrets. Distinct from Rollback, which re-deploys an already-built
// image and therefore cannot pick up a secret written since that build.
// Returns the new deployment created for the redeploy.
func (c *Client) Redeploy(agentID string, version int) (*RollbackResponse, error) {
	var resp RollbackResponse
	path := fmt.Sprintf("/agents/%s/deployments/%d/redeploy", agentID, version)
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
	page, err := c.GetAgentLogs(agentID, LogQuery{Tail: tail})
	if err != nil {
		return nil, err
	}
	return page.Entries, nil
}

// LogQuery bundles the query parameters the server understands.
type LogQuery struct {
	Tail         int    // default 50, max 1000 when > 0
	Since        string // e.g. "1h", "30m", "45s"
	Search       string // ILIKE match on message
	Level        string // comma-separated bucket(s): INFO,WARN,ERROR,DEBUG,SYSTEM
	Stream       string // comma-separated stream(s): stdout,stderr,system
	AfterID      int64  // forward pagination: return logs with id > AfterID, ASC order
	DeploymentID string // scope to one run's logs (deployment_id query param)
}

// The response headers of a logs read: the plan's retention, and whether the
// request's `since` reached past it.
const (
	headerLogRetentionDays = "X-Log-Retention-Days"
	headerLogSinceClamped  = "X-Log-Since-Clamped"
	logRetentionUnlimited  = "unlimited"
)

// LogPage is one read of an agent's logs: the entries, and how far back the
// caller's plan keeps them. The server never returns a log older than the plan's
// retention, even before it deletes it.
type LogPage struct {
	Entries []LogEntry
	// RetentionDays is the plan's log retention in days; nil = the plan keeps
	// logs without limit.
	RetentionDays *int
	// SinceClamped is true when the request's `since` reached back past the
	// retention and the server cut it to it.
	SinceClamped bool
}

// parseLogRetention reads X-Log-Retention-Days: a number of days, or
// "unlimited" (nil). The control plane always sends it, so an absent or
// unreadable value is an error rather than a guess at the plan.
func parseLogRetention(header string) (*int, error) {
	if header == logRetentionUnlimited {
		return nil, nil
	}
	days, err := strconv.Atoi(header)
	if err != nil || days < 1 {
		return nil, fmt.Errorf("the control plane did not state the plan's log retention (%s: %q)", headerLogRetentionDays, header)
	}
	return &days, nil
}

// GetAgentLogs is the filter-aware variant. When AfterID is set, the server
// returns logs in ASC order so a follower can page forward without gaps.
func (c *Client) GetAgentLogs(agentID string, q LogQuery) (*LogPage, error) {
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
	if q.Level != "" {
		params.Set("level", q.Level)
	}
	if q.Stream != "" {
		params.Set("stream", q.Stream)
	}
	if q.AfterID > 0 {
		params.Set("after_id", fmt.Sprintf("%d", q.AfterID))
	}
	if q.DeploymentID != "" {
		params.Set("deployment_id", q.DeploymentID)
	}
	if encoded := params.Encode(); encoded != "" {
		path = path + "?" + encoded
	}

	var logs []LogEntry
	resp, err := c.http.R().SetResult(&logs).Get(c.url(path))
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if err := c.handleResponse(resp); err != nil {
		return nil, err
	}
	retention, err := parseLogRetention(resp.Header().Get(headerLogRetentionDays))
	if err != nil {
		return nil, err
	}
	return &LogPage{
		Entries:       logs,
		RetentionDays: retention,
		SinceClamped:  resp.Header().Get(headerLogSinceClamped) == "true",
	}, nil
}
