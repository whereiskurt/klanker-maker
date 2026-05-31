# Wave 0 Research Spike: Codex CLI + Claude Code Tool Gating

**Researched:** 2026-05-31
**Purpose:** Determine Wave 5 synthesizer scope for `pkg/compiler/agent_claude.go` and `pkg/compiler/agent_codex.go`

---

## Codex CLI 0.133 — `~/.codex/config.toml` Tool Allow/Deny

### Conclusion: NO native tool allow/deny in config.toml (HIGH confidence)

Codex CLI 0.133 does NOT have a top-level tool allowlist/denylist in `~/.codex/config.toml`. The config.toml schema supports:

**What exists:**
- `approval_policy` — controls when Codex pauses for user approval: `"untrusted"`, `"on-request"`, `"never"`, or `{ granular = { ... } }`
- `sandbox_mode` — filesystem/network scope: `"workspace-write"`, `"read-only"`, `"danger-full-access"`
- `[permissions.<name>]` — named permission profiles controlling filesystem path access (read/write/deny per path or glob)
- `[permissions.<name>.network]` — domain allow/deny lists for network access
- `[features]` — feature toggles: `shell_tool`, `web_search`, `multi_agent`, `hooks`
- `[mcp_servers.<id>]` — MCP server definitions with `enabled_tools` allowlist and `disabled_tools` denylist (MCP tools only)
- `[apps.<id>]` — app/connector tool enable/disable
- `default_permissions` — names the active permission profile

**What does NOT exist:**
- No top-level `tools.allow` or `tools.deny` list for built-in tools (bash, file read/write)
- No equivalent of Claude Code's `permissions.allow` / `permissions.deny` arrays for arbitrary tool gating
- No way to prevent the model from using bash, file editing, or other built-in tools via config.toml alone

**Critical in-codebase finding (HIGH confidence):**
The existing `docs/codex-parity.md` and `pkg/compiler/userdata.go:1821` both contain a documented spike result:
> "Codex 0.121/0.133 spike confirmed hooks do NOT fire from `~/.codex/config.toml`"

This means:
1. The `[[hooks.PermissionRequest]]` and `[[hooks.Stop]]` entries written to `~/.codex/config.toml` by the current compiler are inert under Codex 0.133
2. There is no hook-based tool interception mechanism available
3. The `[features] hooks = true` feature flag does not activate Claude-Code-style hooks

### Wave 5 Implication

`synthesizeCodexConfig(agent *profile.AgentSpec) (string, error)` in `pkg/compiler/agent_codex.go` must:

1. Emit the existing notify hook config (`[[hooks.PermissionRequest]]`, `[[hooks.Stop]]`) for forward-compatibility
2. Emit `approval_policy` and `sandbox_mode` from `agent.codex.args` or document that these are args-level flags
3. **NOT** attempt to synthesize tool allow/deny in config.toml — there is no schema for it
4. Document the asymmetry in `docs/agent-tool-gating.md`: "Codex does not support per-tool deny rules in config.toml. Tool gating for Codex is enforcement-layer only (eBPF/proxy network restrictions). A future Codex release may add this capability."

The `agent.codex.tools.autoApprove` and `agent.codex.tools.deny` fields should still be defined in the schema for future-compatibility and symmetry, but Wave 5 should emit a logged note when these fields are populated:
```
[km-bootstrap] NOTE: agent.codex.tools.autoApprove/deny are defined but
Codex 0.133 does not support tool gating in config.toml. Fields preserved
for future Codex release support.
```

**Sources:**
- `pkg/compiler/userdata.go:1821` — in-codebase spike documentation
- `docs/codex-parity.md:82-89` — explicit confirmation that hooks are inert
- OpenAI Codex configuration reference: https://developers.openai.com/codex/config-reference
- OpenAI Codex permissions: https://developers.openai.com/codex/permissions
- OpenAI Codex advanced config: https://developers.openai.com/codex/config-advanced

---

## Claude Code 2.1.132 — `settings.json` Deny Canonical Location

### Conclusion: `permissions.deny` (not `disallowedTools`) is the canonical location (HIGH confidence)

**Current schema structure:**
```json
{
  "permissions": {
    "allow": ["Bash(npm run *)", "Read(~/.zshrc)"],
    "deny":  ["Bash(curl *)", "Read(./.env)", "WebFetch"],
    "ask":   ["Bash(git push *)"],
    "defaultMode": "default",
    "additionalDirectories": []
  },
  "hooks": {
    "Notification": [...],
    "Stop": [...],
    "PostToolUse": [...]
  }
}
```

**Key facts:**
- `permissions.deny` is the authoritative deny list — NOT a top-level `disallowedTools` field
- `disallowedTools` exists as a CLI flag (`--disallowedTools`) but does NOT correspond to a settings.json key
- Rule evaluation order: `deny` → `ask` → `allow` (deny always wins)
- A bare tool name in deny (`"Bash"`, `"WebFetch"`) removes the tool from Claude's context entirely
- A scoped deny (`"Bash(rm *)"`) leaves the tool available but blocks matching calls
- `permissions.allow` is the canonical autoApprove list (replaces legacy `autoApprove` key)

**Legacy `autoApprove` key:**
The existing profiles use `{"autoApprove": [...], "trustedDirectories": [...]}` in inlined `settings.json`. This is a **legacy format** predating the `permissions` object. Claude Code still honors `autoApprove` for backwards compatibility but the canonical current API is:
```json
{
  "permissions": {
    "allow": ["Bash", "Read", "Write", "Edit"]
  }
}
```

**`trustedDirectories` field:**
This is a legitimate settings.json key that controls which directories Claude treats as trusted without additional prompts. It is distinct from `permissions.additionalDirectories`.

### Wave 5 Implications for `synthesizeClaudeSettings`

`pkg/compiler/agent_claude.go` `synthesizeClaudeSettings(agent *profile.AgentSpec)` should produce:

```json
{
  "permissions": {
    "allow": ["<tool1>", "<tool2>"],
    "deny":  ["<denied_tool>"]
  },
  "trustedDirectories": ["/home/sandbox", "/workspace"]
}
```

Then the `mergeNotifyHookIntoSettings` pipeline adds:
```json
{
  "hooks": {
    "Notification": [...km-notify-hook...],
    "Stop":         [...km-notify-hook...],
    "PostToolUse":  [...km-notify-hook...]
  }
}
```

**Migration equivalence:**
The inlined `{"autoApprove": ["Bash","Read","Write","Edit",...], "trustedDirectories": [...]}` should be converted to `{"permissions": {"allow": ["Bash","Read","Write","Edit",...]}, "trustedDirectories": [...]}`. These are semantically equivalent; Claude Code honors both.

**Sources:**
- Official Claude Code permissions docs: https://code.claude.com/docs/en/permissions
- Official Claude Code settings docs: https://code.claude.com/docs/en/settings
- GitHub issue tracking deny enforcement (reported bugs, noted for awareness): https://github.com/anthropics/claude-code/issues/6699
- JSON Schema: https://json.schemastore.org/claude-code-settings.json
