package aws_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/scheduler/types"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/compiler"
)

// mockSchedulerAPI implements SchedulerAPI for unit tests.
type mockSchedulerAPI struct {
	createCalled bool
	createInput  *scheduler.CreateScheduleInput
	createErr    error

	deleteCalled bool
	deleteInput  *scheduler.DeleteScheduleInput
	deleteErr    error
}

func (m *mockSchedulerAPI) CreateSchedule(ctx context.Context, input *scheduler.CreateScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.CreateScheduleOutput, error) {
	m.createCalled = true
	m.createInput = input
	return &scheduler.CreateScheduleOutput{}, m.createErr
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
	input := compiler.BuildTTLScheduleInput("sb-123", ttlTime, "arn:aws:lambda:us-east-1:123456789012:function:km-ttl", "arn:aws:iam::123456789012:role/km-scheduler-role")

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
	input := compiler.BuildTTLScheduleInput("sb-123", time.Time{}, "arn:...", "arn:...")
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

	err := kmaws.DeleteTTLSchedule(context.Background(), mock, "sb-123")
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

	err := kmaws.DeleteTTLSchedule(context.Background(), mock, "sb-notfound")
	if err != nil {
		t.Fatalf("DeleteTTLSchedule should return nil for ResourceNotFoundException, got: %v", err)
	}
}

// TestDeleteTTLSchedule_OtherError verifies that non-NotFound errors are propagated.
func TestDeleteTTLSchedule_OtherError(t *testing.T) {
	otherErr := errors.New("throttled")
	mock := &mockSchedulerAPI{deleteErr: otherErr}

	err := kmaws.DeleteTTLSchedule(context.Background(), mock, "sb-err")
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
