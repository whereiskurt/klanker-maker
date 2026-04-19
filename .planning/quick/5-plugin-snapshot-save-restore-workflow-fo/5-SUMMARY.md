---
phase: quick-5
plan: 01
subsystem: compiler/bootstrap
tags: [plugins, rsync, snapshot, restore, path-rewrite]
dependency_graph:
  requires: []
  provides: [plugin-path-rewrite-on-restore]
  affects: [pkg/compiler/userdata.go, profiles/learn.yaml]
tech_stack:
  added: []
  patterns: [sed-path-rewrite, post-restore-fixup]
key_files:
  created: []
  modified:
    - pkg/compiler/userdata.go
    - profiles/learn.yaml
decisions:
  - "Used generic sed pattern matching any absolute path before /.claude/ rather than key-specific patterns"
  - "learn.yaml .claude already covers plugins; added documentation comment only"
metrics:
  duration: "42s"
  completed: "2026-04-19"
---

# Quick Task 5: Plugin Snapshot Save/Restore Path Rewrite Summary

Post-restore sed rewrite in bootstrap userdata fixes hardcoded absolute paths in Claude plugin manifests so snapshots work across sandboxes with different home directories.

## What Was Done

### Task 1: Post-restore path rewrite in bootstrap userdata
- Added a `for` loop in the rsync restore block of `pkg/compiler/userdata.go` (after tar extract and chown)
- Rewrites `installed_plugins.json` and `known_marketplaces.json` using `sed -i -E` to replace any absolute path prefix before `/.claude/` with the target sandbox's `$SHELL_HOME`
- Only runs if the files exist (idempotent)
- Commit: `2e91bac`

### Task 2: learn.yaml documentation comment
- Added YAML comment above `rsyncPaths` noting the plugin-only pattern: `rsyncPaths: [".claude/plugins"]`
- No functional change; `.claude` already covers `.claude/plugins`
- Validated with `km validate profiles/learn.yaml`
- Commit: `587cc70`

## Deviations from Plan

None - plan executed exactly as written.

## Commits

| Task | Commit    | Description                                      |
|------|-----------|--------------------------------------------------|
| 1    | `2e91bac` | Post-restore path rewrite for plugin snapshots   |
| 2    | `587cc70` | Plugin-only rsyncPaths comment in learn.yaml     |

## Verification

- `go build ./pkg/compiler/` - compiles without errors
- `km validate profiles/learn.yaml` - passes
- Visual inspection confirms sed rewrite appears after tar extract and chown in rsync restore block

## Self-Check: PASSED

All files exist and all commits verified.
