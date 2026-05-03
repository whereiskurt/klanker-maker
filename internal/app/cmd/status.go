package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ANSI color codes for terminal output.
// Disabled when output is not a TTY.
const (
	ansiGreen     = "\033[32m"
	ansiYellow    = "\033[33m"
	ansiRed       = "\033[31m"
	ansiMagenta   = "\033[35m"
	ansiBold      = "\033[1m"
	ansiBoldWhite = "\033[1;37m"
	ansiReset     = "\033[0m"
)

// BudgetFetcher abstracts fetching budget data for a sandbox.
type BudgetFetcher interface {
	FetchBudget(ctx context.Context, sandboxID string) (*kmaws.BudgetSummary, error)
}

// IdentityFetcher abstracts fetching identity data for a sandbox.
type IdentityFetcher interface {
	FetchIdentity(ctx context.Context, sandboxID string) (*kmaws.IdentityRecord, error)
}

// NewStatusCmd creates the "km status" subcommand.
// Usage: km status <sandbox-id>
//
// Prints detailed state for a sandbox: resources (ARNs), metadata (profile, substrate),
// and timestamps (created, TTL expiry), plus budget breakdown if available.
func NewStatusCmd(cfg *config.Config) *cobra.Command {
	return NewStatusCmdWithFetcher(cfg, nil)
}

// NewStatusCmdWithFetcher builds the status command with an optional custom fetcher.
// If fetcher is nil, the real AWS-backed fetcher is used. Used in tests for DI.
func NewStatusCmdWithFetcher(cfg *config.Config, fetcher SandboxFetcher) *cobra.Command {
	return NewStatusCmdWithFetchers(cfg, fetcher, nil)
}

// NewStatusCmdWithFetchers builds the status command with optional custom fetchers for
// both sandbox metadata and budget data. Pass nil for real AWS-backed clients.
func NewStatusCmdWithFetchers(cfg *config.Config, fetcher SandboxFetcher, budgetFetcher BudgetFetcher) *cobra.Command {
	return NewStatusCmdWithAllFetchers(cfg, fetcher, budgetFetcher, nil)
}

// NewStatusCmdWithAllFetchers builds the status command with optional custom fetchers for
// sandbox metadata, budget, and identity data. Pass nil for real AWS-backed clients.
func NewStatusCmdWithAllFetchers(cfg *config.Config, fetcher SandboxFetcher, budgetFetcher BudgetFetcher, identityFetcher IdentityFetcher) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "status [sandbox-id | #number]",
		Short:        "Show detailed state for a sandbox",
		Long:         helpText("status"),
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			if len(args) == 0 {
				return runStatusAll(cmd, cfg, fetcher, budgetFetcher, identityFetcher)
			}
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}
			return runStatus(cmd, cfg, fetcher, budgetFetcher, identityFetcher, sandboxID)
		},
	}
	return cmd
}

// SandboxFetcher abstracts fetching a single sandbox's full status.
type SandboxFetcher interface {
	FetchSandbox(ctx context.Context, sandboxID string) (*kmaws.SandboxRecord, error)
}

// runStatusAll lists all sandboxes and runs status on each one.
func runStatusAll(cmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, budgetFetcher BudgetFetcher, identityFetcher IdentityFetcher) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	records, err := listSandboxes(ctx, cfg)
	if err != nil {
		return fmt.Errorf("list sandboxes: %w", err)
	}
	if len(records) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No sandboxes found")
		return nil
	}

	for i, rec := range records {
		if i > 0 {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		if err := runStatus(cmd, cfg, fetcher, budgetFetcher, identityFetcher, rec.SandboxID); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "  error fetching status for %s: %v\n", rec.SandboxID, err)
		}
	}
	return nil
}

