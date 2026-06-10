// Package aws — slack_channels_test.go
// Tests for SlackChannelStore (Phase 104.3).
package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeChannelDDB is a map-backed fake that satisfies SlackChannelGetPutAPI.
// Key: "{alias}" → channel_id AttributeValueMemberS.
type fakeChannelDDB struct {
	items map[string]map[string]dynamodbtypes.AttributeValue
}

func newFakeChannelDDB() *fakeChannelDDB {
	return &fakeChannelDDB{items: make(map[string]map[string]dynamodbtypes.AttributeValue)}
}

func (f *fakeChannelDDB) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	aliasAttr, ok := input.Key["alias"]
	if !ok {
		return &dynamodb.GetItemOutput{}, nil
	}
	aliasVal, ok := aliasAttr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		return &dynamodb.GetItemOutput{}, nil
	}
	item, found := f.items[aliasVal.Value]
	if !found {
		return &dynamodb.GetItemOutput{}, nil
	}
	return &dynamodb.GetItemOutput{Item: item}, nil
}

func (f *fakeChannelDDB) PutItem(_ context.Context, input *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	aliasAttr, ok := input.Item["alias"]
	if !ok {
		return &dynamodb.PutItemOutput{}, nil
	}
	aliasVal, ok := aliasAttr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		return &dynamodb.PutItemOutput{}, nil
	}
	f.items[aliasVal.Value] = input.Item
	return &dynamodb.PutItemOutput{}, nil
}

// TestSlackChannelStore_UpsertThenGet verifies that a UpsertByAlias followed
// by GetByAlias on the same alias returns the written channel ID.
func TestSlackChannelStore_UpsertThenGet(t *testing.T) {
	fake := newFakeChannelDDB()
	store := &SlackChannelStore{Client: fake, TableName: "km-slack-channels"}

	ctx := context.Background()
	const alias = "github-bot"
	const channelID = "C0X"

	if err := store.UpsertByAlias(ctx, alias, channelID); err != nil {
		t.Fatalf("UpsertByAlias: unexpected error: %v", err)
	}

	got, err := store.GetByAlias(ctx, alias)
	if err != nil {
		t.Fatalf("GetByAlias: unexpected error: %v", err)
	}
	if got != channelID {
		t.Errorf("GetByAlias: got %q, want %q", got, channelID)
	}
}

// TestSlackChannelStore_GetMiss verifies that GetByAlias returns ("", nil) for
// an alias that was never upserted.
func TestSlackChannelStore_GetMiss(t *testing.T) {
	fake := newFakeChannelDDB()
	store := &SlackChannelStore{Client: fake, TableName: "km-slack-channels"}

	got, err := store.GetByAlias(context.Background(), "absent")
	if err != nil {
		t.Fatalf("GetByAlias on miss: unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("GetByAlias on miss: got %q, want empty string", got)
	}
}
