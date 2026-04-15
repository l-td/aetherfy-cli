package test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aetherfy/cli/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper: spin up an httptest server that records the last request path and
// returns the given fixture, then build a Client pointed at it.
func logsTestServer(t *testing.T, fixture []api.LogEntry) (*httptest.Server, *[]string) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

func TestGetAgentLogs_BuildsTailQuery(t *testing.T) {
	srv, paths := logsTestServer(t, []api.LogEntry{})
	client := api.NewClientWithURL(srv.URL, "test-key")

	_, err := client.GetAgentLogs("agent-1", api.LogQuery{Tail: 10})
	require.NoError(t, err)

	assert.Len(t, *paths, 1)
	assert.Equal(t, "/agents/agent-1/logs?tail=10", (*paths)[0])
}

func TestGetAgentLogs_BuildsAllFiltersQuery(t *testing.T) {
	srv, paths := logsTestServer(t, []api.LogEntry{})
	client := api.NewClientWithURL(srv.URL, "test-key")

	_, err := client.GetAgentLogs("agent-1", api.LogQuery{
		Tail:    50,
		Since:   "1h",
		Search:  "boom",
		AfterID: 42,
	})
	require.NoError(t, err)

	// url.Values.Encode sorts keys alphabetically
	assert.Equal(t, "/agents/agent-1/logs?after_id=42&search=boom&since=1h&tail=50", (*paths)[0])
}

func TestGetAgentLogs_NoParamsWhenEmpty(t *testing.T) {
	srv, paths := logsTestServer(t, []api.LogEntry{})
	client := api.NewClientWithURL(srv.URL, "test-key")

	_, err := client.GetAgentLogs("agent-1", api.LogQuery{})
	require.NoError(t, err)
	assert.Equal(t, "/agents/agent-1/logs", (*paths)[0])
}

func TestGetAgentLogs_ParsesLogEntry(t *testing.T) {
	ts, _ := time.Parse(time.RFC3339, "2026-04-15T12:00:00Z")
	fixture := []api.LogEntry{
		{ID: 7, Timestamp: ts, Stream: "stdout", Level: "INFO", Message: "hello"},
		{ID: 8, Timestamp: ts, Stream: "stderr", Level: "ERROR", Message: "oops"},
	}
	srv, _ := logsTestServer(t, fixture)
	client := api.NewClientWithURL(srv.URL, "test-key")

	logs, err := client.GetAgentLogs("agent-1", api.LogQuery{Tail: 10})
	require.NoError(t, err)
	require.Len(t, logs, 2)
	assert.Equal(t, int64(7), logs[0].ID)
	assert.Equal(t, "stdout", logs[0].Stream)
	assert.Equal(t, "INFO", logs[0].Level)
	assert.Equal(t, "hello", logs[0].Message)
	assert.Equal(t, "ERROR", logs[1].Level)
}

func TestGetDeploymentLogs_BackwardsCompatible(t *testing.T) {
	// The original function signature must still work; it's called throughout
	// the CLI and in downstream tests.
	srv, paths := logsTestServer(t, []api.LogEntry{})
	client := api.NewClientWithURL(srv.URL, "test-key")

	_, err := client.GetDeploymentLogs("agent-1", 25)
	require.NoError(t, err)
	assert.Equal(t, "/agents/agent-1/logs?tail=25", (*paths)[0])
}
