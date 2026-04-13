// Package main implements the km budget-enforcer Lambda.
//
// The Lambda runs every minute via EventBridge Scheduler for each active sandbox.
// It:
//  1. Calculates elapsed compute cost = spotRate * (elapsedMinutes / 60).
//  2. Writes the compute spend to DynamoDB (SET — idempotent, recalculated each invocation).
//  3. Reads the full BudgetSummary from DynamoDB.
//  4. Enforces at 80%: sends a one-shot warning email (guarded by warningNotified attribute).
//  5. Enforces at 100% compute: EC2 → StopInstances, ECS → StopTask. Sends enforcement email.
//  6. Enforces at 100% AI (backstop): detaches Bedrock IAM policy from sandbox role. Sends email.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/rs/zerolog/log"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// ============================================================
// Event payload
// ============================================================

// BudgetCheckEvent is the EventBridge Scheduler payload delivered to this Lambda.
// It is set at sandbox creation time with instance metadata for cost calculation.
type BudgetCheckEvent struct {
	SandboxID     string  `json:"sandbox_id"`
	InstanceType  string  `json:"instance_type"`   // for reference/logging
	SpotRate      float64 `json:"spot_rate"`       // pre-calculated hourly rate (set at creation)
	Substrate     string  `json:"substrate"`       // "ec2" or "ecs"
	CreatedAt     string  `json:"created_at"`      // RFC3339 timestamp
	RoleARN       string  `json:"role_arn"`        // IAM role to revoke Bedrock from
	InstanceID    string  `json:"instance_id"`     // EC2 instance ID (empty for ECS)
	TaskARN       string  `json:"task_arn"`        // ECS task ARN (empty for EC2)
	ClusterARN    string  `json:"cluster_arn"`     // ECS cluster ARN (empty for EC2)
	OperatorEmail string  `json:"operator_email"`  // for notifications
}

// ============================================================
// Narrow interfaces for testability
// ============================================================

// EC2StopAPI is the narrow EC2 interface needed to stop an instance.
type EC2StopAPI interface {
	StopInstances(ctx context.Context, input *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error)
}

// IAMDetachAPI is the narrow IAM interface needed to detach a policy from a role.
type IAMDetachAPI interface {
	DetachRolePolicy(ctx context.Context, input *iam.DetachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.DetachRolePolicyOutput, error)
}

// ECSStopAPI is the narrow ECS interface needed to stop a task.
type ECSStopAPI interface {
	StopTask(ctx context.Context, input *ecs.StopTaskInput, optFns ...func(*ecs.Options)) (*ecs.StopTaskOutput, error)
}

// ============================================================
// Bedrock policy ARN for IAM revocation
// ============================================================

// bedrockFullAccessPolicyARN is the AWS-managed policy granting Bedrock access.
// Detaching this from the sandbox role is the backstop when AI budget is exhausted.
const bedrockFullAccessPolicyARN = "arn:aws:iam::aws:policy/AmazonBedrockFullAccess"

// ============================================================
// BudgetHandler
// ============================================================

// BudgetHandler holds injected dependencies for testability.
type BudgetHandler struct {
	DynamoDB       awspkg.BudgetAPI
	SandboxDynamo  awspkg.SandboxMetadataAPI // for lock check and status update
	SandboxTable   string                    // DynamoDB table name (default: "km-sandboxes")
	S3Client       *s3.Client                // for profile download
	SchedulerClient awspkg.SchedulerAPI      // for TTL schedule cleanup
	EC2Client      EC2StopAPI
	ECSClient      ECSStopAPI
	IAMClient      IAMDetachAPI
	SESClient      awspkg.SESV2API
	BudgetTable    string
	StateBucket    string
	EmailDomain    string

	// Injectable functions for testing. When nil, the real implementations are used.
	// getBudgetFn allows tests to inject a pre-configured BudgetSummary without DynamoDB.
	getBudgetFn func(ctx context.Context, sandboxID string) (*awspkg.BudgetSummary, error)
	// isWarningNotifiedFn and setWarningNotifiedFn allow tests to control the one-shot guard.
	isWarningNotifiedFn  func(ctx context.Context, sandboxID string) (bool, error)
	setWarningNotifiedFn func(ctx context.Context, sandboxID string) error
}

