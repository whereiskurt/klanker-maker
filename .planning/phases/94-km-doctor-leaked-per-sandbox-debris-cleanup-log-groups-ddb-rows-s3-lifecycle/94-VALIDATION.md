---
phase: 94
slug: km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-04
---

# Phase 94 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (standard library + existing doctor mocked-API harness) |
| **Config file** | none — package `internal/app/cmd` already has the `doctor_*_test.go` suite |
| **Quick run command** | `go test ./internal/app/cmd/ -run 'StaleLogGroups|OrphanedDDBRows|S3Lifecycle' -count=1` |
| **Full suite command** | `go test ./internal/app/cmd/... ./internal/app/config/... -count=1` |
| **Estimated runtime** | ~20-40 seconds (cmd package), config package ~5s |

---

## Sampling Rate

- **After every task commit:** Run the quick run command (scoped to the new checks).
- **After every plan wave:** Run the full suite command.
- **Before `/gsd:verify-work`:** Full suite must be green AND `make build` succeeds.
- **Max feedback latency:** ~40 seconds.

---

## Per-Task Verification Map

> Task IDs are illustrative — the planner assigns final IDs. Every behavior below
> maps to an automated mocked-API test mirroring the existing `doctor_*_test.go`
> table-driven style, except where flagged manual.

| Behavior | Wave | Derived Requirement | Test Type | Automated Command | File Exists | Status |
|----------|------|---------------------|-----------|-------------------|-------------|--------|
| New API interfaces + DoctorDeps fields compile + inject | 1 | DBG-INFRA | unit/compile | `go test ./internal/app/cmd/ -run Doctor -count=1` | ❌ W0 | ⬜ pending |
| Config knobs `doctor_log_retention_days`/`doctor_s3_expire_days` load from yaml (merge-list) + clamp | 1 | DBG-CFG | unit | `go test ./internal/app/config/ -run 'Doctor.*Days' -count=1` | ❌ W0 | ⬜ pending |
| `--with-deletes` fans out to `--delete-logs`/`--delete-ddb-rows` | 1 | DBG-FLAGS | unit | `go test ./internal/app/cmd/ -run DoctorFlags -count=1` | ❌ W0 | ⬜ pending |
| `checkStaleLogGroups`: orphan detected → WARN (all 4 families incl. `/km/sidecars/`) | 2 | DBG-LOGS | unit | `go test ./internal/app/cmd/ -run StaleLogGroups -count=1` | ❌ W0 | ⬜ pending |
| `checkStaleLogGroups`: literal `km-` Lambda prefix used (NOT resource_prefix) | 2 | DBG-LOGS-PREFIX | unit | `go test ./internal/app/cmd/ -run StaleLogGroups_KmPrefix -count=1` | ❌ W0 | ⬜ pending |
| `checkStaleLogGroups`: all-active → OK; dry-run/flag-unset → WARN no-delete | 2 | DBG-LOGS | unit | `go test ./internal/app/cmd/ -run StaleLogGroups -count=1` | ❌ W0 | ⬜ pending |
| `checkStaleLogGroups`: `--dry-run=false --delete-logs` → deleted/failed counts | 2 | DBG-LOGS | unit | `go test ./internal/app/cmd/ -run StaleLogGroups_Delete -count=1` | ❌ W0 | ⬜ pending |
| `--set-log-retention`: sets retentionInDays on groups lacking it; idempotent no-op | 2 | DBG-LOGS-RET | unit | `go test ./internal/app/cmd/ -run LogRetention -count=1` | ❌ W0 | ⬜ pending |
| `checkOrphanedDDBRows`: orphan rows across 4 tables → WARN | 3 | DBG-DDB | unit | `go test ./internal/app/cmd/ -run OrphanedDDBRows -count=1` | ❌ W0 | ⬜ pending |
| `checkOrphanedDDBRows`: **AI-model `BUDGET#ai#` rows preserved** (regression) | 3 | DBG-DDB-AI | unit | `go test ./internal/app/cmd/ -run OrphanedDDBRows_PreserveAI -count=1` | ❌ W0 | ⬜ pending |
| `checkOrphanedDDBRows`: sandboxes purged only when `status=failed/nocap` (in-flight `starting` guarded) | 3 | DBG-DDB-GUARD | unit | `go test ./internal/app/cmd/ -run OrphanedDDBRows_FailedGuard -count=1` | ❌ W0 | ⬜ pending |
| `checkOrphanedDDBRows`: slack-threads keyed by attribute `sandbox_id` not PK | 3 | DBG-DDB-SLACK | unit | `go test ./internal/app/cmd/ -run OrphanedDDBRows_Slack -count=1` | ❌ W0 | ⬜ pending |
| `checkOrphanedDDBRows`: `--dry-run=false --delete-ddb-rows` → deleted/failed counts | 3 | DBG-DDB | unit | `go test ./internal/app/cmd/ -run OrphanedDDBRows_Delete -count=1` | ❌ W0 | ⬜ pending |
| `checkS3LifecyclePolicy`: missing expiry on transient prefixes → WARN | 4 | DBG-S3 | unit | `go test ./internal/app/cmd/ -run S3Lifecycle -count=1` | ❌ W0 | ⬜ pending |
| `--set-s3-lifecycle`: applies expiry to transient prefixes only, preserves unrelated rules, idempotent | 4 | DBG-S3-SET | unit | `go test ./internal/app/cmd/ -run S3Lifecycle_Set -count=1` | ❌ W0 | ⬜ pending |
| Pagination across multi-page DescribeLogGroups / Scan | 2,3 | DBG-PAGE | unit | `go test ./internal/app/cmd/ -run 'LogGroups_Paginate|DDBRows_Paginate' -count=1` | ❌ W0 | ⬜ pending |
| `--ignore-prefix` / `doctor_ignore_prefixes` respected (multi-install) | 2,3 | DBG-MULTI | unit | `go test ./internal/app/cmd/ -run 'IgnorePrefix' -count=1` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] New mocked-API interfaces (`CWLogsCleanupAPI`, `DDBScanDeleteAPI`, `S3LifecycleAPI`) + fakes in `doctor_test.go` helpers
- [ ] RED stub tests for each new check function (compile against not-yet-implemented checks)
- [ ] Config test stubs for the two new knobs in `internal/app/config/config_test.go`

*Existing `doctor_*_test.go` harness (table-driven, mocked AWS APIs) covers the framework — no new framework install needed.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Live reclamation against `kph` install | DBG-UAT | Requires real AWS account + the ~271 actual orphaned groups / leaked rows; can't be mocked end-to-end | `make build` → `AWS_PROFILE=klanker-application km doctor` (confirm new WARNs fire with real counts) → `km doctor --dry-run=false --delete-logs --delete-ddb-rows` (confirm counts) → `km doctor --dry-run=false --set-log-retention --set-s3-lifecycle` → re-run `km doctor` confirms clean |
| AI-model budget rows survive a live purge | DBG-UAT-AI | Final safety confirmation on real data | After live `--delete-ddb-rows`, scan `{prefix}-budgets` and confirm `BUDGET#ai#` rows still present |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 40s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
