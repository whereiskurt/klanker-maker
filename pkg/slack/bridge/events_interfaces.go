package bridge

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// SandboxChannelInfo is the minimal per-sandbox channel information returned
// by RunningChannelLister and included in PeerClaimResult for non-owner peers.
// Used by the scatter-gather router to build the channel list in the orphan reply (Phase 96).
type SandboxChannelInfo struct {
	ID      string `json:"id"`
	Alias   string `json:"alias"`
	Profile string `json:"profile"`
}

// PeerClaimResult is the per-peer result of a scatter-gather Broadcast call.
// Claimed:true means the peer owns (or conservatively claims) the channel.
// Claimed:false means the peer does not own the channel; Channels lists the
// peer's currently running sandboxes with bound Slack channels.
//
// Rollout safety (LOCKED): any legacy/error/unparseable/timeout peer response
// is treated as Claimed:true to prevent false orphan detection in a mixed-version fleet.
type PeerClaimResult struct {
	PeerURL  string             // for logging
	Claimed  bool               // true = peer owns the channel (or legacy/error)
	Channels []SandboxChannelInfo // only populated when Claimed=false
}

// PeerRelayer broadcasts a raw Slack Events API request to sibling km-install bridges.
// Used by EventsHandler when FetchByChannel finds no local owner (Phase 95+96).
// Implementations MUST:
//   - Forward the verbatim body unchanged (Slack HMAC covers body+timestamp).
//   - Include X-Slack-Signature, X-Slack-Request-Timestamp from slackHeaders.
//   - Add X-KM-Relayed: 1 to the forwarded request.
//   - POST to all peers in parallel, bounded by a ~2.5s context.
//   - Return one PeerClaimResult per peer; legacy/error/timeout => Claimed:true.
//   - Always return promptly (the caller returns 200 regardless).
//
// Phase 96: return type changed from error to ([]PeerClaimResult, error) to
// support claim-aware scatter-gather. The caller tallies results to detect orphan channels.
type PeerRelayer interface {
	Broadcast(ctx context.Context, rawBody string, slackHeaders map[string]string) ([]PeerClaimResult, error)
}

// RunningChannelLister enumerates sandboxes with state=running and a bound
// Slack channel. Used by the scatter-gather handler to build the channel list
// for the router reply (Phase 96).
type RunningChannelLister interface {
	ListRunning(ctx context.Context) ([]SandboxChannelInfo, error)
}

