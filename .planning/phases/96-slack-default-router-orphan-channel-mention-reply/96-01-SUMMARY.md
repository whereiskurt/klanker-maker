---
phase: 96-slack-default-router-orphan-channel-mention-reply
plan: "01"
subsystem: slack-bridge-config
tags: [slack, config, terragrunt, iam, phase-96]
dependency_graph:
  requires: [phase-95-slack-federated-bridge-relay]
  provides: [KM_SLACK_DEFAULT_ROUTER config plumbing, dynamodb:Scan IAM grant]
  affects: [lambda-slack-bridge, km init, km-config.yaml]
tech_stack:
  added: []
  patterns: [tri-state *bool config pattern, v2→v merge-list entry, env-wins drift WARN]
key_files:
  created: []
  modified:
    - internal/app/config/config.go
    - internal/app/config/config_test.go
    - internal/app/cmd/init.go
    - internal/app/cmd/init_test.go
    - infra/live/use1/lambda-slack-bridge/terragrunt.hcl
    - infra/modules/lambda-slack-bridge/v1.0.0/variables.tf
    - infra/modules/lambda-slack-bridge/v1.0.0/main.tf
decisions:
  - "dynamodb:Scan added to dynamodb_sandboxes_pause_hint policy (not a new IAM resource) — keeps sandboxes-table grants in one policy"
  - "DefaultRouter follows *bool tri-state (same as MentionOnly/ReactAlways), NOT string — nil=absent=dormant is the cleaner Phase 95 byte-identity sentinel"
metrics:
  duration: "274s"
  completed: "2026-06-06T03:18:18Z"
  tasks_completed: 3
  files_modified: 7
requirements_satisfied: [SLACK-RTR-CFG, SLACK-RTR-SAFE]
---

# Phase 96 Plan 01: SlackConfig.DefaultRouter Config Plumbing + IAM Grant Summary

Pure plumbing plan — wires `slack.default_router` end-to-end from `km-config.yaml` through `km init` env export, terragrunt `get_env`, the TF module variable, and the Lambda environment block. Also grants `dynamodb:Scan` on the sandboxes table for the running-channels lister that Plans 02/03 build.

## What Was Built

`slack.default_router: true` in km-config.yaml round-trips through `config.Load()` into `cfg.Slack.DefaultRouter == &true`, is exported as `KM_SLACK_DEFAULT_ROUTER=true` by `ExportTerragruntEnvVars`, picked up by `terragrunt.hcl get_env("KM_SLACK_DEFAULT_ROUTER", "false")`, declared in `variables.tf`, and passed to the Lambda as env var `KM_SLACK_DEFAULT_ROUTER`. With the toggle absent or false, behavior is byte-identical to Phase 95. The bridge Lambda execution role now has `dynamodb:Scan` on the base sandboxes table, enabling the `DDBRunningChannelLister` that Plan 02 adds.

## Task Log

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | SlackConfig.DefaultRouter struct + merge-list + population + tests | f4aa61da | config.go, config_test.go |
| 2 | init.go KM_SLACK_DEFAULT_ROUTER export + drift WARN + tests | 2b47ca4a | init.go, init_test.go |
| 3 | Terragrunt + TF module var + Lambda env + dynamodb:Scan | fcc6af48 | terragrunt.hcl, variables.tf, main.tf |

## Verification

- `go build ./...` — green (exit 0)
- `go test ./internal/app/config/... -count=1` — green (all DefaultRouter + Slack tests pass)
- `go test ./internal/app/cmd/... -run 'DefaultRouter|Export' -count=1` — green
- `terraform fmt -check` — green (no output)
- `terraform validate` — green ("Success! The configuration is valid")
- `grep '"slack.default_router"' config.go` — found at line 432 (merge-list) and 538 (population)
- `grep 'dynamodb:Scan' main.tf` — found at line 210 (`DDBSandboxesScanRunningChannels`)
- With `slack.default_router` absent: `KM_SLACK_DEFAULT_ROUTER` not exported (Phase 95 byte-identity preserved)

## Decisions Made

1. **`dynamodb:Scan` added to existing `dynamodb_sandboxes_pause_hint` policy** — rather than creating a new `aws_iam_role_policy` resource, the Scan statement was appended to the existing sandboxes-table policy (`dynamodb_sandboxes_pause_hint`). Keeps all sandboxes-base-table grants in one policy, minimal diff, no new TF resource lifecycle.

2. **`DefaultRouter` follows `*bool` tri-state** (same as `MentionOnly`/`ReactAlways`) — nil means absent/dormant, which preserves Phase 95 byte-identity without any conditional in the Lambda env block. Alternative of using a plain `string` with `""` as the absent sentinel was rejected (inconsistent with existing Slack *bool fields).

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

All key files confirmed present. All 3 task commits found in git log:
- f4aa61da (Task 1: config.go + config_test.go)
- 2b47ca4a (Task 2: init.go + init_test.go)
- fcc6af48 (Task 3: terragrunt.hcl + variables.tf + main.tf)
