---
phase: 03-sidecar-enforcement-lifecycle-management
plan: 05
subsystem: cli
tags: [cobra, s3, cloudwatch, tabwriter, dependency-injection, km-list, km-status, km-logs]

# Dependency graph
requires:
  - phase: 03-sidecar-enforcement-lifecycle-management
    plan: 04
    provides: "SandboxMetadata type in pkg/aws/metadata.go; metadata.json written to S3 by km create"
  - phase: 03-sidecar-enforcement-lifecycle-management
    plan: 02
    provides: "TailLogs in pkg/aws/cloudwatch.go for km logs CloudWatch streaming"
provides:
  - "NewListCmd: km list with S3 scan, --tags, --json flags"
  - "NewStatusCmd: km status <sandbox-id> with resource ARNs and TTL expiry"
  - "NewLogsCmd: km logs <sandbox-id> --follow for CloudWatch tail"
  - "SandboxRecord type and ListAllSandboxesByS3/ListAllSandboxesByTags in pkg/aws/sandbox.go"
  - "ReadSandboxMetadata helper for single-sandbox metadata read"
  - "SandboxLister and SandboxFetcher interfaces exported for DI in tests"
affects:
  - 03-06
  - 04-sidecar-hardening

# Tech tracking
tech-stack:
  added: ["text/tabwriter (stdlib — human-readable table output)"]
  patterns:
    - "DI via exported NewListCmdWithLister / NewStatusCmdWithFetcher — real clients injected in prod, fake in tests"
    - "SandboxLister interface decouples list command from S3/tag client implementations"
    - "SandboxFetcher interface decouples status command from S3+tag combo fetcher"

key-files:
  created:
    - "internal/app/cmd/list.go — NewListCmd with --json/--tags, tabwriter table, SandboxLister interface"
    - "internal/app/cmd/status.go — NewStatusCmd, SandboxFetcher interface, ARN + TTL output"
    - "internal/app/cmd/logs.go — NewLogsCmd --follow/--stream, delegates to pkg/aws.TailLogs"
    - "internal/app/cmd/list_test.go — TestListCmd_TableOutput/JSONOutput/Empty with fakeLister"
    - "internal/app/cmd/status_test.go — TestStatusCmd_Found/NotFound with fakeFetcher"
    - "internal/app/cmd/logs_test.go — TestLogsCmd_ConstructsCorrectLogGroup"
    - "pkg/aws/sandbox.go — SandboxRecord, S3ListAPI, ListAllSandboxesByS3, ListAllSandboxesByTags, ReadSandboxMetadata"
  modified:
    - "internal/app/cmd/root.go — added AddCommand for NewListCmd, NewStatusCmd, NewLogsCmd"

key-decisions:
  - "SandboxLister/SandboxFetcher DI interfaces exported (uppercase) so cmd_test (external package) can inject fake implementations"
  - "SandboxRecord declared in pkg/aws/sandbox.go (not cmd package) — shared type between list, status, and future commands"
  - "km logs --stream flag defaults to 'audit' matching audit-log sidecar stream name from Plan 03-02"
  - "ListAllSandboxesByTags returns Resources-populated stubs without metadata.json read — tag scan is a presence check, not a metadata scan"
  - "sandboxPrefixKey helper kept but unexported — reserved for future write path in km create"

patterns-established:
  - "DI pattern for Cobra commands: NewXxxCmd(cfg) calls NewXxxCmdWithDeps(cfg, nil); nil triggers real AWS client creation; non-nil used in tests"
  - "SandboxLister interface: ListSandboxes(ctx, useTagScan bool) — one method covers both S3 and tag scan modes"
  - "computeTTLRemaining() helper converts *time.Time to human-readable 'Xh00m' or 'expired' string"

requirements-completed: [PROV-03, PROV-04]

# Metrics
duration: 10min
completed: 2026-03-22
---

# Phase 3 Plan 05: km list, km status, km logs CLI Commands Summary

**Operational visibility layer: km list (S3/tag scan + tabwriter table), km status (S3 metadata + tag ARNs), km logs (CloudWatch tail with --follow), all with dependency-injection interfaces for unit testing**

## Performance

- **Duration:** 10 min
- **Started:** 2026-03-22T04:57:19Z
- **Completed:** 2026-03-22T05:07:17Z
- **Tasks:** 2
- **Files modified:** 9

## Accomplishments

