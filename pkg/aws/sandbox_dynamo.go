// Package aws — sandbox_dynamo.go
// DynamoDB CRUD layer for sandbox metadata.
//
// This file provides the data access layer for the km-sandbox-metadata DynamoDB table.
// All CLI commands and Lambdas call these functions after the DynamoDB switchover.
// S3 artifacts (Terraform state, etc.) remain in S3 — only the metadata JSON record
// moves to DynamoDB for O(1) reads, atomic locking, and GSI alias resolution.
//
// Table key design:
//   sandbox_id (S) — hash key (no sort key)
//   alias-index GSI: alias (S) → sandbox_id, for O(1) alias resolution
//   TTL attribute: ttl_expiry (N, Unix epoch seconds) — native DynamoDB TTL
package aws

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// SandboxMetadataAPI is the narrow DynamoDB interface for sandbox metadata operations.
// Implemented by *dynamodb.Client.
type SandboxMetadataAPI interface {
	GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, input *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(ctx context.Context, input *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(ctx context.Context, input *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Scan(ctx context.Context, input *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	Query(ctx context.Context, input *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// ============================================================
// Internal DynamoDB item representation
// ============================================================

// sandboxItemDynamo is the internal struct for marshalling/unmarshalling DynamoDB items.
// Uses explicit dynamodbav tags — does NOT rely on json tag fallback (research Pitfall 1).
// TTLExpiry is stored as an int64 epoch (Number type) for native DynamoDB TTL support.
type sandboxItemDynamo struct {
	SandboxID    string `dynamodbav:"sandbox_id"`
	ProfileName  string `dynamodbav:"profile_name"`
	Substrate    string `dynamodbav:"substrate"`
	Region       string `dynamodbav:"region"`
	Status       string `dynamodbav:"status,omitempty"`
	CreatedAt    string `dynamodbav:"created_at"`
	IdleTimeout  string `dynamodbav:"idle_timeout,omitempty"`
	MaxLifetime  string `dynamodbav:"max_lifetime,omitempty"`
	CreatedBy    string `dynamodbav:"created_by,omitempty"`
	Alias        string `dynamodbav:"alias,omitempty"`
	ClonedFrom   string `dynamodbav:"cloned_from,omitempty"`
	Locked         bool   `dynamodbav:"locked,omitempty"`
	LockedAt       string `dynamodbav:"locked_at,omitempty"`
	TeardownPolicy string `dynamodbav:"teardown_policy,omitempty"`
	ExpiresAt      string `dynamodbav:"expires_at,omitempty"` // RFC3339 display-only expiry, always set when TTL configured
	// TTLExpiryEpoch is int64 so attributevalue.Marshal gives a Number.
	// However we override this with a manual AttributeValueMemberN in WriteSandboxMetadataDynamo
	// to guarantee Number type (research Pitfall: zero int64 marshals as N "0", so we manage TTL manually).
	TTLExpiryEpoch int64 `dynamodbav:"ttl_expiry,omitempty"`
}

// toSandboxMetadata converts an internal DynamoDB item to the public SandboxMetadata type.
func (d *sandboxItemDynamo) toSandboxMetadata() (*SandboxMetadata, error) {
	createdAt, err := time.Parse(time.RFC3339, d.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at %q: %w", d.CreatedAt, err)
	}

	meta := &SandboxMetadata{
		SandboxID:   d.SandboxID,
		ProfileName: d.ProfileName,
		Substrate:   d.Substrate,
		Region:      d.Region,
		Status:      d.Status,
		CreatedAt:   createdAt,
		IdleTimeout: d.IdleTimeout,
		MaxLifetime: d.MaxLifetime,
		CreatedBy:   d.CreatedBy,
		Alias:          d.Alias,
		ClonedFrom:     d.ClonedFrom,
		Locked:         d.Locked,
		TeardownPolicy: d.TeardownPolicy,
	}

	if d.TTLExpiryEpoch != 0 {
		t := time.Unix(d.TTLExpiryEpoch, 0).UTC()
		meta.TTLExpiry = &t
	}

	if d.ExpiresAt != "" {
		if ea, err := time.Parse(time.RFC3339, d.ExpiresAt); err == nil {
			meta.ExpiresAt = &ea
			// Backfill TTLExpiry from ExpiresAt when ttl_expiry was omitted
			// (teardownPolicy=stop/retain skips the DynamoDB TTL attribute).
			if meta.TTLExpiry == nil {
				meta.TTLExpiry = meta.ExpiresAt
			}
		}
	}

	if d.LockedAt != "" {
		lockedAt, err := time.Parse(time.RFC3339, d.LockedAt)
		if err == nil {
			meta.LockedAt = &lockedAt
		}
	}

	return meta, nil
}

// metadataToRecord converts a SandboxMetadata to a SandboxRecord.
// Mirrors the conversion logic in readMetadataRecord (sandbox.go).
func metadataToRecord(meta *SandboxMetadata) SandboxRecord {
	status := meta.Status
	if status == "" {
		status = "running" // backward compat: old metadata without status field
	}
	return SandboxRecord{
		SandboxID:      meta.SandboxID,
		Profile:        meta.ProfileName,
		Substrate:      meta.Substrate,
		Region:         meta.Region,
		Status:         status,
		CreatedAt:      meta.CreatedAt,
		TTLExpiry:      meta.TTLExpiry,
		TTLRemaining:   computeTTLRemaining(meta.TTLExpiry),
		IdleTimeout:    meta.IdleTimeout,
		Alias:          meta.Alias,
		ClonedFrom:     meta.ClonedFrom,
		Locked:         meta.Locked,
		TeardownPolicy: meta.TeardownPolicy,
	}
}

// unmarshalSandboxItem extracts a sandboxItemDynamo from a raw DynamoDB item map.
// We do this manually to handle the ttl_expiry Number attribute correctly.
func unmarshalSandboxItem(item map[string]dynamodbtypes.AttributeValue) (*sandboxItemDynamo, error) {
	d := &sandboxItemDynamo{}

	if v, ok := item["sandbox_id"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.SandboxID = sv.Value
		}
	}
	if v, ok := item["profile_name"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.ProfileName = sv.Value
		}
	}
	if v, ok := item["substrate"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.Substrate = sv.Value
		}
	}
	if v, ok := item["region"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.Region = sv.Value
		}
	}
	if v, ok := item["status"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.Status = sv.Value
		}
	}
	if v, ok := item["created_at"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.CreatedAt = sv.Value
		}
	}
	if v, ok := item["idle_timeout"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.IdleTimeout = sv.Value
		}
	}
	if v, ok := item["max_lifetime"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.MaxLifetime = sv.Value
		}
	}
	if v, ok := item["created_by"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.CreatedBy = sv.Value
		}
	}
	if v, ok := item["alias"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.Alias = sv.Value
		}
	}
	if v, ok := item["cloned_from"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.ClonedFrom = sv.Value
		}
	}
	if v, ok := item["locked"]; ok {
		if bv, ok := v.(*dynamodbtypes.AttributeValueMemberBOOL); ok {
			d.Locked = bv.Value
		}
	}
	if v, ok := item["locked_at"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.LockedAt = sv.Value
		}
	}
	if v, ok := item["teardown_policy"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.TeardownPolicy = sv.Value
		}
	}
	if v, ok := item["expires_at"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.ExpiresAt = sv.Value
		}
	}
	// ttl_expiry is stored as Number (epoch seconds)
	if v, ok := item["ttl_expiry"]; ok {
		if nv, ok := v.(*dynamodbtypes.AttributeValueMemberN); ok {
			epoch, err := strconv.ParseInt(nv.Value, 10, 64)
			if err == nil {
				d.TTLExpiryEpoch = epoch
			}
		}
	}

	return d, nil
}

