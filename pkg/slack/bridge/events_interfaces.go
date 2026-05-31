package bridge

import (
	"context"
	"time"
)

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
