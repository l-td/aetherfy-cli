package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A runtime the server adds is chosen by its fields (GET /deploy-options: runtime_details), whatever its
// name looks like: none of these names has the shape of any runtime the platform offers today. The
// manifest `afy init` writes always carries the runtime's NAME, never a label, and the commented
// `# entrypoint:` hint is the runtime's own default file.
const newShapeOptions = `{"runtimes":["cpython-316","wb-26","tsx-26","dockerfile"],
	"runtime_labels":{"cpython-316":"CPython Sixteen","wb-26":"WB Twenty-Six","tsx-26":"WB Twenty-Six (TS)","dockerfile":"Dockerfile"},
	"runtime_details":{
		"cpython-316":{"language":"python","version":"3.16","typescript":false,"entrypoint":"app.py"},
		"wb-26":{"language":"node","version":"26","typescript":false,"entrypoint":"serve.js"},
		"tsx-26":{"language":"node","version":"26","typescript":true,"entrypoint":"main.ts"}},
	"default_runtimes":{"python":"cpython-316","node":"wb-26"},
	"regions":[{"code":"eu-central-1","allowed":true}],
	"max_regions":1,
	"memory_mb":[{"memory_mb":512,"allowed":true}],
	"max_memory_mb":1024}`

func TestInitChoosesARuntimeOfANewNameShapeByItsFieldsAndHintsItsOwnFile(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		runtime string
		hint    string
	}{
		{"Python, the version it names", map[string]string{"requirements.txt": "", ".python-version": "3.16\n"}, "cpython-316", "# entrypoint: app.py  #"},
		{"Python, no version named", map[string]string{"requirements.txt": ""}, "cpython-316", "# entrypoint: app.py  #"},
		{"Node, the version it names", map[string]string{"package.json": `{"name":"x"}`, ".nvmrc": "26\n"}, "wb-26", "# entrypoint: serve.js  #"},
		{"TypeScript, no version named", map[string]string{"package.json": `{"name":"x"}`, "tsconfig.json": "{}"}, "tsx-26", "# entrypoint: main.ts  #"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			cp := initCP(t)
			cp.on("GET", "/deploy-options", 200, newShapeOptions)
			for name, body := range c.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			stdout, stderr, err := runAfy(t, cp, "init", dir, "-y", "--name", "invoice-parser")
			if err != nil {
				t.Fatalf("init: %v\n%s%s", err, stdout, stderr)
			}
			got := readFile(t, filepath.Join(dir, "aetherfy.yaml"))
			if !strings.Contains(got, "\nruntime: "+c.runtime+"\n") {
				t.Errorf("the yaml must carry the runtime's NAME %q:\n%s", c.runtime, got)
			}
			if strings.Contains(got, "Sixteen") || strings.Contains(got, "Twenty-Six") {
				t.Errorf("the yaml must never carry a label:\n%s", got)
			}
			if !strings.Contains(got, c.hint) {
				t.Errorf("the entry-file hint must be the runtime's own default file (%q):\n%s", c.hint, got)
			}
		})
	}
}

// A runtime the server lists with no fields (an older server) is still accepted by --runtime; there is no file to hint.
func TestInitWritesNoEntryHintForARuntimeTheServerSendsNoFieldsFor(t *testing.T) {
	dir := t.TempDir()
	cp := initCP(t)
	cp.on("GET", "/deploy-options", 200, strings.Replace(newShapeOptions, `"runtimes":["cpython-316",`, `"runtimes":["bare-1","cpython-316",`, 1))
	if _, stderr, err := runAfy(t, cp, "init", dir, "-y", "--name", "invoice-parser", "--runtime", "bare-1"); err != nil {
		t.Fatalf("a runtime the server lists must be accepted: %v\n%s", err, stderr)
	}
	got := readFile(t, filepath.Join(dir, "aetherfy.yaml"))
	if !strings.Contains(got, "\nruntime: bare-1\n") {
		t.Errorf("the yaml must carry the runtime:\n%s", got)
	}
	if strings.Contains(got, "# entrypoint:") {
		t.Errorf("there is no default file to hint:\n%s", got)
	}
}
