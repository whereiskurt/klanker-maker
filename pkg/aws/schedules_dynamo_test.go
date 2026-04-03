package aws_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// Note: mockSandboxMetadataAPI is defined in sandbox_dynamo_test.go and shared
// across the aws_test package.

const testSchedulesTable = "km-schedules-test"

func makeTestScheduleRecord() kmaws.ScheduleRecord {
	return kmaws.ScheduleRecord{
		ScheduleName: "km-at-sb-001-kill",
		Command:      "kill",
		SandboxID:    "sb-001",
		TimeExpr:     "tomorrow at 9am",
		CronExpr:     "at(2026-04-04T09:00:00)",
		IsRecurring:  false,
		Status:       "active",
		CreatedAt:    time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC),
	}
}

// TestPutSchedule_Success verifies PutSchedule calls DynamoDB PutItem with all required fields.
func TestPutSchedule_Success(t *testing.T) {
	rec := makeTestScheduleRecord()
	mock := &mockSandboxMetadataAPI{
		putItemOutput: &dynamodb.PutItemOutput{},
	}

	err := kmaws.PutSchedule(context.Background(), mock, testSchedulesTable, rec)
	if err != nil {
		t.Fatalf("PutSchedule returned unexpected error: %v", err)
	}
	if mock.putItemInput == nil {
		t.Fatal("PutItem was not called")
	}

	item := mock.putItemInput.Item

	// Verify hash key
	sv, ok := item["schedule_name"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok || sv.Value != "km-at-sb-001-kill" {
		t.Errorf("schedule_name = %v; want km-at-sb-001-kill", item["schedule_name"])
	}

	// Verify command
	cmd, ok := item["command"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok || cmd.Value != "kill" {
		t.Errorf("command = %v; want kill", item["command"])
	}

	// Verify sandbox_id is set
	sid, ok := item["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok || sid.Value != "sb-001" {
		t.Errorf("sandbox_id = %v; want sb-001", item["sandbox_id"])
	}

	// Verify status
	status, ok := item["status"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok || status.Value != "active" {
		t.Errorf("status = %v; want active", item["status"])
	}

	// Verify created_at is RFC3339
	_, ok = item["created_at"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Errorf("created_at should be a string attribute, got %T", item["created_at"])
	}
}

// TestPutSchedule_EmptySandboxID verifies sandbox_id is omitted when empty (create command).
func TestPutSchedule_EmptySandboxID(t *testing.T) {
	rec := makeTestScheduleRecord()
	rec.SandboxID = ""
	rec.Command = "create"
	mock := &mockSandboxMetadataAPI{
		putItemOutput: &dynamodb.PutItemOutput{},
	}

	err := kmaws.PutSchedule(context.Background(), mock, testSchedulesTable, rec)
	if err != nil {
		t.Fatalf("PutSchedule returned unexpected error: %v", err)
	}

	item := mock.putItemInput.Item
	if _, exists := item["sandbox_id"]; exists {
		t.Error("sandbox_id should be omitted when empty")
	}
}

// TestGetScheduleRecord_Found verifies unmarshalling of a returned DynamoDB item.
func TestGetScheduleRecord_Found(t *testing.T) {
	createdAt := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	mock := &mockSandboxMetadataAPI{
		getItemOutput: &dynamodb.GetItemOutput{
			Item: map[string]dynamodbtypes.AttributeValue{
				"schedule_name": &dynamodbtypes.AttributeValueMemberS{Value: "km-at-sb-001-kill"},
				"command":       &dynamodbtypes.AttributeValueMemberS{Value: "kill"},
				"sandbox_id":    &dynamodbtypes.AttributeValueMemberS{Value: "sb-001"},
				"time_expr":     &dynamodbtypes.AttributeValueMemberS{Value: "tomorrow at 9am"},
				"cron_expr":     &dynamodbtypes.AttributeValueMemberS{Value: "at(2026-04-04T09:00:00)"},
				"is_recurring":  &dynamodbtypes.AttributeValueMemberBOOL{Value: false},
				"status":        &dynamodbtypes.AttributeValueMemberS{Value: "active"},
				"created_at":    &dynamodbtypes.AttributeValueMemberS{Value: createdAt.UTC().Format(time.RFC3339)},
			},
		},
	}

	rec, err := kmaws.GetScheduleRecord(context.Background(), mock, testSchedulesTable, "km-at-sb-001-kill")
	if err != nil {
		t.Fatalf("GetScheduleRecord returned unexpected error: %v", err)
	}
	if rec == nil {
		t.Fatal("GetScheduleRecord returned nil record when item exists")
	}
	if rec.ScheduleName != "km-at-sb-001-kill" {
		t.Errorf("ScheduleName = %q; want %q", rec.ScheduleName, "km-at-sb-001-kill")
	}
	if rec.Command != "kill" {
		t.Errorf("Command = %q; want kill", rec.Command)
	}
	if rec.SandboxID != "sb-001" {
		t.Errorf("SandboxID = %q; want sb-001", rec.SandboxID)
	}
	if rec.Status != "active" {
		t.Errorf("Status = %q; want active", rec.Status)
	}
}

// TestGetScheduleRecord_NotFound verifies (nil, nil) is returned when item missing.
func TestGetScheduleRecord_NotFound(t *testing.T) {
	mock := &mockSandboxMetadataAPI{
		// Empty GetItemOutput with nil Item means not found.
		getItemOutput: &dynamodb.GetItemOutput{},
	}

	rec, err := kmaws.GetScheduleRecord(context.Background(), mock, testSchedulesTable, "does-not-exist")
	if err != nil {
		t.Fatalf("GetScheduleRecord should return nil error for not-found item, got: %v", err)
	}
	if rec != nil {
		t.Fatalf("GetScheduleRecord should return nil record for not-found item, got: %+v", rec)
	}
}

// TestListScheduleRecords_Sorted verifies records are returned sorted by CreatedAt descending.
func TestListScheduleRecords_Sorted(t *testing.T) {
	older := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)

	mock := &mockSandboxMetadataAPI{
		scanOutputs: []*dynamodb.ScanOutput{
			{
				Items: []map[string]dynamodbtypes.AttributeValue{
					{
						"schedule_name": &dynamodbtypes.AttributeValueMemberS{Value: "km-at-older"},
						"command":       &dynamodbtypes.AttributeValueMemberS{Value: "kill"},
						"status":        &dynamodbtypes.AttributeValueMemberS{Value: "active"},
						"created_at":    &dynamodbtypes.AttributeValueMemberS{Value: older.UTC().Format(time.RFC3339)},
					},
					{
						"schedule_name": &dynamodbtypes.AttributeValueMemberS{Value: "km-at-newer"},
						"command":       &dynamodbtypes.AttributeValueMemberS{Value: "kill"},
						"status":        &dynamodbtypes.AttributeValueMemberS{Value: "active"},
						"created_at":    &dynamodbtypes.AttributeValueMemberS{Value: newer.UTC().Format(time.RFC3339)},
					},
				},
			},
		},
	}

	records, err := kmaws.ListScheduleRecords(context.Background(), mock, testSchedulesTable)
	if err != nil {
		t.Fatalf("ListScheduleRecords returned unexpected error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	// Descending: newer first
	if records[0].ScheduleName != "km-at-newer" {
		t.Errorf("first record should be newer; got %q", records[0].ScheduleName)
	}
	if records[1].ScheduleName != "km-at-older" {
		t.Errorf("second record should be older; got %q", records[1].ScheduleName)
	}
}

// TestDeleteScheduleRecord_Success verifies DeleteScheduleRecord calls DeleteItem.
func TestDeleteScheduleRecord_Success(t *testing.T) {
	mock := &mockSandboxMetadataAPI{
		deleteItemOutput: &dynamodb.DeleteItemOutput{},
	}

	err := kmaws.DeleteScheduleRecord(context.Background(), mock, testSchedulesTable, "km-at-sb-001-kill")
	if err != nil {
		t.Fatalf("DeleteScheduleRecord returned unexpected error: %v", err)
	}
	if mock.deleteItemInput == nil {
		t.Fatal("DeleteItem was not called")
	}

	key, ok := mock.deleteItemInput.Key["schedule_name"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok || key.Value != "km-at-sb-001-kill" {
		t.Errorf("DeleteItem key schedule_name = %v; want km-at-sb-001-kill", mock.deleteItemInput.Key["schedule_name"])
	}
}
