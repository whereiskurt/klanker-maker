// Package main — alerter.go
// DDB-Stream handler for the km-quota-alerter Lambda (Phase 121).
//
// The alerter receives DynamoDB Stream events from the {prefix}-action-quota
// table. For each MODIFY record where:
//   - NewImage has breached_at set (breach detected by pkg/quota)
//   - OldImage does NOT have breached_at set (first breach for this window)
//   - NewImage does NOT have alert_sent set (not already alerted)
//
// The alerter:
//  1. Sends an SES operator email.
//  2. Optionally posts to a Slack control channel (KM_SLACK_CONTROL_CHANNEL).
//  3. For proxy-origin actions (github_*/email_send): resolves the sandbox's
//     main Slack channel from km-sandboxes and posts an enforce-aware user notice.
//  4. Conditionally writes alert_sent (attribute_not_exists guard) — exactly
//     one alert per (sandbox, action, window).
//
// Idempotency: the conditional write uses attribute_not_exists(alert_sent).
// A ConditionalCheckFailedException means another Lambda instance already sent
// the alert — swallowed silently (not an error).
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// ─────────────────────────────────────────────────────────────
// Interfaces (injectable for testing)
// ─────────────────────────────────────────────────────────────

// sesAPI is the minimal SES v2 surface the alerter needs.
type sesAPI interface {
	SendEmail(ctx context.Context, in *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

// ddbAPI is the minimal DynamoDB surface the alerter needs:
//   - UpdateItem: conditional write for alert_sent guard
//   - GetItem: resolve slack_channel_id from km-sandboxes
type ddbAPI interface {
	UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
}

// slackPoster posts a plain-text message to a channel. Best-effort.
type slackPoster interface {
	PostChannelMessage(ctx context.Context, channelID, text string) error
}

// ─────────────────────────────────────────────────────────────
// alerterConfig holds resolved environment variables.
// ─────────────────────────────────────────────────────────────

type alerterConfig struct {
	operatorEmail       string
	emailDomain         string // e.g. sandboxes.example.com; used as From address base
	quotaTableName      string // {prefix}-action-quota
	sandboxTableName    string // {prefix}-sandboxes (optional; for channel lookup)
	slackControlChannel string // optional control channel ID (KM_SLACK_CONTROL_CHANNEL)
	botTokenPath        string // SSM path for bot token (optional; for channel notice)
	resourcePrefix      string // install prefix
}

// ─────────────────────────────────────────────────────────────
// Proxy-origin action detection
// ─────────────────────────────────────────────────────────────

// proxyOriginActions are the actions where enforcement lives in the http-proxy
// (not the bridge). For these, the alerter is responsible for posting the
// channel-level user notice (bridges handle their own in-thread notice).
var proxyOriginActions = map[string]bool{
	"github_pr":      true,
	"github_comment": true,
	"github_review":  true,
	"email_send":     true,
}

// ─────────────────────────────────────────────────────────────
// alerter is the testable core handler.
// ─────────────────────────────────────────────────────────────

type alerter struct {
	cfg   alerterConfig
	ses   sesAPI
	ddb   ddbAPI
	slack slackPoster // may be nil if no bot token
}

// Handle processes a DynamoDBEvent batch. Each record is handled independently;
// errors on individual records are logged but don't fail the batch.
func (a *alerter) Handle(ctx context.Context, ev events.DynamoDBEvent) error {
	for _, rec := range ev.Records {
		if err := a.handleRecord(ctx, rec); err != nil {
			// Log but don't fail the entire batch — other records can still succeed.
			log.Printf("[alerter] record error (eventID=%s): %v", rec.EventID, err)
		}
	}
	return nil
}

// handleRecord processes a single DynamoDB stream record.
func (a *alerter) handleRecord(ctx context.Context, rec events.DynamoDBEventRecord) error {
	// Only process MODIFY events.
	if rec.EventName != string(events.DynamoDBOperationTypeModify) {
		return nil
	}

	newImg := rec.Change.NewImage
	oldImg := rec.Change.OldImage

	// Must have breached_at in NewImage (breach detected by quota.Record).
	if _, hasBreachedAt := newImg["breached_at"]; !hasBreachedAt {
		return nil
	}

	// breached_at must NOT be in OldImage — this is the first-breach MODIFY.
	if _, hadBreachedAt := oldImg["breached_at"]; hadBreachedAt {
		return nil
	}

	// alert_sent must NOT be in NewImage — otherwise another instance beat us.
	if _, hasAlertSent := newImg["alert_sent"]; hasAlertSent {
		return nil
	}

	// Parse PK = {sandbox}#{action}, SK = window.
	pkAV, ok := newImg["PK"]
	if !ok {
		return fmt.Errorf("record missing PK")
	}
	pkStr := pkAV.String()
	if pkStr == "" {
		return fmt.Errorf("PK is empty")
	}

	skAV, ok := newImg["SK"]
	if !ok {
		return fmt.Errorf("record missing SK")
	}
	sk := skAV.String()

	// PK = "{sandbox}#{action}"
	parts := strings.SplitN(pkStr, "#", 2)
	if len(parts) != 2 {
		return fmt.Errorf("unexpected PK format: %q", pkStr)
	}
	sandboxID := parts[0]
	action := parts[1]

	// Resolve count and limit for the notice body (best-effort from attributes).
	countStr := ""
	if countAV, ok := newImg["count"]; ok {
		countStr = countAV.String()
	}

	// Determine enforcement mode: default WARN (v1 ships dormant).
	onBreach := "warn"
	if modeAV, ok := newImg["on_breach"]; ok && modeAV.String() != "" {
		onBreach = modeAV.String()
	}

	// ── Step 1: Send operator SES email ──────────────────────────────────────
	if a.ses != nil && a.cfg.operatorEmail != "" {
		if err := a.sendOperatorEmail(ctx, sandboxID, action, sk, countStr, onBreach); err != nil {
			// Log, don't abort — still attempt alert_sent write.
			log.Printf("[alerter] SES send error (sandbox=%s action=%s window=%s): %v", sandboxID, action, sk, err)
		}
	}

	// ── Step 2: For proxy-origin actions, post channel-level user notice ──────
	if proxyOriginActions[action] && a.cfg.sandboxTableName != "" && a.ddb != nil && a.slack != nil {
		if err := a.postChannelNotice(ctx, sandboxID, action, sk, countStr, onBreach); err != nil {
			// Best-effort — don't fail the record.
			log.Printf("[alerter] channel notice error (sandbox=%s): %v", sandboxID, err)
		}
	}

	// ── Step 3: Conditional write alert_sent (attribute_not_exists guard) ─────
	if a.ddb != nil && a.cfg.quotaTableName != "" {
		if err := a.setAlertSent(ctx, pkStr, sk); err != nil {
			var condErr *dynamodbtypes.ConditionalCheckFailedException
			if errors.As(err, &condErr) {
				// Another Lambda instance already set alert_sent — idempotent, not an error.
				log.Printf("[alerter] alert_sent already set (sandbox=%s action=%s window=%s) — idempotent", sandboxID, action, sk)
				return nil
			}
			return fmt.Errorf("set alert_sent for %s %s %s: %w", sandboxID, action, sk, err)
		}
	}

	return nil
}

// sendOperatorEmail sends the quota-breach notification email to the operator.
func (a *alerter) sendOperatorEmail(ctx context.Context, sandboxID, action, window, count, onBreach string) error {
	from := fmt.Sprintf("notifications@%s", a.cfg.emailDomain)
	if a.cfg.emailDomain == "" {
		from = "notifications@example.com"
	}

	modeLabel := quotaModeLabel(onBreach)
	subject := fmt.Sprintf("km quota %s: %s / %s / %s", onBreach, sandboxID, action, window)
	body := fmt.Sprintf(
		"km quota %s: %s\n\nSandbox:  %s\nAction:   %s\nWindow:   %s\nCount:    %s\nMode:     %s\nTime:     %s\n\n%s\n",
		onBreach, sandboxID,
		sandboxID, action, window, count, modeLabel,
		time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		breachExplainer(onBreach),
	)

	_, err := a.ses.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: awssdk.String(from),
		Destination: &sesv2types.Destination{
			ToAddresses: []string{a.cfg.operatorEmail},
		},
		Content: &sesv2types.EmailContent{
			Simple: &sesv2types.Message{
				Subject: &sesv2types.Content{Data: awssdk.String(subject)},
				Body: &sesv2types.Body{
					Text: &sesv2types.Content{Data: awssdk.String(body)},
				},
			},
		},
	})
	return err
}

