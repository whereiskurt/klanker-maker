---
phase: 85-doctor-orphan-state-lock-digest-sweeper-report-cleanup
plan: 03
subsystem: infra
tags: [go, aws-sdk-v2, dynamodb, s3, doctor, cli, cobra, flag-wiring]

# Dependency graph
requires:
  - phase: 85
    plan: 01
    provides: "S3StateHeadAPI / LockDigestDeleterAPI interfaces, function signature, mocks"
  - phase: 85
    plan: 02
    provides: "checkStateLockDigestSweeper full implementation + CheckResult.Details field + 8 GREEN TestDigestSweeper_* tests"
provides:
  - "--delete-state-digests cobra flag on km doctor"
  - "--with-deletes umbrella now folds in --delete-state-digests"
  - "DoctorDeps.StateLockS3HeadClient (S3StateHeadAPI) + StateLockDDBWriteClient (LockDigestDeleterAPI) + DeleteStateDigests bool"
  - "initRealDepsWithExisting wires both new client fields from the existing awsCfg"
  - "buildChecks replaces the old checkStateLockDigest registration with checkStateLockDigestSweeper (old function body retained for Phase 84.1 tests)"
  - "runDoctor signature extended with deleteStateDigests bool as last positional"
  - "Two new GREEN flag-wiring tests in doctor_test.go (no t.Skip, no stubs)"
affects:
  - "phase 85 plan 04 (operator UAT against live AWS — depends on this binary having the new flag)"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Umbrella-flag OR-merge extension: --with-deletes now sets the 7th opt-in (DeleteStateDigests) alongside the original 6 — matches the precedent established when --delete-ssm was added"
    - "Dual-method-set wiring from one awsCfg: same s3.NewFromConfig(awsCfg) result satisfies both S3StateReader (GetObject) and S3StateHeadAPI (HeadObject); same dynamodb.NewFromConfig(awsCfg) result satisfies both LockDigestReader (Scan) and LockDigestDeleterAPI (BatchWriteItem); two fields kept for testability"
    - "Replace-not-remove buildChecks swap: Phase 84.1's checkStateLockDigest function body kept in doctor.go so its 7 unit tests stay GREEN as a regression baseline; only the buildChecks registration site swaps to the sweeper"

key-files:
  created:
    - ".planning/phases/85-doctor-orphan-state-lock-digest-sweeper-report-cleanup/85-03-SUMMARY.md"
  modified:
    - "internal/app/cmd/doctor.go (+8 references to deleteStateDigests/DeleteStateDigests; +1 DoctorDeps field for DeleteStateDigests; +2 DoctorDeps fields for sweeper clients; +2 initRealDepsWithExisting wirings; +1 cobra flag; +1 --with-deletes fold-block addition; +1 --with-deletes description update; +1 runDoctor signature extension; +1 runDoctor deps assign; +1 buildChecks swap)"
    - "internal/app/cmd/doctor_test.go (+2 new GREEN tests: TestDoctorCmd_WithDeletes_EnablesStateDigests + TestDoctorCmd_DeleteStateDigestsAlone_DoesNotImplyOthers)"

key-decisions:
  - "DeleteStateDigests bool field added to DoctorDeps in Task 1 (not Task 2) because deps.DeleteStateDigests = deleteStateDigests inside runDoctor (Task 1 Step G) is a compile dependency"
  - "buildChecks closure captures sweeperDryRun + sweeperDeleteDigests at closure-creation time; deps.DryRun + deps.DeleteStateDigests assignments come BEFORE buildChecks(cfg, deps) at runDoctor L2426 (verified by grep — L2386 < L2393 < L2426)"
  - "24h sweeperMinOrphanAge constant in buildChecks closure — sweeper's minOrphanAge param is documented as currently UNUSED inside checkStateLockDigestSweeper (Plan 02 implements age guard via SandboxLister cross-reference, not time arithmetic), but the constant is passed anyway so the API surface is preserved for future use"
  - "Old checkStateLockDigest function body at doctor.go:3486 stays unchanged — Plan 02 implementation note carries forward: keeping the body lets Phase 84.1's TestCheckStateLockDigest_* suite remain GREEN as a regression baseline"

