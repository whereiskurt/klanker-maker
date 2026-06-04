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
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// ssmParamStoreClient is the minimal SSM interface needed by productionSSMParamStore.
type ssmParamStoreClient interface {
	GetParameter(ctx context.Context, input *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
	PutParameter(ctx context.Context, input *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
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

// Put writes (overwriting) an SSM String parameter. secure=true switches to a
// SecureString (no caller currently needs it for the Slack channel cache, but
// the signature matches the slackSSMStore.Put convention used elsewhere).
//
// productionSSMParamStore is the only SSMParamStore implementation with write
// capability; the read-only fakes used by destroy/doctor satisfy the narrower
// SSMParamStore contract and intentionally do NOT implement ssmParamWriter, so
// the by-name channel cache is silently skipped on those paths.
func (s *productionSSMParamStore) Put(ctx context.Context, name, value string, secure bool) error {
	input := &ssm.PutParameterInput{
		Name:      awssdk.String(name),
		Value:     awssdk.String(value),
		Type:      ssmtypes.ParameterTypeString,
		Overwrite: awssdk.Bool(true),
	}
	if secure {
		input.Type = ssmtypes.ParameterTypeSecureString
	}
	_, err := s.client.PutParameter(ctx, input)
	if err != nil {
		return fmt.Errorf("SSM PutParameter %s: %w", name, err)
	}
	return nil
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
	// Phase 72 invite orchestrator methods (also implements slack.InviteAPI).
	LookupUserByEmail(ctx context.Context, email string) (userID string, found bool, err error)
	InviteUserToChannelStrict(ctx context.Context, channelID, userID string) error
}

// SSMParamStore is a narrow interface for reading SSM parameters. Used by
// resolveSlackChannel to fetch /km/slack/* config without importing the full
// SSM SDK into test files.
type SSMParamStore interface {
	Get(ctx context.Context, name string, withDecryption bool) (string, error)
}

// ssmParamWriter is the optional write capability of an SSMParamStore. It is
// implemented by productionSSMParamStore (the real create-path store) and by
// the create-path test fake. Read-only stores used by destroy/doctor do NOT
// implement it; resolveSlackChannel type-asserts for it and silently skips the
// by-name channel cache when absent, so those paths stay read-only.
type ssmParamWriter interface {
	Put(ctx context.Context, name, value string, secure bool) error
}

// slackChannelNameCacheKey returns the SSM path that maps a Slack channel NAME
// to its channel ID, e.g. "/km/slack/channel-id-by-name/sb-demo".
//
// This is the O(1) reuse path for per-sandbox channels: the sandbox-id-keyed
// param (/{prefix}/sandbox/{id}/slack-channel-id) is useless for reuse because
// the sandbox id is regenerated on every recreate, whereas the channel name is
// re-derived deterministically from the (stable) --alias. Caching by name lets
// a name_taken recovery resolve the existing channel without enumerating every
// public channel via conversations.list (the O(N) scan that gets rate-limited
// in large workspaces).
func slackChannelNameCacheKey(slackPrefix, channelName string) string {
	return slackPrefix + "channel-id-by-name/" + channelName
}

// cacheSlackChannelIDByName best-effort writes the name→ID mapping. Non-fatal:
// a cache write failure (or a read-only store) only costs a future scan, it
// never fails sandbox provisioning.
func cacheSlackChannelIDByName(ctx context.Context, store SSMParamStore, slackPrefix, channelName, channelID string) {
	writer, ok := store.(ssmParamWriter)
	if !ok || channelID == "" {
		return
	}
	key := slackChannelNameCacheKey(slackPrefix, channelName)
	if err := writer.Put(ctx, key, channelID, false); err != nil {
		log.Debug().Err(err).Str("key", key).Str("channel", channelID).
			Msg("could not cache Slack channel ID by name (non-fatal — next recreate falls back to scan)")
	}
}

// resolveExistingChannelID recovers the ID of an already-existing channel after
// CreateChannel returned name_taken, in priority order:
//
//  1. By-name SSM cache (O(1)) — if a cached ID resolves via conversations.info
//     (single call), reuse it directly. A deleted/renamed channel fails the
//     conversations.info probe and falls through to the scan.
//  2. FindChannelByName enumeration (O(N)) — the fallback. On success the result
//     is written back to the cache so the next recreate is O(1). On a rate-limit
//     failure mid-scan the error is rate-limit-specific (NOT a channels:read
//     scope hint, which would be misleading). An empty result means the name is
//     reserved by an archived channel (Slack's 30-day window).
func resolveExistingChannelID(ctx context.Context, api SlackAPI, ssmStore SSMParamStore, slackPrefix, channelName string) (string, error) {
	// 1. By-name cache.
	if cachedID, _ := ssmStore.Get(ctx, slackChannelNameCacheKey(slackPrefix, channelName), false); cachedID != "" {
		if _, _, infoErr := api.ChannelInfo(ctx, cachedID); infoErr == nil {
			log.Debug().Str("channel", cachedID).Str("name", channelName).
				Msg("reused Slack channel ID from by-name SSM cache (skipped conversations.list scan)")
			return cachedID, nil
		}
		// Cache stale (channel deleted/renamed) — fall through to the scan.
		log.Debug().Str("channel", cachedID).Str("name", channelName).
			Msg("cached Slack channel ID no longer resolves; falling back to conversations.list scan")
	}

	// 2. Enumeration fallback.
	existingID, lookupErr := api.FindChannelByName(ctx, channelName)
	if lookupErr != nil {
		var apierr *slack.SlackAPIError
		if errors.As(lookupErr, &apierr) && apierr.Code == "ratelimited" {
			return "", fmt.Errorf("channel #%s exists but resolving its ID timed out: Slack rate-limited conversations.list while scanning the workspace. "+
				"Retry shortly, or set notification.slack.channelOverride to the channel ID: %w", channelName, lookupErr)
		}
		return "", fmt.Errorf("channel #%s exists (name_taken) but lookup via conversations.list failed: %w\n"+
			"Either grant the bot the channels:read scope and retry, or pick a unique --alias / use notification.slack.channelOverride", channelName, lookupErr)
	}
	if existingID == "" {
		return "", fmt.Errorf("channel name #%s is reserved (likely by an archived channel within Slack's 30-day window); pick a unique --alias or unarchive the existing channel", channelName)
	}
	// Persist so the next recreate skips the scan entirely.
	cacheSlackChannelIDByName(ctx, ssmStore, slackPrefix, channelName, existingID)
	return existingID, nil
}

// SSMRunner is a narrow interface for running shell commands on a sandbox
// instance via SSM SendCommand. Used by injectSlackEnvIntoSandbox.
type SSMRunner interface {
	RunShell(ctx context.Context, instanceID string, script string) error
}

var channelIDRe = regexp.MustCompile(`^C[A-Z0-9]+$`)

// notificationSlack returns p.Spec.Notification.Slack (nil-safe). Phase 92:
// Slack delivery config lives under spec.notification.slack.*.
func notificationSlack(p *profile.SandboxProfile) *profile.NotificationSlackSpec {
	if p == nil || p.Spec.Notification == nil {
		return nil
	}
	return p.Spec.Notification.Slack
}

// notificationSlackInbound returns p.Spec.Notification.Slack.Inbound (nil-safe).
func notificationSlackInbound(p *profile.SandboxProfile) *profile.NotificationSlackInboundSpec {
	sl := notificationSlack(p)
	if sl == nil {
		return nil
	}
	return sl.Inbound
}

// runtimeVSCode returns p.Spec.Runtime.VSCode (nil-safe). Phase 92: the VS Code
// gate moved from spec.cli.vscodeEnabled to spec.runtime.vscode.enabled.
func runtimeVSCode(p *profile.SandboxProfile) *profile.RuntimeVSCodeSpec {
	if p == nil {
		return nil
	}
	return p.Spec.Runtime.VSCode
}

// resolveSlackChannel determines the Slack channel ID and per-sandbox flag for
// a sandbox being created. Returns ("", false, nil) when notifySlackEnabled is
// false or unset — no Slack work is done.
//
// Three modes (mutually exclusive per schema validation in Plan 01):
//
//   - Mode 1 (shared, default): notification.slack.perSandbox=false AND
//     notification.slack.channelOverride=="" → read /km/slack/shared-channel-id from
//     SSM; no Slack API calls.
//
//   - Mode 2 (per-sandbox): notification.slack.perSandbox=true → sanitize
//     alias/sandboxID into a Slack-legal name; conversations.create;
//     conversations.inviteShared with /km/slack/invite-email; perSandbox=true.
//
//   - Mode 3 (override): notification.slack.channelOverride != "" → validate the
//     channel ID format + confirm bot membership via ChannelInfo; perSandbox=false
//     (operator-controlled channel — do not archive at destroy).
func resolveSlackChannel(ctx context.Context, p *profile.SandboxProfile, sandboxID, alias string,
	api SlackAPI, ssmStore SSMParamStore, ssmPrefix string) (channelID string, perSandbox bool, err error) {
	slackPrefix := ssmPrefix + "slack/"

	// Phase 92: Slack delivery config moved from spec.cli.notify* to
	// spec.notification.slack.*.
	sl := notificationSlack(p)
	if sl == nil || sl.Enabled == nil || !*sl.Enabled {
		return "", false, nil
	}

	// Mode 3 — override: operator-controlled channel.
	if sl.ChannelOverride != "" {
		if !channelIDRe.MatchString(sl.ChannelOverride) {
			return "", false, fmt.Errorf("notification.slack.channelOverride %q does not match ^C[A-Z0-9]+$", sl.ChannelOverride)
		}
		_, isMember, infoErr := api.ChannelInfo(ctx, sl.ChannelOverride)
		if infoErr != nil {
			return "", false, fmt.Errorf("validate override channel %s: %w", sl.ChannelOverride, infoErr)
		}
		if !isMember {
			return "", false, fmt.Errorf("bot is not a member of %s — invite km-bot to the channel first", sl.ChannelOverride)
		}
		// perSandbox=false: operator-controlled channel should never be archived.
		return sl.ChannelOverride, false, nil
	}

	// Mode 2 — per-sandbox: create a dedicated channel for this sandbox.
	if sl.PerSandbox != nil && *sl.PerSandbox {
		channelName, derr := deriveSandboxChannelName(sl.ChannelName, p.Metadata.Name, alias, sandboxID)
		if derr != nil {
			return "", false, derr
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
			// failure mode km slack init also recovers from) and survived
			// archiveOnDestroy:false. Resolve the existing channel and reuse
			// rather than failing the whole sandbox provisioning.
			existingID, resolveErr := resolveExistingChannelID(ctx, api, ssmStore, slackPrefix, channelName)
			if resolveErr != nil {
				return "", false, resolveErr
			}
			chID = existingID

		default: // createErr == nil — fresh channel created.
			// Cache the name→ID mapping so a future recreate with the same --alias
			// hits the O(1) reuse path instead of enumerating every public channel.
			cacheSlackChannelIDByName(ctx, ssmStore, slackPrefix, channelName, chID)
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

		// Fetch the invite email from SSM so the operator is always invited.
		inviteEmail, ssmErr := ssmStore.Get(ctx, slackPrefix+"invite-email", false)
		if ssmErr != nil || inviteEmail == "" {
			// Missing invite-email is configurational — the channel exists and
			// the bot is in it, so treat as warning rather than failing the
			// whole create. Operator can run `km slack init` later.
			log.Warn().Str("channel", chID).Msgf("Slack invite-email not configured at %sinvite-email; skipping cross-workspace invite (run km slack init to set)", slackPrefix)
			return chID, true, nil
		}

		// Phase 72: route the primary operator invite through the unified
		// orchestrator (EnsureMemberByEmail). AutoConnect=true is UNCONDITIONAL
		// here — the operator is always invited regardless of useSlackConnect.
		// Native workspace member → conversations.invite (fixes corporate case);
		// external operator → Slack Connect (preserves prior PoC behavior).
		//
		// Pass channelName (e.g. "sb-test123") rather than the opaque Slack channel
		// ID so that SkippedExternal hints render as a usable `km slack invite
		// --channel sb-{name}` command.
		opRes, opErr := slack.EnsureMemberByEmail(ctx, api, chID, inviteEmail, slack.EnsureMemberOpts{
			Interactive: false,
			AutoConnect: true,
		})
		slackInviteResultWarn(opRes, opErr, inviteEmail, channelName)

		// Phase 72: profile-driven auto-invite for ADDITIONAL collaborators
		// (beyond the primary operator above). AutoConnect is gated by
		// useSlackConnect (nil ⇒ true). Interactive is always false — km create
		// may run from km at / scheduled contexts. Non-fatal throughout.
		var invites *profile.NotificationSlackInvitesSpec
		if sl != nil {
			invites = sl.Invites
		}
		autoConnect := invites == nil || invites.UseConnect == nil || *invites.UseConnect
		var inviteEmails []string
		if invites != nil {
			inviteEmails = invites.Emails
		}
		for _, email := range inviteEmails {
			email = strings.TrimSpace(email)
			if email == "" {
				continue
			}
			res, err := slack.EnsureMemberByEmail(ctx, api, chID, email, slack.EnsureMemberOpts{
				Interactive: false,
				AutoConnect: autoConnect,
				// Prompter intentionally nil — non-interactive path never calls it.
			})
			slackInviteResultWarn(res, err, email, channelName)
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

// slackInviteResultWarn maps a non-success EnsureMemberByEmail result to the
// appropriate fail-soft stderr warning. Returns nothing — km create never
// aborts on invite outcomes (CONTEXT.md fail-soft mandate).
//
// InvitedDirect/InvitedConnect/AlreadyMember are silent info successes.
// SkippedExternal emits a stderr hint so the operator knows to use
// `km slack invite --external` for manual Connect.
// Failed emits a stderr warning naming the email, channel, and error.
func slackInviteResultWarn(res slack.EnsureMemberResult, err error, email, channelID string) {
	switch res {
	case slack.InvitedDirect, slack.InvitedConnect:
		log.Info().Str("email", email).Str("channel", channelID).Str("result", res.String()).Msg("invited to Slack channel")
	case slack.AlreadyMember:
		log.Debug().Str("email", email).Str("channel", channelID).Msg("already in Slack channel — no-op")
	case slack.SkippedExternal:
		fmt.Fprintf(os.Stderr,
			"[warn] %s is not a member of the Slack workspace; not sending Connect invite (useSlackConnect: false).\n  To send one: km slack invite --external %s --channel %s\n",
			email, email, channelID,
		)
	case slack.Failed:
		fmt.Fprintf(os.Stderr,
			"[warn] Slack invite failed for %s on channel %s: %v (non-fatal — sandbox provisioning continues)\n",
			email, channelID, err,
		)
	}
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
// deriveSandboxChannelName computes the per-sandbox Slack channel name.
//
//   - custom != "": operator-chosen name. {profile}, {alias}, and {id} tokens are
//     substituted ({profile} = the profile's metadata.name; {alias} falls back to
//     the sandbox ID when no alias is set), then sanitized to Slack's rules and
//     used VERBATIM — no forced "sb-" prefix (the operator owns namespacing).
//   - custom == "": default derivation — sanitize(alias|id) force-prefixed with
//     "sb-" so per-sandbox channels are namespaced (#sb-{alias} / #sb-{id}).
//
// The result is always trimmed to Slack's 80-character channel-name limit.
func deriveSandboxChannelName(custom, profileName, alias, sandboxID string) (string, error) {
	var name string
	if custom != "" {
		aliasTok := alias
		if aliasTok == "" {
			aliasTok = sandboxID
		}
		seed := strings.ReplaceAll(custom, "{profile}", profileName)
		seed = strings.ReplaceAll(seed, "{alias}", aliasTok)
		seed = strings.ReplaceAll(seed, "{id}", sandboxID)
		name = sanitizeChannelName(seed)
		if name == "" {
			return "", fmt.Errorf("notification.slack.channelName %q sanitized to an empty channel name", custom)
		}
	} else {
		seed := alias
		if seed == "" {
			seed = sandboxID
		}
		s := sanitizeChannelName(seed)
		if s == "" {
			return "", fmt.Errorf("could not derive Slack channel name from alias/sandboxID %q", seed)
		}
		name = s
		if !strings.HasPrefix(name, "sb-") {
			name = "sb-" + name
		}
	}
	if len(name) > 80 {
		name = name[:80]
		name = strings.TrimRight(name, "-")
	}
	return name, nil
}

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
