package cmd

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// testFailFastAWSConfig returns a minimal aws.Config whose credential provider
// errors immediately (no network, no SSO token refresh). Any AWS API call that
// tries to retrieve credentials will fail in microseconds rather than hitting
// real AWS endpoints or blocking on SSO refresh.
//
// We return (config, nil) — not (config, error) — because call sites such as
// ResolveSandboxID branch on the LoadAWSConfig error vs. proceeding. Returning
// a valid config that fails only at *API-call* time mirrors exactly what happens
// in production when credentials are unavailable, and lets the fast-path
// pattern-match in ResolveSandboxID still trigger correctly.
func testFailFastAWSConfig() awssdk.Config {
	return awssdk.Config{
		Region: "us-east-1",
		Credentials: awssdk.CredentialsProviderFunc(func(ctx context.Context) (awssdk.Credentials, error) {
			return awssdk.Credentials{}, errors.New("test: no real AWS credentials (fast-fail seam)")
		}),
	}
}

// TestMain zeroes the package's sleep seam for the entire cmd test binary.
//
// internal/app/cmd was ~487s of the ~600s `go test ./...` suite — almost all of
// it in production time.Sleep calls (port-forward bind waits, SSM retry pauses,
// boot grace) that the credential/OAuth/shell/agent tests exercise at full
// wall-clock. Routing those through `sleep` (clock.go) and no-op'ing it here
// removes the waits without changing control flow. Tests that genuinely need a
// real delay can restore `sleep` locally (save/restore) — none do today.
func TestMain(m *testing.M) {
	// Override the LoadAWSConfig seam so that every cobra command test that
	// reaches ResolveSandboxID (or any other call site) gets a credential
	// provider that errors instantly — no real SSO token refresh, no network
	// round-trip to ~/.aws/config.  Tests that need the real loader can restore
	// it locally (save/restore the var).  The known-8 configure/cluster/
	// bootstrap tests that depend on real AWS remain environmentally gated as
	// before; this seam only removes incidental overhead from the passing tests.
	kmaws.LoadAWSConfig = func(ctx context.Context, profile string) (awssdk.Config, error) {
		return testFailFastAWSConfig(), nil
	}
	kmaws.LoadAWSConfigInRegion = func(ctx context.Context, profile, region string) (awssdk.Config, error) {
		return testFailFastAWSConfig(), nil
	}

	sleep = func(time.Duration) {}

	// internal/app/cmd tests never need real Lambda zips; the production default
	// downloads terraform + go-builds 9 lambdas per call (~15s), paid by every
	// TestRunInitPlan_* via RunInitPlanWithRunner. The two tests that exercise the
	// build-func contract override it locally (save/restore), so this global no-op
	// is safe.
	BuildLambdaZipsFunc = func(string) error { return nil }

	// Shrink the select-loop / ticker durations that the `sleep` seam does NOT
	// cover (these are time.After / time.NewTicker waits, not time.Sleep). The
	// shell port-forward reconnect/liveness loops and the SSM pollers otherwise
	// block tests on real 1–20s waits (e.g. TestAgentNonInteractive_IdleReset,
	// TestDesktop*, TestRunReconnectingPortForward). Production keeps the real
	// defaults; only this test binary overrides them. The control flow is
	// unchanged — the selects still fire, just ~instantly. A test that depends on
	// a specific duration restores its own value locally (save/restore) — see
	// TestAgentNonInteractive_IdleReset, which needs the poll loop to outlast the
	// idle-reset heartbeat ticker.
	AgentInitialPollDelay = time.Millisecond
	agentUtilPollInterval = time.Millisecond
	agentUtilInitialDelay = time.Millisecond
	portForwardReconnectBackoff = time.Millisecond
	portForwardBootGrace = time.Millisecond
	tunnelLivenessTick = time.Millisecond

	// Stop the AWS SDK from probing EC2 instance metadata (169.254.169.254) when
	// a test builds a real client without mocked deps. Off-instance that probe
	// times out after ~30s per call — the single biggest cost in this suite
	// (e.g. TestEmailSend_* spent 30s+ here before validating their args).
	// Disabling IMDS + supplying static dummy creds makes config.LoadDefaultConfig
	// return instantly. No real AWS call is made (tests mock at the client seam or
	// fail before the call); these only short-circuit credential discovery.
	os.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	os.Setenv("AWS_ACCESS_KEY_ID", "testing")
	os.Setenv("AWS_SECRET_ACCESS_KEY", "testing")
	os.Setenv("AWS_SESSION_TOKEN", "testing")
	if os.Getenv("AWS_REGION") == "" {
		os.Setenv("AWS_REGION", "us-east-1")
	}

	os.Exit(m.Run())
}
