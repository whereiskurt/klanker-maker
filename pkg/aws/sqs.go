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
func CreateSlackInboundQueue(ctx context.Context, c SQSClient, queueName string) (string, error) {
	out, err := c.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: awssdk.String(queueName),
		Attributes: map[string]string{
			string(sqstypes.QueueAttributeNameFifoQueue):                 "true",
			string(sqstypes.QueueAttributeNameContentBasedDeduplication): "false",
			string(sqstypes.QueueAttributeNameVisibilityTimeout):         "30",
			string(sqstypes.QueueAttributeNameMessageRetentionPeriod):    "1209600",
		},
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
