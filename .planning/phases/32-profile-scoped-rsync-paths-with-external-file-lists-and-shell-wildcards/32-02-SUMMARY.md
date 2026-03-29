---
phase: 32-profile-scoped-rsync-paths-with-external-file-lists-and-shell-wildcards
plan: "02"
subsystem: rsync
tags: [rsync, profile, tdd, path-resolution, security]
dependency_graph:
  requires: [ExecutionSpec.RsyncPaths, ExecutionSpec.RsyncFileList]
  provides: [resolveRsyncPaths, validateRsyncPath, loadFileList, profile-aware rsync save]
  affects: [internal/app/cmd/rsync.go, internal/app/cmd/rsync_test.go]
tech_stack:
  added: []
  patterns: [TDD red-green, best-effort S3 fetch, path validation regex]
key_files:
  created:
    - internal/app/cmd/rsync_test.go
  modified:
    - internal/app/cmd/rsync.go
decisions:
  - Paths passed unquoted to bash for wildcard expansion after regex validation gates injection
  - Profile fetch from S3 is best-effort (error ignored) so rsync save degrades gracefully to global fallback
  - rsyncFileList absolute paths are used as-is; relative paths are joined to profileDir
metrics:
  duration: 289s
  completed_date: "2026-03-29"
  tasks_completed: 2
  files_modified: 2
---

# Phase 32 Plan 02: Profile-Scoped rsync Paths Wired into km rsync save

**One-liner:** Wired `resolveRsyncPaths` into `km rsync save` with S3 profile fetch, external file list merge, dedup, shell-injection validation, and unquoted wildcard-friendly tar paths.

## What Was Built

Three helper functions and an updated save command that make sandbox rsync truly profile-driven:

- `validateRsyncPath(p string) error` — regex `^[a-zA-Z0-9_./*?\[\]-]+$` gates paths; rejects `$`, backtick, `;`, `|`, `&`, `>`, `<`, and spaces
- `loadFileList(path string) ([]string, error)` — reads and parses external `paths:` YAML via `goccy/go-yaml`
- `resolveRsyncPaths(prof *profile.SandboxProfile, profileDir string, globalFallback []string) ([]string, error)` — merges `rsyncPaths` + `rsyncFileList` with de-duplication; falls back to global config when profile is nil or has neither field
- Updated `newRsyncSaveCmd` — fetches `.km-profile.yaml` from `artifacts/{sandbox-id}/` (best-effort, no failure on 404), resolves paths via helper, passes unquoted paths to tar for bash wildcard expansion (`projects/*/config` works correctly)

## Tasks Completed

| Task | Name | Commit | Key Files |
|------|------|--------|-----------|
| 1 (RED) | Add failing tests for path helpers | 5979ae7 | internal/app/cmd/rsync_test.go |
| 1 (GREEN) | Implement validateRsyncPath, loadFileList, resolveRsyncPaths | c81ed90 | internal/app/cmd/rsync.go |
| 2 | Wire helpers into km rsync save command | ae85fd2 | internal/app/cmd/rsync.go |

## Decisions Made

- **Unquoted paths in tar command:** After `validateRsyncPath` blocks injection, paths are passed unquoted so bash expands wildcards like `projects/*/config`. Single-quoting (old approach) would prevent glob expansion.
- **Best-effort S3 profile fetch:** `profErr` is checked but ignored on failure. If the profile YAML isn't in S3 (e.g., old sandbox), rsync save gracefully falls back to `cfg.RsyncPaths` / built-in defaults. This avoids breaking existing sandboxes.
- **Absolute vs relative file list paths:** If `RsyncFileList` is absolute it's used directly; if relative it's joined with `profileDir`. At `km rsync save` time `profileDir` is `"."` (operator cwd), matching the research spec.

## Deviations from Plan

None — plan executed exactly as written.

## Verification

```
go test ./... — all packages PASS
go build -o km ./cmd/km/ — BUILD OK
```

Tests included:
- `TestResolveRsyncPaths` (5 sub-tests: profile paths, global fallback, nil profile, merge+dedup, missing file list)
- `TestLoadFileList` (2 sub-tests: valid YAML, invalid YAML)
- `TestValidateRsyncPath` (15 sub-tests: 7 valid, 8 invalid)

## Self-Check: PASSED

All required files exist and all commits are present in git history.
