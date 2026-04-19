package profile_test

import (
	"os"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// TestArtifactsSpecParsesFromYAML verifies that ArtifactsSpec round-trips from YAML.
func TestArtifactsSpecParsesFromYAML(t *testing.T) {
	yamlData := `
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: artifact-test
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 1h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: [".amazonaws.com"]
      allowedHosts: []

  identity:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:
      enabled: true
      image: "km-dns-proxy:latest"
    httpProxy:
      enabled: true
      image: "km-http-proxy:latest"
    auditLog:
      enabled: true
      image: "km-audit-log:latest"
    tracing:
      enabled: false
      image: "km-otel:latest"
  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch

  agent:
    maxConcurrentTasks: 2
    taskTimeout: 30m
  artifacts:
    paths:
      - "./output/**"
    maxSizeMB: 100
    replicationRegion: "us-west-2"
`

	p, err := profile.Parse([]byte(yamlData))
	if err != nil {
		t.Fatalf("expected profile with artifacts to parse without error, got: %v", err)
	}

	if p.Spec.Artifacts == nil {
		t.Fatal("expected spec.artifacts to be populated, got nil")
	}

	arts := p.Spec.Artifacts
	if len(arts.Paths) != 1 || arts.Paths[0] != "./output/**" {
		t.Errorf("expected paths=[./output/**], got %v", arts.Paths)
	}
	if arts.MaxSizeMB != 100 {
		t.Errorf("expected maxSizeMB=100, got %d", arts.MaxSizeMB)
	}
	if arts.ReplicationRegion != "us-west-2" {
		t.Errorf("expected replicationRegion=us-west-2, got %q", arts.ReplicationRegion)
	}
}

// TestArtifactsSpecOptional verifies that a profile without spec.artifacts is valid.
func TestArtifactsSpecOptional(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/valid-minimal.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	p, err := profile.Parse(data)
	if err != nil {
		t.Fatalf("expected profile without artifacts to parse without error, got: %v", err)
	}

	// Artifacts is optional — nil means not specified
	if p.Spec.Artifacts != nil {
		t.Logf("valid-minimal.yaml has artifacts set: %+v (acceptable)", p.Spec.Artifacts)
	}
}

func TestParseValidProfile(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/valid-minimal.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	p, err := profile.Parse(data)
	if err != nil {
		t.Fatalf("expected valid profile to parse without error, got: %v", err)
	}

	if p.APIVersion != "klankermaker.ai/v1alpha1" {
		t.Errorf("expected apiVersion 'klankermaker.ai/v1alpha1', got '%s'", p.APIVersion)
	}
	if p.Kind != "SandboxProfile" {
		t.Errorf("expected kind 'SandboxProfile', got '%s'", p.Kind)
	}
	if p.Metadata.Name == "" {
		t.Error("expected metadata.name to be non-empty")
	}
}

func TestParsePreservesAllSections(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/valid-minimal.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	p, err := profile.Parse(data)
	if err != nil {
		t.Fatalf("expected valid profile to parse without error, got: %v", err)
	}

	// All 10 spec sections must be present (non-zero)
	if p.Spec.Lifecycle.TTL == "" {
		t.Error("expected spec.lifecycle.ttl to be populated")
	}
	if p.Spec.Runtime.Substrate == "" {
		t.Error("expected spec.runtime.substrate to be populated")
	}
	if p.Spec.Execution.Shell == "" {
		t.Error("expected spec.execution.shell to be populated")
	}
	if p.Spec.SourceAccess.Mode == "" {
		t.Error("expected spec.sourceAccess.mode to be populated")
	}
	if p.Spec.Network.Egress.AllowedDNSSuffixes == nil {
		t.Error("expected spec.network.egress.allowedDNSSuffixes to be populated")
	}
	if p.Spec.Identity.RoleSessionDuration == "" {
		t.Error("expected spec.identity.roleSessionDuration to be populated")
	}
	if p.Spec.Sidecars.DNSProxy.Enabled == false {
		t.Error("expected spec.sidecars.dnsProxy.enabled to be true")
	}
	if p.Spec.Observability.CommandLog.Destination == "" {
		t.Error("expected spec.observability.commandLog.destination to be populated")
	}
	if p.Spec.Agent.MaxConcurrentTasks == 0 {
		t.Error("expected spec.agent.maxConcurrentTasks to be populated")
	}
}

func TestRuntimeSubstrate(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/valid-minimal.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	p, err := profile.Parse(data)
	if err != nil {
		t.Fatalf("expected valid profile to parse without error, got: %v", err)
	}

	substrate := p.Spec.Runtime.Substrate
	if substrate != "ec2" && substrate != "ecs" {
		t.Errorf("expected substrate to be 'ec2' or 'ecs', got '%s'", substrate)
	}
}

func TestMetadataLabels(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/valid-minimal.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	p, err := profile.Parse(data)
	if err != nil {
		t.Fatalf("expected valid profile to parse without error, got: %v", err)
	}

	if len(p.Metadata.Labels) == 0 {
		t.Error("expected metadata.labels to be populated")
	}
	if p.Metadata.Labels["tier"] == "" {
		t.Errorf("expected metadata.labels.tier to be set, got: %v", p.Metadata.Labels)
	}
}

// TestBudgetSpecParsesFromYAML verifies that BudgetSpec round-trips from YAML with
// compute and AI budget limits.
func TestBudgetSpecParsesFromYAML(t *testing.T) {
	yamlData := `
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: budget-test
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 1h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: [".amazonaws.com"]
      allowedHosts: []

  identity:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:
      enabled: true
      image: "km-dns-proxy:latest"
    httpProxy:
      enabled: true
      image: "km-http-proxy:latest"
    auditLog:
      enabled: true
      image: "km-audit-log:latest"
    tracing:
      enabled: false
      image: "km-otel:latest"
  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch

  agent:
    maxConcurrentTasks: 2
    taskTimeout: 30m
  budget:
    compute:
      maxSpendUSD: 2.00
    ai:
      maxSpendUSD: 5.00
    warningThreshold: 0.8
`

	p, err := profile.Parse([]byte(yamlData))
	if err != nil {
		t.Fatalf("expected profile with budget to parse without error, got: %v", err)
	}

	if p.Spec.Budget == nil {
		t.Fatal("expected spec.budget to be populated, got nil")
	}

	b := p.Spec.Budget
	if b.Compute == nil {
		t.Fatal("expected spec.budget.compute to be populated, got nil")
	}
	if b.Compute.MaxSpendUSD != 2.00 {
		t.Errorf("expected compute.maxSpendUSD=2.00, got %f", b.Compute.MaxSpendUSD)
	}
	if b.AI == nil {
		t.Fatal("expected spec.budget.ai to be populated, got nil")
	}
	if b.AI.MaxSpendUSD != 5.00 {
		t.Errorf("expected ai.maxSpendUSD=5.00, got %f", b.AI.MaxSpendUSD)
	}
	if b.WarningThreshold != 0.8 {
		t.Errorf("expected warningThreshold=0.8, got %f", b.WarningThreshold)
	}
}

// TestBudgetSpecWarningThresholdDefault verifies that warningThreshold defaults
// to zero when omitted (caller can treat zero as "use default 0.8").
func TestBudgetSpecWarningThresholdDefault(t *testing.T) {
	yamlData := `
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: budget-threshold-default
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 1h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: [".amazonaws.com"]
      allowedHosts: []

  identity:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:
      enabled: true
      image: "km-dns-proxy:latest"
    httpProxy:
      enabled: true
      image: "km-http-proxy:latest"
    auditLog:
      enabled: true
      image: "km-audit-log:latest"
    tracing:
      enabled: false
      image: "km-otel:latest"
  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch

  agent:
    maxConcurrentTasks: 2
    taskTimeout: 30m
  budget:
    ai:
      maxSpendUSD: 10.00
`

	p, err := profile.Parse([]byte(yamlData))
	if err != nil {
		t.Fatalf("expected profile with partial budget to parse without error, got: %v", err)
	}

	if p.Spec.Budget == nil {
		t.Fatal("expected spec.budget to be populated, got nil")
	}
	// warningThreshold omitted — zero value from Go zero-initialization
	if p.Spec.Budget.WarningThreshold != 0 {
		t.Errorf("expected default warningThreshold=0 (unset), got %f", p.Spec.Budget.WarningThreshold)
	}
	if p.Spec.Budget.Compute != nil {
		t.Errorf("expected compute to be nil when omitted, got %+v", p.Spec.Budget.Compute)
	}
}

// TestRsyncPathsParsing verifies that rsyncPaths and rsyncFileList parse correctly
// from YAML into ExecutionSpec fields.
func TestRsyncPathsParsing(t *testing.T) {
	baseYAML := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: rsync-test
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 1h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: [".amazonaws.com"]
      allowedHosts: []

  identity:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:
      enabled: true
      image: "km-dns-proxy:latest"
    httpProxy:
      enabled: true
      image: "km-http-proxy:latest"
    auditLog:
      enabled: true
      image: "km-audit-log:latest"
    tracing:
      enabled: false
      image: "km-otel:latest"
  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch

  agent:
    maxConcurrentTasks: 2
    taskTimeout: 30m`

	t.Run("rsyncPaths parses into slice", func(t *testing.T) {
		yamlData := baseYAML + `
  execution:
    shell: /bin/bash
    workingDir: /workspace
    rsyncPaths:
      - ".claude"
      - "projects/*/config"
`
		// Re-parse using a full YAML that replaces the execution block
		fullYAML := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: rsync-paths-test
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 1h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
    rsyncPaths:
      - ".claude"
      - "projects/*/config"
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: [".amazonaws.com"]
      allowedHosts: []

  identity:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:
      enabled: true
      image: "km-dns-proxy:latest"
    httpProxy:
      enabled: true
      image: "km-http-proxy:latest"
    auditLog:
      enabled: true
      image: "km-audit-log:latest"
    tracing:
      enabled: false
      image: "km-otel:latest"
  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch

  agent:
    maxConcurrentTasks: 2
    taskTimeout: 30m
`
		_ = yamlData
		p, err := profile.Parse([]byte(fullYAML))
		if err != nil {
			t.Fatalf("expected profile with rsyncPaths to parse without error, got: %v", err)
		}
		paths := p.Spec.Execution.RsyncPaths
		if len(paths) != 2 {
			t.Fatalf("expected RsyncPaths to have 2 entries, got %d: %v", len(paths), paths)
		}
		if paths[0] != ".claude" {
			t.Errorf("expected RsyncPaths[0]='.claude', got %q", paths[0])
		}
		if paths[1] != "projects/*/config" {
			t.Errorf("expected RsyncPaths[1]='projects/*/config', got %q", paths[1])
		}
	})

	t.Run("rsyncFileList parses into string", func(t *testing.T) {
		fullYAML := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: rsync-filelist-test
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 1h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
    rsyncFileList: "cc-files.yaml"
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: [".amazonaws.com"]
      allowedHosts: []

  identity:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:
      enabled: true
      image: "km-dns-proxy:latest"
    httpProxy:
      enabled: true
      image: "km-http-proxy:latest"
    auditLog:
      enabled: true
      image: "km-audit-log:latest"
    tracing:
      enabled: false
      image: "km-otel:latest"
  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch

  agent:
    maxConcurrentTasks: 2
    taskTimeout: 30m
`
		p, err := profile.Parse([]byte(fullYAML))
		if err != nil {
			t.Fatalf("expected profile with rsyncFileList to parse without error, got: %v", err)
		}
		if p.Spec.Execution.RsyncFileList != "cc-files.yaml" {
			t.Errorf("expected RsyncFileList='cc-files.yaml', got %q", p.Spec.Execution.RsyncFileList)
		}
	})

	t.Run("no rsyncPaths or rsyncFileList is backward compatible", func(t *testing.T) {
		fullYAML := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: rsync-compat-test
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 1h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: [".amazonaws.com"]
      allowedHosts: []

  identity:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:
      enabled: true
      image: "km-dns-proxy:latest"
    httpProxy:
      enabled: true
      image: "km-http-proxy:latest"
    auditLog:
      enabled: true
      image: "km-audit-log:latest"
    tracing:
      enabled: false
      image: "km-otel:latest"
  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch

  agent:
    maxConcurrentTasks: 2
    taskTimeout: 30m
`
		p, err := profile.Parse([]byte(fullYAML))
		if err != nil {
			t.Fatalf("expected profile without rsync fields to parse cleanly, got: %v", err)
		}
		if p.Spec.Execution.RsyncPaths != nil {
			t.Errorf("expected RsyncPaths to be nil when omitted, got %v", p.Spec.Execution.RsyncPaths)
		}
		if p.Spec.Execution.RsyncFileList != "" {
			t.Errorf("expected RsyncFileList to be empty string when omitted, got %q", p.Spec.Execution.RsyncFileList)
		}
	})
}

