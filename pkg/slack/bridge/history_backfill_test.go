// pkg/slack/bridge/history_backfill_test.go
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

func TestHistoryAdapter_WireFormatAndRateLimit(t *testing.T) {
	var form map[string][]string
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.history" || r.Method != http.MethodPost {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		r.ParseForm()
		form = r.Form
		switch mode {
		case "429":
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(429)
			w.Write([]byte(`{"ok":false,"error":"ratelimited"}`))
		case "body":
			w.Write([]byte(`{"ok":false,"error":"ratelimited"}`))
		default:
			w.Write([]byte(`{"ok":true,"messages":[{"user":"U1","ts":"2.0","text":"a","thread_ts":"1.0"},{"user":"U2","ts":"1.5","text":"b"}],"has_more":true}`))
		}
	}))
	defer srv.Close()
	a := &SlackThreadReaderAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Tokens: stubTokens{}}
	ctx := context.Background()

	msgs, more, err := a.History(ctx, "C1", "0.5", "9.0", 15)
	if err != nil || !more || len(msgs) != 2 || msgs[0].ThreadTS != "1.0" || msgs[1].Text != "b" {
		t.Fatalf("got %+v more=%v err=%v", msgs, more, err)
	}
	if form["channel"][0] != "C1" || form["oldest"][0] != "0.5" || form["latest"][0] != "9.0" || form["limit"][0] != "15" || form["inclusive"][0] != "false" {
		t.Fatalf("form %v", form)
	}

	a.History(ctx, "C1", "", "", 5000)
	if _, ok := form["oldest"]; ok {
		t.Errorf("oldest must be omitted when empty: %v", form)
	}
	if _, ok := form["latest"]; ok {
		t.Errorf("latest must be omitted when empty: %v", form)
	}
	if form["limit"][0] != "999" {
		t.Errorf("limit not clamped: %v", form["limit"])
	}
	a.History(ctx, "C1", "", "", 0)
	if form["limit"][0] != "200" {
		t.Errorf("limit 0 should default to 200: %v", form["limit"])
	}

	mode = "429"
	_, _, err = a.History(ctx, "C1", "", "", 15)
	rl, ok := err.(*ErrSlackRateLimited)
	if !ok || rl.RetryAfterSeconds != 7 {
		t.Fatalf("429: %#v", err)
	}
	mode = "body"
	_, _, err = a.History(ctx, "C1", "", "", 15)
	rl, ok = err.(*ErrSlackRateLimited)
	if !ok || rl.RetryAfterSeconds != 60 {
		t.Fatalf("ratelimited body: %#v", err)
	}
}

type fakeSQSSend struct{ in []*sqs.SendMessageInput }

func (f *fakeSQSSend) SendMessage(_ context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.in = append(f.in, in)
	return &sqs.SendMessageOutput{}, nil
}

func TestSQSBackfillQueue_SendsStandardWithDelay(t *testing.T) {
	f := &fakeSQSSend{}
	q := &SQSBackfillQueue{Client: f, QueueURL: "https://sqs/q"}
	job := BackfillJob{Channel: "C1", Oldest: "1.0", Latest: "5.0", Remaining: 42}
	if err := q.Enqueue(context.Background(), job, 60); err != nil {
		t.Fatal(err)
	}
	if len(f.in) != 1 {
		t.Fatalf("sends %d", len(f.in))
	}
	in := f.in[0]
	if in.DelaySeconds != 60 || in.MessageGroupId != nil || in.MessageDeduplicationId != nil || *in.QueueUrl != "https://sqs/q" {
		t.Fatalf("input %+v", in)
	}
	var got BackfillJob
	if err := json.Unmarshal([]byte(*in.MessageBody), &got); err != nil || got != job {
		t.Fatalf("body %q -> %+v %v", *in.MessageBody, got, err)
	}
	if err := q.Enqueue(context.Background(), job, 3600); err != nil || f.in[1].DelaySeconds != 900 {
		t.Fatalf("delay not clamped to 900: %v %+v", err, f.in)
	}
	if err := (&SQSBackfillQueue{Client: f}).Enqueue(context.Background(), job, 0); err == nil {
		t.Error("empty queue url must error")
	}
}

