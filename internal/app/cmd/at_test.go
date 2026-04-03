package cmd_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// mockSchedulerAPI satisfies awspkg.SchedulerAPI for testing.
type mockSchedulerAPI struct {
	createScheduleInput *scheduler.CreateScheduleInput
	createScheduleErr   error
	deleteScheduleInput *scheduler.DeleteScheduleInput
	deleteScheduleErr   error
	listSchedulesOutput *scheduler.ListSchedulesOutput
	listSchedulesErr    error
	getScheduleOutput   *scheduler.GetScheduleOutput
	getScheduleErr      error
}

func (m *mockSchedulerAPI) CreateSchedule(ctx context.Context, input *scheduler.CreateScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.CreateScheduleOutput, error) {
	m.createScheduleInput = input
	return &scheduler.CreateScheduleOutput{}, m.createScheduleErr
}

func (m *mockSchedulerAPI) DeleteSchedule(ctx context.Context, input *scheduler.DeleteScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.DeleteScheduleOutput, error) {
	m.deleteScheduleInput = input
	return &scheduler.DeleteScheduleOutput{}, m.deleteScheduleErr
}

func (m *mockSchedulerAPI) ListSchedules(ctx context.Context, input *scheduler.ListSchedulesInput, optFns ...func(*scheduler.Options)) (*scheduler.ListSchedulesOutput, error) {
	if m.listSchedulesOutput != nil {
		return m.listSchedulesOutput, m.listSchedulesErr
	}
	return &scheduler.ListSchedulesOutput{}, m.listSchedulesErr
}

func (m *mockSchedulerAPI) GetSchedule(ctx context.Context, input *scheduler.GetScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
	return m.getScheduleOutput, m.getScheduleErr
}

// mockDynamoAPI satisfies awspkg.SandboxMetadataAPI for testing.
type mockDynamoAtAPI struct {
	putItemInput    *dynamodb.PutItemInput
	putItemErr      error
	scanOutputs     []*dynamodb.ScanOutput
	scanIdx         int
	deleteItemInput *dynamodb.DeleteItemInput
	deleteItemErr   error
}

func (m *mockDynamoAtAPI) PutItem(ctx context.Context, input *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	m.putItemInput = input
	return &dynamodb.PutItemOutput{}, m.putItemErr
}

func (m *mockDynamoAtAPI) GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{}, nil
}

func (m *mockDynamoAtAPI) DeleteItem(ctx context.Context, input *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	m.deleteItemInput = input
	return &dynamodb.DeleteItemOutput{}, m.deleteItemErr
}

func (m *mockDynamoAtAPI) Scan(ctx context.Context, input *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	if m.scanIdx < len(m.scanOutputs) {
		out := m.scanOutputs[m.scanIdx]
		m.scanIdx++
		return out, nil
	}
	return &dynamodb.ScanOutput{}, nil
}

func (m *mockDynamoAtAPI) UpdateItem(ctx context.Context, input *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return &dynamodb.UpdateItemOutput{}, nil
}

func (m *mockDynamoAtAPI) Query(ctx context.Context, input *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}


// testConfig returns a minimal Config for at command tests.
func testAtConfig() *config.Config {
	return &config.Config{
		SchedulesTableName:     "km-schedules-test",
		CreateHandlerLambdaARN: "arn:aws:lambda:us-east-1:123456789:function:km-create-handler",
		TTLLambdaARN:           "arn:aws:lambda:us-east-1:123456789:function:km-ttl",
		SchedulerRoleARN:       "arn:aws:iam::123456789:role/km-scheduler-role",
		MaxSandboxes:           10,
		ArtifactsBucket:        "km-artifacts",
	}
}

