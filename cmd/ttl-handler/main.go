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
	"github.com/rs/zerolog/log"
	atpkg "github.com/whereiskurt/klankrmkr/pkg/at"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/compiler"
	profilepkg "github.com/whereiskurt/klankrmkr/pkg/profile"
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
	// Schedule fields for "schedule-create" events (relay from email Lambda).
	ScheduleTime   string `json:"schedule_time,omitempty"`    // natural language time expression
	ArtifactBucket string `json:"artifact_bucket,omitempty"`
	ArtifactPrefix string `json:"artifact_prefix,omitempty"`
	OperatorEmail  string `json:"operator_email_event,omitempty"` // from email conversation
	OnDemand       bool   `json:"on_demand,omitempty"`
	Alias          string `json:"alias,omitempty"`
	ProfileName    string `json:"profile_name,omitempty"`
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
	// TeardownFunc destroys the sandbox resources after TTL expiry or idle detection.
	// If nil, teardown is skipped (backward compatible with existing tests).
	TeardownFunc func(ctx context.Context, sandboxID string) error
}

// HandleTTLEvent is the Lambda handler method. It is called by lambdaruntime.Start in main().
func (h *TTLHandler) HandleTTLEvent(ctx context.Context, event TTLEvent) error {
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
	case "schedule-create":
		return h.handleScheduleCreate(ctx, event)
	default:
		// "ttl", "idle", "destroy", "" — check teardownPolicy before destroying.
		// If the profile says "stop", stop the instance instead of destroying it.
		if event.EventType != "destroy" {
			if policy := h.lookupTeardownPolicy(ctx, event.SandboxID); policy == "stop" {
				log.Info().
					Str("sandbox_id", event.SandboxID).
					Str("event_type", event.EventType).
					Str("teardown_policy", "stop").
					Msg("teardownPolicy is 'stop' — stopping instead of destroying")
				return h.handleStop(ctx, event)
			}
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
		if statusErr := awspkg.UpdateSandboxStatusDynamo(ctx, h.DynamoClient, h.SandboxTableName, event.SandboxID, status); statusErr != nil {
			log.Warn().Err(statusErr).Str("sandbox_id", event.SandboxID).Msg("failed to update DynamoDB status (non-fatal)")
		} else {
			log.Info().Str("sandbox_id", event.SandboxID).Str("status", status).Msg("DynamoDB status updated")
		}
	}

	// Delete TTL schedule — stopped sandbox shouldn't be destroyed on TTL expiry.
	if h.Scheduler != nil {
		if schedErr := awspkg.DeleteTTLSchedule(ctx, h.Scheduler, event.SandboxID); schedErr != nil {
			log.Warn().Err(schedErr).Str("sandbox_id", event.SandboxID).Msg("failed to delete TTL schedule (non-fatal)")
		}
	}

	log.Info().Str("sandbox_id", event.SandboxID).Int("instances_stopped", stoppedCount).Msg("sandbox stopped")
	return nil
}

