package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Credentials holds the API key and related auth info
type Credentials struct {
	APIKey string `yaml:"api_key"`
	UserID string `yaml:"user_id,omitempty"`
	Email  string `yaml:"email,omitempty"`
	Tier   string `yaml:"tier,omitempty"`
}

// APIKeyPattern validates API key format
var APIKeyPattern = regexp.MustCompile(`^afy_(live|test)_[a-zA-Z0-9]{32,}$`)

// Global credentials instance
var creds *Credentials

// GetCredentials returns the global credentials instance
func GetCredentials() *Credentials {
	if creds == nil {
		creds = &Credentials{}
	}
	return creds
}

// LoadCredentials reads credentials from file or environment
func LoadCredentials() (*Credentials, error) {
	creds = &Credentials{}

	// Check environment variable first (highest priority)
	if apiKey := os.Getenv("AETHERFY_API_KEY"); apiKey != "" {
		creds.APIKey = apiKey
		return creds, nil
	}

	// Try to read from credentials file
	data, err := os.ReadFile(CredentialsPath())
	if err != nil {
		if os.IsNotExist(err) {
			// No credentials file, not an error
			return creds, nil
		}
		return nil, fmt.Errorf("failed to read credentials: %w", err)
	}

	if err := yaml.Unmarshal(data, creds); err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}

	return creds, nil
}

// Save writes credentials to file with secure permissions
func (c *Credentials) Save() error {
	if err := EnsureConfigDir(); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to serialize credentials: %w", err)
	}

	// Write with restricted permissions (owner read/write only)
	if err := os.WriteFile(CredentialsPath(), data, 0600); err != nil {
		return fmt.Errorf("failed to write credentials: %w", err)
	}

	return nil
}

// Delete removes the credentials file
func DeleteCredentials() error {
	path := CredentialsPath()
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil // Already deleted
		}
		return fmt.Errorf("failed to delete credentials: %w", err)
	}
	creds = nil
	return nil
}

// IsLoggedIn returns true if valid credentials exist
func IsLoggedIn() bool {
	c := GetCredentials()
	return c.APIKey != ""
}

// ValidateAPIKey checks if an API key has valid format
func ValidateAPIKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("API key cannot be empty")
	}
	if !APIKeyPattern.MatchString(key) {
		return fmt.Errorf("invalid API key format (expected: afy_live_xxx or afy_test_xxx)")
	}
	return nil
}

// MaskAPIKey returns a masked version of the API key for display
func MaskAPIKey(key string) string {
	if len(key) < 20 {
		return "***"
	}
	return key[:12] + "..." + key[len(key)-4:]
}

// IsTestKey returns true if the API key is a test key
func IsTestKey(key string) bool {
	return strings.HasPrefix(key, "afy_test_")
}
