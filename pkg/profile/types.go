// Package profile provides SandboxProfile type definitions and parsing logic
// for the klankrmkr sandbox platform. Profiles follow a Kubernetes-style
// apiVersion/kind/metadata/spec structure at klankermaker.ai/v1alpha1.
package profile

import (
	"github.com/goccy/go-yaml"
)

// SandboxProfile is the root type for a sandbox profile YAML document.
// It follows the Kubernetes resource model: apiVersion, kind, metadata, spec.
type SandboxProfile struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Extends    string   `yaml:"extends,omitempty"`
	Spec       Spec     `yaml:"spec"`
}

// Metadata holds profile identity information.
type Metadata struct {
	Name   string            `yaml:"name"`
	Labels map[string]string `yaml:"labels,omitempty"`
	Prefix string            `yaml:"prefix,omitempty"`
}

// Spec contains all required sections of a SandboxProfile.
// Artifacts is optional; all other sections are required.
type Spec struct {
	Lifecycle     LifecycleSpec     `yaml:"lifecycle"`
	Runtime       RuntimeSpec       `yaml:"runtime"`
	Execution     ExecutionSpec     `yaml:"execution"`
	SourceAccess  SourceAccessSpec  `yaml:"sourceAccess"`
	Network       NetworkSpec       `yaml:"network"`
	Identity      IdentitySpec      `yaml:"identity"`
	Sidecars      SidecarsSpec      `yaml:"sidecars"`
	Observability ObservabilitySpec `yaml:"observability"`
	Agent         AgentSpec         `yaml:"agent"`
	// Artifacts defines optional artifact collection and upload settings.
	// When nil, artifact collection is disabled.
	Artifacts *ArtifactsSpec `yaml:"artifacts,omitempty"`
	// Budget defines optional spend limits for compute and AI usage.
	// When nil, budget enforcement is disabled.
	Budget *BudgetSpec `yaml:"budget,omitempty"`
	// Email defines optional email signing and encryption policy.
	// When nil, email policy enforcement is disabled.
	Email *EmailSpec `yaml:"email,omitempty"`
	// OTP defines optional one-time password secrets injected at boot.
	// When nil, no OTP secrets are injected.
	OTP *OTPSpec `yaml:"otp,omitempty"`
	// CLI defines operator-side defaults for km shell / km agent commands.
	// These don't affect sandbox provisioning — only CLI behavior when connecting.
	CLI *CLISpec `yaml:"cli,omitempty"`
}

// OTPSpec defines one-time password secrets that are fetched from SSM at boot
// and deleted after first read, providing ephemeral bootstrap credentials.
type OTPSpec struct {
	// Secrets lists SSM parameter paths that are read once at boot and deleted.
	// After the sandbox reads each secret, the SSM parameter is deleted so the
	// credentials cannot be retrieved again.
	Secrets []string `yaml:"secrets,omitempty"`
}

// EmailSpec defines email signing, inbound verification, and encryption policies.
// Each field accepts "required", "optional", or "off".
type EmailSpec struct {
	// Signing controls whether outbound emails must be signed (e.g. DKIM/Ed25519).
	// Values: "required" | "optional" | "off"
	Signing string `yaml:"signing"`
	// VerifyInbound controls whether inbound email signatures must be verified.
	// Values: "required" | "optional" | "off"
	VerifyInbound string `yaml:"verifyInbound"`
	// Encryption controls whether email content must be encrypted.
	// Values: "required" | "optional" | "off"
	Encryption string `yaml:"encryption"`
	// Alias is a human-friendly dot-notation name (e.g. "research.team-a") registered
	// in km-identities. Optional — per-sandbox, not per-profile-template.
	Alias string `yaml:"alias,omitempty"`
	// AllowedSenders controls which sandboxes may send email to this sandbox.
	// Accepted values: "self" (own sandbox only), a sandbox ID, an alias wildcard
	// (e.g. "build.*"), or "*" (any sandbox).
	AllowedSenders []string `yaml:"allowedSenders,omitempty"`
}

