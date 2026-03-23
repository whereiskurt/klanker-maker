---
phase: 15-km-doctor-platform-health-check-and-bootstrap-verification
plan: 01
subsystem: cli
tags: [cobra, aws, sts, s3, dynamodb, kms, organizations, ssm, ec2, health-check, doctor, tdd]

requires:
  - phase: 13-github-app-token-integration-scoped-repo-access-for-sandboxes
    provides: SSMWriteAPI pattern, configure github SSM parameter store paths
  - phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust
    provides: IdentityTableName config field, status.go ANSI constants and isTerminal()
  - phase: 06-budget-enforcement-platform-configuration
    provides: BudgetTableName config field, SandboxLister interface
provides:
  - km doctor command with parallel health checks, --json, --quiet, --exit-code-1-on-error
  - CheckResult type with Name/Status/Message/Remediation (JSON serializable)
  - DoctorConfigProvider interface and appConfigAdapter for *config.Config
  - Narrow AWS DI interfaces: STSCallerAPI, S3HeadBucketAPI, DynamoDescribeAPI, KMSDescribeAPI, OrgsListPoliciesAPI, SSMReadAPI, EC2DescribeAPI
  - DoctorDeps struct with all injectable AWS clients
  - runChecks parallel executor via WaitGroup + mutex-protected results slice
  - 9 check functions: checkConfig, checkCredential, checkStateBucket, checkDynamoTable, checkKMSKey, checkSCP, checkGitHubConfig, checkRegionVPC, checkSandboxSummary
affects:
  - phase-16-documentation-refresh (km doctor is a new command that needs operator guide documentation)

tech-stack:
  added: []
  patterns:
    - DoctorConfigProvider interface wraps *config.Config so tests inject testDoctorConfig without real AWS config
    - Nil-client guard on every check function — nil means skip, never panic
    - Identity table check demotes CheckError to CheckWarn in buildChecks caller (optional feature pattern)
    - KM_REPLICA_REGION env var drives per-region EC2 client initialization (same pattern as existing replica region logic)

key-files:
  created:
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go
    - internal/app/cmd/help/doctor.txt
  modified:
    - internal/app/cmd/root.go

key-decisions:
  - "DoctorConfigProvider interface abstracts *config.Config so tests use testDoctorConfig without requiring real AWS or yaml files"
  - "Nil AWS client in any DoctorDeps field → CheckSkipped for that check (non-fatal, never panics)"
  - "checkGitHubConfig returns CheckWarn (not ERROR) on ParameterNotFound — GitHub integration is optional"
  - "Identity table check demoted from CheckError to CheckWarn in buildChecks — km-identities is an optional feature"
  - "NewDoctorCmdWithDeps takes interface{} for cfg to accept both *appcfg.Config (production) and DoctorConfigProvider (tests) via type switch"
  - "runChecks sorts results by Name for stable output regardless of goroutine completion order"

patterns-established:
  - "DoctorConfigProvider: interface wrapping *config.Config for testability — extend for new config-consuming commands"
  - "Nil-client guard pattern: every check function handles nil client → CheckSkipped before any AWS API call"
  - "buildChecks assembles all check closures; runDoctor orchestrates parallel execution — separation of concerns"

requirements-completed: [DOCTOR-CMD, DOCTOR-CHECKS, DOCTOR-OUTPUT, DOCTOR-JSON, DOCTOR-QUIET, DOCTOR-EXIT, DOCTOR-PARALLEL]

duration: 9min
completed: 2026-03-23
---

# Phase 15 Plan 01: km doctor — Platform Health Check Summary

**km doctor command with 9 parallel AWS health checks, --json/--quiet flags, exit code 1 on error, and narrow DI interfaces for full unit test coverage**

## Performance

- **Duration:** 9 min
- **Started:** 2026-03-23T04:38:12Z
- **Completed:** 2026-03-23T04:47:21Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments

- Implemented all 9 check functions (config, credentials, state bucket, budget table, identity table, KMS key, SCP, GitHub App config, per-region VPC, sandbox summary) each returning `CheckResult{Name, Status, Message, Remediation}`
- Parallel execution via `runChecks(ctx, checks)` using `sync.WaitGroup` + `sync.Mutex`-protected slice, sorted by Name for stable output
- `NewDoctorCmdWithDeps` enables full unit test coverage with mock AWS clients — no real AWS calls in any test
- `--json` flag produces valid JSON array via `json.NewEncoder`; `--quiet` suppresses OK/Skipped results in both text and JSON modes
- Exit code 1 when any check returns `CheckError` (via `fmt.Errorf` return from `RunE`)
- 31 unit tests: 24 for check functions, 7 for command shape/flags/output/exit-code behavior

