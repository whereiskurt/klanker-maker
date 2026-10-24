package bridge

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/whereiskurt/klanker-maker/pkg/quota"
	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// HistoryOwnChannelMax caps a read of the sandbox's own channel (the
// MaxContextMessages figure): an own channel has no route to supply max_messages.
const HistoryOwnChannelMax = 100

var reSlackTS = regexp.MustCompile(`^[0-9]{1,11}(\.[0-9]{1,6})?$`)

// handleHistory serves the signed `history` action: a bounded read of a channel
// (mirror or live) or one thread (always live). Authorisation is fail-closed:
// the sender's own channel, else a channel route that grants history.query and
// resolves to the sender's box. A thread row NEVER authorises a read, and the
// result is not filtered by sender: channel membership is the permission.
func (h *Handler) handleHistory(ctx context.Context, env slack.SlackEnvelope) *Response {
	if env.Limit < 0 {
		return errResp(400, "bad_limit")
	}
	for _, ts := range []string{env.Oldest, env.Latest, env.Before, env.MessageTS} {
		if ts != "" && !reSlackTS.MatchString(ts) {
			return errResp(400, "bad_ts")
		}
	}
	owned, err := h.Channels.OwnedChannel(ctx, env.SenderID)
	if err != nil {
		return errResp(500, "channel_lookup_failed")
	}
	var route *DefaultRoute
	capMax := HistoryOwnChannelMax
	if owned != env.Channel {
		route = h.historyRoute(ctx, env.Channel)
		if route == nil || route.History == nil || !route.History.Query {
			logger.WarnContext(ctx, "bridge: history_not_allowed", "step", "history", "sender_id", env.SenderID, "channel", env.Channel, "reason", "no route with history.query", "status", 403)
			return errResp(403, "history_not_allowed")
		}
		if h.AliasResolver == nil {
			return errResp(503, "history_unavailable")
		}
		info, found, rerr := h.AliasResolver.ResolveAlias(ctx, route.Alias)
		if rerr != nil || !found || info.SandboxID != env.SenderID {
			logger.WarnContext(ctx, "bridge: history_not_allowed", "step", "history", "sender_id", env.SenderID, "channel", env.Channel, "alias", route.Alias, "reason", "sender is not the route's box", "status", 403)
			return errResp(403, "history_not_allowed")
		}
		if route.History.MaxMessages > 0 {
			capMax = route.History.MaxMessages
		}
	}
	if _, block := h.checkQuota(ctx, env.SenderID, quota.ActionSlackHistory, env.Channel, env.MessageTS); block {
		return errResp(429, "quota_exceeded")
	}
	limit := env.Limit
	if limit <= 0 || limit > capMax {
		limit = capMax
	}

	var rows []slack.HistoryMessage
	var more bool
	source := "live"
	switch {
	case env.MessageTS != "": // one thread, always live
		if h.ThreadReader == nil {
			return errResp(503, "history_unavailable")
		}
		replies, rerr := h.ThreadReader.Replies(ctx, env.Channel, env.MessageTS, env.Oldest)
		if rerr != nil {
			return h.historyFailed(ctx, env, rerr)
		}
		rows = historyFromReplies(replies, env.Latest, env.Before)
	case route != nil && route.History.Mirror && h.History != nil:
		source = "mirror"
		// Over-fetch so grep (applied before the cap) still has candidates.
		stored, m, qerr := h.History.Query(ctx, env.Channel, env.Oldest, env.Latest, env.Before, limit*2+1)
		if qerr != nil {
			return h.historyFailed(ctx, env, qerr)
		}
		rows, more = historyFromRows(stored), m
	default:
		if h.HistoryReader == nil {
			return errResp(503, "history_unavailable")
		}
		latest := env.Latest
		if env.Before != "" && (latest == "" || env.Before < latest) {
			latest = env.Before
		}
		page := limit
		if h.HistoryPageSize > 0 && page > h.HistoryPageSize {
			page = h.HistoryPageSize
		}
		if page > MaxHistoryPageSize {
			page = MaxHistoryPageSize
		}
		msgs, m, rerr := h.HistoryReader.History(ctx, env.Channel, env.Oldest, latest, page)
		if rerr != nil {
			return h.historyFailed(ctx, env, rerr)
		}
		rows, more = historyFromReplies(msgs, "", ""), m
	}
	rows = filterHistory(rows, env.Grep)
	rows, truncated := capHistory(rows, limit, MaxContextTextBytes)
	resp := slack.PostResponse{OK: true, Source: source, Messages: rows, Truncated: truncated || more}
	if resp.Truncated && len(rows) > 0 {
		resp.NextBefore = rows[len(rows)-1].TS
	}
	logger.InfoContext(ctx, "bridge: history", "sender_id", env.SenderID, "channel", env.Channel, "source", source, "count", len(rows), "truncated", resp.Truncated, "grep", env.Grep != "", "status", 200)
	return jsonResp(200, resp)
}

