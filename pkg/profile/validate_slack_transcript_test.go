package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// boolPtrTranscript is a local helper to create *bool values for transcript tests.
// (Avoids redeclaring boolPtr/boolPtrInbound which exist in sibling test files.)
func boolPtrTranscript(b bool) *bool { return &b }

// minimalTranscriptProfile builds the smallest valid SandboxProfile with the given
// NotificationSpec.
// Phase 92 (Wave 2): migrated from CLISpec to the structured notification block.
func minimalTranscriptProfile(n *profile.NotificationSpec) *profile.SandboxProfile {
	return &profile.SandboxProfile{
		APIVersion: "klankermaker.ai/v1alpha2",
		Kind:       "SandboxProfile",
		Spec: profile.Spec{
			Notification: n,
		},
	}
}

// containsTranscriptError returns true if any non-warning error message contains substr.
func containsTranscriptError(errs []profile.ValidationError, substr string) bool {
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

// TestValidate_SlackTranscript_RequiresSlackEnabled verifies that setting
// transcript.enabled=true without slack.enabled=true produces an error containing
// "requires notification.slack.enabled".
func TestValidate_SlackTranscript_RequiresSlackEnabled(t *testing.T) {
	p := minimalTranscriptProfile(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtrTranscript(false), // outbound off
			PerSandbox: boolPtrTranscript(true),
			Transcript: &profile.NotificationSlackTranscriptSpec{Enabled: boolPtrTranscript(true)},
		},
	})
	errs := profile.ValidateSemantic(p)
	if !containsTranscriptError(errs, "requires notification.slack.enabled") {
		t.Fatalf("expected error mentioning 'requires notification.slack.enabled', got %+v", errs)
	}
}

// TestValidate_SlackTranscript_RequiresPerSandbox verifies that setting
// transcript.enabled=true without slack.perSandbox=true produces an error containing
// "requires notification.slack.perSandbox".
func TestValidate_SlackTranscript_RequiresPerSandbox(t *testing.T) {
	p := minimalTranscriptProfile(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtrTranscript(true),
			PerSandbox: boolPtrTranscript(false),
			Transcript: &profile.NotificationSlackTranscriptSpec{Enabled: boolPtrTranscript(true)},
		},
	})
	errs := profile.ValidateSemantic(p)
	if !containsTranscriptError(errs, "requires notification.slack.perSandbox") {
		t.Fatalf("expected error mentioning 'requires notification.slack.perSandbox', got %+v", errs)
	}
}

// TestValidate_SlackTranscript_IncompatibleWithChannelOverride verifies that setting
// transcript.enabled=true alongside slack.channelOverride produces an error containing
// "incompatible with notification.slack.channelOverride".
func TestValidate_SlackTranscript_IncompatibleWithChannelOverride(t *testing.T) {
	p := minimalTranscriptProfile(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:         boolPtrTranscript(true),
			PerSandbox:      boolPtrTranscript(true),
			ChannelOverride: "C0123ABC",
			Transcript:      &profile.NotificationSlackTranscriptSpec{Enabled: boolPtrTranscript(true)},
		},
	})
	errs := profile.ValidateSemantic(p)
	if !containsTranscriptError(errs, "incompatible with notification.slack.channelOverride") {
		t.Fatalf("expected incompatibility error, got %+v", errs)
	}
}

// TestValidate_SlackTranscript_AllPrereqsMetAccepted verifies that with all
// three prerequisites satisfied (transcript=true, slack=true, perSandbox=true,
// no override), no transcript-related validation error is emitted.
func TestValidate_SlackTranscript_AllPrereqsMetAccepted(t *testing.T) {
	p := minimalTranscriptProfile(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtrTranscript(true),
			PerSandbox: boolPtrTranscript(true),
			Transcript: &profile.NotificationSlackTranscriptSpec{Enabled: boolPtrTranscript(true)},
		},
	})
	errs := profile.ValidateSemantic(p)
	for _, e := range errs {
		if e.IsWarning {
			continue
		}
		if strings.Contains(e.Message, "transcript") {
			t.Fatalf("all prereqs met should produce no transcript error, got %+v", e)
		}
	}
}

// TestValidate_SlackTranscript_DefaultFalseHasNoImpact verifies that the default
// value of transcript.enabled=false produces no transcript-specific errors
// regardless of other Slack settings (orthogonal to other Slack rules).
func TestValidate_SlackTranscript_DefaultFalseHasNoImpact(t *testing.T) {
	p := minimalTranscriptProfile(&profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtrTranscript(false),
			PerSandbox: boolPtrTranscript(false),
			Transcript: &profile.NotificationSlackTranscriptSpec{Enabled: boolPtrTranscript(false)}, // default
		},
	})
	errs := profile.ValidateSemantic(p)
	for _, e := range errs {
		if strings.Contains(e.Message, "transcript") {
			t.Fatalf("default false should not produce transcript-specific errors, got %+v", e)
		}
	}
}
