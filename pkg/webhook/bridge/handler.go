package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/webhook"
)

// Request is the transport-neutral view of an inbound Function URL request.
type Request struct {
	Path            string
	ContentEncoding string
	Headers         map[string]string // MUST be lowercase-keyed
	Body            []byte
}

// Response carries the HTTP status and a diagnostic log tag.
type Response struct {
	Status int
	Log    string
}

// Envelope is what lands on the sandbox queue and what the poller renders.
type QueueEnvelope struct {
	Source   string `json:"source"`
	Type     string `json:"type"`
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Prompt   string `json:"prompt"`
	Raw      string `json:"raw"`
}

// Handler owns the dispatch decision for every configured source.
type Handler struct {
	Sources   []config.WebhookSource
	RateLimit *config.WebhookRateLimit

	Secrets  SecretFetcher
	Resolver AliasResolver
	Queue    QueueSender
	Resumer  Resumer
	Status   StatusWriter
	Cold     ColdCreator
	Nonces   webhook.NonceStore
	Rates    webhook.RateCounter

	// Now returns the current unix time; injected for deterministic tests.
	Now func() int64
}

// ok is the single response constructor. EVERY path returns 200, including
// internal errors: a non-200 makes the sender redeliver, and for a source with
// a delivery id that redelivery walks straight past dedup.
func ok(tag string) Response { return Response{Status: 200, Log: tag} }

// Handle runs the full pipeline for one inbound request.
func (h *Handler) Handle(ctx context.Context, req Request) Response {
	src := h.findSource(req.Path)
	if src == nil {
		slog.WarnContext(ctx, "webhook_unknown_source", "path", req.Path)
		return ok("unknown_source")
	}

	body, err := webhook.Decompress(req.Body, req.ContentEncoding)
	if err != nil {
		slog.WarnContext(ctx, "webhook_decompress_failed", "source", src.Name, "error", err.Error())
		return ok("decompress_failed")
	}

	secret, err := h.Secrets.Fetch(ctx, src.Auth.SecretPath)
	if err != nil {
		slog.ErrorContext(ctx, "webhook_secret_fetch_failed", "source", src.Name, "error", err.Error())
		return ok("secret_fetch_failed")
	}
	if err := webhook.Authenticate(src.Auth, secret, req.Headers, body); err != nil {
		slog.WarnContext(ctx, "webhook_unauthorized", "source", src.Name)
		return ok("unauthorized")
	}

	env, err := webhook.ParseEnvelope(body, *src)
	if err != nil {
		slog.WarnContext(ctx, "webhook_unparseable", "source", src.Name, "error", err.Error())
		return ok("unparseable")
	}

	// Replay gate — fail CLOSED on a storage error would strand real alerts, so
	// this follows the bridges: an error logs and proceeds.
	replayKey := webhook.ReplayKey(src.Name, env)
	ttl := src.ReplayTTLSeconds
	if ttl <= 0 {
		ttl = config.DefaultReplayTTLSeconds
	}
	if seen, nerr := h.Nonces.CheckAndStore(ctx, replayKey, ttl); nerr != nil {
		slog.WarnContext(ctx, "webhook_replay_nonce_error", "source", src.Name, "error", nerr.Error())
	} else if seen {
		slog.DebugContext(ctx, "webhook_replay_dropped", "source", src.Name, "key", replayKey)
		return ok("replay")
	}

	rule, idx := webhook.MatchRule(env, src.Rules)
	if rule == nil {
		slog.DebugContext(ctx, "webhook_no_rule_match", "source", src.Name, "id", env.ID)
		return ok("no_match")
	}

	// Cooldown gate — fail OPEN.
	if rule.CooldownSeconds > 0 {
		key := webhook.CooldownKey(src.Name, idx, rule.GroupBy, env)
		if seen, cerr := h.Nonces.CheckAndStore(ctx, key, rule.CooldownSeconds); cerr != nil {
			slog.WarnContext(ctx, "webhook_cooldown_nonce_error", "source", src.Name, "error", cerr.Error())
		} else if seen {
			slog.DebugContext(ctx, "webhook_cooldown_suppressed", "source", src.Name, "key", key)
			return ok("cooldown")
		}
	}

	// Rate ceiling — AFTER replay and cooldown, so suppressed duplicates cost
	// nothing. Fails OPEN.
	if !webhook.CheckRate(ctx, h.Rates, h.RateLimit, h.Now()) {
		return ok("rate_limited")
	}

	prompt := webhook.ExpandTemplate(rule.Prompt, env)
	return h.dispatch(ctx, src.Name, rule, env, prompt)
}

