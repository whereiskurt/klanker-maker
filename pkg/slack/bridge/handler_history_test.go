// Package bridge — handler_history_test.go
// Tests for the signed `history` action. Internal package so the shared history
// fakes are reachable; handleHistory is called directly (signing is covered by
// the generic Handle tests).
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

type histChannels struct{ owned map[string]string }

func (c *histChannels) OwnedChannel(_ context.Context, id string) (string, error) {
	return c.owned[id], nil
}

type fakeAliasResolver struct {
	byAlias map[string]SandboxRoutingInfo
}

func (f *fakeAliasResolver) ResolveAlias(_ context.Context, alias string) (SandboxRoutingInfo, bool, error) {
	info, ok := f.byAlias[alias]
	return info, ok, nil
}

type fakeThreadReader struct {
	msgs  []SlackReplyMessage
	calls int
}

func (f *fakeThreadReader) Replies(_ context.Context, _, _, _ string) ([]SlackReplyMessage, error) {
	f.calls++
	return f.msgs, nil
}

// histThreads embeds the store interface (nil) and answers only LookupSandbox:
// any other call would panic, proving history never consults anything else.
type histThreads struct {
	SlackThreadStore
	sb string
}

func (h *histThreads) LookupSandbox(context.Context, string, string) (string, error) {
	return h.sb, nil
}

type histLimits struct{ json string }

func (l *histLimits) FetchLimits(context.Context, string) (string, error) { return l.json, nil }

type histFixture struct {
	h      *Handler
	store  *fakeHistoryStore
	reader *fakeHistoryReader
	thread *fakeThreadReader
}

func newHistFixture() *histFixture {
	f := &histFixture{store: newFakeHistoryStore(), reader: &fakeHistoryReader{}, thread: &fakeThreadReader{}}
	f.h = &Handler{
		Channels:      &histChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Routes:        map[string]*DefaultRoute{},
		AliasResolver: &fakeAliasResolver{byAlias: map[string]SandboxRoutingInfo{"ir": {SandboxID: "sb-abc123"}}},
		History:       f.store,
		HistoryReader: f.reader,
		ThreadReader:  f.thread,
	}
	return f
}

func (f *histFixture) route(channel, alias string, hist *RouteHistory) {
	f.h.Routes[channel] = &DefaultRoute{Alias: alias, History: hist}
}

func historyEnv(sender, channel string) slack.SlackEnvelope {
	return slack.SlackEnvelope{
		Action: slack.ActionHistory, SenderID: sender, Channel: channel,
		Nonce: "aabbccddeeff00112233445566778899", Timestamp: 1714280400, Version: slack.EnvelopeVersion,
	}
}

func replyMsgs(n int, textLen int) []SlackReplyMessage {
	var out []SlackReplyMessage
	for i := 0; i < n; i++ { // newest first, as conversations.history returns
		out = append(out, SlackReplyMessage{User: "U1", TS: fmt.Sprintf("1700000%03d.000100", n-i), Text: strings.Repeat("x", textLen)})
	}
	return out
}

func decodeHistory(t *testing.T, r *Response) slack.PostResponse {
	t.Helper()
	var pr slack.PostResponse
	if err := json.Unmarshal([]byte(r.Body), &pr); err != nil {
		t.Fatalf("decode %q: %v", r.Body, err)
	}
	return pr
}

func (f *histFixture) noReads(t *testing.T) {
	t.Helper()
	if len(f.reader.calls) != 0 || f.thread.calls != 0 || f.store.queryCalls != 0 {
		t.Errorf("expected zero reads, got reader=%d thread=%d store=%d", len(f.reader.calls), f.thread.calls, f.store.queryCalls)
	}
}

func TestHandleHistory_OwnChannelReadsLive(t *testing.T) {
	f := newHistFixture()
	f.reader.msgs = replyMsgs(3, 5)
	resp := f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "C0123ABC"))
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, resp.Body)
	}
	pr := decodeHistory(t, resp)
	if pr.Source != "live" || len(pr.Messages) != 3 {
		t.Fatalf("source=%q msgs=%d", pr.Source, len(pr.Messages))
	}
	if pr.Messages[0].TS < pr.Messages[2].TS {
		t.Errorf("want newest first, got %v", pr.Messages)
	}
	if f.store.queryCalls != 0 {
		t.Errorf("own channel must not touch the store")
	}
}

