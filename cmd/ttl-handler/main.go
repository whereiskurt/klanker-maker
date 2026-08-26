// Package main implements the km TTL handler Lambda.
//
// When an EventBridge scheduler fires a TTL expiry event for a sandbox,
// this handler:
//  1. Validates the sandbox_id in the event payload.
//  2. Downloads the sandbox profile from S3.
//  3. Uploads sandbox artifacts to S3 (the primary gap closure for OBSV-04/OBSV-05).
//  4. Sends a "ttl-expired" lifecycle notification to the operator (if configured).
//  5. Deletes the TTL schedule (self-cleanup).
//  6. Destroys sandbox resources via AWS SDK (PROV-05/PROV-06).
//
// The teardown uses AWS SDK calls (not terragrunt subprocess) because the Lambda
// runtime (provided.al2023) does NOT include the terragrunt binary.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	lambdaruntime "github.com/aws/aws-lambda-go/lambda"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	dynamodbpkg "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	ec2pkg "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	iampkg "github.com/aws/aws-sdk-go-v2/service/iam"
	kmspkg "github.com/aws/aws-sdk-go-v2/service/kms"
	lambdapkg "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	schedulertypes "github.com/aws/aws-sdk-go-v2/service/scheduler/types"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	ssmpkg "github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/rs/zerolog/log"
	atpkg "github.com/whereiskurt/klanker-maker/pkg/at"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
	profilepkg "github.com/whereiskurt/klanker-maker/pkg/profile"
)

// TTLEvent is the EventBridge scheduler or EventBridge Events payload delivered to this Lambda.
type TTLEvent struct {
	SandboxID string `json:"sandbox_id"`
	// EventType distinguishes actions:
	//   "ttl" (default), "idle", "destroy" — trigger full terraform destroy
	//   "stop"    — stop EC2 instance without destroying infrastructure
	//   "resume"  — resume a stopped/paused EC2 instance
	//   "extend"  — extend TTL by Duration
	//   "budget-add" — add budget to a sandbox (compute and/or AI)
	// Empty defaults to "ttl" for backward compatibility.
	EventType string `json:"event_type,omitempty"`
	// Duration is used by "extend" events (e.g. "2h", "30m").
	Duration string `json:"duration,omitempty"`
	// BudgetCompute is the USD amount to add to compute budget (budget-add events).
	BudgetCompute float64 `json:"budget_compute,omitempty"`
	// BudgetAI is the USD amount to add to AI budget (budget-add events).
	BudgetAI float64 `json:"budget_ai,omitempty"`
	// Agent-run fields.
	Prompt    string `json:"prompt,omitempty"`
	NoBedrock bool   `json:"no_bedrock,omitempty"`
	AutoStart bool   `json:"auto_start,omitempty"`
	// Schedule fields for "schedule-create" events (relay from email Lambda).
	ScheduleTime   string `json:"schedule_time,omitempty"` // natural language time expression
	ArtifactBucket string `json:"artifact_bucket,omitempty"`
	ArtifactPrefix string `json:"artifact_prefix,omitempty"`
	OperatorEmail  string `json:"operator_email_event,omitempty"` // from email conversation
	OnDemand       bool   `json:"on_demand,omitempty"`
	Alias          string `json:"alias,omitempty"`
	ProfileName    string `json:"profile_name,omitempty"`
	// Phase 116 Stage B: check runner fields.
	// CheckName is the km check name (e.g. "wiz-intel") used as the cooldown key and
	// to build the check Lambda function name "{prefix}-check-{name}".
	CheckName string `json:"check_name,omitempty"`
	// OnAbsent controls cold-create behaviour when the alias is absent:
	//   "cold-create" (default/empty) — provision a new sandbox and enqueue the prompt.
	//   "skip"                        — drop the dispatch silently.
	OnAbsent string `json:"on_absent,omitempty"`
	// Reason is the human-readable reason string from the Stage A when_py predicate.
	Reason string `json:"reason,omitempty"`
	// CooldownSeconds is the nonce TTL for per-check cooldown enforcement.
	// 0 = no cooldown. Enforced in Stage B via the nonces table key "check-trigger:{name}".
	CooldownSeconds int `json:"cooldown_seconds,omitempty"`
}

