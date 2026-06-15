// aws_adapters_resume_test.go — Phase 114 unit tests for EC2Resumer and
// DynamoSandboxStatusWriter (the slack-bridge resume primitives).
//
// Tests verify:
//   - EC2Resumer always uses tag:km:sandbox-id regardless of ResourcePrefix (Phase-109 fix).
//   - EC2Resumer returns wrapped ErrNoResumableInstance when no instances found.
//   - EC2Resumer calls StartInstances on a stopped instance and returns nil.
//   - EC2Resumer returns a plain (non-sentinel-wrapped) error on transient DescribeInstances failure.
//   - DynamoSandboxStatusWriter.SetStatusRunning uses UpdateItem, never PutItem.
package bridge_test

import (
	"context"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// ============================================================
// Mock EC2StartAPI
// ============================================================

// mockEC2StartAPI records DescribeInstances/StartInstances calls for assertion.
type mockEC2StartAPI struct {
	// Configurable DescribeInstances response.
	describeOut *ec2.DescribeInstancesOutput
	describeErr error

	// Captured DescribeInstances inputs (all calls, in order).
	describeInputs []*ec2.DescribeInstancesInput

	// StartInstances tracking.
	startCalled    bool
	startedIDs     []string
	startOut       *ec2.StartInstancesOutput
	startErr       error
}

func (m *mockEC2StartAPI) DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	m.describeInputs = append(m.describeInputs, in)
	if m.describeErr != nil {
		return nil, m.describeErr
	}
	if m.describeOut != nil {
		return m.describeOut, nil
	}
	// Default: zero reservations (no instances).
	return &ec2.DescribeInstancesOutput{}, nil
}

func (m *mockEC2StartAPI) StartInstances(ctx context.Context, in *ec2.StartInstancesInput, _ ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	m.startCalled = true
	m.startedIDs = append(m.startedIDs, in.InstanceIds...)
	if m.startErr != nil {
		return nil, m.startErr
	}
	if m.startOut != nil {
		return m.startOut, nil
	}
	return &ec2.StartInstancesOutput{}, nil
}

// Compile-time check: mockEC2StartAPI must satisfy bridge.EC2StartAPI.
var _ bridge.EC2StartAPI = &mockEC2StartAPI{}

// ============================================================
// Mock DDBUpdateItemAPI (full surface; PutItem panics if called)
// ============================================================

// mockDDBUpdateItemAPI satisfies bridge.DDBUpdateItemAPI (= DDBQueryGetPutAPI + UpdateItem).
// UpdateItem captures the input for assertion. PutItem records a call so tests can
// assert it was never invoked.
type mockDDBUpdateItemAPI struct {
	updateInput *dynamodb.UpdateItemInput
	updateErr   error
	putCalled   bool
}

func (m *mockDDBUpdateItemAPI) GetItem(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{}, nil
}

func (m *mockDDBUpdateItemAPI) PutItem(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	m.putCalled = true
	return &dynamodb.PutItemOutput{}, nil
}

func (m *mockDDBUpdateItemAPI) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}

func (m *mockDDBUpdateItemAPI) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	m.updateInput = in
	return &dynamodb.UpdateItemOutput{}, m.updateErr
}

// Compile-time check: mockDDBUpdateItemAPI must satisfy bridge.DDBUpdateItemAPI.
var _ bridge.DDBUpdateItemAPI = &mockDDBUpdateItemAPI{}

// ============================================================
// Helper: build a stopped-instance DescribeInstances output.
// ============================================================

func stoppedInstanceOutput(instanceID string) *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{
			{
				Instances: []ec2types.Instance{
					{
						InstanceId: awssdk.String(instanceID),
						State: &ec2types.InstanceState{
							Name: ec2types.InstanceStateNameStopped,
						},
					},
				},
			},
		},
	}
}

// ============================================================
// Tests: EC2Resumer
// ============================================================

// TestEC2Resumer_UsesKmSandboxIdTag asserts that sandboxIDTagKey() returns
// "km:sandbox-id" even when ResourcePrefix is set to a non-"km" value.
// This locks in the Phase-109 fix (e6b9ca75/d8007920): deriving
// "{prefix}:sandbox-id" from ResourcePrefix caused false "no instances" errors
// on non-km installs.
func TestEC2Resumer_UsesKmSandboxIdTag(t *testing.T) {
	mock := &mockEC2StartAPI{
		describeOut: stoppedInstanceOutput("i-abc123"),
	}
	resumer := &bridge.EC2Resumer{
		Client:         mock,
		ResourcePrefix: "sec", // non-km prefix — must NOT affect the tag key
	}

	_ = resumer.StartSandbox(context.Background(), "sb-test-id")

	if len(mock.describeInputs) == 0 {
		t.Fatal("DescribeInstances was never called")
	}
	firstCall := mock.describeInputs[0]
	if len(firstCall.Filters) < 1 {
		t.Fatal("expected at least one filter on DescribeInstances")
	}

	// The first filter must use "tag:km:sandbox-id" regardless of ResourcePrefix.
	tagFilter := firstCall.Filters[0]
	wantName := "tag:km:sandbox-id"
	if awssdk.ToString(tagFilter.Name) != wantName {
		t.Errorf("DescribeInstances filter Name=%q; want %q (Phase-109 fix: must NOT be tag:{prefix}:sandbox-id)",
			awssdk.ToString(tagFilter.Name), wantName)
	}
	if len(tagFilter.Values) != 1 || tagFilter.Values[0] != "sb-test-id" {
		t.Errorf("DescribeInstances filter Values=%v; want [sb-test-id]", tagFilter.Values)
	}
}

