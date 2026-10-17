package bridge_test

import (
	"context"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

func TestDDBThreadStore_DispatchBoundary(t *testing.T) {
	m := &mockDDBUpdateItem{}
	m.getItem = func(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
		if awssdk.ToString(in.ProjectionExpression) != "last_dispatch_ts" {
			t.Errorf("projection %q", awssdk.ToString(in.ProjectionExpression))
		}
		return &dynamodb.GetItemOutput{Item: map[string]dynamodbtypes.AttributeValue{
			"last_dispatch_ts": &dynamodbtypes.AttributeValueMemberS{Value: "1718457000.000100"},
		}}, nil
	}
	s := &bridge.DDBThreadStore{Client: m, TableName: "km-slack-threads"}
	got, err := s.DispatchBoundary(context.Background(), "C1", "1.0")
	if err != nil || got != "1718457000.000100" {
		t.Fatalf("got %q err %v", got, err)
	}
	m.getItem = func(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
		return &dynamodb.GetItemOutput{}, nil
	}
	if got, err := s.DispatchBoundary(context.Background(), "C1", "1.0"); err != nil || got != "" {
		t.Fatalf("no row: got %q err %v", got, err)
	}
}

func TestDDBThreadStore_MarkDispatched_ConditionalAndMonotonic(t *testing.T) {
	m := &mockDDBUpdateItem{}
	var seen *dynamodb.UpdateItemInput
	m.updateItem = func(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
		seen = in
		return &dynamodb.UpdateItemOutput{}, nil
	}
	s := &bridge.DDBThreadStore{Client: m, TableName: "km-slack-threads"}
	if err := s.MarkDispatched(context.Background(), "C1", "1.0", "1718457900.000200"); err != nil {
		t.Fatal(err)
	}
	cond := awssdk.ToString(seen.ConditionExpression)
	if cond != "attribute_not_exists(last_dispatch_ts) OR last_dispatch_ts < :ts" {
		t.Fatalf("condition %q", cond)
	}
	if v := seen.ExpressionAttributeValues[":ts"].(*dynamodbtypes.AttributeValueMemberS).Value; v != "1718457900.000200" {
		t.Fatalf(":ts %q", v)
	}
	// An older ts loses the conditional check: swallowed, not an error.
	m.updateItem = func(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
		return nil, &dynamodbtypes.ConditionalCheckFailedException{}
	}
	if err := s.MarkDispatched(context.Background(), "C1", "1.0", "1718457000.000100"); err != nil {
		t.Fatalf("conditional failure must be a no-op, got %v", err)
	}
	m.updateItem = func(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
		return nil, errors.New("throttled")
	}
	if err := s.MarkDispatched(context.Background(), "C1", "1.0", "1718457900.000200"); err == nil {
		t.Fatal("other errors must surface")
	}
}
