package test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aetherfy/cli/internal/api"
)

// captureAuthHeaders runs a GET request through the client and returns
// the headers the server received. Each test uses an isolated mock so
// header pollution between tests is impossible.
func captureAuthHeaders(t *testing.T, apiKey string) http.Header {
	t.Helper()
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, apiKey)
	var result map[string]any
	if err := client.Get("/anything", &result); err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}
	return got
}

// Live (afy_live_) keys MUST send X-Test-DB: 0. Control-plane will
// then look up the key in the prod aetherfy DB only — no fallback to
// test, no silent cross-DB lookup.
func TestAuthHeaders_LiveKeySetsXTestDBZero(t *testing.T) {
	headers := captureAuthHeaders(t, "afy_live_"+repeat("a", 32))
	if got, want := headers.Get("Authorization"), "Bearer afy_live_"+repeat("a", 32); got != want {
		t.Errorf("Authorization: got %q, want %q", got, want)
	}
	if got, want := headers.Get("X-Test-DB"), "0"; got != want {
		t.Errorf("X-Test-DB: got %q, want %q", got, want)
	}
}

// Test (afy_test_) keys MUST send X-Test-DB: 1. Control-plane will
// then look up the key in aetherfy_test only — no fallback to prod.
func TestAuthHeaders_TestKeySetsXTestDBOne(t *testing.T) {
	headers := captureAuthHeaders(t, "afy_test_"+repeat("b", 32))
	if got, want := headers.Get("X-Test-DB"), "1"; got != want {
		t.Errorf("X-Test-DB: got %q, want %q", got, want)
	}
}

// A key without the afy_(live|test)_ prefix (e.g. the literal
// "test-key" used by other CLI test fixtures) defaults to "0". This
// matches the CLI's IsTestKey heuristic — anything that isn't a
// confirmed test key gets treated as prod.
func TestAuthHeaders_UnprefixedKeyDefaultsToProd(t *testing.T) {
	headers := captureAuthHeaders(t, "test-key")
	if got, want := headers.Get("X-Test-DB"), "0"; got != want {
		t.Errorf("X-Test-DB: got %q, want %q", got, want)
	}
}

// Empty key sends NEITHER Authorization NOR X-Test-DB. Sending
// X-Test-DB without a Bearer key would be a no-op against the server
// but signals intent badly — better to send nothing.
func TestAuthHeaders_EmptyKeyOmitsBothHeaders(t *testing.T) {
	headers := captureAuthHeaders(t, "")
	if got := headers.Get("Authorization"); got != "" {
		t.Errorf("Authorization should be unset for empty key, got %q", got)
	}
	if got := headers.Get("X-Test-DB"); got != "" {
		t.Errorf("X-Test-DB should be unset for empty key, got %q", got)
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
