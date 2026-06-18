package dispatch

import (
	"context"
	"log/slog"
)

// ResumeOrCreate performs the alias-resolution + resume/cold-create DECISION for
// the km check runner (Phase 116 Stage B). It is the shared decision core that
// Plan 116-06 wires into the ttl-handler CheckDispatch event case.
//
// Decision logic (locked from CONTEXT.md Stage B):
//
//  1. Cooldown check (if cooldownSeconds > 0): call nonces.CheckAndStore with
//     key "check-trigger:{check}". If alreadySeen → log + drop (return nil).
//     Fail-open on nonce store errors: proceed to dispatch so a real fire is never
//     stranded on a transient read error (mirrors bridge fail-open semantics).
//
//  2. Resolve alias via resolver.ResolveByAliasWithStatus(alias):
//     - err == nil && status != "" → sandbox exists in some non-terminated state
//       → agentRun.DispatchAgentRun(ctx, sandboxID, prompt); return its err.
//       The AgentRunSink owns auto-resume (stopped/paused → EC2 start + SSM dispatch);
//       the dispatch decision here is simply "exists → agent-run".
//     - Otherwise (resolve err / alias absent) → switch on onAbsent:
//       "cold-create" (or "" default) → cold.ColdCreate(ctx, alias, profile, prompt)
//       "skip"                         → log "check_dispatch_skipped_absent" + return nil
//
// Parameters:
//
//	check           — check name for cooldown key + log context.
//	alias           — sandbox alias to resolve.
//	prompt          — expanded prompt text to dispatch.
//	profile         — SandboxProfile name for cold-create (may be empty for warm-only).
//	onAbsent        — "cold-create" or "skip"; empty string treated as "cold-create".
//	cooldownSeconds — 0 = no cooldown; >0 = suppress within this window via nonces.
//	resolver        — AliasResolver backed by km-sandboxes alias-index GSI.
//	agentRun        — AgentRunSink (warm path: SSM dispatch, owns auto-resume).
//	cold            — ColdCreateSink (absent path: EventBridge SandboxCreate).
//	nonces          — NonceStore backed by km nonces DynamoDB table.
//	log             — structured logger for diagnostic events.
func ResumeOrCreate(
	ctx context.Context,
	check, alias, prompt, profile, onAbsent string,
	cooldownSeconds int,
	resolver AliasResolver,
	agentRun AgentRunSink,
	cold ColdCreateSink,
	nonces NonceStore,
	log *slog.Logger,
) error {
	// Step 1: Cooldown check.
	if cooldownSeconds > 0 {
		key := "check-trigger:" + check
		alreadySeen, err := nonces.CheckAndStore(ctx, key, cooldownSeconds)
		if err != nil {
			// Fail-open: nonce store error → proceed to dispatch (never strand a real fire).
			log.WarnContext(ctx, "check_cooldown_nonce_error",
				slog.String("check", check),
				slog.String("alias", alias),
				slog.String("error", err.Error()),
			)
		} else if alreadySeen {
			log.DebugContext(ctx, "check_cooldown_suppressed",
				slog.String("check", check),
				slog.String("alias", alias),
				slog.Int("cooldown_seconds", cooldownSeconds),
			)
			return nil
		}
	}

	// Step 2: Resolve alias.
	sandboxID, status, resolveErr := resolver.ResolveByAliasWithStatus(ctx, alias)

	if resolveErr == nil && status != "" {
		// Sandbox exists (any non-terminated state: running, stopped, paused, etc.).
		// The AgentRunSink owns auto-resume for stopped/paused boxes; the decision
		// here is purely "exists → agent-run". This matches the CONTEXT "RESOLVED"
		// locked decision: warm path = SSM agent-run dispatch, NOT SQS enqueue.
		log.InfoContext(ctx, "check_dispatch_agent_run",
			slog.String("check", check),
			slog.String("alias", alias),
			slog.String("sandbox_id", sandboxID),
			slog.String("status", status),
		)
		return agentRun.DispatchAgentRun(ctx, sandboxID, prompt)
	}

	// Alias absent (resolve error) or status "" (backward-compat: treated as absent
	// since status="" historically indicated an uninitialized or pruned row — the
	// AgentRunSink path requires a known, addressable sandbox).
	//
	// Note: status="" is the DDB absence case per bridge convention. A box that has
	// been set to status="" would be an anomaly; we err on the side of cold-create
	// (same as absent) rather than dispatching blindly.
	if resolveErr != nil {
		log.DebugContext(ctx, "check_dispatch_alias_absent",
			slog.String("check", check),
			slog.String("alias", alias),
			slog.String("resolve_error", resolveErr.Error()),
		)
	} else {
		// resolveErr == nil but status == "": treat as absent (DDB row exists but
		// has no status attribute — pre-Phase-67 backward compat row; safe to cold-create).
		log.DebugContext(ctx, "check_dispatch_alias_no_status",
			slog.String("check", check),
			slog.String("alias", alias),
		)
	}

	// Apply onAbsent policy.
	switch onAbsent {
	case "skip":
		log.InfoContext(ctx, "check_dispatch_skipped_absent",
			slog.String("check", check),
			slog.String("alias", alias),
			slog.String("on_absent", onAbsent),
		)
		return nil
	default:
		// "cold-create" or "" (empty = default).
		log.InfoContext(ctx, "check_dispatch_cold_create",
			slog.String("check", check),
			slog.String("alias", alias),
			slog.String("profile", profile),
		)
		return cold.ColdCreate(ctx, alias, profile, prompt)
	}
}
