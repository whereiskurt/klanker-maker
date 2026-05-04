package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/localnumber"
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
	var wide bool

	cmd := &cobra.Command{
		Use:          "list",
		Aliases:      []string{"ls"},
		Short:        "List all running sandboxes",
		Long:         helpText("list"),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd, cfg, lister, jsonOutput, useTagScan, wide)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON array")
	cmd.Flags().BoolVar(&useTagScan, "tags", false, "Use AWS tag scan instead of S3 state scan")
	cmd.Flags().BoolVar(&wide, "wide", false, "Show all columns (profile, substrate, region)")
	return cmd
}

// SandboxLister abstracts the sandbox discovery mechanism for testability.
type SandboxLister interface {
	ListSandboxes(ctx context.Context, useTagScan bool) ([]kmaws.SandboxRecord, error)
}

// runList is the command RunE logic, accepting an explicit lister for testability.
func runList(cmd *cobra.Command, cfg *config.Config, lister SandboxLister, jsonOutput, useTagScan, wide bool) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if lister == nil {
		awsProfile := "klanker-terraform"
		awsCfg, err := kmaws.LoadAWSConfig(ctx, awsProfile)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		tableName := cfg.SandboxTableName
		if tableName == "" {
			tableName = "km-sandboxes"
		}
		lister = newRealLister(awsCfg, cfg.StateBucket, tableName)
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
			if strings.HasPrefix(records[i].Substrate, "ec2") {
				if records[i].Status == "running" {
					records[i].Status = checkEC2InstanceStatus(ctx, ec2Client, records[i].SandboxID)
				}
				records[i].Hibernation = checkEC2Hibernation(ctx, ec2Client, records[i].SandboxID)
			}
		}
	}

	// Compute idle remaining for running sandboxes (wide mode or JSON)
	if wide || jsonOutput {
		for i := range records {
			if records[i].Status == "running" && records[i].IdleTimeout != "" {
				remaining := computeIdleRemaining(ctx, records[i].SandboxID, records[i].IdleTimeout, records[i].CreatedAt, nil)
				if remaining >= 0 {
					records[i].IdleRemaining = formatIdleLabel(remaining, false)
				}
			}
		}
	}

	// Load active thread counts for --wide display (km-slack-threads, grouped by channel_id).
	// Only attempted when at least one sandbox has SlackInboundQueueURL set.
	if wide {
		hasInbound := false
		for _, r := range records {
			if r.SlackInboundQueueURL != "" {
				hasInbound = true
				break
			}
		}
		if hasInbound && ec2Err == nil {
			ddbClient := dynamodb.NewFromConfig(awsCfg)
			threadsTable := cfg.GetSlackThreadsTableName()
			for i := range records {
				if records[i].SlackChannelID == "" {
					continue
				}
				count, countErr := countActiveThreads(ctx, ddbClient, threadsTable, records[i].SlackChannelID)
				if countErr == nil {
					records[i].ActiveThreads = count
				}
			}
		}
	}

	// Reconcile local sandbox numbers with live DynamoDB state.
	lnState, _ := localnumber.Load()
	if lnState == nil {
		lnState = &localnumber.State{Next: 1, Map: map[string]int{}}
	}
	liveIDs := make([]string, len(records))
	for i, r := range records {
		liveIDs[i] = r.SandboxID
	}
	localnumber.Reconcile(lnState, liveIDs)
	_ = localnumber.Save(lnState) // best-effort

	// Sort records by local number (ascending).
	numbers := lnState.Map
	sort.Slice(records, func(i, j int) bool {
		ni := numbers[records[i].SandboxID]
		nj := numbers[records[j].SandboxID]
		return ni < nj
	})

	if jsonOutput {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(records)
	}

	return printSandboxTable(cmd, records, wide, lnState.Map)
}

// awsSandboxLister is the real AWS-backed SandboxLister implementation.
type awsSandboxLister struct {
	s3Client     kmaws.S3ListAPI
	tagClient    kmaws.TagAPI
	dynamoClient kmaws.SandboxMetadataAPI
	bucket       string
	tableName    string
}

// newRealLister creates an awsSandboxLister from an AWS config.
func newRealLister(awsCfg awssdk.Config, bucket, tableName string) *awsSandboxLister {
	return &awsSandboxLister{
		s3Client:     s3.NewFromConfig(awsCfg),
		tagClient:    resourcegroupstaggingapi.NewFromConfig(awsCfg),
		dynamoClient: dynamodb.NewFromConfig(awsCfg),
		bucket:       bucket,
		tableName:    tableName,
	}
}

