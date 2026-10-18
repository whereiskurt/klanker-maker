package bridge

import (
	"context"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// handleSession is ActionSession (2026-10-18): a poller records its agent
// session id + agent_type on a thread row. The row must exist (the bridge
// dispatched into it, or claim_thread created it) and name the caller; a box
// can never mint a row here. An empty session_id clears the attribute.
//
// This check is the action's whole authorization: Handle exempts
// ActionSession from the channel-ownership block, because owning a channel is
// neither necessary nor sufficient for owning a thread row.
func (h *Handler) handleSession(ctx context.Context, env slack.SlackEnvelope) *Response {
	if env.AgentType != "claude" && env.AgentType != "codex" {
		return h.sessionReject(ctx, env, 400, "missing_fields", "agent_type must be claude or codex")
	}
	var owner string
	var err error
	var write func() error
	switch env.Source {
	case "slack":
		if env.Channel == "" || env.ThreadTS == "" {
			return h.sessionReject(ctx, env, 400, "missing_fields", "channel and thread_ts required")
		}
		if h.Threads == nil {
			return h.sessionReject(ctx, env, 503, "source_unavailable", "bridge has no slack threads table")
		}
		owner, err = h.Threads.LookupSandbox(ctx, env.Channel, env.ThreadTS)
		write = func() error {
			return h.Threads.SetSession(ctx, env.Channel, env.ThreadTS, env.SessionID, env.AgentType)
		}
	case "github":
		if env.Repo == "" || env.Number <= 0 {
			return h.sessionReject(ctx, env, 400, "missing_fields", "repo and number required")
		}
		if h.GitHubSessions == nil {
			return h.sessionReject(ctx, env, 503, "source_unavailable", "bridge has no github threads table")
		}
		k := ThreadKey{Repo: env.Repo, Number: env.Number}
		owner, err = h.GitHubSessions.LookupSandbox(ctx, k)
		write = func() error { return h.GitHubSessions.SetSession(ctx, k, env.SessionID, env.AgentType) }
	case "h1":
		if env.ReportID == "" || env.Target == "" {
			return h.sessionReject(ctx, env, 400, "missing_fields", "report_id and target required")
		}
		if h.H1Sessions == nil {
			return h.sessionReject(ctx, env, 503, "source_unavailable", "bridge has no h1 threads table")
		}
		k := ThreadKey{ReportID: env.ReportID, Target: env.Target}
		owner, err = h.H1Sessions.LookupSandbox(ctx, k)
		write = func() error { return h.H1Sessions.SetSession(ctx, k, env.SessionID, env.AgentType) }
	default:
		return h.sessionReject(ctx, env, 400, "missing_fields", "source must be slack, github or h1")
	}
	if err != nil {
		logger.ErrorContext(ctx, "bridge: session_lookup_failed", "step", "session", "source", env.Source, "sender_id", env.SenderID, "error", err.Error(), "status", 500)
		return errResp(500, "thread_lookup_failed")
	}
	if owner == "" {
		return h.sessionReject(ctx, env, 404, "thread_unknown", "no row for this thread")
	}
	if owner != env.SenderID {
		return h.sessionReject(ctx, env, 403, "thread_not_owned", "row names another sandbox")
	}
	if werr := write(); werr != nil {
		logger.ErrorContext(ctx, "bridge: session_write_failed", "step", "session", "source", env.Source, "sender_id", env.SenderID, "error", werr.Error(), "status", 500)
		return errResp(500, "session_write_failed")
	}
	logger.InfoContext(ctx, "bridge: session", "source", env.Source, "sender_id", env.SenderID, "agent_type", env.AgentType, "cleared", env.SessionID == "", "status", 200)
	return jsonResp(200, map[string]any{"ok": true})
}

func (h *Handler) sessionReject(ctx context.Context, env slack.SlackEnvelope, status int, code, why string) *Response {
	logger.WarnContext(ctx, "bridge: session_"+code, "step", "session", "source", env.Source, "sender_id", env.SenderID, "reason", why, "status", status)
	return errResp(status, code)
}
