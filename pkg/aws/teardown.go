package aws

import (
	"context"
	"errors"
	"strings"

	"github.com/rs/zerolog/log"
)

// DestroySandboxResources discovers all AWS resources tagged with
// km:sandbox-id=<sandboxID> and terminates EC2 instances via the AWS SDK.
//
// This is the Lambda-specific teardown path used after TTL expiry or idle timeout.
// It does NOT run terragrunt destroy (the Lambda runtime has no km binary).
// Terraform state is left stale but resources are reclaimed; a future enhancement
// can add state cleanup.
//
// Idempotent: returns nil when the sandbox is already destroyed (ErrSandboxNotFound).
// ECS tasks (ARNs containing :task/) are logged as warnings and skipped in v1.
func DestroySandboxResources(ctx context.Context, tagClient TagAPI, ec2Client EC2API, sandboxID string) error {
	loc, err := FindSandboxByID(ctx, tagClient, sandboxID)
	if err != nil {
		if errors.Is(err, ErrSandboxNotFound) {
			log.Info().Str("sandbox_id", sandboxID).Msg("sandbox already destroyed or not found; skipping teardown")
			return nil
		}
		return err
	}

	for _, arn := range loc.ResourceARNs {
		switch {
		case strings.Contains(arn, ":instance/"):
			// Extract instance ID: arn:aws:ec2:region:acct:instance/i-0abc...
			parts := strings.Split(arn, "/")
			instanceID := parts[len(parts)-1]
			log.Info().Str("sandbox_id", sandboxID).Str("instance_id", instanceID).
				Msg("terminating EC2 instance for sandbox teardown")
			if termErr := TerminateSpotInstance(ctx, ec2Client, instanceID); termErr != nil {
				return termErr
			}

		case strings.Contains(arn, ":task/"):
			// ECS task teardown deferred to Phase 12 — log a warning and skip.
			log.Warn().Str("sandbox_id", sandboxID).Str("arn", arn).
				Msg("ECS task teardown via Lambda not yet implemented; skipping (Phase 12)")

		default:
			log.Debug().Str("sandbox_id", sandboxID).Str("arn", arn).
				Msg("unrecognized resource type for Lambda teardown; skipping")
		}
	}

	return nil
}