// S3GetAPI is the narrow S3 interface needed to download the sandbox profile.
type S3GetAPI interface {
	GetObject(ctx context.Context, input *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// S3GetPutAPI combines read, write, and delete S3 operations needed by the handler.
type S3GetPutAPI interface {
	S3GetAPI
	awspkg.S3PutAPI
	awspkg.S3DeleteAPI
}

// SESV2API re-exports the narrow SES interface from pkg/aws for use in this package.
type SESV2API = awspkg.SESV2API

// SchedulerAPI re-exports the narrow Scheduler interface from pkg/aws.
type SchedulerAPI = awspkg.SchedulerAPI

// TTLHandler holds injected dependencies for testability.
type TTLHandler struct {
	S3Client         S3GetPutAPI
	DynamoClient     awspkg.SandboxMetadataAPI
	SandboxTableName string
	SESClient        SESV2API
	Scheduler        SchedulerAPI
	Bucket           string
	StateBucket      string // S3 bucket holding terraform state
	StatePrefix      string // state key prefix (e.g. "tf-km")
	Region           string // AWS region (e.g. "us-east-1")
	RegionLabel      string // short region label (e.g. "use1")
	CWClient         awspkg.CWLogsAPI
	OperatorEmail    string
	Domain           string
	SSMClient        *ssmpkg.Client
	// IdentityTable is the DynamoDB table name for the km-identities table
	// (default "<resource_prefix>-identities"). Used by cleanupSandboxIdentity to
	// delete the sandbox row after terraform destroy. Sourced from KM_IDENTITIES_TABLE.
	IdentityTable string
	// BudgetClient is a DynamoDB client for the km-budgets table.
	// Used by handleStop/handleResume/handleAgentRun to record pause/resume intervals.
	// If nil, budget hooks are skipped (backward compatible with existing tests).
	BudgetClient awspkg.BudgetAPI
	// BudgetTable is the DynamoDB table name for budget tracking (default "km-budgets").
	BudgetTable string
	// TeardownFunc destroys the sandbox resources after TTL expiry or idle detection.
	// If nil, the SDK-only fallback (SDKTeardownFunc) runs instead.
	TeardownFunc func(ctx context.Context, sandboxID string) error
	// SDKTeardownFunc is the fallback destroy path used when TeardownFunc is nil
	// (no terraform binary bundled). It defaults to sdkOnlyTeardown in main(); it
	// is an injectable seam so unit tests can exercise the nil-TeardownFunc branch
	// without making real AWS/IMDS calls. If nil, defaults to sdkOnlyTeardown.
	SDKTeardownFunc func(ctx context.Context, h *TTLHandler, sandboxID string) error
	// LaunchAccounts is the parsed KM_LAUNCH_ACCOUNTS map — the Lambda-side mirror
	// of km-config.yaml's launch_accounts block, keyed by link name (Phase 126,
	// REQ-126-TEARDOWN). Parsed once at cold start in main(); nil/empty means no
	// links are configured, the dormant state — every teardown behaves exactly as
	// it did before this phase.
	LaunchAccounts map[string]launchAccountLink
}

// getEmailDomain returns the sandbox email domain from the KM_EMAIL_DOMAIN env var.
// Falls back to "sandboxes.klankermaker.ai" for un-migrated installs until plan 04 wires the env block.
func getEmailDomain() string {
	if v := os.Getenv("KM_EMAIL_DOMAIN"); v != "" {
		return v
	}
	return "sandboxes.klankermaker.ai"
}

// sandboxTableName / budgetTableName / schedulesTableName all derive their
// fallback from KM_RESOURCE_PREFIX so a non-default install (e.g.
// resource_prefix=kph) gets prefix-correct fallbacks (kph-sandboxes etc.)
// instead of silently using literal "km-*" defaults that don't exist.
// Defense in depth — the explicit KM_SANDBOX_TABLE_NAME / KM_BUDGET_TABLE
// / KM_SCHEDULES_TABLE env vars are normally set by the Lambda terraform,
// but this guards against future regressions in the env block.
func sandboxTableName() string {
	if v := os.Getenv("KM_SANDBOX_TABLE_NAME"); v != "" {
		return v
	}
	return resourcePrefix() + "-sandboxes"
}

func budgetTableName() string {
	if v := os.Getenv("KM_BUDGET_TABLE"); v != "" {
		return v
	}
	return resourcePrefix() + "-budgets"
}

func schedulesTableName() string {
	if v := os.Getenv("KM_SCHEDULES_TABLE"); v != "" {
		return v
	}
	return resourcePrefix() + "-schedules"
}

// identitiesTable returns the DynamoDB identities table name from KM_IDENTITIES_TABLE,
// falling back to "<resource_prefix>-identities" if unset.
func identitiesTable() string {
	if v := os.Getenv("KM_IDENTITIES_TABLE"); v != "" {
		return v
	}
	return resourcePrefix() + "-identities"
}

// ttlHandlerName returns the TTL handler Lambda function name from the
// KM_TTL_HANDLER_NAME env var, falling back to "<resource_prefix>-ttl-handler".
//
// The fallback was hardcoded "km-ttl-handler", which is wrong on any install
// whose resource_prefix is not "km". Not currently biting — the deployed function
// sets KM_TTL_HANDLER_NAME explicitly — but it is a one-forgotten-tfvar bug in the
// self-referencing GetFunction/scheduler calls, so it now matches the sibling
// helpers above.
func ttlHandlerName() string {
	if v := os.Getenv("KM_TTL_HANDLER_NAME"); v != "" {
		return v
	}
	return resourcePrefix() + "-ttl-handler"
}

// ttlSchedulerRole returns the TTL scheduler IAM role name from the
// KM_TTL_SCHEDULER_ROLE env var, falling back to "<resource_prefix>-ttl-scheduler".
// Same hardcoded-prefix bug as ttlHandlerName above.
func ttlSchedulerRole() string {
	if v := os.Getenv("KM_TTL_SCHEDULER_ROLE"); v != "" {
		return v
	}
	return resourcePrefix() + "-ttl-scheduler"
}

// atGroupName returns the EventBridge schedule group name from the KM_AT_GROUP_NAME env var.
func atGroupName() string {
	if v := os.Getenv("KM_AT_GROUP_NAME"); v != "" {
		return v
	}
	return "km-at"
}

// resourcePrefix returns the resource prefix from the KM_RESOURCE_PREFIX env var.
func resourcePrefix() string {
	if v := os.Getenv("KM_RESOURCE_PREFIX"); v != "" {
		return v
	}
	return "km"
}

// parseLaunchAccountsEnv parses KM_LAUNCH_ACCOUNTS — a JSON object keyed by
// link name — once at cold start (Phase 126, REQ-126-TEARDOWN). Absent or
// invalid JSON returns nil, the dormant state: every teardown behaves exactly
// as it did before this phase (buildDestroyTerraformInputs never looks the map
// up when launchAccountName is empty). Mirrors the KM_GITHUB_EVENTS /
// KM_SLACK_PEER_BRIDGES JSON-blob-in-a-Lambda-env-var precedent.
func parseLaunchAccountsEnv() map[string]launchAccountLink {
	raw := os.Getenv("KM_LAUNCH_ACCOUNTS")
	if raw == "" {
		return nil
	}
	var links map[string]launchAccountLink
	if err := json.Unmarshal([]byte(raw), &links); err != nil {
		log.Warn().Err(err).Msg("failed to parse KM_LAUNCH_ACCOUNTS; cross-account teardown dormant")
		return nil
	}
	return links
}

// HandleTTLEvent is the Lambda handler method. It is called by lambdaruntime.Start in main().
func (h *TTLHandler) HandleTTLEvent(ctx context.Context, event TTLEvent) error {
	// Phase 116 Stage B: check-dispatch and check-run do NOT carry a sandbox_id — they
	// target an alias (check-dispatch) or a check name (check-run). Route these BEFORE
	// the sandbox_id guard so EventBridge CheckDispatch events are not rejected.
	switch event.EventType {
	case "check-dispatch":
		return h.handleCheckDispatch(ctx, event)
	case "check-run":
		return h.handleCheckRun(ctx, event)
	}

	if event.SandboxID == "" {
		return fmt.Errorf("ttl-handler: sandbox_id is required in event payload")
	}

	// Route by event type
	switch event.EventType {
	case "stop":
		return h.handleStop(ctx, event)
	case "resume":
		return h.handleResume(ctx, event)
	case "extend":
		return h.handleExtend(ctx, event)
	case "budget-add":
		return h.handleBudgetAdd(ctx, event)
	case "agent-run":
		return h.handleAgentRun(ctx, event)
	case "schedule-create":
		return h.handleScheduleCreate(ctx, event)
	default:
		// "ttl", "idle", "destroy", "" — automatic teardown triggers.
		//
		// Explicit "destroy" (operator km destroy) overrides teardownPolicy and the
		// lock; the CLI enforces the safety lock client-side before it ever reaches
		// here. For AUTOMATIC triggers (ttl/idle/empty) we honor both safety
		// mechanisms and the full teardownPolicy set, mirroring the canonical
		// pkg/lifecycle.ExecuteTeardown semantics (destroy / stop / retain).
		if event.EventType != "destroy" {
			// Safety lock (km lock): never auto-stop or auto-destroy a locked
			// sandbox — the operator must km unlock first. Previously absent, so
			// idle/TTL reaped locked sandboxes.
			if h.isSandboxLocked(ctx, event.SandboxID) {
				log.Warn().
					Str("sandbox_id", event.SandboxID).
					Str("event_type", event.EventType).
					Msg("sandbox is locked (km lock) — skipping automatic teardown")
				return nil
			}
			switch policy := h.lookupTeardownPolicy(ctx, event.SandboxID); policy {
			case "stop":
				log.Info().
					Str("sandbox_id", event.SandboxID).
					Str("event_type", event.EventType).
					Str("teardown_policy", "stop").
					Msg("teardownPolicy is 'stop' — stopping instead of destroying")
				return h.handleStop(ctx, event)
			case "retain":
				// retain means: leave everything running; operator tears down
				// manually. Previously this fell through to destroy — the defect
				// that reaped retain sandboxes on the idle timer.
				log.Info().
					Str("sandbox_id", event.SandboxID).
					Str("event_type", event.EventType).
					Str("teardown_policy", "retain").
					Msg("teardownPolicy is 'retain' — preserving sandbox; operator must destroy manually")
				return nil
			}
			// "destroy" (and any unknown value) falls through to handleDestroy.
		}
		return h.handleDestroy(ctx, event)
	}
}

// handleStop stops the EC2 instance without destroying infrastructure.
// Updates DynamoDB status to "stopped" or "paused" (hibernated) so km list
// reflects the actual state. Deletes the TTL schedule since the sandbox is
// now stopped and shouldn't be destroyed on TTL expiry.
func (h *TTLHandler) handleStop(ctx context.Context, event TTLEvent) error {
	log.Info().Str("sandbox_id", event.SandboxID).Msg("stop event received")

	// Guard: for TTL expiry events (empty event_type), check if the sandbox was
	// recently resumed (future TTL) and skip the stale event. Idle events are
	// always legitimate — the sidecar detected real inactivity.
	if event.EventType == "" && h.DynamoClient != nil {
		meta, metaErr := awspkg.ReadSandboxMetadataDynamo(ctx, h.DynamoClient, h.SandboxTableName, event.SandboxID)
		if metaErr == nil && meta != nil && meta.ExpiresAt != nil && time.Until(*meta.ExpiresAt) > 5*time.Minute {
			log.Info().
				Str("sandbox_id", event.SandboxID).
				Time("expires_at", *meta.ExpiresAt).
				Msg("sandbox has future TTL — skipping stale stop event (likely post-resume)")
			return nil
		}
	}

	hibernate := h.lookupHibernation(ctx, event.SandboxID)

	awsCfg, err := awspkg.LoadAWSConfig(ctx, "")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	ec2Client := ec2pkg.NewFromConfig(awsCfg)

	// Find instance by tag
	descOut, err := ec2Client.DescribeInstances(ctx, &ec2pkg.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("tag:km:sandbox-id"), Values: []string{event.SandboxID}},
			{Name: awssdk.String("instance-state-name"), Values: []string{"running"}},
		},
	})
	if err != nil {
		return fmt.Errorf("describe instances: %w", err)
	}

	var stoppedCount int
	for _, res := range descOut.Reservations {
		for _, inst := range res.Instances {
			instanceID := awssdk.ToString(inst.InstanceId)

			// Spot instances cannot be stopped or hibernated
			if inst.InstanceLifecycle == ec2types.InstanceLifecycleTypeSpot {
				log.Warn().Str("instance_id", instanceID).Msg("skipping spot instance — cannot stop")
				continue
			}

			if hibernate {
				log.Info().Str("instance_id", instanceID).Msg("hibernating instance")
				_, err := ec2Client.StopInstances(ctx, &ec2pkg.StopInstancesInput{
					InstanceIds: []string{instanceID},
					Hibernate:   awssdk.Bool(true),
				})
				if err != nil && strings.Contains(err.Error(), "UnsupportedHibernationConfiguration") {
					log.Warn().Str("instance_id", instanceID).Msg("hibernate not available, falling back to stop")
					_, err = ec2Client.StopInstances(ctx, &ec2pkg.StopInstancesInput{
						InstanceIds: []string{instanceID},
					})
				}
				if err != nil {
					return fmt.Errorf("hibernate instance %s: %w", instanceID, err)
				}
			} else {
				log.Info().Str("instance_id", instanceID).Msg("stopping instance")
				_, err := ec2Client.StopInstances(ctx, &ec2pkg.StopInstancesInput{
					InstanceIds: []string{instanceID},
				})
				if err != nil {
					return fmt.Errorf("stop instance %s: %w", instanceID, err)
				}
			}
			stoppedCount++
		}
	}

	if stoppedCount == 0 {
		log.Warn().Str("sandbox_id", event.SandboxID).Msg("no running instances found to stop")
		return nil
	}

	// Update DynamoDB status so km list reflects actual state.
	if h.DynamoClient != nil {
		status := "stopped"
		if hibernate {
			status = "paused"
		}
		// Record pause start in budget table so the stopped interval is excluded
		// from compute cost. Fires for both "stopped" and "paused" — AWS charges
		// $0 compute in both cases, so the budget enforcer must not tick wall-clock
		// against either. EC2 substrate only (handleStop is EC2-only by
		// construction). Non-fatal.
		if h.BudgetClient != nil && h.BudgetTable != "" {
			if err := awspkg.RecordPauseStart(ctx, h.BudgetClient, h.BudgetTable, event.SandboxID, time.Now().UTC()); err != nil {
				log.Warn().Err(err).Str("sandbox_id", event.SandboxID).Msg("failed to record pause start in budget table (non-fatal)")
			}
		}
		// Clear ttl_expiry so DynamoDB's native TTL doesn't auto-delete the record.
		// The sandbox should remain visible in km list until explicitly destroyed.
		if statusErr := awspkg.UpdateSandboxStatusAndClearTTL(ctx, h.DynamoClient, h.SandboxTableName, event.SandboxID, status); statusErr != nil {
			log.Warn().Err(statusErr).Str("sandbox_id", event.SandboxID).Msg("failed to update DynamoDB status (non-fatal)")
		} else {
			log.Info().Str("sandbox_id", event.SandboxID).Str("status", status).Msg("DynamoDB status updated, ttl_expiry cleared")
		}
	}

	// Delete TTL schedule — stopped sandbox shouldn't be destroyed on TTL expiry.
	if h.Scheduler != nil {
		if schedErr := awspkg.DeleteTTLSchedule(ctx, h.Scheduler, event.SandboxID, resourcePrefix()); schedErr != nil {
			log.Warn().Err(schedErr).Str("sandbox_id", event.SandboxID).Msg("failed to delete TTL schedule (non-fatal)")
		}
	}

	log.Info().Str("sandbox_id", event.SandboxID).Int("instances_stopped", stoppedCount).Msg("sandbox stopped")
	return nil
}

// handleResume starts a stopped EC2 instance, updates DynamoDB status to
// "running", and recreates the TTL schedule based on the profile's TTL duration
// (counting from now). This ensures resumed sandboxes don't run indefinitely.
// refreshGitHubTokenOnResume re-mints a waking sandbox's GitHub installation
// token by invoking its per-sandbox refresher Lambda directly. Best-effort: the
// outcome is logged and never propagated, since the instance has already been
// started by the time this runs.
//
// A sandbox with no sourceAccess.github has no refresher schedule; that is an
// info line, not a warning.
func refreshGitHubTokenOnResume(ctx context.Context, awsCfg awssdk.Config, sandboxID string) {
	rCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := awspkg.ForceGitHubTokenRefresh(rCtx,
		scheduler.NewFromConfig(awsCfg),
		lambdapkg.NewFromConfig(awsCfg),
		resourcePrefix(), sandboxID)

	switch {
	case err == nil:
		log.Info().Str("sandbox_id", sandboxID).Msg("github token refreshed on resume")
	case errors.Is(err, awspkg.ErrNoGitHubRefresher):
		log.Info().Str("sandbox_id", sandboxID).Msg("no github-token refresher for sandbox; skipping token refresh")
	default:
		log.Warn().Err(err).Str("sandbox_id", sandboxID).
			Msg("github token refresh failed on resume (non-fatal) — git operations in this sandbox may 401")
	}
}