// TestTlsCaptureSpecParsesFromYAML verifies that TlsCaptureSpec round-trips from YAML.
func TestTlsCaptureSpecParsesFromYAML(t *testing.T) {
	yamlData := `
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: tls-capture-test
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 1h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: [".amazonaws.com"]
      allowedHosts: []

  identity:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:
      enabled: true
      image: "km-dns-proxy:latest"
    httpProxy:
      enabled: true
      image: "km-http-proxy:latest"
    auditLog:
      enabled: true
      image: "km-audit-log:latest"
    tracing:
      enabled: false
      image: "km-otel:latest"
  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch
    tlsCapture:
      enabled: true
      libraries:
        - openssl
      capturePayloads: false

  agent:
    maxConcurrentTasks: 2
    taskTimeout: 30m
`

	p, err := profile.Parse([]byte(yamlData))
	if err != nil {
		t.Fatalf("expected profile with tlsCapture to parse without error, got: %v", err)
	}

	if p.Spec.Observability.TlsCapture == nil {
		t.Fatal("expected spec.observability.tlsCapture to be populated, got nil")
	}

	tc := p.Spec.Observability.TlsCapture
	if !tc.Enabled {
		t.Error("expected tlsCapture.enabled=true")
	}
	if len(tc.Libraries) != 1 || tc.Libraries[0] != "openssl" {
		t.Errorf("expected libraries=[openssl], got %v", tc.Libraries)
	}
	if tc.CapturePayloads {
		t.Error("expected capturePayloads=false")
	}
}

