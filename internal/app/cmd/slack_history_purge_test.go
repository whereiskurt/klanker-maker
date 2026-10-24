package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	kmslack "github.com/whereiskurt/klanker-maker/pkg/slack"
	slackbridge "github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

type fakePurgeStore struct {
	meta    []slackbridge.ChannelMeta
	rows    map[string]int
	purged  []string
	putMeta []slackbridge.ChannelMeta
}

func (f *fakePurgeStore) PutMessage(context.Context, slackbridge.HistoryMessageRow) error {
	return nil
}
func (f *fakePurgeStore) UpdateText(context.Context, string, string, string, string) error {
	return nil
}
func (f *fakePurgeStore) DeleteMessage(context.Context, string, string) error { return nil }
func (f *fakePurgeStore) Query(context.Context, string, string, string, string, int) ([]slackbridge.HistoryMessageRow, bool, error) {
	return nil, false, nil
}
func (f *fakePurgeStore) PutMeta(_ context.Context, m slackbridge.ChannelMeta) error {
	f.putMeta = append(f.putMeta, m)
	return nil
}
func (f *fakePurgeStore) GetMeta(_ context.Context, ch string) (*slackbridge.ChannelMeta, error) {
	for _, m := range f.meta {
		if m.Channel == ch {
			c := m
			return &c, nil
		}
	}
	return nil, nil
}
func (f *fakePurgeStore) ListMeta(context.Context) ([]slackbridge.ChannelMeta, error) {
	return f.meta, nil
}
func (f *fakePurgeStore) PurgeChannel(_ context.Context, ch string) (int, error) {
	f.purged = append(f.purged, ch)
	return f.rows[ch], nil
}

func purgeFixture() (*fakePurgeStore, *fakeLister) {
	mk := func(id, name, alias string) slackbridge.ChannelMeta {
		return slackbridge.ChannelMeta{Channel: id, Name: name, Route: slackbridge.RouteEntry{Alias: alias},
			BackfillState: "done", BackfillWritten: 7, BackfillCursor: "x"}
	}
	st := &fakePurgeStore{
		meta: []slackbridge.ChannelMeta{mk("C2", "inc_2", "ir"), mk("C1", "inc_1", "ir"), mk("C3", "sec-1", "sec")},
		rows: map[string]int{"C1": 40, "C2": 2, "C3": 5},
	}
	l := &fakeLister{channels: []kmslack.SlackChannelSummary{{ID: "C1", Name: "inc_1"}, {ID: "C2", Name: "inc_2"}, {ID: "C3", Name: "sec-1"}}}
	return st, l
}

func ids(ts []historyPurgeTarget) string {
	var s []string
	for _, t := range ts {
		s = append(s, t.channelID)
	}
	return strings.Join(s, ",")
}

func TestResolvePurgeTargets_ByIDNameAliasAll(t *testing.T) {
	ctx := context.Background()
	st, l := purgeFixture()
	cases := []struct {
		arg, alias string
		all        bool
		want       string
	}{
		{"C1", "", false, "C1"},
		{"#inc_2", "", false, "C2"},
		{"", "ir", false, "C1,C2"},
		{"", "", true, "C1,C2,C3"},
	}
	for _, c := range cases {
		got, err := resolvePurgeTargets(ctx, st, l, nil, c.arg, c.alias, c.all)
		if err != nil || ids(got) != c.want {
			t.Errorf("%+v: got %q err %v, want %q", c, ids(got), err, c.want)
		}
	}
	if _, err := resolvePurgeTargets(ctx, st, l, nil, "#nope", "", false); err == nil || !strings.Contains(err.Error(), "#inc_1") {
		t.Errorf("#nope: want error listing bot channels, got %v", err)
	}
	if _, err := resolvePurgeTargets(ctx, st, l, nil, "C9", "", false); err == nil || !strings.Contains(err.Error(), "not mirrored") {
		t.Errorf("C9: want not mirrored, got %v", err)
	}
	for _, c := range []struct {
		arg, alias string
		all        bool
	}{{"", "", false}, {"C1", "ir", false}, {"", "ir", true}} {
		if _, err := resolvePurgeTargets(ctx, st, l, nil, c.arg, c.alias, c.all); err == nil {
			t.Errorf("%+v: want exactly-one error", c)
		}
	}
}

