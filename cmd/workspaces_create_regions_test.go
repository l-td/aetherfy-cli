package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// afy workspaces create --regions: a workspace OWNS its regions, 1..the plan's
// max_regions, chosen at create, on every plan. There is no account region to
// fall back on: omitted without a terminal is refused naming the allowed ones.

// regionOptions is GET /deploy-options for a plan allowing eu-central-1 and
// ap-southeast-1 (us-east-1 is listed but not open) with the given limit. It
// carries no home_region: the field is gone from the contract.
func regionOptions(max int) string {
	return `{"runtimes":["python3.11"],
	"regions":[{"code":"us-east-1","allowed":false},{"code":"eu-central-1","allowed":true},{"code":"ap-southeast-1","allowed":true}],
	"max_regions":` + string(rune('0'+max)) + `,
	"memory_mb":[{"memory_mb":512,"allowed":true}]}`
}

func createdWorkspace(regions string) string {
	return `{"id":"w-9","name":"research","regions":` + regions + `}`
}

func workspaceCreateCP(t *testing.T, max int) *fakeCP {
	cp := newFakeCP(t)
	cp.on("GET", "/deploy-options", 200, regionOptions(max))
	cp.on("POST", "/workspaces", 201, createdWorkspace(`["eu-central-1"]`))
	return cp
}

func TestWorkspacesCreateSendsTheRegionsItWasGiven(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"one", []string{"--regions", "eu-central-1"}, []string{"eu-central-1"}},
		{"comma-separated", []string{"--regions", "eu-central-1,ap-southeast-1"}, []string{"eu-central-1", "ap-southeast-1"}},
		{"repeated", []string{"--regions", "eu-central-1", "--regions", "ap-southeast-1"}, []string{"eu-central-1", "ap-southeast-1"}},
		{"a repeat is sent once", []string{"--regions", "eu-central-1,eu-central-1"}, []string{"eu-central-1"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cp := workspaceCreateCP(t, 2)
			_, stderr, err := runAfy(t, cp, append([]string{"workspaces", "create", "research"}, tc.args...)...)
			if err != nil {
				t.Fatalf("create: %v\n%s", err, stderr)
			}
			posts := cp.called("POST", "/workspaces")
			if len(posts) != 1 {
				t.Fatalf("want one POST /workspaces, got %+v", cp.all())
			}
			got := []string{}
			for _, r := range posts[0].Body["regions"].([]interface{}) {
				got = append(got, r.(string))
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("sent regions %v, want %v", got, tc.want)
			}
			if posts[0].Body["name"] != "research" {
				t.Errorf("body = %v", posts[0].Body)
			}
		})
	}
}

func TestWorkspacesCreateRefusesBeforeSendingAnythingInvalid(t *testing.T) {
	cases := []struct {
		name string
		max  int
		args []string
		want string // a phrase the sentence must carry
	}{
		{"a region the plan does not open", 2, []string{"--regions", "us-east-1"}, "not open on your plan"},
		{"not a region at all", 2, []string{"--regions", "mars-1"}, "is not a region"},
		{"more than the plan's limit", 1, []string{"--regions", "eu-central-1,ap-southeast-1"}, "allows 1 per workspace"},
		{"an empty region", 2, []string{"--regions", "eu-central-1,"}, "empty region"},
		{"an empty value", 2, []string{"--regions", ""}, "at least one region"},
		{"omitted, no terminal, a one-region plan", 1, nil, "pass --regions with up to 1 of: eu-central-1, ap-southeast-1"},
		{"omitted, no terminal, a multi-region plan", 2, nil, "pass --regions with up to 2 of: eu-central-1, ap-southeast-1"},
		{"omitted with --json", 2, []string{"--json"}, "pass --regions"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cp := workspaceCreateCP(t, tc.max)
			_, stderr, err := runAfy(t, cp, append([]string{"workspaces", "create", "research"}, tc.args...)...)
			if err == nil {
				t.Fatal("create succeeded; it must refuse")
			}
			if code := ExitCode(err); code != exitInputRefused {
				t.Errorf("exit %d, want %d (input refused)", code, exitInputRefused)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("the refusal does not say %q:\n%s", tc.want, stderr)
			}
			if n := len(cp.called("POST", "/workspaces")); n != 0 {
				t.Errorf("a refused create sent %d POST /workspaces", n)
			}
		})
	}
}