// runStatus is the command RunE logic for km status.
func runStatus(cmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, budgetFetcher BudgetFetcher, identityFetcher IdentityFetcher, sandboxID string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if fetcher == nil {
		awsProfile := "klanker-terraform"
		awsCfg, err := kmaws.LoadAWSConfig(ctx, awsProfile)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		tableName := cfg.SandboxTableName
		if tableName == "" {
			tableName = "km-sandboxes"
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket, tableName)

		// Also initialize real budget fetcher if not injected
		if budgetFetcher == nil {
			tableName := cfg.BudgetTableName
			if tableName == "" {
				tableName = "km-budgets"
			}
			budgetFetcher = &realBudgetFetcher{
				client:    dynamodb.NewFromConfig(awsCfg),
				tableName: tableName,
			}
		}

		// Also initialize real identity fetcher if not injected
		if identityFetcher == nil {
			tableName := cfg.IdentityTableName
			if tableName == "" {
				tableName = "km-identities"
			}
			identityFetcher = &realIdentityFetcher{
				client:    dynamodb.NewFromConfig(awsCfg),
				tableName: tableName,
			}
		}
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, kmaws.ErrSandboxNotFound) {
			return fmt.Errorf("sandbox not found: %s", sandboxID)
		}
		return fmt.Errorf("fetch sandbox status: %w", err)
	}

	// Fetch budget data (graceful degradation — may be nil if sandbox has no budget)
	var budget *kmaws.BudgetSummary
	if budgetFetcher != nil {
		budget, _ = budgetFetcher.FetchBudget(ctx, sandboxID)
		// Ignore error: sandbox may have no budget defined. Budget section simply omitted.
	}

	// Fetch identity data (graceful degradation — may be nil if sandbox has no identity)
	var identity *kmaws.IdentityRecord
	if identityFetcher != nil {
		identity, _ = identityFetcher.FetchIdentity(ctx, sandboxID)
		// Ignore error: sandbox may have no identity published. Identity section simply omitted.
	}

	bannerID := sandboxID
	if identity != nil && identity.Alias != "" {
		bannerID = fmt.Sprintf("%s (%s)", sandboxID, identity.Alias)
	} else if rec.Alias != "" {
		bannerID = fmt.Sprintf("%s (%s)", sandboxID, rec.Alias)
	}
	fprintBanner(cmd.OutOrStdout(), "km status", bannerID)
	isTTY := isTerminal(cmd.OutOrStdout())
	printSandboxStatus(ctx, cmd, rec, budget, identity, isTTY)
	return nil
}

// awsSandboxFetcher is the real AWS-backed SandboxFetcher.
type awsSandboxFetcher struct {
	s3Client     kmaws.S3ListAPI
	tagClient    kmaws.TagAPI
	dynamoClient kmaws.SandboxMetadataAPI
	bucket       string
	tableName    string
}

// newRealFetcher creates an awsSandboxFetcher from an AWS config.
func newRealFetcher(awsCfg awssdk.Config, bucket, tableName string) *awsSandboxFetcher {
	return &awsSandboxFetcher{
		s3Client:     s3.NewFromConfig(awsCfg),
		tagClient:    resourcegroupstaggingapi.NewFromConfig(awsCfg),
		dynamoClient: dynamodb.NewFromConfig(awsCfg),
		bucket:       bucket,
		tableName:    tableName,
	}
}

// FetchSandbox reads metadata from DynamoDB (with S3 fallback) and resource ARNs from the tagging API.
func (f *awsSandboxFetcher) FetchSandbox(ctx context.Context, sandboxID string) (*kmaws.SandboxRecord, error) {
	// Read metadata from DynamoDB; fall back to S3 on ResourceNotFoundException.
	meta, err := kmaws.ReadSandboxMetadataDynamo(ctx, f.dynamoClient, f.tableName, sandboxID)
	if err != nil {
		var rnf *dynamodbtypes.ResourceNotFoundException
		if errors.As(err, &rnf) && f.bucket != "" {
			// Table doesn't exist — fall back to S3
			meta, err = kmaws.ReadSandboxMetadata(ctx, f.s3Client, f.bucket, sandboxID)
		}
		if err != nil {
			return nil, err
		}
	}

	// Get resource ARNs via tag API
	loc, err := kmaws.FindSandboxByID(ctx, f.tagClient, sandboxID)
	if err != nil && !errors.Is(err, kmaws.ErrSandboxNotFound) {
		return nil, fmt.Errorf("fetch resources for sandbox %s: %w", sandboxID, err)
	}

	status := meta.Status
	if status == "" {
		status = "running" // backward compat: old metadata without status field
	}

	rec := &kmaws.SandboxRecord{
		SandboxID:   meta.SandboxID,
		Profile:     meta.ProfileName,
		Substrate:   meta.Substrate,
		Region:      meta.Region,
		Status:      status,
		CreatedAt:   meta.CreatedAt,
		TTLExpiry:   meta.TTLExpiry,
		IdleTimeout: meta.IdleTimeout,
	}
	if loc != nil {
		rec.Resources = loc.ResourceARNs
	}

	// Live EC2 instance check: detect killed/stopped/terminated
	if strings.HasPrefix(rec.Substrate, "ec2") && rec.Status == "running" {
		awsCfg, cfgErr := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if cfgErr == nil {
			ec2Client := ec2.NewFromConfig(awsCfg)
			rec.Status = checkEC2InstanceStatus(ctx, ec2Client, sandboxID)
		}
	}

	return rec, nil
}

