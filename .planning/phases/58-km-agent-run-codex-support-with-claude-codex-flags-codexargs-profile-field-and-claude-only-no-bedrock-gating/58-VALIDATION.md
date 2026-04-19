---
phase: 58
slug: km-agent-run-codex-support
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-19
---

# Phase 58 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` package (standard library) |
| **Config file** | none — uses `go test` defaults |
| **Quick run command** | `go test ./internal/app/cmd/ -run TestAgent -v && go test ./pkg/profile/ -run TestCLISpec -v` |
| **Full suite command** | `go test ./internal/app/cmd/... ./pkg/profile/...` |
| **Estimated runtime** | ~15 seconds (quick), ~240 seconds (full, includes pre-existing suite) |

---

## Sampling Rate

- **After every task commit:** Run quick command (~15s)
- **After every plan wave:** Run full suite command
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds (quick), 240 seconds (full)

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|----------|-----------|-------------------|-------------|--------|
| 58-01-01 | 01 | 1 | `spec.cli.codexArgs` parses from YAML | unit | `go test ./pkg/profile/ -run TestCLISpec_CodexArgs -v` | ❌ W0 new | ⬜ pending |
| 58-01-02 | 01 | 1 | `spec.cli.codexArgs` optional (nil when absent) | unit | `go test ./pkg/profile/ -run TestCLISpec_CodexArgsOptional -v` | ❌ W0 new | ⬜ pending |
| 58-01-03 | 01 | 1 | JSON Schema accepts `cli.codexArgs` | unit | `./km validate profiles/learn.yaml` (with codexArgs added) | ✅ via build | ⬜ pending |
| 58-02-01 | 02 | 2 | `BuildAgentShellCommands` emits codex invocation with `--json` and `--dangerously-bypass-approvals-and-sandbox` | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_CommandConstruction -v` | ✅ extend | ⬜ pending |
| 58-02-02 | 02 | 2 | Claude path unchanged (regression) | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_CommandConstruction -v` | ✅ exists | ⬜ pending |
| 58-02-03 | 02 | 2 | `--no-bedrock` unset stanza absent from codex script | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_NoBedrock -v` | ✅ extend | ⬜ pending |
| 58-02-04 | 02 | 2 | `codexArgs` from profile plumbed into codex shell invocation | unit | `go test ./internal/app/cmd/ -run TestBuildAgentShellCommands_Codex -v` | ❌ W0 new | ⬜ pending |
| 58-03-01 | 03 | 3 | `km agent run --codex --prompt "..."` fires codex non-interactively | unit | `go test ./internal/app/cmd/ -run TestAgentRun_CodexFlag -v` | ❌ W0 new | ⬜ pending |
| 58-03-02 | 03 | 3 | `km agent run --codex --no-bedrock` returns error before SSM call | unit | `go test ./internal/app/cmd/ -run TestAgentRun_CodexNoBedrockError -v` | ❌ W0 new | ⬜ pending |
| 58-03-03 | 03 | 3 | `km agent run --claude --codex` (both set) returns error | unit | `go test ./internal/app/cmd/ -run TestAgentRun_ClaudeCodexMutex -v` | ❌ W0 new | ⬜ pending |
| 58-03-04 | 03 | 3 | Default (no flag) still invokes claude (backward compat) | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_SendCommand -v` | ✅ exists | ⬜ pending |
| 58-03-05 | 03 | 3 | `loadProfileCLICodexArgs` helper returns codex args from profile | unit | `go test ./internal/app/cmd/ -run TestLoadProfileCLICodexArgs -v` | ❌ W0 new | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Wave 0 in this phase is embedded in the RED step of each plan's TDD cycle (no separate install phase). New test functions needed:

- [ ] `pkg/profile/types_test.go` — add `TestCLISpec_CodexArgsParsesFromYAML` + `TestCLISpec_CodexArgsOptional` (mirror of existing ClaudeArgs tests)
- [ ] `internal/app/cmd/agent_test.go` — extend `TestAgentNonInteractive_CommandConstruction` with codex sub-case
- [ ] `internal/app/cmd/agent_test.go` — extend `TestAgentNonInteractive_NoBedrock` with "codex path has no unset stanza"
- [ ] `internal/app/cmd/agent_test.go` — add `TestBuildAgentShellCommands_Codex` (multiple sub-cases: base codex invocation, with codexArgs, no --no-bedrock contamination)
- [ ] `internal/app/cmd/agent_test.go` — add `TestAgentRun_CodexFlag` (full cmd path through RunE, asserts SendCommand script contains codex)
- [ ] `internal/app/cmd/agent_test.go` — add `TestAgentRun_CodexNoBedrockError` (asserts error before any SSM call)
- [ ] `internal/app/cmd/agent_test.go` — add `TestAgentRun_ClaudeCodexMutex` (both flags set → error)
- [ ] `internal/app/cmd/agent_test.go` — add `TestLoadProfileCLICodexArgs` if helper is added (skip if consolidated into `loadProfileCLIAgentArgs(agent)`)

No framework install needed — Go `testing` is stdlib and already used pervasively.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Real codex execution inside a live sandbox produces expected JSONL output | phase-goal success criterion #1 | Requires AWS + provisioned sandbox + codex auth | After implementation: `km create profiles/learn.yaml --alias codex-test`; `km agent run codex-test --codex --prompt "list files in /workspace" --wait`; verify `output.json` contains JSONL events |
| `codexArgs: ["--model", "o4-mini"]` in profile actually invokes o4-mini | phase-goal success criterion #5 | Requires live codex + OpenAI auth | Add codexArgs to a test profile, fire a prompt, verify from codex's JSONL event log that the model used matches |

---

## Validation Sign-Off

- [ ] All tasks have automated `<verify>` commands or Wave 0 test file dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 new tests listed above all exist before any production code for that task
- [ ] No watch-mode flags in any verify command
- [ ] Feedback latency < 15s for quick command
- [ ] `nyquist_compliant: true` set in frontmatter after all Wave 0 gaps closed and tests are green

**Approval:** pending
