package profile

// github_notification_test.go — Phase 97 Plan 03 tests
//
// Tests for:
//   - NotificationGitHubSpec / NotificationGitHubInboundSpec types (tri-state *bool)
//   - notification.github.inbound.enabled round-trips through parse + schema validate
//   - notification.github absent ⇒ nil (dormant)
//   - mergeNotificationGitHubSpec field-level nil-aware merge

import (
	"testing"
)

// ============================================================
// Type / parse tests
// ============================================================

// TestNotificationGitHub_ParseEnabled verifies that a profile YAML with
// notification.github.inbound.enabled: true round-trips through Parse().
func TestNotificationGitHub_ParseEnabled(t *testing.T) {
	yaml := `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: test-github
spec:
  lifecycle:
    ttl: "2h"
    idleTimeout: "20m"
    teardownPolicy: stop
  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: ["github.com"]
      allowedHosts: []
  iam:
    roleSessionDuration: "2h"
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
  notification:
    github:
      inbound:
        enabled: true
`
	p, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Spec.Notification == nil {
		t.Fatal("Notification is nil after parse")
	}
	if p.Spec.Notification.Github == nil {
		t.Fatal("Notification.Github is nil after parse")
	}
	if p.Spec.Notification.Github.Inbound == nil {
		t.Fatal("Notification.Github.Inbound is nil after parse")
	}
	if p.Spec.Notification.Github.Inbound.Enabled == nil {
		t.Fatal("Notification.Github.Inbound.Enabled is nil (expected *true)")
	}
	if !*p.Spec.Notification.Github.Inbound.Enabled {
		t.Error("Notification.Github.Inbound.Enabled: expected true")
	}
}

// TestNotificationGitHub_ParseAbsent verifies that when notification.github is
// absent, the field is nil (dormant invariant — zero artifacts).
func TestNotificationGitHub_ParseAbsent(t *testing.T) {
	yaml := `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: test-no-github
spec:
  lifecycle:
    ttl: "2h"
    idleTimeout: "20m"
    teardownPolicy: stop
  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: []
      allowedHosts: []
  iam:
    roleSessionDuration: "2h"
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
	p, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// notification block absent → nil
	if p.Spec.Notification != nil && p.Spec.Notification.Github != nil {
		t.Error("expected Notification.Github to be nil when absent from YAML")
	}
}

// ============================================================
// Merge tests
// ============================================================

// TestMergeNotificationGitHub_ChildOverridesParent verifies that a child profile's
// github.inbound.enabled=true overrides a nil parent field.
func TestMergeNotificationGitHub_ChildOverridesParent(t *testing.T) {
	boolTrue := true
	parent := &NotificationSpec{Github: nil}
	child := &NotificationSpec{
		Github: &NotificationGitHubSpec{
			Inbound: &NotificationGitHubInboundSpec{Enabled: &boolTrue},
		},
	}
	result := mergeNotificationSpec(parent, child)
	if result.Github == nil {
		t.Fatal("merge: Github nil after child override")
	}
	if result.Github.Inbound == nil {
		t.Fatal("merge: Github.Inbound nil after child override")
	}
	if result.Github.Inbound.Enabled == nil || !*result.Github.Inbound.Enabled {
		t.Error("merge: Github.Inbound.Enabled expected true")
	}
}

// TestMergeNotificationGitHub_ParentInheritsWhenChildNil verifies that when
// the child has no github block, the parent's github block is inherited.
func TestMergeNotificationGitHub_ParentInheritsWhenChildNil(t *testing.T) {
	boolTrue := true
	parent := &NotificationSpec{
		Github: &NotificationGitHubSpec{
			Inbound: &NotificationGitHubInboundSpec{Enabled: &boolTrue},
		},
	}
	child := &NotificationSpec{Github: nil}
	result := mergeNotificationSpec(parent, child)
	if result.Github == nil {
		t.Fatal("merge: Github nil — parent not inherited")
	}
	if result.Github.Inbound == nil || result.Github.Inbound.Enabled == nil || !*result.Github.Inbound.Enabled {
		t.Error("merge: Github.Inbound.Enabled expected true from parent")
	}
}

// TestMergeNotificationGitHub_BothNil verifies nil parent + nil child → nil result.
func TestMergeNotificationGitHub_BothNil(t *testing.T) {
	result := mergeNotificationSpec(nil, nil)
	if result != nil {
		t.Errorf("both nil → expected nil result, got %+v", result)
	}
}
