package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// GetAgentLogs must translate the LogQuery struct into the query string the
// control plane understands. The level/stream filters are comma-separated
// multi-value params (PR 1).
func TestGetAgentLogs_BuildsQueryString(t *testing.T) {
	cases := []struct {
		name  string
		query LogQuery
		want  map[string]string // expected query params (exact match)
	}{
		{
			name:  "level single",
			query: LogQuery{Level: "ERROR"},
			want:  map[string]string{"level": "ERROR"},
		},
		{
			name:  "level multi-value",
			query: LogQuery{Level: "ERROR,WARN"},
			want:  map[string]string{"level": "ERROR,WARN"},
		},
		{
			name:  "stream filter",
			query: LogQuery{Stream: "stderr"},
			want:  map[string]string{"stream": "stderr"},
		},
		{
			name:  "level and stream combined with tail",
			query: LogQuery{Tail: 100, Level: "ERROR", Stream: "stderr,system"},
			want:  map[string]string{"tail": "100", "level": "ERROR", "stream": "stderr,system"},
		},
		{
			name:  "empty filters omitted",
			query: LogQuery{Tail: 50},
			want:  map[string]string{"tail": "50"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotQuery map[string]string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQuery = map[string]string{}
				for k, v := range r.URL.Query() {
					gotQuery[k] = v[0]
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte("[]"))
			}))
			defer srv.Close()

			client := NewClientWithURL(srv.URL, "afy_test_key")
			if _, err := client.GetAgentLogs("my-agent", tc.query); err != nil {
				t.Fatalf("GetAgentLogs returned error: %v", err)
			}

			if len(gotQuery) != len(tc.want) {
				t.Fatalf("query params = %v, want %v", gotQuery, tc.want)
			}
			for k, want := range tc.want {
				if got := gotQuery[k]; got != want {
					t.Errorf("query[%q] = %q, want %q", k, got, want)
				}
			}
		})
	}
}
