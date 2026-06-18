// Package cmd — doctor_check_test.go
// Unit tests for the Phase 116 km check serverless runner doctor group:
//   - checkChecksTableExists     — DDB table existence
//   - checkOrphanCheckLambdas    — {prefix}-check-* Lambdas not in DDB
//   - checkOrphanCheckSchedules  — schedules targeting absent-DDB checks
//   - checkChecksTriggerDrift    — KM_CHECK_TRIGGER sourceHash mismatch
package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
	appcfg "github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/check"
)

// =============================================================================
// Mock: minimal ChecksDDBAPI for tests
// =============================================================================

// fakeChecksDDB is a minimal check.ChecksDDBAPI for the checks-doctor tests.
// ListCheckRows uses Scan; we only need to implement Scan.
type fakeChecksDDB struct {
	rows    []check.CheckRow
	scanErr error
	// getOutput is used for GetItem calls (unused by doctor sub-checks directly
	// but needed to satisfy the interface).
	getOutput *dynamodb.GetItemOutput
	getErr    error
}

var _ check.ChecksDDBAPI = (*fakeChecksDDB)(nil)

func (f *fakeChecksDDB) PutItem(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeChecksDDB) UpdateItem(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *fakeChecksDDB) GetItem(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getOutput != nil {
		return f.getOutput, nil
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (f *fakeChecksDDB) DeleteItem(_ context.Context, _ *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}

func (f *fakeChecksDDB) Scan(_ context.Context, _ *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	// Build minimal DynamoDB items from our rows.
	items := make([]map[string]dynamodbtypes.AttributeValue, 0, len(f.rows))
	for _, r := range f.rows {
		item := map[string]dynamodbtypes.AttributeValue{
			"name":        &dynamodbtypes.AttributeValueMemberS{Value: r.Name},
			"arn":         &dynamodbtypes.AttributeValueMemberS{Value: r.ARN},
			"source_hash": &dynamodbtypes.AttributeValueMemberS{Value: r.SourceHash},
		}
		items = append(items, item)
	}
	return &dynamodb.ScanOutput{Items: items}, nil
}

// =============================================================================
// Mock: scheduler for checkOrphanCheckSchedules
// =============================================================================

// fakeSchedulerForChecks implements kmaws.SchedulerAPI minimally.
type fakeSchedulerForChecks struct {
	schedules    []schedulertypes.ScheduleSummary
	listErr      error
	notFound     bool // simulate group not found
}

func (f *fakeSchedulerForChecks) CreateSchedule(_ context.Context, _ *scheduler.CreateScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.CreateScheduleOutput, error) {
	return &scheduler.CreateScheduleOutput{}, nil
}

func (f *fakeSchedulerForChecks) DeleteSchedule(_ context.Context, _ *scheduler.DeleteScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.DeleteScheduleOutput, error) {
	return &scheduler.DeleteScheduleOutput{}, nil
}

func (f *fakeSchedulerForChecks) ListSchedules(_ context.Context, _ *scheduler.ListSchedulesInput, _ ...func(*scheduler.Options)) (*scheduler.ListSchedulesOutput, error) {
	if f.notFound {
		return nil, errors.New("ResourceNotFoundException: Schedule Group km-checks does not exist")
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	return &scheduler.ListSchedulesOutput{Schedules: f.schedules}, nil
}

func (f *fakeSchedulerForChecks) GetSchedule(_ context.Context, _ *scheduler.GetScheduleInput, _ ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
	return &scheduler.GetScheduleOutput{}, nil
}

func (f *fakeSchedulerForChecks) CreateScheduleGroup(_ context.Context, _ *scheduler.CreateScheduleGroupInput, _ ...func(*scheduler.Options)) (*scheduler.CreateScheduleGroupOutput, error) {
	return &scheduler.CreateScheduleGroupOutput{}, nil
}

// =============================================================================
// Tests: checkChecksTableExists
// =============================================================================

func TestCheckChecksTableExists_OK(t *testing.T) {
	client := &mockDynamoClient{output: &dynamodb.DescribeTableOutput{}}
	r := checkChecksTableExists(context.Background(), client, "km-checks")
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "km-checks") {
		t.Errorf("expected table name in message, got: %s", r.Message)
	}
}

func TestCheckChecksTableExists_Missing(t *testing.T) {
	client := &mockDynamoClient{err: &dynamodbtypes.ResourceNotFoundException{Message: awssdk.String("table not found")}}
	r := checkChecksTableExists(context.Background(), client, "km-checks")
	if r.Status != CheckError {
		t.Fatalf("expected ERROR for missing table, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckChecksTableExists_NilClient_Skipped(t *testing.T) {
	r := checkChecksTableExists(context.Background(), nil, "km-checks")
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED for nil client, got %s: %s", r.Status, r.Message)
	}
}

// =============================================================================
// Tests: checkOrphanCheckLambdas
// =============================================================================

func TestCheckOrphanCheckLambdas_NoOrphans_OK(t *testing.T) {
	// Lambda "km-check-qotd" is registered in DDB.
	ddb := &fakeChecksDDB{rows: []check.CheckRow{{Name: "qotd", ARN: "arn:aws:lambda:us-east-1:123:function:km-check-qotd"}}}
	lam := &fakeLambdaCleanup{functionNames: []string{"km-check-qotd", "km-ttl-handler"}}
	r := checkOrphanCheckLambdas(context.Background(), lam, ddb, "km-checks", "km")
	if r.Status != CheckOK {
		t.Fatalf("expected OK when no orphans, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckOrphanCheckLambdas_OrphanDetected_Warn(t *testing.T) {
	// "km-check-ghost" is a Lambda not in DDB.
	ddb := &fakeChecksDDB{rows: []check.CheckRow{{Name: "qotd"}}}
	lam := &fakeLambdaCleanup{functionNames: []string{"km-check-qotd", "km-check-ghost", "km-ttl-handler"}}
	r := checkOrphanCheckLambdas(context.Background(), lam, ddb, "km-checks", "km")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN for orphan Lambda, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "km-check-ghost") {
		t.Errorf("expected orphan name in message, got: %s", r.Message)
	}
	if strings.Contains(r.Message, "km-ttl-handler") {
		t.Errorf("platform Lambda must not appear in orphan list, got: %s", r.Message)
	}
}

func TestCheckOrphanCheckLambdas_NilClients_Skipped(t *testing.T) {
	r := checkOrphanCheckLambdas(context.Background(), nil, nil, "km-checks", "km")
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED for nil Lambda client, got %s", r.Status)
	}
}

func TestCheckOrphanCheckLambdas_ListError_Warn(t *testing.T) {
	lam := &fakeLambdaCleanup{listErr: errors.New("AccessDenied")}
	ddb := &fakeChecksDDB{}
	r := checkOrphanCheckLambdas(context.Background(), lam, ddb, "km-checks", "km")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN on ListFunctions error, got %s: %s", r.Status, r.Message)
	}
}

// =============================================================================
// Tests: checkOrphanCheckSchedules
// =============================================================================

func TestCheckOrphanCheckSchedules_NoOrphans_OK(t *testing.T) {
	ddb := &fakeChecksDDB{rows: []check.CheckRow{{Name: "qotd"}}}
	sched := &fakeSchedulerForChecks{
		schedules: []schedulertypes.ScheduleSummary{
			{
				Name: awssdk.String("qotd-rate"),
				Target: &schedulertypes.TargetSummary{
					Arn: awssdk.String("arn:aws:lambda:us-east-1:123:function:km-check-qotd"),
				},
			},
		},
	}
	r := checkOrphanCheckSchedules(context.Background(), sched, ddb, "km-checks", "km")
	if r.Status != CheckOK {
		t.Fatalf("expected OK when no orphan schedules, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckOrphanCheckSchedules_OrphanDetected_Warn(t *testing.T) {
	ddb := &fakeChecksDDB{rows: []check.CheckRow{{Name: "qotd"}}}
	sched := &fakeSchedulerForChecks{
		schedules: []schedulertypes.ScheduleSummary{
			{
				Name: awssdk.String("ghost-rate"),
				Target: &schedulertypes.TargetSummary{
					Arn: awssdk.String("arn:aws:lambda:us-east-1:123:function:km-check-ghost"),
				},
			},
		},
	}
	r := checkOrphanCheckSchedules(context.Background(), sched, ddb, "km-checks", "km")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN for orphan schedule, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "ghost-rate") {
		t.Errorf("expected orphan schedule name in message, got: %s", r.Message)
	}
}

func TestCheckOrphanCheckSchedules_GroupNotFound_OK(t *testing.T) {
	// A missing schedule group is expected on dormant installs (no checks deployed).
	ddb := &fakeChecksDDB{}
	sched := &fakeSchedulerForChecks{notFound: true}
	r := checkOrphanCheckSchedules(context.Background(), sched, ddb, "km-checks", "km")
	if r.Status != CheckOK {
		t.Fatalf("expected OK when schedule group not found, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckOrphanCheckSchedules_NilClient_Skipped(t *testing.T) {
	r := checkOrphanCheckSchedules(context.Background(), nil, nil, "km-checks", "km")
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED for nil scheduler client, got %s", r.Status)
	}
}

// =============================================================================
// Tests: checkChecksTriggerDrift
// =============================================================================

func TestCheckChecksTriggerDrift_NoTriggers_OK(t *testing.T) {
	// No triggers configured → capture-only mode → OK.
	ddb := &fakeChecksDDB{rows: []check.CheckRow{{Name: "qotd", SourceHash: "abc123"}}}
	r := checkChecksTriggerDrift(context.Background(), ddb, "km-checks", nil)
	if r.Status != CheckOK {
		t.Fatalf("expected OK with no triggers, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckChecksTriggerDrift_InSync_OK(t *testing.T) {
	// Build a trigger and bake it to get the expected hash.
	trigger := appcfg.CheckTrigger{
		Check:  "qotd",
		WhenPy: "return True",
		Alias:  "my-box",
		Prompt: "Hello {{reason}}",
	}
	_, expectedHash, err := check.BakeTrigger(trigger)
	if err != nil {
		t.Fatalf("BakeTrigger failed: %v", err)
	}

	ddb := &fakeChecksDDB{rows: []check.CheckRow{{Name: "qotd", SourceHash: expectedHash}}}
	r := checkChecksTriggerDrift(context.Background(), ddb, "km-checks", []appcfg.CheckTrigger{trigger})
	if r.Status != CheckOK {
		t.Fatalf("expected OK when trigger is in sync, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckChecksTriggerDrift_Drifted_Warn(t *testing.T) {
	// DDB row has an old hash; config has different content.
	trigger := appcfg.CheckTrigger{
		Check:  "qotd",
		WhenPy: "return True",
		Alias:  "my-box",
		Prompt: "New prompt",
	}
	ddb := &fakeChecksDDB{rows: []check.CheckRow{{Name: "qotd", SourceHash: "stale-hash-abc"}}}
	r := checkChecksTriggerDrift(context.Background(), ddb, "km-checks", []appcfg.CheckTrigger{trigger})
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN on drifted trigger, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "qotd") {
		t.Errorf("expected check name in message, got: %s", r.Message)
	}
	if !strings.Contains(r.Remediation, "km check sync") {
		t.Errorf("expected 'km check sync' in remediation, got: %s", r.Remediation)
	}
}

func TestCheckChecksTriggerDrift_NoRowForCheck_OK(t *testing.T) {
	// Trigger configured but check not yet deployed (no DDB row) → skip that check.
	trigger := appcfg.CheckTrigger{Check: "not-deployed", WhenPy: "return True", Alias: "box"}
	ddb := &fakeChecksDDB{rows: []check.CheckRow{}} // empty table
	r := checkChecksTriggerDrift(context.Background(), ddb, "km-checks", []appcfg.CheckTrigger{trigger})
	// No DDB row means we cannot compare hashes; result is OK (no false positives).
	if r.Status != CheckOK {
		t.Fatalf("expected OK when check not yet deployed, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckChecksTriggerDrift_NilDDB_Skipped(t *testing.T) {
	trigger := appcfg.CheckTrigger{Check: "qotd", WhenPy: "return True", Alias: "box"}
	r := checkChecksTriggerDrift(context.Background(), nil, "km-checks", []appcfg.CheckTrigger{trigger})
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED for nil DDB client, got %s", r.Status)
	}
}

// =============================================================================
// Compile-time: verify fakeSchedulerForChecks satisfies the interface
// =============================================================================

func init() {
	_ = awssdk.String("") // suppress unused-import lint on awssdk
}
