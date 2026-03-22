---
phase: 06-budget-enforcement-platform-configuration
plan: "09"
subsystem: cli-shell
tags: [shell, ssm, ecs-exec, substrate-detection, km-cli, CONF-05]
dependency_graph:
  requires: [pkg/aws/sandbox.go (SandboxRecord), internal/app/cmd/status.go (SandboxFetcher, newRealFetcher)]
  provides: [internal/app/cmd/shell.go (NewShellCmd)]
  affects: [internal/app/cmd/root.go]
tech_stack:
  added: [os/exec (AWS CLI subprocess dispatch), ShellExecFunc DI pattern]
  patterns: [SandboxFetcher DI, helpFS embed, cobra.ExactArgs(1), TDD red/green]
key_files:
  created:
    - internal/app/cmd/shell.go
    - internal/app/cmd/shell_test.go
    - internal/app/cmd/help/shell.txt
    - internal/app/cmd/spot_rate.go
  modified:
    - internal/app/cmd/root.go
key_decisions:
  - "ShellExecFunc package-level type for DI — tests capture exec.Cmd.Args without executing AWS CLI"
  - "extractResourceID strips final slash-segment from ARN — instance IDs and task IDs extracted the same way"
  - "ECS dispatch passes full cluster/task ARNs (not just IDs) — aws ecs execute-command accepts ARNs directly"
  - "Rule 3 auto-fix: spot_rate.go created to provide staticSpotRate() referenced in create.go and spot_rate_test.go but missing from package"
metrics:
  duration: 286s
  completed_date: "2026-03-22"
  tasks_completed: 2
  files_created: 4
  files_modified: 1
---

# Phase 06 Plan 09: km shell — Substrate-Abstracted Interactive Shell Summary

**One-liner:** `km shell <sandbox-id>` auto-detects EC2/ECS substrate and dispatches SSM Session Manager or ECS Exec without operator knowledge of the underlying AWS incantation.

## What Was Built

The `km shell` command gives operators a single entry point to an interactive shell regardless of whether the sandbox runs on EC2 or ECS. The command fetches sandbox metadata (via the existing `SandboxFetcher` interface), checks running state, identifies the substrate, and dispatches the correct AWS CLI call with full I/O forwarding (stdin/stdout/stderr attached to the terminal).

**EC2 dispatch:**
```
aws ssm start-session --target <instance-id> --region <region>
```

**ECS dispatch:**
```
aws ecs execute-command --cluster <cluster-arn> --task <task-arn> \
  --interactive --command /bin/bash --region <region>
```

## Architecture

`shell.go` follows the same DI pattern established in `status.go`:
- `NewShellCmd(cfg)` — production path, creates real fetcher from AWS config
- `NewShellCmdWithFetcher(cfg, fetcher, execFn)` — DI variant for tests

The `ShellExecFunc` type (`func(*exec.Cmd) error`) replaces `cmd.Run()` in tests, allowing argument inspection without network calls. The `defaultShellExec` variable holds the real implementation.

ARN parsing uses `extractResourceID` and `findResourceARN` helpers that scan the Resources slice for ARNs containing a pattern (e.g., `:instance/`, `:cluster/`, `:task/`).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Created missing `spot_rate.go`**
- **Found during:** Task 1 (full test suite build)
- **Issue:** `internal/app/cmd/create.go` calls `staticSpotRate(instanceType)` and `internal/app/cmd/spot_rate_test.go` tests it, but no file in the package defines the function. Both files were added by plan 06-08 (untracked) without the implementation.
- **Fix:** Created `spot_rate.go` with static spot price table for common EC2 families (t3, t3a, c5, m5, r5, g4dn) and a $0.10/hr conservative fallback for unknown types.
- **Files modified:** `internal/app/cmd/spot_rate.go` (new)
- **Commit:** part of feat(06-09) commit c65184c

## Verification Results

| Check | Result |
|-------|--------|
| `go test ./internal/app/cmd/ -run TestShell -v` | 5/5 PASS |
| `go test ./internal/app/cmd/ -count=1` | PASS (no regressions) |
| `go build ./cmd/km/` | OK |
| `grep NewShellCmd internal/app/cmd/root.go` | line 46 confirmed |
| `go vet ./internal/app/cmd/` | no issues |
| `km shell --help` | shows usage with EC2/ECS substrate details |

## Commits

| Hash | Message |
|------|---------|
| 97f1516 | test(06-09): add failing tests for km shell substrate detection |
| c65184c | feat(06-09): implement km shell with substrate-aware dispatch |
| 33417ea | feat(06-09): register NewShellCmd in root.go |

## Self-Check: PASSED

All created files exist on disk. All commits verified in git log.
