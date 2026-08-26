// aws_adapters_quota_test.go — Task 9A (Phase 121 follow-up): unit tests for
// the two adapters backing the action-quota gate: DDBActionLimitsFetcher
// (reads action_limits off the sandbox row) and DynamoFreezer (latches
// action_frozen). Mirrors pkg/h1/bridge/action_limits_fetcher_test.go.
package bridge_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/whereiskurt/klanker-maker/pkg/webhook/bridge"
)

// ============================================================
// DDBActionLimitsFetcher
// ============================================================

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

func TestDDBActionLimitsFetcher_ReturnsLimitsJSON(t *testing.T) {
	const wantJSON = `{"webhook_dispatch":{"perDay":5,"onBreach":"freeze"}}`
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

func TestDDBActionLimitsFetcher_AbsentRowIsDormant(t *testing.T) {
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

var _ bridge.ActionLimitsFetcher = (*bridge.DDBActionLimitsFetcher)(nil)

// ============================================================
// DynamoFreezer
// ============================================================

type fakeFreezerUpdateClient struct {
	updateInput *dynamodb.UpdateItemInput
}

func (f *fakeFreezerUpdateClient) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updateInput = in
	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *fakeFreezerUpdateClient) DeleteItem(_ context.Context, _ *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}

func TestDynamoFreezer_FreezeSandbox_LatchesActionFrozen(t *testing.T) {
	fake := &fakeFreezerUpdateClient{}
	fz := &bridge.DynamoFreezer{Client: fake, Table: "km-sandboxes"}

	if err := fz.FreezeSandbox(context.Background(), "sb-1", "quota exceeded", "auto:webhook_dispatch:hour"); err != nil {
		t.Fatalf("FreezeSandbox error: %v", err)
	}

	if fake.updateInput == nil {
		t.Fatal("expected an UpdateItem call")
	}
	if *fake.updateInput.TableName != "km-sandboxes" {
		t.Errorf("table: got %q, want km-sandboxes", *fake.updateInput.TableName)
	}
	key, ok := fake.updateInput.Key["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok || key.Value != "sb-1" {
		t.Errorf("key sandbox_id: got %+v, want sb-1", fake.updateInput.Key["sandbox_id"])
	}
	reason, ok := fake.updateInput.ExpressionAttributeValues[":reason"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok || reason.Value != "quota exceeded" {
		t.Errorf(":reason: got %+v, want %q", fake.updateInput.ExpressionAttributeValues[":reason"], "quota exceeded")
	}
	by, ok := fake.updateInput.ExpressionAttributeValues[":by"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok || by.Value != "auto:webhook_dispatch:hour" {
		t.Errorf(":by: got %+v, want %q", fake.updateInput.ExpressionAttributeValues[":by"], "auto:webhook_dispatch:hour")
	}
}

var _ bridge.Freezer = (*bridge.DynamoFreezer)(nil)