func TestRunSlackHistoryPurge_DeletesRowsAndMeta(t *testing.T) {
	st, _ := purgeFixture()
	var out bytes.Buffer
	ts := []historyPurgeTarget{{channelID: "C1", name: "inc_1", alias: "ir"}, {channelID: "C3", name: "sec-1", alias: "sec"}}
	if err := runSlackHistoryPurge(context.Background(), st, ts, false, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Join(st.purged, ",") != "C1,C3" || len(st.putMeta) != 0 {
		t.Fatalf("purged=%v putMeta=%v", st.purged, st.putMeta)
	}
	s := out.String()
	for _, w := range []string{"#inc_1 (C1, alias ir): 40 row(s) purged, meta removed", "#sec-1 (C3, alias sec): 5 row(s) purged, meta removed", "2 channel(s), 45 row(s) purged"} {
		if !strings.Contains(s, w) {
			t.Errorf("output missing %q:\n%s", w, s)
		}
	}
}

func TestRunSlackHistoryPurge_KeepMetaRewritesMetaAfterPurge(t *testing.T) {
	st, _ := purgeFixture()
	var out bytes.Buffer
	ts := []historyPurgeTarget{{channelID: "C1", name: "inc_1", alias: "ir"}}
	if err := runSlackHistoryPurge(context.Background(), st, ts, true, &out); err != nil {
		t.Fatal(err)
	}
	if len(st.purged) != 1 || len(st.putMeta) != 1 {
		t.Fatalf("purged=%v putMeta=%v", st.purged, st.putMeta)
	}
	m := st.putMeta[0]
	if m.Channel != "C1" || m.Route.Alias != "ir" || m.BackfillState != "off" || m.BackfillWritten != 0 || m.BackfillCursor != "" {
		t.Errorf("meta = %+v", m)
	}
	if !strings.Contains(out.String(), "meta kept") {
		t.Errorf("output: %s", out.String())
	}
}

func TestSlackHistoryPurgeCmd_RequiresYesOrConfirms(t *testing.T) {
	st, _ := purgeFixture()
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		c := newSlackHistoryPurgeCmdWithStore(st, nil, nil, &out)
		c.SetArgs(args)
		c.SetOut(&out)
		c.SetErr(&out)
		err := c.Execute()
		return out.String(), err
	}
	if _, err := run("C1"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("no --yes on non-TTY: want error naming --yes, got %v", err)
	}
	if len(st.purged) != 0 {
		t.Fatalf("purged without confirmation: %v", st.purged)
	}
	out, err := run("C1", "--yes")
	if err != nil || len(st.purged) != 1 || !strings.Contains(out, "40 row(s) purged") {
		t.Fatalf("with --yes: err=%v purged=%v out=%s", err, st.purged, out)
	}
}

// liveRoutes is a purgeRouteTable over a fixed document: C4 is a named route
// with mirror: true, C5 a glob match with mirror: true, C6 a query-only route,
// and C1 (which has a meta row) is also listed.
func liveRoutes() purgeRouteTable {
	mirror := &slackbridge.RouteHistory{Query: true, Mirror: true}
	doc := &slackbridge.RoutesDocument{V: slackbridge.RoutesDocumentVersion,
		Routes: []slackbridge.RouteEntry{
			{Channel: "C4", Name: "sec-ops", Alias: "sec", Profile: "p", Allow: []string{"a@example.com"}, History: mirror},
			{Channel: "C6", Name: "plain", Alias: "sec", Profile: "p", Allow: []string{"a@example.com"}, History: &slackbridge.RouteHistory{Query: true}},
			{Channel: "C1", Name: "inc_1", Alias: "ir", Profile: "p", Allow: []string{"a@example.com"}, History: mirror},
		},
		Patterns: []slackbridge.PatternEntry{{Glob: "inc_*", Alias: "ir", Profile: "p", Allow: []string{"a@example.com"}, History: mirror,
			Channels: []slackbridge.PatternChannel{{ID: "C5", Name: "inc_5"}}}}}
	return func(context.Context) (*slackbridge.RoutesDocument, error) { return doc, nil }
}

