package bridge

// webhook_handler.go — the km-h1-bridge Handle() flow.
//
// Ported from pkg/github/bridge/webhook_handler.go and re-shaped for HackerOne per
// the Plan 04 interfaces-block delta. The two structural changes vs the GitHub
// bridge are the HEART of this phase:
//
//  1. TWO trigger models instead of one @-mention:
//       - auto-triage  : the X-H1-Event being present in a program's events: map
//                        IS the trigger (no handle, no allow gate — the operator's
//                        events: choice is the authorization).
//       - comment-keyword: a report_comment_created whose body contains the literal
//                          program handle (ContainsHandle), allow-gated deny-by-default.
//
//  2. MULTI-TARGET FANOUT: one trigger fans the SAME prompt to N targets. The
//     single-target 3-way dispatch (warm/cold/resume) is wrapped in a
//     `for i, target := range targets` loop. Each target gets its own dedupID
//     (so N targets are NOT deduped to one) and its own (report_id, target)
//     thread-continuity row.
//
// The SAFETY-CRITICAL reply gate (Plan 04 Task 3) lives in computeReplyToResearcher
// + the per-target envelope construction in the fanout loop:
//   - INTERNAL by default (envelope zero value).
//   - researcher-visible reply ONLY when /reply_to_researcher present AND the actor
//     is in the program allowlist (BOTH required; command-alone DOWNGRADES to internal).
//   - of N fanout targets, ONLY the primary (index 0) may carry the external flag;
//     every other target is forced internal. Never N external replies.
//
// Federation/relay, the PR-only filter, the bot-type loop check, and the 👀 reactor
// are DELETED (no HackerOne analog). Internal errors return 200 (never 5xx) so the
// platform does not redeliver with a fresh GUID that bypasses dedup.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/whereiskurt/klanker-maker/pkg/quota"
)

// WebhookRequest is the normalized inbound request to WebhookHandler.Handle.
// Headers are expected to be lowercase-keyed (the Lambda main.go normalizes).
type WebhookRequest struct {
	// Headers are the HTTP request headers, keyed by lowercase name.
	Headers map[string]string
	// RawBody is the verbatim (already base64-DECODED) body bytes used for HMAC verify.
	RawBody []byte
	// Body is the string form of RawBody (convenience).
	Body string
}

// WebhookResponse is the handler's HTTP response.
type WebhookResponse struct {
	StatusCode int
	Body       string
}

