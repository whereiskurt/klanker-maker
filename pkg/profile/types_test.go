package profile_test

import (
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// TestArtifactsSpecParsesFromYAML verifies that ArtifactsSpec round-trips from YAML.
func TestArtifactsSpecParsesFromYAML(t *testing.T) {
	yamlData := `
apiVersion: klankermaker.ai/v1alpha2
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

  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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

	if p.APIVersion != "klankermaker.ai/v1alpha2" {
		t.Errorf("expected apiVersion 'klankermaker.ai/v1alpha2', got '%s'", p.APIVersion)
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
	if p.Spec.IAM.RoleSessionDuration == "" {
		t.Error("expected spec.iam.roleSessionDuration to be populated")
	}
	if p.Spec.Sidecars.DNSProxy.Enabled == false {
		t.Error("expected spec.sidecars.dnsProxy.enabled to be true")
	}
	if p.Spec.Observability.CommandLog.Destination == "" {
		t.Error("expected spec.observability.commandLog.destination to be populated")
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
apiVersion: klankermaker.ai/v1alpha2
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

  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
apiVersion: klankermaker.ai/v1alpha2
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

  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
	baseYAML := `apiVersion: klankermaker.ai/v1alpha2
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

  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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

`
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
		fullYAML := `apiVersion: klankermaker.ai/v1alpha2
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

  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
		fullYAML := `apiVersion: klankermaker.ai/v1alpha2
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

  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
		fullYAML := `apiVersion: klankermaker.ai/v1alpha2
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

  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
apiVersion: klankermaker.ai/v1alpha2
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

  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
apiVersion: klankermaker.ai/v1alpha2
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
  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
  agent:
    claude:
      args:
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
	if p.Spec.Agent == nil || p.Spec.Agent.Claude == nil {
		t.Fatal("expected Spec.Agent.Claude to be set, got nil")
	}
	want := []string{"--dangerously-skip-permissions", "--model", "claude-opus-4-7"}
	if got := p.Spec.Agent.Claude.Args; len(got) != len(want) {
		t.Fatalf("expected %d claude args, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if p.Spec.Agent.Claude.Args[i] != w {
			t.Errorf("agent.claude.args[%d] = %q, want %q", i, p.Spec.Agent.Claude.Args[i], w)
		}
	}
}

// TestCLISpec_ClaudeArgsOptional verifies that claudeArgs is optional and parses
// as nil/empty when omitted.
func TestCLISpec_ClaudeArgsOptional(t *testing.T) {
	yamlData := []byte(`
apiVersion: klankermaker.ai/v1alpha2
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
  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
	if p.Spec.Agent != nil && p.Spec.Agent.Claude != nil && len(p.Spec.Agent.Claude.Args) != 0 {
		t.Errorf("expected empty claude args, got %v", p.Spec.Agent.Claude.Args)
	}
}

// TestCLISpec_CodexArgsParsesFromYAML verifies that spec.cli.codexArgs parses
// into a string slice and can be used to default extra args on km agent run --codex.
func TestCLISpec_CodexArgsParsesFromYAML(t *testing.T) {
	yamlData := []byte(`
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: cli-codex-args-test
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
  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
  agent:
    codex:
      args:
        - "--model"
        - "o4-mini"
        - "--dangerously-bypass-approvals-and-sandbox"
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
	if p.Spec.Agent == nil || p.Spec.Agent.Codex == nil {
		t.Fatal("expected Spec.Agent.Codex to be set, got nil")
	}
	want := []string{"--model", "o4-mini", "--dangerously-bypass-approvals-and-sandbox"}
	if got := p.Spec.Agent.Codex.Args; len(got) != len(want) {
		t.Fatalf("expected %d codex args, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if p.Spec.Agent.Codex.Args[i] != w {
			t.Errorf("agent.codex.args[%d] = %q, want %q", i, p.Spec.Agent.Codex.Args[i], w)
		}
	}
}

// TestCLISpec_CodexArgsOptional verifies that codexArgs is optional and parses
// as nil/empty when omitted.
func TestCLISpec_CodexArgsOptional(t *testing.T) {
	yamlData := []byte(`
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: cli-codex-optional-test
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
  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
	if p.Spec.Agent != nil && p.Spec.Agent.Codex != nil && len(p.Spec.Agent.Codex.Args) != 0 {
		t.Errorf("expected empty codex args, got %v", p.Spec.Agent.Codex.Args)
	}
}

// TestParse_CLISpec_NotifyFields verifies that a YAML profile setting all four
// notify fields round-trips correctly through profile.Parse().
func TestParse_CLISpec_NotifyFields(t *testing.T) {
	yamlData := []byte(`
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: notify-fields-test
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
  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch
  notification:
    events:
      onPermission: true
      onIdle: true
      cooldownSeconds: 120
    email:
      address: "ops-team@example.com"
`)

	p, err := profile.Parse(yamlData)
	if err != nil {
		t.Fatalf("expected profile with notify fields to parse without error, got: %v", err)
	}
	if p.Spec.Notification == nil || p.Spec.Notification.Events == nil {
		t.Fatal("expected Spec.Notification.Events to be set, got nil")
	}
	ev := p.Spec.Notification.Events
	if ev.OnPermission == nil || !*ev.OnPermission {
		t.Error("expected events.OnPermission=true, got false/nil")
	}
	if ev.OnIdle == nil || !*ev.OnIdle {
		t.Error("expected events.OnIdle=true, got false/nil")
	}
	if ev.CooldownSeconds == nil || *ev.CooldownSeconds != 120 {
		t.Errorf("expected events.CooldownSeconds=120, got %v", ev.CooldownSeconds)
	}
	if p.Spec.Notification.Email == nil || p.Spec.Notification.Email.Address != "ops-team@example.com" {
		t.Errorf("expected email.Address=%q, got %+v", "ops-team@example.com", p.Spec.Notification.Email)
	}
}

// TestParse_CLISpec_NotifyFields_DefaultsZero verifies that a YAML profile
// omitting all four notify fields parses cleanly with zero values (backwards compat).
func TestParse_CLISpec_NotifyFields_DefaultsZero(t *testing.T) {
	yamlData := []byte(`
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: notify-zero-defaults-test
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
  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch
  cli:
    noBedrock: true
`)

	p, err := profile.Parse(yamlData)
	if err != nil {
		t.Fatalf("expected profile without notify fields to parse without error, got: %v", err)
	}
	if p.Spec.CLI == nil {
		t.Fatal("expected Spec.CLI to be set, got nil")
	}
	// The notification block must be nil when omitted (backwards compat).
	if p.Spec.Notification != nil {
		t.Errorf("expected Spec.Notification=nil when omitted, got %+v", p.Spec.Notification)
	}
}

// TestParse_CLISpec_NotifyFields_ExplicitFalse verifies that explicit false values
// for notifyOnPermission and notifyOnIdle round-trip correctly.
func TestParse_CLISpec_NotifyFields_ExplicitFalse(t *testing.T) {
	yamlData := []byte(`
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: notify-explicit-false-test
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
  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch
  notification:
    events:
      onPermission: false
      onIdle: false
`)

	p, err := profile.Parse(yamlData)
	if err != nil {
		t.Fatalf("expected profile to parse, got: %v", err)
	}
	if p.Spec.Notification == nil || p.Spec.Notification.Events == nil {
		t.Fatal("expected Spec.Notification.Events to be set, got nil")
	}
	ev := p.Spec.Notification.Events
	if ev.OnPermission == nil || *ev.OnPermission != false {
		t.Error("expected events.OnPermission=&false (explicit), got non-false/nil")
	}
	if ev.OnIdle == nil || *ev.OnIdle != false {
		t.Error("expected events.OnIdle=&false (explicit), got non-false/nil")
	}
}

// minimalCLIProfileYAML returns a valid profile YAML with the cli section containing the given cliFields.
func minimalCLIProfileYAML(cliFields string) []byte {
	return []byte(`apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: slack-fields-test
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
  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch
  cli:
` + cliFields)
}

// minimalAgentDefaultProfileYAML returns a valid profile YAML with the structured
// spec.agent block containing the given agentFields (indented under spec.agent).
// Phase 92 (Wave 4): companion to minimalCLIProfileYAML for the re-homed
// agent.default field (formerly cli.agent).
func minimalAgentDefaultProfileYAML(agentFields string) []byte {
	return []byte(`apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: agent-default-test
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
  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch
  agent:
` + agentFields)
}

// minimalNotificationProfileYAML returns a valid profile YAML with the notification
// section containing the given notificationFields (indented under spec.notification).
// Phase 92 (Wave 2): companion to minimalCLIProfileYAML for the structured block.
func minimalNotificationProfileYAML(notificationFields string) []byte {
	return []byte(`apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: notification-fields-test
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
  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch
  notification:
` + notificationFields)
}

// TestParse_CLISpec_SlackFields_AllSet verifies that a YAML profile setting all five
// Slack fields round-trips correctly through profile.Parse().
// Phase 92 (Wave 2): migrated to the notification.slack block.
func TestParse_CLISpec_SlackFields_AllSet(t *testing.T) {
	yamlData := minimalNotificationProfileYAML(`    email:
      enabled: false
    slack:
      enabled: true
      perSandbox: true
      channelOverride: "C0123ABC"
      archiveOnDestroy: false
`)

	p, err := profile.Parse(yamlData)
	if err != nil {
		t.Fatalf("expected profile with all five Slack fields to parse, got: %v", err)
	}
	if p.Spec.Notification == nil || p.Spec.Notification.Slack == nil {
		t.Fatal("expected Spec.Notification.Slack to be set, got nil")
	}
	slack := p.Spec.Notification.Slack
	if p.Spec.Notification.Email == nil || p.Spec.Notification.Email.Enabled == nil {
		t.Fatal("expected email.enabled to be non-nil, got nil")
	}
	if *p.Spec.Notification.Email.Enabled != false {
		t.Errorf("expected *email.enabled=false, got %v", *p.Spec.Notification.Email.Enabled)
	}
	if slack.Enabled == nil {
		t.Fatal("expected slack.enabled to be non-nil, got nil")
	}
	if *slack.Enabled != true {
		t.Errorf("expected *slack.enabled=true, got %v", *slack.Enabled)
	}
	if slack.PerSandbox == nil || !*slack.PerSandbox {
		t.Error("expected slack.perSandbox=true, got false/nil")
	}
	if slack.ChannelOverride != "C0123ABC" {
		t.Errorf("expected slack.channelOverride=%q, got %q", "C0123ABC", slack.ChannelOverride)
	}
	if slack.ArchiveOnDestroy == nil {
		t.Fatal("expected slack.archiveOnDestroy to be non-nil, got nil")
	}
	if *slack.ArchiveOnDestroy != false {
		t.Errorf("expected *slack.archiveOnDestroy=false, got %v", *slack.ArchiveOnDestroy)
	}
}

// TestParse_CLISpec_SlackFields_OmittedNilPointers verifies that a YAML profile with
// no Slack block parses cleanly with nil pointers (Phase 62 backward compat).
func TestParse_CLISpec_SlackFields_OmittedNilPointers(t *testing.T) {
	yamlData := minimalNotificationProfileYAML(`    events:
      onPermission: true
`)

	p, err := profile.Parse(yamlData)
	if err != nil {
		t.Fatalf("expected Phase 62 profile to parse cleanly, got: %v", err)
	}
	if p.Spec.Notification == nil {
		t.Fatal("expected Spec.Notification to be set, got nil")
	}
	if p.Spec.Notification.Slack != nil {
		t.Errorf("expected notification.slack=nil (unset), got %+v", p.Spec.Notification.Slack)
	}
	if p.Spec.Notification.Email != nil {
		t.Errorf("expected notification.email=nil (unset), got %+v", p.Spec.Notification.Email)
	}
}

// ============================================================
// Phase 73 VSCode field tests (Wave 0 stubs — Wave 1 Plan 73-02 implements)
// ============================================================

// Note: boolPtr is defined in validate_test.go (same package) — reuse it here.

// TestVSCodeEnabled_DefaultTrue asserts that IsVSCodeEnabled returns true for
// nil RuntimeVSCodeSpec, empty RuntimeVSCodeSpec, and {Enabled: &true}.
// Phase 92 (Wave 2): the gate moved from cli.vscodeEnabled to runtime.vscode.enabled.
func TestVSCodeEnabled_DefaultTrue(t *testing.T) {
	if !profile.IsVSCodeEnabled(nil) {
		t.Fatal("nil vscode should return true")
	}
	if !profile.IsVSCodeEnabled(&profile.RuntimeVSCodeSpec{}) {
		t.Fatal("empty RuntimeVSCodeSpec should return true")
	}
	tru := true
	if !profile.IsVSCodeEnabled(&profile.RuntimeVSCodeSpec{Enabled: &tru}) {
		t.Fatal("&true should return true")
	}
}

// TestVSCodeEnabled_False asserts that IsVSCodeEnabled returns false when
// RuntimeVSCodeSpec.Enabled is explicitly set to &false.
func TestVSCodeEnabled_False(t *testing.T) {
	fls := false
	if profile.IsVSCodeEnabled(&profile.RuntimeVSCodeSpec{Enabled: &fls}) {
		t.Fatal("&false should return false")
	}
}

// ============================================================
// Phase 87 Wave 0: AdditionalSnapshotSpec type tests (SNAP-01)
// ============================================================

// TestAdditionalSnapshotSpec_YAMLParse verifies YAML round-trip for
// AdditionalSnapshotSpec including *bool nil/true/false semantics.
func TestAdditionalSnapshotSpec_YAMLParse(t *testing.T) {
	// Helper to build a minimal profile YAML with the given runtime section
	buildYAML := func(runtimeExtra string) []byte {
		return []byte(`apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: snapshot-parse-test
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 1h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
` + runtimeExtra + `
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: [".amazonaws.com"]
      allowedHosts: []
  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
`)
	}

	t.Run("omitted additionalSnapshots is nil or empty", func(t *testing.T) {
		p, err := profile.Parse(buildYAML(""))
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		if len(p.Spec.Runtime.AdditionalSnapshots) != 0 {
			t.Errorf("expected AdditionalSnapshots to be nil/empty, got %v", p.Spec.Runtime.AdditionalSnapshots)
		}
	})

	t.Run("single entry with required fields", func(t *testing.T) {
		yaml := buildYAML(`    additionalSnapshots:
      - snapshotId: snap-0123456789abcdef0
        mountPoint: /data
`)
		p, err := profile.Parse(yaml)
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		if len(p.Spec.Runtime.AdditionalSnapshots) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(p.Spec.Runtime.AdditionalSnapshots))
		}
		s := p.Spec.Runtime.AdditionalSnapshots[0]
		if s.SnapshotID != "snap-0123456789abcdef0" {
			t.Errorf("expected SnapshotID=snap-0123456789abcdef0, got %q", s.SnapshotID)
		}
		if s.MountPoint != "/data" {
			t.Errorf("expected MountPoint=/data, got %q", s.MountPoint)
		}
	})

	t.Run("three entries preserve order", func(t *testing.T) {
		yaml := buildYAML(`    additionalSnapshots:
      - snapshotId: snap-aaaaaaaaaaaaaaaa1
        mountPoint: /data1
      - snapshotId: snap-bbbbbbbbbbbbbbb2
        mountPoint: /data2
      - snapshotId: snap-ccccccccccccccc3
        mountPoint: /data3
`)
		p, err := profile.Parse(yaml)
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		snaps := p.Spec.Runtime.AdditionalSnapshots
		if len(snaps) != 3 {
			t.Fatalf("expected 3 entries, got %d", len(snaps))
		}
		if snaps[0].SnapshotID != "snap-aaaaaaaaaaaaaaaa1" {
			t.Errorf("snaps[0] order wrong: %q", snaps[0].SnapshotID)
		}
		if snaps[1].SnapshotID != "snap-bbbbbbbbbbbbbbb2" {
			t.Errorf("snaps[1] order wrong: %q", snaps[1].SnapshotID)
		}
		if snaps[2].SnapshotID != "snap-ccccccccccccccc3" {
			t.Errorf("snaps[2] order wrong: %q", snaps[2].SnapshotID)
		}
	})

	t.Run("encrypted: true sets *bool non-nil true", func(t *testing.T) {
		yaml := buildYAML(`    additionalSnapshots:
      - snapshotId: snap-0123456789abcdef0
        mountPoint: /data
        encrypted: true
`)
		p, err := profile.Parse(yaml)
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		s := p.Spec.Runtime.AdditionalSnapshots[0]
		if s.Encrypted == nil {
			t.Fatal("expected Encrypted to be non-nil for explicit true, got nil")
		}
		if !*s.Encrypted {
			t.Errorf("expected *Encrypted=true, got false")
		}
	})

	t.Run("encrypted: false sets *bool non-nil false", func(t *testing.T) {
		yaml := buildYAML(`    additionalSnapshots:
      - snapshotId: snap-0123456789abcdef0
        mountPoint: /data
        encrypted: false
`)
		p, err := profile.Parse(yaml)
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		s := p.Spec.Runtime.AdditionalSnapshots[0]
		if s.Encrypted == nil {
			t.Fatal("expected Encrypted to be non-nil for explicit false, got nil")
		}
		if *s.Encrypted {
			t.Errorf("expected *Encrypted=false, got true")
		}
	})

	t.Run("encrypted omitted sets *bool nil (proves pointer semantics)", func(t *testing.T) {
		yaml := buildYAML(`    additionalSnapshots:
      - snapshotId: snap-0123456789abcdef0
        mountPoint: /data
`)
		p, err := profile.Parse(yaml)
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		s := p.Spec.Runtime.AdditionalSnapshots[0]
		if s.Encrypted != nil {
			t.Errorf("expected Encrypted=nil when omitted (pointer semantics), got %v", *s.Encrypted)
		}
	})

	t.Run("explicit size parses into Size field", func(t *testing.T) {
		yaml := buildYAML(`    additionalSnapshots:
      - snapshotId: snap-0123456789abcdef0
        mountPoint: /data
        size: 50
`)
		p, err := profile.Parse(yaml)
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		s := p.Spec.Runtime.AdditionalSnapshots[0]
		if s.Size != 50 {
			t.Errorf("expected Size=50, got %d", s.Size)
		}
	})

	t.Run("omitted size yields 0", func(t *testing.T) {
		yaml := buildYAML(`    additionalSnapshots:
      - snapshotId: snap-0123456789abcdef0
        mountPoint: /data
`)
		p, err := profile.Parse(yaml)
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		s := p.Spec.Runtime.AdditionalSnapshots[0]
		if s.Size != 0 {
			t.Errorf("expected Size=0 when omitted, got %d", s.Size)
		}
	})

	t.Run("device parses when provided", func(t *testing.T) {
		yaml := buildYAML(`    additionalSnapshots:
      - snapshotId: snap-0123456789abcdef0
        mountPoint: /data
        device: /dev/sdg
`)
		p, err := profile.Parse(yaml)
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		s := p.Spec.Runtime.AdditionalSnapshots[0]
		if s.Device != "/dev/sdg" {
			t.Errorf("expected Device=/dev/sdg, got %q", s.Device)
		}
	})
}

// TestAdditionalSnapshotSpec_JSONSchemaValidation verifies JSON schema enforcement
// for the additionalSnapshots array: bad snapshotId patterns, bad device, size 0,
// and additional properties.
func TestAdditionalSnapshotSpec_JSONSchemaValidation(t *testing.T) {
	// buildSnapshotProfileRaw produces a profile YAML for schema validation.
	buildSnapshotProfileRaw := func(snapshotEntry string) []byte {
		return []byte(`apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: snapshot-schema-test
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 1h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
    additionalSnapshots:
` + snapshotEntry + `
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: [".amazonaws.com"]
      allowedHosts: []
  iam:
    roleSessionDuration: 1h
    allowedRegions: ["us-east-1"]
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
`)
	}

	t.Run("accepts snap-01234567 (8-char hex)", func(t *testing.T) {
		raw := buildSnapshotProfileRaw(`      - snapshotId: snap-01234567
        mountPoint: /data
`)
		errs := profile.Validate(raw)
		if len(errs) != 0 {
			t.Errorf("expected no errors for valid 8-char snapshotId, got: %v", errs)
		}
	})

	t.Run("accepts snap-0123456789abcdef0 (17-char hex)", func(t *testing.T) {
		raw := buildSnapshotProfileRaw(`      - snapshotId: snap-0123456789abcdef0
        mountPoint: /data
`)
		errs := profile.Validate(raw)
		if len(errs) != 0 {
			t.Errorf("expected no errors for valid 17-char snapshotId, got: %v", errs)
		}
	})

	t.Run("rejects snap-XYZ (non-hex chars)", func(t *testing.T) {
		raw := buildSnapshotProfileRaw(`      - snapshotId: snap-XYZ
        mountPoint: /data
`)
		errs := profile.Validate(raw)
		if len(errs) == 0 {
			t.Error("expected schema error for non-hex snapshotId, got none")
		}
	})

	t.Run("rejects snap-0123abc (7 hex chars, too short)", func(t *testing.T) {
		raw := buildSnapshotProfileRaw(`      - snapshotId: snap-0123abc
        mountPoint: /data
`)
		errs := profile.Validate(raw)
		if len(errs) == 0 {
			t.Error("expected schema error for 7-char snapshotId (too short), got none")
		}
	})

	t.Run("rejects device /dev/sda (root range, not in [f-p])", func(t *testing.T) {
		raw := buildSnapshotProfileRaw(`      - snapshotId: snap-01234567
        mountPoint: /data
        device: /dev/sda
`)
		errs := profile.Validate(raw)
		if len(errs) == 0 {
			t.Error("expected schema error for device /dev/sda, got none")
		}
	})

	t.Run("rejects device /dev/sdq (out of [f-p])", func(t *testing.T) {
		raw := buildSnapshotProfileRaw(`      - snapshotId: snap-01234567
        mountPoint: /data
        device: /dev/sdq
`)
		errs := profile.Validate(raw)
		if len(errs) == 0 {
			t.Error("expected schema error for device /dev/sdq, got none")
		}
	})

	t.Run("accepts device /dev/sdf through /dev/sdp", func(t *testing.T) {
		for _, dev := range []string{"/dev/sdf", "/dev/sdg", "/dev/sdh", "/dev/sdi", "/dev/sdp"} {
			dev := dev
			t.Run(dev, func(t *testing.T) {
				raw := buildSnapshotProfileRaw(`      - snapshotId: snap-01234567
        mountPoint: /data
        device: ` + dev + `
`)
				errs := profile.Validate(raw)
				if len(errs) != 0 {
					t.Errorf("expected no errors for device %q, got: %v", dev, errs)
				}
			})
		}
	})

	t.Run("rejects size: 0 (must be >= 1)", func(t *testing.T) {
		raw := buildSnapshotProfileRaw(`      - snapshotId: snap-01234567
        mountPoint: /data
        size: 0
`)
		errs := profile.Validate(raw)
		if len(errs) == 0 {
			t.Error("expected schema error for size: 0, got none")
		}
	})

	t.Run("rejects unknown additional property kmsKeyId", func(t *testing.T) {
		raw := buildSnapshotProfileRaw(`      - snapshotId: snap-01234567
        mountPoint: /data
        kmsKeyId: key-12345
`)
		errs := profile.Validate(raw)
		if len(errs) == 0 {
			t.Error("expected schema error for unknown property kmsKeyId, got none")
		}
	})
}

// TestCLISpec_Agent_EnumValid: claude and codex are accepted.
// SC-1: schema accepts the two locked enum values.
// Phase 92 (Wave 4): the field moved from cli.agent to spec.agent.default.
func TestCLISpec_Agent_EnumValid(t *testing.T) {
	cases := []struct {
		name      string
		agentLine string
	}{
		{"claude", "    default: claude\n"},
		{"codex", "    default: codex\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := profile.Validate(minimalAgentDefaultProfileYAML(tc.agentLine))
			if len(errs) > 0 {
				t.Fatalf("expected no errors for agent=%s, got %v", tc.name, errs)
			}
		})
	}
}

// TestCLISpec_Agent_EnumInvalid: anything not in {claude, codex} is rejected
// with an error referencing the agent field.
// SC-1: schema rejects out-of-enum values.
func TestCLISpec_Agent_EnumInvalid(t *testing.T) {
	cases := []struct {
		name      string
		agentLine string
	}{
		{"goose-rejected", "    default: goose\n"},
		{"uppercase-rejected", "    default: CLAUDE\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := profile.Validate(minimalAgentDefaultProfileYAML(tc.agentLine))
			if len(errs) == 0 {
				t.Fatalf("expected validation error for agent line %q, got none", tc.agentLine)
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e.Error(), "agent") {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected error message to reference agent field; got %v", errs)
			}
		})
	}
}

// TestCLISpec_Agent_AbsenceIsClaudeDefault: when spec.agent is omitted entirely,
// the profile validates and parses with p.Spec.Agent == nil (zero value).
// The "default ≡ claude" behavior lives downstream in the compiler (Plan 70-02)
// and the poller (Plan 70-05); the schema accepts absence.
// Phase 92 (Wave 4): the field moved from cli.agent to spec.agent.default, so
// absence now means a nil *AgentSpec.
func TestCLISpec_Agent_AbsenceIsClaudeDefault(t *testing.T) {
	// noBedrock: false provides a present-but-minimal cli block with no agent block.
	yaml := minimalCLIProfileYAML("    noBedrock: false\n")
	errs := profile.Validate(yaml)
	if len(errs) > 0 {
		t.Fatalf("expected no errors when spec.agent omitted, got %v", errs)
	}
	p, parseErr := profile.Parse(yaml)
	if parseErr != nil {
		t.Fatalf("parse error: %v", parseErr)
	}
	if p.Spec.Agent != nil && p.Spec.Agent.Default != "" {
		t.Fatalf("expected p.Spec.Agent.Default == \"\" (zero value), got %q", p.Spec.Agent.Default)
	}
}

// TestParse_CLISpec_SlackFields_ExplicitFalse verifies that explicit false for
// *bool Slack fields round-trips as non-nil pointer to false (not nil).
// This is the key bool-vs-*bool discrimination test.
func TestParse_CLISpec_SlackFields_ExplicitFalse(t *testing.T) {
	yamlData := minimalNotificationProfileYAML(`    email:
      enabled: false
    slack:
      enabled: false
`)

	p, err := profile.Parse(yamlData)
	if err != nil {
		t.Fatalf("expected profile with explicit false Slack booleans to parse, got: %v", err)
	}
	if p.Spec.Notification == nil || p.Spec.Notification.Email == nil || p.Spec.Notification.Slack == nil {
		t.Fatal("expected Spec.Notification.Email and .Slack to be set, got nil")
	}
	if p.Spec.Notification.Email.Enabled == nil {
		t.Fatal("expected email.enabled to be non-nil (explicit false), got nil")
	}
	if *p.Spec.Notification.Email.Enabled != false {
		t.Errorf("expected *email.enabled=false, got %v", *p.Spec.Notification.Email.Enabled)
	}
	if p.Spec.Notification.Slack.Enabled == nil {
		t.Fatal("expected slack.enabled to be non-nil (explicit false), got nil")
	}
	if *p.Spec.Notification.Slack.Enabled != false {
		t.Errorf("expected *slack.enabled=false, got %v", *p.Spec.Notification.Slack.Enabled)
	}
}
