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

	// OrganizationAccountID is the AWS Organizations management account ID (SCP target).
	// Maps to km-config.yaml key accounts.organization. Optional: blank skips SCP deployment.
	OrganizationAccountID string

	// DNSParentAccountID is the AWS account ID owning the parent Route53 hosted zone for cfg.Domain.
	// Maps to km-config.yaml key accounts.dns_parent. Blank skips DNS delegation in km init.
	DNSParentAccountID    string

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

	// IdentityTableName is the DynamoDB table name for sandbox identity tracking.
	// Maps to km-config.yaml key identity_table_name. Defaults to "km-identities".
	IdentityTableName string

	// SandboxTableName is the DynamoDB table name for sandbox metadata.
	// Maps to km-config.yaml key sandbox_table_name. Defaults to "km-sandboxes".
	SandboxTableName string

	// ArtifactsBucket is the S3 bucket used for storing sandbox artifacts and profiles.
	// Set via KM_ARTIFACTS_BUCKET environment variable or artifacts_bucket in km-config.yaml.
	// Required for ECS sandbox re-provisioning via km budget add.
	ArtifactsBucket string

	// AWSProfile is the AWS CLI profile name used for infrastructure operations.
	// Set via KM_AWS_PROFILE environment variable or aws_profile in km-config.yaml.
	// Defaults to "klanker-terraform" when empty.
	AWSProfile string

	// Route53ZoneID is the hosted zone ID for the sandboxes subdomain.
	// Set via KM_ROUTE53_ZONE_ID environment variable or route53_zone_id in km-config.yaml.
	// Auto-created by km init if not set.
	Route53ZoneID string

	// OperatorEmail is the email address that receives sandbox lifecycle notifications
	// (TTL expiry, idle timeout, budget exhaustion, spot interruption, errors).
	// Set via operator_email in km-config.yaml or KM_OPERATOR_EMAIL environment variable.
	OperatorEmail string

	// SafePhrase is the shared secret for email-to-create authentication.
	// Included in emails as "KM-AUTH: <phrase>" to authorize sandbox creation.
	// Set via safe_phrase in km-config.yaml or KM_SAFE_PHRASE environment variable.
	// Written to SSM at /km/config/remote-create/safe-phrase during km init.
	SafePhrase string

	// RsyncPaths is the list of relative paths (from sandbox user $HOME) to
	// include in rsync snapshots. Default: [".claude", ".bashrc", ".gitconfig"]
	RsyncPaths []string

	// MaxSandboxes is the maximum number of concurrently active sandboxes allowed.
	// Set via max_sandboxes in km-config.yaml or KM_MAX_SANDBOXES environment variable.
	// A value of 0 means unlimited (no enforcement). Defaults to 10.
	MaxSandboxes int

	// SchedulesTableName is the DynamoDB table name for km-at schedule metadata.
	// Maps to km-config.yaml key schedules_table_name. Defaults to "km-schedules".
	SchedulesTableName string

	// CreateHandlerLambdaARN is the Lambda function ARN invoked by km-at create schedules
	// to provision sandboxes on a deferred or recurring basis.
	// Set via create_handler_lambda_arn in km-config.yaml or KM_CREATE_HANDLER_LAMBDA_ARN.
	CreateHandlerLambdaARN string

	// DoctorStaleAMIDays is the age threshold (in days) used by `km doctor` to flag
	// unused AMIs as stale. An AMI is "stale" when (a) it is older than this threshold,
	// (b) no profile in cfg.ProfileSearchPaths references it, AND (c) no running sandbox
	// currently uses it. Maps to km-config.yaml key doctor_stale_ami_days. Defaults to 30.
	DoctorStaleAMIDays int

	// SlackThreadsTableName is the DynamoDB table name for the Slack-inbound
	// (channel_id, thread_ts) → claude_session_id mapping. Default
	// "km-slack-threads"; respects ResourcePrefix when set (Phase 66
	// forward-compat). Maps to km-config.yaml key slack_threads_table_name.
	SlackThreadsTableName string

	// ResourcePrefix is the Phase-66 multi-instance prefix applied to AWS
	// resource names (e.g. "km", "stg", "kpf"). Default "km" via
	// GetResourcePrefix(). Phase 66 will populate this from km-config.yaml;
	// Phase 67 ships the shim helper so downstream code can use the helper
	// unconditionally. Maps to km-config.yaml key resource_prefix.
	ResourcePrefix string
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
	v.SetDefault("max_sandboxes", 10)
	v.SetDefault("budget_table_name", "km-budgets")
	v.SetDefault("identity_table_name", "km-identities")
	v.SetDefault("sandbox_table_name", "km-sandboxes")
	v.SetDefault("artifacts_bucket", "")
	v.SetDefault("aws_profile", "klanker-terraform")
	v.SetDefault("rsync_paths", []string{".claude", ".bashrc", ".bash_profile", ".gitconfig"})
	v.SetDefault("schedules_table_name", "km-schedules")
	v.SetDefault("create_handler_lambda_arn", "")
	v.SetDefault("doctor_stale_ami_days", 30)
	v.SetDefault("slack_threads_table_name", "km-slack-threads")
	v.SetDefault("resource_prefix", "km")

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

	// Secondary config file: km-config.yaml in current directory or repo root.
	// Values in km-config.yaml are merged on top of ~/.km/config.yaml but environment
	// variables (set via AutomaticEnv above) retain highest precedence.
	v2 := viper.New()
	v2.SetConfigName("km-config")
	v2.SetConfigType("yaml")
	// Explicit config path override (used by Lambda cold start)
	if configPath := os.Getenv("KM_CONFIG_PATH"); configPath != "" {
		v2.SetConfigFile(configPath)
	} else {
		v2.AddConfigPath(".")
		// Also search KM_REPO_ROOT (used in Lambda where CWD != repo root)
		if repoRoot := os.Getenv("KM_REPO_ROOT"); repoRoot != "" {
			v2.AddConfigPath(repoRoot)
		}
	}
	if err := v2.ReadInConfig(); err == nil {
		// Merge platform keys from v2 into v only when not already overridden by env.
		for _, key := range []string{
			"domain",
			"accounts.organization",
			"accounts.dns_parent",
			"accounts.terraform",
			"accounts.application",
			"sso.start_url",
			"sso.region",
			"region",
			"budget_table_name",
			"identity_table_name",
			"sandbox_table_name",
			"artifacts_bucket",
			"aws_profile",
			"state_bucket",
			"route53_zone_id",
			"operator_email",
			"safe_phrase",
			"rsync_paths",
			"max_sandboxes",
			"schedules_table_name",
			"create_handler_lambda_arn",
			"ttl_lambda_arn",
			"scheduler_role_arn",
			"doctor_stale_ami_days",
			"slack_threads_table_name",
			"resource_prefix",
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
		Domain:                v.GetString("domain"),
		OrganizationAccountID: v.GetString("accounts.organization"),
		DNSParentAccountID:    v.GetString("accounts.dns_parent"),
		TerraformAccountID:    v.GetString("accounts.terraform"),
		ApplicationAccountID: v.GetString("accounts.application"),
		SSOStartURL:          v.GetString("sso.start_url"),
		SSORegion:            v.GetString("sso.region"),
		PrimaryRegion:        v.GetString("region"),
		BudgetTableName:      v.GetString("budget_table_name"),
		IdentityTableName:    v.GetString("identity_table_name"),
		SandboxTableName:     v.GetString("sandbox_table_name"),
		ArtifactsBucket:      v.GetString("artifacts_bucket"),
		AWSProfile:           v.GetString("aws_profile"),
		Route53ZoneID:        v.GetString("route53_zone_id"),
		OperatorEmail:        v.GetString("operator_email"),
		SafePhrase:           v.GetString("safe_phrase"),
		RsyncPaths:             v.GetStringSlice("rsync_paths"),
		MaxSandboxes:           v.GetInt("max_sandboxes"),
		SchedulesTableName:     v.GetString("schedules_table_name"),
		CreateHandlerLambdaARN: v.GetString("create_handler_lambda_arn"),
		DoctorStaleAMIDays:     v.GetInt("doctor_stale_ami_days"),
		SlackThreadsTableName:  v.GetString("slack_threads_table_name"),
		ResourcePrefix:         v.GetString("resource_prefix"),
	}

	// If the AWS profile was set by default (not explicitly configured), verify it
	// exists in ~/.aws/config or ~/.aws/credentials. On EC2 instances there are no
	// named profiles — clear the field so the SDK falls through to the default
	// credential chain (instance profile / IMDS).
	if cfg.AWSProfile != "" && !v.IsSet("aws_profile") {
		if !awsProfileExists(cfg.AWSProfile) {
			cfg.AWSProfile = ""
		}
	}

	// Clamp DoctorStaleAMIDays: a zero or negative value would never flag any AMI,
	// which is almost certainly operator misconfiguration. Fall back to the default.
	if cfg.DoctorStaleAMIDays <= 0 {
		cfg.DoctorStaleAMIDays = 30
	}

	return cfg, nil
}

