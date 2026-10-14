package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/archive"
	"github.com/l-td/aetherfy-cli/internal/link"
	"gopkg.in/yaml.v3"
)

// afy init: a new agent (created in a workspace, then the yaml and the link
// written), or --agent: an existing one linked by id, its configuration
// written unchanged when the folder has none.

const deployOptions = `{"regions":[{"code":"us-east-1","allowed":false},{"code":"eu-central-1","allowed":true},{"code":"ap-southeast-1","allowed":true}],
	"max_regions":1,"home_region":"ap-southeast-1",
	"memory_mb":[{"memory_mb":256,"allowed":false},{"memory_mb":512,"allowed":true},{"memory_mb":1024,"allowed":true}],
	"max_memory_mb":1024}`

// initWorkspaces' one workspace has regions within the plan's (deployOptions): eu-central-1
// and ap-southeast-1.
const initWorkspaces = `[{"id":"w-1","name":"default","regions":["eu-central-1","ap-southeast-1"]}]`

func initCP(t *testing.T) *fakeCP {
	cp := newFakeCP(t)
	cp.on("GET", "/workspaces", 200, initWorkspaces)
	cp.on("GET", "/deploy-options", 200, deployOptions)
	cp.on("POST", "/agents", 201, `{"id":"`+prodID+`","name":"invoice-parser","workspace_id":"w-1","workspace_name":"default"}`)
	return cp
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestInitCreatesTheAgentThenWritesTheYamlAndTheLink(t *testing.T) {
	dir := t.TempDir()
	cp := initCP(t)

	stdout, stderr, err := runAfy(t, cp, "init", dir, "-y", "--name", "invoice-parser", "--runtime", "python3.11", "--entrypoint", "main.py")
	if err != nil {
		t.Fatalf("init: %v\n%s%s", err, stdout, stderr)
	}

	posts := cp.called("POST", "/agents")
	if len(posts) != 1 {
		t.Fatalf("want one POST /agents, got %+v", cp.all())
	}
	if b := posts[0].Body; len(b) != 2 || b["name"] != "invoice-parser" || b["workspace_id"] != "w-1" {
		t.Errorf("POST /agents body = %v, want exactly {name, workspace_id}", b)
	}

	body := readFile(t, filepath.Join(dir, "aetherfy.yaml"))
	if !strings.HasPrefix(body, yamlSchemaModeline+"\n") {
		t.Errorf("the modeline must be the first line:\n%s", body)
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "description", "workspace"} {
		if _, ok := doc[k]; ok {
			t.Errorf("aetherfy.yaml must not declare %s:\n%s", k, body)
		}
	}
	// From /deploy-options: the home region, and the smallest size allowed.
	if r, _ := doc["regions"].([]interface{}); len(r) != 1 || r[0] != "ap-southeast-1" {
		t.Errorf("regions = %v, want [ap-southeast-1] (the home region)", doc["regions"])
	}
	if doc["memory_mb"] != 512 {
		t.Errorf("memory_mb = %v, want 512 (the smallest size the plan allows)", doc["memory_mb"])
	}
	if err := archive.CheckUnknownFields([]byte(body)); err != nil {
		t.Errorf("the generated file names a key a deploy refuses: %v", err)
	}

	f, err := link.Read(dir)
	if err != nil || f.Targets["default"] != prodID || len(f.Targets) != 1 {
		t.Errorf("link = %+v, %v; want {default: %s}", f, err, prodID)
	}
}

// A deploy refuses regions outside the workspace's, so init offers only the
// plan's regions that are inside it, and a workspace with none yet all of them.
func TestInitOffersOnlyRegionsInsideTheWorkspace(t *testing.T) {
	for _, c := range []struct {
		name, workspace, flag, want string
		refused                     bool
	}{
		{"the home region is outside the workspace", `[{"id":"w-1","name":"default","regions":["eu-central-1"]}]`, "", "eu-central-1", false},
		{"a plan region outside the workspace", `[{"id":"w-1","name":"default","regions":["eu-central-1"]}]`, "ap-southeast-1", "", true},
		{"a workspace with no regions yet", `[{"id":"w-1","name":"default","regions":[]}]`, "", "ap-southeast-1", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			cp := initCP(t)
			cp.on("GET", "/workspaces", 200, c.workspace)
			args := []string{"init", dir, "-y", "--runtime", "python3.11"}
			if c.flag != "" {
				args = append(args, "--region", c.flag)
			}
			_, stderr, err := runAfy(t, cp, args...)
			if c.refused {
				if err == nil || ExitCode(err) != exitInputRefused || len(cp.called("POST", "/agents")) != 0 {
					t.Fatalf("want a refusal before anything is created, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("init: %v\n%s", err, stderr)
			}
			var doc map[string]interface{}
			_ = yaml.Unmarshal([]byte(readFile(t, filepath.Join(dir, "aetherfy.yaml"))), &doc)
			if r, _ := doc["regions"].([]interface{}); len(r) != 1 || r[0] != c.want {
				t.Errorf("regions = %v, want [%s]", doc["regions"], c.want)
			}
		})
	}
}

func TestInitRefusesARegionOrSizeThePlanDoesNotAllow(t *testing.T) {
	for _, flag := range [][]string{{"--region", "us-east-1"}, {"--memory", "256"}} {
		dir := t.TempDir()
		cp := initCP(t)
		args := append([]string{"init", dir, "-y", "--runtime", "python3.11"}, flag...)
		_, stderr, err := runAfy(t, cp, args...)
		if err == nil || ExitCode(err) != exitInputRefused {
			t.Fatalf("%v: want exit %d, got %v", flag, exitInputRefused, err)
		}
		if !strings.Contains(err.Error()+stderr, "allowed:") {
			t.Errorf("%v: the refusal must list what is allowed: %v %s", flag, err, stderr)
		}
		if len(cp.called("POST", "/agents")) != 0 || fileExists(filepath.Join(dir, "aetherfy.yaml")) {
			t.Errorf("%v: nothing may be created or written", flag)
		}
	}
}

func TestInitARefusedCreateWritesNothing(t *testing.T) {
	const sentence = "An agent named 'invoice-parser' already exists."
	dir := t.TempDir()
	cp := initCP(t)
	cp.on("POST", "/agents", 409, `{"detail":{"code":"AGENT_NAME_TAKEN","message":"`+sentence+`"}}`)

	_, stderr, err := runAfy(t, cp, "init", dir, "-y", "--name", "invoice-parser", "--runtime", "python3.11")
	if err == nil {
		t.Fatal("a refused create must fail init")
	}
	if !strings.Contains(stderr, "Error: "+sentence+"\n") {
		t.Errorf("the server's sentence must be printed unaltered:\n%s", stderr)
	}
	if fileExists(filepath.Join(dir, "aetherfy.yaml")) || fileExists(link.Path(dir)) {
		t.Error("a refused create must write neither aetherfy.yaml nor the link")
	}
}

func TestInitRefusesToRelinkALinkedTargetToANewAgent(t *testing.T) {
	dir := t.TempDir()
	if err := link.Write(dir, "default", stagingID); err != nil {
		t.Fatal(err)
	}
	cp := initCP(t)
	_, _, err := runAfy(t, cp, "init", dir, "-y", "--runtime", "python3.11")
	if err == nil || !strings.Contains(err.Error(), "already linked to agent "+stagingID) {
		t.Fatalf("want a refusal naming the linked agent, got %v", err)
	}
	if len(cp.all()) != 0 {
		t.Errorf("nothing may be sent: %+v", cp.all())
	}
}

func TestInitAgentLinksAnExistingAgentAndWritesItsExportUnchanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty-folder")
	cp := newFakeCP(t)
	cp.on("GET", "/agents/invoice-parser", 200, `{"id":"`+prodID+`","name":"invoice-parser"}`)
	cp.on("GET", "/agents/"+prodID+"/yaml", 200, serverYAML)

	if _, stderr, err := runAfy(t, cp, "init", dir, "--agent", "invoice-parser"); err != nil {
		t.Fatalf("init --agent: %v\n%s", err, stderr)
	}
	if got := readFile(t, filepath.Join(dir, "aetherfy.yaml")); got != serverYAML {
		t.Errorf("the export must be written byte for byte as served.\nwant:\n%s\ngot:\n%s", serverYAML, got)
	}
	f, err := link.Read(dir)
	if err != nil || f.Targets["default"] != prodID {
		t.Errorf("link = %+v, %v", f, err)
	}
	if len(cp.called("POST", "/agents")) != 0 {
		t.Error("linking must not create an agent")
	}
}

func TestInitAgentWithATargetKeepsTheOtherTargetsAndTheYaml(t *testing.T) {
	dir := t.TempDir()
	const own = "runtime: python3.11\n# mine\n"
	if err := os.WriteFile(filepath.Join(dir, "aetherfy.yaml"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := link.Write(dir, "default", prodID); err != nil {
		t.Fatal(err)
	}
	cp := newFakeCP(t)
	cp.on("GET", "/agents/invoice-parser-staging", 200, `{"id":"`+stagingID+`","name":"invoice-parser-staging"}`)

	stdout, stderr, err := runAfy(t, cp, "init", dir, "--agent", "invoice-parser-staging", "--target", "staging")
	if err != nil {
		t.Fatalf("init --agent --target: %v\n%s", err, stderr)
	}
	f, _ := link.Read(dir)
	if f == nil || f.Targets["default"] != prodID || f.Targets["staging"] != stagingID {
		t.Errorf("link = %+v, want default kept and staging added", f)
	}
	if got := readFile(t, filepath.Join(dir, "aetherfy.yaml")); got != own {
		t.Errorf("an existing aetherfy.yaml must be kept without --force; got:\n%s", got)
	}
	if len(cp.called("GET", "/agents/"+stagingID+"/yaml")) != 0 {
		t.Error("the export must not be fetched when the yaml is kept")
	}
	if !strings.Contains(stdout, "afy deploy --target staging") {
		t.Errorf("the next step must name the target:\n%s", stdout)
	}
}

func TestInitAgentThatDoesNotExistWritesNothing(t *testing.T) {
	dir := t.TempDir()
	cp := newFakeCP(t)
	cp.on("GET", "/agents/ghost", 404, `{"detail":{"code":"AGENT_NOT_FOUND","message":"Agent 'ghost' not found"}}`)

	_, stderr, err := runAfy(t, cp, "init", dir, "--agent", "ghost")
	if err == nil || !strings.Contains(stderr, "Agent 'ghost' not found") {
		t.Fatalf("want the server's refusal, got %v\n%s", err, stderr)
	}
	if fileExists(filepath.Join(dir, "aetherfy.yaml")) || fileExists(link.Path(dir)) {
		t.Error("nothing may be written")
	}
}
