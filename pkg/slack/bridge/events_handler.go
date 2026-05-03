package bridge

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

const (
	// EventNoncePrefix isolates Slack event_id values from the existing
	// operator-envelope nonce keyspace in km-slack-bridge-nonces.
	EventNoncePrefix = "event:"
	// EventNonceTTL controls how long we remember an event_id as "seen".
	// 24h matches Slack's recommendation for Events API replay windows.
	EventNonceTTL = 24 * time.Hour
)

// EventsHandler handles POST /events from Slack Events API.
type EventsHandler struct {
	SigningSecret SigningSecretFetcher
	BotUserID     BotUserIDFetcher
	Nonces        EventNonceStore           // reuses km-slack-bridge-nonces table via EventNonceStore
	Sandboxes     SandboxByChannelFetcher
	Threads       SlackThreadStore
	SQS           SQSSender
	PauseHinter   PauseHintPoster           // optional; if nil, paused-hint branch is skipped
	Logger        *slog.Logger
	// Now is injected for tests; defaults to time.Now.
	Now func() time.Time

	// Phase 67.1: ACK reaction (👀) on successful SQS enqueue.
	// Reactor is optional — nil means feature off (back-compat for tests
	// and for early deployments before SlackReactorAdapter is wired).
	Reactor Reactor
	// AckEmoji is the emoji name (without colons) for the ACK reaction.
	// Empty defaults to "eyes" at call site. Configured via KM_SLACK_ACK_EMOJI
	// env var at cold start.
	AckEmoji string
}

