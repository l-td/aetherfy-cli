package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// These drive `afy connections connect` and `disconnect` through their run
// functions against an httptest control plane, the way github_connect_test.go
// drives `afy github connect`. What is pinned is which requests the flow makes
// and what "connected" has to mean: a connection with this name, in this
// scope, newer than the one there before.

type connServer struct {
	mu        sync.Mutex
	begins    []map[string]interface{}
	beginPath string
	lists     int
	listings  []interface{} // one per list call; the last repeats
	expiresAt time.Time
	deleted   string
	revoked   bool
}

func (s *connServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/connections"):
			body, _ := io.ReadAll(r.Body)
			var req map[string]interface{}
			_ = json.Unmarshal(body, &req)
			s.begins = append(s.begins, req)
			s.beginPath = r.URL.Path
			name, _ := req["name"].(string)
			if name == "" {
				name, _ = req["provider"].(string)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"connect_url": "https://accounts.google.com/o/oauth2/v2/auth?state=t.abc",
				"expires_at":  s.expiresAt.Format(time.RFC3339Nano),
				"name":        name,
			})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/connections"):
			s.lists++
			idx := s.lists - 1
			if idx >= len(s.listings) {
				idx = len(s.listings) - 1
			}
			_ = json.NewEncoder(w).Encode(s.listings[idx])
		case r.Method == http.MethodDelete:
			s.deleted = r.URL.Path
			parts := strings.Split(r.URL.Path, "/")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"name": parts[len(parts)-1], "provider_revoked": s.revoked,
			})
		default:
			http.NotFound(w, r)
		}
	}
}

func conn(name, scope, connectedAt string) map[string]interface{} {
	c := map[string]interface{}{
		"name": name, "provider": "google", "scope": scope, "account_label": "you@example.com",
		"scopes": []string{"openid"}, "status": "connected", "connected_at": connectedAt,
	}
	if scope == "agent" {
		c["agent"] = map[string]interface{}{"id": "a-1", "name": "reporter"}
	} else {
		c["workspace"] = "team"
	}
	return c
}

func list(items ...map[string]interface{}) []map[string]interface{} {
	if items == nil {
		return []map[string]interface{}{}
	}
	return items
}

func stubConnectionsBrowser(t *testing.T) *int {
	t.Helper()
	opened := 0
	prev := connectionsOpenBrowser
	connectionsOpenBrowser = func(string) error { opened++; return nil }
	t.Cleanup(func() { connectionsOpenBrowser = prev })
	return &opened
}

func TestConnectWaitsForTheNewConnection(t *testing.T) {
	s := &connServer{
		// baseline (read after begin), two empty polls, then the grant.
		listings:  []interface{}{list(), list(), list(), list(conn("google", "agent", "2026-09-28T12:00:00Z"))},
		expiresAt: time.Now().Add(5 * time.Second),
	}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()
	opened := stubConnectionsBrowser(t)

	code := runConnectionConnect(api.NewClientWithURL(srv.URL, "afy_test_key"),
		api.ConnectionTarget{Agent: "reporter"},
		api.ConnectionCreateRequest{Provider: "google", Scopes: []string{"https://www.googleapis.com/auth/drive.file"}},
		false, time.Millisecond)

	if code != connectOK {
		t.Fatalf("exit code: want %d, got %d", connectOK, code)
	}
	if len(s.begins) != 1 || s.beginPath != "/agents/reporter/connections" {
		t.Fatalf("begin: want one POST to /agents/reporter/connections, got %d to %q", len(s.begins), s.beginPath)
	}
	if got := s.begins[0]["scopes"]; got == nil {
		t.Errorf("the --scope values were not sent: %v", s.begins[0])
	}
	if _, sent := s.begins[0]["name"]; sent {
		t.Errorf("an unset --name must be left to the server, got %v", s.begins[0]["name"])
	}
	if *opened != 1 {
		t.Errorf("browser opened %d time(s), want 1", *opened)
	}
}

func TestReconnectNeedsANewerGrantNotJustTheOldOne(t *testing.T) {
	old := conn("google", "agent", "2026-09-28T12:00:00Z")
	s := &connServer{
		listings: []interface{}{
			list(old), list(old), list(old),
			list(conn("google", "agent", "2026-09-28T12:05:00Z")),
		},
		expiresAt: time.Now().Add(5 * time.Second),
	}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()
	stubConnectionsBrowser(t)

	code := runConnectionConnect(api.NewClientWithURL(srv.URL, "k"), api.ConnectionTarget{Agent: "reporter"},
		api.ConnectionCreateRequest{Provider: "google"}, false, time.Millisecond)
	if code != connectOK {
		t.Fatalf("exit code: want %d, got %d", connectOK, code)
	}
	if s.lists < 4 {
		t.Errorf("reported success after %d list call(s): the unchanged grant counted as new", s.lists)
	}
}

