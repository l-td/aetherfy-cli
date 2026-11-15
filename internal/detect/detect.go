package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The languages (and the one mode) a project can be detected as. None is a runtime: the platform
// offers the runtimes, and Resolve turns what a project asks for into one of them.
const (
	Python     = "python"
	Node       = "node"
	Bun        = "bun"
	Dockerfile = "dockerfile"
)

// ProjectHints holds what was auto-detected in a project directory.
type ProjectHints struct {
	Language         string // Python, Node, Bun or Dockerfile; empty when nothing was recognised
	Version          string // the version the project asks for: "3.12" for Python, "22" for Node; empty when it names none
	TypeScript       bool   // Node only: the project is TypeScript, so it runs through tsx
	Entrypoint       string // e.g. "main.py", "index.js", "index.ts" — empty for dockerfile runtime
	HasMastra        bool   // mastra in package.json dependencies
	HasPyprojectToml bool   // pyproject.toml found (uv or PEP 517 project)
	HasUvLock        bool   // uv.lock found alongside pyproject.toml (uv project mode)
}

// Project scans dir and returns detected hints.
func Project(dir string) ProjectHints {
	hints := ProjectHints{}

	// Dockerfile detection — takes highest priority. If a Dockerfile is present
	// the user owns the entire container; entrypoint is irrelevant.
	if fileExists(filepath.Join(dir, "Dockerfile")) {
		hints.Language = Dockerfile
		return hints
	}

	// Python detection — fire on a dependency manifest OR a main.py at
	// the project root. main.py is the conventional aetherfy entrypoint
	// (already special-cased by the entrypoint detection below) and is
	// the same signal real users follow for "small bot / single-script"
	// Python projects that don't need a requirements.txt. Symmetric with
	// existing entrypoint detection — both fire on the same file — so the
	// non-interactive `afy init -y` path doesn't fail on the common bare-
	// main.py project shape. Dependency *resolution* still needs a
	// manifest, surfaced separately by the uv.lock warnings. Node has no
	// symmetric case here because package.json is universal in real Node
	// projects.
	hasPyproject := fileExists(filepath.Join(dir, "pyproject.toml"))
	hasRequirements := fileExists(filepath.Join(dir, "requirements.txt"))
	hasMainPy := fileExists(filepath.Join(dir, "main.py"))
	if hasRequirements || hasPyproject || hasMainPy {
		hints.Language = Python
		hints.Version = PythonVersion(dir)
		if hasMainPy {
			hints.Entrypoint = "main.py"
		}
		if hasPyproject {
			hints.HasPyprojectToml = true
			if fileExists(filepath.Join(dir, "uv.lock")) {
				hints.HasUvLock = true
			}
		}
	}

	// Node / Bun detection — overrides python if both present (unusual but safe)
	jsCandidates := []string{"index.ts", "index.js", "main.ts", "main.js", "src/index.ts", "src/index.js"}
	if fileExists(filepath.Join(dir, "package.json")) {
		hints.Version = ""
		if fileExists(filepath.Join(dir, "bun.lockb")) {
			hints.Language = Bun
		} else {
			hints.Language = Node
			hints.Version = NodeVersion(dir)
		}
		for _, candidate := range jsCandidates {
			if fileExists(filepath.Join(dir, candidate)) {
				hints.Entrypoint = candidate
				break
			}
		}
		hints.HasMastra = hasMastraDep(filepath.Join(dir, "package.json"))
		// TypeScript: if the project is TypeScript (tsconfig.json present, or the resolved
		// entrypoint is a .ts file), Node runs it through `tsx`. Bun is left alone because Bun
		// runs TypeScript natively.
		if hints.Language == Node {
			hints.TypeScript = fileExists(filepath.Join(dir, "tsconfig.json")) || strings.HasSuffix(hints.Entrypoint, ".ts")
		}
	} else if fileExists(filepath.Join(dir, "bunfig.toml")) {
		hints.Language = Bun
		hints.Version = ""
		for _, candidate := range jsCandidates {
			if fileExists(filepath.Join(dir, candidate)) {
				hints.Entrypoint = candidate
				break
			}
		}
	}

	return hints
}

// PythonVersion detects the Python version a project asks for, as "3.<minor>".
// Priority: .python-version → pyproject.toml requires-python → "" (it names none).
func PythonVersion(dir string) string {
	// .python-version (pyenv / asdf)
	if data, err := os.ReadFile(filepath.Join(dir, ".python-version")); err == nil {
		if v := ParsePythonVersion(strings.TrimSpace(string(data))); v != "" {
			return v
		}
	}

	// pyproject.toml — requires-python = ">=3.12"
	if data, err := os.ReadFile(filepath.Join(dir, "pyproject.toml")); err == nil {
		re := regexp.MustCompile(`requires-python\s*=\s*"[^"]*?(\d+\.\d+)`)
		if m := re.FindStringSubmatch(string(data)); len(m) == 2 {
			if v := ParsePythonVersion(m[1]); v != "" {
				return v
			}
		}
	}

	return ""
}

// ParsePythonVersion extracts "3.<minor>" from a raw version string.
// Handles "3.11", "3.11.5", "python3.10", etc. Returns "" when there is none.
func ParsePythonVersion(s string) string {
	re := regexp.MustCompile(`(?:^|[^0-9.])3\.(\d+)`)
	if m := re.FindStringSubmatch(s); len(m) == 2 {
		return "3." + m[1]
	}
	return ""
}

// NodeVersion detects the Node.js major version a project asks for.
// Priority: .nvmrc → .node-version → package.json engines.node → "" (it names none).
func NodeVersion(dir string) string {
	for _, file := range []string{".nvmrc", ".node-version"} {
		if data, err := os.ReadFile(filepath.Join(dir, file)); err == nil {
			if v := ParseNodeVersion(strings.TrimSpace(string(data))); v != "" {
				return v
			}
		}
	}

	// package.json engines.node
	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg map[string]interface{}
		if json.Unmarshal(data, &pkg) == nil {
			if engines, ok := pkg["engines"].(map[string]interface{}); ok {
				if nodeVer, ok := engines["node"].(string); ok {
					if v := ParseNodeVersion(nodeVer); v != "" {
						return v
					}
				}
			}
		}
	}

	return ""
}

// ltsNames are the codenames .nvmrc accepts, each with the Node major it names.
var ltsNames = map[string]string{
	"lts/hydrogen": "18",
	"lts/iron":     "20",
	"lts/jod":      "22",
	"lts/krypton":  "24",
	"lts/lithium":  "26",
}

// ParseNodeVersion extracts the major version from a raw version string.
// Handles "20", "v22.11.0", ">=20.0.0", "^22", "lts/jod". Returns "" when there is none.
func ParseNodeVersion(s string) string {
	if v, ok := ltsNames[strings.ToLower(strings.TrimSpace(s))]; ok {
		return v
	}
	re := regexp.MustCompile(`(\d+)`)
	if m := re.FindStringSubmatch(s); len(m) == 2 {
		return m[1]
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func hasMastraDep(packageJSONPath string) bool {
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return false
	}
	var pkg map[string]interface{}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	for _, key := range []string{"dependencies", "devDependencies"} {
		if deps, ok := pkg[key].(map[string]interface{}); ok {
			if _, found := deps["mastra"]; found {
				return true
			}
		}
	}
	return false
}
