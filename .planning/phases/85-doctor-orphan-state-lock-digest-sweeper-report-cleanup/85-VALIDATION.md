---
phase: 85
slug: doctor-orphan-state-lock-digest-sweeper-report-cleanup
status: revised
nyquist_compliant: true
wave_0_complete: false
created: 2026-05-18
revised: 2026-05-18
---

# Phase 85 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Revised 2026-05-18 to address checker warnings 3 (shared-module test) and 5 (SUMMARY enforcement).

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go stdlib `testing` package |
| **Config file** | none — `go test` standard |
| **Quick run command** | `go test ./internal/app/cmd/ -run TestDigestSweeper -v` |
| **Full suite command** | `go test ./internal/app/cmd/ -v` |
| **Estimated runtime** | Quick ~2s · Full ~30s |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/ -run TestDigestSweeper -v`
- **After every plan wave:** Run `go test ./internal/app/cmd/ -v`
- **Before `/gsd:verify-work`:** Full suite must be green (incl. Phase 84.1 `TestCheckStateLockDigest` regressions)
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 85-01-01 | 01 | 0 | IN-SCOPE-7 | unit (Wave 0 stub) | `go build ./internal/app/cmd/...` | ✅ | ⬜ pending |
| 85-01-02 | 01 | 0 | IN-SCOPE-7 | unit (8 red stubs) | `go test ./internal/app/cmd/ -run TestDigestSweeper -v` (8 RED) | ❌ W0 | ⬜ pending |
| 85-01-03 | 01 | 0 | enforce summary | doc | `test -f .planning/phases/85-.../85-01-SUMMARY.md` | ❌ W0 | ⬜ pending |
| 85-02-00 | 02 | 1 | ACCEPT-READ (Details field) | unit | `grep -q "Details\s*\[\]string" internal/app/cmd/doctor.go` | ❌ W0 | ⬜ pending |
| 85-02-01 | 02 | 1 | IN-SCOPE-7 / TDD-2,3,4 + OutputFormat + SharedModule | unit | `go test ./internal/app/cmd/ -run "TestDigestSweeper_OrphanAgeFailsSkipped\|TestDigestSweeper_S3HeadAmbiguousError\|TestDigestSweeper_LiveS3StaleDigest\|TestDigestSweeper_OutputFormat\|TestDigestSweeper_SharedModuleLockID_NoSandboxID" -v` | ❌ W0 | ⬜ pending |
| 85-02-02 | 02 | 1 | IN-SCOPE-7 / TDD-1,5 + UnprocessedItems | unit | `go test ./internal/app/cmd/ -run "TestDigestSweeper_OrphanAgePassesDeleted\|TestDigestSweeper_26Items_TwoBatches\|TestDigestSweeper_UnprocessedItems" -v` | ❌ W0 | ⬜ pending |
| 85-02-03 | 02 | 1 | enforce summary | doc | `test -f .planning/phases/85-.../85-02-SUMMARY.md` | ❌ W0 | ⬜ pending |
| 85-03-01 | 03 | 2 | IN-SCOPE-2 | unit | `go test ./internal/app/cmd/ -run "TestDoctorCmd_WithDeletes_EnablesStateDigests\|TestDoctorCmd_DeleteStateDigestsAlone_DoesNotImplyOthers" -v` (both GREEN — no SKIP) | ❌ W0 | ⬜ pending |
| 85-03-02 | 03 | 2 | IN-SCOPE-2 / IN-SCOPE-5 | unit + smoke | `go test ./internal/app/cmd/ -v && ./bin/km doctor --help \| grep delete-state-digests` | ❌ W0 | ⬜ pending |
| 85-03-03 | 03 | 2 | enforce summary | doc | `test -f .planning/phases/85-.../85-03-SUMMARY.md` | ❌ W0 | ⬜ pending |
| 85-04-01 | 04 | 3 | ACCEPT-READ (perf) | manual UAT | `time km doctor` on operator's ~275-orphan account | ✅ | ⬜ pending |
| 85-04-02 | 04 | 3 | ACCEPT-READ (--json full list) | manual UAT | `km doctor --json \| jq '.[] \| select(.name \| contains("digest")) \| .details \| length'` equals summary count (unconditional PASS) | ✅ | ⬜ pending |
| 85-04-03 | 04 | 3 | ACCEPT-WRITE | manual UAT | `km doctor --with-deletes --dry-run=false`; verify live row untouched | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## TDD Test Catalog (Plan 01 RED → Plan 02 GREEN)

| # | Test Name | Coverage | Plan 02 GREEN turn-on task |
|---|-----------|----------|-------------------------------|
| 1 | TestDigestSweeper_OrphanAgePassesDeleted | TDD-1: orphan + age-pass → row deleted | Task 2 |
| 2 | TestDigestSweeper_OrphanAgeFailsSkipped | TDD-2: orphan + age-fail → row skipped | Task 1 |
| 3 | TestDigestSweeper_S3HeadAmbiguousError_Skipped | TDD-3: ambiguous S3 error → no delete | Task 1 |
| 4 | TestDigestSweeper_LiveS3StaleDigest_NotDeletedBySweeper | TDD-4: live S3 + stale MD5 → no delete | Task 1 |
| 5 | TestDigestSweeper_26Items_TwoBatches | TDD-5: 26 items → 2 BatchWriteItem calls | Task 2 |
| 6 | TestDigestSweeper_OutputFormat | summary + 10 inline + continuation + uncapped Details | Task 1 |
| 7 | TestDigestSweeper_UnprocessedItems | partial throttle → reported as failed | Task 2 |
| 8 | **TestDigestSweeper_SharedModuleLockID_NoSandboxID** | **shared-module LockID (no `/sandboxes/`) → deletable when gate open + NotFound** | **Task 1 (added 2026-05-18 per checker warning #3)** |

---

## Wave 0 Requirements

- [ ] `internal/app/cmd/doctor_state_digest_sweeper.go` — new file housing `checkStateLockDigestSweeper`, `S3StateHeadAPI`, `LockDigestDeleterAPI` interfaces (stub implementations + types only, no logic yet)
- [ ] New TDD test stubs in `internal/app/cmd/doctor_state_digest_test.go` for all 5 required cases + OutputFormat + UnprocessedItems + SharedModuleLockID_NoSandboxID (8 total, all red-state)
- [ ] Framework install: none — Go stdlib `testing` already in use

**Existing passing tests that must remain green (regression baseline):**
- `go test ./internal/app/cmd/ -run TestCheckStateLockDigest -v` (Phase 84.1)
- `go test ./internal/app/cmd/ -run TestCheckStaleLambdas -v`
- `go test ./internal/app/cmd/ -run TestParseLockID -v`
- `go test ./internal/app/cmd/ -run TestDoctorCmd -v` (Phase 84.x flag-wiring tests)

---

## Enforceable SUMMARY.md Convention (added 2026-05-18 per checker warning #5)

Each plan in Waves 0–2 has a dedicated `<task>` that creates its `*-SUMMARY.md` and is verified by `test -f` + min-line-count + content grep. SUMMARY.md is NOT advisory in this phase:

- Plan 01 → Task 3 produces 85-01-SUMMARY.md
- Plan 02 → Task 3 produces 85-02-SUMMARY.md
- Plan 03 → Task 3 produces 85-03-SUMMARY.md
- Plan 04 → operator-produced 85-04-UAT.md and (post-UAT) 85-04-SUMMARY.md

Downstream plans reference upstream SUMMARYs in their `@context` blocks AND rely on the same locked-signature `<interfaces>` block as the source of truth. If the executor skips a SUMMARY task, the plan's verify gate fails — preventing the "advisory drift" risk the checker flagged.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| `km doctor` < 30s wall clock on ~275-orphan account | ACCEPT-READ (perf) | Requires real AWS account with accumulated orphans; cannot be reproduced in unit tests | `time km doctor` on operator's current account; assert real-time output ≤ 30s |
| `km doctor --with-deletes --dry-run=false` cleans orphans without touching live state locks | ACCEPT-WRITE | Requires live DDB lock table + live S3 state; unit tests use mocks only | Snapshot `aws dynamodb scan` of lock table before/after; verify orphan rows gone, all rows with extant S3 state object preserved, running sandbox lock row preserved |
| `--json` emits full list via `.details` when inline preview is capped at 10 | ACCEPT-READ (format, UNCONDITIONAL — no GAP fallback) | Output rendering of large lists best confirmed against real account state | `km doctor --json \| jq '.[] \| select(.name \| contains("digest")) \| .details \| length'`; assert array length equals operator's actual orphan count. STEP 2 in Plan 04 UAT is gated — FAIL aborts before destructive STEP 4. |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references (`doctor_state_digest_sweeper.go` + 8 test stubs)
- [x] No watch-mode flags
- [x] Feedback latency < 30s
- [x] `nyquist_compliant: true` set in frontmatter
- [x] SUMMARY.md creation is enforceable (per-plan task with verify gate)
- [x] Shared-module LockID fallback has a dedicated TDD test (Plan 01 RED → Plan 02 GREEN, Task 1)
- [x] `--json` `.details` plumbing is an unconditional UAT assertion (no GAP fallback)

**Approval:** revised pending re-check