func backfillHandler(meta *ChannelMeta, rd *fakeHistoryReader) (*EventsHandler, *fakeHistoryStore, *fakeBackfill) {
	st := newFakeHistoryStore()
	if meta != nil {
		st.meta[meta.Channel] = *meta
	}
	bf := &fakeBackfill{}
	now := time.Unix(1_800_000_000, 0)
	// C7 is a static route that mirrors (retention 1h): the backfill resolves
	// the channel's CURRENT route, never the meta row's copy.
	routes := map[string]*DefaultRoute{"C7": histRoute("ir", &RouteHistory{Query: true, Mirror: true, RetentionSeconds: 3600})}
	return &EventsHandler{History: st, HistoryReader: rd, Backfill: bf, Routes: routes, Now: func() time.Time { return now }}, st, bf
}

func pendingMeta() *ChannelMeta {
	return &ChannelMeta{Channel: "C7", BackfillState: "pending", Route: RouteEntry{History: &RouteHistory{Mirror: true, RetentionSeconds: 3600}}}
}

func descMsgs(n int, top int) []SlackReplyMessage {
	var out []SlackReplyMessage
	for i := 0; i < n; i++ {
		out = append(out, SlackReplyMessage{User: "U1", Text: fmt.Sprintf("m%d", i), TS: fmt.Sprintf("%d.000100", top-i)})
	}
	return out
}

func TestBackfillStep_WritesPageAndReenqueues(t *testing.T) {
	rd := &fakeHistoryReader{msgs: descMsgs(15, 1700000100), more: true}
	h, st, bf := backfillHandler(pendingMeta(), rd)
	h.HistoryPageSize, h.HistoryIntervalSeconds = 15, 60
	if err := h.BackfillStep(context.Background(), BackfillJob{Channel: "C7", Oldest: "1700000000.000000", Remaining: 200}); err != nil {
		t.Fatal(err)
	}
	if len(rd.calls) != 1 || rd.calls[0].Limit != 15 {
		t.Fatalf("reader calls %+v", rd.calls)
	}
	if len(st.rows) != 15 {
		t.Fatalf("rows %d", len(st.rows))
	}
	wantExp := time.Unix(1_800_000_000, 0).Add(time.Hour).Unix()
	for _, r := range st.rows {
		if r.ExpiresAt != wantExp {
			t.Fatalf("expires_at %d want %d", r.ExpiresAt, wantExp)
		}
	}
	cursor := "1700000086.000100"
	m := st.meta["C7"]
	if m.BackfillState != "running" || m.BackfillCursor != cursor || m.BackfillWritten != 15 {
		t.Fatalf("meta %+v", m)
	}
	if len(bf.jobs) != 1 || bf.jobs[0] != (BackfillJob{Channel: "C7", Oldest: "1700000000.000000", Latest: cursor, Remaining: 185}) || bf.delays[0] != 60 {
		t.Fatalf("jobs %+v delays %v", bf.jobs, bf.delays)
	}

	// Defaults: fields left 0.
	rd2 := &fakeHistoryReader{msgs: descMsgs(3, 1700000100), more: true}
	h2, _, bf2 := backfillHandler(pendingMeta(), rd2)
	if err := h2.BackfillStep(context.Background(), BackfillJob{Channel: "C7", Oldest: "1.0", Remaining: 1000}); err != nil {
		t.Fatal(err)
	}
	if rd2.calls[0].Limit != 200 || len(bf2.delays) != 1 || bf2.delays[0] != 2 {
		t.Fatalf("defaults: limit %d delays %v", rd2.calls[0].Limit, bf2.delays)
	}
}

