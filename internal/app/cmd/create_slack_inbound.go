package cmd

// create_slack_inbound.go — Phase 67 Plan 06
//
// Orchestration helpers for per-sandbox SQS FIFO queue provisioning at
// km create time. Called from the Slack channel-creation block in create.go
// immediately after resolveSlackChannel succeeds.
//
// Design principles:
//   - Thin over pkg/aws/sqs.go helpers (all SQS SDK calls go through the
//     SQSClient interface — mockable in tests without a real AWS connection).
//   - DDB attribute update is injected as a func — matches the pattern used by
//     create_slack_test.go so no real DynamoDB connection is required in tests.
//   - Queue URL is published to SSM Parameter Store
//     (/sandbox/{id}/slack-inbound-queue-url). The sandbox poller reads it at
//     startup with a retry/backoff fallback. SSM SendCommand is intentionally
//     avoided because an org-level SCP denies it for the application account.
//   - Rollback is explicit and always best-effort: each cleanup step is
//     attempted even when a prior cleanup step fails.

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/rs/zerolog/log"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
	slackpkg "github.com/whereiskurt/klankrmkr/pkg/slack"
	slackbridge "github.com/whereiskurt/klankrmkr/pkg/slack/bridge"
)

// slackInboundDeps bundles all dependencies for provisionSlackInboundQueue.
// Using a struct enables clean dependency injection in tests without passing
// a dozen individual arguments.
type slackInboundDeps struct {
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
	// PutSSMParameter writes a String SSM Parameter Store entry. The sandbox
	// poller reads /sandbox/{id}/slack-inbound-queue-url at startup with a
	// retry/backoff fallback when the env var is empty. SSM SendCommand cannot
	// be used because an org-level SCP denies it for the application account.
	PutSSMParameter func(ctx context.Context, name, value string) error

	// Phase 67-07: ready-announcement fields.
	// PostOperatorSigned posts a message to the given Slack channel via the
	// operator-signed bridge `post` action and returns the message timestamp (ts).
	// Used by postReadyAnnouncement to post the "Sandbox ready" message.
	// May be nil — postReadyAnnouncement is a no-op when nil.
	PostOperatorSigned func(ctx context.Context, channelID, body string) (messageTS string, err error)
	// UpsertSlackThread writes (or updates) a km-slack-threads row anchoring the
	// (channelID, threadTS) → sandboxID mapping. Empty claude_session_id is written
	// intentionally — the first reply starts a fresh Claude session.
	// May be nil — postReadyAnnouncement skips the upsert when nil.
	UpsertSlackThread func(ctx context.Context, channelID, threadTS, sandboxID string) error
}

