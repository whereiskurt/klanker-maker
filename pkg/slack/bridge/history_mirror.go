// pkg/slack/bridge/history_mirror.go
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ChannelInfoFetcher resolves a channel id to its name (conversations.info).
// Needs channels:read / groups:read; a private channel without groups:read
// answers missing_scope, which the caller logs and treats as "no match".
type ChannelInfoFetcher interface {
	Info(ctx context.Context, channel string) (name string, isPrivate bool, err error)
}

// SlackChannelInfoAdapter implements ChannelInfoFetcher over the Slack Web API.
type SlackChannelInfoAdapter struct {
	HTTPClient *http.Client
	BaseURL    string
	Tokens     BotTokenFetcher
}

func (a *SlackChannelInfoAdapter) Info(ctx context.Context, channel string) (string, bool, error) {
	token, err := a.Tokens.Fetch(ctx)
	if err != nil {
		return "", false, fmt.Errorf("conversations.info: fetch bot token: %w", err)
	}
	base := a.BaseURL
	if base == "" {
		base = "https://slack.com/api"
	}
	form := url.Values{"channel": {channel}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/conversations.info", strings.NewReader(form.Encode()))
	if err != nil {
		return "", false, fmt.Errorf("conversations.info: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded") // legacy method: rejects JSON bodies
	hc := a.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("conversations.info: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error"`
		Channel struct {
			Name      string `json:"name"`
			IsPrivate bool   `json:"is_private"`
		} `json:"channel"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", false, fmt.Errorf("conversations.info: decode (http %d): %w", resp.StatusCode, err)
	}
	if !out.OK {
		return "", false, fmt.Errorf("conversations.info: %s", out.Error)
	}
	return out.Channel.Name, out.Channel.IsPrivate, nil
}

// BackfillJob is one page-sized unit of history backfill for a channel.
type BackfillJob struct {
	Channel   string `json:"channel"`
	Oldest    string `json:"oldest"`           // Slack ts; window start (exclusive)
	Latest    string `json:"latest,omitempty"` // cursor: fetch strictly older than this; "" = now
	Remaining int    `json:"remaining"`        // messages still allowed under max_messages
}

// BackfillEnqueuer queues a backfill job, optionally delayed.
type BackfillEnqueuer interface {
	Enqueue(ctx context.Context, job BackfillJob, delaySeconds int32) error
}

// historySystemSubtypes are channel chatter a human reader skims past; they
// are not stored. Everything else a member can read is.
var historySystemSubtypes = map[string]bool{"channel_join": true, "channel_leave": true, "channel_topic": true, "channel_purpose": true, "channel_name": true, "channel_archive": true, "channel_unarchive": true}

// defaultHistoryRetentionSeconds is what a route with no retention gets.
const defaultHistoryRetentionSeconds = 7 * 24 * 3600

// effectiveRetentionSeconds is the retention historyExpiry applies.
func effectiveRetentionSeconds(r *RouteHistory) int {
	if r != nil && r.RetentionSeconds > 0 {
		return r.RetentionSeconds
	}
	return defaultHistoryRetentionSeconds
}

func historyExpiry(now time.Time, r *RouteHistory) int64 {
	return now.Add(time.Duration(effectiveRetentionSeconds(r)) * time.Second).Unix()
}

// humanizeSeconds renders a window in the largest whole unit it divides into
// exactly (days, then hours), else minutes (rounded up, at least 1).
func humanizeSeconds(sec int) string {
	unit := func(n int, name string) string {
		if n == 1 {
			return fmt.Sprintf("1 %s", name)
		}
		return fmt.Sprintf("%d %ss", n, name)
	}
	switch {
	case sec >= 86400 && sec%86400 == 0:
		return unit(sec/86400, "day")
	case sec >= 3600 && sec%3600 == 0:
		return unit(sec/3600, "hour")
	}
	m := (sec + 59) / 60
	if m < 1 {
		m = 1
	}
	return unit(m, "minute")
}

// slackTS renders a time as a Slack ts ("1729700000.000000").
func slackTS(t time.Time) string { return fmt.Sprintf("%d.%06d", t.Unix(), t.Nanosecond()/1000) }

// routeForChannel is selectRoute's lookup: the static table, then a meta row
// written when the bot joined a glob-matched channel, then nil.
func (h *EventsHandler) routeForChannel(ctx context.Context, channel string) (*DefaultRoute, string) {
	if r, ok := h.Routes[channel]; ok && r != nil {
		return r, channel
	}
	if h.History == nil {
		return nil, ""
	}
	meta, err := h.History.GetMeta(ctx, channel)
	if err != nil {
		h.log().Warn("events: history meta read failed", "channel", channel, "err", err)
		return nil, ""
	}
	if meta == nil {
		return nil, ""
	}
	table := BuildRouteTable(&RoutesDocument{V: RoutesDocumentVersion, Routes: []RouteEntry{meta.Route}}, func(string, ...any) {})
	if r := table[channel]; r != nil {
		return r, channel
	}
	return nil, ""
}

// mirrorMessage stores what a member of a mirror: true channel can read. No
// sender filter: membership is the permission. Errors are logged, never fatal.
func (h *EventsHandler) mirrorMessage(ctx context.Context, msg slackMessageEvent) {
	if h.History == nil || msg.ChannelType == "im" || msg.ChannelType == "mpim" {
		return
	}
	route, _ := h.routeForChannel(ctx, msg.Channel)
	if route == nil || route.History == nil || !route.History.Mirror {
		return
	}
	var err error
	switch msg.Subtype {
	case "message_deleted":
		if msg.DeletedTS != "" {
			err = h.History.DeleteMessage(ctx, msg.Channel, msg.DeletedTS)
		}
	case "message_changed":
		if msg.Message != nil && msg.Message.TS != "" {
			err = h.History.UpdateText(ctx, msg.Channel, msg.Message.TS, msg.Message.Text, msg.TS)
		}
	default:
		if historySystemSubtypes[msg.Subtype] || msg.TS == "" {
			return
		}
		row := HistoryMessageRow{Channel: msg.Channel, TS: msg.TS, User: msg.User, BotID: msg.BotID, Text: msg.Text,
			ThreadTS: msg.ThreadTS, Subtype: msg.Subtype, ExpiresAt: historyExpiry(h.now(), route.History)}
		for _, f := range msg.Files {
			row.Files = append(row.Files, f.Name)
		}
		err = h.History.PutMessage(ctx, row)
	}
	if err != nil {
		h.log().Warn("events: history mirror write failed (dispatch unaffected)", "channel", msg.Channel, "ts", msg.TS, "subtype", msg.Subtype, "err", err)
	}
}

// onMemberJoined matches a channel the bot just joined against the glob
// patterns and, on a match, records the binding and queues the backfill.
func (h *EventsHandler) onMemberJoined(ctx context.Context, msg slackMessageEvent) {
	if h.History == nil || len(h.Patterns) == 0 || h.ChannelInfo == nil {
		return
	}
	uid, _ := h.BotUserID.Fetch(ctx)
	if uid == "" || msg.User != uid || msg.ChannelType == "im" || msg.ChannelType == "mpim" {
		return
	}
	if _, static := h.Routes[msg.Channel]; static {
		return // exact beats glob
	}
	name, _, err := h.ChannelInfo.Info(ctx, msg.Channel)
	if err != nil {
		h.log().Warn("events: history: channel name lookup failed; glob routes cannot match it", "channel", msg.Channel, "err", err)
		return
	}
	var match *PatternRoute
	overlap := ""
	for i := range h.Patterns {
		if !MatchChannelGlob(h.Patterns[i].Glob, name) {
			continue
		}
		if match == nil {
			match = &h.Patterns[i]
		} else if overlap == "" {
			overlap = h.Patterns[i].Glob
		}
	}
	if match == nil {
		return
	}
	existing, _ := h.History.GetMeta(ctx, msg.Channel)
	now := h.now()
	meta := ChannelMeta{Channel: msg.Channel, Name: name, Route: RouteFromPattern(*match, msg.Channel, name),
		MatchedAt: now.UTC().Format(time.RFC3339), BackfillState: "off", Overlap: overlap}
	if existing != nil {
		meta.Announced = existing.Announced
	}
	hist := match.Entry.History
	if hist != nil && hist.Mirror && hist.BackfillSeconds > 0 {
		meta.BackfillOldest = slackTS(now.Add(-time.Duration(hist.BackfillSeconds) * time.Second))
		meta.BackfillState = "pending"
	}
	if err := h.History.PutMeta(ctx, meta); err != nil {
		h.log().Error("events: history: meta write failed", "channel", msg.Channel, "err", err)
		return
	}
	if meta.BackfillState == "pending" && h.Backfill != nil {
		maxMsgs := hist.MaxMessages
		if maxMsgs <= 0 {
			maxMsgs = 200
		}
		if err := h.Backfill.Enqueue(ctx, BackfillJob{Channel: msg.Channel, Oldest: meta.BackfillOldest, Remaining: maxMsgs}, 0); err != nil {
			h.log().Error("events: history: backfill enqueue failed", "channel", msg.Channel, "err", err)
		}
	}
	if hist != nil && hist.Announce && !hist.Mirror {
		h.log().Info("events: history: announce ignored on a query-only route", "channel", msg.Channel)
	} else if hist != nil && hist.Announce && !meta.Announced && h.Slack != nil {
		text := "I'm keeping this channel's recent history for context"
		if hist.BackfillSeconds > 0 {
			text += fmt.Sprintf(" (back %s from when I joined)", humanizeSeconds(hist.BackfillSeconds))
		}
		text += fmt.Sprintf("; messages expire from my copy after %s.", humanizeSeconds(effectiveRetentionSeconds(hist)))
		if _, perr := h.Slack.PostMessage(ctx, msg.Channel, "", text, ""); perr == nil {
			meta.Announced = true
			_ = h.History.PutMeta(ctx, meta)
		} else {
			h.log().Warn("events: history: announce failed", "channel", msg.Channel, "err", perr)
		}
	}
	h.log().Info("events: history: channel matched", "channel", msg.Channel, "name", name, "glob", match.Glob, "alias", match.Entry.Alias, "backfill", meta.BackfillState, "overlap", overlap)
}

// onChannelLeft drops the channel's mirror when the bot leaves or it is deleted.
func (h *EventsHandler) onChannelLeft(ctx context.Context, channel string) {
	if h.History == nil {
		return
	}
	n, err := h.History.PurgeChannel(ctx, channel)
	if err != nil {
		h.log().Warn("events: history: purge failed", "channel", channel, "err", err)
		return
	}
	h.log().Info("events: history: channel purged", "channel", channel, "rows", n)
}
