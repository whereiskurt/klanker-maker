package bridge_test

// action_limits_fetcher_test.go — Phase 121 follow-up (bridge main.go wiring).
// DDBActionLimitsFetcher resolves the per-sandbox action_limits JSON from the
// km-sandboxes row via GetItem keyed by sandbox_id. It is the production
// ActionLimitsFetcher the Slack bridge Handler.Limits field is wired to.

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

func TestDDBActionLimitsFetcher_ReturnsLimitsJSON(t *testing.T) {
	const wantJSON = `{"slack_post":{"perHour":10,"onBreach":"block"}}`
	var gotKey, gotTable string

	mock := &mockDynamoGetPut{
		getItem: func(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			gotTable = *in.TableName
			if k, ok := in.Key["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS); ok {
				gotKey = k.Value
			}
			return &dynamodb.GetItemOutput{Item: map[string]dynamodbtypes.AttributeValue{
				"sandbox_id":    &dynamodbtypes.AttributeValueMemberS{Value: "sb-123"},
				"action_limits": &dynamodbtypes.AttributeValueMemberS{Value: wantJSON},
			}}, nil
		},
	}
	f := &bridge.DDBActionLimitsFetcher{Client: mock, TableName: "km-sandboxes"}

	got, err := f.FetchLimits(context.Background(), "sb-123")
	if err != nil {
		t.Fatalf("FetchLimits error: %v", err)
	}
	if got != wantJSON {
		t.Errorf("FetchLimits: got %q, want %q", got, wantJSON)
	}
	if gotTable != "km-sandboxes" {
		t.Errorf("GetItem table: got %q, want km-sandboxes", gotTable)
	}
	if gotKey != "sb-123" {
		t.Errorf("GetItem key sandbox_id: got %q, want sb-123", gotKey)
	}
}

func TestDDBActionLimitsFetcher_AbsentRowIsDormant(t *testing.T) {
	mock := &mockDynamoGetPut{
		getItem: func(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{}, nil // no Item → unknown sandbox
		},
	}
	f := &bridge.DDBActionLimitsFetcher{Client: mock, TableName: "km-sandboxes"}

	got, err := f.FetchLimits(context.Background(), "sb-unknown")
	if err != nil {
		t.Fatalf("FetchLimits should not error on absent row: %v", err)
	}
	if got != "" {
		t.Errorf("FetchLimits absent row: got %q, want empty (dormant)", got)
	}
}

// Compile-time check: DDBActionLimitsFetcher satisfies the Handler.Limits interface.
var _ bridge.ActionLimitsFetcher = (*bridge.DDBActionLimitsFetcher)(nil)
