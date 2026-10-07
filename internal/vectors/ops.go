package vectors

import (
	"encoding/json"
	"net/http"
)

// The read and collection-level calls. Each is ONE request with the client's
// timeout, not retried: the CLI prints what the server said and a script
// decides what to do next. Paths and bodies are the SDKs' (aetherfy_vectors/
// client.py get_collections, get_collection, create_collection,
// delete_collection, count, retrieve, search, scroll, delete).

// VectorParams is a collection's vector configuration as vectordb stores it.
type VectorParams struct {
	Size     int    `json:"size"`
	Distance string `json:"distance"`
}

// CollectionInfo is one collection as GET /collections and
// GET /collections/{name} describe it. Fields the list does not carry
// (regions, updated_at) are empty there.
type CollectionInfo struct {
	// ID is the collection's UUID: what the control plane's routes address
	// it by (afy collections regions / move).
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Config      struct {
		Params struct {
			Vectors VectorParams `json:"vectors"`
		} `json:"params"`
	} `json:"config"`
	Status      string   `json:"status"`
	PointsCount int64    `json:"points_count"`
	Regions     []string `json:"regions"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// ListCollections lists the collections in the client's workspace, or the
// workspaceless ones when it has none. The two are separate address spaces.
func (c *Client) ListCollections() ([]CollectionInfo, error) {
	var out struct {
		Collections []CollectionInfo `json:"collections"`
	}
	if err := c.do(http.MethodGet, c.collectionsPath(), nil, c.timeout, &out); err != nil {
		return nil, err
	}
	return out.Collections, nil
}

// GetCollection describes one collection.
func (c *Client) GetCollection(name string) (*CollectionInfo, error) {
	var out struct {
		Result CollectionInfo `json:"result"`
	}
	if err := c.do(http.MethodGet, c.collectionPath(name, ""), nil, c.timeout, &out); err != nil {
		return nil, err
	}
	return &out.Result, nil
}

// CreateCollectionRequest is the body of POST /collections. Regions is sent
// only when given: omitting it lets the server place the collection in the
// full scope, and an explicit empty list is the server's to refuse.
type CreateCollectionRequest struct {
	Name    string       `json:"name"`
	Vectors VectorParams `json:"vectors"`
	Regions []string     `json:"regions,omitempty"`
}

// CreateCollection creates a collection and returns the regions the server
// placed it in (the stored ones, on an idempotent re-create).
func (c *Client) CreateCollection(req CreateCollectionRequest) ([]string, error) {
	var out struct {
		Regions []string `json:"regions"`
	}
	if err := c.do(http.MethodPost, c.collectionsPath(), req, c.timeout, &out); err != nil {
		return nil, err
	}
	return out.Regions, nil
}

// DeleteCollection deletes a collection and its points.
func (c *Client) DeleteCollection(name string) error {
	return c.do(http.MethodDelete, c.collectionPath(name, ""), nil, c.timeout, nil)
}

// CountPoints counts the points matching filter (nil for all), exactly.
func (c *Client) CountPoints(collection string, filter json.RawMessage) (int64, error) {
	body := map[string]interface{}{"exact": true}
	if len(filter) > 0 {
		body["filter"] = filter
	}
	var out struct {
		Result struct {
			Count int64 `json:"count"`
		} `json:"result"`
	}
	if err := c.do(http.MethodPost, c.collectionPath(collection, "/points/count"), body, c.timeout, &out); err != nil {
		return 0, err
	}
	return out.Result.Count, nil
}

// Point is a stored point or a search hit. Score is set on hits only, Vector
// only on a scroll that asked for vectors.
type Point struct {
	ID      json.RawMessage `json:"id"`
	Score   *float64        `json:"score,omitempty"`
	Payload json.RawMessage `json:"payload"`
	Vector  json.RawMessage `json:"vector,omitempty"`
}

// GetPoints retrieves points by id, with their payloads and without vectors.
// An id that does not exist is absent from the answer, not an error.
func (c *Client) GetPoints(collection string, ids []interface{}) ([]Point, error) {
	body := map[string]interface{}{"ids": ids, "with_payload": true, "with_vector": false}
	var out struct {
		Result []Point `json:"result"`
	}
	if err := c.do(http.MethodPost, c.collectionPath(collection, "/points/retrieve"), body, c.timeout, &out); err != nil {
		return nil, err
	}
	return out.Result, nil
}

// Search returns the limit points nearest to vector, filtered by filter (nil
// for none), with their payloads. It is sent as POST .../points/query, Qdrant's
// search route: the API refuses the retired /points/search with 410
// ROUTE_RETIRED. The vector goes in the body as "query", and the matches come
// back under result.points.
func (c *Client) Search(collection string, vector []float64, limit int, filter json.RawMessage) ([]Point, error) {
	body := map[string]interface{}{
		"query":        vector,
		"limit":        limit,
		"offset":       0,
		"with_payload": true,
		"with_vector":  false,
	}
	if len(filter) > 0 {
		body["filter"] = filter
	}
	var out struct {
		Result struct {
			Points []Point `json:"points"`
		} `json:"result"`
	}
	if err := c.do(http.MethodPost, c.collectionPath(collection, "/points/query"), body, c.timeout, &out); err != nil {
		return nil, err
	}
	return out.Result.Points, nil
}

// The page size of a scroll. ScrollLimitMax is the most the vectors API
// returns in one read: vectordb's READ_LIMIT_MAX (backend/utils/
// catchAllAllowlist.js), which refuses a larger limit 400. The default is the
// SDKs' scroll default. aetherfy-e2e-tests tests/pyunit/
// test_index_timeouts_pair.py reads both files and reds if the two differ.
const (
	ScrollLimitDefault = 10
	ScrollLimitMax     = 1000
)

// ScrollPage is one page of a scroll. NextPageOffset is the id the next page
// starts at, JSON null on the last page; it is passed back as Scroll's offset.
type ScrollPage struct {
	Points         []Point         `json:"points"`
	NextPageOffset json.RawMessage `json:"next_page_offset"`
}

// Scroll reads one page of points in id order, filtered by filter (nil for
// none), starting at offset (nil for the first page), with their payloads and,
// when withVectors, their vectors. The SDKs' scroll: POST .../points/scroll.
func (c *Client) Scroll(collection string, limit int, offset interface{}, filter json.RawMessage, withVectors bool) (*ScrollPage, error) {
	body := map[string]interface{}{
		"limit":        limit,
		"with_payload": true,
		"with_vector":  withVectors,
	}
	if offset != nil {
		body["offset"] = offset
	}
	if len(filter) > 0 {
		body["filter"] = filter
	}
	var out struct {
		Result ScrollPage `json:"result"`
	}
	if err := c.do(http.MethodPost, c.collectionPath(collection, "/points/scroll"), body, c.timeout, &out); err != nil {
		return nil, err
	}
	if len(out.Result.NextPageOffset) == 0 {
		out.Result.NextPageOffset = json.RawMessage("null")
	}
	return &out.Result, nil
}

// ExistingPointIDs returns which of ids exist, as the server spells them: a
// retrieve without payloads or vectors.
func (c *Client) ExistingPointIDs(collection string, ids []interface{}) ([]json.RawMessage, error) {
	body := map[string]interface{}{"ids": ids, "with_payload": false, "with_vector": false}
	var out struct {
		Result []Point `json:"result"`
	}
	if err := c.do(http.MethodPost, c.collectionPath(collection, "/points/retrieve"), body, c.timeout, &out); err != nil {
		return nil, err
	}
	found := make([]json.RawMessage, 0, len(out.Result))
	for _, p := range out.Result {
		found = append(found, p.ID)
	}
	return found, nil
}

// DeletePoints deletes points by id, or every point filter matches: exactly
// one of ids and filter is set. The SDKs' delete: POST .../points/delete with
// {"points": ids} or {"filter": filter}. The write is applied when the answer
// comes back (the SDKs' `wait` is accepted and changes nothing).
func (c *Client) DeletePoints(collection string, ids []json.RawMessage, filter json.RawMessage) error {
	body := map[string]interface{}{}
	if len(filter) > 0 {
		body["filter"] = filter
	} else {
		body["points"] = ids
	}
	return c.do(http.MethodPost, c.collectionPath(collection, "/points/delete"), body, c.timeout, nil)
}
