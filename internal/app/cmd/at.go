// Package cmd — at.go
// Implements the "km at" command for scheduling deferred and recurring sandbox operations
// using EventBridge Scheduler. Includes "km at list" and "km at cancel" subcommands.
// "km schedule" is registered as an alias in root.go.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	atpkg "github.com/whereiskurt/klankrmkr/pkg/at"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// schedulableCommand defines routing metadata for each schedulable km command.
type schedulableCommand struct {
	// targetARNField is "create" (uses CreateHandlerLambdaARN) or "ttl" (uses TTLLambdaARN).
	targetARNField string
	// eventType is the SandboxIdle event type sent to the TTL Lambda. Empty for "create".
	eventType string
}

// schedulableCommands maps km command names to their scheduler routing metadata.
var schedulableCommands = map[string]schedulableCommand{
	"create":     {targetARNField: "create"},
	"destroy":    {targetARNField: "ttl", eventType: "destroy"},
	"kill":       {targetARNField: "ttl", eventType: "destroy"},
	"stop":       {targetARNField: "ttl", eventType: "stop"},
	"pause":      {targetARNField: "ttl", eventType: "stop"},
	"resume":     {targetARNField: "ttl", eventType: "resume"},
	"extend":     {targetARNField: "ttl", eventType: "extend"},
	"budget-add": {targetARNField: "ttl", eventType: "budget-add"},
}

// atDeps holds injectable dependencies for the at command family (for testing).
type atDeps struct {
	scheduler            awspkg.SchedulerAPI
	dynamo               awspkg.SandboxMetadataAPI
	cfg                  *config.Config
	now                  func() time.Time
	countActiveSandboxes func(ctx context.Context) (int, error)
}

// NewAtCmd creates the "km at" command.
func NewAtCmd(cfg *config.Config) *cobra.Command {
	return newAtCmdInternal(cfg, nil, nil, nil)
}

// NewAtCmdWithDeps creates a testable "km at" command with injected dependencies.
func NewAtCmdWithDeps(cfg *config.Config, sched awspkg.SchedulerAPI, dynamo awspkg.SandboxMetadataAPI, counter func(ctx context.Context) (int, error)) *cobra.Command {
	return newAtCmdInternal(cfg, sched, dynamo, counter)
}

