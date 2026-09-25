package vectors

import (
	"encoding/json"
	"net/http"
)

// The read and collection-level calls. Each is ONE request with the client's
// timeout, not retried: the CLI prints what the server said and a script
// decides what to do next. Paths and bodies are the SDKs' (aetherfy_vectors/
// client.py get_collections, get_collection, create_collection,
// delete_collection, count, retrieve, search).

// VectorParams is a collection's vector configuration as vectordb stores it.
type VectorParams struct {
	Size     int    `json:"size"`
	Distance string `json:"distance"`
}

// CollectionInfo is one collection as GET /collections and
// GET /collections/{name} describe it. Fields the list does not carry
// (regions, updated_at) are empty there.
type CollectionInfo struct {
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

// Point is a stored point or a search hit. Score is set on hits only.
type Point struct {
	ID      json.RawMessage `json:"id"`
	Score   *float64        `json:"score,omitempty"`
	Payload json.RawMessage `json:"payload"`
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
// for none), with their payloads.
func (c *Client) Search(collection string, vector []float64, limit int, filter json.RawMessage) ([]Point, error) {
	body := map[string]interface{}{
		"vector":       vector,
		"limit":        limit,
		"offset":       0,
		"with_payload": true,
		"with_vector":  false,
	}
	if len(filter) > 0 {
		body["filter"] = filter
	}
	var out struct {
		Result []Point `json:"result"`
	}
	if err := c.do(http.MethodPost, c.collectionPath(collection, "/points/search"), body, c.timeout, &out); err != nil {
		return nil, err
	}
	return out.Result, nil
}
