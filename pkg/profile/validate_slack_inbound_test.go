package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// boolPtrInbound is a local helper to create *bool values for inbound tests.
// (Avoids redeclaring boolPtr which is already in validate_test.go.)
func boolPtrInbound(b bool) *bool { return &b }

// minimalInboundProfile builds the smallest valid SandboxProfile with the given CLISpec.
func minimalInboundProfile(cli *profile.CLISpec) *profile.SandboxProfile {
	return &profile.SandboxProfile{
		APIVersion: "klankermaker.ai/v1alpha1",
		Kind:       "SandboxProfile",
		Spec: profile.Spec{
			CLI: cli,
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
// notifySlackInboundEnabled=true without notifySlackEnabled=true produces
// an error containing "requires notifySlackEnabled".
func TestValidate_SlackInbound_RequiresSlackEnabled(t *testing.T) {
	f := false
	p := minimalInboundProfile(&profile.CLISpec{
		NotifySlackEnabled:        &f, // outbound off
		NotifySlackPerSandbox:     true,
		NotifySlackInboundEnabled: true,
	})
	errs := profile.ValidateSemantic(p)
	if !containsInboundError(errs, "requires notifySlackEnabled") {
		t.Fatalf("expected error mentioning 'requires notifySlackEnabled', got %+v", errs)
	}
}

// TestValidate_SlackInbound_RequiresPerSandbox verifies that setting
// notifySlackInboundEnabled=true without notifySlackPerSandbox=true produces
// an error containing "requires notifySlackPerSandbox".
func TestValidate_SlackInbound_RequiresPerSandbox(t *testing.T) {
	tr := true
	p := minimalInboundProfile(&profile.CLISpec{
		NotifySlackEnabled:        &tr,
		NotifySlackPerSandbox:     false,
		NotifySlackInboundEnabled: true,
	})
	errs := profile.ValidateSemantic(p)
	if !containsInboundError(errs, "requires notifySlackPerSandbox") {
		t.Fatalf("expected error mentioning 'requires notifySlackPerSandbox', got %+v", errs)
	}
}

// TestValidate_SlackInbound_RejectsChannelOverride verifies that setting
// notifySlackInboundEnabled=true alongside notifySlackChannelOverride produces
// an error containing "incompatible with notifySlackChannelOverride".
func TestValidate_SlackInbound_RejectsChannelOverride(t *testing.T) {
	tr := true
	p := minimalInboundProfile(&profile.CLISpec{
		NotifySlackEnabled:         &tr,
		NotifySlackPerSandbox:      true,
		NotifySlackChannelOverride: "C0123ABC",
		NotifySlackInboundEnabled:  true,
	})
	errs := profile.ValidateSemantic(p)
	if !containsInboundError(errs, "incompatible with notifySlackChannelOverride") {
		t.Fatalf("expected incompatibility error, got %+v", errs)
	}
}

// TestValidate_SlackInbound_DefaultFalseHasNoImpact verifies that the default
// value of notifySlackInboundEnabled=false produces no inbound-specific errors.
func TestValidate_SlackInbound_DefaultFalseHasNoImpact(t *testing.T) {
	f := false
	p := minimalInboundProfile(&profile.CLISpec{
		NotifySlackEnabled:        &f,
		NotifySlackPerSandbox:     false,
		NotifySlackInboundEnabled: false, // default
	})
	errs := profile.ValidateSemantic(p)
	for _, e := range errs {
		if strings.Contains(e.Message, "notifySlackInboundEnabled") {
			t.Fatalf("default false should not produce inbound-specific errors, got %+v", e)
		}
	}
}
