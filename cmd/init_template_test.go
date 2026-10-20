package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/archive"
	"github.com/l-td/aetherfy-cli/internal/detect"
	"github.com/l-td/aetherfy-cli/internal/link"
	"github.com/l-td/aetherfy-cli/internal/starters"
	"gopkg.in/yaml.v3"
)

// afy init --template: a NEW agent from a starter project. The starter's files
// are written once the agent exists, then aetherfy.yaml and the link.

func readYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s is not YAML: %v", path, err)
	}
	if err := archive.CheckUnknownFields(data); err != nil {
		t.Fatalf("the server would refuse %s: %v\n%s", path, err, data)
	}
	return doc
}

// Every starter: its files are written, aetherfy.yaml presets the starter's
// runtime and entrypoint as a service, the file is one the server accepts, and
// the agent is created under the directory's name and linked.
func TestInitTemplateWritesTheStarterAndItsYAML(t *testing.T) {
	for _, s := range starters.All {
		t.Run(s.Name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "my-agent")
			cp := initCP(t)
			stdout, stderr, err := runAfy(t, cp, "init", "--template", s.Name, dir, "-y")
			if err != nil {
				t.Fatalf("afy init --template %s: %v\n%s%s", s.Name, err, stdout, stderr)
			}
			files, err := s.Files()
			if err != nil {
				t.Fatal(err)
			}
			for p, want := range files {
				got, err := os.ReadFile(filepath.Join(dir, p))
				if err != nil {
					t.Fatalf("%s was not written: %v", p, err)
				}
				if string(got) != string(want) {
					t.Errorf("%s differs from the embedded starter", p)
				}
			}
			doc := readYAML(t, filepath.Join(dir, "aetherfy.yaml"))
			if doc["runtime"] != s.Runtime || doc["entrypoint"] != s.Entrypoint || doc["type"] != "service" {
				t.Errorf("aetherfy.yaml = %v; want runtime %s, entrypoint %s, type service", doc, s.Runtime, s.Entrypoint)
			}
			posts := cp.called("POST", "/agents")
			if len(posts) != 1 || posts[0].Body["name"] != "my-agent" {
				t.Errorf("want one POST /agents named after the directory, got %+v", posts)
			}
			if f, err := link.Read(dir); err != nil || f.Targets["default"] != prodID {
				t.Errorf("link = %+v, %v; want {default: %s}", f, err, prodID)
			}
		})
	}
}

// DETECTION AGREES WITH THE PRESET. The preset is what aetherfy.yaml says; a
// scan of the same files (what every later `afy init`, and the framework advice,
// reads) must reach the same runtime and entrypoint, or init would print one
// runtime and the folder say another (.python-version / engines.node). And every
// starter is a framework the table knows, so its advice line is not silent.
func TestEveryStarterIsDetectedAsItsPreset(t *testing.T) {
	for _, s := range starters.All {
		t.Run(s.Name, func(t *testing.T) {
			dir := t.TempDir()
			files, err := s.Files()
			if err != nil {
				t.Fatal(err)
			}
			for p, b := range files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, p), b, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			h := detect.Project(dir)
			if h.Runtime != s.Runtime || h.Entrypoint != s.Entrypoint {
				t.Errorf("the scan of %s reached runtime %q, entrypoint %q; the preset is %q, %q",
					s.Name, h.Runtime, h.Entrypoint, s.Runtime, s.Entrypoint)
			}
			if len(h.Frameworks) == 0 {
				t.Errorf("no framework detected in %s: its advice line would be silent", s.Name)
			}
		})
	}
}

// A flag still wins over the starter's preset.
func TestInitTemplateFlagsOverrideThePreset(t *testing.T) {
	dir := t.TempDir()
	cp := initCP(t)
	if stdout, stderr, err := runAfy(t, cp, "init", "--template", "node-ts-hono", "--runtime", "node20-ts", dir, "-y"); err != nil {
		t.Fatalf("%v\n%s%s", err, stdout, stderr)
	}
	if doc := readYAML(t, filepath.Join(dir, "aetherfy.yaml")); doc["runtime"] != "node20-ts" {
		t.Errorf("runtime = %v; --runtime should win over the starter's preset", doc["runtime"])
	}
}

// NOTHING IS OVERWRITTEN, AND NOTHING IS HALF-WRITTEN. One existing file makes
// the whole template refuse, before the agent is created and before the first
// file is written.
func TestInitTemplateRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	mine := []byte("my own README\n")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), mine, 0644); err != nil {
		t.Fatal(err)
	}
	cp := initCP(t)
	stdout, stderr, err := runAfy(t, cp, "init", "--template", "python-fastapi", dir, "-y")
	refusal := stdout + stderr
	if err == nil || !strings.Contains(refusal+err.Error(), "README.md") || !strings.Contains(refusal+err.Error(), "--force") {
		t.Fatalf("err = %v\n%s; want a refusal naming README.md and --force", err, refusal)
	}
	if n := len(cp.called("POST", "/agents")); n != 0 {
		t.Errorf("an agent was created (%d) although the template refused", n)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "README.md")); string(got) != string(mine) {
		t.Error("the customer's README.md was changed")
	}
	for _, p := range []string{"main.py", "requirements.txt", "aetherfy.yaml"} {
		if fileExists(filepath.Join(dir, p)) {
			t.Errorf("%s was written although the template refused", p)
		}
	}
}

