package bridge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeHistoryDDB emulates the slice of DynamoDB the history store uses. Items
// are keyed channel|ts. Query honours real key-condition semantics: BETWEEN is
// inclusive on both ends, so exclusivity must come from the store, not the fake.
type fakeHistoryDDB struct {
	items       map[string]map[string]types.AttributeValue
	maxPage     int // simulated 1 MB page cap, in items; 0 = unlimited
	batchSizes  []int
	unprocessed func(call int, reqs []types.WriteRequest) []types.WriteRequest // optional leftover per call
	batchCalls  int
	puts        []*dynamodb.PutItemInput
	updates     []*dynamodb.UpdateItemInput
	queries     []*dynamodb.QueryInput
}

func newFakeHistoryDDB() *fakeHistoryDDB {
	return &fakeHistoryDDB{items: map[string]map[string]types.AttributeValue{}}
}

func hs(item map[string]types.AttributeValue, k string) string {
	if v, ok := item[k].(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}

func hkey(ch, ts string) string { return ch + "|" + ts }

func (f *fakeHistoryDDB) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.puts = append(f.puts, in)
	f.items[hkey(hs(in.Item, "channel"), hs(in.Item, "ts"))] = in.Item
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeHistoryDDB) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	it := f.items[hkey(hs(in.Key, "channel"), hs(in.Key, "ts"))]
	return &dynamodb.GetItemOutput{Item: it}, nil
}

func (f *fakeHistoryDDB) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	delete(f.items, hkey(hs(in.Key, "channel"), hs(in.Key, "ts")))
	return &dynamodb.DeleteItemOutput{}, nil
}

func (f *fakeHistoryDDB) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updates = append(f.updates, in)
	k := hkey(hs(in.Key, "channel"), hs(in.Key, "ts"))
	it, ok := f.items[k]
	if in.ConditionExpression != nil && *in.ConditionExpression == "attribute_exists(ts)" && !ok {
		return nil, &types.ConditionalCheckFailedException{}
	}
	if !ok {
		it = map[string]types.AttributeValue{}
		f.items[k] = it
	}
	it["text"] = in.ExpressionAttributeValues[":t"]
	it["edited_ts"] = in.ExpressionAttributeValues[":e"]
	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *fakeHistoryDDB) BatchWriteItem(_ context.Context, in *dynamodb.BatchWriteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	out := &dynamodb.BatchWriteItemOutput{}
	for table, reqs := range in.RequestItems {
		f.batchSizes = append(f.batchSizes, len(reqs))
		f.batchCalls++
		var left []types.WriteRequest
		if f.unprocessed != nil {
			left = f.unprocessed(f.batchCalls, reqs)
		}
		skip := map[string]bool{}
		for _, l := range left {
			skip[hs(l.DeleteRequest.Key, "ts")] = true
		}
		if len(left) > 0 {
			out.UnprocessedItems = map[string][]types.WriteRequest{table: left}
		}
		for _, r := range reqs {
			if skip[hs(r.DeleteRequest.Key, "ts")] {
				continue
			}
			if r.DeleteRequest != nil {
				delete(f.items, hkey(hs(r.DeleteRequest.Key, "channel"), hs(r.DeleteRequest.Key, "ts")))
			}
		}
	}
	return out, nil
}

func (f *fakeHistoryDDB) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	f.queries = append(f.queries, in)
	v := func(n string) (string, bool) {
		a, ok := in.ExpressionAttributeValues[n]
		if !ok {
			return "", false
		}
		return a.(*types.AttributeValueMemberS).Value, true
	}
	ch, _ := v(":c")
	lo, hasLo := v(":lo")
	hi, hasHi := v(":hi")
	var all []map[string]types.AttributeValue
	for _, it := range f.items {
		if hs(it, "channel") != ch {
			continue
		}
		ts := hs(it, "ts")
		if hasLo && ts < lo { // BETWEEN: inclusive
			continue
		}
		if hasHi && ts > hi { // BETWEEN: inclusive
			continue
		}
		all = append(all, it)
	}
	sort.Slice(all, func(i, j int) bool { return hs(all[i], "ts") < hs(all[j], "ts") })
	if in.ScanIndexForward != nil && !*in.ScanIndexForward {
		for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
			all[i], all[j] = all[j], all[i]
		}
	}
	if in.ExclusiveStartKey != nil {
		start := hs(in.ExclusiveStartKey, "ts")
		for i, it := range all {
			if hs(it, "ts") == start {
				all = all[i+1:]
				break
			}
		}
	}
	page := len(all)
	if in.Limit != nil && int(*in.Limit) < page {
		page = int(*in.Limit)
	}
	if f.maxPage > 0 && f.maxPage < page {
		page = f.maxPage
	}
	out := &dynamodb.QueryOutput{Items: all[:page]}
	if page < len(all) {
		out.LastEvaluatedKey = map[string]types.AttributeValue{
			"channel": &types.AttributeValueMemberS{Value: ch},
			"ts":      &types.AttributeValueMemberS{Value: hs(all[page-1], "ts")},
		}
	}
	return out, nil
}