// unmarshalSlackFields reads Phase 63 Slack fields from a raw DynamoDB item into SandboxMetadata.
// Called by ReadSandboxMetadataDynamo and ListAllSandboxesByDynamo after toSandboxMetadata().
func unmarshalSlackFields(item map[string]dynamodbtypes.AttributeValue, meta *SandboxMetadata) {
	if v, ok := item["slack_channel_id"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			meta.SlackChannelID = sv.Value
		}
	}
	if v, ok := item["slack_per_sandbox"]; ok {
		if bv, ok := v.(*dynamodbtypes.AttributeValueMemberBOOL); ok {
			meta.SlackPerSandbox = bv.Value
		}
	}
	if v, ok := item["slack_archive_on_destroy"]; ok {
		if bv, ok := v.(*dynamodbtypes.AttributeValueMemberBOOL); ok {
			val := bv.Value
			meta.SlackArchiveOnDestroy = &val
		}
	}
}

// marshalSandboxItem converts a SandboxMetadata to a raw DynamoDB item map.
// Manually builds the item to guarantee correct attribute types — in particular:
//   - ttl_expiry: AttributeValueMemberN (Number, Unix epoch) for DynamoDB TTL
//   - alias: omitted entirely when empty (prevents GSI pollution — research Pitfall 5)
func marshalSandboxItem(meta *SandboxMetadata) map[string]dynamodbtypes.AttributeValue {
	item := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":   &dynamodbtypes.AttributeValueMemberS{Value: meta.SandboxID},
		"profile_name": &dynamodbtypes.AttributeValueMemberS{Value: meta.ProfileName},
		"substrate":    &dynamodbtypes.AttributeValueMemberS{Value: meta.Substrate},
		"region":       &dynamodbtypes.AttributeValueMemberS{Value: meta.Region},
		"created_at":   &dynamodbtypes.AttributeValueMemberS{Value: meta.CreatedAt.UTC().Format(time.RFC3339)},
	}

	if meta.Status != "" {
		item["status"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.Status}
	}
	if meta.IdleTimeout != "" {
		item["idle_timeout"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.IdleTimeout}
	}
	if meta.MaxLifetime != "" {
		item["max_lifetime"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.MaxLifetime}
	}
	if meta.CreatedBy != "" {
		item["created_by"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.CreatedBy}
	}
	// alias: omit entirely when empty to prevent GSI index from storing empty-string projections
	if meta.Alias != "" {
		item["alias"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.Alias}
	}
	// cloned_from: omit when empty (no GSI, but keeps items clean — same pattern as alias)
	if meta.ClonedFrom != "" {
		item["cloned_from"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.ClonedFrom}
	}
	if meta.Locked {
		item["locked"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: true}
	}
	if meta.LockedAt != nil {
		item["locked_at"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.LockedAt.UTC().Format(time.RFC3339)}
	}
	if meta.TeardownPolicy != "" {
		item["teardown_policy"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.TeardownPolicy}
	}
	// expires_at: always store when TTL is configured (display-only, not used by DynamoDB native TTL).
	if meta.ExpiresAt != nil {
		item["expires_at"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.ExpiresAt.UTC().Format(time.RFC3339)}
	} else if meta.TTLExpiry != nil {
		// Backward compat: derive expires_at from TTLExpiry if not explicitly set.
		item["expires_at"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.TTLExpiry.UTC().Format(time.RFC3339)}
	}
	// ttl_expiry: store as Number (N) type for DynamoDB TTL — must NOT be a String.
	// Omit when teardownPolicy is "stop" or "retain" so DynamoDB native TTL never
	// auto-deletes the record — the EventBridge schedule handles lifecycle actions.
	if meta.TTLExpiry != nil && meta.TeardownPolicy != "stop" && meta.TeardownPolicy != "retain" {
		item["ttl_expiry"] = &dynamodbtypes.AttributeValueMemberN{
			Value: strconv.FormatInt(meta.TTLExpiry.Unix(), 10),
		}
	}

	// Phase 63 — Slack notification metadata.
	if meta.SlackChannelID != "" {
		item["slack_channel_id"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.SlackChannelID}
	}
	if meta.SlackPerSandbox {
		item["slack_per_sandbox"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: true}
	}
	// slack_archive_on_destroy: only store when explicitly set (nil = default, omit).
	// This preserves round-trip semantics: nil in → nil out, &bool in → &bool out.
	if meta.SlackArchiveOnDestroy != nil {
		item["slack_archive_on_destroy"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: *meta.SlackArchiveOnDestroy}
	}

	return item
}