func TestBackfillStep_StopsAtWindow(t *testing.T) {
	rd := &fakeHistoryReader{msgs: descMsgs(4, 1700000100), more: false}
	h, st, bf := backfillHandler(pendingMeta(), rd)
	if err := h.BackfillStep(context.Background(), BackfillJob{Channel: "C7", Oldest: "1.0", Remaining: 200}); err != nil {
		t.Fatal(err)
	}
	if st.meta["C7"].BackfillState != "done" || len(bf.jobs) != 0 || len(st.rows) != 4 {
		t.Fatalf("meta %+v jobs %+v rows %d", st.meta["C7"], bf.jobs, len(st.rows))
	}
}

func TestBackfillStep_StopsAtCap(t *testing.T) {
	rd := &fakeHistoryReader{msgs: descMsgs(10, 1700000100), more: true}
	h, st, bf := backfillHandler(pendingMeta(), rd)
	if err := h.BackfillStep(context.Background(), BackfillJob{Channel: "C7", Oldest: "1.0", Remaining: 10}); err != nil {
		t.Fatal(err)
	}
	if rd.calls[0].Limit != 10 {
		t.Fatalf("limit %d", rd.calls[0].Limit)
	}
	if st.meta["C7"].BackfillState != "capped" || len(bf.jobs) != 0 {
		t.Fatalf("meta %+v jobs %+v", st.meta["C7"], bf.jobs)
	}
}

func TestBackfillStep_RateLimitedReenqueuesWithRetryAfter(t *testing.T) {
	rd := &fakeHistoryReader{err: &ErrSlackRateLimited{RetryAfterSeconds: 30, Method: "conversations.history"}}
	h, st, bf := backfillHandler(pendingMeta(), rd)
	job := BackfillJob{Channel: "C7", Oldest: "1.0", Latest: "9.0", Remaining: 50}
	if err := h.BackfillStep(context.Background(), job); err != nil {
		t.Fatalf("must return nil: %v", err)
	}
	if st.meta["C7"].BackfillState != "rate_limited" {
		t.Fatalf("state %q", st.meta["C7"].BackfillState)
	}
	if len(bf.jobs) != 1 || bf.jobs[0] != job || bf.delays[0] != 30 {
		t.Fatalf("jobs %+v delays %v", bf.jobs, bf.delays)
	}
}

func TestBackfillStep_NoMetaMeansChannelLeft(t *testing.T) {
	rd := &fakeHistoryReader{msgs: descMsgs(2, 100)}
	h, st, bf := backfillHandler(nil, rd)
	if err := h.BackfillStep(context.Background(), BackfillJob{Channel: "C7", Remaining: 10}); err != nil {
		t.Fatal(err)
	}
	if len(rd.calls) != 0 || len(bf.jobs) != 0 || len(st.rows) != 0 {
		t.Fatalf("calls %d jobs %d rows %d", len(rd.calls), len(bf.jobs), len(st.rows))
	}
}

func TestBackfillStep_BotAndAppMessagesStored(t *testing.T) {
	rd := &fakeHistoryReader{msgs: []SlackReplyMessage{
		{BotID: "B9", Subtype: "bot_message", Text: "deploy ok", TS: "1700000050.000100"},
		{User: "U1", Subtype: "channel_join", Text: "joined", TS: "1700000040.000100"},
		{User: "U1", Text: "hi", TS: "1700000030.000100", Files: []SlackFile{{ID: "F1", Name: "a.png"}}},
	}}
	h, st, _ := backfillHandler(pendingMeta(), rd)
	if err := h.BackfillStep(context.Background(), BackfillJob{Channel: "C7", Oldest: "1.0", Remaining: 50}); err != nil {
		t.Fatal(err)
	}
	row, ok := st.rows["C7|1700000050.000100"]
	if !ok || row.BotID != "B9" || row.Text != "deploy ok" || row.Subtype != "bot_message" {
		t.Fatalf("bot row %+v ok=%v", row, ok)
	}
	if _, ok := st.rows["C7|1700000040.000100"]; ok {
		t.Error("channel_join must not be stored")
	}
	if r := st.rows["C7|1700000030.000100"]; len(r.Files) != 1 || r.Files[0] != "a.png" {
		t.Fatalf("files %+v", r)
	}
	if st.meta["C7"].BackfillWritten != 2 {
		t.Fatalf("written %d", st.meta["C7"].BackfillWritten)
	}
}