- `km list` reads sandbox metadata.json from S3, prints SANDBOX ID/PROFILE/SUBSTRATE/REGION/STATUS/TTL table with tabwriter; --json outputs valid JSON array; --tags switches to ResourceGroupsTagging scan
- `km status <sandbox-id>` reads metadata.json + tag API ARNs, displays full detail including resource list and TTL expiry timestamp
- `km logs <sandbox-id>` connects to CloudWatch log group `/km/sandboxes/<sandbox-id>/`, defaults to "audit" stream (matching Plan 03-02 audit-log sidecar), --follow polls until Ctrl+C (context cancellation)
- All three commands registered in root.go and visible in `km --help`
- Unit tests use exported DI interfaces (SandboxLister, SandboxFetcher) so tests run without AWS credentials

## Task Commits

1. **Task 1: km list and km status commands (PROV-03, PROV-04)** - `9637b24` (feat)
2. **Task 2: km logs command + root.go registration** - `8ce7aa3` (feat)

**Plan metadata:** _(docs commit follows)_

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/aws/sandbox.go` — SandboxRecord type, S3ListAPI interface, ListAllSandboxesByS3, ListAllSandboxesByTags, ReadSandboxMetadata
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/list.go` — NewListCmd, NewListCmdWithLister (DI), SandboxLister interface, awsSandboxLister
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/status.go` — NewStatusCmd, NewStatusCmdWithFetcher (DI), SandboxFetcher interface, awsSandboxFetcher
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/logs.go` — NewLogsCmd with --follow/--stream flags
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/list_test.go` — TestListCmd_TableOutput, TestListCmd_JSONOutput, TestListCmd_Empty
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/status_test.go` — TestStatusCmd_Found, TestStatusCmd_NotFound
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/logs_test.go` — TestLogsCmd_ConstructsCorrectLogGroup
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/root.go` — AddCommand for list, status, logs

## Decisions Made

- Exported `NewListCmdWithLister` and `NewStatusCmdWithFetcher` so external `cmd_test` package can inject fakes without needing an internal test helper
- `SandboxRecord` placed in `pkg/aws` (not `cmd`) so it can be shared with future plans that read sandbox state (lifecycle manager, scheduler)
- `km logs --stream audit` default matches the CloudWatch stream name written by the audit-log sidecar (established in Plan 03-02)
- Tag scan (`--tags`) returns stubs with Resources but no metadata — this is correct since tag scan is a presence/orphan detector, not a metadata reader

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Remove duplicate SandboxMetadata declaration from sandbox.go**
- **Found during:** Task 1 (initial build after creating sandbox.go)
- **Issue:** `pkg/aws/metadata.go` already declared `SandboxMetadata` from Plan 03-04; adding it to `sandbox.go` caused redeclaration compile error
- **Fix:** Removed the duplicate struct from `sandbox.go`; the type from `metadata.go` is used directly (same package)
- **Files modified:** `pkg/aws/sandbox.go`
- **Verification:** `go build ./...` passes
- **Committed in:** `9637b24` (Task 1 commit)

**2. [Rule 3 - Blocking] Pre-existing unused scheduler import in destroy.go**
- **Found during:** Task 2 (running full test suite via buildKM)
- **Issue:** `destroy.go` had `"github.com/aws/aws-sdk-go-v2/service/scheduler"` appearing as unused in a broken build context (turned out to be a goimports artifact — the import IS used at line 150)
- **Fix:** Investigation revealed the import is legitimately used (`scheduler.NewFromConfig`); the earlier build error was a transient tool artifact. No code change required.
- **Files modified:** None (false alarm)
- **Verification:** `go build ./...` passes with scheduler import present
- **Committed in:** N/A

---

**Total deviations:** 1 real auto-fix (Rule 3 blocking — duplicate type declaration)
**Impact on plan:** Minor. The SandboxMetadata type was already provided by Plan 03-04's metadata.go; sandbox.go simply uses it across the package without redeclaring.

## Issues Encountered

- linter/goimports tooling repeatedly modified files during editing (attempted to restore removed imports, triggering "file modified since read" errors). Resolved by re-reading files before each edit.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- PROV-03 and PROV-04 requirements fulfilled: `km list` and `km status` operational
- `km logs` provides the visibility interface needed to access the audit-log sidecar output (OBSV-03)
- Phase 3 complete: sidecar enforcement, lifecycle management, and operational CLI visibility all implemented
- Phase 4 can proceed: all three list/status/logs commands are available for use in Phase 4 testing

---
*Phase: 03-sidecar-enforcement-lifecycle-management*
*Completed: 2026-03-22*
