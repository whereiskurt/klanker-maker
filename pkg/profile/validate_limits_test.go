package profile_test

// validate_limits_test.go — Phase 121 Plan 03 (PROF-01) tests for spec.limits
// schema validation and semantic validation.
//
// Test invariants:
//  1. A profile with a valid spec.limits block passes both schema and semantic checks.
//  2. An invalid onBreach value is rejected by semantic validation.
//  3. Zero or negative limit values are rejected by semantic validation.
//  4. An absent spec.limits block passes validation unchanged (dormant by default).
//
// Schema loading uses profile.ValidateSchema — the same path used by
// the production km validate command.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// minimalProfileWithLimits is a valid v1alpha2 profile YAML with a spec.limits block.
// It uses the learn.yaml structural base (substrate=ec2, proxy enforcement) and
// appends only the fields needed to keep km validate happy without a real AWS
// context. The limits block is appended at the end.
const minimalLimitsBase = `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: limits-test
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 1h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    spot: false
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
    roleSessionDuration: 1h
    allowedRegions: [us-east-1]
  sidecars:
    dnsProxy:
      enabled: false
      image: "none"
    httpProxy:
      enabled: false
      image: "none"
    auditLog:
      enabled: false
      image: "none"
    tracing:
      enabled: false
      image: "none"
  observability:
    commandLog:
      destination: cloudwatch
    networkLog:
      destination: cloudwatch
`

// withLimits appends a limits: block to the minimal base profile YAML.
func withLimits(limitsYAML string) string {
	return minimalLimitsBase + "  limits:\n" + limitsYAML
}

// TestSpecLimits_ValidBlock verifies that a profile with a well-formed spec.limits
// block passes both schema validation and semantic validation.
func TestSpecLimits_ValidBlock(t *testing.T) {
	yaml := withLimits(`
    github_pr:
      lifetime: 100
      perHour: 15
      perDay: 50
      onBreach: freeze
    github_comment:
      perHour: 60
      onBreach: warn
    email_send:
      lifetime: 200
      perHour: 10
      onBreach: block
    slack_post:
      perHour: 120
      onBreach: warn
    h1_comment:
      perHour: 60
      onBreach: warn
    github_review:
      perDay: 20
      onBreach: block
`)

	// Schema validation must pass.
	schemaErrs := profile.ValidateSchema([]byte(yaml))
	for _, e := range schemaErrs {
		t.Errorf("ValidateSchema unexpected error: %s: %s", e.Path, e.Message)
	}

	// Semantic validation must also pass.
	p, err := profile.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	semErrs := profile.ValidateSemantic(p)
	for _, e := range semErrs {
		if !e.IsWarning {
			t.Errorf("ValidateSemantic unexpected error: %s: %s", e.Path, e.Message)
		}
	}

	// Verify the struct was populated correctly (round-trip sanity).
	if p.Spec.Limits == nil {
		t.Fatal("Spec.Limits is nil after parse")
	}
	if p.Spec.Limits.GithubPR == nil {
		t.Fatal("Spec.Limits.GithubPR is nil")
	}
	if p.Spec.Limits.GithubPR.OnBreach != "freeze" {
		t.Errorf("GithubPR.OnBreach: got %q, want %q", p.Spec.Limits.GithubPR.OnBreach, "freeze")
	}
	if p.Spec.Limits.GithubPR.Lifetime == nil || *p.Spec.Limits.GithubPR.Lifetime != 100 {
		t.Errorf("GithubPR.Lifetime: got %v, want 100", p.Spec.Limits.GithubPR.Lifetime)
	}
	if p.Spec.Limits.GithubPR.PerHour == nil || *p.Spec.Limits.GithubPR.PerHour != 15 {
		t.Errorf("GithubPR.PerHour: got %v, want 15", p.Spec.Limits.GithubPR.PerHour)
	}
	if p.Spec.Limits.GithubPR.PerDay == nil || *p.Spec.Limits.GithubPR.PerDay != 50 {
		t.Errorf("GithubPR.PerDay: got %v, want 50", p.Spec.Limits.GithubPR.PerDay)
	}
}

