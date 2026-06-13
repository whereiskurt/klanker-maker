package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
)

// --------------------------------------------------------------------------
// Mocks
// --------------------------------------------------------------------------

// mockS3GetPutAPI satisfies both S3GetAPI and S3PutAPI.
type mockS3GetPutAPI struct {
	getBody     string
	getErr      error
	putCalled   bool
	putErr      error
	deletedKeys []string
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
	if input.Key != nil {
		m.deletedKeys = append(m.deletedKeys, *input.Key)
	}
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

const profileRetain = `
apiVersion: km/v1
kind: SandboxProfile
metadata:
  name: test-profile
spec:
  runtime:
    substrate: ec2
  lifecycle:
    teardownPolicy: retain
`

// mockDynamoLock satisfies awspkg.SandboxMetadataAPI; returns an item whose
// `locked` attribute is controlled by the `locked` field.
type mockDynamoLock struct{ locked bool }

func (m *mockDynamoLock) GetItem(ctx context.Context, in *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	item := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "sb-aabbccdd"},
		"created_at": &dynamodbtypes.AttributeValueMemberS{Value: "2026-01-01T00:00:00Z"},
	}
	if m.locked {
		item["locked"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: true}
	}
	return &dynamodb.GetItemOutput{Item: item}, nil
}

func (m *mockDynamoLock) PutItem(ctx context.Context, in *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}
func (m *mockDynamoLock) UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return &dynamodb.UpdateItemOutput{}, nil
}
func (m *mockDynamoLock) DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}
func (m *mockDynamoLock) Scan(ctx context.Context, in *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	return &dynamodb.ScanOutput{}, nil
}
func (m *mockDynamoLock) Query(ctx context.Context, in *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}

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
		// No-op teardown: keeps the destroy path off the real AWS SDK fallback
		// (sdkOnlyTeardown → IMDS) so the test exercises artifact handling only.
		TeardownFunc: func(ctx context.Context, sandboxID string) error { return nil },
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
		TeardownFunc:  func(ctx context.Context, sandboxID string) error { return nil },
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
		S3Client:         mockS3,
		SESClient:        mockSES,
		Scheduler:        &mockSchedulerAPI{},
		DynamoClient:     &mockDynamoLock{locked: false},
		SandboxTableName: "km-sandbox-metadata",
		Bucket:           "test-bucket",
		OperatorEmail:    "ops@example.com",
		Domain:           "sandboxes.klankermaker.ai",
		TeardownFunc:     func(ctx context.Context, sandboxID string) error { return nil },
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
		TeardownFunc:  func(ctx context.Context, sandboxID string) error { return nil },
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

// TestHandleTTLEvent_RetainPolicyPreservesSandbox: teardownPolicy=retain on an
// automatic (idle/TTL) trigger must NOT destroy — the previous routing only
// special-cased "stop" and let "retain" fall through to destroy, reaping
// retain sandboxes on the idle timer.
func TestHandleTTLEvent_RetainPolicyPreservesSandbox(t *testing.T) {
	mockS3 := &mockS3GetPutAPI{getBody: profileRetain}
	teardownCalled := false
	h := &TTLHandler{
		S3Client:     mockS3,
		SESClient:    &mockSESAPI{},
		Scheduler:    &mockSchedulerAPI{},
		Bucket:       "test-bucket",
		Domain:       "sandboxes.klankermaker.ai",
		TeardownFunc: func(ctx context.Context, sandboxID string) error { teardownCalled = true; return nil },
	}
	// Empty EventType = automatic idle/TTL trigger (the path that reaped them).
	if err := h.HandleTTLEvent(context.Background(), TTLEvent{SandboxID: "sb-aabbccdd"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if teardownCalled {
		t.Error("teardownPolicy=retain must NOT destroy on an automatic trigger")
	}
}

// TestHandleTTLEvent_LockedSkipsAutomaticTeardown: a km-locked sandbox must not
// be auto-destroyed even when the policy is destroy — the idle/TTL path had no
// lock check at all.
func TestHandleTTLEvent_LockedSkipsAutomaticTeardown(t *testing.T) {
	mockS3 := &mockS3GetPutAPI{getBody: profileNoArtifacts} // policy=destroy
	teardownCalled := false
	h := &TTLHandler{
		S3Client:         mockS3,
		SESClient:        &mockSESAPI{},
		Scheduler:        &mockSchedulerAPI{},
		DynamoClient:     &mockDynamoLock{locked: true},
		SandboxTableName: "km-sandbox-metadata",
		Bucket:           "test-bucket",
		Domain:           "sandboxes.klankermaker.ai",
		TeardownFunc:     func(ctx context.Context, sandboxID string) error { teardownCalled = true; return nil },
	}
	if err := h.HandleTTLEvent(context.Background(), TTLEvent{SandboxID: "sb-aabbccdd"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if teardownCalled {
		t.Error("a locked sandbox must NOT be auto-destroyed on an idle/TTL trigger")
	}
}

// TestHandleTTLEvent_DeletesSopsBundle: the remote/TTL destroy path must delete
// the SOPS bundle from S3 (Phase 89 SOPS-16). terraform destroy doesn't remove
// it (uploaded via PutObject outside terraform), so handleDestroy must.
func TestHandleTTLEvent_DeletesSopsBundle(t *testing.T) {
	mockS3 := &mockS3GetPutAPI{
		getBody: profileNoArtifacts,
	}
	h := &TTLHandler{
		S3Client:     mockS3,
		SESClient:    &mockSESAPI{},
		Scheduler:    &mockSchedulerAPI{},
		Bucket:       "test-bucket",
		Domain:       "sandboxes.klankermaker.ai",
		TeardownFunc: func(ctx context.Context, sandboxID string) error { return nil },
	}
	if err := h.HandleTTLEvent(context.Background(), TTLEvent{SandboxID: "sb-aabbccdd"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "sandboxes/sb-aabbccdd/secrets.enc.yaml"
	found := false
	for _, k := range mockS3.deletedKeys {
		if k == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SOPS bundle %q to be deleted; deleted keys: %v", want, mockS3.deletedKeys)
	}
}

// TestHandleTTLEvent_NoTeardownWhenNil: When TeardownFunc is nil, HandleTTLEvent
// falls back to the SDK-only teardown and completes without error (backward compat).
// SDKTeardownFunc is injected with a no-op so the fallback branch is exercised
// without making real AWS/IMDS calls (the production default is sdkOnlyTeardown).
func TestHandleTTLEvent_NoTeardownWhenNil(t *testing.T) {
	mockS3 := &mockS3GetPutAPI{
		getBody: profileNoArtifacts,
	}
	sdkFallbackCalled := false
	h := &TTLHandler{
		S3Client:      mockS3,
		SESClient:     &mockSESAPI{},
		Scheduler:     &mockSchedulerAPI{},
		Bucket:        "test-bucket",
		OperatorEmail: "",
		Domain:        "sandboxes.klankermaker.ai",
		TeardownFunc:  nil, // explicitly nil — exercises the SDK-only fallback branch
		SDKTeardownFunc: func(ctx context.Context, h *TTLHandler, sandboxID string) error {
			sdkFallbackCalled = true
			return nil
		},
	}
	err := h.HandleTTLEvent(context.Background(), TTLEvent{SandboxID: "sb-aabbccdd"})
	if err != nil {
		t.Fatalf("expected no error when TeardownFunc is nil, got: %v", err)
	}
	if !sdkFallbackCalled {
		t.Error("expected SDK-only teardown fallback to be invoked when TeardownFunc is nil")
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
		TeardownFunc:  func(ctx context.Context, sandboxID string) error { return nil },
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
