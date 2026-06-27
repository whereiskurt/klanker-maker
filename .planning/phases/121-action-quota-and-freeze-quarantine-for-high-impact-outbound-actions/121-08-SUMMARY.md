---
phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions
plan: "08"
subsystem: infrastructure
tags: [dynamodb, terraform, init, action-quota, streams]
dependency_graph:
  requires: [121-01]
  provides: [dynamodb-action-quota table, stream_arn output, regionalModules entry]
  affects: [121-09 (alerter event-source mapping uses stream_arn)]
tech_stack:
  added: [dynamodb-action-quota TF module v1.0.0]
  patterns: [dynamodb PAY_PER_REQUEST + Streams + TTL + SSE, terragrunt live unit, regionalModules registration]
key_files:
  created:
    - infra/modules/dynamodb-action-quota/v1.0.0/main.tf
    - infra/modules/dynamodb-action-quota/v1.0.0/variables.tf
    - infra/modules/dynamodb-action-quota/v1.0.0/outputs.tf
    - infra/live/use1/dynamodb-action-quota/terragrunt.hcl
  modified:
    - internal/app/cmd/init.go
decisions:
  - "Inserted dynamodb-action-quota after dynamodb-checks (before check-runner-role) so it's ordered with other DynamoDB tables and before ses"
  - "No dependency blocks in live unit: table has no upstream outputs"
  - "stream_arn exposed as output for plan 09 alerter event-source mapping"
metrics:
  duration: "113s"
  completed: "2026-06-27T13:09:00Z"
  tasks_completed: 3
  files_changed: 5
---

# Phase 121 Plan 08: dynamodb-action-quota Table Module Summary

One-liner: DynamoDB action-quota table with PAY_PER_REQUEST + Streams(NEW_AND_OLD_IMAGES) + TTL + SSE, registered across full deploy surface (TF module + live unit + regionalModules).

## What Was Built

The `{prefix}-action-quota` DynamoDB table — the counter store for the Phase 121 action quota system. Full deploy surface registered so `km init` applies it:

- **TF module** (`infra/modules/dynamodb-action-quota/v1.0.0/`): `PK={sandbox}#{action}` / `SK=window`, `PAY_PER_REQUEST`, `stream_enabled=true` + `stream_view_type=NEW_AND_OLD_IMAGES`, `ttl{attribute_name=ttl}`, `server_side_encryption{enabled=true}`, outputs: `table_name`, `table_arn`, `stream_arn`.
- **Live terragrunt unit** (`infra/live/use1/dynamodb-action-quota/terragrunt.hcl`): mirrors dynamodb-slack-channels pattern; `table_name={site.label}-action-quota`; no dependency blocks.
- **`regionalModules()` entry** (`internal/app/cmd/init.go`): inserted after `dynamodb-checks`, before `check-runner-role`, before `ses` — one of the two Phase 121 additions that bring the module count from 24 to 26 (plan 09 adds the second).

## Commits

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | TF module (Streams + TTL + SSE) | a33b32fa | main.tf, variables.tf, outputs.tf |
| 2 | Live terragrunt unit | 377d3916 | infra/live/use1/dynamodb-action-quota/terragrunt.hcl |
| 3 | Register in regionalModules() | e748afac | internal/app/cmd/init.go |

## Verification

- `terraform fmt -check .` — exit 0 (fmt-clean)
- `terraform validate` — expected "Missing required provider" (no `required_providers` block; root.hcl provides at terragrunt runtime — same as all other modules)
- `grep -q 'dynamodb-action-quota' internal/app/cmd/init.go` — found
- `go build ./internal/app/cmd/...` — exit 0
- `TestRunInitPlan_ModuleOrder` — currently reports 25/26: expected; plan 09 (alerter Lambda) adds the 26th entry. Test will be green once plan 09 lands.

## Deviations from Plan

None — plan executed exactly as written.

## Deploy Notes

**`make build` REQUIRED before `km init`** — a stale km binary silently skips the new `dynamodb-action-quota` entry in `regionalModules()` (memory `project_make_build_precedes_km_init`). Deploy sequence for this module:

```
make build                   # operator binary (carries the new regionalModules entry)
make build-lambdas           # (when plan 09 alerter also lands)
km init --dry-run=false      # applies the table + Streams + IAM
```

No existing sandboxes need recreate (table is a new infra resource; no userdata change in this plan).

## Self-Check: PASSED

Files exist:
- FOUND: infra/modules/dynamodb-action-quota/v1.0.0/main.tf
- FOUND: infra/modules/dynamodb-action-quota/v1.0.0/variables.tf
- FOUND: infra/modules/dynamodb-action-quota/v1.0.0/outputs.tf
- FOUND: infra/live/use1/dynamodb-action-quota/terragrunt.hcl

Commits exist:
- FOUND: a33b32fa
- FOUND: 377d3916
- FOUND: e748afac