// provisionSlackInboundQueue creates the per-sandbox SQS FIFO queue, persists
// its URL to the km-sandboxes DynamoDB row as slack_inbound_queue_url, and
// injects KM_SLACK_INBOUND_QUEUE_URL into the sandbox's env file via SSM.
//
// Returns ("", nil) when notifySlackInboundEnabled is false or unset — the
// no-op path leaves no SQS API calls, no DDB writes, and no SSM commands.
//
// On any failure after queue creation, the function attempts rollback (delete
// queue, best-effort DDB clear) and returns a wrapped error. The caller in
// create.go MUST also archive the Slack channel if this returns an error
// (mirrors Phase 63 channel-failure rollback semantics).
//
// INVARIANT — last_pause_hint_ts is NOT pre-populated:
//   The km-sandboxes row MUST NOT have last_pause_hint_ts written at create
//   time. Plan 67-05's DDBPauseHinter treats "attribute absent" as
//   "cooldown expired — post the first hint immediately." Writing epoch-0 or
//   now() would either always trigger (epoch-0 is older than 1h cooldown) or
//   suppress the very first pause hint if the sandbox pauses immediately after
//   creation. The attribute is written only by DDBPauseHinter itself on its
//   first successful post. This function MUST NOT call UpdateSandboxAttr for
//   last_pause_hint_ts.
func provisionSlackInboundQueue(ctx context.Context, deps slackInboundDeps) (queueURL string, err error) {
	cli := deps.Profile.Spec.CLI
	if cli == nil || !cli.NotifySlackInboundEnabled {
		return "", nil
	}

	resourcePrefix := "km"
	if deps.Cfg != nil {
		resourcePrefix = deps.Cfg.GetResourcePrefix()
	}
	queueName := awspkg.SlackInboundQueueName(resourcePrefix, deps.SandboxID)

	queueURL, err = awspkg.CreateSlackInboundQueue(ctx, deps.SQS, queueName)
	if err != nil {
		return "", fmt.Errorf("provision slack inbound queue: %w", err)
	}
	log.Info().Str("sandbox_id", deps.SandboxID).Str("queue_name", queueName).
		Msg("Slack inbound queue created")
	fmt.Fprintf(os.Stderr, "  ✓ Slack: created inbound queue %s\n", queueName)

	// Persist queue URL to DDB sandbox metadata row.
	//
	// NOTE: we only write slack_inbound_queue_url here. We deliberately do NOT
	// write last_pause_hint_ts (see INVARIANT comment on provisionSlackInboundQueue).
	if updateErr := deps.UpdateSandboxAttr(ctx, deps.SandboxID, "slack_inbound_queue_url", queueURL); updateErr != nil {
		// Best-effort queue cleanup to avoid orphaned AWS resources.
		if delErr := awspkg.DeleteSlackInboundQueue(ctx, deps.SQS, queueURL); delErr != nil {
			log.Warn().Err(delErr).Str("queue_url", queueURL).
				Msg("rollback: failed to delete SQS queue after DDB persist failure")
		}
		return "", fmt.Errorf("persist slack_inbound_queue_url to DDB: %w", updateErr)
	}

	// Publish queue URL to SSM Parameter Store. The sandbox poller reads
	// /sandbox/{id}/slack-inbound-queue-url at startup with a retry/backoff
	// fallback when KM_SLACK_INBOUND_QUEUE_URL is empty. SSM SendCommand is
	// not used because an org-level SCP denies it for the application account.
	paramName := "/sandbox/" + deps.SandboxID + "/slack-inbound-queue-url"
	if putErr := deps.PutSSMParameter(ctx, paramName, queueURL); putErr != nil {
		// Best-effort queue cleanup. The DDB slack_inbound_queue_url attribute
		// is left in place — km destroy's stale-queue check and km doctor will
		// reconcile it. Documented in CONTEXT.md edge cases.
		if delErr := awspkg.DeleteSlackInboundQueue(ctx, deps.SQS, queueURL); delErr != nil {
			log.Warn().Err(delErr).Str("queue_url", queueURL).
				Msg("rollback: failed to delete SQS queue after SSM Parameter Store write failure")
		}
		return "", fmt.Errorf("write SSM parameter %s: %w", paramName, putErr)
	}
	fmt.Fprintf(os.Stderr, "  ✓ Slack: wrote queue URL to SSM Parameter Store %s\n", paramName)

	return queueURL, nil
}

// rollbackSlackInboundQueue deletes the SQS queue and clears the DDB attribute.
// Best-effort: always attempts both steps; returns the first non-nil error but
// does not short-circuit on the first failure.
//
// Called from create.go when a step after provisionSlackInboundQueue fails.
// When queueURL is empty (provisioning was skipped), returns nil immediately.
func rollbackSlackInboundQueue(ctx context.Context, deps slackInboundDeps, queueURL string) error {
	if queueURL == "" {
		return nil
	}
	fmt.Fprintf(os.Stderr, "  ↺ Slack: rolling back inbound queue %s\n", queueURL)

	var firstErr error
	if delErr := awspkg.DeleteSlackInboundQueue(ctx, deps.SQS, queueURL); delErr != nil {
		log.Warn().Err(delErr).Str("queue_url", queueURL).Msg("rollback: delete queue failed")
		firstErr = delErr
	}
	// Clear the DDB attribute so km doctor doesn't flag a stale queue.
	if deps.UpdateSandboxAttr != nil {
		if clearErr := deps.UpdateSandboxAttr(ctx, deps.SandboxID, "slack_inbound_queue_url", ""); clearErr != nil {
			log.Warn().Err(clearErr).Str("sandbox_id", deps.SandboxID).
				Msg("rollback: failed to clear slack_inbound_queue_url from DDB")
			if firstErr == nil {
				firstErr = clearErr
			}
		}
	}
	return firstErr
}

