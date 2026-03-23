---
phase: 18-loose-ends
plan: "01"
subsystem: km-init-configure
tags: [init, configure, regional-infrastructure, state-bucket, tdd]
dependency_graph:
  requires: []
  provides: [expanded-km-init, state-bucket-configure]
  affects: [km-init, km-configure, km-list, km-status, km-uninit]
tech_stack:
  added: []
  patterns: [InitRunner-interface, exported-testable-functions, omitempty-yaml]
key_files:
  created:
    - internal/app/cmd/init_test.go
  modified:
    - internal/app/cmd/init.go
    - internal/app/cmd/configure.go
    - internal/app/cmd/configure_test.go
    - internal/app/cmd/help/init.txt
key_decisions:
  - Export RunInitWithRunner (not export_test.go) so cmd_test package tests can call the testable core directly
  - Skip-with-warning rather than error for missing directories and unset env vars — aligns with idempotency goal
  - state_bucket uses omitempty so operators who don't provide it get a clean YAML without a blank field
metrics:
  duration: 396s
  completed: "2026-03-23"
  tasks_completed: 2
  files_modified: 5
---

# Phase 18 Plan 01: km init all regional modules + state_bucket in configure Summary

Expanded `km init` from network-only to all 6 regional infrastructure modules and added `state_bucket` prompt/flag to `km configure`.

## Tasks Completed

### Task 1: Expand km init to deploy all 6 regional modules

**Commit:** 074df90

Added `InitRunner` interface, `regionalModule` struct, `regionalModules()` function, and `RunInitWithRunner()` exported function to `internal/app/cmd/init.go`.

Modules deployed in dependency order:
1. network — no env var prereqs
2. dynamodb-budget — no env var prereqs
3. dynamodb-identities — no env var prereqs
4. ses — requires `KM_ROUTE53_ZONE_ID`
5. s3-replication — requires `KM_ARTIFACTS_BUCKET`
6. ttl-handler — requires `KM_ARTIFACTS_BUCKET`

Behavior:
- Missing directories: skip with warning, continue
- Missing env vars: skip with warning, continue
- Apply failure: stop and return error with module name
- Network outputs.json capture preserved after network module

Updated `internal/app/cmd/help/init.txt` to document all 6 modules, env var requirements, and prerequisite notes.

Tests in `init_test.go` (7 tests):
- `TestInitAllModulesOrder` — binary-level test; verifies help output mentions "network"
- `TestRunInitWithRunnerAllModules` — 6 Apply calls in correct order
- `TestRunInitSkipsMissingDirectory` — only network applied when other dirs absent
- `TestRunInitSkipsSESWithoutZoneID` — 5 modules when KM_ROUTE53_ZONE_ID unset
- `TestRunInitSkipsArtifactModulesWithoutBucket` — 4 modules when KM_ARTIFACTS_BUCKET unset
- `TestRunInitStopsOnApplyError` — returns error with module name, stops after failure
- `TestRunInitIdempotent` — second call succeeds

### Task 2: Add state_bucket to km configure wizard

**Commit:** 240b346

Added `StateBucket string \`yaml:"state_bucket,omitempty"\`` to `platformConfig` struct in `configure.go`.

Added:
- `--state-bucket` flag to `newConfigureCmdWithIO`
- `stateBucket` parameter to `runConfigure` signature
- Interactive prompt: "S3 state bucket for sandbox metadata (used by km list/status)" after region prompt
- `StateBucket: stateBucket` in `platformConfig` build

Behavior:
- When `--state-bucket` provided (non-interactive): written to YAML
- When empty: omitted from YAML (omitempty)
- Interactive: prompted after Primary region, optional (empty OK)

New tests added to `configure_test.go`:
- `TestConfigureStateBucketFlag` — verifies `--state-bucket` value written to km-config.yaml
- `TestConfigureStateBucketOmittedWhenEmpty` — verifies state_bucket absent when not provided

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Pre-existing test compilation errors in cmd_test package**

- **Found during:** Task 1 test execution
- **Issue:** `create_github_test.go` (package `cmd`) referenced `generateAndStoreGitHubToken` expecting `*ssm.Client` but the function already used `SSMGetPutAPI` interface in `create.go`. On closer inspection, `create.go` already had the correct implementation; the file was consistently using the interface. The initial compiler error message was misleading — the actual root cause was that `uninit_test.go` and `init_test.go` both referenced functions not yet exported. `uninit_test.go` referenced `cmd.RunUninitWithDeps` which exists in `uninit.go` (already created in Phase 18 pre-work). `init_test.go` had stale `runInitWithRunner` (lowercase) references.
- **Fix:** Updated all `runInitWithRunner` calls in `init_test.go` to `cmd.RunInitWithRunner` (the exported function). This resolved compilation.
- **Files modified:** `internal/app/cmd/init_test.go`
- **Commit:** Part of 074df90

## Self-Check: PASSED

- FOUND: internal/app/cmd/init.go
- FOUND: internal/app/cmd/init_test.go
- FOUND: internal/app/cmd/configure.go
- FOUND: internal/app/cmd/configure_test.go
- FOUND: internal/app/cmd/help/init.txt
- FOUND: 074df90 (Task 1 commit)
- FOUND: 240b346 (Task 2 commit)