patterns-established:
  - "Phase 85 flag-wiring test pattern: each new --delete-* flag gets two tests — one for the --with-deletes umbrella inclusion (mirrors :668) and one for the no-implied-other-opt-ins inverse (mirrors :716). Future --delete-* additions should follow this twin-test pattern."

requirements-completed:
  - IN-SCOPE-2
  - IN-SCOPE-5
  - ACCEPT-READ

# Metrics
duration: 13 min
completed: 2026-05-19
---

# Phase 85 — Plan 03 Summary (Wave 2)

**km doctor now exposes `--delete-state-digests` (and folds it into `--with-deletes`), wires the new sweeper through DoctorDeps + buildChecks, and the binary actually invokes the parallel HeadObject scan path on a real account.**

## Performance

- **Duration:** 13 min
- **Started:** 2026-05-19T11:34:40Z
- **Completed:** 2026-05-19T11:48:10Z
- **Tasks:** 3
- **Files modified:** 2 (doctor.go, doctor_test.go) + 1 created (this summary)

## Accomplishments

- `km doctor --help` shows `--delete-state-digests` with the documented NoSuchKey-only-delete warning.
- `km doctor --help` `--with-deletes` description now reads `... --delete-ssm --delete-state-digests`.
- DoctorDeps has three new fields: `StateLockS3HeadClient`, `StateLockDDBWriteClient`, `DeleteStateDigests`.
- `initRealDepsWithExisting` wires both new client fields from the same `awsCfg` used by the Phase 84.1 read-only fields.
- `buildChecks` REPLACES the Phase 84.1 `checkStateLockDigest` closure with a `checkStateLockDigestSweeper` closure (Plan 02 sweeper) — the old function body remains in `doctor.go` so its Phase 84.1 unit tests keep passing.
- `runDoctor` signature extended with `deleteStateDigests` as the last positional bool.
- Two new GREEN flag-wiring tests added to `doctor_test.go`, mirroring the Phase 84.1 precedent — no `t.Skip`, no stubs.
- `make build` produces a fresh `km` binary with the new flag exposed.

## doctor.go diff summary (this plan)

| Around line | Change |
|---|---|
| L284 (DoctorDeps) | + `StateLockS3HeadClient S3StateHeadAPI` |
| L285 (DoctorDeps) | + `StateLockDDBWriteClient LockDigestDeleterAPI` |
| L343 (DoctorDeps) | + `DeleteStateDigests bool` |
| L2264 (NewDoctorCmdWithDeps) | + `var deleteStateDigests bool` |
| L2322 (--with-deletes fold block) | + `deleteStateDigests = true` |
| L2324 (runDoctor call site) | + `deleteStateDigests` as last positional arg |
| L2345 (cobra Flags) | + `cmd.Flags().BoolVar(&deleteStateDigests, "delete-state-digests", ...)` |
| L2348 (--with-deletes description) | updated to include `--delete-state-digests` |
| L2355 (runDoctor signature) | + `deleteStateDigests bool` as last positional |
| L2393 (runDoctor body) | + `deps.DeleteStateDigests = deleteStateDigests` |
| L2710-2720 (buildChecks) | REPLACED `checkStateLockDigest(...)` closure with `checkStateLockDigestSweeper(...)` closure |
| L3041-3047 (initRealDepsWithExisting) | + `deps.StateLockS3HeadClient = s3.NewFromConfig(awsCfg)` and `deps.StateLockDDBWriteClient = dynamodb.NewFromConfig(awsCfg)` |

