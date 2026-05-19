# Phase 85 — Deferred Items (out-of-scope discoveries)

Items discovered during Phase 85 execution that are outside the plan's scope
boundary. These are NOT fixed within Phase 85 — log only.

## Pre-existing test failure: TestUnlockCmd_RequiresStateBucket

- **File:** `internal/app/cmd/unlock_test.go:73`
- **Symptom:** test asserts error message must mention `'state bucket'`, but
  the actual error is `sandbox sb-aabbccdd is not locked`.
- **Reproduction:** `go test ./internal/app/cmd/ -run TestUnlockCmd_RequiresStateBucket -v`
  fails on commit `204aca8` AND on `781398c` (Phase 85 Plan 01 head) —
  unrelated to Phase 85 work.
- **Discovered:** Plan 02 (2026-05-19) full-package test run.
- **Why deferred:** Out of Phase 85 scope (sweeper has zero overlap with the
  `km unlock` command surface). Belongs in a separate `km unlock` test-fix
  phase or hot-fix commit.