func (h *Handler) findSource(path string) *config.WebhookSource {
	name := strings.Trim(path, "/")
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}
	for i := range h.Sources {
		if strings.EqualFold(h.Sources[i].Name, name) {
			return &h.Sources[i]
		}
	}
	return nil
}

// dispatch resolves the alias and routes warm / resume / cold.
func (h *Handler) dispatch(ctx context.Context, source string, rule *config.WebhookRule,
	env *webhook.Envelope, prompt string) Response {

	sandboxID, status, rerr := h.Resolver.ResolveByAliasWithStatus(ctx, rule.Alias)
	if rerr != nil || status == "" {
		return h.coldPath(ctx, rule, env, prompt, "absent")
	}

	if isStopped(status) {
		if serr := h.Resumer.StartSandbox(ctx, sandboxID); serr != nil {
			if errors.Is(serr, ErrNoResumableInstance) {
				// Terminal: the row is orphaned. Clear it so the alias reads as
				// absent, then cold-create. Leaving it would let the stale row
				// shadow creation forever (Phase 109).
				if derr := h.Status.DeleteSandboxRow(ctx, sandboxID); derr != nil {
					slog.ErrorContext(ctx, "webhook_stale_row_delete_failed",
						"sandbox_id", sandboxID, "error", derr.Error())
				}
				return h.coldPath(ctx, rule, env, prompt, "orphaned_row")
			}
			// Transient: log and still enqueue — the prompt drains when the box
			// comes back rather than being lost.
			slog.WarnContext(ctx, "webhook_resume_transient_error",
				"sandbox_id", sandboxID, "error", serr.Error())
		}
	}

	queueURL, qerr := h.Resolver.QueueURL(ctx, sandboxID)
	if qerr != nil {
		slog.ErrorContext(ctx, "webhook_queue_url_missing",
			"sandbox_id", sandboxID, "error", qerr.Error())
		return ok("queue_url_missing")
	}

	payload, merr := json.Marshal(QueueEnvelope{
		Source: source, Type: env.Type, ID: env.ID, Severity: env.Severity,
		Title: env.Title, URL: env.URL, Prompt: prompt, Raw: env.Raw,
	})
	if merr != nil {
		slog.ErrorContext(ctx, "webhook_marshal_failed", "error", merr.Error())
		return ok("marshal_failed")
	}

	// MessageGroupId = sandbox id => strictly serial per box.
	if serr := h.Queue.Send(ctx, queueURL, sandboxID, string(payload)); serr != nil {
		slog.ErrorContext(ctx, "webhook_enqueue_failed",
			"sandbox_id", sandboxID, "error", serr.Error())
		return ok("enqueue_failed")
	}

	slog.InfoContext(ctx, "webhook_dispatched",
		"source", source, "sandbox_id", sandboxID, "id", env.ID, "severity", env.Severity)
	return ok("dispatched")
}

func (h *Handler) coldPath(ctx context.Context, rule *config.WebhookRule,
	env *webhook.Envelope, prompt, reason string) Response {

	if strings.EqualFold(rule.OnAbsent, "skip") {
		slog.InfoContext(ctx, "webhook_skipped_absent", "alias", rule.Alias, "reason", reason)
		return ok("skipped_absent")
	}
	if err := h.Cold.ColdCreate(ctx, rule.Alias, rule.Profile, prompt); err != nil {
		slog.ErrorContext(ctx, "webhook_cold_create_failed",
			"alias", rule.Alias, "error", err.Error())
		return ok("cold_create_failed")
	}
	slog.InfoContext(ctx, "webhook_cold_created",
		"alias", rule.Alias, "profile", rule.Profile, "reason", reason, "id", env.ID)
	return ok("cold_created")
}

func isStopped(status string) bool {
	switch strings.ToLower(status) {
	case "stopped", "paused", "stopping":
		return true
	}
	return false
}
