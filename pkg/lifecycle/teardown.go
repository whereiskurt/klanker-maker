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
}

// ExecuteTeardown dispatches to the appropriate teardown action based on policy.
//
// Supported policies:
//   - "destroy" (default): calls callbacks.Destroy
//   - "stop": calls callbacks.Stop
//   - "retain": logs intent at info level; does nothing (operator must act manually)
//   - anything else: returns an error
func ExecuteTeardown(ctx context.Context, policy string, sandboxID string, callbacks TeardownCallbacks) error {
	switch policy {
	case "destroy":
		if callbacks.Destroy == nil {
			return fmt.Errorf("teardown policy=destroy: Destroy callback is nil")
		}
		return callbacks.Destroy(ctx, sandboxID)

	case "stop":
		if callbacks.Stop == nil {
			return fmt.Errorf("teardown policy=stop: Stop callback is nil")
		}
		return callbacks.Stop(ctx, sandboxID)

	case "retain":
		log.Info().
			Str("sandbox_id", sandboxID).
			Str("policy", "retain").
			Msg("teardown policy=retain; operator action required")
		return nil

	default:
		return fmt.Errorf("unknown teardown policy: %s", policy)
	}
}
