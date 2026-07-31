package test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aetherfy/cli/internal/api"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

func TestGitHubStatusType(t *testing.T) {
	now := time.Now()
	installationID := int64(12345678)
	s := &api.GitHubStatus{
		Connected:      true,
		InstallationID: &installationID,
		ConnectedAt:    &now,
	}
	if !s.Connected {
		t.Error("expected Connected to be true")
	}
	if s.InstallationID == nil || *s.InstallationID != 12345678 {
		t.Errorf("unexpected installation_id: %v", s.InstallationID)
	}
	if s.ConnectedAt == nil {
		t.Error("expected ConnectedAt to be set")
	}
}

func TestGitHubStatusNotConnected(t *testing.T) {
	s := &api.GitHubStatus{Connected: false}
	if s.Connected {
		t.Error("expected Connected to be false")
	}
	if s.ConnectedAt != nil {
		t.Error("expected ConnectedAt to be nil when not connected")
	}
}

func TestGitHubLinkRequest(t *testing.T) {
	req := &api.GitHubLinkRequest{Repo: "myorg/myrepo", Branch: "develop", RootDir: "agents/bot"}
	if req.Repo != "myorg/myrepo" {
		t.Errorf("unexpected repo: %s", req.Repo)
	}
	if req.Branch != "develop" {
		t.Errorf("unexpected branch: %s", req.Branch)
	}
	if req.RootDir != "agents/bot" {
		t.Errorf("unexpected root_dir: %s", req.RootDir)
	}
}

func TestGitHubLinkRequestBranchOptional(t *testing.T) {
	req := &api.GitHubLinkRequest{Repo: "myorg/myrepo"}
	if req.Branch != "" {
		t.Errorf("expected branch to be empty, got: %s", req.Branch)
	}
	if req.RootDir != "" {
		t.Errorf("expected root_dir to be empty, got: %s", req.RootDir)
	}
}

// An omitted root_dir must not appear in the JSON at all — the server treats
// its absence as "the repository root", and sending "" would be a needless
// third spelling of the same thing.
func TestGitHubLinkRequestOmitsEmptyRootDir(t *testing.T) {
	body, err := json.Marshal(&api.GitHubLinkRequest{Repo: "myorg/myrepo", Branch: "main"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(string(body), "root_dir") {
		t.Errorf("root_dir should be omitted when empty, got: %s", body)
	}
}

func TestGitHubLinkResponse(t *testing.T) {
	resp := &api.GitHubLinkResponse{
		Repo:          "myorg/myrepo",
		Branch:        "main",
		RootDir:       "agents/bot",
		WebhookID:     "123456789",
		WebhookSecret: "cafebabe",
	}
	if resp.Repo != "myorg/myrepo" {
		t.Errorf("unexpected repo: %s", resp.Repo)
	}
	if resp.WebhookID == "" {
		t.Error("expected WebhookID to be set")
	}
	if resp.RootDir != "agents/bot" {
		t.Errorf("unexpected root_dir: %s", resp.RootDir)
	}
	// The secret is returned once, at link time, and `afy github link` prints
	// it. Dropping it from the struct silently broke that promise.
	if resp.WebhookSecret != "cafebabe" {
		t.Errorf("unexpected webhook_secret: %s", resp.WebhookSecret)
	}
}

// ---------------------------------------------------------------------------
// API client — GitHub methods (against a local httptest server)
// ---------------------------------------------------------------------------

func TestGitHubConnectURL(t *testing.T) {
	client := api.NewClientWithURL("https://agents.aetherfy.com/api/v1", "test-key")
	url := client.GitHubConnectURL()
	if url != "https://agents.aetherfy.com/api/v1/auth/github" {
		t.Errorf("unexpected connect URL: %s", url)
	}
}

func TestGitHubStatus_Connected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/github/status" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"connected":       true,
			"installation_id": 12345678,
		})
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	status, err := client.GitHubStatus()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Connected {
		t.Error("expected Connected to be true")
	}
	if status.InstallationID == nil || *status.InstallationID != 12345678 {
		t.Errorf("unexpected installation_id: %v", status.InstallationID)
	}
}

func TestGitHubStatus_NotConnected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"connected": false})
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	status, err := client.GitHubStatus()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Connected {
		t.Error("expected Connected to be false")
	}
}

func TestGitHubDisconnect_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/auth/github" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	if err := client.GitHubDisconnect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGitHubLinkAgent_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/agents/agent-1/github" {
			http.NotFound(w, r)
			return
		}
		var sent api.GitHubLinkRequest
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"repo":           "myorg/myrepo",
			"branch":         "main",
			"root_dir":       sent.RootDir,
			"webhook_id":     "987654321",
			"webhook_secret": "cafebabe",
		})
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	resp, err := client.GitHubLinkAgent("agent-1", "myorg/myrepo", "main", "agents/bot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Repo != "myorg/myrepo" {
		t.Errorf("unexpected repo: %s", resp.Repo)
	}
	if resp.Branch != "main" {
		t.Errorf("unexpected branch: %s", resp.Branch)
	}
	if resp.WebhookID != "987654321" {
		t.Errorf("unexpected webhook_id: %s", resp.WebhookID)
	}
	// root_dir must survive the round trip — the server echoes back what it
	// stored, which is what the agent will actually build from.
	if resp.RootDir != "agents/bot" {
		t.Errorf("unexpected root_dir: %s", resp.RootDir)
	}
	if resp.WebhookSecret != "cafebabe" {
		t.Errorf("unexpected webhook_secret: %s", resp.WebhookSecret)
	}
}

