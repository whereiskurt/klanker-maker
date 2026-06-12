---
phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests
verified: 2026-06-12T02:39:42Z
status: passed
score: 6/6 must-haves verified
re_verification: false
---

# Phase 107: Reconcile 22 Stale internal/app/cmd Unit Tests — Verification Report

**Phase Goal:** Get `go test ./internal/app/cmd/ -count=1` to a clean, trustworthy green by reconciling 22 deterministically-failing unit tests whose assertions drifted out of sync with current production behavior. Test-hygiene ONLY — production behavior is the source of truth. One user-approved production escalation: shell pre-flight error fix.
**Verified:** 2026-06-12T02:39:42Z
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `go test ./internal/app/cmd/ -count=1` exits 0 with `ok` summary | VERIFIED | Full suite `ok github.com/whereiskurt/klanker-maker/internal/app/cmd 486.524s` EXIT=0 documented in 107-08-SUMMARY.md; `go vet` also exits 0; `go build ./...` exits 0 |
| 2 | All 22 previously-failing tests now pass (FAIL set 22 → 0) | VERIFIED | Spot-run of all 22 by name: 5 shell/escalation + 4 email + 3 uninit + 4 statebucket + 2 create + 4 misc — every test returns PASS (confirmed live in this verification) |
| 3 | Green-stays-green: TestRunInitPlan_ModuleOrder + 10 TestScoped* still pass | VERIFIED | `go test -run 'TestScoped\|TestRunInitPlan_ModuleOrder'` EXIT=0, 11 named PASSes, 0.920s (confirmed live) |
| 4 | Diff shape: exactly 13 `*_test.go` files + 1 `shell.go` changed, no other production code | VERIFIED | `git diff --stat d5cdffcd~1..HEAD -- internal/app/cmd/` shows exactly 14 files: 13 `*_test.go` + `shell.go`; filtering for non-test, non-shell production files returns empty |
| 5 | The shell.go change is isolated in its own commit and is the ONE sanctioned production fix | VERIFIED | Commit `8cefdf13` touches only `shell.go` + `VERSION`; adds `preflightError` sentinel type; wraps 5 pre-flight return sites; discriminates in RunE using `errors.As` |
| 6 | No stray production code changes across the 7 code-change commits | VERIFIED | `git diff --name-only d5cdffcd~1..HEAD -- internal/app/cmd/` filtered for non-test/non-shell production `.go` files: empty result |

**Score:** 6/6 truths verified

---

### Required Artifacts (Phase Commits)

| Commit | Artifact | Plan | Status | Details |
|--------|----------|------|--------|---------|
| `d5cdffcd` | `shell_docker_test.go` reconciled | 107-01 | VERIFIED | `bash --login` + always `-u sandbox` assertions match `execDockerShell` production output |
| `248a3458` | `email_test.go` SSM mock keys reconciled | 107-02 | VERIFIED | Mocks re-keyed to `/km/sandbox/...` prefix; 4 email tests PASS |
| `eb8118c5` | `uninit_test.go` wantOrder 19→22 | 107-03 | VERIFIED | 22-module reverse-order destruction verified live; 3 uninit tests PASS |
| `f81906cf` | `list_test.go`, `lock_test.go`, `unlock_test.go`, `status_test.go` | 107-04 | VERIFIED | DynamoDB-primary behavior asserted; tests renamed to reflect actual behavior; 4 tests PASS |
| `a4c4b0c5` | `create_docker_test.go`, `create_override_test.go` | 107-05 | VERIFIED | `PLACEHOLDER_OPERATOR_KEY` dropped; `runCreateRemote` signature grep updated; 2 tests PASS |
| `bc6c3a2c` | `agent_auth_test.go`, `at_test.go`, `shell_learn_test.go`, `init_test.go` (EFS) | 107-06 | VERIFIED | Learn-output `""` default; EFS err==nil; agent-auth `claude auth status` route; at-list future time; 4 tests PASS |
| `8cefdf13` | `shell.go` — `preflightError` sentinel type + RunE discrimination | 107-07 | VERIFIED | Isolated commit; `preflightError` wraps 5 pre-flight returns; `errors.As` in RunE; 3 shell escalation tests PASS |
| (107-08) | `107-08-SUMMARY.md` — green gate evidence | 107-08 | VERIFIED | Full-suite EXIT=0, `grep -c "^--- FAIL"` = 0, diff-shape confirmed |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `shell_docker_test.go` assertions | `shell.go:execDockerShell` command string | grep match `bash --login` | VERIFIED | Test asserts the exact command string that `execDockerShell` produces |
| `uninit_test.go:TestUninitDestroyOrder` wantOrder | `init.go:regionalModules()` count | 22-module reverse slice | VERIFIED | Uninit test destroys in reverse of the 22-module `regionalModules()` list; TestRunInitPlan_ModuleOrder also confirms count = 22 |
| `shell.go:preflightError` | `shell.go:RunE errors.As` | `errors.As(runErr, &pf)` | VERIFIED | Pre-flight errors propagate; session-exit errors still swallowed; TestShellCmd_MissingSSMDoc (session-exit) remains green |
| Email test SSM keys | `email.go` SSM path construction | `/km/sandbox/{id}/signing-key` prefix | VERIFIED | 4 email tests PASS confirming the mock keys match production paths |

