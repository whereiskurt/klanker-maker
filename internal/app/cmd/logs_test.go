package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// mockLogsCWLogsAPI is a local CWLogsAPI stub for logs_test.go.
// It implements kmaws.CWLogsAPI with controllable outputs and input capture.
type mockLogsCWLogsAPI struct {
	getLogEventsInput    *cloudwatchlogs.GetLogEventsInput
	getLogEventsOutput   *cloudwatchlogs.GetLogEventsOutput
	getLogEventsErr      error
	filterLogEventsInput  *cloudwatchlogs.FilterLogEventsInput
	filterLogEventsOutput *cloudwatchlogs.FilterLogEventsOutput
	filterLogEventsErr    error
}

func (m *mockLogsCWLogsAPI) CreateLogGroup(_ context.Context, _ *cloudwatchlogs.CreateLogGroupInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogGroupOutput, error) {
	return &cloudwatchlogs.CreateLogGroupOutput{}, nil
}

func (m *mockLogsCWLogsAPI) CreateLogStream(_ context.Context, _ *cloudwatchlogs.CreateLogStreamInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogStreamOutput, error) {
	return &cloudwatchlogs.CreateLogStreamOutput{}, nil
}

func (m *mockLogsCWLogsAPI) PutLogEvents(_ context.Context, _ *cloudwatchlogs.PutLogEventsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutLogEventsOutput, error) {
	return &cloudwatchlogs.PutLogEventsOutput{}, nil
}

func (m *mockLogsCWLogsAPI) GetLogEvents(_ context.Context, input *cloudwatchlogs.GetLogEventsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error) {
	m.getLogEventsInput = input
	if m.getLogEventsErr != nil {
		return nil, m.getLogEventsErr
	}
	if m.getLogEventsOutput != nil {
		return m.getLogEventsOutput, nil
	}
	return &cloudwatchlogs.GetLogEventsOutput{}, nil
}

func (m *mockLogsCWLogsAPI) PutRetentionPolicy(_ context.Context, _ *cloudwatchlogs.PutRetentionPolicyInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutRetentionPolicyOutput, error) {
	return &cloudwatchlogs.PutRetentionPolicyOutput{}, nil
}

func (m *mockLogsCWLogsAPI) DeleteLogGroup(_ context.Context, _ *cloudwatchlogs.DeleteLogGroupInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DeleteLogGroupOutput, error) {
	return &cloudwatchlogs.DeleteLogGroupOutput{}, nil
}

func (m *mockLogsCWLogsAPI) CreateExportTask(_ context.Context, _ *cloudwatchlogs.CreateExportTaskInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateExportTaskOutput, error) {
	return &cloudwatchlogs.CreateExportTaskOutput{}, nil
}

func (m *mockLogsCWLogsAPI) FilterLogEvents(_ context.Context, input *cloudwatchlogs.FilterLogEventsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error) {
	m.filterLogEventsInput = input
	if m.filterLogEventsErr != nil {
		return nil, m.filterLogEventsErr
	}
	if m.filterLogEventsOutput != nil {
		return m.filterLogEventsOutput, nil
	}
	return &cloudwatchlogs.FilterLogEventsOutput{}, nil
}

// compile-time check that mockLogsCWLogsAPI satisfies the interface.
var _ kmaws.CWLogsAPI = (*mockLogsCWLogsAPI)(nil)

// ---- Tests ----

