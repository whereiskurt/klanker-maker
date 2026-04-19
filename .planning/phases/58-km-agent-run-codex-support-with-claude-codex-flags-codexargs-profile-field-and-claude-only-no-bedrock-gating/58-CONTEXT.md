# Phase 58: km agent run — Codex support - Context

**Gathered:** 2026-04-19
**Status:** Ready for planning
**Source:** Conversation with user 2026-04-19 (DESCRIPTION.md promoted)

<domain>
## Phase Boundary

**In scope:**
- `km agent run <sb> --prompt "..."` gains `--claude` (default) and `--codex` mutually-exclusive flags, mirroring the existing interactive `km agent <sb> --claude|--codex` pattern
- Refactor `BuildAgentShellCommands` in `internal/app/cmd/agent.go` to take an agent type and emit the correct CLI invocation (currently hardcoded to `claude -p "$PROMPT" --output-format json --dangerously-skip-permissions --bare`)
- Codex non-interactive invocation: `codex exec --json --dangerously-bypass-approvals-and-sandbox "$PROMPT"`
- Gate the `--no-bedrock` env-unset stanza (and the OAuth extraction block) to claude-only
- Add `spec.cli.codexArgs []string` to the profile schema, parallel to existing `spec.cli.claudeArgs`; loader returns the relevant list based on selected agent
- Pass-through output contract: codex's stdout lands in `output.json` as-is; no normalization to claude's `{result, total_cost_usd}` shape
- Error path: `km agent run --codex --no-bedrock` returns a clear error from `RunE` **before any SSM call**, message `"--no-bedrock is only valid with --claude"` (same pattern as the existing `--interactive + --wait` mutual-exclusion check at agent.go:178)

**Out of scope:**
- Codex MCP configuration, auth flows (`codex login`), or OpenAI API key plumbing beyond what already exists for the interactive `--codex` path
- Normalizing codex output to match claude's JSON shape — explicit non-goal
- `km agent results` consumer CLI changes — it streams whatever is in `output.json` already
- Interactive `km agent --claude|--codex` — unchanged
- Supporting any third agent type (goose, etc.) — future phase if needed

**Touchpoints:**
- `internal/app/cmd/agent.go` — `newAgentRunCmd`, `BuildAgentShellCommands`, `runAgentNonInteractive`
- `internal/app/cmd/agent_test.go` — extend `TestAgentNonInteractive_CommandConstruction` and `TestAgentNonInteractive_NoBedrock`
- `pkg/profile/types.go` — add `ClaudeArgs` sibling `CodexArgs []string` to `CLISpec`
- `pkg/profile/schemas/sandbox_profile.schema.json` — mirror `claudeArgs` entry for `codexArgs`
- `pkg/profile/types_test.go` — add a `codexArgs` parse test paralleling `TestCLISpec_ClaudeArgsParsesFromYAML`

</domain>

<decisions>
## Implementation Decisions (confirmed with user 2026-04-19)

### Flag design
- **LOCKED:** Mirror the interactive path — `--claude` (default) and `--codex` as mutually exclusive bools on `km agent run`. No `--agent <name>` enum. Default is `--claude` for backward compatibility so no existing invocation breaks.
- **LOCKED:** Mutual-exclusion between `--claude` and `--codex` enforced in `RunE` with an explicit error (pattern: existing `--interactive + --wait` check at agent.go:178).

### Codex invocation
- **LOCKED:** `codex exec --json --dangerously-bypass-approvals-and-sandbox "$PROMPT"` — the codex equivalent of claude's `--dangerously-skip-permissions` flag is `--dangerously-bypass-approvals-and-sandbox`. User has not previously exercised this flag manually; planner should verify the exact flag name against codex version shipped in `profiles/learn.yaml` (`codex-x86_64-unknown-linux-musl v0.121.0`).
- **LOCKED:** JSON output flag is `--json`, not `--output-format json` like claude. Planner should confirm.
- The prompt continues to be passed base64-encoded and decoded inside the shell script (preserves existing escaping safety from phase 50). Only the binary + flags line changes.
- No `--bare` equivalent needed for codex; codex doesn't have the OAuth/Bedrock dual-mode that drove claude's `--bare`.

### `--no-bedrock` scoping
- **LOCKED:** Claude-only. `km agent run --codex --no-bedrock` is a **hard error** returned from `RunE` before any SSM interaction. Error text: `"--no-bedrock is only valid with --claude"`.
- **LOCKED:** Neither the env-unset block (`unset CLAUDE_CODE_USE_BEDROCK` etc.) nor the OAuth-extraction block is ever injected into a codex-run script — they're anchored inside the claude branch of the refactored `BuildAgentShellCommands`.

### Output contract
- **LOCKED:** Pass-through. Whatever `codex exec --json` writes to stdout lands in `output.json` unchanged. `km agent results <sb>` consumers must be aware the shape differs between agents. No normalization, no envelope wrapping, no translation.
- **Implication:** A downstream tool that expects `.result` or `.total_cost_usd` from output.json will break for codex runs. This is acceptable per user decision — honesty over pretend compatibility.