// TestSpecLimits_AbsentBlock verifies that a profile with no spec.limits block
// passes validation unchanged (dormant by default — byte-identical to pre-Phase-121).
func TestSpecLimits_AbsentBlock(t *testing.T) {
	yaml := minimalLimitsBase

	schemaErrs := profile.ValidateSchema([]byte(yaml))
	for _, e := range schemaErrs {
		t.Errorf("ValidateSchema unexpected error: %s: %s", e.Path, e.Message)
	}

	p, err := profile.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Spec.Limits != nil {
		t.Errorf("Spec.Limits: got non-nil (%+v), want nil (dormant by default)", p.Spec.Limits)
	}

	// Semantic validation must not produce any errors from the absent limits block.
	semErrs := profile.ValidateSemantic(p)
	for _, e := range semErrs {
		if !e.IsWarning && strings.Contains(e.Path, "limits") {
			t.Errorf("ValidateSemantic unexpected limits error: %s: %s", e.Path, e.Message)
		}
	}
}

// TestSpecLimits_BadOnBreach verifies that invalid onBreach values are rejected
// by semantic validation.
func TestSpecLimits_BadOnBreach(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{
			name:    "unknown_value",
			value:   "halt",
			wantErr: "onBreach",
		},
		{
			name:    "typo_warn",
			value:   "Warn",
			wantErr: "onBreach",
		},
		{
			name:    "typo_block",
			value:   "BLOCK",
			wantErr: "onBreach",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			yaml := withLimits(fmt.Sprintf(`
    github_pr:
      perHour: 10
      onBreach: %s
`, tc.value))

			p, err := profile.Parse([]byte(yaml))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			errs := profile.ValidateSemantic(p)
			found := false
			for _, e := range errs {
				if !e.IsWarning && strings.Contains(e.Path, tc.wantErr) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected semantic error containing path %q for onBreach=%q; got errs: %v",
					tc.wantErr, tc.value, errs)
			}
		})
	}
}

// TestSpecLimits_ZeroNegativeValues verifies that zero and negative limit values
// are rejected by semantic validation.
// TestSpecLimits_ZeroAllowed_NegativeRejected verifies the hard-deny semantic:
// a window value of 0 is a VALID limit (count > 0 trips on the very first
// attempt — a hard deny under onBreach:block/freeze, or a tripwire alert under
// the default warn). Negative values remain rejected (nonsensical).
func TestSpecLimits_ZeroAllowed_NegativeRejected(t *testing.T) {
	// Zero is now a valid limit for every window — no semantic error.
	t.Run("zero_lifetime_ok", func(t *testing.T) {
		p := buildProfileWithLimits()
		var zero int64 = 0
		p.Spec.Limits.GithubPR = &profile.ActionLimitSpec{Lifetime: &zero, OnBreach: "block"}
		assertLimitsSemanticNoError(t, p, "spec.limits.github_pr.lifetime")
	})
	t.Run("zero_perHour_ok", func(t *testing.T) {
		p := buildProfileWithLimits()
		var zero int64 = 0
		p.Spec.Limits.SlackPost = &profile.ActionLimitSpec{PerHour: &zero, OnBreach: "block"}
		assertLimitsSemanticNoError(t, p, "spec.limits.slack_post.perHour")
	})
	t.Run("zero_perDay_ok", func(t *testing.T) {
		p := buildProfileWithLimits()
		var zero int64 = 0
		p.Spec.Limits.EmailSend = &profile.ActionLimitSpec{PerDay: &zero, OnBreach: "block"}
		assertLimitsSemanticNoError(t, p, "spec.limits.email_send.perDay")
	})

	// Negative values are still rejected.
	t.Run("negative_lifetime", func(t *testing.T) {
		p := buildProfileWithLimits()
		var neg int64 = -1
		p.Spec.Limits.GithubPR = &profile.ActionLimitSpec{Lifetime: &neg}
		assertLimitsSemanticError(t, p, "spec.limits.github_pr.lifetime")
	})
	t.Run("negative_perHour", func(t *testing.T) {
		p := buildProfileWithLimits()
		var neg int64 = -5
		p.Spec.Limits.SlackPost = &profile.ActionLimitSpec{PerHour: &neg}
		assertLimitsSemanticError(t, p, "spec.limits.slack_post.perHour")
	})
	t.Run("negative_perDay", func(t *testing.T) {
		p := buildProfileWithLimits()
		var neg int64 = -1
		p.Spec.Limits.EmailSend = &profile.ActionLimitSpec{PerDay: &neg}
		assertLimitsSemanticError(t, p, "spec.limits.email_send.perDay")
	})
}