func TestResolvePurgeTargets_StaticMirrorChannelsWithoutMetaRows(t *testing.T) {
	ctx := context.Background()
	st, l := purgeFixture()
	l.channels = append(l.channels, kmslack.SlackChannelSummary{ID: "C4", Name: "sec-ops"}, kmslack.SlackChannelSummary{ID: "C6", Name: "plain"})
	cases := []struct {
		arg, alias string
		all        bool
		want       string
	}{
		{"C4", "", false, "C4"},
		{"#sec-ops", "", false, "C4"},
		{"C5", "", false, "C5"},
		{"", "sec", false, "C3,C4"},
		{"", "ir", false, "C1,C2,C5"},
		{"", "", true, "C1,C2,C3,C4,C5"},
	}
	for _, c := range cases {
		got, err := resolvePurgeTargets(ctx, st, l, liveRoutes(), c.arg, c.alias, c.all)
		if err != nil || ids(got) != c.want {
			t.Errorf("%+v: got %q err %v, want %q", c, ids(got), err, c.want)
		}
		for _, tg := range got {
			if want := tg.channelID == "C4" || tg.channelID == "C5"; tg.noMeta != want {
				t.Errorf("%+v: %s noMeta=%v", c, tg.channelID, tg.noMeta)
			}
		}
	}
	if _, err := resolvePurgeTargets(ctx, st, l, liveRoutes(), "C6", "", false); err == nil || !strings.Contains(err.Error(), "not mirrored") {
		t.Errorf("a query-only route has nothing mirrored: %v", err)
	}
	failing := func(context.Context) (*slackbridge.RoutesDocument, error) { return nil, errors.New("ssm down") }
	if _, err := resolvePurgeTargets(ctx, st, l, failing, "", "", true); err == nil || !strings.Contains(err.Error(), "ssm down") {
		t.Errorf("an unreadable route table must stop --all: %v", err)
	}
}

func TestRunSlackHistoryPurge_NoMetaRowTargetSaysSo(t *testing.T) {
	st, _ := purgeFixture()
	st.rows["C4"] = 3
	var out bytes.Buffer
	if err := runSlackHistoryPurge(context.Background(), st, []historyPurgeTarget{{channelID: "C4", name: "sec-ops", alias: "sec", noMeta: true}}, false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "#sec-ops (C4, alias sec): 3 row(s) purged, (no meta row)") || strings.Join(st.purged, ",") != "C4" {
		t.Fatalf("out=%s purged=%v", out.String(), st.purged)
	}
}

func TestReadLiveRoutesDocument(t *testing.T) {
	ctx := context.Background()
	store := &fakeRoutesSSM{m: map[string]string{}}
	if doc, err := readLiveRoutesDocument(store, "/km/slack/routes")(ctx); err != nil || doc != nil {
		t.Fatalf("absent table: doc=%v err=%v", doc, err)
	}
	b, _ := slackbridge.CanonicalRoutesDocument(nil, []slackbridge.PatternEntry{{Glob: "inc_*", Alias: "ir", Profile: "p", Allow: []string{"a@example.com"},
		History: &slackbridge.RouteHistory{Query: true, Mirror: true}, Channels: []slackbridge.PatternChannel{{ID: "C5", Name: "inc_5"}}}})
	store.m["/km/slack/routes"] = string(b)
	doc, err := readLiveRoutesDocument(store, "/km/slack/routes")(ctx)
	if err != nil || len(staticMirrorChannels(doc)) != 1 {
		t.Fatalf("doc=%+v err=%v", doc, err)
	}
	store.m["/km/slack/routes"] = "{not json"
	if _, err := readLiveRoutesDocument(store, "/km/slack/routes")(ctx); err == nil {
		t.Fatal("an invalid table must be an error")
	}
}