### Profile schema
- **LOCKED:** Add `spec.cli.codexArgs []string`, parallel to existing `spec.cli.claudeArgs` (added in commit 4dbbe63). Not a unified `agentArgs` map — keeps loader logic parallel and schema diff minimal.
- **LOCKED:** `claudeArgs` only applies to claude runs; `codexArgs` only applies to codex runs. No cross-contamination (the existing `BuildAgentCommand` helper already demonstrates this pattern for the interactive path at agent.go:125-145).
- The `loadProfileCLIClaudeArgs` helper added in 4dbbe63 should get a twin `loadProfileCLICodexArgs`, or be refactored into `loadProfileCLIAgentArgs(agent string)` — planner's call.

### Testing approach
- **Binding:** TDD per project convention (phase 54-55 pattern, feedback_rebuild_km memory).
- Must extend `TestAgentNonInteractive_CommandConstruction` with a codex variant verifying the correct codex flags and the **absence** of the `--no-bedrock` unset stanza.
- Must add a new test verifying `km agent run --codex --no-bedrock` returns an error without invoking SSM.
- Must add `TestCLISpec_CodexArgsParsesFromYAML` paralleling the claudeArgs test.
- Must add `TestBuildAgentShellCommands_Codex` if the refactor introduces a new exported signature.

### Claude's Discretion
- **Signature of `BuildAgentShellCommands`:** Current signature is `(prompt, artifactsBucket string, noBedrock ...bool) ([]string, string)` — the variadic `noBedrock` is ugly. Planner can clean this up to a struct-arg or positional required args as part of the refactor.
- **Whether to expose a new helper `BuildAgentCommandLine(agent, args)`** vs inlining the agent-switch into `BuildAgentShellCommands` — planner's call based on testability.
- **Loader consolidation:** `loadProfileCLINoBedrock` and `loadProfileCLIClaudeArgs` currently both call the shared `loadProfileCLI` — adding `loadProfileCLICodexArgs` is trivial; planner decides whether to further consolidate into a single helper returning the whole `CLISpec`.
- **Config defaults via `learn.yaml`:** Not required for this phase; user has not asked for `codexArgs` in `learn.yaml`. Leave for a follow-up if wanted.

</decisions>

<specifics>
## Specific Ideas / Anchors

### Code references to follow
- `internal/app/cmd/agent.go:144-208` — `newAgentRunCmd` definition + flag wiring
- `internal/app/cmd/agent.go:99-126` — existing interactive `--claude|--codex` switch to mirror
- `internal/app/cmd/agent.go:1018-1072` — `BuildAgentShellCommands` to refactor
- `internal/app/cmd/agent.go:1067-1140` — `loadProfileCLI*` helper family to extend
- `pkg/profile/types.go:354-367` — `CLISpec` struct (already has `ClaudeArgs`)
- `pkg/profile/schemas/sandbox_profile.schema.json:474-494` — `cli` section (already has `claudeArgs`)

### Existing test patterns to mirror
- `internal/app/cmd/agent_test.go:137-168` — `TestAgentNonInteractive_CommandConstruction`
- `internal/app/cmd/agent_test.go:170-193` — `TestAgentNonInteractive_NoBedrock` (variadic-bool test — may need update for signature cleanup)
- `internal/app/cmd/agent_test.go:830-888` — `TestBuildAgentCommand` (6 sub-tests — parallel pattern for codex)
- `pkg/profile/types_test.go:TestCLISpec_ClaudeArgsParsesFromYAML` — mirror for codex

### Codex binary location in sandbox
- `/usr/local/bin/codex` (installed via `initCommands` in `profiles/learn.yaml:63-65`)
- Version anchor: `rust-v0.121.0` — planner should verify flag names exist in that version

### Example desired CLI surface
```bash
# Default (unchanged — still claude)
km agent run sb-abc --prompt "fix tests"

# Explicit claude (new — same behavior as default)
km agent run sb-abc --claude --prompt "fix tests"

# Codex (new)
km agent run sb-abc --codex --prompt "list files"

# Codex with profile codexArgs from learn.yaml (future)
km agent run sb-abc --codex --prompt "..." --wait

# Error path (new)
km agent run sb-abc --codex --no-bedrock --prompt "..."
# → "--no-bedrock is only valid with --claude"
```

</specifics>

<deferred>
## Deferred Ideas

- **`codexArgs` entry in `learn.yaml`:** User didn't ask; easy follow-up once this lands.
- **Output normalization envelope:** Explicitly rejected for this phase; could revisit if `km agent results` ever grows cross-agent reporting.
- **Goose / other agent support:** Schema is extensible but no demand signal yet.
- **`km agent run --no-interactive-login`-style auth flags for codex:** Defer until someone hits an auth issue; interactive `--codex` already works inside learn sandboxes, so the creds path is presumed good.
- **Unified `--agent <name>` enum flag:** Considered and rejected in favor of mirrored `--claude|--codex` for surface consistency.

</deferred>

---

*Phase: 58-km-agent-run-codex-support*
*Context gathered: 2026-04-19 via conversation promoting DESCRIPTION.md*