// newAtCmdInternal builds the at command with either real or injected dependencies.
func newAtCmdInternal(cfg *config.Config, schedClient awspkg.SchedulerAPI, dynamoClient awspkg.SandboxMetadataAPI, counter func(ctx context.Context) (int, error)) *cobra.Command {
	var cronFlag string
	var nameFlag string
	var groupFlag string

	cmd := &cobra.Command{
		Use:          "at '<time-expr>' <command> [args...]",
		Short:        "Schedule a sandbox operation for a future time",
		Long:         "Schedule a deferred or recurring sandbox operation using EventBridge Scheduler.\nExamples:\n  km at '10pm tomorrow' create dev.yaml\n  km at 'every thursday at 3pm' kill sb-abc123\n  km at --cron 'cron(0 15 ? * 5 *)' kill sb-abc123",
		SilenceUsage: true,
		Args:         cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			// Build deps (lazy init for real AWS clients)
			deps := buildAtDeps(ctx, cfg, schedClient, dynamoClient, counter)

			// Lazy-init real AWS clients if not injected (production path)
			if deps.scheduler == nil || deps.dynamo == nil {
				awsCfg, err := awspkg.LoadAWSConfig(ctx, resolveAWSProfile(cfg))
				if err != nil {
					return fmt.Errorf("load AWS config: %w", err)
				}
				if deps.scheduler == nil {
					deps.scheduler = scheduler.NewFromConfig(awsCfg)
				}
				if deps.dynamo == nil {
					deps.dynamo = dynamodb.NewFromConfig(awsCfg)
				}
			}

			// When --cron is used, the first positional arg is the command (no time expression arg)
			var timeExprArg string
			var cmdArg string
			var extraArgs []string

			if cronFlag != "" {
				// --cron mode: args[0] is the command
				if len(args) < 1 {
					return fmt.Errorf("usage: km at --cron '<expr>' <command> [args...]")
				}
				cmdArg = args[0]
				if len(args) > 1 {
					extraArgs = args[1:]
				}
			} else {
				// Normal mode: args[0] is time expression, args[1] is command
				timeExprArg = args[0]
				cmdArg = args[1]
				if len(args) > 2 {
					extraArgs = args[2:]
				}
			}

			// Validate command is schedulable
			cmdInfo, ok := schedulableCommands[cmdArg]
			if !ok {
				supported := make([]string, 0, len(schedulableCommands))
				for k := range schedulableCommands {
					supported = append(supported, k)
				}
				return fmt.Errorf("command %q is not schedulable -- only %s are supported", cmdArg, strings.Join(supported, ", "))
			}

			// Resolve target Lambda ARN
			var targetARN string
			switch cmdInfo.targetARNField {
			case "create":
				targetARN = cfg.CreateHandlerLambdaARN
				if targetARN == "" {
					return fmt.Errorf("Lambda ARN not configured -- run 'km init' first")
				}
			case "ttl":
				targetARN = cfg.TTLLambdaARN
				if targetARN == "" {
					return fmt.Errorf("Lambda ARN not configured -- run 'km init' first")
				}
			}

			// Parse the schedule expression
			var spec atpkg.ScheduleSpec
			var err error
			if cronFlag != "" {
				// Raw cron — validate and use as-is
				if err = atpkg.ValidateCron(cronFlag); err != nil {
					return fmt.Errorf("invalid cron expression: %w", err)
				}
				spec = atpkg.ScheduleSpec{
					Expression:  cronFlag,
					IsRecurring: true,
					HumanExpr:   cronFlag,
				}
			} else {
				spec, err = atpkg.Parse(timeExprArg, deps.now())
				if err != nil {
					return fmt.Errorf("parse time expression: %w", err)
				}
			}

			// Extract and resolve sandbox ID (first extra arg for lifecycle commands).
			// Supports aliases, numbers from km list, and raw sandbox IDs.
			sandboxID := ""
			if cmdInfo.targetARNField == "ttl" && len(extraArgs) > 0 {
				resolved, resolveErr := ResolveSandboxID(ctx, cfg, extraArgs[0])
				if resolveErr != nil {
					return fmt.Errorf("resolve sandbox %q: %w", extraArgs[0], resolveErr)
				}
				sandboxID = resolved
			}

			// SCHED-GUARDRAIL: Recurring create schedules check sandbox count at CLI time.
			// (The Lambda also enforces at fire-time — this is advisory/early-warning.)
			if cmdArg == "create" && spec.IsRecurring && cfg.MaxSandboxes > 0 {
				count, countErr := deps.countActiveSandboxes(ctx)
				if countErr != nil {
					log.Warn().Err(countErr).Msg("failed to check sandbox count -- proceeding with schedule")
				} else if count >= cfg.MaxSandboxes {
					fmt.Fprintf(cmd.ErrOrStderr(),
						"Warning: active sandbox count (%d) is at or above limit (%d). "+
							"Recurring create schedule not created. "+
							"Reduce active sandboxes or increase max_sandboxes.\n",
						count, cfg.MaxSandboxes)
					return nil
				}
			}

			// Generate schedule name
			scheduleName := nameFlag
			if scheduleName == "" {
				scheduleName = atpkg.GenerateScheduleName(cmdArg, sandboxID, timeExprArg)
			}

			// Build Target.Input JSON
			targetInput, err := buildTargetInput(cmdArg, cmdInfo, sandboxID, cfg.ArtifactsBucket, extraArgs)
			if err != nil {
				return fmt.Errorf("build target input: %w", err)
			}

			// For "create" commands: upload profile YAML to S3 so the Lambda can find it at fire-time.
			// If the profile file doesn't exist locally, include the path in the detail and
			// let the Lambda resolve it from its toolchain (which has profiles/ baked in).
			if cmdArg == "create" && cfg.ArtifactsBucket != "" {
				profilePath, _, _, _ := parseCreateArgs(extraArgs)
				if profilePath != "" {
					if profileData, readErr := os.ReadFile(profilePath); readErr == nil {
						s3Key := "scheduled/" + profilePath
						awsCfg2, _ := awspkg.LoadAWSConfig(ctx, resolveAWSProfile(cfg))
						s3Client := s3.NewFromConfig(awsCfg2)
						if _, putErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
							Bucket: aws.String(cfg.ArtifactsBucket),
							Key:    aws.String(s3Key),
							Body:   strings.NewReader(string(profileData)),
						}); putErr != nil {
							fmt.Fprintf(cmd.ErrOrStderr(), "  [warn] profile upload failed: %v (Lambda will use builtin)\n", putErr)
						} else {
							fmt.Fprintf(cmd.OutOrStdout(), "  Profile uploaded to s3://%s/%s\n", cfg.ArtifactsBucket, s3Key)
						}
					}
				}
			}

			// Determine ActionAfterCompletion
			actionAfterCompletion := schedulertypes.ActionAfterCompletionDelete
			if spec.IsRecurring {
				actionAfterCompletion = schedulertypes.ActionAfterCompletionNone
			}

			// Build CreateScheduleInput
			groupName := groupFlag
			schedInput := &scheduler.CreateScheduleInput{
				Name:                       aws.String(scheduleName),
				GroupName:                  aws.String(groupName),
				ScheduleExpression:         aws.String(spec.Expression),
				ScheduleExpressionTimezone: aws.String("UTC"),
				Target: &schedulertypes.Target{
					Arn:     aws.String(targetARN),
					RoleArn: aws.String(cfg.SchedulerRoleARN),
					Input:   aws.String(targetInput),
				},
				ActionAfterCompletion: actionAfterCompletion,
				FlexibleTimeWindow: &schedulertypes.FlexibleTimeWindow{
					Mode: schedulertypes.FlexibleTimeWindowModeOff,
				},
			}

			// Create EventBridge schedule
			if err := awspkg.CreateAtSchedule(ctx, deps.scheduler, schedInput); err != nil {
				return fmt.Errorf("create EventBridge schedule: %w", err)
			}

			// Store record in DynamoDB
			rec := awspkg.ScheduleRecord{
				ScheduleName: scheduleName,
				Command:      cmdArg,
				SandboxID:    sandboxID,
				TimeExpr:     timeExprArg,
				CronExpr:     spec.Expression,
				IsRecurring:  spec.IsRecurring,
				Status:       "active",
				CreatedAt:    deps.now(),
			}
			if err := awspkg.PutSchedule(ctx, deps.dynamo, cfg.SchedulesTableName, rec); err != nil {
				return fmt.Errorf("store schedule record: %w", err)
			}

			// Print confirmation
			fmt.Fprintf(cmd.OutOrStdout(), "Scheduled: %s -- %s %s at %s\n",
				scheduleName, cmdArg, strings.Join(extraArgs, " "), spec.Expression)
			return nil
		},
	}

	cmd.Flags().StringVar(&cronFlag, "cron", "", "Raw EventBridge cron() expression (bypasses NL parsing)")
	cmd.Flags().StringVar(&nameFlag, "name", "", "Override auto-generated schedule name")
	cmd.Flags().StringVar(&groupFlag, "group", "km-at", "EventBridge Scheduler group name")

	// Stop parsing flags after the first positional arg so that
	// command-specific flags (--alias, --on-demand, --docker) are passed through
	// as positional args rather than being consumed by Cobra.
	cmd.Flags().SetInterspersed(false)

	return cmd
}

