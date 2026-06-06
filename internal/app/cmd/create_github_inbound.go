package cmd

// create_github_inbound.go — Phase 97 Plan 03
//
// Orchestration helpers for per-sandbox GitHub inbound SQS FIFO queue provisioning
// at km create time. Called from the GitHub inbound block in create.go when
// notification.github.inbound.enabled=true.
//
// Design principles (mirrors create_slack_inbound.go):
//   - Thin over pkg/aws/sqs.go helpers (all SQS SDK calls go through the
//     SQSClient interface — mockable in tests without a real AWS connection).
//   - DDB attribute update is injected as a func — matches the pattern used by
//     create_slack_inbound.go so no real DynamoDB connection is required in tests.
//   - Queue URL is published to SSM Parameter Store
//     (/{prefix}/sandbox/{id}/github-inbound-queue-url). The GitHub poller reads it
//     at startup with a retry/backoff fallback.
//   - Rollback is explicit and always best-effort: each cleanup step is
//     attempted even when a prior cleanup step fails.

import (
	"context"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// githubInboundDeps bundles all dependencies for provisionGitHubInboundQueue.
// Using a struct enables clean dependency injection in tests without passing
// a dozen individual arguments. Mirrors slackInboundDeps.
type githubInboundDeps struct {
	// Profile is the resolved SandboxProfile (read from YAML + CLI overrides).
	Profile *profile.SandboxProfile
	// Cfg is the operator config (provides GetResourcePrefix(), SandboxTableName, Region).
	Cfg *config.Config
	// SandboxID is the sandbox being created (e.g. "sb-abc123").
	SandboxID string
	// SQS is the SQS client (real or mock).
	SQS awspkg.SQSClient
	// UpdateSandboxAttr persists a single string attribute to the km-sandboxes
	// DynamoDB row. Signature matches the internal DynamoDB UpdateItem pattern
	// used throughout sandbox_dynamo.go.
	UpdateSandboxAttr func(ctx context.Context, sandboxID, attr, value string) error
	// PutSSMParameter writes a String SSM Parameter Store entry. The GitHub poller
	// reads /{prefix}/sandbox/{id}/github-inbound-queue-url at startup with a
	// retry/backoff fallback when KM_GITHUB_INBOUND_QUEUE_URL is empty.
	PutSSMParameter func(ctx context.Context, name, value string) error
}

// notificationGitHubInbound returns p.Spec.Notification.Github.Inbound (nil-safe).
// Mirrors notificationSlackInbound in create_slack.go.
func notificationGitHubInbound(p *profile.SandboxProfile) *profile.NotificationGitHubInboundSpec {
	if p == nil || p.Spec.Notification == nil || p.Spec.Notification.Github == nil {
		return nil
	}
	return p.Spec.Notification.Github.Inbound
}

// provisionGitHubInboundQueue creates the per-sandbox GitHub inbound FIFO queue,
// persists its URL to the km-sandboxes DynamoDB row as github_inbound_queue_url, and
// publishes KM_GITHUB_INBOUND_QUEUE_URL via SSM Parameter Store.
//
// Returns ("", nil) when notification.github.inbound.enabled is false or unset — the
// no-op path leaves no SQS API calls, no DDB writes, and no SSM commands (dormant invariant).
//
// On any failure after queue creation, the function attempts rollback (delete
// queue, best-effort DDB clear) and returns a wrapped error.
func provisionGitHubInboundQueue(ctx context.Context, deps githubInboundDeps) (queueURL string, err error) {
	inbound := notificationGitHubInbound(deps.Profile)
	if inbound == nil || inbound.Enabled == nil || !*inbound.Enabled {
		return "", nil
	}

	resourcePrefix := "km"
	if deps.Cfg != nil {
		resourcePrefix = deps.Cfg.GetResourcePrefix()
	}
	queueName := awspkg.GitHubInboundQueueName(resourcePrefix, deps.SandboxID)

	queueURL, err = awspkg.CreateGitHubInboundQueue(ctx, deps.SQS, queueName)
	if err != nil {
		return "", fmt.Errorf("provision github inbound queue: %w", err)
	}
	log.Info().Str("sandbox_id", deps.SandboxID).Str("queue_name", queueName).
		Msg("GitHub inbound queue created")
	fmt.Fprintf(os.Stderr, "  ✓ GitHub: created inbound queue %s\n", queueName)

	// Persist queue URL to DDB sandbox metadata row.
	if updateErr := deps.UpdateSandboxAttr(ctx, deps.SandboxID, "github_inbound_queue_url", queueURL); updateErr != nil {
		// Best-effort queue cleanup to avoid orphaned AWS resources.
		if delErr := awspkg.DeleteGitHubInboundQueue(ctx, deps.SQS, queueURL); delErr != nil {
			log.Warn().Err(delErr).Str("queue_url", queueURL).
				Msg("rollback: failed to delete GitHub SQS queue after DDB persist failure")
		}
		return "", fmt.Errorf("persist github_inbound_queue_url to DDB: %w", updateErr)
	}

	// Publish queue URL to SSM Parameter Store.
	paramName := awspkg.SandboxParameterPath(deps.Cfg.GetResourcePrefix(), deps.SandboxID, "github-inbound-queue-url")
	if putErr := deps.PutSSMParameter(ctx, paramName, queueURL); putErr != nil {
		// Best-effort queue cleanup. DDB attribute is left — km destroy cleans up.
		if delErr := awspkg.DeleteGitHubInboundQueue(ctx, deps.SQS, queueURL); delErr != nil {
			log.Warn().Err(delErr).Str("queue_url", queueURL).
				Msg("rollback: failed to delete GitHub SQS queue after SSM Parameter Store write failure")
		}
		return "", fmt.Errorf("write SSM parameter %s: %w", paramName, putErr)
	}
	fmt.Fprintf(os.Stderr, "  ✓ GitHub: wrote queue URL to SSM Parameter Store %s\n", paramName)

	return queueURL, nil
}

// rollbackGitHubInboundQueue deletes the SQS queue and clears the DDB attribute.
// Best-effort: always attempts both steps; returns the first non-nil error but
// does not short-circuit on the first failure.
//
// Called from create.go when a step after provisionGitHubInboundQueue fails.
// When queueURL is empty (provisioning was skipped), returns nil immediately.
func rollbackGitHubInboundQueue(ctx context.Context, deps githubInboundDeps, queueURL string) error {
	if queueURL == "" {
		return nil
	}
	fmt.Fprintf(os.Stderr, "  ↺ GitHub: rolling back inbound queue %s\n", queueURL)

	var firstErr error
	if delErr := awspkg.DeleteGitHubInboundQueue(ctx, deps.SQS, queueURL); delErr != nil {
		log.Warn().Err(delErr).Str("queue_url", queueURL).Msg("rollback: delete github queue failed")
		firstErr = delErr
	}
	// Clear the DDB attribute so km doctor doesn't flag a stale queue.
	if deps.UpdateSandboxAttr != nil {
		if clearErr := deps.UpdateSandboxAttr(ctx, deps.SandboxID, "github_inbound_queue_url", ""); clearErr != nil {
			log.Warn().Err(clearErr).Str("sandbox_id", deps.SandboxID).
				Msg("rollback: failed to clear github_inbound_queue_url from DDB")
			if firstErr == nil {
				firstErr = clearErr
			}
		}
	}
	return firstErr
}