// TestLogsCmd_FallbackWithEvents verifies that when GetLogEvents returns a
// ResourceNotFoundException, runLogs falls back to FilterLogEvents on the
// create-handler Lambda log group and prints the prelude + chronological events.
func TestLogsCmd_FallbackWithEvents(t *testing.T) {
	cfg := &config.Config{}

	mock := &mockLogsCWLogsAPI{
		getLogEventsErr: &cwltypes.ResourceNotFoundException{Message: aws.String("group missing")},
		filterLogEventsOutput: &cloudwatchlogs.FilterLogEventsOutput{
			Events: []cwltypes.FilteredLogEvent{
				{Timestamp: aws.Int64(1715000000000), Message: aws.String(`{"level":"error","msg":"slack channel archived"}`)},
				{Timestamp: aws.Int64(1715000001000), Message: aws.String(`{"level":"info","msg":"retrying"}`)},
			},
		},
	}

	logsCmd := cmd.NewLogsCmdWithClient(cfg, mock)
	root := &cobra.Command{Use: "km"}
	root.AddCommand(logsCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"logs", "learn-abc12345"})

	if err := root.Execute(); err != nil {
		t.Fatalf("logs command returned unexpected error: %v", err)
	}

	out := buf.String()
	const prelude = "── per-sandbox log group not found; falling back to create-handler Lambda ──"
	if !strings.Contains(out, prelude) {
		t.Errorf("output missing prelude line; got:\n%s", out)
	}
	// Events should be printed chronologically as "<RFC3339 ts> <message>".
	if !strings.Contains(out, `{"level":"error","msg":"slack channel archived"}`) {
		t.Errorf("output missing first event message; got:\n%s", out)
	}
	if !strings.Contains(out, `{"level":"info","msg":"retrying"}`) {
		t.Errorf("output missing second event message; got:\n%s", out)
	}
	// Timestamps should be RFC3339 format — check for at least one date prefix.
	if !strings.Contains(out, "2024-05-0") && !strings.Contains(out, "2024-05-1") {
		// Just check there's some RFC3339-ish date in output.
		if !strings.Contains(out, "T") || !strings.Contains(out, "Z") {
			t.Errorf("output does not appear to contain RFC3339 timestamps; got:\n%s", out)
		}
	}
}

// TestLogsCmd_FallbackBothEmpty verifies that when GetLogEvents returns
// ResourceNotFoundException AND FilterLogEvents returns no events, the command
// prints the prelude and a friendly empty-state hint, then exits cleanly.
func TestLogsCmd_FallbackBothEmpty(t *testing.T) {
	cfg := &config.Config{}

	mock := &mockLogsCWLogsAPI{
		getLogEventsErr: &cwltypes.ResourceNotFoundException{Message: aws.String("group missing")},
		filterLogEventsOutput: &cloudwatchlogs.FilterLogEventsOutput{
			Events: []cwltypes.FilteredLogEvent{},
		},
	}

	logsCmd := cmd.NewLogsCmdWithClient(cfg, mock)
	root := &cobra.Command{Use: "km"}
	root.AddCommand(logsCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"logs", "learn-abc12345"})

	if err := root.Execute(); err != nil {
		t.Fatalf("logs command returned unexpected error: %v", err)
	}

	out := buf.String()
	const prelude = "── per-sandbox log group not found; falling back to create-handler Lambda ──"
	if !strings.Contains(out, prelude) {
		t.Errorf("output missing prelude line; got:\n%s", out)
	}
	const emptyHint = "No create-handler activity found for learn-abc12345 in the last 24h."
	if !strings.Contains(out, emptyHint) {
		t.Errorf("output missing empty-state hint; got:\n%s", out)
	}
	const statusHint = "km status learn-abc12345"
	if !strings.Contains(out, statusHint) {
		t.Errorf("output missing km status hint; got:\n%s", out)
	}
}