// historyRoute is the static table, then the meta row (a glob matched at join).
func (h *Handler) historyRoute(ctx context.Context, channel string) *DefaultRoute {
	if r, ok := h.Routes[channel]; ok && r != nil {
		return r
	}
	if h.History == nil {
		return nil
	}
	meta, err := h.History.GetMeta(ctx, channel)
	if err != nil || meta == nil {
		return nil
	}
	return BuildRouteTable(&RoutesDocument{V: RoutesDocumentVersion, Routes: []RouteEntry{meta.Route}}, func(string, ...any) {})[channel]
}

func (h *Handler) historyFailed(ctx context.Context, env slack.SlackEnvelope, err error) *Response {
	resp := slackResponse("", err) // 503 + Retry-After for *ErrSlackRateLimited, 502 + code otherwise
	logger.ErrorContext(ctx, "bridge: slack_call_failed", "step", "history", "channel", env.Channel, "slack_error", err.Error(), "status", resp.StatusCode)
	return resp
}

func historyFromReplies(in []SlackReplyMessage, latest, before string) []slack.HistoryMessage {
	out := make([]slack.HistoryMessage, 0, len(in))
	for _, m := range in {
		if m.TS == "" || (latest != "" && m.TS > latest) || (before != "" && m.TS >= before) || historySystemSubtypes[m.Subtype] {
			continue
		}
		hm := slack.HistoryMessage{TS: m.TS, User: m.User, BotID: m.BotID, Text: m.Text, ThreadTS: m.ThreadTS}
		for _, f := range m.Files {
			hm.Files = append(hm.Files, f.Name)
		}
		out = append(out, hm)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS > out[j].TS }) // newest first
	return out
}

func historyFromRows(in []HistoryMessageRow) []slack.HistoryMessage {
	out := make([]slack.HistoryMessage, 0, len(in))
	for _, r := range in {
		out = append(out, slack.HistoryMessage{TS: r.TS, User: r.User, BotID: r.BotID, Text: r.Text, ThreadTS: r.ThreadTS, Files: r.Files})
	}
	return out // the store returns newest first
}

func filterHistory(in []slack.HistoryMessage, grep string) []slack.HistoryMessage {
	if grep == "" {
		return in
	}
	g := strings.ToLower(grep)
	out := in[:0:0]
	for _, m := range in {
		if strings.Contains(strings.ToLower(m.Text), g) {
			out = append(out, m)
		}
	}
	return out
}

// capHistory keeps the newest messages under both caps; truncated reports a cut.
func capHistory(in []slack.HistoryMessage, limit, maxBytes int) ([]slack.HistoryMessage, bool) {
	var out []slack.HistoryMessage
	bytes := 0
	for _, m := range in {
		if len(out) >= limit || bytes+len(m.Text) > maxBytes {
			return out, true
		}
		out = append(out, m)
		bytes += len(m.Text)
	}
	return out, false
}
