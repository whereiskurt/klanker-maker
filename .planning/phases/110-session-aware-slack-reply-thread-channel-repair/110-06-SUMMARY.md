---
phase: 110-session-aware-slack-reply-thread-channel-repair
plan: "06"
subsystem: doctor-observability-docs
tags:
  - slack
  - km-doctor
  - dead-channel
  - skill-doc
  - plugin-bump
dependency_graph:
  requires:
    - 110-03 (km-slack reply subcommand)
    - 110-04 (operator km slack reply)
    - 110-05 (repair commands: prune-threads/forget-thread/forget-channel)
  provides:
    - checkSlackThreadDeadChannels (km doctor WARN)
    - checkSlackChannelDeadAlias (km doctor WARN)
    - klanker:slack Session-aware Reply section
    - plugin version 0.4.8 (client cache refresh)
  affects:
    - internal/app/cmd/doctor_slack_threads.go
    - internal/app/cmd/doctor_slack_threads_test.go
    - internal/app/cmd/doctor.go
    - skills/slack/SKILL.md
    - .claude-plugin/plugin.json
    - .claude-plugin/marketplace.json
tech_stack:
  added: []
  patterns:
    - TDD (RED/GREEN per task)
    - Doctor check injection pattern (nil-SKIP-safe, Error→Warn downgrade)
    - DoctorDDBScanAPI narrow interface (Scan only)
    - SlackChannelChecker reuse from slack_repair.go
key_files:
  created:
    - internal/app/cmd/doctor_slack_threads.go
    - internal/app/cmd/doctor_slack_threads_test.go
  modified:
    - internal/app/cmd/doctor.go
    - skills/slack/SKILL.md
    - .claude-plugin/plugin.json
    - .claude-plugin/marketplace.json
decisions:
  - "DoctorDDBScanAPI defined in doctor_slack_threads.go (not doctor.go) for self-contained check; *dynamodb.Client satisfies both DoctorDDBScanAPI and DDBScanDeleteAPI so DDBScanDeleteClient is reused without a new DoctorDeps field"
  - "SlackDeadChannelChecker DoctorDeps field wired from SSM bot-token in initRealDepsWithExisting — nil when token absent (SKIP-safe)"
  - "checks registered after checkSlackPeerBridges in buildChecks, mirroring Phase 95 pattern; Error→Warn downgrade applied at registration"
  - "Single-page scanAllItems helper (no pagination) acceptable for low-volume operator tables; missed dead rows on large tables are advisory not fatal"
  - "Skill doc Codex auto-detect documented as LOW-confidence with WARN+fallback semantics matching the km-slack reply implementation"
metrics:
  duration: "876s"
  completed_date: "2026-06-13"
  tasks_completed: 2
  files_changed: 6
---

# Phase 110 Plan 06: km doctor Dead-Channel Checks + Skill Doc + Plugin Bump Summary

Two `km doctor` WARN checks for dead Slack channel mappings; `klanker:slack` skill updated with the session-aware `km-slack reply` section; plugin version bumped to 0.4.8 so clients pick up the new content.

## What Was Built

### checkSlackThreadDeadChannels (doctor_slack_threads.go)

Scans `km-slack-threads`, collects unique `channel_id` values, probes each via `conversations.info` using the injected `SlackChannelChecker`. Returns:
- SKIPPED when checker is nil (no bot token)
- WARN on scan error (transient)
- WARN listing dead channel IDs with remediation `km slack prune-threads`
- OK when all probed channels are alive

### checkSlackChannelDeadAlias (doctor_slack_threads.go)

Scans `km-slack-channels` alias rows, probes each `channel_id`. Returns:
- SKIPPED when checker is nil
- WARN listing dead alias names with remediation `km slack forget-channel <alias>` + `km slack adopt`
- OK when all channels alive or no alias rows exist

### DoctorDDBScanAPI interface

Narrow `Scan`-only interface in `doctor_slack_threads.go`. `*dynamodb.Client` satisfies both this and the existing `DDBScanDeleteAPI`, so `deps.DDBScanDeleteClient` is reused at registration — no new DoctorDeps field needed for DDB.

### SlackDeadChannelChecker DoctorDeps field + wiring

`SlackDeadChannelChecker SlackChannelChecker` added to DoctorDeps. `initRealDepsWithExisting` reads the bot token from SSM `{prefix}slack/bot-token`; on success, constructs a `slackClientChannelChecker` (already defined in `slack_repair.go`) and assigns it. Token absent or SSM error → nil → both checks SKIP.

### Registration in doctor.go buildChecks

Both checks registered after `checkSlackPeerBridges` (Phase 95) in the Slack section, with the standard Error→Warn downgrade closure pattern.

### klanker:slack Skill Section

Added `## Session-aware Reply (km-slack reply)` section to `skills/slack/SKILL.md`:
- 4-step resolution order table (--thread > $KM_SLACK_THREAD_TS > session lookup > channel root)
- Session auto-detect heuristic for Claude (JSONL by mtime) and Codex (LOW-confidence / WARN+fallback)
- Flag reference and usage examples
- Operator-side `km slack reply` equivalent
- Repair commands table cross-linking all four Phase 110 Plan 05 commands
- `km doctor` dead-channel checks cross-referenced

### Plugin Version Bump

Both `.claude-plugin/plugin.json` and `.claude-plugin/marketplace.json` bumped from `0.4.7` to `0.4.8` in lockstep (clients cache the old version otherwise).

## Task Commits

1. **Task 1: checkSlackThreadDeadChannels + checkSlackChannelDeadAlias + register** - `7fd93b27`
2. **Task 2: klanker:slack skill section + plugin version bump** - `fc0debd7`

## Test Results

- Targeted: `TestCheckSlackThreadDeadChannels` (5 sub-tests) + `TestCheckSlackChannelDeadAlias` (5 sub-tests): GREEN
- Full cmd suite: `ok github.com/whereiskurt/klanker-maker/internal/app/cmd 480s` — exit 0

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- FOUND: internal/app/cmd/doctor_slack_threads.go
- FOUND: internal/app/cmd/doctor_slack_threads_test.go
- FOUND: internal/app/cmd/doctor.go (contains checkSlackThreadDeadChannels|checkSlackChannelDeadAlias registrations)
- FOUND: skills/slack/SKILL.md (contains km-slack reply)
- FOUND: .claude-plugin/plugin.json (contains 0.4.8)
- FOUND: .claude-plugin/marketplace.json (contains 0.4.8)
- FOUND: commit 7fd93b27 (Task 1)
- FOUND: commit fc0debd7 (Task 2)
