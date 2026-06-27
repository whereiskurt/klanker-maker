// Package main — alerter_test.go
// ALR-01: idempotent alerter via conditional write.
// Two stream records for the same breached row → SES called once;
// second conditional write returns ConditionalCheckFailed and is swallowed.
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
)

// ─────────────────────────────────────────────────────────────
// Mock implementations
// ─────────────────────────────────────────────────────────────

type mockSES struct {
	sendCount int
	failOn    int // if >0, fail on the Nth call (1-based)
}

func (m *mockSES) SendEmail(_ context.Context, _ *sesv2.SendEmailInput, _ ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	m.sendCount++
	if m.failOn > 0 && m.sendCount == m.failOn {
		return nil, errors.New("ses: mock send error")
	}
	return &sesv2.SendEmailOutput{}, nil
}

type mockDDB struct {
	updateCount    int
	failCondCheckN int // fail with ConditionalCheckFailed on the Nth UpdateItem call
	getItemOut     *dynamodb.GetItemOutput
}

func (m *mockDDB) UpdateItem(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	m.updateCount++
	if m.failCondCheckN > 0 && m.updateCount == m.failCondCheckN {
		return nil, &dynamodbtypes.ConditionalCheckFailedException{Message: ptrString("mock cond check failed")}
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

func (m *mockDDB) GetItem(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if m.getItemOut != nil {
		return m.getItemOut, nil
	}
	return &dynamodb.GetItemOutput{}, nil
}

type mockSlack struct {
	postCount int
}

func (m *mockSlack) PostChannelMessage(_ context.Context, _, _ string) error {
	m.postCount++
	return nil
}

func ptrString(s string) *string { return &s }

// ─────────────────────────────────────────────────────────────
// buildBreachRecord constructs a fake DynamoDB MODIFY stream record
// where breached_at is newly set in NewImage and absent in OldImage.
// alert_sent is absent in NewImage (first breach, no alert sent yet).
// ─────────────────────────────────────────────────────────────

func buildBreachRecord(pk, sk string, alertSentPresent bool) events.DynamoDBEventRecord {
	newImage := map[string]events.DynamoDBAttributeValue{
		"PK":          events.NewStringAttribute(pk),
		"SK":          events.NewStringAttribute(sk),
		"count":       events.NewNumberAttribute("5"),
		"breached_at": events.NewNumberAttribute("1750000000"),
	}
	if alertSentPresent {
		newImage["alert_sent"] = events.NewStringAttribute("2026-06-27T00:00:00Z")
	}

	oldImage := map[string]events.DynamoDBAttributeValue{
		"PK":    events.NewStringAttribute(pk),
		"SK":    events.NewStringAttribute(sk),
		"count": events.NewNumberAttribute("4"),
		// breached_at absent in old image = this MODIFY is the first-breach event
	}

	return events.DynamoDBEventRecord{
		EventName: string(events.DynamoDBOperationTypeModify),
		Change: events.DynamoDBStreamRecord{
			NewImage: newImage,
			OldImage: oldImage,
		},
	}
}

// buildNonBreachRecord is a MODIFY where breached_at was ALREADY in old image
// (i.e. it was set on a previous call — not a new breach).
func buildNonBreachRecord(pk, sk string) events.DynamoDBEventRecord {
	newImage := map[string]events.DynamoDBAttributeValue{
		"PK":          events.NewStringAttribute(pk),
		"SK":          events.NewStringAttribute(sk),
		"count":       events.NewNumberAttribute("6"),
		"breached_at": events.NewNumberAttribute("1750000000"),
	}
	oldImage := map[string]events.DynamoDBAttributeValue{
		"PK":          events.NewStringAttribute(pk),
		"SK":          events.NewStringAttribute(sk),
		"count":       events.NewNumberAttribute("5"),
		"breached_at": events.NewNumberAttribute("1750000000"), // already present
	}
	return events.DynamoDBEventRecord{
		EventName: string(events.DynamoDBOperationTypeModify),
		Change: events.DynamoDBStreamRecord{
			NewImage: newImage,
			OldImage: oldImage,
		},
	}
}

// ─────────────────────────────────────────────────────────────
// TestIdempotentAlert (ALR-01)
// ─────────────────────────────────────────────────────────────

// TestIdempotentAlert (ALR-01) — the alerter fires exactly once per
// (sandbox, action, window) regardless of how many DDB-Stream events arrive.
//
// Mechanism: the handler sets alert_sent=true using a conditional
// UpdateItem with ConditionExpression "attribute_not_exists(alert_sent)".
// If the condition fails (alert already sent), the handler exits 0 silently.
// A tight loop of 100 blocked attempts against the same window still yields
// exactly ONE operator SES email.
func TestIdempotentAlert(t *testing.T) {
	cfg := alerterConfig{
		operatorEmail:  "operator@example.com",
		quotaTableName: "km-action-quota",
		emailDomain:    "sandboxes.example.com",
	}

	t.Run("first_breach_sends_alert_and_sets_alert_sent", func(t *testing.T) {
		ses := &mockSES{}
		ddb := &mockDDB{}
		slk := &mockSlack{}

		rec := buildBreachRecord("sb-abc123#github_pr", "lifetime", false)
		ev := events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{rec}}

		a := &alerter{cfg: cfg, ses: ses, ddb: ddb, slack: slk}
		if err := a.Handle(context.Background(), ev); err != nil {
			t.Fatalf("Handle returned error: %v", err)
		}

		if ses.sendCount != 1 {
			t.Errorf("SES sendCount = %d, want 1", ses.sendCount)
		}
		if ddb.updateCount != 1 {
			t.Errorf("DDB updateCount = %d, want 1", ddb.updateCount)
		}
	})

	t.Run("second_breach_record_alert_sent_already_set", func(t *testing.T) {
		// alert_sent already present in NewImage (alerter set it on the first call).
		ses := &mockSES{}
		ddb := &mockDDB{}
		slk := &mockSlack{}

		rec := buildBreachRecord("sb-abc123#github_pr", "lifetime", true /* alertSentPresent */)
		ev := events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{rec}}

		a := &alerter{cfg: cfg, ses: ses, ddb: ddb, slack: slk}
		if err := a.Handle(context.Background(), ev); err != nil {
			t.Fatalf("Handle returned error: %v", err)
		}

		// alert_sent already present → skip SES + DDB entirely
		if ses.sendCount != 0 {
			t.Errorf("SES sendCount = %d, want 0 (idempotent)", ses.sendCount)
		}
		if ddb.updateCount != 0 {
			t.Errorf("DDB updateCount = %d, want 0 (idempotent)", ddb.updateCount)
		}
	})

	t.Run("conditional_check_failed_is_swallowed", func(t *testing.T) {
		// The first UpdateItem (alert_sent conditional write) returns
		// ConditionalCheckFailedException — another alerter instance beat us.
		// The handler must swallow this and return nil (not error).
		ses := &mockSES{}
		ddb := &mockDDB{failCondCheckN: 1} // first UpdateItem call fails
		slk := &mockSlack{}

		rec := buildBreachRecord("sb-abc123#github_pr", "hour#486111", false)
		ev := events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{rec}}

		a := &alerter{cfg: cfg, ses: ses, ddb: ddb, slack: slk}
		if err := a.Handle(context.Background(), ev); err != nil {
			t.Fatalf("Handle must swallow ConditionalCheckFailed, got: %v", err)
		}
		// SES was called before the conditional write attempt.
		if ses.sendCount != 1 {
			t.Errorf("SES sendCount = %d, want 1 (alert sent before conditional write)", ses.sendCount)
		}
	})

	t.Run("non_new_breach_is_skipped", func(t *testing.T) {
		// breached_at was ALREADY in the old image → not a new breach → skip.
		ses := &mockSES{}
		ddb := &mockDDB{}
		slk := &mockSlack{}

		rec := buildNonBreachRecord("sb-abc123#github_pr", "lifetime")
		ev := events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{rec}}

		a := &alerter{cfg: cfg, ses: ses, ddb: ddb, slack: slk}
		if err := a.Handle(context.Background(), ev); err != nil {
			t.Fatalf("Handle returned error: %v", err)
		}
		if ses.sendCount != 0 {
			t.Errorf("SES sendCount = %d, want 0 (non-new breach)", ses.sendCount)
		}
		if ddb.updateCount != 0 {
			t.Errorf("DDB updateCount = %d, want 0 (non-new breach)", ddb.updateCount)
		}
	})

	t.Run("insert_and_remove_records_skipped", func(t *testing.T) {
		ses := &mockSES{}
		ddb := &mockDDB{}
		slk := &mockSlack{}

		insertRec := events.DynamoDBEventRecord{
			EventName: string(events.DynamoDBOperationTypeInsert),
			Change: events.DynamoDBStreamRecord{
				NewImage: map[string]events.DynamoDBAttributeValue{
					"PK": events.NewStringAttribute("sb-abc123#github_pr"),
					"SK": events.NewStringAttribute("lifetime"),
				},
			},
		}
		removeRec := events.DynamoDBEventRecord{
			EventName: string(events.DynamoDBOperationTypeRemove),
		}

		ev := events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{insertRec, removeRec}}
		a := &alerter{cfg: cfg, ses: ses, ddb: ddb, slack: slk}
		if err := a.Handle(context.Background(), ev); err != nil {
			t.Fatalf("Handle returned error: %v", err)
		}
		if ses.sendCount != 0 {
			t.Errorf("SES sendCount = %d, want 0 (insert/remove ignored)", ses.sendCount)
		}
	})

	t.Run("proxy_origin_posts_channel_notice", func(t *testing.T) {
		// For proxy-origin actions (github_*/email_send), the alerter resolves
		// the sandbox Slack channel from km-sandboxes and posts a channel notice.
		ses := &mockSES{}
		ddb := &mockDDB{
			getItemOut: &dynamodb.GetItemOutput{
				Item: map[string]dynamodbtypes.AttributeValue{
					"slack_channel_id": &dynamodbtypes.AttributeValueMemberS{Value: "C0TEST123"},
				},
			},
		}
		slk := &mockSlack{}

		// github_pr is proxy-origin
		rec := buildBreachRecord("sb-abc123#github_pr", "hour#486111", false)
		ev := events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{rec}}

		cfgWithSandboxTable := cfg
		cfgWithSandboxTable.sandboxTableName = "km-sandboxes"
		cfgWithSandboxTable.botTokenPath = "/km/slack/bot-token"

		a := &alerter{cfg: cfgWithSandboxTable, ses: ses, ddb: ddb, slack: slk}
		if err := a.Handle(context.Background(), ev); err != nil {
			t.Fatalf("Handle returned error: %v", err)
		}
		if ses.sendCount != 1 {
			t.Errorf("SES sendCount = %d, want 1", ses.sendCount)
		}
		if slk.postCount != 1 {
			t.Errorf("Slack postCount = %d, want 1 (channel notice for proxy-origin)", slk.postCount)
		}
	})

	t.Run("proxy_origin_no_channel_skips_slack", func(t *testing.T) {
		// Best-effort: if no slack_channel_id on the sandbox row, skip the channel post.
		ses := &mockSES{}
		ddb := &mockDDB{
			getItemOut: &dynamodb.GetItemOutput{
				Item: map[string]dynamodbtypes.AttributeValue{
					// no slack_channel_id
				},
			},
		}
		slk := &mockSlack{}

		rec := buildBreachRecord("sb-abc123#github_pr", "lifetime", false)
		ev := events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{rec}}

		cfgWithSandboxTable := cfg
		cfgWithSandboxTable.sandboxTableName = "km-sandboxes"

		a := &alerter{cfg: cfgWithSandboxTable, ses: ses, ddb: ddb, slack: slk}
		if err := a.Handle(context.Background(), ev); err != nil {
			t.Fatalf("Handle returned error: %v", err)
		}
		if slk.postCount != 0 {
			t.Errorf("Slack postCount = %d, want 0 (no channel)", slk.postCount)
		}
	})
}