func (h *TTLHandler) handleResume(ctx context.Context, event TTLEvent) error {
	log.Info().Str("sandbox_id", event.SandboxID).Msg("resume event received")

	awsCfg, err := awspkg.LoadAWSConfig(ctx, "")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	ec2Client := ec2pkg.NewFromConfig(awsCfg)

	descOut, err := ec2Client.DescribeInstances(ctx, &ec2pkg.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("tag:km:sandbox-id"), Values: []string{event.SandboxID}},
			{Name: awssdk.String("instance-state-name"), Values: []string{"stopped"}},
		},
	})
	if err != nil {
		return fmt.Errorf("describe instances: %w", err)
	}

	var resumedCount int
	for _, res := range descOut.Reservations {
		for _, inst := range res.Instances {
			instanceID := awssdk.ToString(inst.InstanceId)
			log.Info().Str("instance_id", instanceID).Msg("starting instance")
			_, err := ec2Client.StartInstances(ctx, &ec2pkg.StartInstancesInput{
				InstanceIds: []string{instanceID},
			})
			if err != nil {
				return fmt.Errorf("start instance %s: %w", instanceID, err)
			}
			resumedCount++
		}
	}

	if resumedCount == 0 {
		log.Warn().Str("sandbox_id", event.SandboxID).Msg("no stopped instances found to resume")
		return nil
	}

	// Close the open pause interval in the budget table so paused time stops accruing.
	// Non-fatal: a DynamoDB error only logs a warning and lifecycle continues.
	if h.BudgetClient != nil && h.BudgetTable != "" {
		if err := awspkg.RecordResumeClose(ctx, h.BudgetClient, h.BudgetTable, event.SandboxID, time.Now().UTC()); err != nil {
			log.Warn().Err(err).Str("sandbox_id", event.SandboxID).Msg("failed to record resume close in budget table (non-fatal)")
		}
	}

	// Update DynamoDB status to running.
	if h.DynamoClient != nil {
		if statusErr := awspkg.UpdateSandboxStatusDynamo(ctx, h.DynamoClient, h.SandboxTableName, event.SandboxID, "running"); statusErr != nil {
			log.Warn().Err(statusErr).Str("sandbox_id", event.SandboxID).Msg("failed to update DynamoDB status (non-fatal)")
		}
	}

	// Wake-up re-credential (mirrors runResume in internal/app/cmd/resume.go):
	// force a GitHub installation-token re-mint rather than waiting for the
	// refresher's next 45-minute tick. Non-fatal — the instance is already
	// starting — but logged, so a refresher that has been failing silently on
	// every tick shows up here instead of as a git 401 inside the sandbox.
	refreshGitHubTokenOnResume(ctx, awsCfg, event.SandboxID)

	// Recreate TTL schedule from the profile's TTL duration, counting from now.
	// This ensures resumed sandboxes don't run indefinitely without a TTL.
	profileBytes, profileErr := downloadProfileFromS3(ctx, h.S3Client, h.Bucket, event.SandboxID)
	if profileErr == nil {
		p, parseErr := profilepkg.Parse(profileBytes)
		if parseErr == nil && p != nil && p.Spec.Lifecycle.TTL != "" && p.Spec.Lifecycle.TTL != "0" {
			ttlDuration, durErr := time.ParseDuration(p.Spec.Lifecycle.TTL)
			if durErr == nil {
				newExpiry := time.Now().Add(ttlDuration)

				// Discover Lambda ARN and scheduler role.
				lambdaClient := lambdapkg.NewFromConfig(awsCfg)
				fnOut, fnErr := lambdaClient.GetFunction(ctx, &lambdapkg.GetFunctionInput{
					FunctionName: awssdk.String(ttlHandlerName()),
				})
				iamClient := iampkg.NewFromConfig(awsCfg)
				roleOut, roleErr := iamClient.GetRole(ctx, &iampkg.GetRoleInput{
					RoleName: awssdk.String(ttlSchedulerRole()),
				})

				if fnErr == nil && roleErr == nil {
					schedulerClient := scheduler.NewFromConfig(awsCfg)
					schedInput := compiler.BuildTTLScheduleInput(event.SandboxID, newExpiry,
						awssdk.ToString(fnOut.Configuration.FunctionArn),
						awssdk.ToString(roleOut.Role.Arn),
						resourcePrefix())
					if schedErr := awspkg.CreateTTLSchedule(ctx, schedulerClient, schedInput); schedErr != nil {
						log.Warn().Err(schedErr).Str("sandbox_id", event.SandboxID).Msg("failed to recreate TTL schedule (non-fatal)")
					} else {
						log.Info().Str("sandbox_id", event.SandboxID).Time("ttl_expiry", newExpiry).Msg("TTL schedule recreated")
					}

					// Update TTL expiry in DynamoDB metadata.
					if h.DynamoClient != nil {
						meta, readErr := awspkg.ReadSandboxMetadataDynamo(ctx, h.DynamoClient, h.SandboxTableName, event.SandboxID)
						if readErr == nil {
							meta.TTLExpiry = &newExpiry
							awspkg.WriteSandboxMetadataDynamo(ctx, h.DynamoClient, h.SandboxTableName, meta)
						}
					}
				} else {
					if fnErr != nil {
						log.Warn().Err(fnErr).Msg("could not discover Lambda ARN for TTL schedule")
					}
					if roleErr != nil {
						log.Warn().Err(roleErr).Msg("could not discover scheduler role for TTL schedule")
					}
				}
			}
		}
	} else {
		log.Warn().Err(profileErr).Str("sandbox_id", event.SandboxID).Msg("could not load profile for TTL schedule recreation")
	}

	log.Info().Str("sandbox_id", event.SandboxID).Int("instances_resumed", resumedCount).Msg("sandbox resumed")
	return nil
}

// handleBudgetAdd increases budget limits for a sandbox via DynamoDB update.
func (h *TTLHandler) handleBudgetAdd(ctx context.Context, event TTLEvent) error {
	log.Info().Str("sandbox_id", event.SandboxID).
		Float64("compute", event.BudgetCompute).Float64("ai", event.BudgetAI).
		Msg("budget-add event received")

	if event.BudgetCompute == 0 && event.BudgetAI == 0 {
		return fmt.Errorf("budget-add: at least one of budget_compute or budget_ai must be > 0")
	}

	awsCfg, err := awspkg.LoadAWSConfig(ctx, "")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	dynamoClient := dynamodbpkg.NewFromConfig(awsCfg)
	budgetTbl := budgetTableName()

	// Atomic increment of budget limits via UpdateItem ADD expression
	update := &dynamodbpkg.UpdateItemInput{
		TableName: awssdk.String(budgetTbl),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: event.SandboxID},
			"sk":         &dynamodbtypes.AttributeValueMemberS{Value: "budget"},
		},
		UpdateExpression: awssdk.String("ADD compute_limit :c, ai_limit :a"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":c": &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%.2f", event.BudgetCompute)},
			":a": &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%.2f", event.BudgetAI)},
		},
	}
	if _, err := dynamoClient.UpdateItem(ctx, update); err != nil {
		return fmt.Errorf("update budget for %s: %w", event.SandboxID, err)
	}

	log.Info().Str("sandbox_id", event.SandboxID).
		Float64("compute_added", event.BudgetCompute).Float64("ai_added", event.BudgetAI).
		Msg("budget increased")
	return nil
}

// handleAgentRun dispatches an agent prompt to a sandbox via SSM SendCommand.
// Optionally resumes the sandbox first if --auto-start was specified.
func (h *TTLHandler) handleAgentRun(ctx context.Context, event TTLEvent) error {
	log.Info().Str("sandbox_id", event.SandboxID).Bool("auto_start", event.AutoStart).
		Msg("agent-run event received")

	if event.Prompt == "" {
		return fmt.Errorf("agent-run: prompt is required")
	}

	awsCfg, err := awspkg.LoadAWSConfig(ctx, "")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	ec2Client := ec2pkg.NewFromConfig(awsCfg)

	// Find instance by sandbox tag
	descOut, err := ec2Client.DescribeInstances(ctx, &ec2pkg.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("tag:km:sandbox-id"), Values: []string{event.SandboxID}},
		},
	})
	if err != nil {
		return fmt.Errorf("describe instances: %w", err)
	}

	var instanceID string
	var instanceState string
	for _, res := range descOut.Reservations {
		for _, inst := range res.Instances {
			state := string(inst.State.Name)
			if state == "terminated" || state == "shutting-down" {
				continue
			}
			instanceID = awssdk.ToString(inst.InstanceId)
			instanceState = state
		}
	}
	if instanceID == "" {
		return fmt.Errorf("no instance found for sandbox %s", event.SandboxID)
	}

	// If instance is stopped/hibernated and auto-start requested, resume it
	if instanceState != "running" {
		if !event.AutoStart {
			return fmt.Errorf("sandbox %s is %s — use --auto-start to resume automatically", event.SandboxID, instanceState)
		}
		log.Info().Str("instance", instanceID).Str("state", instanceState).Msg("resuming instance for agent-run")
		if _, err := ec2Client.StartInstances(ctx, &ec2pkg.StartInstancesInput{
			InstanceIds: []string{instanceID},
		}); err != nil {
			return fmt.Errorf("start instance %s: %w", instanceID, err)
		}
		// Wait for running state
		for i := 0; i < 36; i++ {
			time.Sleep(5 * time.Second)
			desc, err := ec2Client.DescribeInstances(ctx, &ec2pkg.DescribeInstancesInput{
				InstanceIds: []string{instanceID},
			})
			if err == nil {
				for _, res := range desc.Reservations {
					for _, inst := range res.Instances {
						if string(inst.State.Name) == "running" {
							instanceState = "running"
						}
					}
				}
			}
			if instanceState == "running" {
				break
			}
		}
		if instanceState != "running" {
			return fmt.Errorf("instance %s did not reach running state", instanceID)
		}
		// Close the open pause interval in the budget table so paused time stops accruing.
		// Non-fatal: a DynamoDB error only logs a warning and lifecycle continues.
		if h.BudgetClient != nil && h.BudgetTable != "" {
			if err := awspkg.RecordResumeClose(ctx, h.BudgetClient, h.BudgetTable, event.SandboxID, time.Now().UTC()); err != nil {
				log.Warn().Err(err).Str("sandbox_id", event.SandboxID).Msg("failed to record resume close in budget table (non-fatal)")
			}
		}
		// Update DynamoDB status back to running
		if h.DynamoClient != nil {
			_ = awspkg.UpdateSandboxStatusDynamo(ctx, h.DynamoClient, h.SandboxTableName, event.SandboxID, "running")
		}
		// Wait for SSM agent to register
		time.Sleep(15 * time.Second)
	}

	// Build agent shell commands (same pattern as BuildAgentShellCommands in agent.go)
	ssmClient := h.SSMClient
	if ssmClient == nil {
		ssmClient = ssmpkg.NewFromConfig(awsCfg)
	}

	b64Prompt := base64.StdEncoding.EncodeToString([]byte(event.Prompt))
	runID := time.Now().UTC().Format("20060102T150405Z")
	script := buildAgentRunScript(b64Prompt, h.Bucket, runID, event.NoBedrock)

	sessionName := fmt.Sprintf("km-agent-%s", runID)
	cmds := []string{
		fmt.Sprintf("cat > /tmp/km-agent-run.sh << 'KMEOF'\n%s\nKMEOF", script),
		"chmod +x /tmp/km-agent-run.sh",
		fmt.Sprintf("sudo -u sandbox bash -c \"tmux new-session -d -s '%s' '/tmp/km-agent-run.sh; exec bash'\"", sessionName),
	}

	_, err = ssmClient.SendCommand(ctx, &ssmpkg.SendCommandInput{
		InstanceIds:    []string{instanceID},
		DocumentName:   awssdk.String("AWS-RunShellScript"),
		TimeoutSeconds: awssdk.Int32(600),
		Parameters:     map[string][]string{"commands": cmds},
	})
	if err != nil {
		return fmt.Errorf("send agent command via SSM to %s: %w", instanceID, err)
	}

	log.Info().Str("sandbox_id", event.SandboxID).Str("instance", instanceID).
		Str("run_id", runID).Msg("agent dispatched via SSM")
	return nil
}