// BudgetSpec defines optional spend limits for compute and AI workloads in a sandbox.
// Both Compute and AI sub-sections are optional (pointer, omitempty).
// WarningThreshold is the fraction of the limit at which alerts are triggered (default 0.8 when zero).
type BudgetSpec struct {
	Compute          *ComputeBudget `yaml:"compute,omitempty"`
	AI               *AIBudget      `yaml:"ai,omitempty"`
	WarningThreshold float64        `yaml:"warningThreshold,omitempty"` // default 0.8 when zero
}

// ComputeBudget caps EC2/Fargate compute spend for the sandbox.
type ComputeBudget struct {
	MaxSpendUSD float64 `yaml:"maxSpendUSD"`
}

// AIBudget caps Bedrock model spend for the sandbox across all models.
type AIBudget struct {
	MaxSpendUSD float64 `yaml:"maxSpendUSD"`
}

// ArtifactsSpec defines artifact collection paths and S3 upload settings.
type ArtifactsSpec struct {
	// Paths is a list of glob patterns or directory paths to collect as artifacts.
	Paths []string `yaml:"paths"`
	// MaxSizeMB is the maximum file size in megabytes to upload.
	// Set to 0 for unlimited.
	MaxSizeMB int `yaml:"maxSizeMB"`
	// ReplicationRegion is an optional secondary AWS region to replicate artifacts to.
	ReplicationRegion string `yaml:"replicationRegion,omitempty"`
}

// LifecycleSpec controls sandbox lifetime and teardown behavior.
type LifecycleSpec struct {
	// TTL is the maximum lifetime of the sandbox as a duration string (e.g. "24h").
	TTL string `yaml:"ttl"`
	// IdleTimeout is the duration after which an idle sandbox is torn down.
	IdleTimeout string `yaml:"idleTimeout"`
	// TeardownPolicy defines what happens when the sandbox expires: destroy, stop, or retain.
	TeardownPolicy string `yaml:"teardownPolicy"`
	// MaxLifetime is the absolute maximum duration from sandbox creation time (e.g. "72h").
	// When set, km extend will refuse to extend the sandbox TTL beyond CreatedAt + MaxLifetime.
	// Empty string means no cap (backward compatible).
	MaxLifetime string `yaml:"maxLifetime,omitempty" json:"maxLifetime,omitempty"`
}

// AdditionalVolumeSpec defines an extra EBS volume to attach and auto-mount.
type AdditionalVolumeSpec struct {
	// Size is the volume size in GB (must be >= 1).
	Size int `yaml:"size" json:"size"`
	// MountPoint is the filesystem path to mount the volume at (e.g. /data).
	MountPoint string `yaml:"mountPoint" json:"mountPoint"`
	// Encrypted indicates whether the EBS volume should be encrypted at rest.
	Encrypted bool `yaml:"encrypted,omitempty" json:"encrypted,omitempty"`
}