func TestHandleHistory_RoutedChannelWithQueryServedFromMirror(t *testing.T) {
	f := newHistFixture()
	f.route("CINC", "ir", &RouteHistory{Query: true, Mirror: true, MaxMessages: 50})
	for i := 5; i >= 1; i-- {
		f.store.queryRows = append(f.store.queryRows, HistoryMessageRow{Channel: "CINC", TS: fmt.Sprintf("1700000%03d.000100", i), Text: "m"})
	}
	resp := f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "CINC"))
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, resp.Body)
	}
	pr := decodeHistory(t, resp)
	if pr.Source != "mirror" || len(pr.Messages) != 5 {
		t.Fatalf("source=%q msgs=%d", pr.Source, len(pr.Messages))
	}
	if pr.Messages[0].TS < pr.Messages[4].TS {
		t.Errorf("want newest first")
	}
	if len(f.reader.calls) != 0 {
		t.Errorf("reader must not be called for a mirrored route")
	}
}

func TestHandleHistory_RoutedChannelQueryOnlyReadsLive(t *testing.T) {
	f := newHistFixture()
	f.route("CINC", "ir", &RouteHistory{Query: true, MaxMessages: 50})
	f.reader.msgs = replyMsgs(2, 5)
	e := historyEnv("sb-abc123", "CINC")
	e.Limit = 15
	resp := f.h.handleHistory(context.Background(), e)
	pr := decodeHistory(t, resp)
	if resp.StatusCode != 200 || pr.Source != "live" {
		t.Fatalf("status %d source %q", resp.StatusCode, pr.Source)
	}
	if len(f.reader.calls) != 1 || f.reader.calls[0].Limit > 15 {
		t.Errorf("reader calls %+v", f.reader.calls)
	}
	if f.store.queryCalls != 0 {
		t.Errorf("no mirror: store must not be queried")
	}
}

func TestHandleHistory_MetaRowRouteCounts(t *testing.T) {
	f := newHistFixture()
	f.h.Patterns = []PatternRoute{patternRoute("inc_*", "ir", &RouteHistory{Query: true})}
	f.store.meta["CINC"] = ChannelMeta{Channel: "CINC", Name: "inc_1", Route: RouteEntry{
		Channel: "CINC", Pattern: "inc_*", Alias: "ir", Profile: "p", Allow: []string{"a@example.com"},
		History: &RouteHistory{Query: true},
	}}
	f.reader.msgs = replyMsgs(1, 5)
	resp := f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "CINC"))
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, resp.Body)
	}
}

// A meta row never authorises on its own snapshot: the glob it was bound by
// must still be configured, and today's version of it decides.
func TestHandleHistory_MetaRowResolvesThroughCurrentPatterns(t *testing.T) {
	snapshot := RouteEntry{Channel: "CINC", Pattern: "inc_*", Alias: "ir", Profile: "p", Allow: []string{"a@example.com"}, History: &RouteHistory{Query: true}}
	t.Run("glob removed", func(t *testing.T) {
		f := newHistFixture()
		f.store.meta["CINC"] = ChannelMeta{Channel: "CINC", Name: "inc_1", Route: snapshot}
		resp := f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "CINC"))
		if resp.StatusCode != 403 || !strings.Contains(resp.Body, "history_not_allowed") {
			t.Fatalf("a removed glob must revoke history: %d %s", resp.StatusCode, resp.Body)
		}
		f.noReads(t)
	})
	t.Run("glob lost query", func(t *testing.T) {
		f := newHistFixture()
		f.h.Patterns = []PatternRoute{patternRoute("inc_*", "ir", nil)}
		f.store.meta["CINC"] = ChannelMeta{Channel: "CINC", Name: "inc_1", Route: snapshot}
		resp := f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "CINC"))
		if resp.StatusCode != 403 {
			t.Fatalf("the current glob has no history block: %d %s", resp.StatusCode, resp.Body)
		}
	})
	t.Run("glob moved to another alias", func(t *testing.T) {
		f := newHistFixture()
		f.h.Patterns = []PatternRoute{patternRoute("inc_*", "other", &RouteHistory{Query: true})}
		f.h.AliasResolver = &fakeAliasResolver{byAlias: map[string]SandboxRoutingInfo{"ir": {SandboxID: "sb-abc123"}, "other": {SandboxID: "sb-other"}}}
		f.store.meta["CINC"] = ChannelMeta{Channel: "CINC", Name: "inc_1", Route: snapshot}
		resp := f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "CINC"))
		if resp.StatusCode != 403 {
			t.Fatalf("the snapshot's alias must not authorise: %d %s", resp.StatusCode, resp.Body)
		}
	})
	t.Run("meta read error fails closed", func(t *testing.T) {
		f := newHistFixture()
		f.h.Patterns = []PatternRoute{patternRoute("inc_*", "ir", &RouteHistory{Query: true})}
		f.store.getMetaErr = fmt.Errorf("ddb down")
		resp := f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "CINC"))
		if resp.StatusCode != 403 {
			t.Fatalf("got %d %s", resp.StatusCode, resp.Body)
		}
	})
}

