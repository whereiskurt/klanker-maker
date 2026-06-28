package profile

import (
	"testing"
)

// minimalEC2YAML returns a minimal-but-valid EC2 profile YAML.
// The azPreference value is set so the caller can inject it.
func minimalEC2YAML(azPref string) string {
	return `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: test-az-pref
spec:
  lifecycle:
    ttl: "24h"
    idleTimeout: "2h"
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    spot: true
    instanceType: g6e.12xlarge
    region: us-east-1
` + azPref + `  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
    github:
      allowedRepos:
        - "github.com/example/*"
      allowedRefs:
        - main
  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
      allowedHosts:
        - "api.example.com"
  iam:
    roleSessionDuration: "1h"
    allowedRegions:
      - us-east-1
  sidecars:
    dnsProxy:
      enabled: false
      image: km-dns-proxy:latest
    httpProxy:
      enabled: false
      image: km-http-proxy:latest
    auditLog:
      enabled: false
      image: km-audit-log:latest
    tracing:
      enabled: false
      image: km-tracing:latest
  observability:
    commandLog:
      destination: cloudwatch
      logGroup: /km/sandboxes
    networkLog:
      destination: cloudwatch
      logGroup: /km/network
`
}

// TestValidate_AZPreference verifies that spec.runtime.azPreference is accepted
// as a []string field by the JSON schema validator (Phase 124, REQ-124-SURFACE).
func TestValidate_AZPreference(t *testing.T) {
	azPref := "    azPreference:\n      - us-east-1c\n      - us-east-1b\n"
	errs := Validate([]byte(minimalEC2YAML(azPref)))
	if len(errs) != 0 {
		t.Errorf("expected no validation errors with azPreference set, got %d: %v", len(errs), errs)
	}
}

// TestValidate_AZPreference_EmptyList verifies an empty azPreference list is valid.
func TestValidate_AZPreference_EmptyList(t *testing.T) {
	azPref := "    azPreference: []\n"
	errs := Validate([]byte(minimalEC2YAML(azPref)))
	if len(errs) != 0 {
		t.Errorf("expected no validation errors with empty azPreference, got %d: %v", len(errs), errs)
	}
}

// TestValidate_AZPreference_Absent verifies that omitting azPreference is still valid
// (field is additive / optional — absence must not break validation or byte-identical
// compile output for existing profiles).
func TestValidate_AZPreference_Absent(t *testing.T) {
	errs := Validate([]byte(minimalEC2YAML("")))
	if len(errs) != 0 {
		t.Errorf("expected no validation errors without azPreference, got %d: %v", len(errs), errs)
	}
}
