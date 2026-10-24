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

// routeForChannel is the channel's route: the static table (named routes and
// the channels each glob matched at km init), then a meta row written when the
// bot joined a glob-matched channel, resolved through the CURRENT pattern table
// (resolveMetaRoute), then nil. A meta-row read error is returned, never folded
// into "no route": the caller must not fall back to the default route on a
// store blip (wrong alias, wrong continuity row).
func (h *EventsHandler) routeForChannel(ctx context.Context, channel string) (*DefaultRoute, string, error) {
	if r, ok := h.Routes[channel]; ok && r != nil {
		return r, channel, nil
	}
	if h.History == nil {
		return nil, "", nil
	}
	meta, err := h.History.GetMeta(ctx, channel)
	if err != nil {
		return nil, "", err
	}
	if meta != nil {
		h.bound.Store(channel, true) // a row exists: the mirror need not bind it
	}
	if r := resolveMetaRoute(meta, h.Patterns); r != nil {
		return r, channel, nil
	}
	return nil, "", nil
}

// resolveMetaRoute is a meta row's route as TODAY's config defines it: the
// route the row's glob (meta.Route.Pattern) produces from the current pattern
// table, or nil when that glob is no longer configured (or the row was bound by
// a named route, which the static table answers). meta.Route is bookkeeping for
// km doctor and purge; it never routes or authorises on its own, so removing or
// narrowing a glob takes effect at the next cold start.
func resolveMetaRoute(meta *ChannelMeta, patterns []PatternRoute) *DefaultRoute {
	if meta == nil || meta.Route.Pattern == "" {
		return nil
	}
	for _, p := range patterns {
		if p.Glob == meta.Route.Pattern {
			return compileRouteEntry(RouteFromPattern(p, meta.Channel, meta.Name), func(string, ...any) {})
		}
	}
	return nil
}

