---
phase: 52-clone-sandbox
plan: "02"
subsystem: clone-command
tags: [clone, ssm, s3, workspace, cobra, tdd]
dependency_graph:
  requires: [52-01]
  provides: [km-clone-command, BuildWorkspaceStagingCmd, GenerateCloneAliases, NewCloneCmdWithDeps]
  affects: [internal/app/cmd/clone.go, internal/app/cmd/clone_test.go, internal/app/cmd/root.go]
tech_stack:
  added: []
  patterns: [cobra-with-deps-DI, SSMSendAPI-injection, SandboxFetcher-injection, tdd-red-green]
key_files:
  created:
    - internal/app/cmd/clone.go
    - internal/app/cmd/clone_test.go
  modified:
    - internal/app/cmd/root.go
decisions:
  - "Use injected SSMSendAPI/SandboxFetcher for test isolation (same WithDeps pattern as agent.go)"
  - "Workspace staging uploads once to source-scoped key, post-provision SSM download sends to each clone"
  - "BuildWorkspaceStagingCmd exported for unit testability without SSM execution"
  - "GenerateCloneAliases exported for unit testability; single=as-is, multi=suffix -N"
  - "fetchStoredProfile returns stub bytes in test mode (ssmClient != nil) to avoid S3 in unit tests"
metrics:
  duration: "~3min"
  completed_date: "2026-04-13"
  tasks_completed: 2
  files_modified: 3
---

# Phase 52 Plan 02: km clone Command — Workspace Staging and Multi-Clone Summary

km clone command with SSM workspace staging, S3 profile fetch, multi-clone alias generation, ECS rejection, non-running source error, and ClonedFrom DynamoDB lineage write — registered in root, fully unit tested via TDD.

## Tasks Completed

| # | Task | Commit | Files |
|---|------|--------|-------|
| 1 RED | Add failing tests for clone command | 5773bc1 | clone_test.go |
| 1 GREEN | Implement clone.go with all flags and workspace staging | 46b1271 | clone.go |
| 2 | Register clone command in root and verify build | eddb831 | root.go |

## Decisions Made

1. **WithDeps DI pattern** — mirrors agent.go; SandboxFetcher and SSMSendAPI injected for tests, nil = real AWS
2. **Single staging upload** — workspace tarred from source once with timestamp key; each clone downloads post-provision via SSM, avoiding N uploads
3. **Exported test helpers** — BuildWorkspaceStagingCmd and GenerateCloneAliases exported so tests can verify shell command structure and alias logic without executing SSM
4. **Test-mode stub profile** — fetchStoredProfile returns minimal YAML stub when ssmClient is non-nil to keep unit tests self-contained (no S3 mocking needed for error-path tests)

## Deviations from Plan

None — plan executed exactly as written.

## Verification

- `go test ./internal/app/cmd/ -run TestClone -v -count=1` — PASS (9 tests)
- `go test ./pkg/aws/ -run TestClonedFrom -v -count=1` — PASS (6 tests, from Plan 01)
- `make build` — km v0.0.302 built cleanly
- `./km clone --help` — shows correct usage, all 4 flags (--alias, --count, --no-copy, --verbose)
- `go vet ./internal/app/cmd/ ./pkg/aws/` — clean

## Self-Check: PASSED

- internal/app/cmd/clone.go — NewCloneCmd, NewCloneCmdWithDeps, BuildWorkspaceStagingCmd, GenerateCloneAliases present
- internal/app/cmd/clone_test.go — 9 TestClone_* tests present
- internal/app/cmd/root.go — NewCloneCmd registered after NewRsyncCmd
- Commits 5773bc1, 46b1271, eddb831 verified in git log
