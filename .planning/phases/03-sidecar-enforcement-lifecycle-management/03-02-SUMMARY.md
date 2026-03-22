---
phase: 03-sidecar-enforcement-lifecycle-management
plan: 02
subsystem: observability
tags: [cloudwatch, audit-log, sidecar, golang, tdd, zerolog, aws-sdk-go-v2]

requires:
  - phase: 02-core-provisioning-security-baseline
    provides: pkg/aws patterns (TagAPI interface, mock testing conventions)

provides:
  - sidecars/audit-log/auditlog.go — AuditEvent type, Destination interface, Process() loop, StdoutDest, CloudWatchDest (buffered), S3Dest (stub)
  - sidecars/audit-log/cmd/main.go — sidecar binary with env-var config, SIGTERM flush, realCWBackend adapter
  - pkg/aws/cloudwatch.go — CWLogsAPI interface, LogEvent type, EnsureLogGroup, PutLogEvents, GetLogEvents, TailLogs
  - pkg/aws/cloudwatch_test.go — 7 unit tests with mockCWLogsAPI

affects:
  - 03-03 (EventBridge/lifecycle — scheduler.go will wire to pkg/aws CloudWatch)
  - 03-04 (km logs command — uses TailLogs from cloudwatch.go)
  - 03-01 (dns-proxy / http-proxy — output CloudWatch-compatible JSON lines consumed by audit-log)

tech-stack:
  added:
    - github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs v1.64.1
  patterns:
    - TDD red/green: failing stubs -> full implementation, all tests green before commit
    - Narrow interface pattern: CWLogsAPI mirrors TagAPI from discover.go — only methods used
    - CloudWatchBackend interface in auditlog package decouples sidecar from AWS SDK (testable without credentials)
    - S3Dest stub with explicit warning log — no silent data loss, Phase 4 is the correct scope
    - Destination interface + Process() separation — io.Reader abstraction enables stdin in prod, bytes.Buffer in tests

key-files:
  created:
    - sidecars/audit-log/auditlog.go
    - sidecars/audit-log/cmd/main.go
    - pkg/aws/cloudwatch.go
    - pkg/aws/cloudwatch_test.go
    - sidecars/audit-log/audit_log_test.go
  modified:
    - go.mod
    - go.sum

key-decisions:
  - "Package layout: auditlog.go (package auditlog) + cmd/main.go (package main) in subdirectory — Go disallows two packages in one directory; cmd/ pattern separates library from binary"
  - "CloudWatchDest flushes at 25-event threshold or on Flush() call — balances latency vs API call cost"
  - "S3Dest is stub in this plan — falls back to stdout with a warning; full archive is Phase 4 scope"
  - "GetLogEvents signature uses int32 limit matching AWS SDK cloudwatchlogs.GetLogEventsInput.Limit type"

patterns-established:
  - "mockCWLogsAPI in cloudwatch_test.go follows exact same pattern as mockTagAPI in discover_test.go"
  - "Narrow CWBackend interface in auditlog package: PutLogMessages + EnsureLogGroup — keeps sidecar tests credential-free"

requirements-completed: [OBSV-01, OBSV-02, OBSV-03]

duration: 12min
completed: 2026-03-22
---

# Phase 3 Plan 02: Audit Log Sidecar and CloudWatch Helpers Summary

**Audit log sidecar (zerolog JSON-line aggregator with CloudWatchDest/StdoutDest/S3Dest) and pkg/aws CloudWatch helpers (EnsureLogGroup, PutLogEvents, GetLogEvents, TailLogs) with narrow mock-testable interfaces**

## Performance

- **Duration:** 12 min
- **Started:** 2026-03-22T04:44:51Z
- **Completed:** 2026-03-22T04:56:00Z
- **Tasks:** 2 (each with TDD red/green commits)
- **Files modified:** 6 created, 2 modified (go.mod, go.sum)

## Accomplishments

- Audit log sidecar reads newline-delimited JSON events from stdin, routes to StdoutDest/CloudWatchDest/S3Dest based on AUDIT_LOG_DEST env var
- CloudWatchDest buffers up to 25 events and flushes via a narrow CWBackend interface — fully testable without AWS credentials
- pkg/aws CloudWatch helpers expose EnsureLogGroup (idempotent), PutLogEvents (10k-batch), GetLogEvents, TailLogs (2s poll with ctx cancel)
- 12 new unit tests passing in under 1 second, zero AWS calls required

## Task Commits

