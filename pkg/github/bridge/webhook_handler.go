package bridge

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"
)

const (
	// GitHubDeliveryNoncePrefix isolates X-GitHub-Delivery GUIDs in the nonces table.
	GitHubDeliveryNoncePrefix = "github-delivery:"
	// GitHubDeliveryNonceTTLSeconds is the TTL for delivery GUID dedup entries.
	// 24h window covers GitHub's redelivery window comfortably.
	GitHubDeliveryNonceTTLSeconds = 86400
)

// WebhookRequest is the normalized inbound request to WebhookHandler.Handle.
// Headers are expected to be lowercase-keyed (caller normalizes before passing).
type WebhookRequest struct {
	// Headers are the HTTP request headers, keyed by lowercase name.
	Headers map[string]string
	// RawBody is the verbatim request body bytes used for HMAC verification.
	// MUST be the exact bytes received over the wire — verify before parsing.
	RawBody []byte
	// Body is the string representation of RawBody (convenience for JSON parse).
	Body string
}

// WebhookResponse is the handler's HTTP response.
type WebhookResponse struct {
	StatusCode int
	Body       string
}

// WebhookHandler implements the km-github-bridge event dispatch logic.
//
// Handle() ordering (RESEARCH Pattern 2, 11 steps):
//  1. Verify X-Hub-Signature-256 (raw body); bad/absent → 401.
//  2. Parse issue_comment payload; action != "created" → 200 drop.
//  3. Loop guard: comment.user.type == "Bot" OR login == BotLogin → 200 drop.
//  4. PR check: issue.pull_request absent → 200 drop (PR-only MVP).
//  5. @{bot-login} mention check → else 200 drop.
//  6. Authorize: sender.login in allowlist → else 200 SILENT (no reaction).
//  7. Dedupe: Reserve("github-delivery:"+guid) → replayed → 200.
//  8. Resolve owner/repo → {alias, profile, allow}.
//  9. Lookup sandbox by alias (ResolveSandboxAliasDynamo):
//     - Found (warm) → Enqueue to github-inbound FIFO.
//     - Not found (cold) → PutSandboxCreateEvent with GithubEnvelope.
//  10. Mint installation token, POST 👀 reaction SYNCHRONOUSLY; return 200.
//
// Critical: return 200 on internal errors (SQS/DDB failures) — GitHub redelivers
// 5xx with a NEW X-GitHub-Delivery GUID, bypassing our dedupe (same rationale as
// the Slack bridge; see events_handler.go:158-163).
type WebhookHandler struct {
	// Secret fetches the webhook signing secret from SSM (cached).
	Secret SecretFetcher

	// BotLogin fetches the bot's GitHub login (e.g. "klanker-maker[bot]") from SSM.
	// Used for loop guard and mention detection.
	BotLogin BotLoginFetcher

	// Nonces is the delivery-GUID replay-protection store (reuses the nonces DDB table).
	Nonces DeliveryNonceStore

	// Resolver looks up a sandbox_id by alias (alias-index GSI).
	// When the concrete value also satisfies SandboxAliasResolverWithStatus, Handle()
	// uses ResolveByAliasWithStatus for the unified 3-way dispatch (absent→cold-create,
	// stopped/paused→resume+enqueue, running→enqueue). Falls back to ResolveByAlias
	// when only the base interface is provided (Phase 97 behavior).
	Resolver SandboxAliasResolver

	// Resumer starts stopped or paused EC2 sandbox instances. When non-nil and
	// Resolver satisfies SandboxAliasResolverWithStatus, a stopped/paused alias
	// triggers StartSandbox + enqueue instead of a cold-create.
	// Nil → treat stopped like running (just enqueue, Phase 97 behavior).
	Resumer SandboxResumer

	// Publisher publishes a SandboxCreate EventBridge event (cold path).
	Publisher EventBridgePublisher

	// SQS enqueues messages to the per-sandbox github-inbound FIFO queue (warm path).
	SQS SQSSender

	// Reactor posts the 👀 reaction on the originating comment.
	Reactor GitHubReactor

	// Entries is the parsed github.repos config (set at cold-start from KM_GITHUB_REPOS).
	Entries []RepoEntry

	// DefaultProfile is the fallback profile when a matched entry has no Profile set.
	DefaultProfile string

	// ResourcePrefix is the km resource_prefix for FIFO queue name derivation.
	ResourcePrefix string

	// SandboxesTable is the DynamoDB km-sandboxes table name.
	SandboxesTable string

	// Threads tracks (repo, number) → {sandbox_id, agent_session_id} in km-github-threads.
	// When non-nil, known threads bypass the @-mention requirement (GH-X-THREADBYPASS) and
	// the poller can resume the same agent session on follow-up turns (GH-X-CONTINUITY).
	// When nil, Handle() behaves exactly as Phase 97 (mention always required).
	Threads GitHubThreadStore

	// StatusWriter writes status=running back to km-sandboxes after a successful
	// auto-resume (GH-X-RESUME Gap B fix). When non-nil, Handle() calls
	// SetStatusRunning after StartSandbox succeeds so km list / km resume reflect
	// the current state and follow-up @-mentions don't re-fire StartInstances.
	// Nil → no status write-back (pre-98-06 behavior).
	StatusWriter SandboxStatusWriter

	// Commands is the parsed command map from SSM {prefix}/config/github/commands.
	// Nil or empty map → dormant (Phase 99 command pass is skipped; byte-identical
	// to Phase 98 behavior). Populated at Lambda cold start from SSMCommandsFetcher.
	Commands map[string]CommandEntry

	// DefaultCommand is the install-wide default command name. When set and a
	// comment contains no explicit /command, the handler uses this command's
	// prompt template and routing overrides. Per-repo DefaultCommand from the
	// matched RepoEntry takes precedence over this field.
	// Empty string → no install-wide default (free-form passthrough when no command).
	DefaultCommand string

	// Commenter posts reply comments (multi-command errors, deny, /help) via the
	// GitHub App installation token. Nil → reply paths are skipped (errors logged).
	Commenter CommentPoster

	// Logger; defaults to slog.Default() when nil.
	Logger *slog.Logger
}