func TestHandleHistory_Refusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *histFixture)
	}{
		{"foreign channel, no route", func(f *histFixture) {}},
		{"routed to a different alias", func(f *histFixture) {
			f.route("CINC", "other", &RouteHistory{Query: true})
			f.h.AliasResolver = &fakeAliasResolver{byAlias: map[string]SandboxRoutingInfo{"other": {SandboxID: "sb-other"}}}
		}},
		{"alias not found", func(f *histFixture) {
			f.route("CINC", "ghost", &RouteHistory{Query: true})
		}},
		{"alias resolver error", func(f *histFixture) {
			f.route("CINC", "ir", &RouteHistory{Query: true})
			f.h.AliasResolver = &errAliasResolver{}
		}},
		{"route without history block", func(f *histFixture) { f.route("CINC", "ir", nil) }},
		{"history without query", func(f *histFixture) { f.route("CINC", "ir", &RouteHistory{Mirror: true, MaxMessages: 10}) }},
		{"thread row names sender but no route", func(f *histFixture) {
			f.h.Threads = &histThreads{sb: "sb-abc123"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newHistFixture()
			tc.setup(f)
			e := historyEnv("sb-abc123", "CINC")
			e.MessageTS = "1700000001.000100"
			resp := f.h.handleHistory(context.Background(), e)
			if resp.StatusCode != 403 || !strings.Contains(resp.Body, "history_not_allowed") {
				t.Fatalf("want 403 history_not_allowed, got %d %s", resp.StatusCode, resp.Body)
			}
			f.noReads(t)
		})
	}
}

type errAliasResolver struct{}

func (*errAliasResolver) ResolveAlias(context.Context, string) (SandboxRoutingInfo, bool, error) {
	return SandboxRoutingInfo{SandboxID: "sb-abc123"}, true, fmt.Errorf("ddb down")
}

func TestHandleHistory_NilAliasResolverIs503(t *testing.T) {
	f := newHistFixture()
	f.route("CINC", "ir", &RouteHistory{Query: true})
	f.h.AliasResolver = nil
	resp := f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "CINC"))
	if resp.StatusCode != 503 || !strings.Contains(resp.Body, "history_unavailable") {
		t.Fatalf("got %d %s", resp.StatusCode, resp.Body)
	}
	f.noReads(t)
}

type countingPoster struct{ posts int }

func (c *countingPoster) PostMessage(context.Context, string, string, string, string) (string, error) {
	c.posts++
	return "1.1", nil
}
func (c *countingPoster) ArchiveChannel(context.Context, string) error { return nil }
func (c *countingPoster) GetPermalink(context.Context, string, string) (string, error) {
	return "", nil
}
func (c *countingPoster) UpdateMessage(context.Context, string, string, string) (string, error) {
	return "", nil
}

func TestHandleHistory_QuotaTripNeverPostsNotice(t *testing.T) {
	for _, mode := range []string{"block", "warn"} {
		f := newHistFixture()
		f.route("CINC", "ir", &RouteHistory{Query: true})
		f.reader.msgs = replyMsgs(1, 5)
		poster := &countingPoster{}
		f.h.Slack = poster
		f.h.Quota = NewFakeQuotaClient(2)
		f.h.QuotaTable = "km-action-quota"
		f.h.Limits = &histLimits{json: `{"slack_history":{"perHour":1,"onBreach":"` + mode + `"}}`}
		resp := f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "CINC"))
		if mode == "block" && resp.StatusCode != 429 {
			t.Errorf("block: want 429, got %d", resp.StatusCode)
		}
		if poster.posts != 0 {
			t.Errorf("%s: a history read posted %d notice(s)", mode, poster.posts)
		}
	}
}

