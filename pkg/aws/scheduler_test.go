package aws_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/scheduler/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
)

// mockSchedulerAPI implements SchedulerAPI for unit tests.
type mockSchedulerAPI struct {
	createCalled    bool
	createInput     *scheduler.CreateScheduleInput
	createCallCount int
	createErr       error
	// createErrUntilGroup, when set, is returned by CreateSchedule on every
	// call made before the schedule group exists. Once CreateScheduleGroup
	// runs (success OR conflict) the group is considered present and
	// subsequent CreateSchedule calls return createErr (default nil).
	createErrUntilGroup error
	groupExists         bool

	createGroupCalled bool
	createGroupCount  int
	createGroupInput  *scheduler.CreateScheduleGroupInput
	createGroupErr    error

	deleteCalled bool
	deleteInput  *scheduler.DeleteScheduleInput
	deleteErr    error
}

func (m *mockSchedulerAPI) CreateSchedule(ctx context.Context, input *scheduler.CreateScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.CreateScheduleOutput, error) {
	m.createCalled = true
	m.createInput = input
	m.createCallCount++
	if m.createErrUntilGroup != nil && !m.groupExists {
		return nil, m.createErrUntilGroup
	}
	return &scheduler.CreateScheduleOutput{}, m.createErr
}

func (m *mockSchedulerAPI) CreateScheduleGroup(ctx context.Context, input *scheduler.CreateScheduleGroupInput, optFns ...func(*scheduler.Options)) (*scheduler.CreateScheduleGroupOutput, error) {
	m.createGroupCalled = true
	m.createGroupCount++
	m.createGroupInput = input
	// After this call the group exists, whether we created it now or it was
	// already present (ConflictException). Mirror real-world idempotency.
	m.groupExists = true
	if m.createGroupErr != nil {
		return nil, m.createGroupErr
	}
	return &scheduler.CreateScheduleGroupOutput{}, nil
}

func (m *mockSchedulerAPI) DeleteSchedule(ctx context.Context, input *scheduler.DeleteScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.DeleteScheduleOutput, error) {
	m.deleteCalled = true
	m.deleteInput = input
	return &scheduler.DeleteScheduleOutput{}, m.deleteErr
}

func (m *mockSchedulerAPI) ListSchedules(ctx context.Context, input *scheduler.ListSchedulesInput, optFns ...func(*scheduler.Options)) (*scheduler.ListSchedulesOutput, error) {
	return &scheduler.ListSchedulesOutput{}, nil
}

func (m *mockSchedulerAPI) GetSchedule(ctx context.Context, input *scheduler.GetScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
	return &scheduler.GetScheduleOutput{}, nil
}

func TestCreateTTLSchedule_Success(t *testing.T) {
	ttlTime := time.Now().Add(2 * time.Hour)
	input := compiler.BuildTTLScheduleInput("sb-123", ttlTime, "arn:aws:lambda:us-east-1:123456789012:function:km-ttl", "arn:aws:iam::123456789012:role/km-scheduler-role", "km")

	if input == nil {
		t.Fatal("BuildTTLScheduleInput returned nil for non-zero ttlTime")
	}

	mock := &mockSchedulerAPI{}
	err := kmaws.CreateTTLSchedule(context.Background(), mock, input)
	if err != nil {
		t.Fatalf("CreateTTLSchedule returned unexpected error: %v", err)
	}
	if !mock.createCalled {
		t.Fatal("expected CreateSchedule to be called on the mock")
	}
	if mock.createInput == nil {
		t.Fatal("CreateSchedule was called with nil input")
	}

	// Verify schedule name
	if mock.createInput.Name == nil || *mock.createInput.Name != "km-ttl-sb-123" {
		t.Errorf("expected schedule Name=%q, got %v", "km-ttl-sb-123", mock.createInput.Name)
	}

	// Verify at() expression matches ttlTime
	if mock.createInput.ScheduleExpression == nil {
		t.Fatal("ScheduleExpression is nil")
	}
	expr := *mock.createInput.ScheduleExpression
	expected := "at(" + ttlTime.UTC().Format("2006-01-02T15:04:05") + ")"
	if expr != expected {
		t.Errorf("ScheduleExpression = %q; want %q", expr, expected)
	}

	// Verify action after completion is DELETE
	if mock.createInput.ActionAfterCompletion != types.ActionAfterCompletionDelete {
		t.Errorf("ActionAfterCompletion = %v; want DELETE", mock.createInput.ActionAfterCompletion)
	}
}

