package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"

	kmslack "github.com/whereiskurt/klanker-maker/pkg/slack"
	slackbridge "github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

type fakeSQSSend struct{ sent []*sqs.SendMessageInput }

func (f *fakeSQSSend) SendMessage(_ context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.sent = append(f.sent, in)
	return &sqs.SendMessageOutput{}, nil
}

// orderStore records the order of PurgeChannel and PutMeta calls.
type orderStore struct {
	fakePurgeStore
	calls []string
}

func (o *orderStore) PutMeta(ctx context.Context, m slackbridge.ChannelMeta) error {
	o.calls = append(o.calls, "put:"+m.Channel)
	return o.fakePurgeStore.PutMeta(ctx, m)
}
func (o *orderStore) PurgeChannel(ctx context.Context, ch string) (int, error) {
	o.calls = append(o.calls, "purge:"+ch)
	return o.fakePurgeStore.PurgeChannel(ctx, ch)
}

// fillRoutes: C4 named mirror route (alias sec), C6 named non-mirror route,
// glob inc_* (alias ir, mirror, 7d back, 50 msgs) matching C1/C2 by name.
func fillRoutes() purgeRouteTable {
	mirror := &slackbridge.RouteHistory{Query: true, Mirror: true, BackfillSeconds: 7 * 86400, MaxMessages: 50}
	doc := &slackbridge.RoutesDocument{V: slackbridge.RoutesDocumentVersion,
		Routes: []slackbridge.RouteEntry{
			{Channel: "C4", Name: "sec-ops", Alias: "sec", Profile: "p", Allow: []string{"a@example.com"}, History: &slackbridge.RouteHistory{Query: true, Mirror: true}},
			{Channel: "C6", Name: "plain", Alias: "ir", Profile: "p", Allow: []string{"a@example.com"}, History: &slackbridge.RouteHistory{Query: true}},
		},
		Patterns: []slackbridge.PatternEntry{{Glob: "inc_*", Alias: "ir", Profile: "p", Allow: []string{"a@example.com"}, History: mirror}}}
	return func(context.Context) (*slackbridge.RoutesDocument, error) { return doc, nil }
}

func fillLister() *fakeLister {
	return &fakeLister{channels: []kmslack.SlackChannelSummary{
		{ID: "C1", Name: "inc_1"}, {ID: "C2", Name: "inc_2"}, {ID: "C3", Name: "other"},
		{ID: "C4", Name: "sec-ops"}, {ID: "C6", Name: "plain"}}}
}

func fillIDs(ts []fillTarget) string {
	var s []string
	for _, t := range ts {
		s = append(s, t.channelID)
	}
	return strings.Join(s, ",")
}

func TestResolveFillTargets(t *testing.T) {
	ctx := context.Background()
	var warn bytes.Buffer
	cases := []struct {
		arg, alias, want string
	}{
		{"C1", "", "C1"},
		{"#inc_2", "", "C2"},
		{"C4", "", "C4"},
		{"", "ir", "C1,C2"}, // C3 no route, C6 non-mirror excluded
	}
	for _, c := range cases {
		got, err := resolveFillTargets(ctx, fillLister(), fillRoutes(), c.arg, c.alias, &warn)
		if err != nil || fillIDs(got) != c.want {
			t.Errorf("%+v: got %q err %v, want %q", c, fillIDs(got), err, c.want)
		}
	}
	if !strings.Contains(warn.String(), "plain") || !strings.Contains(warn.String(), "without history.mirror") {
		t.Errorf("alias form should warn about the non-mirror channel: %q", warn.String())
	}
	got, _ := resolveFillTargets(ctx, fillLister(), fillRoutes(), "C1", "", &warn)
	if got[0].entry.Pattern != "inc_*" || got[0].entry.Alias != "ir" || got[0].name != "inc_1" || got[0].entry.Channel != "C1" {
		t.Errorf("glob target entry = %+v", got[0])
	}
	got, _ = resolveFillTargets(ctx, fillLister(), fillRoutes(), "C4", "", &warn)
	if got[0].entry.Pattern != "" || got[0].entry.Alias != "sec" {
		t.Errorf("static target entry = %+v", got[0])
	}
}

func TestResolveFillTargets_Errors(t *testing.T) {
	ctx := context.Background()
	var w bytes.Buffer
	check := func(arg, alias, want string) {
		t.Helper()
		_, err := resolveFillTargets(ctx, fillLister(), fillRoutes(), arg, alias, &w)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q/%q: want error containing %q, got %v", arg, alias, want, err)
		}
	}
	check("C9", "", "the bot is not in channel C9; invite it first")
	check("#nope", "", "#inc_1")
	check("C3", "", "slack.routes")
	check("C6", "", "channel #plain is routed to ir without history.mirror — add mirror: true and km init --slack")
	check("", "ghost", "ghost")
	check("", "", "exactly one")
	check("C1", "ir", "exactly one")
	if _, err := resolveFillTargets(ctx, fillLister(), nil, "C1", "", &w); err == nil || !strings.Contains(err.Error(), "slack.routes") {
		t.Errorf("no route table: %v", err)
	}
}

