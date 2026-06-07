package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

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

// ssmAgentAuthChecker is the real implementation: it resolves the EC2 instance ID
// from the sandbox record's Resources and runs a single SSM command that checks
// both claude and codex auth in one shot.
type ssmAgentAuthChecker struct {
	ssmClient SSMSendAPI
}

// CheckAuth implements AgentAuthChecker.
// It uses a single SSM command to check both claude and codex auth state.
// Non-running sandboxes (no instance ID resolvable) return an error.
func (c *ssmAgentAuthChecker) CheckAuth(ctx context.Context, rec *kmaws.SandboxRecord) (bool, bool, error) {
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return false, false, fmt.Errorf("find EC2 instance: %w", err)
	}
	return checkAgentAuth(ctx, c.ssmClient, instanceID)
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
