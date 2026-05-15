package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
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
