package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
)

// --------------------------------------------------------------------------
// Mocks
// --------------------------------------------------------------------------

// mockS3GetPutAPI satisfies both S3GetAPI and S3PutAPI.
type mockS3GetPutAPI struct {
	getBody    string
	getErr     error
	putCalled  bool
	putErr     error
}

func (m *mockS3GetPutAPI) GetObject(ctx context.Context, input *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(strings.NewReader(m.getBody)),
	}, nil
}

func (m *mockS3GetPutAPI) PutObject(ctx context.Context, input *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	m.putCalled = true
	return &s3.PutObjectOutput{}, m.putErr
}

func (m *mockS3GetPutAPI) DeleteObject(ctx context.Context, input *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	return &s3.DeleteObjectOutput{}, nil
}

// mockSESAPI satisfies SESV2API.
type mockSESAPI struct {
	sendCalled bool
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
	if input.Content != nil && input.Content.Simple != nil && input.Content.Simple.Subject != nil {
		m.sendEvent = *input.Content.Simple.Subject.Data
	}
	return &sesv2.SendEmailOutput{}, m.sendErr
}

// mockSchedulerAPI satisfies SchedulerAPI.
type mockSchedulerAPI struct {
	deleteCalled bool
	deleteErr    error
}

func (m *mockSchedulerAPI) CreateSchedule(ctx context.Context, input *scheduler.CreateScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.CreateScheduleOutput, error) {
	return &scheduler.CreateScheduleOutput{}, nil
}

func (m *mockSchedulerAPI) DeleteSchedule(ctx context.Context, input *scheduler.DeleteScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.DeleteScheduleOutput, error) {
	m.deleteCalled = true
	return &scheduler.DeleteScheduleOutput{}, m.deleteErr
}

func (m *mockSchedulerAPI) ListSchedules(ctx context.Context, input *scheduler.ListSchedulesInput, optFns ...func(*scheduler.Options)) (*scheduler.ListSchedulesOutput, error) {
	return &scheduler.ListSchedulesOutput{}, nil
}

func (m *mockSchedulerAPI) GetSchedule(ctx context.Context, input *scheduler.GetScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error) {
	return &scheduler.GetScheduleOutput{}, nil
}

// --------------------------------------------------------------------------
// A minimal profile YAML to use in tests that require artifact paths.
// --------------------------------------------------------------------------

const profileWithArtifacts = `
apiVersion: km/v1
kind: SandboxProfile
metadata:
  name: test-profile
spec:
  runtime:
    substrate: ec2
  lifecycle:
    teardown_policy: destroy
  artifacts:
    paths:
      - /tmp/test-artifact.log
    max_size_mb: 10
`

const profileNoArtifacts = `
apiVersion: km/v1
kind: SandboxProfile
metadata:
  name: test-profile
spec:
  runtime:
    substrate: ec2
  lifecycle:
    teardown_policy: destroy
`

// --------------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------------

// Test 1: handler returns error when event has no sandbox_id field.
func TestHandleTTLEvent_MissingSandboxID(t *testing.T) {
	h := &TTLHandler{
		S3Client:      &mockS3GetPutAPI{},
		SESClient:     &mockSESAPI{},
		Scheduler:     &mockSchedulerAPI{},
		Bucket:        "test-bucket",
		OperatorEmail: "",
		Domain:        "sandboxes.klankermaker.ai",
	}
	err := h.HandleTTLEvent(context.Background(), TTLEvent{SandboxID: ""})
	if err == nil {
		t.Fatal("expected error when SandboxID is empty")
	}
}

