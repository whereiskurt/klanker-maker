package bridge

import (
	"context"
	"strings"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// handleReact is ActionReact (2026-10-22): a sandbox adds or removes the bot's
// reaction on a message in its own channel. Channel ownership was already
// enforced at step 6 (react is not in the routed-thread carve-out, so a
// thread-anchored envelope in a foreign channel is still channel_mismatch).
//
// Order: target ts → emoji names (re-validated here, the client is not
// trusted) → exclusive cap → poster capability → one reactions.add (or
// reactions.remove with Remove) → one reactions.remove per other exclusive
// name. Slack's already_reacted and no_reaction are success, so a retried
// react never fails on its own earlier success; the first other error stops
// the fan-out and surfaces with its Slack code.
func (h *Handler) handleReact(ctx context.Context, env slack.SlackEnvelope) *Response {
	if env.MessageTS == "" {
		logger.WarnContext(ctx, "bridge: missing_message_ts", "step", "react", "sender_id", env.SenderID, "status", 400)
		return errResp(400, "missing_message_ts")
	}
	emoji, err := slack.NormalizeEmojiName(env.Emoji)
	if err != nil {
		logger.WarnContext(ctx, "bridge: react_bad_emoji", "step", "react", "sender_id", env.SenderID, "error", err.Error(), "status", 400)
		return errResp(400, "bad_emoji")
	}
	if len(env.Exclusive) > slack.MaxExclusiveReactions {
		logger.WarnContext(ctx, "bridge: react_too_many_exclusive", "step", "react", "sender_id", env.SenderID, "count", len(env.Exclusive), "status", 400)
		return errResp(400, "too_many_exclusive")
	}
	others := make([]string, 0, len(env.Exclusive))
	for _, raw := range env.Exclusive {
		n, nerr := slack.NormalizeEmojiName(raw)
		if nerr != nil {
			logger.WarnContext(ctx, "bridge: react_bad_emoji", "step", "react", "sender_id", env.SenderID, "error", nerr.Error(), "status", 400)
			return errResp(400, "bad_emoji")
		}
		if n != emoji {
			others = append(others, n)
		}
	}

	rp, ok := h.Slack.(ReactionPoster)
	if !ok {
		logger.ErrorContext(ctx, "bridge: react_unsupported", "step", "react", "status", 500)
		return errResp(500, "react_unsupported")
	}

	if env.Remove {
		if rerr := rp.RemoveReaction(ctx, env.Channel, env.MessageTS, emoji); rerr != nil && !isIdempotentReactionError(rerr) {
			return h.reactFailed(ctx, env, "reactions.remove", emoji, rerr)
		}
		logger.InfoContext(ctx, "bridge: ok", "action", env.Action, "channel", env.Channel, "ts", env.MessageTS, "emoji", emoji, "removed", true, "status", 200)
		return slackResponse(env.MessageTS, nil)
	}

	if aerr := rp.AddReaction(ctx, env.Channel, env.MessageTS, emoji); aerr != nil && !isIdempotentReactionError(aerr) {
		return h.reactFailed(ctx, env, "reactions.add", emoji, aerr)
	}
	for _, o := range others {
		if rerr := rp.RemoveReaction(ctx, env.Channel, env.MessageTS, o); rerr != nil && !isIdempotentReactionError(rerr) {
			return h.reactFailed(ctx, env, "reactions.remove", o, rerr)
		}
	}
	logger.InfoContext(ctx, "bridge: ok", "action", env.Action, "channel", env.Channel, "ts", env.MessageTS, "emoji", emoji, "exclusive_removed", len(others), "status", 200)
	return slackResponse(env.MessageTS, nil)
}

func (h *Handler) reactFailed(ctx context.Context, env slack.SlackEnvelope, method, emoji string, err error) *Response {
	resp := slackResponse("", err)
	logger.ErrorContext(ctx, "bridge: slack_call_failed",
		"step", "react",
		"action", env.Action,
		"method", method,
		"channel", env.Channel,
		"ts", env.MessageTS,
		"emoji", emoji,
		"slack_error", err.Error(),
		"status", resp.StatusCode,
	)
	return resp
}

// isIdempotentReactionError reports whether err carries one of the two Slack
// codes that mean "already in the requested state": already_reacted from
// reactions.add and no_reaction from reactions.remove. The adapter returns the
// code as the last colon-separated token, the same shape slackResponse parses.
func isIdempotentReactionError(err error) bool {
	msg := err.Error()
	code := msg
	if i := strings.LastIndex(msg, ":"); i >= 0 {
		code = msg[i+1:]
	}
	switch strings.TrimSpace(code) {
	case "already_reacted", "no_reaction":
		return true
	}
	return false
}
