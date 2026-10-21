package archive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// AetherfyConfig represents the parsed aetherfy.yaml file.
//
// ITS YAML TAGS ARE THE CLI'S FIELD SET. A key in the file that no tag here
// names is refused before upload (CheckUnknownFields), and the set is compared
// with the control plane's AetherfyConfig model wherever that checkout exists
// (TestKnownFieldsEqualTheControlPlaneModel). So every field the server accepts
// has a field here, including the ones the CLI never reads.
type AetherfyConfig struct {
	Runtime    string   `yaml:"runtime"`
	Type       string   `yaml:"type"`
	Regions    []string `yaml:"regions"`
	MemoryMB   int      `yaml:"memory_mb"`
	KeepAlive  bool     `yaml:"keep_alive"`
	Entrypoint string   `yaml:"entrypoint,omitempty"`
	// Schedule is a 5-field UTC cron expression, valid on an agent of either
	// type. The server is the authoritative validator (CP-2) — the CLI never
	// validates cron client-side. Like every field here, a deploy applies the
	// file whole: omitted (or null), the agent has no schedule. It changes
	// with any deploy, so it must NOT get a runtime-style immutability
	// special-case in the diff view.
	Schedule string `yaml:"schedule,omitempty"`

	// Declared so they are KNOWN, not because the CLI reads them. Typed `any`
	// on purpose: the server is the validator of their values, and a Go type
	// here would refuse a value it accepts (a quoted "5" for an int, say)
	// before the server ever saw it.
	IdleTimeoutMinutes any          `yaml:"idle_timeout_minutes,omitempty"`
	GithubDependencies any          `yaml:"github_dependencies,omitempty"`
	Spawn              *SpawnConfig `yaml:"spawn,omitempty"`
}

// SpawnConfig is the `spawn:` block. A struct, not `any`, because its field
// names are checked too: `spawn.workres` is refused like a top-level typo.
type SpawnConfig struct {
	Enabled any `yaml:"enabled,omitempty"`
	Workers any `yaml:"workers,omitempty"`
}

// MissingConfigMessage is the control plane's sentence for a release without
// aetherfy.yaml (shared/config_parser.py MISSING_CONFIG_MESSAGE): an upload
// and a push without one are refused with it. The CLI refuses a deploy before
// uploading, so the server's sentence cannot arrive; this one says the same
// thing, the same way, after the path it looked at, as the push refusal names
// the repo, commit, branch and path it read.
const MissingConfigMessage = "Missing required aetherfy.yaml. Add it beside your code with at least `runtime:`; " +
	"every field it leaves out takes its default."

// MissingConfigError is a folder without aetherfy.yaml.
type MissingConfigError struct{ Path string }

func (e *MissingConfigError) Error() string { return e.Path + ": " + MissingConfigMessage }

// ParseAetherfyConfig reads and parses aetherfy.yaml from the given directory
func ParseAetherfyConfig(dir string) (*AetherfyConfig, error) {
	configPath := filepath.Join(dir, "aetherfy.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &MissingConfigError{Path: configPath}
		}
		return nil, fmt.Errorf("failed to read aetherfy.yaml: %w", err)
	}
	var cfg AetherfyConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse aetherfy.yaml: %w", err)
	}
	// After the decode, so a file that is not YAML at all keeps its own error.
	if err := CheckConfigKeys(data); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// DefaultIgnorePatterns are patterns to ignore by default
var DefaultIgnorePatterns = []string{
	".git",
	".gitignore",
	".env",
	".env.*",
	"__pycache__",
	"*.pyc",
	"*.pyo",
	".pytest_cache",
	".mypy_cache",
	"node_modules",
	".npm",
	"venv",
	".venv",
	"env",
	".DS_Store",
	"Thumbs.db",
	"*.log",
	".afyignore",
}

// shouldIgnore checks if a path should be ignored based on patterns
func shouldIgnore(path string, isDir bool, patterns []string) bool {
	// Normalize path separators
	path = filepath.ToSlash(path)
	base := filepath.Base(path)

	for _, pattern := range patterns {
		// Check exact match on base name
		if matched, _ := filepath.Match(pattern, base); matched {
			return true
		}

		// Check if pattern matches any part of the path
		parts := strings.Split(path, "/")
		for _, part := range parts {
			if matched, _ := filepath.Match(pattern, part); matched {
				return true
			}
		}
	}

	return false
}

// LoadIgnorePatterns reads patterns from .afyignore file
func LoadIgnorePatterns(dir string) ([]string, error) {
	ignoreFile := filepath.Join(dir, ".afyignore")

	data, err := os.ReadFile(ignoreFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No ignore file is fine
		}
		return nil, err
	}

	var patterns []string
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}

	return patterns, nil
}

// ValidateAetherfyConfig checks that aetherfy.yaml exists and contains the
// one field the backend requires: runtime. entrypoint is optional — the
// backend falls back to a runtime-specific default (main.py, index.js,
// main.ts) if not declared. The agent is not named in the file: a folder
// reaches its agent through .aetherfy/agent.json.
func ValidateAetherfyConfig(dir string) error {
	cfg, err := ParseAetherfyConfig(dir)
	if err != nil {
		return err
	}
	var missing []string
	if cfg.Runtime == "" {
		missing = append(missing, "runtime")
	}
	if len(missing) > 0 {
		return fmt.Errorf("aetherfy.yaml is missing required field(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

// ParseAfyIgnore reads patterns from .afyignore file (alias for LoadIgnorePatterns)
func ParseAfyIgnore(dir string) ([]string, error) {
	return LoadIgnorePatterns(dir)
}

// ShouldIgnore checks if a path should be ignored (exported version)
func ShouldIgnore(path string, customPatterns []string) bool {
	allPatterns := append(DefaultIgnorePatterns, customPatterns...)
	return shouldIgnore(path, false, allPatterns)
}
