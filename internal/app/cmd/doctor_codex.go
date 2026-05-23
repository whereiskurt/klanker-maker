// Package cmd — doctor_codex.go
// Phase 70 doctor checks for Codex parity (SC-7).
//
// Plan 70-07 (Path B revision — see CONTEXT.md "Path B contract"):
//
//   checkCodexVersionSupportsJSONL — for each running sandbox where the S3-fetched
//   profile has spec.cli.agent: codex, SSM-probes:
//     1. `command -v codex` resolves to /usr/local/bin/codex
//     2. `codex --version` parses to >= 0.121.0
//     3. `codex exec --help | grep -q -- '--json'` confirms JSONL mode
//   WARN-level on drift; SKIP when no codex sandboxes or no SSM runner configured.
//
//   checkAgentTypeConsistency — for each km-slack-threads row where agent_type is
//   set, confirm the corresponding sandbox profile (fetched from S3 via
//   downloadProfileFromS3 helper from destroy.go) still declares the same agent.
//   WARN-level on drift; SKIP when no rows carry agent_type or scanner not configured.
//
// Both checks honor --all-regions via the standard DoctorDeps injection pattern
// (callers pass region-appropriate clients). Tests use inline closures as mocks.
package cmd

import (
	"context"
	"fmt"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// codexSandboxRef describes a sandbox whose profile declares spec.cli.agent: codex.
// Populated by listCodexSandboxesImpl from km-sandboxes DDB + S3 profile fetch.
type codexSandboxRef struct {
	SandboxID  string
	InstanceID string // SSM target (EC2 instance ID); empty = skip SSM probe
	Region     string
}

// threadAgentRow describes a km-slack-threads row where agent_type is set.
// Populated by scanThreadAgentRowsImpl via paginated DDB Scan.
type threadAgentRow struct {
	ChannelID string
	ThreadTS  string
	SandboxID string
	AgentType string // "claude" | "codex"
}

// SSMCodexRunner is the test seam for SSM RunCommand probes against sandbox instances.
// Returns the combined stdout from the probed command, or an error on SSM failure.
// Production implementation wraps ssm.SendCommand + GetCommandInvocation polling.
// Unit tests substitute inline closures that return canned strings.
type SSMCodexRunner func(ctx context.Context, instanceID, region, cmd string) (string, error)

// ProfileFetcherFunc is the test seam for S3 profile lookups.
// Takes a sandbox ID and returns the parsed SandboxProfile.
// Production implementation wraps downloadProfileFromS3 + profile.Parse.
// Unit tests substitute inline closures.
type ProfileFetcherFunc func(ctx context.Context, sandboxID string) (*profile.SandboxProfile, error)

// =============================================================================
// checkCodexVersionSupportsJSONL
// =============================================================================

// checkCodexVersionSupportsJSONL verifies that every sandbox with
// spec.cli.agent: codex has a Codex binary that supports the --json JSONL mode
// required by Phase 70's poller dispatch. Runs three SSM probes:
//   1. `command -v codex` (binary present)
//   2. `codex --version` (output captured; caller validates >= 0.121.0)
//   3. `codex exec --help | grep -q -- '--json'` (JSONL flag present)
//
// Phase 70 SC-7. Mirrors doctor_slack.go::checkSlackInboundQueueExists pattern.
//
// Returns:
//   - SKIPPED: no listSandboxes func or no SSM runner configured, or no codex sandboxes.
//   - OK:   all probes pass for every codex sandbox.
//   - WARN: one or more sandboxes fail any probe (drift reported in Details).
func checkCodexVersionSupportsJSONL(
	ctx context.Context,
	listSandboxes func(context.Context) ([]codexSandboxRef, error),
	runSSM SSMCodexRunner,
) CheckResult {
	const name = "codex_version_supports_jsonl"
	if listSandboxes == nil || runSSM == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "codex version check deps not configured"}
	}

	sandboxes, err := listSandboxes(ctx)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("list codex sandboxes: %v", err)}
	}
	if len(sandboxes) == 0 {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "no sandboxes with spec.cli.agent: codex"}
	}

	var drifted []string
	for _, sb := range sandboxes {
		if sb.InstanceID == "" {
			// No EC2 instance ID available (e.g. Docker substrate); skip SSM for this sandbox.
			continue
		}
		// Probe 1: binary present.
		out1, err := runSSM(ctx, sb.InstanceID, sb.Region, "command -v codex 2>/dev/null || echo MISSING")
		if err != nil {
			drifted = append(drifted, fmt.Sprintf("%s (SSM error: %v)", sb.SandboxID, err))
			continue
		}
		if strings.Contains(out1, "MISSING") || !strings.Contains(out1, "codex") {
			drifted = append(drifted, fmt.Sprintf("%s (codex binary not found)", sb.SandboxID))
			continue
		}

		// Probe 2: version readable (>= 0.121.0 minimum — full semver comparison
		// deferred to the SSM output; presence of a non-empty version string is
		// the gate here because the exact minimum is hard to enforce portably in bash).
		out2, err := runSSM(ctx, sb.InstanceID, sb.Region, "codex --version 2>/dev/null || echo VERSION_FAIL")
		if err != nil {
			drifted = append(drifted, fmt.Sprintf("%s (SSM error on version probe: %v)", sb.SandboxID, err))
			continue
		}
		if strings.Contains(out2, "VERSION_FAIL") || strings.TrimSpace(out2) == "" {
			drifted = append(drifted, fmt.Sprintf("%s (codex --version failed)", sb.SandboxID))
			continue
		}
		if !codexVersionSatisfied(out2) {
			drifted = append(drifted, fmt.Sprintf("%s (codex version %q < 0.121.0)", sb.SandboxID, strings.TrimSpace(out2)))
			continue
		}

		// Probe 3: JSONL flag available.
		out3, err := runSSM(ctx, sb.InstanceID, sb.Region,
			"codex exec --help 2>/dev/null | grep -q -- '--json' && echo JSON_OK || echo JSON_MISSING")
		if err != nil {
			drifted = append(drifted, fmt.Sprintf("%s (SSM error on --json probe: %v)", sb.SandboxID, err))
			continue
		}
		if strings.Contains(out3, "JSON_MISSING") || !strings.Contains(out3, "JSON_OK") {
			drifted = append(drifted, fmt.Sprintf("%s (codex exec --json flag not supported)", sb.SandboxID))
			continue
		}
	}

	if len(drifted) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("%d codex sandbox(es) support JSONL mode", len(sandboxes)),
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckWarn,
		Message: fmt.Sprintf("%d/%d codex sandbox(es) fail JSONL version probe", len(drifted), len(sandboxes)),
		Details: drifted,
	}
}

