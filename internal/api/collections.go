package api

import "fmt"

// The control plane's collection and region-change routes. A collection is
// addressed here by its UUID (the vectors API's GET /collections/{name}
// answers it as "id"); a workspace by its name.

// RegionsChangeLimits is where a copying regions change falls in the
// account's rolling-window limits (the control plane's
// shared/region_change_limits.py). A nil limit is no limit.
type RegionsChangeLimits struct {
	Counted    bool `json:"counted"`
	WindowDays int  `json:"window_days"`
	Count      struct {
		Limit      *int `json:"limit"`
		Used       int  `json:"used"`
		ThisChange *int `json:"this_change"`
	} `json:"count"`
	Size struct {
		LimitBytes     *int64 `json:"limit_bytes"`
		UsedBytes      int64  `json:"used_bytes"`
		LeftAfterBytes *int64 `json:"left_after_bytes"`
	} `json:"size"`
}

// RegionsChangePreview is what a regions change would do, built by the same
// planner the change runs (POST /collections/{id}/regions/preview). Refusal
// is the answer the change would get -- its "status" plus the error envelope
// -- or nil.
type RegionsChangePreview struct {
	CollectionID    string                 `json:"collection_id"`
	FromRegions     []string               `json:"from_regions"`
	ToRegions       []string               `json:"to_regions"`
	RegionsToAdd    []string               `json:"regions_to_add"`
	RegionsToRemove []string               `json:"regions_to_remove"`
	NoOp            bool                   `json:"no_op"`
	CopyBytes       int64                  `json:"copy_bytes"`
	DataDeletedFrom []string               `json:"data_deleted_from"`
	Limits          *RegionsChangeLimits   `json:"limits"`
	Refusal         map[string]interface{} `json:"refusal"`
}

// RefusalError is the refusal a preview reports, as the error the change
// itself would have returned.
func (p *RegionsChangePreview) RefusalError() *APIError {
	if p.Refusal == nil {
		return nil
	}
	e := &APIError{Message: "refused"}
	if s, ok := p.Refusal["status"].(float64); ok {
		e.StatusCode = int(s)
	}
	if c, ok := p.Refusal["code"].(string); ok {
		e.Code = c
	}
	if m, ok := p.Refusal["message"].(string); ok && m != "" {
		e.Message = m
	}
	return e
}

// CollectionChange is PATCH /collections/{id}'s answer: 200 with NoOp, or
// 202 with the operation that carries it out.
type CollectionChange struct {
	OperationID       string                `json:"operation_id"`
	CollectionID      string                `json:"collection_id"`
	NoOp              bool                  `json:"no_op"`
	Regions           []string              `json:"regions"`
	RegionsToAdd      []string              `json:"regions_to_add"`
	RegionsToRemove   []string              `json:"regions_to_remove"`
	SourceWorkspaceID *string               `json:"source_workspace_id"`
	TargetWorkspaceID *string               `json:"target_workspace_id"`
	Plan              *RegionsChangePreview `json:"plan"`
}

// WorkspaceRegionsChange is PATCH /workspaces/{name}/regions's answer:
// Status "no_op" with CurrentRegions, or 202 with the operation.
type WorkspaceRegionsChange struct {
	OperationID     string   `json:"operation_id"`
	Status          string   `json:"status"`
	CurrentRegions  []string `json:"current_regions"`
	FromRegions     []string `json:"from_regions"`
	ToRegions       []string `json:"to_regions"`
	RegionsToAdd    []string `json:"regions_to_add"`
	RegionsToRemove []string `json:"regions_to_remove"`
}

// Operation is a region change or collection move, polled by its id.
// Status is pending, in_progress, succeeded or failed.
type Operation struct {
	OperationID     string   `json:"operation_id"`
	Scope           string   `json:"scope"`
	Status          string   `json:"status"`
	RegionsToAdd    []string `json:"regions_to_add"`
	RegionsToRemove []string `json:"regions_to_remove"`
	StartedAt       *string  `json:"started_at"`
	CompletedAt     *string  `json:"completed_at"`
	Failures        []struct {
		Message string `json:"message"`
	} `json:"failures"`
}

