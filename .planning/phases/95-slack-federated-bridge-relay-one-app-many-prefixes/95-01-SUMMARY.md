---
phase: 95-slack-federated-bridge-relay-one-app-many-prefixes
plan: 01
subsystem: infra
tags: [slack, config, terraform, viper, km-init]

# Dependency graph
requires:
  - phase: 91-slack-polite-bot
    provides: "slack.mention_only / slack.react_always config pattern (merge-list + ExportTerragruntEnvVars + TF module) — copied exactly for peer_bridges"
provides:
  - "SlackConfig.PeerBridges []string field in config.go with merge-list entry + GetStringSlice population"
  - "KM_SLACK_PEER_BRIDGES exported by ExportTerragruntEnvVars with comma-join + env-wins drift WARN"
  - "slack_peer_bridges TF variable in lambda-slack-bridge module; KM_SLACK_PEER_BRIDGES in Lambda env"
affects: [95-02-relay-logic, plan-02-consumers-of-KM_SLACK_PEER_BRIDGES]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "[]string config field with merge-list entry + GetStringSlice population (distinct from *bool tri-state)"
    - "comma-join env export: strings.Join(PeerBridges, ',') vs strconv.FormatBool for *bool fields"

key-files:
  created:
    - internal/app/cmd/slack_peer_bridges_init_test.go
  modified:
    - internal/app/config/config.go
    - internal/app/config/config_test.go
    - internal/app/cmd/init.go
    - infra/live/use1/lambda-slack-bridge/terragrunt.hcl
    - infra/modules/lambda-slack-bridge/v1.0.0/variables.tf
    - infra/modules/lambda-slack-bridge/v1.0.0/main.tf

key-decisions:
  - "Use []string + GetStringSlice (not *bool tri-state) for PeerBridges — nil slice == federation off sentinel"
  - "Merge-list entry 'slack.peer_bridges' is CRITICAL: without it, viper silently drops the value; test asserts len==2 to make footgun visible"
  - "gate on len(PeerBridges)>0 in ExportTerragruntEnvVars (empty explicitly-set slice also treated as federation off)"

patterns-established:
  - "[]string config field pattern: struct field + merge-list entry + IsSet/GetStringSlice population + len>0 export gate"

requirements-completed: [SLACK-FED-CFG, SLACK-FED-PLUMB]

# Metrics
duration: 5min
completed: 2026-06-05
---

# Phase 95 Plan 01: Slack Federated Bridge Relay — Config + Plumbing Summary

**`slack.peer_bridges []string` config field round-trips from km-config.yaml through viper merge-list into cfg.Slack.PeerBridges, exported as KM_SLACK_PEER_BRIDGES (comma-joined) by km init, and reaches the bridge Lambda via terragrunt get_env → TF variable → environment.variables**

## Performance

- **Duration:** ~5 min
- **Started:** 2026-06-05T02:26:28Z
- **Completed:** 2026-06-05T02:30:47Z
- **Tasks:** 3
- **Files modified:** 6 (+ 1 new test file)

## Accomplishments

- Added `SlackConfig.PeerBridges []string` field with CRITICAL merge-list entry `"slack.peer_bridges"` (closes the project_config_key_merge_list footgun for this field)
- Exported `KM_SLACK_PEER_BRIDGES` in `ExportTerragruntEnvVars` with comma-join and env-wins drift WARN, matching the MentionOnly/ReactAlways pattern exactly (with `[]string` adaptation: `strings.Join` not `strconv.FormatBool`)
- Full TF plumbing: `terragrunt.hcl` reads via `get_env`, `variables.tf` declares `slack_peer_bridges` string var (default ""), `main.tf` writes `KM_SLACK_PEER_BRIDGES = var.slack_peer_bridges` into Lambda env — byte-identical to today when unset

## Task Commits

1. **Task 1: SlackConfig.PeerBridges field + merge-list + population** - `e338ba0b` (feat)
2. **Task 2: Export KM_SLACK_PEER_BRIDGES in init.go with drift WARN** - `a3695bf3` (feat)
3. **Task 3: Terraform + Terragrunt plumbing for slack_peer_bridges** - `1c54a8bf` (feat)

## Files Created/Modified

- `internal/app/config/config.go` — `PeerBridges []string` field in `SlackConfig`; `"slack.peer_bridges"` in v2→v merge-list; `GetStringSlice` population block
- `internal/app/config/config_test.go` — `TestLoadSlackPeerBridges_Set` (len==2 assertion catches merge-list footgun), `TestLoadSlackPeerBridges_Absent` (nil == federation off)
- `internal/app/cmd/init.go` — `KM_SLACK_PEER_BRIDGES` export in `ExportTerragruntEnvVars` after ReactAlways block
- `internal/app/cmd/slack_peer_bridges_init_test.go` — (new) 4 tests: `_Set`, `_Absent`, `_DriftWarn`, `_NoOverwriteWhenEnvMatches`
- `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` — `slack_peer_bridges = get_env("KM_SLACK_PEER_BRIDGES", "")` with Phase 95 comment
- `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` — `variable "slack_peer_bridges"` (string, default "")
- `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` — `KM_SLACK_PEER_BRIDGES = var.slack_peer_bridges` in Lambda environment block

## Decisions Made

- Used `[]string` with `GetStringSlice` directly rather than `*bool` tri-state — `nil` slice is the "federation off" sentinel; no pointer indirection needed
- Assert `len==2` (not just non-nil) in `TestLoadSlackPeerBridges_Set` to ensure the merge-list footgun is visibly caught — a nil result with the file value set means the merge-list entry is missing
- Gate `ExportTerragruntEnvVars` on `len(PeerBridges) > 0` (not `!= nil`) so an explicitly-set empty slice also produces federation-off behavior

## Deviations from Plan

None — plan executed exactly as written. The test structure (4 tests instead of the plan's 3) was a minor addition: the `_NoOverwriteWhenEnvMatches` case ensures the drift-WARN logic correctly passes through when env already equals yaml, which is an implicit requirement of the env-wins shape.

## Issues Encountered

None.

## Next Phase Readiness

- Plan 02 (relay logic) can now read `KM_SLACK_PEER_BRIDGES` at cold-start in `cmd/km-slack-bridge/main.go` and wire `HTTPPeerRelayer` into `EventsHandler`
- Deploy: `make build-lambdas && km init --dry-run=false` (NOT `--sidecars` — Lambda env block requires full terragrunt apply)

---
*Phase: 95-slack-federated-bridge-relay-one-app-many-prefixes*
*Completed: 2026-06-05*
