package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// wrapEvent wraps a CreateEvent in a CloudWatchEvent envelope for testing.
func wrapEvent(ce CreateEvent) events.CloudWatchEvent {
	detail, _ := json.Marshal(ce)
	return events.CloudWatchEvent{
		Source:     "km.sandbox",
		DetailType: "SandboxCreate",
		Detail:     json.RawMessage(detail),
	}
}

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

// mockSandboxMetadataAPI is a local stub for the SandboxMetadataAPI interface used by
// cmd/create-handler tests. The package-private mock in pkg/aws/sandbox_dynamo_test.go
// (package aws_test) cannot be imported, so this is a minimal copy of the interface.
type mockSandboxMetadataAPI struct {
	getInput    *dynamodb.GetItemInput
	putInput    *dynamodb.PutItemInput
	updateInput *dynamodb.UpdateItemInput
	deleteInput *dynamodb.DeleteItemInput
	scanInput   *dynamodb.ScanInput
	queryInput  *dynamodb.QueryInput

	getOutput   *dynamodb.GetItemOutput
	putErr      error
	updateErr   error
	deleteErr   error
	scanOutput  *dynamodb.ScanOutput
	queryOutput *dynamodb.QueryOutput
}

func (m *mockSandboxMetadataAPI) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	m.getInput = input
	if m.getOutput != nil {
		return m.getOutput, nil
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (m *mockSandboxMetadataAPI) PutItem(_ context.Context, input *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	m.putInput = input
	return &dynamodb.PutItemOutput{}, m.putErr
}

func (m *mockSandboxMetadataAPI) UpdateItem(_ context.Context, input *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	m.updateInput = input
	return &dynamodb.UpdateItemOutput{}, m.updateErr
}

func (m *mockSandboxMetadataAPI) DeleteItem(_ context.Context, input *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	m.deleteInput = input
	return &dynamodb.DeleteItemOutput{}, m.deleteErr
}

func (m *mockSandboxMetadataAPI) Scan(_ context.Context, input *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	m.scanInput = input
	if m.scanOutput != nil {
		return m.scanOutput, nil
	}
	return &dynamodb.ScanOutput{}, nil
}

func (m *mockSandboxMetadataAPI) Query(_ context.Context, input *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	m.queryInput = input
	if m.queryOutput != nil {
		return m.queryOutput, nil
	}
	return &dynamodb.QueryOutput{}, nil
}

// compile-time check that mockSandboxMetadataAPI satisfies awspkg.SandboxMetadataAPI.
var _ awspkg.SandboxMetadataAPI = (*mockSandboxMetadataAPI)(nil)

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
		ToolchainDir:  "/tmp", // test toolchain dir
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

	err := h.Handle(context.Background(), wrapEvent(event))
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

// fakeEC2DescribeAPI satisfies EC2DescribeAPI for the Bug J idempotency guard tests.
type fakeEC2DescribeAPI struct {
	instanceID string // non-empty => returns one matching instance
	err        error
}

func (f *fakeEC2DescribeAPI) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.instanceID == "" {
		return &ec2.DescribeInstancesOutput{}, nil
	}
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{
			{Instances: []ec2types.Instance{{InstanceId: aws.String(f.instanceID)}}},
		},
	}, nil
}

// TestCreateHandler_IdempotentRetry_SkipsWhenInstanceExists verifies the Bug J guard:
// an EventBridge retry for a sandbox-id that already has a provisioned instance does NOT
// re-run the km create subprocess (no duplicate box).
func TestCreateHandler_IdempotentRetry_SkipsWhenInstanceExists(t *testing.T) {
	commandRan := false
	h := &CreateHandler{
		S3Client:     &mockS3GetAPI{getBody: minimalProfile},
		SESClient:    &mockSESAPI{},
		EC2Client:    &fakeEC2DescribeAPI{instanceID: "i-existing"},
		Domain:       "sandboxes.example.com",
		ToolchainDir: "/tmp",
		RunCommand: func(cmd string, args []string, env []string) ([]byte, error) {
			commandRan = true
			return []byte("sandbox created"), nil
		},
	}
	event := CreateEvent{SandboxID: "sb-dup", ArtifactBucket: "km-artifacts", ArtifactPrefix: "remote-create/sb-dup"}
	if err := h.Handle(context.Background(), wrapEvent(event)); err != nil {
		t.Fatalf("Handle returned unexpected error: %v", err)
	}
	if commandRan {
		t.Error("expected the create subprocess to be SKIPPED when an instance already exists (idempotent retry)")
	}
}