// parseCreateArgs extracts create-specific flags from the extra args after the command name.
// Returns profile path, alias, onDemand flag, and an error for unsupported flags.
func parseCreateArgs(extraArgs []string) (profilePath, alias string, onDemand bool, err error) {
	for i := 0; i < len(extraArgs); i++ {
		switch extraArgs[i] {
		case "--alias":
			if i+1 >= len(extraArgs) {
				return "", "", false, fmt.Errorf("--alias requires a value")
			}
			alias = extraArgs[i+1]
			i++ // skip value
		case "--on-demand":
			onDemand = true
		case "--docker":
			return "", "", false, fmt.Errorf("--docker is not supported for scheduled creates (requires local execution)")
		default:
			if strings.HasPrefix(extraArgs[i], "--") {
				return "", "", false, fmt.Errorf("unknown flag: %s", extraArgs[i])
			}
			if profilePath == "" {
				profilePath = extraArgs[i]
			}
		}
	}
	return profilePath, alias, onDemand, nil
}

// buildTargetInput builds the JSON payload sent to the Lambda as Target.Input.
// For "create": sends SandboxCreate detail with profile, alias, on_demand parsed from extraArgs.
// For lifecycle commands: sends SandboxIdle event JSON.
func buildTargetInput(cmdArg string, cmdInfo schedulableCommand, sandboxID, artifactsBucket string, extraArgs []string) (string, error) {
	if cmdInfo.targetARNField == "create" {
		profilePath, alias, onDemand, parseErr := parseCreateArgs(extraArgs)
		if parseErr != nil {
			return "", parseErr
		}
		// SandboxCreate event shape: Lambda generates fresh sandbox ID at fire-time.
		detail := map[string]interface{}{
			"sandbox_id":      "",
			"artifact_bucket": artifactsBucket,
			"artifact_prefix": "scheduled/",
			"on_demand":       onDemand,
			"created_by":      "schedule",
		}
		if profilePath != "" {
			detail["profile_path"] = profilePath
		}
		if alias != "" {
			detail["alias"] = alias
		}
		b, err := json.Marshal(detail)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}

	// Lifecycle commands: SandboxIdle event shape.
	// First extra arg is sandbox ID (or alias to resolve).
	detail := map[string]interface{}{
		"sandbox_id": sandboxID,
		"event_type": cmdInfo.eventType,
	}
	// For "extend", include duration from extraArgs (second extra arg if present)
	if cmdArg == "extend" && len(extraArgs) >= 2 {
		detail["duration"] = extraArgs[1]
	}
	// For "budget-add", parse --compute and --ai from extraArgs
	if cmdArg == "budget-add" {
		for i := 1; i < len(extraArgs); i++ {
			switch extraArgs[i] {
			case "--compute":
				if i+1 < len(extraArgs) {
					detail["budget_compute"], _ = strconv.ParseFloat(extraArgs[i+1], 64)
					i++
				}
			case "--ai":
				if i+1 < len(extraArgs) {
					detail["budget_ai"], _ = strconv.ParseFloat(extraArgs[i+1], 64)
					i++
				}
			}
		}
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// NewAtListCmd creates the "km at list" subcommand.
func NewAtListCmd(cfg *config.Config) *cobra.Command {
	return NewAtListCmdWithDeps(cfg, nil)
}

// NewAtListCmdWithDeps creates a testable "km at list" subcommand with injected DynamoDB.
func NewAtListCmdWithDeps(cfg *config.Config, dynamo awspkg.SandboxMetadataAPI) *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List all scheduled sandbox operations",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			dynamoClient := dynamo
			if dynamoClient == nil {
				// Real AWS client
				awsCfg, err := awspkg.LoadAWSConfig(ctx, resolveAWSProfile(cfg))
				if err != nil {
					return fmt.Errorf("load AWS config: %w", err)
				}
				dynamoClient = dynamodb.NewFromConfig(awsCfg)
			}

			records, err := awspkg.ListScheduleRecords(ctx, dynamoClient, cfg.SchedulesTableName)
			if err != nil {
				return fmt.Errorf("list schedule records: %w", err)
			}

			if len(records) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No scheduled operations.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tCOMMAND\tTARGET\tSCHEDULE\tSTATUS\tCREATED")
			for _, r := range records {
				target := r.SandboxID
				if target == "" {
					target = "(new)"
				}
				created := r.CreatedAt.UTC().Format("2006-01-02 15:04")
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					r.ScheduleName, r.Command, target, r.CronExpr, r.Status, created)
			}
			return w.Flush()
		},
	}
}