// ListSandboxes implements SandboxLister using real AWS clients.
// Primary: DynamoDB Scan (O(1) per page, no N GetObject calls).
// Fallback to S3 on ResourceNotFoundException (table not yet provisioned).
func (l *awsSandboxLister) ListSandboxes(ctx context.Context, useTagScan bool) ([]kmaws.SandboxRecord, error) {
	if useTagScan {
		return kmaws.ListAllSandboxesByTags(ctx, l.tagClient, l.bucket)
	}
	records, err := kmaws.ListAllSandboxesByDynamo(ctx, l.dynamoClient, l.tableName)
	if err != nil {
		var rnf *dynamodbtypes.ResourceNotFoundException
		if errors.As(err, &rnf) {
			// Table doesn't exist — fall back to S3
			if l.bucket == "" {
				return nil, fmt.Errorf("state bucket not configured: set KM_STATE_BUCKET or state_bucket in km-config.yaml")
			}
			return kmaws.ListAllSandboxesByS3(ctx, l.s3Client, l.bucket)
		}
		return nil, err
	}
	return records, nil
}

// printSandboxTable writes a human-readable tab-aligned table to cmd.OutOrStdout.
// Each row is numbered using persistent local numbers from the numbers map (falling back
// to positional i+1 if no local number is available). Pass nil for numbers to use positional.
// Status is color-coded: red for "failed", yellow for "partial"/"killed", green for "running".
// Locked sandboxes are shown in bold white with a lock icon.
// When wide=false, profile/substrate/region columns are hidden for a narrower display.
func printSandboxTable(cmd *cobra.Command, records []kmaws.SandboxRecord, wide bool, numbers map[string]int) error {
	out := cmd.OutOrStdout()
	// Use fixed-width printf instead of tabwriter to avoid ANSI color codes
	// breaking column alignment (tabwriter counts bytes, not visible chars).
	// Compute max sandbox ID width for dynamic column sizing
	idWidth := len("SANDBOX ID")
	for _, r := range records {
		if len(r.SandboxID) > idWidth {
			idWidth = len(r.SandboxID)
		}
	}
	idWidth += 2 // padding

	// truncCol truncates a string to maxLen, adding ".." suffix if truncated.
	truncCol := func(s string, maxLen int) string {
		if len(s) <= maxLen {
			return s
		}
		if maxLen <= 2 {
			return s[:maxLen]
		}
		return s[:maxLen-2] + ".."
	}

	// Determine whether to show 💬 column: only when at least one sandbox has
	// inbound enabled (SlackInboundQueueURL set) to avoid an empty column.
	showThreads := false
	if wide {
		for _, r := range records {
			if r.SlackInboundQueueURL != "" {
				showThreads = true
				break
			}
		}
	}

	if wide {
		if showThreads {
			fmt.Fprintf(out, "%-3s %-8s  %-*s %-16s %-10s %-12s %-10s %-6s %-6s %-5s %s\n",
				"#", "ALIAS", idWidth, "SANDBOX ID", "PROFILE", "SUBSTRATE", "REGION", "STATUS", "TTL", "IDLE", "💬", "CLONED FROM")
		} else {
			fmt.Fprintf(out, "%-3s %-8s  %-*s %-16s %-10s %-12s %-10s %-6s %-6s %s\n",
				"#", "ALIAS", idWidth, "SANDBOX ID", "PROFILE", "SUBSTRATE", "REGION", "STATUS", "TTL", "IDLE", "CLONED FROM")
		}
	} else {
		fmt.Fprintf(out, "%-3s %-8s  %-*s %-10s %s\n",
			"#", "ALIAS", idWidth, "SANDBOX ID", "STATUS", "TTL")
	}
	for i, r := range records {
		ttl := r.TTLRemaining
		if ttl == "" {
			ttl = "-"
		}
		alias := truncCol(r.Alias, 8)
		if alias == "" {
			alias = "-"
		}
		profile := truncCol(r.Profile, 16)
		// Pad status to fixed width BEFORE adding color codes
		statusLabel := r.Status
		if wide && r.Hibernation {
			statusLabel += "(h)"
		}
		paddedStatus := fmt.Sprintf("%-10s", statusLabel)
		colorStatus := colorizeRaw(r.Status, false, paddedStatus)
		lock := ""
		if r.Locked {
			lock = " 🔒"
		}
		bw := func(s string) string {
			if r.Locked {
				return ansiBoldWhite + s + ansiReset
			}
			return s
		}
		localNum := 0
		if numbers != nil {
			localNum = numbers[r.SandboxID]
		}
		if localNum == 0 {
			localNum = i + 1 // fallback to positional if no local number
		}
		num := bw(fmt.Sprintf("%-3d", localNum))
		if wide {
			idle := r.IdleRemaining
			if idle == "" {
				idle = "-"
			}
			// Strip " remaining" suffix for compact display
			idle = strings.TrimSuffix(idle, " remaining")
			clonedFrom := r.ClonedFrom
			if clonedFrom == "" {
				clonedFrom = "-"
			}
			if showThreads {
				threads := "-"
				if r.SlackChannelID != "" {
					threads = fmt.Sprintf("%d", r.ActiveThreads)
				}
				fmt.Fprintf(out, "%s %s  %s %s %s %s %s %-6s %-6s %-5s %s%s\n",
					num, bw(fmt.Sprintf("%-8s", alias)), bw(fmt.Sprintf("%-*s", idWidth, r.SandboxID)),
					bw(fmt.Sprintf("%-16s", profile)), bw(fmt.Sprintf("%-10s", r.Substrate)),
					bw(fmt.Sprintf("%-12s", r.Region)), colorStatus, bw(ttl), bw(idle),
					bw(threads), bw(truncCol(clonedFrom, 14)), lock)
			} else {
				fmt.Fprintf(out, "%s %s  %s %s %s %s %s %-6s %-6s %s%s\n",
					num, bw(fmt.Sprintf("%-8s", alias)), bw(fmt.Sprintf("%-*s", idWidth, r.SandboxID)),
					bw(fmt.Sprintf("%-16s", profile)), bw(fmt.Sprintf("%-10s", r.Substrate)),
					bw(fmt.Sprintf("%-12s", r.Region)), colorStatus, bw(ttl), bw(idle),
					bw(truncCol(clonedFrom, 14)), lock)
			}
		} else {
			fmt.Fprintf(out, "%s %s  %s %s %s%s\n",
				num, bw(fmt.Sprintf("%-8s", alias)), bw(fmt.Sprintf("%-*s", idWidth, r.SandboxID)),
				colorStatus, bw(ttl), lock)
		}
	}
	return nil
}

