---
phase: 07-unwired-code-paths
plan: "01"
subsystem: audit-log-sidecar
tags: [audit-log, redaction, idle-detection, sidecar, security]
dependency_graph:
  requires: []
  provides: [OBSV-07-wired, PROV-06-wired]
  affects: [sidecars/audit-log/cmd/main.go]
tech_stack:
  added: []
  patterns: [wrapper-pattern, factory-refactor, CW-client-sharing]
key_files:
  created:
    - sidecars/audit-log/cmd/main_test.go
  modified:
    - sidecars/audit-log/cmd/main.go
decisions:
  - "buildDest() signature takes cwClient parameter — pre-creation in main() allows single CW session shared between dest and idle detector"
  - "newIdleDetector() helper extracted for testability — keeps main() readable and allows compile-time type checks in tests"
  - "OnIdle calls cancel() only — TTL Lambda handles actual sandbox destroy; audit-log sidecar exits cleanly"
  - "Idle detection silently skipped when dest is not cloudwatch — no warning needed; detector requires CW polling"
metrics:
  duration: 92s
  completed_date: "2026-03-22"
  tasks_completed: 1
  files_modified: 2
requirements_satisfied: [PROV-06, OBSV-07]
---

# Phase 07 Plan 01: Wire RedactingDestination and IdleDetector into Audit-Log Sidecar

**One-liner:** Wired RedactingDestination wrapper around all buildDest paths and IdleDetector goroutine startup when IDLE_TIMEOUT_MINUTES is set and dest is cloudwatch.

## What Was Built

Two audit gaps closed: RedactingDestination and IdleDetector were implemented and unit-tested (phases 03 and 04) but never called from the production binary. This plan wires both into `sidecars/audit-log/cmd/main.go`.

### RedactingDestination (OBSV-07)

`buildDest()` now wraps every returned destination with `auditlog.NewRedactingDestination(inner, nil)` as the final return step. The switch statement builds the inner destination (CloudWatch, S3, or stdout), and the wrapper is applied once after — pattern is clean and not per-case. Nil literals are passed because the built-in regex patterns cover AWS access key IDs (`AKIA...`), Bearer tokens, and hex strings 40+ characters long.

The `buildDest()` signature was extended with a `cwClient kmaws.CWLogsAPI` parameter. The CW client is now constructed in `main()` via `newCWClient()` before calling `buildDest()`, allowing it to be shared with the idle detector without creating a second AWS session.

### IdleDetector (PROV-06)

In `main()`, after `buildDest()` returns, the code reads `IDLE_TIMEOUT_MINUTES`. When non-empty and `destName == "cloudwatch"`:

1. Parse integer minutes via `strconv.Atoi`
2. Construct `lifecycle.IdleDetector` via `newIdleDetector()` helper with the pre-created CW client
3. Set `OnIdle` to log a warning and call `cancel()` — clean exit without orphaning resources
4. Launch with `go detector.Run(ctx)` in a goroutine

When `IDLE_TIMEOUT_MINUTES` is empty or dest is not cloudwatch, the detector is silently skipped (no warning — this is a normal configuration).

## Tests

`sidecars/audit-log/cmd/main_test.go` (4 tests, same package):

| Test | What it checks |
|------|---------------|
| `TestBuildDestStdoutReturnsRedacting` | `buildDest("stdout",...)` returns `*auditlog.RedactingDestination` |
| `TestBuildDestS3ReturnsRedacting` | `buildDest("s3",...)` returns `*auditlog.RedactingDestination` |
| `TestBuildDestNilCWClientStdout` | nil CW client with stdout dest works without error |
| `TestIdleDetectorTypeExists` | `newIdleDetector()` compiles and returns non-nil detector |

All 19 tests across `sidecars/audit-log/...` and `pkg/lifecycle/...` pass.

## Verification

```
grep NewRedactingDestination sidecars/audit-log/cmd/main.go
# → line 157: return auditlog.NewRedactingDestination(inner, nil), nil

grep "IdleDetector\|detector.Run" sidecars/audit-log/cmd/main.go
# → multiple matches confirming detector construction and goroutine launch

go build -o /tmp/audit-log-binary github.com/whereiskurt/klankrmkr/sidecars/audit-log/cmd
# → success

go test ./sidecars/audit-log/... ./pkg/lifecycle/... -count=1
# → all pass
```

## Deviations from Plan

### Auto-fixed Issues

None — plan executed exactly as written.

**Note on `go build ./sidecars/audit-log/cmd/...` error:** The plan's verify command `go build ./sidecars/audit-log/cmd/...` fails with "build output 'cmd' already exists and is a directory" because there is a top-level `cmd/` directory in the repo root. This is a pre-existing naming collision, not introduced by this plan. The binary builds cleanly via `go build -o /tmp/audit-log-binary github.com/whereiskurt/klankrmkr/sidecars/audit-log/cmd` and `go vet ./sidecars/audit-log/cmd/...` passes with no output. This was not treated as a blocking issue.

## Commits

| Hash | Description |
|------|-------------|
| 4af76cb | feat(07-01): wire RedactingDestination and IdleDetector into audit-log binary |

## Self-Check

Files exist:
- `sidecars/audit-log/cmd/main.go` — modified
- `sidecars/audit-log/cmd/main_test.go` — created

Commit 4af76cb present in git log.