// postChannelNotice resolves the sandbox's Slack channel and posts the
// enforce-aware user notice. Best-effort: if no channel, skips silently.
func (a *alerter) postChannelNotice(ctx context.Context, sandboxID, action, window, count, onBreach string) error {
	// Resolve sandbox Slack channel from km-sandboxes.
	out, err := a.ddb.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(a.cfg.sandboxTableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
	})
	if err != nil {
		return fmt.Errorf("get sandbox row for %s: %w", sandboxID, err)
	}
	if out.Item == nil {
		return nil // sandbox not in table (new or already destroyed)
	}
	channelAV, ok := out.Item["slack_channel_id"]
	if !ok {
		return nil // no Slack channel for this sandbox
	}
	channelID := ""
	if sv, ok := channelAV.(*dynamodbtypes.AttributeValueMemberS); ok {
		channelID = sv.Value
	}
	if channelID == "" {
		return nil
	}

	// Compose the enforce-aware user notice.
	var notice string
	switch onBreach {
	case "block":
		notice = fmt.Sprintf("🛑 Quota exceeded: `%s` (%s, window=%s). Further actions blocked until the window resets.", action, count, window)
	case "freeze":
		notice = fmt.Sprintf("🛑 Quota exceeded: `%s` (%s, window=%s). Sandbox frozen — no further high-impact actions until released by your operator.", action, count, window)
	default: // warn
		notice = fmt.Sprintf("⚠️ Quota reached: `%s` hit %s this %s. WARN mode — actions still flowing; heads-up.", action, count, window)
	}

	return a.slack.PostChannelMessage(ctx, channelID, notice)
}

