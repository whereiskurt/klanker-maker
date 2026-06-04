// Package profile provides SandboxProfile type definitions and parsing logic
// for the klanker-maker sandbox platform. Profiles follow a Kubernetes-style
// apiVersion/kind/metadata/spec structure at klankermaker.ai/v1alpha2.
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
	IAM           IAMSpec           `yaml:"iam"`
	Sidecars      SidecarsSpec      `yaml:"sidecars"`
	Observability ObservabilitySpec `yaml:"observability"`
	// Phase 92 (Wave 1): the dead top-level `agent:` block (MaxConcurrentTasks,
	// TaskTimeout, AllowedTools) was removed — it was never read by any code path.
	// Wave 4 re-introduces an `agent:` block with brand-new structured tool-gating
	// semantics as a *AgentSpec pointer.
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
	// Agent defines the structured agent block (Phase 92 Wave 4): default agent
	// selection plus per-agent (claude/codex) tool gating, trusted directories,
	// permissions passthrough, and CLI args. Optional — when nil, the agent
	// defaults to "claude" and no tool gating is synthesized. Replaces the
	// re-homed cli.agent/cli.claudeArgs/cli.codexArgs fields. Wave 5 owns the
	// synthesizer (agent.claude.tools.* → Claude-Code settings.json).
	Agent *AgentSpec `yaml:"agent,omitempty" json:"agent,omitempty"`
	// Secrets defines an optional SOPS-encrypted bundle to inject as environment
	// variables at sandbox boot. When nil (absent), no secret injection occurs —
	// backwards compatible with all pre-Phase-89 profiles.
	Secrets *SecretsSpec `yaml:"secrets,omitempty" json:"secrets,omitempty"`
	// Notification defines optional operator notification policy (email + Slack +
	// per-event gates). When nil (absent), no notification is configured.
	// Phase 92 (Wave 2): replaces the 14 cli.notify* fields with a structured
	// block. Every sub-field is optional and pointer-typed where a tri-state
	// (unset / true / false) matters, so child profiles can override individual
	// settings without clobbering the parent (see mergeNotificationSpec).
	Notification *NotificationSpec `yaml:"notification,omitempty" json:"notification,omitempty"`
}

// NotificationSpec is the Phase 92 structured replacement for the old
// cli.notify* fields. All sub-blocks are optional pointers so inheritance can
// merge them field-by-field.
type NotificationSpec struct {
	// Events gates which Claude Code hook events trigger a notification.
	Events *NotificationEventsSpec `json:"events,omitempty" yaml:"events,omitempty"`
	// Email configures email delivery of notifications.
	Email *NotificationEmailSpec `json:"email,omitempty" yaml:"email,omitempty"`
	// Slack configures Slack delivery of notifications (incl. inbound chat,
	// transcript streaming, and auto-invites).
	Slack *NotificationSlackSpec `json:"slack,omitempty" yaml:"slack,omitempty"`
}

// NotificationEventsSpec gates which hook events fire a notification.
// Replaces cli.notifyOnPermission / notifyOnIdle / notifyCooldownSeconds.
type NotificationEventsSpec struct {
	// OnPermission emails/notifies the operator on a Claude Notification hook
	// (permission prompt). nil = default false.
	OnPermission *bool `json:"onPermission,omitempty" yaml:"onPermission,omitempty"`
	// OnIdle emails/notifies the operator on a Claude Stop hook (idle / turn
	// complete). nil = default false.
	OnIdle *bool `json:"onIdle,omitempty" yaml:"onIdle,omitempty"`
	// CooldownSeconds suppresses notifications within N seconds of the last send
	// (per-sandbox, shared across event types). nil = no cooldown.
	CooldownSeconds *int `json:"cooldownSeconds,omitempty" yaml:"cooldownSeconds,omitempty"`
}

// NotificationEmailSpec configures email delivery.
// Replaces cli.notifyEmailEnabled / notificationEmailAddress.
type NotificationEmailSpec struct {
	// Enabled controls whether the notify-hook dispatches email. nil = default
	// behaviorally true (Phase 62 backward compat); &false skips email.
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	// Address overrides the recipient (default operator inbox). Empty = default.
	Address string `json:"address,omitempty" yaml:"address,omitempty"`
}