// WebhookHandler implements the km-h1-bridge event dispatch logic. All collaborators
// are interfaces so the handler is exhaustively unit-testable with mocks.
type WebhookHandler struct {
	// Secret fetches the HackerOne webhook signing secret from SSM (cached).
	Secret SecretFetcher

	// APIUsername is the HackerOne customer-API Basic-Auth identity — the loop-guard
	// username. A comment authored by this username is the bridge's own internal ACK
	// (or a sandbox reply) and must NOT re-trigger. There is no Bot user type in H1.
	APIUsername string

	// Nonces is the delivery-GUID replay-protection store (shared nonces table).
	Nonces DeliveryNonceStore

	// Resolver looks up a sandbox_id by alias. When it also satisfies
	// SandboxAliasResolverWithStatus, Handle() uses the status-aware 3-way dispatch.
	Resolver SandboxAliasResolver

	// Resumer starts a stopped/paused sandbox. Nil → stopped treated like running.
	Resumer SandboxResumer

	// Publisher publishes a SandboxCreate EventBridge event (cold path).
	Publisher EventBridgePublisher

	// SQS enqueues to the per-sandbox h1-inbound FIFO queue (warm path).
	SQS SQSSender

	// StatusWriter writes status=running back to km-sandboxes after a successful
	// auto-resume. Nil → no write-back.
	StatusWriter SandboxStatusWriter

	// Threads tracks (report_id, target) → {sandbox_id, agent_session_id, agent_type}.
	// When non-nil a known report thread bypasses the @handle requirement and per-target
	// rows are upserted on dispatch. Nil → handle always required, no continuity.
	Threads H1ThreadStore

	// Commenter posts the synchronous INTERNAL "on it" ack (and command-reply / deny
	// / conflict replies, which are ALWAYS internal). Nil → ack/reply paths skipped.
	Commenter H1Commenter

	// Entries is the parsed h1.programs config (from KM_H1_PROGRAMS at cold-start).
	Entries []ProgramEntry

	// DefaultProfile is the fallback profile when a target omits Profile.
	DefaultProfile string

	// BotHandle is the install-wide comment-keyword token (e.g. "@km"). A program's
	// BotHandle overrides it per-program.
	BotHandle string

	// Commands is the parsed command map (from SSM {prefix}/config/h1/commands).
	// Nil/empty → command pass dormant (free-form dispatch).
	Commands map[string]CommandEntry

	// DefaultCommand is the install-wide default command name (per-program default wins).
	DefaultCommand string

	// Logger; defaults to slog.Default() when nil.
	Logger *slog.Logger

	// Phase 121 (H1-01) — quota enforcement on h1_comment dispatch.
	// When both Quota and Limits are non-nil, quota.Record is called before each
	// SQS enqueue. nil Quota or nil Limits → dormant (byte-identical to pre-Phase-121).
	Quota      H1QuotaAPI            // DDB client for the action-quota table
	QuotaTable string                // e.g. "km-action-quota" (from KM_QUOTA_TABLE env var)
	Limits     H1ActionLimitsFetcher // resolves per-sandbox action-limits JSON

	// Phase 121 (H1-01) — frozen-dispatch gate. When FrozenCheck is non-nil and the
	// sandbox is quarantine-latched (action_frozen=true), dispatch is refused and an
	// INTERNAL control-plane notice is posted via Commenter. nil → gate dormant.
	FrozenCheck H1FrozenChecker

	// Phase 121 (GAP-2) — auto-freeze on BreachFreeze trips. When non-nil and a quota
	// trip with onBreach:freeze occurs, Freezer.FreezeSandbox is called to latch
	// action_frozen=true on the km-sandboxes row (by="auto:<action>:<window>"). nil ⇒
	// dormant — BreachFreeze still blocks the action but does not auto-quarantine.
	// Implemented by DynamoFreezer (aws_adapters.go).
	Freezer Freezer
}

// Freezer latches action_frozen=true on the sandbox row (auto-on-breach freeze).
// nil ⇒ dormant (no auto-freeze). Implemented by the aws_adapters DynamoFreezer.
type Freezer interface {
	FreezeSandbox(ctx context.Context, sandboxID, reason, by string) error
}

func (h *WebhookHandler) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

func ok200() WebhookResponse { return WebhookResponse{StatusCode: 200, Body: "ok"} }

