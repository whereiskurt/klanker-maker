package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// --------------------------------------------------------------------------
// Mock: DynamoDB (BudgetAPI) — stateful to simulate real DynamoDB behavior
// --------------------------------------------------------------------------

type mockBudgetDB struct {
	// Called args tracking
	updateItemCalls []*dynamodb.UpdateItemInput
	queryCalls      []*dynamodb.QueryInput

	// Pre-configured response for GetBudget queries
	budgetSummary *awspkg.BudgetSummary

	// Track warning notified attribute for one-shot warning
	warningNotified bool
	updateErr       error
	queryErr        error
}

func (m *mockBudgetDB) UpdateItem(ctx context.Context, input *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	m.updateItemCalls = append(m.updateItemCalls, input)
	return &dynamodb.UpdateItemOutput{}, m.updateErr
}

func (m *mockBudgetDB) GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{}, nil
}

func (m *mockBudgetDB) Query(ctx context.Context, input *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	m.queryCalls = append(m.queryCalls, input)
	if m.queryErr != nil {
		return nil, m.queryErr
	}
	// Return empty items — GetBudget builds summary from items
	return &dynamodb.QueryOutput{Items: nil}, nil
}

// --------------------------------------------------------------------------
// Mock: EC2StopAPI
// --------------------------------------------------------------------------

type mockEC2StopAPI struct {
	stopCalled    bool
	stopInput     *ec2.StopInstancesInput
	stopErr       error
}

func (m *mockEC2StopAPI) StopInstances(ctx context.Context, input *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	m.stopCalled = true
	m.stopInput = input
	return &ec2.StopInstancesOutput{}, m.stopErr
}

// --------------------------------------------------------------------------
// Mock: IAMDetachAPI
// --------------------------------------------------------------------------

type mockIAMDetachAPI struct {
	detachCalled bool
	detachInput  *iam.DetachRolePolicyInput
	detachErr    error
}

func (m *mockIAMDetachAPI) DetachRolePolicy(ctx context.Context, input *iam.DetachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.DetachRolePolicyOutput, error) {
	m.detachCalled = true
	m.detachInput = input
	return &iam.DetachRolePolicyOutput{}, m.detachErr
}

// --------------------------------------------------------------------------
// Mock: ECSStopAPI
// --------------------------------------------------------------------------

type mockECSStopAPI struct {
	stopCalled bool
	stopInput  *ecs.StopTaskInput
	stopErr    error
}

func (m *mockECSStopAPI) StopTask(ctx context.Context, input *ecs.StopTaskInput, optFns ...func(*ecs.Options)) (*ecs.StopTaskOutput, error) {
	m.stopCalled = true
	m.stopInput = input
	return &ecs.StopTaskOutput{}, m.stopErr
}

// --------------------------------------------------------------------------
// Mock: SESV2API
// --------------------------------------------------------------------------

type mockSESAPI struct {
	sendCalled bool
	sendCount  int
	sendEvent  string
	sendErr    error
}

func (m *mockSESAPI) CreateEmailIdentity(ctx context.Context, input *sesv2.CreateEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.CreateEmailIdentityOutput, error) {
	return &sesv2.CreateEmailIdentityOutput{}, nil
}

func (m *mockSESAPI) DeleteEmailIdentity(ctx context.Context, input *sesv2.DeleteEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.DeleteEmailIdentityOutput, error) {
	return &sesv2.DeleteEmailIdentityOutput{}, nil
}