// NotificationSlackSpec configures Slack delivery.
// Replaces cli.notifySlackEnabled / notifySlackPerSandbox /
// notifySlackChannelOverride / slackArchiveOnDestroy and the inbound /
// transcript / invites sub-blocks.
type NotificationSlackSpec struct {
	// Enabled enables Slack delivery. nil = disabled (default).
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	// PerSandbox creates a #sb-{id} channel at km create. nil = default false
	// (use the platform-wide shared channel).
	PerSandbox *bool `json:"perSandbox,omitempty" yaml:"perSandbox,omitempty"`
	// ChannelOverride hard-pins notifications to a Slack channel ID
	// (^C[A-Z0-9]+$). Mutually exclusive with PerSandbox=true. Empty = default.
	ChannelOverride string `json:"channelOverride,omitempty" yaml:"channelOverride,omitempty"`
	// ChannelName customizes the auto-created per-sandbox channel name (requires
	// PerSandbox=true). Used verbatim (sanitized to Slack rules) with NO forced
	// "sb-" prefix; supports {profile} (metadata.name), {alias}, and {id} token
	// substitution. Empty = the default "sb-{alias}" / "sb-{id}" derivation.
	// Mutually exclusive with ChannelOverride.
	ChannelName string `json:"channelName,omitempty" yaml:"channelName,omitempty"`
	// ArchiveOnDestroy archives the per-sandbox channel at km destroy. nil =
	// default true. Only meaningful when PerSandbox=true.
	ArchiveOnDestroy *bool `json:"archiveOnDestroy,omitempty" yaml:"archiveOnDestroy,omitempty"`
	// Inbound configures bidirectional Slack chat (Phase 67).
	Inbound *NotificationSlackInboundSpec `json:"inbound,omitempty" yaml:"inbound,omitempty"`
	// Transcript configures per-turn transcript streaming (Phase 68).
	Transcript *NotificationSlackTranscriptSpec `json:"transcript,omitempty" yaml:"transcript,omitempty"`
	// Invites configures auto-invite of operators to the per-sandbox channel (Phase 72).
	Invites *NotificationSlackInvitesSpec `json:"invites,omitempty" yaml:"invites,omitempty"`
}

// NotificationSlackInboundSpec configures bidirectional Slack chat.
// Replaces cli.notifySlackInboundEnabled / notifySlackInboundMentionOnly /
// notifySlackInboundReactAlways.
type NotificationSlackInboundSpec struct {
	// Enabled enables inbound dispatch. nil = default false. Requires
	// slack.enabled=true and slack.perSandbox=true; incompatible with
	// slack.channelOverride.
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	// MentionOnly gates polite-bot mode (Phase 91). nil = channel-mode default.
	MentionOnly *bool `json:"mentionOnly,omitempty" yaml:"mentionOnly,omitempty"`
	// ReactAlways controls whether the km-slack bridge posts a 👀 reaction on
	// every inbound message (true, default) or only on top-level engagement
	// messages (false). Phase 91.4/91.5 (re-homed from cli.notifySlackInboundReactAlways
	// in Phase 92, Wave 3). nil = default true (chatty-reactor back-compat).
	ReactAlways *bool `json:"reactAlways,omitempty" yaml:"reactAlways,omitempty"`
}

// NotificationSlackTranscriptSpec configures per-turn transcript streaming.
// Replaces cli.notifySlackTranscriptEnabled.
type NotificationSlackTranscriptSpec struct {
	// Enabled enables transcript streaming. nil = default false. Requires
	// slack.enabled=true and slack.perSandbox=true; incompatible with
	// slack.channelOverride.
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}

// NotificationSlackInvitesSpec configures auto-invites to the per-sandbox channel.
// Replaces cli.notifySlackInviteEmails / useSlackConnect.
type NotificationSlackInvitesSpec struct {
	// Emails is the list of addresses to auto-invite. Requires slack.enabled=true.
	// Empty = no-op.
	Emails []string `json:"emails,omitempty" yaml:"emails,omitempty"`
	// UseConnect gates the Slack Connect fallback for non-native addresses. nil =
	// default true.
	UseConnect *bool `json:"useConnect,omitempty" yaml:"useConnect,omitempty"`
}

// RuntimeVSCodeSpec gates VS Code Remote-SSH provisioning.
// Phase 92 (Wave 2): replaces cli.vscodeEnabled, re-homed under spec.runtime.vscode.
type RuntimeVSCodeSpec struct {
	// Enabled gates the sshd + authorized_keys userdata block. nil = default
	// enabled (omit-means-true); &false skips SSH provisioning.
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}