// Handle processes one inbound HackerOne webhook request. ~11-step flow:
// verify → event-gate → parse → loop-guard → resolve → thread-bypass →
// trigger-gate → authz → dedup → command-pass → fanout-dispatch → internal-ACK.
func (h *WebhookHandler) Handle(ctx context.Context, req WebhookRequest) WebhookResponse {
	// ── Step 1: verify signature ─────────────────────────────────────────────
	secret, err := h.Secret.Fetch(ctx)
	if err != nil {
		// Secret-fetch failure is an INTERNAL error → 200 (not 5xx) so the platform
		// does not redeliver with a fresh GUID that bypasses dedup (Pitfall 3).
		h.log().Error("h1-bridge: fetch webhook secret", "err", err)
		return ok200()
	}
	sigHeader := req.Headers["x-h1-signature"]
	if err := VerifyH1Signature(secret, sigHeader, req.RawBody); err != nil {
		h.log().Warn("h1-bridge: signature check failed", "err", err)
		return WebhookResponse{StatusCode: 401, Body: "unauthorized"}
	}

	// ── Step 1b: event gate ──────────────────────────────────────────────────
	// Accept report_comment_created (the comment-keyword trigger) OR any event that
	// appears in SOME resolved program's events: map (the auto-triage trigger). All
	// other events drop with 200.
	eventType := req.Headers["x-h1-event"]

	// ── Step 2: parse payload ────────────────────────────────────────────────
	payload, err := ParsePayload(req.RawBody)
	if err != nil {
		h.log().Warn("h1-bridge: malformed payload", "err", err)
		return ok200()
	}

	// ── Step 3: loop guard ───────────────────────────────────────────────────
	// Drop the bridge's own (or the sandbox helper's) comment so an internal ACK
	// does not re-trigger. HackerOne has no Bot user type — compare the actor
	// username against the Basic-Auth identity.
	if h.APIUsername != "" && strings.EqualFold(payload.ActorUsername(), h.APIUsername) {
		h.log().Debug("h1-bridge: loop-guard matched (actor == api_username)",
			"actor", payload.ActorUsername())
		return ok200()
	}

	// ── Step 4.5: resolve program handle → targets ───────────────────────────
	targets, allow, events, commands, matched := Resolve(payload.ProgramHandle(), h.Entries, h.DefaultProfile)
	if !matched {
		h.log().Info("h1-bridge: no program config, silent drop", "program", payload.ProgramHandle())
		return ok200()
	}

	// Determine the trigger model.
	_, isAutoTriageEvent := events[eventType]
	isComment := eventType == "report_comment_created"
	if !isAutoTriageEvent && !isComment {
		h.log().Info("h1-bridge: event not a trigger, dropping", "event", eventType, "program", payload.ProgramHandle())
		return ok200()
	}

	// Per-program handle override.
	botHandle := h.BotHandle
	for _, e := range h.Entries {
		if e.Handle == payload.ProgramHandle() && e.BotHandle != "" {
			botHandle = e.BotHandle
			break
		}
	}

	// ── Step 4b: known-thread bypass ─────────────────────────────────────────
	// If ANY (report_id, target) row exists for this report, a comment may bypass the
	// handle requirement (1:1 thread with the bot — re-typing the handle is unnatural).
	threadKnown := false
	if h.Threads != nil {
		for _, tgt := range targets {
			if sid, _, _, lookErr := h.Threads.LookupSandbox(ctx, payload.ReportID(), tgt.Alias); lookErr == nil && sid != "" {
				threadKnown = true
				break
			} else if lookErr != nil {
				h.log().Warn("h1-bridge: thread lookup failed; treating as new", "err", lookErr,
					"report", payload.ReportID(), "target", tgt.Alias)
			}
		}
	}

	// ── Steps 5–6: trigger gate + authz ──────────────────────────────────────
	// We build the prompt body and decide the reply intent based on the trigger.
	var promptBody string
	var agentVerb string
	var replyToResearcherIntent bool // /reply_to_researcher present in this comment?

	if isAutoTriageEvent && !isComment {
		// Auto-triage path: the event presence IS the trigger; does NOT gate on allow
		// (OQ3 — the operator's events: choice is the authorization). Build the prompt
		// from the event template, pre-expanding the report fields.
		entry := events[eventType]
		fields := ReportFields{
			ReportID: payload.ReportID(),
			Title:    payload.Title(),
			State:    payload.State(),
			Program:  payload.ProgramHandle(),
		}
		promptBody = ExpandTemplateFields(entry.Prompt, "", fields)
	} else {
		// Comment-keyword path.
		// Step 5: @handle scan (unless a known thread bypasses it).
		if !ContainsHandle(payload.CommentBody(), botHandle) && !threadKnown {
			h.log().Info("h1-bridge: no handle + unknown thread, dropping", "program", payload.ProgramHandle())
			return ok200()
		}

		// Step 6: deny-by-default allow gate (comment-keyword only).
		if !isInAllowlist(payload.ActorUsername(), allow) {
			h.log().Info("h1-bridge: actor not in allowlist, silent drop",
				"actor", payload.ActorUsername(), "program", payload.ProgramHandle())
			return ok200()
		}

		// ── Command pass (+ agent verb + /reply_to_researcher intent) ──────────
		parsed := ParseCommands(payload.CommentBody(), commands)
		replyToResearcherIntent = parsed.ReplyToResearcher

		// Agent-verb conflict short-circuits (post an internal reply, no dispatch).
		if parsed.AgentVerbConflict {
			h.postInternalReply(ctx, payload.ReportID(),
				"Specify one agent — found /claude and /codex.")
			return ok200()
		}
		agentVerb = parsed.AgentVerb

		if len(commands) > 0 || h.DefaultCommand != "" {
			programDefaultCmd := lookupProgramDefaultCommand(h.Entries, payload.ProgramHandle())
			// Routing uses the primary target's alias/profile as the program defaults.
			programAlias, programProfile := "", h.DefaultProfile
			if len(targets) > 0 {
				programAlias = targets[0].Alias
				programProfile = targets[0].Profile
			}
			res := RunCommandPass(
				payload.CommentBody(),
				commands,
				h.DefaultCommand, programDefaultCmd,
				payload.ActorUsername(),
				programAlias, programProfile, h.DefaultProfile,
				botHandle,
				"", // currentAgentType: thread agent (not tracked at handler granularity here)
			)
			switch res.Action {
			case CommandActionReply, CommandActionDeny:
				// Command reply / inner-deny → INTERNAL reply, no dispatch.
				h.postInternalReply(ctx, payload.ReportID(), res.ReplyText)
				return ok200()
			case CommandActionDispatch:
				promptBody = res.Prompt
			case CommandActionPassthrough:
				promptBody = expandFreeForm(payload, botHandle, agentVerb)
			}
		} else {
			promptBody = expandFreeForm(payload, botHandle, agentVerb)
		}
	}

	// ── Step 7: dedupe delivery GUID ─────────────────────────────────────────
	deliveryGUID := req.Headers["x-h1-delivery"]
	if deliveryGUID != "" && h.Nonces != nil {
		nonceKey := H1DeliveryNoncePrefix + deliveryGUID
		replayed, nErr := h.Nonces.CheckAndStore(ctx, nonceKey, H1DeliveryNonceTTLSeconds)
		if nErr != nil {
			h.log().Error("h1-bridge: nonce store error", "err", nErr)
			return ok200()
		}
		if replayed {
			h.log().Info("h1-bridge: replayed delivery", "guid", deliveryGUID)
			return ok200()
		}
	}

	// ── Reply gate (SAFETY-CRITICAL) ─────────────────────────────────────────
	// researcherReply is true ONLY when /reply_to_researcher was present AND the actor
	// is in the program allowlist. BOTH required — command-present-alone DOWNGRADES to
	// internal (the allow gate is the SAME deny-by-default gate as dispatch). Note the
	// auto-triage path never sets replyToResearcherIntent, so it is always internal.
	researcherReply := ComputeReplyToResearcher(replyToResearcherIntent, payload.ActorUsername(), allow)

	// ── Steps 8/9: command pass already done → fanout dispatch ───────────────
	// Wrap the single-target 3-way dispatch in a per-target loop (Pattern 4). Each
	// target gets a distinct dedupID (so N targets are NOT deduped to one) and its own
	// (report_id, target) thread row. ONLY the primary target (index 0) may carry the
	// external reply flag; every other target is forced internal.
	dispatched := false
	for i, target := range targets {
		// Reply gate, per-target: only the primary (first) target may post externally;
		// /reply_to_researcher is honored only by the primary target, other targets
		// reply internally. This prevents N external replies under fanout.
		env := H1Envelope{
			Source:            "hackerone",
			Program:           payload.ProgramHandle(),
			ReportID:          payload.ReportID(),
			Kind:              eventType,
			ActivityID:        payload.ActivityID(),
			Actor:             payload.ActorUsername(),
			Body:              promptBody,
			Agent:             agentVerb,
			ReplyToResearcher: researcherReply && i == 0,
		}
		envJSON, mErr := json.Marshal(env)
		if mErr != nil {
			h.log().Error("h1-bridge: marshal envelope", "err", mErr, "target", target.Alias)
			continue
		}

		groupID := fmt.Sprintf("h1-%s-%s", payload.ReportID(), target.Alias)
		dedupID := fmt.Sprintf("%s-%s", deliveryGUID, groupID)

		if h.dispatchTarget(ctx, target, payload.ReportID(), string(envJSON), groupID, dedupID) {
			dispatched = true
		}
	}

	// ── Step 10: synchronous INTERNAL ack ────────────────────────────────────
	// Post exactly one internal "on it" comment (never researcher-visible). The ack is
	// always internal regardless of the reply gate — the gate governs the AGENT's reply
	// from the sandbox, not this synchronous acknowledgement.
	if dispatched && h.Commenter != nil {
		if cErr := h.Commenter.PostComment(ctx, payload.ReportID(), "On it — dispatched to a sandbox agent.", true); cErr != nil {
			h.log().Warn("h1-bridge: internal ack failed (non-fatal)", "err", cErr)
		}
	}

	return ok200()
}

