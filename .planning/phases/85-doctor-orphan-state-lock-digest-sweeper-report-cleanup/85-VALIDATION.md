---
phase: 85
slug: doctor-orphan-state-lock-digest-sweeper-report-cleanup
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-18
---

# Phase 85 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

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

> Plan IDs and task numbers are filled in by `gsd-planner` in step 8. Rows below seeded from RESEARCH.md `## Validation Architecture` → planner refines.

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 85-00-01 | 00 | 0 | IN-SCOPE-7 | unit (Wave 0 stub) | n/a — install Wave 0 file | ❌ W0 | ⬜ pending |
| 85-XX-01 | XX | 1 | IN-SCOPE-7 / TDD-1 | unit | `go test ./internal/app/cmd/ -run TestDigestSweeper_OrphanAgePassesDeleted -v` | ❌ W0 | ⬜ pending |
| 85-XX-02 | XX | 1 | IN-SCOPE-7 / TDD-2 | unit | `go test ./internal/app/cmd/ -run TestDigestSweeper_OrphanAgeFailsSkipped -v` | ❌ W0 | ⬜ pending |
| 85-XX-03 | XX | 1 | IN-SCOPE-7 / TDD-3 | unit | `go test ./internal/app/cmd/ -run TestDigestSweeper_S3HeadAmbiguousError -v` | ❌ W0 | ⬜ pending |
| 85-XX-04 | XX | 1 | IN-SCOPE-7 / TDD-4 | unit | `go test ./internal/app/cmd/ -run TestDigestSweeper_LiveS3StaleDigest -v` | ❌ W0 | ⬜ pending |
| 85-XX-05 | XX | 1 | IN-SCOPE-7 / TDD-5 | unit | `go test ./internal/app/cmd/ -run TestDigestSweeper_26Items_TwoBatches -v` | ❌ W0 | ⬜ pending |
| 85-XX-06 | XX | 2 | IN-SCOPE-2 | unit | `go test ./internal/app/cmd/ -run TestDoctor -v` (flag wiring) | ✅ | ⬜ pending |
| 85-XX-07 | XX | 2 | ACCEPT-READ | unit | `go test ./internal/app/cmd/ -run TestDigestSweeper_OutputFormat -v` | ❌ W0 | ⬜ pending |
| 85-XX-08 | XX | 2 | IN-SCOPE-4 | unit (optional) | `go test ./internal/app/cmd/ -run TestDigestSweeper_UnprocessedItems -v` | ❌ W0 | ⬜ pending |
| 85-XX-09 | XX | 3 | ACCEPT-READ | manual UAT (perf) | `time km doctor` on operator's ~275-orphan account | ✅ | ⬜ pending |
| 85-XX-10 | XX | 3 | ACCEPT-WRITE | manual UAT (live AWS) | `km doctor --with-deletes --dry-run=false`; verify live row untouched | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/app/cmd/doctor_state_digest_sweeper.go` — new file housing `checkStateLockDigestSweeper`, `S3StateHeadAPI`, `LockDigestDeleterAPI` interfaces (stub implementations + types only, no logic yet)
- [ ] New TDD test stubs in `internal/app/cmd/doctor_state_digest_test.go` for all 5 required cases (red-state, function signatures match Wave 0 stubs) — additionally seed `TestDigestSweeper_OutputFormat` and optional `TestDigestSweeper_UnprocessedItems`
- [ ] Framework install: none — Go stdlib `testing` already in use

**Existing passing tests that must remain green (regression baseline):**
- `go test ./internal/app/cmd/ -run TestCheckStateLockDigest -v` (Phase 84.1)
- `go test ./internal/app/cmd/ -run TestCheckStaleLambdas -v`
- `go test ./internal/app/cmd/ -run TestParseLockID -v`

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| `km doctor` < 30s wall clock on ~275-orphan account | ACCEPT-READ (perf) | Requires real AWS account with accumulated orphans; cannot be reproduced in unit tests | `time km doctor` on operator's current account; assert real-time output ≤ 30s |
| `km doctor --with-deletes --dry-run=false` cleans orphans without touching live state locks | ACCEPT-WRITE | Requires live DDB lock table + live S3 state; unit tests use mocks only | Snapshot `aws dynamodb scan` of lock table before/after; verify orphan rows gone, all rows with extant S3 state object preserved, running sandbox lock row preserved |
| `--json` emits full list when inline preview is capped at 10 | ACCEPT-READ (format) | Output rendering of large lists best confirmed against real account state | `km doctor --json \| jq '.checks[] \| select(.name | contains("digest"))'`; assert array length equals operator's actual orphan count |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (`doctor_state_digest_sweeper.go` + test stubs)
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
