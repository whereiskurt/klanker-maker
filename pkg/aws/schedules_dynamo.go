// Package aws — schedules_dynamo.go
// DynamoDB CRUD layer for km-at schedule metadata.
//
// Table key design:
//   schedule_name (S) — hash key (no sort key)
//   All other fields stored as strings or booleans using explicit attribute types.
//
// Convention: follows sandbox_dynamo.go patterns exactly — explicit
// AttributeValueMemberS/BOOL marshalling, no attributevalue.MarshalMap.
package aws

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ScheduleRecord is the public representation of a km-at schedule entry.
// Stored in the km-schedules DynamoDB table.
type ScheduleRecord struct {
	// ScheduleName is the hash key — uniquely identifies the EventBridge schedule.
	// Typically formatted as "km-at-{sandboxID}-{command}".
	ScheduleName string

	// Command is the deferred action (e.g. "kill", "stop", "create").
	Command string

	// SandboxID is the target sandbox. Empty for "create" commands.
	SandboxID string

	// TimeExpr is the original human-readable time expression (e.g. "tomorrow at 9am").
	TimeExpr string

	// CronExpr is the resolved EventBridge expression (e.g. "at(2026-04-10T09:00:00)").
	CronExpr string

	// IsRecurring indicates whether this is a recurring (cron) vs one-time (at) schedule.
	IsRecurring bool

	// Status tracks the lifecycle of the schedule: "active", "completed", "cancelled".
	Status string

	// CreatedAt is the time the schedule was created.
	CreatedAt time.Time
}

// PutSchedule stores a ScheduleRecord in DynamoDB.
// Uses explicit attribute marshalling per project convention — no MarshalMap.
// sandbox_id is omitted when empty (e.g. for "create" commands).
func PutSchedule(ctx context.Context, client SandboxMetadataAPI, tableName string, rec ScheduleRecord) error {
	item := map[string]dynamodbtypes.AttributeValue{
		"schedule_name": &dynamodbtypes.AttributeValueMemberS{Value: rec.ScheduleName},
		"command":       &dynamodbtypes.AttributeValueMemberS{Value: rec.Command},
		"time_expr":     &dynamodbtypes.AttributeValueMemberS{Value: rec.TimeExpr},
		"cron_expr":     &dynamodbtypes.AttributeValueMemberS{Value: rec.CronExpr},
		"is_recurring":  &dynamodbtypes.AttributeValueMemberBOOL{Value: rec.IsRecurring},
		"status":        &dynamodbtypes.AttributeValueMemberS{Value: rec.Status},
		"created_at":    &dynamodbtypes.AttributeValueMemberS{Value: rec.CreatedAt.UTC().Format(time.RFC3339)},
	}

	// Omit sandbox_id entirely when empty (create command has no target sandbox).
	if rec.SandboxID != "" {
		item["sandbox_id"] = &dynamodbtypes.AttributeValueMemberS{Value: rec.SandboxID}
	}

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &tableName,
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("PutSchedule %q: %w", rec.ScheduleName, err)
	}
	return nil
}

// GetScheduleRecord retrieves a ScheduleRecord by schedule_name.
// Returns (nil, nil) when the item is not found — callers should check for nil.
func GetScheduleRecord(ctx context.Context, client SandboxMetadataAPI, tableName, scheduleName string) (*ScheduleRecord, error) {
	out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &tableName,
		Key: map[string]dynamodbtypes.AttributeValue{
			"schedule_name": &dynamodbtypes.AttributeValueMemberS{Value: scheduleName},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("GetScheduleRecord %q: %w", scheduleName, err)
	}
	if len(out.Item) == 0 {
		// Item not found — return nil, nil (not an error).
		return nil, nil
	}
	return unmarshalScheduleItem(out.Item)
}

// ListScheduleRecords scans the schedules table and returns all records
// sorted by CreatedAt descending (newest first).
func ListScheduleRecords(ctx context.Context, client SandboxMetadataAPI, tableName string) ([]ScheduleRecord, error) {
	var records []ScheduleRecord

	var lastKey map[string]dynamodbtypes.AttributeValue
	for {
		input := &dynamodb.ScanInput{
			TableName: &tableName,
		}
		if lastKey != nil {
			input.ExclusiveStartKey = lastKey
		}

		out, err := client.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("ListScheduleRecords scan: %w", err)
		}

		for _, rawItem := range out.Items {
			rec, err := unmarshalScheduleItem(rawItem)
			if err != nil {
				// Skip malformed items rather than aborting the entire list.
				continue
			}
			records = append(records, *rec)
		}

		if out.LastEvaluatedKey == nil {
			break
		}
		lastKey = out.LastEvaluatedKey
	}

	// Sort descending by CreatedAt (newest first).
	sort.Slice(records, func(i, j int) bool {
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})

	return records, nil
}

// DeleteScheduleRecord deletes a schedule record by schedule_name.
// Idempotent — no error is returned if the item does not exist.
func DeleteScheduleRecord(ctx context.Context, client SandboxMetadataAPI, tableName, scheduleName string) error {
	_, err := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &tableName,
		Key: map[string]dynamodbtypes.AttributeValue{
			"schedule_name": &dynamodbtypes.AttributeValueMemberS{Value: scheduleName},
		},
	})
	if err != nil {
		return fmt.Errorf("DeleteScheduleRecord %q: %w", scheduleName, err)
	}
	return nil
}

// unmarshalScheduleItem converts a raw DynamoDB item map to a ScheduleRecord.
// Uses explicit type assertions for each attribute — no json tag fallback.
func unmarshalScheduleItem(item map[string]dynamodbtypes.AttributeValue) (*ScheduleRecord, error) {
	rec := &ScheduleRecord{}

	if v, ok := item["schedule_name"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			rec.ScheduleName = sv.Value
		}
	}
	if v, ok := item["command"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			rec.Command = sv.Value
		}
	}
	if v, ok := item["sandbox_id"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			rec.SandboxID = sv.Value
		}
	}
	if v, ok := item["time_expr"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			rec.TimeExpr = sv.Value
		}
	}
	if v, ok := item["cron_expr"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			rec.CronExpr = sv.Value
		}
	}
	if v, ok := item["is_recurring"]; ok {
		if bv, ok := v.(*dynamodbtypes.AttributeValueMemberBOOL); ok {
			rec.IsRecurring = bv.Value
		}
	}
	if v, ok := item["status"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			rec.Status = sv.Value
		}
	}
	if v, ok := item["created_at"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			t, err := time.Parse(time.RFC3339, sv.Value)
			if err != nil {
				return nil, fmt.Errorf("unmarshalScheduleItem: parse created_at %q: %w", sv.Value, err)
			}
			rec.CreatedAt = t
		}
	}

	return rec, nil
}
