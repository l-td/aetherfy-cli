package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aetherfy/cli/internal/detect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── ParsePythonVersion ───────────────────────────────────────────────────────

func TestParsePythonVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"3.11", "python3.11"},
		{"3.11.5", "python3.11"},
		{"3.12", "python3.12"},
		{"3.12.3", "python3.12"},
		{"3.13", "python3.13"},
		{"3.13.1", "python3.13"},
		{"python3.12", "python3.12"},
		// Future version → falls back to 3.13
		{"3.15", "python3.13"},
		{"3.14", "python3.13"},
		// Unsupported (below 3.11)
		{"3.10", ""},
		{"3.9", ""},
		{"2.7", ""},
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
	assert.Equal(t, "python3.12", detect.PythonVersion(dir))
}

func TestPythonVersion_PythonVersionFilePriority(t *testing.T) {
	// .python-version takes priority over pyproject.toml
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".python-version"), []byte("3.12\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(`[project]
requires-python = ">=3.13"
`), 0644))
	assert.Equal(t, "python3.12", detect.PythonVersion(dir))
}

func TestPythonVersion_PyprojectToml(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(`[project]
name = "my-app"
requires-python = ">=3.12"
`), 0644))
	assert.Equal(t, "python3.12", detect.PythonVersion(dir))
}

func TestPythonVersion_PyprojectTomlUnsupportedVersion(t *testing.T) {
	// requires-python = ">=3.10" — 3.10 not supported → falls back to default 3.11
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(`[project]
requires-python = ">=3.10"
`), 0644))
	assert.Equal(t, "python3.11", detect.PythonVersion(dir))
}

func TestPythonVersion_Default(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, "python3.11", detect.PythonVersion(dir))
}

// ─── ParseNodeVersion ─────────────────────────────────────────────────────────

func TestParseNodeVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Plain integers
		{"20", "node20"},
		{"22", "node22"},
		// v-prefixed
		{"v20.11.0", "node20"},
		{"v22.3.0", "node22"},
		// Range constraints
		{">=20.0.0", "node20"},
		{">=22.0.0", "node22"},
		{"^20", "node20"},
		{"^22", "node22"},
		// LTS names
		{"lts/iron", "node20"},
		{"lts/jod", "node22"},
		{"LTS/Iron", "node20"}, // case-insensitive
		{"LTS/Jod", "node22"},
		// Future version → node22
		{"24", "node22"},
		// Unsupported: odd non-LTS versions, legacy, garbage
		{"18", ""},
		{"19", ""},
		{"21", ""},
		{"16", ""},
		{"lts/hydrogen", ""}, // Node 18 — EOL, not supported
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

func TestNodeVersion_Nvmrc_Node20(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("20\n"), 0644))
	assert.Equal(t, "node20", detect.NodeVersion(dir))
}

func TestNodeVersion_Nvmrc_Node22(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("22\n"), 0644))
	assert.Equal(t, "node22", detect.NodeVersion(dir))
}

func TestNodeVersion_NvmrcVPrefixed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("v22.3.0\n"), 0644))
	assert.Equal(t, "node22", detect.NodeVersion(dir))
}

func TestNodeVersion_NvmrcLTSNameJod(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("lts/jod\n"), 0644))
	assert.Equal(t, "node22", detect.NodeVersion(dir))
}

func TestNodeVersion_NvmrcUnsupportedFallsBackToDefault(t *testing.T) {
	// Node 18 is EOL / not supported — falls back to node20 default
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("18\n"), 0644))
	assert.Equal(t, "node20", detect.NodeVersion(dir))
}

func TestNodeVersion_NodeVersionFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".node-version"), []byte("22\n"), 0644))
	assert.Equal(t, "node22", detect.NodeVersion(dir))
}

func TestNodeVersion_NvmrcPriority(t *testing.T) {
	// .nvmrc takes priority over .node-version
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("20\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".node-version"), []byte("22\n"), 0644))
	assert.Equal(t, "node20", detect.NodeVersion(dir))
}

func TestNodeVersion_PackageJsonEngines(t *testing.T) {
	dir := t.TempDir()
	pkg := map[string]interface{}{
		"name":    "my-app",
		"engines": map[string]interface{}{"node": ">=22.0.0"},
	}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	assert.Equal(t, "node22", detect.NodeVersion(dir))
}

func TestNodeVersion_Default(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, "node20", detect.NodeVersion(dir))
}

// ─── Project (full scan) ──────────────────────────────────────────────────────

func TestProject_PythonWithMainPy(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("requests\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.py"), []byte("print('hi')"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".python-version"), []byte("3.12\n"), 0644))

	h := detect.Project(dir)
	assert.Equal(t, "python3.12", h.Runtime)
	assert.Equal(t, "main.py", h.Entrypoint)
	assert.False(t, h.VectorDB)
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
	assert.Equal(t, "python3.11", h.Runtime, "main.py alone should signal Python (default version)")
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
	assert.Empty(t, h.Runtime, "a non-main.py file should not signal Python without a manifest")
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
	assert.Equal(t, "node22", h.Runtime)
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
	assert.Equal(t, "bun", h.Runtime)
	assert.Equal(t, "index.ts", h.Entrypoint)
}

func TestProject_BunViaBunfigToml(t *testing.T) {
	// bunfig.toml without package.json — zero-dep Bun project
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bunfig.toml"), []byte("[install]\nproduction = true\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.ts"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, "bun", h.Runtime)
	assert.Equal(t, "index.ts", h.Entrypoint)
}

func TestProject_BunBunfigWithMainTs(t *testing.T) {
	// bunfig.toml + main.ts (no index.ts) → detects main.ts as entrypoint
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bunfig.toml"), []byte(""), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.ts"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, "bun", h.Runtime)
	assert.Equal(t, "main.ts", h.Entrypoint)
}

func TestProject_NodeWithMainTs(t *testing.T) {
	// package.json + main.ts (no index.ts/index.js) → detects main.ts and
	// promotes node20 → node20-ts because the entrypoint is TypeScript.
	dir := t.TempDir()
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.ts"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, "node20-ts", h.Runtime)
	assert.Equal(t, "main.ts", h.Entrypoint)
}

func TestProject_NodeTsViaIndexTs(t *testing.T) {
	// package.json + index.ts → node20-ts (default node major + TS promotion)
	dir := t.TempDir()
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.ts"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, "node20-ts", h.Runtime)
	assert.Equal(t, "index.ts", h.Entrypoint)
}

func TestProject_Node22TsViaNvmrc(t *testing.T) {
	// .nvmrc pins node22, index.ts present → node22-ts
	dir := t.TempDir()
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".nvmrc"), []byte("22\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.ts"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, "node22-ts", h.Runtime)
	assert.Equal(t, "index.ts", h.Entrypoint)
}

func TestProject_NodeTsViaTsconfigWithJsEntrypoint(t *testing.T) {
	// tsconfig.json present but user wrote plain index.js — still promote to
	// node20-ts because the project is declared TypeScript.
	dir := t.TempDir()
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte("{}"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.js"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, "node20-ts", h.Runtime)
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
	assert.Equal(t, "bun", h.Runtime)
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
	assert.Equal(t, "bun", h.Runtime)
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
	assert.Equal(t, "node20", h.Runtime)
	assert.Equal(t, "index.js", h.Entrypoint)
}

func TestProject_VectorDB(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "qdrant_memory"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("qdrant-client\n"), 0644))

	h := detect.Project(dir)
	assert.True(t, h.VectorDB)
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
	assert.Equal(t, "dockerfile", h.Runtime)
	assert.Equal(t, "", h.Entrypoint) // entrypoint is irrelevant for dockerfile runtime
	assert.False(t, h.VectorDB)
}

func TestProject_DockerfileTakesPriorityOverPython(t *testing.T) {
	// If a Dockerfile is present alongside Python files, dockerfile wins
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM python:3.12\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("requests\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.py"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, "dockerfile", h.Runtime)
	assert.Equal(t, "", h.Entrypoint)
}

func TestProject_DockerfileTakesPriorityOverNode(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM node:22\n"), 0644))
	pkg := map[string]interface{}{"name": "test"}
	data, _ := json.Marshal(pkg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0644))

	h := detect.Project(dir)
	assert.Equal(t, "dockerfile", h.Runtime)
	assert.Equal(t, "", h.Entrypoint)
}

func TestProject_DockerfileWithVectorDB(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "qdrant_memory"), 0755))

	h := detect.Project(dir)
	assert.Equal(t, "dockerfile", h.Runtime)
	assert.True(t, h.VectorDB)
}

func TestProject_Empty(t *testing.T) {
	dir := t.TempDir()
	h := detect.Project(dir)
	assert.Equal(t, "", h.Runtime)
	assert.Equal(t, "", h.Entrypoint)
	assert.False(t, h.VectorDB)
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
	assert.Equal(t, "python3.12", h.Runtime)
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
	assert.Equal(t, "python3.11", h.Runtime)
	assert.True(t, h.HasPyprojectToml)
	assert.False(t, h.HasUvLock)
}

func TestProject_PythonRequirementsTxtNoUvLock(t *testing.T) {
	// requirements.txt only → HasPyprojectToml=false, HasUvLock=false
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("requests\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.py"), []byte(""), 0644))

	h := detect.Project(dir)
	assert.Equal(t, "python3.11", h.Runtime)
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
