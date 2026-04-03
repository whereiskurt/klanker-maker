package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// TestSchemaRootVolumeParsing verifies that rootVolumeSize parses correctly from YAML.
func TestSchemaRootVolumeParsing(t *testing.T) {
	t.Run("rootVolumeSize 50 parses correctly", func(t *testing.T) {
		yaml := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: storage-test
spec:
  lifecycle:
    ttl: "24h"
    idleTimeout: "1h"
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
    rootVolumeSize: 50
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
    roleSessionDuration: "1h"
    allowedRegions: ["us-east-1"]
    sessionPolicy: standard
  sidecars:
    dnsProxy:
      enabled: false
      image: "dns:latest"
    httpProxy:
      enabled: false
      image: "proxy:latest"
    auditLog:
      enabled: false
      image: "audit:latest"
    tracing:
      enabled: false
      image: "trace:latest"
  observability:
    commandLog:
      destination: stdout
    networkLog:
      destination: stdout
  agent:
    maxConcurrentTasks: 1
    taskTimeout: "1h"
`
		p, err := profile.Parse([]byte(yaml))
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		if p.Spec.Runtime.RootVolumeSize != 50 {
			t.Errorf("expected RootVolumeSize=50, got %d", p.Spec.Runtime.RootVolumeSize)
		}
	})

	t.Run("rootVolumeSize omitted parses as 0", func(t *testing.T) {
		yaml := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: storage-test-zero
spec:
  lifecycle:
    ttl: "24h"
    idleTimeout: "1h"
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
    roleSessionDuration: "1h"
    allowedRegions: ["us-east-1"]
    sessionPolicy: standard
  sidecars:
    dnsProxy:
      enabled: false
      image: "dns:latest"
    httpProxy:
      enabled: false
      image: "proxy:latest"
    auditLog:
      enabled: false
      image: "audit:latest"
    tracing:
      enabled: false
      image: "trace:latest"
  observability:
    commandLog:
      destination: stdout
    networkLog:
      destination: stdout
  agent:
    maxConcurrentTasks: 1
    taskTimeout: "1h"
`
		p, err := profile.Parse([]byte(yaml))
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		if p.Spec.Runtime.RootVolumeSize != 0 {
			t.Errorf("expected RootVolumeSize=0 when omitted, got %d", p.Spec.Runtime.RootVolumeSize)
		}
	})
}

// TestSchemaAdditionalVolumeParsing verifies that additionalVolume parses correctly.
func TestSchemaAdditionalVolumeParsing(t *testing.T) {
	t.Run("additionalVolume with all fields parses correctly", func(t *testing.T) {
		yaml := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: storage-extra-vol
spec:
  lifecycle:
    ttl: "24h"
    idleTimeout: "1h"
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
    additionalVolume:
      size: 100
      mountPoint: /data
      encrypted: true
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
    roleSessionDuration: "1h"
    allowedRegions: ["us-east-1"]
    sessionPolicy: standard
  sidecars:
    dnsProxy:
      enabled: false
      image: "dns:latest"
    httpProxy:
      enabled: false
      image: "proxy:latest"
    auditLog:
      enabled: false
      image: "audit:latest"
    tracing:
      enabled: false
      image: "trace:latest"
  observability:
    commandLog:
      destination: stdout
    networkLog:
      destination: stdout
  agent:
    maxConcurrentTasks: 1
    taskTimeout: "1h"
`
		p, err := profile.Parse([]byte(yaml))
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		if p.Spec.Runtime.AdditionalVolume == nil {
			t.Fatal("expected AdditionalVolume to be non-nil")
		}
		if p.Spec.Runtime.AdditionalVolume.Size != 100 {
			t.Errorf("expected Size=100, got %d", p.Spec.Runtime.AdditionalVolume.Size)
		}
		if p.Spec.Runtime.AdditionalVolume.MountPoint != "/data" {
			t.Errorf("expected MountPoint=/data, got %q", p.Spec.Runtime.AdditionalVolume.MountPoint)
		}
		if !p.Spec.Runtime.AdditionalVolume.Encrypted {
			t.Errorf("expected Encrypted=true")
		}
	})

	t.Run("additionalVolume omitted parses as nil", func(t *testing.T) {
		yaml := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: storage-no-extra-vol
spec:
  lifecycle:
    ttl: "24h"
    idleTimeout: "1h"
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
    roleSessionDuration: "1h"
    allowedRegions: ["us-east-1"]
    sessionPolicy: standard
  sidecars:
    dnsProxy:
      enabled: false
      image: "dns:latest"
    httpProxy:
      enabled: false
      image: "proxy:latest"
    auditLog:
      enabled: false
      image: "audit:latest"
    tracing:
      enabled: false
      image: "trace:latest"
  observability:
    commandLog:
      destination: stdout
    networkLog:
      destination: stdout
  agent:
    maxConcurrentTasks: 1
    taskTimeout: "1h"
`
		p, err := profile.Parse([]byte(yaml))
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}
		if p.Spec.Runtime.AdditionalVolume != nil {
			t.Errorf("expected AdditionalVolume=nil when omitted")
		}
	})
}