// TestLogsCmd_FallbackFollow_NoOp verifies that when GetLogEvents returns
// ResourceNotFoundException and --follow is set, the command prints the prelude
// and a note that --follow is not supported in fallback mode, does NOT call
// FilterLogEvents, and exits cleanly.
func TestLogsCmd_FallbackFollow_NoOp(t *testing.T) {
	cfg := &config.Config{}

	mock := &mockLogsCWLogsAPI{
		getLogEventsErr: &cwltypes.ResourceNotFoundException{Message: aws.String("group missing")},
	}

	logsCmd := cmd.NewLogsCmdWithClient(cfg, mock)
	root := &cobra.Command{Use: "km"}
	root.AddCommand(logsCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"logs", "learn-abc12345", "--follow"})

	if err := root.Execute(); err != nil {
		t.Fatalf("logs command returned unexpected error: %v", err)
	}

	out := buf.String()
	const prelude = "── per-sandbox log group not found; falling back to create-handler Lambda ──"
	if !strings.Contains(out, prelude) {
		t.Errorf("output missing prelude line; got:\n%s", out)
	}
	if !strings.Contains(out, "--follow is not supported in fallback mode") {
		t.Errorf("output missing --follow note; got:\n%s", out)
	}
	// FilterLogEvents must NOT be called when --follow is set in fallback mode.
	if mock.filterLogEventsInput != nil {
		t.Errorf("expected FilterLogEvents NOT to be called in follow-fallback mode, but it was")
	}
}

// TestLogsCmd_NonNotFoundError_Surfaces verifies that non-ResourceNotFoundException
// errors from GetLogEvents are returned as wrapped errors without triggering the
// fallback path (FilterLogEvents must NOT be called).
func TestLogsCmd_NonNotFoundError_Surfaces(t *testing.T) {
	cfg := &config.Config{}

	mock := &mockLogsCWLogsAPI{
		getLogEventsErr: errors.New("network blip"),
	}

	logsCmd := cmd.NewLogsCmdWithClient(cfg, mock)
	root := &cobra.Command{Use: "km"}
	root.AddCommand(logsCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"logs", "learn-abc12345"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for non-ResourceNotFoundException failure, got nil")
	}
	// Fallback must NOT be triggered.
	if mock.filterLogEventsInput != nil {
		t.Errorf("expected FilterLogEvents NOT to be called for non-ResourceNotFoundException error")
	}
}

// TestLogsCmd_PerSandboxGroupPresent proves that:
//  1. NewLogsCmdWithClient injects the mock client correctly (DI seam works).
//  2. The log group is constructed as /{prefix}/sandboxes/{sandbox-id}/.
//  3. Events returned by GetLogEvents are printed to stdout.
func TestLogsCmd_PerSandboxGroupPresent(t *testing.T) {
	// Zero-value Config: GetResourcePrefix() returns "km".
	cfg := &config.Config{}

	mock := &mockLogsCWLogsAPI{
		getLogEventsOutput: &cloudwatchlogs.GetLogEventsOutput{
			Events: []cwltypes.OutputLogEvent{
				{Message: aws.String("audit-event-1"), Timestamp: aws.Int64(1000)},
				{Message: aws.String("audit-event-2"), Timestamp: aws.Int64(2000)},
			},
		},
	}

	logsCmd := cmd.NewLogsCmdWithClient(cfg, mock)
	root := &cobra.Command{Use: "km"}
	root.AddCommand(logsCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)

	// "sb-a1b2c3d4" matches sandboxIDLike pattern, so ResolveSandboxID returns it directly
	// even when AWS alias lookup fails (no real AWS credentials in tests).
	root.SetArgs([]string{"logs", "sb-a1b2c3d4"})

	if err := root.Execute(); err != nil {
		t.Fatalf("logs command returned unexpected error: %v", err)
	}

	// Verify GetLogEvents was called with the correct log group.
	if mock.getLogEventsInput == nil {
		t.Fatal("expected GetLogEvents to be called, but getLogEventsInput is nil")
	}
	wantLogGroup := "/km/sandboxes/sb-a1b2c3d4/"
	if got := aws.ToString(mock.getLogEventsInput.LogGroupName); got != wantLogGroup {
		t.Errorf("GetLogEvents LogGroupName = %q, want %q", got, wantLogGroup)
	}

	// Verify output contains the event messages.
	out := buf.String()
	if !strings.Contains(out, "audit-event-1") {
		t.Errorf("output does not contain 'audit-event-1'; got:\n%s", out)
	}
	if !strings.Contains(out, "audit-event-2") {
		t.Errorf("output does not contain 'audit-event-2'; got:\n%s", out)
	}
}
