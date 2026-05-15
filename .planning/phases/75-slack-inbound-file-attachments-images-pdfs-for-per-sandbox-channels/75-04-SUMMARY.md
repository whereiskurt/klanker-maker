---
phase: 75-slack-inbound-file-attachments-images-pdfs-for-per-sandbox-channels
plan: "04"
subsystem: slack-bridge-iam-lifecycle-scopes
tags: [slack, iam, terraform, s3, lifecycle, scope-check, tdd]
dependency_graph:
  requires: [75-01]
  provides: [bridge-s3-write-permission, s3-lifecycle-slack-inbound, files-read-scope-enforcement]
  affects: [75-06-uat, lambda-slack-bridge, km-slack-init, km-doctor]
tech_stack:
  added: [infra/modules/s3-artifacts-lifecycle/v1.0.0]
  patterns: [terraform-module-no-required-providers, terragrunt-env-var-sourcing, tdd-red-green]
key_files:
  created:
    - infra/modules/s3-artifacts-lifecycle/v1.0.0/main.tf
    - infra/modules/s3-artifacts-lifecycle/v1.0.0/variables.tf
    - infra/live/use1/s3-artifacts-lifecycle/terragrunt.hcl
  modified:
    - infra/modules/lambda-slack-bridge/v1.0.0/main.tf
    - internal/app/cmd/slack.go
    - internal/app/cmd/slack_test.go
    - internal/app/cmd/doctor_slack.go
    - internal/app/cmd/doctor_slack_inbound_test.go
decisions:
  - "Scoped s3:PutObject to slack-inbound/* prefix only (principle of least privilege — mirrors transcript_s3_read pattern)"
  - "memory_size bumped 256→1024 to accommodate 100MB in-memory file buffering for PutObject retry-rewindability (Pitfall 2)"
  - "New standalone s3-artifacts-lifecycle module (not embedded in bridge module) — future phases can extend it (transcripts/ prefix candidate)"
  - "artifacts_bucket sourced via get_env(KM_ARTIFACTS_BUCKET) matching lambda-slack-bridge convention"
  - "Pre-existing TestUnlockCmd_RequiresStateBucket failure is out-of-scope (confirmed failing before these changes)"
metrics:
  duration: "~12 minutes"
  completed: "2026-05-15"
  tasks: 3
  files_modified: 7
  files_created: 3
---

# Phase 75 Plan 04: Platform Prerequisites (IAM + Lifecycle + Scope Checks) Summary

