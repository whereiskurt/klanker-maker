---
phase: 86-km-create-prompt-queue
plan: "01"
subsystem: testing
tags: [tdd, red-state, stubs, nyquist]
dependency_graph:
  requires: []
  provides:
    - RED-state test stubs for PQ-01..PQ-08 (Nyquist gate satisfied)
    - internal/app/cmd/create_prompt_test.go
    - internal/app/cmd/agent_test.go (TestAgentListQueue appended)
    - pkg/profile/configfiles/km-queue-runner_test.sh
  affects:
    - Wave 1 (86-02): implements against TestCreatePromptFlag, TestResolvePrompts, TestCreatePromptDockerReject
    - Wave 1 (86-03): implements against TestPushQueueFiles, TestCreatePromptWait, TestCreatePromptWaitFail
    - Wave 3 (86-05): implements against TestAgentListQueue
    - Wave 2 (86-04): implements against TestQueueRunnerStateMachine + bash harness
tech_stack:
  added: []
  patterns:
    - Go table-driven tests with t.Skip Wave-N markers
    - cmd_test package with shared mock types (mockAgentSSM, fakeFetcher)
    - Plain bash + exit-code-based test harness (no bats/shellspec dependency)
key_files:
  created:
    - internal/app/cmd/create_prompt_test.go (227 lines)
    - pkg/profile/configfiles/km-queue-runner_test.sh (139 lines)
  modified:
    - internal/app/cmd/agent_test.go (+131 lines, TestAgentListQueue appended)
    - .planning/phases/86-km-create-prompt-queue/86-VALIDATION.md (nyquist_compliant + wave_0_complete flipped to true)
decisions:
  - "Used package cmd_test (not package cmd) to match all other test files in the directory"
  - "Used t.Skip with Wave-N markers instead of compile-guard stubs — cleaner and matches go test SKIP semantics"
  - "create_prompt_test.go named separately from create_test.go (source-check tests) to isolate Phase 86 stubs"
  - "bash harness uses plain bash + exit-code pattern (no external tool dependency per CONTEXT.md discretion)"
metrics:
  duration: "633 seconds (~10.5 min)"
  completed: "2026-05-20T02:49:37Z"
  tasks: 3
  files_created: 2
  files_modified: 2
---

# Phase 86 Plan 01: RED-State Test Stubs (Wave 0) Summary

Nyquist gate satisfied: RED-state test stubs created for every Phase 86 acceptance behavior (PQ-01..PQ-08) before implementation begins.

## One-liner

RED-state Go stubs + bash harness covering all 8 PQ acceptance behaviors, locking the test surface for Waves 1-4 via `t.Skip("Wave N: ...")` markers.

## What Was Done

### Task 1: create_prompt_test.go (PQ-01..PQ-06, PQ-08)

New file `internal/app/cmd/create_prompt_test.go` (227 lines) in `package cmd_test`. Contains 7 test functions:

| Test | PQ ID | What it asserts |
|------|-------|-----------------|
| TestCreatePromptFlag | PQ-01 | `--prompt` is StringArrayVar, repeatable; `--wait` is BoolVar |
| TestResolvePrompts | PQ-02 | `@file` read, `@@` escape, missing-file error with path in message |
| TestCreatePromptDockerReject | PQ-03 | `--prompt + --docker` returns error containing "queue requires EC2" |
| TestPushQueueFiles | PQ-04 | One SSM call, base64 prompts, `001.prompt` / `002.prompt`, meta.json shape |
| TestCreatePromptWait | PQ-05 | waitForQueueDrain returns (0, nil) when all entries reach "done" |
| TestCreatePromptWaitFail | PQ-06 | waitForQueueDrain returns non-zero + error on first failure |
| TestQueueRunnerStateMachine | PQ-08 | reconcileEntry table: running->pending, done->done, failed->failed, etc. |

All skip with `t.Skip("Wave 1/2: ... not yet implemented")`.

### Task 2: agent_test.go augmented (PQ-07)

`TestAgentListQueue` appended to `internal/app/cmd/agent_test.go` (+131 lines). Three sub-tests:
- `flag_registered` — `--queue` is BoolVar, defaults to false
- `empty_queue` — no `NNN|status|` rows in empty-dir output
- `mixed_status` — 5 entries, all 5 status labels present, 001 before 002, prompt preview <=80 chars

All skip with `t.Skip("Wave 3: --queue flag on agent list not yet implemented")`.

### Task 3: km-queue-runner_test.sh (PQ-08 bash-side)

New file `pkg/profile/configfiles/km-queue-runner_test.sh` (139 lines), executable. Self-contained plain bash harness with 7 test functions covering runner state machine and auth probe behaviors. All return SKIP in Wave 0.

```
Results: 7 SKIP, 0 PASS, 0 FAIL
Exit: 0
```

## Verification Results

```
go vet ./internal/app/cmd/            → exit 0
make build                            → Built km v0.2.693 (30caa70)
TestCreatePromptFlag                  → SKIP (Wave 1)
TestResolvePrompts                    → SKIP (Wave 1)
TestCreatePromptDockerReject          → SKIP (Wave 1)
TestPushQueueFiles                    → SKIP (Wave 1)
TestCreatePromptWait                  → SKIP (Wave 1)
TestCreatePromptWaitFail              → SKIP (Wave 1)
TestQueueRunnerStateMachine           → SKIP (Wave 2)
TestAgentListQueue                    → SKIP (Wave 3)
bash km-queue-runner_test.sh          → 7 SKIP, 0 PASS, 0 FAIL, exit 0
```

Pre-existing failures (TestProbeCodexPort_Primary, TestStep11d_Success_WritesChannelIDParam, TestAtList_WithRecords) verified to exist before Phase 86 changes — out of scope.

## VALIDATION.md Update

`86-VALIDATION.md` frontmatter updated:
- `nyquist_compliant: false` → `nyquist_compliant: true`
- `wave_0_complete: false` → `wave_0_complete: true`
- Approval status flipped to approved

## Deviations from Plan

None — plan executed exactly as written.

The plan noted "Use `package cmd` (same as existing tests)" but existing tests are `package cmd_test` (external test package). Used `package cmd_test` for consistency with the rest of the test suite. This is consistent with the plan's intent (reuse existing fakes).

## Commits

| Hash | Message |
|------|---------|
| 3a0c606 | test(86-01): add RED-state stubs for PQ-01..PQ-06 + PQ-08 |
| 55c95a5 | test(86-01): append TestAgentListQueue RED-state stub (PQ-07) |
| 30caa70 | test(86-01): add bash harness skeleton for runner state machine (PQ-08) |

## Self-Check

Files created:
- `internal/app/cmd/create_prompt_test.go` — exists, 227 lines
- `pkg/profile/configfiles/km-queue-runner_test.sh` — exists, 139 lines, executable
- `internal/app/cmd/agent_test.go` — modified, now 1480 lines

Commits:
- 3a0c606, 55c95a5, 30caa70 — all verified in git log

## Self-Check: PASSED
