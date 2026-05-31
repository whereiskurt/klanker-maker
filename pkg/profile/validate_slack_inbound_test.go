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
