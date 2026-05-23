// Package cmd — doctor_codex_test.go
// Unit tests for the Phase 70 doctor checks:
//   - checkCodexVersionSupportsJSONL (codex_version_supports_jsonl)
//   - checkAgentTypeConsistency (agent_type_consistency)
//
// All tests use inline closures as mock deps — no real AWS calls.
package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// =============================================================================
// checkCodexVersionSupportsJSONL tests
// =============================================================================

// TestDoctor_CodexHookPresent_AllGreen — all codex sandboxes pass every SSM
// probe (binary present, version >= 0.121.0, --json flag supported).
// SC-7 happy path. Replaces Wave 0 stub.
func TestDoctor_CodexHookPresent_AllGreen(t *testing.T) {
	listSandboxes := func(_ context.Context) ([]codexSandboxRef, error) {
		return []codexSandboxRef{
			{SandboxID: "sb-aaa", InstanceID: "i-111", Region: "us-east-1"},
			{SandboxID: "sb-bbb", InstanceID: "i-222", Region: "us-east-1"},
		}, nil
	}
	runSSM := func(_ context.Context, instanceID, _, cmd string) (string, error) {
		switch {
		case strings.Contains(cmd, "command -v codex"):
			return "/usr/local/bin/codex", nil
		case strings.Contains(cmd, "codex --version"):
			return "codex-cli 0.133.0", nil
		case strings.Contains(cmd, "codex exec --help"):
			return "JSON_OK", nil
		}
		return "", nil
	}
	res := checkCodexVersionSupportsJSONL(context.Background(), listSandboxes, runSSM)
	if res.Status != CheckOK {
		t.Errorf("expected CheckOK, got %s (msg: %s, details: %v)", res.Status, res.Message, res.Details)
	}
	if !strings.Contains(res.Message, "2 codex sandbox") {
		t.Errorf("expected message mentioning 2 sandboxes, got: %s", res.Message)
	}
}

// TestDoctor_CodexHookPresent_DriftWarns — first sandbox is missing the codex
// binary; second sandbox's version is too old (< 0.121.0).
// SC-7 drift case. Replaces Wave 0 stub.
func TestDoctor_CodexHookPresent_DriftWarns(t *testing.T) {
	listSandboxes := func(_ context.Context) ([]codexSandboxRef, error) {
		return []codexSandboxRef{
			{SandboxID: "sb-missing", InstanceID: "i-001", Region: "us-east-1"},
			{SandboxID: "sb-old", InstanceID: "i-002", Region: "us-east-1"},
		}, nil
	}
	runSSM := func(_ context.Context, instanceID, _, cmd string) (string, error) {
		if instanceID == "i-001" {
			// Binary not found.
			if strings.Contains(cmd, "command -v codex") {
				return "MISSING", nil
			}
			return "", nil
		}
		// i-002: binary present but version 0.100.0 (too old).
		if strings.Contains(cmd, "command -v codex") {
			return "/usr/local/bin/codex", nil
		}
		if strings.Contains(cmd, "codex --version") {
			return "codex-cli 0.100.0", nil
		}
		return "", nil
	}
	res := checkCodexVersionSupportsJSONL(context.Background(), listSandboxes, runSSM)
	if res.Status != CheckWarn {
		t.Errorf("expected CheckWarn, got %s (msg: %s)", res.Status, res.Message)
	}
	if len(res.Details) != 2 {
		t.Errorf("expected 2 drift details, got %d: %v", len(res.Details), res.Details)
	}
	// First detail should mention the missing sandbox.
	if !strings.Contains(res.Details[0], "sb-missing") {
		t.Errorf("expected first detail to mention sb-missing, got: %s", res.Details[0])
	}
	// Second detail should mention the old-version sandbox.
	if !strings.Contains(res.Details[1], "sb-old") {
		t.Errorf("expected second detail to mention sb-old, got: %s", res.Details[1])
	}
}

// TestDoctor_CodexHookPresent_NoCodexSandboxes_Skipped — no sandboxes have
// spec.cli.agent: codex; check returns CheckSkipped (false-positive guard).
// SC-7 false-positive guard. Replaces Wave 0 stub.
func TestDoctor_CodexHookPresent_NoCodexSandboxes_Skipped(t *testing.T) {
	listSandboxes := func(_ context.Context) ([]codexSandboxRef, error) {
		return nil, nil // empty — no codex sandboxes
	}
	runSSM := func(_ context.Context, _, _, _ string) (string, error) {
		t.Fatal("runSSM must not be called when there are no codex sandboxes")
		return "", nil
	}
	res := checkCodexVersionSupportsJSONL(context.Background(), listSandboxes, runSSM)
	if res.Status != CheckSkipped {
		t.Errorf("expected CheckSkipped, got %s (msg: %s)", res.Status, res.Message)
	}
}

// TestDoctor_CodexHookPresent_NilDeps_Skipped — nil listSandboxes or nil
// runSSM produces CheckSkipped (not a panic, not a WARN).
func TestDoctor_CodexHookPresent_NilDeps_Skipped(t *testing.T) {
	res := checkCodexVersionSupportsJSONL(context.Background(), nil, nil)
	if res.Status != CheckSkipped {
		t.Errorf("expected CheckSkipped with nil deps, got %s", res.Status)
	}
}

// =============================================================================
// checkAgentTypeConsistency tests
// =============================================================================

