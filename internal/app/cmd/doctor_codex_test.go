// Package cmd — doctor_codex_test.go
// Unit tests for the Phase 70 doctor checks:
//   - checkCodexVersionSupportsJSONL (codex_version_supports_jsonl)
//   - checkAgentTypeConsistency (agent_type_consistency)
//
// Wave 0 stubs seeded in Task 1; real tests replace them in Task 2.
package cmd

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// TestDoctor_CodexHookPresent_AllGreen — all codex sandboxes have codex binary
// with supported version and --json flag present; check returns CheckOK.
// SC-7: Plan 70-07 Task 2 implements; Wave 0 baseline stub.
func TestDoctor_CodexHookPresent_AllGreen(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-07 Task 2")
}

// TestDoctor_CodexHookPresent_DriftWarns — codex binary missing or version too
// old or --json flag absent; check returns CheckWARN with details.
// SC-7: Plan 70-07 Task 2 implements; Wave 0 baseline stub.
func TestDoctor_CodexHookPresent_DriftWarns(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-07 Task 2")
}

// TestDoctor_CodexHookPresent_NoCodexSandboxes_Skipped — false-positive guard:
// when no sandboxes have spec.cli.agent: codex, the check returns CheckSkipped.
// SC-7 false-positive: Plan 70-07 Task 2 implements; Wave 0 baseline stub.
func TestDoctor_CodexHookPresent_NoCodexSandboxes_Skipped(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-07 Task 2")
}

// TestDoctor_AgentTypeConsistency_AllConsistent — every km-slack-threads row
// with agent_type set matches the corresponding profile's spec.cli.agent.
// SC-7: Plan 70-07 Task 2 implements; Wave 0 baseline stub.
func TestDoctor_AgentTypeConsistency_AllConsistent(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-07 Task 2")
}

// TestDoctor_AgentTypeConsistency_DriftWarns — row says codex but profile says
// claude (or vice versa); check returns CheckWARN.
// SC-7: Plan 70-07 Task 2 implements; Wave 0 baseline stub.
func TestDoctor_AgentTypeConsistency_DriftWarns(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-07 Task 2")
}

// --- helpers used by the real tests (Task 2) below this line ---

// containsStr is a helper to avoid importing strings in test conditions.
func containsStr(s, sub string) bool { return strings.Contains(s, sub) }

// makeCodexProfile returns a minimal SandboxProfile with spec.cli.agent set.
func makeCodexProfile(agent string) *profile.SandboxProfile {
	p := &profile.SandboxProfile{}
	p.Spec.CLI = &profile.CLISpec{Agent: agent}
	return p
}
