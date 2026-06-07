package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// FormatUptime returns a compact human-readable duration since createdAt.
// Bands:
//   - < 1 hour:  "8m"    (minutes only)
//   - 1h–<1d:   "3h12m" (hours+minutes; drop "m" when M==0 → "3h")
//   - >= 1 day:  "2d4h"  (days+hours; drop "h" when H==0 → "2d")
//   - zero/negative: "0m"
//
// Exported so tests in the cmd_test package can call it directly.
func FormatUptime(createdAt time.Time) string {
	return formatUptime(createdAt)
}

// formatUptime is the unexported implementation used by list.go and status.go.
func formatUptime(createdAt time.Time) string {
	d := time.Since(createdAt)
	if d <= 0 {
		return "0m"
	}

	total := d.Truncate(time.Minute)
	if total == 0 {
		return "0m"
	}

	minutes := int(total.Minutes())
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}

	hours := minutes / 60
	mins := minutes % 60
	if hours < 24 {
		if mins == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh%dm", hours, mins)
	}

	days := hours / 24
	hrs := hours % 24
	if hrs == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd%dh", days, hrs)
}

// AgentAuthChecker checks whether the claude and codex CLIs are authenticated
// on a given sandbox. The real implementation makes a single SSM round-trip;
// tests stub this interface to avoid any AWS calls.
type AgentAuthChecker interface {
	CheckAuth(ctx context.Context, rec *kmaws.SandboxRecord) (claudeLoggedIn bool, codexLoggedIn bool, err error)
}

// ec2DescribeInstancesAPI is the slice of the EC2 client used to resolve a
// sandbox's instance ID by tag. Satisfied by *ec2.Client; stubbed in tests.
type ec2DescribeInstancesAPI interface {
	DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// ssmAgentAuthChecker is the real implementation: it resolves the EC2 instance ID
// for the sandbox and runs a single SSM command that checks both claude and codex
// auth in one shot.
type ssmAgentAuthChecker struct {
	ssmClient SSMSendAPI
	ec2Client ec2DescribeInstancesAPI
}

// CheckAuth implements AgentAuthChecker.
// It uses a single SSM command to check both claude and codex auth state.
// Non-running sandboxes (no instance ID resolvable) return an error.
//
// Instance-ID resolution has two sources because the two callers populate the
// record differently: km status builds rec.Resources from the tag API (instance
// ARN present), while km list's default DynamoDB scan leaves rec.Resources empty.
// So we try the ARN fast-path first, then fall back to an EC2 tag lookup — without
// the fallback, km list --auth showed "?" for every sandbox on the default path.
func (c *ssmAgentAuthChecker) CheckAuth(ctx context.Context, rec *kmaws.SandboxRecord) (bool, bool, error) {
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		if c.ec2Client == nil {
			return false, false, fmt.Errorf("find EC2 instance: %w", err)
		}
		instanceID, err = resolveInstanceIDByTag(ctx, c.ec2Client, rec.SandboxID)
		if err != nil {
			return false, false, fmt.Errorf("resolve instance for %s: %w", rec.SandboxID, err)
		}
	}
	return checkAgentAuth(ctx, c.ssmClient, instanceID)
}

// resolveInstanceIDByTag finds the EC2 instance ID for a sandbox via the
// km:sandbox-id tag — the same lookup checkEC2InstanceStatus uses for live
// status. Prefers a running instance; falls back to the first match. Skips
// terminated/shutting-down instances so a stale prior instance with the same
// tag isn't selected over the live one.
func resolveInstanceIDByTag(ctx context.Context, client ec2DescribeInstancesAPI, sandboxID string) (string, error) {
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("tag:km:sandbox-id"), Values: []string{sandboxID}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe instances: %w", err)
	}
	var fallback string
	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			if inst.InstanceId == nil {
				continue
			}
			if inst.State != nil &&
				(inst.State.Name == ec2types.InstanceStateNameTerminated ||
					inst.State.Name == ec2types.InstanceStateNameShuttingDown) {
				continue
			}
			if inst.State != nil && inst.State.Name == ec2types.InstanceStateNameRunning {
				return *inst.InstanceId, nil
			}
			if fallback == "" {
				fallback = *inst.InstanceId
			}
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("no EC2 instance found for sandbox %s", sandboxID)
}

// checkAgentAuth runs ONE SSM command that performs both the claude auth check
// and the codex auth check in a single round-trip, minimising latency when
// fanning out across many sandboxes.
//
// The command does two things sequentially on the box:
//  1. Runs `claude auth status` as the sandbox user and prints its JSON output
//     so the loggedIn field is visible.
//  2. Tests whether /home/sandbox/.codex/auth.json exists and prints a sentinel.
func checkAgentAuth(ctx context.Context, ssmClient SSMSendAPI, instanceID string) (claudeLoggedIn bool, codexLoggedIn bool, err error) {
	cmd := `sudo -u sandbox bash -lc 'claude auth status 2>/dev/null'
test -f /home/sandbox/.codex/auth.json && echo KM_CODEX_OK || echo KM_CODEX_MISSING`

	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, cmd)
	if err != nil {
		return false, false, fmt.Errorf("SSM auth check: %w", err)
	}

	// claude: parse loggedIn JSON field — tolerant of spacing differences
	// between claude CLI versions (matches verifyClaudeAuthStatus in agent_auth.go).
	claudeLoggedIn = strings.Contains(out, `"loggedIn": true`) || strings.Contains(out, `"loggedIn":true`)

	// codex: file-based sentinel
	codexLoggedIn = strings.Contains(out, "KM_CODEX_OK")

	return claudeLoggedIn, codexLoggedIn, nil
}