// TestCreateHandler_FirstCreate_ProceedsWhenNoInstance verifies the guard does NOT block a
// genuine first create (no existing instance) or a transient DescribeInstances error.
func TestCreateHandler_FirstCreate_ProceedsWhenNoInstance(t *testing.T) {
	for _, tc := range []struct {
		name string
		ec2  *fakeEC2DescribeAPI
	}{
		{"no instance", &fakeEC2DescribeAPI{}},
		{"describe error fails open", &fakeEC2DescribeAPI{err: errors.New("transient")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			commandRan := false
			h := &CreateHandler{
				S3Client:     &mockS3GetAPI{getBody: minimalProfile},
				SESClient:    &mockSESAPI{},
				EC2Client:    tc.ec2,
				Domain:       "sandboxes.example.com",
				ToolchainDir: "/tmp",
				RunCommand: func(cmd string, args []string, env []string) ([]byte, error) {
					commandRan = true
					return []byte("sandbox created"), nil
				},
			}
			event := CreateEvent{SandboxID: "sb-first", ArtifactBucket: "km-artifacts", ArtifactPrefix: "remote-create/sb-first"}
			if err := h.Handle(context.Background(), wrapEvent(event)); err != nil {
				t.Fatalf("Handle returned unexpected error: %v", err)
			}
			if !commandRan {
				t.Error("expected the create subprocess to run (guard must not block a first create)")
			}
		})
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
		ToolchainDir: "/bin/false",
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

	err := h.Handle(context.Background(), wrapEvent(event))
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
		ToolchainDir: "/bin/true",
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

	if err := h.Handle(context.Background(), wrapEvent(event)); err != nil {
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

// TestCreateHandler_AliasForwarded verifies --alias is passed to the subprocess
// when the event includes an alias override.
func TestCreateHandler_AliasForwarded(t *testing.T) {
	mockS3 := &mockS3GetAPI{getBody: minimalProfile}

	var capturedArgs []string
	h := &CreateHandler{
		S3Client:     mockS3,
		SESClient:    &mockSESAPI{},
		Domain:       "sandboxes.example.com",
		ToolchainDir: "/bin/true",
		RunCommand: func(cmd string, args []string, env []string) ([]byte, error) {
			capturedArgs = args
			return []byte("ok"), nil
		},
	}

	event := CreateEvent{
		SandboxID:      "sb-aliasforward",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-aliasforward",
		Alias:          "cc1",
	}

	if err := h.Handle(context.Background(), wrapEvent(event)); err != nil {
		t.Fatalf("Handle returned unexpected error: %v", err)
	}

	hasAlias := false
	for i, arg := range capturedArgs {
		if arg == "--alias" && i+1 < len(capturedArgs) && capturedArgs[i+1] == "cc1" {
			hasAlias = true
			break
		}
	}
	if !hasAlias {
		t.Errorf("expected --alias cc1 in args, got: %v", capturedArgs)
	}
}

// --------------------------------------------------------------------------
// extractFailureReason tests (Phase 77, Task 1)
// --------------------------------------------------------------------------

// TestExtractFailureReason_LastErrorLine verifies bottom-up scan returns the LAST "Error:" line.
func TestExtractFailureReason_LastErrorLine(t *testing.T) {
	input := "some preamble\nError: first error\nmiddle noise\nError: actual root cause\ntrailing noise"
	got := extractFailureReason(input)
	want := "Error: actual root cause"
	if got != want {
		t.Errorf("extractFailureReason: got %q, want %q", got, want)
	}
}

// TestExtractFailureReason_NoErrorLine_TailFallback verifies the no-Error-line fallback
// prefixes with the marker and contains the tail of the input.
func TestExtractFailureReason_NoErrorLine_TailFallback(t *testing.T) {
	input := "timeout exceeded\nstack trace junk\nexit code 1"
	got := extractFailureReason(input)
	const marker = "<no error line; tail of subprocess output> "
	if !strings.HasPrefix(got, marker) {
		t.Errorf("extractFailureReason: expected prefix %q, got %q", marker, got)
	}
	if !strings.Contains(got, "exit code 1") {
		t.Errorf("extractFailureReason: expected tail to contain 'exit code 1', got %q", got)
	}
}

// TestExtractFailureReason_TrimsTo1024 verifies the result is capped at 1024 chars.
func TestExtractFailureReason_TrimsTo1024(t *testing.T) {
	line := "Error: " + strings.Repeat("X", 2000)
	got := extractFailureReason(line)
	if len(got) > 1024 {
		t.Errorf("extractFailureReason: result length %d exceeds 1024", len(got))
	}
	if !strings.HasPrefix(got, "Error:") {
		t.Errorf("extractFailureReason: expected result to start with 'Error:', got %q", got[:min(20, len(got))])
	}
}

// TestExtractFailureReason_EmptyInput verifies empty input returns empty string.
func TestExtractFailureReason_EmptyInput(t *testing.T) {
	got := extractFailureReason("")
	// Empty input: no Error: line, tail fallback with empty tail — marker + ""
	// We accept either empty or marker-prefixed empty tail; document: returns marker+"".
	const marker = "<no error line; tail of subprocess output> "
	if got != "" && got != marker {
		t.Errorf("extractFailureReason: empty input got %q, want empty string or marker", got)
	}
}

// TestExtractFailureReason_TrailingWhitespace verifies trailing blank lines do not
// prevent finding an Error: line (bottom-up scan skips blank trailing lines).
func TestExtractFailureReason_TrailingWhitespace(t *testing.T) {
	input := "preamble\nError: the real cause\n\n\n"
	got := extractFailureReason(input)
	want := "Error: the real cause"
	if got != want {
		t.Errorf("extractFailureReason: got %q, want %q", got, want)
	}
}

// min is a local helper for Go 1.20 compatibility.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestCreateHandler_NoAliasWhenEmpty verifies --alias is NOT passed when event
// has no alias set.
func TestCreateHandler_NoAliasWhenEmpty(t *testing.T) {
	mockS3 := &mockS3GetAPI{getBody: minimalProfile}

	var capturedArgs []string
	h := &CreateHandler{
		S3Client:     mockS3,
		SESClient:    &mockSESAPI{},
		Domain:       "sandboxes.example.com",
		ToolchainDir: "/bin/true",
		RunCommand: func(cmd string, args []string, env []string) ([]byte, error) {
			capturedArgs = args
			return []byte("ok"), nil
		},
	}

	event := CreateEvent{
		SandboxID:      "sb-noalias",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-noalias",
	}

	if err := h.Handle(context.Background(), wrapEvent(event)); err != nil {
		t.Fatalf("Handle returned unexpected error: %v", err)
	}

	for _, arg := range capturedArgs {
		if arg == "--alias" {
			t.Errorf("expected no --alias in args when event.Alias is empty, got: %v", capturedArgs)
			break
		}
	}
}

// --------------------------------------------------------------------------
// Branch tests for failure reason persistence (Phase 77, Task 2)
// --------------------------------------------------------------------------

// TestCreateHandler_FailurePath_WritesFailureReason verifies that on subprocess failure
// the handler calls UpdateSandboxStatusAndReasonDynamo with status="failed",
// the extracted reason, and a parseable RFC3339 timestamp.
func TestCreateHandler_FailurePath_WritesFailureReason(t *testing.T) {
	mockS3 := &mockS3GetAPI{getBody: minimalProfile}
	mockDynamo := &mockSandboxMetadataAPI{}

	subprocErr := errors.New("exit status 1")
	subprocOut := []byte("some preamble\nError: provision Slack channel: archived\nmore noise")

	h := &CreateHandler{
		S3Client:     mockS3,
		SESClient:    &mockSESAPI{},
		DynamoClient: mockDynamo,
		TableName:    "km-sandboxes",
		Domain:       "sandboxes.example.com",
		ToolchainDir: "/tmp",
		RunCommand: func(cmd string, args []string, env []string) ([]byte, error) {
			return subprocOut, subprocErr
		},
	}

	event := CreateEvent{
		SandboxID:      "sb-failreason",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-failreason",
		OperatorEmail:  "op@example.com",
	}

	err := h.Handle(context.Background(), wrapEvent(event))
	if err == nil {
		t.Fatal("expected Handle to return an error on subprocess failure")
	}
	if !errors.Is(err, subprocErr) {
		t.Errorf("expected wrapped subprocErr, got: %v", err)
	}

	if mockDynamo.updateInput == nil {
		t.Fatal("expected DynamoDB UpdateItem to be called, but updateInput is nil")
	}

	expr := *mockDynamo.updateInput.UpdateExpression
	if !strings.Contains(expr, "failure_reason") {
		t.Errorf("UpdateExpression missing 'failure_reason': %q", expr)
	}
	if !strings.Contains(expr, "failed_at") {
		t.Errorf("UpdateExpression missing 'failed_at': %q", expr)
	}

	statusVal, ok := mockDynamo.updateInput.ExpressionAttributeValues[":status"]
	if !ok {
		t.Fatal("missing :status in ExpressionAttributeValues")
	}
	statusS, ok := statusVal.(*dynamodbtypes.AttributeValueMemberS)
	if !ok || statusS.Value != "failed" {
		t.Errorf("expected :status = 'failed', got: %v", statusVal)
	}

	reasonVal, ok := mockDynamo.updateInput.ExpressionAttributeValues[":reason"]
	if !ok {
		t.Fatal("missing :reason in ExpressionAttributeValues")
	}
	reasonS, ok := reasonVal.(*dynamodbtypes.AttributeValueMemberS)
	if !ok || reasonS.Value != "Error: provision Slack channel: archived" {
		t.Errorf("expected :reason = 'Error: provision Slack channel: archived', got: %v", reasonVal)
	}

	tsVal, ok := mockDynamo.updateInput.ExpressionAttributeValues[":ts"]
	if !ok {
		t.Fatal("missing :ts in ExpressionAttributeValues")
	}
	tsS, ok := tsVal.(*dynamodbtypes.AttributeValueMemberS)
	if !ok || tsS.Value == "" {
		t.Errorf("expected :ts to be a non-empty string, got: %v", tsVal)
	}
	if _, parseErr := time.Parse(time.RFC3339, tsS.Value); parseErr != nil {
		t.Errorf("expected :ts to be RFC3339-parseable, got %q: %v", tsS.Value, parseErr)
	}
}

// TestCreateHandler_NocapPath_WritesFailureReason verifies that capacity errors
// set status="nocap" and the extracted reason is persisted via UpdateSandboxStatusAndReasonDynamo.
func TestCreateHandler_NocapPath_WritesFailureReason(t *testing.T) {
	mockS3 := &mockS3GetAPI{getBody: minimalProfile}
	mockDynamo := &mockSandboxMetadataAPI{}

	subprocErr := errors.New("exit status 1")
	subprocOut := []byte("InsufficientInstanceCapacity\nError: no spot capacity in az us-east-1c")

	h := &CreateHandler{
		S3Client:     mockS3,
		SESClient:    &mockSESAPI{},
		DynamoClient: mockDynamo,
		TableName:    "km-sandboxes",
		Domain:       "sandboxes.example.com",
		ToolchainDir: "/tmp",
		RunCommand: func(cmd string, args []string, env []string) ([]byte, error) {
			return subprocOut, subprocErr
		},
	}

	event := CreateEvent{
		SandboxID:      "sb-nocap",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-nocap",
		OperatorEmail:  "op@example.com",
	}

	err := h.Handle(context.Background(), wrapEvent(event))
	if err == nil {
		t.Fatal("expected Handle to return an error on subprocess failure")
	}

	if mockDynamo.updateInput == nil {
		t.Fatal("expected DynamoDB UpdateItem to be called, but updateInput is nil")
	}

	statusVal, ok := mockDynamo.updateInput.ExpressionAttributeValues[":status"]
	if !ok {
		t.Fatal("missing :status in ExpressionAttributeValues")
	}
	statusS, ok := statusVal.(*dynamodbtypes.AttributeValueMemberS)
	if !ok || statusS.Value != "nocap" {
		t.Errorf("expected :status = 'nocap', got: %v", statusVal)
	}

	reasonVal, ok := mockDynamo.updateInput.ExpressionAttributeValues[":reason"]
	if !ok {
		t.Fatal("missing :reason in ExpressionAttributeValues")
	}
	reasonS, ok := reasonVal.(*dynamodbtypes.AttributeValueMemberS)
	if !ok || reasonS.Value != "Error: no spot capacity in az us-east-1c" {
		t.Errorf("expected :reason = 'Error: no spot capacity in az us-east-1c', got: %v", reasonVal)
	}
}

// TestCreateHandler_FailurePath_NoErrorLine_StillWritesTailReason verifies that when
// subprocess output has no "Error:" prefix, the tail-fallback reason is still persisted.
func TestCreateHandler_FailurePath_NoErrorLine_StillWritesTailReason(t *testing.T) {
	mockS3 := &mockS3GetAPI{getBody: minimalProfile}
	mockDynamo := &mockSandboxMetadataAPI{}

	subprocErr := errors.New("exit status 1")
	subprocOut := []byte("oh no the sky fell")

	h := &CreateHandler{
		S3Client:     mockS3,
		SESClient:    &mockSESAPI{},
		DynamoClient: mockDynamo,
		TableName:    "km-sandboxes",
		Domain:       "sandboxes.example.com",
		ToolchainDir: "/tmp",
		RunCommand: func(cmd string, args []string, env []string) ([]byte, error) {
			return subprocOut, subprocErr
		},
	}

	event := CreateEvent{
		SandboxID:      "sb-notailreason",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-notailreason",
	}

	err := h.Handle(context.Background(), wrapEvent(event))
	if err == nil {
		t.Fatal("expected Handle to return an error on subprocess failure")
	}

	if mockDynamo.updateInput == nil {
		t.Fatal("expected DynamoDB UpdateItem to be called, but updateInput is nil")
	}

	reasonVal, ok := mockDynamo.updateInput.ExpressionAttributeValues[":reason"]
	if !ok {
		t.Fatal("missing :reason in ExpressionAttributeValues")
	}
	reasonS, ok := reasonVal.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatalf("expected :reason to be a string attribute, got: %T", reasonVal)
	}
	const marker = "<no error line; tail of subprocess output> "
	if !strings.HasPrefix(reasonS.Value, marker) {
		t.Errorf("expected :reason to start with tail marker, got: %q", reasonS.Value)
	}
}

// TestCreateHandler_DDBWriteFailure_NonFatal verifies that a DynamoDB write error does NOT
// cause Handle to return the DDB error — it logs a warning and returns the original runErr.
func TestCreateHandler_DDBWriteFailure_NonFatal(t *testing.T) {
	mockS3 := &mockS3GetAPI{getBody: minimalProfile}
	mockDynamo := &mockSandboxMetadataAPI{updateErr: errors.New("ddb throttled")}

	subprocErr := errors.New("exit status 1")

	h := &CreateHandler{
		S3Client:     mockS3,
		SESClient:    &mockSESAPI{},
		DynamoClient: mockDynamo,
		TableName:    "km-sandboxes",
		Domain:       "sandboxes.example.com",
		ToolchainDir: "/tmp",
		RunCommand: func(cmd string, args []string, env []string) ([]byte, error) {
			return []byte("some output"), subprocErr
		},
	}

	event := CreateEvent{
		SandboxID:      "sb-ddbfail",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-ddbfail",
	}

	err := h.Handle(context.Background(), wrapEvent(event))
	if err == nil {
		t.Fatal("expected Handle to return an error")
	}
	// The handler must return the wrapped subprocess error, NOT the DDB error.
	if !errors.Is(err, subprocErr) {
		t.Errorf("expected wrapped subprocErr in return value, got: %v", err)
	}
}

// --------------------------------------------------------------------------
// Phase 89 SOPS bundle bridge — patchProfileForSops
// --------------------------------------------------------------------------

// multiKeyS3 is an S3GetAPI mock that routes by S3 key, supporting both the
// profile and the SOPS bundle in a single test.
type multiKeyS3 struct {
	bodies map[string]string
	errs   map[string]error
}

func (m *multiKeyS3) GetObject(ctx context.Context, input *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := ""
	if input.Key != nil {
		key = *input.Key
	}
	if err, ok := m.errs[key]; ok && err != nil {
		return nil, err
	}
	body, ok := m.bodies[key]
	if !ok {
		return nil, errors.New("not found: " + key)
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestPatchProfileForSops_NoSecrets_NoOp(t *testing.T) {
	profileYAML := `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: nosec
spec:
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
  network:
    enforcement: proxy
`
	mock := &multiKeyS3{bodies: map[string]string{}}
	out, bundlePath, err := patchProfileForSops(context.Background(), mock, "bkt", "remote-create/sb-x", "sb-x", []byte(profileYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundlePath != "" {
		t.Errorf("expected empty bundlePath when no secrets, got %q", bundlePath)
	}
	if string(out) != profileYAML {
		t.Errorf("expected unchanged profile bytes when no secrets; got diff")
	}
}

func TestPatchProfileForSops_WithSecrets_RewritesToAbsolutePath(t *testing.T) {
	profileYAML := `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: codex
spec:
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
  network:
    enforcement: proxy
  secrets:
    sopsFile: ./secrets/codex.enc.yaml
`
	bundleContent := "OPENAI_API_KEY: ENC[AES256_GCM,data:fake]\nsops:\n  version: 3.11.0\n"
	mock := &multiKeyS3{
		bodies: map[string]string{
			"remote-create/sb-xyz/.km-secrets-bundle.enc.yaml": bundleContent,
		},
	}

	out, bundlePath, err := patchProfileForSops(context.Background(), mock, "bkt", "remote-create/sb-xyz", "sb-xyz", []byte(profileYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() {
		if bundlePath != "" {
			_ = os.Remove(bundlePath)
		}
	}()

	expectedBundle := "/tmp/sb-xyz-secrets.enc.yaml"
	if bundlePath != expectedBundle {
		t.Errorf("bundlePath = %q, want %q", bundlePath, expectedBundle)
	}

	// Bundle must exist on disk with the downloaded content.
	got, readErr := os.ReadFile(bundlePath)
	if readErr != nil {
		t.Fatalf("bundle not written to %s: %v", bundlePath, readErr)
	}
	if string(got) != bundleContent {
		t.Errorf("bundle content mismatch:\n got: %q\nwant: %q", got, bundleContent)
	}

	// Patched profile must reference the absolute path, not the relative one.
	if !strings.Contains(string(out), expectedBundle) {
		t.Errorf("patched profile missing absolute bundle path %q:\n%s", expectedBundle, out)
	}
	if strings.Contains(string(out), "./secrets/codex.enc.yaml") {
		t.Errorf("patched profile still has original relative path:\n%s", out)
	}
}

func TestPatchProfileForSops_DownloadError_Propagates(t *testing.T) {
	profileYAML := `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: codex
spec:
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
  network:
    enforcement: proxy
  secrets:
    sopsFile: ./secrets/codex.enc.yaml
`
	mock := &multiKeyS3{
		bodies: map[string]string{},
		errs: map[string]error{
			"remote-create/sb-err/.km-secrets-bundle.enc.yaml": errors.New("AccessDenied"),
		},
	}
	_, _, err := patchProfileForSops(context.Background(), mock, "bkt", "remote-create/sb-err", "sb-err", []byte(profileYAML))
	if err == nil {
		t.Fatal("expected error when bundle download fails")
	}
	if !strings.Contains(err.Error(), "download sops bundle") {
		t.Errorf("expected 'download sops bundle' in error, got: %v", err)
	}
}

// keyAwareS3Mock returns a per-key body so the desktop-creds.txt read can be
// distinguished from the profile/vscode-pubkey reads.
type keyAwareS3Mock struct {
	bodyByKeySuffix map[string]string
	defaultBody     string
}

func (m *keyAwareS3Mock) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	body := m.defaultBody
	if input.Key != nil {
		for suffix, b := range m.bodyByKeySuffix {
			if strings.HasSuffix(*input.Key, suffix) {
				body = b
				break
			}
		}
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(body))}, nil
}

// -----------------------------------------------------------------------
// Phase 97 — github-inbound enqueue tests (Task 3)
// -----------------------------------------------------------------------

// mockSQSSendAPI is a narrow interface matching the SQS operations the create-handler uses.
type mockSQSSendAPI struct {
	getQueueURLCalled int
	getQueueURLErr    error
	getQueueURLOut    *sqs.GetQueueUrlOutput

	sendMessageCalled int
	sendMessageInput  *sqs.SendMessageInput
	sendMessageErr    error
}

func (m *mockSQSSendAPI) GetQueueUrl(_ context.Context, input *sqs.GetQueueUrlInput, _ ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error) {
	m.getQueueURLCalled++
	if m.getQueueURLErr != nil {
		return nil, m.getQueueURLErr
	}
	if m.getQueueURLOut != nil {
		return m.getQueueURLOut, nil
	}
	qName := ""
	if input.QueueName != nil {
		qName = *input.QueueName
	}
	url := "https://sqs.us-east-1.amazonaws.com/123456789012/" + qName
	return &sqs.GetQueueUrlOutput{QueueUrl: &url}, nil
}

func (m *mockSQSSendAPI) SendMessage(_ context.Context, input *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	m.sendMessageCalled++
	m.sendMessageInput = input
	if m.sendMessageErr != nil {
		return nil, m.sendMessageErr
	}
	id := "msg-id-001"
	return &sqs.SendMessageOutput{MessageId: &id}, nil
}

// TestCreateHandler_GithubEnvelope_EnqueuesAfterProvision verifies that when the
// create event carries a non-empty GithubEnvelope, the create-handler enqueues
// it into the sandbox's github-inbound FIFO queue after provisioning.
func TestCreateHandler_GithubEnvelope_EnqueuesAfterProvision(t *testing.T) {
	mockSQS := &mockSQSSendAPI{}
	mockS3 := &mockS3GetAPI{getBody: minimalProfile}

	h := &CreateHandler{
		S3Client:     mockS3,
		SESClient:    &mockSESAPI{},
		Domain:       "sandboxes.example.com",
		ToolchainDir: "/tmp",
		SQSClient:    mockSQS,
		RunCommand: func(_ string, _ []string, _ []string) ([]byte, error) {
			return []byte("ok"), nil
		},
	}
	envelope := `{"source":"github","action":"created","pr":7}`
	event := CreateEvent{
		SandboxID:      "sb-ghtest",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-ghtest",
		GithubEnvelope: envelope,
	}
	if err := h.Handle(context.Background(), wrapEvent(event)); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if mockSQS.sendMessageCalled != 1 {
		t.Fatalf("expected 1 SendMessage call, got %d", mockSQS.sendMessageCalled)
	}
	if mockSQS.sendMessageInput == nil {
		t.Fatal("expected SendMessage to be called with non-nil input")
	}
	if mockSQS.sendMessageInput.MessageBody == nil || *mockSQS.sendMessageInput.MessageBody != envelope {
		t.Errorf("expected MessageBody=%q, got %v", envelope, mockSQS.sendMessageInput.MessageBody)
	}
	if mockSQS.sendMessageInput.MessageGroupId == nil || *mockSQS.sendMessageInput.MessageGroupId == "" {
		t.Error("expected non-empty MessageGroupId for FIFO queue")
	}
	if mockSQS.sendMessageInput.MessageDeduplicationId == nil || *mockSQS.sendMessageInput.MessageDeduplicationId == "" {
		t.Error("expected non-empty MessageDeduplicationId for FIFO queue")
	}
}

// TestCreateHandler_GithubEnvelope_EmptyEnvelope_NoEnqueue verifies that when
// GithubEnvelope is empty, no SQS SendMessage is called (non-github creates unaffected).
func TestCreateHandler_GithubEnvelope_EmptyEnvelope_NoEnqueue(t *testing.T) {
	mockSQS := &mockSQSSendAPI{}
	mockS3 := &mockS3GetAPI{getBody: minimalProfile}

	h := &CreateHandler{
		S3Client:     mockS3,
		SESClient:    &mockSESAPI{},
		Domain:       "sandboxes.example.com",
		ToolchainDir: "/tmp",
		SQSClient:    mockSQS,
		RunCommand: func(_ string, _ []string, _ []string) ([]byte, error) {
			return []byte("ok"), nil
		},
	}
	event := CreateEvent{
		SandboxID:      "sb-noenvelope",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-noenvelope",
		// No GithubEnvelope
	}
	if err := h.Handle(context.Background(), wrapEvent(event)); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if mockSQS.sendMessageCalled != 0 {
		t.Errorf("expected 0 SendMessage calls for empty envelope, got %d", mockSQS.sendMessageCalled)
	}
}

// TestCreateHandler_GithubEnvelope_EnqueueError_NonFatal verifies that an SQS
// SendMessage error does NOT cause Handle to fail (best-effort semantics).
func TestCreateHandler_GithubEnvelope_EnqueueError_NonFatal(t *testing.T) {
	mockSQS := &mockSQSSendAPI{sendMessageErr: errors.New("sqs unavailable")}
	mockS3 := &mockS3GetAPI{getBody: minimalProfile}

	h := &CreateHandler{
		S3Client:     mockS3,
		SESClient:    &mockSESAPI{},
		Domain:       "sandboxes.example.com",
		ToolchainDir: "/tmp",
		SQSClient:    mockSQS,
		RunCommand: func(_ string, _ []string, _ []string) ([]byte, error) {
			return []byte("ok"), nil
		},
	}
	event := CreateEvent{
		SandboxID:      "sb-sqserr",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-sqserr",
		GithubEnvelope: `{"source":"github","pr":3}`,
	}
	// Handle must succeed even if SQS errors — enqueue is best-effort
	if err := h.Handle(context.Background(), wrapEvent(event)); err != nil {
		t.Fatalf("Handle should succeed even when enqueue fails, got: %v", err)
	}
}

// TestCreateHandler_DesktopCredsEnv verifies the handler reads desktop-creds.txt and
// threads KM_DESKTOP_KASM_USER/KM_DESKTOP_KASM_PASS into the km create subprocess env,
// so the box's KasmVNC password matches the operator's ~/.km/desktop/<id> (no 401).
func TestCreateHandler_DesktopCredsEnv(t *testing.T) {
	mockS3 := &keyAwareS3Mock{
		defaultBody:     minimalProfile,
		bodyByKeySuffix: map[string]string{"desktop-creds.txt": "kasm:s3cret-pass"},
	}
	var capturedEnv []string
	h := &CreateHandler{
		S3Client:     mockS3,
		SESClient:    &mockSESAPI{},
		Domain:       "sandboxes.example.com",
		ToolchainDir: "/tmp",
		RunCommand: func(_ string, _ []string, env []string) ([]byte, error) {
			capturedEnv = env
			return []byte("sandbox created"), nil
		},
	}
	event := CreateEvent{
		SandboxID:      "sb-deskcreds",
		ArtifactBucket: "km-artifacts",
		ArtifactPrefix: "remote-create/sb-deskcreds",
	}
	if err := h.Handle(context.Background(), wrapEvent(event)); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	wantUser, wantPass := false, false
	for _, e := range capturedEnv {
		if e == "KM_DESKTOP_KASM_USER=kasm" {
			wantUser = true
		}
		if e == "KM_DESKTOP_KASM_PASS=s3cret-pass" {
			wantPass = true
		}
	}
	if !wantUser || !wantPass {
		t.Errorf("desktop creds not threaded into subprocess env (user=%v pass=%v)", wantUser, wantPass)
	}
}