// ============================================================
// Exported CRUD functions
// ============================================================

// ReadSandboxMetadataDynamo retrieves a sandbox metadata record from DynamoDB by sandbox_id.
// Returns ErrSandboxNotFound when the item does not exist (0 attributes in response).
func ReadSandboxMetadataDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID string) (*SandboxMetadata, error) {
	out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get sandbox metadata for %s: %w", sandboxID, err)
	}
	if len(out.Item) == 0 {
		return nil, fmt.Errorf("%w: no DynamoDB record for sandbox %s", ErrSandboxNotFound, sandboxID)
	}

	d, err := unmarshalSandboxItem(out.Item)
	if err != nil {
		return nil, fmt.Errorf("unmarshal sandbox metadata for %s: %w", sandboxID, err)
	}

	meta, err := d.toSandboxMetadata()
	if err != nil {
		return nil, fmt.Errorf("convert sandbox metadata for %s: %w", sandboxID, err)
	}

	unmarshalSlackFields(out.Item, meta)
	return meta, nil
}

// WriteSandboxMetadataDynamo stores or replaces a sandbox metadata record in DynamoDB.
// ttl_expiry is stored as Number (Unix epoch seconds) for native DynamoDB TTL.
// alias is omitted from the item when empty to prevent GSI pollution.
func WriteSandboxMetadataDynamo(ctx context.Context, client SandboxMetadataAPI, tableName string, meta *SandboxMetadata) error {
	item := marshalSandboxItem(meta)

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: awssdk.String(tableName),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("write sandbox metadata for %s: %w", meta.SandboxID, err)
	}
	return nil
}