// DDBScanAPI is the narrow DynamoDB interface required by DDBRunningChannelLister.
// *dynamodb.Client satisfies this interface.
type DDBScanAPI interface {
	Scan(ctx context.Context, in *dynamodb.ScanInput, opts ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

// RouterCooldownStore gates the per-channel router-reply cooldown.
// Reserve returns nil on first call (reply is permitted; cooldown begins) or
// ErrNonceReplayed when the cooldown window is still active (suppress the reply).
//
// Implementation: DynamoNonceStore.Reserve with a "router-cooldown:{channelID}"
// key and a TTL of 3600s. No new table — reuses km-slack-bridge-nonces (Phase 96).
type RouterCooldownStore interface {
	Reserve(ctx context.Context, channelID string, cooldownSeconds int) error
}

// SQSSender sends a single message to a FIFO SQS queue.
type SQSSender interface {
	Send(ctx context.Context, queueURL, body, groupID, deduplicationID string) error
}

// SlackThreadStore reads and writes the km-slack-threads DDB table.
type SlackThreadStore interface {
	// Get returns the claude_session_id for (channel, thread_ts), empty string if no row.
	Get(ctx context.Context, channelID, threadTS string) (claudeSessionID string, err error)
	// Upsert creates a row keyed by (channel, thread_ts) only if absent
	// (attribute_not_exists condition). Idempotent on replay; never overwrites
	// claude_session_id once set by the poller.
	Upsert(ctx context.Context, channelID, threadTS, sandboxID string) error
	// LookupSandbox returns the sandbox_id for (channel, thread_ts), empty
	// string if no row exists. Phase 91.3: used by the events handler to
	// bypass the mention-only filter on replies in threads the bot is already
	// engaged with — sandbox_id is set at Upsert time (before the poller sets
	// claude_session_id), so this signal fires as soon as the bot's first
	// mention-triggered dispatch enqueues, not after the poller responds.
	LookupSandbox(ctx context.Context, channelID, threadTS string) (sandboxID string, err error)
}

// SandboxByChannelFetcher resolves a Slack channel_id to sandbox routing info
// via the slack_channel_id-index GSI on km-sandboxes.
type SandboxByChannelFetcher interface {
	FetchByChannel(ctx context.Context, channelID string) (info SandboxRoutingInfo, err error)
}

// SandboxRoutingInfo is the minimal subset of km-sandboxes attributes needed
// to dispatch an inbound event.
type SandboxRoutingInfo struct {
	SandboxID string
	QueueURL  string // empty when notifySlackInboundEnabled was false
	Paused    bool   // from km-sandboxes.state == "paused"
	// Phase 91.5: per-sandbox override of the install-level KM_SLACK_REACT_ALWAYS.
	// Tri-state via *bool — written by create_slack_inbound.go from the profile's
	// cli.notifySlackInboundReactAlways field only when explicitly set, so:
	//   nil    → row has no slack_react_always attribute → use install default
	//   &true  → react on every dispatch for this sandbox
	//   &false → react only on top-level engagement messages for this sandbox
	ReactAlways *bool
	// Per-sandbox override of the install-level KM_SLACK_MENTION_ONLY. Tri-state
	// via *bool — written by create_slack_inbound.go from the profile's
	// notification.slack.inbound.mentionOnly field only when explicitly set, so:
	//   nil    → row has no slack_mention_only attribute → use install default
	//   &true  → this sandbox requires an @-mention (with thread-bypass)
	//   &false → this sandbox dispatches every message (chatty), ignoring mentions
	MentionOnly *bool
}

// SigningSecretFetcher returns the Slack signing secret used for HMAC
// verification (cached, mirrors SSMBotTokenFetcher pattern).
type SigningSecretFetcher interface {
	Fetch(ctx context.Context) (string, error)
}

// BotUserIDFetcher returns the bot's own user_id for the bot-loop self-message
// filter (cached at Lambda warm time via auth.test).
type BotUserIDFetcher interface {
	Fetch(ctx context.Context) (string, error)
}

// PauseHintPoster posts a one-time "sandbox is paused; message queued" hint
// into a Slack thread when the destination sandbox is paused. Implementations
// MUST enforce the CONTEXT.md cooldown: only post if no hint was posted within
// the last 1h for this sandbox. Returns nil if posted, nil if cooldown active
// (no error — silent skip), error only on transport/storage failure.
//
// Plan 67-05 implements this with a DDB-backed adapter writing
// `last_pause_hint_ts` on the km-sandboxes row (conditional write to handle
// bridge cold-start race) and posting via the existing operator-signed `post`
// action through the bridge Lambda's POST / route.
type PauseHintPoster interface {
	PostIfCooldownExpired(ctx context.Context, channelID, threadTS string) error
}

// EventNonceStore checks and atomically stores an event_id to detect replays.
// This interface differs from the existing NonceStore (which uses Reserve/
// ErrNonceReplayed) in order to provide a cleaner bool return for the
// events handler's dedup branch.
type EventNonceStore interface {
	// CheckAndStore returns (true, nil) if the id was already seen (replay),
	// (false, nil) on first insertion, or (false, err) on storage failure.
	CheckAndStore(ctx context.Context, id string, ttl time.Duration) (alreadySeen bool, err error)
}

// Reactor posts a reaction emoji to a Slack message.
// Used by EventsHandler to ACK inbound messages with 👀 after SQS enqueue.
// Implementations MUST treat already_reacted as idempotent success (return nil).
// React to msg.TS (the originating message), NOT threadTS (the session anchor).
type Reactor interface {
	Add(ctx context.Context, channel, ts, emoji string) error
}
