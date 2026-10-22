package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// afy workspaces regions checks --regions locally, exactly as `workspaces
// create` does, and renders the rules the server names when it refuses.

func regionsChangeCP(t *testing.T, max int) *fakeCP {
	cp := newFakeCP(t)
	cp.on("GET", "/deploy-options", 200, regionOptions(max))
	cp.on("PATCH", "/workspaces/research/regions", 202,
		`{"operation_id":"op-1","status":"pending","from_regions":["eu-central-1"],"to_regions":["eu-central-1","ap-southeast-1"]}`)
	return cp
}

func TestWorkspacesRegionsRefusesAnInvalidRegionBeforeSendingAnything(t *testing.T) {
	cases := []struct {
		name string
		max  int
		args string
		want []string
	}{
		{"a region that does not exist", 2, "eu-central-1,bogus-region",
			[]string{"bogus-region is not a region", "allowed on your plan: eu-central-1, ap-southeast-1", "Nothing was changed."}},
		{"a region the plan does not open", 2, "us-east-1",
			[]string{"region us-east-1 is not open on your plan", "allowed: eu-central-1, ap-southeast-1"}},
		{"more than the plan allows", 1, "eu-central-1,ap-southeast-1",
			[]string{"2 regions given, but your plan allows 1 per workspace"}},
		{"an empty entry", 2, "eu-central-1,",
			[]string{"--regions got an empty region", "allowed: eu-central-1, ap-southeast-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cp := regionsChangeCP(t, tc.max)
			_, stderr, err := runAfy(t, cp, "workspaces", "regions", "research", "--regions", tc.args)
			if err == nil || ExitCode(err) != exitInputRefused {
				t.Fatalf("want exit %d, got %v (exit %d)\n%s", exitInputRefused, err, ExitCode(err), stderr)
			}
			for _, w := range tc.want {
				if !strings.Contains(stderr, w) {
					t.Errorf("stderr lacks %q:\n%s", w, stderr)
				}
			}
			if n := len(cp.called("PATCH", "/workspaces/research/regions")); n != 0 {
				t.Errorf("nothing may be sent, got %d PATCH", n)
			}
		})
	}
}

func TestWorkspacesRegionsSendsAValidChange(t *testing.T) {
	cp := regionsChangeCP(t, 2)
	_, stderr, err := runAfy(t, cp, "workspaces", "regions", "research", "--regions", "eu-central-1,ap-southeast-1,eu-central-1")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	patches := cp.called("PATCH", "/workspaces/research/regions")
	if len(patches) != 1 {
		t.Fatalf("want one PATCH, got %+v", cp.all())
	}
	got, _ := json.Marshal(patches[0].Body["regions"])
	if string(got) != `["eu-central-1","ap-southeast-1"]` {
		t.Errorf("sent %s (a repeat is sent once)", got)
	}
}

const violationsRefusal = `{"detail":{"code":"VALIDATION_VIOLATIONS","message":"Request validation failed.","violations":[` +
	`{"resource":"regions","code":"PLAN_REGION_LIMIT","current":2,"limit":1,"message":"Your plan allows 1 region per workspace; you asked for 2."},` +
	`{"resource":"regions","code":"REGION_UNAVAILABLE","region":"ap-southeast-1","message":"ap-southeast-1 has no capacity right now."}]}}`

func TestWorkspacesRegionsRendersTheRulesTheServerNames(t *testing.T) {
	cp := regionsChangeCP(t, 2)
	cp.on("PATCH", "/workspaces/research/regions", 400, violationsRefusal)

	_, stderr, err := runAfy(t, cp, "workspaces", "regions", "research", "--regions", "eu-central-1,ap-southeast-1")
	if err == nil {
		t.Fatal("a refusal must fail the command")
	}
	for _, w := range []string{
		"Error: Request validation failed.",
		"  - Your plan allows 1 region per workspace; you asked for 2.",
		"  - ap-southeast-1 has no capacity right now.",
	} {
		if !strings.Contains(stderr, w) {
			t.Errorf("stderr lacks %q:\n%s", w, stderr)
		}
	}

	_, stderr, err = runAfy(t, cp, "workspaces", "regions", "research", "--regions", "eu-central-1,ap-southeast-1", "--json")
	if err == nil {
		t.Fatal("a refusal must fail the command")
	}
	doc := jsonErrorOnStderr(t, "violations", stderr)
	msg, _ := doc["message"].(string)
	if doc["code"] != "VALIDATION_VIOLATIONS" || !strings.Contains(msg, "ap-southeast-1 has no capacity right now.") {
		t.Errorf("--json error = %v", doc)
	}
}

// The pydantic form already joins its entries into the summary; the entries
// (loc/msg, no `message`) are not appended a second time.
func TestAValidationErrorSummaryIsNotRepeated(t *testing.T) {
	cp := regionsChangeCP(t, 2)
	cp.on("PATCH", "/workspaces/research/regions", 422,
		`{"detail":{"code":"VALIDATION_ERROR","message":"regions must not be empty","violations":[{"loc":["body","regions"],"msg":"regions must not be empty"}]}}`)
	_, stderr, err := runAfy(t, cp, "workspaces", "regions", "research", "--regions", "eu-central-1")
	if err == nil {
		t.Fatal("a refusal must fail the command")
	}
	if n := strings.Count(stderr, "regions must not be empty"); n != 1 {
		t.Errorf("want the summary once, got %d:\n%s", n, stderr)
	}
}

func TestWorkspacesCreateEchoesTheRegionsItCreatedTheWorkspaceWith(t *testing.T) {
	cp := workspaceCreateCP(t, 2)
	cp.on("POST", "/workspaces", 201, createdWorkspace(`["eu-central-1","ap-southeast-1"]`))

	stdout, stderr, err := runAfy(t, cp, "workspaces", "create", "research", "--regions", "eu-central-1,ap-southeast-1")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "Regions") || !strings.Contains(stdout, "eu-central-1, ap-southeast-1") {
		t.Errorf("the success must name the regions:\n%s", stdout)
	}

	stdout, stderr, err = runAfy(t, cp, "workspaces", "create", "research", "--regions", "eu-central-1,ap-southeast-1", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	var doc struct {
		Regions []string `json:"regions"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if strings.Join(doc.Regions, ",") != "eu-central-1,ap-southeast-1" {
		t.Errorf("--json regions = %v", doc.Regions)
	}
}
