package cmd

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// RemoteCommandPublisher abstracts the EventBridge publish call for remote
// sandbox commands (destroy, extend, stop). It enables unit testing of the
// --remote path without real AWS credentials.
type RemoteCommandPublisher interface {
	PublishSandboxCommand(ctx context.Context, sandboxID, eventType string, extra ...string) error
}

// realRemotePublisher is the production implementation that loads AWS config and
// publishes to the real EventBridge.
type realRemotePublisher struct {
	cfg *config.Config
}

// newRealRemotePublisher creates a realRemotePublisher for the given config.
func newRealRemotePublisher(cfg *config.Config) *realRemotePublisher {
	return &realRemotePublisher{cfg: cfg}
}

// PublishSandboxCommand loads AWS config and publishes the sandbox command event.
func (p *realRemotePublisher) PublishSandboxCommand(ctx context.Context, sandboxID, eventType string, extra ...string) error {
	awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	ebClient := eventbridge.NewFromConfig(awsCfg)
	fmt.Printf("Sending remote %s for %s...\n", eventType, sandboxID)
	if err := awspkg.PublishSandboxCommand(ctx, ebClient, sandboxID, eventType, extra...); err != nil {
		return fmt.Errorf("publish %s event: %w", eventType, err)
	}
	fmt.Printf("Event published. The Lambda will %s %s in the background.\n", eventType, sandboxID)
	region := p.cfg.PrimaryRegion
	if region == "" {
		region = "us-east-1"
	}
	// The function name is prefix-scoped. Hardcoding "km-" here printed a hint at
	// a log group that does not exist on any install whose resource_prefix is not
	// "km" — following it yields ResourceNotFoundException, which is a large part
	// of why `km logs` has a reputation for never working.
	fmt.Printf("Monitor: aws logs tail /aws/lambda/%s-ttl-handler --follow --profile klanker-terraform --region %s\n",
		p.cfg.GetResourcePrefix(), region)
	return nil
}
