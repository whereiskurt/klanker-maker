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

// ClusterConfig represents a single registered Kubernetes cluster for cross-account
// IRSA (IAM Roles for Service Accounts) integration. Each entry maps a cluster's
// OIDC provider to a per-namespace/service-account IAM role in the application account.
// Populated from the km-config.yaml `clusters:` list (Plan 80).
// SlackConfig holds install-level Slack defaults that flow into the bridge
// Lambda environment via terragrunt.hcl get_env() calls. Phase 91.1 added
// MentionOnly so operators no longer need to `export KM_SLACK_MENTION_ONLY=true`
// before `km init` — set `slack.mention_only: true` in km-config.yaml instead.
type SlackConfig struct {
	// MentionOnly is the install-level default for the polite-bot @-mention
	// filter. Tri-state via *bool:
	//   nil    → key absent from yaml; bridge defaults to "false" (chatty)
	//   &true  → polite mode (bridge only acts on messages containing <@{bot_user_id}>)
	//   &false → chatty mode (bridge reacts to every message)
	// Maps to km-config.yaml key slack.mention_only. Exported as
	// KM_SLACK_MENTION_ONLY for infra/live/use1/lambda-slack-bridge/terragrunt.hcl
	// get_env() at terragrunt-apply time during `km init`.
	MentionOnly *bool `mapstructure:"mention_only" yaml:"mention_only,omitempty"`

	// ReactAlways is the install-level default for the Phase 91.4 first-only
	// reactor. Tri-state via *bool:
	//   nil    → key absent from yaml; bridge defaults to "true" (react on every dispatch)
	//   &true  → react on every dispatch (current chatty-reactor behaviour)
	//   &false → react ONLY on top-level engagement messages; thread replies
	//            dispatched via Phase 91.3 mention-bypass are silent
	// Maps to km-config.yaml key slack.react_always. Exported as
	// KM_SLACK_REACT_ALWAYS for the bridge Lambda environment block.
	ReactAlways *bool `mapstructure:"react_always" yaml:"react_always,omitempty"`
}

type ClusterConfig struct {
	Name            string `mapstructure:"name"              yaml:"name"`
	OIDCProviderARN string `mapstructure:"oidc_provider_arn" yaml:"oidc_provider_arn"`
	Namespace       string `mapstructure:"namespace"         yaml:"namespace"`
	ServiceAccount  string `mapstructure:"service_account"   yaml:"service_account"`
	RoleARN         string `mapstructure:"role_arn"          yaml:"role_arn"`
}

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
	DNSParentAccountID string

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

	// SlackStreamMessagesTableName is the DynamoDB table name for the Phase 68
	// transcript-streaming (channel_id, slack_ts) → {sandbox_id, session_id,
	// transcript_offset, ttl_expiry} mapping. Default "km-slack-stream-messages";
	// respects ResourcePrefix when set. Maps to km-config.yaml key
	// slack_stream_messages_table_name.
	SlackStreamMessagesTableName string

	// ResourcePrefix is the Phase-66 multi-instance prefix applied to AWS
	// resource names (e.g. "km", "stg", "kpf"). Default "km" via
	// GetResourcePrefix(). Phase 66 will populate this from km-config.yaml;
	// Phase 67 ships the shim helper so downstream code can use the helper
	// unconditionally. Maps to km-config.yaml key resource_prefix.
	ResourcePrefix string

	// EmailSubdomain is the subdomain used for SES email addresses
	// ({sandboxID}@{subdomain}.{domain}). Maps to km-config.yaml key
	// email_subdomain. Defaults to "sandboxes" via GetEmailDomain().
	// One-time choice at km init — changing requires fresh DNS/SES verification.
	EmailSubdomain string

	// ContainerSubstratesEnabled gates the ECR image build/push steps in
	// km init: km-sandbox container image plus the four sidecar images
	// (dns-proxy, http-proxy, audit-log, tracing). Container images are only
	// pulled by the docker and ecs substrates; EC2 sandboxes get raw binaries
	// from S3 (see pkg/compiler/userdata.go). Pointer-typed for tri-state
	// (unset/true/false): nil means "use default", which ShouldBuildContainerImages
	// resolves to true so existing installs keep building images. Maps to
	// km-config.yaml key container_substrates_enabled.
	ContainerSubstratesEnabled *bool

	// Clusters is the list of registered Kubernetes clusters for cross-account IRSA
	// integration. Each entry maps a cluster's OIDC provider to an IAM role in the
	// application account. Maps to km-config.yaml key clusters (Plan 80).
	// Absent key → empty slice (no error). Managed via `km cluster add/list/rm`.
	Clusters []ClusterConfig `mapstructure:"clusters" yaml:"clusters"`

	// Slack holds install-level Slack defaults (Phase 91.1). Currently only
	// MentionOnly is populated; future Slack-wide knobs slot in here. Maps to
	// km-config.yaml key slack. Absent key → zero value (no error).
	Slack SlackConfig `mapstructure:"slack" yaml:"slack,omitempty"`

	// YAMLDefaults holds the raw km-config.yaml values for env-bound keys,
	// snapshotted during Load() BEFORE viper's AutomaticEnv binds env vars into
	// the cfg fields. Used by ExportTerragruntEnvVars to detect drift between
	// the env var and the yaml-configured value.
	// Keys are dotted yaml paths (e.g. "region", "artifacts_bucket", "domain").
	// Empty map when km-config.yaml is not found or key is absent in yaml.
	YAMLDefaults map[string]string
}

