package api

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// APIKey is one row of the account's keys, as GET /keys and the approved
// device sign-in give it. NEVER a secret or a hash: key_prefix is the first
// characters of the key and is all the server keeps to show.
type APIKey struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	KeyPrefix       string  `json:"key_prefix"`
	CreatedAt       string  `json:"created_at"`
	LastUsedAt      *string `json:"last_used_at"`
	IsSystemManaged bool    `json:"is_system_managed"`
}

// KeyList is the answer of GET /keys. Raw is the body as the server wrote it:
// `afy keys list --json` prints that, not a re-encoding of the rows.
type KeyList struct {
	Keys []APIKey        `json:"keys"`
	Raw  json.RawMessage `json:"-"`
}

// rawBody receives a JSON body untouched.
type rawBody []byte

func (r *rawBody) UnmarshalJSON(b []byte) error {
	*r = append((*r)[:0], b...)
	return nil
}

// getDocument reads path and returns the body as written, after the checks
// every JSON read gets: an error status is the server's refusal, and a 2xx that
// is not JSON did not come from the API (NotAPIResponseError).
func (c *Client) getDocument(path string) (json.RawMessage, error) {
	var raw rawBody
	if err := c.Get(path, &raw); err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// ListKeys returns the account's API keys.
func (c *Client) ListKeys() (*KeyList, error) {
	raw, err := c.getDocument("/keys")
	if err != nil {
		return nil, err
	}
	list := &KeyList{Raw: raw}
	if err := json.Unmarshal(raw, list); err != nil {
		return nil, requestError(err)
	}
	return list, nil
}

// RevokeKey revokes one key by id. 404 API_KEY_NOT_FOUND when the account has
// no such key.
func (c *Client) RevokeKey(id string) error {
	var result map[string]interface{}
	return c.DeleteWithResult(fmt.Sprintf("/keys/%s", url.PathEscape(id)), &result)
}
