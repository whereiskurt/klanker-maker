package cmd

// destroy_webhook_inbound.go — Phase 127
// km destroy drain sequence for webhook-inbound sandboxes.
//
// Sequence (mirrors drainGitHubInbound in destroy_github_inbound.go):
//  1. Delete SQS queue (drops unprocessed webhook events).
//  2. Delete SSM parameter holding the queue URL.
//
// Each step is best-effort: failures are logged but do NOT block km destroy.
// The caller in destroy.go MUST call drainWebhookInbound BEFORE any final
// status update so cleanup is attempted even when Terraform fails.

import (
	"context"
	"fmt"
	"os"

	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// webhookDestroyInboundDeps bundles the pieces needed by drainWebhookInbound.
// All fields are optional: nil pointers / empty strings cause the corresponding
// step to be skipped.
type webhookDestroyInboundDeps struct {
	SandboxID      string
	ResourcePrefix string // km-config.yaml resource_prefix for SSM key scoping
	// QueueURL is the SQS FIFO queue URL. Empty → drain is a no-op.
	QueueURL string

	// SQS client for queue deletion (required when QueueURL is non-empty).
	SQS awspkg.SQSClient
	// DeleteSSMParameter removes the SSM Parameter Store entry that holds the
	// inbound queue URL. nil skips this step.
	DeleteSSMParameter func(ctx context.Context, name string) error
}

// drainWebhookInbound is the orchestrator for km destroy's webhook-inbound path.
// Both steps are best-effort: failures are logged but do not block km destroy.
func drainWebhookInbound(ctx context.Context, deps webhookDestroyInboundDeps) {
	if deps.QueueURL == "" {
		// Sandbox has no webhook inbound queue — no-op.
		return
	}
	fmt.Fprintf(os.Stderr, "  Webhook inbound drain: starting (queue=%s)\n", deps.QueueURL)

	// Step 1: delete the SQS queue.
	//
	// This deletes ONLY the per-sandbox source queue (deps.QueueURL). The shared
	// per-install webhook-inbound DLQ ({prefix}-webhook-inbound-dlq.fifo) is
	// install-scoped and is NEVER deleted by km destroy (km uninit owns the
	// shared DLQ's lifecycle). Do not add a DLQ delete here — sibling sandboxes
	// still redrive into it.
	if deps.SQS != nil {
		if err := awspkg.DeleteWebhookInboundQueue(ctx, deps.SQS, deps.QueueURL); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ Webhook inbound drain: queue delete failed: %v (continuing)\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "  ✓ Webhook inbound drain: queue deleted\n")
		}
	}

	// Step 2: delete the SSM Parameter Store entry holding the queue URL.
	if deps.DeleteSSMParameter != nil && deps.SandboxID != "" {
		paramName := awspkg.SandboxParameterPath(deps.ResourcePrefix, deps.SandboxID, "webhook-inbound-queue-url")
		if err := deps.DeleteSSMParameter(ctx, paramName); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ Webhook inbound drain: SSM parameter delete failed: %v (continuing)\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "  ✓ Webhook inbound drain: SSM parameter deleted\n")
		}
	}
}
