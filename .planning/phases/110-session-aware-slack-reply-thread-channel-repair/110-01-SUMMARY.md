---
phase: 110-session-aware-slack-reply-thread-channel-repair
plan: "01"
subsystem: dynamodb-slack-threads
tags: [dynamodb, gsi, slack, session-index, terraform]
dependency_graph:
  requires: []
  provides: [session-index GSI on km-slack-threads table]
  affects: [infra/live/use1/dynamodb-slack-threads, infra/modules/dynamodb-slack-threads/v1.1.0]
tech_stack:
  added: []
  patterns: [in-place module version bump (v1.0.0 → v1.1.0)]
key_files:
  created:
    - infra/modules/dynamodb-slack-threads/v1.1.0/main.tf
    - infra/modules/dynamodb-slack-threads/v1.1.0/outputs.tf
    - infra/modules/dynamodb-slack-threads/v1.1.0/variables.tf
  modified:
    - infra/live/use1/dynamodb-slack-threads/terragrunt.hcl
decisions:
  - "KEYS_ONLY projection for session-index GSI — reads only return table keys (channel_id, thread_ts) + GSI key; caller fetches the row by primary key if more fields are needed, keeping GSI storage minimal"
  - "No regionalModules() entry added — in-place source path bump only; module-order count unchanged (TestRunInitPlan_ModuleOrder stays green)"
  - "No .terraform.lock.hcl committed — terragrunt manages provider locking from live cache; module-source lock files are a known drift footgun"
metrics:
  duration: "113s"
  completed_date: "2026-06-13"
  tasks_completed: 2
  files_changed: 4
---

# Phase 110 Plan 01: session-index GSI on km-slack-threads — Summary

**One-liner:** DynamoDB session-index GSI (KEYS_ONLY, hash=claude_session_id) added to km-slack-threads via in-place v1.0.0→v1.1.0 module bump; live unit repointed.

## What Was Built

Added the `session-index` GSI to the `km-slack-threads` DynamoDB table. This GSI has:
- Hash key: `claude_session_id` (S)
- Projection: `KEYS_ONLY` (returns `channel_id`, `thread_ts`, `claude_session_id`)

This enables O(1) session-id → (channel_id, thread_ts) resolution — the foundation for Plan 02 (bridge lookup-thread action) and Plan 04 (km slack reply --session).

The implementation follows the Phase 104 pattern: create a new versioned module directory (`v1.1.0`), copy files verbatim from `v1.0.0`, add only the GSI changes to `main.tf`, then repoint the live terragrunt unit to the new source path.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Create v1.1.0 module with session-index GSI | 9d95ab8a | infra/modules/dynamodb-slack-threads/v1.1.0/{main,outputs,variables}.tf |
| 2 | Repoint live unit to v1.1.0 | 3559b944 | infra/live/use1/dynamodb-slack-threads/terragrunt.hcl |

## Verification Results

- `terraform validate` in v1.1.0/: passes (deprecation warning on hash_key — same as v1.0.0 baseline; not a failure)
- `session-index`, `KEYS_ONLY`, `claude_session_id` present in main.tf: confirmed
- No `.terraform.lock.hcl` in v1.1.0/: confirmed
- Live unit contains `dynamodb-slack-threads/v1.1.0` and no `v1.0.0` reference: confirmed
- `go test ./internal/app/cmd/ -run TestRunInitPlan_ModuleOrder`: GREEN (module count unchanged — in-place bump, no new entry)

## Deviations from Plan

None — plan executed exactly as written.

## Deploy Note

Deploy requires **full `km init --dry-run=false`** (NOT `--sidecars`). A GSI is a Terraform resource change; `--sidecars` only rebuilds binaries and does not trigger a terragrunt apply. After apply, confirm `session-index` reaches ACTIVE state in the DynamoDB console (online GSI add; seconds–minutes for a low-volume table). A subsequent `km init --plan` must be a no-op.

## Self-Check: PASSED

All created files exist on disk. Both task commits (9d95ab8a, 3559b944) confirmed in git log.
