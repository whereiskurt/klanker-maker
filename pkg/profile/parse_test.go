package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// minimalProfileYAML is a reusable base profile for email alias tests.
// It omits the email section so tests can append their own.
const minimalProfileBase = `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: email-alias-test
  labels:
    tier: test
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
      allowedDNSSuffixes:
        - ".amazonaws.com"
      allowedHosts: []

  identity:
    roleSessionDuration: "1h"
    allowedRegions:
      - us-east-1
    sessionPolicy: minimal
  sidecars:
    dnsProxy:
      enabled: true
      image: km-dns-proxy:latest
    httpProxy:
      enabled: true
      image: km-http-proxy:latest
    auditLog:
      enabled: true
      image: km-audit-log:latest
    tracing:
      enabled: true
      image: km-tracing:latest
  observability:
    commandLog:
      destination: cloudwatch
      logGroup: /klankrmkr/sandboxes
    networkLog:
      destination: cloudwatch
      logGroup: /klankrmkr/network

  agent:
    maxConcurrentTasks: 4
    taskTimeout: "30m"
`

// TestParse_EmailAlias verifies that a profile with email.alias and
// email.allowedSenders parses into EmailSpec with the correct values.
func TestParse_EmailAlias(t *testing.T) {
	yaml := minimalProfileBase + `  email:
    signing: optional
    verifyInbound: optional
    encryption: off
    alias: "research.team-a"
    allowedSenders:
      - "self"
      - "build.*"
`

	p, err := profile.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("expected parse to succeed, got error: %v", err)
	}

	if p.Spec.Email == nil {
		t.Fatal("expected spec.email to be non-nil")
	}

	if p.Spec.Email.Alias != "research.team-a" {
		t.Errorf("expected email.alias=%q, got %q", "research.team-a", p.Spec.Email.Alias)
	}

	if len(p.Spec.Email.AllowedSenders) != 2 {
		t.Fatalf("expected 2 allowedSenders, got %d: %v", len(p.Spec.Email.AllowedSenders), p.Spec.Email.AllowedSenders)
	}
	if p.Spec.Email.AllowedSenders[0] != "self" {
		t.Errorf("expected allowedSenders[0]=%q, got %q", "self", p.Spec.Email.AllowedSenders[0])
	}
	if p.Spec.Email.AllowedSenders[1] != "build.*" {
		t.Errorf("expected allowedSenders[1]=%q, got %q", "build.*", p.Spec.Email.AllowedSenders[1])
	}
}

// TestParse_EmailAlias_Empty verifies that a profile with an email section but
// no alias or allowedSenders parses with zero-value Alias and nil AllowedSenders.
func TestParse_EmailAlias_Empty(t *testing.T) {
	yaml := minimalProfileBase + `  email:
    signing: optional
    verifyInbound: optional
    encryption: off
`

	p, err := profile.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("expected parse to succeed, got error: %v", err)
	}

	if p.Spec.Email == nil {
		t.Fatal("expected spec.email to be non-nil")
	}

	if p.Spec.Email.Alias != "" {
		t.Errorf("expected email.alias to be empty string when omitted, got %q", p.Spec.Email.Alias)
	}

	if p.Spec.Email.AllowedSenders != nil {
		t.Errorf("expected email.allowedSenders to be nil when omitted, got %v", p.Spec.Email.AllowedSenders)
	}
}

// TestValidateSchema_EmailAlias_Invalid verifies that km validate rejects an
// alias containing uppercase letters via the JSON schema pattern.
func TestValidateSchema_EmailAlias_Invalid(t *testing.T) {
	yaml := minimalProfileBase + `  email:
    signing: optional
    verifyInbound: optional
    encryption: off
    alias: "Research.team-a"
`

	errs := profile.ValidateSchema([]byte(yaml))
	if len(errs) == 0 {
		t.Error("expected schema validation to reject alias with uppercase letters, got no errors")
		return
	}

	// At least one error should reference alias or pattern
	found := false
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, "alias") || strings.Contains(msg, "pattern") ||
			strings.Contains(msg, "Research") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error referencing alias pattern, errors were: %v", errs)
	}
}

// TestValidateSchema_EmailAlias_NoDot verifies that km validate rejects an
// alias without a dot (e.g. "research") via the JSON schema pattern.
func TestValidateSchema_EmailAlias_NoDot(t *testing.T) {
	yaml := minimalProfileBase + `  email:
    signing: optional
    verifyInbound: optional
    encryption: off
    alias: "research"
`

	errs := profile.ValidateSchema([]byte(yaml))
	if len(errs) == 0 {
		t.Error("expected schema validation to reject alias without a dot, got no errors")
		return
	}

	// At least one error should reference alias or pattern
	found := false
	for _, e := range errs {
		msg := e.Error()
		if strings.Contains(msg, "alias") || strings.Contains(msg, "pattern") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error referencing alias pattern, errors were: %v", errs)
	}
}
