// Package aws — slack_channels.go
// SlackChannelStore: durable alias→channel_id mapping backed by the
// km-slack-channels DynamoDB table (Phase 104.3).
//
// Design decisions:
//   - No ConditionExpression — upsert always overwrites (write-through, not
//     attribute_not_exists guard). This differs from DDBThreadStore.Upsert
//     which guards with attribute_not_exists(channel_id); channel IDs are
//     stable and the latest is always correct.
//   - No TTL attribute — channel mappings should survive across destroy/recreate
//     cycles; expiring them would defeat the purpose of the durable store.
//   - GetItem miss returns ("", nil) — callers treat empty string as a cache miss
//     and fall through to the SSM by-name cache or Slack API.
//   - Only GetItem + PutItem are required; the interface does not include
//     UpdateItem, DeleteItem, Query, or Scan.
package aws

import (
	"context"
	"fmt"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// SlackChannelGetPutAPI is the minimal DynamoDB interface required by
// SlackChannelStore. Using a narrow interface (GetItem + PutItem only) keeps
// the mock surface small and avoids coupling to the full DynamoDB client.
type SlackChannelGetPutAPI interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
}

// SlackChannelStore implements the cmd.SlackChannelStore interface (defined in
// internal/app/cmd/create_slack.go) using the km-slack-channels DynamoDB table.
//
// Table schema (GSI-free, alias is the sole partition key):
//
//	alias      (S) — partition key, e.g. "github-bot"
//	channel_id (S) — Slack channel ID, e.g. "C0123ABCDEF"
//	updated_at (S) — RFC3339 timestamp of last write
type SlackChannelStore struct {
	Client    SlackChannelGetPutAPI
	TableName string // e.g. "km-slack-channels"
}

// GetByAlias returns the Slack channel ID for the given alias.
// Returns ("", nil) on a cache miss — callers should treat the empty string
// as a miss and fall back to SSM or the Slack API.
func (s *SlackChannelStore) GetByAlias(ctx context.Context, alias string) (string, error) {
	out, err := s.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(s.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"alias": &dynamodbtypes.AttributeValueMemberS{Value: alias},
		},
	})
	if err != nil {
		return "", fmt.Errorf("slack channels GetItem (alias=%s): %w", alias, err)
	}
	if out.Item == nil {
		return "", nil
	}
	v, ok := out.Item["channel_id"]
	if !ok {
		return "", nil
	}
	sv, ok := v.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		return "", nil
	}
	return sv.Value, nil
}

// UpsertByAlias writes the alias→channelID mapping to DynamoDB, overwriting
// any previous value. No ConditionExpression — always overwrites (the latest
// channel ID for an alias is always correct). Sets updated_at to now (RFC3339).
func (s *SlackChannelStore) UpsertByAlias(ctx context.Context, alias, channelID string) error {
	_, err := s.Client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: awssdk.String(s.TableName),
		Item: map[string]dynamodbtypes.AttributeValue{
			"alias":      &dynamodbtypes.AttributeValueMemberS{Value: alias},
			"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: channelID},
			"updated_at": &dynamodbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
		},
	})
	if err != nil {
		return fmt.Errorf("slack channels PutItem (alias=%s, channel=%s): %w", alias, channelID, err)
	}
	return nil
}
