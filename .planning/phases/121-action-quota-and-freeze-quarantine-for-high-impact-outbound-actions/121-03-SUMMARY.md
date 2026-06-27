---
phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions
plan: "03"
subsystem: profile-schema + config
tags: [limits, quota, schema, config, validation]
dependency_graph:
  requires: [121-01]
  provides: [spec.limits typed block, limits: install-default config, GetLimitsConfig()]
  affects: [pkg/quota resolve (plan 07 consumer), compiler (plan 07)]
tech_stack:
  added: []
  patterns: [typed profile block (ptr, omitempty), JSON schema additionalProperties:false + $ref $defs, semantic validation belt-and-suspenders, viper UnmarshalKey merge-list, config getter]
key_files:
  created:
    - pkg/profile/validate_limits_test.go
    - internal/app/config/config_limits_test.go
  modified:
    - pkg/profile/types.go
    - pkg/profile/validate.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - internal/app/config/config.go
decisions:
  - "ActionLimitConfig defined locally in config.go (no import cycle: config must not import pkg/profile)"
  - "LimitsSpec fields are pointers (*ActionLimitSpec) so absent actions stay nil and inherit the install-default or unlimited"
  - "ActionLimitSpec uses *int64 for window fields to distinguish 'absent' from '0'"
  - "Additive change — no apiVersion bump (consistent with Phase 118/119 precedent)"
  - "JSON schema uses $ref/#/$defs/ActionLimitSpec shared across all 6 action keys (no duplication)"
metrics:
  duration: 296s
  completed: "2026-06-27"
  tasks_completed: 2
  files_modified: 6
requirements: [PROF-01, CFG-01]
---

# Phase 121 Plan 03: spec.limits + install-default config Summary

**One-liner:** Typed spec.limits block (LimitsSpec + ActionLimitSpec) with JSON schema validation + install-level limits: config (LimitsConfig + GetLimitsConfig()) with merge-list registration.

## What Was Built

### Task 1 — spec.limits types + JSON schema + km validate rules (PROF-01)
**Commit:** `66b78929`

Added the per-profile `spec.limits` configuration surface:

- `LimitsSpec` struct with six action pointers (github_pr, github_comment, github_review, email_send, slack_post, h1_comment) in `pkg/profile/types.go`
- `ActionLimitSpec` with `*int64` window fields (Lifetime/PerHour/PerDay) and `OnBreach string`
- `Limits *LimitsSpec` field wired onto the `Spec` struct (additive, no apiVersion bump)
- JSON schema: `limits` object under `spec.properties` with `additionalProperties:false`, six known action keys each `$ref`-ing a new `ActionLimitSpec` definition in `$defs`
- `ActionLimitSpec` $def: `minimum:1` on integer windows, `onBreach` enum `["warn","block","freeze"]`, `additionalProperties:false`
- `validateLimits()` in `validate.go`: rejects onBreach outside {warn,block,freeze}; rejects lifetime/perHour/perDay ≤ 0
- `validate_limits_test.go` with `TestSpecLimits_*` covering: valid block, absent block (dormant), bad onBreach, zero/negative values (semantic layer), schema enum enforcement, schema minimum:1, schema additionalProperties

### Task 2 — limits: install-default config + v2→v merge-list entry (CFG-01)
**Commit:** `0eda8487`

Added the install-level `limits:` configuration surface in `internal/app/config/config.go`:

- `LimitsConfig` struct (local, avoids import cycle) with six `*ActionLimitConfig` pointer fields
- `ActionLimitConfig` type mirroring `ActionLimitSpec` field-for-field (mapstructure snake_case keys)
- `Limits LimitsConfig` field on `Config` struct
- `"limits"` added to the v2→v merge-list in `Load()` with `// Phase 121: install-level action quota defaults` comment
- `v.UnmarshalKey("limits", &cfg.Limits)` decode block
- `GetLimitsConfig() LimitsConfig` getter
- `config_limits_test.go` with `TestLimitsConfigLoaded` (populated + dormant), `TestLimitsConfigLoaded_MergeListRegression` (targeted guard for the merge-list footgun), `TestGetLimitsConfig` (getter round-trip)

## Verification

All verification criteria from the plan passed:

- `go test ./pkg/profile/... -run TestSpecLimits -count=1` GREEN (8/8 subtests pass)
- `go test ./internal/app/config/... -run TestLimitsConfigLoaded -count=1` GREEN
- `grep -q '"limits"' internal/app/config/config.go` — merge-list entry present
- No apiVersion change in the JSON schema (confirmed with `git diff`)
- Full `pkg/profile/...` suite: GREEN
- Full `internal/app/config/...` suite: GREEN

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

Files verified present:
- FOUND: pkg/profile/types.go
- FOUND: pkg/profile/validate.go
- FOUND: pkg/profile/schemas/sandbox_profile.schema.json
- FOUND: pkg/profile/validate_limits_test.go
- FOUND: internal/app/config/config.go
- FOUND: internal/app/config/config_limits_test.go

Commits verified:
- FOUND: 66b78929 (Task 1 — spec.limits types + schema + validate)
- FOUND: 0eda8487 (Task 2 — limits: config + merge-list + getter)
