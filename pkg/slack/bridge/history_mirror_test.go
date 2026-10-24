// pkg/slack/bridge/history_mirror_test.go
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// rawEventReq builds a signed event_callback around an arbitrary inner event.
func rawEventReq(t *testing.T, now time.Time, ev map[string]any, eventID string) EventsRequest {
	t.Helper()
	evb, _ := json.Marshal(ev)
	body := fmt.Sprintf(`{"type":"event_callback","event_id":%q,"event":%s}`, eventID, evb)
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	return EventsRequest{Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr}, Body: body}
}

func histRoute(alias string, h *RouteHistory) *DefaultRoute {
	r := NewDefaultRoute(alias, alias, []string{"*@example.com"}, nil)
	r.History = h
	return r
}

// mirrorHandler: channelRoutedHandler plus a store, with C1 mirrored and C2 query-only.
func mirrorHandler(t *testing.T, now time.Time) (*EventsHandler, *fakeHistoryStore, *fakeSQS) {
	t.Helper()
	h, sqs, _, _, _, _ := channelRoutedHandler(t, now)
	st := newFakeHistoryStore()
	h.History = st
	h.Routes["C1"] = histRoute("ir-bot", &RouteHistory{Mirror: true, Query: true, RetentionSeconds: 3600})
	h.Routes["C2"] = histRoute("ir-bot", &RouteHistory{Query: true})
	return h, st, sqs
}

func TestMirror_StoresEveryMessageInAMirroredChannel(t *testing.T) {
	now := time.Now()
	h, st, sqs := mirrorHandler(t, now)
	ctx := context.Background()
	h.Handle(ctx, rawEventReq(t, now, map[string]any{"type": "message", "channel": "C1", "channel_type": "channel", "user": "U1", "text": "hi", "ts": "1.1"}, "M1"))
	h.Handle(ctx, rawEventReq(t, now, map[string]any{"type": "message", "subtype": "bot_message", "channel": "C1", "channel_type": "channel", "bot_id": "B2", "text": "alert", "ts": "1.2"}, "M2"))
	h.Handle(ctx, rawEventReq(t, now, map[string]any{"type": "message", "channel": "C1", "channel_type": "channel", "user": "UBOT123", "bot_id": "BBOT", "text": "reply", "ts": "1.3"}, "M3"))
	h.Handle(ctx, rawEventReq(t, now, map[string]any{"type": "message", "subtype": "file_share", "channel": "C1", "channel_type": "channel", "user": "U1", "text": "see", "ts": "1.4",
		"files": []map[string]any{{"id": "F1", "name": "report.pdf"}}}, "M4"))
	if len(st.rows) != 4 {
		t.Fatalf("want 4 rows, got %d: %+v", len(st.rows), st.rows)
	}
	if r := st.rows["C1|1.2"]; r.BotID != "B2" || r.Text != "alert" {
		t.Fatalf("app row: %+v", r)
	}
	if r := st.rows["C1|1.3"]; r.User != "UBOT123" || r.BotID != "BBOT" || r.Text != "reply" {
		t.Fatalf("bot's own row must be kept: %+v", r)
	}
	if r := st.rows["C1|1.1"]; r.ExpiresAt != now.Add(time.Hour).Unix() || r.User != "U1" {
		t.Fatalf("human row: %+v want expiry %d", r, now.Add(time.Hour).Unix())
	}
	if r := st.rows["C1|1.4"]; len(r.Files) != 1 || r.Files[0] != "report.pdf" {
		t.Fatalf("files must be names only: %+v", r.Files)
	}
	// The mirror must not short-circuit dispatch: a mentioned message still enqueues.
	h.Handle(ctx, rawEventReq(t, now, map[string]any{"type": "message", "channel": "C1", "channel_type": "channel", "user": "U0OTHER", "text": "<@UBOT123> go", "ts": "1.5"}, "M5"))
	if len(sqs.sends) != 1 {
		t.Fatalf("mentioned message must still dispatch: %d sends", len(sqs.sends))
	}
	if _, ok := st.rows["C1|1.5"]; !ok {
		t.Fatal("the mentioned message must be mirrored too")
	}
}

