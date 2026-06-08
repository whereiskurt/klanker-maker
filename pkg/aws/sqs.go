// Package aws — sqs.go
// SQS helpers for per-sandbox FIFO queue lifecycle (Phase 67 inbound dispatch).
//
// These helpers are called at km create / km destroy time to provision and
// tear down the per-sandbox Slack-inbound queue. They are intentionally thin
// wrappers around the AWS SDK so the orchestration layer in
// internal/app/cmd/create_slack_inbound.go can inject mocks in tests.
package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// SQSClient is the subset of *sqs.Client used by km create/destroy.
// Extracted as an interface to allow mocking in unit tests without a real AWS
// connection.
type SQSClient interface {
	CreateQueue(ctx context.Context, in *sqs.CreateQueueInput, opts ...func(*sqs.Options)) (*sqs.CreateQueueOutput, error)
	DeleteQueue(ctx context.Context, in *sqs.DeleteQueueInput, opts ...func(*sqs.Options)) (*sqs.DeleteQueueOutput, error)
	GetQueueAttributes(ctx context.Context, in *sqs.GetQueueAttributesInput, opts ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error)
	ListQueues(ctx context.Context, in *sqs.ListQueuesInput, opts ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error)
}

// NewSQSClient returns a default sqs.Client bound to the given region.
func NewSQSClient(ctx context.Context, region string) (*sqs.Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("aws config for SQS (region=%s): %w", region, err)
	}
	return sqs.NewFromConfig(cfg), nil
}

// dlqMaxReceiveCount is the number of times SQS will deliver a message before
// moving it to the dead-letter queue. Phase 99.1: cures the head-of-line-blocking
// FIFO poison-message wedge — after 3 failed receives the poison envelope is
// auto-evicted to the shared DLQ so the message group unblocks.
const dlqMaxReceiveCount = 3

// SlackInboundDLQName returns the shared (per-install, not per-sandbox) Slack
// inbound dead-letter queue name. Format: {resource_prefix}-slack-inbound-dlq.fifo
func SlackInboundDLQName(resourcePrefix string) string {
	return fmt.Sprintf("%s-slack-inbound-dlq.fifo", resourcePrefix)
}

// GitHubInboundDLQName returns the shared (per-install, not per-sandbox) GitHub
// inbound dead-letter queue name. Format: {resource_prefix}-github-inbound-dlq.fifo
func GitHubInboundDLQName(resourcePrefix string) string {
	return fmt.Sprintf("%s-github-inbound-dlq.fifo", resourcePrefix)
}

// DLQArn deterministically derives a queue ARN from region, account ID, and
// queue name — no SQS API call required. Format:
// arn:aws:sqs:{region}:{accountID}:{queueName}
func DLQArn(region, accountID, dlqName string) string {
	return fmt.Sprintf("arn:aws:sqs:%s:%s:%s", region, accountID, dlqName)
}

// GetQueueARN resolves a queue's ARN via a single GetQueueAttributes call for
// the QueueArn attribute. Used when the account ID is not known a priori (the
// deterministic DLQArn helper is preferred when it is).
func GetQueueARN(ctx context.Context, c SQSClient, queueURL string) (string, error) {
	out, err := c.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: awssdk.String(queueURL),
		AttributeNames: []sqstypes.QueueAttributeName{
			sqstypes.QueueAttributeNameQueueArn,
		},
	})
	if err != nil {
		return "", fmt.Errorf("get queue ARN %s: %w", queueURL, err)
	}
	if v, ok := out.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]; ok && v != "" {
		return v, nil
	}
	return "", fmt.Errorf("get queue ARN %s: QueueArn attribute missing", queueURL)
}

// redrivePolicyJSON marshals the SQS RedrivePolicy attribute value. The SQS API
// requires this attribute to be a JSON-encoded STRING (Phase 99.1 RESEARCH
// Pitfall 2), not a nested object.
func redrivePolicyJSON(dlqARN string) (string, error) {
	b, err := json.Marshal(map[string]any{
		"deadLetterTargetArn": dlqARN,
		"maxReceiveCount":     dlqMaxReceiveCount,
	})
	if err != nil {
		return "", fmt.Errorf("marshal RedrivePolicy: %w", err)
	}
	return string(b), nil
}

