package compiler

import "testing"

// TestUserdata_CodexConfig_Emitted verifies the compiler unconditionally writes
// ~/.codex/config.toml into userdata for every sandbox.
// SC-1: Plan 70-02 Task 2 implements; Wave 0 baseline stub.
func TestUserdata_CodexConfig_Emitted(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-02 Task 2")
}

// TestUserdata_CodexConfig_NoPostToolUse guards against accidentally
// adding a PostToolUse hook entry (Tier 3 / Phase 68 deferral per CONTEXT.md).
// SC-1: Plan 70-02 Task 2 implements; Wave 0 baseline stub.
func TestUserdata_CodexConfig_NoPostToolUse(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-02 Task 2")
}

// TestUserdata_KMAgentEnv_DefaultClaude verifies KM_AGENT=claude when
// spec.cli.agent is empty (absence ≡ claude default).
// SC-4/SC-5/SC-6: Plan 70-02 Task 2 implements; Wave 0 baseline stub.
func TestUserdata_KMAgentEnv_DefaultClaude(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-02 Task 2")
}

// TestUserdata_KMAgentEnv_Codex verifies KM_AGENT=codex when spec.cli.agent: codex.
// SC-4/SC-5/SC-6: Plan 70-02 Task 2 implements; Wave 0 baseline stub.
func TestUserdata_KMAgentEnv_Codex(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-02 Task 2")
}

// TestUserdata_KMAgentEnv_BothFiles verifies KM_AGENT appears in both
// /etc/profile.d/km-notify-env.sh and /etc/km/notify.env.
// SC-4/SC-5/SC-6: Plan 70-02 Task 2 implements; Wave 0 baseline stub.
func TestUserdata_KMAgentEnv_BothFiles(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-02 Task 2")
}