// postReadyAnnouncement posts the "Sandbox <id> ready" message via the existing
// Phase 63 operator-signed bridge `post` action and records the returned
// message_ts in km-slack-threads with an empty claude_session_id.
//
// The first user reply directly under that message starts a fresh Claude session.
//
// Returns nil on best-effort failure (logs WARN) — failing to post the ready
// announcement does NOT abort km create. The user can always start a top-level
// post.
func postReadyAnnouncement(ctx context.Context, deps slackInboundDeps, channelID string) error {
	if deps.Profile == nil || deps.Profile.Spec.CLI == nil || !deps.Profile.Spec.CLI.NotifySlackInboundEnabled {
		return nil
	}
	if deps.PostOperatorSigned == nil {
		return nil
	}
	if channelID == "" {
		// No channel — skip silently (not an error condition).
		return nil
	}

	region := ""
	if deps.Cfg != nil {
		region = deps.Cfg.PrimaryRegion
	}

	profileName := ""
	if deps.Profile != nil {
		profileName = deps.Profile.Metadata.Name
	}

	// Compose announcement message.
	msg := fmt.Sprintf(
		"Sandbox `%s` ready. Reply here or in any thread to give it a task.\n"+
			"_Profile: %s · Region: %s · Reply with a prompt to start a Claude turn._",
		deps.SandboxID, profileName, region,
	)

	// Post via the existing operator-signed bridge action.
	messageTS, err := deps.PostOperatorSigned(ctx, channelID, msg)
	if err != nil {
		// Non-fatal: log WARN and continue.
		fmt.Fprintf(os.Stderr, "  ⚠ Slack: ready announcement failed: %v (km create continues)\n", err)
		return nil
	}
	fmt.Fprintf(os.Stderr, "  ✓ Slack: posted ready announcement (ts=%s)\n", messageTS)

	// Anchor in km-slack-threads with empty claude_session_id.
	if deps.UpsertSlackThread != nil && messageTS != "" {
		if upsertErr := deps.UpsertSlackThread(ctx, channelID, messageTS, deps.SandboxID); upsertErr != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ Slack: thread anchor write failed: %v (non-fatal)\n", upsertErr)
		}
	}
	return nil
}

// makePostOperatorSigned returns a PostOperatorSigned callback that loads the
// operator signing key from SSM, builds an envelope, signs it, and posts it to
// the bridge Lambda. Returns the Slack message timestamp (ts) on success.
//
// This is the production factory for slackInboundDeps.PostOperatorSigned.
// It mirrors the signing pattern in destroy_slack.go (destroySlackChannel).
func makePostOperatorSigned(ssmClient *ssm.Client, bridgeURL string) func(ctx context.Context, channelID, body string) (string, error) {
	return func(ctx context.Context, channelID, body string) (string, error) {
		if bridgeURL == "" {
			return "", fmt.Errorf("PostOperatorSigned: bridge URL is empty")
		}
		priv, err := loadSlackOperatorKey(ctx, ssmClient)
		if err != nil {
			return "", fmt.Errorf("PostOperatorSigned: load key: %w", err)
		}
		env, err := slackpkg.BuildEnvelope(slackpkg.ActionPost, slackpkg.SenderOperator, channelID, "Sandbox Ready", body, "")
		if err != nil {
			return "", fmt.Errorf("PostOperatorSigned: build envelope: %w", err)
		}
		_, sig, err := slackpkg.SignEnvelope(env, ed25519.PrivateKey(priv))
		if err != nil {
			return "", fmt.Errorf("PostOperatorSigned: sign envelope: %w", err)
		}
		resp, err := slackpkg.PostToBridge(ctx, bridgeURL, env, sig)
		if err != nil {
			return "", fmt.Errorf("PostOperatorSigned: post to bridge: %w", err)
		}
		return resp.TS, nil
	}
}

// makeUpsertSlackThread returns a UpsertSlackThread callback that writes a
// km-slack-threads anchor row using the existing bridge DDBThreadStore.
// Reuses pkg/slack/bridge.DDBThreadStore.Upsert so the schema stays consistent.
func makeUpsertSlackThread(ddbClient slackbridge.DDBQueryGetPutAPI, tableName string) func(ctx context.Context, channelID, threadTS, sandboxID string) error {
	store := &slackbridge.DDBThreadStore{Client: ddbClient, TableName: tableName}
	return store.Upsert
}