**One-liner:** Bridge IAM s3:PutObject scoped to slack-inbound/*, 256→1024MB memory bump, new 30-day S3 lifecycle module, and files:read scope enforcement in km slack init + km doctor.

## Tasks Completed

### Task 04-01: Bridge IAM + memory_size 1024
**Commit:** `09d7423`
**Files:** `infra/modules/lambda-slack-bridge/v1.0.0/main.tf`

Added `aws_iam_role_policy.slack_bridge_files_s3_write` immediately after the existing `slack_bridge_transcript_s3_read` policy. Policy is:
- Gated on `var.artifacts_bucket != "" ? 1 : 0` (mirrors transcript read pattern)
- Scoped to `arn:aws:s3:::${var.artifacts_bucket}/slack-inbound/*` only — never bucket-wide
- Action: `s3:PutObject` only

Bumped `memory_size` from 256 to 1024 with a Phase 75 / Pitfall 2 comment explaining the retry-rewindability requirement for 100MB file buffering.

### Task 04-02: s3-artifacts-lifecycle module + regional wiring
**Commit:** `f2ae068`
**Files:** `infra/modules/s3-artifacts-lifecycle/v1.0.0/main.tf`, `infra/modules/s3-artifacts-lifecycle/v1.0.0/variables.tf`, `infra/live/use1/s3-artifacts-lifecycle/terragrunt.hcl`

Created new standalone Terraform module:
- `aws_s3_bucket_lifecycle_configuration` with rule `slack-inbound-30day` (status=Enabled, prefix=`slack-inbound/`, expiration=30 days)
- Single input variable `bucket_name` (no `required_providers` per project memory `project_terragrunt_providers_in_root`)
- Regional terragrunt wiring follows lambda-slack-bridge convention: `get_env("KM_ARTIFACTS_BUCKET", "")` for bucket name sourcing

### Task 04-03: files:read scope checks + TDD tests
**Commit:** `8e5b86b`
**Files:** `internal/app/cmd/slack.go`, `internal/app/cmd/slack_test.go`, `internal/app/cmd/doctor_slack.go`, `internal/app/cmd/doctor_slack_inbound_test.go`

TDD RED → GREEN cycle:
- RED: Added `TestSlackInit_FilesReadScope_Required` and `TestDoctor_FilesReadScope_Missing_Reports` — both failed as expected
- GREEN: Appended `"files:read"` to the `required` slice in both `VerifyEventsAPIScopes` (slack.go) and `checkSlackAppEventsScopes` (doctor_slack.go); updated success message to enumerate all four scopes
- Updated existing tests whose expected inputs/counts assumed three scopes: `TestDoctor_SlackInboundEventsSubscription_HasAllScopes`, `TestSlackInit_ScopeCheck_AllPresent`, `TestSlackInit_ScopeCheck_MissingBoth`, `TestSlackInit_ScopeCheck_MissingReactionsWrite`

## Verification Results

```
terraform fmt -check: PASS (all three TF files)
grep memory_size = 1024: 1 match
grep slack-inbound/*: 1 match
grep files:read (slack.go + doctor_slack.go): 3 matches total (slice + success message)
go test -run 'TestSlackInit_FilesReadScope_Required|TestDoctor_FilesReadScope_Missing_Reports|TestDoctor_SlackInboundEventsSubscription': PASS
```

Pre-existing test failure `TestUnlockCmd_RequiresStateBucket` confirmed failing before these changes (verified via git stash) — out of scope per deviation rules.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Updated five existing tests whose expected values assumed the pre-Phase-75 three-scope set**
- **Found during:** Task 04-03 GREEN step
- **Issue:** `TestDoctor_SlackInboundEventsSubscription_HasAllScopes`, `TestSlackInit_ScopeCheck_AllPresent`, `TestSlackInit_ScopeCheck_MissingBoth`, `TestSlackInit_ScopeCheck_MissingReactionsWrite` all had inputs or count assertions that broke when `files:read` became a fourth required scope
- **Fix:** Added `files:read` to stub scope slices in "all present" tests; changed `len(missing) != 3` to `!= 4` in MissingBoth; rewrote MissingReactionsWrite to expect two missing scopes (reactions:write + files:read) rather than one
- **Files modified:** `internal/app/cmd/slack_test.go`, `internal/app/cmd/doctor_slack_inbound_test.go`
- **Commit:** `8e5b86b` (same commit as the main task)

The plan explicitly anticipated this at: "All other `TestDoctor_SlackInboundEventsSubscription_*` tests continue passing (some may have needed their expected-substring updated for the new success message — fix those, not the production code)"

## Key Links (What Connects to What)

| From | To | Via |
|------|----|-----|
| `lambda-slack-bridge/main.tf` | `arn:aws:s3:::${bucket}/slack-inbound/*` | `aws_iam_role_policy.slack_bridge_files_s3_write` |
| `infra/live/use1/s3-artifacts-lifecycle/terragrunt.hcl` | `infra/modules/s3-artifacts-lifecycle/v1.0.0` | `terraform.source` |
| `slack.go` VerifyEventsAPIScopes | files:read enforcement | `required` slice contains `"files:read"` |
| `doctor_slack.go` checkSlackAppEventsScopes | files:read enforcement + success message | `required` slice + updated message string |

## Self-Check: PASSED

All files created/modified exist on disk. All three task commits (09d7423, f2ae068, 8e5b86b) verified in git log.
