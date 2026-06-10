---
phase: 104-slack-channel-o-1-resolution-on-alias-reuse
plan: "02"
subsystem: infra
tags: [dynamodb, terraform, terragrunt, slack, init]

# Dependency graph
requires:
  - phase: 104-01
    provides: bounded-scan resolver and lookup-first create-time resolution logic

provides:
  - "DynamoDB km-slack-channels table (PK alias, no TTL) as the durable P2 store"
  - "Live terragrunt unit infra/live/use1/dynamodb-slack-channels/terragrunt.hcl"
  - "init.go regionalModules entry so km init enumerates and deploys the table"

affects:
  - 104-03-PLAN.md (IAM grant + Go store helper + resolver wiring that reads/writes this table)
  - 104-04-PLAN.md (km slack adopt command that writes rows into this table)
  - 104-05-PLAN.md (deploy audit that validates km init ADD for this module)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Mirror dynamodb-slack-threads module shape exactly (no TTL variant)"
    - "Table name derives from site label to support multi-install prefixes"
    - "No required_providers in module (root.hcl owns provider declaration)"

key-files:
  created:
    - infra/modules/dynamodb-slack-channels/v1.0.0/main.tf
    - infra/modules/dynamodb-slack-channels/v1.0.0/variables.tf
    - infra/modules/dynamodb-slack-channels/v1.0.0/outputs.tf
    - infra/live/use1/dynamodb-slack-channels/terragrunt.hcl
  modified:
    - internal/app/cmd/init.go

key-decisions:
  - "No TTL block on slack-channels table: alias→channel_id mapping must survive sandbox destroy/recreate cycles; stale rows self-heal via channel_not_found recreate path"
  - "Table name = ${site.label}-slack-channels: derives from site label (not hardcoded km-) so multi-install prefixes never collide"
  - "Entry inserted immediately after dynamodb-slack-threads in init.go regionalModules: preserves dependency ordering (threads table already exists; channels table is peer)"
  - "make build required before any km init check: stale binary silently skips new modules (memory project_make_build_precedes_km_init)"

patterns-established:
  - "No-TTL DynamoDB module: mirror slack-threads module shape, omit ttl {} block, document self-heal path in comment"

requirements-completed: [SLACK-CHAN-STORE, SLACK-CHAN-DEPLOY]

# Metrics
duration: 3min
completed: 2026-06-10
---

# Phase 104 Plan 02: DynamoDB km-slack-channels Table Module Summary

**PAY_PER_REQUEST DynamoDB table with PK `alias` and no TTL, provisioned as a Terraform module + live terragrunt unit + init.go registration for durable O(1) Slack channel resolution on alias reuse**

## Performance

- **Duration:** 3 min
- **Started:** 2026-06-10T19:38:32Z
- **Completed:** 2026-06-10T19:40:49Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments
- Created `dynamodb-slack-channels/v1.0.0` Terraform module (hash_key=alias, PAY_PER_REQUEST, SSE on, no TTL) — terraform validate passes
- Created live terragrunt unit at `infra/live/use1/dynamodb-slack-channels/` mirroring `dynamodb-slack-threads` exactly (remote_state key, source path, and km:component tag updated)
- Registered `dynamodb-slack-channels` in `init.go` `regionalModules()` immediately after `dynamodb-slack-threads`; `make build` clean (km v0.4.916)

## Task Commits

Each task was committed atomically:

1. **Task 1: dynamodb-slack-channels Terraform module (PK alias, no TTL)** - `7cff24cf` (feat)
2. **Task 2: Live terragrunt unit + init.go regionalModules registration** - `87c8f02a` (feat)

## Files Created/Modified
- `infra/modules/dynamodb-slack-channels/v1.0.0/main.tf` - aws_dynamodb_table.slack_channels resource (hash_key=alias, no TTL, SSE on, PAY_PER_REQUEST)
- `infra/modules/dynamodb-slack-channels/v1.0.0/variables.tf` - table_name + tags variables
- `infra/modules/dynamodb-slack-channels/v1.0.0/outputs.tf` - table_name + table_arn outputs
- `infra/live/use1/dynamodb-slack-channels/terragrunt.hcl` - live unit with table_name = ${label}-slack-channels
- `internal/app/cmd/init.go` - regionalModules entry for dynamodb-slack-channels (after slack-threads)

## Decisions Made
- No TTL block: the alias→channel_id mapping must persist across destroy/recreate cycles. Stale rows self-heal when the create-handler detects channel_not_found and overwrites the row (104-01 resolver).
- Table name derives from `site.label` not a hardcoded prefix, preserving multi-install isolation.
- Inserted after `dynamodb-slack-threads` in init.go to preserve ordering (threads must exist before channels is wired in 104-03).

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
- AWS SSO credentials expired during verification; `km init --plan | grep slack-channels` requires live AWS. Build-green is the hard gate per plan notes; live ADD verification deferred to 104-05 deploy audit.

## User Setup Required
None — no external service configuration required. Table is provisioned by `km init --dry-run=false` in 104-05 deploy audit.

## Next Phase Readiness
- TF module + live unit + init.go entry are all present; plan 104-03 can immediately wire the create-handler IAM grant, Go store helper (`pkg/slack/channels_store.go`), and resolver read/write calls
- `km init --dry-run=false` will ADD the table on first real deploy (verified in 104-05)
- Multi-install table name isolation confirmed by design (label-derived)

## Self-Check: PASSED

Files verified:
- `infra/modules/dynamodb-slack-channels/v1.0.0/main.tf` — FOUND
- `infra/modules/dynamodb-slack-channels/v1.0.0/variables.tf` — FOUND
- `infra/modules/dynamodb-slack-channels/v1.0.0/outputs.tf` — FOUND
- `infra/live/use1/dynamodb-slack-channels/terragrunt.hcl` — FOUND
- `init.go dynamodb-slack-channels entry` — FOUND

Commits verified:
- `7cff24cf` feat(infra): dynamodb-slack-channels table module — FOUND
- `87c8f02a` feat(init): live terragrunt unit + regional-module registration — FOUND

---
*Phase: 104-slack-channel-o-1-resolution-on-alias-reuse*
*Completed: 2026-06-10*
