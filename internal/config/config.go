package config

import (
	"fmt"
	"os"

	"github.com/spf13/viper"
)

// Config holds the application configuration
type Config struct {
	APIURL        string `mapstructure:"api_url" yaml:"api_url"`
	DefaultRegion string `mapstructure:"default_region" yaml:"default_region"`
	OutputFormat  string `mapstructure:"output_format" yaml:"output_format"`
	NoColor       bool   `mapstructure:"no_color" yaml:"no_color"`
	Verbose       bool   `mapstructure:"verbose" yaml:"verbose"`
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		APIURL:        "https://agents.aetherfy.com/api/v1",
		DefaultRegion: "iad",
		OutputFormat:  "text",
		NoColor:       false,
		Verbose:       false,
	}
}

// Global config instance
var cfg *Config

// Get returns the global config instance
func Get() *Config {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return cfg
}

// Load reads the config from file and environment
func Load() (*Config, error) {
	v := viper.New()

	// Set defaults
	v.SetDefault("api_url", "https://agents.aetherfy.com/api/v1")
	v.SetDefault("default_region", "iad")
	v.SetDefault("output_format", "text")
	v.SetDefault("no_color", false)
	v.SetDefault("verbose", false)

	// Config file settings
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(ConfigDir())

	// Environment variables
	v.SetEnvPrefix("AETHERFY")
	v.AutomaticEnv()

	// Read config file (ignore if not found)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config: %w", err)
		}
	}

	// Check for NO_COLOR environment variable
	if os.Getenv("NO_COLOR") != "" {
		v.Set("no_color", true)
	}

	// Unmarshal to struct
	cfg = &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("error parsing config: %w", err)
	}

	return cfg, nil
}

// Save writes the config to file
func (c *Config) Save() error {
	if err := EnsureConfigDir(); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	v := viper.New()
	v.Set("api_url", c.APIURL)
	v.Set("default_region", c.DefaultRegion)
	v.Set("output_format", c.OutputFormat)
	v.Set("no_color", c.NoColor)

	return v.WriteConfigAs(ConfigPath())
}

// SetAPIURL sets the API URL and updates the global config
func SetAPIURL(url string) {
	Get().APIURL = url
}

// SetVerbose sets verbose mode
func SetVerbose(verbose bool) {
	Get().Verbose = verbose
}

// SetNoColor sets no-color mode
func SetNoColor(noColor bool) {
	Get().NoColor = noColor
}

// SetOutputFormat sets the output format
func SetOutputFormat(format string) {
	Get().OutputFormat = format
}
