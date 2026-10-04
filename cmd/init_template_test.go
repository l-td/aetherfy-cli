package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/archive"
	"github.com/l-td/aetherfy-cli/internal/starters"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

// runInitCmd drives `afy init` through the root command, exactly as a user
// types it, and resets every init flag afterwards: they are package globals,
// and a value left behind would leak into the next test.
func runInitCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	defer func() {
		initCmd.Flags().VisitAll(func(f *pflag.Flag) {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})
		rootCmd.SetArgs(nil)
	}()
	rootCmd.SetArgs(append([]string{"init"}, args...))
	var err error
	out := captureStdout(t, func() { err = rootCmd.Execute() })
	return out, err
}

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
// the scan that follows prints the framework advice for what was written.
func TestInitTemplateWritesTheStarterAndItsYAML(t *testing.T) {
	for _, s := range starters.All {
		t.Run(s.Name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "my-agent")
			out, err := runInitCmd(t, "--template", s.Name, dir)
			if err != nil {
				t.Fatalf("afy init --template %s: %v\n%s", s.Name, err, out)
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
			if doc["name"] != "my-agent" {
				t.Errorf("name = %v; want the directory's name", doc["name"])
			}
			// DETECTION AGREES WITH THE PRESET: the scan of the written
			// starter reaches the same runtime, so init never prints one
			// runtime and writes another (.python-version / engines.node).
			if !strings.Contains(out, "Detected runtime: "+s.Runtime+"\n") {
				t.Errorf("the scan of %s did not detect %s:\n%s", s.Name, s.Runtime, out)
			}
			if !strings.Contains(out, "in dependencies — ") {
				t.Errorf("no framework advice line after writing %s:\n%s", s.Name, out)
			}
		})
	}
}

// A flag still wins over the starter's preset.
func TestInitTemplateFlagsOverrideThePreset(t *testing.T) {
	dir := t.TempDir()
	if out, err := runInitCmd(t, "--template", "node-ts-hono", "--runtime", "node20-ts", dir); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc := readYAML(t, filepath.Join(dir, "aetherfy.yaml")); doc["runtime"] != "node20-ts" {
		t.Errorf("runtime = %v; --runtime should win over the starter's preset", doc["runtime"])
	}
}

// NOTHING IS OVERWRITTEN, AND NOTHING IS HALF-WRITTEN. One existing file makes
// the whole template refuse, before the first file is created.
func TestInitTemplateRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	mine := []byte("my own README\n")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), mine, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := runInitCmd(t, "--template", "python-fastapi", dir)
	if err == nil || !strings.Contains(err.Error(), "README.md") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v; want a refusal naming README.md and --force", err)
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
	if err := os.WriteFile(filepath.Join(dir, "aetherfy.yaml"), []byte("name: x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := runInitCmd(t, "--template", "node-express", dir); err == nil || !strings.Contains(err.Error(), "aetherfy.yaml") {
		t.Fatalf("err = %v; want a refusal naming aetherfy.yaml", err)
	}
}

func TestInitTemplateForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := runInitCmd(t, "--template", "python-litestar", "--force", dir); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "main.py")); !strings.Contains(string(got), "Litestar") {
		t.Error("--force did not replace main.py")
	}
}

func TestInitTemplateUnknownNameListsTheRealOnes(t *testing.T) {
	_, err := runInitCmd(t, "--template", "python-flask", t.TempDir())
	if err == nil {
		t.Fatal("an unknown template was accepted")
	}
	for _, name := range starters.Names() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the refusal does not name %s: %v", name, err)
		}
	}
}

func TestInitListTemplates(t *testing.T) {
	dir := t.TempDir()
	out, err := runInitCmd(t, "--list-templates", dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range starters.All {
		if !strings.Contains(out, s.Name) || !strings.Contains(out, s.Runtime) {
			t.Errorf("--list-templates does not list %s (%s):\n%s", s.Name, s.Runtime, out)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("--list-templates wrote files: %v", entries)
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