// handleResume starts a stopped EC2 instance.
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
		}
	}

	log.Info().Str("sandbox_id", event.SandboxID).Msg("sandbox resumed")
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
	budgetTableName := os.Getenv("KM_BUDGET_TABLE")
	if budgetTableName == "" {
		budgetTableName = "km-budgets"
	}

	// Atomic increment of budget limits via UpdateItem ADD expression
	update := &dynamodbpkg.UpdateItemInput{
		TableName: awssdk.String(budgetTableName),
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
		GroupName:                  awssdk.String("km-at"),
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

	// Save record to km-schedules so km at list shows it.
	schedTableName := os.Getenv("KM_SCHEDULES_TABLE")
	if schedTableName == "" {
		schedTableName = "km-schedules"
	}
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
	awspkg.DeleteTTLSchedule(ctx, schedulerClient, event.SandboxID)

	// Discover Lambda ARN and scheduler role for the new schedule
	awsCfg, _ := awspkg.LoadAWSConfig(ctx, "")
	lambdaClient := lambdapkg.NewFromConfig(awsCfg)
	fnOut, fnErr := lambdaClient.GetFunction(ctx, &lambdapkg.GetFunctionInput{
		FunctionName: awssdk.String("km-ttl-handler"),
	})
	if fnErr != nil {
		return fmt.Errorf("discover Lambda ARN: %w", fnErr)
	}
	iamClient := iampkg.NewFromConfig(awsCfg)
	roleOut, roleErr := iamClient.GetRole(ctx, &iampkg.GetRoleInput{
		RoleName: awssdk.String("km-ttl-scheduler"),
	})
	if roleErr != nil {
		return fmt.Errorf("discover scheduler role: %w", roleErr)
	}

	schedInput := compiler.BuildTTLScheduleInput(event.SandboxID, newExpiry,
		awssdk.ToString(fnOut.Configuration.FunctionArn),
		awssdk.ToString(roleOut.Role.Arn))
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
		if schedErr := awspkg.DeleteTTLSchedule(ctx, h.Scheduler, sandboxID); schedErr != nil {
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
		if err := sdkOnlyTeardown(ctx, h, sandboxID); err != nil {
			log.Error().Err(err).Str("sandbox_id", sandboxID).Msg("SDK-only teardown failed")
			return fmt.Errorf("sdk teardown sandbox %s: %w", sandboxID, err)
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
func (h *TTLHandler) lookupTeardownPolicy(ctx context.Context, sandboxID string) string {
	profileBytes, err := downloadProfileFromS3(ctx, h.S3Client, h.Bucket, sandboxID)
	if err != nil {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).
			Msg("could not load profile for teardownPolicy check; defaulting to destroy")
		return "destroy"
	}
	profile, parseErr := profilepkg.Parse(profileBytes)
	if parseErr != nil || profile == nil {
		return "destroy"
	}
	policy := profile.Spec.Lifecycle.TeardownPolicy
	if policy == "" {
		return "destroy"
	}
	return policy
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
// Returns nil on any error — callers should treat metadata as optional enrichment.
func readMetadataBestEffort(ctx context.Context, client awspkg.SandboxMetadataAPI, tableName, sandboxID string) *awspkg.SandboxMetadata {
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

	// State key: tf-km/use1/sandboxes/<sandbox-id>/terraform.tfstate
	stateKey := fmt.Sprintf("%s/%s/sandboxes/%s/terraform.tfstate", statePrefix, regionLabel, sandboxID)

	// Write a minimal main.tf that references the same module and backend.
	// terraform destroy only needs the module source and state — it reads
	// resource addresses from state and destroys them.
	mainTF := fmt.Sprintf(`
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
  region = %q
}

module "sandbox" {
  source       = "./module"
  km_label     = "km"
  region_label = %q
  region_full  = %q
  sandbox_id   = %q
  vpc_id       = "destroy-placeholder"
  public_subnets     = []
  availability_zones = []
  ec2spots           = []
}
`, h.StateBucket, stateKey, region,
		statePrefix+"-locks-"+regionLabel, region,
		regionLabel, region, sandboxID)

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
	// Also clean up budget-enforcer state file from S3.
	if h.StateBucket != "" {
		// Also clean up budget-enforcer state file
		budgetStateKey := fmt.Sprintf("tf-km/sandboxes/%s/budget-enforcer/terraform.tfstate", sandboxID)
		h.S3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: awssdk.String(h.StateBucket),
			Key:    awssdk.String(budgetStateKey),
		})
	}

	// Export CloudWatch logs to S3 then delete the log group.
	// Export is fire-and-forget (async in AWS) and non-fatal — deletion proceeds regardless.
	if h.CWClient != nil {
		if h.Bucket != "" {
			if exportErr := awspkg.ExportSandboxLogs(ctx, h.CWClient, sandboxID, h.Bucket); exportErr != nil {
				log.Warn().Err(exportErr).Str("sandbox_id", sandboxID).Msg("failed to export sandbox logs to S3 (non-fatal)")
			} else {
				log.Info().Str("sandbox_id", sandboxID).Str("bucket", h.Bucket).Msg("sandbox logs export task initiated")
			}
		}
		if cwErr := awspkg.DeleteSandboxLogGroup(ctx, h.CWClient, sandboxID); cwErr != nil {
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
	fnName := "km-budget-enforcer-" + sandboxID
	if _, delErr := lambdaClient.DeleteFunction(ctx, &lambdapkg.DeleteFunctionInput{
		FunctionName: awssdk.String(fnName),
	}); delErr != nil {
		log.Debug().Str("function", fnName).Msg("budget-enforcer Lambda not found or already deleted")
	} else {
		log.Info().Str("function", fnName).Msg("budget-enforcer Lambda deleted")
	}

	// Delete budget-enforcer EventBridge schedule
	schedulerClient := scheduler.NewFromConfig(awsCfg)
	schedName := "km-budget-" + sandboxID
	if _, delErr := schedulerClient.DeleteSchedule(ctx, &scheduler.DeleteScheduleInput{
		Name: awssdk.String(schedName),
	}); delErr != nil {
		var notFound *schedulertypes.ResourceNotFoundException
		if !errors.As(delErr, &notFound) {
			log.Debug().Str("schedule", schedName).Msg("budget schedule not found or already deleted")
		}
	} else {
		log.Info().Str("schedule", schedName).Msg("budget-enforcer schedule deleted")
	}

	// Delete budget-enforcer IAM roles.
	iamClient := iampkg.NewFromConfig(awsCfg)
	for _, roleName := range []string{
		"km-budget-enforcer-" + sandboxID,
		"km-budget-scheduler-" + sandboxID,
	} {
		deleteIAMRole(ctx, iamClient, roleName)
	}

	// Delete budget-enforcer log group
	if h.CWClient != nil {
		logGroup := "/aws/lambda/km-budget-enforcer-" + sandboxID
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
	scheduleName := "km-github-token-" + sandboxID
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
	lambdaName := "km-github-token-refresher-" + sandboxID
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
		logGroup := "/aws/lambda/km-github-token-refresher-" + sandboxID
		h.CWClient.DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{
			LogGroupName: awssdk.String(logGroup),
		})
	}

	// 4. Delete IAM roles (refresher + scheduler).
	for _, roleName := range []string{
		"km-github-token-refresher-" + sandboxID,
		"km-github-token-scheduler-" + sandboxID,
	} {
		deleteIAMRole(ctx, iamClient, roleName)
	}

	// 5. Schedule KMS key deletion and remove alias.
	kmsAlias := "alias/km-github-token-" + sandboxID
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
			// Also delete the instance profile if it's sandbox-specific.
			if strings.Contains(awssdk.ToString(ip.InstanceProfileName), "km-") {
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
	ssmRoleName := "km-ec2spot-ssm-" + sandboxID + "-" + h.RegionLabel
	deleteIAMRole(ctx, iamClient, ssmRoleName)

	// Also clean instance profile directly (may have same name pattern).
	ipName := "km-ec2spot-profile-" + sandboxID + "-" + h.RegionLabel
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

	// 6. Export and delete CloudWatch logs.
	if h.CWClient != nil {
		if h.Bucket != "" {
			awspkg.ExportSandboxLogs(ctx, h.CWClient, sandboxID, h.Bucket)
		}
		awspkg.DeleteSandboxLogGroup(ctx, h.CWClient, sandboxID)
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
	domain := os.Getenv("KM_EMAIL_DOMAIN")
	if domain == "" {
		domain = "sandboxes.klankermaker.ai"
	}

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	sandboxTableName := os.Getenv("SANDBOX_TABLE_NAME")
	if sandboxTableName == "" {
		sandboxTableName = "km-sandboxes"
	}

	s3Client := s3.NewFromConfig(awsCfg)
	dynamoClient := dynamodbpkg.NewFromConfig(awsCfg)

	h := &TTLHandler{
		S3Client:         s3Client,
		DynamoClient:     dynamoClient,
		SandboxTableName: sandboxTableName,
		SESClient:        sesv2.NewFromConfig(awsCfg),
		Scheduler:        scheduler.NewFromConfig(awsCfg),
		CWClient:         cloudwatchlogs.NewFromConfig(awsCfg),
		Bucket:           bucket,
		StateBucket:      os.Getenv("KM_STATE_BUCKET"),
		StatePrefix:      os.Getenv("KM_STATE_PREFIX"),
		Region:           region,
		RegionLabel:      os.Getenv("KM_REGION_LABEL"),
		OperatorEmail:    os.Getenv("KM_OPERATOR_EMAIL"),
		Domain:           domain,
		TeardownFunc:     nil, // set below
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