// GetResourcePrefix returns the configured resource prefix, falling back to
// "km" when unset. Phase 66 populates this from km-config.yaml; Phase 67
// callers use this helper directly so they remain forward-compatible.
func (c *Config) GetResourcePrefix() string {
	if c == nil || c.ResourcePrefix == "" {
		return "km"
	}
	return c.ResourcePrefix
}

// GetSlackThreadsTableName returns the Slack-threads DynamoDB table name.
// If SlackThreadsTableName is explicitly set, that value wins. Otherwise
// the name is derived from GetResourcePrefix() + "-slack-threads", which
// defaults to "km-slack-threads" when no prefix is configured.
func (c *Config) GetSlackThreadsTableName() string {
	if c == nil {
		return "km-slack-threads"
	}
	if c.SlackThreadsTableName != "" {
		return c.SlackThreadsTableName
	}
	return c.GetResourcePrefix() + "-slack-threads"
}

// awsProfileExists checks whether a named AWS profile is defined in
// ~/.aws/config or ~/.aws/credentials.
func awsProfileExists(profile string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, name := range []string{"config", "credentials"} {
		data, err := os.ReadFile(filepath.Join(home, ".aws", name))
		if err != nil {
			continue
		}
		content := string(data)
		// AWS config uses [profile <name>], credentials uses [<name>]
		if strings.Contains(content, "[profile "+profile+"]") ||
			strings.Contains(content, "["+profile+"]") {
			return true
		}
	}
	return false
}