// dispatchTarget performs the 3-way dispatch for ONE target. Returns true when a
// dispatch (enqueue or cold-create) was attempted. All errors are non-fatal (logged)
// so the handler still returns 200.
func (h *WebhookHandler) dispatchTarget(ctx context.Context, target Target, reportID, envJSON, groupID, dedupID string) bool {
	alias := target.Alias
	profile := target.Profile

	rws, hasStatus := h.Resolver.(SandboxAliasResolverWithStatus)
	if !hasStatus {
		// Fallback: base resolver, no status awareness (warm-or-cold only).
		sandboxID, resolveErr := h.Resolver.ResolveByAlias(ctx, alias)
		if resolveErr != nil {
			h.log().Info("h1-bridge: cold create", "alias", alias, "profile", profile, "resolve_err", resolveErr)
			if pErr := h.Publisher.PutSandboxCreate(ctx, alias, profile, envJSON); pErr != nil {
				h.log().Error("h1-bridge: publish SandboxCreate", "err", pErr)
			}
			return true
		}
		h.enqueueAndUpsert(ctx, sandboxID, reportID, target.Alias, envJSON, groupID, dedupID)
		return true
	}

	sandboxID, status, resolveErr := rws.ResolveByAliasWithStatus(ctx, alias)
	if resolveErr != nil {
		// Alias truly absent → cold-create.
		h.log().Info("h1-bridge: cold create", "alias", alias, "profile", profile, "resolve_err", resolveErr)
		if pErr := h.Publisher.PutSandboxCreate(ctx, alias, profile, envJSON); pErr != nil {
			h.log().Error("h1-bridge: publish SandboxCreate", "err", pErr)
		}
		return true
	}

	if (status == "stopped" || status == "paused") && h.Resumer != nil {
		// Resume path: start the box, then enqueue so the prompt drains on boot. A
		// TRANSIENT Resumer error is non-fatal (logged) and the enqueue still happens.
		// But a TERMINAL ErrNoResumableInstance (the instance is gone — an orphaned
		// stopped row) must NOT enqueue to the dead per-sandbox queue: instead delete
		// the stale row (so the alias becomes absent → no ambiguous-alias trap) and
		// cold-create.
		h.log().Info("h1-bridge: auto-resume", "alias", alias, "sandbox_id", sandboxID, "status", status)
		rErr := h.Resumer.StartSandbox(ctx, sandboxID)
		if rErr != nil && errors.Is(rErr, ErrNoResumableInstance) {
			// Orphaned alias row: status=stopped/paused but the EC2 instance is gone.
			// Self-heal: delete the stale row, then cold-create. Skip the enqueue (no
			// live poller) and the thread upsert (no live sandbox_id to record). The
			// cold-created box gets its thread row on the first reply / next warm turn.
			h.log().Warn("h1-bridge: orphaned stopped row (no instance); cold-creating",
				"alias", alias, "sandbox_id", sandboxID)
			if h.StatusWriter != nil {
				if dErr := h.StatusWriter.DeleteSandboxRow(ctx, sandboxID); dErr != nil {
					h.log().Error("h1-bridge: delete stale row failed (cold-create may hit ambiguous-alias)",
						"err", dErr, "sandbox_id", sandboxID)
				}
			}
			if pErr := h.Publisher.PutSandboxCreate(ctx, alias, profile, envJSON); pErr != nil {
				h.log().Error("h1-bridge: publish SandboxCreate (orphan fallback)", "err", pErr)
			}
			return true
		}
		if rErr != nil {
			h.log().Error("h1-bridge: auto-resume failed (non-fatal; enqueue continues)", "err", rErr, "sandbox_id", sandboxID)
		} else if h.StatusWriter != nil {
			if swErr := h.StatusWriter.SetStatusRunning(ctx, sandboxID); swErr != nil {
				h.log().Warn("h1-bridge: status write-back failed (non-fatal)", "err", swErr, "sandbox_id", sandboxID)
			}
		}
		h.enqueueAndUpsert(ctx, sandboxID, reportID, target.Alias, envJSON, groupID, dedupID)
		return true
	}

	// Running (or stopped without a Resumer) → warm enqueue.
	h.log().Info("h1-bridge: warm enqueue", "alias", alias, "sandbox_id", sandboxID)
	h.enqueueAndUpsert(ctx, sandboxID, reportID, target.Alias, envJSON, groupID, dedupID)
	return true
}