// TestSpecLimits_SchemaOnBreachEnum verifies that the JSON schema rejects unknown
// onBreach values (additionalProperties:false + enum enforcement).
func TestSpecLimits_SchemaOnBreachEnum(t *testing.T) {
	yaml := withLimits(`
    github_pr:
      perHour: 10
      onBreach: invalid_value
`)
	errs := profile.ValidateSchema([]byte(yaml))
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "onBreach") || strings.Contains(e.Path, "onBreach") ||
			strings.Contains(e.Message, "invalid_value") || strings.Contains(e.Message, "enum") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ValidateSchema: expected schema error for invalid onBreach value; got: %v", errs)
	}
}

// TestSpecLimits_SchemaMinimum verifies that the JSON schema ACCEPTS zero
// (hard-deny floor, minimum:0) but rejects negative window values.
func TestSpecLimits_SchemaMinimum(t *testing.T) {
	t.Run("zero_accepted", func(t *testing.T) {
		yaml := withLimits(`
    github_comment:
      perHour: 0
      onBreach: block
`)
		errs := profile.ValidateSchema([]byte(yaml))
		for _, e := range errs {
			if strings.Contains(e.Path, "perHour") || strings.Contains(e.Message, "minimum") {
				t.Errorf("ValidateSchema: perHour: 0 should be accepted (minimum:0); got: %s: %s", e.Path, e.Message)
			}
		}
	})
	t.Run("negative_rejected", func(t *testing.T) {
		yaml := withLimits(`
    github_comment:
      perHour: -1
`)
		errs := profile.ValidateSchema([]byte(yaml))
		if len(errs) == 0 {
			t.Error("ValidateSchema: expected schema error for perHour: -1 (minimum: 0), got none")
		}
	})
}

// TestSpecLimits_SchemaAdditionalProperties verifies that the JSON schema rejects
// unknown fields in the limits block (additionalProperties:false).
func TestSpecLimits_SchemaAdditionalProperties(t *testing.T) {
	yaml := withLimits(`
    github_pr:
      perHour: 5
      unknownField: true
`)
	errs := profile.ValidateSchema([]byte(yaml))
	if len(errs) == 0 {
		t.Error("ValidateSchema: expected schema error for unknown field in limits.github_pr, got none")
	}
}

// --- helpers ---

// buildProfileWithLimits returns a minimal parsed SandboxProfile with an empty
// LimitsSpec. Tests mutate the Limits field to inject specific scenarios.
func buildProfileWithLimits() *profile.SandboxProfile {
	p, _ := profile.Parse([]byte(minimalLimitsBase))
	if p == nil {
		panic("buildProfileWithLimits: parse returned nil")
	}
	p.Spec.Limits = &profile.LimitsSpec{}
	return p
}

// assertLimitsSemanticError runs ValidateSemantic and checks that at least one
// non-warning error has the given path.
func assertLimitsSemanticError(t *testing.T, p *profile.SandboxProfile, wantPath string) {
	t.Helper()
	errs := profile.ValidateSemantic(p)
	for _, e := range errs {
		if !e.IsWarning && e.Path == wantPath {
			return
		}
	}
	t.Errorf("expected semantic error at path %q; got: %v", wantPath, errs)
}

// assertLimitsSemanticNoError runs ValidateSemantic and fails if any non-warning
// error is reported at the given path (used to assert a value is now accepted).
func assertLimitsSemanticNoError(t *testing.T, p *profile.SandboxProfile, path string) {
	t.Helper()
	for _, e := range profile.ValidateSemantic(p) {
		if !e.IsWarning && e.Path == path {
			t.Errorf("unexpected semantic error at path %q: %s", path, e.Message)
		}
	}
}