func TestMirror_SkipsChannelsWithoutMirror(t *testing.T) {
	now := time.Now()
	h, st, _ := mirrorHandler(t, now)
	ctx := context.Background()
	h.Handle(ctx, rawEventReq(t, now, map[string]any{"type": "message", "channel": "C2", "channel_type": "channel", "user": "U1", "text": "x", "ts": "2.1"}, "S1"))
	h.Handle(ctx, rawEventReq(t, now, map[string]any{"type": "message", "channel": "C9", "channel_type": "channel", "user": "U1", "text": "x", "ts": "2.2"}, "S2"))
	if len(st.rows) != 0 {
		t.Fatalf("no mirror ⇒ no rows: %+v", st.rows)
	}
}

func TestMirror_EditAndDeleteAreApplied(t *testing.T) {
	now := time.Now()
	h, st, _ := mirrorHandler(t, now)
	ctx := context.Background()
	h.Handle(ctx, rawEventReq(t, now, map[string]any{"type": "message", "subtype": "message_changed", "channel": "C1", "channel_type": "channel", "ts": "2.0",
		"message": map[string]any{"ts": "1.5", "text": "fixed", "user": "U1"}}, "E1"))
	if len(st.updates) != 1 || st.updates[0] != "C1|1.5|fixed|2.0" {
		t.Fatalf("updates: %v", st.updates)
	}
	h.Handle(ctx, rawEventReq(t, now, map[string]any{"type": "message", "subtype": "message_deleted", "channel": "C1", "channel_type": "channel", "ts": "3.0", "deleted_ts": "1.5"}, "E2"))
	if len(st.deletes) != 1 || st.deletes[0] != "C1|1.5" {
		t.Fatalf("deletes: %v", st.deletes)
	}
	if len(st.rows) != 0 {
		t.Fatalf("edit/delete must not store rows: %+v", st.rows)
	}
}

func TestMirror_SystemSubtypesNotStored(t *testing.T) {
	now := time.Now()
	h, st, _ := mirrorHandler(t, now)
	for i, sub := range []string{"channel_join", "channel_leave", "channel_topic", "channel_purpose"} {
		h.Handle(context.Background(), rawEventReq(t, now, map[string]any{"type": "message", "subtype": sub, "channel": "C1", "channel_type": "channel", "user": "U1", "text": "x", "ts": fmt.Sprintf("4.%d", i)}, fmt.Sprintf("Y%d", i)))
	}
	if len(st.rows) != 0 {
		t.Fatalf("system subtypes must not be stored: %+v", st.rows)
	}
}

func TestMirror_WriteErrorNeverBlocksDispatch(t *testing.T) {
	now := time.Now()
	h, st, sqs := mirrorHandler(t, now)
	st.putErr = errors.New("ddb down")
	resp := h.Handle(context.Background(), rawEventReq(t, now, map[string]any{"type": "message", "channel": "C1", "channel_type": "channel", "user": "U0OTHER", "text": "<@UBOT123> go", "ts": "5.1"}, "W1"))
	if resp.StatusCode != 200 || len(sqs.sends) != 1 {
		t.Fatalf("store error must not block dispatch: %+v sends=%d", resp, len(sqs.sends))
	}
}

// joinHandler: a handler with a store, channel info and backfill, and one inc_* pattern.
func joinHandler(t *testing.T, now time.Time, hist *RouteHistory) (*EventsHandler, *fakeHistoryStore, *fakeBackfill, *fakeSlackPoster) {
	t.Helper()
	h, _, _, _, _, _ := channelRoutedHandler(t, now)
	st := newFakeHistoryStore()
	bf := &fakeBackfill{}
	poster := &fakeSlackPoster{}
	h.History, h.Backfill, h.Slack = st, bf, poster
	h.ChannelInfo = &fakeChannelInfo{name: map[string]string{"C7": "inc_7", "C8": "inc_2026_x"}}
	h.Patterns = []PatternRoute{patternRoute("inc_*", "ir", hist)}
	return h, st, bf, poster
}

func patternRoute(glob, alias string, hist *RouteHistory) PatternRoute {
	e := PatternEntry{Glob: glob, Alias: alias, Profile: alias, Allow: []string{"*@example.com"}, MentionOnly: true, History: hist}
	r := NewDefaultRoute(alias, alias, e.Allow, nil)
	r.MentionOnly, r.History, r.Pattern = true, hist, glob
	return PatternRoute{Glob: glob, Entry: e, Route: r}
}

func joinEvent(t *testing.T, now time.Time, user, channel, channelType, id string) EventsRequest {
	ev := map[string]any{"type": "member_joined_channel", "user": user, "channel": channel}
	if channelType != "" {
		ev["channel_type"] = channelType
	}
	return rawEventReq(t, now, ev, id)
}

