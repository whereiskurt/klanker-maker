---
phase: 93-km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward
plan: "01"
subsystem: profile-schema
tags: [desktop, kasmvnc, schema, types, tdd]
dependency_graph:
  requires: [93-00]
  provides: [RuntimeDesktopSpec, IsDesktopEnabled, desktop-json-schema]
  affects: [pkg/profile/types.go, pkg/profile/types_test.go, pkg/profile/schemas/sandbox_profile.schema.json]
tech_stack:
  added: []
  patterns: [tdd-red-green, mirror-vscode-sibling]
key_files:
  created: []
  modified:
    - pkg/profile/types.go
    - pkg/profile/types_test.go
    - pkg/profile/schemas/sandbox_profile.schema.json
decisions:
  - "RuntimeDesktopSpec is opt-in (default false) — deliberate opposite of IsVSCodeEnabled default-on; KasmVNC is a heavy install"
  - "JSON Schema uses additionalProperties: false on desktop object matching house style"
  - "browsers enum includes chrome (Google Chrome) in addition to chromium per RESEARCH.md keyword mapping"
metrics:
  duration: "119s"
  completed: "2026-06-02"
  tasks_completed: 2
  files_modified: 3
---

# Phase 93 Plan 01: Desktop Schema Root Summary

RuntimeDesktopSpec + IsDesktopEnabled default-false helper + JSON Schema desktop block — the schema root consumed by validation, compiler, and CLI across the rest of Phase 93.

## What Was Built

Two tasks executed via TDD:

**Task 1 (TDD RED → GREEN):** Added `RuntimeDesktopSpec` struct, `RuntimeSpec.Desktop` field, and `IsDesktopEnabled` helper to `pkg/profile/types.go`.
- `RuntimeDesktopSpec` is a sibling to `RuntimeVSCodeSpec` (after line 177) with fields: `Enabled *bool`, `Mode string`, `Browsers []string`, `Geometry string`
- `Desktop *RuntimeDesktopSpec` field added to `RuntimeSpec` after the `VSCode` field
- `IsDesktopEnabled(*RuntimeDesktopSpec) bool` added after `IsVSCodeEnabled` — returns false for nil block or nil Enabled (opt-in default), true only for explicit `&true`
- `TestIsDesktopEnabled` updated from Wave 0 `t.Skip` to four table-driven cases; all GREEN

**Task 2:** Added `desktop` object schema to `pkg/profile/schemas/sandbox_profile.schema.json` under `runtime.properties`, sibling to the `vscode` block.
- `enabled`: boolean, opt-in default false
- `mode`: enum `["kiosk", "full"]`
- `browsers`: array with items enum `["firefox", "chromium", "chrome", "brave"]`
- `geometry`: string with pattern `^[0-9]+x[0-9]+$`
- `additionalProperties: false` on the desktop object (house style)

## Verification

- `go build ./pkg/profile/...` — PASS
- `go test ./pkg/profile/... -run TestIsDesktopEnabled -count=1` — PASS (4/4 subtests)
- `go test ./pkg/profile/... -run TestSchema -count=1` — PASS (all existing schema tests still green)
- `python3` JSON parse confirms `desktop` block exists under `runtime.properties`

## Deviations from Plan

**1. [Rule 2 - Missing field] Added `chrome` to browsers enum**
- **Found during:** Task 2 (JSON Schema browsers array)
- **Issue:** RESEARCH.md explicitly documents `chrome` (Google Chrome, different from `chromium`) as a first-class enum member with its own apt package (`google-chrome-stable`) and binary mapping
- **Fix:** Added `chrome` to the browsers enum `["firefox", "chromium", "chrome", "brave"]`
- **Files modified:** `pkg/profile/schemas/sandbox_profile.schema.json`
- The plan text said "subset of {firefox, chromium, brave}" but RESEARCH.md Patterns table adds `chrome` as a distinct entry

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| RED test | fd483639 | test(93-01): add failing TestIsDesktopEnabled test (RED) |
| Task 1 GREEN | 58e0b26f | feat(93-01): add RuntimeDesktopSpec + IsDesktopEnabled to types.go |
| Task 2 | c5117764 | feat(93-01): add spec.runtime.desktop JSON Schema block |

## Self-Check: PASSED

All checked files exist and commits verified:

- `pkg/profile/types.go` — FOUND (RuntimeDesktopSpec + IsDesktopEnabled + Desktop field)
- `pkg/profile/types_test.go` — FOUND (TestIsDesktopEnabled GREEN)
- `pkg/profile/schemas/sandbox_profile.schema.json` — FOUND (desktop block present)
- Commit fd483639 — FOUND
- Commit 58e0b26f — FOUND
- Commit c5117764 — FOUND
