// Package config provides the central configuration struct for the km CLI.
// Configuration is loaded from ~/.km/config.yaml, environment variables (KM_ prefix),
// and CLI flags (highest precedence).
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds all configuration values for the km CLI.
type Config struct {
	// ProfileSearchPaths is the ordered list of directories to search for profiles.
	// Built-in profiles are always searched first, before these paths.
	ProfileSearchPaths []string

	// LogLevel controls the zerolog log level (trace, debug, info, warn, error).
	LogLevel string

	// Version is the km CLI version string, injected at build time.
	Version string
}

// Load reads configuration from (in order of increasing precedence):
//  1. Defaults
//  2. ~/.km/config.yaml
//  3. Environment variables with KM_ prefix
//  4. CLI flags (applied by the root command after Load returns)
//
// Returns a Config with all values resolved from the above sources.
func Load() (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("profile_search_paths", []string{"./profiles", "~/.km/profiles"})
	v.SetDefault("log_level", "info")

	// Config file
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	home, err := os.UserHomeDir()
	if err == nil {
		v.AddConfigPath(filepath.Join(home, ".km"))
	}
	v.AddConfigPath(".")

	// Read config file — ignore "not found" errors; fail on parse errors
	if err := v.ReadInConfig(); err != nil {
		if _, notFound := err.(viper.ConfigFileNotFoundError); !notFound {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
	}

	// Environment variable overrides (KM_PROFILE_SEARCH_PATHS, KM_LOG_LEVEL, etc.)
	v.SetEnvPrefix("KM")
	v.AutomaticEnv()

	cfg := &Config{
		ProfileSearchPaths: v.GetStringSlice("profile_search_paths"),
		LogLevel:           v.GetString("log_level"),
	}

	return cfg, nil
}