// HandleBudgetCheck is the Lambda handler method.
func (h *BudgetHandler) HandleBudgetCheck(ctx context.Context, event BudgetCheckEvent) error {
	if event.SandboxID == "" {
		return fmt.Errorf("budget-enforcer: sandbox_id is required in event payload")
	}
	sandboxID := event.SandboxID

	log.Info().Str("sandbox_id", sandboxID).Str("substrate", event.Substrate).
		Float64("spot_rate", event.SpotRate).Msg("budget check event received")

	// Step 0: Verify sandbox still exists. If the sandbox was destroyed but the
	// budget schedule survived cleanup (non-fatal delete failure), self-delete
	// the schedule to stop orphaned invocations.
	if h.SandboxDynamo != nil {
		if _, readErr := awspkg.ReadSandboxMetadataDynamo(ctx, h.SandboxDynamo, h.SandboxTable, sandboxID); readErr != nil {
			if errors.Is(readErr, awspkg.ErrSandboxNotFound) {
				log.Warn().Str("sandbox_id", sandboxID).
					Msg("sandbox no longer exists in DynamoDB — deleting orphaned budget schedule")
				h.selfDeleteSchedule(ctx, sandboxID)
				return nil
			}
			// Transient DynamoDB error — proceed with budget check rather than
			// incorrectly self-deleting on a temporary read failure.
			log.Warn().Err(readErr).Str("sandbox_id", sandboxID).
				Msg("could not verify sandbox existence (non-fatal — proceeding with budget check)")
		}
	}

	// Step 1: Calculate elapsed compute cost.
	elapsedCost, err := h.calculateComputeCost(event)
	if err != nil {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).
			Msg("could not calculate compute cost; using 0")
		elapsedCost = 0
	}

	// Step 2: Write compute spend to DynamoDB using SET (idempotent — recalculated each invocation).
	if err := h.setComputeSpend(ctx, sandboxID, elapsedCost); err != nil {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).
			Float64("cost_usd", elapsedCost).Msg("failed to write compute spend to DynamoDB (non-fatal)")
	}

	// Step 3: Read full budget state.
	var budget *awspkg.BudgetSummary
	if h.getBudgetFn != nil {
		budget, err = h.getBudgetFn(ctx, sandboxID)
	} else {
		budget, err = awspkg.GetBudget(ctx, h.DynamoDB, h.BudgetTable, sandboxID)
	}
	if err != nil {
		return fmt.Errorf("budget-enforcer: get budget for sandbox %s: %w", sandboxID, err)
	}

	// Step 4: Determine enforcement actions based on budget thresholds.
	computePct := computePercent(budget.ComputeSpent, budget.ComputeLimit)
	aiPct := computePercent(budget.AISpent, budget.AILimit)

	log.Info().Str("sandbox_id", sandboxID).
		Float64("compute_pct", computePct).
		Float64("ai_pct", aiPct).
		Msg("budget check thresholds evaluated")

	// Step 5a: Warning at 80% (once only).
	threshold := budget.WarningThreshold
	if threshold <= 0 {
		threshold = 0.8 // default
	}
	if (computePct >= threshold || aiPct >= threshold) && computePct < 1.0 && aiPct < 1.0 {
		h.maybeSendWarning(ctx, sandboxID, event.OperatorEmail, computePct, aiPct)
	}

	// Step 5b: 100% compute enforcement.
	if computePct >= 1.0 && budget.ComputeLimit > 0 {
		log.Warn().Str("sandbox_id", sandboxID).
			Float64("compute_spent", budget.ComputeSpent).
			Float64("compute_limit", budget.ComputeLimit).
			Msg("compute budget exhausted — suspending sandbox")

		h.enforceBudgetCompute(ctx, event)

		// Send enforcement email (best-effort).
		if event.OperatorEmail != "" && h.SESClient != nil {
			details := fmt.Sprintf("compute-budget-exhausted: spent=%.4f limit=%.4f", budget.ComputeSpent, budget.ComputeLimit)
			if notifyErr := awspkg.SendLifecycleNotification(ctx, h.SESClient, event.OperatorEmail, sandboxID, details, h.EmailDomain); notifyErr != nil {
				log.Warn().Err(notifyErr).Str("sandbox_id", sandboxID).Msg("failed to send compute enforcement notification (non-fatal)")
			}
		}
	}

	// Step 5c: 100% AI enforcement (Bedrock IAM backstop).
	if aiPct >= 1.0 && budget.AILimit > 0 {
		log.Warn().Str("sandbox_id", sandboxID).
			Float64("ai_spent", budget.AISpent).
			Float64("ai_limit", budget.AILimit).
			Msg("AI budget exhausted — detaching Bedrock IAM policy (backstop)")

		if event.RoleARN != "" && h.IAMClient != nil {
			if detachErr := h.detachBedrockPolicy(ctx, event.RoleARN); detachErr != nil {
				log.Warn().Err(detachErr).Str("sandbox_id", sandboxID).
					Msg("failed to detach Bedrock policy (non-fatal — proxy enforcement still active)")
			}
		}

		// Send AI enforcement email (best-effort).
		if event.OperatorEmail != "" && h.SESClient != nil {
			details := fmt.Sprintf("ai-budget-exhausted: spent=%.4f limit=%.4f", budget.AISpent, budget.AILimit)
			if notifyErr := awspkg.SendLifecycleNotification(ctx, h.SESClient, event.OperatorEmail, sandboxID, details, h.EmailDomain); notifyErr != nil {
				log.Warn().Err(notifyErr).Str("sandbox_id", sandboxID).Msg("failed to send AI enforcement notification (non-fatal)")
			}
		}
	}

	log.Info().Str("sandbox_id", sandboxID).Msg("budget check complete")
	return nil
}