Each task was committed atomically with TDD red/green structure:

1. **Task 1 RED: audit log failing tests** - `da8f46d` (test)
2. **Task 1 GREEN: audit log implementation** - `2aa78e4` (feat)
3. **Task 2 RED: CloudWatch helper failing tests** - `7c2f426` (test)
4. **Task 2 GREEN: CloudWatch helper implementation** - `4ac5026` (feat)

## Files Created/Modified

- `sidecars/audit-log/auditlog.go` — AuditEvent struct (locked schema), Destination interface, Process() stdin loop, StdoutDest, CloudWatchDest (25-event buffer), S3Dest stub
- `sidecars/audit-log/cmd/main.go` — binary entry point: env vars, SIGTERM handler, realCWBackend adapter bridging CWLogsAPI to CloudWatchBackend
- `sidecars/audit-log/audit_log_test.go` — 6 unit tests: shell_command parse, dns_query parse, stdout routing, CW routing via mock, invalid JSON skip, AuditEvent serialization
- `pkg/aws/cloudwatch.go` — CWLogsAPI interface, LogEvent type, EnsureLogGroup, PutLogEvents, GetLogEvents, TailLogs, isAlreadyExists helper
- `pkg/aws/cloudwatch_test.go` — 7 unit tests with mockCWLogsAPI (same pattern as mockTagAPI)
- `go.mod` / `go.sum` — added aws-sdk-go-v2/service/cloudwatchlogs v1.64.1

## Decisions Made

- **Package layout:** cmd/main.go subdirectory required because Go disallows mixing `package main` with `package auditlog` in the same directory
- **CloudWatchDest flush threshold:** 25 events (well within CW 10k limit) — balances latency vs API calls
- **S3Dest scope:** stub with explicit warning log and stdout fallback — silent discard would lose data; full archive is Phase 4 scope as documented in 03-CONTEXT.md
- **GetLogEvents int32 limit:** matches AWS SDK type directly — avoids unnecessary int conversion

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Moved main.go from package root to cmd/ subdirectory**
- **Found during:** Task 1 (audit log sidecar binary)
- **Issue:** Go toolchain disallows `package main` and `package auditlog` in the same directory; tests could not build
- **Fix:** Created `sidecars/audit-log/cmd/main.go` (package main) — `auditlog.go` stays in package root as the importable library
- **Files modified:** sidecars/audit-log/cmd/main.go (created), sidecars/audit-log/main.go (removed)
- **Verification:** `go test ./sidecars/audit-log/...` passes; `go build ./sidecars/audit-log/...` succeeds including cmd
- **Committed in:** 2aa78e4 (Task 1 feat commit)

---

**Total deviations:** 1 auto-fixed (Rule 3 — blocking build issue)
**Impact on plan:** Necessary structural fix; import path `sidecars/audit-log` for the library and `sidecars/audit-log/cmd` for the binary is idiomatic Go. No scope creep.

## Issues Encountered

- Pre-existing `scheduler_test.go` in `pkg/aws/` has 4 placeholder `t.Fatal("not implemented")` tests — these fail when running `./pkg/aws/...` broadly. They pre-date this plan and are out of scope. Not fixed.

## User Setup Required

None — all tests run without AWS credentials. CloudWatch destination requires AWS credentials at runtime only.

## Next Phase Readiness

- `pkg/aws.CWLogsAPI` and helpers ready for `km logs` command (Plan 03-04)
- `auditlog.CloudWatchBackend` interface enables dns-proxy and http-proxy to feed events into the same audit log pipeline
- S3 archive path defined but stubbed — Plan 04 implements full artifact upload
- `scheduler_test.go` stubs in `pkg/aws/` await implementation in Plan 03-03

---
*Phase: 03-sidecar-enforcement-lifecycle-management*
*Completed: 2026-03-22*

## Self-Check: PASSED

- FOUND: sidecars/audit-log/auditlog.go
- FOUND: sidecars/audit-log/cmd/main.go
- FOUND: pkg/aws/cloudwatch.go
- FOUND: pkg/aws/cloudwatch_test.go
- FOUND: .planning/phases/03-sidecar-enforcement-lifecycle-management/03-02-SUMMARY.md
- FOUND commit da8f46d: test(03-02) audit log RED
- FOUND commit 2aa78e4: feat(03-02) audit log GREEN
- FOUND commit 7c2f426: test(03-02) cloudwatch RED
- FOUND commit 4ac5026: feat(03-02) cloudwatch GREEN
