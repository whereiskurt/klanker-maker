package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// boolPtrTranscript is a local helper to create *bool values for transcript tests.
// (Avoids redeclaring boolPtr/boolPtrInbound which exist in sibling test files.)
func boolPtrTranscript(b bool) *bool { return &b }

// minimalTranscriptProfile builds the smallest valid SandboxProfile with the given CLISpec.
func minimalTranscriptProfile(cli *profile.CLISpec) *profile.SandboxProfile {
	return &profile.SandboxProfile{
		APIVersion: "klankermaker.ai/v1alpha1",
		Kind:       "SandboxProfile",
		Spec: profile.Spec{
			CLI: cli,
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
// notifySlackTranscriptEnabled=true without notifySlackEnabled=true produces
// an error containing "requires notifySlackEnabled".
func TestValidate_SlackTranscript_RequiresSlackEnabled(t *testing.T) {
	f := false
	p := minimalTranscriptProfile(&profile.CLISpec{
		NotifySlackEnabled:           &f, // outbound off
		NotifySlackPerSandbox:        true,
		NotifySlackTranscriptEnabled: true,
	})
	errs := profile.ValidateSemantic(p)
	if !containsTranscriptError(errs, "requires notifySlackEnabled") {
		t.Fatalf("expected error mentioning 'requires notifySlackEnabled', got %+v", errs)
	}
}

// TestValidate_SlackTranscript_RequiresPerSandbox verifies that setting
// notifySlackTranscriptEnabled=true without notifySlackPerSandbox=true produces
// an error containing "requires notifySlackPerSandbox".
func TestValidate_SlackTranscript_RequiresPerSandbox(t *testing.T) {
	tr := true
	p := minimalTranscriptProfile(&profile.CLISpec{
		NotifySlackEnabled:           &tr,
		NotifySlackPerSandbox:        false,
		NotifySlackTranscriptEnabled: true,
	})
	errs := profile.ValidateSemantic(p)
	if !containsTranscriptError(errs, "requires notifySlackPerSandbox") {
		t.Fatalf("expected error mentioning 'requires notifySlackPerSandbox', got %+v", errs)
	}
}

// TestValidate_SlackTranscript_IncompatibleWithChannelOverride verifies that setting
// notifySlackTranscriptEnabled=true alongside notifySlackChannelOverride produces
// an error containing "incompatible with notifySlackChannelOverride".
func TestValidate_SlackTranscript_IncompatibleWithChannelOverride(t *testing.T) {
	tr := true
	p := minimalTranscriptProfile(&profile.CLISpec{
		NotifySlackEnabled:           &tr,
		NotifySlackPerSandbox:        true,
		NotifySlackChannelOverride:   "C0123ABC",
		NotifySlackTranscriptEnabled: true,
	})
	errs := profile.ValidateSemantic(p)
	if !containsTranscriptError(errs, "incompatible with notifySlackChannelOverride") {
		t.Fatalf("expected incompatibility error, got %+v", errs)
	}
}

// TestValidate_SlackTranscript_AllPrereqsMetAccepted verifies that with all
// three prerequisites satisfied (transcript=true, slack=true, perSandbox=true,
// no override), no transcript-related validation error is emitted.
func TestValidate_SlackTranscript_AllPrereqsMetAccepted(t *testing.T) {
	tr := true
	p := minimalTranscriptProfile(&profile.CLISpec{
		NotifySlackEnabled:           &tr,
		NotifySlackPerSandbox:        true,
		NotifySlackTranscriptEnabled: true,
	})
	errs := profile.ValidateSemantic(p)
	for _, e := range errs {
		if e.IsWarning {
			continue
		}
		if strings.Contains(e.Message, "notifySlackTranscriptEnabled") {
			t.Fatalf("all prereqs met should produce no transcript error, got %+v", e)
		}
	}
}

// TestValidate_SlackTranscript_DefaultFalseHasNoImpact verifies that the default
// value of notifySlackTranscriptEnabled=false produces no transcript-specific
// errors regardless of other Slack settings (orthogonal to other Slack rules).
func TestValidate_SlackTranscript_DefaultFalseHasNoImpact(t *testing.T) {
	f := false
	p := minimalTranscriptProfile(&profile.CLISpec{
		NotifySlackEnabled:           &f,
		NotifySlackPerSandbox:        false,
		NotifySlackTranscriptEnabled: false, // default
	})
	errs := profile.ValidateSemantic(p)
	for _, e := range errs {
		if strings.Contains(e.Message, "notifySlackTranscriptEnabled") {
			t.Fatalf("default false should not produce transcript-specific errors, got %+v", e)
		}
	}
}
