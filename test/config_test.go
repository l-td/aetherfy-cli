package test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aetherfy/cli/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()

	assert.Equal(t, "https://api.aetherfy.run/api/v1", cfg.APIURL)
	assert.Equal(t, "iad", cfg.DefaultRegion)
	assert.Equal(t, "text", cfg.OutputFormat)
	assert.False(t, cfg.NoColor)
	assert.False(t, cfg.Verbose)
}

func TestConfigDir(t *testing.T) {
	dir := config.ConfigDir()
	assert.NotEmpty(t, dir)
	assert.True(t, filepath.IsAbs(dir) || dir == ".aetherfy")
}

func TestConfigPath(t *testing.T) {
	path := config.ConfigPath()
	assert.Contains(t, path, "config.yaml")
}

func TestCredentialsPath(t *testing.T) {
	path := config.CredentialsPath()
	assert.Contains(t, path, "credentials.yaml")
}

func TestValidateAPIKey(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"valid live key", "afy_live_abcdefghijklmnopqrstuvwxyz123456", false},
		{"valid test key", "afy_test_abcdefghijklmnopqrstuvwxyz123456", false},
		{"empty key", "", true},
		{"invalid prefix", "invalid_key_12345678901234567890123456", true},
		{"too short", "afy_live_short", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := config.ValidateAPIKey(tt.key)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMaskAPIKey(t *testing.T) {
	key := "afy_live_abcdefghijklmnopqrstuvwxyz123456"
	masked := config.MaskAPIKey(key)

	assert.Contains(t, masked, "afy_live_abc")
	assert.Contains(t, masked, "...")
	assert.NotContains(t, masked, "abcdefghijklmnopqrstuvwxyz123456")
}

func TestIsTestKey(t *testing.T) {
	assert.True(t, config.IsTestKey("afy_test_xxxxx"))
	assert.False(t, config.IsTestKey("afy_live_xxxxx"))
}

func TestEnsureConfigDir(t *testing.T) {
	// Set a temp dir for testing
	tmpDir := t.TempDir()
	os.Setenv("AETHERFY_CONFIG_DIR", tmpDir)
	defer os.Unsetenv("AETHERFY_CONFIG_DIR")

	err := config.EnsureConfigDir()
	assert.NoError(t, err)

	// Check directory exists
	_, err = os.Stat(tmpDir)
	assert.NoError(t, err)
}
