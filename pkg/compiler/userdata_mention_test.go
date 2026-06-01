package compiler

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// boolPtr is a test helper that returns a pointer to the given bool.
func boolPtr(b bool) *bool { return &b }

// intPtr is a test helper that returns a pointer to the given int.
func intPtr(i int) *int { return &i }

// mentionProfile builds a SandboxProfile whose notification.slack block encodes
// the given (perSandbox, channelOverride, mentionOnly) mode. Phase 92: the
// mention-only resolution moved off cli.* onto notification.slack.*; the
// resolver now takes a *profile.SandboxProfile.
func mentionProfile(perSandbox bool, channelOverride string, mentionOnly *bool) *profile.SandboxProfile {
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{}
	p.Spec.Notification = &profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			PerSandbox:      boolPtr(perSandbox),
			ChannelOverride: channelOverride,
			Inbound:         &profile.NotificationSlackInboundSpec{MentionOnly: mentionOnly},
		},
	}
	return p
}

// TestResolveMentionOnly exercises the 9-case (Mode × override) truth table for
// resolveMentionOnly(*profile.SandboxProfile) plus the nil-notification defensive edge.
//
// Truth table (Mode 1=shared, Mode 2=per-sandbox, Mode 3=channel-override):
//
//	Mode 1 (shared):          override nil   → true  (polite)
//	Mode 1 (shared):          override &true → true  (forced polite)
//	Mode 1 (shared):          override &false→ false (forced chatty)
//	Mode 2 (per-sandbox):     override nil   → false (every-message)
//	Mode 2 (per-sandbox):     override &true → true  (forced polite)
//	Mode 2 (per-sandbox):     override &false→ false (forced chatty)
//	Mode 3 (channel-override):override nil   → true  (polite)
//	Mode 3 (channel-override):override &true → true  (forced polite)
//	Mode 3 (channel-override):override &false→ false (forced chatty)
//	nil notification:                       → true (shared-mode default)
func TestResolveMentionOnly(t *testing.T) {
	tests := []struct {
		name           string
		profile        *profile.SandboxProfile
		expectedResult bool
	}{
		// Mode 1: shared channel (perSandbox=false, channelOverride="")
		{"Mode1_shared_override_nil_want_true", mentionProfile(false, "", nil), true},
		{"Mode1_shared_override_true_want_true", mentionProfile(false, "", boolPtr(true)), true},
		{"Mode1_shared_override_false_want_false", mentionProfile(false, "", boolPtr(false)), false},
		// Mode 2: per-sandbox (perSandbox=true, channelOverride="")
		{"Mode2_perSandbox_override_nil_want_false", mentionProfile(true, "", nil), false},
		{"Mode2_perSandbox_override_true_want_true", mentionProfile(true, "", boolPtr(true)), true},
		{"Mode2_perSandbox_override_false_want_false", mentionProfile(true, "", boolPtr(false)), false},
		// Mode 3: channel-override (perSandbox=false, channelOverride="C123ABC")
		{"Mode3_channelOverride_override_nil_want_true", mentionProfile(false, "C123ABC", nil), true},
		{"Mode3_channelOverride_override_true_want_true", mentionProfile(false, "C123ABC", boolPtr(true)), true},
		{"Mode3_channelOverride_override_false_want_false", mentionProfile(false, "C123ABC", boolPtr(false)), false},
		// Edge: nil notification → shared-mode default true
		{"nilNotification_want_true", baseProfile(), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveMentionOnly(tc.profile)
			if got != tc.expectedResult {
				t.Errorf("resolveMentionOnly(%s) = %v, want %v", tc.name, got, tc.expectedResult)
			}
		})
	}
}

