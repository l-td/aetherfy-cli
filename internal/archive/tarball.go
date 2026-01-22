package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CreateTarball creates a gzipped tarball (.tar.gz) of a directory
func CreateTarball(sourceDir string, ignorePatterns []string) ([]byte, error) {
	// Combine default and custom ignore patterns
	allPatterns := append(DefaultIgnorePatterns, ignorePatterns...)

	buf := new(bytes.Buffer)
	gzipWriter := gzip.NewWriter(buf)
	tarWriter := tar.NewWriter(gzipWriter)

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

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		// Use forward slashes and relative path
		header.Name = filepath.ToSlash(relPath)

		// Write header
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		// Write file content (skip directories)
		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			if _, err := io.Copy(tarWriter, file); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk directory: %w", err)
	}

	// Close writers in correct order
	if err := tarWriter.Close(); err != nil {
		return nil, fmt.Errorf("failed to close tar writer: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, fmt.Errorf("failed to close gzip writer: %w", err)
	}

	return buf.Bytes(), nil
}

// CreateTarballWithValidation creates a tarball with aetherfy.yaml validation
func CreateTarballWithValidation(sourceDir string) ([]byte, error) {
	// Validate aetherfy.yaml exists
	if err := ValidateAetherfyConfig(sourceDir); err != nil {
		return nil, err
	}

	// Load custom ignore patterns
	patterns, err := LoadIgnorePatterns(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load .afyignore: %w", err)
	}

	return CreateTarball(sourceDir, patterns)
}