func TestCreateTTLSchedule_NoTTL(t *testing.T) {
	// Zero time = TTL not configured
	input := compiler.BuildTTLScheduleInput("sb-123", time.Time{}, "arn:...", "arn:...", "km")
	if input != nil {
		t.Fatalf("BuildTTLScheduleInput should return nil for zero ttlTime, got %+v", input)
	}

	mock := &mockSchedulerAPI{}
	err := kmaws.CreateTTLSchedule(context.Background(), mock, nil)
	if err != nil {
		t.Fatalf("CreateTTLSchedule(nil input) should return nil, got: %v", err)
	}
	if mock.createCalled {
		t.Error("CreateSchedule should NOT be called when input is nil (TTL not configured)")
	}
}

func TestDeleteTTLSchedule_Success(t *testing.T) {
	mock := &mockSchedulerAPI{}

	err := kmaws.DeleteTTLSchedule(context.Background(), mock, "sb-123", "km")
	if err != nil {
		t.Fatalf("DeleteTTLSchedule returned unexpected error: %v", err)
	}
	if !mock.deleteCalled {
		t.Fatal("expected DeleteSchedule to be called on the mock")
	}
	if mock.deleteInput == nil || mock.deleteInput.Name == nil {
		t.Fatal("DeleteSchedule called with nil input or nil Name")
	}
	if *mock.deleteInput.Name != "km-ttl-sb-123" {
		t.Errorf("DeleteSchedule Name = %q; want %q", *mock.deleteInput.Name, "km-ttl-sb-123")
	}
}

func TestDeleteTTLSchedule_NotFound(t *testing.T) {
	// Simulate ResourceNotFoundException — should be treated as idempotent success
	notFoundErr := &types.ResourceNotFoundException{
		Message: stringPtr("Schedule km-ttl-sb-notfound not found"),
	}
	mock := &mockSchedulerAPI{deleteErr: notFoundErr}

	err := kmaws.DeleteTTLSchedule(context.Background(), mock, "sb-notfound", "km")
	if err != nil {
		t.Fatalf("DeleteTTLSchedule should return nil for ResourceNotFoundException, got: %v", err)
	}
}

// TestDeleteTTLSchedule_OtherError verifies that non-NotFound errors are propagated.
func TestDeleteTTLSchedule_OtherError(t *testing.T) {
	otherErr := errors.New("throttled")
	mock := &mockSchedulerAPI{deleteErr: otherErr}

	err := kmaws.DeleteTTLSchedule(context.Background(), mock, "sb-err", "km")
	if err == nil {
		t.Fatal("expected error from DeleteTTLSchedule when SDK returns non-NotFound error")
	}
	if !strings.Contains(err.Error(), "throttled") {
		t.Errorf("expected error to contain 'throttled', got: %v", err)
	}
}

func stringPtr(s string) *string { return &s }

func TestCreateAtSchedule_Success(t *testing.T) {
	input := &scheduler.CreateScheduleInput{
		Name:               stringPtr("km-at-sb-123-kill"),
		ScheduleExpression: stringPtr("at(2026-04-10T12:00:00)"),
	}
	mock := &mockSchedulerAPI{}
	err := kmaws.CreateAtSchedule(context.Background(), mock, input)
	if err != nil {
		t.Fatalf("CreateAtSchedule returned unexpected error: %v", err)
	}
	if !mock.createCalled {
		t.Fatal("expected CreateSchedule to be called on the mock")
	}
}

// TestCreateAtSchedule_GroupAutoCreated covers the self-heal path: the first
// CreateSchedule fails because the named schedule group does not exist, so
// CreateAtSchedule creates the group once and retries the create successfully.
func TestCreateAtSchedule_GroupAutoCreated(t *testing.T) {
	input := &scheduler.CreateScheduleInput{
		Name:      stringPtr("sec-at-sb-123-kill"),
		GroupName: stringPtr("sec-at"),
	}
	notFound := &types.ResourceNotFoundException{Message: stringPtr("Schedule group sec-at does not exist")}
	mock := &mockSchedulerAPI{createErrUntilGroup: notFound}

	err := kmaws.CreateAtSchedule(context.Background(), mock, input)
	if err != nil {
		t.Fatalf("CreateAtSchedule should self-heal a missing group, got: %v", err)
	}
	if !mock.createGroupCalled {
		t.Fatal("expected CreateScheduleGroup to be called when the group is missing")
	}
	if mock.createGroupCount != 1 {
		t.Errorf("expected CreateScheduleGroup called exactly once, got %d", mock.createGroupCount)
	}
	if mock.createGroupInput == nil || mock.createGroupInput.Name == nil || *mock.createGroupInput.Name != "sec-at" {
		t.Errorf("CreateScheduleGroup called with wrong group name: %+v", mock.createGroupInput)
	}
	if mock.createCallCount != 2 {
		t.Errorf("expected CreateSchedule called twice (initial + retry), got %d", mock.createCallCount)
	}
}

