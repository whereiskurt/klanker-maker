# Agent tool gating — the `spec.agent:` block

**Phase 92 (Wave 5).** Operator-facing guide for the structured `spec.agent:`
block that replaced the pre-Phase-92 inlined `configFiles["/home/sandbox/.claude/settings.json"]`
antipattern.

## What this is

`spec.agent:` is a typed YAML block on a SandboxProfile that declares, per agent
(Claude Code and Codex):

- which CLI agent is the **default** (`agent.default`),
- which tools are **auto-approved** / **denied** (`agent.<agent>.tools.autoApprove` /
  `.deny`),
- which directories Claude **trusts** without prompting
  (`agent.claude.trustedDirectories`),
- a `permissions:` **passthrough** map for Claude settings.json keys not worth
  typing (`agent.claude.permissions`),
- per-agent CLI **args** (`agent.claude.args` / `agent.codex.args`).

The compiler **synthesizes** `~/.claude/settings.json` and `~/.codex/config.toml`
from this block at `km create` time. Operators no longer hand-write JSON inside
their profile YAML.

## Quick example

```yaml
spec:
  agent:
    default: claude
    claude:
      trustedDirectories: [/home/sandbox, /workspace]
      tools:
        autoApprove: [Bash, Read, Write, Edit, Glob, Grep, WebFetch, WebSearch, NotebookEdit]
        deny: [WebFetch]   # deny wins over allow per Claude Code 2.1.132
      args: ["--dangerously-skip-permissions"]
    codex:
      args: []
```

## Synthesis pipeline

For **Claude Code** (`pkg/compiler/agent_claude.go` → `pkg/compiler/userdata.go`):

1. `synthesizeClaudeSettings(spec.agent.claude)` → a settings.json map with
   `permissions.allow` / `permissions.deny` + top-level `trustedDirectories` +
   any passthrough keys.
2. `mergeNotifyHookIntoSettings(settings, ...)` → adds the km-notify
   `hooks.Notification` / `hooks.Stop` / `hooks.PostToolUse` entries.
3. The merged JSON is written to `/home/sandbox/.claude/settings.json` via the
   userdata `configFiles` path.

For **Codex** (`pkg/compiler/agent_codex.go` → `pkg/compiler/userdata.go`):

1. `synthesizeCodexConfig(spec.agent.codex)` → the base `[features] hooks` block
   + inert `[[hooks.PermissionRequest]]` / `[[hooks.Stop]]` entries, plus an args
   echo and an asymmetry NOTE when tools are populated.
2. The result is written to `/home/sandbox/.codex/config.toml` early in the
   userdata (so a profile's `initCommands` can still overwrite it — `codex.yaml`
   writes its own model/provider config that way).

## Claude Code ⇄ Codex asymmetry

**Claude Code 2.1.132 honors per-tool gating.** `permissions.allow` and
`permissions.deny` in `settings.json` are real: a tool in `deny` is removed from
Claude's context (deny → ask → allow evaluation order, deny always wins). The
synthesizer emits canonical `permissions.allow` / `permissions.deny` (NOT the
legacy top-level `autoApprove` key, and NOT the `--disallowedTools` CLI flag,
which is not a settings.json key).

**Codex 0.133 has NO native per-tool allow/deny in `config.toml`** (Wave 0 research
spike — `.planning/research/codex-config-toml.md`). The config.toml schema offers
`approval_policy`, `sandbox_mode`, and named filesystem/network `[permissions.*]`
profiles, but no equivalent of Claude's arbitrary-tool allow/deny arrays. The
existing `[[hooks.*]]` blocks are **inert** under Codex 0.121–0.133 (confirmed by
the Phase 70 spike — see `docs/codex-parity.md`).

Therefore:

- `synthesizeCodexConfig` emits the inert hook blocks for forward-compat (a future
  Codex release that activates them needs no migration).
- When `agent.codex.tools.{autoApprove,deny}` are populated, the synthesizer
  **preserves** the fields in the YAML and emits a `# NOTE:` block in the generated
  toml documenting that they are **declared but NOT enforced**.
- For actual Codex tool gating **today**, use sandbox-level enforcement: the eBPF
  network allowlist (`spec.network.enforcement: ebpf | both`) and Codex's own
  `sandbox_mode` filesystem scoping.

## No-merge-with-`configFiles` rule (locked decision)

A profile that populates **both** `agent.claude.tools.autoApprove`/`deny` **and**
an inlined `execution.configFiles["/home/sandbox/.claude/settings.json"]` is a
**hard validation error** (`km validate` fails). There is no merge fallback —
pick one mode.

Rationale: the synthesizer cannot know whether to trust the inline JSON or
override it with the typed fields. Silently merging is confusing; silently
preferring one breaks the operator's mental model. The mixed-mode validator
(`pkg/profile/validate.go`, Phase 92 Wave 4) rejects the conflict with a clear
`ValidationError` naming both paths.

## Migrating from pre-Phase-92 inlined JSON

**Before (pre-Phase-92):**

```yaml
spec:
  execution:
    configFiles:
      "/home/sandbox/.claude/settings.json": |
        {"autoApprove":["Bash","Read"], "trustedDirectories":["/home/sandbox"]}
```

**After (Phase 92):**

```yaml
spec:
  agent:
    claude:
      trustedDirectories: [/home/sandbox]
      tools:
        autoApprove: [Bash, Read]
```

Note: the synthesizer emits canonical `permissions.allow` (not the legacy
`autoApprove` top-level key). Claude Code 2.1.132 honors both forms; the canonical
form is preferred going forward and avoids tech debt. The two forms are
**semantically equivalent** — the same tools are auto-approved — which is exactly
what the Phase 92 byte-identity reconciliation test
(`pkg/compiler/userdata_phase92_byte_identity_test.go`, VC-3) asserts: everything
outside the settings.json blob stays byte-identical, and the blob itself is proven
to grant the same effective tool set, `trustedDirectories`, and km-notify hooks.

## Permissions passthrough (`agent.claude.permissions`)

`agent.claude.permissions` is a `map[string]any` passthrough for Claude Code
settings.json keys not worth typing individually — the **one** schema exception
(`additionalProperties: true`) per the Phase 92 locked decision. Each key is merged
into the synthesized `permissions` object; the typed `allow`/`deny` win on key
collision (an operator cannot silently widen the gated tool set via the
passthrough escape hatch).

Well-known passthrough keys:

- `defaultMode` — e.g. `"default"`, `"acceptEdits"`, `"plan"`.
- `ask` — array of tool patterns that prompt before running.
- `additionalDirectories` — extra directories Claude may access (distinct from
  top-level `trustedDirectories`).

Anything else flows through; Claude Code either honors or ignores it per its own
release.

## Future work

- **Codex tool gating** once OpenAI ships native per-tool allow/deny in
  `config.toml`. The `agent.codex.tools.*` fields are already defined and preserved
  for that day.
- **Per-tool quota / rate limiting** — out of scope for Phase 92.
