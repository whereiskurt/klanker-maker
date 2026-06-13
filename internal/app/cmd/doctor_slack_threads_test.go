// Package cmd — doctor_slack_threads_test.go
// Phase 110 Plan 06 — tests for two dead-channel doctor checks:
//   - checkSlackThreadDeadChannels: scan km-slack-threads, WARN on channel_not_found
//   - checkSlackChannelDeadAlias: scan km-slack-channels, WARN on channel_not_found
//
// Both checks SKIP when no Slack checker is provided (no bot token).
// All tests use lightweight local fakes (no real AWS calls).
package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// =============================================================================
// Fake DDB scan client for doctor checks
// =============================================================================

// fakeDDBScanClient implements a minimal Scan-only interface for the dead-channel
// doctor checks. Returns the configured items on the first call; subsequent calls
// return an empty page (no truncation).
type fakeDDBScanClient struct {
	items   []map[string]dynamodbtypes.AttributeValue
	scanErr error
	calls   int
}

func (f *fakeDDBScanClient) Scan(_ context.Context, _ *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	f.calls++
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	return &dynamodb.ScanOutput{Items: f.items}, nil
}

// =============================================================================
// TestCheckSlackThreadDeadChannels
// =============================================================================

// TestCheckSlackThreadDeadChannels exercises the five meaningful branches of
// checkSlackThreadDeadChannels:
//
//  1. nil checker (no bot token) → SKIPPED
//  2. scan error → WARN (transient AWS failure)
//  3. single thread row; channel is alive → OK
//  4. single thread row; channel returns channel_not_found → WARN with remediation
//  5. mixed rows with one dead channel → WARN naming the dead channel_id
func TestCheckSlackThreadDeadChannels(t *testing.T) {
	aliveID := "CALIVE001"
	deadID := "CDEAD001"

	mkRow := func(channelID string) map[string]dynamodbtypes.AttributeValue {
		return map[string]dynamodbtypes.AttributeValue{
			"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: channelID},
			"thread_ts":  &dynamodbtypes.AttributeValueMemberS{Value: "1234.5678"},
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "sb-test-01"},
		}
	}

	tests := []struct {
		name        string
		checker     SlackChannelChecker
		items       []map[string]dynamodbtypes.AttributeValue
		scanErr     error
		wantStatus  CheckStatus
		wantMsgSub  string
		wantRemSub  string
	}{
		{
			name:       "nil checker → SKIPPED",
			checker:    nil,
			wantStatus: CheckSkipped,
			wantMsgSub: "Slack",
		},
		{
			name:       "scan error → WARN",
			checker:    &fakeSlackChannelChecker{},
			items:      nil,
			scanErr:    errors.New("DynamoDB unavailable"),
			wantStatus: CheckWarn,
			wantMsgSub: "DynamoDB unavailable",
		},
		{
			name:       "all channels alive → OK",
			checker:    &fakeSlackChannelChecker{dead: map[string]bool{aliveID: false}},
			items:      []map[string]dynamodbtypes.AttributeValue{mkRow(aliveID)},
			wantStatus: CheckOK,
			wantMsgSub: "no dead channels",
		},
		{
			name:       "dead channel → WARN with remediation",
			checker:    &fakeSlackChannelChecker{dead: map[string]bool{deadID: true}},
			items:      []map[string]dynamodbtypes.AttributeValue{mkRow(deadID)},
			wantStatus: CheckWarn,
			wantMsgSub: deadID,
			wantRemSub: "km slack prune-threads",
		},
		{
			name:    "mixed rows → WARN names dead channel",
			checker: &fakeSlackChannelChecker{dead: map[string]bool{deadID: true}},
			items: []map[string]dynamodbtypes.AttributeValue{
				mkRow(aliveID),
				mkRow(deadID),
				mkRow(aliveID), // duplicate alive (deduped)
			},
			wantStatus: CheckWarn,
			wantMsgSub: deadID,
			wantRemSub: "km slack prune-threads",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ddb DoctorDDBScanAPI
			if tc.checker != nil {
				fake := &fakeDDBScanClient{items: tc.items, scanErr: tc.scanErr}
				ddb = fake
			}
			got := checkSlackThreadDeadChannels(context.Background(), ddb, "km-slack-threads", tc.checker)
			if got.Status != tc.wantStatus {
				t.Errorf("Status: got %v, want %v (msg=%q)", got.Status, tc.wantStatus, got.Message)
			}
			if tc.wantMsgSub != "" && !strings.Contains(got.Message, tc.wantMsgSub) {
				t.Errorf("Message: got %q, want substring %q", got.Message, tc.wantMsgSub)
			}
			if tc.wantRemSub != "" && !strings.Contains(got.Remediation, tc.wantRemSub) {
				t.Errorf("Remediation: got %q, want substring %q", got.Remediation, tc.wantRemSub)
			}
		})
	}
}

