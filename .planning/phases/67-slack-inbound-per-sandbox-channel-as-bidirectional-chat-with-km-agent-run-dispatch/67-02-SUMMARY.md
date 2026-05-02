---
phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
plan: "02"
subsystem: infra/config
tags: [dynamodb, terraform, config, slack-inbound, gsi, phase-66-shim]
dependency_graph:
  requires: [67-00]
  provides: [dynamodb-slack-threads-module, sandboxes-v1.1.0-gsi, config-helpers]
  affects: [67-03, 67-04, 67-05, 67-06]
tech_stack:
  added: []
  patterns:
    - "DynamoDB module versioning: copy v1.0.0 to v1.1.0 for additive GSI, never modify in-place"
    - "Config helper pattern: explicit field wins, fallback via prefix derivation, nil-safe"
    - "TDD: RED commit with failing tests, GREEN commit with implementation"
key_files:
  created:
    - infra/modules/dynamodb-slack-threads/v1.0.0/main.tf
    - infra/modules/dynamodb-slack-threads/v1.0.0/variables.tf
    - infra/modules/dynamodb-slack-threads/v1.0.0/outputs.tf
    - infra/modules/dynamodb-sandboxes/v1.1.0/main.tf
    - infra/modules/dynamodb-sandboxes/v1.1.0/variables.tf
    - infra/modules/dynamodb-sandboxes/v1.1.0/outputs.tf
    - infra/live/use1/dynamodb-slack-threads/terragrunt.hcl
  modified:
    - infra/live/use1/dynamodb-sandboxes/terragrunt.hcl
    - internal/app/config/config.go
    - internal/app/config/config_test.go
decisions:
  - "Live Terragrunt config path is infra/live/use1/ (not management/dynamodb/ as in plan) — followed real codebase pattern"
  - "GetResourcePrefix and SlackThreadsTableName added in Phase 67 (no Phase 66 collision detected)"
  - "v1.0.0 of dynamodb-sandboxes left byte-for-byte unchanged; new GSI in v1.1.0 copy only"
  - "ttl_expiry on slack-threads table is Number type (Unix epoch) per RESEARCH.md Pitfall #3"
metrics:
  duration: "4 minutes"
  completed: "2026-05-02T23:56:50Z"
  tasks_completed: 2
  files_modified: 9
---

# Phase 67 Plan 02: Data Foundation — DynamoDB Modules + Config Helpers Summary

**One-liner:** DynamoDB slack-threads module (channel_id PK, thread_ts SK, ttl_expiry TTL) plus sandboxes v1.1.0 with additive slack_channel_id-index GSI, live Terragrunt configs, and Phase-66-compatible Config helpers.

## What Was Built

### Task 1: DynamoDB Modules + Terragrunt Live Configs

**New module: `infra/modules/dynamodb-slack-threads/v1.0.0/`**
- `main.tf`: `aws_dynamodb_table.slack_threads` with PK `channel_id` (S), SK `thread_ts` (S)
- TTL on `ttl_expiry` (N, Unix epoch seconds) — per RESEARCH.md Pitfall #3: must NOT use ISO8601 string
- PAY_PER_REQUEST billing, server-side encryption, PITR disabled (same as nonces reference module)
- Tags: `Component = "km-slack-inbound"`

**New module version: `infra/modules/dynamodb-sandboxes/v1.1.0/`**
- v1.0.0 copied wholesale (unchanged); v1.1.0 adds two things:
  1. New `attribute { name = "slack_channel_id" type = "S" }` block
  2. New `global_secondary_index { name = "slack_channel_id-index" hash_key = "slack_channel_id" projection_type = "ALL" }` block
- All other attributes, GSIs, TTL, billing mode: identical to v1.0.0
- This is a Terraform in-place modify (GSI add) — no destroy/replace on the existing table

**Deviation: Live config path** — Plan specified `infra/live/management/dynamodb/` but the real codebase uses `infra/live/use1/dynamodb-*`. Applied Rule 3 (auto-fix blocking issue): live configs created at correct paths:
- New: `infra/live/use1/dynamodb-slack-threads/terragrunt.hcl`
- Updated: `infra/live/use1/dynamodb-sandboxes/terragrunt.hcl` (v1.0.0 → v1.1.0)

