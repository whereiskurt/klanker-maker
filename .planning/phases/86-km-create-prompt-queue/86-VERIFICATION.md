---
phase: 86-km-create-prompt-queue
verified: 2026-05-19T23:45:00Z
status: human_needed
score: 8/8 code-level must-haves verified
human_verification:
  - test: "km create profiles/learn.yaml --prompt 'echo hello' --wait"
    expected: "Exit 0; km agent list <sb> shows 1 run; output contains 'hello'"
    why_human: "Requires real EC2 sandbox provisioning + Bedrock IAM + SSM agent (PQ-09)"
  - test: "km create profiles/learn.yaml --prompt @plan.txt --prompt 'publish step' --wait"
    expected: "Both runs visible in order, --wait exits 0, second start time > first end time"
    why_human: "Real systemd timing + linear chain execution order requires live sandbox (PQ-10)"
  - test: "km create profiles/learn.yaml --prompt 'exit 1' --prompt 'should not run' (no --wait); wait 2 min; km agent list --queue <sb>"
    expected: "001=failed, 002=skipped"
    why_human: "Fail-stops-chain requires on-box runner processing real exit codes (PQ-11)"
  - test: "km create profiles/learn.yaml --prompt 'sleep 300'; km pause <sb>; km resume <sb>; verify runner reconciles running->pending"
    expected: "Entry 001 resets to pending after resume; runner re-executes to done"
    why_human: "Requires real EC2 stop/start cycle and systemd auto-restart on resume (PQ-12)"
  - test: "km create profiles/learn.yaml --no-bedrock --prompt 'tell me your model'; confirm queue stays pending; km shell <sb>; claude auth login; confirm queue drains"
    expected: "Queue stays pending indefinitely pre-auth; drains within ~10s post-auth"
    why_human: "Requires interactive claude auth login browser flow inside live sandbox (PQ-13)"
  - test: "km create profiles/learn.yaml (no --prompt); km shell <sb>; verify systemctl is-active km-queue.service"
    expected: "No queue dir; no runs; service active-idle; CPU near 0%"
    why_human: "Regression check against full sandbox lifecycle requires real EC2 (R1)"
---

# Phase 86: km-create-prompt-queue Verification Report

**Phase Goal:** Add repeatable `--prompt <text-or-@file>` to `km create` that queues prompts on-box at `/workspace/.km-agent/queue/` and drains them sequentially once Claude auth is available. Composes existing `km agent run` primitives — no schema/Lambda/Terragrunt changes. Linear chain semantics: indefinite auth wait, fail-stops-chain, remaining marked `skipped`. Add `km agent list --queue` view.

**Verified:** 2026-05-19T23:45:00Z
**Status:** human_needed
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `--prompt` flag registered on `km create` as repeatable StringArrayVar | VERIFIED | `create.go:264` `StringArrayVar(&prompts, "prompt", nil, ...)`. `TestCreatePromptFlag` PASS. |
| 2 | `@file` reads file UTF-8; `@@` escapes literal `@`; missing file errors before AWS | VERIFIED | `create_prompt.go:82-102` `resolvePrompts`. `TestResolvePrompts` (6 subtests) PASS. |
| 3 | `--prompt + --docker` hard-fails before any provisioning | VERIFIED | `create.go:150-153` rejects before provisioning. `TestCreatePromptDockerReject` (2 subtests) PASS. S1 smoke test PASS. |
| 4 | SSM queue-file push sends correct base64 content + meta.json structure | VERIFIED | `create_prompt.go:127-159` `pushQueueFiles`. `TestPushQueueFiles` PASS. |
| 5 | `--wait` polls meta.json until all `done`, exits 0 | VERIFIED | `create_prompt.go:233-298` `waitForQueueDrain`. `TestCreatePromptWait` PASS. |
| 6 | `--wait` exits non-zero when first prompt fails; remaining marked `skipped` | VERIFIED | `waitForQueueDrain` returns `ExitCodeError`; root.go:144-146 `errors.As` detection; `TestCreatePromptWaitFail` PASS. |
| 7 | `km agent list --queue` shows queue entries with status | VERIFIED | `agent.go:844` `BoolVar(&queueView, "queue")`; `runAgentListQueue` at line 1107. `TestAgentListQueue` (3 subtests) PASS. |
| 8 | Queue runner bash: reconcile `running`→`pending` on start | VERIFIED | `create_prompt.go:305-310` `ReconcileMetaStatus`; bash runner heredoc in `userdata.go:1891+`. Bash test harness 7/7 PASS. |
| 9 | PQ-09..PQ-13, R1: real-AWS operator UAT | HUMAN NEEDED | AWS SSO expired; blocker is operator-environment only, not a code gap. 5/5 autonomous smoke tests PASS. |