func TestGitHubLinkAgent_NotConnected_Returns422(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"detail": "GitHub account not connected",
		})
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	_, err := client.GitHubLinkAgent("agent-1", "myorg/myrepo", "main", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("expected *api.APIError, got %T", err)
	}
	if apiErr.StatusCode != 422 {
		t.Errorf("expected status 422, got %d", apiErr.StatusCode)
	}
}

// A rejected --root-dir is ALSO a 422, but from pydantic, not from the
// not-connected gate. The code is what tells them apart — `github link` keys
// its "run afy github connect" hint on it, so a user who typed a bad path is
// not told to reconnect an account that is already fine.
func TestGitHubLinkAgent_BadRootDir_Returns422WithValidationCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"detail": map[string]interface{}{
				"code":    "VALIDATION_ERROR",
				"message": "Value error, Invalid root_dir '../../etc': must not contain a '..' segment.",
			},
		})
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	_, err := client.GitHubLinkAgent("agent-1", "myorg/myrepo", "main", "../../etc")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("expected *api.APIError, got %T", err)
	}
	if apiErr.StatusCode != 422 {
		t.Errorf("expected status 422, got %d", apiErr.StatusCode)
	}
	if apiErr.Code != "VALIDATION_ERROR" {
		t.Errorf("expected code VALIDATION_ERROR, got %q", apiErr.Code)
	}
	if !strings.Contains(apiErr.Message, "root_dir") {
		t.Errorf("expected the message to name root_dir, got %q", apiErr.Message)
	}
}

func TestGitHubUnlinkAgent_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/agents/agent-1/github" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	if err := client.GitHubUnlinkAgent("agent-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGitHubUnlinkAgent_NotLinked_Idempotent(t *testing.T) {
	// Server always returns 204 even if not linked
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "test-key")
	if err := client.GitHubUnlinkAgent("never-linked-agent"); err != nil {
		t.Fatalf("expected idempotent 204, got error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// --from-github repo@ref format validation (regex used in deploy.go)
// ---------------------------------------------------------------------------

var fromGithubPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(@\S+)?$`)

func TestFromGithubPattern_ValidRepoOnly(t *testing.T) {
	cases := []string{
		"owner/repo",
		"my-org/my-repo",
		"Org123/Repo.Name",
		"a/b",
	}
	for _, c := range cases {
		if !fromGithubPattern.MatchString(c) {
			t.Errorf("expected %q to match --from-github pattern", c)
		}
	}
}

func TestFromGithubPattern_ValidRepoWithRef(t *testing.T) {
	cases := []string{
		"owner/repo@main",
		"owner/repo@v1.2.3",
		"owner/repo@" + "a" + "bcdef1234567890abcdef1234567890abcdef123", // 40-char SHA
		"myorg/mylib@feature/my-branch",
	}
	for _, c := range cases {
		if !fromGithubPattern.MatchString(c) {
			t.Errorf("expected %q to match --from-github pattern", c)
		}
	}
}

func TestFromGithubPattern_Invalid(t *testing.T) {
	cases := []string{
		"notavalidrepo",       // no slash
		"owner/",              // empty repo name
		"/repo",               // empty owner
		"owner/repo name",     // space in repo name
		"https://github.com/owner/repo", // full URL not accepted
		"",
	}
	for _, c := range cases {
		if fromGithubPattern.MatchString(c) {
			t.Errorf("expected %q NOT to match --from-github pattern", c)
		}
	}
}

// isHexString duplicate (mirrors cmd/deploy.go unexported helper — tested here for safety)
func isHexString(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func TestIsHexString_ValidSHA(t *testing.T) {
	sha := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	if len(sha) != 40 {
		t.Fatalf("test SHA must be 40 chars")
	}
	if !isHexString(sha) {
		t.Errorf("expected valid 40-char hex SHA to pass")
	}
}

func TestIsHexString_BranchName(t *testing.T) {
	if isHexString("main") {
		t.Errorf("'main' should not be hex (contains 'm')")
	}
}

func TestIsHexString_Empty(t *testing.T) {
	// Empty string: all-chars loop never fires → true (vacuously)
	// This matches Go's standard: strings.Map(hex, "") == ""
	// The caller checks len == 40 before calling isHexString, so this is safe.
	if !isHexString("") {
		t.Errorf("empty string should return true (vacuously hex)")
	}
}
