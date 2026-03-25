---
phase: 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements
plan: "01"
subsystem: budget-display, cloudwatch-logs
tags: [budget, precision, cloudwatch, s3-export, teardown, iam, terraform]
dependency_graph:
  requires: []
  provides:
    - 4-decimal budget display in km status, km budget add, km create, ConfigUI
    - ExportSandboxLogs function in pkg/aws/cloudwatch.go
    - CloudWatch log export before deletion in km destroy and TTL Lambda
  affects:
    - internal/app/cmd/status.go
    - internal/app/cmd/budget.go
    - internal/app/cmd/create.go
    - internal/app/cmd/destroy.go
    - cmd/configui/handlers_budget.go
    - pkg/aws/cloudwatch.go
    - cmd/ttl-handler/main.go
    - infra/modules/ttl-handler/v1.0.0/main.tf
tech_stack:
  added: []
  patterns:
    - "ExportSandboxLogs: fire-and-forget async export (non-fatal, warn-and-continue)"
    - "TDD: RED (failing tests) then GREEN (implementation) per task"
key_files:
  created: []
  modified:
    - internal/app/cmd/status.go
    - internal/app/cmd/budget.go
    - internal/app/cmd/create.go
    - internal/app/cmd/destroy.go
    - cmd/configui/handlers_budget.go
    - pkg/aws/cloudwatch.go
    - pkg/aws/cloudwatch_test.go
    - pkg/lifecycle/idle_test.go
    - cmd/ttl-handler/main.go
    - infra/modules/ttl-handler/v1.0.0/main.tf
decisions:
  - "Export is fire-and-forget (non-fatal): log deletion proceeds immediately after CreateExportTask"
  - "ExportSandboxLogs returns nil for ResourceNotFoundException (no logs = nothing to export)"
  - "S3 bucket policy restricts logs.amazonaws.com with aws:SourceAccount condition for security"
metrics:
  duration: "706s"
  completed: "2026-03-25"
  tasks: 2
  files_modified: 10
---

# Phase 21 Plan 01: Budget Display Precision and CloudWatch Log Export Summary

Budget amounts now display with 4 decimal places (e.g. "$0.0012" instead of "$0.00"), and CloudWatch logs are exported to S3 via an async CreateExportTask call before log group deletion in both the CLI destroy path and TTL Lambda teardown path.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Budget display precision — change %.2f to %.4f | 4f714d7 | status.go, budget.go, create.go, handlers_budget.go |
| 1 RED | Failing tests for 4-decimal precision | 5bde62e | handlers_budget_test.go |
| 2 RED | Failing tests for ExportSandboxLogs | f178abe | cloudwatch_test.go |
| 2 | CloudWatch log export to S3 before deletion | acb0a46 | cloudwatch.go, cloudwatch_test.go, idle_test.go, destroy.go, ttl-handler/main.go, main.tf |

## What Was Built

### Task 1: Budget Display Precision (4 decimal places)

All user-facing budget format strings changed from `%.2f` to `%.4f`:

- `internal/app/cmd/status.go`: Compute, AI, and per-model breakdown lines
- `internal/app/cmd/budget.go`: Budget-updated confirmation message
- `internal/app/cmd/create.go`: Budget-limits-set confirmation message
- `cmd/configui/handlers_budget.go`: `formatUSD` helper function

Sub-penny AI charges like $0.0012 now display correctly instead of rounding to "$0.00".

### Task 2: CloudWatch Log Export to S3

**New interface method:** `CWLogsAPI.CreateExportTask` added to `pkg/aws/cloudwatch.go`.

**New function:** `ExportSandboxLogs(ctx, client, sandboxID, destBucket)`:
- Log group: `/km/sandboxes/{sandboxID}/`
- Destination prefix: `logs/{sandboxID}`
- Time range: last 7 days (matching retention policy)
- Returns nil for ResourceNotFoundException (idempotent, no logs = nothing to export)
- Returns wrapped error for other failures

**Integration in `destroy.go`** (Step 13): Calls `ExportSandboxLogs` before `DeleteSandboxLogGroup`, non-fatal.

**Integration in `ttl-handler/main.go`** `terraformDestroy`: Same pattern, non-fatal.

**IAM policy update** (`infra/modules/ttl-handler/v1.0.0/main.tf`): Added `logs:CreateExportTask` and `logs:DescribeExportTasks` to the `cloudwatch_logs` policy.

**S3 bucket policy** (new `aws_s3_bucket_policy.artifacts_logs_export`): Grants `logs.amazonaws.com` `s3:PutObject` on `${artifact_bucket_arn}/logs/*` with `aws:SourceAccount` condition.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Removed unused imports from create.go**
- **Found during:** Task 2 (full test suite run exposed build failure)
- **Issue:** `create.go` had pre-existing uncommitted changes adding `cryptorand "crypto/rand"` and `"encoding/hex"` imports that were never used, causing compile errors
- **Fix:** Removed both unused imports
- **Files modified:** `internal/app/cmd/create.go`
- **Commit:** acb0a46 (bundled with Task 2)

### Pre-existing Test Failures (out of scope)

- `TestBootstrapSCPApplyPath`: Requires live AWS KMS/SSO credentials — pre-existing, not caused by this plan
- `TestDestroyCmd_InvalidSandboxID`: Intermittent binary caching flakiness — pre-existing, passes in isolation

## Self-Check: PASSED

Files created/modified exist:
- internal/app/cmd/status.go: updated with %.4f
- internal/app/cmd/budget.go: updated with %.4f
- internal/app/cmd/create.go: updated with %.4f, unused imports removed
- cmd/configui/handlers_budget.go: formatUSD now %.4f
- pkg/aws/cloudwatch.go: ExportSandboxLogs function + CreateExportTask in interface
- internal/app/cmd/destroy.go: ExportSandboxLogs call before DeleteSandboxLogGroup
- cmd/ttl-handler/main.go: ExportSandboxLogs call before DeleteSandboxLogGroup
- infra/modules/ttl-handler/v1.0.0/main.tf: CreateExportTask in IAM + S3 bucket policy