// ============================================================
// Cost calculation
// ============================================================

// calculateComputeCost computes cost = spotRate * (elapsedMinutes / 60).
func (h *BudgetHandler) calculateComputeCost(event BudgetCheckEvent) (float64, error) {
	if event.CreatedAt == "" {
		return 0, fmt.Errorf("created_at is empty")
	}
	createdAt, err := time.Parse(time.RFC3339, event.CreatedAt)
	if err != nil {
		return 0, fmt.Errorf("parse created_at %q: %w", event.CreatedAt, err)
	}
	elapsedMinutes := time.Since(createdAt).Minutes()
	if elapsedMinutes < 0 {
		elapsedMinutes = 0
	}
	cost := event.SpotRate * (elapsedMinutes / 60.0)
	return cost, nil
}

// ============================================================
// DynamoDB helpers
// ============================================================

// setComputeSpend writes the absolute compute cost for this sandbox using SET (not ADD).
// This is idempotent: each Lambda invocation recalculates from scratch and overwrites.
func (h *BudgetHandler) setComputeSpend(ctx context.Context, sandboxID string, costUSD float64) error {
	pk := fmt.Sprintf("SANDBOX#%s", sandboxID)
	_, err := h.DynamoDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(h.BudgetTable),
		Key: map[string]dynamodbtypes.AttributeValue{
			"PK": &dynamodbtypes.AttributeValueMemberS{Value: pk},
			"SK": &dynamodbtypes.AttributeValueMemberS{Value: "BUDGET#compute"},
		},
		UpdateExpression: awssdk.String("SET spentUSD = :cost"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":cost": &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%.6f", costUSD)},
		},
	})
	if err != nil {
		return fmt.Errorf("set compute spend for sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// isWarningNotified checks whether the one-shot warning has already been sent.
func (h *BudgetHandler) isWarningNotified(ctx context.Context, sandboxID string) (bool, error) {
	if h.isWarningNotifiedFn != nil {
		return h.isWarningNotifiedFn(ctx, sandboxID)
	}
	pk := fmt.Sprintf("SANDBOX#%s", sandboxID)
	out, err := h.DynamoDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(h.BudgetTable),
		Key: map[string]dynamodbtypes.AttributeValue{
			"PK": &dynamodbtypes.AttributeValueMemberS{Value: pk},
			"SK": &dynamodbtypes.AttributeValueMemberS{Value: "BUDGET#meta"},
		},
		ProjectionExpression: awssdk.String("warningNotified"),
	})
	if err != nil {
		return false, nil // safe default: allow warning to be sent
	}
	if out.Item == nil {
		return false, nil
	}
	if boolAV, ok := out.Item["warningNotified"]; ok {
		if boolMember, ok := boolAV.(*dynamodbtypes.AttributeValueMemberBOOL); ok {
			return boolMember.Value, nil
		}
	}
	return false, nil
}