// mirrorMessage stores what a member of a mirror: true channel can read. No
// sender filter: membership is the permission. route is the channel's route as
// Handle resolved it (nil = not mirrored). Errors are logged, never fatal.
func (h *EventsHandler) mirrorMessage(ctx context.Context, msg slackMessageEvent, route *DefaultRoute) {
	if h.History == nil || msg.ChannelType == "im" || msg.ChannelType == "mpim" {
		return
	}
	if route == nil || route.History == nil || !route.History.Mirror {
		return
	}
	h.bindOnFirstSight(ctx, msg.Channel, route)
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

// onMemberJoined binds a channel the bot just joined: a static route with
// mirror: true binds as it is (the rejoin case: leaving purged the old row),
// otherwise the glob patterns are matched in config order and the first match
// binds. Handle has already deduplicated the event, so a Slack retry never
// re-binds.
func (h *EventsHandler) onMemberJoined(ctx context.Context, msg slackMessageEvent) {
	if h.History == nil {
		return
	}
	uid, _ := h.BotUserID.Fetch(ctx)
	if uid == "" || msg.User != uid || msg.ChannelType == "im" || msg.ChannelType == "mpim" {
		return
	}
	if r, static := h.Routes[msg.Channel]; static {
		// Exact beats glob. Only a mirrored route has anything to bind.
		if r != nil && r.History != nil && r.History.Mirror {
			existing, err := h.History.GetMeta(ctx, msg.Channel)
			if err != nil {
				h.log().Warn("events: history: meta read failed; channel not bound", "channel", msg.Channel, "err", err)
				return
			}
			h.bindChannel(ctx, msg.Channel, h.channelName(ctx, msg.Channel), routeEntryOf(msg.Channel, r), "", existing)
		}
		return
	}
	if len(h.Patterns) == 0 || h.ChannelInfo == nil {
		return
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
	existing, err := h.History.GetMeta(ctx, msg.Channel)
	if err != nil {
		h.log().Warn("events: history: meta read failed; channel not bound", "channel", msg.Channel, "err", err)
		return
	}
	h.bindChannel(ctx, msg.Channel, name, RouteFromPattern(*match, msg.Channel, name), overlap, existing)
}

// bindOnFirstSight binds a mirrored channel that has no meta row: a static
// route with mirror: true (named, or a glob match km init recorded) never gets
// a join event while the bot is already a member, so the first message seen
// binds it instead, with the same announcement and backfill as a join. The
// per-warm-Lambda set keeps the extra GetMeta to once per channel per cold
// start; a channel whose row Handle already read is marked by routeForChannel.
func (h *EventsHandler) bindOnFirstSight(ctx context.Context, channel string, route *DefaultRoute) {
	if _, done := h.bound.Load(channel); done {
		return
	}
	existing, err := h.History.GetMeta(ctx, channel)
	if err != nil {
		h.log().Warn("events: history: meta read failed; will retry binding on the next message", "channel", channel, "err", err)
		return
	}
	if existing != nil {
		h.bound.Store(channel, true)
		return
	}
	h.bindChannel(ctx, channel, h.channelName(ctx, channel), routeEntryOf(channel, route), "", nil)
}

// bindChannel records a channel's meta row and starts its backfill. The order
// is fixed so no two writers race on the row: decide and post the
// announcement FIRST (Announced set in memory on success), then ONE PutMeta
// carrying it, then the single Enqueue; BackfillStep's later full-row writes
// follow the enqueue. existing is the current row (nil when none): only its
// Announced flag survives a rebind, so a rejoin never re-announces.
func (h *EventsHandler) bindChannel(ctx context.Context, channel, name string, entry RouteEntry, overlap string, existing *ChannelMeta) {
	now := h.now()
	hist := entry.History
	meta := ChannelMeta{Channel: channel, Name: name, Route: entry,
		MatchedAt: now.UTC().Format(time.RFC3339), BackfillState: "off", Overlap: overlap}
	if existing != nil {
		meta.Announced = existing.Announced
	}
	if _, posted := h.announced.Load(channel); posted {
		meta.Announced = true // posted earlier from this container; only the write is retried
	}
	if hist != nil && hist.Mirror && hist.BackfillSeconds > 0 {
		meta.BackfillOldest = slackTS(now.Add(-time.Duration(hist.BackfillSeconds) * time.Second))
		meta.BackfillState = "pending"
	}
	h.bound.Store(channel, true) // before the announcement: its own message event must not bind again
	if hist != nil && hist.Announce && !hist.Mirror {
		h.log().Info("events: history: announce ignored on a query-only route", "channel", channel)
	} else if hist != nil && hist.Announce && !meta.Announced && h.Slack != nil {
		text := "I'm keeping this channel's recent history for context"
		if hist.BackfillSeconds > 0 {
			text += fmt.Sprintf(" (back %s from when I joined)", humanizeSeconds(hist.BackfillSeconds))
		}
		text += fmt.Sprintf("; messages expire from my copy after %s.", humanizeSeconds(effectiveRetentionSeconds(hist)))
		if _, perr := h.Slack.PostMessage(ctx, channel, "", text, ""); perr == nil {
			meta.Announced = true
			h.announced.Store(channel, true)
		} else {
			h.log().Warn("events: history: announce failed", "channel", channel, "err", perr)
		}
	}
	if err := h.History.PutMeta(ctx, meta); err != nil {
		h.bound.Delete(channel) // let the next event retry the bind
		h.log().Error("events: history: meta write failed", "channel", channel, "err", err)
		return
	}
	if meta.BackfillState == "pending" && h.Backfill != nil {
		maxMsgs := hist.MaxMessages
		if maxMsgs <= 0 {
			maxMsgs = 200
		}
		if err := h.Backfill.Enqueue(ctx, BackfillJob{Channel: channel, Oldest: meta.BackfillOldest, Remaining: maxMsgs}, 0); err != nil {
			h.log().Error("events: history: backfill enqueue failed", "channel", channel, "err", err)
		}
	}
	h.log().Info("events: history: channel bound", "channel", channel, "name", name, "glob", entry.Pattern, "alias", entry.Alias, "backfill", meta.BackfillState, "overlap", overlap, "announced", meta.Announced)
}

// routeEntryOf is the bookkeeping RouteEntry a meta row records for a static
// route (km doctor and purge read its alias and pattern; nothing routes by it).
func routeEntryOf(channel string, r *DefaultRoute) RouteEntry {
	return RouteEntry{Channel: channel, Pattern: r.Pattern, Alias: r.Alias, Profile: r.Profile, MentionOnly: r.MentionOnly, History: r.History}
}

// channelName is the channel's name for a meta row, "" when it cannot be had.
func (h *EventsHandler) channelName(ctx context.Context, channel string) string {
	if h.ChannelInfo == nil {
		return ""
	}
	name, _, err := h.ChannelInfo.Info(ctx, channel)
	if err != nil {
		h.log().Warn("events: history: channel name lookup failed; meta row has no name", "channel", channel, "err", err)
		return ""
	}
	return name
}

// onChannelLeft drops the channel's mirror when the bot leaves or it is deleted.
func (h *EventsHandler) onChannelLeft(ctx context.Context, channel string) {
	if h.History == nil {
		return
	}
	h.bound.Delete(channel) // a rejoin binds afresh
	h.announced.Delete(channel)
	n, err := h.History.PurgeChannel(ctx, channel)
	if err != nil {
		h.log().Warn("events: history: purge failed", "channel", channel, "err", err)
		return
	}
	h.log().Info("events: history: channel purged", "channel", channel, "rows", n)
}
