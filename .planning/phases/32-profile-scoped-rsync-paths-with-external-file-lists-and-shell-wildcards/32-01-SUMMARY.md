---
phase: 32-profile-scoped-rsync-paths-with-external-file-lists-and-shell-wildcards
plan: "01"
subsystem: profile
tags: [schema, rsync, types, tdd]
dependency_graph:
  requires: []
  provides: [ExecutionSpec.RsyncPaths, ExecutionSpec.RsyncFileList, schema.rsyncPaths, schema.rsyncFileList]
  affects: [pkg/profile/types.go, pkg/profile/schemas/sandbox_profile.schema.json]
tech_stack:
  added: []
  patterns: [TDD red-green, JSON Schema additionalProperties extension]
key_files:
  created: []
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/types_test.go
    - pkg/profile/validate_test.go
decisions:
  - Added RsyncPaths and RsyncFileList as omitempty fields so profiles without them remain valid (backward compat)
  - JSON schema items constraint on rsyncPaths enforces string-only arrays at validate time
metrics:
  duration: 87s
  completed_date: "2026-03-29"
  tasks_completed: 1
  files_modified: 4
---

# Phase 32 Plan 01: Profile rsyncPaths and rsyncFileList Schema Fields Summary

**One-liner:** Added `RsyncPaths []string` and `RsyncFileList string` to `ExecutionSpec` with matching JSON schema properties so profiles can declare per-workload rsync paths.

## What Was Built

Foundation fields for profile-scoped rsync. Profiles can now declare:
- `rsyncPaths` — an array of paths (relative to sandbox `$HOME`) to include in rsync snapshots; shell wildcards like `projects/*/config` are supported
- `rsyncFileList` — a path to a local YAML file containing additional rsync paths, resolved from operator cwd at `km rsync save` time

These fields are wired up at the Go type level (`ExecutionSpec`) and validated by the JSON schema. Plan 02 will wire them into the rsync save command itself.

## Tasks Completed

| Task | Name | Commit | Key Files |
|------|------|--------|-----------|
| 1 (RED) | Add failing tests for rsyncPaths and rsyncFileList | 3a5b2fd | pkg/profile/types_test.go, pkg/profile/validate_test.go |
| 1 (GREEN) | Implement RsyncPaths and RsyncFileList fields + schema | d590b7c | pkg/profile/types.go, pkg/profile/schemas/sandbox_profile.schema.json |

## Decisions Made

- **omitempty on both fields:** Profiles without `rsyncPaths` or `rsyncFileList` parse cleanly without any nil-pointer risks. The JSON schema properties are not in the `required` array, providing full backward compatibility.
- **JSON schema items: { type: string }:** The schema constrains `rsyncPaths` to string-only arrays, so `km validate` will reject profiles that accidentally put integers or objects in the list.

## Deviations from Plan

None — plan executed exactly as written.

## Verification

```
go test ./pkg/profile/... -v
```

All 71+ profile tests pass including:
- `TestRsyncPathsParsing` (3 sub-tests)
- `TestRsyncSchemaValidation` (5 sub-tests)

## Self-Check: PASSED

All required files exist and all commits are present in git history.