// inboundQueueAttrs builds the locked 4-attribute map shared by the Slack and
// GitHub inbound FIFO queues. When dlqARN is non-empty it appends a RedrivePolicy
// attribute pointing at the shared DLQ; when empty the map is byte-identical to
// the pre-Phase-99.1 4-attr map (dormancy invariant).
func inboundQueueAttrs(dlqARN string) (map[string]string, error) {
	attrs := map[string]string{
		string(sqstypes.QueueAttributeNameFifoQueue):                 "true",
		string(sqstypes.QueueAttributeNameContentBasedDeduplication): "false",
		string(sqstypes.QueueAttributeNameVisibilityTimeout):         "30",
		string(sqstypes.QueueAttributeNameMessageRetentionPeriod):    "1209600",
	}
	if dlqARN != "" {
		rp, err := redrivePolicyJSON(dlqARN)
		if err != nil {
			return nil, err
		}
		attrs["RedrivePolicy"] = rp
	}
	return attrs, nil
}

// CreateSharedInboundDLQ creates a shared FIFO dead-letter queue and returns its
// URL. The DLQ is FIFO (FifoQueue=true, ContentBasedDeduplication=false) to match
// the source FIFO queues (RESEARCH Pitfall 1 — a non-FIFO DLQ cannot be attached
// to a FIFO source). 14-day retention mirrors the source queues so operators have
// time to inspect poison messages. Idempotent: a QueueNameExists race resolves to
// the existing URL via ListQueues suffix-match (same pattern as the per-sandbox
// Create*InboundQueue helpers).
func CreateSharedInboundDLQ(ctx context.Context, c SQSClient, dlqName string) (string, error) {
	out, err := c.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: awssdk.String(dlqName),
		Attributes: map[string]string{
			string(sqstypes.QueueAttributeNameFifoQueue):                 "true",
			string(sqstypes.QueueAttributeNameContentBasedDeduplication): "false",
			string(sqstypes.QueueAttributeNameMessageRetentionPeriod):    "1209600",
		},
	})
	if err != nil {
		var existsErr *sqstypes.QueueNameExists
		if errors.As(err, &existsErr) {
			list, lerr := c.ListQueues(ctx, &sqs.ListQueuesInput{
				QueueNamePrefix: awssdk.String(dlqName),
			})
			if lerr == nil {
				for _, u := range list.QueueUrls {
					if strings.HasSuffix(u, "/"+dlqName) {
						return u, nil
					}
				}
			}
			return "", fmt.Errorf("create DLQ %s: exists but URL lookup failed: %w", dlqName, lerr)
		}
		return "", fmt.Errorf("create DLQ %s: %w", dlqName, err)
	}
	return awssdk.ToString(out.QueueUrl), nil
}

// SlackInboundQueueName returns the FIFO queue name for a sandbox.
// Phase 66 prefix-aware: callers pass cfg.GetResourcePrefix().
// Format: {resource_prefix}-slack-inbound-{sandbox-id}.fifo
func SlackInboundQueueName(resourcePrefix, sandboxID string) string {
	return fmt.Sprintf("%s-slack-inbound-%s.fifo", resourcePrefix, sandboxID)
}

// CreateSlackInboundQueue creates a per-sandbox FIFO queue with the locked
// attributes from CONTEXT.md:
//   - FifoQueue=true (strict ordering per conversation turn)
//   - ContentBasedDeduplication=false (explicit dedup via Slack event_id)
//   - VisibilityTimeout=30s (failed turns re-queue after 30s for natural retry)
//   - MessageRetentionPeriod=1209600 (14 days — survives km pause/resume cycles)
//
// Returns the queue URL on success. Idempotent: if a queue with the same name
// already exists with compatible attributes, returns its URL without error.
//
// Phase 99.1: when dlqARN is non-empty, a RedrivePolicy attribute is attached so
// poison messages auto-evict to the shared DLQ after dlqMaxReceiveCount receives.
// When dlqARN is empty the attribute map is byte-identical to pre-99.1 (dormancy).
func CreateSlackInboundQueue(ctx context.Context, c SQSClient, queueName, dlqARN string) (string, error) {
	attrs, err := inboundQueueAttrs(dlqARN)
	if err != nil {
		return "", fmt.Errorf("create queue %s: %w", queueName, err)
	}
	out, err := c.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  awssdk.String(queueName),
		Attributes: attrs,
	})
	if err != nil {
		// Idempotent reconciliation: QueueNameExists is a benign race — another
		// km create or retry already made it.
		var existsErr *sqstypes.QueueNameExists
		if errors.As(err, &existsErr) {
			// Look up its URL via list-queues prefix match.
			list, lerr := c.ListQueues(ctx, &sqs.ListQueuesInput{
				QueueNamePrefix: awssdk.String(queueName),
			})
			if lerr == nil {
				for _, u := range list.QueueUrls {
					if strings.HasSuffix(u, "/"+queueName) {
						return u, nil
					}
				}
			}
			return "", fmt.Errorf("create queue %s: exists but URL lookup failed: %w", queueName, lerr)
		}
		return "", fmt.Errorf("create queue %s: %w", queueName, err)
	}
	return awssdk.ToString(out.QueueUrl), nil
}