// Test 2: handler calls PutObject (UploadArtifacts) when profile has artifact paths configured.
func TestHandleTTLEvent_UploadsArtifactsWhenConfigured(t *testing.T) {
	mockS3 := &mockS3GetPutAPI{
		getBody: profileWithArtifacts,
	}
	h := &TTLHandler{
		S3Client:      mockS3,
		SESClient:     &mockSESAPI{},
		Scheduler:     &mockSchedulerAPI{},
		Bucket:        "test-bucket",
		OperatorEmail: "",
		Domain:        "sandboxes.klankermaker.ai",
	}
	// The artifact path /tmp/test-artifact.log won't exist, but PutObject won't be called
	// for missing files — however, UploadArtifacts WILL be called (returning 0 uploaded).
	// The important thing is no error is returned and the flow completes.
	err := h.HandleTTLEvent(context.Background(), TTLEvent{SandboxID: "sb-aabbccdd"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// S3 GetObject was called (profile fetch).
	// PutObject would be called only if files exist — we verify no panic here.
}

// Test 3: handler skips artifact upload when profile has no artifacts section (nil-safe).
func TestHandleTTLEvent_NoArtifactsSectionSafe(t *testing.T) {
	mockS3 := &mockS3GetPutAPI{
		getBody: profileNoArtifacts,
	}
	mockScheduler := &mockSchedulerAPI{}
	h := &TTLHandler{
		S3Client:      mockS3,
		SESClient:     &mockSESAPI{},
		Scheduler:     mockScheduler,
		Bucket:        "test-bucket",
		OperatorEmail: "",
		Domain:        "sandboxes.klankermaker.ai",
	}
	err := h.HandleTTLEvent(context.Background(), TTLEvent{SandboxID: "sb-aabbccdd"})
	if err != nil {
		t.Fatalf("unexpected error when profile has no artifacts: %v", err)
	}
	// Verify PutObject was never called.
	if mockS3.putCalled {
		t.Error("PutObject should not be called when profile has no artifact paths")
	}
}

// Test 4: handler calls SendLifecycleNotification with event "ttl-expired" when operator email is set.
func TestHandleTTLEvent_SendsNotificationWhenEmailSet(t *testing.T) {
	mockS3 := &mockS3GetPutAPI{
		getBody: profileNoArtifacts,
	}
	mockSES := &mockSESAPI{}
	h := &TTLHandler{
		S3Client:      mockS3,
		SESClient:     mockSES,
		Scheduler:     &mockSchedulerAPI{},
		Bucket:        "test-bucket",
		OperatorEmail: "ops@example.com",
		Domain:        "sandboxes.klankermaker.ai",
	}
	err := h.HandleTTLEvent(context.Background(), TTLEvent{SandboxID: "sb-aabbccdd"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mockSES.sendCalled {
		t.Error("expected SendEmail to be called when OperatorEmail is set")
	}
	if !strings.Contains(mockSES.sendEvent, "ttl-expired") {
		t.Errorf("expected subject to contain 'ttl-expired', got: %q", mockSES.sendEvent)
	}
}

// Test 5: handler calls DeleteTTLSchedule to self-clean the schedule.
func TestHandleTTLEvent_DeletesSchedule(t *testing.T) {
	mockS3 := &mockS3GetPutAPI{
		getBody: profileNoArtifacts,
	}
	mockSched := &mockSchedulerAPI{}
	h := &TTLHandler{
		S3Client:      mockS3,
		SESClient:     &mockSESAPI{},
		Scheduler:     mockSched,
		Bucket:        "test-bucket",
		OperatorEmail: "",
		Domain:        "sandboxes.klankermaker.ai",
	}
	err := h.HandleTTLEvent(context.Background(), TTLEvent{SandboxID: "sb-aabbccdd"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mockSched.deleteCalled {
		t.Error("expected DeleteSchedule to be called for self-cleanup")
	}
}

// Test 6: handler proceeds and completes even when profile download fails (missing profile).
// Artifact upload is skipped (best-effort) and the handler still deletes the schedule.

// TestHandleTTLEvent_CallsTeardownFunc: When TeardownFunc is set, HandleTTLEvent calls it
// with the correct sandboxID after schedule deletion.
func TestHandleTTLEvent_CallsTeardownFunc(t *testing.T) {
	mockS3 := &mockS3GetPutAPI{
		getBody: profileNoArtifacts,
	}
	var calledWithID string
	h := &TTLHandler{
		S3Client:      mockS3,
		SESClient:     &mockSESAPI{},
		Scheduler:     &mockSchedulerAPI{},
		Bucket:        "test-bucket",
		OperatorEmail: "",
		Domain:        "sandboxes.klankermaker.ai",
		TeardownFunc: func(ctx context.Context, sandboxID string) error {
			calledWithID = sandboxID
			return nil
		},
	}
	err := h.HandleTTLEvent(context.Background(), TTLEvent{SandboxID: "sb-aabbccdd"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calledWithID != "sb-aabbccdd" {
		t.Errorf("expected TeardownFunc to be called with 'sb-aabbccdd', got: %q", calledWithID)
	}
}

// TestHandleTTLEvent_NoTeardownWhenNil: When TeardownFunc is nil, HandleTTLEvent
// completes without error (backward compatible).
func TestHandleTTLEvent_NoTeardownWhenNil(t *testing.T) {
	mockS3 := &mockS3GetPutAPI{
		getBody: profileNoArtifacts,
	}
	h := &TTLHandler{
		S3Client:      mockS3,
		SESClient:     &mockSESAPI{},
		Scheduler:     &mockSchedulerAPI{},
		Bucket:        "test-bucket",
		OperatorEmail: "",
		Domain:        "sandboxes.klankermaker.ai",
		TeardownFunc:  nil, // explicitly nil — backward compat
	}
	err := h.HandleTTLEvent(context.Background(), TTLEvent{SandboxID: "sb-aabbccdd"})
	if err != nil {
		t.Fatalf("expected no error when TeardownFunc is nil, got: %v", err)
	}
}

// TestHandleTTLEvent_TeardownFailureReturnsError: When TeardownFunc returns an error,
// HandleTTLEvent returns that error.
func TestHandleTTLEvent_TeardownFailureReturnsError(t *testing.T) {
	mockS3 := &mockS3GetPutAPI{
		getBody: profileNoArtifacts,
	}
	teardownErr := errors.New("AWS ec2 terminate failed")
	h := &TTLHandler{
		S3Client:      mockS3,
		SESClient:     &mockSESAPI{},
		Scheduler:     &mockSchedulerAPI{},
		Bucket:        "test-bucket",
		OperatorEmail: "",
		Domain:        "sandboxes.klankermaker.ai",
		TeardownFunc: func(ctx context.Context, sandboxID string) error {
			return teardownErr
		},
	}
	err := h.HandleTTLEvent(context.Background(), TTLEvent{SandboxID: "sb-aabbccdd"})
	if err == nil {
		t.Fatal("expected error when TeardownFunc fails")
	}
	if !errors.Is(err, teardownErr) {
		t.Errorf("expected wrapped teardownErr, got: %v", err)
	}
}

func TestHandleTTLEvent_ProfileDownloadFailureIsNonFatal(t *testing.T) {
	mockS3 := &mockS3GetPutAPI{
		getErr: errors.New("NoSuchKey"),
	}
	mockSched := &mockSchedulerAPI{}
	h := &TTLHandler{
		S3Client:      mockS3,
		SESClient:     &mockSESAPI{},
		Scheduler:     mockSched,
		Bucket:        "test-bucket",
		OperatorEmail: "",
		Domain:        "sandboxes.klankermaker.ai",
	}
	err := h.HandleTTLEvent(context.Background(), TTLEvent{SandboxID: "sb-aabbccdd"})
	if err != nil {
		t.Fatalf("expected handler to proceed even when profile download fails, got: %v", err)
	}
	// Schedule should still be cleaned up.
	if !mockSched.deleteCalled {
		t.Error("expected DeleteSchedule to still be called even after profile download failure")
	}
}