// realBudgetFetcher is the real AWS-backed BudgetFetcher.
type realBudgetFetcher struct {
	client    kmaws.BudgetAPI
	tableName string
}

// FetchBudget reads budget data from DynamoDB for a sandbox.
func (f *realBudgetFetcher) FetchBudget(ctx context.Context, sandboxID string) (*kmaws.BudgetSummary, error) {
	return kmaws.GetBudget(ctx, f.client, f.tableName, sandboxID)
}

// realIdentityFetcher is the real AWS-backed IdentityFetcher.
type realIdentityFetcher struct {
	client    kmaws.IdentityTableAPI
	tableName string
}

// FetchIdentity reads identity data from DynamoDB for a sandbox.
func (f *realIdentityFetcher) FetchIdentity(ctx context.Context, sandboxID string) (*kmaws.IdentityRecord, error) {
	return kmaws.FetchPublicKey(ctx, f.client, f.tableName, sandboxID)
}

// isTerminal returns true if the writer is a real TTY (supports ANSI codes).
func isTerminal(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		fi, err := f.Stat()
		if err != nil {
			return false
		}
		// Check for character device (terminal)
		return (fi.Mode() & os.ModeCharDevice) != 0
	}
	return false
}

// colorPercent wraps a percentage string with ANSI color codes based on threshold.
// green < 80%, yellow 80-99%, red >= 100%.
func colorPercent(percent float64, isTTY bool) string {
	if !isTTY {
		return fmt.Sprintf("%.1f%%", percent)
	}
	var colorCode string
	switch {
	case percent >= 100.0:
		colorCode = ansiRed
	case percent >= 80.0:
		colorCode = ansiYellow
	default:
		colorCode = ansiGreen
	}
	return fmt.Sprintf("%s%.1f%%%s", colorCode, percent, ansiReset)
}

