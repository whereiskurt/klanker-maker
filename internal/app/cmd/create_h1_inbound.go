package cmd

// create_h1_inbound.go — Phase 103 Plan 08
//
// Orchestration helpers for per-sandbox HackerOne inbound SQS FIFO queue
// provisioning at km create time. Forked from create_github_inbound.go. Called
// from the H1 inbound block in create.go when notification.h1.inbound.enabled=true.
//
// Design principles (mirrors create_github_inbound.go / create_slack_inbound.go):
//   - Thin over pkg/aws/sqs.go helpers (all SQS SDK calls go through the SQSClient
//     interface — mockable in tests without a real AWS connection).
//   - DDB attribute update is injected as a func — no real DynamoDB connection in tests.
//   - Queue URL is published to SSM Parameter Store
//     (/{prefix}/sandbox/{id}/h1-inbound-queue-url). The km-h1-inbound-poller (Plan 09)
//     reads it at startup with a retry/backoff fallback when KM_H1_INBOUND_QUEUE_URL
//     is empty.
//   - Rollback is explicit and always best-effort: each cleanup step is attempted
//     even when a prior cleanup step fails.
//
// POISON-WEDGE PROTECTION (memory project_inbound_poller_fifo_poison_wedge, resolved
// Phase 99.1): DLQArn threads the shared per-install h1-inbound DLQ ARN into
// CreateH1InboundQueue, which attaches a RedrivePolicy (maxReceiveCount=3) so a
// poison H1 envelope is auto-evicted to the shared DLQ instead of head-of-line
// blocking the FIFO message group forever. Empty DLQArn ⇒ no RedrivePolicy
// (dormancy preserved). The H1 queue ALSO uses a 1800s VisibilityTimeout (not 30s)
// so a long triage turn is not re-delivered mid-flight (Phase 97 dup-review loops);
// that override lives in CreateH1InboundQueue, not here.