// setWarningNotified marks the one-shot warning as sent in DynamoDB.
func (h *BudgetHandler) setWarningNotified(ctx context.Context, sandboxID string) error {
	if h.setWarningNotifiedFn != nil {
		return h.setWarningNotifiedFn(ctx, sandboxID)
	}
	pk := fmt.Sprintf("SANDBOX#%s", sandboxID)
	_, err := h.DynamoDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(h.BudgetTable),
		Key: map[string]dynamodbtypes.AttributeValue{
			"PK": &dynamodbtypes.AttributeValueMemberS{Value: pk},
			"SK": &dynamodbtypes.AttributeValueMemberS{Value: "BUDGET#meta"},
		},
		UpdateExpression: awssdk.String("SET warningNotified = :t"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":t": &dynamodbtypes.AttributeValueMemberBOOL{Value: true},
		},
	})
	return err
}

// ============================================================
// Threshold helpers
// ============================================================

// computePercent returns spend/limit as a fraction (0.0 to 1.0+).
// Returns 0 when limit is 0 to avoid division by zero.
func computePercent(spent, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	return spent / limit
}

// ============================================================
// Enforcement: warning email
// ============================================================

// maybeSendWarning sends a one-shot warning email when threshold is crossed.
func (h *BudgetHandler) maybeSendWarning(ctx context.Context, sandboxID, operatorEmail string, computePct, aiPct float64) {
	if operatorEmail == "" || h.SESClient == nil {
		return
	}
	notified, err := h.isWarningNotified(ctx, sandboxID)
	if err != nil {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).Msg("could not check warning notification state")
	}
	if notified {
		return
	}

	details := fmt.Sprintf("budget-warning: compute=%.0f%% ai=%.0f%%",
		computePct*100, aiPct*100)
	if notifyErr := awspkg.SendLifecycleNotification(ctx, h.SESClient, operatorEmail, sandboxID, details, h.EmailDomain); notifyErr != nil {
		log.Warn().Err(notifyErr).Str("sandbox_id", sandboxID).Msg("failed to send warning email (non-fatal)")
		return
	}

	if err := h.setWarningNotified(ctx, sandboxID); err != nil {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).Msg("failed to set warningNotified flag (non-fatal)")
	}
}

// ============================================================
// Enforcement: compute budget (suspend sandbox)
// ============================================================