func (h *WebhookHandler) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// Handle processes one inbound GitHub webhook request.
// See struct-level doc for the 11-step ordering.
func (h *WebhookHandler) Handle(ctx context.Context, req WebhookRequest) WebhookResponse {
	// ── Step 1: Verify signature ────────────────────────────────────────────
	secret, err := h.Secret.Fetch(ctx)
	if err != nil {
		// Internal error fetching secret — log and 200 (NOT 500) per Pitfall 3.
		h.log().Error("github-bridge: fetch webhook secret", "err", err)
		return WebhookResponse{StatusCode: 200, Body: "ok"}
	}
	sigHeader := req.Headers["x-hub-signature-256"]
	if err := VerifyGitHubSignature(secret, sigHeader, req.RawBody); err != nil {
		h.log().Warn("github-bridge: signature check failed", "err", err)
		return WebhookResponse{StatusCode: 401, Body: "unauthorized"}
	}

	// Only process issue_comment events (the X-GitHub-Event header).
	eventType := req.Headers["x-github-event"]
	if eventType != "issue_comment" {
		h.log().Info("github-bridge: ignoring non-issue_comment event", "event", eventType)
		return WebhookResponse{StatusCode: 200, Body: "ok"}
	}

	// ── Step 2: Parse payload ────────────────────────────────────────────────
	var payload IssueCommentPayload
	if err := json.Unmarshal(req.RawBody, &payload); err != nil {
		h.log().Warn("github-bridge: malformed payload", "err", err)
		return WebhookResponse{StatusCode: 200, Body: "ok"}
	}
	if payload.Action != "created" {
		h.log().Info("github-bridge: ignoring action", "action", payload.Action)
		return WebhookResponse{StatusCode: 200, Body: "ok"}
	}

	// ── Step 3: Loop guard ───────────────────────────────────────────────────
	botLogin, err := h.BotLogin.Fetch(ctx)
	if err != nil {
		h.log().Error("github-bridge: fetch bot-login", "err", err)
		return WebhookResponse{StatusCode: 200, Body: "ok"}
	}
	if isGitHubBotLoop(payload.Comment.User, botLogin) {
		h.log().Debug("github-bridge: bot-loop filter matched",
			"user_type", payload.Comment.User.Type,
			"login", payload.Comment.User.Login)
		return WebhookResponse{StatusCode: 200, Body: "ok"}
	}

	// ── Step 4: PR-only filter ───────────────────────────────────────────────
	if payload.Issue.PullRequest == nil {
		h.log().Info("github-bridge: issue comment (not PR), dropping",
			"issue", payload.Issue.Number)
		return WebhookResponse{StatusCode: 200, Body: "ok"}
	}

	// ── Step 4b: known-thread bypass (GH-X-THREADBYPASS) ────────────────────
	// If (repo, number) is already tracked in km-github-threads, skip the
	// mention requirement. Mirrors Phase 91.3 Slack thread-bypass logic.
	// Threads == nil → Phase 97 behavior (mention always required).
	//
	// threadStoredSandboxID holds the sandbox_id from the continuity row. It is
	// compared with the alias-resolved sandbox_id in the dispatch block below;
	// if they differ (box recreated), InvalidateStaleSession is called (Gap E fix).
	threadKnown := false
	threadStoredSandboxID := ""
	if h.Threads != nil {
		if sid, _, lookupErr := h.Threads.LookupSandbox(ctx, payload.Repository.FullName, payload.Issue.Number); lookupErr == nil && sid != "" {
			threadKnown = true
			threadStoredSandboxID = sid
			h.log().Debug("github-bridge: known thread; bypassing mention check",
				"repo", payload.Repository.FullName,
				"number", payload.Issue.Number,
				"sandbox_id", sid)
		} else if lookupErr != nil {
			h.log().Warn("github-bridge: thread lookup failed; treating as new thread",
				"err", lookupErr, "repo", payload.Repository.FullName, "number", payload.Issue.Number)
		}
	}

	// ── Step 5: @-mention filter ─────────────────────────────────────────────
	if !ContainsMention(payload.Comment.Body, botLogin) && !threadKnown {
		h.log().Info("github-bridge: no mention, dropping",
			"repo", payload.Repository.FullName)
		return WebhookResponse{StatusCode: 200, Body: "ok"}
	}

	// ── Step 6: Authorize sender ─────────────────────────────────────────────
	alias, profile, allow, matched := Resolve(payload.Repository.FullName, h.Entries, h.DefaultProfile)
	if !matched {
		h.log().Info("github-bridge: no repo config, silent drop",
			"repo", payload.Repository.FullName)
		return WebhookResponse{StatusCode: 200, Body: "ok"}
	}
	if !isInAllowlist(payload.Comment.User.Login, allow) {
		// Silent — no reaction, no comment, invisible to unauthorized users.
		h.log().Info("github-bridge: sender not in allowlist, silent drop",
			"sender", payload.Comment.User.Login, "repo", payload.Repository.FullName)
		return WebhookResponse{StatusCode: 200, Body: "ok"}
	}

	// ── Step 7: Dedupe delivery GUID ─────────────────────────────────────────
	deliveryGUID := req.Headers["x-github-delivery"]
	if deliveryGUID != "" {
		nonceKey := GitHubDeliveryNoncePrefix + deliveryGUID
		replayed, err := h.Nonces.CheckAndStore(ctx, nonceKey, GitHubDeliveryNonceTTLSeconds)
		if err != nil {
			h.log().Error("github-bridge: nonce store error", "err", err)
			return WebhookResponse{StatusCode: 200, Body: "ok"}
		}
		if replayed {
			h.log().Info("github-bridge: replayed delivery", "guid", deliveryGUID)
			return WebhookResponse{StatusCode: 200, Body: "ok"}
		}
	}

	// ── Steps 8–9: Resolve alias + dispatch ──────────────────────────────────

	// ── Phase 99: command pass (dormant when Commands empty AND DefaultCommand "") ─
	// Slotted AFTER dedupe (line ~241) and BEFORE envelope construction.
	// When dormant, this block is skipped entirely — byte-identical to Phase 98.
	// When active, RunCommandPass may:
	//   Reply/Deny  → PostComment reply + return 200 (no dispatch)
	//   Dispatch    → overrides alias/profile/promptBody before envelope construction
	//   Passthrough → falls through to free-form ExtractMentionBody (same as dormant)
	var promptBody string
	if len(h.Commands) > 0 || h.DefaultCommand != "" {
		// Resolve per-repo default command (from matched RepoEntry.DefaultCommand).
		repoDefaultCmd := lookupRepoDefaultCommand(h.Entries, payload.Repository.FullName)

		res := RunCommandPass(
			payload.Comment.Body,
			h.Commands,
			h.DefaultCommand, repoDefaultCmd,
			payload.Comment.User.Login,
			alias, profile, h.DefaultProfile,
			botLogin,
		)

		switch res.Action {
		case CommandActionReply, CommandActionDeny:
			// Post comment reply and return 200 — no dispatch.
			if h.Commenter != nil {
				owner := OwnerFromFullName(payload.Repository.FullName)
				repo := RepoFromFullName(payload.Repository.FullName)
				if cErr := h.Commenter.PostComment(ctx,
					InstallIDString(payload.Installation.ID),
					owner, repo, payload.Issue.Number, res.ReplyText); cErr != nil {
					h.log().Warn("github-bridge: post command reply failed (non-fatal)", "err", cErr)
				}
			}
			return WebhookResponse{StatusCode: 200, Body: "ok"}

		case CommandActionDispatch:
			// Command overrides alias/profile/prompt — fall through to envelope construction.
			alias = res.Alias
			profile = res.Profile
			promptBody = res.Prompt

		case CommandActionPassthrough:
			// No command (and no effective default) → free-form body.
			promptBody = ExtractMentionBody(payload.Comment.Body, botLogin)
		}
	} else {
		// Dormant path: no commands configured — free-form dispatch (Phase 98 behavior).
		promptBody = ExtractMentionBody(payload.Comment.Body, botLogin)
	}

	env := GitHubEnvelope{
		Source:        "github",
		Repo:          payload.Repository.FullName,
		Number:        payload.Issue.Number,
		Kind:          "issue_comment",
		CommentID:     payload.Comment.ID,
		HTMLURL:       payload.Comment.HTMLURL,
		Sender:        payload.Comment.User.Login,
		Body:          promptBody,
		InstallID:     InstallIDString(payload.Installation.ID),
		DefaultBranch: payload.Repository.DefaultBranch,
	}

	envJSON, err := json.Marshal(env)
	if err != nil {
		h.log().Error("github-bridge: marshal envelope", "err", err)
		return WebhookResponse{StatusCode: 200, Body: "ok"}
	}

	// ── Unified 3-way dispatch ────────────────────────────────────────────────
	// Prefer ResolveByAliasWithStatus (available when Resolver also satisfies the
	// extended interface). Falls back to ResolveByAlias for Phase 97 compatibility.
	//
	//   absent  → cold-create  (Publisher.PutSandboxCreate)
	//   stopped/paused → Resumer.StartSandbox + enqueue  (resume path, GH-X-RESUME)
	//   running → enqueue only  (Phase 97 warm path)
	groupID := fmt.Sprintf("github-%s-%d", payload.Repository.FullName, payload.Issue.Number)
	dedupID := fmt.Sprintf("%s-%s", deliveryGUID, groupID)

	if rws, ok := h.Resolver.(SandboxAliasResolverWithStatus); ok {
		// Extended path: status-aware dispatch.
		sandboxID, status, resolveErr := rws.ResolveByAliasWithStatus(ctx, alias)

		// Gap E fix (98-06): if the continuity row holds a sandbox_id from a previous
		// (now destroyed) box, invalidate it so the next dispatch does not carry a
		// cross-box --resume that always fails with "No conversation found". This
		// comparisons uses the sandbox_id stored in the thread row (threadStoredSandboxID,
		// set in Step 4b) vs the freshly alias-resolved sandbox_id.
		if resolveErr == nil && h.Threads != nil &&
			threadStoredSandboxID != "" && threadStoredSandboxID != sandboxID {
			h.log().Info("github-bridge: stale continuity row (sandbox recreated); invalidating session",
				"stored_sandbox_id", threadStoredSandboxID,
				"current_sandbox_id", sandboxID,
				"repo", payload.Repository.FullName, "number", payload.Issue.Number)
			if invErr := h.Threads.InvalidateStaleSession(ctx,
				payload.Repository.FullName, payload.Issue.Number, sandboxID); invErr != nil {
				h.log().Warn("github-bridge: InvalidateStaleSession failed (non-fatal)", "err", invErr)
			}
		}

		if resolveErr != nil {
			// Alias not found → cold-create. The alias is truly absent from DDB — no
			// second sandbox risk (a stopped sandbox holds its alias row).
			h.log().Info("github-bridge: cold create", "alias", alias, "profile", profile,
				"resolve_err", resolveErr)
			if pubErr := h.Publisher.PutSandboxCreate(ctx, alias, profile, string(envJSON)); pubErr != nil {
				h.log().Error("github-bridge: publish SandboxCreate", "err", pubErr)
			}
		} else if (status == "stopped" || status == "paused") && h.Resumer != nil {
			// Resume path: start the EC2 instance, then enqueue so the prompt drains
			// once the box boots. Resumer error is non-fatal (logged); enqueue still
			// happens so the prompt is not lost on a transient StartInstances failure.
			h.log().Info("github-bridge: auto-resume", "alias", alias, "sandbox_id", sandboxID, "status", status)
			if rErr := h.Resumer.StartSandbox(ctx, sandboxID); rErr != nil {
				h.log().Error("github-bridge: auto-resume failed (non-fatal; enqueue continues)", "err", rErr, "sandbox_id", sandboxID)
			} else if h.StatusWriter != nil {
				// Gap B fix (98-06): flip km-sandboxes status=running so km list / km resume
				// see running state and a follow-up @-mention doesn't re-fire StartInstances.
				// UpdateItem only — do NOT PutItem (SandboxMetadata lossy round-trip footgun).
				if swErr := h.StatusWriter.SetStatusRunning(ctx, sandboxID); swErr != nil {
					h.log().Warn("github-bridge: status write-back failed (non-fatal; enqueue continues)",
						"err", swErr, "sandbox_id", sandboxID)
				} else {
					h.log().Info("github-bridge: status write-back running", "sandbox_id", sandboxID)
				}
			}
			if queueURL, qErr := h.Resolver.GitHubQueueURL(ctx, sandboxID); qErr != nil {
				h.log().Error("github-bridge: lookup github queue URL (resume path)", "sandbox_id", sandboxID, "err", qErr)
			} else {
				if sErr := h.SQS.Send(ctx, queueURL, string(envJSON), groupID, dedupID); sErr != nil {
					h.log().Error("github-bridge: SQS enqueue (resume path)", "err", sErr)
				} else if h.Threads != nil {
					if uErr := h.Threads.Upsert(ctx, payload.Repository.FullName, payload.Issue.Number, sandboxID); uErr != nil {
						h.log().Warn("github-bridge: thread upsert failed (non-fatal, resume path)", "err", uErr)
					}
				}
			}
		} else {
			// Running (or stopped without a Resumer) → warm enqueue only.
			h.log().Info("github-bridge: warm enqueue (status-aware)", "alias", alias, "sandbox_id", sandboxID)
			if queueURL, qErr := h.Resolver.GitHubQueueURL(ctx, sandboxID); qErr != nil {
				h.log().Error("github-bridge: lookup github queue URL", "sandbox_id", sandboxID, "err", qErr)
			} else {
				if sErr := h.SQS.Send(ctx, queueURL, string(envJSON), groupID, dedupID); sErr != nil {
					h.log().Error("github-bridge: SQS enqueue", "err", sErr)
				} else if h.Threads != nil {
					if uErr := h.Threads.Upsert(ctx, payload.Repository.FullName, payload.Issue.Number, sandboxID); uErr != nil {
						h.log().Warn("github-bridge: thread upsert failed (non-fatal)", "err", uErr,
							"repo", payload.Repository.FullName, "number", payload.Issue.Number)
					}
				}
			}
		}
	} else {
		// Fallback: Phase 97 behavior — base SandboxAliasResolver, no status awareness.
		sandboxID, resolveErr := h.Resolver.ResolveByAlias(ctx, alias)
		if resolveErr != nil {
			h.log().Info("github-bridge: cold create", "alias", alias, "profile", profile,
				"resolve_err", resolveErr)
			if pubErr := h.Publisher.PutSandboxCreate(ctx, alias, profile, string(envJSON)); pubErr != nil {
				h.log().Error("github-bridge: publish SandboxCreate", "err", pubErr)
			}
		} else {
			// Warm path — enqueue to per-sandbox github-inbound FIFO.
			queueURL, qErr := h.Resolver.GitHubQueueURL(ctx, sandboxID)
			if qErr != nil {
				h.log().Error("github-bridge: lookup github queue URL", "sandbox_id", sandboxID, "err", qErr)
			} else {
				if sErr := h.SQS.Send(ctx, queueURL, string(envJSON), groupID, dedupID); sErr != nil {
					h.log().Error("github-bridge: SQS enqueue", "err", sErr)
				} else if h.Threads != nil {
					if uErr := h.Threads.Upsert(ctx, payload.Repository.FullName, payload.Issue.Number, sandboxID); uErr != nil {
						h.log().Warn("github-bridge: thread upsert failed (non-fatal)", "err", uErr,
							"repo", payload.Repository.FullName, "number", payload.Issue.Number)
					}
				}
			}
			h.log().Info("github-bridge: warm enqueue", "alias", alias, "sandbox_id", sandboxID)
		}
	}

	// ── Step 10: Post 👀 reaction synchronously ──────────────────────────────
	// CRITICAL: synchronous — Lambda freezes when Handle returns (RESEARCH Pitfall 3).
	// Errors are logged but do NOT change the 200 response.
	if h.Reactor != nil {
		owner := OwnerFromFullName(payload.Repository.FullName)
		repo := RepoFromFullName(payload.Repository.FullName)
		if rErr := h.Reactor.AddReaction(ctx,
			InstallIDString(payload.Installation.ID),
			owner, repo, payload.Comment.ID, "eyes"); rErr != nil {
			h.log().Warn("github-bridge: reaction failed (non-fatal)", "err", rErr)
		}
	}

	return WebhookResponse{StatusCode: 200, Body: "ok"}
}

