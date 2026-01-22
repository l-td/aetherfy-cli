package test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aetherfy/cli/internal/archive"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateTarball(t *testing.T) {
	// Create temp directory with test files
	tmpDir := t.TempDir()

	// Create test files
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.py"), []byte("print('hello')"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "aetherfy.yaml"), []byte("runtime: python3.11"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "src"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "src", "utils.py"), []byte("# utils"), 0644))

	// Create archive
	data, err := archive.CreateTarballWithValidation(tmpDir)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Verify it's a valid gzipped tarball
	fileNames := extractTarballFileNames(t, data)

	assert.Contains(t, fileNames, "main.py")
	assert.Contains(t, fileNames, "aetherfy.yaml")
	assert.Contains(t, fileNames, "src/utils.py")
}

func TestCreateTarball_ExcludesIgnored(t *testing.T) {
	tmpDir := t.TempDir()

	// Create files including ones that should be ignored
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.py"), []byte("print('hello')"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "aetherfy.yaml"), []byte("runtime: python3.11"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".git", "config"), []byte("git config"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "__pycache__"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "__pycache__", "main.pyc"), []byte("bytecode"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".env"), []byte("SECRET=xxx"), 0644))

	data, err := archive.CreateTarballWithValidation(tmpDir)
	require.NoError(t, err)

	fileNames := extractTarballFileNames(t, data)

	// Should include
	assert.Contains(t, fileNames, "main.py")
	assert.Contains(t, fileNames, "aetherfy.yaml")

	// Should exclude
	for _, name := range fileNames {
		assert.NotContains(t, name, ".git")
		assert.NotContains(t, name, "__pycache__")
		assert.NotEqual(t, ".env", name)
	}
}

func TestCreateTarball_EmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := archive.CreateTarballWithValidation(tmpDir)
	assert.Error(t, err) // Should fail - no aetherfy.yaml
}

func TestCreateTarball_MissingAetherfyYaml(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.py"), []byte("print('hello')"), 0644))

	_, err := archive.CreateTarballWithValidation(tmpDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "aetherfy.yaml")
}

func TestAfyIgnore_Parse(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .afyignore
	ignoreContent := `
# Comment
*.log
temp/
secrets.yaml
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".afyignore"), []byte(ignoreContent), 0644))

	patterns, err := archive.ParseAfyIgnore(tmpDir)
	require.NoError(t, err)

	assert.Contains(t, patterns, "*.log")
	assert.Contains(t, patterns, "temp/")
	assert.Contains(t, patterns, "secrets.yaml")
	assert.NotContains(t, patterns, "# Comment")
}

func TestAfyIgnore_NoFile(t *testing.T) {
	tmpDir := t.TempDir()

	patterns, err := archive.ParseAfyIgnore(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, patterns) // No error, just empty
}

func TestShouldIgnore(t *testing.T) {
	tests := []struct {
		path     string
		patterns []string
		want     bool
	}{
		{"main.py", []string{}, false},
		{"debug.log", []string{"*.log"}, true},
		{"temp/file.txt", []string{"temp"}, true},      // Match dir name without slash
		{"src/temp/file.txt", []string{"temp"}, true},  // Match nested dir
		{".git/config", []string{}, true},              // Always ignored
		{"__pycache__/main.pyc", []string{}, true},     // Always ignored
		{".env", []string{}, true},                     // Always ignored
		{"node_modules/pkg/index.js", []string{}, true}, // Always ignored
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := archive.ShouldIgnore(tt.path, tt.patterns)
			assert.Equal(t, tt.want, got)
		})
	}
}

// extractTarballFileNames extracts file names from a gzipped tarball
func extractTarballFileNames(t *testing.T, data []byte) []string {
	t.Helper()

	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	var fileNames []string

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		fileNames = append(fileNames, header.Name)
	}

	return fileNames
}