// CONTROL: a grant that never changes must end in expiry, never in success.
func TestAnUnchangedGrantExpiresRatherThanSucceeds(t *testing.T) {
	old := conn("google", "agent", "2026-09-28T12:00:00Z")
	s := &connServer{listings: []interface{}{list(old)}, expiresAt: time.Now().Add(200 * time.Millisecond)}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()
	stubConnectionsBrowser(t)

	code := runConnectionConnect(api.NewClientWithURL(srv.URL, "k"), api.ConnectionTarget{Agent: "reporter"},
		api.ConnectionCreateRequest{Provider: "google"}, true, time.Millisecond)
	if code != connectFailed {
		t.Fatalf("exit code: want %d (expired), got %d", connectFailed, code)
	}
}

// An agent's list also carries its workspace's connections. A workspace grant
// with the same name is not the agent connection this command started.
func TestAWorkspaceConnectionDoesNotSatisfyAnAgentConnect(t *testing.T) {
	s := &connServer{
		listings:  []interface{}{list(), list(conn("google", "workspace", "2026-09-28T12:00:00Z"))},
		expiresAt: time.Now().Add(200 * time.Millisecond),
	}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()
	stubConnectionsBrowser(t)

	code := runConnectionConnect(api.NewClientWithURL(srv.URL, "k"), api.ConnectionTarget{Agent: "reporter"},
		api.ConnectionCreateRequest{Provider: "google"}, true, time.Millisecond)
	if code != connectFailed {
		t.Fatalf("a workspace grant was read as the agent's: exit %d", code)
	}
}

func TestNoBrowserPrintsTheURLAndOpensNothing(t *testing.T) {
	s := &connServer{
		listings:  []interface{}{list(), list(conn("notes", "workspace", "2026-09-28T12:00:00Z"))},
		expiresAt: time.Now().Add(5 * time.Second),
	}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()
	opened := stubConnectionsBrowser(t)

	var code int
	out := captureStdout(t, func() {
		code = runConnectionConnect(api.NewClientWithURL(srv.URL, "k"), api.ConnectionTarget{Workspace: "team"},
			api.ConnectionCreateRequest{Provider: "notion", Name: "notes"}, true, time.Millisecond)
	})
	if code != connectOK || *opened != 0 {
		t.Fatalf("exit %d, browser opened %d time(s); want 0 and 0", code, *opened)
	}
	if s.beginPath != "/workspaces/team/connections" {
		t.Errorf("begin path: %q", s.beginPath)
	}
	if !strings.Contains(out, "https://accounts.google.com/o/oauth2/v2/auth?state=t.abc") {
		t.Errorf("the consent URL was not printed:\n%s", out)
	}
}

func TestDisconnectReportsAConfirmedRevoke(t *testing.T) {
	s := &connServer{revoked: true}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()

	var code int
	out := captureStdout(t, func() {
		code = runConnectionDisconnect(api.NewClientWithURL(srv.URL, "k"), api.ConnectionTarget{Agent: "reporter"}, "google")
	})
	if code != connectOK || s.deleted != "/agents/reporter/connections/google" {
		t.Fatalf("exit %d, DELETE %q", code, s.deleted)
	}
	if !strings.Contains(out, "confirmed the access is revoked") {
		t.Errorf("a confirmed revoke was not reported:\n%s", out)
	}
}

func TestDisconnectNeverClaimsARevokeThatWasNotConfirmed(t *testing.T) {
	s := &connServer{revoked: false}
	srv := httptest.NewServer(s.handler(t))
	defer srv.Close()

	out := captureStdout(t, func() {
		runConnectionDisconnect(api.NewClientWithURL(srv.URL, "k"), api.ConnectionTarget{Workspace: "team"}, "notes")
	})
	if strings.Contains(out, "confirmed the access is revoked") {
		t.Errorf("claimed a revoke the provider did not confirm:\n%s", out)
	}
	if !strings.Contains(out, "did not confirm a revoke") {
		t.Errorf("the unconfirmed revoke was not said:\n%s", out)
	}
}

func TestOptionalScopesAreTheAllowListMinusTheDefaults(t *testing.T) {
	got := optionalScopes(api.ConnectionProvider{
		DefaultScopes: []string{"openid"},
		AllowedScopes: []string{"openid", "drive.file", "calendar.readonly"},
	})
	if strings.Join(got, ",") != "drive.file,calendar.readonly" {
		t.Errorf("optional scopes: %v", got)
	}
}