Both configs follow the identical `locals { repo_root, site_vars, region_config }` + `remote_state` + `include "root"` pattern from `infra/live/use1/dynamodb-slack-nonces/terragrunt.hcl`.

**Verification:** `terraform fmt -check -recursive` clean, `terraform validate` passes for both modules.

### Task 2: Config Helpers (TDD)

**`internal/app/config/config.go` additions:**

New struct fields on `Config`:
```go
SlackThreadsTableName string  // default "km-slack-threads"
ResourcePrefix        string  // default "km"
```

New helper methods:
```go
func (c *Config) GetResourcePrefix() string
func (c *Config) GetSlackThreadsTableName() string
```

- `GetResourcePrefix()`: returns `c.ResourcePrefix` if non-empty; falls back to `"km"`; nil-safe
- `GetSlackThreadsTableName()`: explicit `SlackThreadsTableName` wins; otherwise `GetResourcePrefix() + "-slack-threads"`; nil-safe returns `"km-slack-threads"`
- Both wired into viper `SetDefault`, km-config.yaml merge list, and struct populator

**Phase 66 collision check:** `grep -n "GetResourcePrefix\|ResourcePrefix" config.go` returned no results before this plan — no collision. Phase 66 can migrate the body of `GetResourcePrefix()` to read from km-config.yaml when it ships; Phase 67 code using these helpers will work unchanged.

**TDD sequence:**
1. RED commit (2dfea1b): failing tests — compile errors on undefined fields/methods
2. GREEN commit (83cf40f): implementation — all three new tests pass, no regressions in 13 existing tests

**`internal/app/config/config_test.go` additions (3 new tests):**
- `TestConfig_GetResourcePrefix_FallbackKM`: empty → "km", set → returns value
- `TestConfig_GetSlackThreadsTableName_DefaultAndPrefix`: empty → "km-slack-threads", prefix "stg" → "stg-slack-threads", explicit field wins
- `TestConfig_NilReceiverSafe`: nil `*Config` returns safe defaults for both helpers

## Verification Results

| Check | Result |
|-------|--------|
| `terraform fmt -check -recursive` on both modules | PASS |
| `terraform validate` on dynamodb-slack-threads v1.0.0 | PASS (Success) |
| `terraform validate` on dynamodb-sandboxes v1.1.0 | PASS (warnings are pre-existing hash_key deprecation, not new) |
| `go build ./...` | PASS |
| `go test ./internal/app/config/... -count=1` | PASS (16/16 tests) |
| v1.0.0 dynamodb-sandboxes unchanged | PASS (git diff empty) |

## Commits

| Hash | Message |
|------|---------|
| b503781 | feat(67-02): add dynamodb-slack-threads module + sandboxes v1.1.0 GSI |
| 2dfea1b | test(67-02): add failing tests for GetResourcePrefix and GetSlackThreadsTableName (RED) |
| 83cf40f | feat(67-02): add SlackThreadsTableName and ResourcePrefix to Config struct (GREEN) |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking Path] Live Terragrunt config path corrected to use1/**
- **Found during:** Task 1 — the plan specified `infra/live/management/dynamodb/` but this path does not exist
- **Issue:** `infra/live/management/` only contains `scp/`. All DynamoDB live configs live under `infra/live/use1/dynamodb-*`
- **Fix:** Created `infra/live/use1/dynamodb-slack-threads/terragrunt.hcl` (new) and updated `infra/live/use1/dynamodb-sandboxes/terragrunt.hcl` (v1.0.0 → v1.1.0)
- **Files modified:** infra/live/use1/dynamodb-slack-threads/terragrunt.hcl, infra/live/use1/dynamodb-sandboxes/terragrunt.hcl
- **Commit:** b503781

## Self-Check: PASSED

All 10 key files exist. All 3 task commits found. Full test suite green. Build clean.
