---
phase: 118-slack-trigger-allowlist-private-per-sandbox-channels
plan: "04"
subsystem: slack-bridge
tags: [feature-b, install-level, config, merge-list, terraform, env, KM_SLACK_ALLOW]
dependency_graph:
  requires:
    - "118-01 EventsHandler.Allow field stub"
  provides:
    - "slack.allow config key (struct + merge-list + loader)"
    - "KM_SLACK_ALLOW env export in km init (env-wins drift WARN)"
    - "lambda-slack-bridge TF var slack_allow + KM_SLACK_ALLOW env"
    - "bridge main.go reads KM_SLACK_ALLOW → EventsHandler.Allow at cold-start"
  affects:
    - "internal/app/config — SlackConfig"
    - "internal/app/cmd — km init env export"
    - "infra/modules/lambda-slack-bridge, infra/live/use1 — TF wiring"
    - "cmd/km-slack-bridge — cold-start wiring"
tech_stack:
  added: []
  patterns:
    - "config v2→v merge-list entry required or YAML silently dropped (project_config_key_merge_list)"
    - "KM_SLACK_ALLOW mirrors KM_SLACK_PEER_BRIDGES []string env-export pattern verbatim"
    - "TF module edited in place (additive default=\"\" var, no version bump)"
key_files:
  created:
    - "internal/app/config/config_slack_allow_test.go (merge-list regression guard)"
  modified:
    - "internal/app/config/config.go (SlackConfig.Allow + merge-list + loader)"
    - "internal/app/cmd/init.go (KM_SLACK_ALLOW export, env-wins drift WARN)"
    - "infra/modules/lambda-slack-bridge/v1.0.0/variables.tf (slack_allow var)"
    - "infra/modules/lambda-slack-bridge/v1.0.0/main.tf (KM_SLACK_ALLOW env)"
    - "infra/live/use1/lambda-slack-bridge/terragrunt.hcl (get_env passthrough)"
    - "cmd/km-slack-bridge/main.go (KM_SLACK_ALLOW → eventsHandler.Allow)"
decisions:
  - "slack.allow MUST be in the v2→v merge-list (config.go) — struct+getter alone silently drops it; guarded by config_slack_allow_test.go"
  - "TF module v1.0.0 edited in place (additive var default=\"\") — no version bump"
  - "Empty list → KM_SLACK_ALLOW omitted → terragrunt default \"\" → dormant (everyone allowed)"
metrics:
  completed: "2026-06-24"
  tasks_completed: 3
  files_modified: 6
---

# Phase 118 Plan 04: Feature B — Install-Level Wiring Summary

Carried `slack.allow` from `km-config.yaml` through the config loader (including the v2→v merge-list footgun), the `KM_SLACK_ALLOW` env export in `km init`, the `lambda-slack-bridge` Terraform module/terragrunt input, and into `EventsHandler.Allow` at bridge cold-start.

## What Was Done

### Task 1 — SlackConfig.Allow (config)
`Allow []string` on `SlackConfig`, `"slack.allow"` added to the v2→v merge-list (the silent-drop footgun), and the loader block. New `config_slack_allow_test.go` is the regression guard.

### Task 2 — km init exports KM_SLACK_ALLOW
Comma-joined export with env-wins drift WARN, mirroring `KM_SLACK_PEER_BRIDGES`. Empty list → no export → dormant.

### Task 3 — TF + terragrunt + bridge
`variable "slack_allow"` + `KM_SLACK_ALLOW = var.slack_allow` in the module (in place, no version bump); `slack_allow = get_env("KM_SLACK_ALLOW","")` in terragrunt; `cmd/km-slack-bridge/main.go` reads `KM_SLACK_ALLOW` into `eventsHandler.Allow` at cold-start.

## Verification Summary
```
go build ./... → clean
go test ./internal/app/config/... -run SlackAllow → PASS (merge-list regression guard)
terraform fmt -check infra/modules/lambda-slack-bridge/v1.0.0 → clean
```
Live: after `km init --dry-run=false`, the deployed `km-slack-bridge` Lambda env showed `KM_SLACK_ALLOW=U0B0162H1GX` (full pipeline confirmed).

## Commits
| Hash | Message |
|------|---------|
| 112c9e10 | (config struct + merge-list + loader + regression test — swept here by parallel interleaving) |
| 82851cd4 | feat(118-04): install-level KM_SLACK_ALLOW → bridge EventsHandler.Allow |

## Deviations from Plan
Recovered after a parallel-wave executor stall (118-04 never started in the run). Task 1's config work had been swept into commit 112c9e10 by parallel `git add` interleaving; Tasks 2–3 completed directly in 82851cd4. Functionally complete and verified live.