// NewAtCancelCmd creates the "km at cancel <name>" subcommand.
func NewAtCancelCmd(cfg *config.Config) *cobra.Command {
	return NewAtCancelCmdWithDeps(cfg, nil, nil)
}

// NewAtCancelCmdWithDeps creates a testable "km at cancel" subcommand with injected deps.
func NewAtCancelCmdWithDeps(cfg *config.Config, sched awspkg.SchedulerAPI, dynamo awspkg.SandboxMetadataAPI) *cobra.Command {
	var groupFlag string

	cmd := &cobra.Command{
		Use:          "cancel <schedule-name>",
		Short:        "Cancel a scheduled sandbox operation",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			scheduleName := args[0]

			schedClient := sched
			dynamoClient := dynamo

			if schedClient == nil || dynamoClient == nil {
				awsCfg, err := awspkg.LoadAWSConfig(ctx, resolveAWSProfile(cfg))
				if err != nil {
					return fmt.Errorf("load AWS config: %w", err)
				}
				if schedClient == nil {
					schedClient = scheduler.NewFromConfig(awsCfg)
				}
				if dynamoClient == nil {
					dynamoClient = dynamodb.NewFromConfig(awsCfg)
				}
			}

			// Delete from EventBridge (idempotent — no error if missing)
			if err := awspkg.DeleteAtSchedule(ctx, schedClient, scheduleName, groupFlag); err != nil {
				return fmt.Errorf("delete EventBridge schedule: %w", err)
			}

			// Delete from DynamoDB (idempotent — no error if missing)
			if err := awspkg.DeleteScheduleRecord(ctx, dynamoClient, cfg.SchedulesTableName, scheduleName); err != nil {
				return fmt.Errorf("delete schedule record: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Cancelled: %s\n", scheduleName)
			return nil
		},
	}

	cmd.Flags().StringVar(&groupFlag, "group", "km-at", "EventBridge Scheduler group name")
	return cmd
}