func (m *mockSESAPI) SendEmail(ctx context.Context, input *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	m.sendCalled = true
	m.sendCount++
	if input.Content != nil && input.Content.Simple != nil && input.Content.Simple.Subject != nil {
		m.sendEvent = *input.Content.Simple.Subject.Data
	}
	return &sesv2.SendEmailOutput{}, m.sendErr
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// newBudgetHandlerWithMocks constructs a BudgetHandler with all mock dependencies.
func newBudgetHandlerWithMocks(db *mockBudgetDB, ec2stop *mockEC2StopAPI, ecsStop *mockECSStopAPI, iamDetach *mockIAMDetachAPI, ses *mockSESAPI) *BudgetHandler {
	return &BudgetHandler{
		DynamoDB:    db,
		EC2Client:   ec2stop,
		ECSClient:   ecsStop,
		IAMClient:   iamDetach,
		SESClient:   ses,
		BudgetTable: "km-budgets",
		EmailDomain: "sandboxes.klankermaker.ai",
	}
}

// budgetCheckEvent builds a BudgetCheckEvent with an appropriate CreatedAt
// so elapsedMinutes ≈ elapsedMin.
func budgetCheckEvent(sandboxID, substrate string, spotRate float64, elapsedMin float64, roleARN, instanceID, taskARN string) BudgetCheckEvent {
	createdAt := time.Now().UTC().Add(-time.Duration(elapsedMin * float64(time.Minute)))
	return BudgetCheckEvent{
		SandboxID:     sandboxID,
		SpotRate:      spotRate,
		Substrate:     substrate,
		CreatedAt:     createdAt.Format(time.RFC3339),
		RoleARN:       roleARN,
		InstanceID:    instanceID,
		TaskARN:       taskARN,
		OperatorEmail: "ops@example.com",
	}
}

// --------------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------------

// Test 1: handler with sandbox running 30min at $0.10/hr writes $0.05 compute spend to DynamoDB.
func TestBudgetHandler_WritesComputeSpend(t *testing.T) {
	db := &mockBudgetDB{
		budgetSummary: &awspkg.BudgetSummary{
			ComputeSpent:     0,
			ComputeLimit:     10.0,
			AISpent:          0,
			AILimit:          5.0,
			WarningThreshold: 0.8,
		},
	}
	ec2stop := &mockEC2StopAPI{}
	ecsStop := &mockECSStopAPI{}
	iamDetach := &mockIAMDetachAPI{}
	ses := &mockSESAPI{}

	h := newBudgetHandlerWithMocks(db, ec2stop, ecsStop, iamDetach, ses)

	event := budgetCheckEvent("sb-test001", "ec2", 0.10, 30, "arn:aws:iam::123:role/test", "i-12345", "", )

	err := h.HandleBudgetCheck(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// DynamoDB UpdateItem should have been called (compute spend write)
	if len(db.updateItemCalls) == 0 {
		t.Fatal("expected DynamoDB UpdateItem to be called for compute spend")
	}
}

// Test 2: handler with compute spend at 100% calls StopInstances for EC2 substrate.
func TestBudgetHandler_EC2_StopsAt100PercentCompute(t *testing.T) {
	db := &mockBudgetDB{}
	ec2stop := &mockEC2StopAPI{}
	ecsStop := &mockECSStopAPI{}
	iamDetach := &mockIAMDetachAPI{}
	ses := &mockSESAPI{}

	h := newBudgetHandlerWithMocks(db, ec2stop, ecsStop, iamDetach, ses)
	// Override GetBudget to return 100% compute spent
	h.getBudgetFn = func(ctx context.Context, sandboxID string) (*awspkg.BudgetSummary, error) {
		return &awspkg.BudgetSummary{
			ComputeSpent:     5.0,
			ComputeLimit:     5.0, // 100%
			AISpent:          0,
			AILimit:          10.0,
			WarningThreshold: 0.8,
		}, nil
	}

	event := budgetCheckEvent("sb-ec2100", "ec2", 0.10, 60, "arn:aws:iam::123:role/test", "i-abc123", "")

	err := h.HandleBudgetCheck(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !ec2stop.stopCalled {
		t.Error("expected StopInstances to be called at 100% compute budget")
	}
	if ecsStop.stopCalled {
		t.Error("StopTask should NOT be called for EC2 substrate")
	}
}

// Test 3: handler with compute spend at 100% and ECS substrate calls StopTask.
func TestBudgetHandler_ECS_StopsAt100PercentCompute(t *testing.T) {
	db := &mockBudgetDB{}
	ec2stop := &mockEC2StopAPI{}
	ecsStop := &mockECSStopAPI{}
	iamDetach := &mockIAMDetachAPI{}
	ses := &mockSESAPI{}

	h := newBudgetHandlerWithMocks(db, ec2stop, ecsStop, iamDetach, ses)
	h.getBudgetFn = func(ctx context.Context, sandboxID string) (*awspkg.BudgetSummary, error) {
		return &awspkg.BudgetSummary{
			ComputeSpent:     3.0,
			ComputeLimit:     3.0, // 100%
			AISpent:          0,
			AILimit:          10.0,
			WarningThreshold: 0.8,
		}, nil
	}

	event := budgetCheckEvent("sb-ecs100", "ecs", 0.05, 60, "arn:aws:iam::123:role/test", "", "arn:aws:ecs:us-east-1:123:task/cluster/abc123")

	err := h.HandleBudgetCheck(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !ecsStop.stopCalled {
		t.Error("expected StopTask to be called at 100% compute budget for ECS substrate")
	}
	if ec2stop.stopCalled {
		t.Error("StopInstances should NOT be called for ECS substrate")
	}
}

// Test 4: handler with AI spend at 100% detaches Bedrock IAM policy AND stops the
// instance. AI exhaustion alone used to only detach Bedrock — leaving the sandbox
// burning spot $ on compute. It must pause the instance too.
func TestBudgetHandler_AIExhaustion_StopsInstanceAndDetachesBedrock(t *testing.T) {
	db := &mockBudgetDB{}
	ec2stop := &mockEC2StopAPI{}
	ecsStop := &mockECSStopAPI{}
	iamDetach := &mockIAMDetachAPI{}
	ses := &mockSESAPI{}

	h := newBudgetHandlerWithMocks(db, ec2stop, ecsStop, iamDetach, ses)
	h.getBudgetFn = func(ctx context.Context, sandboxID string) (*awspkg.BudgetSummary, error) {
		return &awspkg.BudgetSummary{
			ComputeSpent:     1.0,
			ComputeLimit:     10.0, // compute OK
			AISpent:          5.0,
			AILimit:          5.0, // AI at 100%
			WarningThreshold: 0.8,
		}, nil
	}

	event := budgetCheckEvent("sb-ai100", "ec2", 0.10, 10, "arn:aws:iam::123:role/test-role", "i-abc", "")

	err := h.HandleBudgetCheck(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !iamDetach.detachCalled {
		t.Error("expected DetachRolePolicy to be called at 100% AI budget (Bedrock backstop)")
	}
	if !ec2stop.stopCalled {
		t.Error("expected StopInstances to be called at 100% AI budget (sandbox must be paused, not just Bedrock-detached)")
	}
}

// Test 4b: AI exhaustion on ECS substrate stops the task.
func TestBudgetHandler_AIExhaustion_ECS_StopsTask(t *testing.T) {
	db := &mockBudgetDB{}
	ec2stop := &mockEC2StopAPI{}
	ecsStop := &mockECSStopAPI{}
	iamDetach := &mockIAMDetachAPI{}
	ses := &mockSESAPI{}

	h := newBudgetHandlerWithMocks(db, ec2stop, ecsStop, iamDetach, ses)
	h.getBudgetFn = func(ctx context.Context, sandboxID string) (*awspkg.BudgetSummary, error) {
		return &awspkg.BudgetSummary{
			ComputeSpent: 1.0, ComputeLimit: 10.0,
			AISpent: 5.0, AILimit: 5.0,
			WarningThreshold: 0.8,
		}, nil
	}

	event := budgetCheckEvent("sb-ai-ecs", "ecs", 0.05, 10, "arn:aws:iam::123:role/r", "", "arn:aws:ecs:us-east-1:123:task/c/t")

	if err := h.HandleBudgetCheck(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !ecsStop.stopCalled {
		t.Error("expected StopTask to be called at 100% AI budget on ECS substrate")
	}
}

// Test 5: handler with spend at 80% sends warning email via SES.
func TestBudgetHandler_SendsWarningEmailAt80Percent(t *testing.T) {
	db := &mockBudgetDB{}
	ec2stop := &mockEC2StopAPI{}
	ecsStop := &mockECSStopAPI{}
	iamDetach := &mockIAMDetachAPI{}
	ses := &mockSESAPI{}

	h := newBudgetHandlerWithMocks(db, ec2stop, ecsStop, iamDetach, ses)
	h.getBudgetFn = func(ctx context.Context, sandboxID string) (*awspkg.BudgetSummary, error) {
		return &awspkg.BudgetSummary{
			ComputeSpent:     4.0,
			ComputeLimit:     5.0, // 80%
			AISpent:          0,
			AILimit:          5.0,
			WarningThreshold: 0.8,
		}, nil
	}
	// No previous warning
	h.isMetaFlagFn = func(ctx context.Context, sandboxID, attrName string) (bool, error) {
		return false, nil
	}
	h.setMetaFlagFn = func(ctx context.Context, sandboxID, attrName string) error {
		return nil
	}

	event := budgetCheckEvent("sb-warn80", "ec2", 0.10, 40, "arn:aws:iam::123:role/test", "i-abc", "")

	err := h.HandleBudgetCheck(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !ses.sendCalled {
		t.Error("expected warning email to be sent at 80% compute budget")
	}
}

// Test 6: handler with spend below 80% does not send email or enforce.
func TestBudgetHandler_Noop_BelowThreshold(t *testing.T) {
	db := &mockBudgetDB{}
	ec2stop := &mockEC2StopAPI{}
	ecsStop := &mockECSStopAPI{}
	iamDetach := &mockIAMDetachAPI{}
	ses := &mockSESAPI{}

	h := newBudgetHandlerWithMocks(db, ec2stop, ecsStop, iamDetach, ses)
	h.getBudgetFn = func(ctx context.Context, sandboxID string) (*awspkg.BudgetSummary, error) {
		return &awspkg.BudgetSummary{
			ComputeSpent:     2.0,
			ComputeLimit:     5.0, // 40% — well below 80%
			AISpent:          1.0,
			AILimit:          10.0, // 10%
			WarningThreshold: 0.8,
		}, nil
	}

	event := budgetCheckEvent("sb-noop01", "ec2", 0.10, 20, "arn:aws:iam::123:role/test", "i-abc", "")

	err := h.HandleBudgetCheck(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ses.sendCalled {
		t.Error("no email should be sent when spend is below 80%")
	}
	if ec2stop.stopCalled {
		t.Error("StopInstances should not be called below 100%")
	}
	if iamDetach.detachCalled {
		t.Error("DetachRolePolicy should not be called below 100% AI budget")
	}
}

// Test 7: handler returns error when sandbox_id is missing.
func TestBudgetHandler_MissingSandboxID(t *testing.T) {
	h := newBudgetHandlerWithMocks(&mockBudgetDB{}, &mockEC2StopAPI{}, &mockECSStopAPI{}, &mockIAMDetachAPI{}, &mockSESAPI{})

	event := BudgetCheckEvent{SandboxID: ""}
	err := h.HandleBudgetCheck(context.Background(), event)
	if err == nil {
		t.Fatal("expected error when sandbox_id is empty")
	}
}

// Test 8: warning email is only sent once (warningNotified guard).
func TestBudgetHandler_WarningEmailSentOnlyOnce(t *testing.T) {
	db := &mockBudgetDB{}
	ses := &mockSESAPI{}
	h := newBudgetHandlerWithMocks(db, &mockEC2StopAPI{}, &mockECSStopAPI{}, &mockIAMDetachAPI{}, ses)

	h.getBudgetFn = func(ctx context.Context, sandboxID string) (*awspkg.BudgetSummary, error) {
		return &awspkg.BudgetSummary{
			ComputeSpent:     4.0,
			ComputeLimit:     5.0, // 80%
			WarningThreshold: 0.8,
		}, nil
	}

	// Simulate warning already notified
	h.isMetaFlagFn = func(ctx context.Context, sandboxID, attrName string) (bool, error) {
		if attrName == attrWarningNotified {
			return true, nil // already notified
		}
		return false, nil
	}
	h.setMetaFlagFn = func(ctx context.Context, sandboxID, attrName string) error {
		return nil
	}

	event := budgetCheckEvent("sb-warn-once", "ec2", 0.10, 40, "arn:aws:iam::123:role/r", "i-123", "")

	err := h.HandleBudgetCheck(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ses.sendCalled {
		t.Error("warning email should NOT be sent again when warningNotified is already true")
	}
}

// Ensure mockBudgetDB satisfies awspkg.BudgetAPI at compile time.
var _ awspkg.BudgetAPI = (*mockBudgetDB)(nil)

// Ensure mock satisfies awspkg.SESV2API at compile time.
var _ awspkg.SESV2API = (*mockSESAPI)(nil)

// Ensure EC2StopAPI interface is satisfied.
var _ EC2StopAPI = (*mockEC2StopAPI)(nil)

// Ensure IAMDetachAPI interface is satisfied.
var _ IAMDetachAPI = (*mockIAMDetachAPI)(nil)

// Ensure ECSStopAPI interface is satisfied.
var _ ECSStopAPI = (*mockECSStopAPI)(nil)

// Ensure BudgetCheckEvent is defined in main package.
var _ = fmt.Sprintf("%T", BudgetCheckEvent{})

// --------------------------------------------------------------------------
// Mock: SandboxMetadataAPI (for sandbox existence check)
// --------------------------------------------------------------------------

type mockSandboxMetadataAPI struct {
	getItemOutput *dynamodb.GetItemOutput
	getItemErr    error
}

func (m *mockSandboxMetadataAPI) GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if m.getItemErr != nil {
		return nil, m.getItemErr
	}
	if m.getItemOutput != nil {
		return m.getItemOutput, nil
	}
	// Default: empty item (sandbox not found)
	return &dynamodb.GetItemOutput{}, nil
}

func (m *mockSandboxMetadataAPI) PutItem(ctx context.Context, input *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}

func (m *mockSandboxMetadataAPI) UpdateItem(ctx context.Context, input *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return &dynamodb.UpdateItemOutput{}, nil
}

func (m *mockSandboxMetadataAPI) DeleteItem(ctx context.Context, input *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}

func (m *mockSandboxMetadataAPI) Scan(ctx context.Context, input *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	return &dynamodb.ScanOutput{}, nil
}

func (m *mockSandboxMetadataAPI) Query(ctx context.Context, input *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}

var _ awspkg.SandboxMetadataAPI = (*mockSandboxMetadataAPI)(nil)

// --------------------------------------------------------------------------
// Mock: SchedulerAPI (for self-delete)
// --------------------------------------------------------------------------

type mockSchedulerAPI struct {
	deleteCalled bool
	deleteName   string
	deleteErr    error
}

func (m *mockSchedulerAPI) CreateSchedule(ctx context.Context, input *scheduler.CreateScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.CreateScheduleOutput, error) {
	return &scheduler.CreateScheduleOutput{}, nil
}

func (m *mockSchedulerAPI) DeleteSchedule(ctx context.Context, input *scheduler.DeleteScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.DeleteScheduleOutput, error) {
	m.deleteCalled = true
	if input.Name != nil {
		m.deleteName = *input.Name
	}
	return &scheduler.DeleteScheduleOutput{}, m.deleteErr
}

func (m *mockSchedulerAPI) ListSchedules(ctx context.Context, input *scheduler.ListSchedulesInput, optFns ...func(*scheduler.Options)) (*scheduler.ListSchedulesOutput, error) {
	return &scheduler.ListSchedulesOutput{}, nil
}

func (m *mockSchedulerAPI) GetSchedule(ctx context.Context, input *scheduler.GetScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
	return &scheduler.GetScheduleOutput{}, nil
}

var _ awspkg.SchedulerAPI = (*mockSchedulerAPI)(nil)

// --------------------------------------------------------------------------
// Tests: Self-healing (orphaned schedule cleanup)
// --------------------------------------------------------------------------

// Test 9: handler self-deletes budget schedule when sandbox no longer exists in DynamoDB.
func TestBudgetHandler_SelfDeletesWhenSandboxGone(t *testing.T) {
	db := &mockBudgetDB{}
	ses := &mockSESAPI{}
	ec2stop := &mockEC2StopAPI{}
	sched := &mockSchedulerAPI{}
	// Empty GetItem response → ReadSandboxMetadataDynamo returns ErrSandboxNotFound
	sandboxDynamo := &mockSandboxMetadataAPI{}

	h := newBudgetHandlerWithMocks(db, ec2stop, &mockECSStopAPI{}, &mockIAMDetachAPI{}, ses)
	h.SandboxDynamo = sandboxDynamo
	h.SandboxTable = "km-sandboxes"
	h.SchedulerClient = sched

	event := budgetCheckEvent("sb-orphan01", "ec2", 0.10, 60, "arn:aws:iam::123:role/test", "i-gone", "")

	err := h.HandleBudgetCheck(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !sched.deleteCalled {
		t.Fatal("expected budget schedule to be self-deleted when sandbox is gone")
	}
	if sched.deleteName != "km-budget-sb-orphan01" {
		t.Errorf("expected schedule name km-budget-sb-orphan01, got %s", sched.deleteName)
	}

	// Should NOT have proceeded to budget check / enforcement
	if ec2stop.stopCalled {
		t.Error("should not enforce budget for a non-existent sandbox")
	}
	if ses.sendCalled {
		t.Error("should not send emails for a non-existent sandbox")
	}
}

// Test 10: handler proceeds normally when sandbox exists in DynamoDB.
func TestBudgetHandler_ProceedsWhenSandboxExists(t *testing.T) {
	db := &mockBudgetDB{}
	ses := &mockSESAPI{}
	sched := &mockSchedulerAPI{}
	// Return a valid sandbox item so ReadSandboxMetadataDynamo succeeds
	sandboxDynamo := &mockSandboxMetadataAPI{
		getItemOutput: &dynamodb.GetItemOutput{
			Item: map[string]dynamodbtypes.AttributeValue{
				"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "sb-exists01"},
				"status":     &dynamodbtypes.AttributeValueMemberS{Value: "running"},
				"profile":    &dynamodbtypes.AttributeValueMemberS{Value: "default"},
				"substrate":  &dynamodbtypes.AttributeValueMemberS{Value: "ec2"},
				"region":     &dynamodbtypes.AttributeValueMemberS{Value: "us-east-1"},
				"created_at": &dynamodbtypes.AttributeValueMemberS{Value: "2026-04-12T00:00:00Z"},
			},
		},
	}

	h := newBudgetHandlerWithMocks(db, &mockEC2StopAPI{}, &mockECSStopAPI{}, &mockIAMDetachAPI{}, ses)
	h.SandboxDynamo = sandboxDynamo
	h.SandboxTable = "km-sandboxes"
	h.SchedulerClient = sched
	h.getBudgetFn = func(ctx context.Context, sandboxID string) (*awspkg.BudgetSummary, error) {
		return &awspkg.BudgetSummary{
			ComputeSpent: 1.0, ComputeLimit: 10.0,
			AISpent: 0, AILimit: 5.0, WarningThreshold: 0.8,
		}, nil
	}

	event := budgetCheckEvent("sb-exists01", "ec2", 0.10, 30, "arn:aws:iam::123:role/test", "i-abc", "")

	err := h.HandleBudgetCheck(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT self-delete
	if sched.deleteCalled {
		t.Error("should not delete budget schedule when sandbox exists")
	}
}

// Test 11: handler proceeds (does not self-delete) on transient DynamoDB read error.
func TestBudgetHandler_ProceedsOnTransientDynamoError(t *testing.T) {
	db := &mockBudgetDB{}
	sched := &mockSchedulerAPI{}
	sandboxDynamo := &mockSandboxMetadataAPI{
		getItemErr: fmt.Errorf("simulated transient DynamoDB error"),
	}

	h := newBudgetHandlerWithMocks(db, &mockEC2StopAPI{}, &mockECSStopAPI{}, &mockIAMDetachAPI{}, &mockSESAPI{})
	h.SandboxDynamo = sandboxDynamo
	h.SandboxTable = "km-sandboxes"
	h.SchedulerClient = sched
	h.getBudgetFn = func(ctx context.Context, sandboxID string) (*awspkg.BudgetSummary, error) {
		return &awspkg.BudgetSummary{
			ComputeSpent: 1.0, ComputeLimit: 10.0,
			AISpent: 0, AILimit: 5.0, WarningThreshold: 0.8,
		}, nil
	}

	event := budgetCheckEvent("sb-transient", "ec2", 0.10, 30, "arn:aws:iam::123:role/test", "i-abc", "")

	err := h.HandleBudgetCheck(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must NOT self-delete on transient error — could incorrectly delete a live schedule
	if sched.deleteCalled {
		t.Error("should not delete budget schedule on transient DynamoDB error")
	}
}

// --------------------------------------------------------------------------
// Tests: one-shot gating on 100% exhaustion emails
// --------------------------------------------------------------------------

// Test 12: compute-exhausted email is suppressed once the one-shot flag is set.
// Regression test for minute-interval email spam at 100%+.
func TestBudgetHandler_ComputeExhaustedEmailOnlyOnce(t *testing.T) {
	db := &mockBudgetDB{}
	ses := &mockSESAPI{}
	h := newBudgetHandlerWithMocks(db, &mockEC2StopAPI{}, &mockECSStopAPI{}, &mockIAMDetachAPI{}, ses)
	h.getBudgetFn = func(ctx context.Context, sandboxID string) (*awspkg.BudgetSummary, error) {
		return &awspkg.BudgetSummary{
			ComputeSpent: 6.0, ComputeLimit: 5.0, // 120%
			AISpent: 0, AILimit: 5.0,
			WarningThreshold: 0.8,
		}, nil
	}
	h.isMetaFlagFn = func(ctx context.Context, sandboxID, attrName string) (bool, error) {
		return attrName == attrComputeExhaustedNotified, nil
	}
	h.setMetaFlagFn = func(ctx context.Context, sandboxID, attrName string) error { return nil }

	event := budgetCheckEvent("sb-c100-dup", "ec2", 0.10, 120, "arn:aws:iam::123:role/r", "i-abc", "")
	if err := h.HandleBudgetCheck(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ses.sendCalled {
		t.Errorf("compute-exhausted email must NOT be resent when %s flag is already set", attrComputeExhaustedNotified)
	}
}

// Test 13: first-time compute exhaustion sends email AND sets the one-shot flag.
func TestBudgetHandler_ComputeExhaustedEmailFirstTime(t *testing.T) {
	db := &mockBudgetDB{}
	ses := &mockSESAPI{}
	h := newBudgetHandlerWithMocks(db, &mockEC2StopAPI{}, &mockECSStopAPI{}, &mockIAMDetachAPI{}, ses)
	h.getBudgetFn = func(ctx context.Context, sandboxID string) (*awspkg.BudgetSummary, error) {
		return &awspkg.BudgetSummary{
			ComputeSpent: 6.0, ComputeLimit: 5.0,
			AISpent: 0, AILimit: 5.0,
			WarningThreshold: 0.8,
		}, nil
	}
	h.isMetaFlagFn = func(ctx context.Context, sandboxID, attrName string) (bool, error) { return false, nil }
	var setCalls []string
	h.setMetaFlagFn = func(ctx context.Context, sandboxID, attrName string) error {
		setCalls = append(setCalls, attrName)
		return nil
	}

	event := budgetCheckEvent("sb-c100-first", "ec2", 0.10, 120, "arn:aws:iam::123:role/r", "i-abc", "")
	if err := h.HandleBudgetCheck(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ses.sendCalled {
		t.Error("expected compute-exhausted email on first-time 100%+ trigger")
	}
	found := false
	for _, attr := range setCalls {
		if attr == attrComputeExhaustedNotified {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %s flag to be set after successful email; setCalls=%v", attrComputeExhaustedNotified, setCalls)
	}
}

// Test 14: AI-exhausted email is suppressed once the one-shot flag is set.
// Compute enforcement (StopInstances) still runs — that path is not email-gated.
func TestBudgetHandler_AIExhaustedEmailOnlyOnce(t *testing.T) {
	db := &mockBudgetDB{}
	ses := &mockSESAPI{}
	ec2stop := &mockEC2StopAPI{}
	h := newBudgetHandlerWithMocks(db, ec2stop, &mockECSStopAPI{}, &mockIAMDetachAPI{}, ses)
	h.getBudgetFn = func(ctx context.Context, sandboxID string) (*awspkg.BudgetSummary, error) {
		return &awspkg.BudgetSummary{
			ComputeSpent: 1.0, ComputeLimit: 10.0,
			AISpent: 5.0, AILimit: 5.0, // 100%
			WarningThreshold: 0.8,
		}, nil
	}
	h.isMetaFlagFn = func(ctx context.Context, sandboxID, attrName string) (bool, error) {
		return attrName == attrAIExhaustedNotified, nil
	}
	h.setMetaFlagFn = func(ctx context.Context, sandboxID, attrName string) error { return nil }

	event := budgetCheckEvent("sb-a100-dup", "ec2", 0.10, 20, "arn:aws:iam::123:role/r", "i-abc", "")
	if err := h.HandleBudgetCheck(context.Background(), event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ses.sendCalled {
		t.Errorf("ai-exhausted email must NOT be resent when %s flag is already set", attrAIExhaustedNotified)
	}
	if !ec2stop.stopCalled {
		t.Error("StopInstances must still run even when the AI email is suppressed")
	}
}