// TestTlsCaptureSpecIsEnabled verifies the IsEnabled() method for various states.
func TestTlsCaptureSpecIsEnabled(t *testing.T) {
	t.Run("nil spec returns false", func(t *testing.T) {
		var tc *profile.TlsCaptureSpec
		if tc.IsEnabled() {
			t.Error("expected IsEnabled()=false for nil TlsCaptureSpec")
		}
	})

	t.Run("disabled spec returns false", func(t *testing.T) {
		tc := &profile.TlsCaptureSpec{Enabled: false}
		if tc.IsEnabled() {
			t.Error("expected IsEnabled()=false for disabled TlsCaptureSpec")
		}
	})

	t.Run("enabled spec returns true", func(t *testing.T) {
		tc := &profile.TlsCaptureSpec{Enabled: true}
		if !tc.IsEnabled() {
			t.Error("expected IsEnabled()=true for enabled TlsCaptureSpec")
		}
	})
}

// TestTlsCaptureSpecEffectiveLibraries verifies EffectiveLibraries() behavior.
func TestTlsCaptureSpecEffectiveLibraries(t *testing.T) {
	t.Run("nil spec returns nil", func(t *testing.T) {
		var tc *profile.TlsCaptureSpec
		libs := tc.EffectiveLibraries()
		if libs != nil {
			t.Errorf("expected nil for nil spec, got %v", libs)
		}
	})

	t.Run("disabled spec returns nil", func(t *testing.T) {
		tc := &profile.TlsCaptureSpec{Enabled: false, Libraries: []string{"openssl"}}
		libs := tc.EffectiveLibraries()
		if libs != nil {
			t.Errorf("expected nil for disabled spec, got %v", libs)
		}
	})

	t.Run("empty libraries defaults to openssl", func(t *testing.T) {
		tc := &profile.TlsCaptureSpec{Enabled: true}
		libs := tc.EffectiveLibraries()
		if len(libs) != 1 || libs[0] != "openssl" {
			t.Errorf("expected [openssl] for empty libraries, got %v", libs)
		}
	})

	t.Run("specific libraries returned as-is", func(t *testing.T) {
		tc := &profile.TlsCaptureSpec{Enabled: true, Libraries: []string{"openssl", "gnutls"}}
		libs := tc.EffectiveLibraries()
		if len(libs) != 2 || libs[0] != "openssl" || libs[1] != "gnutls" {
			t.Errorf("expected [openssl gnutls], got %v", libs)
		}
	})

	t.Run("all expands to all supported libraries", func(t *testing.T) {
		tc := &profile.TlsCaptureSpec{Enabled: true, Libraries: []string{"all"}}
		libs := tc.EffectiveLibraries()
		expected := []string{"openssl", "gnutls", "nss", "go", "rustls"}
		if len(libs) != len(expected) {
			t.Fatalf("expected %d libraries for 'all', got %d: %v", len(expected), len(libs), libs)
		}
		for i, e := range expected {
			if libs[i] != e {
				t.Errorf("expected libs[%d]=%q, got %q", i, e, libs[i])
			}
		}
	})
}

