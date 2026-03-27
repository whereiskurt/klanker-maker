package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
)

// --------------------------------------------------------------------------
// Mocks
// --------------------------------------------------------------------------

// mockS3GetAPI satisfies S3GetAPI for the create handler.
type mockS3GetAPI struct {
	getBody string
	getErr  error
}

func (m *mockS3GetAPI) GetObject(ctx context.Context, input *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(strings.NewReader(m.getBody)),
	}, nil
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

// minimalProfile is a valid SandboxProfile YAML used in tests.
const minimalProfile = `
apiVersion: km/v1
kind: SandboxProfile
metadata:
  name: test-profile
spec:
  runtime:
    substrate: ec2
    region: us-east-1
  lifecycle:
    teardown_policy: destroy
`

// --------------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------------

// TestCreateEvent_JSONRoundTrip verifies CreateEvent marshals/unmarshals correctly.
func TestCreateEvent_JSONRoundTrip(t *testing.T) {
	orig := CreateEvent{
		SandboxID:      "sb-abc123",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-abc123",
		OperatorEmail:  "user@example.com",
		OnDemand:       true,
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal CreateEvent: %v", err)
	}

	var got CreateEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal CreateEvent: %v", err)
	}

	if got.SandboxID != orig.SandboxID {
		t.Errorf("SandboxID: got %q, want %q", got.SandboxID, orig.SandboxID)
	}
	if got.ArtifactBucket != orig.ArtifactBucket {
		t.Errorf("ArtifactBucket: got %q, want %q", got.ArtifactBucket, orig.ArtifactBucket)
	}
	if got.ArtifactPrefix != orig.ArtifactPrefix {
		t.Errorf("ArtifactPrefix: got %q, want %q", got.ArtifactPrefix, orig.ArtifactPrefix)
	}
	if got.OperatorEmail != orig.OperatorEmail {
		t.Errorf("OperatorEmail: got %q, want %q", got.OperatorEmail, orig.OperatorEmail)
	}
	if got.OnDemand != orig.OnDemand {
		t.Errorf("OnDemand: got %v, want %v", got.OnDemand, orig.OnDemand)
	}
}

// TestCreateHandler_HappyPath verifies Handle downloads profile, invokes km subprocess,
// and returns nil on success without sending SES notification.
func TestCreateHandler_HappyPath(t *testing.T) {
	mockS3 := &mockS3GetAPI{getBody: minimalProfile}
	mockSES := &mockSESAPI{}

	commandRan := false
	h := &CreateHandler{
		S3Client:      mockS3,
		SESClient:     mockSES,
		Domain:        "sandboxes.example.com",
		KMBinaryPath:  "/bin/true", // always succeeds
		RunCommand: func(cmd string, args []string, env []string) ([]byte, error) {
			commandRan = true
			return []byte("sandbox created"), nil
		},
	}

	event := CreateEvent{
		SandboxID:      "sb-happypath",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-happypath",
		OperatorEmail:  "user@example.com",
	}

	err := h.Handle(context.Background(), event)
	if err != nil {
		t.Fatalf("Handle returned unexpected error: %v", err)
	}
	if !commandRan {
		t.Error("expected km subprocess to be invoked")
	}
	// km create handles the "created" notification — handler must NOT send it again
	if mockSES.sendCalled {
		t.Error("Handle must NOT send duplicate 'created' notification — km create already sends it")
	}
}

// TestCreateHandler_FailurePath verifies Handle sends "create-failed" notification via SES
// when the km subprocess returns an error.
func TestCreateHandler_FailurePath(t *testing.T) {
	mockS3 := &mockS3GetAPI{getBody: minimalProfile}
	mockSES := &mockSESAPI{}

	h := &CreateHandler{
		S3Client:     mockS3,
		SESClient:    mockSES,
		Domain:       "sandboxes.example.com",
		KMBinaryPath: "/bin/false",
		RunCommand: func(cmd string, args []string, env []string) ([]byte, error) {
			return []byte("exit status 1"), errExecFailed
		},
	}

	event := CreateEvent{
		SandboxID:      "sb-failpath",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-failpath",
		OperatorEmail:  "user@example.com",
	}

	err := h.Handle(context.Background(), event)
	if err == nil {
		t.Fatal("expected error from Handle when subprocess fails")
	}
	if !mockSES.sendCalled {
		t.Error("expected SES create-failed notification to be sent when subprocess fails")
	}
	if !strings.Contains(mockSES.sendEvent, "create-failed") {
		t.Errorf("expected SES event to contain 'create-failed', got: %s", mockSES.sendEvent)
	}
}

// TestCreateHandler_OnDemandFlag verifies --on-demand is appended when OnDemand is true.
func TestCreateHandler_OnDemandFlag(t *testing.T) {
	mockS3 := &mockS3GetAPI{getBody: minimalProfile}

	var capturedArgs []string
	h := &CreateHandler{
		S3Client:     mockS3,
		SESClient:    &mockSESAPI{},
		Domain:       "sandboxes.example.com",
		KMBinaryPath: "/bin/true",
		RunCommand: func(cmd string, args []string, env []string) ([]byte, error) {
			capturedArgs = args
			return []byte("ok"), nil
		},
	}

	event := CreateEvent{
		SandboxID:      "sb-ondemand",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-ondemand",
		OnDemand:       true,
	}

	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle returned unexpected error: %v", err)
	}

	hasOnDemand := false
	for _, arg := range capturedArgs {
		if arg == "--on-demand" {
			hasOnDemand = true
			break
		}
	}
	if !hasOnDemand {
		t.Errorf("expected --on-demand in args when event.OnDemand=true, got: %v", capturedArgs)
	}
}