import (
	"context"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// h1InboundDeps bundles all dependencies for provisionH1InboundQueue. Using a
// struct enables clean dependency injection in tests. Mirrors githubInboundDeps.
type h1InboundDeps struct {
	// Profile is the resolved SandboxProfile (read from YAML + CLI overrides).
	Profile *profile.SandboxProfile
	// Cfg is the operator config (provides GetResourcePrefix(), SandboxTableName, Region).
	Cfg *config.Config
	// SandboxID is the sandbox being created (e.g. "sb-abc123").
	SandboxID string
	// SQS is the SQS client (real or mock).
	SQS awspkg.SQSClient
	// DLQArn is the shared (per-install) H1-inbound dead-letter-queue ARN. When
	// non-empty it is threaded into CreateH1InboundQueue, which attaches a
	// RedrivePolicy (maxReceiveCount=3). Empty ⇒ no RedrivePolicy (dormancy
	// preserved). Derived from region + account ID + H1InboundDLQName(prefix).
	DLQArn string
	// UpdateSandboxAttr persists a single string attribute to the km-sandboxes row.
	UpdateSandboxAttr func(ctx context.Context, sandboxID, attr, value string) error
	// PutSSMParameter writes a String SSM Parameter Store entry. The H1 poller reads
	// /{prefix}/sandbox/{id}/h1-inbound-queue-url at startup with a retry/backoff
	// fallback when KM_H1_INBOUND_QUEUE_URL is empty.
	PutSSMParameter func(ctx context.Context, name, value string) error
}

// notificationH1Inbound returns the per-sandbox H1 inbound spec (nil-safe), the
// gate for provisionH1InboundQueue. The notification.h1.inbound schema field is
// introduced in Plan 09 (it carries the km-h1-inbound-poller userdata + the
// dormancy golden). Until that field lands this accessor returns nil so the H1
// queue provisioning stays dormant and the build is green in Plan-08 isolation;
// Plan 09 repoints this at p.Spec.Notification.H1.Inbound and wires the create.go
// call site alongside the gate field it owns.
//
// Mirrors notificationGitHubInbound / notificationSlackInbound.
func notificationH1Inbound(p *profile.SandboxProfile) *profile.NotificationGitHubInboundSpec {
	// Forward-compat stub (Plan 09 wiring point). Returning nil keeps the H1
	// inbound path dormant — no SQS calls, no DDB writes, no SSM commands.
	_ = p
	return nil
}

// provisionH1InboundQueue creates the per-sandbox HackerOne inbound FIFO queue,
// persists its URL to the km-sandboxes row as h1_inbound_queue_url, and publishes
// the URL via SSM Parameter Store (for KM_H1_INBOUND_QUEUE_URL fallback).
//
// Returns ("", nil) when notification.h1.inbound.enabled is false or unset — the
// no-op path leaves no SQS API calls, no DDB writes, and no SSM commands (dormant
// invariant). On any failure after queue creation, the function attempts rollback
// (delete queue, best-effort DDB clear) and returns a wrapped error.
func provisionH1InboundQueue(ctx context.Context, deps h1InboundDeps) (queueURL string, err error) {
	inbound := notificationH1Inbound(deps.Profile)
	if inbound == nil || inbound.Enabled == nil || !*inbound.Enabled {
		return "", nil
	}

	resourcePrefix := "km"
	if deps.Cfg != nil {
		resourcePrefix = deps.Cfg.GetResourcePrefix()
	}
	queueName := awspkg.H1InboundQueueName(resourcePrefix, deps.SandboxID)

	queueURL, err = awspkg.CreateH1InboundQueue(ctx, deps.SQS, queueName, deps.DLQArn)
	if err != nil {
		return "", fmt.Errorf("provision h1 inbound queue: %w", err)
	}
	log.Info().Str("sandbox_id", deps.SandboxID).Str("queue_name", queueName).
		Msg("H1 inbound queue created")
	fmt.Fprintf(os.Stderr, "  ✓ HackerOne: created inbound queue %s\n", queueName)

	// Persist queue URL to DDB sandbox metadata row.
	if updateErr := deps.UpdateSandboxAttr(ctx, deps.SandboxID, "h1_inbound_queue_url", queueURL); updateErr != nil {
		if delErr := awspkg.DeleteH1InboundQueue(ctx, deps.SQS, queueURL); delErr != nil {
			log.Warn().Err(delErr).Str("queue_url", queueURL).
				Msg("rollback: failed to delete H1 SQS queue after DDB persist failure")
		}
		return "", fmt.Errorf("persist h1_inbound_queue_url to DDB: %w", updateErr)
	}

	// Publish queue URL to SSM Parameter Store.
	paramName := awspkg.SandboxParameterPath(deps.Cfg.GetResourcePrefix(), deps.SandboxID, "h1-inbound-queue-url")
	if putErr := deps.PutSSMParameter(ctx, paramName, queueURL); putErr != nil {
		if delErr := awspkg.DeleteH1InboundQueue(ctx, deps.SQS, queueURL); delErr != nil {
			log.Warn().Err(delErr).Str("queue_url", queueURL).
				Msg("rollback: failed to delete H1 SQS queue after SSM Parameter Store write failure")
		}
		return "", fmt.Errorf("write SSM parameter %s: %w", paramName, putErr)
	}
	fmt.Fprintf(os.Stderr, "  ✓ HackerOne: wrote queue URL to SSM Parameter Store %s\n", paramName)

	return queueURL, nil
}

// rollbackH1InboundQueue deletes the SQS queue and clears the DDB attribute.
// Best-effort: always attempts both steps; returns the first non-nil error but
// does not short-circuit. Called from create.go when a step after
// provisionH1InboundQueue fails. When queueURL is empty (provisioning skipped),
// returns nil immediately.
func rollbackH1InboundQueue(ctx context.Context, deps h1InboundDeps, queueURL string) error {
	if queueURL == "" {
		return nil
	}
	fmt.Fprintf(os.Stderr, "  ↺ HackerOne: rolling back inbound queue %s\n", queueURL)

	var firstErr error
	if delErr := awspkg.DeleteH1InboundQueue(ctx, deps.SQS, queueURL); delErr != nil {
		log.Warn().Err(delErr).Str("queue_url", queueURL).Msg("rollback: delete h1 queue failed")
		firstErr = delErr
	}
	if deps.UpdateSandboxAttr != nil {
		if clearErr := deps.UpdateSandboxAttr(ctx, deps.SandboxID, "h1_inbound_queue_url", ""); clearErr != nil {
			log.Warn().Err(clearErr).Str("sandbox_id", deps.SandboxID).
				Msg("rollback: failed to clear h1_inbound_queue_url from DDB")
			if firstErr == nil {
				firstErr = clearErr
			}
		}
	}
	return firstErr
}
