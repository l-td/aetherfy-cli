package vectors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// PutSchema's If-Match: sent, quoted as an ETag, when a version is named, and
// absent when not. vectordb strips the quotes (routes/schema.js), so either
// spelling matches; the quoted one is the header's own form.
func TestPutSchemaSendsIfMatchOnlyWhenAVersionIsNamed(t *testing.T) {
	for _, tc := range []struct {
		ifMatch, want string
	}{{"v1", `"v1"`}, {"", ""}} {
		var got string
		var path string
		var body map[string]interface{}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, path = r.Header.Get("If-Match"), r.URL.EscapedPath()
			_ = json.NewDecoder(r.Body).Decode(&body)
			_, _ = w.Write([]byte(`{"success":true,"etag":"v2"}`))
		}))
		c := New(srv.URL, "k", "research")
		etag, err := c.PutSchema("articles", SchemaWrite{Schema: json.RawMessage(`{"fields":{}}`)}, tc.ifMatch)
		srv.Close()
		if err != nil || etag != "v2" {
			t.Fatalf("PutSchema(%q) = %q, %v", tc.ifMatch, etag, err)
		}
		if got != tc.want {
			t.Errorf("If-Match = %q, want %q", got, tc.want)
		}
		if path != "/api/v1/workspaces/research/collections/articles/schema" {
			t.Errorf("path = %s", path)
		}
		if _, sent := body["description"]; sent {
			t.Errorf("a nil description was sent: %v", body)
		}
	}
}
