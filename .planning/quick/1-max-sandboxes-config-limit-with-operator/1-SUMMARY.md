---
phase: quick
plan: 1
subsystem: config, create, ses
tags: [sandbox-limit, config, ses, operator-notification]
dependency_graph:
  requires: []
  provides: [max-sandboxes-enforcement, sandbox-limit-notification]
  affects: [internal/app/cmd/create.go, internal/app/config/config.go, internal/app/cmd/configure.go, pkg/aws/ses.go]
tech_stack:
  added: []
  patterns: [viper-config-field, s3-list-api-mock, tdd-red-green]
key_files:
  created:
    - internal/app/cmd/create_limit_test.go
  modified:
    - internal/app/config/config.go
    - internal/app/config/config_test.go
    - internal/app/cmd/configure.go
    - internal/app/cmd/configure_test.go
    - internal/app/cmd/create.go
    - pkg/aws/ses.go
    - pkg/aws/ses_test.go
decisions:
  - "checkSandboxLimit is in package cmd (unexported) and tested via internal test file to avoid exporting"
  - "S3 list failure is non-fatal — allows creation rather than blocking on S3 unavailability"
  - "StateBucket empty check gates limit enforcement — avoids S3 call when bucket not configured"
metrics:
  duration: 6m
  completed: "2026-03-29T04:05:03Z"
  tasks_completed: 2
  files_changed: 7
---

# Quick Task 1: Max Sandboxes Config Limit with Operator Notification Summary

**One-liner:** Hard sandbox limit via `max_sandboxes` config field (default 10) with S3-based count enforcement and SES operator email on rejection.

## What Was Built

### Task 1: Config, configure wizard, and SendLimitNotification

- `Config.MaxSandboxes int` field added with viper default of 10, loaded from `km-config.yaml` key `max_sandboxes`, overridable via `KM_MAX_SANDBOXES` env var. Value 0 means unlimited.
- `platformConfig.MaxSandboxes` field with `yaml:"max_sandboxes,omitempty"` added to configure wizard struct.
- `--max-sandboxes` flag added to `km configure`. Interactive mode prompts "Maximum concurrent sandboxes (0=unlimited)" with default "10". Non-interactive writes only if > 0 (omitempty).
- `SendLimitNotification(ctx, client, operatorEmail, sandboxID, domain, currentCount, maxCount)` added to `pkg/aws/ses.go` — subject `km sandbox limit-reached: {sandboxID}`, body contains sandbox ID, count ratio, and remediation hint.

### Task 2: Enforcement in runCreate and runCreateRemote

- `checkSandboxLimit(ctx, s3Client, bucket, maxSandboxes)` helper added at end of `create.go`:
  - Returns `(0, nil)` immediately when `maxSandboxes <= 0` (unlimited)
  - Calls `ListAllSandboxesByS3` and counts records where `Status != "destroyed"`
  - Returns `(activeCount, error)` when `activeCount >= maxSandboxes` with message `sandbox limit reached (N/N) — increase max_sandboxes in km-config.yaml or destroy unused sandboxes`
  - S3 list failure is non-fatal: logs warning and allows creation
- Wired into `runCreate` (Step 5c) and `runCreateRemote` (Step 5c) after AWS credential validation.
- On limit hit: best-effort `SendLimitNotification` (warns on SES failure, doesn't block), then prints `ERROR: ...` to stderr and returns the error.

## Tests

| Test | Result |
|------|--------|
| TestMaxSandboxesDefault | PASS |
| TestMaxSandboxesFromConfig | PASS |
| TestMaxSandboxesEnvOverride | PASS |
| TestSendLimitNotification_SubjectContainsSandboxIDAndEvent | PASS |
| TestSendLimitNotification_FromAddressIsNotificationsAt | PASS |
| TestSendLimitNotification_BodyContainsCountAndMax | PASS |
| TestSendLimitNotification_ToAddressIsOperator | PASS |
| TestConfigureMaxSandboxesFlag | PASS |
| TestConfigureMaxSandboxesOmittedWhenZero | PASS |
| TestCheckSandboxLimit_AtLimit | PASS |
| TestCheckSandboxLimit_BelowLimit | PASS |
| TestCheckSandboxLimit_Unlimited | PASS |
| TestCheckSandboxLimit_DestroyedNotCounted | PASS |

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| 1 | e99d0dc | feat(quick-1): add MaxSandboxes to Config, platformConfig, and SendLimitNotification |
| 2 | 6f2c123 | feat(quick-1): enforce sandbox limit in runCreate and runCreateRemote |

## Deviations from Plan

None - plan executed exactly as written.

## Self-Check: PASSED