// buildAgentRunScript builds the bash script that runs a scheduled agent prompt
// on the sandbox via SSM. It is a pure function (no AWS calls) so its output can
// be unit-tested.
//
// It is intentionally kept in parity with the interactive / direct `km agent run`
// execution context (BuildAgentShellCommands in internal/app/cmd/agent.go). The
// previous hand-rolled fork diverged in three ways that all silently broke
// scheduled `km at agent run` on no-bedrock (direct-API) sandboxes:
//
//  1. It only sourced km-profile-env.sh + km-identity.sh, so KM_SLACK_* (defined
//     in km-slack-runtime.sh / km-notify-env.sh) were absent and Slack-posting
//     skills had no channel/bridge to post to. We now source the full
//     /etc/profile.d/*.sh set like a login shell.
//  2. It passed --bare, which Claude Code documents as "Minimal mode: skip hooks,
//     LSP, plugin sync..." — that suppressed plugin/skill loading (e.g.
//     klanker:slack) and notification hooks. --bare is now omitted (matching the
//     Phase 68 decision on the direct path).
//  3. It injected the Claude OAuth token into ANTHROPIC_API_KEY, which is sent as
//     the x-api-key header; an sk-ant-oat OAuth token presented that way is
//     rejected with 401 "Invalid API key". The token must be presented as a
//     Bearer token via CLAUDE_CODE_OAUTH_TOKEN. We also auto-detect no-bedrock
//     mode at runtime (empty CLAUDE_CODE_USE_BEDROCK after sourcing profile.d) so
//     a no-bedrock sandbox authenticates even without an explicit --no-bedrock.
//
// noBedrock=true (from `km at ... --no-bedrock`) additionally force-unsets the
// Bedrock env on an otherwise Bedrock-capable sandbox.
func buildAgentRunScript(b64Prompt, bucket, runID string, noBedrock bool) string {
	unsetBedrock := ""
	if noBedrock {
		unsetBedrock = `unset CLAUDE_CODE_USE_BEDROCK
unset ANTHROPIC_BASE_URL
unset ANTHROPIC_DEFAULT_SONNET_MODEL
unset ANTHROPIC_DEFAULT_HAIKU_MODEL
unset ANTHROPIC_DEFAULT_OPUS_MODEL
`
	}
	return fmt.Sprintf(`#!/bin/bash
export HOME=/home/sandbox
# Source the full profile.d set like an interactive login shell so KM_SLACK_*,
# audit, proxy, and agent env are all present (interactive/direct-run parity).
for f in /etc/profile.d/*.sh; do source "$f" 2>/dev/null; done
%s# Direct-API (no-bedrock) auth: headless claude needs the OAuth token presented
# as a Bearer token via CLAUDE_CODE_OAUTH_TOKEN. ANTHROPIC_API_KEY is sent as the
# x-api-key header and an sk-ant-oat OAuth token is rejected 401 there. Skip when
# Bedrock is active or a key/token is already set.
if [ -z "$CLAUDE_CODE_USE_BEDROCK" ] && [ -z "$ANTHROPIC_API_KEY" ] && [ -z "$CLAUDE_CODE_OAUTH_TOKEN" ] && [ -f "$HOME/.claude/.credentials.json" ]; then
  OAUTH_TOKEN=$(python3 -c "import json; d=json.load(open('$HOME/.claude/.credentials.json')); print(d.get('claudeAiOauth',{}).get('accessToken',''))" 2>/dev/null)
  if [ -n "$OAUTH_TOKEN" ]; then export CLAUDE_CODE_OAUTH_TOKEN="$OAUTH_TOKEN"; fi
fi
KM_ARTIFACTS_BUCKET="%s"
cd /workspace
RUN_ID="%s"
RUN_DIR="/workspace/.km-agent/runs/$RUN_ID"
mkdir -p "$RUN_DIR"
echo "running" > "$RUN_DIR/status"
PROMPT=$(echo "%s" | base64 -d)
claude -p "$PROMPT" --output-format json --dangerously-skip-permissions \
  > "$RUN_DIR/output.json" 2>"$RUN_DIR/stderr.log"
EC=$?
if [ $EC -eq 0 ]; then echo "complete" > "$RUN_DIR/status"; else echo "failed" > "$RUN_DIR/status"; echo "$EC" > "$RUN_DIR/exit_code"; fi
if [ -n "$KM_ARTIFACTS_BUCKET" ] && [ -n "$KM_SANDBOX_ID" ]; then
  aws s3 cp "$RUN_DIR/output.json" "s3://$KM_ARTIFACTS_BUCKET/agent-runs/$KM_SANDBOX_ID/$RUN_ID/output.json" --quiet 2>/dev/null || true
  aws s3 cp "$RUN_DIR/status" "s3://$KM_ARTIFACTS_BUCKET/agent-runs/$KM_SANDBOX_ID/$RUN_ID/status" --quiet 2>/dev/null || true
fi
tmux wait-for -S km-done-%s`, unsetBedrock, bucket, runID, b64Prompt, runID)
}

// handleScheduleCreate creates an EventBridge Scheduler schedule for a deferred sandbox create.
// This is a relay from the email Lambda which can't call scheduler:CreateSchedule directly (SCP).
func (h *TTLHandler) handleScheduleCreate(ctx context.Context, event TTLEvent) error {
	log.Info().Str("sandbox_id", event.SandboxID).Str("schedule_time", event.ScheduleTime).
		Msg("schedule-create event received")

	if event.ScheduleTime == "" {
		return fmt.Errorf("schedule-create: schedule_time is required")
	}

	// Parse the natural language time expression.
	spec, err := atpkg.Parse(event.ScheduleTime, time.Now())
	if err != nil {
		return fmt.Errorf("parse schedule time %q: %w", event.ScheduleTime, err)
	}

	// Build the create event detail JSON that the create-handler Lambda expects.
	createDetail := map[string]interface{}{
		"sandbox_id":      event.SandboxID,
		"artifact_bucket": event.ArtifactBucket,
		"artifact_prefix": event.ArtifactPrefix,
		"operator_email":  event.OperatorEmail,
		"on_demand":       event.OnDemand,
		"alias":           event.Alias,
		"created_by":      "email-schedule",
	}
	detailBytes, _ := json.Marshal(createDetail)

	// Resolve create-handler Lambda ARN from env.
	createHandlerARN := os.Getenv("KM_CREATE_HANDLER_ARN")
	if createHandlerARN == "" {
		return fmt.Errorf("schedule-create: KM_CREATE_HANDLER_ARN not set")
	}

	schedulerRoleARN := os.Getenv("KM_SCHEDULER_ROLE_ARN")
	if schedulerRoleARN == "" {
		return fmt.Errorf("schedule-create: KM_SCHEDULER_ROLE_ARN not set")
	}

	scheduleName := atpkg.GenerateScheduleName("create", event.SandboxID, event.ScheduleTime)

	schedInput := &scheduler.CreateScheduleInput{
		Name:                       awssdk.String(scheduleName),
		GroupName:                  awssdk.String(atGroupName()),
		ScheduleExpression:         awssdk.String(spec.Expression),
		ScheduleExpressionTimezone: awssdk.String("UTC"),
		Target: &schedulertypes.Target{
			Arn:     awssdk.String(createHandlerARN),
			RoleArn: awssdk.String(schedulerRoleARN),
			Input:   awssdk.String(string(detailBytes)),
		},
		ActionAfterCompletion: schedulertypes.ActionAfterCompletionDelete,
		FlexibleTimeWindow: &schedulertypes.FlexibleTimeWindow{
			Mode: schedulertypes.FlexibleTimeWindowModeOff,
		},
	}

	if err := awspkg.CreateAtSchedule(ctx, h.Scheduler, schedInput); err != nil {
		return fmt.Errorf("create schedule: %w", err)
	}

	// Save record to the schedules table so km at list shows it.
	schedTableName := schedulesTableName()
	rec := awspkg.ScheduleRecord{
		ScheduleName: scheduleName,
		Command:      "create",
		SandboxID:    event.SandboxID,
		TimeExpr:     event.ScheduleTime,
		CronExpr:     spec.Expression,
		IsRecurring:  spec.IsRecurring,
		Status:       "active",
		CreatedAt:    time.Now(),
	}
	_ = awspkg.PutSchedule(ctx, h.DynamoClient, schedTableName, rec)

	log.Info().Str("sandbox_id", event.SandboxID).Str("schedule", scheduleName).
		Str("expression", spec.Expression).Msg("schedule created")
	return nil
}

// handleExtend updates the TTL schedule and metadata with a new expiry.
func (h *TTLHandler) handleExtend(ctx context.Context, event TTLEvent) error {
	log.Info().Str("sandbox_id", event.SandboxID).Str("duration", event.Duration).Msg("extend event received")

	addDuration, err := time.ParseDuration(event.Duration)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", event.Duration, err)
	}

	// Read current metadata from DynamoDB.
	meta, err := awspkg.ReadSandboxMetadataDynamo(ctx, h.DynamoClient, h.SandboxTableName, event.SandboxID)
	if err != nil {
		return fmt.Errorf("read metadata: %w", err)
	}

	// Calculate new expiry
	var newExpiry time.Time
	if meta.TTLExpiry != nil && meta.TTLExpiry.After(time.Now()) {
		newExpiry = meta.TTLExpiry.Add(addDuration)
	} else {
		newExpiry = time.Now().Add(addDuration)
	}

	// Delete old schedule, create new one
	schedulerClient := scheduler.NewFromConfig(func() awssdk.Config {
		cfg, _ := awspkg.LoadAWSConfig(ctx, "")
		return cfg
	}())
	awspkg.DeleteTTLSchedule(ctx, schedulerClient, event.SandboxID, resourcePrefix())

	// Discover Lambda ARN and scheduler role for the new schedule
	awsCfg, _ := awspkg.LoadAWSConfig(ctx, "")
	lambdaClient := lambdapkg.NewFromConfig(awsCfg)
	fnOut, fnErr := lambdaClient.GetFunction(ctx, &lambdapkg.GetFunctionInput{
		FunctionName: awssdk.String(ttlHandlerName()),
	})
	if fnErr != nil {
		return fmt.Errorf("discover Lambda ARN: %w", fnErr)
	}
	iamClient := iampkg.NewFromConfig(awsCfg)
	roleOut, roleErr := iamClient.GetRole(ctx, &iampkg.GetRoleInput{
		RoleName: awssdk.String(ttlSchedulerRole()),
	})
	if roleErr != nil {
		return fmt.Errorf("discover scheduler role: %w", roleErr)
	}

	schedInput := compiler.BuildTTLScheduleInput(event.SandboxID, newExpiry,
		awssdk.ToString(fnOut.Configuration.FunctionArn),
		awssdk.ToString(roleOut.Role.Arn),
		resourcePrefix())
	if err := awspkg.CreateTTLSchedule(ctx, schedulerClient, schedInput); err != nil {
		return fmt.Errorf("create schedule: %w", err)
	}

	// Update metadata in DynamoDB.
	meta.TTLExpiry = &newExpiry
	if writeErr := awspkg.WriteSandboxMetadataDynamo(ctx, h.DynamoClient, h.SandboxTableName, meta); writeErr != nil {
		log.Warn().Err(writeErr).Str("sandbox_id", event.SandboxID).Msg("failed to update metadata in DynamoDB (non-fatal)")
	}

	log.Info().Str("sandbox_id", event.SandboxID).Time("new_expiry", newExpiry).Msg("TTL extended")
	return nil
}

