package cmd

// create_webhook_inbound.go — Phase 127
//
// Orchestration helpers for per-sandbox generic webhook inbound SQS FIFO queue
// provisioning at km create time. Called from the webhook inbound block in
// create.go when notification.webhook.inbound.enabled=true.
//
// Design principles (mirrors create_github_inbound.go):
//   - Thin over pkg/aws/sqs.go helpers (all SQS SDK calls go through the
//     SQSClient interface — mockable in tests without a real AWS connection).
//   - DDB attribute update is injected as a func — matches the pattern used by
//     create_github_inbound.go so no real DynamoDB connection is required in tests.
//   - Queue URL is published to SSM Parameter Store
//     (/{prefix}/sandbox/{id}/webhook-inbound-queue-url). The webhook poller reads
//     it at startup with a retry/backoff fallback.
//   - Rollback is explicit and always best-effort: each cleanup step is
//     attempted even when a prior cleanup step fails.

import (
	"context"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// webhookInboundDeps bundles all dependencies for provisionWebhookInboundQueue.
// Using a struct enables clean dependency injection in tests without passing
// a dozen individual arguments. Mirrors githubInboundDeps.
type webhookInboundDeps struct {
	// Profile is the resolved SandboxProfile (read from YAML + CLI overrides).
	Profile *profile.SandboxProfile
	// Cfg is the operator config (provides GetResourcePrefix(), SandboxTableName, Region).
	Cfg *config.Config
	// SandboxID is the sandbox being created (e.g. "sb-abc123").
	SandboxID string
	// SQS is the SQS client (real or mock).
	SQS awspkg.SQSClient
	// DLQArn is the shared (per-install) webhook-inbound dead-letter-queue ARN.
	// When non-empty it is threaded into CreateWebhookInboundQueue, which attaches
	// a RedrivePolicy (maxReceiveCount=3) so a poison envelope is auto-evicted to
	// the shared DLQ instead of head-of-line-blocking the FIFO group forever.
	// Empty ⇒ no RedrivePolicy (dormancy preserved). Derived from region + account
	// ID + WebhookInboundDLQName(prefix).
	DLQArn string
	// UpdateSandboxAttr persists a single string attribute to the km-sandboxes
	// DynamoDB row. Signature matches the internal DynamoDB UpdateItem pattern
	// used throughout sandbox_dynamo.go.
	UpdateSandboxAttr func(ctx context.Context, sandboxID, attr, value string) error
	// PutSSMParameter writes a String SSM Parameter Store entry. The webhook
	// poller reads /{prefix}/sandbox/{id}/webhook-inbound-queue-url at startup
	// with a retry/backoff fallback when KM_WEBHOOK_INBOUND_QUEUE_URL is empty.
	PutSSMParameter func(ctx context.Context, name, value string) error
}

// notificationWebhookInbound returns p.Spec.Notification.Webhook.Inbound (nil-safe).
// Mirrors notificationGitHubInbound in create_github_inbound.go.
func notificationWebhookInbound(p *profile.SandboxProfile) *profile.NotificationWebhookInboundSpec {
	if p == nil || p.Spec.Notification == nil || p.Spec.Notification.Webhook == nil {
		return nil
	}
	return p.Spec.Notification.Webhook.Inbound
}

// provisionWebhookInboundQueue creates the per-sandbox generic webhook inbound
// FIFO queue, persists its URL to the km-sandboxes DynamoDB row as
// webhook_inbound_queue_url, and publishes KM_WEBHOOK_INBOUND_QUEUE_URL via SSM
// Parameter Store.
//
// Returns ("", nil) when notification.webhook.inbound.enabled is false or unset —
// the no-op path leaves no SQS API calls, no DDB writes, and no SSM commands
// (dormant invariant).
//
// On any failure after queue creation, the function attempts rollback (delete
// queue, best-effort DDB clear) and returns a wrapped error.
func provisionWebhookInboundQueue(ctx context.Context, deps webhookInboundDeps) (queueURL string, err error) {
	inbound := notificationWebhookInbound(deps.Profile)
	if inbound == nil || inbound.Enabled == nil || !*inbound.Enabled {
		return "", nil
	}

	resourcePrefix := "km"
	if deps.Cfg != nil {
		resourcePrefix = deps.Cfg.GetResourcePrefix()
	}
	queueName := awspkg.WebhookInboundQueueName(resourcePrefix, deps.SandboxID)

	queueURL, err = awspkg.CreateWebhookInboundQueue(ctx, deps.SQS, queueName, deps.DLQArn)
	if err != nil {
		return "", fmt.Errorf("provision webhook inbound queue: %w", err)
	}
	log.Info().Str("sandbox_id", deps.SandboxID).Str("queue_name", queueName).
		Msg("Webhook inbound queue created")
	fmt.Fprintf(os.Stderr, "  ✓ Webhook: created inbound queue %s\n", queueName)

	// Persist queue URL to DDB sandbox metadata row.
	if updateErr := deps.UpdateSandboxAttr(ctx, deps.SandboxID, "webhook_inbound_queue_url", queueURL); updateErr != nil {
		// Best-effort queue cleanup to avoid orphaned AWS resources.
		if delErr := awspkg.DeleteWebhookInboundQueue(ctx, deps.SQS, queueURL); delErr != nil {
			log.Warn().Err(delErr).Str("queue_url", queueURL).
				Msg("rollback: failed to delete webhook SQS queue after DDB persist failure")
		}
		return "", fmt.Errorf("persist webhook_inbound_queue_url to DDB: %w", updateErr)
	}

	// Publish queue URL to SSM Parameter Store.
	paramName := awspkg.SandboxParameterPath(deps.Cfg.GetResourcePrefix(), deps.SandboxID, "webhook-inbound-queue-url")
	if putErr := deps.PutSSMParameter(ctx, paramName, queueURL); putErr != nil {
		// Best-effort queue cleanup. DDB attribute is left — km destroy cleans up.
		if delErr := awspkg.DeleteWebhookInboundQueue(ctx, deps.SQS, queueURL); delErr != nil {
			log.Warn().Err(delErr).Str("queue_url", queueURL).
				Msg("rollback: failed to delete webhook SQS queue after SSM Parameter Store write failure")
		}
		return "", fmt.Errorf("write SSM parameter %s: %w", paramName, putErr)
	}
	fmt.Fprintf(os.Stderr, "  ✓ Webhook: wrote queue URL to SSM Parameter Store %s\n", paramName)

	return queueURL, nil
}

// rollbackWebhookInboundQueue deletes the SQS queue and clears the DDB attribute.
// Best-effort: always attempts both steps; returns the first non-nil error but
// does not short-circuit on the first failure.
//
// Called from create.go when a step after provisionWebhookInboundQueue fails.
// When queueURL is empty (provisioning was skipped), returns nil immediately.
func rollbackWebhookInboundQueue(ctx context.Context, deps webhookInboundDeps, queueURL string) error {
	if queueURL == "" {
		return nil
	}
	fmt.Fprintf(os.Stderr, "  ↺ Webhook: rolling back inbound queue %s\n", queueURL)

	var firstErr error
	if delErr := awspkg.DeleteWebhookInboundQueue(ctx, deps.SQS, queueURL); delErr != nil {
		log.Warn().Err(delErr).Str("queue_url", queueURL).Msg("rollback: delete webhook queue failed")
		firstErr = delErr
	}
	// Clear the DDB attribute so km doctor doesn't flag a stale queue.
	if deps.UpdateSandboxAttr != nil {
		if clearErr := deps.UpdateSandboxAttr(ctx, deps.SandboxID, "webhook_inbound_queue_url", ""); clearErr != nil {
			log.Warn().Err(clearErr).Str("sandbox_id", deps.SandboxID).
				Msg("rollback: failed to clear webhook_inbound_queue_url from DDB")
			if firstErr == nil {
				firstErr = clearErr
			}
		}
	}
	return firstErr
}