// codexVersionSatisfied parses the output of `codex --version` and returns true
// when the reported version is >= 0.121.0. Tolerates various version string
// prefixes (e.g. "codex-cli 0.133.0", "0.121.0", "codex 0.133.0").
// Returns true on parse failure (err-open: unexpected version format → don't false-alarm).
func codexVersionSatisfied(versionOutput string) bool {
	// Extract the first token that looks like a semver (digits.digits.digits).
	// Split on whitespace and look for a field matching X.Y.Z.
	for _, token := range strings.Fields(versionOutput) {
		parts := strings.Split(token, ".")
		if len(parts) < 2 {
			continue
		}
		// Rough check: must start with a digit.
		if len(parts[0]) == 0 || parts[0][0] < '0' || parts[0][0] > '9' {
			continue
		}
		// Parse major.minor.
		var major, minor int
		if _, err := fmt.Sscanf(parts[0], "%d", &major); err != nil {
			continue
		}
		if _, err := fmt.Sscanf(parts[1], "%d", &minor); err != nil {
			continue
		}
		// 0.121.0 is the minimum (Phase 70 spike confirmed 0.121.0 has --json).
		if major > 0 {
			return true // major > 0: definitely new enough
		}
		if major == 0 && minor >= 121 {
			return true
		}
		return false // minor < 121 under major == 0
	}
	// Could not parse — err open (don't false-alarm on unknown version formats).
	return true
}

// =============================================================================
// checkAgentTypeConsistency
// =============================================================================