// handleDestroy is the original destroy path.
func (h *TTLHandler) handleDestroy(ctx context.Context, event TTLEvent) error {
	sandboxID := event.SandboxID

	log.Info().Str("sandbox_id", sandboxID).Msg("TTL expiry event received")

	// Guard: for TTL expiry events only (empty event_type), check if the sandbox
	// was recently resumed (future TTL) and skip the stale event.
	// Explicit destroy (event_type "destroy") and idle events always proceed.
	if event.EventType == "" && h.DynamoClient != nil {
		meta, metaErr := awspkg.ReadSandboxMetadataDynamo(ctx, h.DynamoClient, h.SandboxTableName, sandboxID)
		if metaErr == nil && meta != nil && meta.ExpiresAt != nil && time.Until(*meta.ExpiresAt) > 5*time.Minute {
			log.Info().
				Str("sandbox_id", sandboxID).
				Time("expires_at", *meta.ExpiresAt).
				Msg("sandbox has future TTL — skipping stale destroy event (likely post-resume)")
			return nil
		}
	}

	// Step 2: Download sandbox profile from S3 (best-effort — missing profile skips artifact upload).
	var sandboxProfile *profilepkg.SandboxProfile
	profileBytes, profileErr := downloadProfileFromS3(ctx, h.S3Client, h.Bucket, sandboxID)
	if profileErr != nil {
		log.Warn().Err(profileErr).Str("sandbox_id", sandboxID).
			Msg("could not load sandbox profile from S3; skipping artifact upload")
	} else {
		sandboxProfile, _ = profilepkg.Parse(profileBytes)
	}

	// Step 3: Upload artifacts if the profile specifies artifact paths.
	var artifactsUploaded, artifactsSkipped int
	var artifactPaths []string
	if sandboxProfile != nil && sandboxProfile.Spec.Artifacts != nil && len(sandboxProfile.Spec.Artifacts.Paths) > 0 {
		arts := sandboxProfile.Spec.Artifacts
		artifactPaths = arts.Paths
		uploaded, skipped, uploadErr := awspkg.UploadArtifacts(ctx, h.S3Client, h.Bucket, sandboxID, arts.Paths, arts.MaxSizeMB)
		artifactsUploaded = uploaded
		artifactsSkipped = len(skipped)
		if uploadErr != nil {
			log.Warn().Err(uploadErr).Str("sandbox_id", sandboxID).
				Msg("artifact upload error (best-effort); continuing")
		} else {
			log.Info().Str("sandbox_id", sandboxID).
				Int("uploaded", uploaded).
				Int("skipped", len(skipped)).
				Msg("artifact upload complete")
		}
	} else {
		log.Debug().Str("sandbox_id", sandboxID).Msg("no artifact paths configured; skipping upload")
	}

	// Step 4: Send detailed lifecycle notification (if operator email is configured).
	if h.OperatorEmail != "" && h.SESClient != nil {
		detail := awspkg.NotificationDetail{
			SandboxID:         sandboxID,
			Event:             eventLabel(event),
			ArtifactsUploaded: artifactsUploaded,
			ArtifactsSkipped:  artifactsSkipped,
			ArtifactPaths:     artifactPaths,
		}
		// Read sandbox metadata for status-like fields (best-effort).
		if meta := readMetadataBestEffort(ctx, h.DynamoClient, h.SandboxTableName, sandboxID); meta != nil {
			detail.ProfileName = meta.ProfileName
			detail.Substrate = meta.Substrate
			detail.Region = meta.Region
			detail.CreatedAt = meta.CreatedAt
			detail.TTLExpiry = meta.TTLExpiry
			detail.IdleTimeout = meta.IdleTimeout
		}
		if notifyErr := awspkg.SendDetailedNotification(ctx, h.SESClient, h.OperatorEmail, h.Domain, detail); notifyErr != nil {
			log.Warn().Err(notifyErr).Str("sandbox_id", sandboxID).
				Msg("failed to send lifecycle notification (non-fatal)")
		}
	}

	// Step 5: Delete TTL schedule (self-cleanup, idempotent).
	if h.Scheduler != nil {
		if schedErr := awspkg.DeleteTTLSchedule(ctx, h.Scheduler, sandboxID, resourcePrefix()); schedErr != nil {
			log.Warn().Err(schedErr).Str("sandbox_id", sandboxID).
				Msg("failed to delete TTL schedule (non-fatal)")
		}
	}

	// Step 6: Destroy sandbox resources (PROV-05/PROV-06).
	if h.TeardownFunc != nil {
		if err := h.TeardownFunc(ctx, sandboxID); err != nil {
			log.Error().Err(err).Str("sandbox_id", sandboxID).Msg("sandbox teardown failed")
			return fmt.Errorf("teardown sandbox %s: %w", sandboxID, err)
		}
		log.Info().Str("sandbox_id", sandboxID).Msg("sandbox resources destroyed")
	} else {
		// Fallback: no terraform binary — terminate EC2 and clean up via SDK.
		log.Warn().Str("sandbox_id", sandboxID).Msg("no terraform — using SDK-only teardown")
		sdkTeardown := h.SDKTeardownFunc
		if sdkTeardown == nil {
			sdkTeardown = sdkOnlyTeardown
		}
		if err := sdkTeardown(ctx, h, sandboxID); err != nil {
			log.Error().Err(err).Str("sandbox_id", sandboxID).Msg("SDK-only teardown failed")
			return fmt.Errorf("sdk teardown sandbox %s: %w", sandboxID, err)
		}
	}

	// Step 7 (Phase 89 SOPS-16): delete the SOPS bundle from S3. The bundle is
	// uploaded by km create via PutObject (outside terraform), so terraform
	// destroy does not remove it. Local `km destroy` deletes it in destroy.go,
	// but the remote/TTL path runs terraform destroy here — without this, the
	// bundle is orphaned until the sandbox-secrets-7day lifecycle rule expires
	// it. Best-effort + non-fatal; DeleteObject is idempotent on a missing key.
	if h.S3Client != nil {
		bundleKey := fmt.Sprintf("sandboxes/%s/secrets.enc.yaml", sandboxID)
		if _, delErr := h.S3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: awssdk.String(h.Bucket),
			Key:    awssdk.String(bundleKey),
		}); delErr != nil {
			log.Warn().Err(delErr).Str("sandbox_id", sandboxID).
				Msg("failed to delete SOPS bundle from S3 (non-fatal; lifecycle rule is the backstop)")
		} else {
			log.Info().Str("sandbox_id", sandboxID).Str("key", bundleKey).
				Msg("deleted SOPS bundle from S3")
		}
	}

	log.Info().Str("sandbox_id", sandboxID).Msg("TTL handler completed")
	return nil
}

// lookupHibernation downloads the sandbox profile from S3 and returns whether
// hibernation is enabled. Returns false on any error — fail-safe to normal stop.
func (h *TTLHandler) lookupHibernation(ctx context.Context, sandboxID string) bool {
	profileBytes, err := downloadProfileFromS3(ctx, h.S3Client, h.Bucket, sandboxID)
	if err != nil {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).
			Msg("could not load profile for hibernation check; defaulting to false")
		return false
	}
	profile, parseErr := profilepkg.Parse(profileBytes)
	if parseErr != nil || profile == nil {
		return false
	}
	return profile.Spec.Runtime.Hibernation
}

// lookupTeardownPolicy downloads the sandbox profile from S3 and returns the
// teardownPolicy value ("destroy" or "stop"). Returns "destroy" on any error
// or if the profile doesn't specify a policy — fail-safe to full cleanup.
// isSandboxLocked reports whether the sandbox carries a km lock (the DynamoDB
// `locked` attribute). A locked sandbox must never be auto-stopped or
// auto-destroyed by TTL/idle triggers — the operator must km unlock first.
//
// Fails OPEN (returns false) when there is no DynamoDB client or the read
// errors, so a transient DynamoDB fault cannot wedge automatic teardown forever
// and leak resources; the failure is logged. The authoritative teardownPolicy
// (S3-backed) is the primary safety net; the lock is defense-in-depth.
func (h *TTLHandler) isSandboxLocked(ctx context.Context, sandboxID string) bool {
	if h.DynamoClient == nil {
		return false
	}
	meta, err := awspkg.ReadSandboxMetadataDynamo(ctx, h.DynamoClient, h.SandboxTableName, sandboxID)
	if err != nil {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).
			Msg("could not read lock state from DynamoDB; treating as unlocked")
		return false
	}
	return meta != nil && meta.Locked
}

func (h *TTLHandler) lookupTeardownPolicy(ctx context.Context, sandboxID string) string {
	// Primary: read from S3 profile (authoritative source).
	profileBytes, err := downloadProfileFromS3(ctx, h.S3Client, h.Bucket, sandboxID)
	if err == nil {
		profile, parseErr := profilepkg.Parse(profileBytes)
		if parseErr == nil && profile != nil && profile.Spec.Lifecycle.TeardownPolicy != "" {
			return profile.Spec.Lifecycle.TeardownPolicy
		}
	} else {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).
			Msg("could not load profile from S3 for teardownPolicy check; trying DynamoDB")
	}

	// Fallback: read teardown_policy from DynamoDB metadata.
	// This prevents a transient S3 error from destroying a teardownPolicy=stop sandbox.
	if h.DynamoClient != nil {
		meta, metaErr := awspkg.ReadSandboxMetadataDynamo(ctx, h.DynamoClient, h.SandboxTableName, sandboxID)
		if metaErr == nil && meta != nil && meta.TeardownPolicy != "" {
			log.Info().Str("sandbox_id", sandboxID).Str("teardown_policy", meta.TeardownPolicy).
				Msg("teardownPolicy resolved from DynamoDB fallback")
			return meta.TeardownPolicy
		}
	}

	return "destroy"
}

// eventLabel returns a human-friendly label for the TTL event type.
func eventLabel(event TTLEvent) string {
	switch event.EventType {
	case "idle":
		return "idle-timeout"
	case "destroy":
		return "destroyed"
	case "stop":
		return "stopped"
	case "":
		return "ttl-expired"
	default:
		return event.EventType
	}
}