// TestEC2Resumer_NoInstances_ReturnsErrNoResumable asserts that when
// DescribeInstances returns zero reservations, StartSandbox returns an error
// that wraps ErrNoResumableInstance (testable via errors.Is). StartInstances
// must NOT be called in this case.
func TestEC2Resumer_NoInstances_ReturnsErrNoResumable(t *testing.T) {
	mock := &mockEC2StartAPI{
		// Default: zero reservations (no matching instances).
		describeOut: &ec2.DescribeInstancesOutput{},
	}
	resumer := &bridge.EC2Resumer{Client: mock}

	err := resumer.StartSandbox(context.Background(), "sb-orphan")
	if err == nil {
		t.Fatal("expected an error when no instances found, got nil")
	}
	if !errors.Is(err, bridge.ErrNoResumableInstance) {
		t.Errorf("expected errors.Is(err, ErrNoResumableInstance)=true, got err=%v", err)
	}
	if mock.startCalled {
		t.Error("StartInstances must NOT be called when no instances found")
	}
}

// TestEC2Resumer_StoppedInstance_StartsAndReturnsNil asserts that when
// DescribeInstances returns one stopped instance, StartSandbox calls
// StartInstances with that instance ID and returns nil.
func TestEC2Resumer_StoppedInstance_StartsAndReturnsNil(t *testing.T) {
	const wantInstanceID = "i-stopped-7f3a"
	mock := &mockEC2StartAPI{
		describeOut: stoppedInstanceOutput(wantInstanceID),
	}
	resumer := &bridge.EC2Resumer{Client: mock}

	err := resumer.StartSandbox(context.Background(), "sb-paused-box")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.startCalled {
		t.Error("StartInstances must be called when a stopped instance is found")
	}
	if len(mock.startedIDs) != 1 || mock.startedIDs[0] != wantInstanceID {
		t.Errorf("StartInstances called with IDs=%v; want [%s]", mock.startedIDs, wantInstanceID)
	}
}

// TestEC2Resumer_TransientDescribeError_ReturnsPlainError asserts that when
// DescribeInstances returns a transient error, StartSandbox returns an error
// where errors.Is(err, ErrNoResumableInstance) is FALSE (plain non-sentinel error).
// This preserves the fail-soft behavior: the caller logs the error without
// treating the sandbox as permanently unreachable.
func TestEC2Resumer_TransientDescribeError_ReturnsPlainError(t *testing.T) {
	mock := &mockEC2StartAPI{
		describeErr: errors.New("RequestTimeout: connection reset"),
	}
	resumer := &bridge.EC2Resumer{Client: mock}

	err := resumer.StartSandbox(context.Background(), "sb-transient")
	if err == nil {
		t.Fatal("expected an error on DescribeInstances failure, got nil")
	}
	if errors.Is(err, bridge.ErrNoResumableInstance) {
		t.Errorf("transient DescribeInstances error must NOT wrap ErrNoResumableInstance; got %v", err)
	}
	if mock.startCalled {
		t.Error("StartInstances must NOT be called when DescribeInstances errors")
	}
}

// ============================================================
// Tests: DynamoSandboxStatusWriter
// ============================================================

// TestDynamoSandboxStatusWriter_UsesUpdateItem asserts that SetStatusRunning
// issues a DynamoDB UpdateItem (not PutItem) with the correct expression to
// flip status to "running". The test also asserts PutItem is never called
// (the lossy round-trip footgun guard).
func TestDynamoSandboxStatusWriter_UsesUpdateItem(t *testing.T) {
	mock := &mockDDBUpdateItemAPI{}
	writer := &bridge.DynamoSandboxStatusWriter{
		Client:    mock,
		TableName: "km-sandboxes",
	}

	const sandboxID = "sb-flip-me"
	err := writer.SetStatusRunning(context.Background(), sandboxID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// PutItem must never be called — it would strip non-struct DDB attributes.
	if mock.putCalled {
		t.Error("PutItem must NEVER be called by SetStatusRunning (lossy round-trip footgun)")
	}

	// UpdateItem must have been captured.
	if mock.updateInput == nil {
		t.Fatal("UpdateItem was never called")
	}

	// Assert table name.
	gotTable := awssdk.ToString(mock.updateInput.TableName)
	if gotTable != "km-sandboxes" {
		t.Errorf("UpdateItem TableName=%q; want km-sandboxes", gotTable)
	}

	// Assert primary key.
	sidAttr, ok := mock.updateInput.Key["sandbox_id"]
	if !ok {
		t.Fatal("UpdateItem Key must contain sandbox_id")
	}
	sidVal, ok := sidAttr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok || sidVal.Value != sandboxID {
		t.Errorf("UpdateItem Key[sandbox_id]=%v; want MemberS{%s}", sidAttr, sandboxID)
	}

	// Assert UpdateExpression contains the status flip.
	gotExpr := awssdk.ToString(mock.updateInput.UpdateExpression)
	if gotExpr != "SET #st = :running" {
		t.Errorf("UpdateExpression=%q; want \"SET #st = :running\"", gotExpr)
	}

	// Assert ExpressionAttributeNames maps #st → status (DynamoDB reserved word alias).
	stAlias, ok := mock.updateInput.ExpressionAttributeNames["#st"]
	if !ok || stAlias != "status" {
		t.Errorf("ExpressionAttributeNames[\"#st\"]=%q; want \"status\"", stAlias)
	}

	// Assert ExpressionAttributeValues maps :running → "running".
	runningAttr, ok := mock.updateInput.ExpressionAttributeValues[":running"]
	if !ok {
		t.Fatal("ExpressionAttributeValues must contain :running")
	}
	runningVal, ok := runningAttr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok || runningVal.Value != "running" {
		t.Errorf("ExpressionAttributeValues[\":running\"]=%v; want MemberS{running}", runningAttr)
	}
}
