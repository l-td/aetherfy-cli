package vectors

import (
	"encoding/json"
	"net/http"
)

// A collection's payload schema: vectordb routes/schema.js, mounted under the
// collection (contract sheet 1.5). The schema itself is opaque here: the CLI
// passes it through as written and prints it as the server answers.
//
// The version is the etag. A write that names one (If-Match) is refused 412
// SCHEMA_VERSION_MISMATCH when the schema changed since that read, and nothing
// changes; a write without one overwrites whatever is there.

// Schema is GET …/schema's answer, and the file `afy collections schema get`
// writes for `set` to read back: Etag is the version it was read at.
type Schema struct {
	Schema          json.RawMessage `json:"schema"`
	EnforcementMode string          `json:"enforcement_mode,omitempty"`
	Description     *string         `json:"description"`
	Etag            string          `json:"etag,omitempty"`
}

// SchemaWrite is PUT …/schema's body. An empty EnforcementMode is left to the
// server's default ("off"); a nil Description is not sent.
type SchemaWrite struct {
	Schema          json.RawMessage `json:"schema"`
	EnforcementMode string          `json:"enforcement_mode,omitempty"`
	Description     *string         `json:"description,omitempty"`
}

// SchemaFieldAnalysis is one payload field as the analyzer saw it in the
// sample (vectordb services/schemaAnalyzer.js:177-201): the share of points
// carrying it, the share of each type among them, its warnings, the first
// value seen of each type, and, for an object, the same analysis of its own
// fields. ElementTypes is sent only for an array field.
type SchemaFieldAnalysis struct {
	Presence     float64                        `json:"presence"`
	Types        map[string]float64             `json:"types"`
	ElementTypes map[string]float64             `json:"element_types,omitempty"`
	Warnings     []string                       `json:"warnings"`
	Samples      map[string]json.RawMessage     `json:"samples,omitempty"`
	Nested       map[string]SchemaFieldAnalysis `json:"nested,omitempty"`
}

// SchemaAnalysis is POST …/schema/analyze's answer.
type SchemaAnalysis struct {
	SampleSize      int64                          `json:"sample_size"`
	TotalPoints     int64                          `json:"total_points"`
	Fields          map[string]SchemaFieldAnalysis `json:"fields"`
	SuggestedSchema json.RawMessage                `json:"suggested_schema"`
}

// GetSchema reads a collection's schema and the version it is at.
func (c *Client) GetSchema(collection string) (*Schema, error) {
	var out Schema
	if err := c.do(http.MethodGet, c.collectionPath(collection, "/schema"), nil, c.timeout, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutSchema writes a collection's schema and returns its new version. With
// ifMatch, the write applies only if the schema is still at that version
// (or there is none yet); with "", it overwrites.
func (c *Client) PutSchema(collection string, req SchemaWrite, ifMatch string) (string, error) {
	var extra http.Header
	if ifMatch != "" {
		extra = http.Header{"If-Match": {`"` + ifMatch + `"`}}
	}
	var out struct {
		Etag string `json:"etag"`
	}
	if err := c.doWith(http.MethodPut, c.collectionPath(collection, "/schema"), req, extra, c.timeout, &out); err != nil {
		return "", err
	}
	return out.Etag, nil
}

// DeleteSchema removes a collection's schema.
func (c *Client) DeleteSchema(collection string) error {
	return c.do(http.MethodDelete, c.collectionPath(collection, "/schema"), nil, c.timeout, nil)
}

// AnalyzeSchema samples the collection's points and suggests a schema.
// sampleSize 0 leaves the size to the server's default; its range is the
// server's to refuse.
func (c *Client) AnalyzeSchema(collection string, sampleSize int) (*SchemaAnalysis, error) {
	body := map[string]interface{}{}
	if sampleSize != 0 {
		body["sample_size"] = sampleSize
	}
	var out SchemaAnalysis
	if err := c.do(http.MethodPost, c.collectionPath(collection, "/schema/analyze"), body, c.timeout, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
