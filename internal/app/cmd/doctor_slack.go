// Package cmd — doctor_slack.go
// Slack health checks for km doctor (Plan 63-09).
// checkSlackTokenValidity: validates the bot token via a test message through
// the bridge Lambda.
// checkStaleSlackChannels: warns about per-sandbox Slack channels for sandboxes
// that no longer have active EC2 instances.
package cmd

import (
	"context"
	"fmt"
	"strings"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	slackpkg "github.com/whereiskurt/klankrmkr/pkg/slack"
)

// SlackMetadataScanner returns sandbox metadata records that have Slack enabled.
// Implemented by a thin wrapper around ListAllSandboxesByDynamo; injected for tests.
type SlackMetadataScanner interface {
	ListSlackEnabled(ctx context.Context) ([]kmaws.SandboxMetadata, error)
}

// EC2InstanceLister checks whether an EC2 instance (sandbox) is still active.
// Injected for tests; production uses an EC2 DescribeInstances call.
type EC2InstanceLister interface {
	InstanceExists(ctx context.Context, sandboxID string) (bool, error)
}

// checkSlackTokenValidity validates the Slack bot token by sending a test
// message through the bridge Lambda. Returns:
//
//   - SKIPPED: /km/slack/bot-token not set → Slack not configured.
//   - WARN: bot-token set but /km/slack/bridge-url missing.
//   - OK: test envelope posted successfully.
//   - WARN: bridge returned non-OK response (invalid_auth etc.).
//   - ERROR: bridge network error.
func checkSlackTokenValidity(
	ctx context.Context,
	ssmStore SSMParamStore,
	region string,
	keyLoader PrivKeyLoaderFunc,
	poster BridgePosterFunc,
) CheckResult {
	botToken, _ := ssmStore.Get(ctx, "/km/slack/bot-token", true)
	if botToken == "" {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckSkipped,
			Message: "/km/slack/bot-token not configured — Slack integration not set up",
		}
	}

	bridgeURL, _ := ssmStore.Get(ctx, "/km/slack/bridge-url", false)
	if bridgeURL == "" {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckWarn,
			Message: "/km/slack/bridge-url not configured — run km slack init to deploy the bridge Lambda",
		}
	}

	channelID, _ := ssmStore.Get(ctx, "/km/slack/shared-channel-id", false)
	if channelID == "" {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckWarn,
			Message: "/km/slack/shared-channel-id not configured — run km slack init",
		}
	}

	priv, err := keyLoader(ctx, region)
	if err != nil {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckWarn,
			Message: fmt.Sprintf("operator signing key unavailable: %v — km slack test may fail", err),
		}
	}

	env, envErr := slackpkg.BuildEnvelope(slackpkg.ActionTest, slackpkg.SenderOperator,
		channelID, "km doctor", "Platform health check", "")
	if envErr != nil {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckError,
			Message: fmt.Sprintf("failed to build test envelope: %v", envErr),
		}
	}

	_, sig, sigErr := slackpkg.SignEnvelope(env, priv)
	if sigErr != nil {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckError,
			Message: fmt.Sprintf("failed to sign test envelope: %v", sigErr),
		}
	}

	resp, postErr := poster(ctx, bridgeURL, env, sig)
	if postErr != nil {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckError,
			Message: fmt.Sprintf("bridge request failed: %v", postErr),
		}
	}
	if resp == nil || !resp.OK {
		errMsg := ""
		if resp != nil {
			errMsg = resp.Error
		}
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckWarn,
			Message: fmt.Sprintf("bridge returned not-OK: %s", errMsg),
		}
	}

	return CheckResult{
		Name:    "Slack bot token",
		Status:  CheckOK,
		Message: fmt.Sprintf("test message delivered (ts=%s)", resp.TS),
	}
}

// checkStaleSlackChannels scans for per-sandbox Slack channels that belong to
// sandboxes without active EC2 instances (stale channels). Returns:
//
//   - OK: no Slack-enabled records found, or all sandboxes are active.
//   - WARN: N stale per-sandbox channels found.
func checkStaleSlackChannels(
	ctx context.Context,
	scanner SlackMetadataScanner,
	ec2 EC2InstanceLister,
) CheckResult {
	records, err := scanner.ListSlackEnabled(ctx)
	if err != nil {
		return CheckResult{
			Name:    "Stale Slack channels",
			Status:  CheckWarn,
			Message: fmt.Sprintf("failed to list Slack-enabled sandboxes: %v", err),
		}
	}

	var stale []string
	for _, meta := range records {
		if !meta.SlackPerSandbox || meta.SlackChannelID == "" {
			continue // skip shared/override channels — they're operator-managed
		}
		exists, existErr := ec2.InstanceExists(ctx, meta.SandboxID)
		if existErr != nil {
			continue // skip on error; don't false-alarm
		}
		if !exists {
			stale = append(stale, meta.SlackChannelID)
		}
	}

	if len(stale) == 0 {
		return CheckResult{
			Name:    "Stale Slack channels",
			Status:  CheckOK,
			Message: "no stale per-sandbox channels",
		}
	}

	return CheckResult{
		Name:   "Stale Slack channels",
		Status: CheckWarn,
		Message: fmt.Sprintf(
			"%d stale per-sandbox channel(s) with no active sandbox: %s — run km destroy to archive",
			len(stale),
			strings.Join(stale, ", "),
		),
	}
}
