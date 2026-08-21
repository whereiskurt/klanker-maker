package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cloudwatchlogstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// NewLogsCmd creates the "km logs" subcommand.
// Usage: km logs <sandbox-id> [--follow] [--stream <name>]
//
// Tails CloudWatch log group /km/sandboxes/<sandbox-id>/ from the named stream (default "audit").
// If --follow is set, streams log events until Ctrl+C (context cancellation).
// Delegates to NewLogsCmdWithClient with a nil client (real AWS-backed client).
func NewLogsCmd(cfg *config.Config) *cobra.Command {
	return NewLogsCmdWithClient(cfg, nil)
}

// NewLogsCmdWithClient creates the "km logs" subcommand with an injected CWLogsAPI client.
// When client is nil, the real cloudwatchlogs.Client is built at command execution time.
// When client is non-nil, the injected client is used (for tests).
// This DI seam mirrors the NewStatusCmdWithFetcher pattern in status.go.
func NewLogsCmdWithClient(cfg *config.Config, client kmaws.CWLogsAPI) *cobra.Command {
	var follow bool
	var stream string

	cmd := &cobra.Command{
		Use:          "logs <sandbox-id | #number>",
		Short:        "Tail audit logs for a sandbox",
		Long:         helpText("logs"),
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}
			return runLogs(cmd, cfg, client, sandboxID, stream, follow)
		},
	}

	cmd.Flags().BoolVar(&follow, "follow", false, "Stream logs continuously until Ctrl+C")
	cmd.Flags().StringVar(&stream, "stream", "audit", "Log stream name within the sandbox log group")

	return cmd
}

// runLogs implements the km logs command.
// When client is nil, builds the real cloudwatchlogs.Client from the AWS config (production path).
// When client is non-nil, uses the injected client (test path).
func runLogs(cmd *cobra.Command, cfg *config.Config, client kmaws.CWLogsAPI, sandboxID, stream string, follow bool) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	logGroup := "/" + cfg.GetResourcePrefix() + "/sandboxes/" + sandboxID + "/"

	cwClient := client
	if cwClient == nil {
		awsProfile := "klanker-terraform"
		awsCfg, err := kmaws.LoadAWSConfig(ctx, awsProfile)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		cwClient = cloudwatchlogs.NewFromConfig(awsCfg)
	}

	// Count what TailLogs actually emits. The audit sidecar creates the group and
	// its stream at boot, so both exist immediately but empty — and TailLogs prints
	// nothing and returns nil, which looks exactly like a broken command. The Phase
	// 77 fallback below does not help: it only fires when the group is ABSENT.
	counting := &countingWriter{w: cmd.OutOrStdout()}

	err := kmaws.TailLogs(ctx, cwClient, logGroup, stream, follow, counting)
	if err == nil && counting.n == 0 && !follow {
		// Say what happened AND what this stream can never contain, because the
		// usual reason an operator is here ("why didn't my sandbox boot?") is a
		// question these events structurally cannot answer: the producer is the
		// _km_audit PROMPT_COMMAND hook, which fires only in interactive bash login
		// shells. No cloud-init output, no agent activity, no sidecar stdout.
		fmt.Fprintf(cmd.OutOrStdout(),
			"no audit events yet for %s — this stream carries interactive shell commands only.\n"+
				"For provisioning and agent activity see: km status %s / km otel %s\n",
			sandboxID, sandboxID, sandboxID)
		return nil
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		// Phase 77: fall back to the create-handler Lambda log group when the
		// per-sandbox group never existed (failed sandboxes whose user-data
		// never ran). Only trigger on ResourceNotFoundException — all other
		// errors continue to surface as wrapped errors (per Pitfall 5 in
		// 77-RESEARCH.md, errors.As is used; errors.Is does NOT unwrap %w-wrapped typed errors).
		var notFound *cloudwatchlogstypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return runLogsLambdaFallback(cmd, cfg, cwClient, sandboxID, follow)
		}
		return fmt.Errorf("tail logs for sandbox %s: %w", sandboxID, err)
	}

	return nil
}

// countingWriter tracks whether anything was written, so runLogs can tell an empty
// stream apart from a stream it simply forwarded. Wrapping the writer keeps
// kmaws.TailLogs' signature (and its other callers) untouched.
type countingWriter struct {
	w io.Writer
	n int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

// runLogsLambdaFallback queries the create-handler Lambda's CloudWatch log group
// for entries pertaining to a single sandbox over the last 24h. Triggered when the
// per-sandbox audit log group does not exist (Phase 77).
//
// The --follow flag is a no-op in fallback mode — failure is terminal and the log
// group will never gain new entries after the create-handler exits.
func runLogsLambdaFallback(cmd *cobra.Command, cfg *config.Config, client kmaws.CWLogsAPI, sandboxID string, follow bool) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "── per-sandbox log group not found; falling back to create-handler Lambda ──")

	if follow {
		fmt.Fprintf(out, "--follow is not supported in fallback mode (failure is terminal); use km status %s for the persisted reason.\n", sandboxID)
		return nil
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	events, err := kmaws.FilterCreateHandlerLogs(ctx, client, cfg.GetResourcePrefix(), sandboxID)
	if err != nil {
		return fmt.Errorf("filter create-handler logs for %s: %w", sandboxID, err)
	}

	if len(events) == 0 {
		fmt.Fprintf(out, "No create-handler activity found for %s in the last 24h. Try km status %s for the persisted failure reason.\n", sandboxID, sandboxID)
		return nil
	}

	for _, ev := range events {
		ts := time.UnixMilli(ev.Timestamp).UTC().Format(time.RFC3339)
		fmt.Fprintf(out, "%s %s\n", ts, ev.Message)
	}
	return nil
}