// enqueueAndUpsert resolves the per-sandbox h1-inbound queue URL, enqueues the
// envelope, and (on success) upserts the (report_id, target) thread row. All errors
// are non-fatal (logged).
func (h *WebhookHandler) enqueueAndUpsert(ctx context.Context, sandboxID, reportID, target, envJSON, groupID, dedupID string) {
	// Phase 121 (H1-01): frozen gate — refuse dispatch when sandbox is quarantine-latched.
	// Notice is posted INTERNALLY (never researcher-visible). Fail-open on checker error.
	if h.FrozenCheck != nil {
		frozen, reason, fErr := h.FrozenCheck.IsFrozen(ctx, sandboxID)
		if fErr != nil {
			h.log().Warn("h1-bridge: frozen check failed (fail-open)", "sandbox", sandboxID, "err", fErr)
		} else if frozen {
			if reason == "" {
				reason = "quota limit exceeded or operator action"
			}
			notice := "🛑 This sandbox is frozen (" + reason + "). No further actions or replies until your operator releases it."
			h.postInternalReply(ctx, reportID, notice)
			h.log().Warn("h1-bridge: dispatch refused (sandbox frozen)", "sandbox", sandboxID, "reason", reason)
			return
		}
	}

	// Phase 121 (H1-01): quota.Record for h1_comment. Fail-open on any error.
	// BLOCK trip → skip the SQS enqueue; WARN → enqueue + post internal notice.
	if h.Quota != nil && h.Limits != nil && h.QuotaTable != "" {
		limitsJSON, lErr := h.Limits.FetchLimits(ctx, sandboxID)
		if lErr != nil {
			h.log().Warn("h1-bridge: limits fetch failed (fail-open)", "sandbox", sandboxID, "err", lErr)
		} else if limitsJSON != "" {
			var limits quota.Limits
			if jsonErr := json.Unmarshal([]byte(limitsJSON), &limits); jsonErr == nil {
				if actionLimit, ok := limits[quota.ActionH1Comment]; ok {
					d, recErr := quota.Record(ctx, h.Quota, h.QuotaTable, sandboxID, quota.ActionH1Comment, actionLimit)
					if recErr != nil {
						h.log().Warn("h1-bridge: quota record failed (fail-open)", "sandbox", sandboxID, "err", recErr)
					} else if d.Tripped {
						h.postH1QuotaNotice(ctx, reportID, d)
						switch d.OnBreach {
						case quota.BreachFreeze:
							// Auto-latch: write action_frozen=true so the frozen-dispatch gate fires on
							// subsequent turns. Fail-soft: log a Warn on freeze error but still block.
							if h.Freezer != nil {
								by := fmt.Sprintf("auto:%s:%s", quota.ActionH1Comment, d.WorstWindow)
								reason := fmt.Sprintf("quota exceeded: %s (%s window)", quota.ActionH1Comment, d.WorstWindow)
								if fErr := h.Freezer.FreezeSandbox(ctx, sandboxID, reason, by); fErr != nil {
									h.log().Warn("h1-bridge: auto-freeze failed (action still blocked)", "sandbox", sandboxID, "err", fErr)
								}
							}
							h.log().Warn("h1-bridge: dispatch blocked by quota (freeze)", "sandbox", sandboxID, "breach", d.OnBreach)
							return // block — skip SQS enqueue
						case quota.BreachBlock:
							h.log().Warn("h1-bridge: dispatch blocked by quota", "sandbox", sandboxID, "breach", d.OnBreach)
							return // block — skip SQS enqueue (no quarantine)
						}
						// BreachWarn: continue with enqueue after posting notice
					}
				}
			} else {
				h.log().Warn("h1-bridge: limits json parse failed (fail-open)", "sandbox", sandboxID, "err", jsonErr)
			}
		}
	}

	queueURL, qErr := h.Resolver.H1QueueURL(ctx, sandboxID)
	if qErr != nil {
		h.log().Error("h1-bridge: lookup h1 queue URL", "sandbox_id", sandboxID, "err", qErr)
		return
	}
	if sErr := h.SQS.Send(ctx, queueURL, envJSON, groupID, dedupID); sErr != nil {
		h.log().Error("h1-bridge: SQS enqueue", "err", sErr)
		return
	}
	if h.Threads != nil {
		if uErr := h.Threads.Upsert(ctx, reportID, target, sandboxID); uErr != nil {
			h.log().Warn("h1-bridge: thread upsert failed (non-fatal)", "err", uErr,
				"report", reportID, "target", target)
		}
	}
}

