package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// NewLogsCmd creates the "km logs" subcommand.
// Usage: km logs <sandbox-id> [--follow] [--stream <name>]
//
// Tails CloudWatch log group /km/sandboxes/<sandbox-id>/ from the named stream (default "audit").
// If --follow is set, streams log events until Ctrl+C (context cancellation).
func NewLogsCmd(cfg *config.Config) *cobra.Command {
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
			return runLogs(cmd, cfg, sandboxID, stream, follow)
		},
	}

	cmd.Flags().BoolVar(&follow, "follow", false, "Stream logs continuously until Ctrl+C")
	cmd.Flags().StringVar(&stream, "stream", "audit", "Log stream name within the sandbox log group")

	return cmd
}

// runLogs implements the km logs command.
func runLogs(cmd *cobra.Command, cfg *config.Config, sandboxID, stream string, follow bool) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	logGroup := "/km/sandboxes/" + sandboxID + "/"

	awsProfile := "klanker-terraform"
	awsCfg, err := kmaws.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	cwClient := cloudwatchlogs.NewFromConfig(awsCfg)

	err = kmaws.TailLogs(ctx, cwClient, logGroup, stream, follow, cmd.OutOrStdout())
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("tail logs for sandbox %s: %w", sandboxID, err)
	}

	return nil
}