// TestCreateAtSchedule_GroupConflictTreatedAsSuccess covers the race where the
// group already exists (or is created concurrently): CreateScheduleGroup returns
// ConflictException, which must be swallowed and the retry must still proceed.
func TestCreateAtSchedule_GroupConflictTreatedAsSuccess(t *testing.T) {
	input := &scheduler.CreateScheduleInput{
		Name:      stringPtr("sec-at-sb-123-kill"),
		GroupName: stringPtr("sec-at"),
	}
	notFound := &types.ResourceNotFoundException{Message: stringPtr("Schedule group sec-at does not exist")}
	conflict := &types.ConflictException{Message: stringPtr("Schedule group sec-at already exists")}
	mock := &mockSchedulerAPI{createErrUntilGroup: notFound, createGroupErr: conflict}

	err := kmaws.CreateAtSchedule(context.Background(), mock, input)
	if err != nil {
		t.Fatalf("CreateAtSchedule should treat ConflictException as success, got: %v", err)
	}
	if !mock.createGroupCalled {
		t.Fatal("expected CreateScheduleGroup to be called")
	}
	if mock.createCallCount != 2 {
		t.Errorf("expected CreateSchedule called twice (initial + retry), got %d", mock.createCallCount)
	}
}

// TestCreateAtSchedule_NoGroupNoSelfHeal verifies that a ResourceNotFoundException
// is NOT acted on when the input has no GroupName (nothing to create).
func TestCreateAtSchedule_NoGroupNoSelfHeal(t *testing.T) {
	input := &scheduler.CreateScheduleInput{
		Name: stringPtr("km-at-sb-123-kill"),
		// no GroupName
	}
	notFound := &types.ResourceNotFoundException{Message: stringPtr("not found")}
	mock := &mockSchedulerAPI{createErr: notFound}

	err := kmaws.CreateAtSchedule(context.Background(), mock, input)
	if err == nil {
		t.Fatal("expected error to propagate when there is no group to create")
	}
	if mock.createGroupCalled {
		t.Error("CreateScheduleGroup must NOT be called when input has no GroupName")
	}
}

// TestCreateAtSchedule_UnrelatedErrorNotHealed verifies that a non-NotFound error
// (e.g. ValidationException) is returned as-is without attempting group creation.
func TestCreateAtSchedule_UnrelatedErrorNotHealed(t *testing.T) {
	input := &scheduler.CreateScheduleInput{
		Name:      stringPtr("sec-at-sb-123-kill"),
		GroupName: stringPtr("sec-at"),
	}
	validationErr := &types.ValidationException{Message: stringPtr("bad schedule expression")}
	mock := &mockSchedulerAPI{createErr: validationErr}

	err := kmaws.CreateAtSchedule(context.Background(), mock, input)
	if err == nil {
		t.Fatal("expected ValidationException to propagate")
	}
	if mock.createGroupCalled {
		t.Error("CreateScheduleGroup must NOT be called for an unrelated error")
	}
	if mock.createCallCount != 1 {
		t.Errorf("expected CreateSchedule called once (no retry), got %d", mock.createCallCount)
	}
}

func TestDeleteAtSchedule_Success(t *testing.T) {
	mock := &mockSchedulerAPI{}
	err := kmaws.DeleteAtSchedule(context.Background(), mock, "km-at-sb-123-kill", "km-at")
	if err != nil {
		t.Fatalf("DeleteAtSchedule returned unexpected error: %v", err)
	}
	if !mock.deleteCalled {
		t.Fatal("expected DeleteSchedule to be called on the mock")
	}
	if mock.deleteInput == nil || mock.deleteInput.Name == nil {
		t.Fatal("DeleteSchedule called with nil input or nil Name")
	}
	if *mock.deleteInput.Name != "km-at-sb-123-kill" {
		t.Errorf("DeleteAtSchedule Name = %q; want %q", *mock.deleteInput.Name, "km-at-sb-123-kill")
	}
}

func TestDeleteAtSchedule_NotFound(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{
		Message: stringPtr("Schedule not found"),
	}
	mock := &mockSchedulerAPI{deleteErr: notFoundErr}
	err := kmaws.DeleteAtSchedule(context.Background(), mock, "km-at-sb-notfound-kill", "km-at")
	if err != nil {
		t.Fatalf("DeleteAtSchedule should return nil for ResourceNotFoundException, got: %v", err)
	}
}