// printSandboxStatus prints detailed sandbox information including optional budget and identity sections.
func printSandboxStatus(ctx context.Context, cmd *cobra.Command, rec *kmaws.SandboxRecord, budget *kmaws.BudgetSummary, identity *kmaws.IdentityRecord, isTTY bool) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Sandbox ID:  %s\n", rec.SandboxID)
	if rec.Alias != "" {
		fmt.Fprintf(out, "Alias:       %s\n", rec.Alias)
	} else if identity != nil && identity.Alias != "" {
		fmt.Fprintf(out, "Alias:       %s\n", identity.Alias)
	}
	fmt.Fprintf(out, "Profile:     %s\n", rec.Profile)
	fmt.Fprintf(out, "Substrate:   %s\n", rec.Substrate)
	fmt.Fprintf(out, "Region:      %s\n", rec.Region)
	statusDisplay := rec.Status
	if isTTY {
		statusDisplay = colorizeListStatus(rec.Status)
	}
	fmt.Fprintf(out, "Status:      %s\n", statusDisplay)
	fmt.Fprintf(out, "Created At:  %s\n", rec.CreatedAt.Local().Format("2006-01-02 3:04:05 PM MST"))
	if rec.TTLExpiry != nil {
		fmt.Fprintf(out, "TTL Expiry:  %s\n", rec.TTLExpiry.Local().Format("2006-01-02 3:04:05 PM MST"))
	}

	// Show idle countdown — try metadata first, fall back to profile default
	if rec.Status == "running" {
		idleTimeout := rec.IdleTimeout
		if idleTimeout == "" {
			idleTimeout = "15m" // default if not in metadata (old sandboxes)
		}
		idleStr := getIdleCountdown(context.Background(), rec.SandboxID, idleTimeout, rec.CreatedAt, isTTY, budget)
		if idleStr != "" {
			idleLabel := "Idle Kill"
			if rec.TeardownPolicy == "stop" || rec.TeardownPolicy == "retain" {
				idleLabel = "Idle Stop"
			}
			fmt.Fprintf(out, "%s:  %s\n", idleLabel, idleStr)
		}
	}

	if len(rec.Resources) > 0 {
		fmt.Fprintf(out, "Resources (%d):\n", len(rec.Resources))
		for _, arn := range rec.Resources {
			fmt.Fprintf(out, "  - %s\n", arn)
		}
	}

	// Budget section — only printed when budget data is available and has non-zero limits
	if budget != nil && (budget.ComputeLimit > 0 || budget.AILimit > 0) {
		fmt.Fprintf(out, "Budget:\n")

		if budget.ComputeLimit > 0 {
			pct := 0.0
			if budget.ComputeLimit > 0 {
				pct = (budget.ComputeSpent / budget.ComputeLimit) * 100
			}
			fmt.Fprintf(out, "  Compute: $%.4f / $%.4f (%s)\n",
				budget.ComputeSpent, budget.ComputeLimit, colorPercent(pct, isTTY))
		}

		if budget.AILimit > 0 {
			pct := 0.0
			if budget.AILimit > 0 {
				pct = (budget.AISpent / budget.AILimit) * 100
			}
			fmt.Fprintf(out, "  AI:      $%.4f / $%.4f (%s)\n",
				budget.AISpent, budget.AILimit, colorPercent(pct, isTTY))

			// Per-model breakdown (sorted for deterministic output)
			if len(budget.AIByModel) > 0 {
				models := make([]string, 0, len(budget.AIByModel))
				for modelID := range budget.AIByModel {
					models = append(models, modelID)
				}
				sort.Strings(models)
				for _, modelID := range models {
					ms := budget.AIByModel[modelID]
					fmt.Fprintf(out, "    %-30s $%.4f (%dK in / %dK out)\n",
						modelID+":",
						ms.SpentUSD,
						ms.InputTokens/1000,
						ms.OutputTokens/1000,
					)
				}
			}
		}

		warnPct := budget.WarningThreshold
		if warnPct == 0 {
			warnPct = 0.80
		}
		fmt.Fprintf(out, "  Warning threshold: %.0f%%\n", warnPct*100)
	}

	// Slack inbound section — only printed when sandbox has Slack channel set.
	// Shows queue URL and depth for inbound-enabled sandboxes, or "disabled" for
	// outbound-only Slack sandboxes (Phase 63).
	if rec.SlackInboundQueueURL != "" {
		fmt.Fprintf(out, "Slack Inbound:\n")
		fmt.Fprintf(out, "  queue:        %s\n", rec.SlackInboundQueueURL)

		// Lazy-init SQS client — mirrors the EC2 client pattern in computeIdleRemaining.
		awsCfg, sqsErr := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if sqsErr == nil {
			sqsClient := sqs.NewFromConfig(awsCfg)
			depth, depthErr := kmaws.QueueDepth(ctx, sqsClient, rec.SlackInboundQueueURL)
			if depthErr != nil {
				fmt.Fprintf(out, "  queue depth:  <error: %v>\n", depthErr)
			} else {
				fmt.Fprintf(out, "  queue depth:  %d message(s) waiting\n", depth)
			}
		} else {
			fmt.Fprintf(out, "  queue depth:  <error: %v>\n", sqsErr)
		}

		// Thread count from km-slack-threads DDB.
		if rec.SlackChannelID != "" {
			awsCfg2, ddbErr := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
			if ddbErr == nil {
				ddbClient := dynamodb.NewFromConfig(awsCfg2)
				threadCount, threadErr := countActiveThreads(ctx, ddbClient, "km-slack-threads", rec.SlackChannelID)
				if threadErr != nil {
					fmt.Fprintf(out, "  threads:      <error: %v>\n", threadErr)
				} else {
					fmt.Fprintf(out, "  threads:      %d active\n", threadCount)
				}
			}
		}
	} else if rec.SlackChannelID != "" {
		// Sandbox has Slack outbound (Phase 63) but no inbound — note for clarity.
		fmt.Fprintf(out, "Slack Inbound: disabled\n")
	}

	// Identity section — only printed when identity record is available and has a public key
	if identity != nil && identity.PublicKeyB64 != "" {
		fmt.Fprintf(out, "Identity:\n")

		// Truncate public key to first 16 chars for display
		displayKey := identity.PublicKeyB64
		if len(displayKey) > 16 {
			displayKey = displayKey[:16] + "..."
		}
		fmt.Fprintf(out, "  Public Key:      %s\n", displayKey)

		signing := identity.Signing
		if signing == "" {
			signing = "unknown"
		}
		fmt.Fprintf(out, "  Signing:         %s\n", signing)

		verifyInbound := identity.VerifyInbound
		if verifyInbound == "" {
			verifyInbound = "unknown"
		}
		fmt.Fprintf(out, "  Verify Inbound:  %s\n", verifyInbound)

		encryption := identity.Encryption
		if encryption == "" {
			encryption = "unknown"
		}
		fmt.Fprintf(out, "  Encryption:      %s\n", encryption)

		if identity.Alias != "" {
			fmt.Fprintf(out, "  Alias:            %s\n", identity.Alias)
		}
		if len(identity.AllowedSenders) > 0 {
			fmt.Fprintf(out, "  Allowed Senders:  %s\n", strings.Join(identity.AllowedSenders, ", "))
		} else {
			fmt.Fprintf(out, "  Allowed Senders:  not configured\n")
		}
	}
}