// postH1QuotaNotice posts an enforce-aware INTERNAL quota notice to the H1 report.
// Control-plane: never counted by quota.Record; always internal (never researcher-visible).
func (h *WebhookHandler) postH1QuotaNotice(ctx context.Context, reportID string, d quota.Decision) {
	if h.Commenter == nil {
		return
	}
	win := d.WorstWindow
	var count, limit int64
	for _, w := range d.Windows {
		if w.Window == win && w.Exceeded {
			count, limit = w.Count, w.Limit
			break
		}
	}
	var notice string
	switch d.OnBreach {
	case quota.BreachWarn:
		notice = fmt.Sprintf("⚠️ Quota reached: `h1_comment` hit %d/%d this %s. WARN mode — actions still flowing.", count, limit, win)
	case quota.BreachBlock:
		notice = fmt.Sprintf("🛑 Quota exceeded: `h1_comment` (%d/%d %s). Further comments blocked until the window resets.", count, limit, win)
	case quota.BreachFreeze:
		notice = fmt.Sprintf("🛑 Quota exceeded: `h1_comment` (%d/%d %s). Sandbox is now frozen — operator release required.", count, limit, win)
	default:
		notice = "⚠️ Quota alert: `h1_comment` limit reached."
	}
	if cErr := h.Commenter.PostComment(ctx, reportID, notice, true); cErr != nil {
		h.log().Warn("h1-bridge: quota notice post failed (non-fatal)", "err", cErr)
	}
}

