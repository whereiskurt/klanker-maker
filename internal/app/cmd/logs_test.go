package cmd_test

import (
	"bytes"
	"context"
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