// RuntimeSpec controls the compute substrate and instance configuration.
type RuntimeSpec struct {
	// Substrate is the compute backend: ec2 or ecs.
	Substrate string `yaml:"substrate"`
	// Spot indicates whether spot instances should be used (EC2 only).
	Spot bool `yaml:"spot"`
	// InstanceType is the EC2 instance type (e.g. t3.medium) or ECS task size.
	InstanceType string `yaml:"instanceType"`
	// Region is the AWS region to provision in.
	Region string `yaml:"region"`
	// RootVolumeSize is the root EBS volume size in GB. 0 or omitted uses the AMI default.
	RootVolumeSize int `yaml:"rootVolumeSize,omitempty" json:"rootVolumeSize,omitempty"`
	// AdditionalVolume defines an optional extra EBS volume to attach and auto-mount (EC2 only).
	AdditionalVolume *AdditionalVolumeSpec `yaml:"additionalVolume,omitempty" json:"additionalVolume,omitempty"`
	// Hibernation enables EC2 hibernation (on-demand instances only; incompatible with spot).
	Hibernation bool `yaml:"hibernation,omitempty" json:"hibernation,omitempty"`
	// AMI is an AMI slug to resolve per-region (e.g. "ubuntu-24.04"). Empty defaults to amazon-linux-2023.
	AMI string `yaml:"ami,omitempty" json:"ami,omitempty"`
	// MountEFS controls whether this sandbox mounts the regional EFS shared filesystem (EC2 only).
	// When true, the EFS filesystem ID is read from infra/live/<region>/efs/outputs.json and
	// passed into userdata to mount at EFSMountPoint.
	MountEFS bool `yaml:"mountEFS,omitempty" json:"mountEFS,omitempty"`
	// EFSMountPoint is the filesystem path where EFS is mounted. Defaults to "/shared" when omitted.
	EFSMountPoint string `yaml:"efsMountPoint,omitempty" json:"efsMountPoint,omitempty"`
}

// ExecutionSpec controls the shell environment within the sandbox.
type ExecutionSpec struct {
	// Shell is the path to the shell executable (e.g. /bin/bash).
	Shell string `yaml:"shell"`
	// WorkingDir is the initial working directory.
	WorkingDir string `yaml:"workingDir"`
	// UseBedrock routes Anthropic API calls through AWS Bedrock instead of api.anthropic.com.
	// When true, the compiler injects CLAUDE_CODE_USE_BEDROCK=1, ANTHROPIC_BASE_URL (Bedrock endpoint),
	// and model ID mappings (Sonnet/Opus/Haiku) as environment variables. No ANTHROPIC_API_KEY needed —
	// authentication uses the sandbox's AWS credentials via SigV4.
	UseBedrock bool `yaml:"useBedrock,omitempty"`
	// Env is a map of additional environment variables to inject.
	Env map[string]string `yaml:"env,omitempty"`
	// InitCommands is a list of shell commands to run after the sandbox starts.
	// Runs sequentially as root before the user session begins.
	// Example: ["apt-get update", "npm install -g @anthropic/claude-code"]
	InitCommands []string `yaml:"initCommands,omitempty"`
	// InitScripts is a list of local script file paths to upload and run on startup.
	// Paths are relative to the profile file or repo root.
	// Scripts are uploaded to S3 alongside the profile and executed in order.
	// Example: ["scripts/setup-claude.sh", "scripts/install-tools.sh"]
	InitScripts []string `yaml:"initScripts,omitempty"`
	// Rsync is the name of a saved home directory snapshot to restore on boot.
	// Created via `km rsync save <sandbox> <name>`. Restored from S3 before initCommands.
	Rsync string `yaml:"rsync,omitempty"`
	// RsyncPaths is a list of paths relative to the sandbox user's $HOME to include
	// in rsync snapshots. Shell wildcards are supported (e.g. projects/*/config).
	// When empty, the rsync command uses its default behaviour.
	RsyncPaths []string `yaml:"rsyncPaths,omitempty"`
	// RsyncFileList is the path to a local YAML file containing additional rsync paths.
	// Resolved from the operator's cwd at `km rsync save` time.
	RsyncFileList string `yaml:"rsyncFileList,omitempty"`
	// Privileged grants the sandbox user wheel group membership and
	// passwordless sudo access. When false (default), the sandbox user
	// has no sudo capability. Operators who want to remove sudo entirely
	// from the instance can use a custom AMI without sudo installed.
	Privileged bool `yaml:"privileged,omitempty"`
	// ConfigFiles is a map of absolute file paths to their contents.
	// Each entry is written to the sandbox filesystem during bootstrap,
	// owned by the sandbox user. Use this to pre-seed tool configuration
	// (e.g. Claude settings.json, Goose config, .gitconfig).
	//
	// Example:
	//   configFiles:
	//     "/home/sandbox/.claude/settings.json": |
	//       {"trustedDirectories":["/home/sandbox","/workspace"]}
	ConfigFiles map[string]string `yaml:"configFiles,omitempty"`
}

