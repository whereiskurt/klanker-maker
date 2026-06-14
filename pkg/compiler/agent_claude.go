package compiler

import (
	"encoding/json"
	"fmt"
	"strings"

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

// mergeSynthesizedClaudeSettings deep-merges the typed-synthesized settings keys
// ON TOP of an operator's inlined ~/.claude/settings.json (the configFiles entry),
// rather than clobbering it. This is the fix for the silent-data-loss bug where
// any populated spec.agent.claude field caused the Wave-5 seeding step to
// overwrite the inlined file wholesale — dropping operator keys the synthesizer
// does not own (enabledPlugins, env, model, statusLine, enabledMcpjsonServers, …).
//
// Merge semantics (typed wins on the keys it owns; everything else preserved):
//   - "permissions": sub-map merge — typed allow/deny/passthrough sub-keys win on
//     collision, but inlined permissions sub-keys the synthesizer did not set
//     (e.g. additionalDirectories) are preserved.
//   - "trustedDirectories" (and any other top-level typed key): typed value
//     replaces the inlined value (the typed block is the canonical source).
//   - All other inlined top-level keys (enabledPlugins, env, model, …) are
//     carried through untouched.
//
// `inlined` is the raw JSON string from configFiles (may be empty). `typed` is the
// output of synthesizeClaudeSettings. Returns the merged JSON string. Errors iff
// the inlined JSON is malformed (fail-fast — same contract as the downstream
// mergeNotifyHookIntoSettings).
func mergeSynthesizedClaudeSettings(inlined string, typed map[string]any) (string, error) {
	const settingsPath = "/home/sandbox/.claude/settings.json"

	base := map[string]any{}
	if strings.TrimSpace(inlined) != "" {
		if err := json.Unmarshal([]byte(inlined), &base); err != nil {
			return "", fmt.Errorf("invalid JSON in spec.execution.configFiles[%q]: %w", settingsPath, err)
		}
	}

	for k, v := range typed {
		if k == "permissions" {
			typedPerms, _ := v.(map[string]any)
			basePerms, _ := base["permissions"].(map[string]any)
			if basePerms == nil {
				basePerms = map[string]any{}
			}
			// Typed allow/deny/passthrough win over any inlined permissions
			// sub-keys; inlined sub-keys the synthesizer did not set survive.
			for pk, pv := range typedPerms {
				basePerms[pk] = pv
			}
			base["permissions"] = basePerms
			continue
		}
		base[k] = v
	}

	buf, err := json.MarshalIndent(base, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal merged claude settings.json: %w", err)
	}
	return string(buf), nil
}
