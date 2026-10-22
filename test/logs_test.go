package test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper: spin up an httptest server that records the last request path and
// returns the given fixture, then build a Client pointed at it.
func logsTestServer(t *testing.T, fixture []api.LogEntry) (*httptest.Server, *[]string) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		w.Header().Set("X-Log-Retention-Days", "30")
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

	page, err := client.GetAgentLogs("agent-1", api.LogQuery{Tail: 10})
	require.NoError(t, err)
	logs := page.Entries
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

// THE PLAN'S LOG WINDOW. The control plane never returns a log older than the
// plan's retention and says how far back that is in X-Log-Retention-Days (a
// number of days, or "unlimited"), and in X-Log-Since-Clamped when the request's
// `since` reached past it.
func logsWithHeaders(t *testing.T, headers map[string]string) *api.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(srv.Close)
	return api.NewClientWithURL(srv.URL, "test-key")
}

func TestGetAgentLogs_ReadsThePlansRetention(t *testing.T) {
	page, err := logsWithHeaders(t, map[string]string{"X-Log-Retention-Days": "90"}).GetAgentLogs("agent-1", api.LogQuery{})
	require.NoError(t, err)
	require.NotNil(t, page.RetentionDays)
	assert.Equal(t, 90, *page.RetentionDays)
	assert.False(t, page.SinceClamped)
}

func TestGetAgentLogs_NegotiatedIsNoRetention(t *testing.T) {
	page, err := logsWithHeaders(t, map[string]string{"X-Log-Retention-Days": "negotiated"}).GetAgentLogs("agent-1", api.LogQuery{})
	require.NoError(t, err)
	assert.Nil(t, page.RetentionDays)
}

func TestGetAgentLogs_ReadsWhetherSinceWasCut(t *testing.T) {
	page, err := logsWithHeaders(t, map[string]string{"X-Log-Retention-Days": "7", "X-Log-Since-Clamped": "true"}).
		GetAgentLogs("agent-1", api.LogQuery{Since: "30d"})
	require.NoError(t, err)
	assert.True(t, page.SinceClamped)
}

func TestGetAgentLogs_ARetentionTheControlPlaneDidNotStateIsAnError(t *testing.T) {
	for name, headers := range map[string]map[string]string{
		"absent":   {},
		"garbage":  {"X-Log-Retention-Days": "a week"},
		"zero":     {"X-Log-Retention-Days": "0"},
		"negative": {"X-Log-Retention-Days": "-3"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := logsWithHeaders(t, headers).GetAgentLogs("agent-1", api.LogQuery{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "X-Log-Retention-Days")
		})
	}
}

func TestGetAgentLogs_ReadsTheStoredBytesCapAndTheSizeCapMark(t *testing.T) {
	page, err := logsWithHeaders(t, map[string]string{
		"X-Log-Retention-Days":         "14",
		"X-Log-Max-Bytes-Stored":       "262144000",
		"X-Log-Cap-Rolled-Out-Through": "2026-10-20T10:00:00+00:00",
	}).GetAgentLogs("agent-1", api.LogQuery{})
	require.NoError(t, err)
	require.NotNil(t, page.MaxBytesStored)
	assert.Equal(t, int64(250*1024*1024), *page.MaxBytesStored)
	require.NotNil(t, page.CapRolledOutThrough)
	assert.Equal(t, 2026, page.CapRolledOutThrough.Year())
}

func TestGetAgentLogs_ANegotiatedCapAndNoMarkAreNil(t *testing.T) {
	page, err := logsWithHeaders(t, map[string]string{
		"X-Log-Retention-Days": "14", "X-Log-Max-Bytes-Stored": "negotiated",
	}).GetAgentLogs("agent-1", api.LogQuery{})
	require.NoError(t, err)
	assert.Nil(t, page.MaxBytesStored)
	assert.Nil(t, page.CapRolledOutThrough)
}

func TestGetAgentLogs_AMarkThatIsNotATimeIsAnInvalidResponse(t *testing.T) {
	_, err := logsWithHeaders(t, map[string]string{
		"X-Log-Retention-Days": "14", "X-Log-Cap-Rolled-Out-Through": "yesterday",
	}).GetAgentLogs("agent-1", api.LogQuery{})
	var invalid *api.ResponseInvalidError
	require.ErrorAs(t, err, &invalid)
}

func TestGetAgentLogs_AnUnreadableRetentionIsAnInvalidResponse(t *testing.T) {
	_, err := logsWithHeaders(t, map[string]string{"X-Log-Retention-Days": "a week"}).GetAgentLogs("agent-1", api.LogQuery{})
	var invalid *api.ResponseInvalidError
	require.ErrorAs(t, err, &invalid)
}