func (h *EventsHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *EventsHandler) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// Handle processes an incoming /events request.
//
// Order of operations is critical:
//  1. Parse body just enough to detect url_verification (challenges bypass
//     signature check per Slack docs).
//  2. Verify signing secret (HMAC + timestamp window).
//  3. Re-parse for event_callback dispatch.
//  4. Filter bot/self/subtype messages.
//  5. Dedup event_id via Nonces (reuses existing km-slack-bridge-nonces).
//  6. Resolve channel→sandbox.
//  7. Idempotently upsert km-slack-threads row.
//  8. Send SQS message.
//  9. If info.Paused, fire PauseHinter.PostIfCooldownExpired in a goroutine
//     so we still return 200 within Slack's 3s ack window. Errors logged.
//  10. If Reactor is wired, fire Reactor.Add in a goroutine to ACK with 👀
//      (or whatever KM_SLACK_ACK_EMOJI is set to). Errors logged. NEVER blocks.
//      Reacts on msg.TS (the originating message), NOT threadTS.
//
// Response codes:
//   - 400 ONLY for malformed JSON / missing required fields (truly bad request)
//   - 401 ONLY for signing-secret mismatch / stale timestamp / future timestamp
//   - 200 for everything else, including: replayed event_id, unknown channel,
//     bot-self-message, signing-secret fetcher failure, DDB error, SQS write
//     failure, threads-upsert failure, sandbox-lookup failure.
//
// Why 200-on-internal-error: Slack retries 5xx responses with a NEW event_id
// (bypassing our event_id-based dedup) within ~30s. During an SQS or DDB
// outage, returning 5xx creates a retry storm — Slack gives up only after
// 3 retries (~10min) and the same message arrives multiple times when the
// downstream recovers. Logging + 200 is the only safe default — see
// RESEARCH.md Pitfall 2 and CONTEXT.md flow step 9.
func (h *EventsHandler) Handle(ctx context.Context, req EventsRequest) EventsResponse {
	var env slackEnvelope
	if err := json.Unmarshal([]byte(req.Body), &env); err != nil {
		h.log().Warn("events: malformed json", "err", err)
		return EventsResponse{StatusCode: 400, Body: "bad request"}
	}

	// 1. url_verification short-circuit — Slack docs explicitly say this
	//    arrives ONCE during URL save and must be echoed BEFORE signature
	//    verification (signing secret may not yet be configured).
	if env.Type == "url_verification" {
		b, _ := json.Marshal(map[string]string{"challenge": env.Challenge})
		return EventsResponse{
			StatusCode: 200,
			Body:       string(b),
			Headers:    map[string]string{"content-type": "application/json"},
		}
	}

	// 2. Signature verification
	secret, err := h.SigningSecret.Fetch(ctx)
	if err != nil {
		// Internal error — log and 200 (NOT 500) per RESEARCH.md Pitfall 2.
		// Slack would retry 5xx with a new event_id and bypass dedup.
		h.log().Error("events: fetch signing secret", "err", err)
		return EventsResponse{StatusCode: 200, Body: "ok"}
	}
	tsHdr := req.Headers["x-slack-request-timestamp"]
	sigHdr := req.Headers["x-slack-signature"]
	if err := verifySlackSignature(secret, tsHdr, req.Body, sigHdr, h.now()); err != nil {
		h.log().Warn("events: signature check failed", "err", err)
		return EventsResponse{StatusCode: 401, Body: "unauthorized"}
	}

	// 3. event_callback dispatch
	if env.Type != "event_callback" {
		h.log().Info("events: ignoring non-callback type", "type", env.Type)
		return EventsResponse{StatusCode: 200, Body: "ok"}
	}

	var msg slackMessageEvent
	if err := json.Unmarshal(env.Event, &msg); err != nil {
		h.log().Warn("events: bad inner event", "err", err)
		return EventsResponse{StatusCode: 200, Body: "ok"} // ack to prevent Slack retry
	}
	if msg.Type != "message" {
		// Reserved for future expansion (e.g. app_mention is deferred).
		h.log().Info("events: ignoring non-message event", "type", msg.Type)
		return EventsResponse{StatusCode: 200, Body: "ok"}
	}

	// 4. Bot-loop filter (BEFORE any expensive work)
	if h.isBotLoop(ctx, msg) {
		h.log().Debug("events: bot-loop filter matched", "subtype", msg.Subtype, "bot_id", msg.BotID, "user", msg.User)
		return EventsResponse{StatusCode: 200, Body: "ok"}
	}

	// 5. Dedup event_id
	if env.EventID != "" {
		seen, err := h.Nonces.CheckAndStore(ctx, EventNoncePrefix+env.EventID, EventNonceTTL)
		if err != nil {
			h.log().Error("events: nonce check", "err", err, "event_id", env.EventID)
			// Continue — better to risk a duplicate than to drop
		} else if seen {
			return EventsResponse{StatusCode: 200, Body: "ok"}
		}
	}

	// 6. Resolve channel → sandbox
	info, err := h.Sandboxes.FetchByChannel(ctx, msg.Channel)
	if err != nil {
		h.log().Error("events: channel lookup", "err", err, "channel", msg.Channel)
		return EventsResponse{StatusCode: 200, Body: "ok"} // 200 to prevent retry storm
	}
	if info.SandboxID == "" || info.QueueURL == "" {
		h.log().Warn("events: unknown channel or inbound disabled", "channel", msg.Channel)
		return EventsResponse{StatusCode: 200, Body: "ok"}
	}

	// 7. Determine thread anchor: top-level posts use msg.TS as the new thread_ts.
	threadTS := msg.ThreadTS
	if threadTS == "" {
		threadTS = msg.TS
	}
	if err := h.Threads.Upsert(ctx, msg.Channel, threadTS, info.SandboxID); err != nil {
		h.log().Warn("events: threads upsert", "err", err, "channel", msg.Channel, "thread_ts", threadTS)
		// best-effort — poller can recreate the row when it does its DDB lookup
	}

	// 8. SQS write — the critical path
	body := InboundQueueBody{
		Channel:  msg.Channel,
		ThreadTS: threadTS,
		Text:     msg.Text,
		User:     msg.User,
		EventTS:  msg.TS,
	}
	bodyBytes, _ := json.Marshal(body)
	dedupID := env.EventID
	if dedupID == "" {
		dedupID = msg.TS
	}
	if err := h.SQS.Send(ctx, info.QueueURL, string(bodyBytes), info.SandboxID, dedupID); err != nil {
		// Internal error — log and 200 (NOT 500) per RESEARCH.md Pitfall 2.
		// CONTEXT.md flow step 9 mandates 200 on transport failure: 5xx triggers
		// a Slack retry storm with new event_ids that bypass nonce dedup.
		h.log().Error("events: sqs send", "err", err, "queue", info.QueueURL, "sandbox", info.SandboxID)
		return EventsResponse{StatusCode: 200, Body: "ok"}
	}

	h.log().Info("events: enqueued", "sandbox", info.SandboxID, "channel", msg.Channel, "thread_ts", threadTS, "event_id", env.EventID)

	// 9. Paused-sandbox hint (LOCKED in CONTEXT.md "Edge Cases").
	//    Fire-and-forget so Slack still gets a 200 within the 3s ack window.
	//    The PauseHinter implementation enforces the 1h cooldown internally;
	//    handler is just a trigger.
	if info.Paused && h.PauseHinter != nil {
		ch, ts := msg.Channel, threadTS
		go func() {
			// Use a fresh context — request ctx may be canceled after the
			// 200 response is written. Use a short deadline so the goroutine
			// never lingers.
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := h.PauseHinter.PostIfCooldownExpired(bgCtx, ch, ts); err != nil {
				h.log().Warn("events: pause hint post failed", "err", err, "channel", ch, "thread_ts", ts)
			}
		}()
	}

	// 10. ACK reaction (Phase 67.1).
	//     Fire-and-forget so the 200 still ships within Slack's 3s ack window.
	//     React on msg.TS — the originating message, NOT threadTS (which is the
	//     session anchor and points to the thread root for in-thread replies).
	//     RESEARCH.md Pitfall 1: using threadTS causes message_not_found for replies.
	if h.Reactor != nil {
		ch, msgTS := msg.Channel, msg.TS
		emoji := h.AckEmoji
		if emoji == "" {
			emoji = "eyes"
		}
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := h.Reactor.Add(bgCtx, ch, msgTS, emoji); err != nil {
				h.log().Warn("events: reaction failed", "err", err, "channel", ch, "ts", msgTS, "emoji", emoji)
			}
		}()
	}

	return EventsResponse{StatusCode: 200, Body: "ok"}
}

