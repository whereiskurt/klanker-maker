---
phase: 58-km-agent-run-codex-support
verified: 2026-04-19T17:10:00Z
status: passed
score: 7/7 must-haves verified
re_verification: false
---

# Phase 58: Codex Agent Support for km agent run — Verification Report

**Phase Goal:** Add codex agent support to `km agent run` via `--claude`/`--codex` flags, introduce `spec.cli.codexArgs` profile field, gate `--no-bedrock` to claude-only (codex has no Bedrock path).
**Verified:** 2026-04-19T17:10:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `spec.cli.codexArgs []string` field exists on CLISpec with JSON Schema support | VERIFIED | `pkg/profile/types.go:371` `CodexArgs []string \`yaml:"codexArgs,omitempty"\``; schema `sandbox_profile.schema.json:488` mirrors `claudeArgs` shape |
| 2 | `--claude` / `--codex` flags on `km agent run` with mutual exclusion | VERIFIED | `agent.go:230-231` registers both flags; `agent.go:188-190` fires mutex error `--claude and --codex are mutually exclusive` before ResolveSandboxID |
| 3 | `--no-bedrock` returns error when paired with `--codex` before SSM/AWS calls | VERIFIED | `agent.go:192-194` checks `useCodex && noBedrock` before `ResolveSandboxID` at line 203; `TestAgentRun_CodexNoBedrockError` asserts `FetchSandbox==0` and `SendCommand==0` |
| 4 | `BuildAgentShellCommands` branches on agent type; codex emits `codex exec --json --dangerously-bypass-approvals-and-sandbox [codexArgs...] "$PROMPT"` | VERIFIED | `agent.go:1082-1095` switch on `opts.AgentType`; codex branch builds correct command with CodexArgs before prompt |
| 5 | `--no-bedrock` env-unset + OAuth extraction stanzas only present in claude branch | VERIFIED | `agent.go:1107-1118` `noBedrockLines` only set under `default:` (claude) branch when `opts.NoBedrock`; codex `case` comment: "noBedrockLines stays empty — codex never uses Bedrock env vars"; `TestAgentNonInteractive_NoBedrock/codex_ignores_nobedrock` passes |
| 6 | `loadProfileCLICodexArgs` helper loads profile codexArgs (fail-open on error) | VERIFIED | `agent.go:1176-1182` mirrors `loadProfileCLIClaudeArgs`; returns `p.CodexArgs`, returns nil on any error path |
| 7 | Default behavior unchanged (no flag = claude, backward compatible) | VERIFIED | `agent.go:208-212` `agentType := "claude"` default; `TestAgentRun_DefaultClaudeBackwardCompat` passes asserting `claude -p`, `--output-format json`, `--dangerously-skip-permissions` |

**Score:** 7/7 truths verified

### Required Artifacts

| Artifact | Status | Evidence |
|----------|--------|----------|
| `pkg/profile/types.go` | VERIFIED | `CodexArgs []string \`yaml:"codexArgs,omitempty"\`` at line 371, added after `ClaudeArgs` field |
| `pkg/profile/types_test.go` | VERIFIED | `TestCLISpec_CodexArgsParsesFromYAML` (line 949) and `TestCLISpec_CodexArgsOptional` (line 1021) both pass |
| `pkg/profile/schemas/sandbox_profile.schema.json` | VERIFIED | `codexArgs` property at line 488 with `type: array, items: {type: string}` and description |
| `internal/app/cmd/agent.go` | VERIFIED | `AgentRunOptions` struct (line 1061), refactored `BuildAgentShellCommands` (line 1075), `--claude`/`--codex` flags (lines 230-231), mutex + no-bedrock gates (lines 188-193), `loadProfileCLICodexArgs` (line 1176), `NewAgentRunCmd` test seam (line 239) |
| `internal/app/cmd/agent_test.go` | VERIFIED | `TestBuildAgentShellCommands_Codex` (line 689), `TestAgentRun_ClaudeCodexMutex` (line 939), `TestAgentRun_CodexNoBedrockError` (line 963), `TestAgentRun_CodexFlag` (line 984), `TestAgentRun_DefaultClaudeBackwardCompat` (line 1013), `TestLoadProfileCLICodexArgs` (line 1041) — all pass |

### Key Link Verification