// VerifyGitHubSignature verifies the HMAC-SHA256 signature per GitHub docs.
// Pattern from RESEARCH Pattern 1 / events_handler.go:705 (constant-time).
//
// GitHub sends: X-Hub-Signature-256: sha256=<hex(HMAC-SHA256(rawBody, secret))>
// No timestamp header → no skew check; replay protection via X-GitHub-Delivery dedup.
func VerifyGitHubSignature(secret, sigHeader string, rawBody []byte) error {
	if !strings.HasPrefix(sigHeader, "sha256=") {
		return fmt.Errorf("github-bridge: missing or wrong-format signature header (got %q)", sigHeader)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	// Constant-time compare prevents timing attacks.
	if !hmac.Equal([]byte(expected), []byte(sigHeader)) {
		return fmt.Errorf("github-bridge: signature mismatch")
	}
	return nil
}

// isGitHubBotLoop returns true when the comment author is a Bot or the
// bot's own login (App installs show up as "{app-slug}[bot]" with type="Bot").
func isGitHubBotLoop(u UserField, botLogin string) bool {
	if strings.EqualFold(u.Type, "Bot") {
		return true
	}
	if strings.EqualFold(u.Login, botLogin) {
		return true
	}
	return false
}

// isInAllowlist reports whether login (case-insensitive) is in the allow slice.
// Deny-by-default: empty allow list → always false.
func isInAllowlist(login string, allow []string) bool {
	lower := strings.ToLower(login)
	for _, a := range allow {
		if strings.ToLower(a) == lower {
			return true
		}
	}
	return false
}

// formatInstallID formats an int64 installation ID as a string.
// Kept for backward compat if callers need it; prefer InstallIDString.
func formatInstallID(id int64) string {
	return strconv.FormatInt(id, 10)
}

// lookupRepoDefaultCommand returns the DefaultCommand for the matched RepoEntry
// by scanning entries for fullName. Returns "" when no entry matches (which won't
// happen in practice — Resolve already confirmed a match — but is safe to handle).
// This is a minimal scan because Resolve() returns alias/profile but not the
// RepoEntry itself; keeping it separate avoids widening Resolve's return type.
func lookupRepoDefaultCommand(entries []RepoEntry, fullName string) string {
	// Exact match first (mirrors Resolve's resolution order).
	for _, e := range entries {
		if e.Match == fullName {
			return e.DefaultCommand
		}
	}
	// Glob match — isGlob is defined in resolve.go (same package); path.Match is stdlib.
	for _, e := range entries {
		if isGlob(e.Match) {
			ok, err := path.Match(e.Match, fullName)
			if err == nil && ok {
				return e.DefaultCommand
			}
		}
	}
	return ""
}
