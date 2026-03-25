package aws_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ---- mockCWLogsAPI ----
// Implements kmaws.CWLogsAPI for unit testing without real AWS calls.

type mockCWLogsAPI struct {
	createGroupErr   error
	createStreamErr  error
	putEventsInput   *cloudwatchlogs.PutLogEventsInput
	putEventsErr     error
	getEventsOutput  *cloudwatchlogs.GetLogEventsOutput
	getEventsErr     error
	exportTaskInput  *cloudwatchlogs.CreateExportTaskInput
	exportTaskOutput *cloudwatchlogs.CreateExportTaskOutput
	exportTaskErr    error
}

func (m *mockCWLogsAPI) CreateLogGroup(
	_ context.Context,
	_ *cloudwatchlogs.CreateLogGroupInput,
	_ ...func(*cloudwatchlogs.Options),
) (*cloudwatchlogs.CreateLogGroupOutput, error) {
	return &cloudwatchlogs.CreateLogGroupOutput{}, m.createGroupErr
}

func (m *mockCWLogsAPI) CreateLogStream(
	_ context.Context,
	_ *cloudwatchlogs.CreateLogStreamInput,
	_ ...func(*cloudwatchlogs.Options),
) (*cloudwatchlogs.CreateLogStreamOutput, error) {
	return &cloudwatchlogs.CreateLogStreamOutput{}, m.createStreamErr
}

func (m *mockCWLogsAPI) PutLogEvents(
	_ context.Context,
	input *cloudwatchlogs.PutLogEventsInput,
	_ ...func(*cloudwatchlogs.Options),
) (*cloudwatchlogs.PutLogEventsOutput, error) {
	m.putEventsInput = input
	return &cloudwatchlogs.PutLogEventsOutput{}, m.putEventsErr
}

func (m *mockCWLogsAPI) GetLogEvents(
	_ context.Context,
	_ *cloudwatchlogs.GetLogEventsInput,
	_ ...func(*cloudwatchlogs.Options),
) (*cloudwatchlogs.GetLogEventsOutput, error) {
	return m.getEventsOutput, m.getEventsErr
}

func (m *mockCWLogsAPI) PutRetentionPolicy(
	_ context.Context,
	_ *cloudwatchlogs.PutRetentionPolicyInput,
	_ ...func(*cloudwatchlogs.Options),
) (*cloudwatchlogs.PutRetentionPolicyOutput, error) {
	return &cloudwatchlogs.PutRetentionPolicyOutput{}, nil
}

func (m *mockCWLogsAPI) DeleteLogGroup(
	_ context.Context,
	_ *cloudwatchlogs.DeleteLogGroupInput,
	_ ...func(*cloudwatchlogs.Options),
) (*cloudwatchlogs.DeleteLogGroupOutput, error) {
	return &cloudwatchlogs.DeleteLogGroupOutput{}, nil
}

func (m *mockCWLogsAPI) CreateExportTask(
	_ context.Context,
	input *cloudwatchlogs.CreateExportTaskInput,
	_ ...func(*cloudwatchlogs.Options),
) (*cloudwatchlogs.CreateExportTaskOutput, error) {
	m.exportTaskInput = input
	if m.exportTaskOutput != nil {
		return m.exportTaskOutput, m.exportTaskErr
	}
	return &cloudwatchlogs.CreateExportTaskOutput{}, m.exportTaskErr
}

// ---- Tests ----

func TestEnsureLogGroup_Created(t *testing.T) {
	mock := &mockCWLogsAPI{}
	err := kmaws.EnsureLogGroup(context.Background(), mock, "/km/sandboxes/sb-test/", "audit")
	if err != nil {
		t.Fatalf("EnsureLogGroup returned error: %v", err)
	}
}

func TestEnsureLogGroup_AlreadyExists(t *testing.T) {
	alreadyExists := &types.ResourceAlreadyExistsException{
		Message: aws.String("log group already exists"),
	}
	mock := &mockCWLogsAPI{
		createGroupErr:  alreadyExists,
		createStreamErr: alreadyExists,
	}
	err := kmaws.EnsureLogGroup(context.Background(), mock, "/km/sandboxes/sb-test/", "audit")
	if err != nil {
		t.Fatalf("EnsureLogGroup should return nil for ResourceAlreadyExistsException, got: %v", err)
	}
}

func TestPutLogEvents_Success(t *testing.T) {
	mock := &mockCWLogsAPI{}
	events := []kmaws.LogEvent{
		{Timestamp: 1000, Message: "event 1"},
		{Timestamp: 2000, Message: "event 2"},
		{Timestamp: 3000, Message: "event 3"},
	}
	err := kmaws.PutLogEvents(context.Background(), mock, "/km/sandboxes/sb-test/", "audit", events)
	if err != nil {
		t.Fatalf("PutLogEvents returned error: %v", err)
	}
	if mock.putEventsInput == nil {
		t.Fatal("expected PutLogEvents to be called, but putEventsInput is nil")
	}
	if len(mock.putEventsInput.LogEvents) != 3 {
		t.Errorf("expected 3 log events in PutLogEvents call, got %d", len(mock.putEventsInput.LogEvents))
	}
	if aws.ToString(mock.putEventsInput.LogGroupName) != "/km/sandboxes/sb-test/" {
		t.Errorf("log group = %q, want %q", aws.ToString(mock.putEventsInput.LogGroupName), "/km/sandboxes/sb-test/")
	}
	if aws.ToString(mock.putEventsInput.LogStreamName) != "audit" {
		t.Errorf("log stream = %q, want %q", aws.ToString(mock.putEventsInput.LogStreamName), "audit")
	}
}

