package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/rs/zerolog/log"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
	"github.com/whereiskurt/klankrmkr/pkg/slack"
)

// ssmParamStoreClient is the minimal SSM interface needed by productionSSMParamStore.
type ssmParamStoreClient interface {
	GetParameter(ctx context.Context, input *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// productionSSMParamStore adapts an SSM client to the SSMParamStore interface.
// Used by destroy.go and doctor.go to pass a real SSM client as SSMParamStore.
type productionSSMParamStore struct {
	client ssmParamStoreClient
}

func (s *productionSSMParamStore) Get(ctx context.Context, name string, withDecryption bool) (string, error) {
	out, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(name),
		WithDecryption: awssdk.Bool(withDecryption),
	})
	if err != nil {
		var notFound *ssmtypes.ParameterNotFound
		if errors.As(err, &notFound) {
			return "", nil // treat missing as empty
		}
		return "", err
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", nil
	}
	return *out.Parameter.Value, nil
}

// SlackAPI is the operator-side Slack client interface used during km create.
// *slack.Client satisfies this interface.
//
// FindChannelByName + JoinChannel were added so per-sandbox channel creation
// can recover from the same name_taken failure mode km slack init already
// handles: the channel exists in Slack from a prior create attempt, but the
// bot isn't a member (Slack App reinstalls drop bot membership). Without
// auto-recovery, every operator who hit this had to either manually delete
// the channel, archive it, or invent a new --alias.
type SlackAPI interface {
	CreateChannel(ctx context.Context, name string) (string, error)
	FindChannelByName(ctx context.Context, name string) (string, error)
	JoinChannel(ctx context.Context, channelID string) error
	InviteShared(ctx context.Context, channelID, email string) error
	ChannelInfo(ctx context.Context, channelID string) (memberCount int, isMember bool, err error)
}

// SSMParamStore is a narrow interface for reading SSM parameters. Used by
// resolveSlackChannel to fetch /km/slack/* config without importing the full
// SSM SDK into test files.
type SSMParamStore interface {
	Get(ctx context.Context, name string, withDecryption bool) (string, error)
}

// SSMRunner is a narrow interface for running shell commands on a sandbox
// instance via SSM SendCommand. Used by injectSlackEnvIntoSandbox.
type SSMRunner interface {
	RunShell(ctx context.Context, instanceID string, script string) error
}

var channelIDRe = regexp.MustCompile(`^C[A-Z0-9]+$`)

