package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/l-td/aetherfy-cli/internal/detect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── ParsePythonVersion ───────────────────────────────────────────────────────

func TestParsePythonVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"3.11", "3.11"},
		{"3.11.5", "3.11"},
		{"3.12.3", "3.12"},
		{"3.14", "3.14"},
		{"python3.12", "3.12"},
		// A version is read as it is written, whether or not the platform offers it:
		// Resolve is what maps it onto a runtime.
		{"3.10", "3.10"},
		{"3.9", "3.9"},
		{"3.99", "3.99"},
		{"2.7", ""},
		{"13.5", ""}, // a 3.5 inside another number is not a Python version
		{"invalid", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, detect.ParsePythonVersion(tt.input))
		})
	}
}

// ─── PythonVersion (directory scan) ──────────────────────────────────────────

func TestPythonVersion_PythonVersionFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".python-version"), []byte("3.12.3\n"), 0644))
	assert.Equal(t, "3.12", detect.PythonVersion(dir))
}

func TestPythonVersion_PythonVersionFilePriority(t *testing.T) {
	// .python-version takes priority over pyproject.toml
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".python-version"), []byte("3.12\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(`[project]
requires-python = ">=3.13"
`), 0644))
	assert.Equal(t, "3.12", detect.PythonVersion(dir))
}

func TestPythonVersion_PyprojectToml(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(`[project]
name = "my-app"
requires-python = ">=3.12"
`), 0644))
	assert.Equal(t, "3.12", detect.PythonVersion(dir))
}

func TestPythonVersion_PyprojectTomlOlderThanAnyOffered(t *testing.T) {
	// requires-python = ">=3.10" asks for 3.10: detection reads it, Resolve moves it up.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(`[project]
requires-python = ">=3.10"
`), 0644))
	assert.Equal(t, "3.10", detect.PythonVersion(dir))
}

func TestPythonVersion_NamesNone(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, "", detect.PythonVersion(dir))
}

// ─── ParseNodeVersion ─────────────────────────────────────────────────────────

func TestParseNodeVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Plain integers
		{"22", "22"},
		{"24", "24"},
		// v-prefixed
		{"v22.3.0", "22"},
		{"v24.11.0", "24"},
		// Range constraints
		{">=22.0.0", "22"},
		{"^24", "24"},
		// LTS names, case-insensitive
		{"lts/jod", "22"},
		{"lts/krypton", "24"},
		{"LTS/Lithium", "26"},
		{"lts/iron", "20"},
		{"lts/hydrogen", "18"},
		// A version is read as it is written, whether or not the platform offers it
		{"20", "20"},
		{"21", "21"},
		{"99", "99"},
		{"invalid", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, detect.ParseNodeVersion(tt.input))
		})
	}
}

// ─── NodeVersion (directory scan) ────────────────────────────────────────────

func TestNodeVersion_Nvmrc(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("24\n"), 0644))
	assert.Equal(t, "24", detect.NodeVersion(dir))
}

func TestNodeVersion_NvmrcVPrefixed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("v22.3.0\n"), 0644))
	assert.Equal(t, "22", detect.NodeVersion(dir))
}

func TestNodeVersion_NvmrcLTSNameKrypton(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("lts/krypton\n"), 0644))
	assert.Equal(t, "24", detect.NodeVersion(dir))
}

func TestNodeVersion_NvmrcGarbageFallsThrough(t *testing.T) {
	// An .nvmrc that names no version is skipped, and the next source is read.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("latest\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".node-version"), []byte("22\n"), 0644))
	assert.Equal(t, "22", detect.NodeVersion(dir))
}

func TestNodeVersion_NodeVersionFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".node-version"), []byte("22\n"), 0644))
	assert.Equal(t, "22", detect.NodeVersion(dir))
}

func TestNodeVersion_NvmrcPriority(t *testing.T) {
	// .nvmrc takes priority over .node-version
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("24\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".node-version"), []byte("22\n"), 0644))
	assert.Equal(t, "24", detect.NodeVersion(dir))
}

func TestNodeVersion_PackageJsonEngines(t *testing.T) {
	dir := t.TempDir()
	pkg := map[string]interface{}{
		"name":    "my-app",
		"engines": map[string]interface{}{"node": ">=22.0.0"},
	}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	assert.Equal(t, "22", detect.NodeVersion(dir))
}

func TestNodeVersion_NamesNone(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, "", detect.NodeVersion(dir))
}

// ─── Project (full scan) ──────────────────────────────────────────────────────

func TestProject_PythonWithMainPy(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("requests\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.py"), []byte("print('hi')"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".python-version"), []byte("3.12\n"), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Python, h.Language)
	assert.Equal(t, "3.12", h.Version)
	assert.Equal(t, "main.py", h.Entrypoint)
	assert.False(t, h.HasMastra)
}

// Bare-script Python: just main.py, no dependency manifest. Common
// project shape for small bots / single-file agents — `afy init -y`
// must detect this so it doesn't fail with "could not detect runtime"
// on the non-interactive path.
func TestProject_PythonBareMainPyNoManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.py"), []byte("print('hi')"), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Python, h.Language)
	assert.Equal(t, "", h.Version)
	assert.Equal(t, "main.py", h.Entrypoint)
	assert.False(t, h.HasPyprojectToml)
	assert.False(t, h.HasUvLock)
}

// A stray .py file with a non-conventional name should NOT trigger
// Python detection — only main.py counts as a project signal. This
// keeps the runtime detector symmetric with the entrypoint detector
// (which also only recognizes main.py) and avoids false-positives on
// directories that happen to contain a one-off Python utility script.
func TestProject_PythonNonMainPyAlone(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "script.py"), []byte("print('hi')"), 0644))

	h := detect.Project(dir)
	assert.Empty(t, h.Language, "nothing was recognised")
	assert.Empty(t, h.Entrypoint)
}

func TestProject_NodeWithIndexJs(t *testing.T) {
	dir := t.TempDir()
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.js"), []byte(""), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("22\n"), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Node, h.Language)
	assert.Equal(t, "22", h.Version)
	assert.False(t, h.TypeScript)
	assert.Equal(t, "index.js", h.Entrypoint)
}

func TestProject_BunWithIndexTs(t *testing.T) {
	dir := t.TempDir()
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bun.lockb"), []byte(""), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.ts"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Bun, h.Language)
	assert.Equal(t, "", h.Version)
	assert.Equal(t, "index.ts", h.Entrypoint)
}

func TestProject_BunViaBunfigToml(t *testing.T) {
	// bunfig.toml without package.json — zero-dep Bun project
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bunfig.toml"), []byte("[install]\nproduction = true\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.ts"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Bun, h.Language)
	assert.Equal(t, "", h.Version)
	assert.Equal(t, "index.ts", h.Entrypoint)
}

func TestProject_BunBunfigWithMainTs(t *testing.T) {
	// bunfig.toml + main.ts (no index.ts) → detects main.ts as entrypoint
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bunfig.toml"), []byte(""), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.ts"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Bun, h.Language)
	assert.Equal(t, "", h.Version)
	assert.Equal(t, "main.ts", h.Entrypoint)
}

func TestProject_NodeWithMainTs(t *testing.T) {
	// package.json + main.ts (no index.ts/index.js) → detects main.ts and
	// marks the project TypeScript because the entrypoint is.
	dir := t.TempDir()
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.ts"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Node, h.Language)
	assert.Equal(t, "", h.Version)
	assert.True(t, h.TypeScript)
	assert.Equal(t, "main.ts", h.Entrypoint)
}

func TestProject_NodeTsViaIndexTs(t *testing.T) {
	// package.json + index.ts → TypeScript Node, no version named
	dir := t.TempDir()
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.ts"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Node, h.Language)
	assert.Equal(t, "", h.Version)
	assert.True(t, h.TypeScript)
	assert.Equal(t, "index.ts", h.Entrypoint)
}

func TestProject_Node22TsViaNvmrc(t *testing.T) {
	// .nvmrc pins 22, index.ts present → TypeScript Node 22
	dir := t.TempDir()
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("22\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.ts"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Node, h.Language)
	assert.Equal(t, "22", h.Version)
	assert.True(t, h.TypeScript)
	assert.Equal(t, "index.ts", h.Entrypoint)
}

func TestProject_NodeTsViaTsconfigWithJsEntrypoint(t *testing.T) {
	// tsconfig.json present but user wrote plain index.js — still promote to
	// TypeScript Node because the project is declared TypeScript.
	dir := t.TempDir()
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte("{}"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.js"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Node, h.Language)
	assert.Equal(t, "", h.Version)
	assert.True(t, h.TypeScript)
	assert.Equal(t, "index.js", h.Entrypoint)
}

func TestProject_BunNotPromotedByTsconfig(t *testing.T) {
	// Bun runs TypeScript natively — don't promote to any "-ts" variant.
	dir := t.TempDir()
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bun.lockb"), []byte(""), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte("{}"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.ts"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Bun, h.Language)
	assert.Equal(t, "", h.Version)
	assert.Equal(t, "index.ts", h.Entrypoint)
}

func TestProject_IndexTsTakesPriorityOverMainTs(t *testing.T) {
	// When both index.ts and main.ts exist, index.ts wins
	dir := t.TempDir()
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bun.lockb"), []byte(""), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.ts"), []byte(""), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.ts"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Bun, h.Language)
	assert.Equal(t, "", h.Version)
	assert.Equal(t, "index.ts", h.Entrypoint)
}

func TestProject_PackageJsonTakesPriorityOverBunfig(t *testing.T) {
	// package.json (no bun.lockb) + bunfig.toml → Node, not Bun
	// (package.json path checks for bun.lockb; without it, it's Node)
	dir := t.TempDir()
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bunfig.toml"), []byte(""), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.js"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Node, h.Language)
	assert.Equal(t, "", h.Version)
	assert.False(t, h.TypeScript)
	assert.Equal(t, "index.js", h.Entrypoint)
}

func TestProject_HasMastra(t *testing.T) {
	dir := t.TempDir()
	pkg := map[string]interface{}{
		"name":         "test",
		"dependencies": map[string]interface{}{"mastra": "^0.1.0"},
	}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))

	h := detect.Project(dir)
	assert.True(t, h.HasMastra)
}

func TestProject_Dockerfile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\n"), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Dockerfile, h.Language)
	assert.Equal(t, "", h.Version)
	assert.Equal(t, "", h.Entrypoint) // entrypoint is irrelevant for dockerfile runtime
}

func TestProject_DockerfileTakesPriorityOverPython(t *testing.T) {
	// If a Dockerfile is present alongside Python files, dockerfile wins
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM python:3.12\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("requests\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.py"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Dockerfile, h.Language)
	assert.Equal(t, "", h.Version)
	assert.Equal(t, "", h.Entrypoint)
}

func TestProject_DockerfileTakesPriorityOverNode(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM node:22\n"), 0644))
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Dockerfile, h.Language)
	assert.Equal(t, "", h.Version)
	assert.Equal(t, "", h.Entrypoint)
}

func TestProject_Empty(t *testing.T) {
	dir := t.TempDir()
	h := detect.Project(dir)
	assert.Empty(t, h.Language, "nothing was recognised")
	assert.Equal(t, "", h.Entrypoint)
	assert.False(t, h.HasMastra)
	assert.False(t, h.HasUvLock)
}

// ─── HasUvLock detection ──────────────────────────────────────────────────────

func TestProject_PythonWithUvLock(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(`[project]
name = "my-app"
requires-python = ">=3.12"
dependencies = []
`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "uv.lock"), []byte("version = 1\nrequires-python = \">=3.12\"\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.py"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Python, h.Language)
	assert.Equal(t, "3.12", h.Version)
	assert.Equal(t, "main.py", h.Entrypoint)
	assert.True(t, h.HasPyprojectToml)
	assert.True(t, h.HasUvLock)
}

func TestProject_PythonWithPyprojectButNoUvLock(t *testing.T) {
	// pyproject.toml without uv.lock → HasPyprojectToml=true, HasUvLock=false
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(`[project]
name = "my-app"
requires-python = ">=3.11"
`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.py"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Python, h.Language)
	assert.Equal(t, "3.11", h.Version)
	assert.True(t, h.HasPyprojectToml)
	assert.False(t, h.HasUvLock)
}

func TestProject_PythonRequirementsTxtNoUvLock(t *testing.T) {
	// requirements.txt only → HasPyprojectToml=false, HasUvLock=false
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("requests\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.py"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, detect.Python, h.Language)
	assert.Equal(t, "", h.Version)
	assert.False(t, h.HasPyprojectToml)
	assert.False(t, h.HasUvLock)
}

func TestProject_UvLockWithoutPyprojectToml(t *testing.T) {
	// uv.lock present but no pyproject.toml → HasUvLock must be false
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("requests\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "uv.lock"), []byte("version = 1\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.py"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.False(t, h.HasPyprojectToml)
	assert.False(t, h.HasUvLock)
}

func TestProject_NodeProjectHasNoPythonFlags(t *testing.T) {
	// Node / Bun projects never have Python-specific flags set
	dir := t.TempDir()
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.js"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.False(t, h.HasPyprojectToml)
	assert.False(t, h.HasUvLock)
}

func TestProject_EntrypointPriority(t *testing.T) {
	// index.ts should be preferred over index.js when both exist
	dir := t.TempDir()
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.ts"), []byte(""), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.js"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, "index.ts", h.Entrypoint)
}