func TestHandleHistory_PagingCursorSurvivesEmptyPage(t *testing.T) {
	f := newHistFixture()
	f.reader.msgs = []SlackReplyMessage{{TS: "1700000003.000100", Text: "a"}, {TS: "1700000002.000100", Text: "b"}}
	f.reader.more = true
	e := historyEnv("sb-abc123", "C0123ABC")
	e.Grep = "zzz"
	pr := decodeHistory(t, f.h.handleHistory(context.Background(), e))
	if len(pr.Messages) != 0 || !pr.Truncated || pr.NextBefore != "1700000002.000100" {
		t.Fatalf("got %+v", pr)
	}
}

func TestHandleHistory_OversizeMessageDeliveredCut(t *testing.T) {
	f := newHistFixture()
	f.reader.msgs = []SlackReplyMessage{{TS: "1700000001.000100", Text: strings.Repeat("y", 40<<10)}}
	pr := decodeHistory(t, f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "C0123ABC")))
	if len(pr.Messages) != 1 || !strings.HasSuffix(pr.Messages[0].Text, "…[truncated]") || !pr.Truncated || pr.NextBefore != "1700000001.000100" {
		t.Fatalf("got %d msgs truncated=%v next=%q", len(pr.Messages), pr.Truncated, pr.NextBefore)
	}
	if len(pr.Messages[0].Text) > MaxContextTextBytes+len(historyTruncMarker) {
		t.Errorf("text not cut: %d", len(pr.Messages[0].Text))
	}
}

func TestHandleHistory_CapsAndPaging(t *testing.T) {
	t.Run("own channel cap 100", func(t *testing.T) {
		f := newHistFixture()
		f.reader.msgs = replyMsgs(120, 5)
		pr := decodeHistory(t, f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "C0123ABC")))
		if len(pr.Messages) != HistoryOwnChannelMax || !pr.Truncated {
			t.Fatalf("msgs=%d truncated=%v", len(pr.Messages), pr.Truncated)
		}
		if pr.NextBefore != pr.Messages[len(pr.Messages)-1].TS {
			t.Errorf("next_before %q, want oldest returned %q", pr.NextBefore, pr.Messages[len(pr.Messages)-1].TS)
		}
	})
	t.Run("route max and request limit", func(t *testing.T) {
		f := newHistFixture()
		f.route("CINC", "ir", &RouteHistory{Query: true, MaxMessages: 50})
		f.reader.msgs = replyMsgs(120, 5)
		e := historyEnv("sb-abc123", "CINC")
		e.Limit = 500
		if n := len(decodeHistory(t, f.h.handleHistory(context.Background(), e)).Messages); n != 50 {
			t.Errorf("limit 500 under max 50 gave %d", n)
		}
		e.Limit = 10
		if n := len(decodeHistory(t, f.h.handleHistory(context.Background(), e)).Messages); n != 10 {
			t.Errorf("limit 10 gave %d", n)
		}
	})
	t.Run("live page size", func(t *testing.T) {
		f := newHistFixture()
		f.h.HistoryPageSize = 15
		f.reader.msgs = replyMsgs(3, 5)
		f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "C0123ABC"))
		if len(f.reader.calls) != 1 || f.reader.calls[0].Limit != 15 {
			t.Errorf("reader calls %+v", f.reader.calls)
		}
	})
	t.Run("text cap", func(t *testing.T) {
		f := newHistFixture()
		f.reader.msgs = replyMsgs(40, 1024)
		pr := decodeHistory(t, f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "C0123ABC")))
		if len(pr.Messages) > 32 || !pr.Truncated {
			t.Errorf("msgs=%d truncated=%v, want <=32 and truncated", len(pr.Messages), pr.Truncated)
		}
	})
}