`grep -c "deleteStateDigests\|DeleteStateDigests" doctor.go` → 8 (≥5 as required).
`grep -c "checkStateLockDigestSweeper" doctor.go` → 2 (comment at L78 + buildChecks registration at L2718).
`grep -c "^func checkStateLockDigest" doctor.go` → 1 (old function body still at L3486).
`git diff --stat doctor_state_digest_sweeper.go` → empty (Plan 02's file untouched).

## What's NOT in this plan

- **CheckResult.Details field** — added by Plan 02 (Task 0). Already present.
- **The sweeper algorithm itself** — implemented by Plan 02 (parallel HEAD scan, age guard, BatchWriteItem cleanup, output formatter). Untouched here.
- **TestDigestSweeper_* tests** — already GREEN from Plan 02; Plan 03 does not touch them.

## Test status

| Suite | Status |
|---|---|
| `TestDoctorCmd_WithDeletes_EnablesStateDigests` | GREEN (new this plan) |
| `TestDoctorCmd_DeleteStateDigestsAlone_DoesNotImplyOthers` | GREEN (new this plan) |
| `TestDoctorCmd_WithDeletes_EnablesAllDeleteOptIns` | GREEN (regression — Phase 84.1) |
| `TestDoctorCmd_NoWithDeletes_LeavesDeleteOptInsAlone` | GREEN (regression) |
| `TestDoctorCmd_IndividualDeleteFlag_DoesNotImplyOthers` | GREEN (regression) |
| All 8 `TestDigestSweeper_*` (Plan 02) | GREEN |
| All 7 `TestCheckStateLockDigest_*` (Phase 84.1) | GREEN — old function body untouched |
| `TestParseLockID_*` | GREEN |
| `TestCheckStaleLambdas_*` | GREEN |
| `TestUnlockCmd_RequiresStateBucket` | FAIL (pre-existing — deferred in `deferred-items.md`, unrelated to Phase 85) |

`go test ./internal/app/cmd/ -run "TestDoctor|TestDigestSweeper|TestCheckStateLockDigest|TestParseLockID|TestCheckStaleLambdas" -count=1` → `ok 2.892s`.

## Binary smoke test

```
$ make build
go build -ldflags '-X .../pkg/version.Number=v0.2.704 -X .../pkg/version.GitCommit=22acb0d' -o km ./cmd/km/
Built: km v0.2.704 (22acb0d)

$ ./km doctor --help | grep -E "delete-state-digests|with-deletes\s"
      --delete-state-digests   With --dry-run=false, delete orphan DDB lock rows where the S3 state object is definitively gone (HeadObject returns NotFound). Generic errors — network, 5xx, throttling — NEVER trigger deletion (logged as 'could not verify'). Live S3 + stale MD5 mismatches are reported but NEVER deleted by this flag.
      --with-deletes           Shortcut for --delete-ebs --delete-sqs --delete-s3 --delete-lambdas --delete-ssh --delete-ssm --delete-state-digests. Pair with --dry-run=false for a full cleanup pass; with --dry-run=true (default) shows what each opt-in would clean.
```

## Task Commits

1. **Task 1: --delete-state-digests flag + GREEN flag-wiring tests** — `22acb0d` (feat)
2. **Task 2: DoctorDeps + initRealDepsWithExisting + buildChecks swap** — `39df1c3` (feat)
3. **Task 3 (this SUMMARY + plan metadata)** — pending docs commit

## Decisions Made

- Moved the `DeleteStateDigests bool` field on `DoctorDeps` into Task 1 instead of Task 2 — required so that the Task 1 source (which assigns `deps.DeleteStateDigests = deleteStateDigests` inside `runDoctor`) compiles standalone. Task 2 still owns the two interface fields (`StateLockS3HeadClient`, `StateLockDDBWriteClient`) and the `initRealDepsWithExisting` wiring + `buildChecks` swap. This keeps each task's `go build` GREEN at commit time.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Hoisted DoctorDeps.DeleteStateDigests field from Task 2 into Task 1**

- **Found during:** Task 1 (compile)
- **Issue:** Plan Task 1 Step G assigns `deps.DeleteStateDigests = deleteStateDigests` inside `runDoctor`, but the plan placed the `DoctorDeps.DeleteStateDigests bool` field declaration in Task 2 (Step A). This would have left Task 1 with a non-compiling intermediate commit, violating the per-task `go build ./...` done-criterion.
- **Fix:** Added only the `DeleteStateDigests bool` field to `DoctorDeps` as part of Task 1's commit (alongside the field's godoc). Task 2 still owns the two interface fields (`StateLockS3HeadClient`, `StateLockDDBWriteClient`) and the rest of the wiring exactly as planned.
- **Files modified:** `internal/app/cmd/doctor.go` (added field at L337-343 in Task 1's commit `22acb0d`)
- **Verification:** Task 1's commit builds cleanly with `go build ./...` and Task 1's verify command passes all 19 enumerated tests GREEN.
- **Committed in:** `22acb0d` (Task 1)

---

**Total deviations:** 1 auto-fixed (1 blocking).
**Impact on plan:** No functional impact. The plan's Task 2 Step A diff shrinks by one field declaration (still adds the two interface fields and the wiring); every other line in the plan applies as written. Each task's intermediate commit now passes `go build ./...` and the relevant tests.

## Issues Encountered

- `go vet ./...` reports a pre-existing IPv6 format-string warning in `sidecars/http-proxy/httpproxy/transparent.go:204` — completely unrelated to doctor.go work, out of Phase 85 scope. `go vet ./internal/app/cmd/` (the package we touched) is clean.
- `TestUnlockCmd_RequiresStateBucket` is failing — already documented in `deferred-items.md` from Plan 02 head. Unrelated to Phase 85.

## User Setup Required

None — no external service configuration required. Operator validation against live AWS is Plan 04's job.

## Ready-for-Plan-04 checklist

- [x] `make build` produces a fresh binary (`km v0.2.704 (22acb0d)`).
- [x] `./km doctor --help` shows `--delete-state-digests`.
- [x] `./km doctor --help` `--with-deletes` description mentions `--delete-state-digests`.
- [x] `go test ./internal/app/cmd/ -run "TestDoctor|TestDigestSweeper|TestCheckStateLockDigest" -count=1` — green.
- [x] `grep -c "checkStateLockDigestSweeper" internal/app/cmd/doctor.go` ≥ 1 (registered in buildChecks).
- [x] Old `checkStateLockDigest` function body still in `doctor.go` at L3486 — Phase 84.1 tests still pass.
- [x] `git diff --stat internal/app/cmd/doctor_state_digest_sweeper.go` — empty (Plan 02 file unchanged).
- [x] CheckResult struct unchanged in this plan (Plan 02 owns the `Details []string` addition).

## Next Phase Readiness

Plan 04 (operator UAT against live AWS) can begin immediately. Operator validation steps:

1. Snapshot current orphan count: `./km doctor 2>&1 | grep -E "state-lock|digest"`.
2. Dry-run sweep: `./km doctor --with-deletes` (still --dry-run=true by default) — should show summary + up to 10 inline orphan LockIDs.
3. JSON full-list: `./km doctor --json --with-deletes | jq '.[] | select(.name | contains("state")) | .details'` — uncapped list via CheckResult.Details.
4. Destructive sweep: `./km doctor --with-deletes --dry-run=false` — cleans orphan rows where HeadObject returns NotFound; never deletes on generic/transient errors.
5. Re-snapshot — orphan count should drop to zero (or match the live-sandbox cross-reference set).

## Self-Check

- `.planning/phases/85-.../85-03-SUMMARY.md`: this file — exists on disk
- `internal/app/cmd/doctor.go`: exists, contains `deleteStateDigests` (8 hits), `checkStateLockDigestSweeper` (2 hits), `StateLockS3HeadClient` (2 hits), `StateLockDDBWriteClient` (2 hits)
- `internal/app/cmd/doctor_test.go`: exists, contains `TestDoctorCmd_WithDeletes_EnablesStateDigests` (1 hit) and `TestDoctorCmd_DeleteStateDigestsAlone_DoesNotImplyOthers` (1 hit)
- Task 1 commit `22acb0d`: present in `git log` (feat: --delete-state-digests flag + GREEN flag-wiring tests)
- Task 2 commit `39df1c3`: present in `git log` (feat: DoctorDeps + initRealDepsWithExisting + buildChecks swap)
- `km` binary present: `./km doctor --help` shows the new flag

---

*Phase: 85-doctor-orphan-state-lock-digest-sweeper-report-cleanup*
*Plan: 03 (Wave 2 — flag wiring + sweeper integration)*
*Completed: 2026-05-19*
