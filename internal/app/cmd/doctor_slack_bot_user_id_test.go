package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
)

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