// resolveSlackChannel determines the Slack channel ID and per-sandbox flag for
// a sandbox being created. Returns ("", false, nil) when notifySlackEnabled is
// false or unset — no Slack work is done.
//
// Three modes (mutually exclusive per schema validation in Plan 01):
//
//   - Mode 1 (shared, default): NotifySlackPerSandbox=false AND
//     NotifySlackChannelOverride=="" → read /km/slack/shared-channel-id from
//     SSM; no Slack API calls.
//
//   - Mode 2 (per-sandbox): NotifySlackPerSandbox=true → sanitize
//     alias/sandboxID into a Slack-legal name; conversations.create;
//     conversations.inviteShared with /km/slack/invite-email; perSandbox=true.
//
//   - Mode 3 (override): NotifySlackChannelOverride != "" → validate the
//     channel ID format + confirm bot membership via ChannelInfo; perSandbox=false
//     (operator-controlled channel — do not archive at destroy).
func resolveSlackChannel(ctx context.Context, p *profile.SandboxProfile, sandboxID, alias string,
	api SlackAPI, ssmStore SSMParamStore, ssmPrefix string) (channelID string, perSandbox bool, err error) {
	slackPrefix := ssmPrefix + "slack/"

	cli := p.Spec.CLI
	if cli == nil || cli.NotifySlackEnabled == nil || !*cli.NotifySlackEnabled {
		return "", false, nil
	}

	// Mode 3 — override: operator-controlled channel.
	if cli.NotifySlackChannelOverride != "" {
		if !channelIDRe.MatchString(cli.NotifySlackChannelOverride) {
			return "", false, fmt.Errorf("notifySlackChannelOverride %q does not match ^C[A-Z0-9]+$", cli.NotifySlackChannelOverride)
		}
		_, isMember, infoErr := api.ChannelInfo(ctx, cli.NotifySlackChannelOverride)
		if infoErr != nil {
			return "", false, fmt.Errorf("validate override channel %s: %w", cli.NotifySlackChannelOverride, infoErr)
		}
		if !isMember {
			return "", false, fmt.Errorf("bot is not a member of %s — invite km-bot to the channel first", cli.NotifySlackChannelOverride)
		}
		// perSandbox=false: operator-controlled channel should never be archived.
		return cli.NotifySlackChannelOverride, false, nil
	}

	// Mode 2 — per-sandbox: create a dedicated channel for this sandbox.
	if cli.NotifySlackPerSandbox {
		nameSeed := alias
		if nameSeed == "" {
			nameSeed = sandboxID
		}
		sanitized := sanitizeChannelName(nameSeed)
		if sanitized == "" {
			return "", false, fmt.Errorf("could not derive Slack channel name from alias/sandboxID %q", nameSeed)
		}
		// Always prefix per-sandbox channels with "sb-" to namespace them.
		// This matches the #sb-{alias} or #sb-{id} naming from CONTEXT.md.
		channelName := sanitized
		if !strings.HasPrefix(channelName, "sb-") {
			channelName = "sb-" + channelName
		}
		if len(channelName) > 80 {
			channelName = channelName[:80]
			channelName = strings.TrimRight(channelName, "-")
		}

		chID, createErr := api.CreateChannel(ctx, channelName)
		var apierr *slack.SlackAPIError
		nameTaken := errors.As(createErr, &apierr) && apierr.Code == "name_taken"

		switch {
		case createErr != nil && !nameTaken:
			return "", false, fmt.Errorf("create channel #%s: %w", channelName, createErr)

		case nameTaken:
			// Channel exists in Slack but isn't tracked here — typically because
			// a prior create attempt for the same alias already created it (the
			// failure mode km slack init also recovers from). Look up the existing
			// channel and reuse rather than failing the whole sandbox provisioning.
			existingID, lookupErr := api.FindChannelByName(ctx, channelName)
			if lookupErr != nil {
				return "", false, fmt.Errorf("channel #%s exists (name_taken) but lookup via conversations.list failed: %w\n"+
					"Either grant the bot the channels:read scope and retry, or pick a unique --alias / use notifySlackChannelOverride", channelName, lookupErr)
			}
			if existingID == "" {
				return "", false, fmt.Errorf("channel name #%s is reserved (likely by an archived channel within Slack's 30-day window); pick a unique --alias or unarchive the existing channel", channelName)
			}
			chID = existingID
		}

		// Ensure the bot is in the channel. Required because:
		//   - Brand-new channel: Slack auto-joins the creator bot, but a Slack
		//     App reinstall later drops the bot out.
		//   - Reused channel (name_taken path above): bot may have been kicked
		//     or never joined under the current bot session.
		// Without this, chat.postMessage from the bridge fails with
		// not_in_channel even though the channel exists.
		if joinErr := api.JoinChannel(ctx, chID); joinErr != nil {
			isAPIErr := errors.As(joinErr, &apierr)
			switch {
			case isAPIErr && apierr.Code == "missing_scope":
				return "", false, fmt.Errorf("bot needs channels:join scope to ensure membership in #%s (channel %s): %w\n"+
					"Add the scope in Slack App config → OAuth & Permissions, reinstall the app, then re-run km slack rotate-token", channelName, chID, joinErr)
			case isAPIErr && apierr.Code == "is_archived":
				return "", false, fmt.Errorf("channel #%s (%s) is archived; pick a different --alias or unarchive it via:\n"+
					"  curl -H \"Authorization: Bearer $BOT_TOKEN\" -d \"channel=%s\" https://slack.com/api/conversations.unarchive",
					channelName, chID, chID)
			default:
				log.Warn().Err(joinErr).Str("channel", chID).Msg("auto-join channel failed (non-fatal); /invite the bot manually if needed")
			}
		}

		// Fetch the invite email from SSM so the bot can receive cross-workspace invites.
		inviteEmail, ssmErr := ssmStore.Get(ctx, slackPrefix+"invite-email", false)
		if ssmErr != nil || inviteEmail == "" {
			// Missing invite-email is configurational — the channel exists and
			// the bot is in it, so treat as warning rather than failing the
			// whole create. Operator can run `km slack init` later.
			log.Warn().Str("channel", chID).Msgf("Slack invite-email not configured at %sinvite-email; skipping cross-workspace invite (run km slack init to set)", slackPrefix)
			return chID, true, nil
		}

		if inviteErr := api.InviteShared(ctx, chID, inviteEmail); inviteErr != nil {
			// Invite failure is non-fatal: the channel is live, the bot is in
			// it, sandbox notifications will still flow. The cross-workspace
			// invite is a convenience for the operator's external Slack;
			// failing here used to abort sandbox provisioning, which was
			// disproportionate (the failure typically means the operator
			// already accepted the invite, the workspace isn't on Pro tier,
			// or the email already has a connection).
			log.Warn().Err(inviteErr).Str("channel", chID).Str("email", inviteEmail).Msg("Slack Connect invite failed (non-fatal — channel and bot are healthy; manually re-invite if needed)")
		}

		return chID, true, nil
	}

	// Mode 1 — shared (default): read channel ID from SSM.
	chID, ssmErr := ssmStore.Get(ctx, slackPrefix+"shared-channel-id", false)
	if ssmErr != nil || chID == "" {
		return "", false, fmt.Errorf("%sshared-channel-id not set — run km slack init first", slackPrefix)
	}
	return chID, false, nil
}

