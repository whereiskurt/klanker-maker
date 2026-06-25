package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// boolPtrInbound is a local helper to create *bool values for inbound tests.
// (Avoids redeclaring boolPtr which is already in validate_test.go.)
func boolPtrInbound(b bool) *bool { return &b }

// minimalInboundProfile builds the smallest valid SandboxProfile with the given
// NotificationSpec.
// Phase 92 (Wave 2): migrated from CLISpec to the structured notification block.
func minimalInboundProfile(n *profile.NotificationSpec) *profile.SandboxProfile {
	return &profile.SandboxProfile{
		APIVersion: "klankermaker.ai/v1alpha2",
		Kind:       "SandboxProfile",
		Spec: profile.Spec{
			Notification: n,
		},
	}
}

// containsInboundError returns true if any non-warning error message contains substr.
func containsInboundError(errs []profile.ValidationError, substr string) bool {
	for _, e := range errs {
		if e.IsWarning {
			continue
		}
		if strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

// TestValidate_SlackInbound_RequiresSlackEnabled verifies that setting
// inbound.enabled=true without slack.enabled=true produces an error containing
// "requires notification.slack.enabled".
func TestValidate_SlackInbound_RequiresSlackEnabled(t *testing.T) {
	p := minimalInboundProfile(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtrInbound(false), // outbound off
			PerSandbox: boolPtrInbound(true),
			Inbound:    &profile.NotificationSlackInboundSpec{Enabled: boolPtrInbound(true)},
		},
	})
	errs := profile.ValidateSemantic(p)
	if !containsInboundError(errs, "requires notification.slack.enabled") {
		t.Fatalf("expected error mentioning 'requires notification.slack.enabled', got %+v", errs)
	}
}

// TestValidate_SlackInbound_RequiresPerSandbox verifies that setting
// inbound.enabled=true without slack.perSandbox=true produces an error containing
// "requires notification.slack.perSandbox".
func TestValidate_SlackInbound_RequiresPerSandbox(t *testing.T) {
	p := minimalInboundProfile(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtrInbound(true),
			PerSandbox: boolPtrInbound(false),
			Inbound:    &profile.NotificationSlackInboundSpec{Enabled: boolPtrInbound(true)},
		},
	})
	errs := profile.ValidateSemantic(p)
	if !containsInboundError(errs, "requires notification.slack.perSandbox") {
		t.Fatalf("expected error mentioning 'requires notification.slack.perSandbox', got %+v", errs)
	}
}

// TestValidate_SlackInbound_RejectsChannelOverride verifies that setting
// inbound.enabled=true alongside slack.channelOverride produces an error containing
// "incompatible with notification.slack.channelOverride".
func TestValidate_SlackInbound_RejectsChannelOverride(t *testing.T) {
	p := minimalInboundProfile(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:         boolPtrInbound(true),
			PerSandbox:      boolPtrInbound(true),
			ChannelOverride: "C0123ABC",
			Inbound:         &profile.NotificationSlackInboundSpec{Enabled: boolPtrInbound(true)},
		},
	})
	errs := profile.ValidateSemantic(p)
	if !containsInboundError(errs, "incompatible with notification.slack.channelOverride") {
		t.Fatalf("expected incompatibility error, got %+v", errs)
	}
}

// containsInboundWarn returns true if any IsWarning=true error message contains substr.
func containsInboundWarn(errs []profile.ValidationError, substr string) bool {
	for _, e := range errs {
		if !e.IsWarning {
			continue
		}
		if strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

// intPtrInbound returns a *int pointing at v for inbound tests.
// (Avoids redeclaring intPtr which is already in validate_test.go.)
func intPtrInbound(v int) *int { return &v }

// TestValidate_SlackInbound_MaxConcurrency_WarnsWithoutPerSandbox verifies that
// setting maxConcurrentThreads>1 WITHOUT (perSandbox=true AND inbound.enabled=true)
// produces a WARNING at spec.notification.slack.inbound.maxConcurrentThreads.
// RED: the WARN rule is not yet added to ValidateSemantic.
func TestValidate_SlackInbound_MaxConcurrency_WarnsWithoutPerSandbox(t *testing.T) {
	p := minimalInboundProfile(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtrInbound(true),
			PerSandbox: boolPtrInbound(false), // perSandbox not set
			Inbound: &profile.NotificationSlackInboundSpec{
				Enabled:              boolPtrInbound(false),
				MaxConcurrentThreads: intPtrInbound(3), // cap>1 but conditions not met
			},
		},
	})
	errs := profile.ValidateSemantic(p)
	if !containsInboundWarn(errs, "maxConcurrentThreads") {
		t.Fatalf("expected WARNING mentioning 'maxConcurrentThreads' when cap>1 without perSandbox+inbound.enabled, got %+v", errs)
	}
}

// TestValidate_SlackInbound_MaxConcurrency_NoWarnWhenCorrect verifies that
// maxConcurrentThreads>1 WITH both perSandbox=true AND inbound.enabled=true
// produces NO warning about maxConcurrentThreads.
// RED: this may actually pass once the WARN rule is added (it's the positive case).
// Kept here to guard regression.
func TestValidate_SlackInbound_MaxConcurrency_NoWarnWhenCorrect(t *testing.T) {
	p := minimalInboundProfile(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtrInbound(true),
			PerSandbox: boolPtrInbound(true),
			Inbound: &profile.NotificationSlackInboundSpec{
				Enabled:              boolPtrInbound(true),
				MaxConcurrentThreads: intPtrInbound(3),
			},
		},
	})
	errs := profile.ValidateSemantic(p)
	if containsInboundWarn(errs, "maxConcurrentThreads") {
		t.Fatalf("expected NO maxConcurrentThreads warning when perSandbox=true and inbound.enabled=true, got %+v", errs)
	}
}

// TestValidate_SlackInbound_MaxConcurrency_RejectsZero verifies that
// maxConcurrentThreads:0 fails JSON-schema validation (minimum:1).
// This test exercises ValidateSchema via raw YAML bytes.
// May turn GREEN once Task 1's schema is in place — kept as regression guard.
func TestValidate_SlackInbound_MaxConcurrency_RejectsZero(t *testing.T) {
	// Raw YAML with maxConcurrentThreads:0 (below the schema minimum:1).
	raw := []byte(`apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: schema-reject-zero
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
  iam:
    roleSessionDuration: "1h"
    allowedRegions:
      - us-east-1
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
    networkLog:
      destination: cloudwatch
  notification:
    slack:
      enabled: true
      perSandbox: true
      inbound:
        enabled: true
        maxConcurrentThreads: 0
`)
	errs := profile.ValidateSchema(raw)
	if len(errs) == 0 {
		t.Fatal("expected schema validation to reject maxConcurrentThreads:0 (minimum:1), got no errors")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "maxConcurrentThreads") || strings.Contains(e.Message, "minimum") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an error referencing maxConcurrentThreads/minimum, got %+v", errs)
	}
}

// TestValidate_SlackInbound_DefaultFalseHasNoImpact verifies that the default
// value of inbound.enabled=false produces no inbound-specific errors.
func TestValidate_SlackInbound_DefaultFalseHasNoImpact(t *testing.T) {
	p := minimalInboundProfile(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtrInbound(false),
			PerSandbox: boolPtrInbound(false),
			Inbound:    &profile.NotificationSlackInboundSpec{Enabled: boolPtrInbound(false)}, // default
		},
	})
	errs := profile.ValidateSemantic(p)
	for _, e := range errs {
		if strings.Contains(e.Message, "inbound") {
			t.Fatalf("default false should not produce inbound-specific errors, got %+v", e)
		}
	}
}
