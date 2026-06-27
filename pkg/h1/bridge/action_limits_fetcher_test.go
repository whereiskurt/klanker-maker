package bridge_test

// action_limits_fetcher_test.go — Phase 121 follow-up (H1 bridge main.go wiring).
// DDBActionLimitsFetcher resolves the per-sandbox action_limits JSON from the
// km-sandboxes row via GetItem keyed by sandbox_id (production WebhookHandler.Limits).

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

type fakeLimitsGetItem struct {
	item map[string]dynamodbtypes.AttributeValue
	key  string
	tbl  string
}

func (f *fakeLimitsGetItem) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.tbl = *in.TableName
	if k, ok := in.Key["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS); ok {
		f.key = k.Value
	}
	return &dynamodb.GetItemOutput{Item: f.item}, nil
}

func TestH1DDBActionLimitsFetcher_ReturnsLimitsJSON(t *testing.T) {
	const wantJSON = `{"h1_comment":{"perDay":5,"onBreach":"freeze"}}`
	fake := &fakeLimitsGetItem{item: map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":    &dynamodbtypes.AttributeValueMemberS{Value: "sb-9"},
		"action_limits": &dynamodbtypes.AttributeValueMemberS{Value: wantJSON},
	}}
	f := &bridge.DDBActionLimitsFetcher{Client: fake, TableName: "km-sandboxes"}

	got, err := f.FetchLimits(context.Background(), "sb-9")
	if err != nil {
		t.Fatalf("FetchLimits error: %v", err)
	}
	if got != wantJSON {
		t.Errorf("FetchLimits: got %q, want %q", got, wantJSON)
	}
	if fake.tbl != "km-sandboxes" || fake.key != "sb-9" {
		t.Errorf("GetItem table/key: got %q/%q, want km-sandboxes/sb-9", fake.tbl, fake.key)
	}
}

func TestH1DDBActionLimitsFetcher_AbsentRowIsDormant(t *testing.T) {
	fake := &fakeLimitsGetItem{item: nil}
	f := &bridge.DDBActionLimitsFetcher{Client: fake, TableName: "km-sandboxes"}

	got, err := f.FetchLimits(context.Background(), "sb-unknown")
	if err != nil {
		t.Fatalf("FetchLimits should not error on absent row: %v", err)
	}
	if got != "" {
		t.Errorf("FetchLimits absent row: got %q, want empty", got)
	}
}

var _ bridge.H1ActionLimitsFetcher = (*bridge.DDBActionLimitsFetcher)(nil)