// enforceBudgetCompute suspends the sandbox when compute budget is exhausted.
// EC2: checks lock, hibernates if on-demand, updates DynamoDB status, deletes TTL schedule.
// ECS: calls StopTask (artifact upload is best-effort via TTL handler path).
func (h *BudgetHandler) enforceBudgetCompute(ctx context.Context, event BudgetCheckEvent) {
	sandboxID := event.SandboxID
	switch event.Substrate {
	case "ec2":
		if event.InstanceID == "" {
			log.Warn().Str("sandbox_id", sandboxID).Msg("EC2 enforcement: instance_id is empty — cannot stop instance")
			return
		}
		if h.EC2Client == nil {
			log.Warn().Str("sandbox_id", sandboxID).Msg("EC2 enforcement: EC2 client is nil")
			return
		}

		// Check lock — locked sandboxes skip compute enforcement.
		if h.SandboxDynamo != nil {
			meta, readErr := awspkg.ReadSandboxMetadataDynamo(ctx, h.SandboxDynamo, h.SandboxTable, sandboxID)
			if readErr == nil && meta.Locked {
				log.Info().Str("sandbox_id", sandboxID).Msg("sandbox is locked — skipping compute budget enforcement")
				return
			}
		}

		// Check profile for hibernation support.
		hibernate := h.lookupHibernation(ctx, sandboxID)

		stopInput := &ec2.StopInstancesInput{
			InstanceIds: []string{event.InstanceID},
		}
		if hibernate {
			stopInput.Hibernate = awssdk.Bool(true)
			log.Info().Str("sandbox_id", sandboxID).Str("instance_id", event.InstanceID).
				Msg("hibernating instance due to compute budget exhaustion")
		}

		if _, err := h.EC2Client.StopInstances(ctx, stopInput); err != nil {
			// If hibernate fails, fall back to plain stop.
			if hibernate {
				log.Warn().Err(err).Str("instance_id", event.InstanceID).
					Msg("hibernate failed, falling back to stop")
				stopInput.Hibernate = nil
				_, err = h.EC2Client.StopInstances(ctx, stopInput)
				hibernate = false
			}
			if err != nil {
				log.Warn().Err(err).Str("sandbox_id", sandboxID).
					Str("instance_id", event.InstanceID).
					Msg("StopInstances failed (non-fatal — sandbox may already be stopped)")
				return
			}
		}
		log.Info().Str("sandbox_id", sandboxID).Str("instance_id", event.InstanceID).
			Bool("hibernated", hibernate).
			Msg("EC2 instance stopped due to compute budget exhaustion")

		// Update DynamoDB status.
		if h.SandboxDynamo != nil {
			status := "stopped"
			if hibernate {
				status = "paused"
			}
			if statusErr := awspkg.UpdateSandboxStatusDynamo(ctx, h.SandboxDynamo, h.SandboxTable, sandboxID, status); statusErr != nil {
				log.Warn().Err(statusErr).Str("sandbox_id", sandboxID).Msg("failed to update DynamoDB status (non-fatal)")
			}
		}

		// Delete TTL schedule — stopped sandbox shouldn't be destroyed on TTL expiry.
		if h.SchedulerClient != nil {
			if schedErr := awspkg.DeleteTTLSchedule(ctx, h.SchedulerClient, sandboxID); schedErr != nil {
				log.Warn().Err(schedErr).Str("sandbox_id", sandboxID).Msg("failed to delete TTL schedule (non-fatal)")
			}
		}

	case "ecs":
		if event.TaskARN == "" {
			log.Warn().Str("sandbox_id", sandboxID).Msg("ECS enforcement: task_arn is empty — cannot stop task")
			return
		}
		if h.ECSClient == nil {
			log.Warn().Str("sandbox_id", sandboxID).Msg("ECS enforcement: ECS client is nil")
			return
		}
		// TODO: Trigger artifact upload before stopping task (via S3-stored profile).
		// For now, StopTask is called directly; artifact upload is handled by the TTL handler.
		stopInput := &ecs.StopTaskInput{
			Task:   awssdk.String(event.TaskARN),
			Reason: awssdk.String("compute budget exhausted by km budget-enforcer"),
		}
		if event.ClusterARN != "" {
			stopInput.Cluster = awssdk.String(event.ClusterARN)
		}
		if _, err := h.ECSClient.StopTask(ctx, stopInput); err != nil {
			log.Warn().Err(err).Str("sandbox_id", sandboxID).
				Str("task_arn", event.TaskARN).
				Msg("StopTask failed (non-fatal — task may already be stopped)")
		} else {
			log.Info().Str("sandbox_id", sandboxID).Str("task_arn", event.TaskARN).
				Msg("ECS task stopped due to compute budget exhaustion")
		}

	default:
		log.Warn().Str("sandbox_id", sandboxID).Str("substrate", event.Substrate).
			Msg("unknown substrate — cannot enforce compute budget suspension")
	}
}

// ============================================================
// Self-healing: delete orphaned budget schedule
// ============================================================

// selfDeleteSchedule removes this Lambda's own EventBridge schedule.
// Called when the sandbox no longer exists in DynamoDB, meaning the schedule
// survived sandbox destruction (non-fatal delete failure in destroy/TTL path).
func (h *BudgetHandler) selfDeleteSchedule(ctx context.Context, sandboxID string) {
	if h.SchedulerClient == nil {
		log.Warn().Str("sandbox_id", sandboxID).Msg("scheduler client is nil — cannot self-delete budget schedule")
		return
	}
	schedName := "km-budget-" + sandboxID
	if _, err := h.SchedulerClient.DeleteSchedule(ctx, &scheduler.DeleteScheduleInput{
		Name: awssdk.String(schedName),
	}); err != nil {
		log.Warn().Err(err).Str("schedule", schedName).
			Msg("failed to self-delete orphaned budget schedule")
	} else {
		log.Info().Str("schedule", schedName).Str("sandbox_id", sandboxID).
			Msg("orphaned budget schedule self-deleted — sandbox no longer exists")
	}
}

