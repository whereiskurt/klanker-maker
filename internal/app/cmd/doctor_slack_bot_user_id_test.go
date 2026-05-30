package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAnyProfileMentionOnly verifies the helper that gates the bot-user-id
// doctor check (Phase 91).
func TestAnyProfileMentionOnly(t *testing.T) {
	// Helper to write a profile yaml into a temp dir and return the dir path.
	writeProfile := func(t *testing.T, content string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Run("empty dir → false", func(t *testing.T) {
		dir := t.TempDir()
		if anyProfileMentionOnly([]string{dir}) {
			t.Error("expected false for empty dir")
		}
	})

	t.Run("non-existent dir → false", func(t *testing.T) {
		if anyProfileMentionOnly([]string{"/nonexistent-dir-phase91"}) {
			t.Error("expected false for missing dir")
		}
	})

	t.Run("profile without notifySlackEnabled → false", func(t *testing.T) {
		dir := writeProfile(t, `
spec:
  cli:
    notifySlackEnabled: false
`)
		if anyProfileMentionOnly([]string{dir}) {
			t.Error("expected false when notifySlackEnabled is false")
		}
	})

	t.Run("shared channel (Mode 1) + notifySlackEnabled → true", func(t *testing.T) {
		dir := writeProfile(t, `
spec:
  cli:
    notifySlackEnabled: true
`)
		if !anyProfileMentionOnly([]string{dir}) {
			t.Error("expected true for Mode 1 profile (shared, default mention-only)")
		}
	})

	t.Run("per-sandbox (Mode 2) → false", func(t *testing.T) {
		dir := writeProfile(t, `
spec:
  cli:
    notifySlackEnabled: true
    notifySlackPerSandbox: true
`)
		if anyProfileMentionOnly([]string{dir}) {
			t.Error("expected false for Mode 2 profile (per-sandbox, default every-message)")
		}
	})

	t.Run("explicit mentionOnly: true on Mode 2 → true", func(t *testing.T) {
		dir := writeProfile(t, `
spec:
  cli:
    notifySlackEnabled: true
    notifySlackPerSandbox: true
    notifySlackInboundMentionOnly: true
`)
		if !anyProfileMentionOnly([]string{dir}) {
			t.Error("expected true when explicit override is true")
		}
	})

	t.Run("explicit mentionOnly: false on Mode 1 → false", func(t *testing.T) {
		dir := writeProfile(t, `
spec:
  cli:
    notifySlackEnabled: true
    notifySlackInboundMentionOnly: false
`)
		if anyProfileMentionOnly([]string{dir}) {
			t.Error("expected false when explicit override is false")
		}
	})
}

// TestCheckSlackBotUserIDCached exercises all four return paths of
// checkSlackBotUserIDCached (POL-10, Phase 91):
//
//   - getUID == nil      → SKIPPED (no mention-only profile or Slack not configured)
//   - getUID returns uid → OK (cache populated)
//   - getUID returns ""  → WARN with remediation pointing to km slack init --force
//   - getUID returns err → WARN (transient SSM failure; do not fail doctor)
func TestCheckSlackBotUserIDCached(t *testing.T) {
	tests := []struct {
		name       string
		getUID     func(context.Context) (string, error)
		wantStatus CheckStatus
		wantMsgSub string // substring that must appear in Message
		wantRemSub string // substring that must appear in Remediation; "" = no check
	}{
		{
			name:       "nil getUID → SKIPPED",
			getUID:     nil,
			wantStatus: CheckSkipped,
			wantMsgSub: "no local profile",
		},
		{
			name: "populated → OK",
			getUID: func(context.Context) (string, error) {
				return "UBOT123", nil
			},
			wantStatus: CheckOK,
			wantMsgSub: "UBOT123",
		},
		{
			name: "empty → WARN with remediation",
			getUID: func(context.Context) (string, error) {
				return "", nil
			},
			wantStatus: CheckWarn,
			wantMsgSub: "not cached",
			wantRemSub: "km slack init --force",
		},
		{
			name: "error → WARN",
			getUID: func(context.Context) (string, error) {
				return "", errors.New("ssm down")
			},
			wantStatus: CheckWarn,
			wantMsgSub: "ssm down",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkSlackBotUserIDCached(context.Background(), "/km/", tc.getUID)
			if got.Status != tc.wantStatus {
				t.Errorf("status: got %v, want %v", got.Status, tc.wantStatus)
			}
			if !strings.Contains(got.Message, tc.wantMsgSub) {
				t.Errorf("message: got %q, want substring %q", got.Message, tc.wantMsgSub)
			}
			if tc.wantRemSub != "" && !strings.Contains(got.Remediation, tc.wantRemSub) {
				t.Errorf("remediation: got %q, want substring %q", got.Remediation, tc.wantRemSub)
			}
		})
	}
}