---

### Requirements Coverage

All 9 requirement IDs from the ROADMAP are covered by plans with `requirements:` declarations. REQUIREMENTS.md has no phase-107 entries (requirements are defined inline in ROADMAP.md).

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| TEST-HYGIENE-SHELL | 107-01 | Docker-shell assertions reconciled (`bash --login`, always `-u sandbox`) | SATISFIED | `TestShellDockerContainerName`, `TestShellDockerNoRootFlag` PASS |
| TEST-HYGIENE-EMAIL | 107-02 | Email SSM mock keys re-keyed to `/km/sandbox/...` prefix | SATISFIED | 4 email tests PASS |
| TEST-HYGIENE-UNINIT | 107-03 | Uninit wantOrder updated 19→22, two count consts bumped | SATISFIED | 3 uninit tests PASS |
| TEST-HYGIENE-STATEBUCKET | 107-04 | List/lock/unlock/status empty-bucket assertions reconciled to DynamoDB-primary | SATISFIED | 4 statebucket tests PASS (with renamed test IDs) |
| TEST-HYGIENE-CREATE | 107-05 | Create tests: drop PLACEHOLDER_OPERATOR_KEY, update runCreateRemote signature grep | SATISFIED | `TestCreateDockerWritesComposeFile`, `TestApplyLifecycleOverrides_RunCreateRemoteSignature` PASS |
| TEST-HYGIENE-MISC | 107-06 | agent-auth, at-list, learn-output, EFS reconciled | SATISFIED | 4 misc tests PASS |
| TEST-HYGIENE-SHELL-FIX | 107-07 | ONE approved production fix: `preflightError` sentinel in `shell.go` RunE | SATISFIED | `TestShellCmd_StoppedSandbox`, `_UnknownSubstrate`, `_MissingInstanceID` PASS; isolated in own commit `8cefdf13` |
| TEST-HYGIENE-GREEN | 107-08 | Full suite EXIT=0, FAIL set 22→0, diff-shape clean | SATISFIED | `ok ... 486.524s` EXIT=0, `grep -c "^--- FAIL"` = 0 documented in summary |
| TEST-HYGIENE-TRIAGE | 107-08 | Diff-shape guardrail: only `*_test.go` + sanctioned `shell.go` | SATISFIED | `git diff --stat` shows 13 test files + 1 shell.go; no other production code |

---

### Anti-Patterns Found

None. Scanned `shell.go` and all modified test files:

- No `TODO`/`FIXME`/`PLACEHOLDER` comments in `shell.go`
- `go vet ./internal/app/cmd/` exits 0
- `go build ./...` exits 0
- The `preflightError` type is a proper Go error sentinel (implements `Error()` and `Unwrap()`), not a stub
- No `return nil` replacing a real error path — the fix adds error propagation, not suppression

---

### Human Verification Required

None. All goal criteria are programmatically verifiable and confirmed:

- Test pass/fail status is deterministic (no real AWS calls; all mocked)
- Diff shape is verifiable via `git diff --stat`
- Production fix correctness is verifiable by reading the `preflightError` sentinel pattern and the 3 test assertions that exercise it

---

### Gaps Summary

No gaps. All 6 observable truths verified. All 9 requirement IDs satisfied. All 8 plan commits confirmed in git history. The `internal/app/cmd` suite is in a trustworthy-green state.

**Memory update warranted:** The `project_cmd_suite_pre_existing_failures` memory entry ("22 deterministic pre-existing failures") is now stale — the suite baseline is 0 failures. Recommend updating that entry after operator sign-off.

---

_Verified: 2026-06-12T02:39:42Z_
_Verifier: Claude (gsd-verifier)_