// TestDoctor_AgentTypeConsistency_AllConsistent — every thread row matches its
// sandbox profile agent. One claude row, one codex row.
// SC-7 happy path. Replaces Wave 0 stub.
func TestDoctor_AgentTypeConsistency_AllConsistent(t *testing.T) {
	rows := []threadAgentRow{
		{ChannelID: "C1", ThreadTS: "t1", SandboxID: "sb-claude", AgentType: "claude"},
		{ChannelID: "C1", ThreadTS: "t2", SandboxID: "sb-codex", AgentType: "codex"},
	}
	scan := func(_ context.Context) ([]threadAgentRow, error) { return rows, nil }
	fetch := func(_ context.Context, sandboxID string) (*profile.SandboxProfile, error) {
		p := &profile.SandboxProfile{}
		p.Spec.CLI = &profile.CLISpec{}
		if sandboxID == "sb-codex" {
			p.Spec.CLI.Agent = "codex"
		}
		// sb-claude: Agent is "" → treated as "claude" (locked decision)
		return p, nil
	}
	res := checkAgentTypeConsistency(context.Background(), scan, fetch)
	if res.Status != CheckOK {
		t.Errorf("expected CheckOK, got %s (msg: %s, details: %v)", res.Status, res.Message, res.Details)
	}
	if !strings.Contains(res.Message, "2 thread row") {
		t.Errorf("expected message mentioning 2 rows, got: %s", res.Message)
	}
}

// TestDoctor_AgentTypeConsistency_DriftWarns — one row says codex but the
// profile declares claude (operator flipped the profile post-create).
// SC-7 drift case. Replaces Wave 0 stub.
func TestDoctor_AgentTypeConsistency_DriftWarns(t *testing.T) {
	rows := []threadAgentRow{
		{ChannelID: "C1", ThreadTS: "t1", SandboxID: "sb-flipped", AgentType: "codex"},
	}
	scan := func(_ context.Context) ([]threadAgentRow, error) { return rows, nil }
	fetch := func(_ context.Context, _ string) (*profile.SandboxProfile, error) {
		// Profile was flipped to claude after the thread was created with codex.
		p := &profile.SandboxProfile{}
		p.Spec.CLI = &profile.CLISpec{Agent: "claude"}
		return p, nil
	}
	res := checkAgentTypeConsistency(context.Background(), scan, fetch)
	if res.Status != CheckWarn {
		t.Errorf("expected CheckWarn, got %s (msg: %s)", res.Status, res.Message)
	}
	if len(res.Details) != 1 {
		t.Errorf("expected 1 drift detail, got %d: %v", len(res.Details), res.Details)
	}
	// Detail must reference channel+thread or sandbox.
	detail := res.Details[0]
	if !strings.Contains(detail, "C1/t1") && !strings.Contains(detail, "sb-flipped") {
		t.Errorf("expected drift detail referencing C1/t1 or sb-flipped, got: %s", detail)
	}
}

// TestDoctor_AgentTypeConsistency_ProfileFetchError_Skipped — when the profile
// fetch returns an error (deleted sandbox), the row is silently skipped rather
// than flagged as drift.
func TestDoctor_AgentTypeConsistency_ProfileFetchError_Skipped(t *testing.T) {
	rows := []threadAgentRow{
		{ChannelID: "C1", ThreadTS: "t1", SandboxID: "sb-gone", AgentType: "codex"},
	}
	scan := func(_ context.Context) ([]threadAgentRow, error) { return rows, nil }
	fetch := func(_ context.Context, _ string) (*profile.SandboxProfile, error) {
		return nil, errors.New("s3: NoSuchKey") // sandbox profile was purged
	}
	res := checkAgentTypeConsistency(context.Background(), scan, fetch)
	// A single row whose profile can't be fetched → 0 rows with agent_type
	// are actually checked → message says 1 row consistent (we returned 1 row
	// but skipped the drift check). The check should not WARN due to fetch error.
	if res.Status == CheckWarn {
		t.Errorf("expected NOT CheckWarn when profile fetch errors, got: %s (details: %v)", res.Status, res.Details)
	}
}

// TestDoctor_AgentTypeConsistency_NilDeps_Skipped — nil scan or nil fetch
// produces CheckSkipped.
func TestDoctor_AgentTypeConsistency_NilDeps_Skipped(t *testing.T) {
	res := checkAgentTypeConsistency(context.Background(), nil, nil)
	if res.Status != CheckSkipped {
		t.Errorf("expected CheckSkipped with nil deps, got %s", res.Status)
	}
}

// =============================================================================
// codexVersionSatisfied unit tests
// =============================================================================

func TestCodexVersionSatisfied(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"codex-cli 0.133.0", true},
		{"codex-cli 0.121.0", true},
		{"codex 0.200.0", true},
		{"0.121.0", true},
		{"1.0.0", true},
		{"codex-cli 0.100.0", false},
		{"codex-cli 0.120.9", false},
		{"", true},          // empty → err-open (don't false-alarm)
		{"bad version", true}, // unparseable → err-open
	}
	for _, tc := range cases {
		got := codexVersionSatisfied(tc.input)
		if got != tc.want {
			t.Errorf("codexVersionSatisfied(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// --- helpers used by tests above ---

// containsStr is a helper to avoid importing strings in test conditions.
// Kept for symmetry; most tests use strings.Contains directly.
func containsStr(s, sub string) bool { return strings.Contains(s, sub) }

// makeCodexProfile returns a minimal SandboxProfile with spec.cli.agent set.
func makeCodexProfile(agent string) *profile.SandboxProfile {
	p := &profile.SandboxProfile{}
	p.Spec.CLI = &profile.CLISpec{Agent: agent}
	return p
}
