package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
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
	FindChannelByName(ctx context.Context, name string, maxPages int) (string, error)
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

// lookupStoredChannelID returns a previously-stored channel ID for the alias,
// DDB-first (authoritative) then SSM by-name (back-compat). Empty alias ⇒ skip
// DDB (no stable reuse key). Returns ("", false) on miss. fromDDB is true only
// when the hit came from the authoritative DDB store — callers back-fill DDB on
// an SSM-sourced hit (fromDDB=false) so pre-104 channels migrate on first touch.
func lookupStoredChannelID(ctx context.Context, store SlackChannelStore, ssmStore SSMParamStore,
	slackPrefix, alias, channelName string) (id string, fromDDB bool) {
	if store != nil && alias != "" {
		if v, err := store.GetByAlias(ctx, alias); err == nil && v != "" {
			return v, true
		}
	}
	if v, _ := ssmStore.Get(ctx, slackChannelNameCacheKey(slackPrefix, channelName), false); v != "" {
		return v, false
	}
	return "", false
}

// storeChannelMapping write-throughs the name→ID binding to BOTH the durable DDB
// store (by alias) and the SSM by-name cache. Best-effort: never fails the create.
func storeChannelMapping(ctx context.Context, store SlackChannelStore, ssmStore SSMParamStore,
	slackPrefix, alias, channelName, channelID string) {
	if store != nil && alias != "" && channelID != "" {
		if err := store.UpsertByAlias(ctx, alias, channelID); err != nil {
			log.Debug().Err(err).Str("alias", alias).Msg("DDB channel mapping upsert failed (non-fatal)")
		}
	}
	cacheSlackChannelIDByName(ctx, ssmStore, slackPrefix, channelName, channelID)
}

// validateStoredChannel probes conversations.info with bounded retry. Returns:
//
//	ok=true             → channel live, use it
//	gone=true           → definitive channel_not_found, invalidate + recreate
//	ok=false,gone=false → transient after retries → caller optimistically uses the ID
func validateStoredChannel(ctx context.Context, api SlackAPI, channelID string) (ok, gone bool) {
	var lastErr error
	for attempt := 0; attempt <= slackInfoRetries; attempt++ {
		if _, _, err := api.ChannelInfo(ctx, channelID); err == nil {
			return true, false
		} else {
			lastErr = err
			if slack.IsChannelNotFound(err) {
				return false, true
			}
		}
		if attempt < slackInfoRetries {
			if sleepErr := slackResolveSleep(ctx, slackInfoRetryDelay); sleepErr != nil {
				break
			}
		}
	}
	log.Debug().Err(lastErr).Str("channel", channelID).
		Msg("conversations.info transient after retries — optimistically trusting stored ID")
	return false, false
}

// slackResolveFailFast returns the fail-fast error when a channel exists but km
// has no stored ID for it and enumeration is disabled (the default).
func slackResolveFailFast(channelName string) error {
	return fmt.Errorf("Slack channel #%s exists but km has no stored ID for it. "+
		"Seed the mapping with `km slack adopt <alias> <channelID>` (find the ID in Slack → channel → About → Channel ID), "+
		"or set notification.slack.channelOverride=<id>. "+
		"(Workspace enumeration is disabled by default; set KM_SLACK_MAX_SCAN_PAGES>0 to allow a bounded scan.)", channelName)
}

// SSMRunner is a narrow interface for running shell commands on a sandbox
// instance via SSM SendCommand. Used by injectSlackEnvIntoSandbox.
type SSMRunner interface {
	RunShell(ctx context.Context, instanceID string, script string) error
}

var channelIDRe = regexp.MustCompile(`^C[A-Z0-9]+$`)

// ─── Slack channel-resolution bounding knobs (Phase 104 P0/P1) ───────────────

// SlackResolveBudget caps total wall-clock for per-sandbox Slack channel
// resolution. Far below the 900s create-handler ceiling, far above a normal
// create+info round-trip. Override: KM_SLACK_RESOLVE_BUDGET (seconds).
var SlackResolveBudget = 45 * time.Second

