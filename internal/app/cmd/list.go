package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// NewListCmd creates the "km list" subcommand.
// Usage: km list [--json] [--tags]
//
// Scans S3 (default) or AWS resource tags for running sandboxes and prints
// a table of sandbox ID, profile, substrate, region, status, and TTL remaining.
func NewListCmd(cfg *config.Config) *cobra.Command {
	return NewListCmdWithLister(cfg, nil)
}

// NewListCmdWithLister builds the list command with an optional custom lister.
// If lister is nil, the real AWS-backed lister is used. This overload is used
// in tests to inject fake lister implementations.
func NewListCmdWithLister(cfg *config.Config, lister SandboxLister) *cobra.Command {
	var jsonOutput bool
	var useTagScan bool

	cmd := &cobra.Command{
		Use:          "list",
		Aliases:      []string{"ls"},
		Short:        "List all running sandboxes",
		Long:         helpText("list"),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd, cfg, lister, jsonOutput, useTagScan)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON array")
	cmd.Flags().BoolVar(&useTagScan, "tags", false, "Use AWS tag scan instead of S3 state scan")
	return cmd
}

// SandboxLister abstracts the sandbox discovery mechanism for testability.
type SandboxLister interface {
	ListSandboxes(ctx context.Context, useTagScan bool) ([]kmaws.SandboxRecord, error)
}

// runList is the command RunE logic, accepting an explicit lister for testability.
func runList(cmd *cobra.Command, cfg *config.Config, lister SandboxLister, jsonOutput, useTagScan bool) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if lister == nil {
		if cfg.StateBucket == "" {
			return fmt.Errorf("state bucket not configured: set KM_STATE_BUCKET or state_bucket in km-config.yaml")
		}
		awsProfile := "klanker-terraform"
		awsCfg, err := kmaws.LoadAWSConfig(ctx, awsProfile)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		lister = newRealLister(awsCfg, cfg.StateBucket)
	}

	records, err := lister.ListSandboxes(ctx, useTagScan)
	if err != nil {
		return fmt.Errorf("list sandboxes: %w", err)
	}

	if len(records) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No running sandboxes.")
		return nil
	}

	// Check live instance status for EC2 sandboxes to detect spot reclamation / termination.
	awsProfile := "klanker-terraform"
	awsCfg, ec2Err := kmaws.LoadAWSConfig(ctx, awsProfile)
	if ec2Err == nil {
		ec2Client := ec2.NewFromConfig(awsCfg)
		for i := range records {
			if records[i].Substrate == "ec2" && records[i].Status == "running" {
				records[i].Status = checkEC2InstanceStatus(ctx, ec2Client, records[i].SandboxID)
			}
		}
	}

	if jsonOutput {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(records)
	}

	return printSandboxTable(cmd, records)
}

// awsSandboxLister is the real AWS-backed SandboxLister implementation.
type awsSandboxLister struct {
	s3Client  kmaws.S3ListAPI
	tagClient kmaws.TagAPI
	bucket    string
}

// newRealLister creates an awsSandboxLister from an AWS config.
func newRealLister(awsCfg awssdk.Config, bucket string) *awsSandboxLister {
	return &awsSandboxLister{
		s3Client:  s3.NewFromConfig(awsCfg),
		tagClient: resourcegroupstaggingapi.NewFromConfig(awsCfg),
		bucket:    bucket,
	}
}

// ListSandboxes implements SandboxLister using real AWS clients.
func (l *awsSandboxLister) ListSandboxes(ctx context.Context, useTagScan bool) ([]kmaws.SandboxRecord, error) {
	if useTagScan {
		return kmaws.ListAllSandboxesByTags(ctx, l.tagClient, l.bucket)
	}
	return kmaws.ListAllSandboxesByS3(ctx, l.s3Client, l.bucket)
}

// printSandboxTable writes a human-readable tab-aligned table to cmd.OutOrStdout.
// Each row is numbered 1-N so users can reference sandboxes by number in other commands.
// Status is color-coded: red for "failed", yellow for "partial"/"killed", green for "running".
func printSandboxTable(cmd *cobra.Command, records []kmaws.SandboxRecord) error {
	out := cmd.OutOrStdout()
	// Use fixed-width printf instead of tabwriter to avoid ANSI color codes
	// breaking column alignment (tabwriter counts bytes, not visible chars).
	fmt.Fprintf(out, "%-3s %-14s %-12s %-10s %-12s %-10s %s\n",
		"#", "SANDBOX ID", "PROFILE", "SUBSTRATE", "REGION", "STATUS", "TTL")
	for i, r := range records {
		ttl := r.TTLRemaining
		if ttl == "" {
			ttl = "-"
		}
		// Pad status to fixed width BEFORE adding color codes
		paddedStatus := fmt.Sprintf("%-10s", r.Status)
		colorStatus := colorizeRaw(r.Status, paddedStatus)
		fmt.Fprintf(out, "%-3d %-14s %-12s %-10s %-12s %s %s\n",
			i+1, r.SandboxID, r.Profile, r.Substrate, r.Region, colorStatus, ttl)
	}
	return nil
}

// colorizeListStatus returns the status string wrapped in ANSI color codes for display.
// "failed"  → red
// "partial" → yellow
// "killed"  → yellow (unexpected termination, needs attention)
// "running" → green
// others    → no color
func colorizeListStatus(status string) string {
	switch status {
	case "failed":
		return ansiRed + status + ansiReset
	case "partial", "killed", "starting":
		return ansiYellow + status + ansiReset
	case "running":
		return ansiGreen + status + ansiReset
	default:
		return status
	}
}

// colorizeRaw wraps a pre-padded display string with ANSI color based on the raw status value.
func colorizeRaw(status, display string) string {
	switch status {
	case "failed":
		return ansiRed + display + ansiReset
	case "partial", "killed", "starting":
		return ansiYellow + display + ansiReset
	case "running":
		return ansiGreen + display + ansiReset
	default:
		return display
	}
}

// checkEC2InstanceStatus looks up the EC2 instance for a sandbox by tag and returns
// the live status: "running", "stopped", "terminated" (shown as "killed"), etc.
func checkEC2InstanceStatus(ctx context.Context, client *ec2.Client, sandboxID string) string {
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   awssdk.String("tag:km:sandbox-id"),
				Values: []string{sandboxID},
			},
		},
	})
	if err != nil || len(out.Reservations) == 0 {
		return "killed" // can't find instance — likely terminated and gone
	}

	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			switch inst.State.Name {
			case ec2types.InstanceStateNameRunning:
				return "running"
			case ec2types.InstanceStateNameStopped:
				return "stopped"
			case ec2types.InstanceStateNameTerminated, ec2types.InstanceStateNameShuttingDown:
				return "killed"
			case ec2types.InstanceStateNamePending:
				return "starting"
			default:
				return string(inst.State.Name)
			}
		}
	}
	return "killed"
}