// setAlertSent writes alert_sent using attribute_not_exists(alert_sent) as the
// condition expression so exactly one Lambda instance wins the write.
// Returns ConditionalCheckFailedException if another instance already wrote it.
func (a *alerter) setAlertSent(ctx context.Context, pk, sk string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := a.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(a.cfg.quotaTableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"PK": &dynamodbtypes.AttributeValueMemberS{Value: pk},
			"SK": &dynamodbtypes.AttributeValueMemberS{Value: sk},
		},
		UpdateExpression:    awssdk.String("SET alert_sent = :ts"),
		ConditionExpression: awssdk.String("attribute_not_exists(alert_sent)"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":ts": &dynamodbtypes.AttributeValueMemberS{Value: now},
		},
	})
	return err
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

func quotaModeLabel(onBreach string) string {
	switch onBreach {
	case "block":
		return "BLOCK (actions denied until window resets)"
	case "freeze":
		return "FREEZE (sandbox quarantined; release with km unlock)"
	default:
		return "WARN (actions still flowing; observation only)"
	}
}

func breachExplainer(onBreach string) string {
	switch onBreach {
	case "block":
		return "The quota window must reset before this action is permitted again. No operator action required (auto-unblocks when the window rolls)."
	case "freeze":
		return "The sandbox is now quarantined. All high-impact actions are blocked. Release with: km unlock <sandbox>"
	default:
		return "This is an observation-only alert (warn mode). No action required. To enforce, set onBreach: block or freeze in km-config.yaml."
	}
}
