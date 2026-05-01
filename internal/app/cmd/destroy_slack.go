// Package cmd — destroy_slack.go
// Slack teardown logic for km destroy: posts a final sandbox-destroyed message
// to the per-sandbox channel and (when SlackArchiveOnDestroy is nil or &true)
// archives the channel via the bridge Lambda.
//
// Called by destroy.go immediately before the DynamoDB record is deleted.
// All failures are non-fatal and logged as warnings — km destroy continues
// even if the Slack teardown step fails.
//
// Plan 63-09 (km destroy) wires the call site in destroy.go. This file
// provides the helper function.
package cmd

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/slack"
)

// BridgePosterFunc is the function type used to post to the Slack bridge
// Lambda. The default implementation calls slack.PostToBridge; tests inject a
// fake to avoid real HTTP calls.
type BridgePosterFunc func(ctx context.Context, bridgeURL string, env *slack.SlackEnvelope, sig []byte) (*slack.PostResponse, error)

// PrivKeyLoaderFunc loads the operator's Ed25519 private key from SSM for
// signing bridge envelopes. Tests inject a fake that returns a test key.
// The region parameter is used to pick the correct SSM endpoint.
type PrivKeyLoaderFunc func(ctx context.Context, region string) (ed25519.PrivateKey, error)

// destroySlackChannel posts a final "sandbox destroyed" message to the
// per-sandbox channel and optionally archives it.
//
// Decision matrix (Plan 63-09 spec):
//
//   - meta.SlackChannelID == "" → return nil (no-op; Slack was not configured).
//   - meta.SlackPerSandbox == false → return nil (shared/override channel; don't archive).
//   - bridgeURL SSM unset → log WARN, return nil.
//   - key load fails → log WARN, return nil.
//   - final post fails → log WARN, return nil (skip archive).
//   - archive: nil or &true → attempt archive; &false → skip.
//   - archive failure → log WARN, return nil.
//
// All failure paths are non-fatal: km destroy must complete even if the Slack
// step fails (infra teardown is already underway by the time this is called).
func destroySlackChannel(
	ctx context.Context,
	meta *kmaws.SandboxMetadata,
	region string,
	ssmStore SSMParamStore,
	keyLoader PrivKeyLoaderFunc,
	bridgePoster BridgePosterFunc,
) error {
	// Case A — no channel configured.
	if meta.SlackChannelID == "" {
		fmt.Fprintf(os.Stderr, "  Slack: no channel configured — teardown notification skipped\n")
		return nil
	}

	// Case B — shared/override mode: don't post teardown or archive.
	if !meta.SlackPerSandbox {
		fmt.Fprintf(os.Stderr, "  Slack: shared/override mode — teardown notification skipped\n")
		return nil
	}

	// Fetch bridge URL from SSM.
	bridgeURL, _ := ssmStore.Get(ctx, "/km/slack/bridge-url", false)
	if bridgeURL == "" {
		// Case F — bridge URL not configured.
		log.Warn().Str("sandbox_id", meta.SandboxID).
			Msg("destroySlackChannel: /km/slack/bridge-url not configured; skipping teardown notification")
		fmt.Fprintf(os.Stderr, "  ⚠ Slack: /km/slack/bridge-url not configured — teardown notification skipped\n")
		return nil
	}

	// Load operator signing key.
	priv, err := keyLoader(ctx, region)
	if err != nil {
		// Case G — key load failed.
		log.Warn().Err(err).Str("sandbox_id", meta.SandboxID).
			Msg("destroySlackChannel: failed to load operator signing key; skipping teardown notification")
		fmt.Fprintf(os.Stderr, "  ⚠ Slack: failed to load operator signing key — teardown notification skipped: %v\n", err)
		return nil
	}

	// Build and post final "sandbox destroyed" message.
	body := fmt.Sprintf("Sandbox `%s` has been destroyed.", meta.SandboxID)
	if meta.Alias != "" {
		body = fmt.Sprintf("Sandbox `%s` (%s) has been destroyed.", meta.SandboxID, meta.Alias)
	}
	finalEnv, envErr := slack.BuildEnvelope(slack.ActionPost, slack.SenderOperator,
		meta.SlackChannelID, "Sandbox Destroyed", body, "")
	if envErr != nil {
		log.Warn().Err(envErr).Str("sandbox_id", meta.SandboxID).
			Msg("destroySlackChannel: failed to build final-post envelope")
		return nil
	}

	_, finalSig, sigErr := slack.SignEnvelope(finalEnv, priv)
	if sigErr != nil {
		log.Warn().Err(sigErr).Str("sandbox_id", meta.SandboxID).
			Msg("destroySlackChannel: failed to sign final-post envelope")
		return nil
	}

	if _, postErr := bridgePoster(ctx, bridgeURL, finalEnv, finalSig); postErr != nil {
		// Case H — final post failed; skip archive.
		log.Warn().Err(postErr).Str("sandbox_id", meta.SandboxID).
			Msg("destroySlackChannel: final-post bridge call failed; skipping archive")
		fmt.Fprintf(os.Stderr, "  ⚠ Slack: final-post bridge call failed — archive skipped: %v\n", postErr)
		return nil
	}
	fmt.Fprintf(os.Stderr, "  ✓ Slack: posted teardown message to %s\n", meta.SlackChannelID)

	// Determine whether to archive (nil = default true; &false = skip).
	shouldArchive := meta.SlackArchiveOnDestroy == nil || *meta.SlackArchiveOnDestroy
	if !shouldArchive {
		// Case E — explicit archive=false.
		fmt.Fprintf(os.Stderr, "  Slack: archive disabled (slackArchiveOnDestroy=false) — channel %s kept\n", meta.SlackChannelID)
		return nil
	}

	// Build and post archive action.
	archEnv, archEnvErr := slack.BuildEnvelope(slack.ActionArchive, slack.SenderOperator,
		meta.SlackChannelID, "", "", "")
	if archEnvErr != nil {
		log.Warn().Err(archEnvErr).Str("sandbox_id", meta.SandboxID).
			Msg("destroySlackChannel: failed to build archive envelope")
		return nil
	}

	_, archSig, archSigErr := slack.SignEnvelope(archEnv, priv)
	if archSigErr != nil {
		log.Warn().Err(archSigErr).Str("sandbox_id", meta.SandboxID).
			Msg("destroySlackChannel: failed to sign archive envelope")
		return nil
	}

	if _, archErr := bridgePoster(ctx, bridgeURL, archEnv, archSig); archErr != nil {
		// Case I — archive failed; non-fatal.
		log.Warn().Err(archErr).Str("sandbox_id", meta.SandboxID).
			Msg("destroySlackChannel: archive bridge call failed (non-fatal)")
		fmt.Fprintf(os.Stderr, "  ⚠ Slack: archive of %s failed (non-fatal): %v\n", meta.SlackChannelID, archErr)
		return nil
	}

	fmt.Fprintf(os.Stderr, "  ✓ Slack: archived channel %s\n", meta.SlackChannelID)
	return nil
}