// DeleteSandboxMetadataDynamo removes a sandbox metadata record from DynamoDB.
// Idempotent — DynamoDB DeleteItem is a no-op when the key does not exist.
func DeleteSandboxMetadataDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID string) error {
	_, err := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
	})
	if err != nil {
		return fmt.Errorf("delete sandbox metadata for %s: %w", sandboxID, err)
	}
	return nil
}

// ListAllSandboxesByDynamo scans the km-sandbox-metadata table and returns all sandbox records.
// Paginates using LastEvaluatedKey until all pages are consumed (research Pitfall 7).
func ListAllSandboxesByDynamo(ctx context.Context, client SandboxMetadataAPI, tableName string) ([]SandboxRecord, error) {
	var records []SandboxRecord
	var lastKey map[string]dynamodbtypes.AttributeValue

	for {
		input := &dynamodb.ScanInput{
			TableName: awssdk.String(tableName),
		}
		if len(lastKey) > 0 {
			input.ExclusiveStartKey = lastKey
		}

		out, err := client.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("scan sandbox metadata table %s: %w", tableName, err)
		}

		for _, item := range out.Items {
			d, err := unmarshalSandboxItem(item)
			if err != nil {
				// Skip malformed items rather than aborting the whole list
				continue
			}
			meta, err := d.toSandboxMetadata()
			if err != nil {
				continue
			}
			unmarshalSlackFields(item, meta)
			records = append(records, metadataToRecord(meta))
		}

		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		lastKey = out.LastEvaluatedKey
	}

	return records, nil
}