func TestWorkspacesCreateAsksOnATerminal(t *testing.T) {
	t.Run("one choice on a one-region plan", func(t *testing.T) {
		cp := workspaceCreateCP(t, 1)
		asked := scriptPrompts(t, "2\n")
		if _, stderr, err := runAfy(t, cp, "workspaces", "create", "research"); err != nil {
			t.Fatalf("create: %v\n%s", err, stderr)
		}
		posts := cp.called("POST", "/workspaces")
		if len(posts) != 1 || !reflect.DeepEqual(posts[0].Body["regions"], []interface{}{"ap-southeast-1"}) {
			t.Errorf("POST = %+v, want [ap-southeast-1] (choice 2)", posts)
		}
		if strings.Contains(asked.String(), "up to") {
			t.Errorf("a one-region plan is asked for one choice, not several:\n%s", asked)
		}
	})
	t.Run("several up to the limit", func(t *testing.T) {
		cp := workspaceCreateCP(t, 2)
		asked := scriptPrompts(t, "1,2\n")
		if _, stderr, err := runAfy(t, cp, "workspaces", "create", "research"); err != nil {
			t.Fatalf("create: %v\n%s", err, stderr)
		}
		posts := cp.called("POST", "/workspaces")
		if len(posts) != 1 || !reflect.DeepEqual(posts[0].Body["regions"], []interface{}{"eu-central-1", "ap-southeast-1"}) {
			t.Errorf("POST = %+v, want both regions", posts)
		}
		if !strings.Contains(asked.String(), "up to 2") {
			t.Errorf("the question must say the limit:\n%s", asked)
		}
	})
	t.Run("more than the limit is refused", func(t *testing.T) {
		cp := workspaceCreateCP(t, 2)
		// All three regions open, a limit of 2: three picks are one too many.
		cp.on("GET", "/deploy-options", 200, strings.Replace(regionOptions(2), `{"code":"us-east-1","allowed":false}`, `{"code":"us-east-1","allowed":true}`, 1))
		scriptPrompts(t, "1,2,3\n")
		_, stderr, err := runAfy(t, cp, "workspaces", "create", "research")
		if err == nil || ExitCode(err) != exitInputRefused {
			t.Fatalf("err = %v, want a refused input", err)
		}
		if !strings.Contains(stderr, "your plan allows 2") {
			t.Errorf("the refusal does not name the limit:\n%s", stderr)
		}
		if n := len(cp.called("POST", "/workspaces")); n != 0 {
			t.Errorf("a refused pick sent %d creates", n)
		}
	})
}

func TestWorkspacesCreateJSONCarriesTheRegions(t *testing.T) {
	cp := workspaceCreateCP(t, 2)
	cp.on("POST", "/workspaces", 201, createdWorkspace(`["eu-central-1","ap-southeast-1"]`))
	stdout, _, err := runAfy(t, cp, "workspaces", "create", "research", "--regions", "eu-central-1,ap-southeast-1", "--json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var ws struct {
		Regions []string `json:"regions"`
	}
	if err := json.Unmarshal([]byte(stdout), &ws); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if !reflect.DeepEqual(ws.Regions, []string{"eu-central-1", "ap-southeast-1"}) {
		t.Errorf("--json regions = %v", ws.Regions)
	}
}

func TestWorkspacesCreateRefusalFromTheServerKeepsItsCode(t *testing.T) {
	cp := workspaceCreateCP(t, 2)
	cp.on("POST", "/workspaces", 409, `{"detail":{"code":"WORKSPACE_NAME_TAKEN","message":"A workspace named 'research' already exists"}}`)
	stdout, stderr, code := runAfyProcess(t, cp, nil, "workspaces", "create", "research", "--regions", "eu-central-1", "--json")
	if code != 1 || strings.TrimSpace(stdout) != "" {
		t.Fatalf("exit %d, stdout %q", code, stdout)
	}
	if e := jsonErrorOnStderr(t, "create", stderr); e["code"] != "WORKSPACE_NAME_TAKEN" {
		t.Errorf("error = %v, want the server's code", e)
	}
}