// TestTlsCaptureBackwardsCompatible verifies profiles without tlsCapture still parse.
func TestTlsCaptureBackwardsCompatible(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/valid-minimal.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	p, err := profile.Parse(data)
	if err != nil {
		t.Fatalf("expected profile without tlsCapture to parse without error, got: %v", err)
	}

	if p.Spec.Observability.TlsCapture != nil {
		t.Logf("valid-minimal.yaml has tlsCapture set: %+v (acceptable if test fixture was updated)", p.Spec.Observability.TlsCapture)
	}
}

// TestBudgetSpecOptional verifies that a profile without spec.budget is still valid.
func TestBudgetSpecOptional(t *testing.T) {
	data, err := os.ReadFile("../../testdata/profiles/valid-minimal.yaml")
	if err != nil {
		t.Fatalf("failed to read test fixture: %v", err)
	}

	p, err := profile.Parse(data)
	if err != nil {
		t.Fatalf("expected profile without budget to parse without error, got: %v", err)
	}

	// Budget is optional — nil means not specified
	if p.Spec.Budget != nil {
		t.Logf("valid-minimal.yaml has budget set: %+v (acceptable)", p.Spec.Budget)
	}
}

// TestCLISpec_ClaudeArgsParsesFromYAML verifies that spec.cli.claudeArgs parses
// into a string slice and can be used to default extra args on km agent --claude.
func TestCLISpec_ClaudeArgsParsesFromYAML(t *testing.T) {
	yamlData := []byte(`
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: cli-claude-args-test
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 1h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: [".amazonaws.com"]
      allowedHosts: []
  identity:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:
      enabled: true
      image: "km-dns-proxy:latest"
    httpProxy:
      enabled: true
      image: "km-http-proxy:latest"
    auditLog:
      enabled: true
      image: "km-audit-log:latest"
    tracing:
      enabled: true
      image: "km-tracing:latest"
  cli:
    noBedrock: true
    claudeArgs:
      - "--dangerously-skip-permissions"
      - "--model"
      - "claude-opus-4-7"
`)

	p, err := profile.Parse(yamlData)
	if err != nil {
		t.Fatalf("expected profile to parse, got: %v", err)
	}
	if p.Spec.CLI == nil {
		t.Fatal("expected Spec.CLI to be set, got nil")
	}
	if !p.Spec.CLI.NoBedrock {
		t.Error("expected CLI.NoBedrock=true")
	}
	want := []string{"--dangerously-skip-permissions", "--model", "claude-opus-4-7"}
	if got := p.Spec.CLI.ClaudeArgs; len(got) != len(want) {
		t.Fatalf("expected %d claudeArgs, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if p.Spec.CLI.ClaudeArgs[i] != w {
			t.Errorf("claudeArgs[%d] = %q, want %q", i, p.Spec.CLI.ClaudeArgs[i], w)
		}
	}
}

// TestCLISpec_ClaudeArgsOptional verifies that claudeArgs is optional and parses
// as nil/empty when omitted.
func TestCLISpec_ClaudeArgsOptional(t *testing.T) {
	yamlData := []byte(`
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: cli-optional-test
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 1h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: [".amazonaws.com"]
      allowedHosts: []
  identity:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:
      enabled: true
      image: "km-dns-proxy:latest"
    httpProxy:
      enabled: true
      image: "km-http-proxy:latest"
    auditLog:
      enabled: true
      image: "km-audit-log:latest"
    tracing:
      enabled: true
      image: "km-tracing:latest"
  cli:
    noBedrock: false
`)

	p, err := profile.Parse(yamlData)
	if err != nil {
		t.Fatalf("expected profile to parse, got: %v", err)
	}
	if p.Spec.CLI == nil {
		t.Fatal("expected Spec.CLI to be set, got nil")
	}
	if len(p.Spec.CLI.ClaudeArgs) != 0 {
		t.Errorf("expected empty claudeArgs, got %v", p.Spec.CLI.ClaudeArgs)
	}
}