// SlackMaxScanPages caps the conversations.list fallback. 0 = scan disabled
// (fail fast with adopt/channelOverride guidance) — the safe default for huge
// workspaces. Override: KM_SLACK_MAX_SCAN_PAGES.
var SlackMaxScanPages = 0

// slackInfoRetries is the bounded retry count for a transient conversations.info
// probe before optimistically trusting the stored ID.
var slackInfoRetries = 2

// slackInfoRetryDelay is the backoff between transient info retries.
var slackInfoRetryDelay = 500 * time.Millisecond

func init() {
	if v := os.Getenv("KM_SLACK_RESOLVE_BUDGET"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			SlackResolveBudget = time.Duration(secs) * time.Second
		}
	}
	if v := os.Getenv("KM_SLACK_MAX_SCAN_PAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			SlackMaxScanPages = n
		}
	}
}

// slackResolveSleep is a ctx-aware sleep for the resolve retry loop. Package-level
// so tests can swap it for a no-op.
var slackResolveSleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// SlackChannelStore is the durable alias→channelID mapping (Phase 104 P2). The
// DDB-backed implementation lives in pkg/aws; resolveSlackChannel reads it first
// and write-throughs on create/resolve. A nil store disables the DDB layer (SSM
// by-name cache still applies) so tests and prefix-less paths degrade cleanly.
type SlackChannelStore interface {
	GetByAlias(ctx context.Context, alias string) (channelID string, err error)
	UpsertByAlias(ctx context.Context, alias, channelID string) error
}