// readMetadataBestEffort reads sandbox metadata from DynamoDB by sandbox ID.
// Returns nil on any error or when client is nil — callers should treat metadata
// as optional enrichment only.
func readMetadataBestEffort(ctx context.Context, client awspkg.SandboxMetadataAPI, tableName, sandboxID string) *awspkg.SandboxMetadata {
	if client == nil {
		return nil
	}
	meta, err := awspkg.ReadSandboxMetadataDynamo(ctx, client, tableName, sandboxID)
	if err != nil {
		return nil
	}
	return meta
}

// downloadProfileFromS3 retrieves the sandbox profile YAML stored at
// artifacts/{sandboxID}/.km-profile.yaml in the given S3 bucket.
// This mirrors the same function in internal/app/cmd/destroy.go.
func downloadProfileFromS3(ctx context.Context, client S3GetAPI, bucket, sandboxID string) ([]byte, error) {
	key := "artifacts/" + sandboxID + "/.km-profile.yaml"
	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get profile from S3 s3://%s/%s: %w", bucket, key, err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// launchAccountLink is the Lambda-side mirror of config.LaunchAccountConfig's
// destroy-relevant fields — only what terraformDestroy needs to render a
// cross-account assume_role provider block. Parsed from the KM_LAUNCH_ACCOUNTS
// environment variable (a JSON object keyed by link name, mirroring the
// existing JSON-blob-in-a-Lambda-env-var precedent used by KM_GITHUB_EVENTS /
// KM_SLACK_PEER_BRIDGES) once at cold start, Phase 126 (REQ-126-TEARDOWN).
type launchAccountLink struct {
	LauncherRoleARN string `json:"launcher_role_arn"`
	ExternalIDSSM   string `json:"external_id_ssm"`
	Region          string `json:"region"`
}

// destroyTerraformInputs bundles the explicit inputs renderDestroyMainTF depends
// on, so it is a pure function testable without a Lambda runtime or any AWS/SSM
// call (Phase 126 plan 08's acceptance criteria: "the rendering function is
// pure — it takes no handler pointer and performs no AWS call").
type destroyTerraformInputs struct {
	StateBucket string
	StateKey    string
	LockTable   string
	Region      string
	RegionLabel string
	SandboxID   string
	// ModuleLabel replaces the pre-Phase-126 hardcoded "km" literal with the
	// resource-prefix helper (Task 2's second, independent fix) — a no-op on a
	// default-prefix install, a correctness fix on any other one.
	ModuleLabel string
	// LauncherRoleARN / ExternalID are set only for a linked teardown; both empty
	// renders the plain provider block, byte-identical to Phase 125.
	LauncherRoleARN string
	ExternalID      string
}

// renderDestroyMainTF renders the minimal main.tf terraformDestroy writes to
// its scratch work directory. Pure: no handler pointer, no AWS call — every one
// of Task 2's six behaviors is exercised against this function's output alone.
func renderDestroyMainTF(in destroyTerraformInputs) string {
	assumeRoleBlock := ""
	if in.LauncherRoleARN != "" {
		assumeRoleBlock = fmt.Sprintf(`
  assume_role {
    role_arn    = %q
    external_id = %q
  }`, in.LauncherRoleARN, in.ExternalID)
	}

	return fmt.Sprintf(`
terraform {
  required_version = ">= 1.6.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }
  backend "s3" {
    bucket         = %q
    key            = %q
    region         = %q
    encrypt        = true
    dynamodb_table = %q
  }
}

provider "aws" {
  region = %q%s
}

module "sandbox" {
  source       = "./module"
  km_label     = %q
  region_label = %q
  region_full  = %q
  sandbox_id   = %q
  vpc_id       = "destroy-placeholder"
  public_subnets     = []
  availability_zones = []
  ec2spots           = []
}
`, in.StateBucket, in.StateKey, in.Region,
		in.LockTable, in.Region, assumeRoleBlock,
		in.ModuleLabel, in.RegionLabel, in.Region, in.SandboxID)
}

// ttlExternalIDReader abstracts the SSM read for a link's external id, so
// buildDestroyTerraformInputs is testable with a stub instead of a real SSM
// client — mirrors the SSMParamStore.Get seam convention used throughout
// internal/app/cmd.
type ttlExternalIDReader func(ctx context.Context, ssmPath string) (string, error)

// buildDestroyTerraformInputs resolves the region, region label, state key, and
// (when linked) the assume-role inputs for a teardown's rendered configuration,
// from the sandbox's launch account name and the parsed KM_LAUNCH_ACCOUNTS link
// map.
//
// launchAccountName == "" (a home-account sandbox, or an unreadable metadata
// row — both fall through to this case): dormant. Home region/label are used
// unchanged and readExternalID is never called — Test 1's byte-identity
// assertion depends on this path performing no extra work.
//
// launchAccountName set but absent from links: a hard error naming the link
// and KM_LAUNCH_ACCOUNTS (Test 5) — the Lambda's environment is missing a link
// the sandbox's own row claims exists.
//
// readExternalID failing (Test 4): also a hard error, wrapping the underlying
// failure. In both error cases, rendering a configuration with an empty
// external id (or a home-account provider) would fail confusingly at apply
// time or, worse, silently report success while the linked instance kept
// running and billing (T-126-42) — so this function returns the zero value on
// any error, never a partially-populated one.
func buildDestroyTerraformInputs(
	ctx context.Context,
	launchAccountName string,
	links map[string]launchAccountLink,
	homeRegion, homeRegionLabel, moduleLabel, stateBucket, statePrefix, sandboxID string,
	readExternalID ttlExternalIDReader,
) (destroyTerraformInputs, error) {
	in := destroyTerraformInputs{
		StateBucket: stateBucket,
		Region:      homeRegion,
		RegionLabel: homeRegionLabel,
		ModuleLabel: moduleLabel,
		SandboxID:   sandboxID,
	}

	if launchAccountName != "" {
		link, ok := links[launchAccountName]
		if !ok {
			return destroyTerraformInputs{}, fmt.Errorf(
				"sandbox's launch_account %q is not present in KM_LAUNCH_ACCOUNTS — the Lambda's environment is missing this link (re-run km init after registering it)",
				launchAccountName)
		}
		externalID, err := readExternalID(ctx, link.ExternalIDSSM)
		if err != nil {
			return destroyTerraformInputs{}, fmt.Errorf(
				"read external id for launch_account %q from %s: %w", launchAccountName, link.ExternalIDSSM, err)
		}
		in.Region = link.Region
		in.RegionLabel = compiler.RegionLabel(link.Region)
		in.LauncherRoleARN = link.LauncherRoleARN
		in.ExternalID = externalID
	}

	// State key: {prefix}/{regionLabel}/sandboxes/<sandbox-id>/terraform.tfstate.
	// The backend bucket is ALWAYS the home account's (state never crosses the
	// account boundary — only the provider does, per Pattern 1); the regionLabel
	// component matches exactly what the create path wrote it under
	// (create.go: regionLabel := compiler.RegionLabel(region), where region comes
	// from resolveLaunchRegion(profile, launchTarget) — the same link-first
	// precedence this function follows).
	in.StateKey = fmt.Sprintf("%s/%s/sandboxes/%s/terraform.tfstate", statePrefix, in.RegionLabel, sandboxID)
	in.LockTable = statePrefix + "-locks-" + in.RegionLabel

	return in, nil
}

// readSSMParameter fetches and decrypts a single SSM parameter. The production
// ttlExternalIDReader closure in terraformDestroy wraps this; tests call
// buildDestroyTerraformInputs directly with a stub reader instead.
func readSSMParameter(ctx context.Context, client *ssmpkg.Client, name string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("no SSM client configured")
	}
	out, err := client.GetParameter(ctx, &ssmpkg.GetParameterInput{
		Name:           awssdk.String(name),
		WithDecryption: awssdk.Bool(true),
	})
	if err != nil {
		return "", err
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("parameter %s has no value", name)
	}
	return *out.Parameter.Value, nil
}

