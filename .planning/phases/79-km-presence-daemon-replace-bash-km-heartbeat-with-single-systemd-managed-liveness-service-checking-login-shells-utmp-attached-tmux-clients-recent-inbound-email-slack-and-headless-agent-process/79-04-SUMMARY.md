---
phase: 79-km-presence-daemon
plan: "04"
subsystem: km-doctor
tags: [doctor, cloudwatch, presence-daemon, health-check]
dependency_graph:
  requires: ["79-00", "79-02"]
  provides: ["presence_daemon_healthy doctor check"]
  affects: ["internal/app/cmd/doctor.go", "internal/app/cmd/doctor_presence.go"]
tech_stack:
  added: ["cloudwatchlogs.NewFromConfig (doctor.go import)"]
  patterns: ["CWLogsFilterAPI narrow interface", "runningSandboxListerFunc adapter", "demote-ERROR-to-WARN closure in buildChecks"]
key_files:
  created: []
  modified:
    - internal/app/cmd/doctor_presence.go
    - internal/app/cmd/doctor.go
decisions:
  - "Used runningSandboxListerFunc closure wrapping existing SandboxLister.ListSandboxes (filtered to status='running') — reuses existing DDB-backed lister rather than creating a new helper"
  - "Log group prefix confirmed as '/{resource_prefix}/sandboxes/' from audit-log sidecar source (sidecars/audit-log/cmd/main.go:50) and pkg/aws/cloudwatch.go:69"
  - "DoctorDeps gained three fields: CWFilterClient, PresenceSandboxLister, PresenceLogGroupPrefix"
metrics:
  duration: "~8min"
  completed: "2026-05-11T01:20:15Z"
  tasks_completed: 2
  files_modified: 2
---

# Phase 79 Plan 04: Wire presence_daemon_healthy doctor check — Summary

**One-liner:** CloudWatch FilterLogEvents check for source:"presence" heartbeats per running sandbox, wired into DoctorDeps + buildChecks with WARN-not-ERROR pattern.

## What Was Built

### Task 1: checkPresenceDaemonHealthy implementation (doctor_presence.go)

Replaced the Wave 0 hardcoded-WARN stub with the real implementation:

- Accepts `cw CWLogsFilterAPI`, `lister runningSandboxLister`, `logGroupPrefix string`
- Returns `CheckSkipped` when either dependency is nil
- Calls `lister.ListRunningSandboxIDs(ctx)` to get active sandboxes
- For each sandbox ID, calls `FilterLogEvents` with:
  - `FilterPattern: "source":"presence"`
  - `StartTime: now - 5 minutes` (UnixMilli)
  - `Limit: 1`
  - `LogGroupName: logGroupPrefix + sandboxID`
- CW errors (e.g. ResourceNotFoundException for pre-Phase-79 sandboxes) treated as stale
- Returns `CheckOK` when all sandboxes have recent events
- Returns `CheckWarn` listing stale sandbox IDs with remediation hint

Added `runningSandboxListerFunc` adapter type to allow closure injection in `initRealDepsWithExisting` without defining a new struct.

### Task 2: DoctorDeps wiring + buildChecks registration (doctor.go)

**DoctorDeps additions (lines 334-345):**
- `CWFilterClient CWLogsFilterAPI` — CloudWatch Logs client for FilterLogEvents
- `PresenceSandboxLister runningSandboxLister` — running-sandbox ID lister
- `PresenceLogGroupPrefix string` — CW log group prefix (resource-prefix-aware)

**initRealDepsWithExisting wiring (lines 2925-2947):**
- `deps.CWFilterClient = cloudwatchlogs.NewFromConfig(awsCfg)`
- `deps.PresenceSandboxLister = runningSandboxListerFunc(...)` — wraps `deps.Lister.ListSandboxes` filtered to `Status == "running"`
- `deps.PresenceLogGroupPrefix = "/" + cfg.GetResourcePrefix() + "/sandboxes/"`

