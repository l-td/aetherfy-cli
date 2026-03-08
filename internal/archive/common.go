package archive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// AetherfyConfig represents the parsed aetherfy.yaml file
type AetherfyConfig struct {
	Name       string   `yaml:"name"`
	Runtime    string   `yaml:"runtime"`
	Type       string   `yaml:"type"`
	Regions    []string `yaml:"regions"`
	MemoryMB   int      `yaml:"memory_mb"`
	KeepAlive  bool     `yaml:"keep_alive"`
	Entrypoint string   `yaml:"entrypoint,omitempty"`
	Workspace  string   `yaml:"workspace,omitempty"`
}

// ParseAetherfyConfig reads and parses aetherfy.yaml from the given directory
func ParseAetherfyConfig(dir string) (*AetherfyConfig, error) {
	configPath := filepath.Join(dir, "aetherfy.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("aetherfy.yaml not found in %s", dir)
		}
		return nil, fmt.Errorf("failed to read aetherfy.yaml: %w", err)
	}
	var cfg AetherfyConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse aetherfy.yaml: %w", err)
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

// ValidateAetherfyConfig checks if aetherfy.yaml exists in directory
func ValidateAetherfyConfig(dir string) error {
	configPath := filepath.Join(dir, "aetherfy.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("aetherfy.yaml not found in %s", dir)
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