// ResolveSandboxAliasDynamo queries the alias-index GSI for O(1) alias resolution.
// Returns the sandbox_id of the matching sandbox, or an error if not found.
func ResolveSandboxAliasDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, alias string) (string, error) {
	out, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:              awssdk.String(tableName),
		IndexName:              awssdk.String("alias-index"),
		KeyConditionExpression: awssdk.String("alias = :alias"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":alias": &dynamodbtypes.AttributeValueMemberS{Value: alias},
		},
		Limit: awssdk.Int32(2), // fetch 2 to detect duplicates
	})
	if err != nil {
		return "", fmt.Errorf("resolve alias %q via GSI: %w", alias, err)
	}
	if len(out.Items) == 0 {
		return "", fmt.Errorf("alias %q not found: no active sandbox with that alias", alias)
	}
	if len(out.Items) > 1 {
		return "", fmt.Errorf("alias %q is ambiguous: matched multiple sandboxes", alias)
	}

	item := out.Items[0]
	sandboxIDAttr, ok := item["sandbox_id"]
	if !ok {
		return "", fmt.Errorf("alias %q: GSI item missing sandbox_id", alias)
	}
	sv, ok := sandboxIDAttr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		return "", fmt.Errorf("alias %q: sandbox_id is not a String attribute", alias)
	}
	return sv.Value, nil
}

// LockSandboxDynamo atomically locks a sandbox using a DynamoDB ConditionExpression.
// Uses a conditional UpdateItem — no read-modify-write race condition.
// Returns an "already locked" error if the sandbox is already locked (ConditionalCheckFailedException).
func LockSandboxDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		UpdateExpression: awssdk.String("SET locked = :t, locked_at = :now"),
		ConditionExpression: awssdk.String(
			"attribute_exists(sandbox_id) AND (attribute_not_exists(locked) OR locked = :f)",
		),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":t":   &dynamodbtypes.AttributeValueMemberBOOL{Value: true},
			":f":   &dynamodbtypes.AttributeValueMemberBOOL{Value: false},
			":now": &dynamodbtypes.AttributeValueMemberS{Value: now},
		},
	})
	if err != nil {
		var condFailed *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &condFailed) {
			return fmt.Errorf("sandbox %s is already locked", sandboxID)
		}
		return fmt.Errorf("lock sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// UnlockSandboxDynamo atomically unlocks a sandbox using a DynamoDB ConditionExpression.
// Returns an error if the sandbox is not locked (ConditionalCheckFailedException).
func UnlockSandboxDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID string) error {
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		UpdateExpression:    awssdk.String("SET locked = :f REMOVE locked_at"),
		ConditionExpression: awssdk.String("attribute_exists(sandbox_id) AND locked = :t"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":t": &dynamodbtypes.AttributeValueMemberBOOL{Value: true},
			":f": &dynamodbtypes.AttributeValueMemberBOOL{Value: false},
		},
	})
	if err != nil {
		var condFailed *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &condFailed) {
			return fmt.Errorf("sandbox %s is not locked", sandboxID)
		}
		return fmt.Errorf("unlock sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// UpdateSandboxStatusDynamo updates only the status field of a sandbox record.
// Used by pause/resume/stop/destroy for lightweight status transitions without a full PutItem.
func UpdateSandboxStatusDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID, status string) error {
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		UpdateExpression: awssdk.String("SET #s = :status"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":status": &dynamodbtypes.AttributeValueMemberS{Value: status},
		},
	})
	if err != nil {
		return fmt.Errorf("update status for sandbox %s to %q: %w", sandboxID, status, err)
	}
	return nil
}

// UpdateSandboxStatusAndClearTTL updates the status field AND removes the ttl_expiry
// attribute so DynamoDB's native TTL doesn't auto-delete the record. Used when
// teardownPolicy=stop to preserve the record for later resume or explicit destroy.
func UpdateSandboxStatusAndClearTTL(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID, status string) error {
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		UpdateExpression: awssdk.String("SET #s = :status REMOVE ttl_expiry"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":status": &dynamodbtypes.AttributeValueMemberS{Value: status},
		},
	})
	if err != nil {
		return fmt.Errorf("update status and clear TTL for sandbox %s: %w", sandboxID, err)
	}
	return nil
}