## Task Commits

Each task was committed atomically:

1. **Task 1: CheckResult types, DI interfaces, all check functions, and tests** - `6014986` (test + feat)
2. **Task 2: Cobra command wiring, JSON/quiet output, exit code, root registration, help text** - `b53bb5c` (feat)

## Files Created/Modified

- `internal/app/cmd/doctor.go` — CheckStatus/CheckResult types, 7 narrow DI interfaces, DoctorConfigProvider, DoctorDeps, 9 check functions, runChecks, formatCheckLine, filterNonOK, NewDoctorCmd, NewDoctorCmdWithDeps, runDoctor, buildChecks, initRealDeps (797 lines)
- `internal/app/cmd/doctor_test.go` — 7 mock AWS clients, 31 unit tests covering all check functions and command behavior (727 lines)
- `internal/app/cmd/help/doctor.txt` — Help text with check categories, flags, symbols, exit code documentation (78 lines)
- `internal/app/cmd/root.go` — Added `root.AddCommand(NewDoctorCmd(cfg))` after ShellCmd registration

## Decisions Made

- **DoctorConfigProvider interface** wraps `*config.Config` via `appConfigAdapter` so test code uses `testDoctorConfig` without needing real files or env vars — consistent with established DI patterns in the codebase
- **Nil-client guard** on every check function (not just the orchestrator) — each function is independently testable and never panics on nil
- **checkGitHubConfig → CheckWarn** on ParameterNotFound — GitHub App integration is optional; operators who haven't run `km configure github` should see a warning, not an error that blocks CI
- **Identity table → CheckWarn** — demotion happens in `buildChecks` caller so `checkDynamoTable` remains a pure function returning CheckError; callers control severity
- **`NewDoctorCmdWithDeps` takes `interface{}`** for cfg parameter — allows accepting both `*appcfg.Config` and `DoctorConfigProvider` via type switch without changing the production `NewDoctorCmd(cfg *appcfg.Config)` signature

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Added nil-client guards to checkDynamoTable, checkKMSKey, checkStateBucket**
- **Found during:** Task 2 (TestDoctorCmd_AllChecksPass_ExitZero)
- **Issue:** When DoctorDeps has nil clients, check functions called methods on nil pointers causing panic
- **Fix:** Added `if client == nil { return CheckResult{Status: CheckSkipped} }` guard to each function
- **Files modified:** internal/app/cmd/doctor.go
- **Verification:** TestDoctorCmd_AllChecksPass_ExitZero passes; nil clients produce CheckSkipped results
- **Committed in:** b53bb5c (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 bug)
**Impact on plan:** Essential for correctness — nil clients are the default in all-OK test paths.

## Issues Encountered

- `testConfig` in tests was missing `GetAWSProfile`, `GetStateBucket`, `GetBudgetTableName`, `GetIdentityTableName` to satisfy `DoctorConfigProvider` — added the methods to the struct (compile error caught immediately)
- `TestDoctorCmd_AllChecksPass_ExitZero` initially used empty `minimalConfig()` which caused `checkConfig` to return CheckError for missing fields — fixed by populating all required fields in `minimalConfig()`

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- `km doctor` is fully functional and can be used for CI gating (`km doctor && km create profile.yaml`)
- Phase 16 (documentation refresh) should document `km doctor` in the operator guide including all check categories, flags, and remediation patterns
- The `KM_REPLICA_REGION` env var drives per-region EC2 VPC checks — this is already documented in help/doctor.txt

## Self-Check: PASSED

- FOUND: internal/app/cmd/doctor.go (797 lines, min 300)
- FOUND: internal/app/cmd/doctor_test.go (727 lines, min 200)
- FOUND: internal/app/cmd/help/doctor.txt (78 lines, min 10)
- FOUND commit 6014986 (Task 1)
- FOUND commit b53bb5c (Task 2)
- All 31 doctor unit tests pass
- go vet clean, go build ./cmd/km/ succeeds

---
*Phase: 15-km-doctor-platform-health-check-and-bootstrap-verification*
*Completed: 2026-03-23*