// colorizeListStatus returns the status string wrapped in ANSI color codes for display.
// "failed"  → red
// "partial" → yellow
// "killed"  → yellow (unexpected termination, needs attention)
// "reaped"  → yellow (spot instance reclaimed by AWS)
// "paused"  → magenta (hibernated or stopped, can resume)
// "stopped" → magenta (stopped, can resume)
// "running" → green
// others    → no color
func colorizeListStatus(status string) string {
	switch status {
	case "failed", "nocap":
		return ansiRed + status + ansiReset
	case "partial", "killed", "reaped", "starting":
		return ansiYellow + status + ansiReset
	case "paused", "stopped":
		return ansiMagenta + status + ansiReset
	case "running":
		return ansiGreen + status + ansiReset
	default:
		return status
	}
}

// colorizeRaw wraps a pre-padded display string with ANSI color based on the raw status value.
func colorizeRaw(status string, _ bool, display string) string {
	switch status {
	case "failed", "nocap":
		return ansiRed + display + ansiReset
	case "partial", "killed", "reaped", "starting":
		return ansiYellow + display + ansiReset
	case "paused", "stopped":
		return ansiMagenta + display + ansiReset
	case "running":
		return ansiGreen + display + ansiReset
	default:
		return display
	}
}

// checkEC2Hibernation looks up the EC2 instance for a sandbox by tag and returns
// whether hibernation is configured on the instance.
func checkEC2Hibernation(ctx context.Context, client *ec2.Client, sandboxID string) bool {
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   awssdk.String("tag:km:sandbox-id"),
				Values: []string{sandboxID},
			},
		},
	})
	if err != nil || len(out.Reservations) == 0 {
		return false
	}
	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			if inst.HibernationOptions != nil && inst.HibernationOptions.Configured != nil {
				return *inst.HibernationOptions.Configured
			}
		}
	}
	return false
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
				if inst.StateReason != nil && inst.StateReason.Code != nil &&
					*inst.StateReason.Code == "Server.SpotInstanceTermination" {
					return "reaped"
				}
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