// =============================================================================
// TestCheckSlackChannelDeadAlias
// =============================================================================

// TestCheckSlackChannelDeadAlias exercises the five meaningful branches of
// checkSlackChannelDeadAlias:
//
//  1. nil checker (no bot token) → SKIPPED
//  2. scan error → WARN (transient AWS failure)
//  3. single alias row; channel is alive → OK
//  4. single alias row; channel returns channel_not_found → WARN with remediation
//  5. no alias rows → OK ("no alias rows")
func TestCheckSlackChannelDeadAlias(t *testing.T) {
	aliveID := "CALIVE002"
	deadID := "CDEAD002"
	deadAlias := "my-dead-sandbox"

	mkAliasRow := func(alias, channelID string) map[string]dynamodbtypes.AttributeValue {
		return map[string]dynamodbtypes.AttributeValue{
			"alias":      &dynamodbtypes.AttributeValueMemberS{Value: alias},
			"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: channelID},
		}
	}

	tests := []struct {
		name       string
		checker    SlackChannelChecker
		items      []map[string]dynamodbtypes.AttributeValue
		scanErr    error
		wantStatus CheckStatus
		wantMsgSub string
		wantRemSub string
	}{
		{
			name:       "nil checker → SKIPPED",
			checker:    nil,
			wantStatus: CheckSkipped,
			wantMsgSub: "Slack",
		},
		{
			name:       "scan error → WARN",
			checker:    &fakeSlackChannelChecker{},
			items:      nil,
			scanErr:    errors.New("access denied"),
			wantStatus: CheckWarn,
			wantMsgSub: "access denied",
		},
		{
			name:       "no alias rows → OK",
			checker:    &fakeSlackChannelChecker{},
			items:      nil,
			wantStatus: CheckOK,
			wantMsgSub: "no alias rows",
		},
		{
			name:       "alive alias → OK",
			checker:    &fakeSlackChannelChecker{dead: map[string]bool{aliveID: false}},
			items:      []map[string]dynamodbtypes.AttributeValue{mkAliasRow("my-alive-sandbox", aliveID)},
			wantStatus: CheckOK,
			wantMsgSub: "no dead channels",
		},
		{
			name:       "dead alias → WARN with remediation",
			checker:    &fakeSlackChannelChecker{dead: map[string]bool{deadID: true}},
			items:      []map[string]dynamodbtypes.AttributeValue{mkAliasRow(deadAlias, deadID)},
			wantStatus: CheckWarn,
			wantMsgSub: deadAlias,
			wantRemSub: "km slack forget-channel",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ddb DoctorDDBScanAPI
			if tc.checker != nil {
				fake := &fakeDDBScanClient{items: tc.items, scanErr: tc.scanErr}
				ddb = fake
			}
			got := checkSlackChannelDeadAlias(context.Background(), ddb, "km-slack-channels", tc.checker)
			if got.Status != tc.wantStatus {
				t.Errorf("Status: got %v, want %v (msg=%q)", got.Status, tc.wantStatus, got.Message)
			}
			if tc.wantMsgSub != "" && !strings.Contains(got.Message, tc.wantMsgSub) {
				t.Errorf("Message: got %q, want substring %q", got.Message, tc.wantMsgSub)
			}
			if tc.wantRemSub != "" && !strings.Contains(got.Remediation, tc.wantRemSub) {
				t.Errorf("Remediation: got %q, want substring %q", got.Remediation, tc.wantRemSub)
			}
		})
	}
}