func TestBackfillStep_CursorAdvancesOverSkippedMessages(t *testing.T) {
	var msgs []SlackReplyMessage
	for i := 0; i < 5; i++ {
		msgs = append(msgs, SlackReplyMessage{User: "U1", Subtype: "channel_join", TS: fmt.Sprintf("%d.000100", 1700000100-i)})
	}
	rd := &fakeHistoryReader{msgs: msgs, more: true}
	h, st, bf := backfillHandler(pendingMeta(), rd)
	if err := h.BackfillStep(context.Background(), BackfillJob{Channel: "C7", Oldest: "1.0", Latest: "1700000200.000000", Remaining: 200}); err != nil {
		t.Fatal(err)
	}
	oldest := "1700000096.000100"
	m := st.meta["C7"]
	if m.BackfillState != "running" || m.BackfillWritten != 0 || len(st.rows) != 0 || m.BackfillCursor != oldest {
		t.Fatalf("meta %+v rows %d", m, len(st.rows))
	}
	if len(bf.jobs) != 1 || bf.jobs[0].Latest != oldest {
		t.Fatalf("jobs %+v", bf.jobs)
	}
}

type failTSStore struct {
	*fakeHistoryStore
	failTS string
}

func (f *failTSStore) PutMessage(ctx context.Context, m HistoryMessageRow) error {
	if m.TS == f.failTS {
		return fmt.Errorf("boom")
	}
	return f.fakeHistoryStore.PutMessage(ctx, m)
}

func TestBackfillStep_CursorStopsAtFailedWrite(t *testing.T) {
	rd := &fakeHistoryReader{msgs: descMsgs(4, 1700000100), more: true}
	h, st, bf := backfillHandler(pendingMeta(), rd)
	failTS := "1700000097.000100" // the oldest of the page
	h.History = &failTSStore{fakeHistoryStore: st, failTS: failTS}
	err := h.BackfillStep(context.Background(), BackfillJob{Channel: "C7", Oldest: "1.0", Remaining: 200})
	if err == nil {
		t.Fatal("a failed row write must return an error so SQS redelivers the page")
	}
	if len(st.rows) != 3 {
		t.Fatalf("rows %d", len(st.rows))
	}
	want := "1700000098.000100" // just newer than the failed one
	m := st.meta["C7"]
	if m.BackfillCursor != want || !strings.HasPrefix(m.BackfillState, "error:") || m.BackfillWritten != 0 {
		t.Fatalf("meta %+v; want cursor %s, an error state, and the redone page not counted yet", m, want)
	}
	if len(bf.jobs) != 0 {
		t.Fatalf("SQS redelivers the same job; no re-enqueue: %+v", bf.jobs)
	}
}

// A failed write on the LAST page used to end the backfill as done.
func TestBackfillStep_FailedWriteOnLastPageIsRetriedNotDone(t *testing.T) {
	rd := &fakeHistoryReader{msgs: descMsgs(2, 1700000100), more: false}
	h, st, bf := backfillHandler(pendingMeta(), rd)
	h.History = &failTSStore{fakeHistoryStore: st, failTS: "1700000100.000100"}
	if err := h.BackfillStep(context.Background(), BackfillJob{Channel: "C7", Oldest: "1.0", Remaining: 200}); err == nil {
		t.Fatal("want an error")
	}
	if st.meta["C7"].BackfillState == "done" || len(bf.jobs) != 0 {
		t.Fatalf("meta %+v jobs %+v", st.meta["C7"], bf.jobs)
	}
}