// terraformDestroy runs `terraform destroy -auto-approve` against the sandbox's
// S3-backed state. The terraform binary is bundled alongside bootstrap in the Lambda zip.
func terraformDestroy(ctx context.Context, h *TTLHandler, sandboxID string) error {
	// Lambda writable directory — clean up any leftovers from previous failed runs
	workDir := filepath.Join("/tmp", "tf-"+sandboxID)
	os.RemoveAll(workDir)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	region := h.Region
	if region == "" {
		region = "us-east-1"
	}
	regionLabel := h.RegionLabel
	if regionLabel == "" {
		regionLabel = "use1"
	}
	statePrefix := h.StatePrefix
	if statePrefix == "" {
		statePrefix = "tf-km"
	}

	// Determine the terraform module source from the state file.
	// For now, assume ec2spot — the most common substrate.
	// TODO: Read substrate from metadata.json to handle ECS sandboxes.
	moduleSource := "ec2spot"

	// Phase 126 (REQ-126-TEARDOWN): resolve the sandbox's launch account, if any,
	// from its DynamoDB row. h.Region/h.RegionLabel/h.StatePrefix are Lambda-
	// instance-wide (cold-start env vars) — a linked sandbox's region must come
	// from the LINK record instead (126-RESEARCH.md Pattern 5, final paragraph),
	// not from these instance fields. A metadata-read failure (or no DynamoClient
	// injected, as in most existing tests) falls through to the unmodified home
	// path — a metadata outage must not block a home-account destroy, mirroring
	// destroy.go's identical fail-open choice for the operator-side path.
	launchAccountName := ""
	if h.DynamoClient != nil {
		if meta, metaErr := awspkg.ReadSandboxMetadataDynamo(ctx, h.DynamoClient, h.SandboxTableName, sandboxID); metaErr == nil {
			if meta != nil {
				launchAccountName = meta.LaunchAccount
			}
		} else {
			log.Warn().Err(metaErr).Str("sandbox_id", sandboxID).
				Msg("failed to read sandbox metadata for launch-account resolution — proceeding as home-account destroy")
		}
	}

	tfInputs, tfErr := buildDestroyTerraformInputs(ctx, launchAccountName, h.LaunchAccounts,
		region, regionLabel, resourcePrefix(), h.StateBucket, statePrefix, sandboxID,
		func(rctx context.Context, ssmPath string) (string, error) {
			return readSSMParameter(rctx, h.SSMClient, ssmPath)
		})
	if tfErr != nil {
		// Test 5 (unknown link) / Test 4 (external-id read failure): an unknown
		// link or a failed external-id read is a hard error for THIS teardown — a
		// silent home-account destroy attempt would report success while the
		// linked instance kept running and billing (T-126-42).
		return fmt.Errorf("resolve launch account for sandbox %s teardown: %w", sandboxID, tfErr)
	}

	// Write a minimal main.tf that references the same module and backend.
	// terraform destroy only needs the module source and state — it reads
	// resource addresses from state and destroys them.
	mainTF := renderDestroyMainTF(tfInputs)

	if err := os.WriteFile(filepath.Join(workDir, "main.tf"), []byte(mainTF), 0o644); err != nil {
		return fmt.Errorf("write main.tf: %w", err)
	}

	// Download the module source from the Lambda's bundled modules directory.
	// The Lambda zip includes infra/modules/ alongside the bootstrap binary.
	// In Lambda, the binary runs from /var/task/ — modules are at /var/task/infra/modules/
	bundledModule := filepath.Join("/var/task", "infra", "modules", moduleSource, "v1.0.0")
	if _, err := os.Stat(bundledModule); os.IsNotExist(err) {
		// Fallback: module not bundled, try direct state-only destroy
		log.Warn().Str("module", bundledModule).Msg("module not bundled in Lambda; attempting state-only destroy")
		return terraformDestroyStateOnly(ctx, workDir)
	}

	// Symlink the module so terraform can read it
	if err := os.Symlink(bundledModule, filepath.Join(workDir, "module")); err != nil {
		return fmt.Errorf("symlink module: %w", err)
	}

	// terraform init — use /tmp for all data to stay within ephemeral storage
	log.Info().Str("sandbox_id", sandboxID).Msg("running terraform init")
	tfEnv := append(os.Environ(), "TF_DATA_DIR="+filepath.Join(workDir, ".terraform"))
	initCmd := exec.CommandContext(ctx, "/var/task/terraform", "init", "-no-color", "-input=false")
	initCmd.Dir = workDir
	initCmd.Env = tfEnv
	if out, err := initCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("terraform init: %s: %w", string(out), err)
	}

	// terraform destroy with -lock=false: the Lambda is the authoritative teardown
	// path for expired sandboxes. EventBridge may retry and invoke multiple concurrent
	// Lambdas — without -lock=false they deadlock on the state lock.
	log.Info().Str("sandbox_id", sandboxID).Msg("running terraform destroy")
	destroyCmd := exec.CommandContext(ctx, "/var/task/terraform", "destroy", "-auto-approve", "-no-color", "-input=false", "-lock=false")
	destroyCmd.Dir = workDir
	destroyCmd.Env = tfEnv
	out, err := destroyCmd.CombinedOutput()
	log.Info().Str("sandbox_id", sandboxID).Str("output", string(out)).Msg("terraform destroy output")
	if err != nil {
		return fmt.Errorf("terraform destroy: %s: %w", string(out), err)
	}

	// Clean up sub-module resources (Lambda, schedule, IAM roles) via SDK.
	// Simpler than running a second terraform destroy for each sub-module.
	cleanupBudgetEnforcer(ctx, h, sandboxID)
	cleanupGitHubToken(ctx, h, sandboxID)

	// Clean up DynamoDB metadata so km list no longer shows this sandbox.
	if h.DynamoClient != nil {
		if delErr := awspkg.DeleteSandboxMetadataDynamo(ctx, h.DynamoClient, h.SandboxTableName, sandboxID); delErr != nil {
			log.Warn().Err(delErr).Str("sandbox_id", sandboxID).Msg("failed to delete DynamoDB metadata (non-fatal)")
		}
	}
	// Clean up the sandbox's identity (SSM signing/encryption/safe-phrase params + km-identities row).
	// Without this, the next sandbox to reuse the alias inherits this row's stale pubkey via the
	// bridge's alias-index GSI lookup — 401 bad_signature until manually deleted.
	cleanupSandboxIdentity(ctx, h, sandboxID)
	// Also clean up budget-enforcer state file from S3.
	if h.StateBucket != "" {
		// Also clean up budget-enforcer state file
		stateKeyPrefix := h.StatePrefix
		if stateKeyPrefix == "" {
			stateKeyPrefix = "tf-km"
		}
		budgetStateKey := fmt.Sprintf("%s/sandboxes/%s/budget-enforcer/terraform.tfstate", stateKeyPrefix, sandboxID)
		h.S3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: awssdk.String(h.StateBucket),
			Key:    awssdk.String(budgetStateKey),
		})
	}

	// Export CloudWatch logs to S3 then delete the log group.
	// Export is fire-and-forget (async in AWS) and non-fatal — deletion proceeds regardless.
	if h.CWClient != nil {
		if h.Bucket != "" {
			if exportErr := awspkg.ExportSandboxLogs(ctx, h.CWClient, sandboxID, h.Bucket, resourcePrefix()); exportErr != nil {
				log.Warn().Err(exportErr).Str("sandbox_id", sandboxID).Msg("failed to export sandbox logs to S3 (non-fatal)")
			} else {
				log.Info().Str("sandbox_id", sandboxID).Str("bucket", h.Bucket).Msg("sandbox logs export task initiated")
			}
		}
		if cwErr := awspkg.DeleteSandboxLogGroup(ctx, h.CWClient, sandboxID, resourcePrefix()); cwErr != nil {
			log.Warn().Err(cwErr).Str("sandbox_id", sandboxID).Msg("failed to delete log group (non-fatal)")
		}
	}

	return nil
}

// terraformDestroyStateOnly runs terraform destroy without module source — relies on
// state containing enough info for terraform to identify and destroy resources.
func terraformDestroyStateOnly(ctx context.Context, workDir string) error {
	initCmd := exec.CommandContext(ctx, "/var/task/terraform", "init", "-no-color", "-input=false")
	initCmd.Dir = workDir
	if out, err := initCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("terraform init (state-only): %s: %w", string(out), err)
	}

	destroyCmd := exec.CommandContext(ctx, "/var/task/terraform", "destroy", "-auto-approve", "-no-color", "-input=false")
	destroyCmd.Dir = workDir
	out, err := destroyCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("terraform destroy (state-only): %s: %w", string(out), err)
	}
	log.Info().Str("output", string(out)).Msg("terraform destroy (state-only) output")
	return nil
}

// cleanupBudgetEnforcer removes budget-enforcer resources for a sandbox via SDK calls.
// All errors are non-fatal (logged as warnings) since the sandbox is already destroyed.
func cleanupBudgetEnforcer(ctx context.Context, h *TTLHandler, sandboxID string) {
	awsCfg, err := awspkg.LoadAWSConfig(ctx, "")
	if err != nil {
		log.Warn().Err(err).Msg("could not load AWS config for budget cleanup")
		return
	}

	// Delete budget-enforcer Lambda
	lambdaClient := lambdapkg.NewFromConfig(awsCfg)
	fnName := resourcePrefix() + "-budget-enforcer-" + sandboxID
	if _, delErr := lambdaClient.DeleteFunction(ctx, &lambdapkg.DeleteFunctionInput{
		FunctionName: awssdk.String(fnName),
	}); delErr != nil {
		log.Debug().Str("function", fnName).Msg("budget-enforcer Lambda not found or already deleted")
	} else {
		log.Info().Str("function", fnName).Msg("budget-enforcer Lambda deleted")
	}

	// Delete budget-enforcer EventBridge schedule
	schedulerClient := scheduler.NewFromConfig(awsCfg)
	schedName := resourcePrefix() + "-budget-" + sandboxID
	if _, delErr := schedulerClient.DeleteSchedule(ctx, &scheduler.DeleteScheduleInput{
		Name: awssdk.String(schedName),
	}); delErr != nil {
		var notFound *schedulertypes.ResourceNotFoundException
		if errors.As(delErr, &notFound) {
			log.Debug().Str("schedule", schedName).Msg("budget schedule not found or already deleted")
		} else {
			log.Warn().Err(delErr).Str("schedule", schedName).Msg("failed to delete budget schedule (non-fatal)")
		}
	} else {
		log.Info().Str("schedule", schedName).Msg("budget-enforcer schedule deleted")
	}

	// Delete budget-enforcer IAM roles.
	iamClient := iampkg.NewFromConfig(awsCfg)
	for _, roleName := range []string{
		resourcePrefix() + "-budget-enforcer-" + sandboxID,
		resourcePrefix() + "-budget-scheduler-" + sandboxID,
	} {
		deleteIAMRole(ctx, iamClient, roleName)
	}

	// Delete budget-enforcer log group
	if h.CWClient != nil {
		logGroup := "/aws/lambda/" + resourcePrefix() + "-budget-enforcer-" + sandboxID
		h.CWClient.DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{
			LogGroupName: awssdk.String(logGroup),
		})
	}
}

// cleanupGitHubToken removes all resources created by the github-token
// Terraform module using SDK calls. Mirrors cleanupGitHubTokenResources in destroy.go.
// Each step is idempotent and non-fatal.
func cleanupGitHubToken(ctx context.Context, h *TTLHandler, sandboxID string) {
	awsCfg, err := awspkg.LoadAWSConfig(ctx, "")
	if err != nil {
		log.Warn().Err(err).Msg("could not load AWS config for github-token cleanup")
		return
	}

	iamClient := iampkg.NewFromConfig(awsCfg)
	kmsClient := kmspkg.NewFromConfig(awsCfg)
	lambdaClient := lambdapkg.NewFromConfig(awsCfg)
	schedClient := scheduler.NewFromConfig(awsCfg)

	// 1. Delete EventBridge schedule.
	scheduleName := resourcePrefix() + "-github-token-" + sandboxID
	if _, delErr := schedClient.DeleteSchedule(ctx, &scheduler.DeleteScheduleInput{
		Name: awssdk.String(scheduleName),
	}); delErr != nil {
		if !strings.Contains(delErr.Error(), "ResourceNotFoundException") && !strings.Contains(delErr.Error(), "not found") {
			log.Warn().Err(delErr).Str("schedule", scheduleName).Msg("failed to delete github-token schedule (non-fatal)")
		}
	} else {
		log.Info().Str("schedule", scheduleName).Msg("github-token schedule deleted")
	}

	// 2. Delete Lambda function.
	lambdaName := resourcePrefix() + "-github-token-refresher-" + sandboxID
	if _, delErr := lambdaClient.DeleteFunction(ctx, &lambdapkg.DeleteFunctionInput{
		FunctionName: awssdk.String(lambdaName),
	}); delErr != nil {
		if !strings.Contains(delErr.Error(), "ResourceNotFoundException") && !strings.Contains(delErr.Error(), "not found") {
			log.Warn().Err(delErr).Str("function", lambdaName).Msg("failed to delete github-token Lambda (non-fatal)")
		}
	} else {
		log.Info().Str("function", lambdaName).Msg("github-token Lambda deleted")
	}

	// 3. Delete CloudWatch log group.
	if h.CWClient != nil {
		logGroup := "/aws/lambda/" + resourcePrefix() + "-github-token-refresher-" + sandboxID
		h.CWClient.DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{
			LogGroupName: awssdk.String(logGroup),
		})
	}

	// 4. Delete IAM roles (refresher + scheduler).
	for _, roleName := range []string{
		resourcePrefix() + "-github-token-refresher-" + sandboxID,
		resourcePrefix() + "-github-token-scheduler-" + sandboxID,
	} {
		deleteIAMRole(ctx, iamClient, roleName)
	}

	// 5. Schedule KMS key deletion and remove alias.
	kmsAlias := "alias/" + resourcePrefix() + "-github-token-" + sandboxID
	descOut, descErr := kmsClient.DescribeKey(ctx, &kmspkg.DescribeKeyInput{
		KeyId: awssdk.String(kmsAlias),
	})
	if descErr == nil && descOut.KeyMetadata != nil {
		keyID := awssdk.ToString(descOut.KeyMetadata.KeyId)
		if _, schedErr := kmsClient.ScheduleKeyDeletion(ctx, &kmspkg.ScheduleKeyDeletionInput{
			KeyId:               awssdk.String(keyID),
			PendingWindowInDays: awssdk.Int32(7),
		}); schedErr != nil {
			if !strings.Contains(schedErr.Error(), "pending deletion") {
				log.Warn().Err(schedErr).Str("key", kmsAlias).Msg("failed to schedule KMS key deletion (non-fatal)")
			}
		} else {
			log.Info().Str("key", kmsAlias).Msg("KMS key scheduled for deletion")
		}
		kmsClient.DeleteAlias(ctx, &kmspkg.DeleteAliasInput{
			AliasName: awssdk.String(kmsAlias),
		})
	}
}

