package config

import (
	"os"
	"path/filepath"
	"runtime"
)

const (
	// ConfigDirName is the name of the config directory
	ConfigDirName = ".aetherfy"
	// ConfigFileName is the name of the config file
	ConfigFileName = "config.yaml"
	// CredentialsFileName is the name of the credentials file
	CredentialsFileName = "credentials.yaml"
)

// ConfigDir returns the path to the config directory
func ConfigDir() string {
	// Check for custom config dir
	if dir := os.Getenv("AETHERFY_CONFIG_DIR"); dir != "" {
		return dir
	}

	// Platform-specific default
	switch runtime.GOOS {
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "aetherfy")
		}
	case "darwin", "linux":
		// Try XDG first on Linux
		if runtime.GOOS == "linux" {
			if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
				return filepath.Join(xdgConfig, "aetherfy")
			}
		}
	}

	// Default to ~/.aetherfy
	home, err := os.UserHomeDir()
	if err != nil {
		return ConfigDirName
	}
	return filepath.Join(home, ConfigDirName)
}

// ConfigPath returns the full path to the config file
func ConfigPath() string {
	return filepath.Join(ConfigDir(), ConfigFileName)
}

// CredentialsPath returns the full path to the credentials file
func CredentialsPath() string {
	return filepath.Join(ConfigDir(), CredentialsFileName)
}

// EnsureConfigDir creates the config directory if it doesn't exist
func EnsureConfigDir() error {
	dir := ConfigDir()
	return os.MkdirAll(dir, 0700)
}