func fillTargetFor(t *testing.T, id string) []fillTarget {
	t.Helper()
	got, err := resolveFillTargets(context.Background(), fillLister(), fillRoutes(), id, "", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestRunSlackHistoryFill_BindsAndQueuesOneJob(t *testing.T) {
	st := &orderStore{}
	sq := &fakeSQSSend{}
	q := &slackbridge.SQSBackfillQueue{Client: sq, QueueURL: "https://sqs/q"}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	if err := runSlackHistoryFill(context.Background(), st, q, fillTargetFor(t, "C1"), false, now, &out); err != nil {
		t.Fatal(err)
	}
	if len(st.putMeta) != 1 {
		t.Fatalf("putMeta=%v", st.putMeta)
	}
	m := st.putMeta[0]
	wantOldest := "1792238400.000000" // 2026-10-17T12:00:00Z
	if m.Channel != "C1" || m.Name != "inc_1" || m.BackfillState != "pending" || m.BackfillOldest != wantOldest ||
		!m.Announced || m.Route.Pattern != "inc_*" || m.Route.Alias != "ir" || m.MatchedAt != "2026-10-24T12:00:00Z" {
		t.Errorf("meta = %+v", m)
	}
	if len(sq.sent) != 1 || sq.sent[0].DelaySeconds != 0 {
		t.Fatalf("sent=%v", sq.sent)
	}
	var job slackbridge.BackfillJob
	if err := json.Unmarshal([]byte(*sq.sent[0].MessageBody), &job); err != nil {
		t.Fatal(err)
	}
	if job.Channel != "C1" || job.Oldest != wantOldest || job.Remaining != 50 {
		t.Errorf("job = %+v", job)
	}
	if want := "#inc_1 (C1, ir): bound, backfill queued (back 7 days, ≤50 msgs)"; !strings.Contains(out.String(), want) {
		t.Errorf("out = %q, want %q", out.String(), want)
	}
}

func TestRunSlackHistoryFill_NoBackfillWindow(t *testing.T) {
	st := &orderStore{}
	sq := &fakeSQSSend{}
	q := &slackbridge.SQSBackfillQueue{Client: sq, QueueURL: "https://sqs/q"}
	var out bytes.Buffer
	// C4's route mirrors with backfill 0.
	if err := runSlackHistoryFill(context.Background(), st, q, fillTargetFor(t, "C4"), false, time.Now(), &out); err != nil {
		t.Fatal(err)
	}
	if st.putMeta[0].BackfillState != "off" || !st.putMeta[0].Announced || len(sq.sent) != 0 {
		t.Errorf("meta=%+v sent=%d", st.putMeta[0], len(sq.sent))
	}
	if !strings.Contains(out.String(), "bound, no backfill (route has backfill 0)") {
		t.Errorf("out = %q", out.String())
	}
}

func TestRunSlackHistoryFill_RefusesAlreadyBound(t *testing.T) {
	st := &orderStore{fakePurgeStore: fakePurgeStore{meta: []slackbridge.ChannelMeta{{Channel: "C1", Name: "inc_1", BackfillState: "done"}}}}
	sq := &fakeSQSSend{}
	q := &slackbridge.SQSBackfillQueue{Client: sq, QueueURL: "https://sqs/q"}
	err := runSlackHistoryFill(context.Background(), st, q, fillTargetFor(t, "C1"), false, time.Now(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "already bound (backfill_state=done); use --purge-first to clear and reload") {
		t.Fatalf("err = %v", err)
	}
	if len(st.calls) != 0 || len(sq.sent) != 0 {
		t.Errorf("nothing may be written or sent: calls=%v sent=%d", st.calls, len(sq.sent))
	}
}

func TestRunSlackHistoryFill_PurgeFirstPurgesBeforePutMeta(t *testing.T) {
	st := &orderStore{fakePurgeStore: fakePurgeStore{
		meta: []slackbridge.ChannelMeta{{Channel: "C1", Name: "inc_1", BackfillState: "done"}}, rows: map[string]int{"C1": 12}}}
	sq := &fakeSQSSend{}
	q := &slackbridge.SQSBackfillQueue{Client: sq, QueueURL: "https://sqs/q"}
	var out bytes.Buffer
	if err := runSlackHistoryFill(context.Background(), st, q, fillTargetFor(t, "C1"), true, time.Now(), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Join(st.calls, ",") != "purge:C1,put:C1" || len(sq.sent) != 1 {
		t.Errorf("calls=%v sent=%d", st.calls, len(sq.sent))
	}
	if !strings.Contains(out.String(), "12 row(s) purged") {
		t.Errorf("out = %q", out.String())
	}
}

func TestSlackHistoryFillCmd_YesRuleAndUsage(t *testing.T) {
	st := &orderStore{}
	sq := &fakeSQSSend{}
	mk := func() (*bytes.Buffer, func(args ...string) error) {
		var out bytes.Buffer
		c := newSlackHistoryFillCmd(func(context.Context) (*fillDeps, error) {
			return &fillDeps{Store: st, Lister: fillLister(), Routes: fillRoutes(),
				Queue: func(context.Context) (slackbridge.BackfillEnqueuer, error) {
					return &slackbridge.SQSBackfillQueue{Client: sq, QueueURL: "u"}, nil
				}}, nil
		}, &out)
		c.SetOut(&out)
		c.SetErr(&out)
		return &out, func(args ...string) error { c.SetArgs(args); return c.Execute() }
	}
	_, run := mk()
	if err := run("C1"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("no --yes on non-TTY: %v", err)
	}
	if len(st.putMeta) != 0 {
		t.Fatal("wrote without confirmation")
	}
	out, run := mk()
	if err := run("C1", "--yes"); err != nil || len(st.putMeta) != 1 || !strings.Contains(out.String(), "bound, backfill queued") {
		t.Fatalf("with --yes: err=%v out=%s", err, out.String())
	}

	var uo bytes.Buffer
	hist := newSlackHistoryCmd(nil, nil)
	hist.SetOut(&uo)
	_ = hist.Help()
	if !strings.Contains(uo.String(), "fill") || !strings.Contains(uo.String(), "purge") {
		t.Errorf("usage should list fill and purge: %s", uo.String())
	}
}
