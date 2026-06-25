package cmd

// doctor_slack_threads_missing_scope_test.go — Phase 118.
//
// The dead-channel checks probe each channel via conversations.info. For a
// PRIVATE channel the probe fails with missing_scope when the bot lacks
// groups:read — previously this was swallowed as a transient error and the check
// reported a clean "no dead channels (N probed)" bill, silently blind to the
// private channels it could not inspect. These tests assert the checks now
// surface that blind spot as a WARN.

import (
	"context"
	"strings"
	"testing"

	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	kmslack "github.com/whereiskurt/klanker-maker/pkg/slack"
)

func missingScopeErr() error {
	return &kmslack.SlackAPIError{Method: "conversations.info", Code: "missing_scope"}
}

// TestCheckSlackThreadDeadChannels_MissingScopeBlindSpot — a channel that returns
// missing_scope (private channel, no groups:read) is reported as unprobed, not as
// a clean pass.
func TestCheckSlackThreadDeadChannels_MissingScopeBlindSpot(t *testing.T) {
	privateID := "CPRIV001"
	aliveID := "CALIVE9"
	row := func(ch string) map[string]dynamodbtypes.AttributeValue {
		return map[string]dynamodbtypes.AttributeValue{
			"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: ch},
			"thread_ts":  &dynamodbtypes.AttributeValueMemberS{Value: "1.2"},
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "sb-x"},
		}
	}
	checker := &fakeSlackChannelChecker{
		dead: map[string]bool{aliveID: false},
		err:  map[string]error{privateID: missingScopeErr()},
	}
	ddb := &fakeDDBScanClient{items: []map[string]dynamodbtypes.AttributeValue{row(aliveID), row(privateID)}}

	got := checkSlackThreadDeadChannels(context.Background(), ddb, "km-slack-threads", checker)
	if got.Status != CheckWarn {
		t.Fatalf("expected WARN (blind spot), got %s (msg=%q)", got.Status, got.Message)
	}
	if !strings.Contains(got.Message, "missing_scope") || !strings.Contains(got.Message, "groups:read") {
		t.Errorf("message should explain the missing_scope/groups:read blind spot; got: %s", got.Message)
	}
}

// TestCheckSlackChannelDeadAlias_MissingScopeBlindSpot — same blind-spot surfacing
// for the alias-table check.
func TestCheckSlackChannelDeadAlias_MissingScopeBlindSpot(t *testing.T) {
	privateID := "CPRIV002"
	aliasRow := func(alias, ch string) map[string]dynamodbtypes.AttributeValue {
		return map[string]dynamodbtypes.AttributeValue{
			"alias":      &dynamodbtypes.AttributeValueMemberS{Value: alias},
			"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: ch},
		}
	}
	checker := &fakeSlackChannelChecker{
		err: map[string]error{privateID: missingScopeErr()},
	}
	ddb := &fakeDDBScanClient{items: []map[string]dynamodbtypes.AttributeValue{aliasRow("priv-sb", privateID)}}

	got := checkSlackChannelDeadAlias(context.Background(), ddb, "km-slack-channels", checker)
	if got.Status != CheckWarn {
		t.Fatalf("expected WARN (blind spot), got %s (msg=%q)", got.Status, got.Message)
	}
	if !strings.Contains(got.Message, "missing_scope") || !strings.Contains(got.Message, "groups:read") {
		t.Errorf("message should explain the missing_scope/groups:read blind spot; got: %s", got.Message)
	}
}

// TestCheckSlackThreadDeadChannels_TransientStillClean — a non-missing_scope
// transient error must NOT be reported as a blind spot (it's a hiccup, OK stands).
func TestCheckSlackThreadDeadChannels_TransientStillClean(t *testing.T) {
	flaky := "CFLAKY1"
	row := map[string]dynamodbtypes.AttributeValue{
		"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: flaky},
		"thread_ts":  &dynamodbtypes.AttributeValueMemberS{Value: "1.2"},
		"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "sb-x"},
	}
	checker := &fakeSlackChannelChecker{
		err: map[string]error{flaky: &kmslack.SlackAPIError{Method: "conversations.info", Code: "ratelimited"}},
	}
	ddb := &fakeDDBScanClient{items: []map[string]dynamodbtypes.AttributeValue{row}}

	got := checkSlackThreadDeadChannels(context.Background(), ddb, "km-slack-threads", checker)
	if got.Status != CheckOK {
		t.Fatalf("expected OK for a transient (non-scope) error, got %s (msg=%q)", got.Status, got.Message)
	}
}