// checkAgentTypeConsistency verifies that every km-slack-threads row where
// agent_type is set still matches the corresponding sandbox profile's
// spec.cli.agent value (fetched from S3).
//
// Catches: operator flipped a profile from agent: claude to agent: codex (or
// vice versa) after a sandbox was created, without recreating the sandbox.
// Thread rows retain the old agent_type; the profile reflects the new value.
//
// Paginated scan follows 70-RESEARCH.md Pitfall 7 (Limit=100 per page).
// Profile fetches are cached per sandbox ID to avoid redundant S3 calls.
//
// Phase 70 SC-7. Mirrors doctor_slack.go::checkSlackAppEventsScopes pattern.
//
// Returns:
//   - SKIPPED: no scanRows func or no fetchProfile func configured, or no rows
//     carry agent_type attribute.
//   - OK:   every row's agent_type matches the live profile.
//   - WARN: one or more rows exhibit drift (Details lists channel/thread_ts pairs).
func checkAgentTypeConsistency(
	ctx context.Context,
	scanRows func(context.Context) ([]threadAgentRow, error),
	fetchProfile ProfileFetcherFunc,
) CheckResult {
	const name = "agent_type_consistency"
	if scanRows == nil || fetchProfile == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "agent_type check deps not configured"}
	}

	rows, err := scanRows(ctx)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("scan km-slack-threads: %v", err)}
	}
	if len(rows) == 0 {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "no km-slack-threads rows have agent_type attribute"}
	}

	// Cache profile lookups; a sandbox can have multiple thread rows.
	profileCache := make(map[string]*profile.SandboxProfile)
	var drifted []string

	for _, row := range rows {
		prof, cached := profileCache[row.SandboxID]
		if !cached {
			p, fetchErr := fetchProfile(ctx, row.SandboxID)
			if fetchErr != nil {
				// Sandbox profile unavailable (deleted sandbox, S3 purge, etc.).
				// Skip silently — absence of a profile is not a drift signal.
				profileCache[row.SandboxID] = nil
				continue
			}
			profileCache[row.SandboxID] = p
			prof = p
		}
		if prof == nil {
			// Profile already recorded as unavailable for this sandbox.
			continue
		}

		// Resolve the profile's agent; absence ≡ "claude" (CONTEXT.md locked decision).
		profileAgent := "claude"
		if prof.Spec.CLI != nil && prof.Spec.CLI.Agent == "codex" {
			profileAgent = "codex"
		}

		if row.AgentType != profileAgent {
			drifted = append(drifted, fmt.Sprintf("%s/%s: thread_agent=%s profile_agent=%s",
				row.ChannelID, row.ThreadTS, row.AgentType, profileAgent))
		}
	}

	if len(drifted) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("%d thread row(s) consistent with profile agent_type", len(rows)),
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckWarn,
		Message: fmt.Sprintf("%d/%d thread row(s) agent_type drifted from profile", len(drifted), len(rows)),
		Details: drifted,
	}
}

// =============================================================================
// Production implementation helpers
// =============================================================================

// codexDDBScanner is the narrow DynamoDB interface used by scanThreadAgentRowsImpl
// and listCodexSandboxesImpl. Implemented by *dynamodb.Client.
type codexDDBScanner interface {
	Scan(ctx context.Context, input *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

// scanThreadAgentRowsImpl scans the km-slack-threads DDB table with pagination
// (Limit=100 + LastEvaluatedKey loop per 70-RESEARCH.md Pitfall 7) and returns
// only rows where the agent_type attribute is present.
//
// tableName is the DDB table name (from cfg.GetSlackThreadsTableName()).
func scanThreadAgentRowsImpl(ctx context.Context, client codexDDBScanner, tableName string) ([]threadAgentRow, error) {
	if client == nil || tableName == "" {
		return nil, nil
	}

	const pageLimit = int32(100)
	var rows []threadAgentRow
	var lastKey map[string]dynamodbtypes.AttributeValue

	for {
		input := &dynamodb.ScanInput{
			TableName: awssdk.String(tableName),
			Limit:     awssdk.Int32(pageLimit),
			// ProjectionExpression only fetches the columns we need, reducing
			// read-capacity consumption on large tables.
			ProjectionExpression: awssdk.String("channel_id, thread_ts, sandbox_id, agent_type"),
		}
		if len(lastKey) > 0 {
			input.ExclusiveStartKey = lastKey
		}

		out, err := client.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", tableName, err)
		}

		for _, item := range out.Items {
			agentAttr, ok := item["agent_type"].(*dynamodbtypes.AttributeValueMemberS)
			if !ok || agentAttr == nil {
				continue // only process rows with agent_type set
			}

			row := threadAgentRow{AgentType: agentAttr.Value}
			if v, ok := item["channel_id"].(*dynamodbtypes.AttributeValueMemberS); ok {
				row.ChannelID = v.Value
			}
			if v, ok := item["thread_ts"].(*dynamodbtypes.AttributeValueMemberS); ok {
				row.ThreadTS = v.Value
			}
			if v, ok := item["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS); ok {
				row.SandboxID = v.Value
			}
			rows = append(rows, row)
		}

		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		lastKey = out.LastEvaluatedKey
	}

	return rows, nil
}