func testStore() (*DDBHistoryStore, *fakeHistoryDDB) {
	f := newFakeHistoryDDB()
	return &DDBHistoryStore{Client: f, TableName: "km-slack-history"}, f
}

func putN(t *testing.T, s *DDBHistoryStore, ch string, n int) []string {
	t.Helper()
	var tss []string
	for i := 1; i <= n; i++ {
		ts := fmt.Sprintf("1730000%03d.000100", i)
		tss = append(tss, ts)
		if err := s.PutMessage(context.Background(), HistoryMessageRow{Channel: ch, TS: ts, User: "U1", Text: "m" + ts, ExpiresAt: 1730000000}); err != nil {
			t.Fatal(err)
		}
	}
	return tss
}

func tsList(rows []HistoryMessageRow) []string {
	var o []string
	for _, r := range rows {
		o = append(o, r.TS)
	}
	return o
}

func TestDDBHistoryStore_PutQueryNewestFirstAndPaging(t *testing.T) {
	s, _ := testStore()
	ctx := context.Background()
	tss := putN(t, s, "C1", 5)

	rows, more, err := s.Query(ctx, "C1", "", "", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(tsList(rows), ","); got != tss[4]+","+tss[3] || !more {
		t.Fatalf("page1 = %s more=%v", got, more)
	}
	rows, more, _ = s.Query(ctx, "C1", "", "", rows[1].TS, 2)
	if got := strings.Join(tsList(rows), ","); got != tss[2]+","+tss[1] || !more {
		t.Fatalf("page2 = %s more=%v", got, more)
	}
	rows, more, _ = s.Query(ctx, "C1", "", "", rows[1].TS, 2)
	if got := strings.Join(tsList(rows), ","); got != tss[0] || more {
		t.Fatalf("page3 = %s more=%v", got, more)
	}
}

func TestDDBHistoryStore_QueryWindowBounds(t *testing.T) {
	s, _ := testStore()
	ctx := context.Background()
	tss := putN(t, s, "C1", 5)
	// (tss[1], tss[3]] : oldest exclusive, latest inclusive.
	rows, more, err := s.Query(ctx, "C1", tss[1], tss[3], "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(tsList(rows), ","); got != tss[3]+","+tss[2] || more {
		t.Fatalf("window = %s more=%v", got, more)
	}
	// before is strictly older, combined with a window.
	rows, _, _ = s.Query(ctx, "C1", tss[0], tss[4], tss[3], 10)
	if got := strings.Join(tsList(rows), ","); got != tss[2]+","+tss[1] {
		t.Fatalf("window+before = %s", got)
	}
	// latest < before: latest still bounds.
	rows, _, _ = s.Query(ctx, "C1", "", tss[1], tss[3], 10)
	if got := strings.Join(tsList(rows), ","); got != tss[1]+","+tss[0] {
		t.Fatalf("latest<before = %s", got)
	}
	// Excluded boundary rows must not eat the limit or fake a "more".
	rows, more, _ = s.Query(ctx, "C1", tss[0], "", tss[4], 3)
	if got := strings.Join(tsList(rows), ","); got != tss[3]+","+tss[2]+","+tss[1] || more {
		t.Fatalf("limit with boundaries = %s more=%v", got, more)
	}
}

func TestDDBHistoryStore_UpdateTextOnlyIfExists(t *testing.T) {
	s, f := testStore()
	ctx := context.Background()
	if err := s.UpdateText(ctx, "C1", "1730000001.000100", "new", "1730000009.000100"); err != nil {
		t.Fatalf("absent row must be a no-op, got %v", err)
	}
	if len(f.items) != 0 {
		t.Fatalf("absent row was created: %v", f.items)
	}
	if ce := f.updates[0].ConditionExpression; ce == nil || *ce != "attribute_exists(ts)" {
		t.Fatalf("condition = %v", ce)
	}
	tss := putN(t, s, "C1", 1)
	if err := s.UpdateText(ctx, "C1", tss[0], "edited", "1730000009.000100"); err != nil {
		t.Fatal(err)
	}
	rows, _, _ := s.Query(ctx, "C1", "", "", "", 5)
	if rows[0].Text != "edited" || rows[0].EditedTS != "1730000009.000100" {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestDDBHistoryStore_DeleteMessage(t *testing.T) {
	s, _ := testStore()
	ctx := context.Background()
	tss := putN(t, s, "C1", 2)
	if err := s.DeleteMessage(ctx, "C1", tss[0]); err != nil {
		t.Fatal(err)
	}
	rows, _, _ := s.Query(ctx, "C1", "", "", "", 5)
	if len(rows) != 1 || rows[0].TS != tss[1] {
		t.Fatalf("rows = %v", tsList(rows))
	}
}

func TestDDBHistoryStore_MetaRoundTripAndList(t *testing.T) {
	s, _ := testStore()
	ctx := context.Background()
	if m, err := s.GetMeta(ctx, "C9"); m != nil || err != nil {
		t.Fatalf("absent meta = %v, %v", m, err)
	}
	in := ChannelMeta{
		Channel: "C1", Name: "sec-alerts",
		Route: RouteEntry{Channel: "C1", Name: "sec-alerts", Pattern: "sec-*", Alias: "ir", Profile: "ir-bot",
			Allow: []string{"a@x.com"}, History: &RouteHistory{Query: true, Mirror: true, RetentionSeconds: 604800}},
		MatchedAt: "2026-10-24T10:00:00Z", BackfillOldest: "1729000000.000000", BackfillCursor: "1729500000.000000",
		BackfillState: "running", BackfillWritten: 42, Announced: true, Overlap: "ops-*",
	}
	if err := s.PutMeta(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := s.PutMeta(ctx, ChannelMeta{Channel: "C2", Name: "two"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMeta(ctx, "C1")
	if err != nil || got == nil {
		t.Fatalf("GetMeta = %v, %v", got, err)
	}
	if got.Name != in.Name || got.MatchedAt != in.MatchedAt || got.BackfillOldest != in.BackfillOldest ||
		got.BackfillCursor != in.BackfillCursor || got.BackfillState != "running" || got.BackfillWritten != 42 ||
		!got.Announced || got.Overlap != "ops-*" {
		t.Fatalf("scalars lost: %+v", got)
	}
	if got.Route.Alias != "ir" || got.Route.Pattern != "sec-*" || got.Route.History == nil ||
		!got.Route.History.Mirror || got.Route.History.RetentionSeconds != 604800 || len(got.Route.Allow) != 1 {
		t.Fatalf("route lost: %+v", got.Route)
	}
	all, err := s.ListMeta(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListMeta = %d, %v", len(all), err)
	}
	// Meta rows never leak into a channel query, nor message rows into meta.
	putN(t, s, "C1", 2)
	rows, _, _ := s.Query(ctx, "C1", "", "", "", 10)
	if len(rows) != 2 {
		t.Fatalf("channel query returned %d rows", len(rows))
	}
	if rows, _, _ := s.Query(ctx, historyMetaPK, "", "", "", 10); len(rows) != 0 {
		t.Fatalf("meta partition leaked %d rows into Query", len(rows))
	}
	if all, _ := s.ListMeta(ctx); len(all) != 2 {
		t.Fatalf("messages leaked into ListMeta: %d", len(all))
	}
}

func TestDDBHistoryStore_ListMetaPaginates(t *testing.T) {
	s, f := testStore()
	f.maxPage = 2
	for i := 0; i < 5; i++ {
		if err := s.PutMeta(context.Background(), ChannelMeta{Channel: fmt.Sprintf("C%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.ListMeta(context.Background())
	if err != nil || len(all) != 5 {
		t.Fatalf("ListMeta = %d, %v", len(all), err)
	}
}

func TestDDBHistoryStore_PurgeChannelDeletesRowsAndMeta(t *testing.T) {
	s, f := testStore()
	ctx := context.Background()
	f.maxPage = 40 // force paging
	putN(t, s, "C1", 60)
	putN(t, s, "C2", 3)
	if err := s.PutMeta(ctx, ChannelMeta{Channel: "C1", Name: "one"}); err != nil {
		t.Fatal(err)
	}
	n, err := s.PurgeChannel(ctx, "C1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 60 {
		t.Fatalf("deleted = %d, want 60", n)
	}
	for _, sz := range f.batchSizes {
		if sz > 25 {
			t.Fatalf("batch of %d exceeds 25", sz)
		}
	}
	total := 0
	for _, sz := range f.batchSizes {
		total += sz
	}
	if total != 60 {
		t.Fatalf("batched %d deletes, want 60 (sizes %v)", total, f.batchSizes)
	}
	if m, _ := s.GetMeta(ctx, "C1"); m != nil {
		t.Fatal("meta row survived purge")
	}
	if rows, _, _ := s.Query(ctx, "C1", "", "", "", 100); len(rows) != 0 {
		t.Fatalf("%d rows survived", len(rows))
	}
	if rows, _, _ := s.Query(ctx, "C2", "", "", "", 100); len(rows) != 3 {
		t.Fatalf("other channel disturbed: %d", len(rows))
	}
	if pe := f.queries[0].ProjectionExpression; pe == nil || *pe != "channel, ts" {
		t.Fatalf("purge projection = %v", pe)
	}
}

func TestDDBHistoryStore_RowShape(t *testing.T) {
	s, f := testStore()
	err := s.PutMessage(context.Background(), HistoryMessageRow{
		Channel: "C1", TS: "1730000001.000100", Text: "hello", ThreadTS: "1730000000.000100",
		Files: []string{"a.png", "b.pdf"}, ExpiresAt: 1730000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	item := f.puts[0].Item
	if item["channel"].(*types.AttributeValueMemberS).Value != "C1" || item["ts"].(*types.AttributeValueMemberS).Value != "1730000001.000100" {
		t.Fatalf("keys: %v", item)
	}
	if item["expires_at"].(*types.AttributeValueMemberN).Value != "1730000000" {
		t.Fatalf("expires_at = %v", item["expires_at"])
	}
	files := item["files"].(*types.AttributeValueMemberL).Value
	if len(files) != 2 || files[0].(*types.AttributeValueMemberS).Value != "a.png" {
		t.Fatalf("files = %v", files)
	}
	for _, k := range []string{"user", "bot_id", "subtype", "edited_ts"} {
		if _, ok := item[k]; ok {
			t.Fatalf("empty attr %q was written", k)
		}
	}
	if item["thread_ts"].(*types.AttributeValueMemberS).Value != "1730000000.000100" {
		t.Fatal("thread_ts missing")
	}
	// Round trip keeps names, no bytes.
	rows, _, _ := s.Query(context.Background(), "C1", "", "", "", 5)
	if len(rows[0].Files) != 2 || rows[0].ExpiresAt != 1730000000 {
		t.Fatalf("round trip = %+v", rows[0])
	}
	// No-files row has no files attribute.
	_ = s.PutMessage(context.Background(), HistoryMessageRow{Channel: "C1", TS: "1730000002.000100", Text: "x", ExpiresAt: 1})
	if _, ok := f.puts[1].Item["files"]; ok {
		t.Fatal("files written when empty")
	}
}

func TestDDBHistoryStore_PurgeRetriesUnprocessedThenDrains(t *testing.T) {
	s, f := testStore()
	s.RetryBase = time.Millisecond
	putN(t, s, "C1", 10)
	f.unprocessed = func(call int, reqs []types.WriteRequest) []types.WriteRequest {
		if call == 1 {
			return reqs[:3] // throttle three deletes on the first call only
		}
		return nil
	}
	n, err := s.PurgeChannel(context.Background(), "C1")
	if err != nil || n != 10 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if f.batchCalls != 2 {
		t.Fatalf("batch calls = %d, want 2", f.batchCalls)
	}
	if rows, _, _ := s.Query(context.Background(), "C1", "", "", "", 50); len(rows) != 0 {
		t.Fatalf("%d rows survived", len(rows))
	}
}

func TestDDBHistoryStore_PurgeUnprocessedNeverDrainsIsBounded(t *testing.T) {
	s, f := testStore()
	s.RetryBase = time.Microsecond
	putN(t, s, "C1", 4)
	f.unprocessed = func(_ int, reqs []types.WriteRequest) []types.WriteRequest { return reqs[:2] }
	_, err := s.PurgeChannel(context.Background(), "C1")
	if err == nil || !strings.Contains(err.Error(), "2 deletes still unprocessed") {
		t.Fatalf("err = %v", err)
	}
	if f.batchCalls != 1+purgeMaxAttempts {
		t.Fatalf("batch calls = %d, want %d", f.batchCalls, 1+purgeMaxAttempts)
	}
}

func TestDDBHistoryStore_PurgeRefusesMetaPartition(t *testing.T) {
	s, _ := testStore()
	ctx := context.Background()
	_ = s.PutMeta(ctx, ChannelMeta{Channel: "C1"})
	if _, err := s.PurgeChannel(ctx, historyMetaPK); err == nil {
		t.Fatal("expected refusal")
	}
	if m, _ := s.GetMeta(ctx, "C1"); m == nil {
		t.Fatal("meta rows were deleted")
	}
}
