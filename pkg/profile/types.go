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
	Policy        PolicySpec        `yaml:"policy"`
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
}

// ExecutionSpec controls the shell environment within the sandbox.
type ExecutionSpec struct {
	// Shell is the path to the shell executable (e.g. /bin/bash).
	Shell string `yaml:"shell"`
	// WorkingDir is the initial working directory.
	WorkingDir string `yaml:"workingDir"`
	// Env is a map of additional environment variables to inject.
	Env map[string]string `yaml:"env,omitempty"`
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
	Permissions  []string `yaml:"permissions"`
}

// NetworkSpec controls egress network policy.
type NetworkSpec struct {
	Egress EgressSpec `yaml:"egress"`
}

// EgressSpec defines what outbound network traffic is permitted.
type EgressSpec struct {
	// AllowedDNSSuffixes is the list of DNS suffix patterns allowed for resolution.
	AllowedDNSSuffixes []string `yaml:"allowedDNSSuffixes"`
	// AllowedHosts is the list of explicit hostnames allowed for egress.
	AllowedHosts []string `yaml:"allowedHosts"`
	// AllowedMethods is the list of HTTP methods permitted.
	AllowedMethods []string `yaml:"allowedMethods"`
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

// ObservabilitySpec controls logging and observability destinations.
type ObservabilitySpec struct {
	CommandLog LogDestination `yaml:"commandLog"`
	NetworkLog LogDestination `yaml:"networkLog"`
}

// LogDestination defines where logs should be sent.
type LogDestination struct {
	// Destination is the log backend: cloudwatch, s3, or stdout.
	Destination string `yaml:"destination"`
	// LogGroup is the CloudWatch log group or S3 prefix.
	LogGroup string `yaml:"logGroup,omitempty"`
}

// PolicySpec defines security and access policy within the sandbox.
type PolicySpec struct {
	// AllowShellEscape permits shell escape sequences. Should be false for hardened profiles.
	AllowShellEscape bool `yaml:"allowShellEscape"`
	// AllowedCommands is the allowlist of commands the agent may invoke.
	AllowedCommands []string `yaml:"allowedCommands,omitempty"`
	// FilesystemPolicy controls read-only and writable path enforcement.
	FilesystemPolicy *FilesystemPolicy `yaml:"filesystemPolicy,omitempty"`
}

// FilesystemPolicy specifies filesystem access constraints.
type FilesystemPolicy struct {
	ReadOnlyPaths []string `yaml:"readOnlyPaths"`
	WritablePaths []string `yaml:"writablePaths"`
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
