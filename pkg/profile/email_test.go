package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// minimalProfileYAML returns a base valid profile YAML without spec.email,
// used to compose test cases that add the email section.
func minimalProfileWithEmailYAML(email string) string {
	return `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: email-test
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
` + email
}

// TestEmailSpecParsesFromYAML verifies EmailSpec round-trips through YAML parsing.
func TestEmailSpecParsesFromYAML(t *testing.T) {
	yaml := minimalProfileWithEmailYAML(`  email:
    signing: required
    verifyInbound: required
    encryption: off
`)

	p, err := profile.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("expected profile with email to parse without error, got: %v", err)
	}

	if p.Spec.Email == nil {
		t.Fatal("expected spec.email to be populated, got nil")
	}

	e := p.Spec.Email
	if e.Signing != "required" {
		t.Errorf("expected signing=required, got %q", e.Signing)
	}
	if e.VerifyInbound != "required" {
		t.Errorf("expected verifyInbound=required, got %q", e.VerifyInbound)
	}
	if e.Encryption != "off" {
		t.Errorf("expected encryption=off, got %q", e.Encryption)
	}
}

// TestEmailSpecSchemaValidation verifies valid email spec passes JSON schema.
func TestEmailSpecSchemaValidation(t *testing.T) {
	yaml := minimalProfileWithEmailYAML(`  email:
    signing: required
    verifyInbound: required
    encryption: off
`)

	errs := profile.ValidateSchema([]byte(yaml))
	if len(errs) != 0 {
		t.Errorf("expected no schema errors for valid email spec, got %d errors:", len(errs))
		for _, e := range errs {
			t.Logf("  - %s", e.Error())
		}
	}
}

// TestEmailSpecSchemaRejectsInvalidEnum verifies that invalid enum values are rejected.
func TestEmailSpecSchemaRejectsInvalidEnum(t *testing.T) {
	yaml := minimalProfileWithEmailYAML(`  email:
    signing: invalid
    verifyInbound: required
    encryption: off
`)

	errs := profile.ValidateSchema([]byte(yaml))
	if len(errs) == 0 {
		t.Error("expected schema error for signing=invalid, got none")
		return
	}

	found := false
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, "signing") || strings.Contains(msg, "enum") || strings.Contains(msg, "invalid") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error referencing signing enum, errors were: %v", errs)
	}
}

// TestEmailSpecOptional verifies that a profile without spec.email is valid.
func TestEmailSpecOptional(t *testing.T) {
	yaml := minimalProfileWithEmailYAML("") // no email section

	p, err := profile.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("expected profile without email to parse without error, got: %v", err)
	}

	if p.Spec.Email != nil {
		t.Errorf("expected spec.email to be nil when absent, got %+v", p.Spec.Email)
	}
}

// TestEmailSpecOptionalPassesSchema verifies that a profile without spec.email passes schema validation.
func TestEmailSpecOptionalPassesSchema(t *testing.T) {
	yaml := minimalProfileWithEmailYAML("") // no email section

	errs := profile.ValidateSchema([]byte(yaml))
	if len(errs) != 0 {
		t.Errorf("expected no schema errors for profile without email, got %d errors:", len(errs))
		for _, e := range errs {
			t.Logf("  - %s", e.Error())
		}
	}
}

// TestEmailSpecAllOptionalValues verifies all three enum values are accepted.
func TestEmailSpecAllOptionalValues(t *testing.T) {
	values := []string{"required", "optional", "off"}
	for _, v := range values {
		t.Run("signing="+v, func(t *testing.T) {
			yaml := minimalProfileWithEmailYAML(`  email:
    signing: ` + v + `
    verifyInbound: optional
    encryption: off
`)
			errs := profile.ValidateSchema([]byte(yaml))
			if len(errs) != 0 {
				t.Errorf("expected no errors for signing=%s, got: %v", v, errs)
			}
		})
	}
}