// SourceAccessSpec controls access to source code repositories.
type SourceAccessSpec struct {
	// Mode is the access mode: allowlist (default).
	Mode   string       `yaml:"mode"`
	GitHub *GitHubAccess `yaml:"github,omitempty"`
}

// GitHubAccess defines GitHub repository access controls.
type GitHubAccess struct {
	AllowedRepos []string `yaml:"allowedRepos"`
	AllowedRefs  []string `yaml:"allowedRefs"`
}

// NetworkSpec controls egress network policy.
type NetworkSpec struct {
	Egress    EgressSpec `yaml:"egress"`
	HTTPSOnly bool       `yaml:"httpsOnly,omitempty"` // Block plain HTTP; on EC2 security groups enforce this, on Docker the proxy enforces it
	// Enforcement selects the network enforcement mechanism.
	// "proxy" (default): iptables DNAT + proxy sidecars (current behavior).
	// "ebpf": pure eBPF cgroup programs, no iptables.
	// "both": eBPF primary + proxy sidecars for L7 inspection.
	// Omitting the field is equivalent to "proxy" (backwards compatible).
	// eBPF enforcement is scoped to EC2 substrate only in Phase 40.
	Enforcement string `yaml:"enforcement,omitempty"`
}

// EgressSpec defines what outbound network traffic is permitted.
type EgressSpec struct {
	// AllowedDNSSuffixes is the list of DNS suffix patterns allowed for resolution.
	AllowedDNSSuffixes []string `yaml:"allowedDNSSuffixes"`
	// AllowedHosts is the list of explicit hostnames allowed for egress.
	AllowedHosts []string `yaml:"allowedHosts"`
}

// IdentitySpec controls AWS IAM identity and session configuration.
type IdentitySpec struct {
	// RoleSessionDuration is the maximum duration for assumed role sessions.
	RoleSessionDuration string `yaml:"roleSessionDuration"`
	// AllowedRegions is the list of AWS regions the sandbox may access.
	AllowedRegions []string `yaml:"allowedRegions"`
	// SessionPolicy is the IAM session policy scope: minimal, standard, etc.
	SessionPolicy string `yaml:"sessionPolicy"`
	// AllowedSecretPaths is the allowlist of SSM Parameter Store paths the sandbox
	// may read at boot time. Secrets are injected as environment variables via user-data.
	AllowedSecretPaths []string `yaml:"allowedSecretPaths,omitempty"`
}

// SidecarsSpec defines the sidecar processes that run alongside the sandbox.
type SidecarsSpec struct {
	DNSProxy  SidecarConfig `yaml:"dnsProxy"`
	HTTPProxy SidecarConfig `yaml:"httpProxy"`
	AuditLog  SidecarConfig `yaml:"auditLog"`
	Tracing   SidecarConfig `yaml:"tracing"`
}

// SidecarConfig holds configuration for a single sidecar process or container.
type SidecarConfig struct {
	Enabled bool   `yaml:"enabled"`
	Image   string `yaml:"image"`
}

// ClaudeTelemetrySpec controls Claude Code OpenTelemetry export settings.
type ClaudeTelemetrySpec struct {
	Enabled        *bool `yaml:"enabled,omitempty"`        // default true — master switch for Claude Code OTEL
	LogPrompts     bool  `yaml:"logPrompts,omitempty"`     // default false — OTEL_LOG_USER_PROMPTS
	LogToolDetails bool  `yaml:"logToolDetails,omitempty"` // default false — OTEL_LOG_TOOL_DETAILS
}