func TestGetLogEvents_Success(t *testing.T) {
	mock := &mockCWLogsAPI{
		getEventsOutput: &cloudwatchlogs.GetLogEventsOutput{
			Events: []types.OutputLogEvent{
				{Message: aws.String("line 1"), Timestamp: aws.Int64(1000)},
				{Message: aws.String("line 2"), Timestamp: aws.Int64(2000)},
			},
		},
	}
	got, err := kmaws.GetLogEvents(context.Background(), mock, "/km/sandboxes/sb-test/", "audit", 100)
	if err != nil {
		t.Fatalf("GetLogEvents returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	if got[0].Message != "line 1" {
		t.Errorf("event[0].Message = %q, want %q", got[0].Message, "line 1")
	}
	if got[1].Message != "line 2" {
		t.Errorf("event[1].Message = %q, want %q", got[1].Message, "line 2")
	}
}

func TestGetLogEvents_Empty(t *testing.T) {
	mock := &mockCWLogsAPI{
		getEventsOutput: &cloudwatchlogs.GetLogEventsOutput{
			Events: []types.OutputLogEvent{},
		},
	}
	got, err := kmaws.GetLogEvents(context.Background(), mock, "/km/sandboxes/sb-test/", "audit", 100)
	if err != nil {
		t.Fatalf("GetLogEvents returned error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d events", len(got))
	}
}

func TestTailLogs_NoFollow(t *testing.T) {
	mock := &mockCWLogsAPI{
		getEventsOutput: &cloudwatchlogs.GetLogEventsOutput{
			Events: []types.OutputLogEvent{
				{Message: aws.String("hello"), Timestamp: aws.Int64(1000)},
			},
		},
	}
	var buf strings.Builder
	err := kmaws.TailLogs(context.Background(), mock, "/km/sandboxes/sb-test/", "audit", false, &buf)
	if err != nil {
		t.Fatalf("TailLogs returned error: %v", err)
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("TailLogs output does not contain expected message: %q", buf.String())
	}
}

// ---- ExportSandboxLogs tests ----

// TestExportSandboxLogs_Success verifies that ExportSandboxLogs calls CreateExportTask
// with the correct log group, destination bucket, prefix, and time range.
func TestExportSandboxLogs_Success(t *testing.T) {
	mock := &mockCWLogsAPI{}
	err := kmaws.ExportSandboxLogs(context.Background(), mock, "sb-aabbccdd", "km-artifacts-bucket")
	if err != nil {
		t.Fatalf("ExportSandboxLogs returned error: %v", err)
	}
	if mock.exportTaskInput == nil {
		t.Fatal("expected CreateExportTask to be called, but exportTaskInput is nil")
	}

	// Verify log group name follows convention
	wantLogGroup := "/km/sandboxes/sb-aabbccdd/"
	if got := *mock.exportTaskInput.LogGroupName; got != wantLogGroup {
		t.Errorf("LogGroupName = %q, want %q", got, wantLogGroup)
	}

	// Verify destination bucket
	wantBucket := "km-artifacts-bucket"
	if got := *mock.exportTaskInput.Destination; got != wantBucket {
		t.Errorf("Destination = %q, want %q", got, wantBucket)
	}

	// Verify destination prefix
	wantPrefix := "logs/sb-aabbccdd"
	if got := *mock.exportTaskInput.DestinationPrefix; got != wantPrefix {
		t.Errorf("DestinationPrefix = %q, want %q", got, wantPrefix)
	}

	// Verify time range is set (non-zero)
	if mock.exportTaskInput.From == nil || *mock.exportTaskInput.From == 0 {
		t.Error("expected From time to be set to a past timestamp")
	}
	if mock.exportTaskInput.To == nil || *mock.exportTaskInput.To == 0 {
		t.Error("expected To time to be set to current time")
	}
}

// TestExportSandboxLogs_ResourceNotFound verifies that ExportSandboxLogs returns nil
// when the log group does not exist (ResourceNotFoundException). No logs = nothing to export.
func TestExportSandboxLogs_ResourceNotFound(t *testing.T) {
	mock := &mockCWLogsAPI{
		exportTaskErr: &types.ResourceNotFoundException{
			Message: aws.String("log group does not exist"),
		},
	}
	err := kmaws.ExportSandboxLogs(context.Background(), mock, "sb-notexist", "km-artifacts-bucket")
	if err != nil {
		t.Errorf("ExportSandboxLogs should return nil for ResourceNotFoundException, got: %v", err)
	}
}

// TestExportSandboxLogs_OtherError verifies that ExportSandboxLogs returns a wrapped error
// for non-ResourceNotFoundException failures.
func TestExportSandboxLogs_OtherError(t *testing.T) {
	mock := &mockCWLogsAPI{
		exportTaskErr: errors.New("access denied"),
	}
	err := kmaws.ExportSandboxLogs(context.Background(), mock, "sb-aabbccdd", "km-artifacts-bucket")
	if err == nil {
		t.Fatal("expected error for non-ResourceNotFoundException failure, got nil")
	}
	if !strings.Contains(err.Error(), "export") {
		t.Errorf("expected error to mention 'export', got: %v", err)
	}
}

// TestTailLogs_ContextCancel verifies that TailLogs stops when ctx is cancelled.
func TestTailLogs_ContextCancel(t *testing.T) {
	// Return empty events — TailLogs with follow=true would poll forever.
	mock := &mockCWLogsAPI{
		getEventsOutput: &cloudwatchlogs.GetLogEventsOutput{
			Events: []types.OutputLogEvent{},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := kmaws.TailLogs(ctx, mock, "/km/sandboxes/sb-test/", "audit", true, io.Discard)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("TailLogs returned unexpected error: %v", err)
	}
}
