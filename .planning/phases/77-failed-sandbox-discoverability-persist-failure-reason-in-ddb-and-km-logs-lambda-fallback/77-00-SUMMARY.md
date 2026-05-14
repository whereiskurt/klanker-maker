---
phase: 77-failed-sandbox-discoverability-persist-failure-reason-in-ddb-and-km-logs-lambda-fallback
plan: 0
subsystem: testing
tags: [cloudwatchlogs, dependency-injection, dynamodb, mock, cobra, go-testing]

# Dependency graph
requires: []
provides:
  - CWLogsAPI interface extended with FilterLogEvents method
  - NewLogsCmdWithClient DI seam in logs.go mirroring NewStatusCmdWithFetcher pattern
  - mockCWLogsAPI in cloudwatch_test.go stubs FilterLogEvents
  - mockLogsCWLogsAPI in logs_test.go (full CWLogsAPI stub with input capture)
  - TestLogsCmd_PerSandboxGroupPresent smoke test for the DI seam
  - mockSandboxMetadataAPI in cmd/create-handler/main_test.go (full SandboxMetadataAPI stub)
affects: [77-01, 77-02, 77-03, 77-04]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "DI seam via NewXxxWithClient(cfg, client) matching NewStatusCmdWithFetcher pattern"
    - "compile-time interface guard: var _ kmaws.SandboxMetadataAPI = (*mockSandboxMetadataAPI)(nil)"

key-files:
  created:
    - internal/app/cmd/logs_test.go
  modified:
    - pkg/aws/cloudwatch.go
    - pkg/aws/cloudwatch_test.go
    - internal/app/cmd/logs.go
    - cmd/create-handler/main_test.go

key-decisions:
  - "FilterLogEvents added to end of CWLogsAPI interface — minimal diff, real *cloudwatchlogs.Client already satisfies it"
  - "NewLogsCmdWithClient follows NewStatusCmdWithFetcher pattern exactly — nil client builds real cloudwatchlogs client at runtime"
  - "mockSandboxMetadataAPI is a private copy in the create-handler test package — pkg/aws/sandbox_dynamo_test.go is aws_test package and cannot be imported cross-package"

patterns-established:
  - "DI seam pattern: NewXxxCmd(cfg) delegates to NewXxxCmdWithClient(cfg, nil)"
  - "mockSandboxMetadataAPI shape for create-handler tests: tracks last input per method, pre-seedable outputs"

requirements-completed: [TEST-77, INTERFACE-77, DI-77]

# Metrics
duration: 4min
completed: 2026-05-14
---

# Phase 77 Plan 00: Test Infrastructure Summary

**CWLogsAPI widened with FilterLogEvents, NewLogsCmdWithClient DI seam added to logs.go, and mockSandboxMetadataAPI added to create-handler tests — enabling Wave 1+ plans to compile and run against real mockable contracts**

## Performance

- **Duration:** 4 min
- **Started:** 2026-05-14T23:52:32Z
- **Completed:** 2026-05-14T23:56:53Z
- **Tasks:** 3
- **Files modified:** 5

## Accomplishments
- Extended CWLogsAPI interface with FilterLogEvents; real `*cloudwatchlogs.Client` satisfies it natively (no production wrapper needed)
- Added NewLogsCmdWithClient(cfg, client) DI seam to logs.go; NewLogsCmd delegates to it with nil client (production behavior unchanged)
- Added TestLogsCmd_PerSandboxGroupPresent proving the seam works against GetLogEvents happy path
- Added private mockSandboxMetadataAPI in cmd/create-handler/main_test.go with compile-time interface guard

## Task Commits

Each task was committed atomically:

1. **Task 1: Add FilterLogEvents to CWLogsAPI interface and mocks** - `a4f12ab` (feat)
2. **Task 2: Add DI seam to logs.go and TestLogsCmd_PerSandboxGroupPresent** - `6badd38` (feat)
3. **Task 3: Add local mockSandboxMetadataAPI to cmd/create-handler/main_test.go** - `578c5a7` (feat)

## Files Created/Modified
- `pkg/aws/cloudwatch.go` - Added FilterLogEvents to CWLogsAPI interface
- `pkg/aws/cloudwatch_test.go` - Added filterLogEvents fields and stub method to mockCWLogsAPI
- `internal/app/cmd/logs.go` - Refactored to NewLogsCmdWithClient DI seam; runLogs accepts injected CWLogsAPI
- `internal/app/cmd/logs_test.go` - Replaced trivial string test with mockLogsCWLogsAPI + TestLogsCmd_PerSandboxGroupPresent
- `cmd/create-handler/main_test.go` - Added dynamodb/awspkg imports and private mockSandboxMetadataAPI struct

## Decisions Made
- FilterLogEvents placed at end of CWLogsAPI interface (after CreateExportTask) for minimal diff footprint
- NewLogsCmdWithClient uses nil-check to branch between injected client and real cloudwatchlogs.NewFromConfig; mirrors NewStatusCmdWithFetcher pattern exactly
- mockSandboxMetadataAPI is a private copy in create-handler package (pkg/aws/sandbox_dynamo_test.go is package aws_test and cannot be imported cross-package); compile-time guard ensures it stays in sync

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
- `go test ./internal/app/cmd/` run from the parent repo directory vs. the worktree showed different test files (worktree is a separate filesystem path). Running from the worktree cwd confirmed TestLogsCmd_PerSandboxGroupPresent passes correctly. Pre-existing failing tests in the suite (TestProbeCodexPort_Primary, TestStep11d_Success_WritesChannelIDParam, TestAgentList timeout) are out-of-scope and untouched.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Wave 1+ plans can now compile and run against CWLogsAPI (FilterLogEvents available), logs.go DI seam, and create-handler SandboxMetadataAPI mock
- Plan 77-01 can inject mockSandboxMetadataAPI into CreateHandler.DynamoClient to test DynamoDB failure-reason persistence
- Plan 77-04 can use mockLogsCWLogsAPI.filterLogEventsInput/Output to test the CloudWatch fallback path in km logs

---
*Phase: 77-failed-sandbox-discoverability-persist-failure-reason-in-ddb-and-km-logs-lambda-fallback*
*Completed: 2026-05-14*