// runAtCmd is a helper that wires a testable at command and executes it with given args.
// Returns stdout output, stderr output, and the command error.
func runAtCmd(t *testing.T, cfg *config.Config, sched *mockSchedulerAPI, dynamo *mockDynamoAtAPI, activeCount int, countErr error, args []string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	atCmd := cmd.NewAtCmdWithDeps(cfg, sched, dynamo, func(ctx context.Context) (int, error) {
		return activeCount, countErr
	})
	listCmd := cmd.NewAtListCmdWithDeps(cfg, dynamo)
	cancelCmd := cmd.NewAtCancelCmdWithDeps(cfg, sched, dynamo)
	atCmd.AddCommand(listCmd)
	atCmd.AddCommand(cancelCmd)

	// Wrap in a dummy root so arg parsing works correctly
	root := &cobra.Command{Use: "km", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(atCmd)
	root.SetOut(&stdout)
	root.SetErr(&stderr)

	root.SetArgs(append([]string{"at"}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

// TestAtCmd_OneTimeCreate verifies that "km at 'in 2 hours' create dev.yaml" creates
// an EventBridge at() schedule targeting the create-handler Lambda.
func TestAtCmd_OneTimeCreate(t *testing.T) {
	cfg := testAtConfig()
	sched := &mockSchedulerAPI{}
	dynamo := &mockDynamoAtAPI{putItemInput: nil}

	_, _, err := runAtCmd(t, cfg, sched, dynamo, 0, nil, []string{"in 2 hours", "create", "dev.yaml"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if sched.createScheduleInput == nil {
		t.Fatal("CreateSchedule was not called")
	}

	// Verify expression is an at() expression
	expr := *sched.createScheduleInput.ScheduleExpression
	if !strings.HasPrefix(expr, "at(") {
		t.Errorf("expected at() expression, got: %q", expr)
	}

	// Verify target is the create handler
	if sched.createScheduleInput.Target == nil {
		t.Fatal("Target is nil")
	}
	if sched.createScheduleInput.Target.Arn == nil || *sched.createScheduleInput.Target.Arn != cfg.CreateHandlerLambdaARN {
		t.Errorf("expected create handler ARN %q, got %v", cfg.CreateHandlerLambdaARN, sched.createScheduleInput.Target.Arn)
	}

	// Verify ActionAfterCompletion is DELETE for one-time
	if sched.createScheduleInput.ActionAfterCompletion != schedulertypes.ActionAfterCompletionDelete {
		t.Errorf("expected ActionAfterCompletion=DELETE for one-time schedule, got %v", sched.createScheduleInput.ActionAfterCompletion)
	}

	// Verify DynamoDB put was called
	if dynamo.putItemInput == nil {
		t.Fatal("PutItem was not called")
	}
}

// TestAtCmd_RecurringKill verifies "km at 'every thursday at 3pm' kill sb-abc" creates
// a recurring cron() schedule targeting the TTL Lambda.
func TestAtCmd_RecurringKill(t *testing.T) {
	cfg := testAtConfig()
	sched := &mockSchedulerAPI{}
	dynamo := &mockDynamoAtAPI{}

	_, _, err := runAtCmd(t, cfg, sched, dynamo, 0, nil, []string{"every thursday at 3pm", "kill", "sb-abc"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if sched.createScheduleInput == nil {
		t.Fatal("CreateSchedule was not called")
	}

	// Verify expression is a cron() expression
	expr := *sched.createScheduleInput.ScheduleExpression
	if !strings.HasPrefix(expr, "cron(") {
		t.Errorf("expected cron() expression, got: %q", expr)
	}

	// Verify target is the TTL Lambda
	if sched.createScheduleInput.Target == nil || sched.createScheduleInput.Target.Arn == nil {
		t.Fatal("Target ARN is nil")
	}
	if *sched.createScheduleInput.Target.Arn != cfg.TTLLambdaARN {
		t.Errorf("expected TTL Lambda ARN %q, got %q", cfg.TTLLambdaARN, *sched.createScheduleInput.Target.Arn)
	}

	// Recurring: ActionAfterCompletion = NONE
	if sched.createScheduleInput.ActionAfterCompletion != schedulertypes.ActionAfterCompletionNone {
		t.Errorf("expected ActionAfterCompletion=NONE for recurring schedule, got %v", sched.createScheduleInput.ActionAfterCompletion)
	}
}

// TestAtCmd_CronFlag verifies that --cron bypasses the NL parser.
func TestAtCmd_CronFlag(t *testing.T) {
	cfg := testAtConfig()
	sched := &mockSchedulerAPI{}
	dynamo := &mockDynamoAtAPI{}

	_, _, err := runAtCmd(t, cfg, sched, dynamo, 0, nil, []string{"--cron", "cron(0 15 ? * 5 *)", "kill", "sb-abc"})
	if err != nil {
		t.Fatalf("expected no error with --cron, got: %v", err)
	}

	if sched.createScheduleInput == nil {
		t.Fatal("CreateSchedule was not called")
	}
	expr := *sched.createScheduleInput.ScheduleExpression
	if expr != "cron(0 15 ? * 5 *)" {
		t.Errorf("expected raw cron expression, got: %q", expr)
	}
}

// TestAtCmd_UnsupportedCommand verifies that unsupported commands return an error.
func TestAtCmd_UnsupportedCommand(t *testing.T) {
	cfg := testAtConfig()
	sched := &mockSchedulerAPI{}
	dynamo := &mockDynamoAtAPI{}

	_, _, err := runAtCmd(t, cfg, sched, dynamo, 0, nil, []string{"in 2 hours", "validate", "dev.yaml"})
	if err == nil {
		t.Fatal("expected error for unsupported command 'validate', got nil")
	}
	if !strings.Contains(err.Error(), "validate") || !strings.Contains(err.Error(), "not schedulable") {
		t.Errorf("expected 'not schedulable' error for validate, got: %v", err)
	}

	if sched.createScheduleInput != nil {
		t.Error("CreateSchedule should not have been called for unsupported command")
	}
}

// TestAtCmd_MissingLambdaARN verifies that missing Lambda ARN returns an error.
func TestAtCmd_MissingLambdaARN(t *testing.T) {
	cfg := testAtConfig()
	cfg.CreateHandlerLambdaARN = ""
	cfg.TTLLambdaARN = ""
	sched := &mockSchedulerAPI{}
	dynamo := &mockDynamoAtAPI{}

	_, _, err := runAtCmd(t, cfg, sched, dynamo, 0, nil, []string{"in 2 hours", "create", "dev.yaml"})
	if err == nil {
		t.Fatal("expected error for missing Lambda ARN, got nil")
	}
	if !strings.Contains(err.Error(), "km init") {
		t.Errorf("expected 'km init' in error message, got: %v", err)
	}
}

// TestAtCmd_RecurringCreateAtLimit verifies that a recurring create schedule is blocked
// when active sandbox count >= max_sandboxes.
func TestAtCmd_RecurringCreateAtLimit(t *testing.T) {
	cfg := testAtConfig()
	cfg.MaxSandboxes = 10
	sched := &mockSchedulerAPI{}
	dynamo := &mockDynamoAtAPI{}

	// Active count at limit
	_, stderr, err := runAtCmd(t, cfg, sched, dynamo, 10, nil, []string{"every thursday at 3pm", "create", "dev.yaml"})
	if err != nil {
		t.Fatalf("expected nil error (warning, no schedule), got: %v", err)
	}

	// CreateSchedule should NOT have been called
	if sched.createScheduleInput != nil {
		t.Error("CreateSchedule should NOT be called when at sandbox limit")
	}

	// Warning should appear in stderr
	if !strings.Contains(stderr, "at or above limit") {
		t.Errorf("expected 'at or above limit' warning in stderr, got: %q", stderr)
	}
	if !strings.Contains(stderr, "10") {
		t.Errorf("expected sandbox counts in warning, got: %q", stderr)
	}
}

// TestAtCmd_RecurringCreateBelowLimit verifies that a recurring create schedule proceeds
// when count < max_sandboxes.
func TestAtCmd_RecurringCreateBelowLimit(t *testing.T) {
	cfg := testAtConfig()
	cfg.MaxSandboxes = 10
	sched := &mockSchedulerAPI{}
	dynamo := &mockDynamoAtAPI{}

	_, _, err := runAtCmd(t, cfg, sched, dynamo, 5, nil, []string{"every thursday at 3pm", "create", "dev.yaml"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// CreateSchedule should have been called (below limit)
	if sched.createScheduleInput == nil {
		t.Error("CreateSchedule should be called when below sandbox limit")
	}
}

// TestAtCmd_RecurringCreateUnlimited verifies that MaxSandboxes=0 skips the guardrail check.
func TestAtCmd_RecurringCreateUnlimited(t *testing.T) {
	cfg := testAtConfig()
	cfg.MaxSandboxes = 0 // unlimited
	sched := &mockSchedulerAPI{}
	dynamo := &mockDynamoAtAPI{}

	// Even with a high count, no guardrail
	_, _, err := runAtCmd(t, cfg, sched, dynamo, 999, nil, []string{"every thursday at 3pm", "create", "dev.yaml"})
	if err != nil {
		t.Fatalf("expected no error for unlimited config, got: %v", err)
	}

	if sched.createScheduleInput == nil {
		t.Error("CreateSchedule should be called when MaxSandboxes=0 (unlimited)")
	}
}

// TestAtList_Empty verifies "km at list" prints "No scheduled operations." when no records.
func TestAtList_Empty(t *testing.T) {
	cfg := testAtConfig()
	dynamo := &mockDynamoAtAPI{
		scanOutputs: []*dynamodb.ScanOutput{
			{Items: []map[string]dynamodbtypes.AttributeValue{}},
		},
	}

	stdout, _, err := runAtCmd(t, cfg, nil, dynamo, 0, nil, []string{"list"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(stdout, "No scheduled operations") {
		t.Errorf("expected 'No scheduled operations' in output, got: %q", stdout)
	}
}

// TestAtList_WithRecords verifies "km at list" returns a formatted table.
func TestAtList_WithRecords(t *testing.T) {
	cfg := testAtConfig()
	createdAt := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	dynamo := &mockDynamoAtAPI{
		scanOutputs: []*dynamodb.ScanOutput{
			{
				Items: []map[string]dynamodbtypes.AttributeValue{
					{
						"schedule_name": &dynamodbtypes.AttributeValueMemberS{Value: "km-at-kill-sb-001"},
						"command":       &dynamodbtypes.AttributeValueMemberS{Value: "kill"},
						"sandbox_id":    &dynamodbtypes.AttributeValueMemberS{Value: "sb-001"},
						"time_expr":     &dynamodbtypes.AttributeValueMemberS{Value: "tomorrow at 9am"},
						"cron_expr":     &dynamodbtypes.AttributeValueMemberS{Value: "at(2026-04-04T09:00:00)"},
						"is_recurring":  &dynamodbtypes.AttributeValueMemberBOOL{Value: false},
						"status":        &dynamodbtypes.AttributeValueMemberS{Value: "active"},
						"created_at":    &dynamodbtypes.AttributeValueMemberS{Value: createdAt.UTC().Format(time.RFC3339)},
					},
				},
			},
		},
	}

	stdout, _, err := runAtCmd(t, cfg, nil, dynamo, 0, nil, []string{"list"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(stdout, "km-at-kill-sb-001") {
		t.Errorf("expected schedule name in output, got: %q", stdout)
	}
	if !strings.Contains(stdout, "kill") {
		t.Errorf("expected command in output, got: %q", stdout)
	}
}

// TestAtCancel verifies "km at cancel <name>" calls DeleteSchedule + DeleteScheduleRecord.
func TestAtCancel(t *testing.T) {
	cfg := testAtConfig()
	sched := &mockSchedulerAPI{}
	dynamo := &mockDynamoAtAPI{}

	stdout, _, err := runAtCmd(t, cfg, sched, dynamo, 0, nil, []string{"cancel", "km-at-kill-sb-001"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// DeleteSchedule should have been called
	if sched.deleteScheduleInput == nil {
		t.Error("DeleteSchedule was not called")
	}
	if sched.deleteScheduleInput != nil && *sched.deleteScheduleInput.Name != "km-at-kill-sb-001" {
		t.Errorf("expected DeleteSchedule name 'km-at-kill-sb-001', got %q", *sched.deleteScheduleInput.Name)
	}

	// DeleteItem should have been called
	if dynamo.deleteItemInput == nil {
		t.Error("DeleteItem was not called")
	}

	// Print confirmation
	if !strings.Contains(stdout, "km-at-kill-sb-001") {
		t.Errorf("expected schedule name in cancel confirmation, got stdout=%q", stdout)
	}
}