func TestBackfillStep_SlackErrorIsRecordedAndReturned(t *testing.T) {
	rd := &fakeHistoryReader{err: errors.New("conversations.history: internal_error")}
	h, st, bf := backfillHandler(pendingMeta(), rd)
	err := h.BackfillStep(context.Background(), BackfillJob{Channel: "C7", Oldest: "1.0", Remaining: 50})
	if err == nil {
		t.Fatal("a non-rate-limit Slack error must be returned so SQS redelivers (and dead-letters after maxReceiveCount)")
	}
	if s := st.meta["C7"].BackfillState; s != "error: conversations.history: internal_error" {
		t.Fatalf("state %q", s)
	}
	if len(bf.jobs) != 0 {
		t.Fatalf("no self re-enqueue on an error: %+v", bf.jobs)
	}
}

func TestBackfillStep_ReenqueueFailureIsReturned(t *testing.T) {
	rd := &fakeHistoryReader{msgs: descMsgs(3, 1700000100), more: true}
	h, _, _ := backfillHandler(pendingMeta(), rd)
	h.Backfill = &failingBackfill{}
	if err := h.BackfillStep(context.Background(), BackfillJob{Channel: "C7", Oldest: "1.0", Remaining: 200}); err == nil {
		t.Fatal("a lost re-enqueue would end the backfill silently; it must be returned")
	}
}

type failingBackfill struct{}

func (failingBackfill) Enqueue(context.Context, BackfillJob, int32) error {
	return errors.New("sqs down")
}

func TestBackfillStep_StopsWhenTheRouteNoLongerMirrors(t *testing.T) {
	rd := &fakeHistoryReader{msgs: descMsgs(3, 1700000100), more: true}
	h, st, bf := backfillHandler(pendingMeta(), rd)
	h.Routes = nil // the glob or named route was removed
	if err := h.BackfillStep(context.Background(), BackfillJob{Channel: "C7", Oldest: "1.0", Remaining: 200}); err != nil {
		t.Fatal(err)
	}
	if len(rd.calls) != 0 || len(st.rows) != 0 || len(bf.jobs) != 0 || st.meta["C7"].BackfillState != "off" {
		t.Fatalf("calls=%d rows=%d jobs=%+v meta=%+v", len(rd.calls), len(st.rows), bf.jobs, st.meta["C7"])
	}
}

func TestBackfillStep_MetaRowResolvesThroughCurrentPattern(t *testing.T) {
	rd := &fakeHistoryReader{msgs: descMsgs(2, 1700000100), more: false}
	meta := pendingMeta()
	meta.Route = RouteEntry{Channel: "C7", Pattern: "inc_*", Alias: "ir", History: &RouteHistory{Mirror: true, Query: true, RetentionSeconds: 60}}
	h, st, _ := backfillHandler(meta, rd)
	h.Routes = nil
	h.Patterns = []PatternRoute{patternRoute("inc_*", "ir", &RouteHistory{Mirror: true, Query: true, RetentionSeconds: 7200})}
	if err := h.BackfillStep(context.Background(), BackfillJob{Channel: "C7", Oldest: "1.0", Remaining: 200}); err != nil {
		t.Fatal(err)
	}
	want := time.Unix(1_800_000_000, 0).Add(2 * time.Hour).Unix()
	for _, r := range st.rows {
		if r.ExpiresAt != want {
			t.Fatalf("expiry must follow the current pattern's retention: %d want %d", r.ExpiresAt, want)
		}
	}
	if len(st.rows) != 2 || st.meta["C7"].BackfillState != "done" {
		t.Fatalf("rows=%d meta=%+v", len(st.rows), st.meta["C7"])
	}
}
