---
phase: 29-configurable-sandbox-id-prefix
plan: "03"
subsystem: sandbox-aliasing
tags: [aliases, metadata, s3, km-list, km-create, sandbox-ref]
dependency-graph:
  requires: [29-01]
  provides: [sandbox-aliasing]
  affects: [km-create, km-list, km-destroy, km-status, sandbox_ref]
tech-stack:
  added: []
  patterns:
    - S3 metadata scan for alias resolution (O(n), TODO DynamoDB GSI for O(1))
    - Auto-incrementing alias from profile template (wrkr-1, wrkr-2, ...)
    - Alias field in SandboxMetadata with omitempty JSON tag for backwards compat
key-files:
  created:
    - pkg/aws/sandbox_test.go
    - internal/app/cmd/alias_test.go
    - .planning/phases/29-configurable-sandbox-id-prefix/deferred-items.md
  modified:
    - pkg/aws/metadata.go
    - pkg/aws/sandbox.go
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/validate_test.go
    - profiles/claude-dev.yaml
    - internal/app/cmd/create.go
    - internal/app/cmd/sandbox_ref.go
    - internal/app/cmd/list.go
    - internal/app/cmd/destroy.go
decisions:
  - "Alias stored in SandboxMetadata.Alias field (json:omitempty) for backwards compat"
  - "ResolveSandboxAlias scans S3 metadata O(n) — TODO DynamoDB GSI for O(1) at scale"
  - "NextAliasFromTemplate uses max+1 strategy (not gap-filling) for predictability"
  - "--alias flag overrides profile metadata.alias template"
  - "Alias resolution inserted between ID pattern check and numeric check in ResolveSandboxID"
metrics:
  duration: ~25min
  completed: "2026-03-28"
  tasks: 2
  files: 10
---

# Phase 29 Plan 03: Sandbox Aliasing Summary

Human-friendly sandbox aliases stored in S3 metadata, resolved by scanning active sandboxes, displayed in km list, and freed on destroy.

## Objective

Add sandbox aliasing — human-friendly names that resolve to sandbox IDs. Operators can refer to sandboxes by short aliases (e.g. `orc`, `wrkr`) instead of hex IDs.

## Tasks Completed

| Task | Description | Commit | Files |
|------|-------------|--------|-------|
| 1 | Alias field in metadata + schema + resolution function + profile template | 1aec821 | pkg/aws/metadata.go, pkg/aws/sandbox.go, pkg/aws/sandbox_test.go, pkg/profile/types.go, pkg/profile/schemas/sandbox_profile.schema.json, pkg/profile/validate_test.go, profiles/claude-dev.yaml |
| 2 | Wire --alias flag into create, resolve in sandbox_ref, display in list | 2265d90 | internal/app/cmd/create.go, internal/app/cmd/sandbox_ref.go, internal/app/cmd/list.go, internal/app/cmd/destroy.go, internal/app/cmd/alias_test.go |

## What Was Built

### Core Data Model
- `SandboxMetadata.Alias` (`json:"alias,omitempty"`) — backwards compatible with old metadata
- `SandboxRecord.Alias` — populated from metadata in `readMetadataRecord`

### Functions
- `ResolveSandboxAlias(ctx, client, bucket, alias) (string, error)` — scans S3 metadata for matching alias; errors on not-found or duplicate
- `NextAliasFromTemplate(template, existingAliases) string` — generates `{template}-{max+1}` (e.g. `wrkr-1`, `wrkr-2`)

### Schema
- `metadata.alias` field in `sandbox_profile.schema.json` — pattern `^[a-z][a-z0-9]{0,15}$` (max 16 chars, lowercase alphanumeric, starts with letter)

### CLI
- `km create --alias orc` — sets alias directly; overrides profile template
- Profile `metadata.alias: wrkr` — auto-generates `wrkr-1`, `wrkr-2`, etc. on each `km create`
- `km list` — now shows `ALIAS` column between SANDBOX ID and PROFILE
- `km destroy` — prints `Alias freed: <alias>` before metadata deletion
- `ResolveSandboxID("orc")` — alias resolution fallback before numeric check

### Profile Update
- `profiles/claude-dev.yaml` now has `metadata.alias: claude` (and `metadata.prefix: claude` from plan 01)

## Decisions Made

1. **O(n) alias resolution via S3 scan** — future TODO: DynamoDB GSI on `km-identities` table for O(1) lookup when sandbox count makes S3 scan a bottleneck. Comment in code.

2. **max+1 not gap-filling** for NextAliasFromTemplate — existing `wrkr-1`, `wrkr-3` → returns `wrkr-4` (not `wrkr-2`). Prevents confusion where a destroyed `wrkr-2` is reused by a different sandbox.

3. **Alias resolution in ResolveSandboxID** — inserted between sandbox ID pattern check and numeric check, so IDs always bypass S3 scan. Aliases only attempted when StateBucket is configured (safe in tests).

## Deviations from Plan

None — plan executed exactly as written.

## Pre-existing Issues (Out of Scope)

19 tests in `internal/app/cmd` fail because Plan 29-02 introduced a stricter sandbox ID format (`^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$`) but test fixtures using old short IDs (`sb-001`, `sb-123`, `sb-ec2`) were not updated. Documented in `deferred-items.md`. These failures pre-exist this plan.

## Self-Check: PASSED

Files exist:
- pkg/aws/metadata.go — FOUND (Alias field)
- pkg/aws/sandbox.go — FOUND (ResolveSandboxAlias, NextAliasFromTemplate)
- pkg/aws/sandbox_test.go — FOUND (9 tests)
- internal/app/cmd/list.go — FOUND (ALIAS column)
- internal/app/cmd/sandbox_ref.go — FOUND (alias resolution path)
- internal/app/cmd/alias_test.go — FOUND (2 tests)

Commits:
- 1aec821 — FOUND (task 1)
- 2265d90 — FOUND (task 2)
