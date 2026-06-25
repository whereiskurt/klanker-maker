package bridge

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
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

	// Phase 75: file attachment download + S3 buffering.
	// FileDownloader is optional — nil means feature off (back-compat for Lambda
	// images deployed before Phase 75). When nil and msg.Files is non-empty, the
	// handler falls through to the text-only SQS dispatch path with a Warn log.
	FileDownloader FileDownloader

	// Slack is the SlackPoster used for posting thread-reply warnings when file
	// downloads fail (e.g. oversize, 403, S3 failure). Optional — nil means warnings
	// are suppressed. Shared with the main bridge Handler via wiring in main.go.
	Slack SlackPoster

	// Phase 91: MentionOnly, when true, makes Handle() skip messages that do not
	// contain `<@{bot_user_id}>` in event.text. Default false → pre-Phase-91
	// behaviour (every message processed). Set from KM_SLACK_MENTION_ONLY env var
	// at Lambda cold-start by wireEventsHandler in cmd/km-slack-bridge/main.go.
	MentionOnly bool

	// Phase 91.4: ReactAlways, when true (default), posts a 👀 reaction on every
	// dispatched message in step 10. When false, the reaction is posted ONLY on
	// top-level engagement messages (msg.ThreadTS == "") — thread replies that
	// reach step 10 (via Phase 91.3 thread-bypass or a fresh mention in an
	// existing thread) dispatch silently. Wired from KM_SLACK_REACT_ALWAYS env
	// var at cold-start (defaults to true when env is unset or "true").
	ReactAlways bool

	// Phase 118: install-level trigger allowlist (Slack user IDs, e.g. "Uxxxx"),
	// populated from KM_SLACK_ALLOW (comma-joined). Empty = everyone allowed
	// (backward-compatible with all pre-Phase-118 behavior). When non-empty, only
	// messages from listed users are dispatched; all others are silently dropped
	// (200 OK, no reaction, no SQS enqueue). Per-sandbox SandboxRoutingInfo.Allow
	// (non-empty) REPLACES this list for that sandbox — it is not additive.
	Allow []string

	// Phase 95: federated relay. When non-nil and FetchByChannel returns empty
	// (unknown channel), Broadcast is called with the verbatim request body and
	// Slack headers so a sibling km-install can process the event locally.
	// nil => federation off => unknown-channel path returns 200 as today
	// (byte-identical — the nil-invariant MUST be maintained; see
	// TestEventsHandler_NilRelayer_MissReturns200).
	Relayer PeerRelayer

	// Phase 96: RunningChannels lists this install's running sandboxes with bound
	// Slack channels. Used by the peer-side relayed-miss path to return
	// {claimed:false, channels:[...]} to the front door, and by the front-door
	// orphan-reply path to aggregate the local channel list. Optional — nil means
	// the list is empty (safe, no panic).
	RunningChannels RunningChannelLister

	// Phase 96: DefaultRouter, when true, enables the front-door orphan-channel
	// reply. On a FetchByChannel miss + zero peer claims + bot @-mention + cooldown
	// clear, Handle posts exactly one threaded reply listing running sandbox channels
	// aggregated from this install + all peers, then returns 200.
	//
	// Default false (zero value) = dormant — byte-identical to Phase 95.
	// Set from KM_SLACK_DEFAULT_ROUTER env var at Lambda cold-start.
	//
	// HARD invariants:
	//   - DefaultRouter=false (or nil Relayer) => NO reply; Phase 95 byte-identical.
	//   - Any Claimed:true in the scatter-gather => NO reply (owner handled it).
	//   - Non-mention message in an orphan channel => NO reply.
	//   - Bot's own reply is dropped by isBotLoop BEFORE the miss site — structural
	//     guarantee, never re-triggers the router.
	DefaultRouter bool

	// Phase 96: RouterCooldown gates the per-channel reply frequency. When non-nil,
	// Reserve("router-cooldown:{channel}", 3600) is called before posting; if
	// ErrNonceReplayed is returned the reply is suppressed for that window. When nil,
	// no cooldown is applied (not recommended in production; main.go always wires it
	// together with DefaultRouter=true).
	RouterCooldown RouterCooldownStore

	// Phase 114: auto-resume. When Resumer is non-nil and info.Paused, the step-9
	// branch starts the stopped/paused EC2 instance so the on-box poller boots and
	// drains the message enqueued at step 8. nil => byte-identical to pre-Phase-114
	// (pause-hint only); the nil-invariant MUST be maintained (see
	// TestEventsHandler_NilResumer_PauseHintOnly).
	Resumer SandboxResumer
	// Phase 114: after a successful (or transient-error, optimistic) resume, flip the
	// km-sandboxes row to status=running so a follow-up message takes the warm path.
	// nil => status not flipped (graceful degradation). UpdateItem, never PutItem.
	StatusWriter SandboxStatusWriter
	// Phase 114: distinct hinter used only on the orphan/degraded path
	// (ErrNoResumableInstance — instance gone, no cold-create in v1). Carries the
	// "couldn't auto-resume; ask an operator" text. nil => orphan path logs only.
	OrphanHinter PauseHintPoster
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
//  10. If Reactor is wired, call Reactor.Add SYNCHRONOUSLY to ACK with 👀
//      (or whatever KM_SLACK_ACK_EMOJI is set to). Errors logged.
//      Reacts on msg.TS (the originating message), NOT threadTS.
//      Synchronous (not goroutine) for the same reason as step 8's file
//      download: AWS Lambda freezes the runtime when Handle returns, and
//      any goroutine still mid-retry has its wall-clock context elapse
//      during the freeze. If reactions.add pushes past Slack's 3s ack
//      window, Slack re-fires the event → the event_id dedup in step 5
//      returns 200 immediately; already_reacted is treated as success.
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

	// 5. Resolve channel → sandbox FIRST. Doing this before dedup means the
	// mention-only filter below can see the per-sandbox info.MentionOnly override,
	// and an unknown channel is dropped before consuming a dedup nonce. Dedup
	// (step 6) runs AFTER the mention filter so non-mention messages in a
	// mention-only channel never consume a nonce — preserving the Phase 91
	// polite-bot efficiency in noisy shared channels. (A Slack retry re-runs this
	// FetchByChannel before being deduped, but retries are rare.)
	info, err := h.Sandboxes.FetchByChannel(ctx, msg.Channel)
	if err != nil {
		h.log().Error("events: channel lookup", "err", err, "channel", msg.Channel)
		return EventsResponse{StatusCode: 200, Body: "ok"} // 200 to prevent retry storm
	}
	if info.SandboxID == "" || info.QueueURL == "" {
		// Phase 95: broadcast-on-miss / drop-on-relay decision table.
		// The relay marker is read from req.Headers (keys already lowercased by
		// the adapter in main.go lowercaseHeaders). This check runs AFTER
		// verifySlackSignature so the loop guard is authenticated.
		//
		// Decision table:
		//   | X-KM-Relayed? | Owns channel? | Action                              |
		//   |---------------|---------------|-------------------------------------|
		//   | absent        | yes           | (falls through — handled above)     |
		//   | absent        | no            | broadcast to all peers, return 200  |
		//   | present       | yes           | (falls through — handled above)     |
		//   | present       | no            | drop (TERMINAL — no re-relay), 200  |
		if req.Headers["x-km-relayed"] != "" {
			// TERMINAL: relayed request + no owner => drop, never re-relay.
			// Phase 96: return {claimed:false, channels:[...]} so the front door
			// can tally cross-install running channels for the orphan reply.
			h.log().Warn("events: relay miss — no owner for relayed message",
				"channel", msg.Channel, "event", "slack_relay_no_owner")
			var runningChannels []SandboxChannelInfo
			if h.RunningChannels != nil {
				if listed, listErr := h.RunningChannels.ListRunning(ctx); listErr != nil {
					h.log().Warn("events: relay miss: running channels list failed", "err", listErr)
				} else {
					runningChannels = listed
				}
			}
			if runningChannels == nil {
				runningChannels = []SandboxChannelInfo{}
			}
			missResp := peerRelayResponse{Claimed: false, Channels: runningChannels}
			missBody, marshalErr := json.Marshal(missResp)
			if marshalErr != nil {
				h.log().Warn("events: relay miss: json marshal failed; falling back", "err", marshalErr)
				return EventsResponse{StatusCode: 200, Body: "ok"}
			}
			return EventsResponse{
				StatusCode: 200,
				Body:       string(missBody),
				Headers:    map[string]string{"content-type": "application/json"},
			}
		}
		if h.Relayer != nil {
			// Broadcast raw event to all peer bridges (synchronous, bounded ~2.5s).
			// Phase 96: capture []PeerClaimResult and tally claims for orphan detection.
			// The caller returns 200 regardless — a partial broadcast is better
			// than dropping the event entirely.
			claimResults, broadcastErr := h.Relayer.Broadcast(ctx, req.Body, req.Headers)
			if broadcastErr != nil {
				h.log().Warn("events: relay broadcast partial failure", "err", broadcastErr,
					"channel", msg.Channel)
			}
			// Phase 96: claim-aware orphan reply.
			// Tally: any claimed:true => owner handled it, no router reply needed.
			anyClaimed := false
			for _, r := range claimResults {
				if r.Claimed {
					anyClaimed = true
					break
				}
			}
			if !anyClaimed {
				h.maybePostOrphanReply(ctx, msg, claimResults)
			}
		} else {
			h.log().Warn("events: unknown channel or inbound disabled", "channel", msg.Channel)
		}
		return EventsResponse{StatusCode: 200, Body: "ok"}
	}
	// present+yes: process locally (fall through — today's path unchanged).

	// 5a. Trigger allowlist (Phase 118). The OUTER gate: runs AFTER channel-ownership
	// (so per-sandbox info.Allow is known) and BEFORE the mention-only / thread-bypass
	// filter, dedup, and dispatch. A non-listed user is silently dropped (no reaction,
	// no reply, no enqueue; 200 OK) — mirroring the GitHub bridge's silent-200 reject.
	//
	// Resolution: a non-empty per-sandbox info.Allow REPLACES the install-level
	// h.Allow entirely for this sandbox; otherwise h.Allow applies; if both are
	// empty the gate is dormant (everyone may trigger — backward-compatible).
	//
	// Placing this before the thread-bypass is deliberate (AC5): a non-listed user
	// must NOT be able to hijack an already-engaged thread via the Phase 91.3 bypass.
	effectiveAllow := h.Allow
	if len(info.Allow) > 0 {
		effectiveAllow = info.Allow
	}
	if len(effectiveAllow) > 0 && !isInSlackAllowlist(msg.User, effectiveAllow) {
		h.log().Debug("events: allowlist: silent drop (user not in trigger allowlist)",
			"user", msg.User, "channel", msg.Channel, "ts", msg.TS,
			"per_sandbox_override", len(info.Allow) > 0)
		return EventsResponse{StatusCode: 200, Body: "ok"}
	}

	// 5b. Mention-only filter (Phase 91; per-sandbox override). When mention-only
	// is in effect, skip messages that do not @-mention the bot. Runs AFTER
	// FetchByChannel so the per-sandbox info.MentionOnly tri-state can override the
	// install-level h.MentionOnly, and BEFORE dedup/upsert/enqueue so a filtered
	// message consumes no nonce, engages no thread, and dispatches nothing.
	// Fail-open on BotUserID fetch error to match isBotLoop's policy.
	//
	// Phase 91.3 thread-bypass: when the message is a reply in a thread the bot is
	// already engaged with (a (channel, thread_ts) row exists in km-slack-threads
	// with sandbox_id set), skip the mention requirement. Threads are 1:1
	// conversations with the bot — once the first mention has dispatched and the
	// upsert lands, every subsequent reply is logically directed at the bot.
	effectiveMentionOnly := h.MentionOnly
	if info.MentionOnly != nil {
		effectiveMentionOnly = *info.MentionOnly
	}
	if effectiveMentionOnly {
		bypassed := false
		if msg.ThreadTS != "" {
			sb, lookupErr := h.Threads.LookupSandbox(ctx, msg.Channel, msg.ThreadTS)
			if lookupErr != nil {
				h.log().Warn("events: mention-only: thread lookup failed; treating as new thread",
					"err", lookupErr, "channel", msg.Channel, "thread_ts", msg.ThreadTS)
			} else if sb != "" {
				h.log().Debug("events: mention-only: bypassed for engaged thread",
					"channel", msg.Channel, "thread_ts", msg.ThreadTS, "sandbox", sb)
				bypassed = true
			}
		}
		if !bypassed {
			uid, err := h.BotUserID.Fetch(ctx)
			if err != nil {
				h.log().Warn("events: mention-only: bot_user_id fetch failed; falling open (allow)", "err", err)
			} else if uid != "" && !strings.Contains(msg.Text, "<@"+uid+">") {
				h.log().Debug("events: mention-only: skipping non-mention message",
					"channel", msg.Channel, "ts", msg.TS, "per_sandbox_override", info.MentionOnly != nil)
				return EventsResponse{StatusCode: 200, Body: "ok"}
			}
		}
	}

	// 6. Dedup event_id — after the mention filter (so filtered messages don't
	// consume a nonce) and before the upsert/enqueue (so duplicates are dropped).
	if env.EventID != "" {
		seen, err := h.Nonces.CheckAndStore(ctx, EventNoncePrefix+env.EventID, EventNonceTTL)
		if err != nil {
			h.log().Error("events: nonce check", "err", err, "event_id", env.EventID)
			// Continue — better to risk a duplicate than to drop
		} else if seen {
			return EventsResponse{StatusCode: 200, Body: "ok"}
		}
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

	// 8. SQS write — critical path.
	//
	// Phase 75 files-fork: when FileDownloader is wired and the message carries
	// files[], skip the synchronous SQS write. Instead, fire a background goroutine
	// that downloads files → PUTs to S3 → posts thread-reply warnings → writes SQS.
	// This keeps Handle's round-trip within Slack's 3s ack window regardless of
	// file sizes (up to 25 × 100 MB = 2.5 GB in theory).
	//
	// When FileDownloader is nil (pre-Phase-75 Lambda images, or tests that don't
	// wire it), fall through to the existing synchronous SQS write path unchanged.
	dedupID := env.EventID
	if dedupID == "" {
		dedupID = msg.TS
	}
	if h.FileDownloader != nil && len(msg.Files) > 0 {
		// Phase 75.2: process file downloads SYNCHRONOUSLY within the handler.
		//
		// We originally spawned a goroutine here so the handler could return 200
		// within Slack's 3-second ack window regardless of file sizes. That model
		// is unsound on AWS Lambda: when the handler returns, the runtime is
		// frozen, the in-flight HTTP request's wall-clock deadline elapses during
		// the freeze, and the next thaw resumes the goroutine to find every
		// operation already timed out (UAT 2026-05-15: every files-fork attempt
		// failed with `Client.Timeout exceeded while awaiting headers`).
		//
		// Going synchronous: Slack may retry at 3s if a file batch takes longer,
		// but the event_id dedup check above already cached this event_id, so the
		// retry is a no-op 200. Lambda timeout is set to 60s to fit the 90s
		// DownloadTimeoutTotal budget (in practice typical: ~1-2s/file).
		bgCtx, bgCancel := context.WithTimeout(ctx, DownloadTimeoutTotal)
		defer bgCancel()

		atts, fileErrs, _ := h.FileDownloader.Download(bgCtx, msg.Files, info.SandboxID, threadTS)

		// Post thread-reply warnings BEFORE the SQS write so the agent
		// sees the failure context when it wakes up. Sort by OriginalName
		// for deterministic warning order.
		sort.Slice(fileErrs, func(i, j int) bool {
			return fileErrs[i].OriginalName < fileErrs[j].OriginalName
		})
		for _, fe := range fileErrs {
			if h.Slack != nil {
				if _, err := h.Slack.PostMessage(bgCtx, msg.Channel, "", "Warning: "+fe.Reason, threadTS); err != nil {
					h.log().Error("events: warning post failed", "err", err, "reason", fe.Reason)
				}
			}
		}

		// Build the SQS body with Attachments[] populated.
		sqsBody := InboundQueueBody{
			Channel:     msg.Channel,
			ThreadTS:    threadTS,
			Text:        msg.Text,
			User:        msg.User,
			EventTS:     msg.TS,
			Attachments: atts,
		}
		sqsBodyBytes, _ := json.Marshal(sqsBody)
		if err := h.SQS.Send(bgCtx, info.QueueURL, string(sqsBodyBytes), threadTS, dedupID); err != nil {
			h.log().Error("events: sqs send (files-sync) failed",
				"err", err, "queue", info.QueueURL, "sandbox", info.SandboxID)
		} else {
			h.log().Info("events: enqueued (files-sync)",
				"sandbox", info.SandboxID, "channel", msg.Channel, "thread_ts", threadTS,
				"attachments", len(atts), "event_id", env.EventID)
		}
	} else {
		// Files-empty path (or FileDownloader nil): synchronous SQS write. UNCHANGED.
		// When FileDownloader is nil and files are present, we fall through here and
		// dispatch text-only — back-compat for Lambda images deployed before Phase 75.
		body := InboundQueueBody{
			Channel:  msg.Channel,
			ThreadTS: threadTS,
			Text:     msg.Text,
			User:     msg.User,
			EventTS:  msg.TS,
		}
		bodyBytes, _ := json.Marshal(body)
		if err := h.SQS.Send(ctx, info.QueueURL, string(bodyBytes), threadTS, dedupID); err != nil {
			// Internal error — log and 200 (NOT 500) per RESEARCH.md Pitfall 2.
			// CONTEXT.md flow step 9 mandates 200 on transport failure: 5xx triggers
			// a Slack retry storm with new event_ids that bypass nonce dedup.
			h.log().Error("events: sqs send", "err", err, "queue", info.QueueURL, "sandbox", info.SandboxID)
			return EventsResponse{StatusCode: 200, Body: "ok"}
		}
		h.log().Info("events: enqueued", "sandbox", info.SandboxID, "channel", msg.Channel, "thread_ts", threadTS, "event_id", env.EventID)
	}

	// 9. Resume-or-hint (Phase 114). The message is ALREADY enqueued at step 8 in all
	//    cases — resume failure NEVER strands the prompt (same fail-soft contract as the
	//    GitHub Phase-109 path). Runs ONLY after the mention-only/thread-bypass filter
	//    (step 5b) and enqueue (step 8), so idle chatter never wakes the box.
	//
	//    SYNCHRONOUS (not a goroutine) — same Phase 75.2 lesson as the step-10 reactor:
	//    AWS Lambda freezes the runtime when Handle returns; a goroutine mid-StartInstances
	//    would see its wall-clock context elapse during the freeze and thaw to find every
	//    AWS call timed out. The bounded sub-context fits inside the 60s Lambda timeout; if
	//    the work pushes past Slack's 3s ack window, Slack re-fires the event and the
	//    event_id dedup in step 6 returns an immediate 200 (already-seen).
	if info.Paused {
		sid := info.SandboxID
		ch, ts := msg.Channel, threadTS
		resumeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if h.Resumer != nil {
			err := h.Resumer.StartSandbox(resumeCtx, sid)
			if err != nil && errors.Is(err, ErrNoResumableInstance) {
				// Orphan: instance gone. v1 has no cold-create. Degraded hint, leave row.
				h.log().Warn("events: auto-resume: no resumable instance (orphaned row)",
					"sandbox", sid, "channel", ch)
				if h.OrphanHinter != nil {
					if herr := h.OrphanHinter.PostIfCooldownExpired(resumeCtx, ch, ts); herr != nil {
						h.log().Warn("events: auto-resume: orphan hint post failed", "err", herr, "channel", ch, "thread_ts", ts)
					}
				}
			} else {
				if err != nil {
					h.log().Warn("events: auto-resume: transient StartInstances error (enqueue continues, flipping status optimistically)",
						"sandbox", sid, "err", err)
				}
				// Success OR transient error: optimistically flip status (fail-soft).
				if h.StatusWriter != nil {
					if werr := h.StatusWriter.SetStatusRunning(resumeCtx, sid); werr != nil {
						h.log().Warn("events: auto-resume: SetStatusRunning failed (non-fatal)", "sandbox", sid, "err", werr)
					}
				}
				if h.PauseHinter != nil {
					if herr := h.PauseHinter.PostIfCooldownExpired(resumeCtx, ch, ts); herr != nil {
						h.log().Warn("events: auto-resume: wake hint post failed", "err", herr, "channel", ch, "thread_ts", ts)
					}
				}
			}
		} else if h.PauseHinter != nil {
			// nil Resumer: byte-identical to pre-Phase-114 (pause-hint only), now
			// running synchronously (same rationale as the step-10 reactor).
			if err := h.PauseHinter.PostIfCooldownExpired(resumeCtx, ch, ts); err != nil {
				h.log().Warn("events: pause hint post failed", "err", err, "channel", ch, "thread_ts", ts)
			}
		}
		cancel()
	}

	// 10. ACK reaction (Phase 67.1; synchronous since Phase 75.2's lesson).
	//     React on msg.TS — the originating message, NOT threadTS (which is the
	//     session anchor and points to the thread root for in-thread replies).
	//     RESEARCH.md Pitfall 1: using threadTS causes message_not_found for replies.
	//
	//     Synchronous handling (no goroutine): AWS Lambda freezes the runtime
	//     when Handle returns. A goroutine mid-retry would see its wall-clock
	//     context elapse during the freeze and resume on the next thaw to find
	//     every operation timed out (Phase 75.2 UAT 2026-05-15). The 10s reactor
	//     budget fits inside the 60s Lambda timeout; if reactions.add pushes
	//     past Slack's 3s ack window, Slack re-fires the event and the event_id
	//     dedup in step 5 absorbs the retry. already_reacted is success.
	if h.Reactor != nil {
		// Phase 91.4: install-level first-only-react. ReactAlways=false +
		// thread-reply → silent. Top-level always reacts (engagement signal).
		//
		// Phase 91.5: per-sandbox override. SandboxRoutingInfo.ReactAlways
		// (tri-state *bool) wins over the install-level h.ReactAlways when
		// non-nil. Source: km-sandboxes.slack_react_always attribute, written
		// at sandbox-create time from the profile's
		// cli.notifySlackInboundReactAlways field. Absent attribute leaves
		// info.ReactAlways nil → install default applies.
		effectiveReactAlways := h.ReactAlways
		if info.ReactAlways != nil {
			effectiveReactAlways = *info.ReactAlways
		}
		if !effectiveReactAlways && msg.ThreadTS != "" {
			h.log().Debug("events: reaction skipped (react-always=false, thread reply)",
				"channel", msg.Channel, "ts", msg.TS, "thread_ts", msg.ThreadTS,
				"sandbox", info.SandboxID, "per_sandbox_override", info.ReactAlways != nil)
		} else {
			emoji := h.AckEmoji
			if emoji == "" {
				emoji = "eyes"
			}
			bgCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := h.Reactor.Add(bgCtx, msg.Channel, msg.TS, emoji); err != nil {
				h.log().Warn("events: reaction failed", "err", err, "channel", msg.Channel, "ts", msg.TS, "emoji", emoji)
			} else {
				h.log().Info("events: reaction posted", "channel", msg.Channel, "ts", msg.TS, "emoji", emoji)
			}
			cancel()
		}
	}

	// Phase 96: relayed-owned path. When the request carried x-km-relayed (peer
	// sent it to us) and we own the channel, return {claimed:true} so the front
	// door knows we handled it. Non-relayed (front-door) owned responses keep
	// their existing plain "ok" body — Plan 03 changes the miss path only.
	if req.Headers["x-km-relayed"] != "" {
		ownedResp := peerRelayResponse{Claimed: true}
		ownedBody, marshalErr := json.Marshal(ownedResp)
		if marshalErr == nil {
			return EventsResponse{
				StatusCode: 200,
				Body:       string(ownedBody),
				Headers:    map[string]string{"content-type": "application/json"},
			}
		}
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
	// (2) Allow-list: only these subtypes count as a real human turn.
	// Phase 75: "file_share" added — user-initiated file upload in a channel.
	switch m.Subtype {
	case "", "thread_broadcast", "file_share":
		// fall through — file_share added in Phase 75
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

// isInSlackAllowlist reports whether userID is present in allow (case-insensitive,
// matching Slack ID casing leniently). Callers MUST gate on len(allow) > 0 first:
// an EMPTY allow means "everyone allowed" (Phase 118, backward-compatible). This
// INVERTS the GitHub bridge's isInAllowlist, where an empty list is deny-by-default.
func isInSlackAllowlist(userID string, allow []string) bool {
	for _, u := range allow {
		if strings.EqualFold(strings.TrimSpace(u), userID) {
			return true
		}
	}
	return false
}

// maybePostOrphanReply posts exactly one threaded reply in an orphan channel
// (a channel not owned by any install) when all gates pass:
//
//  1. h.DefaultRouter must be true (default false = dormant).
//  2. msg.Channel must be non-empty (defensive guard).
//  3. The message must @-mention the bot (`<@{bot_user_id}>` in msg.Text).
//  4. Per-channel cooldown must be clear (RouterCooldownStore.Reserve returns nil).
//
// Reply content: a threaded message listing running sandbox channels aggregated from
// this install's h.RunningChannels.ListRunning plus every peer's result.Channels,
// rendered as `<#CID>` Slack mentions. When the aggregate list is empty, the reply
// uses a guidance-only variant (no channel mentions, naming convention only).
//
// The reply is posted SYNCHRONOUSLY before returning (NOT in a goroutine) because
// AWS Lambda freezes the execution environment when Handle returns — see PITFALL 3
// in 96-RESEARCH.md. A bounded 5-second context caps the Slack API call.
//
// On any gate failure or error, the method returns silently; Handle continues to
// return 200 regardless of what this method does.
func (h *EventsHandler) maybePostOrphanReply(ctx context.Context, msg slackMessageEvent, peerResults []PeerClaimResult) {
	// Gate 1: DefaultRouter feature flag.
	if !h.DefaultRouter {
		return
	}
	// Gate 2: Non-empty channel (defensive).
	if msg.Channel == "" {
		return
	}
	// Gate 3: Bot @-mention required.
	uid, fetchErr := h.BotUserID.Fetch(ctx)
	if fetchErr != nil {
		h.log().Warn("events: router: bot_user_id fetch failed; skipping orphan reply", "err", fetchErr)
		return
	}
	if uid == "" || !strings.Contains(msg.Text, "<@"+uid+">") {
		return // not a mention — silent
	}
	// Gate 4: Per-channel cooldown.
	if h.RouterCooldown != nil {
		cooldownErr := h.RouterCooldown.Reserve(ctx, msg.Channel, 3600)
		if cooldownErr != nil {
			if errors.Is(cooldownErr, ErrNonceReplayed) {
				h.log().Debug("events: router: cooldown active; suppressing orphan reply",
					"channel", msg.Channel)
			} else {
				h.log().Warn("events: router: cooldown Reserve failed; skipping reply",
					"channel", msg.Channel, "err", cooldownErr)
			}
			return
		}
	}

	// Build aggregate channel list: front door's own running channels + all peers' channels.
	seen := make(map[string]bool)
	var channels []SandboxChannelInfo
	if h.RunningChannels != nil {
		local, listErr := h.RunningChannels.ListRunning(ctx)
		if listErr != nil {
			h.log().Warn("events: router: local running channels list failed; continuing with peers only", "err", listErr)
		} else {
			for _, ch := range local {
				if ch.ID != "" && !seen[ch.ID] {
					seen[ch.ID] = true
					channels = append(channels, ch)
				}
			}
		}
	}
	for _, pr := range peerResults {
		for _, ch := range pr.Channels {
			if ch.ID != "" && !seen[ch.ID] {
				seen[ch.ID] = true
				channels = append(channels, ch)
			}
		}
	}

	// Build reply text.
	var text string
	convention := "they're named `#sb-{alias}-{profile}`"
	if len(channels) == 0 {
		text = "No sandbox is bound to this channel. To work with a bot, join one of its channels — " +
			convention + ". None are currently running; ask an operator to create one with `km create`."
	} else {
		var sb strings.Builder
		sb.WriteString("No sandbox is bound to this channel. To work with a bot, join one of its channels — ")
		sb.WriteString(convention)
		sb.WriteString(". Currently running:\n")
		for _, ch := range channels {
			sb.WriteString("• <#")
			sb.WriteString(ch.ID)
			sb.WriteString(">")
			if ch.Alias != "" || ch.Profile != "" {
				sb.WriteString(" — ")
				if ch.Alias != "" {
					sb.WriteString(ch.Alias)
				}
				if ch.Profile != "" {
					sb.WriteString(" (")
					sb.WriteString(ch.Profile)
					sb.WriteString(")")
				}
			}
			sb.WriteString("\n")
		}
		text = strings.TrimRight(sb.String(), "\n")
	}

	// Thread anchor: top-level posts use msg.TS; replies in a thread use msg.ThreadTS.
	replyThreadTS := msg.ThreadTS
	if replyThreadTS == "" {
		replyThreadTS = msg.TS
	}

	// Post SYNCHRONOUSLY with a bounded context (PITFALL 3: no goroutine in Lambda).
	replyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := h.Slack.PostMessage(replyCtx, msg.Channel, "", text, replyThreadTS); err != nil {
		h.log().Warn("events: router: orphan reply post failed", "channel", msg.Channel, "err", err)
	} else {
		h.log().Info("events: router: orphan reply posted", "channel", msg.Channel, "thread_ts", replyThreadTS, "channels_listed", len(channels))
	}
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
