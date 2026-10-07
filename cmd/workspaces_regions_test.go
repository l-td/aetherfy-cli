package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// afy workspaces regions at the HTTP seam runWorkspacesRegions drives, in the
// agents_lifecycle_test.go pattern: the PATCH it sends, and the control
// plane's refusal surfaced with its code. --wait shares pollOperation with
// afy collections regions, whose tests follow an operation to each end.

func TestWorkspacesRegionsPatchesTheWorkspacesRegions(t *testing.T) {
	var method, path string
	var body map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"operation_id":"op-1","status":"pending","from_regions":["us-east-1"],"to_regions":["us-east-1","eu-central-1"]}`))
	}))
	defer srv.Close()

	change, err := api.NewClientWithURL(srv.URL, "afy_test_key").
		UpdateWorkspaceRegions("research", []string{"us-east-1", "eu-central-1"})
	if err != nil {
		t.Fatalf("UpdateWorkspaceRegions: %v", err)
	}
	if method != http.MethodPatch || path != "/workspaces/research/regions" {
		t.Errorf("request: want PATCH /workspaces/research/regions, got %s %s", method, path)
	}
	if want := []string{"us-east-1", "eu-central-1"}; !reflect.DeepEqual(body["regions"], want) {
		t.Errorf("body regions: want %v, got %v", want, body["regions"])
	}
	if change.OperationID != "op-1" || !reflect.DeepEqual(change.ToRegions, []string{"us-east-1", "eu-central-1"}) {
		t.Errorf("change: got %+v", change)
	}
}

func TestWorkspacesRegionsSurfacesTheRefusalsCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":{"code":"WORKSPACE_NARROW_BLOCKED_BY_RESOURCES","message":"1 resource(s) still use a region being removed."}}`))
	}))
	defer srv.Close()

	_, err := api.NewClientWithURL(srv.URL, "afy_test_key").
		UpdateWorkspaceRegions("research", []string{"us-east-1"})
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *api.APIError, got %v", err)
	}
	if apiErr.StatusCode != http.StatusUnprocessableEntity || apiErr.Code != "WORKSPACE_NARROW_BLOCKED_BY_RESOURCES" {
		t.Errorf("want 422 WORKSPACE_NARROW_BLOCKED_BY_RESOURCES, got %d %s", apiErr.StatusCode, apiErr.Code)
	}
	if ExitCode(err) != exitRequestFailed {
		t.Errorf("exit: want %d, got %d", exitRequestFailed, ExitCode(err))
	}
}