// sanitizeChannelName produces a Slack-legal channel name fragment from a
// free-form alias or sandbox ID. Slack rules: 1-80 chars, lowercase letters,
// digits, hyphens, underscores only.
//
// Transformations applied:
//   - Convert to lowercase.
//   - Replace any character that is not [a-z0-9_] with a hyphen.
//   - Collapse consecutive hyphens into a single hyphen.
//   - Trim leading/trailing hyphens.
//   - Cap at 80 characters (trimming trailing hyphens after truncation).
//
// Returns "" for unrecoverable inputs (empty after sanitization).
func sanitizeChannelName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
			prevHyphen = false
		} else if !prevHyphen {
			b.WriteRune('-')
			prevHyphen = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 80 {
		out = out[:80]
		out = strings.TrimRight(out, "-")
	}
	return out
}

// writeSlackChannelIDToSSM writes the resolved Slack channel ID to
// /{resource_prefix}/sandbox/{id}/slack-channel-id. The sandbox's cloud-init
// bootstrap polls this path (with the operator-wide bridge URL) and writes
// both values to /etc/profile.d/km-slack-runtime.sh so the inbound poller
// and Stop hook can source them.
//
// Replaces injectSlackEnvIntoSandbox (ssm:SendCommand, denied by org-level SCP
// for the application account). Phase 67 gap closure.
func writeSlackChannelIDToSSM(ctx context.Context, putParam func(ctx context.Context, name, value string) error, resourcePrefix, sandboxID, channelID string) error {
	return putParam(ctx, kmaws.SandboxParameterPath(resourcePrefix, sandboxID, "slack-channel-id"), channelID)
}

// ssmSendCommandClient is the minimal SSM interface needed by productionSSMRunner.
type ssmSendCommandClient interface {
	SendCommand(ctx context.Context, input *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
}

// productionSSMRunner implements SSMRunner using AWS SSM SendCommand.
// Used by injectSlackEnvIntoSandbox to push env vars into a running sandbox.
type productionSSMRunner struct {
	client ssmSendCommandClient
}

func (r *productionSSMRunner) RunShell(ctx context.Context, instanceID string, script string) error {
	_, err := r.client.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{instanceID},
		DocumentName: awssdk.String("AWS-RunShellScript"),
		Parameters: map[string][]string{
			"commands": {script},
		},
		TimeoutSeconds: awssdk.Int32(30),
	})
	return err
}

// printTranscriptWarning emits a single audience-containment warning to stderr
// when notifySlackTranscriptEnabled resolves to true at km create time. Includes
// the resolved channel ID and the current Slack member count (fetched via the
// Phase 67 ChannelInfo helper). Non-blocking: any ChannelInfo error degrades to
// "Audience: unknown Slack users" but does NOT fail km create.
//
// Phase 68 Plan 10 — operators must see this warning early enough to abort
// (Ctrl-C) before the sandbox provisions and starts streaming transcripts that
// may include sensitive tool I/O.
func printTranscriptWarning(ctx context.Context, api SlackAPI, channelID string) {
	memberCount := "unknown"
	if api != nil {
		members, _, err := api.ChannelInfo(ctx, channelID)
		if err == nil && members > 0 {
			memberCount = fmt.Sprintf("%d", members)
		}
	}
	fmt.Fprintf(os.Stderr,
		"⚠ Slack transcript streaming enabled — full Claude transcripts (including tool I/O) will be posted to channel %s. Audience: %s Slack users.\n",
		channelID, memberCount,
	)
}

// runStep11dInject publishes the resolved Slack channel ID to SSM Parameter
// Store at /sandbox/{id}/slack-channel-id so the sandbox's cloud-init bootstrap
// can pick it up alongside the operator-wide /km/slack/bridge-url.
//
// Replaces the previous ssm:SendCommand-based injection (denied by org-level
// SCP for the application account). Non-fatal on failure: the sandbox is
// already provisioned; the bootstrap step will emit a WARN if the param never
// appears.
//
// The retryMax/retryDelay parameters are kept on the signature for source
// compatibility with existing call sites and tests but aren't used — a single
// PutParameter call is enough.
func runStep11dInject(
	ctx context.Context,
	ssmStore SSMParamStore,
	putParam func(ctx context.Context, name, value string) error,
	sandboxID, slackChannelID string,
	retryMax int,
	retryDelay time.Duration,
	ssmPrefix string,
) {
	_ = retryMax
	_ = retryDelay
	bridgeURLPath := ssmPrefix + "slack/bridge-url"
	bridgeURL, _ := ssmStore.Get(ctx, bridgeURLPath, false)
	if bridgeURL == "" {
		log.Warn().Str("sandbox_id", sandboxID).
			Msg("Step 11d: bridge-url SSM param not configured — Slack env not published (run km slack init)")
		fmt.Fprintf(os.Stderr, "  ⚠ Slack: %s not configured — env not published (run km slack init)\n", bridgeURLPath)
		return
	}
	if err := writeSlackChannelIDToSSM(ctx, putParam, strings.Trim(ssmPrefix, "/"), sandboxID, slackChannelID); err != nil {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).
			Msg("Step 11d: failed to write slack-channel-id to SSM Parameter Store (non-fatal — sandbox is provisioned)")
		fmt.Fprintf(os.Stderr, "  ⚠ Slack: SSM PutParameter failed — env not published (non-fatal): %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "  ✓ Slack: channel %s published to SSM Parameter Store\n", slackChannelID)
}
