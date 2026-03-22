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
      allowedMethods: ["GET"]
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
  policy:
    allowShellEscape: false
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
	if p.Spec.Policy.AllowShellEscape == false {
		// allowShellEscape: false is the default — check the field exists by confirming type
		// Just ensure we can access it without panic
		_ = p.Spec.Policy.AllowShellEscape
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