**Score:** 8/8 code-level must-haves verified. 6/6 operator UAT scenarios human_needed (environment blocker, not code gap).

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/cmd/create_prompt.go` | resolvePrompts, pushQueueFiles, kickQueueRunner, doStep16PromptPush, ExitCodeError, waitForQueueDrain, ReconcileMetaStatus | VERIFIED | 378 lines, all functions present and substantive |
| `internal/app/cmd/create.go` | `--prompt` StringArrayVar, `--wait` BoolVar, `--docker` mutex, Step 16 hook | VERIFIED | Lines 264-267 flag registration; lines 150-207 mutex + hook |
| `internal/app/cmd/create_prompt_test.go` | 9 tests for PQ-01..PQ-08 | VERIFIED | 494 lines, 9 test functions, all PASS |
| `internal/app/cmd/agent.go` | `--queue` BoolVar, `runAgentListQueue` | VERIFIED | Line 844 flag; line 1107 implementation |
| `internal/app/cmd/agent_test.go` | `TestAgentListQueue` | VERIFIED | 3 subtests: flag_registered, empty_queue, mixed_status — all PASS |
| `pkg/compiler/userdata.go` | km-queue-runner heredoc + km-queue.service heredoc with Restart=on-failure + systemctl enable | VERIFIED | Lines 1891-2110; heredoc present; Restart=on-failure at line 2101 |
| `pkg/profile/configfiles/km-queue-runner_test.sh` | 7 bash tests | VERIFIED | 16535 bytes, 7/7 PASS (0 FAIL) |
| `CLAUDE.md` | Updated bullets for `km create --prompt` and `km agent list --queue` | VERIFIED | Line 31 `--prompt` and `--wait`; line 42 `--queue` |
| `OPERATOR-GUIDE.md` | `### km create --prompt` section | VERIFIED | Line 389 section present with usage, recovery procedure |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `create.go` RunE | `doStep16PromptPush` | `create_prompt.go:328` | WIRED | Lines 203-207 and 218-226 call doStep16PromptPush post-provision |
| `doStep16PromptPush` | `pushQueueFiles` | `create_prompt.go:352-358` | WIRED | Calls pushQueueFiles then kickQueueRunner |
| `doStep16PromptPush` | `waitForQueueDrain` | `create_prompt.go:365` | WIRED | Called when wait=true |
| `waitForQueueDrain` failure | `ExitCodeError` return | `create_prompt.go:377` | WIRED | Returns `&ExitCodeError{Code: exitCode}` |
| `ExitCodeError` | `root.go` os.Exit boundary | `errors.As(err, &exitErr)` at root.go:145 | WIRED | Detected and translated to os.Exit(exitErr.Code) |
| `agent.go` list cmd | `runAgentListQueue` | `agent.go:838` | WIRED | Branched when queueView=true |
| `userdata.go` heredoc | `km-queue-runner` + `km-queue.service` | Inline heredoc lines 1893/2092 | WIRED | Runner installed at /opt/km/bin; service at /etc/systemd/system; `systemctl enable km-queue` at line 2109 |

---

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| PQ-01 | 86-02 | `--prompt` repeatable StringArrayVar on km create | SATISFIED | `create.go:264`, TestCreatePromptFlag PASS |
| PQ-02 | 86-02 | `@file` reads UTF-8; `@@` escapes; missing file errors before AWS | SATISFIED | `resolvePrompts` in create_prompt.go:82-102, TestResolvePrompts 6/6 PASS, S2/S3/S4 smoke tests PASS |
| PQ-03 | 86-02 | `--prompt + --docker` hard-fail before provisioning | SATISFIED | `create.go:150-153`, TestCreatePromptDockerReject 2/2 PASS, S1 smoke PASS |
| PQ-04 | 86-02 | SSM push sends base64 content + meta.json structure | SATISFIED | `pushQueueFiles` in create_prompt.go:127-159, TestPushQueueFiles PASS |
| PQ-05 | 86-04 | `--wait` polls until all done; exits 0 | SATISFIED | `waitForQueueDrain`, TestCreatePromptWait PASS |
| PQ-06 | 86-04 | `--wait` exits non-zero on failure; remaining skipped | SATISFIED | ExitCodeError + root.go boundary, TestCreatePromptWaitFail PASS |
| PQ-07 | 86-05 | `km agent list --queue` shows queue entries with status | SATISFIED | `agent.go:844+1107`, TestAgentListQueue 3/3 PASS |
| PQ-08 | 86-03 | Runner bash: reconcile running->pending on start | SATISFIED | `ReconcileMetaStatus`, bash harness 7/7 PASS, userdata.go heredoc seeding |
| PQ-09 | 86-06 | Single-prompt happy path (real-AWS) | HUMAN NEEDED | AWS SSO expired; code path verified by unit tests and autonomous smoke tests |
| PQ-10 | 86-06 | Two-prompt linear chain (real-AWS) | HUMAN NEEDED | Requires live sandbox |
| PQ-11 | 86-06 | Fail-stops-chain (real-AWS) | HUMAN NEEDED | Requires live sandbox |
| PQ-12 | 86-06 | Pause/resume reconcile (real-AWS) | HUMAN NEEDED | Requires EC2 stop/start cycle |
| PQ-13 | 86-06 | Direct-API indefinite auth wait (real-AWS) | HUMAN NEEDED | Requires interactive claude auth login in sandbox |
| R1 | 86-06 | Regression: no --prompt = unchanged behavior (real-AWS) | HUMAN NEEDED | On-box check of queue dir absence and service idle state |

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| None found | — | — | — | — |