// postInternalReply posts an INTERNAL comment (command reply / deny / conflict).
// These are NEVER researcher-visible.
func (h *WebhookHandler) postInternalReply(ctx context.Context, reportID, body string) {
	if h.Commenter == nil || body == "" {
		return
	}
	if cErr := h.Commenter.PostComment(ctx, reportID, body, true); cErr != nil {
		h.log().Warn("h1-bridge: post internal reply failed (non-fatal)", "err", cErr)
	}
}

// ComputeReplyToResearcher is the SAFETY-CRITICAL gate. A researcher-visible reply is
// permitted ONLY when /reply_to_researcher was present AND the actor is in the program
// allowlist. Command-present-but-not-allowlisted DOWNGRADES to internal (never silently
// external). Empty allowlist → false (deny-by-default). Exported so the reply-gate tests
// can assert the truth table directly (the per-target primary-only rule — only targets[0]
// may carry an external flag — is enforced in the fanout loop in Handle()).
func ComputeReplyToResearcher(commandPresent bool, actor string, allow []string) bool {
	return commandPresent && isInAllowlist(actor, allow)
}

// expandFreeForm extracts the free-form prompt after the handle, stripping the agent
// verb and /reply_to_researcher reserved tokens so they don't reach the agent.
func expandFreeForm(payload *H1WebhookPayload, botHandle, agentVerb string) string {
	return ExtractArgsWithAgent(payload.CommentBody(), strings.TrimPrefix(botHandle, "@"), "", agentVerb)
}

// isInAllowlist reports whether login (case-insensitive) is in allow. Deny-by-default:
// an empty allow list → always false.
func isInAllowlist(login string, allow []string) bool {
	lower := strings.ToLower(login)
	for _, a := range allow {
		if strings.ToLower(a) == lower {
			return true
		}
	}
	return false
}

// lookupProgramDefaultCommand returns the DefaultCommand for the matched program.
func lookupProgramDefaultCommand(entries []ProgramEntry, handle string) string {
	for _, e := range entries {
		if e.Handle == handle {
			return e.DefaultCommand
		}
	}
	return ""
}