| From | To | Via | Status |
|------|----|-----|--------|
| `pkg/profile/types.go CLISpec` | `sandbox_profile.schema.json` | `codexArgs` YAML tag matches schema property name | WIRED |
| `pkg/profile/types_test.go` | `pkg/profile/types.go CLISpec.CodexArgs` | `p.Spec.CLI.CodexArgs` asserted in both test functions | WIRED |
| `agent.go newAgentRunCmd RunE` | `agent.go runAgentNonInteractive` | `agentType` string arg — "claude" (default) or "codex" (when --codex set); line 221 | WIRED |
| `agent.go runAgentNonInteractive` | `agent.go loadProfileCLICodexArgs` | Conditional load at lines 484-490: codex path calls `loadProfileCLICodexArgs` | WIRED |
| `agent.go loadProfileCLICodexArgs` | `pkg/profile/types.go CLISpec.CodexArgs` | `p.CodexArgs` returned at line 1181 | WIRED |
| `agent.go BuildAgentShellCommands` | `agent.go AgentRunOptions.CodexArgs` | codex branch appends `opts.CodexArgs` before prompt (lines 1086-1091) | WIRED |

### Requirements Coverage

Requirements CODEX-01 through CODEX-05 appear in plan frontmatter but are not defined in `.planning/REQUIREMENTS.md`. These are phase-internal requirement IDs — the REQUIREMENTS.md file does not yet contain a codex section. This is a documentation gap (the file covers v1 requirements ending at AGENT-* series) but does not reflect any implementation gap. All plan-defined acceptance criteria are satisfied in code.

| Plan Req ID | Status | Evidence |
|-------------|--------|----------|
| CODEX-01 (Plan 01) | SATISFIED | `CLISpec.CodexArgs` field + schema + 2 unit tests |
| CODEX-02 (Plan 02) | SATISFIED | `AgentRunOptions` struct + `BuildAgentShellCommands` branching |
| CODEX-03 (Plan 02) | SATISFIED | codex-only branch in `BuildAgentShellCommands`; no-bedrock stanza absent from codex path |
| CODEX-04 (Plan 03) | SATISFIED | `--claude`/`--codex` flags registered; mutex + no-bedrock gates fire before ResolveSandboxID |
| CODEX-05 (Plan 03) | SATISFIED | `loadProfileCLICodexArgs` exists; `runAgentNonInteractive` loads correct args by agent type |

### Anti-Patterns Found

None detected in modified files. No TODO/FIXME/placeholder comments, no empty return stubs, no console-log-only handlers in the changed code paths.

### Pre-existing Test Failure (Not Phase 58)

`TestUnlockCmd_RequiresStateBucket` fails in `./internal/app/cmd/` with `"sandbox sb-aabbccdd is not locked"` instead of expected state-bucket error message. Confirmed pre-existing: failure reproduces on commits prior to phase 58. This is not introduced by this phase.

### Human Verification

The operator-approved smoke test from 2026-04-19 (noted in 58-03-SUMMARY.md, sandbox `leaner-ef517a1b`) covered:

1. `make build` success
2. `./km validate profiles/learn.yaml` exits 0
3. `./km agent run sb-fakebox --claude --codex --prompt test` returns `--claude and --codex are mutually exclusive`
4. `./km agent run sb-fakebox --codex --no-bedrock --prompt test` returns `--no-bedrock is only valid with --claude`

These negative-path checks required no live AWS. The optional live codex SSM dispatch (step 4 in the plan) was run against sandbox `leaner-ef517a1b` and approved. No further human verification required.

### Commit History (Phase 58)

All 10 expected commits present:

- `7aecb72 test(58-01): add failing tests for CLISpec.CodexArgs`
- `a2e6311 feat(58-01): add spec.cli.codexArgs to CLISpec and JSON Schema`
- `ac664c4 docs(58-01): complete codexArgs profile schema field plan`
- `107b977 test(58-02): add failing tests for codex branch and AgentRunOptions signature`
- `97daba0 feat(58-02): refactor BuildAgentShellCommands to branch on agent type (claude|codex)`
- `d797bfe docs(58-02): complete BuildAgentShellCommands agent-type branching plan`
- `8ce9552 test(58-03): add failing tests for --claude/--codex flag pair and profile args loader`
- `61b7dfe feat(58-03): add --claude/--codex flags to km agent run with mutex and no-bedrock gating`
- `ad11d6c docs(58-03): complete CLI flag pair plan — checkpoint awaiting operator smoke test`
- `3b7e586 docs(58-03): complete --claude/--codex km agent run plan — smoke test approved`

---

_Verified: 2026-04-19T17:10:00Z_
_Verifier: Claude (gsd-verifier)_