// RuntimeDesktopSpec gates KasmVNC graphical session provisioning.
// Phase 93: new opt-in (heavy install) — nil block or nil Enabled both return
// false from IsDesktopEnabled. Deliberate opposite of VSCode's default-on.
// Fields: Mode (kiosk|full), Browsers (subset of firefox/chromium/chrome/brave),
// Geometry (WxH pattern, default 1920x1080).
type RuntimeDesktopSpec struct {
	// Enabled gates KasmVNC + DE package install. nil = disabled (opt-in; heavy install).
	// Must be set to true explicitly to provision the desktop session.
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	// Mode selects the desktop mode. "kiosk" = matchbox-wm + single-browser fullscreen;
	// "full" = XFCE4 full desktop. Default "kiosk".
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`
	// Browsers lists which browsers to install. Subset of [firefox, chromium, chrome, brave].
	// Default [firefox]. For kiosk mode, browsers[0] is launched maximized.
	Browsers []string `json:"browsers,omitempty" yaml:"browsers,omitempty"`
	// Geometry sets the VNC display resolution in WxH format (e.g. "1920x1080").
	// Default 1920x1080.
	Geometry string `json:"geometry,omitempty" yaml:"geometry,omitempty"`
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

// AdditionalSnapshotSpec describes one snapshot-backed EBS volume entry (Phase 87).
// Multiple entries can coexist with the singular AdditionalVolume field.
type AdditionalSnapshotSpec struct {
	// SnapshotID is the AWS EBS snapshot ID to restore (e.g. snap-0123abcdef01234567).
	SnapshotID string `yaml:"snapshotId" json:"snapshotId"`
	// MountPoint is the filesystem path to mount the restored volume at (e.g. /data).
	MountPoint string `yaml:"mountPoint" json:"mountPoint"`
	// Device optionally pins the volume to a specific device in /dev/sd[f-p].
	// When omitted, the compiler auto-selects the next available device.
	Device string `yaml:"device,omitempty" json:"device,omitempty"`
	// Encrypted is a pointer so omitted (nil) marshals to terraform null,
	// allowing AWS to inherit the snapshot's encryption state.
	// Plain bool would conflate "omitted" with "false" — wrong semantics.
	Encrypted *bool `yaml:"encrypted,omitempty" json:"encrypted,omitempty"`
	// Size optionally overrides the volume size in GB. Must be >= snapshot's VolumeSize.
	// When 0/omitted, the snapshot's native size is used.
	Size int `yaml:"size,omitempty" json:"size,omitempty"`
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
	// AdditionalSnapshots defines zero or more snapshot-backed EBS volumes to attach and auto-mount (EC2 only, Phase 87).
	// Each entry materialises a fresh aws_ebs_volume from an existing EBS snapshot.
	// Multiple entries are allowed and processed in declaration order.
	AdditionalSnapshots []AdditionalSnapshotSpec `yaml:"additionalSnapshots,omitempty" json:"additionalSnapshots,omitempty"`
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
	// VSCode gates VS Code Remote-SSH provisioning (sshd + authorized_keys).
	// Phase 92 (Wave 2): replaces the old cli.vscodeEnabled gate. nil = default
	// enabled. See IsVSCodeEnabled.
	VSCode *RuntimeVSCodeSpec `yaml:"vscode,omitempty" json:"vscode,omitempty"`
	// Desktop gates KasmVNC graphical session provisioning. nil = disabled (opt-in; heavy install).
	// Phase 93: KasmVNC-backed browser/XFCE remote session over SSM port-forward.
	// See IsDesktopEnabled.
	Desktop *RuntimeDesktopSpec `yaml:"desktop,omitempty" json:"desktop,omitempty"`
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

// IAMSpec controls AWS IAM identity and session configuration.
// Phase 92 (Wave 1): renamed from IdentitySpec; the dead SessionPolicy field
// was removed (never read by any code path).
type IAMSpec struct {
	// RoleSessionDuration is the maximum duration for assumed role sessions.
	RoleSessionDuration string `yaml:"roleSessionDuration"`
	// AllowedRegions is the list of AWS regions the sandbox may access.
	AllowedRegions []string `yaml:"allowedRegions"`
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

// Phase 92 (Wave 1): the dead AgentSpec struct (MaxConcurrentTasks, TaskTimeout,
// AllowedTools) was removed here. Wave 4 re-introduces an AgentSpec with new
// structured tool-gating semantics.

// SecretsSpec defines SOPS-encrypted secret injection for sandboxes (Phase 89).
// The bundle's top-level keys become environment variables in /etc/sandbox-secrets.env
// at boot. Reserved keys "sops" and "_meta" are ignored.
type SecretsSpec struct {
	// SopsFile is a path (relative to the profile YAML location) to a
	// SOPS-encrypted YAML bundle. The bundle's top-level keys become
	// environment variables in /etc/sandbox-secrets.env at boot.
	// Reserved keys "sops" and "_meta" are ignored (sops embeds metadata).
	// Empty (the zero value) means no secret injection — backwards compatible.
	SopsFile string `yaml:"sopsFile,omitempty" json:"sopsFile,omitempty"`
}

// CLISpec defines operator-side defaults for km shell / km agent commands.
// These settings don't affect sandbox provisioning — only CLI behavior when
// connecting to or running agents in the sandbox.
type CLISpec struct {
	// NoBedrock makes --no-bedrock the default for km shell and km agent run.
	// The sandbox is still provisioned with Bedrock vars; this only affects
	// the operator's connection. Override with --bedrock on the CLI.
	NoBedrock bool `yaml:"noBedrock,omitempty"`

	// Phase 92 (Wave 4) re-homed Agent/ClaudeArgs/CodexArgs out of CLISpec into
	// the structured spec.agent block (AgentSpec). After Wave 4, CLISpec carries
	// ONLY NoBedrock. Per RESEARCH.md Pitfall 6 we keep this as a single-field
	// struct (not collapsed to a scalar spec.noBedrock) for naming consistency.
	//
	// Phase 92: the 15 notification fields formerly carried here
	// (NotifyOnPermission, NotifyOnIdle, NotifyCooldownSeconds,
	// NotificationEmailAddress, NotifyEmailEnabled, NotifySlackEnabled,
	// NotifySlackPerSandbox, NotifySlackChannelOverride, SlackArchiveOnDestroy,
	// NotifySlackInboundEnabled, NotifySlackInboundMentionOnly,
	// NotifySlackInboundReactAlways, NotifySlackTranscriptEnabled,
	// NotifySlackInviteEmails, UseSlackConnect) and VSCodeEnabled were lifted out
	// of CLISpec into the new spec.notification block (see NotificationSpec below)
	// and spec.runtime.vscode respectively. Wave 2 removed 14; Wave 3 re-homed the
	// 15th (NotifySlackInboundReactAlways → notification.slack.inbound.reactAlways).
}

// AgentSpec is the Phase 92 (Wave 4) structured agent block. It carries the
// default agent selection plus per-agent (claude/codex) tool gating, trusted
// directories, a permissions passthrough, and per-invoke CLI args. It replaces
// the re-homed cli.agent / cli.claudeArgs / cli.codexArgs fields.
//
// Optional (NOT in spec.required). When nil, the agent defaults to "claude" and
// no tool-gating settings.json is synthesized. Wave 5 owns the synthesizer
// (agent.claude.tools.* → Claude-Code settings.json permissions.allow/deny).
type AgentSpec struct {
	// Default selects the default agent CLI for Slack inbound dispatch and
	// `km agent run` / `km shell` when no --claude/--codex flag is passed.
	// One of "claude" or "codex"; absence or "" is equivalent to "claude".
	// Validated by the JSON Schema enum. Phase 70 — see docs/codex-parity.md
	// for the runtime KM_AGENT env var emission and per-message Slack prefix
	// routing.
	Default string `json:"default,omitempty" yaml:"default,omitempty"`
	// Claude carries Claude-Code-specific tool gating, trusted directories,
	// permissions passthrough, and CLI args. Optional.
	Claude *AgentClaudeSpec `json:"claude,omitempty" yaml:"claude,omitempty"`
	// Codex carries Codex-specific tool gating and CLI args. Optional.
	Codex *AgentCodexSpec `json:"codex,omitempty" yaml:"codex,omitempty"`
}

// AgentClaudeSpec carries Claude-Code-specific configuration. Wave 5's
// synthesizer reads Tools/TrustedDirectories/Permissions to emit a
// ~/.claude/settings.json; this wave only defines the typed shape.
type AgentClaudeSpec struct {
	// TrustedDirectories is the list of directories Claude Code trusts without
	// prompting (settings.json trustedDirectories).
	TrustedDirectories []string `json:"trustedDirectories,omitempty" yaml:"trustedDirectories,omitempty"`
	// Tools gates which tools are auto-approved / denied. Wave 5 synthesizes
	// this into permissions.allow / permissions.deny.
	Tools AgentToolsSpec `json:"tools,omitempty" yaml:"tools,omitempty"`
	// Permissions is a passthrough map for Claude-Code settings.json keys not
	// worth typing individually (e.g. per-release additions). The ONE
	// passthrough exception per the CONTEXT.md locked decision — type
	// everything else aggressively.
	Permissions map[string]any `json:"permissions,omitempty" yaml:"permissions,omitempty"`
	// Args are appended to the `claude` command line when launching via
	// `km agent <sb> --claude`. Replaces cli.claudeArgs. User-supplied args
	// after `--` still take precedence.
	Args []string `json:"args,omitempty" yaml:"args,omitempty"`
}

// AgentCodexSpec carries Codex-specific configuration. Codex has no
// trustedDirectories / permissions passthrough — only tool gating and args.
type AgentCodexSpec struct {
	// Tools gates which tools are auto-approved / denied for Codex.
	Tools AgentToolsSpec `json:"tools,omitempty" yaml:"tools,omitempty"`
	// Args are appended to the `codex exec` command line when launching via
	// `km agent run <sb> --codex`. Replaces cli.codexArgs. User-supplied args
	// still take precedence.
	Args []string `json:"args,omitempty" yaml:"args,omitempty"`
}

// AgentToolsSpec is the shared tool-gating shape for both Claude and Codex.
// Value-typed (embedded directly in the parent spec, not a pointer) — an empty
// AgentToolsSpec means "no gating".
type AgentToolsSpec struct {
	// AutoApprove is the list of tools auto-approved without prompting. Wave 5
	// synthesizes this into permissions.allow (NOT legacy autoApprove).
	AutoApprove []string `json:"autoApprove,omitempty" yaml:"autoApprove,omitempty"`
	// Deny is the list of tools denied outright. Wave 5 synthesizes this into
	// permissions.deny (NOT legacy disallowedTools).
	Deny []string `json:"deny,omitempty" yaml:"deny,omitempty"`
}

// IsVSCodeEnabled returns true when the operator's profile has not opted out of VS Code
// Remote-SSH provisioning. Default true (nil vscode block or nil Enabled both return true).
//
// Phase 92 (Wave 2): the gate moved from spec.cli.vscodeEnabled to
// spec.runtime.vscode.enabled, so this helper now takes a *RuntimeVSCodeSpec.
// Wave 3 updates the callers (pkg/compiler/userdata.go, internal/app/cmd/create.go,
// internal/app/cmd/vscode.go) to pass p.Spec.Runtime.VSCode — they WILL fail to
// compile until then, by design.
//
// Used by:
//   - pkg/compiler/userdata.go to gate the conditional userdata block (Plan 73-04)
//   - internal/app/cmd/create.go to decide whether to generate a keypair (Plan 73-05)
func IsVSCodeEnabled(vscode *RuntimeVSCodeSpec) bool {
	if vscode == nil || vscode.Enabled == nil {
		return true
	}
	return *vscode.Enabled
}

// IsDesktopEnabled returns true only when the operator has explicitly set
// spec.runtime.desktop.enabled: true. Default is FALSE (nil block or nil Enabled
// both return false) — this is the deliberate opt-in opposite of IsVSCodeEnabled.
//
// Phase 93: KasmVNC desktop is a heavy install (VNC server + DE + browsers).
// Operators must explicitly opt in; omitting the block skips all desktop provisioning.
//
// Used by:
//   - pkg/compiler/userdata.go to gate the KasmVNC userdata block (Plan 93-03)
//   - internal/app/cmd/create.go to decide whether to generate desktop credential (Plan 93-04)
func IsDesktopEnabled(desktop *RuntimeDesktopSpec) bool {
	if desktop == nil || desktop.Enabled == nil {
		return false
	}
	return *desktop.Enabled
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