No TODO/FIXME/PLACEHOLDER comments, no empty implementations, no stub returns found in the Phase 86 implementation files.

---

### Human Verification Required

The following 6 scenarios require a live AWS sandbox and operator-in-loop. The blocker is AWS SSO token expiry in the autonomous session — not a code gap. All code paths are exercised by unit tests and autonomous smoke tests.

**Pre-requisite:** `aws sso login --profile klanker-terraform`

#### 1. PQ-09 — Single-prompt happy path

**Test:** `./km create profiles/learn.yaml --prompt "echo hello" --wait`
**Expected:** Exit 0; `km agent list <sb>` shows 1 run; `km agent results <sb>` output contains `hello`
**Why human:** Requires real EC2 provisioning, Bedrock IAM, SSM agent, and on-box runner

#### 2. PQ-10 — Two-prompt linear chain

**Test:** `./km create profiles/learn.yaml --prompt @plan.txt --prompt "publish step complete" --wait`
**Expected:** `km agent list <sb> --queue` shows 001=done, 002=done; second run start time > first end time
**Why human:** Linear chain ordering depends on real systemd timing and runner execution

#### 3. PQ-11 — Fail-stops-chain

**Test:** `./km create profiles/learn.yaml --prompt "exit 1" --prompt "should not run"` (no `--wait`); after ~2 min: `./km agent list <sb> --queue`
**Expected:** 001=failed, 002=skipped; only 1 run in `km agent list <sb>` (not 2)
**Why human:** On-box runner must process real exit codes and mark skipped via bash state machine

#### 4. PQ-12 — Pause/resume reconcile

**Test:** `./km create profiles/learn.yaml --prompt "sleep 300; echo done"`; wait 30s; `./km pause <sb>`; `./km resume <sb>`; wait 60s; `./km agent list <sb> --queue`
**Expected:** 001 resets from running to pending after resume; eventually reaches done after ~5 more minutes
**Why human:** Requires real EC2 hibernate/resume cycle and systemd auto-restart behavior

#### 5. PQ-13 — Direct-API indefinite auth wait

**Test:** `./km create profiles/learn.yaml --no-bedrock --prompt "tell me your model"`; confirm 001=pending; `./km shell <sb>`; `claude auth login` (browser OAuth); exit; 10s later `./km agent list <sb> --queue`
**Expected:** Queue stays pending pre-auth; drains to done within ~10s post-auth
**Why human:** Requires interactive browser OAuth flow inside sandbox shell

#### 6. R1 — Regression: km create without --prompt

**Test:** `./km create profiles/learn.yaml` (no --prompt); `./km shell <sb>`; `systemctl is-active km-queue.service`; `ls /workspace/.km-agent/queue/`
**Expected:** No queue dir; no runs; `km-queue.service` active (idle-polling, CPU ~0%); all pre-Phase-86 commands unchanged
**Why human:** Requires full sandbox lifecycle on real EC2 with systemd check

---

## Verification Summary

Phase 86 achieved its code-level goal completely. All 8 PQ requirements with automated verification (PQ-01 through PQ-08) are SATISFIED:

- The `--prompt` flag is registered as a repeatable `StringArrayVar` on `km create`
- `@file` resolution, `@@` escape, and missing-file error are implemented and tested
- The `--docker` mutex rejects before any AWS call
- SSM queue-file push sends correctly base64-encoded `.prompt` and `.meta.json` files
- `--wait` polling loops until all terminal, exits 0 on all-done, non-zero with first-failed exit code
- The `ExitCodeError` typed pattern is wired through RunE to root.go's cobra boundary
- `km agent list --queue` dispatches to `runAgentListQueue` listing queue entries with status
- The bash runner reconcile (`running`→`pending`) is tested by 7/7 bash harness tests
- The on-box runner + systemd unit are seeded unconditionally via `userdata.go` heredoc with `Restart=on-failure`

The remaining 6 requirements (PQ-09..PQ-13, R1) are operator UAT scenarios blocked exclusively by AWS SSO token expiry — a pure operator-environment issue, not a code gap. The UAT runbook (`86-06-UAT.md`) is complete with commands, expected output, and evidence placeholders ready for operator execution.

`make build` succeeds (km v0.2.710). 10/10 Go unit tests PASS. 7/7 bash harness tests PASS. 5/5 autonomous smoke tests PASS.

---

_Verified: 2026-05-19T23:45:00Z_
_Verifier: Claude (gsd-verifier)_