// getIdleCountdown checks CloudWatch audit events and budget AI activity to find
// the most recent sandbox activity, then returns a countdown string like "12m remaining"
// colored by urgency. createdAt is the fallback when no activity signals are found.
func getIdleCountdown(ctx context.Context, sandboxID, idleTimeout string, createdAt time.Time, isTTY bool, budget *kmaws.BudgetSummary) string {
	remaining := computeIdleRemaining(ctx, sandboxID, idleTimeout, createdAt, budget)
	if remaining < 0 {
		return ""
	}
	return formatIdleLabel(remaining, isTTY)
}

// computeIdleRemaining returns the idle time remaining for a sandbox.
// Returns -1 if idle timeout is not configured or cannot be determined.
// Uses multiple activity signals: CloudWatch audit events, budget AI spend,
// active SSM sessions, and sandbox creation time as fallback.
func computeIdleRemaining(ctx context.Context, sandboxID, idleTimeout string, createdAt time.Time, budget *kmaws.BudgetSummary) time.Duration {
	idleDur, parseErr := time.ParseDuration(idleTimeout)
	if parseErr != nil || idleDur == 0 {
		return -1
	}

	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return -1
	}

	// Determine the most recent activity timestamp from multiple signals.
	lastActivity := createdAt

	// Signal 1: EC2 instance launch/resume time — captures when the instance
	// entered "running" state, including after hibernate resume. This prevents
	// showing "imminent" immediately after resume when no audit events exist yet.
	ec2Client := ec2.NewFromConfig(awsCfg)
	descOut, descErr := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("tag:km:sandbox-id"), Values: []string{sandboxID}},
			{Name: awssdk.String("instance-state-name"), Values: []string{"running"}},
		},
	})
	if descErr == nil {
		for _, res := range descOut.Reservations {
			for _, inst := range res.Instances {
				// StateTransitionReason contains the timestamp when the instance
				// entered running state (e.g. after resume). LaunchTime is the
				// original launch and doesn't change on hibernate/resume.
				// Use the later of LaunchTime and the transition reason timestamp.
				if inst.LaunchTime != nil && inst.LaunchTime.After(lastActivity) {
					lastActivity = *inst.LaunchTime
				}
				// Parse transition reason for resume timestamp (format: "User initiated (YYYY-MM-DD HH:MM:SS GMT)")
				if reason := awssdk.ToString(inst.StateTransitionReason); reason != "" {
					if t, parseErr := parseStateTransitionTime(reason); parseErr == nil && t.After(lastActivity) {
						lastActivity = t
					}
				}
			}
		}
	}

	// Signal 2: CloudWatch audit events (shell commands, heartbeats)
	cwClient := cloudwatchlogs.NewFromConfig(awsCfg)
	logGroup := "/km/sandboxes/" + sandboxID + "/"
	events, err := kmaws.GetLogEvents(ctx, cwClient, logGroup, "audit", 10)
	if err == nil && len(events) > 0 {
		// Events are returned from the tail; last element is most recent.
		latest := time.UnixMilli(events[len(events)-1].Timestamp)
		if latest.After(lastActivity) {
			lastActivity = latest
		}
	}

	// Signal 3: Budget AI spend updates (Bedrock/API usage)
	if budget != nil && budget.LastAIActivity != nil && budget.LastAIActivity.After(lastActivity) {
		lastActivity = *budget.LastAIActivity
	}

	// Signal 4: Active SSM sessions — if any session is connected, sandbox is in use.
	if hasActiveSSMSession(ctx, awsCfg, sandboxID) {
		lastActivity = time.Now()
	}

	remaining := idleDur - time.Since(lastActivity)
	if remaining < 0 {
		remaining = 0
	}
	return remaining.Round(time.Second)
}

