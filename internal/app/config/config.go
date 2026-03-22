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

	// StateBucket is the S3 bucket used for Terraform state and sandbox metadata.
	// Set via KM_STATE_BUCKET environment variable.
	StateBucket string

	// TTLLambdaARN is the Lambda function ARN for TTL sandbox teardown.
	// Set via KM_TTL_LAMBDA_ARN environment variable.
	// If empty, TTL schedules are not created.
	TTLLambdaARN string

	// SchedulerRoleARN is the IAM role ARN that EventBridge Scheduler assumes
	// to invoke the TTL Lambda. Set via KM_SCHEDULER_ROLE_ARN environment variable.
	SchedulerRoleARN string
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
	v.SetDefault("state_bucket", "")
	v.SetDefault("ttl_lambda_arn", "")
	v.SetDefault("scheduler_role_arn", "")

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
		StateBucket:        v.GetString("state_bucket"),
		TTLLambdaARN:       v.GetString("ttl_lambda_arn"),
		SchedulerRoleARN:   v.GetString("scheduler_role_arn"),
	}

	return cfg, nil
}
