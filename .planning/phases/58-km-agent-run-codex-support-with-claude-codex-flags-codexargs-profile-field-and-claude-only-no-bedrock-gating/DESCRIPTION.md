# Phase 58: km agent run — Codex support

## Problem

`km agent <sb> --claude` (interactive) and `km agent <sb> --codex` (interactive) both exist today, but **`km agent run <sb> --prompt "..."` (non-interactive, fire-and-forget) is hardcoded to Claude**. There's no way to fire a non-interactive Codex prompt into a sandbox.

The `learn.yaml` profile already installs the Codex binary via `initCommands` (curl → `/usr/local/bin/codex`), so the target is present — only the operator-side wiring is missing.

## Goal

Symmetric agent support in `km agent run`: `--claude` (default, preserves current behavior) and `--codex` flags select which agent binary the tmux-wrapped runner invokes. Profile-level defaults work the same way for both agents.

## Scope

### In

- Add `--claude` / `--codex` mutually exclusive flags to `km agent run`, defaulting to `--claude` for backward compatibility
- Refactor `BuildAgentShellCommands(prompt, artifactsBucket, noBedrock...)` to take an agent type parameter; move the claude vs codex invocation into the generated bash
- Codex invocation uses `codex exec` with `--json` and `--dangerously-bypass-approvals-and-sandbox` (the codex equivalent of claude's `--dangerously-skip-permissions`)
- Gate the `--no-bedrock` env-unset stanza to claude only — it's Anthropic/Bedrock-specific and meaningless for codex
- Add `spec.cli.codexArgs []string` to profile schema, parallel to existing `claudeArgs`; loader helper returns either list based on selected agent
- Pass-through output contract: whatever codex writes to stdout lands in `output.json` as-is. No normalization to claude's `{result, total_cost_usd}` shape. `km agent results` consumers must be aware of the difference.
- Tests: extend `TestAgentNonInteractive_CommandConstruction` with a codex variant; add profile schema parse tests for `codexArgs`; verify `--no-bedrock` does not inject env unsets when `--codex` is selected.

### Out

- Codex MCP config, auth flows (`codex login`), or OpenAI API key plumbing beyond what already exists for interactive `--codex`
- Normalizing codex output to match claude's shape — explicit non-goal per design discussion
- `km agent results` CLI changes — it already streams whatever is in `output.json`
- Interactive `km agent --claude`/`--codex` — unchanged

## Design decisions (confirmed with user 2026-04-19)

1. **Flag style:** mirror interactive — `--claude` and `--codex` bools, mutually exclusive, claude is default
2. **Skip-permissions flag for codex:** `--dangerously-bypass-approvals-and-sandbox` (codex equivalent); user has not previously used this manually
3. **Output:** pass-through. No schema normalization between agents.
4. **Profile field:** new `spec.cli.codexArgs []string`, not a unified `agentArgs` map. Parallel naming keeps the loader logic simple and matches existing `claudeArgs`.
5. **`--no-bedrock` scoping:** claude-only. `--codex --no-bedrock` is a hard error returned from `RunE` before any SSM interaction (mirrors the existing `--interactive + --wait` mutual-exclusion check). Env-unset block and OAuth extraction remain claude-only.

## Dependencies

- **Phase 50** (km agent non-interactive execution) — provides `BuildAgentShellCommands` and the tmux-wrapped run scaffold
- **Phase 51** (km agent tmux sessions) — provides the tmux session pattern reused by both agents
- **Recent work on `spec.cli.claudeArgs`** (commit `4dbbe63`) — the schema/loader pattern to extend

## Success criteria

1. `km agent run <sb> --codex --prompt "list files"` fires codex non-interactively into a sandbox and writes output to `/workspace/.km-agent/runs/<ts>/output.json`
2. `km agent run <sb> --claude --prompt "..."` continues to behave exactly as today (regression-tested)
3. `km agent run <sb> --prompt "..."` (no agent flag) defaults to `--claude` (backward compat)
4. `km agent run <sb> --codex --no-bedrock --prompt "..."` **errors clearly** before any SSM call with a message like `"--no-bedrock is only valid with --claude"`. The env-unset block is never injected into the codex script.
5. A profile with `spec.cli.codexArgs: ["--model", "o4-mini"]` causes those args to be appended to the codex invocation when `--codex` is used; they are NOT appended for `--claude` runs
6. `km agent results <sb>` still returns the raw output.json content regardless of which agent produced it
7. All existing agent tests pass unchanged; new tests cover codex command construction and `codexArgs` plumbing

## Notes for the planner

- Codex exec JSON flag is `--json` (single dash-dash-word), not `--output-format json` like claude
- Codex does not have a `--bare` equivalent — check if any auth-bypass flag is needed when running under systemd/sudo
- The tmux session name and run directory structure stay identical across agents — only the `<agent> <flags> <prompt>` line inside the generated bash changes