// TestSchemaHibernationParsing verifies that hibernation parses correctly.
func TestSchemaHibernationParsing(t *testing.T) {
	yaml := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: hibernation-test
spec:
  lifecycle:
    ttl: "24h"
    idleTimeout: "1h"
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
    hibernation: true
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
    roleSessionDuration: "1h"
    allowedRegions: ["us-east-1"]
    sessionPolicy: standard
  sidecars:
    dnsProxy:
      enabled: false
      image: "dns:latest"
    httpProxy:
      enabled: false
      image: "proxy:latest"
    auditLog:
      enabled: false
      image: "audit:latest"
    tracing:
      enabled: false
      image: "trace:latest"
  observability:
    commandLog:
      destination: stdout
    networkLog:
      destination: stdout
  agent:
    maxConcurrentTasks: 1
    taskTimeout: "1h"
`
	p, err := profile.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !p.Spec.Runtime.Hibernation {
		t.Errorf("expected Hibernation=true")
	}
}

// TestSchemaAMIParsing verifies that ami parses correctly.
func TestSchemaAMIParsing(t *testing.T) {
	yaml := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: ami-test
spec:
  lifecycle:
    ttl: "24h"
    idleTimeout: "1h"
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
    ami: ubuntu-24.04
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
    roleSessionDuration: "1h"
    allowedRegions: ["us-east-1"]
    sessionPolicy: standard
  sidecars:
    dnsProxy:
      enabled: false
      image: "dns:latest"
    httpProxy:
      enabled: false
      image: "proxy:latest"
    auditLog:
      enabled: false
      image: "audit:latest"
    tracing:
      enabled: false
      image: "trace:latest"
  observability:
    commandLog:
      destination: stdout
    networkLog:
      destination: stdout
  agent:
    maxConcurrentTasks: 1
    taskTimeout: "1h"
`
	p, err := profile.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if p.Spec.Runtime.AMI != "ubuntu-24.04" {
		t.Errorf("expected AMI=ubuntu-24.04, got %q", p.Spec.Runtime.AMI)
	}
}

// minimalRuntimeYAML returns the base of a profile YAML with a customizable runtime section.
func minimalRuntimeProfile(runtimeExtra string) string {
	return `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: schema-validation-test
spec:
  lifecycle:
    ttl: "24h"
    idleTimeout: "1h"
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
  identity:
    roleSessionDuration: "1h"
    allowedRegions: ["us-east-1"]
    sessionPolicy: standard
  sidecars:
    dnsProxy:
      enabled: false
      image: "dns:latest"
    httpProxy:
      enabled: false
      image: "proxy:latest"
    auditLog:
      enabled: false
      image: "audit:latest"
    tracing:
      enabled: false
      image: "trace:latest"
  observability:
    commandLog:
      destination: stdout
    networkLog:
      destination: stdout
  agent:
    maxConcurrentTasks: 1
    taskTimeout: "1h"
`
}

// TestSchemaRootVolumeValidation tests JSON schema validation of rootVolumeSize boundaries.
func TestSchemaRootVolumeValidation(t *testing.T) {
	t.Run("rootVolumeSize 50 passes schema", func(t *testing.T) {
		yaml := minimalRuntimeProfile("    rootVolumeSize: 50\n")
		errs := profile.ValidateSchema([]byte(yaml))
		if len(errs) != 0 {
			t.Errorf("expected no schema errors for rootVolumeSize=50, got: %v", errs)
		}
	})

	t.Run("rootVolumeSize -1 fails schema", func(t *testing.T) {
		yaml := minimalRuntimeProfile("    rootVolumeSize: -1\n")
		errs := profile.ValidateSchema([]byte(yaml))
		if len(errs) == 0 {
			t.Error("expected schema error for rootVolumeSize=-1, got none")
		}
		found := false
		for _, e := range errs {
			if strings.Contains(e.Error(), "rootVolumeSize") || strings.Contains(e.Error(), "minimum") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected error mentioning rootVolumeSize or minimum, got: %v", errs)
		}
	})

	t.Run("ami amazon-linux-2023 passes schema", func(t *testing.T) {
		yaml := minimalRuntimeProfile("    ami: amazon-linux-2023\n")
		errs := profile.ValidateSchema([]byte(yaml))
		if len(errs) != 0 {
			t.Errorf("expected no schema errors for ami=amazon-linux-2023, got: %v", errs)
		}
	})
}