// buildAtDeps constructs atDeps with either injected or real AWS clients.
func buildAtDeps(ctx context.Context, cfg *config.Config, schedClient awspkg.SchedulerAPI, dynamoClient awspkg.SandboxMetadataAPI, counter func(ctx context.Context) (int, error)) atDeps {
	deps := atDeps{
		scheduler: schedClient,
		dynamo:    dynamoClient,
		cfg:       cfg,
		now:       time.Now,
	}

	if counter != nil {
		deps.countActiveSandboxes = counter
	} else {
		deps.countActiveSandboxes = func(ctx context.Context) (int, error) {
			if cfg.StateBucket == "" {
				return 0, fmt.Errorf("state bucket not configured")
			}
			awsCfg, err := awspkg.LoadAWSConfig(ctx, resolveAWSProfile(cfg))
			if err != nil {
				return 0, err
			}
			s3Client := s3.NewFromConfig(awsCfg)
			records, err := awspkg.ListAllSandboxesByS3(ctx, s3Client, cfg.StateBucket)
			if err != nil {
				return 0, err
			}
			count := 0
			for _, r := range records {
				if r.Status != "destroyed" && r.Status != "killed" {
					count++
				}
			}
			return count, nil
		}
	}

	if deps.scheduler == nil {
		// Lazy init deferred — will be initialized in RunE when needed.
		// Not done here since AWS config load requires ctx.
	}

	return deps
}

// resolveAWSProfile returns the configured AWS profile or the default.
func resolveAWSProfile(cfg *config.Config) string {
	if cfg.AWSProfile != "" {
		return cfg.AWSProfile
	}
	return "klanker-terraform"
}
