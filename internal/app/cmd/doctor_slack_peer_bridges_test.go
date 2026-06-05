package cmd

import (
	"strings"
	"testing"
)

// TestCheckSlackPeerBridges exercises all return paths of checkSlackPeerBridges
// (Phase 95 — federated bridge relay doctor check).
//
//   - Empty/nil peerBridges → SKIPPED (federation not configured).
//   - Malformed URL entry   → WARN  (operator misconfiguration).
//   - Self-loop             → WARN  (peer URL equals own bridge URL).
//   - All valid + distinct  → OK.
func TestCheckSlackPeerBridges(t *testing.T) {
	const own = "https://abc123.lambda-url.us-east-1.on.aws/events"

	tests := []struct {
		name        string
		peerBridges []string
		ownBridgeURL string
		wantStatus  CheckStatus
		wantMsgSub  string // substring that must appear in Message; "" = no check
		wantRemSub  string // substring that must appear in Remediation; "" = no check
	}{
		{
			name:        "nil peerBridges → SKIPPED",
			peerBridges:  nil,
			ownBridgeURL: own,
			wantStatus:  CheckSkipped,
			wantMsgSub:  "federation off",
		},
		{
			name:        "empty peerBridges → SKIPPED",
			peerBridges:  []string{},
			ownBridgeURL: own,
			wantStatus:  CheckSkipped,
			wantMsgSub:  "federation off",
		},
		{
			name:         "malformed URL → WARN",
			peerBridges:  []string{"::::not a url"},
			ownBridgeURL: own,
			wantStatus:   CheckWarn,
			wantMsgSub:   "malformed",
			wantRemSub:   "peer_bridges",
		},
		{
			name:         "self-loop → WARN",
			peerBridges:  []string{own},
			ownBridgeURL: own,
			wantStatus:   CheckWarn,
			wantMsgSub:   "self-loop",
			wantRemSub:   "peer_bridges",
		},
		{
			name: "valid distinct peers → OK",
			peerBridges: []string{
				"https://def456.lambda-url.us-east-1.on.aws/events",
				"https://ghi789.lambda-url.us-east-1.on.aws/events",
			},
			ownBridgeURL: own,
			wantStatus:   CheckOK,
		},
		{
			name: "valid peers + ownBridgeURL empty → OK (no self-loop possible)",
			peerBridges: []string{
				"https://def456.lambda-url.us-east-1.on.aws/events",
			},
			ownBridgeURL: "",
			wantStatus:   CheckOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkSlackPeerBridges(tc.peerBridges, tc.ownBridgeURL)
			if got.Status != tc.wantStatus {
				t.Errorf("status: got %v, want %v (message: %q)", got.Status, tc.wantStatus, got.Message)
			}
			if tc.wantMsgSub != "" && !strings.Contains(got.Message, tc.wantMsgSub) {
				t.Errorf("message: got %q, want substring %q", got.Message, tc.wantMsgSub)
			}
			if tc.wantRemSub != "" && !strings.Contains(got.Remediation, tc.wantRemSub) {
				t.Errorf("remediation: got %q, want substring %q", got.Remediation, tc.wantRemSub)
			}
		})
	}
}
