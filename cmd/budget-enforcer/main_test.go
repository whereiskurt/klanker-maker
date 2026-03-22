package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/iam"
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

// Test 4: handler with AI spend at 100% detaches Bedrock IAM policy as backstop.
func TestBudgetHandler_DetachesBedrockPolicyAt100PercentAI(t *testing.T) {
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
	// Compute NOT at 100%, so StopInstances should not be called
	if ec2stop.stopCalled {
		t.Error("StopInstances should NOT be called when only AI budget is exceeded")
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
	h.isWarningNotifiedFn = func(ctx context.Context, sandboxID string) (bool, error) {
		return false, nil
	}
	h.setWarningNotifiedFn = func(ctx context.Context, sandboxID string) error {
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
	h.isWarningNotifiedFn = func(ctx context.Context, sandboxID string) (bool, error) {
		return true, nil // already notified
	}
	h.setWarningNotifiedFn = func(ctx context.Context, sandboxID string) error {
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