// parseStateTransitionTime extracts the timestamp from an EC2 StateTransitionReason string.
// Format: "User initiated (2026-04-10 13:45:22 GMT)" — returns the parsed time or error.
func parseStateTransitionTime(reason string) (time.Time, error) {
	start := strings.Index(reason, "(")
	end := strings.Index(reason, ")")
	if start < 0 || end < 0 || end <= start+1 {
		return time.Time{}, fmt.Errorf("no timestamp in reason: %s", reason)
	}
	return time.Parse("2006-01-02 15:04:05 MST", reason[start+1:end])
}

// formatIdleLabel returns a human-readable idle countdown, optionally color-coded.
func formatIdleLabel(remaining time.Duration, isTTY bool) string {
	label := fmt.Sprintf("%s remaining", remaining)
	if remaining == 0 {
		label = "imminent"
	}

	if !isTTY {
		return label
	}

	switch {
	case remaining < 5*time.Minute:
		return ansiRed + label + ansiReset
	case remaining < 15*time.Minute:
		return ansiYellow + label + ansiReset
	default:
		return ansiGreen + label + ansiReset
	}
}

// hasActiveSSMSession checks if there are any active SSM sessions targeting
// the EC2 instance for the given sandbox. This catches activity from km shell
// and ssm send-command that may not generate CloudWatch audit events.
func hasActiveSSMSession(ctx context.Context, awsCfg awssdk.Config, sandboxID string) bool {
	ssmClient := ssm.NewFromConfig(awsCfg)

	// Look up the instance ID by sandbox tag
	ec2Client := ec2.NewFromConfig(awsCfg)
	descOut, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   awssdk.String("tag:km:sandbox-id"),
				Values: []string{sandboxID},
			},
			{
				Name:   awssdk.String("instance-state-name"),
				Values: []string{"running"},
			},
		},
	})
	if err != nil || len(descOut.Reservations) == 0 {
		return false
	}

	var instanceID string
	for _, res := range descOut.Reservations {
		for _, inst := range res.Instances {
			if inst.InstanceId != nil {
				instanceID = *inst.InstanceId
				break
			}
		}
	}
	if instanceID == "" {
		return false
	}

	// Check for active SSM sessions on this instance
	sessOut, err := ssmClient.DescribeSessions(ctx, &ssm.DescribeSessionsInput{
		State: ssmtypes.SessionStateActive,
		Filters: []ssmtypes.SessionFilter{
			{
				Key:   ssmtypes.SessionFilterKeyTargetId,
				Value: awssdk.String(instanceID),
			},
		},
	})
	if err != nil {
		return false
	}
	return len(sessOut.Sessions) > 0
}

// DDBQueryClient is the narrow DynamoDB interface used by countActiveThreads.
// Satisfied by *dynamodb.Client and by test fakes.
type DDBQueryClient interface {
	Query(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// countActiveThreads returns the number of rows in the given DynamoDB table
// (e.g. km-slack-threads) whose partition key channel_id equals channelID.
// Returns 0 silently when channelID is empty.
func countActiveThreads(ctx context.Context, ddb DDBQueryClient, tableName, channelID string) (int, error) {
	if channelID == "" {
		return 0, nil
	}
	out, err := ddb.Query(ctx, &dynamodb.QueryInput{
		TableName:              awssdk.String(tableName),
		KeyConditionExpression: awssdk.String("channel_id = :cid"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":cid": &dynamodbtypes.AttributeValueMemberS{Value: channelID},
		},
		Select: dynamodbtypes.SelectCount,
	})
	if err != nil {
		return 0, err
	}
	return int(out.Count), nil
}
