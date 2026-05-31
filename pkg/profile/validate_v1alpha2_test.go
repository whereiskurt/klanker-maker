package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// validV1alpha2Body is a minimal, schema-valid v1alpha2 profile body (sans
// apiVersion line) used by the Phase 92 version/rename gate tests below.
const validV1alpha2Body = `kind: SandboxProfile
metadata:
  name: gate-test
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 2h
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
    dnsProxy: {enabled: true, image: "x"}
    httpProxy: {enabled: true, image: "x"}
    auditLog: {enabled: true, image: "x"}
    tracing: {enabled: false, image: "x"}
  observability:
    commandLog: {destination: stdout}
    networkLog: {destination: stdout}
`

// TestValidateV1alpha2_Accepted confirms the canonical v1alpha2 apiVersion with
// the renamed iam: block validates cleanly (Phase 92 Wave 1).
func TestValidateV1alpha2_Accepted(t *testing.T) {
	data := []byte("apiVersion: klankermaker.ai/v1alpha2\n" + validV1alpha2Body)
	if errs := profile.Validate(data); len(errs) != 0 {
		t.Fatalf("expected valid v1alpha2 profile, got errors: %v", errs)
	}
}

// TestValidateLegacyV1alpha1_Rejected confirms the version gate is STRICT:
// the previous apiVersion v1alpha1 no longer validates (Phase 92 bumped the
// accepted version to v1alpha2 with no backwards compatibility).
func TestValidateLegacyV1alpha1_Rejected(t *testing.T) {
	data := []byte("apiVersion: klankermaker.ai/v1alpha1\n" + validV1alpha2Body)
	errs := profile.Validate(data)
	if len(errs) == 0 {
		t.Fatal("expected legacy apiVersion v1alpha1 to be rejected, got no errors")
	}
	joined := joinV1alpha2Errs(errs)
	if !strings.Contains(joined, "v1alpha2") && !strings.Contains(joined, "apiVersion") {
		t.Errorf("expected an apiVersion-pattern error mentioning v1alpha2, got: %s", joined)
	}
}

// TestValidateLegacyIdentityKey_Rejected confirms the pre-Phase-92 spec.identity:
// key is rejected on a v1alpha2 profile (renamed to spec.iam:).
func TestValidateLegacyIdentityKey_Rejected(t *testing.T) {
	body := strings.Replace(validV1alpha2Body, "  iam:", "  identity:", 1)
	data := []byte("apiVersion: klankermaker.ai/v1alpha2\n" + body)
	if errs := profile.Validate(data); len(errs) == 0 {
		t.Fatal("expected legacy spec.identity: key to be rejected, got no errors")
	}
}

// TestValidateDeadAgentBlock_Rejected confirms the dead spec.agent: block is
// rejected on a v1alpha2 profile (removed in Wave 1; Wave 4 re-introduces a new
// agent: shape).
func TestValidateDeadAgentBlock_Rejected(t *testing.T) {
	body := validV1alpha2Body + "  agent: {maxConcurrentTasks: 1, taskTimeout: 30m}\n"
	data := []byte("apiVersion: klankermaker.ai/v1alpha2\n" + body)
	if errs := profile.Validate(data); len(errs) == 0 {
		t.Fatal("expected dead spec.agent: block to be rejected, got no errors")
	}
}

func joinV1alpha2Errs(errs []profile.ValidationError) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}