// isBotLoop returns true if the message should NOT be dispatched to the
// sandbox: bot self-messages, system events (channel_join, file_share, etc.),
// or messages with no user attribution.
//
// Slack subtype semantics: an empty subtype field means a regular human
// post. `thread_broadcast` means a user replied in a thread with "Also send
// to channel" ticked — also a real human turn. Every other subtype is a
// system event or alternate message form (file_share, channel_join,
// channel_topic, ekm_access_denied, ...) that is either un-threadable
// (system messages can't accept replies, breaks bot post) or carries no
// user prompt content. Use an allow-list rather than a deny-list because
// Slack has historically added new subtypes (e.g. ekm_access_denied came
// years after launch) and a deny-list silently regresses each time —
// Phase 67 UAT Gap B was a channel_join slip-through that burned a Claude
// turn on a Slack Connect invite acceptance.
func (h *EventsHandler) isBotLoop(ctx context.Context, m slackMessageEvent) bool {
	// (1) Bot ID set — definitively a bot post (covers all bot_message variants).
	if m.BotID != "" {
		return true
	}
	// (2) Allow-list: only these two subtypes count as a real human turn.
	switch m.Subtype {
	case "", "thread_broadcast":
		// fall through
	default:
		h.log().Debug("events: subtype filter dropped",
			"subtype", m.Subtype, "channel", m.Channel, "ts", m.TS)
		return true
	}
	// (3) No user attribution — not a real human turn.
	if m.User == "" {
		return true
	}
	// (4) Second-line defence: user is our own bot's user_id.
	botUID, err := h.BotUserID.Fetch(ctx)
	if err != nil {
		h.log().Warn("events: bot user_id fetch failed", "err", err)
		return false // fail open — let dedup catch loops
	}
	return botUID != "" && m.User == botUID
}

// verifySlackSignature verifies the HMAC + timestamp window per Slack docs.
func verifySlackSignature(signingSecret, tsHeader, rawBody, sigHeader string, now time.Time) error {
	if tsHeader == "" {
		return fmt.Errorf("missing X-Slack-Request-Timestamp")
	}
	ts, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil {
		return fmt.Errorf("bad timestamp header: %w", err)
	}
	skew := now.Unix() - ts
	if skew < 0 {
		skew = -skew
	}
	if skew > 300 {
		return fmt.Errorf("stale timestamp (%ds skew)", skew)
	}
	if !strings.HasPrefix(sigHeader, "v0=") {
		return fmt.Errorf("missing or wrong-format signature")
	}
	base := "v0:" + tsHeader + ":" + rawBody
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte(base))
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sigHeader)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}