var week = &RouteHistory{Mirror: true, Query: true, BackfillSeconds: 7 * 86400, RetentionSeconds: 7 * 86400, MaxMessages: 200}

func TestJoin_BotJoinMatchingGlobWritesMetaAndEnqueuesBackfill(t *testing.T) {
	now := time.Now()
	h, st, bf, poster := joinHandler(t, now, week)
	resp := h.Handle(context.Background(), joinEvent(t, now, "UBOT123", "C7", "channel", "J1"))
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	m, ok := st.meta["C7"]
	if !ok || m.Name != "inc_7" || m.Route.Pattern != "inc_*" || m.Route.Alias != "ir" || m.BackfillState != "pending" || m.Channel != "C7" {
		t.Fatalf("meta: %+v", m)
	}
	wantOldest := slackTS(now.Add(-7 * 24 * time.Hour))
	if len(bf.jobs) != 1 || bf.jobs[0] != (BackfillJob{Channel: "C7", Oldest: wantOldest, Remaining: 200}) || bf.delays[0] != 0 {
		t.Fatalf("jobs: %+v delays %v want oldest %s", bf.jobs, bf.delays, wantOldest)
	}
	if len(poster.msgs) != 0 {
		t.Fatalf("announce false ⇒ no post: %+v", poster.msgs)
	}
}

func TestJoin_AnnounceTruePostsOnce(t *testing.T) {
	now := time.Now()
	hist := *week
	hist.Announce = true
	h, st, _, poster := joinHandler(t, now, &hist)
	h.Handle(context.Background(), joinEvent(t, now, "UBOT123", "C7", "channel", "A1"))
	if len(poster.msgs) != 1 || poster.msgs[0].channel != "C7" || !strings.Contains(poster.msgs[0].body, "7 days") {
		t.Fatalf("announce: %+v", poster.msgs)
	}
	if !st.meta["C7"].Announced {
		t.Fatal("meta.Announced must be recorded")
	}
	h.Handle(context.Background(), joinEvent(t, now, "UBOT123", "C7", "channel", "A2"))
	if len(poster.msgs) != 1 {
		t.Fatalf("second join must not re-announce: %+v", poster.msgs)
	}
}

func TestJoin_QueryOnlyPatternWritesMetaNoBackfill(t *testing.T) {
	now := time.Now()
	h, st, bf, _ := joinHandler(t, now, &RouteHistory{Query: true})
	h.Handle(context.Background(), joinEvent(t, now, "UBOT123", "C7", "channel", "Q1"))
	if m, ok := st.meta["C7"]; !ok || m.BackfillState != "off" {
		t.Fatalf("meta: %+v", st.meta)
	}
	if len(bf.jobs) != 0 {
		t.Fatalf("no backfill expected: %+v", bf.jobs)
	}
}

func TestJoin_FirstGlobWinsAndOverlapRecorded(t *testing.T) {
	now := time.Now()
	h, st, _, _ := joinHandler(t, now, week)
	h.Patterns = []PatternRoute{patternRoute("inc_*", "ir", week), patternRoute("inc_2026_*", "ir2", week)}
	h.Handle(context.Background(), joinEvent(t, now, "UBOT123", "C8", "channel", "F1"))
	m := st.meta["C8"]
	if m.Route.Alias != "ir" || m.Overlap != "inc_2026_*" {
		t.Fatalf("meta: %+v", m)
	}
}

func TestJoin_ExactRouteBeatsGlob(t *testing.T) {
	now := time.Now()
	h, st, bf, _ := joinHandler(t, now, week)
	h.Routes["C7"] = histRoute("ir-bot", nil)
	h.Handle(context.Background(), joinEvent(t, now, "UBOT123", "C7", "channel", "X1"))
	if len(st.meta) != 0 || len(bf.jobs) != 0 {
		t.Fatalf("exact route must win: meta=%+v jobs=%+v", st.meta, bf.jobs)
	}
}

func TestJoin_IgnoresOtherUsersAndDMs(t *testing.T) {
	now := time.Now()
	h, st, bf, _ := joinHandler(t, now, week)
	h.Handle(context.Background(), joinEvent(t, now, "U1", "C7", "channel", "O1"))
	h.Handle(context.Background(), joinEvent(t, now, "UBOT123", "C7", "im", "O2"))
	if len(st.meta) != 0 || len(bf.jobs) != 0 {
		t.Fatalf("meta=%+v jobs=%+v", st.meta, bf.jobs)
	}
}

