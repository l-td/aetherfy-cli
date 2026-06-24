package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aetherfy/cli/internal/api"
	"github.com/aetherfy/cli/internal/archive"
	"github.com/aetherfy/cli/internal/yamldiff"
	"gopkg.in/yaml.v3"
)

// serverYAML is a representative GET /agents/{name}/yaml response — the
// declarative subset serialize_agent_to_yaml produces (no id/status/etc.).
const serverYAML = `# aetherfy.yaml — exported from this agent's current configuration.
name: billing
runtime: python3.11
type: service
memory_mb: 1024
idle_timeout_minutes: 10
keep_alive: true
workspace: invoices
`

// mockYAMLServer serves the agent YAML export at /agents/{name}/yaml.
func mockYAMLServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/agents/billing/yaml") {
			w.Header().Set("Content-Type", "application/yaml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(serverYAML))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":{"code":"AGENT_NOT_FOUND","message":"not found"}}`))
	}))
}

// TestPullFetchesRoundTrippableYAML: the bytes from GetAgentYAML parse as a
// valid aetherfy.yaml (pull -> deploy is well-formed) and carry no
// server-derived fields.
func TestPullFetchesRoundTrippableYAML(t *testing.T) {
	srv := mockYAMLServer(t)
	defer srv.Close()
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	data, err := client.GetAgentYAML("billing")
	if err != nil {
		t.Fatalf("GetAgentYAML: %v", err)
	}

	// Round-trips through the same parser `afy deploy` uses.
	var cfg archive.AetherfyConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("pulled YAML does not parse: %v", err)
	}
	if cfg.Name != "billing" || cfg.Runtime != "python3.11" || cfg.MemoryMB != 1024 {
		t.Errorf("round-trip lost fields: %+v", cfg)
	}

	// No server-derived fields leaked into the export.
	for _, leak := range []string{"status:", "fly_app_name:", "id:", "created_at"} {
		if strings.Contains(string(data), leak) {
			t.Errorf("pulled YAML leaked server field %q", leak)
		}
	}
}

func TestPull404SurfacesError(t *testing.T) {
	srv := mockYAMLServer(t)
	defer srv.Close()
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	if _, err := client.GetAgentYAML("ghost"); err == nil {
		t.Error("expected an error for an unknown agent, got nil")
	}
}

// TestDiffAgainstFetchedServerState exercises the full diff path: fetch the
// server YAML via the client, parse the local file, and classify each field.
func TestDiffAgainstFetchedServerState(t *testing.T) {
	srv := mockYAMLServer(t)
	defer srv.Close()
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	serverBytes, err := client.GetAgentYAML("billing")
	if err != nil {
		t.Fatalf("GetAgentYAML: %v", err)
	}
	var server map[string]interface{}
	if err := yaml.Unmarshal(serverBytes, &server); err != nil {
		t.Fatalf("server YAML: %v", err)
	}

	// Local file: changes memory, omits idle_timeout (preserve), clears
	// workspace, keeps name/runtime the same.
	local := map[string]interface{}{}
	if err := yaml.Unmarshal([]byte(`
name: billing
runtime: python3.11
memory_mb: 512
keep_alive: true
workspace: null
`), &local); err != nil {
		t.Fatalf("local YAML: %v", err)
	}

	diffs := yamldiff.Diff(local, server)
	byKey := map[string]yamldiff.FieldDiff{}
	for _, d := range diffs {
		byKey[d.Key] = d
	}

	if byKey["memory_mb"].Kind != yamldiff.Change {
		t.Errorf("memory_mb want Change, got %v", byKey["memory_mb"].Kind)
	}
	if byKey["idle_timeout_minutes"].Kind != yamldiff.Preserve {
		t.Errorf("idle_timeout_minutes want Preserve, got %v", byKey["idle_timeout_minutes"].Kind)
	}
	if byKey["workspace"].Kind != yamldiff.Clear {
		t.Errorf("workspace want Clear, got %v", byKey["workspace"].Kind)
	}
	if byKey["keep_alive"].Kind != yamldiff.NoOp {
		t.Errorf("keep_alive want NoOp, got %v", byKey["keep_alive"].Kind)
	}
	if !yamldiff.HasChanges(diffs) {
		t.Error("expected changes (memory + workspace), HasChanges=false")
	}
}
