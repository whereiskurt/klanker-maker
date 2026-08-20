package cmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// TestLogsCmd_EmptyStreamIsExplicit covers the biggest reason `km logs` reads as
// broken: the audit sidecar creates the group and stream at boot, so they exist
// immediately but empty. TailLogs then prints NOTHING and exits 0, which is
// indistinguishable from a broken command. The Phase 77 Lambda fallback does not
// engage — it only fires when the group is ABSENT, not when it is present-but-empty.
func TestLogsCmd_EmptyStreamIsExplicit(t *testing.T) {
	cfg := &config.Config{}

	mock := &mockLogsCWLogsAPI{
		getLogEventsOutput: &cloudwatchlogs.GetLogEventsOutput{
			Events: []cwltypes.OutputLogEvent{}, // group + stream exist, zero events
		},
	}

	logsCmd := cmd.NewLogsCmdWithClient(cfg, mock)
	root := &cobra.Command{Use: "km"}
	root.AddCommand(logsCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"logs", "sb-a1b2c3d4"})

	if err := root.Execute(); err != nil {
		t.Fatalf("logs command returned unexpected error: %v", err)
	}

	out := buf.String()
	if strings.TrimSpace(out) == "" {
		t.Fatal("km logs printed nothing for an empty stream and exited 0 — " +
			"indistinguishable from a broken command")
	}
	if !strings.Contains(out, "no audit events") {
		t.Errorf("output does not say the stream is empty; got:\n%s", out)
	}
	// The other half of the confusion: operators reach for km logs to answer
	// "why didn't my sandbox boot", but this stream carries ONLY interactive
	// shell commands (the _km_audit PROMPT_COMMAND hook). No cloud-init, no
	// agent activity, no sidecar stdout. Say so, and point somewhere useful.
	if !strings.Contains(out, "shell command") {
		t.Errorf("output does not explain what this stream actually carries; got:\n%s", out)
	}
	if !strings.Contains(out, "km status") && !strings.Contains(out, "km otel") {
		t.Errorf("output does not point at km status / km otel for provisioning + agent activity; got:\n%s", out)
	}
}

// TestLogsCmd_NonEmptyStreamHasNoNotice guards against printing the notice when
// events did arrive.
func TestLogsCmd_NonEmptyStreamHasNoNotice(t *testing.T) {
	cfg := &config.Config{}

	mock := &mockLogsCWLogsAPI{
		getLogEventsOutput: &cloudwatchlogs.GetLogEventsOutput{
			Events: []cwltypes.OutputLogEvent{
				{Message: aws.String("audit-event-1"), Timestamp: aws.Int64(1000)},
			},
		},
	}

	logsCmd := cmd.NewLogsCmdWithClient(cfg, mock)
	root := &cobra.Command{Use: "km"}
	root.AddCommand(logsCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"logs", "sb-a1b2c3d4"})

	if err := root.Execute(); err != nil {
		t.Fatalf("logs command returned unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "audit-event-1") {
		t.Errorf("output lost the real event; got:\n%s", out)
	}
	if strings.Contains(out, "no audit events") {
		t.Errorf("empty-stream notice printed despite events being present; got:\n%s", out)
	}
}
