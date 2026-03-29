---
phase: quick
plan: 2
subsystem: config-distribution
tags: [lambda, config, s3, toolchain, km-init]
dependency_graph:
  requires: []
  provides: [km-config-s3-upload, lambda-km-config-path]
  affects: [create-handler, km-init, config-loader]
tech_stack:
  added: []
  patterns: [env-var-config-override, non-fatal-s3-download]
key_files:
  created: []
  modified:
    - internal/app/config/config.go
    - internal/app/cmd/init.go
    - cmd/create-handler/main.go
decisions:
  - KM_CONFIG_PATH uses SetConfigFile on v2 viper (not v), keeping primary ~/.km/config.yaml search unchanged
  - km-config.yaml download in downloadToolchain is non-fatal (Warn) for backward compat with older deployments
metrics:
  duration: ~8min
  completed: 2026-03-29T04:24:51Z
  tasks_completed: 2
  tasks_total: 2
---

# Quick Task 2: Upload km-config.yaml to S3 Toolchain for create-handler Summary

**One-liner:** km init now uploads km-config.yaml as toolchain/km-config.yaml to S3, and the create-handler Lambda downloads it at cold start and passes KM_CONFIG_PATH to the km create subprocess so all platform config fields are available explicitly.

## What Was Built

Three files wired together to distribute km-config.yaml from local init through S3 into the Lambda subprocess environment:

1. **config.go** — `Load()` now checks `KM_CONFIG_PATH` env var first; when set, calls `v2.SetConfigFile(configPath)` instead of adding search paths. Existing `KM_REPO_ROOT` fallback is unchanged when `KM_CONFIG_PATH` is absent.

2. **init.go** — `uploadCreateHandlerToolchain()` now includes step 5: stat `$repoRoot/km-config.yaml` and upload it to `toolchain/km-config.yaml` via the existing `s3Upload` helper. Non-fatal if file is missing (prints a warning).

3. **create-handler/main.go** — Two changes:
   - `downloadToolchain()`: after extracting infra.tar.gz, attempts to download `toolchain/km-config.yaml` to `$toolchainDir/km-config.yaml`. Non-fatal on error (logs Warn, continues).
   - `Handle()`: adds `KM_CONFIG_PATH=$toolchainDir/km-config.yaml` alongside the other subprocess env vars.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Add KM_CONFIG_PATH to config loader and upload in init | 8b93fc6 | internal/app/config/config.go, internal/app/cmd/init.go |
| 2 | Download km-config.yaml at Lambda cold start and pass KM_CONFIG_PATH | 72d6ef2 | cmd/create-handler/main.go |

## Verification

- `go build ./...` — PASSED
- `go test ./cmd/create-handler/ -v -count=1` — PASSED (5 tests)
- `grep "KM_CONFIG_PATH"` found in config.go and create-handler/main.go
- `grep "toolchain/km-config.yaml"` found in init.go and create-handler/main.go

## Deviations from Plan

None - plan executed exactly as written.

## Self-Check: PASSED

- `internal/app/config/config.go` — modified, KM_CONFIG_PATH check present
- `internal/app/cmd/init.go` — modified, toolchain/km-config.yaml upload present
- `cmd/create-handler/main.go` — modified, km-config.yaml download + KM_CONFIG_PATH env var present
- Commit 8b93fc6 — exists
- Commit 72d6ef2 — exists
