package cmd

import (
	"context"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	appcfg "github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// launchAccountEC2Cache hands out an EC2 client scoped to the account a sandbox
// actually lives in.
//
// `km list` and `km status` describe instances by the km:sandbox-id tag using the
// HOME account's credentials. For a cross-account sandbox that describe returns
// nothing, and reconcileStatusFromInstances then downgrades the row to "killed" —
// so a RUNNING GPU box shows up dead in the two commands an operator looks at
// most. Observed live on sb-58e176df: instance running in the linked account, SSM
// agent Online, `km list` reporting ☠️ kill.
//
// That is the expensive direction to be wrong in — every other cross-account
// failure in this phase announced itself loudly, this one hides a $10/hr instance.
//
// Clients are built lazily and cached per link name, so a list of N linked
// sandboxes costs one SSM read and one AssumeRole per LINK, not per row. A link
// that fails to resolve is cached as a nil client and retried no further: the
// caller falls back to the home client, which reproduces the pre-existing
// behaviour rather than failing the whole listing.
type launchAccountEC2Cache struct {
	cfg     *appcfg.Config
	baseCfg awssdk.Config
	clients map[string]*ec2.Client
}

func newLaunchAccountEC2Cache(cfg *appcfg.Config, baseCfg awssdk.Config) *launchAccountEC2Cache {
	return &launchAccountEC2Cache{
		cfg:     cfg,
		baseCfg: baseCfg,
		clients: make(map[string]*ec2.Client),
	}
}

// clientFor returns an EC2 client for the named launch-account link, or nil when
// the name is empty (a home-account sandbox), the link is unknown, or the
// assume-role fails. nil means "use the caller's home client".
func (c *launchAccountEC2Cache) clientFor(ctx context.Context, linkName string) *ec2.Client {
	if c == nil || linkName == "" || c.cfg == nil {
		return nil
	}
	if client, ok := c.clients[linkName]; ok {
		return client // may be a cached nil — a link that already failed to resolve
	}

	client := c.build(ctx, linkName)
	c.clients[linkName] = client
	return client
}

func (c *launchAccountEC2Cache) build(ctx context.Context, linkName string) *ec2.Client {
	link, ok := c.cfg.GetLaunchAccount(linkName)
	if !ok {
		return nil
	}
	assumed, paramErr, assumeErr := resolveLaunchAccountAssumedConfig(
		ctx,
		link,
		ssm.NewFromConfig(c.baseCfg),
		func(innerCtx context.Context, roleARN, externalID, region string) (awssdk.Config, error) {
			return kmaws.AssumeRoleConfig(innerCtx, c.baseCfg, roleARN, externalID, region)
		},
	)
	if paramErr != nil || assumeErr != nil {
		// Best-effort by design: a listing must not fail because one link is
		// unreachable. `km doctor`'s Launch Account Assumable check is the place
		// that reports an unhealthy link loudly.
		return nil
	}
	return ec2.NewFromConfig(assumed)
}
