---
phase: 104-slack-channel-o-1-resolution-on-alias-reuse
plan: "04"
subsystem: slack-adopt-doctor
tags: [slack, doctor, dynamodb, operator-cli, escape-hatch]
dependency_graph:
  requires: [104-03]
  provides: [km-slack-adopt-command, km-doctor-slack-channels-check]
  affects: [internal/app/cmd/slack.go, internal/app/cmd/doctor.go]
tech_stack:
  added: []
  patterns: [cobra-subcommand, tdd-red-green, interface-extension, ddb-existence-check]
key_files:
  created:
    - internal/app/cmd/slack_adopt.go
    - internal/app/cmd/slack_adopt_test.go
  modified:
    - internal/app/cmd/slack.go
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go
decisions:
  - "CORRECTION #4 applied: km-slack-channels NOT added to checkOrphanedDDBRows; alias rows are not per-sandbox and must survive destroy"
  - "SSM by-name write-through in adopt derives channelName = sb-{sanitizeChannelName(alias)}, matching the km create derivation; DDB is authoritative"
  - "GetSlackChannelsTableName() added to DoctorConfigProvider interface + appConfigAdapter to enable doctor check without type-assertion"
metrics:
  duration: "370s"
  completed: "2026-06-10"
  tasks_completed: 2
  tasks_total: 2
  files_changed: 5
---

# Phase 104 Plan 04: km slack adopt + km doctor slack-channels check Summary

One-liner: `km slack adopt <alias> <channelID>` operator escape hatch with format+membership validation, DDB+SSM write-through, and a WARN-level `km doctor` existence check for the `km-slack-channels` table.

## Tasks Completed

| # | Name | Commit | Key Files |
|---|------|--------|-----------|
| 1 | km slack adopt command (validate + membership + write-through) | f4e91b3b | slack_adopt.go, slack_adopt_test.go, slack.go |
| 2 | km doctor existence check for km-slack-channels (NOT orphan scan) | 755c4079 | doctor.go, doctor_test.go |

## What Was Built

### Task 1: km slack adopt

`runSlackAdopt(ctx, api, store, alias, channelID, slackPrefix)` validates:
1. `channelID` matches `^C[A-Z0-9]+$` (format gate, actionable hint: Slack → channel → About → Channel ID)
2. `api.ChannelInfo(ctx, channelID)` returns `isMember=true` (rejects with "/invite the bot first")
3. `store.UpsertByAlias(ctx, alias, channelID)` write-throughs to the DDB store
4. `cacheSlackChannelIDByName(ctx, ssmStore, slackPrefix, channelName, channelID)` write-throughs to SSM by-name cache using `sb-{sanitizeChannelName(alias)}` derivation

The cobra command (`adopt <alias> <channelID>`, `cobra.ExactArgs(2)`) is registered under the `km slack` parent. It builds a real `kmslack.Client` from SSM token and a `kmaws.SlackChannelStore` from `cfg.GetSlackChannelsTableName()`.

TDD: tests written first (RED: undefined), then implementation (GREEN: all 3 pass):
- `TestSlackAdopt_RejectsBadChannelID`
- `TestSlackAdopt_RequiresBotMembership`
- `TestSlackAdopt_WritesThrough`

### Task 2: km doctor existence check

Added `GetSlackChannelsTableName() string` to:
- `DoctorConfigProvider` interface (doctor.go ~282)
- `appConfigAdapter` adapter struct (doctor.go ~333)
- `testConfig` and `testDoctorConfig` test stubs (doctor_test.go)

The check uses the existing `checkDynamoTable` helper (DescribeTable probe) with `CheckError → CheckWarn` demotion (mirrors the identity-table pattern: table is optional for non-Slack installs). The table is placed after the identity table check in `buildChecks()`.

CORRECTION #4 strictly applied: `checkOrphanedDDBRows` call at doctor.go:3815 still takes only (budgets, identities, slackThreads, sandboxes) — the slack-channels table is absent. Alias rows must never be auto-deleted (deleting them breaks the next `km create --alias` reuse).

## Verification Results

```
go test ./internal/app/cmd/ -run TestSlackAdopt -v
  PASS TestSlackAdopt_RejectsBadChannelID
  PASS TestSlackAdopt_RequiresBotMembership
  PASS TestSlackAdopt_WritesThrough

make build → km v0.4.921 (755c4079) — clean

./km slack adopt --help → renders under km slack

grep -n checkOrphanedDDBRows doctor.go | grep slack-channels | wc -l → 0
```

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Interface Method] Added GetSlackChannelsTableName() to DoctorConfigProvider**
- **Found during:** Task 2
- **Issue:** `DoctorConfigProvider` interface lacked `GetSlackChannelsTableName()`; `cfg.GetSlackChannelsTableName()` call in `buildChecks` failed compilation.
- **Fix:** Added method to interface, appConfigAdapter, testConfig, testDoctorConfig stubs. Straightforward extension — not an architectural change.
- **Files modified:** doctor.go (interface + adapter), doctor_test.go (2 stubs)
- **Commit:** 755c4079

None beyond the above — plan executed as specified.

## Self-Check

Files exist:
- `internal/app/cmd/slack_adopt.go` — FOUND
- `internal/app/cmd/slack_adopt_test.go` — FOUND

Commits exist:
- `f4e91b3b` (Task 1: km slack adopt) — FOUND
- `755c4079` (Task 2: doctor check) — FOUND

## Self-Check: PASSED
