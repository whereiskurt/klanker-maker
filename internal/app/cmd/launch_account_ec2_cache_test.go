package cmd

import (
	"context"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"

	appcfg "github.com/whereiskurt/klanker-maker/internal/app/config"
)

// The cache must return nil — meaning "use the caller's home EC2 client" — for
// every shape that is not a resolvable link, and must do so WITHOUT touching AWS.
// A home-account sandbox is the overwhelming majority of rows in `km list`, so a
// stray SSM read or AssumeRole on that path would tax every listing.
func TestLaunchAccountEC2Cache_NilPathsMakeNoAWSCall(t *testing.T) {
	ctx := context.Background()
	cfg := &appcfg.Config{
		LaunchAccounts: map[string]appcfg.LaunchAccountConfig{
			"gpuman": {
				AccountID:       "481723467561",
				LauncherRoleARN: "arn:aws:iam::481723467561:role/km-gpu-launcher",
				ExternalIDSSM:   "/km/launch-accounts/gpuman/external-id",
				Region:          "us-east-1",
			},
		},
	}

	t.Run("empty link name is a home-account sandbox", func(t *testing.T) {
		c := newLaunchAccountEC2Cache(cfg, awssdk.Config{})
		if got := c.clientFor(ctx, ""); got != nil {
			t.Error("an empty LaunchAccount must resolve to nil (home client), not a link client")
		}
	})

	t.Run("unknown link name falls back rather than erroring", func(t *testing.T) {
		c := newLaunchAccountEC2Cache(cfg, awssdk.Config{})
		if got := c.clientFor(ctx, "no-such-link"); got != nil {
			t.Error("an unregistered link must resolve to nil so the listing still renders")
		}
	})

	t.Run("nil config is tolerated", func(t *testing.T) {
		c := newLaunchAccountEC2Cache(nil, awssdk.Config{})
		if got := c.clientFor(ctx, "gpuman"); got != nil {
			t.Error("a nil config must resolve to nil, not panic")
		}
	})

	t.Run("nil receiver is tolerated", func(t *testing.T) {
		var c *launchAccountEC2Cache
		if got := c.clientFor(ctx, "gpuman"); got != nil {
			t.Error("a nil cache must resolve to nil, not panic")
		}
	})
}

// A link that fails to resolve is cached as nil and not retried. Without this a
// `km list` over N sandboxes sharing one unreachable link would attempt N SSM
// reads and N AssumeRole calls, turning a broken link into a slow listing.
func TestLaunchAccountEC2Cache_CachesNegativeResult(t *testing.T) {
	ctx := context.Background()
	cfg := &appcfg.Config{} // no links at all — every lookup misses

	c := newLaunchAccountEC2Cache(cfg, awssdk.Config{})
	if got := c.clientFor(ctx, "gpuman"); got != nil {
		t.Fatal("expected nil for an unknown link")
	}
	if _, cached := c.clients["gpuman"]; !cached {
		t.Error("a failed resolution must be cached so it is attempted once, not once per row")
	}
	// Second call must be served from the cache and stay nil.
	if got := c.clientFor(ctx, "gpuman"); got != nil {
		t.Error("cached negative result must remain nil")
	}
}
