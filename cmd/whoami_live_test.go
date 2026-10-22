package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// afy whoami reads the account's email and plan from the API on every run
// (GET /auth/me), so they are right whichever way the key was supplied and
// right after a plan change. runAfy supplies the key through AETHERFY_API_KEY
// and a fresh, empty credentials directory: nothing was saved by `afy login`.

func whoamiJSON(t *testing.T, cp *fakeCP) map[string]interface{} {
	t.Helper()
	stdout, stderr, err := runAfy(t, cp, "whoami", "--json")
	if err != nil {
		t.Fatalf("whoami: %v\n%s", err, stderr)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	return doc
}

func TestWhoamiWithAnEnvVarKeyShowsTheLivePlan(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/auth/me", 200, `{"user_id":"u-1","email":"dev@example.com","tier":"performance"}`)

	doc := whoamiJSON(t, cp)
	if doc["tier"] != "performance" || doc["email"] != "dev@example.com" {
		t.Errorf("tier/email = %v / %v; want the API's, though nothing was saved at login", doc["tier"], doc["email"])
	}
	if n := len(cp.called("GET", "/auth/me")); n != 1 {
		t.Errorf("want one live read, got %d", n)
	}

	stdout, _, err := runAfy(t, cp, "whoami")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Tier", "performance", "Email", "dev@example.com"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("text output lacks %q:\n%s", want, stdout)
		}
	}
}

func TestWhoamiIsRightAfterAPlanChange(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/auth/me", 200, `{"user_id":"u-1","email":"dev@example.com","tier":"starter"}`)
	if got := whoamiJSON(t, cp)["tier"]; got != "starter" {
		t.Fatalf("tier = %v", got)
	}
	cp.on("GET", "/auth/me", 200, `{"user_id":"u-1","email":"dev@example.com","tier":"performance"}`)
	if got := whoamiJSON(t, cp)["tier"]; got != "performance" {
		t.Errorf("tier = %v after the plan changed; whoami must not read a stored value", got)
	}
}

func TestWhoamiThatCannotReadTheAccountFailsInsteadOfPrintingAnEmptyTier(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/auth/me", 401, `{"detail":{"code":"INVALID_API_KEY","message":"The API key is not valid."}}`)
	stdout, stderr, err := runAfy(t, cp, "whoami", "--json")
	if err == nil {
		t.Fatalf("a key the API refuses must fail whoami\n%s", stdout)
	}
	if strings.TrimSpace(stdout) != "" || !strings.Contains(stderr, "INVALID_API_KEY") {
		t.Errorf("stdout %q stderr %q", stdout, stderr)
	}
}
