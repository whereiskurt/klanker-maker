---
phase: 76-km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox
plan: 00
subsystem: testing
tags: [vscode, ssh, ed25519, keypair, rekey, tdd, stubs]

# Dependency graph
requires:
  - phase: 73-km-vscode-remote-ssh
    provides: vscode.go with vsCodeSSMMock, vsCodeFetcherMock, captureStdout, healthySSMOutput test infrastructure
provides:
  - 16 TestVSCodeRekey_* stub functions in vscode_test.go (all SKIP, 0 FAIL, 0 PASS)
  - Locked symbol surface for Wave 1+2: newVSCodeRekeyCmd, runVSCodeRekey, pubkeyFingerprint
affects:
  - 76-01-PLAN (Wave 1 — uncomments CommandRegistered, FlagsExist, NotRunning, Locked_*, VSCodeDisabled, Inconsistent, SSHDDown)
  - 76-02-PLAN (Wave 2 — uncomments NormalRotation, CrossLaptop, VerifyMismatch, RenameOrdering, OverwritesScratch, YesFlag, ConfirmNo, OutputMarkers)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "t.Skip('TODO Wave N (Plan 76-NN): ...') stub pattern for pre-locking test surface before implementation"
    - "All real assertions in commented-out form below t.Skip — uncomment-to-implement for Wave 1/2"

key-files:
  created: []
  modified:
    - internal/app/cmd/vscode_test.go

key-decisions:
  - "All 16 test stubs use commented-out bodies (not live code) to ensure compile-time safety while preserving the full assertion spec for Wave 1/2 implementors"
  - "Removed _ = var blanks from stub bodies since variable declarations are also commented-out — avoids undefined-variable vet errors"
  - "Sequenced SSM mock (for NormalRotation, CrossLaptop, etc.) documented in commented form inside each test body rather than as shared infrastructure, per 76-RESEARCH.md recommendation"

patterns-established:
  - "Wave 0 stub pattern: t.Skip first, then commented assertions referencing locked symbol surface"
  - "Lock bypass injection: package-level var checkSandboxLock = CheckSandboxLock, overridden in tests"

requirements-completed:
  - REKEY-TESTSTUBS

# Metrics
duration: 5min
completed: 2026-05-10
---

# Phase 76 Plan 00: Wave 0 Stub Seeding Summary

**16 TestVSCodeRekey_* test stubs appended to vscode_test.go — all SKIP, none FAIL, locking newVSCodeRekeyCmd/runVSCodeRekey/pubkeyFingerprint symbol surface for Wave 1+2 implementation**

## Performance

- **Duration:** 5 min
- **Started:** 2026-05-10T01:41:52Z
- **Completed:** 2026-05-10T01:46:20Z
- **Tasks:** 1
- **Files modified:** 1

## Accomplishments
- Appended all 16 `TestVSCodeRekey_*` stub functions to `internal/app/cmd/vscode_test.go` after `TestVSCodeStatus_Healthy`
- All 16 new stubs report SKIP (not FAIL, not PASS) — suite stays green
- Existing 5 `TestVSCodeStart_*` and `TestVSCodeStatus_*` tests remain PASS with no regressions
- Symbol surface locked: `newVSCodeRekeyCmd`, `runVSCodeRekey`, `pubkeyFingerprint` referenced in commented-out form

## Task Commits

Each task was committed atomically:

1. **Task 1: Append all 16 TestVSCodeRekey_* stubs to vscode_test.go** - `e1a5043` (test)

**Plan metadata:** (docs commit — recorded below after state update)

## Files Created/Modified
- `internal/app/cmd/vscode_test.go` - 544 lines added: 16 TestVSCodeRekey_* stub test functions

## Stub Functions Added

| # | Function | Wave | Plan |
|---|----------|------|------|
| 1 | TestVSCodeRekey_CommandRegistered | 1 | 76-01 |
| 2 | TestVSCodeRekey_FlagsExist | 1 | 76-01 |
| 3 | TestVSCodeRekey_NotRunning | 1 | 76-01 |
| 4 | TestVSCodeRekey_Locked_NoForce | 1 | 76-01 |
| 5 | TestVSCodeRekey_Locked_WithForce | 1 | 76-01 |
| 6 | TestVSCodeRekey_VSCodeDisabled | 1 | 76-01 |
| 7 | TestVSCodeRekey_Inconsistent | 1 | 76-01 |
| 8 | TestVSCodeRekey_SSHDDown | 1 | 76-01 |
| 9 | TestVSCodeRekey_NormalRotation | 2 | 76-02 |
| 10 | TestVSCodeRekey_CrossLaptop | 2 | 76-02 |
| 11 | TestVSCodeRekey_VerifyMismatch | 2 | 76-02 |
| 12 | TestVSCodeRekey_RenameOrdering | 2 | 76-02 |
| 13 | TestVSCodeRekey_OverwritesScratch | 2 | 76-02 |
| 14 | TestVSCodeRekey_YesFlag | 2 | 76-02 |
| 15 | TestVSCodeRekey_ConfirmNo | 2 | 76-02 |
| 16 | TestVSCodeRekey_OutputMarkers | 2 | 76-02 |

## Decisions Made

- Removed `_ = var` suppression blanks from stub bodies since the variables they referenced were in commented-out code — would have caused `undefined: var` vet errors.
- All stub bodies are entirely commented-out (no live code other than `t.Skip`), ensuring `go build ./...` and `go vet ./...` stay clean without needing any new production symbols.
- Sequenced SSM mock pattern documented inline within each test body (commented) rather than promoted to shared helper — per 76-RESEARCH.md recommendation to keep two-mock-instances approach simpler for Wave 2 authors.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Removed `_ = var` blank identifiers referencing commented-out variables**
- **Found during:** Task 1 (go vet)
- **Issue:** Initial stub implementation included `_ = ctx`, `_ = cfg`, etc. at the bottom of each test body. Since those variables were declared only in commented-out code, `go vet` reported "undefined: ctx/cfg/fetcher" etc.
- **Fix:** Removed all `_ = var` lines from stub bodies — the `t.Skip` at the top means execution never reaches them anyway; they were unnecessary.
- **Files modified:** `internal/app/cmd/vscode_test.go`
- **Verification:** `go build ./...` and `go vet ./internal/app/cmd/...` both exit 0
- **Committed in:** `e1a5043` (part of task commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 — compile error from _ = undefined-var)
**Impact on plan:** Necessary for correctness (vet errors = CI failure). No scope creep.

## Issues Encountered
- Initial vet run caught `undefined: ctx/cfg/fetcher/mockSSM` in multiple test stubs. Root cause: `_ = var` suppression blanks referenced variables declared only inside `// ...` comments. Fixed by removing all blank-identifier suppression lines.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Wave 0 complete. Wave 1 (Plan 76-01) can begin immediately.
- Wave 1 implements `newVSCodeRekeyCmd` + `runVSCodeRekey` pre-flight (EC2 running, lock, SSM status checks) and uncomments TestVSCodeRekey_CommandRegistered through TestVSCodeRekey_SSHDDown.
- Wave 2 (Plan 76-02) implements the rotation logic (key generation, SSM install, verify, atomic rename) and uncomments TestVSCodeRekey_NormalRotation through TestVSCodeRekey_OutputMarkers.
- Wave 2 (Plan 76-03, parallel) updates CLAUDE.md + docs/vscode.md.

---
*Phase: 76-km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox*
*Completed: 2026-05-10*

## Self-Check: PASSED