// ============================================================
// Enforcement: AI budget (IAM backstop)
// ============================================================

// detachBedrockPolicy detaches the Bedrock IAM policy from the sandbox role.
// This is a backstop for SDK calls that bypass the HTTP proxy.
func (h *BudgetHandler) detachBedrockPolicy(ctx context.Context, roleARN string) error {
	// Extract role name from ARN: arn:aws:iam::123456789:role/km-sandbox-sb-abc12345
	roleName, err := roleNameFromARN(roleARN)
	if err != nil {
		return fmt.Errorf("extract role name from ARN %q: %w", roleARN, err)
	}

	_, err = h.IAMClient.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
		RoleName:  awssdk.String(roleName),
		PolicyArn: awssdk.String(bedrockFullAccessPolicyARN),
	})
	if err != nil {
		return fmt.Errorf("detach Bedrock policy from role %s: %w", roleName, err)
	}
	log.Info().Str("role_arn", roleARN).Msg("Bedrock IAM policy detached (AI budget backstop enforced)")
	return nil
}

// roleNameFromARN extracts the role name from an IAM role ARN.
// e.g. "arn:aws:iam::123:role/km-sandbox-sb-abc" -> "km-sandbox-sb-abc"
func roleNameFromARN(roleARN string) (string, error) {
	// ARN format: arn:partition:iam::account-id:role/role-name
	for i := len(roleARN) - 1; i >= 0; i-- {
		if roleARN[i] == '/' {
			return roleARN[i+1:], nil
		}
	}
	return "", fmt.Errorf("no slash found in ARN %q", roleARN)
}

// ============================================================
// Lambda entrypoint
// ============================================================

// lookupHibernation downloads the sandbox profile from S3 and returns whether
// hibernation is enabled. Returns false on any error — fail-safe to normal stop.
func (h *BudgetHandler) lookupHibernation(ctx context.Context, sandboxID string) bool {
	if h.S3Client == nil || h.StateBucket == "" {
		return false
	}
	key := fmt.Sprintf("artifacts/%s/.km-profile.yaml", sandboxID)
	obj, err := h.S3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(h.StateBucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		return false
	}
	defer obj.Body.Close()
	data, err := io.ReadAll(obj.Body)
	if err != nil {
		return false
	}
	p, parseErr := profile.Parse(data)
	if parseErr != nil || p == nil {
		return false
	}
	return p.Spec.Runtime.Hibernation
}

func main() {
	ctx := context.Background()
	awsProfile := os.Getenv("KM_AWS_PROFILE") // empty in Lambda — uses execution role

	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load AWS config")
	}

	budgetTable := os.Getenv("KM_BUDGET_TABLE")
	if budgetTable == "" {
		budgetTable = "km-budgets"
	}
	emailDomain := os.Getenv("KM_EMAIL_DOMAIN")
	if emailDomain == "" {
		emailDomain = "sandboxes.klankermaker.ai"
	}

	sandboxTable := os.Getenv("KM_SANDBOX_TABLE")
	if sandboxTable == "" {
		sandboxTable = "km-sandboxes"
	}
	stateBucket := os.Getenv("KM_STATE_BUCKET")

	dynamoClient := dynamodb.NewFromConfig(awsCfg)
	ec2Client := ec2.NewFromConfig(awsCfg)
	ecsClient := ecs.NewFromConfig(awsCfg)
	iamClient := iam.NewFromConfig(awsCfg)
	sesClient := sesv2.NewFromConfig(awsCfg)
	s3Client := s3.NewFromConfig(awsCfg)
	schedulerClient := scheduler.NewFromConfig(awsCfg)

	h := &BudgetHandler{
		DynamoDB:        dynamoClient,
		SandboxDynamo:   dynamoClient,
		SandboxTable:    sandboxTable,
		S3Client:        s3Client,
		SchedulerClient: schedulerClient,
		EC2Client:       ec2Client,
		ECSClient:       ecsClient,
		IAMClient:       iamClient,
		SESClient:       sesClient,
		BudgetTable:     budgetTable,
		StateBucket:     stateBucket,
		EmailDomain:     emailDomain,
	}

	lambda.Start(h.HandleBudgetCheck)
}
