package api

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// Deploy uploads code and triggers a deployment. confirmOverage=true accepts a
// deploy that tips the account into overage (D2 Part 6) — sent as the
// confirm_overage query param so the control-plane skips the 402 confirm gate.
// codeRoot is the agent's folder inside the archive when the archive is rooted at the repository because
// the agent's aetherfy.yaml includes shared paths (the route's `code_root` field); "" sends no field: the
// archive root is the agent's folder.
func (c *Client) Deploy(agentID string, zipData []byte, codeRoot string, confirmOverage bool) (*DeployResponse, error) {
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
	if codeRoot != "" {
		if err := writer.WriteField("code_root", codeRoot); err != nil {
			return nil, fmt.Errorf("failed to write code_root: %w", err)
		}
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

// maxDeploymentsPageLimit is the most deployments the control plane answers in
// one page (it clamps a larger limit to this).
const maxDeploymentsPageLimit = 100

// DeploymentsQuery bundles the query parameters of the deployment list. Since
// is N followed by s, m, h or d (30m, 24h, 7d); Status is a deployment state;
// Limit is the page size (0 = the server's default); BeforeID is the previous
// page's NextBeforeID, sent as it was received.
type DeploymentsQuery struct {
	Since    string
	Status   string
	BeforeID string
	Limit    int
}

// DeploymentsPage is one page of an agent's deployments, newest first.
// NextBeforeID is the id to pass as BeforeID for the next, older page; nil
// when there are no older deployments.
type DeploymentsPage struct {
	Deployments  []Deployment `json:"deployments"`
	NextBeforeID *string      `json:"next_before_id"`
}

// sortNewestFirst orders deployments NEWEST FIRST, BY VERSION: every caller
// reads [0] as the newest (afy status's failed deploy, the deployments table,
// redeploy's active release, rollback's history), and the order the server
// happens to answer in is not a contract. Versions are unique per agent;
// created_at breaks a tie, should one ever occur.
func sortNewestFirst(deployments []Deployment) {
	sort.SliceStable(deployments, func(i, j int) bool {
		if deployments[i].Version != deployments[j].Version {
			return deployments[i].Version > deployments[j].Version
		}
		return deployments[i].CreatedAt.After(deployments[j].CreatedAt)
	})
}

// ListDeploymentsPage reads one page of an agent's deployments (GET
// /agents/{id}/deployments), newest first, filtered and positioned by q.
func (c *Client) ListDeploymentsPage(agentID string, q DeploymentsQuery) (*DeploymentsPage, error) {
	path := fmt.Sprintf("/agents/%s/deployments", agentID)
	params := url.Values{}
	if q.Since != "" {
		params.Set("since", q.Since)
	}
	if q.Status != "" {
		params.Set("status", q.Status)
	}
	if q.Limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", q.Limit))
	}
	if q.BeforeID != "" {
		params.Set("before_id", q.BeforeID)
	}
	if encoded := params.Encode(); encoded != "" {
		path = path + "?" + encoded
	}

	var page DeploymentsPage
	if err := c.Get(path, &page); err != nil {
		return nil, err
	}
	sortNewestFirst(page.Deployments)
	return &page, nil
}

// ListDeployments returns ALL deployments for an agent, newest first: it
// follows the pages of ListDeploymentsPage (the largest page each time) until
// the server says there are no older ones. A server that answers a cursor it
// has already given is refused rather than followed forever.
func (c *Client) ListDeployments(agentID string) ([]Deployment, error) {
	var all []Deployment
	beforeID := ""
	asked := map[string]bool{"": true}
	for {
		page, err := c.ListDeploymentsPage(agentID, DeploymentsQuery{Limit: maxDeploymentsPageLimit, BeforeID: beforeID})
		if err != nil {
			return nil, err
		}
		all = append(all, page.Deployments...)
		if page.NextBeforeID == nil {
			break
		}
		next := *page.NextBeforeID
		if asked[next] {
			return nil, &ResponseInvalidError{Message: fmt.Sprintf(
				"the control plane's deployment list pointed to the same next page twice (%q), so the list was not followed further", next)}
		}
		asked[next] = true
		beforeID = next
	}
	sortNewestFirst(all)
	return all, nil
}

// CancelDeployment cancels a RELEASE that has not finished, by version. A queued
// release becomes failed at once; a building or deploying one is asked to stop
// and its worker ends it at the next checkpoint (the row comes back still in
// that state, with CancellationRequested set); a release that has ended is
// refused with a 409. A RUN is cancelled with CancelRun.
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

// CancelRun cancels a RUN by its id (POST /deployments/{run}/cancel): a run waiting
// in the line or not yet started is cancelled now, a running one is stopped and
// ends cancelled, and one that had already ended answers 200 with outcome
// "already_finished" (not an error). A release id is refused by the server with
// 409, the answer saying to cancel it by its agent.
func (c *Client) CancelRun(runID string) (*RunCancelResponse, error) {
	var resp RunCancelResponse
	err := c.Post("/deployments/"+url.PathEscape(runID)+"/cancel", nil, &resp)
	if err != nil {
		return nil, err
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

// The response headers of a logs read: what bounds the account's logs on its plan.
const (
	headerLogRetentionDays = "X-Log-Retention-Days"
	headerLogMaxBytes      = "X-Log-Max-Bytes-Stored"
	headerLogSinceClamped  = "X-Log-Since-Clamped"
	headerLogCapRolledOut  = "X-Log-Cap-Rolled-Out-Through"
	logBoundNegotiated     = "negotiated"
)

// LogPage is one read of an agent's logs: the entries, and what bounds the
// caller's logs on its plan. The server never returns a log older than the
// plan's retention, even before it deletes it.
type LogPage struct {
	Entries []LogEntry
	// RetentionDays is the plan's log retention in days; nil = negotiated per
	// contract.
	RetentionDays *int
	// MaxBytesStored is the most log bytes an account keeps on the plan; nil =
	// negotiated per contract.
	MaxBytesStored *int64
	// SinceClamped is true when the request's `since` reached back past the
	// retention and the server cut it to it.
	SinceClamped bool
	// CapRolledOutThrough is set when the plan's size cap has removed lines the
	// retention would still have kept, and this read reaches before them: the
	// newest timestamp it removed. nil otherwise.
	CapRolledOutThrough *time.Time
}

// parseLogRetention reads X-Log-Retention-Days: a number of days, or
// "negotiated" (nil). The control plane always sends it, so an absent or
// unreadable value is a ResponseInvalidError rather than a guess at the plan.
func parseLogRetention(header string) (*int, error) {
	if header == logBoundNegotiated {
		return nil, nil
	}
	days, err := strconv.Atoi(header)
	if err != nil || days < 1 {
		return nil, &ResponseInvalidError{Message: fmt.Sprintf(
			"the control plane did not state the plan's log retention (%s: %q)", headerLogRetentionDays, header)}
	}
	return &days, nil
}

// parseLogMaxBytes reads X-Log-Max-Bytes-Stored: a number of bytes, or
// "negotiated" (nil). Only the size cap's note uses it, so a value that cannot be
// read is not a failure: the note then says the cap without its size.
func parseLogMaxBytes(header string) *int64 {
	n, err := strconv.ParseInt(header, 10, 64)
	if err != nil || n < 1 {
		return nil
	}
	return &n
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
	page := &LogPage{
		Entries:        logs,
		RetentionDays:  retention,
		MaxBytesStored: parseLogMaxBytes(resp.Header().Get(headerLogMaxBytes)),
		SinceClamped:   resp.Header().Get(headerLogSinceClamped) == "true",
	}
	if raw := resp.Header().Get(headerLogCapRolledOut); raw != "" {
		through, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, &ResponseInvalidError{Message: fmt.Sprintf(
				"the control plane sent a size-cap mark that is not a time (%s: %q)", headerLogCapRolledOut, raw)}
		}
		page.CapRolledOutThrough = &through
	}
	return page, nil
}
