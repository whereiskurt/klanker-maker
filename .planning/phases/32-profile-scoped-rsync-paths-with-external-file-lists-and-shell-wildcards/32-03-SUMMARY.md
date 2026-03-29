---
phase: 32-profile-scoped-rsync-paths-with-external-file-lists-and-shell-wildcards
plan: "03"
subsystem: rsync
tags: [rsync, tdd, gap-closure, verification, wildcard]
dependency_graph:
  requires: [buildTarShellCmd inline in rsync.go]
  provides: [TestRsyncSaveCmd, buildTarShellCmd extracted helper]
  affects: [internal/app/cmd/rsync.go, internal/app/cmd/rsync_test.go]
tech_stack:
  added: []
  patterns: [TDD red-green, extracted helper for testability]
key_files:
  created: []
  modified:
    - internal/app/cmd/rsync.go
    - internal/app/cmd/rsync_test.go
decisions:
  - Extracted buildTarShellCmd as package-level function (not exported) for test access within same package
  - Paths joined with space separator (strings.Join) preserving unquoted wildcard expansion
metrics:
  duration: 618s
  completed_date: "2026-03-29"
  tasks_completed: 2
  files_modified: 2
---

# Phase 32 Plan 03: Gap Closure -- TestRsyncSaveCmd and Live Verification

**One-liner:** Extracted buildTarShellCmd helper for testability, added 4-subtest TestRsyncSaveCmd covering RSYNC-06 tar command format, and confirmed wildcard expansion plus fallback on live sandboxes.

## What Was Built

Closed the two remaining verification gaps from 32-VERIFICATION.md:

1. **buildTarShellCmd helper** -- Extracted the inline shell command construction (previously at lines 192-203 in rsync.go) into a standalone `buildTarShellCmd(paths []string, bucket, s3Key string) string` function. The call site in `newRsyncSaveCmd` now delegates to this helper. No behavior change -- purely a refactor for testability.

2. **TestRsyncSaveCmd** -- Added 4 sub-tests covering RSYNC-06 (tar command format with unquoted paths):
   - Literal paths appear unquoted in the for-loop
   - Wildcard paths appear unquoted (no single-quoting that would prevent glob expansion)
   - Output contains `tar czf` and `aws s3 cp` with correct bucket/key
   - Single path works without trailing space artifacts

3. **Live sandbox verification** -- Human confirmed:
   - Wildcard expansion (`projects/*/config`) resolves to actual matching directories, not literal glob
   - Pre-phase-32 sandboxes fall back to global rsync_paths without error

## Tasks Completed

| Task | Name | Commit | Key Files |
|------|------|--------|-----------|
| 1 (RED) | Add failing TestRsyncSaveCmd tests | 885b6e2 | internal/app/cmd/rsync_test.go |
| 1 (GREEN) | Extract buildTarShellCmd, wire into rsync save | d68e0d6 | internal/app/cmd/rsync.go |
| 2 | Human-verify wildcard expansion and fallback | -- (checkpoint) | -- |

## Decisions Made

- **Package-level function (not exported):** `buildTarShellCmd` is lowercase (unexported) since tests are in the same `cmd` package and external callers should not construct shell commands directly.
- **Space-joined paths:** `strings.Join(paths, " ")` preserves the existing unquoted format that allows bash glob expansion after regex validation gates injection.

## Deviations from Plan

None -- plan executed exactly as written.

## Verification

```
go test ./internal/app/cmd/... -run TestRsyncSaveCmd -v -- 4/4 sub-tests PASS
go test ./... -- full suite green (all packages PASS)
go build -o km ./cmd/km/ -- BUILD OK
Human verified: wildcard expansion works on live sandbox
Human verified: fallback to global rsync_paths for pre-phase-32 sandboxes
```

## Self-Check: PASSED

All required files exist and all commits are present in git history.
