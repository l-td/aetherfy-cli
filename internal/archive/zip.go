package archive

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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

// ZipDirectory creates a ZIP archive of a directory
func ZipDirectory(sourceDir string, ignorePatterns []string) ([]byte, error) {
	// Combine default and custom ignore patterns
	allPatterns := append(DefaultIgnorePatterns, ignorePatterns...)

	buf := new(bytes.Buffer)
	writer := zip.NewWriter(buf)

	// Walk the directory
	err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		// Skip the root directory itself
		if relPath == "." {
			return nil
		}

		// Check if should be ignored
		if shouldIgnore(relPath, info.IsDir(), allPatterns) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Create header
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		// Use forward slashes in ZIP
		header.Name = filepath.ToSlash(relPath)
		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		// Create entry in ZIP
		w, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}

		// Write file content
		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			if _, err := io.Copy(w, file); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk directory: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close zip writer: %w", err)
	}

	return buf.Bytes(), nil
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

// CreateZip creates a ZIP archive of a directory with validation
func CreateZip(sourceDir string) ([]byte, error) {
	// Validate aetherfy.yaml exists
	if err := ValidateAetherfyConfig(sourceDir); err != nil {
		return nil, err
	}

	// Load custom ignore patterns
	patterns, err := LoadIgnorePatterns(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load .afyignore: %w", err)
	}

	return ZipDirectory(sourceDir, patterns)
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
