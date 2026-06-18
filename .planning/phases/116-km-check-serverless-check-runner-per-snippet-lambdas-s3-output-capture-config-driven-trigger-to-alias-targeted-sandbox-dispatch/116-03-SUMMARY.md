---
phase: 116-km-check-serverless-check-runner
plan: "03"
subsystem: config
tags: [config, checks, plumbing, tdd, merge-list]
dependency_graph:
  requires: []
  provides:
    - ChecksConfig/CheckTrigger types in internal/app/config/config.go
    - cfg.Checks.Triggers populated from km-config.yaml checks: block
    - GetChecksConfig()/GetChecksTriggers() getters
  affects:
    - internal/app/config/ (all downstream consumers of config.Load())
tech_stack:
  added: []
  patterns:
    - "checks: block mirrors github/h1: pattern (UnmarshalKey + merge-list)"
    - "TDD RED/GREEN: failing compile-error test then getter implementation"
key_files:
  created:
    - internal/app/config/config_check_test.go
  modified:
    - internal/app/config/config.go
decisions:
  - "yaml keys in km-config.yaml use snake_case (on_absent, cooldown_seconds) matching mapstructure tags; camelCase yaml struct tags are for marshal only (viper normalizes YAML to lowercase)"
  - "UnmarshalKey returns error (not non-fatal log) for checks: decode failure, matching h1 precedent"
  - "No YAMLDefaults snapshot for checks — list-of-objects, no scalar top-level key, mirrors github.events treatment"
metrics:
  duration: "~4 minutes"
  tasks_completed: 3
  files_modified: 2
  files_created: 1
  completed_date: "2026-06-18"
---

# Phase 116 Plan 03: ChecksConfig Plumbing Summary

**One-liner:** `checks.triggers:` config block fully plumbed through `config.Load()` with `ChecksConfig`/`CheckTrigger` structs, merge-list entry, `UnmarshalKey` decode, and getters — verified by TDD `TestChecksConfigMerge`.

## What Was Built

Added the `checks.triggers:` config block plumbing to `internal/app/config/config.go` following the established new-config-key ritual. Without this, any `checks.triggers:` in `km-config.yaml` would be silently dropped (the `project_config_key_merge_list` footgun).

### Types Added

```go
type ChecksConfig struct {
    Triggers []CheckTrigger `mapstructure:"triggers" yaml:"triggers,omitempty"`
}

type CheckTrigger struct {
    Check           string `mapstructure:"check"            yaml:"check"`
    WhenPy          string `mapstructure:"when_py"          yaml:"when_py,omitempty"`
    Alias           string `mapstructure:"alias"            yaml:"alias"`
    Prompt          string `mapstructure:"prompt"           yaml:"prompt,omitempty"`
    OnAbsent        string `mapstructure:"on_absent"        yaml:"onAbsent,omitempty"`
    CooldownSeconds int    `mapstructure:"cooldown_seconds" yaml:"cooldownSeconds,omitempty"`
}
```

### Config struct field

```go
Checks ChecksConfig `mapstructure:"checks" yaml:"checks,omitempty"`
```

### Merge-list entry (the critical piece)

Added `"checks"` to the v2→v merge-list in `config.Load()` immediately after `"h1"`. Without this single string, the entire `checks:` block in `km-config.yaml` is silently ignored by viper.

### UnmarshalKey decode

```go
if err := v.UnmarshalKey("checks", &cfg.Checks); err != nil {
    return nil, fmt.Errorf("unmarshal checks: %w", err)
}
```

### Getters

- `GetChecksConfig() ChecksConfig` — returns Config.Checks
- `GetChecksTriggers() []CheckTrigger` — returns Config.Checks.Triggers

## TDD Execution

**RED:** `config_check_test.go` written first — compile-failed on missing `GetChecksConfig`/`GetChecksTriggers` methods.

**GREEN:** Getters added; all tests pass. Also discovered that `km-config.yaml` YAML keys must use `snake_case` (`on_absent`, `cooldown_seconds`) to match the `mapstructure` tags — viper normalizes YAML keys to lowercase at read time, so `camelCase` yaml struct tags only apply to marshal operations (struct → YAML), not the viper read path.

## Test Coverage

- `TestChecksConfigMerge/populated` — all 6 fields round-trip correctly
- `TestChecksConfigMerge/absent` — absent checks: block yields empty Triggers (dormant)
- `TestChecksConfigMerge_MergeListRegression` — merge-list entry guard (fails if "checks" is removed from merge-list)
- `TestChecksGetters/populated` + `TestChecksGetters/absent` — getter return values verified

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check

### Files created/modified

- [x] `/Users/khundeck/working/klankrmkr/internal/app/config/config.go` — ChecksConfig/CheckTrigger types + merge-list + UnmarshalKey + getters
- [x] `/Users/khundeck/working/klankrmkr/internal/app/config/config_check_test.go` — TestChecksConfigMerge (all cases green)

### Commits

- ed5efae2 feat(116-03): add ChecksConfig/CheckTrigger structs + Config.Checks field
- 07e286e2 feat(116-03): wire checks into v2->v merge-list + UnmarshalKey decode
- 6f819ad4 test(116-03): add failing TestChecksConfigMerge (RED)
- e8c9f43a feat(116-03): implement GetChecksConfig/GetChecksTriggers getters (GREEN)

## Self-Check: PASSED