func TestJoin_ChannelInfoErrorIsLoggedNotFatal(t *testing.T) {
	now := time.Now()
	h, st, bf, _ := joinHandler(t, now, week)
	h.ChannelInfo = &fakeChannelInfo{err: errors.New("conversations.info: missing_scope")}
	resp := h.Handle(context.Background(), joinEvent(t, now, "UBOT123", "C7", "channel", "I1"))
	if resp.StatusCode != 200 || len(st.meta) != 0 || len(bf.jobs) != 0 {
		t.Fatalf("resp=%+v meta=%+v jobs=%+v", resp, st.meta, bf.jobs)
	}
}

func TestRouteForChannel_MetaRowRoutesLikeATableEntry(t *testing.T) {
	now := time.Now()
	h, sqs, _, alias, _, _ := channelRoutedHandler(t, now)
	st := newFakeHistoryStore()
	h.History = st
	alias.infos["ir"] = SandboxRoutingInfo{SandboxID: "sb-ir", QueueURL: "https://sqs.example/ir.fifo"}
	pr := patternRoute("inc_*", "ir", week)
	st.meta["C7"] = ChannelMeta{Channel: "C7", Name: "inc_7", Route: RouteFromPattern(pr, "C7", "inc_7")}

	r, name := h.selectRoute(context.Background(), slackMessageEvent{Channel: "C7", ChannelType: "channel"})
	if r == nil || r.Alias != "ir" || r.History == nil || !r.History.Mirror || name != "C7" {
		t.Fatalf("selectRoute: %+v %q", r, name)
	}
	h.Handle(context.Background(), rawEventReq(t, now, map[string]any{"type": "message", "channel": "C7", "channel_type": "channel", "user": "U0OTHER", "text": "<@UBOT123> go", "ts": "7.1"}, "R1"))
	if len(sqs.sends) != 1 || sqs.sends[0].url != "https://sqs.example/ir.fifo" {
		t.Fatalf("must dispatch to alias ir: %+v", sqs.sends)
	}
}

func TestLeave_PurgesChannel(t *testing.T) {
	now := time.Now()
	h, st, _, _ := joinHandler(t, now, week)
	for i, typ := range []string{"channel_left", "group_left", "channel_deleted"} {
		h.Handle(context.Background(), rawEventReq(t, now, map[string]any{"type": typ, "channel": "C7"}, fmt.Sprintf("L%d", i)))
	}
	if len(st.purges) != 3 || st.purges[0] != "C7" || st.purges[1] != "C7" || st.purges[2] != "C7" {
		t.Fatalf("purges: %v", st.purges)
	}
}

func TestJoin_AnnounceTextHumanizesWindows(t *testing.T) {
	now := time.Now()
	h, _, _, poster := joinHandler(t, now, &RouteHistory{Mirror: true, Announce: true, BackfillSeconds: 90 * 60, RetentionSeconds: 7 * 86400})
	h.Handle(context.Background(), joinEvent(t, now, "UBOT123", "C7", "channel", "H1"))
	if len(poster.msgs) != 1 || !strings.Contains(poster.msgs[0].body, "90 minutes") || !strings.Contains(poster.msgs[0].body, "7 days") || strings.Contains(poster.msgs[0].body, "0 days") {
		t.Fatalf("announce: %+v", poster.msgs)
	}
	// Retention unset: the text states the effective default (7 days), no backfill clause.
	h2, _, _, p2 := joinHandler(t, now, &RouteHistory{Mirror: true, Announce: true})
	h2.Handle(context.Background(), joinEvent(t, now, "UBOT123", "C7", "channel", "H2"))
	if len(p2.msgs) != 1 || !strings.Contains(p2.msgs[0].body, "7 days") || strings.Contains(p2.msgs[0].body, "back ") {
		t.Fatalf("default retention text: %+v", p2.msgs)
	}
}

func TestJoin_AnnounceIgnoredOnQueryOnlyRoute(t *testing.T) {
	now := time.Now()
	h, st, _, poster := joinHandler(t, now, &RouteHistory{Query: true, Announce: true})
	h.Handle(context.Background(), joinEvent(t, now, "UBOT123", "C7", "channel", "H3"))
	if len(poster.msgs) != 0 || st.meta["C7"].Announced {
		t.Fatalf("query-only route must not announce: %+v", poster.msgs)
	}
}
