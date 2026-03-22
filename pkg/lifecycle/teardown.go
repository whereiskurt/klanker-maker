package lifecycle

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
)

// TeardownCallbacks holds the functions that implement each teardown policy action.
type TeardownCallbacks struct {
	// Destroy fully deprovisions the sandbox (terragrunt destroy + schedule cancel).
	Destroy func(ctx context.Context, sandboxID string) error

	// Stop halts the sandbox compute (EC2 StopInstances or ECS StopTask) without
	// destroying persistent state.
	Stop func(ctx context.Context, sandboxID string) error

	// UploadArtifacts uploads configured artifact paths to S3 before destroy/stop.
	// If nil, the upload step is skipped.
	// Failure is best-effort: a warning is logged but teardown proceeds regardless.
	UploadArtifacts func(ctx context.Context, sandboxID string) error

	// OnNotify sends a lifecycle notification after teardown completes or on error.
	// Called with (ctx, sandboxID, event) where event is "destroyed", "stopped",
	// "retained", or "error".
	// If nil, no notification is sent (backward compatible with existing callers).
	// Failure is best-effort: logged as warning, does not affect teardown result.
	OnNotify func(ctx context.Context, sandboxID string, event string) error
}

// ExecuteTeardown dispatches to the appropriate teardown action based on policy.
//
// Supported policies:
//   - "destroy" (default): calls callbacks.Destroy
//   - "stop": calls callbacks.Stop
//   - "retain": logs intent at info level; does nothing (operator must act manually)
//   - anything else: returns an error
//
// UploadArtifacts (if set) is called before the policy dispatch for ALL policies,
// including "retain". Failure is best-effort: logged as a warning, teardown continues.
//
// OnNotify (if set) is called after policy dispatch with the event name:
//   - "destroyed" on successful destroy
//   - "stopped" on successful stop
//   - "retained" on retain policy
//   - "error" when Destroy or Stop returns an error
//
// OnNotify failure is best-effort: logged as a warning, does not affect return value.
func ExecuteTeardown(ctx context.Context, policy string, sandboxID string, callbacks TeardownCallbacks) error {
	// Upload artifacts before any teardown action (best-effort for all policies).
	if callbacks.UploadArtifacts != nil {
		if err := callbacks.UploadArtifacts(ctx, sandboxID); err != nil {
			log.Warn().Err(err).Str("sandbox_id", sandboxID).Msg("artifact upload failed (best-effort); continuing teardown")
		}
	}

	// notify is a helper to call OnNotify if set; logs warning on failure.
	notify := func(event string) {
		if callbacks.OnNotify == nil {
			return
		}
		if err := callbacks.OnNotify(ctx, sandboxID, event); err != nil {
			log.Warn().Err(err).Str("sandbox_id", sandboxID).Str("event", event).
				Msg("lifecycle notification failed (best-effort)")
		}
	}

	switch policy {
	case "destroy":
		if callbacks.Destroy == nil {
			return fmt.Errorf("teardown policy=destroy: Destroy callback is nil")
		}
		if err := callbacks.Destroy(ctx, sandboxID); err != nil {
			notify("error")
			return err
		}
		notify("destroyed")
		return nil

	case "stop":
		if callbacks.Stop == nil {
			return fmt.Errorf("teardown policy=stop: Stop callback is nil")
		}
		if err := callbacks.Stop(ctx, sandboxID); err != nil {
			notify("error")
			return err
		}
		notify("stopped")
		return nil

	case "retain":
		log.Info().
			Str("sandbox_id", sandboxID).
			Str("policy", "retain").
			Msg("teardown policy=retain; operator action required")
		notify("retained")
		return nil

	default:
		return fmt.Errorf("unknown teardown policy: %s", policy)
	}
}