// aetherfy.yaml counts as a conflict too.
func TestInitTemplateRefusesAnExistingConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aetherfy.yaml"), []byte("runtime: node22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cp := initCP(t)
	stdout, stderr, err := runAfy(t, cp, "init", "--template", "node-express", dir, "-y")
	if err == nil || !strings.Contains(stdout+stderr+err.Error(), "aetherfy.yaml") {
		t.Fatalf("err = %v\n%s%s; want a refusal naming aetherfy.yaml", err, stdout, stderr)
	}
	if len(cp.called("POST", "/agents")) != 0 {
		t.Error("an agent was created although the template refused")
	}
}

func TestInitTemplateForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cp := initCP(t)
	if stdout, stderr, err := runAfy(t, cp, "init", "--template", "python-litestar", "--force", dir, "-y"); err != nil {
		t.Fatalf("%v\n%s%s", err, stdout, stderr)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "main.py")); !strings.Contains(string(got), "Litestar") {
		t.Error("--force did not replace main.py")
	}
}

// A REFUSED CREATE WRITES NOTHING. The starter is written once the agent
// exists, so a name that is already taken leaves the folder as it was and the
// same command can be run again with another --name.
func TestInitTemplateWritesNothingWhenTheCreateIsRefused(t *testing.T) {
	dir := t.TempDir()
	cp := initCP(t)
	cp.on("POST", "/agents", 409, `{"detail":{"code":"AGENT_NAME_TAKEN","message":"That name is taken."}}`)
	_, _, err := runAfy(t, cp, "init", "--template", "python-fastapi", dir, "-y", "--name", "taken")
	if err == nil {
		t.Fatal("a refused create reported success")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the folder was written to although no agent was created: %v", entries)
	}
}

func TestInitTemplateUnknownNameListsTheRealOnes(t *testing.T) {
	cp := initCP(t)
	stdout, stderr, err := runAfy(t, cp, "init", "--template", "python-flask", t.TempDir(), "-y")
	if err == nil {
		t.Fatal("an unknown template was accepted")
	}
	for _, name := range starters.Names() {
		if !strings.Contains(stdout+stderr+err.Error(), name) {
			t.Errorf("the refusal does not name %s: %v\n%s%s", name, err, stdout, stderr)
		}
	}
	if len(cp.called("POST", "/agents")) != 0 {
		t.Error("an agent was created for an unknown template")
	}
}

// A template makes a NEW agent; --agent links an existing one. Together they
// mean nothing, so they are refused before anything is asked of the server.
func TestInitTemplateRefusesToCombineWithAgent(t *testing.T) {
	dir := t.TempDir()
	cp := initCP(t)
	_, _, err := runAfy(t, cp, "init", "--template", "node-express", "--agent", "invoice-parser", dir, "-y")
	if err == nil || ExitCode(err) != exitInputRefused {
		t.Fatalf("err = %v; want an input refusal", err)
	}
	if len(cp.all()) != 0 {
		t.Errorf("the server was called: %+v", cp.all())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the folder was written to: %v", entries)
	}
}

// --list-templates reads the binary: no account, no server, no files.
func TestInitListTemplates(t *testing.T) {
	dir := t.TempDir()
	cp := newFakeCP(t)
	stdout, stderr, err := runAfy(t, cp, "init", "--list-templates", dir)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	for _, s := range starters.All {
		if !strings.Contains(stdout, s.Name) || !strings.Contains(stdout, s.Runtime) {
			t.Errorf("--list-templates does not list %s (%s):\n%s", s.Name, s.Runtime, stdout)
		}
	}
	if len(cp.all()) != 0 {
		t.Errorf("--list-templates called the server: %+v", cp.all())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("--list-templates wrote files: %v", entries)
	}
}

// With --json the list is the result, in the CLI's one JSON contract: the
// human text goes to stderr, stdout carries one object.
func TestInitListTemplatesJSON(t *testing.T) {
	stdout, _, err := runAfy(t, nil, "init", "--list-templates", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Templates []map[string]string `json:"templates"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
	}
	if len(got.Templates) != len(starters.All) {
		t.Fatalf("%d templates listed, want %d", len(got.Templates), len(starters.All))
	}
	for i, s := range starters.All {
		g := got.Templates[i]
		if g["name"] != s.Name || g["runtime"] != s.Runtime || g["entrypoint"] != s.Entrypoint || g["description"] != s.Description {
			t.Errorf("template %d = %v, want %+v", i, g, s)
		}
	}
}

// THE CONFORMANCE TEST: every embedded service starter binds or exports `app`,
// the one thing the platform's launcher requires. Read from the entrypoint the
// catalogue names, so a starter whose entrypoint moved is caught too.
var (
	pythonBindsApp = regexp.MustCompile(`(?m)^app\s*(?::[^=]+)?=`)
	nodeExportsApp = regexp.MustCompile(`(?m)^(export default app;|export \{ app \};|export const app\b|module\.exports = \{ app \};)`)
)

func TestEveryStarterExportsApp(t *testing.T) {
	for _, s := range starters.All {
		files, err := s.Files()
		if err != nil {
			t.Fatal(err)
		}
		src, ok := files[s.Entrypoint]
		if !ok {
			t.Errorf("%s: the entrypoint %s is not in the starter", s.Name, s.Entrypoint)
			continue
		}
		re := nodeExportsApp
		if strings.HasPrefix(s.Runtime, "python") {
			re = pythonBindsApp
		}
		if !re.Match(src) {
			t.Errorf("%s: %s does not bind or export `app`", s.Name, s.Entrypoint)
		}
		if _, ok := files["README.md"]; !ok {
			t.Errorf("%s has no README.md", s.Name)
		}
		if strings.HasPrefix(s.Runtime, "node") {
			if _, ok := files["package-lock.json"]; !ok {
				t.Errorf("%s has no package-lock.json: the image installs from it", s.Name)
			}
		}
	}
}
