package compiler

import (
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// synthesizeClaudeSettings builds a Claude Code 2.1.132 settings.json map from
// the typed AgentClaudeSpec (Phase 92, Wave 5).
//
// Output shape (per Wave 0 research, .planning/research/codex-config-toml.md
// §1b and 92-RESEARCH.md §1b):
//
//	{
//	  "permissions": {
//	    "allow": [...],   // from agent.claude.tools.autoApprove
//	    "deny":  [...]    // from agent.claude.tools.deny
//	    ...passthrough keys from agent.claude.permissions (ask, defaultMode, ...)
//	  },
//	  "trustedDirectories": [...]
//	}
//
// Why "permissions.allow" and NOT the legacy top-level "autoApprove" key?
//
//	The canonical Claude Code 2.1.132 form is "permissions.allow". The legacy
//	"autoApprove" key is still honored for backwards compatibility but is
//	deprecated. Phase 92 chooses the canonical form (Wave 0 research Option B) to
//	avoid tech debt. The pre-Phase-92 fixtures inlined the legacy "autoApprove"
//	shape; the migration is semantically equivalent (same tools auto-approved) but
//	byte-different — see docs/agent-tool-gating.md and the VC-3 reconciliation note
//	in pkg/compiler/userdata_phase92_byte_identity_test.go.
//
// Why "permissions.deny" and NOT "disallowedTools"?
//
//	"disallowedTools" is a Claude Code CLI flag (--disallowedTools), NOT a
//	settings.json key. The settings.json deny location is "permissions.deny".
//	Rule evaluation order is deny -> ask -> allow (deny always wins).
//
// Passthrough (agent.claude.permissions, the ONE untyped exception per the
// CONTEXT.md locked decision): each key is merged into the output "permissions"
// object. Typed tools (allow/deny) win over passthrough on key collision so an
// operator cannot silently override the gated tool set via the passthrough map.
//
// Nil-safe: a nil agent or nil agent.Claude returns an empty map (no settings
// synthesized — used by codex-default profiles where Claude is not configured).
//
// Returns map[string]any so the caller (mergeNotifyHookIntoSettings) can
// deep-merge the km-notify hooks before serializing to JSON.
func synthesizeClaudeSettings(agent *profile.AgentSpec) (map[string]any, error) {
	out := map[string]any{}
	if agent == nil || agent.Claude == nil {
		return out, nil
	}
	c := agent.Claude

	perms := map[string]any{}
	if len(c.Tools.AutoApprove) > 0 {
		perms["allow"] = c.Tools.AutoApprove
	}
	if len(c.Tools.Deny) > 0 {
		perms["deny"] = c.Tools.Deny
	}
	// Passthrough: agent.claude.permissions keys are merged into the output
	// permissions object. Typed allow/deny win on collision (operator cannot
	// override the gated tool set via the passthrough escape hatch).
	for k, v := range c.Permissions {
		if _, exists := perms[k]; exists {
			continue
		}
		perms[k] = v
	}
	if len(perms) > 0 {
		out["permissions"] = perms
	}

	if len(c.TrustedDirectories) > 0 {
		out["trustedDirectories"] = c.TrustedDirectories
	}

	return out, nil
}
