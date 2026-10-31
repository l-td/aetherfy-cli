package vectors

import (
	"net/http"
	"net/url"
)

// Recovering deleted collections: GET /deleted-collections, POST
// /deleted-collections/{id}/restore and GET /deleted-collections/restores/{id}
// (vectordb routes/deletedCollections.js; docs: vectordb
// backend/docs/COLLECTION_RECOVERY.md). A deleted collection is addressed by
// the id the list gives, never by name: two deleted collections can have had
// the same one. Only a person's key may call these; an agent's is refused 403.

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

// DeletedCollection is one entry of the deleted-collections list.
type DeletedCollection struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Workspace   *string   `json:"workspace"`
	WorkspaceID string    `json:"workspace_id"`
	Regions     []string  `json:"regions"`
	Points      int64     `json:"points"`
	Bytes       int64     `json:"bytes"`
	DeletedAt   string    `json:"deleted_at"`
	DeletedBy   DeletedBy `json:"deleted_by"`
	ExpiresAt   *string   `json:"expires_at"`
	// Status is ready, backing_up, unavailable or restoring.
	Status  string           `json:"status"`
	Restore *RestoreProgress `json:"restore"`
}

// RecoveryAllowance is what the plan allows: how long a deleted collection
// can be restored, and what is left of this month's restores and volume. A
// nil number is "no limit".
type RecoveryAllowance struct {
	RecoveryDays         *int   `json:"recovery_days"`
	RestoresPerMonth     *int   `json:"restores_per_month"`
	RestoresUsed         int    `json:"restores_used"`
	RestoresLeft         *int   `json:"restores_left"`
	RestoreBytesPerMonth *int64 `json:"restore_bytes_per_month"`
	RestoreBytesUsed     int64  `json:"restore_bytes_used"`
	RestoreBytesLeft     *int64 `json:"restore_bytes_left"`
	ResetsAt             string `json:"resets_at"`
}

// DeletedCollections is the answer of the list.
type DeletedCollections struct {
	DeletedCollections []DeletedCollection `json:"deleted_collections"`
	Allowance          RecoveryAllowance   `json:"allowance"`
}

// ListDeletedCollections lists every deleted collection of the account that
// can still be restored (or is being backed up or restored), whatever its
// workspace: the caller filters by workspace, as the API's list is the account's.
func (c *Client) ListDeletedCollections() (*DeletedCollections, error) {
	var out DeletedCollections
	if err := c.do(http.MethodGet, "deleted-collections", nil, c.timeout, &out); err != nil {
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

// RestoreDeletedCollection starts restoring a deleted collection (by the id
// the list gave) as a new collection; as, when not empty, is its name. The
// restore runs in the background: follow it with GetRestore.
func (c *Client) RestoreDeletedCollection(id, as string) (*RestoreStarted, error) {
	body := map[string]interface{}{}
	if as != "" {
		body["as"] = as
	}
	var out RestoreStarted
	if err := c.do(http.MethodPost, "deleted-collections/"+url.PathEscape(id)+"/restore", body, c.timeout, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetRestore reads a restore's progress.
func (c *Client) GetRestore(id string) (*RestoreProgress, error) {
	var out RestoreProgress
	if err := c.do(http.MethodGet, "deleted-collections/restores/"+url.PathEscape(id), nil, c.timeout, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Restoring a LIVE collection to one of the last days from the daily backup:
// GET /workspaces/{ws}/collections/{name}/backups and POST .../backups/restore
// (vectordb routes/collectionBackups.js; docs: vectordb
// backend/docs/COLLECTION_BACKUP_RESTORE.md). The restore is always into a NEW
// collection and is followed with GetRestore, as a deleted collection's. Only a
// person's key may call these; an agent's is refused 403.

// CollectionBackup is one restorable day of a collection: when its shards were
// taken (first to last, since they are copied one after another), its points
// and its bytes.
type CollectionBackup struct {
	Day       string `json:"day"`
	TakenFrom string `json:"taken_from"`
	TakenTo   string `json:"taken_to"`
	Points    int64  `json:"points"`
	Bytes     int64  `json:"bytes"`
}

// CollectionBackups is the answer of the list: the days, how far back the plan
// goes, and what is left of this month's restores.
type CollectionBackups struct {
	CollectionID string             `json:"collection_id"`
	DaysBack     int                `json:"days_back"`
	Backups      []CollectionBackup `json:"backups"`
	Allowance    RecoveryAllowance  `json:"allowance"`
}

// ListCollectionBackups lists the days a collection of the workspace can be
// restored from.
func (c *Client) ListCollectionBackups(collection string) (*CollectionBackups, error) {
	var out CollectionBackups
	if err := c.do(http.MethodGet, c.collectionPath(collection, "/backups"), nil, c.timeout, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RestoreCollectionBackup starts restoring one day (YYYY-MM-DD) of a collection
// into a NEW collection called as. The restore runs in the background: follow it
// with GetRestore.
func (c *Client) RestoreCollectionBackup(collection, day, as string) (*RestoreStarted, error) {
	body := map[string]interface{}{"day": day, "as": as}
	var out RestoreStarted
	if err := c.do(http.MethodPost, c.collectionPath(collection, "/backups/restore"), body, c.timeout, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
