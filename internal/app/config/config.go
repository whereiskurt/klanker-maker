// Package config provides the central configuration struct for the km CLI.
// Configuration is loaded from ~/.km/config.yaml, environment variables (KM_ prefix),
// and CLI flags (highest precedence). A repo-root km-config.yaml is also loaded
// (merged via viper) to supply platform-level fields like Domain and AccountIDs.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

	// --- Platform fields (from km-config.yaml at repo root) ---

	// Domain is the base domain for the platform (e.g. "klankermaker.ai").
	// Set via km-config.yaml domain key or KM_DOMAIN environment variable.
	// When empty, callers default to "klankermaker.ai". Used to derive email
	// addresses (sandboxes.{Domain}), schema $id, and apiVersion prefixes so
	// forks work with any domain without code changes.
	Domain string

	// ManagementAccountID is the AWS account ID for the management/root account.
	// Maps to km-config.yaml key accounts.management.
	ManagementAccountID string

	// TerraformAccountID is the AWS account ID used for Terraform/infrastructure operations.
	// Maps to km-config.yaml key accounts.terraform.
	TerraformAccountID string

	// ApplicationAccountID is the AWS account ID where sandboxes are provisioned.
	// Maps to km-config.yaml key accounts.application.
	ApplicationAccountID string

	// SSOStartURL is the AWS SSO portal URL.
	// Maps to km-config.yaml key sso.start_url.
	SSOStartURL string

	// SSORegion is the AWS region where the SSO instance is hosted.
	// Maps to km-config.yaml key sso.region.
	SSORegion string

	// PrimaryRegion is the default AWS region for infrastructure operations.
	// Maps to km-config.yaml key region.
	PrimaryRegion string

	// BudgetTableName is the DynamoDB table name for sandbox budget tracking.
	// Maps to km-config.yaml key budget_table_name. Defaults to "km-budgets".
	BudgetTableName string

	// ArtifactsBucket is the S3 bucket used for storing sandbox artifacts and profiles.
	// Set via KM_ARTIFACTS_BUCKET environment variable or artifacts_bucket in km-config.yaml.
	// Required for ECS sandbox re-provisioning via km budget add.
	ArtifactsBucket string

	// AWSProfile is the AWS CLI profile name used for infrastructure operations.
	// Set via KM_AWS_PROFILE environment variable or aws_profile in km-config.yaml.
	// Defaults to "klanker-terraform" when empty.
	AWSProfile string
}

// isSetByEnv returns true if the given viper key has been overridden by an environment
// variable (KM_ prefix). Viper maps "foo.bar" -> KM_FOO_BAR (dots become underscores).
func isSetByEnv(_ *viper.Viper, key string) bool {
	envKey := "KM_" + strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(key))
	return os.Getenv(envKey) != ""
}

// Load reads configuration from (in order of increasing precedence):
//  1. Defaults
//  2. ~/.km/config.yaml
//  3. ./km-config.yaml (repo-root platform configuration, merged on top)
//  4. Environment variables with KM_ prefix
//  5. CLI flags (applied by the root command after Load returns)
//
// Returns a Config with all values resolved from the above sources.
func Load() (*Config, error) {
	v := viper.New()

	// Defaults for existing fields
	v.SetDefault("profile_search_paths", []string{"./profiles", "~/.km/profiles"})
	v.SetDefault("log_level", "info")
	v.SetDefault("state_bucket", "")
	v.SetDefault("ttl_lambda_arn", "")
	v.SetDefault("scheduler_role_arn", "")

	// Defaults for new platform fields
	v.SetDefault("budget_table_name", "km-budgets")
	v.SetDefault("artifacts_bucket", "")
	v.SetDefault("aws_profile", "")

	// Primary config file: ~/.km/config.yaml
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

	// Secondary config file: km-config.yaml in current directory (repo root).
	// Values in km-config.yaml are merged on top of ~/.km/config.yaml but environment
	// variables (set via AutomaticEnv above) retain highest precedence.
	v2 := viper.New()
	v2.SetConfigName("km-config")
	v2.SetConfigType("yaml")
	v2.AddConfigPath(".")
	if err := v2.ReadInConfig(); err == nil {
		// Merge platform keys from v2 into v only when not already overridden by env.
		for _, key := range []string{
			"domain",
			"accounts.management",
			"accounts.terraform",
			"accounts.application",
			"sso.start_url",
			"sso.region",
			"region",
			"budget_table_name",
			"artifacts_bucket",
			"aws_profile",
		} {
			if v2.IsSet(key) && !isSetByEnv(v, key) {
				v.Set(key, v2.Get(key))
			}
		}
	}
	// Not finding km-config.yaml is fine — continue with existing config.

	cfg := &Config{
		// Existing fields
		ProfileSearchPaths: v.GetStringSlice("profile_search_paths"),
		LogLevel:           v.GetString("log_level"),
		StateBucket:        v.GetString("state_bucket"),
		TTLLambdaARN:       v.GetString("ttl_lambda_arn"),
		SchedulerRoleARN:   v.GetString("scheduler_role_arn"),

		// New platform fields
		Domain:               v.GetString("domain"),
		ManagementAccountID:  v.GetString("accounts.management"),
		TerraformAccountID:   v.GetString("accounts.terraform"),
		ApplicationAccountID: v.GetString("accounts.application"),
		SSOStartURL:          v.GetString("sso.start_url"),
		SSORegion:            v.GetString("sso.region"),
		PrimaryRegion:        v.GetString("region"),
		BudgetTableName:      v.GetString("budget_table_name"),
		ArtifactsBucket:      v.GetString("artifacts_bucket"),
		AWSProfile:           v.GetString("aws_profile"),
	}

	return cfg, nil
}