// deleteIAMRole detaches all policies and deletes an IAM role. Non-fatal and idempotent.
func deleteIAMRole(ctx context.Context, iamClient *iampkg.Client, roleName string) {
	// Detach managed policies.
	attachedOut, _ := iamClient.ListAttachedRolePolicies(ctx, &iampkg.ListAttachedRolePoliciesInput{
		RoleName: awssdk.String(roleName),
	})
	if attachedOut != nil {
		for _, p := range attachedOut.AttachedPolicies {
			iamClient.DetachRolePolicy(ctx, &iampkg.DetachRolePolicyInput{
				RoleName:  awssdk.String(roleName),
				PolicyArn: p.PolicyArn,
			})
		}
	}
	// Delete inline policies.
	policiesOut, _ := iamClient.ListRolePolicies(ctx, &iampkg.ListRolePoliciesInput{
		RoleName: awssdk.String(roleName),
	})
	if policiesOut != nil {
		for _, pName := range policiesOut.PolicyNames {
			iamClient.DeleteRolePolicy(ctx, &iampkg.DeleteRolePolicyInput{
				RoleName:   awssdk.String(roleName),
				PolicyName: awssdk.String(pName),
			})
		}
	}
	// Remove role from instance profiles.
	profilesOut, _ := iamClient.ListInstanceProfilesForRole(ctx, &iampkg.ListInstanceProfilesForRoleInput{
		RoleName: awssdk.String(roleName),
	})
	if profilesOut != nil {
		for _, ip := range profilesOut.InstanceProfiles {
			iamClient.RemoveRoleFromInstanceProfile(ctx, &iampkg.RemoveRoleFromInstanceProfileInput{
				RoleName:            awssdk.String(roleName),
				InstanceProfileName: ip.InstanceProfileName,
			})
			// Also delete the instance profile if it's sandbox-specific (this install's prefix).
			if strings.Contains(awssdk.ToString(ip.InstanceProfileName), resourcePrefix()+"-") {
				iamClient.DeleteInstanceProfile(ctx, &iampkg.DeleteInstanceProfileInput{
					InstanceProfileName: ip.InstanceProfileName,
				})
			}
		}
	}
	// Delete the role.
	if _, delErr := iamClient.DeleteRole(ctx, &iampkg.DeleteRoleInput{
		RoleName: awssdk.String(roleName),
	}); delErr == nil {
		log.Info().Str("role", roleName).Msg("IAM role deleted")
	}
}

// cleanupSandboxIdentityWith deletes the sandbox's SSM signing/encryption/safe-phrase
// parameters and its row from the km-identities DynamoDB table. Non-fatal: failures
// are logged and swallowed so the parent destroy still succeeds. Skips silently when
// either client is nil (mirrors the rest of the TTL handler's optional-dependency style).
//
// Without this, the bridge's alias-based identity lookup will return the destroyed
// sandbox's stale pubkey for any subsequent sandbox that reuses the same alias,
// producing 401 bad_signature on every signed request. The local-destroy path in
// internal/app/cmd/destroy.go already does this cleanup; the remote/TTL Lambda did not.
func cleanupSandboxIdentityWith(ctx context.Context, ssmClient awspkg.IdentitySSMAPI, dynClient awspkg.IdentityTableAPI, tableName, prefix, sandboxID string) {
	if ssmClient == nil || dynClient == nil {
		return
	}
	if err := awspkg.CleanupSandboxIdentity(ctx, ssmClient, dynClient, tableName, prefix, sandboxID); err != nil {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).
			Msg("failed to cleanup sandbox identity (non-fatal)")
	}
}

// cleanupSandboxIdentity is the TTLHandler-bound wrapper around cleanupSandboxIdentityWith.
func cleanupSandboxIdentity(ctx context.Context, h *TTLHandler, sandboxID string) {
	tableName := h.IdentityTable
	if tableName == "" {
		tableName = identitiesTable()
	}
	cleanupSandboxIdentityWith(ctx, h.SSMClient, h.DynamoClient, tableName, resourcePrefix(), sandboxID)
}

// sdkOnlyTeardown is the fallback destroy path when terraform binary isn't bundled.
// Terminates EC2 instances, cleans up security groups, instance profiles, IAM roles,
// EventBridge schedules, KMS keys, and DynamoDB/CW state.
func sdkOnlyTeardown(ctx context.Context, h *TTLHandler, sandboxID string) error {
	awsCfg, err := awspkg.LoadAWSConfig(ctx, "")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	ec2Client := ec2pkg.NewFromConfig(awsCfg)
	iamClient := iampkg.NewFromConfig(awsCfg)

	// 1. Terminate EC2 instance.
	descOut, err := ec2Client.DescribeInstances(ctx, &ec2pkg.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("tag:km:sandbox-id"), Values: []string{sandboxID}},
		},
	})
	if err != nil {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).Msg("could not describe instances")
	} else {
		for _, res := range descOut.Reservations {
			for _, inst := range res.Instances {
				instanceID := awssdk.ToString(inst.InstanceId)
				state := inst.State.Name
				if state == ec2types.InstanceStateNameTerminated || state == ec2types.InstanceStateNameShuttingDown {
					continue
				}
				log.Info().Str("instance_id", instanceID).Msg("terminating EC2 instance")
				ec2Client.TerminateInstances(ctx, &ec2pkg.TerminateInstancesInput{
					InstanceIds: []string{instanceID},
				})
			}
		}
	}

	// 2. Delete security group.
	sgOut, err := ec2Client.DescribeSecurityGroups(ctx, &ec2pkg.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("tag:km:sandbox-id"), Values: []string{sandboxID}},
		},
	})
	if err == nil {
		for _, sg := range sgOut.SecurityGroups {
			sgID := awssdk.ToString(sg.GroupId)
			log.Info().Str("sg_id", sgID).Msg("deleting security group")
			// SG deletion may fail if instance hasn't fully terminated yet — best effort.
			if _, delErr := ec2Client.DeleteSecurityGroup(ctx, &ec2pkg.DeleteSecurityGroupInput{
				GroupId: awssdk.String(sgID),
			}); delErr != nil {
				log.Warn().Err(delErr).Str("sg_id", sgID).Msg("failed to delete security group (may need instance termination to complete)")
			}
		}
	}

	// 3. Delete sandbox IAM role + instance profile.
	ssmRoleName := resourcePrefix() + "-ec2spot-ssm-" + sandboxID + "-" + h.RegionLabel
	deleteIAMRole(ctx, iamClient, ssmRoleName)

	// Also clean instance profile directly (may have same name pattern).
	ipName := resourcePrefix() + "-ec2spot-profile-" + sandboxID + "-" + h.RegionLabel
	iamClient.RemoveRoleFromInstanceProfile(ctx, &iampkg.RemoveRoleFromInstanceProfileInput{
		RoleName:            awssdk.String(ssmRoleName),
		InstanceProfileName: awssdk.String(ipName),
	})
	iamClient.DeleteInstanceProfile(ctx, &iampkg.DeleteInstanceProfileInput{
		InstanceProfileName: awssdk.String(ipName),
	})

	// 4. Clean up sub-module resources.
	cleanupBudgetEnforcer(ctx, h, sandboxID)
	cleanupGitHubToken(ctx, h, sandboxID)

	// 5. Clean up DynamoDB metadata.
	if h.DynamoClient != nil {
		if delErr := awspkg.DeleteSandboxMetadataDynamo(ctx, h.DynamoClient, h.SandboxTableName, sandboxID); delErr != nil {
			log.Warn().Err(delErr).Str("sandbox_id", sandboxID).Msg("failed to delete DynamoDB metadata (non-fatal)")
		}
	}

	// 5b. Clean up the sandbox's identity (SSM signing/encryption/safe-phrase params + km-identities row).
	cleanupSandboxIdentity(ctx, h, sandboxID)

	// 6. Export and delete CloudWatch logs.
	if h.CWClient != nil {
		if h.Bucket != "" {
			awspkg.ExportSandboxLogs(ctx, h.CWClient, sandboxID, h.Bucket, resourcePrefix())
		}
		awspkg.DeleteSandboxLogGroup(ctx, h.CWClient, sandboxID, resourcePrefix())
	}

	return nil
}

// main constructs a TTLHandler with real AWS clients and registers it with the Lambda runtime.
func main() {
	ctx := context.Background()
	awsProfile := os.Getenv("KM_AWS_PROFILE") // empty in Lambda — uses execution role

	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load AWS config")
	}

	bucket := os.Getenv("KM_ARTIFACTS_BUCKET")
	if bucket == "" {
		bucket = "km-artifacts" // fallback; should be set via Lambda env var
	}
	domain := getEmailDomain()

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	sandboxTbl := sandboxTableName()
	budgetTbl := budgetTableName()

	s3Client := s3.NewFromConfig(awsCfg)
	dynamoClient := dynamodbpkg.NewFromConfig(awsCfg)

	h := &TTLHandler{
		S3Client:         s3Client,
		DynamoClient:     dynamoClient,
		SandboxTableName: sandboxTbl,
		SESClient:        sesv2.NewFromConfig(awsCfg),
		Scheduler:        scheduler.NewFromConfig(awsCfg),
		CWClient:         cloudwatchlogs.NewFromConfig(awsCfg),
		SSMClient:        ssmpkg.NewFromConfig(awsCfg),
		IdentityTable:    identitiesTable(),
		Bucket:           bucket,
		StateBucket:      os.Getenv("KM_STATE_BUCKET"),
		StatePrefix:      os.Getenv("KM_STATE_PREFIX"),
		Region:           region,
		RegionLabel:      os.Getenv("KM_REGION_LABEL"),
		OperatorEmail:    os.Getenv("KM_OPERATOR_EMAIL"),
		Domain:           domain,
		// BudgetClient reuses the existing DynamoDB client — no second client construction.
		BudgetClient:   dynamoClient,
		BudgetTable:    budgetTbl,
		TeardownFunc:   nil, // set below
		LaunchAccounts: parseLaunchAccountsEnv(),
	}

	// Use terraform-based teardown if terraform binary is bundled.
	if _, err := os.Stat("/var/task/terraform"); err == nil {
		h.TeardownFunc = func(ctx context.Context, sandboxID string) error {
			return terraformDestroy(ctx, h, sandboxID)
		}
		log.Info().Msg("terraform binary found — using terraform destroy for teardown")
	} else {
		log.Warn().Msg("terraform binary not found — teardown will be skipped")
	}

	lambdaruntime.Start(h.HandleTTLEvent)
}