// ─────────────────────────────────────────────────────────────────────────────

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
	api SlackAPI, store SlackChannelStore, ssmStore SSMParamStore, ssmPrefix string) (channelID string, perSandbox bool, err error) {
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

	// Mode 2 — per-sandbox: bounded, lookup-first channel resolution (Phase 104 P0+P1).
	if sl.PerSandbox != nil && *sl.PerSandbox {
		// Wrap Mode-2 in a wall-clock budget so the create-handler can never wedge.
		ctx, cancel := context.WithTimeout(ctx, SlackResolveBudget)
		defer cancel()

		channelName, derr := deriveSandboxChannelName(sl.ChannelName, p.Metadata.Name, alias, sandboxID)
		if derr != nil {
			return "", false, derr
		}

		start := time.Now()
		path := "failfast"
		var resolvedID string
		defer func() {
			log.Info().
				Str("path", path).
				Int64("ms", time.Since(start).Milliseconds()).
				Str("id", resolvedID).
				Str("channel", channelName).
				Msg("slack_resolve")
		}()

		// ensureBotMemberAndInvite handles the common bot-join + primary-operator +
		// additional-collaborator invite sequence that follows any successful resolution.
		ensureBotMemberAndInvite := func(chID string) (string, bool, error) {
			var apierr *slack.SlackAPIError
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

			inviteEmail, ssmErr := ssmStore.Get(ctx, slackPrefix+"invite-email", false)
			if ssmErr != nil || inviteEmail == "" {
				log.Warn().Str("channel", chID).Msgf("Slack invite-email not configured at %sinvite-email; skipping cross-workspace invite (run km slack init to set)", slackPrefix)
				resolvedID = chID
				return chID, true, nil
			}

			// Phase 72: primary operator invite (always auto-connect).
			opRes, opErr := slack.EnsureMemberByEmail(ctx, api, chID, inviteEmail, slack.EnsureMemberOpts{
				Interactive: false,
				AutoConnect: true,
			})
			slackInviteResultWarn(opRes, opErr, inviteEmail, channelName)

			// Phase 72: additional collaborators from profile.
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
				})
				slackInviteResultWarn(res, err, email, channelName)
			}

			resolvedID = chID
			return chID, true, nil
		}

		// ── Step 1: lookup-first ─────────────────────────────────────────────────
		if storedID, fromDDB := lookupStoredChannelID(ctx, store, ssmStore, slackPrefix, alias, channelName); storedID != "" {
			ok, gone := validateStoredChannel(ctx, api, storedID)
			switch {
			case ok:
				// cache_hit: stored ID is live → O(1), no create.
				path = "cache_hit"
				if !fromDDB {
					// SSM-sourced hit: promote into the authoritative DDB store
					// (migrate-on-touch for pre-104 / out-of-band channels).
					if store != nil && alias != "" {
						if upsertErr := store.UpsertByAlias(ctx, alias, storedID); upsertErr != nil {
							log.Debug().Err(upsertErr).Str("alias", alias).Msg("DDB back-fill on SSM hit failed (non-fatal)")
						}
					}
				}
				return ensureBotMemberAndInvite(storedID)

			case !gone:
				// cache_optimistic: transient info error after retries → trust stored ID.
				// The incident bug: this path previously fell through to an unbounded scan.
				path = "cache_optimistic"
				if !fromDDB {
					if store != nil && alias != "" {
						if upsertErr := store.UpsertByAlias(ctx, alias, storedID); upsertErr != nil {
							log.Debug().Err(upsertErr).Str("alias", alias).Msg("DDB back-fill on SSM hit (optimistic) failed (non-fatal)")
						}
					}
				}
				return ensureBotMemberAndInvite(storedID)

			default:
				// gone: definitive channel_not_found → fall through to create.
				log.Debug().Str("stored_id", storedID).Str("channel", channelName).
					Msg("stored channel ID no longer exists (channel_not_found); recreating")
			}
		}

		// ── Step 2: create ───────────────────────────────────────────────────────
		chID, createErr := api.CreateChannel(ctx, channelName)
		var apierr *slack.SlackAPIError
		nameTaken := errors.As(createErr, &apierr) && apierr.Code == "name_taken"

		if createErr == nil {
			// Fresh channel created.
			path = "created"
			storeChannelMapping(ctx, store, ssmStore, slackPrefix, alias, channelName, chID)
			return ensureBotMemberAndInvite(chID)
		}

		if !nameTaken {
			return "", false, fmt.Errorf("create channel #%s: %w", channelName, createErr)
		}

		// ── Step 3: name_taken with no usable stored mapping ────────────────────
		// Enumeration is the last resort — disabled by default (SlackMaxScanPages=0).
		if SlackMaxScanPages > 0 {
			scannedID, scanErr := api.FindChannelByName(ctx, channelName, SlackMaxScanPages)
			switch {
			case scanErr == nil && scannedID != "":
				path = "scan_capped"
				storeChannelMapping(ctx, store, ssmStore, slackPrefix, alias, channelName, scannedID)
				return ensureBotMemberAndInvite(scannedID)
			case errors.Is(scanErr, slack.ErrScanCapExceeded) || (scanErr == nil && scannedID == ""):
				// Capped with no match, or scan empty (archived reservation).
				if scanErr == nil && scannedID == "" {
					return "", false, fmt.Errorf("channel name #%s is reserved (likely by an archived channel within Slack's 30-day window); pick a unique --alias or unarchive the existing channel", channelName)
				}
				return "", false, slackResolveFailFast(channelName)
			default:
				// Scan error (ratelimited, missing_scope, etc.).
				if errors.As(scanErr, &apierr) && apierr.Code == "ratelimited" {
					return "", false, fmt.Errorf("channel #%s exists but resolving its ID timed out: Slack rate-limited conversations.list while scanning the workspace. "+
						"Retry shortly, or set notification.slack.channelOverride to the channel ID: %w", channelName, scanErr)
				}
				return "", false, fmt.Errorf("channel #%s exists (name_taken) but lookup via conversations.list failed: %w\n"+
					"Either grant the bot the channels:read scope and retry, or set notification.slack.channelOverride", channelName, scanErr)
			}
		}

		// Scan disabled (default) — fail fast with actionable guidance.
		return "", false, slackResolveFailFast(channelName)
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