// IsEnabled returns true if telemetry is enabled (default: true when nil).
func (c *ClaudeTelemetrySpec) IsEnabled() bool {
	if c == nil || c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// ObservabilitySpec controls logging and observability destinations.
type ObservabilitySpec struct {
	CommandLog      LogDestination       `yaml:"commandLog"`
	NetworkLog      LogDestination       `yaml:"networkLog"`
	ClaudeTelemetry *ClaudeTelemetrySpec `yaml:"claudeTelemetry,omitempty"`
	TlsCapture      *TlsCaptureSpec     `yaml:"tlsCapture,omitempty"`
	// LearnMode enables traffic observation recording on the eBPF enforcer.
	// When true, the enforcer records all DNS queries and TLS connections
	// so km shell --learn can generate a SandboxProfile from observed traffic.
	LearnMode bool `yaml:"learnMode,omitempty"`
}

// TlsCaptureSpec controls TLS/SSL plaintext capture via eBPF uprobes.
// When enabled, uprobes attach to TLS library functions (e.g. SSL_read/SSL_write)
// to capture plaintext before encryption / after decryption.
type TlsCaptureSpec struct {
	Enabled         bool     `yaml:"enabled"`
	Libraries       []string `yaml:"libraries,omitempty"`       // openssl, gnutls, nss, go, rustls, all
	CapturePayloads bool     `yaml:"capturePayloads,omitempty"` // capture full payload content (default false)
}

// IsEnabled returns true if TLS capture is configured and enabled.
func (t *TlsCaptureSpec) IsEnabled() bool {
	return t != nil && t.Enabled
}

// EffectiveLibraries returns the list of libraries to instrument.
// If "all" is in the list, returns all supported library names.
// If the list is empty (with enabled=true), defaults to openssl only.
// Currently only "openssl" is implemented; others are accepted by schema but no-op at runtime.
func (t *TlsCaptureSpec) EffectiveLibraries() []string {
	if t == nil || !t.Enabled {
		return nil
	}
	if len(t.Libraries) == 0 {
		return []string{"openssl"} // default to openssl only
	}
	for _, l := range t.Libraries {
		if l == "all" {
			return []string{"openssl", "gnutls", "nss", "go", "rustls"}
		}
	}
	return t.Libraries
}

// LogDestination defines where logs should be sent.
type LogDestination struct {
	// Destination is the log backend: cloudwatch, s3, or stdout.
	Destination string `yaml:"destination"`
	// LogGroup is the CloudWatch log group or S3 prefix.
	LogGroup string `yaml:"logGroup,omitempty"`
}

// AgentSpec controls behavior of the AI agent workload running in the sandbox.
type AgentSpec struct {
	// MaxConcurrentTasks limits the number of parallel tasks the agent may run.
	MaxConcurrentTasks int `yaml:"maxConcurrentTasks"`
	// TaskTimeout is the maximum duration for a single agent task.
	TaskTimeout string `yaml:"taskTimeout"`
	// AllowedTools is the list of tool names the agent is permitted to use.
	AllowedTools []string `yaml:"allowedTools,omitempty"`
}

// CLISpec defines operator-side defaults for km shell / km agent commands.
// These settings don't affect sandbox provisioning — only CLI behavior when
// connecting to or running agents in the sandbox.
type CLISpec struct {
	// NoBedrock makes --no-bedrock the default for km shell and km agent run.
	// The sandbox is still provisioned with Bedrock vars; this only affects
	// the operator's connection. Override with --bedrock on the CLI.
	NoBedrock bool `yaml:"noBedrock,omitempty"`
	// ClaudeArgs are appended to the `claude` command line when launching an
	// interactive session via `km agent <sb> --claude`. Use to bake in flags
	// like --dangerously-skip-permissions or --model for a given profile.
	// Args supplied after `--` on the CLI still take precedence.
	ClaudeArgs []string `yaml:"claudeArgs,omitempty"`
}

// Parse unmarshals a SandboxProfile from raw YAML bytes.
// It returns an error if the YAML is syntactically invalid.
// Use Validate() for schema and semantic validation.
func Parse(data []byte) (*SandboxProfile, error) {
	var p SandboxProfile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
