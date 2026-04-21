package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ProjectHints holds what was auto-detected in a project directory.
type ProjectHints struct {
	Runtime          string // e.g. "python3.11", "python3.12", "python3.13", "node20", "node22", "node20-ts", "node22-ts", "bun", "dockerfile"
	Entrypoint       string // e.g. "main.py", "index.js", "index.ts" — empty for dockerfile runtime
	VectorDB         bool   // qdrant_memory folder found
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
		hints.Runtime = "dockerfile"
		if dirExists(filepath.Join(dir, "qdrant_memory")) {
			hints.VectorDB = true
		}
		return hints
	}

	// Python detection
	hasPyproject := fileExists(filepath.Join(dir, "pyproject.toml"))
	if fileExists(filepath.Join(dir, "requirements.txt")) || hasPyproject {
		hints.Runtime = PythonVersion(dir)
		if fileExists(filepath.Join(dir, "main.py")) {
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
		if fileExists(filepath.Join(dir, "bun.lockb")) {
			hints.Runtime = "bun"
		} else {
			hints.Runtime = NodeVersion(dir)
		}
		for _, candidate := range jsCandidates {
			if fileExists(filepath.Join(dir, candidate)) {
				hints.Entrypoint = candidate
				break
			}
		}
		hints.HasMastra = hasMastraDep(filepath.Join(dir, "package.json"))
		// TypeScript promotion for Node: if the project is TypeScript (tsconfig.json
		// present, or the resolved entrypoint is a .ts file), switch node20→node20-ts
		// and node22→node22-ts so the backend runs it through `tsx`. Bun is left alone
		// because Bun runs TypeScript natively.
		if hints.Runtime == "node20" || hints.Runtime == "node22" {
			if fileExists(filepath.Join(dir, "tsconfig.json")) || strings.HasSuffix(hints.Entrypoint, ".ts") {
				hints.Runtime = hints.Runtime + "-ts"
			}
		}
	} else if fileExists(filepath.Join(dir, "bunfig.toml")) {
		hints.Runtime = "bun"
		for _, candidate := range jsCandidates {
			if fileExists(filepath.Join(dir, candidate)) {
				hints.Entrypoint = candidate
				break
			}
		}
	}

	if dirExists(filepath.Join(dir, "qdrant_memory")) {
		hints.VectorDB = true
	}

	return hints
}

// PythonVersion detects the Python version from project files.
// Priority: .python-version → pyproject.toml requires-python → "python3.11"
func PythonVersion(dir string) string {
	// .python-version (pyenv / asdf)
	if data, err := os.ReadFile(filepath.Join(dir, ".python-version")); err == nil {
		if v := ParsePythonVersion(strings.TrimSpace(string(data))); v != "" {
			return v
		}
	}

	// pyproject.toml — requires-python = ">=3.10"
	if data, err := os.ReadFile(filepath.Join(dir, "pyproject.toml")); err == nil {
		re := regexp.MustCompile(`requires-python\s*=\s*"[^"]*?(\d+)\.(\d+)`)
		if m := re.FindStringSubmatch(string(data)); len(m) == 3 {
			if minor, err := strconv.Atoi(m[2]); err == nil {
				if v := SupportedPythonRuntime(minor); v != "" {
					return v
				}
			}
		}
	}

	return "python3.11"
}

// ParsePythonVersion extracts a supported runtime string from a raw version string.
// Handles "3.11", "3.11.5", "python3.10", etc.
func ParsePythonVersion(s string) string {
	re := regexp.MustCompile(`3\.(\d+)`)
	if m := re.FindStringSubmatch(s); len(m) == 2 {
		if minor, err := strconv.Atoi(m[1]); err == nil {
			return SupportedPythonRuntime(minor)
		}
	}
	return ""
}

// SupportedPythonRuntime maps a minor version integer to a runtime string.
// Returns "" for versions below 3.11 (unsupported by the platform).
func SupportedPythonRuntime(minor int) string {
	switch {
	case minor == 11:
		return "python3.11"
	case minor == 12:
		return "python3.12"
	case minor >= 13:
		// Future versions fall back to the latest known supported
		return "python3.13"
	}
	return ""
}

// NodeVersion detects the Node.js version from project files.
// Priority: .nvmrc → .node-version → package.json engines.node → "node20"
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

	return "node20"
}

// ParseNodeVersion extracts a supported runtime string from a raw version string.
// Handles "20", "v22.11.0", ">=20.0.0", "^22", "lts/iron", "lts/jod".
// Node.js odd releases (19, 21, 23) are not LTS and are not supported.
func ParseNodeVersion(s string) string {
	ltsNames := map[string]string{
		"lts/iron": "node20", // Node.js 20 (Active LTS)
		"lts/jod":  "node22", // Node.js 22 (Active LTS)
	}
	if v, ok := ltsNames[strings.ToLower(strings.TrimSpace(s))]; ok {
		return v
	}

	re := regexp.MustCompile(`(\d+)`)
	if m := re.FindStringSubmatch(s); len(m) == 2 {
		if major, err := strconv.Atoi(m[1]); err == nil {
			switch {
			case major >= 22:
				return "node22"
			case major == 20:
				return "node20"
			// 21 is an odd/non-LTS release — not supported, caller falls back to default
			}
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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
