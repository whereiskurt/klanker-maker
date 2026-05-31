package profile_test

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// TestEFSProfileFields verifies that mountEFS and efsMountPoint fields
// round-trip correctly through YAML parsing on RuntimeSpec.
func TestEFSProfileFields(t *testing.T) {
	yamlData := `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: efs-test
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 1h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
    mountEFS: true
    efsMountPoint: /data
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
      enabled: false
      image: ""
    httpProxy:
      enabled: false
      image: ""
    auditLog:
      enabled: false
      image: ""
    tracing:
      enabled: false
      image: ""
  observability:
    commandLog:
      destination: stdout
    networkLog:
      destination: stdout
`
	p, err := profile.Parse([]byte(yamlData))
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	if !p.Spec.Runtime.MountEFS {
		t.Errorf("expected MountEFS=true, got false")
	}
	if p.Spec.Runtime.EFSMountPoint != "/data" {
		t.Errorf("expected EFSMountPoint=/data, got %q", p.Spec.Runtime.EFSMountPoint)
	}
}

// TestEFSProfileFieldsOmitted verifies that mountEFS and efsMountPoint
// default to their zero values when not present in YAML.
func TestEFSProfileFieldsOmitted(t *testing.T) {
	yamlData := `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: no-efs-test
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
      enabled: false
      image: ""
    httpProxy:
      enabled: false
      image: ""
    auditLog:
      enabled: false
      image: ""
    tracing:
      enabled: false
      image: ""
  observability:
    commandLog:
      destination: stdout
    networkLog:
      destination: stdout
`
	p, err := profile.Parse([]byte(yamlData))
	if err != nil {
		t.Fatalf("Parse() failed: %v", err)
	}

	if p.Spec.Runtime.MountEFS {
		t.Errorf("expected MountEFS=false when not set, got true")
	}
	if p.Spec.Runtime.EFSMountPoint != "" {
		t.Errorf("expected EFSMountPoint='' when not set, got %q", p.Spec.Runtime.EFSMountPoint)
	}
}
