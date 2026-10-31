package vectors

import (
	"net/http"
	"net/url"
)

// Restore points: getting a collection back, live or deleted, from a copy of it
// (vectordb routes/restorePointsMount.js; docs: vectordb
// backend/docs/COLLECTION_BACKUP_RESTORE.md). A collection's restore points are
// the copy taken when it was deleted and each of its daily copies within the
// plan's days. A point is addressed by the id a list gives (opaque), never by
// name: two deleted collections can have had the same one. The restore is always
// into a NEW collection and is followed with GetRestore. Only a person's key may
// call these; an agent's is refused 403.

// RestoreProgress is a restore as the list and the restore routes describe it.
type RestoreProgress struct {
	ID           string `json:"id"`
	CollectionID string `json:"collection_id"`
	State        string `json:"state"`
	RegionsReady int    `json:"regions_ready"`
	RegionsTotal int    `json:"regions_total"`
	Error        string `json:"error,omitempty"`
}

// DeletedBy is who deleted the collection: a person (the dashboard, or an
// account key), or an agent, by its id.
type DeletedBy struct {
	Kind    string `json:"kind"`
	AgentID string `json:"agent_id,omitempty"`
}

// PointCollection is the collection a restore point is of.
type PointCollection struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Workspace   *string `json:"workspace"`
	WorkspaceID string  `json:"workspace_id"`
	Deleted     bool    `json:"deleted"`
}

// RestorePoint is one copy a collection can be restored from. Kind is
// "deletion" (the copy taken when it was deleted) or "daily" (one day's copy).
// Day, TakenFrom and TakenTo are a daily copy's: a collection's shards are
// copied one after another, so a daily copy is as of the RANGE TakenFrom to
// TakenTo. DeletedBy and ExpiresAt are a deletion copy's.
type RestorePoint struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Collection PointCollection `json:"collection"`
	TakenAt    string          `json:"taken_at"`
	Day        string          `json:"day,omitempty"`
	TakenFrom  string          `json:"taken_from,omitempty"`
	TakenTo    string          `json:"taken_to,omitempty"`
	Points     int64           `json:"points"`
	Bytes      int64           `json:"bytes"`
	Regions    []string        `json:"regions"`
	DeletedBy  *DeletedBy      `json:"deleted_by,omitempty"`
	ExpiresAt  *string         `json:"expires_at,omitempty"`
	// Status is ready, backing_up, unavailable or restoring.
	Status  string           `json:"status"`
	Restore *RestoreProgress `json:"restore"`
}

// RestoreAllowance is what the plan allows: how long a copy taken at a deletion
// is kept, how many days back a daily copy reaches, and what is left of this
// month's restores and volume (one allowance for both kinds of point). A nil
// number is "no limit".
type RestoreAllowance struct {
	RecoveryDays         *int   `json:"recovery_days"`
	DailyDays            int    `json:"daily_days"`
	RestoresPerMonth     *int   `json:"restores_per_month"`
	RestoresUsed         int    `json:"restores_used"`
	RestoresLeft         *int   `json:"restores_left"`
	RestoreBytesPerMonth *int64 `json:"restore_bytes_per_month"`
	RestoreBytesUsed     int64  `json:"restore_bytes_used"`
	RestoreBytesLeft     *int64 `json:"restore_bytes_left"`
	ResetsAt             string `json:"resets_at"`
}

// RestorePoints is the answer of a list: the restore points and the allowance.
// Collection is set when the list is of one collection.
type RestorePoints struct {
	Collection    *PointCollection `json:"collection"`
	RestorePoints []RestorePoint   `json:"restore_points"`
	Allowance     RestoreAllowance `json:"allowance"`
}

// ListCollectionRestorePoints lists the restore points of a live collection of
// the workspace, by its name.
func (c *Client) ListCollectionRestorePoints(collection string) (*RestorePoints, error) {
	var out RestorePoints
	if err := c.do(http.MethodGet, c.collectionPath(collection, "/restore-points"), nil, c.timeout, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListRestorePointsOf lists the restore points of one collection by its id, live
// or deleted: for a deleted one, the copy taken at its deletion and the daily
// copies from before it was deleted.
func (c *Client) ListRestorePointsOf(collectionID string) (*RestorePoints, error) {
	var out RestorePoints
	if err := c.do(http.MethodGet, "restore-points?collection_id="+url.QueryEscape(collectionID), nil, c.timeout, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListDeletedRestorePoints lists every deleted collection of the account that
// can still be restored (or is being backed up or restored), each as the copy
// taken at its deletion, whatever its workspace: the caller filters by workspace,
// as the API's list is the account's.
func (c *Client) ListDeletedRestorePoints() (*RestorePoints, error) {
	var out RestorePoints
	if err := c.do(http.MethodGet, "restore-points?deleted=true", nil, c.timeout, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RestoreStarted is the 202 a restore answers with.
type RestoreStarted struct {
	RestoreID    string   `json:"restore_id"`
	CollectionID string   `json:"collection_id"`
	Name         string   `json:"name"`
	WorkspaceID  string   `json:"workspace_id"`
	Regions      []string `json:"regions"`
	State        string   `json:"state"`
	RegionsReady int      `json:"regions_ready"`
	RegionsTotal int      `json:"regions_total"`
}

// RestoreRestorePoint starts restoring a restore point (by the id a list gave)
// as a new collection; as, when not empty, is its name, else the API picks the
// first free of the collection's old name and the old name with the point's date.
// The restore runs in the background: follow it with GetRestore.
func (c *Client) RestoreRestorePoint(pointID, as string) (*RestoreStarted, error) {
	body := map[string]interface{}{}
	if as != "" {
		body["as"] = as
	}
	var out RestoreStarted
	if err := c.do(http.MethodPost, "restore-points/"+url.PathEscape(pointID)+"/restore", body, c.timeout, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetRestore reads a restore's progress.
func (c *Client) GetRestore(id string) (*RestoreProgress, error) {
	var out RestoreProgress
	if err := c.do(http.MethodGet, "restore-points/restores/"+url.PathEscape(id), nil, c.timeout, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