// Done reports whether the operation has reached a terminal state.
func (o *Operation) Done() bool { return o.Status == "succeeded" || o.Status == "failed" }

// PreviewCollectionRegions asks what changing collection id's regions to
// regions would do, without doing it.
func (c *Client) PreviewCollectionRegions(id string, regions []string) (*RegionsChangePreview, error) {
	var p RegionsChangePreview
	if err := c.Post(fmt.Sprintf("/collections/%s/regions/preview", id), map[string]interface{}{"regions": regions}, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ChangeCollectionRegions changes collection id's regions.
func (c *Client) ChangeCollectionRegions(id string, regions []string) (*CollectionChange, error) {
	var r CollectionChange
	if err := c.Patch(fmt.Sprintf("/collections/%s", id), map[string]interface{}{"regions": regions}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// MoveCollection moves collection id to workspaceID, or out of any workspace
// when it is nil.
func (c *Client) MoveCollection(id string, workspaceID *string) (*CollectionChange, error) {
	var r CollectionChange
	if err := c.Patch(fmt.Sprintf("/collections/%s", id), map[string]interface{}{"workspace_id": workspaceID}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateWorkspaceRegions changes workspace name's regions.
func (c *Client) UpdateWorkspaceRegions(name string, regions []string) (*WorkspaceRegionsChange, error) {
	var r WorkspaceRegionsChange
	if err := c.Patch(fmt.Sprintf("/workspaces/%s/regions", name), map[string]interface{}{"regions": regions}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// WorkspaceRef is a workspace as the control plane pairs it: its id, for a
// client that keeps a reference, and its name, for people.
type WorkspaceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CollectionOperation is the change running on one collection (the control
// plane's GET /collections/{id}/operation): a "regions_change" of its
// placement regions, or a "move" to another workspace (TargetWorkspace; nil
// when it moves out of any).
type CollectionOperation struct {
	OperationID     *string       `json:"operation_id"`
	Kind            string        `json:"kind"`
	Status          string        `json:"status"`
	FromRegions     []string      `json:"from_regions"`
	ToRegions       []string      `json:"to_regions"`
	TargetWorkspace *WorkspaceRef `json:"target_workspace"`
	AcceptedAt      string        `json:"accepted_at"`
}

// GetCollectionOperation reads the change running on a collection, by its id,
// or nil when none runs. It is the same in-flight change the control plane's
// 409 names, so a client that did not start it can still see it.
func (c *Client) GetCollectionOperation(collectionID string) (*CollectionOperation, error) {
	var out struct {
		Operation *CollectionOperation `json:"operation"`
	}
	if err := c.Get(fmt.Sprintf("/collections/%s/operation", collectionID), &out); err != nil {
		return nil, err
	}
	return out.Operation, nil
}

// RegionChangeUsage is the account's region changes that copy data, against
// the plan's limits over its rolling window (GET /users/me/region-changes):
// the same window and limits a change is judged against. A nil limit is no
// limit.
type RegionChangeUsage struct {
	WindowDays     int     `json:"window_days"`
	CountUsed      int     `json:"count_used"`
	CountLimit     *int    `json:"count_limit"`
	SizeUsedBytes  int64   `json:"size_used_bytes"`
	SizeLimitBytes *int64  `json:"size_limit_bytes"`
	FreesAt        *string `json:"frees_at"`
}

// GetRegionChangeUsage reads the account's region-change window.
func (c *Client) GetRegionChangeUsage() (*RegionChangeUsage, error) {
	var out RegionChangeUsage
	if err := c.Get("/users/me/region-changes", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetOperation reads an operation's state.
func (c *Client) GetOperation(id string) (*Operation, error) {
	var o Operation
	if err := c.Get(fmt.Sprintf("/operations/%s", id), &o); err != nil {
		return nil, err
	}
	return &o, nil
}
