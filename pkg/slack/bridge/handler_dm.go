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
	email, err := h.SenderEmails.SenderEmail(ctx, env.TargetUser)
	if err != nil || !h.DefaultRoute.Allows(email) {
		reason := "not on allow_emails"
		if err != nil {
			reason = err.Error()
		}
		logger.WarnContext(ctx, "bridge: dm_not_allowed", "step", "dm", "sender_id", env.SenderID,
			"target", env.TargetUser, "reason", reason, "status", 403)
		return errResp(403, "dm_not_allowed")
	}

	body, blocks := env.Body, env.Blocks
	if env.Channel != "" && env.ThreadTS != "" {
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

	if _, block := h.checkQuota(ctx, env.SenderID, quota.ActionSlackPost, "", ""); block {
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
		logger.ErrorContext(ctx, "bridge: slack_call_failed", "step", "dm", "action", "chat.postMessage",
			"channel", dmChannel, "slack_error", err.Error())
		return slackResponse("", err)
	}
	// Continuity: a reply under this DM message routes back to this sandbox.
	// Written only after the post succeeded; a failure is logged, the DM stands.
	if h.Threads != nil {
		if uerr := h.Threads.Upsert(ctx, dmChannel, ts, env.SenderID); uerr != nil {
			logger.WarnContext(ctx, "bridge: dm_thread_row_failed", "step", "dm", "channel", dmChannel, "ts", ts, "error", uerr.Error())
		}
	}
	logger.InfoContext(ctx, "bridge: dm", "sender_id", env.SenderID, "target", env.TargetUser, "channel", dmChannel, "ts", ts, "status", 200)
	return jsonResp(200, map[string]any{"ok": true, "ts": ts, "channel_id": dmChannel})
}
