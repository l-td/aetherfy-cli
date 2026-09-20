package cmd

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// `afy github disconnect [account]` removes ONE connected GitHub account, or
// all of them.
//
// WHY THE ARGUMENT IS A NAME AND NOT AN ID. An Aetherfy account holds several
// installations now, so disconnect has to say WHICH — and an installation id is
// an eight-digit number nobody carries around. The account login is what
// `afy github status` prints and what every repository is named after, so it is
// what a person has to hand.

func _inst(id int64, login, kind string) api.GitHubInstallation {
	return api.GitHubInstallation{
		InstallationID: id,
		AccountID:      id,
		AccountLogin:   login,
		AccountType:    kind,
	}
}

func TestFindGitHubInstallationPicksTheNamedAccount(t *testing.T) {
	installations := []api.GitHubInstallation{
		_inst(1, "l-td", "User"),
		_inst(2, "acme-corp", "Organization"),
		_inst(3, "third-org", "Organization"),
	}

	got := findGitHubInstallation(installations, "acme-corp")
	if got == nil {
		t.Fatal("named account did not resolve")
	}
	if got.InstallationID != 2 {
		t.Errorf("resolved to installation %d, want 2 — disconnecting the wrong "+
			"account stops agents the user did not choose", got.InstallationID)
	}
}

// GitHub logins are case-insensitive. Refusing `Acme-Corp` would be refusing a
// name we printed ourselves.
func TestFindGitHubInstallationIsCaseInsensitive(t *testing.T) {
	installations := []api.GitHubInstallation{_inst(2, "acme-corp", "Organization")}

	for _, typed := range []string{"acme-corp", "Acme-Corp", "ACME-CORP"} {
		if got := findGitHubInstallation(installations, typed); got == nil {
			t.Errorf("%q did not resolve", typed)
		}
	}
}

// THE NEGATIVE CONTROL. Without it the two tests above would pass on a resolver
// that returned the first installation whatever it was handed — and that
// resolver would disconnect a stranger's account on a typo.
func TestFindGitHubInstallationRefusesAnUnknownAccount(t *testing.T) {
	installations := []api.GitHubInstallation{
		_inst(1, "l-td", "User"),
		_inst(2, "acme-corp", "Organization"),
	}

	if got := findGitHubInstallation(installations, "not-connected"); got != nil {
		t.Errorf("a name nobody holds resolved to installation %d; the command "+
			"must list what IS connected instead of guessing", got.InstallationID)
	}
	if got := findGitHubInstallation(nil, "l-td"); got != nil {
		t.Errorf("an empty list resolved to %+v", got)
	}
	// A PREFIX IS NOT A MATCH. "acme" is a different GitHub account from
	// "acme-corp", and a resolver using strings.Contains would happily stop the
	// wrong one.
	if got := findGitHubInstallation(installations, "acme"); got != nil {
		t.Errorf("a prefix matched %q — the comparison must be whole-string",
			got.AccountLogin)
	}
}

// The client addresses the per-installation endpoint, not the remove-all one.
// Getting this wrong disconnects every GitHub account when the user named one.
func TestGitHubDisconnectInstallationCallsItsOwnEndpoint(t *testing.T) {
	var mu sync.Mutex
	var gotMethod, gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotMethod, gotPath = r.Method, r.URL.Path
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "afy_test_key")
	if err := client.GitHubDisconnectInstallation(162752125); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotMethod != http.MethodDelete {
		t.Errorf("method: want DELETE, got %s", gotMethod)
	}
	if gotPath != "/auth/github/installations/162752125" {
		t.Errorf("path: want /auth/github/installations/162752125, got %s — "+
			"the remove-all route would disconnect every account", gotPath)
	}
}

// The no-argument form still removes everything, and still goes to the
// account-level route. Both forms exist on purpose: the CLI keeps remove-all
// for `afy github disconnect`, which is the only caller of it.
func TestGitHubDisconnectWithNoAccountCallsTheAccountRoute(t *testing.T) {
	var mu sync.Mutex
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "afy_test_key")
	if err := client.GitHubDisconnect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/auth/github" {
		t.Errorf("path: want /auth/github, got %s", gotPath)
	}
}

// A 404 is the server saying this account does not hold that installation —
// deliberately not an idempotent 204, so another customer's ids cannot be
// probed. The client must surface it rather than swallowing it into success.
func TestGitHubDisconnectInstallationSurfacesA404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(
			`{"detail":{"code":"GITHUB_INSTALLATION_NOT_FOUND","message":"nope"}}`))
	}))
	defer srv.Close()

	client := api.NewClientWithURL(srv.URL, "afy_test_key")
	err := client.GitHubDisconnectInstallation(999)
	if err == nil {
		t.Fatal("a 404 was reported as a successful disconnect")
	}
	if apiErr, ok := err.(*api.APIError); ok {
		if apiErr.Code != "GITHUB_INSTALLATION_NOT_FOUND" {
			t.Errorf("code: want GITHUB_INSTALLATION_NOT_FOUND, got %q", apiErr.Code)
		}
	}
}
