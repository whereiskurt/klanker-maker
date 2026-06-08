package cmd

// destroy_github_inbound.go — Phase 97 Plan 03
// km destroy drain sequence for GitHub-inbound sandboxes.
//
// Sequence (mirrors drainSlackInbound in destroy_slack_inbound.go):
//  1. Delete SQS queue (drops unprocessed GitHub comment events).
//  2. Delete SSM parameter holding the queue URL.
//
// Each step is best-effort: failures are logged but do NOT block km destroy.
// The caller in destroy.go MUST call drainGitHubInbound BEFORE any final
// status update so cleanup is attempted even when Terraform fails.

import (
	"context"
	"fmt"
	"os"

	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// githubDestroyInboundDeps bundles the pieces needed by drainGitHubInbound.
// All fields are optional: nil pointers / empty strings cause the corresponding
// step to be skipped.
type githubDestroyInboundDeps struct {
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

// drainGitHubInbound is the orchestrator for km destroy's GitHub-inbound path.
// Both steps are best-effort: failures are logged but do not block km destroy.
func drainGitHubInbound(ctx context.Context, deps githubDestroyInboundDeps) {
	if deps.QueueURL == "" {
		// Sandbox has no GitHub inbound queue — no-op.
		return
	}
	fmt.Fprintf(os.Stderr, "  GitHub inbound drain: starting (queue=%s)\n", deps.QueueURL)

	// Step 1: delete the SQS queue.
	//
	// Phase 99.1 (GH-DLQ-TEARDOWN): this deletes ONLY the per-sandbox source queue
	// (deps.QueueURL). The shared per-install GitHub-inbound DLQ
	// ({prefix}-github-inbound-dlq.fifo) is install-scoped and is NEVER deleted by
	// km destroy (CONTEXT D5; km uninit owns the shared DLQ's lifecycle). Do not add
	// a DLQ delete here — sibling sandboxes still redrive into it.
	if deps.SQS != nil {
		if err := awspkg.DeleteGitHubInboundQueue(ctx, deps.SQS, deps.QueueURL); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ GitHub inbound drain: queue delete failed: %v (continuing)\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "  ✓ GitHub inbound drain: queue deleted\n")
		}
	}

	// Step 2: delete the SSM Parameter Store entry holding the queue URL.
	if deps.DeleteSSMParameter != nil && deps.SandboxID != "" {
		paramName := awspkg.SandboxParameterPath(deps.ResourcePrefix, deps.SandboxID, "github-inbound-queue-url")
		if err := deps.DeleteSSMParameter(ctx, paramName); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ GitHub inbound drain: SSM parameter delete failed: %v (continuing)\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "  ✓ GitHub inbound drain: SSM parameter deleted\n")
		}
	}
}
