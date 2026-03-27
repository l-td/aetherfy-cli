package test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aetherfy/cli/internal/api"
)

// ---------------------------------------------------------------------------
// isReservedSecretKey — tested via the API layer (the check is in cmd/secrets.go
// so we test it indirectly through the HTTP round-trip; the pure logic is below)
// ---------------------------------------------------------------------------

func isReservedKey(key string) bool {
	return strings.HasPrefix(strings.ToUpper(key), "AETHERFY_")
}

func TestIsReservedSecretKey_BlocksAetherfyPrefix(t *testing.T) {
	cases := []string{
		"AETHERFY_API_KEY",
		"AETHERFY_AGENT_ID",
		"AETHERFY_DATABASE_URL",
		"AETHERFY_INTERNAL_SECRET",
		"aetherfy_lowercase",
		"Aetherfy_Mixed",
	}
	for _, k := range cases {
		if !isReservedKey(k) {
			t.Errorf("expected key %q to be reserved", k)
		}
	}
}

func TestIsReservedSecretKey_AllowsOtherKeys(t *testing.T) {
	cases := []string{
		"OPENAI_API_KEY",
		"DATABASE_URL",
		"MY_AETHERFY_KEY", // contains but does not start with AETHERFY_
		"API_KEY",
		"",
	}
	for _, k := range cases {
		if isReservedKey(k) {
			t.Errorf("expected key %q to be allowed", k)
		}
	}
}

// ---------------------------------------------------------------------------
// API client — SetSecret / SetWorkspaceSecret
// ---------------------------------------------------------------------------

func TestSetSecret_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/agents/agent-1/secrets" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"key": "MY_KEY"})
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	if err := client.SetSecret("agent-1", "MY_KEY", "my-value"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetSecret_ServerRejectsReservedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"detail": "Key 'AETHERFY_API_KEY' is reserved",
		})
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	err := client.SetSecret("agent-1", "AETHERFY_API_KEY", "value")
	if err == nil {
		t.Fatal("expected error for reserved key, got nil")
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("expected *api.APIError, got %T", err)
	}
	if apiErr.StatusCode != 422 {
		t.Errorf("expected 422, got %d", apiErr.StatusCode)
	}
}