// accountsYamlAuthoritativeKeys lists the viper keys for which km-config.yaml
// values take precedence over shell environment variables. This is an intentional
// asymmetry introduced by Phase 84.3 closure (h):
//
//   - accounts.organization, accounts.dns_parent, accounts.application: these three
//     account IDs are platform topology values that must reflect km-config.yaml so
//     that every km command (km init, km doctor, km info) uses the same account
//     regardless of what the operator has exported in their shell. Operators commonly
//     export KM_ACCOUNTS_APPLICATION for one-off experiments that should NOT silently
//     override the install-level topology for shared-account operations.
//
//   - accounts.terraform: INTENTIONALLY OMITTED. Operators legitimately set
//     KM_ACCOUNTS_TERRAFORM for one-off invocations to a different infra account
//     (e.g. staging vs production). Env-var precedence for this key is therefore
//     preserved. See Phase 84.3 CONTEXT.md decision and RESEARCH.md Pitfall 1.
var accountsYamlAuthoritativeKeys = map[string]bool{
	"accounts.organization": true,
	"accounts.dns_parent":   true,
	"accounts.application":  true,
	// accounts.terraform is intentionally absent — env wins for that key.
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

	// Defaults for new platform fields.
	// Note: table names default to "" so prefix-aware helpers like
	// GetSandboxTableName() can derive {prefix}-{table} from resource_prefix.
	// Hardcoded "km-*" defaults would defeat multi-instance support.
	v.SetDefault("max_sandboxes", 10)
	v.SetDefault("budget_table_name", "")
	v.SetDefault("identity_table_name", "")
	v.SetDefault("sandbox_table_name", "")
	v.SetDefault("artifacts_bucket", "")
	v.SetDefault("aws_profile", "klanker-terraform")
	v.SetDefault("rsync_paths", []string{".claude", ".bashrc", ".bash_profile", ".gitconfig"})
	v.SetDefault("schedules_table_name", "")
	v.SetDefault("create_handler_lambda_arn", "")
	v.SetDefault("doctor_stale_ami_days", 30)
	v.SetDefault("slack_threads_table_name", "")
	v.SetDefault("slack_stream_messages_table_name", "")
	v.SetDefault("resource_prefix", "km")
	v.SetDefault("email_subdomain", "sandboxes")
	v.SetDefault("clusters", []interface{}{})

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
	// SetEnvKeyReplacer maps dot-notation viper keys to underscored env vars:
	// "accounts.terraform" → KM_ACCOUNTS_TERRAFORM, "sso.start_url" → KM_SSO_START_URL.
	// Without this, AutomaticEnv only handles flat (non-dot) keys correctly.
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
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
	var yamlDefaults map[string]string
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
			"slack_stream_messages_table_name",
			"resource_prefix",
			"email_subdomain",
			"container_substrates_enabled",
			"clusters",
			// Phase 91.1: nested key for the polite-bot install-level default.
			"slack.mention_only",
			// Phase 91.4: nested key for the first-only reactor install-level default.
			"slack.react_always",
		} {
			// yaml wins unconditionally for accountsYamlAuthoritativeKeys (organization,
			// dns_parent, application). For all other keys, env-var takes precedence
			// over yaml (standard viper merge semantics).
			if v2.IsSet(key) && (accountsYamlAuthoritativeKeys[key] || !isSetByEnv(v, key)) {
				v.Set(key, v2.Get(key))
			}
		}

		// Snapshot raw yaml values for env-bound keys so ExportTerragruntEnvVars
		// can detect drift between env vars and yaml values (Phase 84.3 gap closure 1).
		// This snapshot is taken AFTER the merge loop so the v2 values are definitive,
		// but BEFORE building cfg (whose fields are baked with env values by AutomaticEnv).
		yamlDefaults = map[string]string{}
		for _, key := range []string{
			"region", "domain", "artifacts_bucket", "resource_prefix",
			"operator_email", "route53_zone_id", "scheduler_role_arn",
			"email_subdomain",
		} {
			if v2.IsSet(key) {
				yamlDefaults[key] = v2.GetString(key)
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
		Domain:                       v.GetString("domain"),
		OrganizationAccountID:        v.GetString("accounts.organization"),
		DNSParentAccountID:           v.GetString("accounts.dns_parent"),
		TerraformAccountID:           v.GetString("accounts.terraform"),
		ApplicationAccountID:         v.GetString("accounts.application"),
		SSOStartURL:                  v.GetString("sso.start_url"),
		SSORegion:                    v.GetString("sso.region"),
		PrimaryRegion:                v.GetString("region"),
		BudgetTableName:              v.GetString("budget_table_name"),
		IdentityTableName:            v.GetString("identity_table_name"),
		SandboxTableName:             v.GetString("sandbox_table_name"),
		ArtifactsBucket:              v.GetString("artifacts_bucket"),
		AWSProfile:                   v.GetString("aws_profile"),
		Route53ZoneID:                v.GetString("route53_zone_id"),
		OperatorEmail:                v.GetString("operator_email"),
		SafePhrase:                   v.GetString("safe_phrase"),
		RsyncPaths:                   v.GetStringSlice("rsync_paths"),
		MaxSandboxes:                 v.GetInt("max_sandboxes"),
		SchedulesTableName:           v.GetString("schedules_table_name"),
		CreateHandlerLambdaARN:       v.GetString("create_handler_lambda_arn"),
		DoctorStaleAMIDays:           v.GetInt("doctor_stale_ami_days"),
		SlackThreadsTableName:        v.GetString("slack_threads_table_name"),
		SlackStreamMessagesTableName: v.GetString("slack_stream_messages_table_name"),
		ResourcePrefix:               v.GetString("resource_prefix"),
		EmailSubdomain:               v.GetString("email_subdomain"),
		YAMLDefaults:                 yamlDefaults,
	}

	// ContainerSubstratesEnabled is tri-state via *bool: only populated when
	// the operator has explicitly set the key, so ShouldBuildContainerImages
	// can default unset → true for back-compat.
	if v.IsSet("container_substrates_enabled") {
		val := v.GetBool("container_substrates_enabled")
		cfg.ContainerSubstratesEnabled = &val
	}

	// Phase 91.1: slack.mention_only is tri-state via *bool. Only populated when
	// the operator has explicitly set the key — absent yaml key → nil pointer →
	// ExportTerragruntEnvVars emits nothing → terragrunt.hcl get_env() default
	// ("false") kicks in. Set to true to flip the install default to polite-bot.
	if v.IsSet("slack.mention_only") {
		val := v.GetBool("slack.mention_only")
		cfg.Slack.MentionOnly = &val
	}

	// Phase 91.4: slack.react_always is tri-state via *bool. Same shape as
	// slack.mention_only. Absent → bridge default "true" (react on every
	// dispatch). Set to false to flip to first-only-react.
	if v.IsSet("slack.react_always") {
		val := v.GetBool("slack.react_always")
		cfg.Slack.ReactAlways = &val
	}

	// Clusters is a structured slice — viper's UnmarshalKey handles the
	// mapstructure decoding from the merged "clusters" key. SetDefault above
	// ensures a non-nil empty slice when the key is absent (RESEARCH.md Pitfall 6).
	if err := v.UnmarshalKey("clusters", &cfg.Clusters); err != nil {
		return nil, fmt.Errorf("unmarshal clusters: %w", err)
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

	// Gap #2b (Phase 84.4.1.1): reject obvious placeholder artifacts_bucket values
	// at load time. Catches angle-bracket tokens only (e.g. "<prefix>-artifacts-12345678").
	//
	// Canonical-shape enforcement (^[a-z][a-z0-9-]*-artifacts-[0-9]{12}$) is intentionally
	// NOT applied here — it lives only at configure time (cmdCanonicalBucketRE in
	// configure.go). Reason: a strict canonical check at Load() breaks legacy installs
	// with pre-Phase-84.3 bucket names (e.g. literal "km-artifacts-12345"), locking the
	// operator out of every km command. See isPlaceholderBucket comment for full history.
	//
	// We validate the yaml-authoritative value (from yamlDefaults) rather than the
	// env-overridden cfg.ArtifactsBucket so that KM_ARTIFACTS_BUCKET env overrides
	// are not blocked.
	bucketToValidate := cfg.ArtifactsBucket
	if yamlVal, ok := yamlDefaults["artifacts_bucket"]; ok {
		bucketToValidate = yamlVal
	}
	if isPlaceholderBucket(bucketToValidate) {
		return nil, fmt.Errorf("artifacts_bucket=%q is a placeholder; re-run `km configure` to derive ${prefix}-artifacts-${account_id} automatically", bucketToValidate)
	}

	return cfg, nil
}

// isPlaceholderBucket reports whether the given artifacts_bucket value is a
// placeholder from km-config.example.yaml that an operator has not replaced.
// Returns true only for angle-bracket tokens (e.g. "<prefix>-artifacts-12345678") —
// those are unambiguously fake.
//
// Phase 84.4-08 UAT removed the prior `name == "km-artifacts-12345"` literal check:
// that name is a real, legitimate bucket on this operator's legacy install
// (predating Phase 84.3's `${prefix}-artifacts-${account_id}` derivation), so
// rejecting it broke `cfg.Load()` and every km command that read the config.
// Anyone with a literal placeholder-shaped name today is genuinely using that
// bucket; treat empty string as "unconfigured", not placeholder.
//
// Returns false for empty string (empty means unconfigured, not placeholder).
// Inline in config.go to avoid cross-package imports from config → cmd.
func isPlaceholderBucket(name string) bool {
	if name == "" {
		return false
	}
	if lt := strings.Index(name, "<"); lt >= 0 {
		if strings.Index(name[lt:], ">") >= 0 {
			return true
		}
	}
	return false
}

// ValidateArtifactsBucket validates the artifacts_bucket value loaded from
// km-config.yaml. Operators may use any S3 bucket name they choose; this
// function only catches obviously-unconfigured values:
//   - empty string returns nil (unconfigured is allowed at Load() time;
//     km configure / km init enforce non-empty separately).
//   - angle-bracket placeholders (any "<…>" token, e.g. the example.yaml
//     "<prefix>-artifacts-<account-id>") return an error.
//
// Lives in the config package to avoid import cycles from config.Load().
func ValidateArtifactsBucket(name string) error {
	if name == "" {
		return nil
	}
	if lt := strings.Index(name, "<"); lt >= 0 {
		if strings.Index(name[lt:], ">") >= 0 {
			return fmt.Errorf("artifacts_bucket=%q is a placeholder; set a real bucket name in km-config.yaml or re-run `km configure`", name)
		}
	}
	return nil
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

// ShouldBuildContainerImages reports whether `km init` should build and push
// the km-sandbox + sidecar container images to ECR. Container images are only
// pulled by the docker and ecs substrates; EC2 sandboxes get raw binaries
// from S3, so EC2-only deployments can disable this and skip ~2–10 min of
// docker buildx + ECR push per init. Defaults to true when unset for back-compat.
func (c *Config) ShouldBuildContainerImages() bool {
	if c == nil || c.ContainerSubstratesEnabled == nil {
		return true
	}
	return *c.ContainerSubstratesEnabled
}

// GetRegionLabel returns the short label for cfg.PrimaryRegion
// (e.g. us-east-1 → use1, ca-central-1 → cac1, ap-southeast-2 → apse2).
// Falls back to "use1" when PrimaryRegion is unset or malformed (<3 parts);
// mirrors pkg/compiler.RegionLabel without importing it (avoids pulling
// compiler into config). Used to suffix regional resource names like the
// platform KMS alias.
func (c *Config) GetRegionLabel() string {
	region := ""
	if c != nil {
		region = c.PrimaryRegion
	}
	if region == "" {
		return "use1"
	}
	parts := strings.Split(region, "-")
	if len(parts) < 3 {
		return region
	}
	areaShort := parts[1]
	switch parts[1] {
	case "east":
		areaShort = "e"
	case "west":
		areaShort = "w"
	case "central":
		areaShort = "c"
	case "south":
		areaShort = "s"
	case "north":
		areaShort = "n"
	case "southeast":
		areaShort = "se"
	case "northeast":
		areaShort = "ne"
	case "northwest":
		areaShort = "nw"
	case "southwest":
		areaShort = "sw"
	}
	return parts[0] + areaShort + parts[2]
}

// GetPlatformKMSAlias returns the KMS key alias used for SSM SecureString
// encryption (sandbox identity keys, GitHub tokens, Slack signing secret, etc.).
// Format: "alias/km-platform-{prefix}-{regionLabel}" — "km" is the hardcoded
// brand namespace, {prefix} is GetResourcePrefix(), {regionLabel} is the short
// region label. The brand-prefix-region structure groups all platform aliases
// under "alias/km-platform-*" for easy filtering, while still distinguishing
// per-install (multi-instance) and per-region keys. Defaults to
// "alias/km-platform-km-use1" when neither prefix nor region is configured.
func (c *Config) GetPlatformKMSAlias() string {
	return "alias/km-platform-" + c.GetResourcePrefix() + "-" + c.GetRegionLabel()
}

// GetEmailDomain returns the full email domain (e.g. "sandboxes.klankermaker.ai").
// Falls back to "sandboxes.klankermaker.ai" when both fields are empty or the receiver
// is nil — mirrors the nil-safety pattern used by GetResourcePrefix.
func (c *Config) GetEmailDomain() string {
	sub := "sandboxes"
	if c != nil && c.EmailSubdomain != "" {
		sub = c.EmailSubdomain
	}
	domain := "klankermaker.ai"
	if c != nil && c.Domain != "" {
		domain = c.Domain
	}
	return sub + "." + domain
}

// GetSsmPrefix returns the SSM parameter path prefix (e.g. "/km/").
// Uses GetResourcePrefix() which handles nil-safety and the "km" default.
func (c *Config) GetSsmPrefix() string {
	return "/" + c.GetResourcePrefix() + "/"
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

// GetSlackStreamMessagesTableName returns the Slack-stream-messages DynamoDB
// table name (Phase 68 transcript streaming). If SlackStreamMessagesTableName
// is explicitly set, that value wins. Otherwise the name is derived from
// GetResourcePrefix() + "-slack-stream-messages", which defaults to
// "km-slack-stream-messages" when no prefix is configured.
//
// Decision (Plan 68-03 Open Question 1): the suffix is "-slack-stream-messages"
// (NOT "-km-slack-stream-messages"), mirroring Phase 67's "-slack-threads"
// pattern, so the default prefix yields "km-slack-stream-messages" rather
// than "km-km-slack-stream-messages".
func (c *Config) GetSlackStreamMessagesTableName() string {
	if c == nil {
		return "km-slack-stream-messages"
	}
	if c.SlackStreamMessagesTableName != "" {
		return c.SlackStreamMessagesTableName
	}
	return c.GetResourcePrefix() + "-slack-stream-messages"
}

// GetSandboxSessionDocumentName returns the per-install SSM Session Manager
// document name, e.g. "km-Sandbox-Session", "tg-Sandbox-Session". Mirrors the
// computation in infra/modules/ssm-session-doc/v2.0.0/main.tf (Phase 84.4.1).
//
// Phase 84.4.1: replaces 5 hardcoded "KM-Sandbox-Session" callsites
// (shell.go:500, agent.go:356/430/619, agent_auth.go:157/411). Note the
// lowercase 'k' — v1.0.0 used "KM-Sandbox-Session" (uppercase); v2.0.0 uses
// "${prefix}-Sandbox-Session" with lowercase prefix per the v2.0.0 contract.
func (c *Config) GetSandboxSessionDocumentName() string {
	return c.GetResourcePrefix() + "-Sandbox-Session"
}

// GetSandboxTableName returns the DynamoDB sandboxes table name.
// Derives from GetResourcePrefix() + "-sandboxes", defaulting to "km-sandboxes".
func (c *Config) GetSandboxTableName() string {
	if c == nil {
		return "km-sandboxes"
	}
	if c.SandboxTableName != "" {
		return c.SandboxTableName
	}
	return c.GetResourcePrefix() + "-sandboxes"
}

// GetBudgetTableName returns the DynamoDB budgets table name.
// Derives from GetResourcePrefix() + "-budgets", defaulting to "km-budgets".
func (c *Config) GetBudgetTableName() string {
	if c == nil {
		return "km-budgets"
	}
	if c.BudgetTableName != "" {
		return c.BudgetTableName
	}
	return c.GetResourcePrefix() + "-budgets"
}

// GetIdentityTableName returns the DynamoDB identities table name.
// Derives from GetResourcePrefix() + "-identities", defaulting to "km-identities".
func (c *Config) GetIdentityTableName() string {
	if c == nil {
		return "km-identities"
	}
	if c.IdentityTableName != "" {
		return c.IdentityTableName
	}
	return c.GetResourcePrefix() + "-identities"
}

// GetSchedulesTableName returns the DynamoDB schedules table name.
// Derives from GetResourcePrefix() + "-schedules", defaulting to "km-schedules".
func (c *Config) GetSchedulesTableName() string {
	if c == nil {
		return "km-schedules"
	}
	if c.SchedulesTableName != "" {
		return c.SchedulesTableName
	}
	return c.GetResourcePrefix() + "-schedules"
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