**buildChecks registration (lines 2688-2700):**
- Closure captures `cwFilter`, `presenceLister`, `presenceLogGroupPrefix` from deps
- Calls `checkPresenceDaemonHealthy(ctx, ...)` with demote-ERROR-to-WARN pattern
- Registered after the orphaned-artifacts check, before the Phase 66 email check

**Import added:** `"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"`

## Resolved Questions for Plan 79-05

### Log group prefix
Confirmed: `"/{resource_prefix}/sandboxes/"` (e.g. `/km/sandboxes/`).
Sources:
- `sidecars/audit-log/cmd/main.go:50` — `CW_LOG_GROUP` default is `"/km/sandboxes/<SANDBOX_ID>/"`
- `pkg/aws/cloudwatch.go:69` — `DeleteSandboxLogGroup` uses `"/" + prefix + "/sandboxes/" + sandboxID + "/"`

The doctor check does NOT append a trailing slash to the log group name (the prefix already has the trailing slash from the production wiring at line 2947; the sandbox ID is concatenated directly). The test uses prefix `/km/audit` which concatenates to `/km/auditsb-abc123` — this is intentional for isolation in tests.

### runningSandboxLister implementation choice
Used option (b) from the plan: closure adapter wrapping existing `SandboxLister.ListSandboxes` filtered to `Status == "running"`. No new struct needed — `runningSandboxListerFunc` is a func-type alias defined in `doctor_presence.go`.

### How km doctor displays this check
The check name is `"Presence daemon healthy"` (returned in `CheckResult.Name`). It will appear in `km doctor` output as:
- `✓ Presence daemon healthy — all N running sandbox(es) have recent presence events (<=5min)`
- `⚠ Presence daemon healthy — N sandbox(es) have no recent presence events: sb-xxx, ...`
- `- Presence daemon healthy — CloudWatch Logs client not configured` (SKIPPED when AWS config init fails)
- `- Presence daemon healthy — sandbox lister not configured` (SKIPPED when lister nil)

The check runs for ALL km doctor invocations (no flag gate). Pre-Phase-79 sandboxes will produce WARN with remediation: `'km destroy && km create'`. Phase-79+ sandboxes with a crashed daemon will show WARN with remediation: `'systemctl status km-presence'`.

## Test Results

All 3 Wave 0 stub tests now GREEN:

| Test | Status | What it verifies |
|------|--------|-----------------|
| `TestDoctor_PresenceDaemonHealthy_OK` | PASS | Lister returns 1 ID; CW fake returns 1 event → CheckOK |
| `TestDoctor_PresenceDaemonHealthy_Stale` | PASS | Lister returns 1 ID; CW fake returns no events → CheckWarn with ID in message |
| `TestDoctor_PresenceDaemonHealthy_Skipped` | PASS | nil cw → CheckSkipped |

Full doctor test suite: `go test ./internal/app/cmd/... -count=1 -run TestDoctor` — PASS, no regressions.

## Deviations from Plan

None — plan executed exactly as written.

## Commits

| Hash | Description |
|------|-------------|
| 28c1f3b | feat(79-04): implement checkPresenceDaemonHealthy + turn 3 Wave 0 tests GREEN |
| 675f647 | feat(79-04): wire presence daemon check into DoctorDeps + buildChecks |

## Self-Check

- [x] `internal/app/cmd/doctor_presence.go` exists and contains `FilterLogEvents`
- [x] `internal/app/cmd/doctor.go` contains `checkPresenceDaemonHealthy` (2 hits: buildChecks registration + initRealDepsWithExisting)
- [x] `internal/app/cmd/doctor.go` contains `CWFilterClient` (3 hits: struct field, buildChecks capture, wiring)
- [x] Commits 28c1f3b and 675f647 exist
- [x] `go build ./...` passes
- [x] All 3 tests GREEN
