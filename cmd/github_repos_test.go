package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// reposServer answers GET /auth/github/repositories with `body`, or the given
// non-200 status when body is empty.
func reposServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"detail":{"code":"GITHUB_NOT_CONNECTED","message":"nope"}}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const twoRepos = `{"repositories":[` +
	`{"full_name":"acme/apple","private":false,"default_branch":"main",` +
	`"installation_id":1,"account_login":"acme","account_type":"Organization"},` +
	`{"full_name":"acme/zebra","private":true,"default_branch":"trunk",` +
	`"installation_id":1,"account_login":"acme","account_type":"Organization"}]}`

// TWO ACCOUNTS in one list — the union an Aetherfy account holding a personal
// installation and an organization one actually returns.
const twoAccounts = `{"repositories":[` +
	`{"full_name":"acme/apple","private":false,"default_branch":"main",` +
	`"installation_id":1,"account_login":"acme","account_type":"Organization"},` +
	`{"full_name":"l-td/templates","private":false,"default_branch":"main",` +
	`"installation_id":2,"account_login":"l-td","account_type":"User"}]}`

// THE WHOLE REASON THIS EXISTS. A 404 from link means a mistyped repo, a
// mistyped OWNER, or a repository the App was never granted, and the owner is
// the one nobody can check: it is the account the App is installed on, not the
// user's login. Listing what they CAN link answers all three at once.
func TestLinkableReposListsWhatCanBeLinked(t *testing.T) {
	srv := reposServer(t, http.StatusOK, twoRepos)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	out := captureStdout(t, func() { printLinkableRepos(client, "acme/apple") })

	for _, want := range []string{"acme/apple", "acme/zebra"} {
		if !strings.Contains(out, want) {
			t.Errorf("did not list %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "acme") {
		t.Errorf("did not name the account:\n%s", out)
	}
}

// An owner that matches ONE of several connected accounts is not a wrong
// owner. Before the union this could not arise — there was one account, so
// "not that one" meant "wrong" — and a check that still compared against a
// single account would blame the owner of every repository on the second one.
func TestLinkableReposStaysQuietWhenTheOwnerIsOneOfSeveral(t *testing.T) {
	srv := reposServer(t, http.StatusOK, twoAccounts)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	out := captureStdout(t, func() { printLinkableRepos(client, "l-td/typo") })

	if strings.Contains(out, "not on the owner you named") {
		t.Errorf("blamed an owner that IS connected:\n%s", out)
	}
}

// ...and when the owner is on neither, BOTH accounts are named. Naming only
// the first would send someone to install on an account they already have.
func TestLinkableReposNamesEveryAccountWhenTheOwnerIsOnNone(t *testing.T) {
	srv := reposServer(t, http.StatusOK, twoAccounts)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	out := captureStdout(t, func() { printLinkableRepos(client, "stranger/apple") })

	if !strings.Contains(out, "not on the owner you named") {
		t.Errorf("did not say the owner is the half that differs:\n%s", out)
	}
	for _, want := range []string{"acme", "l-td"} {
		if !strings.Contains(out, want) {
			t.Errorf("did not name the connected account %q:\n%s", want, out)
		}
	}
}

func TestLinkableReposNamesTheOwnerWhenThatIsWhatDiffers(t *testing.T) {
	srv := reposServer(t, http.StatusOK, twoRepos)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	out := captureStdout(t, func() { printLinkableRepos(client, "mylogin/apple") })

	if !strings.Contains(out, "mylogin/apple") {
		t.Errorf("did not repeat what was asked for:\n%s", out)
	}
	if !strings.Contains(out, "not on the owner you named") {
		t.Errorf("did not say the owner is the half that differs:\n%s", out)
	}
}

// THE NEGATIVE CONTROL for the test above. A right owner with a wrong repo is
// an ordinary typo the list already answers, and claiming the owner is wrong
// there would send someone to re-install an App that is installed correctly.
func TestLinkableReposStaysQuietAboutTheOwnerWhenTheOwnerIsRight(t *testing.T) {
	srv := reposServer(t, http.StatusOK, twoRepos)
	client := api.NewClientWithURL(srv.URL, "afy_test_key")

	out := captureStdout(t, func() { printLinkableRepos(client, "acme/typo") })

	if strings.Contains(out, "not on the owner you named") {
		t.Errorf("blamed the owner for a mistyped repository name:\n%s", out)
	}
	if !strings.Contains(out, "acme/apple") {
		t.Errorf("stopped listing the repositories:\n%s", out)
	}
}

// FAIL-SOFT. This runs after the link already failed and printed why. A second
// failure here that printed its own error would bury the first one, which is
// the message the user actually needs.
func TestLinkableReposPrintsNothingWhenItCannotAsk(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"the account is not connected", http.StatusUnprocessableEntity, ""},
		{"the installations reach nothing", http.StatusOK, `{"repositories":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := reposServer(t, tc.status, tc.body)
			client := api.NewClientWithURL(srv.URL, "afy_test_key")

			out := captureStdout(t, func() { printLinkableRepos(client, "acme/apple") })

			if strings.TrimSpace(out) != "" {
				t.Errorf("printed over the link failure:\n%s", out)
			}
		})
	}
}