func TestHandleHistory_GrepIsCaseInsensitiveSubstringBeforeCap(t *testing.T) {
	f := newHistFixture()
	f.reader.msgs = []SlackReplyMessage{
		{TS: "1700000003.000100", Text: "WAF rule live"},
		{TS: "1700000002.000100", Text: "rollback"},
		{TS: "1700000001.000100", Text: "waf tuning"},
	}
	e := historyEnv("sb-abc123", "C0123ABC")
	e.Grep = "waf"
	pr := decodeHistory(t, f.h.handleHistory(context.Background(), e))
	if len(pr.Messages) != 2 {
		t.Fatalf("grep gave %d messages", len(pr.Messages))
	}
	// Grep applies before the cap: limit 1 still finds the match that follows a non-match.
	f.reader.msgs = []SlackReplyMessage{{TS: "1700000003.000100", Text: "nope"}, {TS: "1700000002.000100", Text: "waf here"}}
	e.Limit = 1
	pr = decodeHistory(t, f.h.handleHistory(context.Background(), e))
	if len(pr.Messages) != 1 || pr.Messages[0].Text != "waf here" {
		t.Errorf("grep-before-cap: %+v", pr.Messages)
	}
}

func TestHandleHistory_ThreadReadsRepliesLive(t *testing.T) {
	f := newHistFixture()
	f.route("CINC", "ir", &RouteHistory{Query: true, Mirror: true, MaxMessages: 50})
	f.thread.msgs = []SlackReplyMessage{{TS: "1.1", Text: "root"}, {TS: "1.2", Text: "reply"}}
	e := historyEnv("sb-abc123", "CINC")
	e.MessageTS = "1.1"
	resp := f.h.handleHistory(context.Background(), e)
	pr := decodeHistory(t, resp)
	if resp.StatusCode != 200 || pr.Source != "live" || len(pr.Messages) != 2 {
		t.Fatalf("status %d source %q msgs %d", resp.StatusCode, pr.Source, len(pr.Messages))
	}
	if f.thread.calls != 1 || len(f.reader.calls) != 0 || f.store.queryCalls != 0 {
		t.Errorf("thread=%d reader=%d store=%d", f.thread.calls, len(f.reader.calls), f.store.queryCalls)
	}
}

func TestHandleHistory_RateLimitedIs503WithRetryAfter(t *testing.T) {
	f := newHistFixture()
	f.reader.err = &ErrSlackRateLimited{RetryAfterSeconds: 42}
	resp := f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "C0123ABC"))
	if resp.StatusCode != 503 || !strings.Contains(resp.Body, "rate_limited") || resp.Headers["Retry-After"] != "42" {
		t.Fatalf("got %d %s %v", resp.StatusCode, resp.Body, resp.Headers)
	}
}

func TestHandleHistory_RecordsQuota(t *testing.T) {
	f := newHistFixture()
	f.reader.msgs = replyMsgs(1, 5)
	f.h.Quota = NewFakeQuotaClient(2)
	f.h.QuotaTable = "km-action-quota"
	f.h.Limits = &histLimits{json: `{"slack_history":{"perHour":1,"onBreach":"block"}}`}
	resp := f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "C0123ABC"))
	if resp.StatusCode != 429 || !strings.Contains(resp.Body, "quota_exceeded") {
		t.Fatalf("want 429 quota_exceeded, got %d %s", resp.StatusCode, resp.Body)
	}
	f.noReads(t)

	// Under the limit: allowed (count 1 == limit 1 is not exceeded).
	f.h.Quota = NewFakeQuotaClient(1)
	if resp := f.h.handleHistory(context.Background(), historyEnv("sb-abc123", "C0123ABC")); resp.StatusCode != 200 {
		t.Fatalf("under quota: %d %s", resp.StatusCode, resp.Body)
	}
}

func TestHandleHistory_MissingFieldsAndBadBounds(t *testing.T) {
	f := newHistFixture()
	e := historyEnv("sb-abc123", "C0123ABC")
	e.Limit = -1
	if r := f.h.handleHistory(context.Background(), e); r.StatusCode != 400 || !strings.Contains(r.Body, "bad_limit") {
		t.Errorf("limit -1: %d %s", r.StatusCode, r.Body)
	}
	e = historyEnv("sb-abc123", "C0123ABC")
	e.Oldest = "abc"
	if r := f.h.handleHistory(context.Background(), e); r.StatusCode != 400 || !strings.Contains(r.Body, "bad_ts") {
		t.Errorf("oldest abc: %d %s", r.StatusCode, r.Body)
	}
	// Empty channel: Handle's channelRequired keeps history on the required list.
	e = historyEnv("sb-abc123", "")
	b, _ := json.Marshal(e)
	r := f.h.Handle(context.Background(), &Request{Body: string(b)})
	if r.StatusCode != 400 || !strings.Contains(r.Body, "missing_fields") {
		t.Errorf("empty channel: %d %s", r.StatusCode, r.Body)
	}
}
