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

// TestSchemaAMIRawIDValid verifies that raw EC2 AMI IDs (ami-xxxxxxxx) are accepted by the schema.
func TestSchemaAMIRawIDValid(t *testing.T) {
	cases := []struct {
		name string
		ami  string
	}{
		{"17-char canonical form", "ami-0abcdef1234567890"},
		{"8-char legacy form", "ami-12345678"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yaml := minimalRuntimeProfile("    ami: " + tc.ami + "\n")
			// Verify JSON schema accepts the raw AMI ID.
			errs := profile.ValidateSchema([]byte(yaml))
			if len(errs) != 0 {
				t.Fatalf("expected no schema errors for ami=%q, got: %v", tc.ami, errs)
			}
			// Verify Go struct unmarshals the raw ID verbatim.
			p, err := profile.Parse([]byte(yaml))
			if err != nil {
				t.Fatalf("expected no parse error for ami=%q, got: %v", tc.ami, err)
			}
			if p.Spec.Runtime.AMI != tc.ami {
				t.Errorf("expected AMI=%q, got %q", tc.ami, p.Spec.Runtime.AMI)
			}
		})
	}
}

// TestSchemaAMIRawIDInvalid verifies that malformed AMI IDs and unknown slugs are rejected by JSON schema validation.
func TestSchemaAMIRawIDInvalid(t *testing.T) {
	cases := []struct {
		name string
		ami  string
	}{
		{"uppercase hex chars", "ami-GGGGGGGG"},
		{"too short (7 hex)", "ami-123"},
		{"too long (19 hex)", "ami-0abcdef1234567890ab"},
		{"no hex chars", "ami-"},
		{"unknown slug", "ubuntu-25.04"},
		{"uppercase prefix", "AMI-0abc12345"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yaml := minimalRuntimeProfile("    ami: " + tc.ami + "\n")
			errs := profile.ValidateSchema([]byte(yaml))
			if len(errs) == 0 {
				t.Errorf("expected schema error for ami=%q, got none", tc.ami)
			}
		})
	}
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