// DeleteSlackInboundQueue is best-effort — returns nil if the queue is already
// gone. Used by km destroy and rollback paths in km create.
func DeleteSlackInboundQueue(ctx context.Context, c SQSClient, queueURL string) error {
	if queueURL == "" {
		return nil
	}
	_, err := c.DeleteQueue(ctx, &sqs.DeleteQueueInput{
		QueueUrl: awssdk.String(queueURL),
	})
	if err != nil {
		var notFound *sqstypes.QueueDoesNotExist
		if errors.As(err, &notFound) {
			return nil // already gone — treat as success
		}
		return fmt.Errorf("delete queue %s: %w", queueURL, err)
	}
	return nil
}

// GitHubInboundQueueName returns the FIFO queue name for a sandbox's GitHub inbound queue.
// Phase 97: owned by pkg/aws so both the km CLI (plan 03) and the create-handler
// Lambda (plan 02) can reference it without an intra-wave ordering dependency.
// Format: {resource_prefix}-github-inbound-{sandbox-id}.fifo
func GitHubInboundQueueName(resourcePrefix, sandboxID string) string {
	return fmt.Sprintf("%s-github-inbound-%s.fifo", resourcePrefix, sandboxID)
}

// CreateGitHubInboundQueue creates a per-sandbox GitHub inbound FIFO queue.
// Attributes mirror CreateSlackInboundQueue exactly (FIFO, ContentBasedDeduplication=false,
// visibility=30s, retention=1209600/14d). Returns the queue URL on success.
// Idempotent: QueueNameExists is treated as success via URL lookup.
//
// Phase 99.1: when dlqARN is non-empty, a RedrivePolicy attribute is attached so
// poison messages auto-evict to the shared DLQ after dlqMaxReceiveCount receives.
// When dlqARN is empty the attribute map is byte-identical to pre-99.1 (dormancy).
func CreateGitHubInboundQueue(ctx context.Context, c SQSClient, queueName, dlqARN string) (string, error) {
	attrs, err := inboundQueueAttrs(dlqARN)
	if err != nil {
		return "", fmt.Errorf("create github queue %s: %w", queueName, err)
	}
	out, err := c.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  awssdk.String(queueName),
		Attributes: attrs,
	})
	if err != nil {
		var existsErr *sqstypes.QueueNameExists
		if errors.As(err, &existsErr) {
			list, lerr := c.ListQueues(ctx, &sqs.ListQueuesInput{
				QueueNamePrefix: awssdk.String(queueName),
			})
			if lerr == nil {
				for _, u := range list.QueueUrls {
					if strings.HasSuffix(u, "/"+queueName) {
						return u, nil
					}
				}
			}
			return "", fmt.Errorf("create github queue %s: exists but URL lookup failed: %w", queueName, lerr)
		}
		return "", fmt.Errorf("create github queue %s: %w", queueName, err)
	}
	return awssdk.ToString(out.QueueUrl), nil
}

// DeleteGitHubInboundQueue is best-effort — returns nil if the queue is already
// gone. Used by km destroy and rollback paths in km create.
func DeleteGitHubInboundQueue(ctx context.Context, c SQSClient, queueURL string) error {
	if queueURL == "" {
		return nil
	}
	_, err := c.DeleteQueue(ctx, &sqs.DeleteQueueInput{
		QueueUrl: awssdk.String(queueURL),
	})
	if err != nil {
		var notFound *sqstypes.QueueDoesNotExist
		if errors.As(err, &notFound) {
			return nil // already gone — treat as success
		}
		return fmt.Errorf("delete github queue %s: %w", queueURL, err)
	}
	return nil
}

// QueueDepth returns the ApproximateNumberOfMessages for a queue.
// Used by km status / km doctor to surface queue backlog.
func QueueDepth(ctx context.Context, c SQSClient, queueURL string) (int64, error) {
	out, err := c.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: awssdk.String(queueURL),
		AttributeNames: []sqstypes.QueueAttributeName{
			sqstypes.QueueAttributeNameApproximateNumberOfMessages,
		},
	})
	if err != nil {
		return 0, fmt.Errorf("get queue attrs %s: %w", queueURL, err)
	}
	if v, ok := out.Attributes[string(sqstypes.QueueAttributeNameApproximateNumberOfMessages)]; ok {
		var n int64
		fmt.Sscanf(v, "%d", &n)
		return n, nil
	}
	return 0, nil
}
