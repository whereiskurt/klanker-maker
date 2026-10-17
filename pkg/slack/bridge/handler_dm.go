package bridge

import (
	"context"
	"regexp"

	"github.com/whereiskurt/klanker-maker/pkg/quota"
	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

var reDMTarget = regexp.MustCompile(`^[UW][A-Z0-9]+$`)

// handleDM is ActionDM (2026-10-17): a sandbox asks the bot to DM a person.
// Every refusal is fail-closed and names its step. Order: route configured →
// target shape → target email on the route allowlist → --from-thread
// authorization + permalink → quota → conversations.open → post → thread row.
func (h *Handler) handleDM(ctx context.Context, env slack.SlackEnvelope) *Response {
	if h.DefaultRoute == nil || h.SenderEmails == nil {
		logger.WarnContext(ctx, "bridge: dm_disabled", "step", "dm", "sender_id", env.SenderID, "status", 403)
		return errResp(403, "dm_disabled")
	}
	if !reDMTarget.MatchString(env.TargetUser) {
		logger.WarnContext(ctx, "bridge: dm_bad_target", "step", "dm", "sender_id", env.SenderID, "status", 400)
		return errResp(400, "bad_target")
	}
	// The posting sandbox's allowEmails, when its row carries one, replaces the
	// install list (the same override the inbound path applies). A fetch error
	// denies (fail closed): the row may carry a narrower list we cannot see. An
	// absent row (no error, empty info) uses the install list. The route
	// profile's own list never gates a DM target: it governs who may create or
	// reach the route's box, not whom any sandbox may message.
	allows, listName := h.DefaultRoute.Allows, "allow_emails"
	if h.SandboxByID != nil {
		info, ferr := h.SandboxByID.FetchBySandboxID(ctx, env.SenderID)
		if ferr != nil {
			logger.WarnContext(ctx, "bridge: dm_sender_row_unavailable",
				"step", "dm", "sender_id", env.SenderID, "error", ferr.Error(), "status", 403)
			return errResp(403, "dm_not_allowed")
		}
		if len(info.AllowEmails) > 0 {
			al, cerr := CompileEmailAllowlist(info.AllowEmails)
			if cerr != nil {
				logger.ErrorContext(ctx, "bridge: dm_sandbox_allow_emails_invalid; denying",
					"step", "dm", "sender_id", env.SenderID, "error", cerr.Error())
			}
			allows, listName = al.Allows, "sandbox allowEmails" // nil al denies everyone
		}
	}
	email, err := h.SenderEmails.SenderEmail(ctx, env.TargetUser)
	if err != nil || !allows(email) {
		reason := "not on " + listName
		if err != nil {
			reason = err.Error()
		}
		logger.WarnContext(ctx, "bridge: dm_not_allowed", "step", "dm", "sender_id", env.SenderID,
			"target", env.TargetUser, "reason", reason, "status", 403)
		return errResp(403, "dm_not_allowed")
	}

	body, blocks := env.Body, env.Blocks
	fromThread := env.Channel != "" && env.ThreadTS != ""
	if !fromThread && (env.Channel != "" || env.ThreadTS != "") {
		empty := "thread_ts"
		if env.Channel == "" {
			empty = "channel"
		}
		logger.WarnContext(ctx, "bridge: dm_from_thread_ignored", "step", "dm", "sender_id", env.SenderID,
			"empty_field", empty, "channel", env.Channel, "thread_ts", env.ThreadTS)
	}
	if fromThread {
		owned, oerr := h.Channels.OwnedChannel(ctx, env.SenderID)
		if oerr != nil {
			logger.ErrorContext(ctx, "bridge: channel_lookup_failed", "step", "dm", "sender_id", env.SenderID, "error", oerr.Error(), "status", 500)
			return errResp(500, "channel_lookup_failed")
		}
		if owned == "" || owned != env.Channel {
			probe := env
			probe.Action = slack.ActionPost // threadRoutedToSender admits post/upload only
			if !h.threadRoutedToSender(ctx, probe) {
				logger.WarnContext(ctx, "bridge: channel_mismatch", "step", "dm", "sender_id", env.SenderID,
					"channel", env.Channel, "thread_ts", env.ThreadTS, "status", 403)
				return errResp(403, "channel_mismatch")
			}
		}
		if link, perr := h.Slack.GetPermalink(ctx, env.Channel, env.ThreadTS); perr != nil {
			logger.WarnContext(ctx, "bridge: dm_permalink_failed", "step", "dm", "error", perr.Error())
		} else {
			line := "From: " + link
			body += "\n\n" + line
			if blocks != "" {
				if withLink, ok := slack.AppendContextBlock(blocks, line); ok {
					blocks = withLink
				} else {
					blocks = "" // text fallback still carries the link
				}
			}
		}
	}

	// The trip notice lands on the originating thread when there is one; a
	// top-level dm has no channel yet (the DM is opened after the quota check).
	noticeChannel, noticeThread := "", ""
	if fromThread {
		noticeChannel, noticeThread = env.Channel, env.ThreadTS
	}
	if _, block := h.checkQuota(ctx, env.SenderID, quota.ActionSlackPost, noticeChannel, noticeThread); block {
		logger.WarnContext(ctx, "bridge: quota_exceeded", "step", "dm", "sender_id", env.SenderID,
			"target", env.TargetUser, "status", 429)
		return errResp(429, "quota_exceeded")
	}

	opener, ok := h.Slack.(DMOpener)
	if !ok {
		logger.ErrorContext(ctx, "bridge: dm_unsupported", "step", "dm", "status", 500)
		return errResp(500, "dm_unsupported")
	}
	dmChannel, err := opener.OpenDM(ctx, env.TargetUser)
	if err != nil {
		logger.ErrorContext(ctx, "bridge: slack_call_failed", "step", "dm", "action", "conversations.open",
			"target", env.TargetUser, "slack_error", err.Error(), "status", 502)
		return slackResponse("", err)
	}

	var ts string
	if blocks != "" {
		if bp, okBP := h.Slack.(BlockPoster); okBP {
			ts, err = bp.PostMessageBlocks(ctx, dmChannel, env.Subject, body, blocks, "")
		} else {
			ts, err = h.Slack.PostMessage(ctx, dmChannel, env.Subject, body, "")
		}
	} else {
		ts, err = h.Slack.PostMessage(ctx, dmChannel, env.Subject, body, "")
	}
	if err != nil {
		resp := slackResponse("", err)
		logger.ErrorContext(ctx, "bridge: slack_call_failed", "step", "dm", "action", "chat.postMessage",
			"channel", dmChannel, "slack_error", err.Error(), "status", resp.StatusCode)
		return resp
	}
	// Continuity: a reply under this DM message routes back to this sandbox.
	// Written only after the post succeeded; a failure is logged, the DM stands.
	if h.Threads != nil {
		if uerr := h.Threads.Upsert(ctx, dmChannel, ts, env.SenderID); uerr != nil {
			logger.WarnContext(ctx, "bridge: dm_thread_row_failed", "step", "dm", "channel", dmChannel, "ts", ts, "error", uerr.Error())
		}
	} else {
		logger.WarnContext(ctx, "bridge: dm_no_thread_store", "step", "dm", "sender_id", env.SenderID,
			"channel", dmChannel, "ts", ts)
	}
	logger.InfoContext(ctx, "bridge: dm", "sender_id", env.SenderID, "target", env.TargetUser, "channel", dmChannel, "ts", ts, "status", 200)
	return jsonResp(200, map[string]any{"ok": true, "ts": ts, "channel_id": dmChannel})
}