func TestInitReadsNoHomeRegion(t *testing.T) {
	// home_region is gone from the contract: an answer that still carries one
	// must not decide anything. The region is the first the plan allows.
	dir := t.TempDir()
	cp := initCP(t)
	cp.on("GET", "/deploy-options", 200, strings.Replace(deployOptions, `"max_regions":1,`, `"max_regions":1,"home_region":"ap-southeast-1",`, 1))
	if _, stderr, err := runAfy(t, cp, "init", dir, "-y", "--name", "invoice-parser", "--runtime", "python3.11", "--entrypoint", "main.py"); err != nil {
		t.Fatalf("init: %v\n%s", err, stderr)
	}
	if !strings.Contains(readFile(t, dir+"/aetherfy.yaml"), "- eu-central-1") {
		t.Errorf("the region is not the first allowed (eu-central-1):\n%s", readFile(t, dir+"/aetherfy.yaml"))
	}
}

// --- narrowing a workspace: 422 WORKSPACE_NARROW_BLOCKED_BY_RESOURCES ---

const narrowBlocked = `{"detail":{"code":"WORKSPACE_NARROW_BLOCKED_BY_RESOURCES","message":"2 resource(s) still use a region being removed.","regions_in_use":["eu-central-1","ap-southeast-1"]}}`

func TestWorkspacesRegionsNarrowRefusalNamesTheRegionsInUse(t *testing.T) {
	cp := newFakeCP(t)
	cp.on("GET", "/deploy-options", 200, regionOptions(2))
	cp.on("PATCH", "/workspaces/research/regions", 422, narrowBlocked)

	// Text: the server's sentence, then the regions it names.
	_, stderr, err := runAfy(t, cp, "workspaces", "regions", "research", "--regions", "ap-southeast-1")
	if err == nil {
		t.Fatal("a blocked narrow succeeded")
	}
	if ExitCode(err) != exitRequestFailed {
		t.Errorf("exit %d, want %d", ExitCode(err), exitRequestFailed)
	}
	for _, want := range []string{"2 resource(s) still use a region being removed.", "Regions still in use: eu-central-1, ap-southeast-1."} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, stderr)
		}
	}
	if strings.Count(stderr, "still use a region") != 1 {
		t.Errorf("the sentence must print once:\n%s", stderr)
	}

	// --json: the code and status stay the server's, the sentence carries the regions.
	stdout, stderr, code := runAfyProcess(t, cp, nil, "workspaces", "regions", "research", "--regions", "ap-southeast-1", "--json")
	if code != 1 || strings.TrimSpace(stdout) != "" {
		t.Fatalf("exit %d, stdout %q", code, stdout)
	}
	e := jsonErrorOnStderr(t, "regions", stderr)
	if e["code"] != "WORKSPACE_NARROW_BLOCKED_BY_RESOURCES" || e["status"] != 422.0 {
		t.Errorf("error = %v, want the server's code and 422", e)
	}
	if msg, _ := e["message"].(string); !strings.Contains(msg, "eu-central-1, ap-southeast-1") {
		t.Errorf("the message does not name the regions: %v", e)
	}
}

func TestWorkspacesRegionsNarrowRefusalWithoutRegionsInUseIsTheServersSentence(t *testing.T) {
	// A control plane that does not send regions_in_use (yet): the sentence is
	// printed as written, with nothing appended and nothing invented.
	cp := newFakeCP(t)
	cp.on("GET", "/deploy-options", 200, regionOptions(2))
	cp.on("PATCH", "/workspaces/research/regions", 422,
		`{"detail":{"code":"WORKSPACE_NARROW_BLOCKED_BY_RESOURCES","message":"1 resource(s) still use a region being removed."}}`)
	_, stderr, err := runAfy(t, cp, "workspaces", "regions", "research", "--regions", "ap-southeast-1")
	if err == nil {
		t.Fatal("a blocked narrow succeeded")
	}
	if strings.Contains(stderr, "still in use") || !strings.Contains(stderr, "1 resource(s) still use a region being removed.") {
		t.Errorf("want the server's sentence alone:\n%s", stderr)
	}
}
