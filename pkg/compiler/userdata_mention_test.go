package compiler

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// boolPtr is a test helper that returns a pointer to the given bool.
func boolPtr(b bool) *bool { return &b }

// TestResolveMentionOnly exercises the 9-case (Mode × override) truth table for
// resolveMentionOnly(*profile.CLISpec) plus the nil-cli defensive edge.
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
//	nil cli:                               → false (defensive)
func TestResolveMentionOnly(t *testing.T) {
	tests := []struct {
		name           string
		cli            *profile.CLISpec
		expectedResult bool
	}{
		// Mode 1: shared channel (NotifySlackPerSandbox=false, ChannelOverride="")
		{
			name: "Mode1_shared_override_nil_want_true",
			cli: &profile.CLISpec{
				NotifySlackPerSandbox:         false,
				NotifySlackChannelOverride:     "",
				NotifySlackInboundMentionOnly:  nil,
			},
			expectedResult: true,
		},
		{
			name: "Mode1_shared_override_true_want_true",
			cli: &profile.CLISpec{
				NotifySlackPerSandbox:         false,
				NotifySlackChannelOverride:     "",
				NotifySlackInboundMentionOnly:  boolPtr(true),
			},
			expectedResult: true,
		},
		{
			name: "Mode1_shared_override_false_want_false",
			cli: &profile.CLISpec{
				NotifySlackPerSandbox:         false,
				NotifySlackChannelOverride:     "",
				NotifySlackInboundMentionOnly:  boolPtr(false),
			},
			expectedResult: false,
		},
		// Mode 2: per-sandbox (NotifySlackPerSandbox=true, ChannelOverride="")
		{
			name: "Mode2_perSandbox_override_nil_want_false",
			cli: &profile.CLISpec{
				NotifySlackPerSandbox:         true,
				NotifySlackChannelOverride:     "",
				NotifySlackInboundMentionOnly:  nil,
			},
			expectedResult: false,
		},
		{
			name: "Mode2_perSandbox_override_true_want_true",
			cli: &profile.CLISpec{
				NotifySlackPerSandbox:         true,
				NotifySlackChannelOverride:     "",
				NotifySlackInboundMentionOnly:  boolPtr(true),
			},
			expectedResult: true,
		},
		{
			name: "Mode2_perSandbox_override_false_want_false",
			cli: &profile.CLISpec{
				NotifySlackPerSandbox:         true,
				NotifySlackChannelOverride:     "",
				NotifySlackInboundMentionOnly:  boolPtr(false),
			},
			expectedResult: false,
		},
		// Mode 3: channel-override (NotifySlackPerSandbox=false, ChannelOverride="C123ABC")
		{
			name: "Mode3_channelOverride_override_nil_want_true",
			cli: &profile.CLISpec{
				NotifySlackPerSandbox:         false,
				NotifySlackChannelOverride:     "C123ABC",
				NotifySlackInboundMentionOnly:  nil,
			},
			expectedResult: true,
		},
		{
			name: "Mode3_channelOverride_override_true_want_true",
			cli: &profile.CLISpec{
				NotifySlackPerSandbox:         false,
				NotifySlackChannelOverride:     "C123ABC",
				NotifySlackInboundMentionOnly:  boolPtr(true),
			},
			expectedResult: true,
		},
		{
			name: "Mode3_channelOverride_override_false_want_false",
			cli: &profile.CLISpec{
				NotifySlackPerSandbox:         false,
				NotifySlackChannelOverride:     "C123ABC",
				NotifySlackInboundMentionOnly:  boolPtr(false),
			},
			expectedResult: false,
		},
		// Edge: nil cli → defensive false
		{
			name:           "nilCLI_want_false",
			cli:            nil,
			expectedResult: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveMentionOnly(tc.cli)
			if got != tc.expectedResult {
				t.Errorf("resolveMentionOnly(%+v) = %v, want %v", tc.cli, got, tc.expectedResult)
			}
		})
	}
}

// TestMentionOnlyCompiler verifies that KM_SLACK_MENTION_ONLY is emitted into the
// generated userdata when NotifySlackEnabled=&true, and absent when Slack is disabled.
//
// Sampled cases (full 9-case coverage is in TestResolveMentionOnly):
//   - Mode 1 shared, Slack enabled, override nil → value "true"
//   - Mode 2 per-sandbox, Slack enabled, override nil → value "false"
//   - Mode 2 per-sandbox, Slack enabled, override &true → value "true"
//   - Slack disabled (nil) → KEY ABSENT
//   - Slack explicitly false → KEY ABSENT
func TestMentionOnlyCompiler(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name        string
		cli         *profile.CLISpec
		wantPresent bool
		wantValue   string // only checked when wantPresent=true
	}{
		{
			name: "Mode1_shared_slackEnabled_overrideNil_want_true",
			cli: &profile.CLISpec{
				NotifySlackEnabled:            &trueVal,
				NotifySlackPerSandbox:         false,
				NotifySlackChannelOverride:     "",
				NotifySlackInboundMentionOnly:  nil,
			},
			wantPresent: true,
			wantValue:   "true",
		},
		{
			name: "Mode2_perSandbox_slackEnabled_overrideNil_want_false",
			cli: &profile.CLISpec{
				NotifySlackEnabled:            &trueVal,
				NotifySlackPerSandbox:         true,
				NotifySlackChannelOverride:     "",
				NotifySlackInboundMentionOnly:  nil,
			},
			wantPresent: true,
			wantValue:   "false",
		},
		{
			name: "Mode2_perSandbox_slackEnabled_overrideTrue_want_true",
			cli: &profile.CLISpec{
				NotifySlackEnabled:            &trueVal,
				NotifySlackPerSandbox:         true,
				NotifySlackChannelOverride:     "",
				NotifySlackInboundMentionOnly:  boolPtr(true),
			},
			wantPresent: true,
			wantValue:   "true",
		},
		{
			name: "slackEnabledNil_keyAbsent",
			cli: &profile.CLISpec{
				NotifySlackEnabled:            nil,
				NotifySlackPerSandbox:         false,
				NotifySlackChannelOverride:     "",
				NotifySlackInboundMentionOnly:  nil,
			},
			wantPresent: false,
		},
		{
			name: "slackEnabledFalse_keyAbsent",
			cli: &profile.CLISpec{
				NotifySlackEnabled:            &falseVal,
				NotifySlackPerSandbox:         false,
				NotifySlackChannelOverride:     "",
				NotifySlackInboundMentionOnly:  nil,
			},
			wantPresent: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := baseProfile()
			p.Spec.CLI = tc.cli

			ud, err := generateUserData(p, "sb-mention-test", nil, "my-bucket", false, nil)
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
func TestResolveReactAlways(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	tests := []struct {
		name string
		cli  *profile.CLISpec
		want bool
	}{
		{"nil cli → true (defensive)", nil, true},
		{"override nil → true (chatty default)", &profile.CLISpec{}, true},
		{"override &true → true", &profile.CLISpec{NotifySlackInboundReactAlways: boolPtr(true)}, true},
		{"override &false → false (first-only)", &profile.CLISpec{NotifySlackInboundReactAlways: boolPtr(false)}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveReactAlways(tc.cli); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}