// TestMentionOnlyCompiler verifies that KM_SLACK_MENTION_ONLY is emitted into the
// generated userdata when notification.slack.enabled=&true, and absent when Slack
// is disabled.
//
// Sampled cases (full 9-case coverage is in TestResolveMentionOnly):
//   - Mode 1 shared, Slack enabled, override nil → value "true"
//   - Mode 2 per-sandbox, Slack enabled, override nil → value "false"
//   - Mode 2 per-sandbox, Slack enabled, override &true → value "true"
//   - Slack disabled (nil) → KEY ABSENT
//   - Slack explicitly false → KEY ABSENT
func TestMentionOnlyCompiler(t *testing.T) {
	// slackProfile builds a profile with notification.slack.{enabled,perSandbox,
	// inbound.mentionOnly} for the compiler-level assertion.
	slackProfile := func(enabled *bool, perSandbox bool, mentionOnly *bool) *profile.SandboxProfile {
		p := baseProfile()
		p.Spec.CLI = &profile.CLISpec{}
		p.Spec.Notification = &profile.NotificationSpec{
			Slack: &profile.NotificationSlackSpec{
				Enabled:    enabled,
				PerSandbox: boolPtr(perSandbox),
				Inbound:    &profile.NotificationSlackInboundSpec{MentionOnly: mentionOnly},
			},
		}
		return p
	}

	tests := []struct {
		name        string
		profile     *profile.SandboxProfile
		wantPresent bool
		wantValue   string // only checked when wantPresent=true
	}{
		{"Mode1_shared_slackEnabled_overrideNil_want_true", slackProfile(boolPtr(true), false, nil), true, "true"},
		{"Mode2_perSandbox_slackEnabled_overrideNil_want_false", slackProfile(boolPtr(true), true, nil), true, "false"},
		{"Mode2_perSandbox_slackEnabled_overrideTrue_want_true", slackProfile(boolPtr(true), true, boolPtr(true)), true, "true"},
		{"slackEnabledNil_keyAbsent", slackProfile(nil, false, nil), false, ""},
		{"slackEnabledFalse_keyAbsent", slackProfile(boolPtr(false), false, nil), false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ud, err := generateUserData(tc.profile, "sb-mention-test", nil, "my-bucket", false, nil)
			if err != nil {
				t.Fatalf("generateUserData: %v", err)
			}

			if tc.wantPresent {
				want := `KM_SLACK_MENTION_ONLY="` + tc.wantValue + `"`
				if !containsString(ud, want) {
					t.Errorf("expected %q in userdata, not found\nuserdata excerpt around KM_SLACK: %s",
						want, extractSlackLines(ud))
				}
			} else {
				if containsString(ud, "KM_SLACK_MENTION_ONLY=") {
					t.Errorf("expected KM_SLACK_MENTION_ONLY to be ABSENT from userdata, but found it")
				}
			}
		})
	}
}

// containsString is a local helper to avoid importing "strings" duplication.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		findSubstr(s, substr))
}

// findSubstr scans s for substr using a simple linear scan.
func findSubstr(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// extractSlackLines returns a small excerpt of lines containing "SLACK" for
// diagnostic output in test failures.
func extractSlackLines(ud string) string {
	var buf []byte
	start := 0
	for i, c := range ud {
		if c == '\n' {
			line := ud[start:i]
			if findSubstr(line, "SLACK") {
				buf = append(buf, []byte(line+"\n")...)
			}
			start = i + 1
		}
		_ = i
	}
	if len(buf) == 0 {
		return "(no SLACK lines found)"
	}
	return string(buf)
}

// TestResolveReactAlways — Phase 91.4 resolver. Symmetric with TestResolveMentionOnly
// but simpler: explicit override wins; nil default = true (chatty-reactor).
// Phase 92: re-homed to notification.slack.inbound.reactAlways.
func TestResolveReactAlways(t *testing.T) {
	// reactProfile builds a profile with notification.slack.inbound.reactAlways set.
	reactProfile := func(reactAlways *bool) *profile.SandboxProfile {
		p := baseProfile()
		p.Spec.CLI = &profile.CLISpec{}
		p.Spec.Notification = &profile.NotificationSpec{
			Slack: &profile.NotificationSlackSpec{
				Inbound: &profile.NotificationSlackInboundSpec{ReactAlways: reactAlways},
			},
		}
		return p
	}
	tests := []struct {
		name    string
		profile *profile.SandboxProfile
		want    bool
	}{
		{"nil notification → true (defensive)", baseProfile(), true},
		{"override nil → true (chatty default)", reactProfile(nil), true},
		{"override &true → true", reactProfile(boolPtr(true)), true},
		{"override &false → false (first-only)", reactProfile(boolPtr(false)), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveReactAlways(tc.profile); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}
