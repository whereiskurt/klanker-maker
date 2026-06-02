---
phase: 93-km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward
plan: "02"
subsystem: profile-validation
tags: [validation, desktop, kasmvnc, semantic-rules, ubuntu-guard]
dependency_graph:
  requires: ["93-01"]  # RuntimeDesktopSpec + IsDesktopEnabled must exist
  provides: ["DSK-03-VALIDATE"]
  affects: [pkg/profile/validate.go, pkg/profile/validate_test.go]
tech_stack:
  added: []
  patterns:
    - "validateDesktop() sub-function pattern (mirrors validateAdditionalSnapshots)"
    - "rawAMIIDPatternLocal copied locally to avoid pkg/profile→pkg/compiler import cycle"
key_files:
  created: []
  modified:
    - pkg/profile/validate.go
    - pkg/profile/validate_test.go
decisions:
  - "Copied ^ami-[0-9a-f]{8,17}$ regex locally into validate.go — importing pkg/compiler from pkg/profile would create an import cycle"
  - "Empty mode defaults to 'kiosk' (valid) — consistent with locked design; only explicitly wrong names error"
  - "Empty browsers with mode=kiosk is an ERROR; empty browsers with mode=full is OK (full mode doesn't launch a specific browser on start)"
  - "ubuntu- prefix check via strings.HasPrefix — catches ubuntu-24.04, ubuntu-22.04, and any future ubuntu-X.Y slugs"
  - "Raw AMI ID gets WARN not ERROR — cannot determine OS family offline without AWS API call"
metrics:
  duration: "8 minutes"
  completed: "2026-06-02"
  tasks: 2
  files: 2
---

# Phase 93 Plan 02: Desktop Semantic Validation Rules Summary

**One-liner:** KasmVNC desktop ValidateSemantic rules — mode/browsers/geometry enum checks and Ubuntu-only AMI guard with raw-ID WARN path.

## What Was Built

Added `validateDesktop()` to `ValidateSemantic` in `pkg/profile/validate.go`, gated by `IsDesktopEnabled`. The function enforces four rule groups and fires only when `spec.runtime.desktop.enabled: true`.

### Rules Implemented

**Mode enum (spec.runtime.desktop.mode):**
- `kiosk` and `full` are valid
- Empty mode defaults to `kiosk` (valid — no error)
- Any other value (gnome, kde, KIOSK) → hard ERROR

**Browsers membership (spec.runtime.desktop.browsers):**
- Accepted set: `{firefox, chromium, chrome, brave}`
- Each entry validated; unknown browser (edge, safari) → hard ERROR at `spec.runtime.desktop.browsers[i]`
- mode=kiosk + empty/nil browsers → hard ERROR at `spec.runtime.desktop.browsers` (matchbox-wm must launch `browsers[0]`)
- mode=full + empty browsers → OK (full XFCE doesn't auto-launch a browser)

**Geometry regex (spec.runtime.desktop.geometry):**
- Must match `^[0-9]+x[0-9]+$` (lowercase x, digits only)
- Empty string → OK (platform defaults to 1920x1080)
- Invalid: `1920X1080`, `huge`, `1920x`, `x1080`, `1920 x 1080` → hard ERROR

**AMI / Ubuntu-only guard:**
- `strings.HasPrefix(ami, "ubuntu-")` → OK (covers ubuntu-24.04, ubuntu-22.04, future slugs)
- Raw AMI ID (`ami-[0-9a-f]{8,17}`) → WARN `IsWarning=true` (cannot verify OS family offline)
- Known non-Ubuntu slug (e.g. `amazon-linux-2023`) or empty AMI (platform default = AL2023) → hard ERROR

### Import Cycle Resolution

`pkg/compiler.IsRawAMIID` could not be imported because `pkg/compiler` imports `pkg/profile`. The regex `^ami-[0-9a-f]{8,17}$` was copied locally as `rawAMIIDPatternLocal` with an explicit comment tracking the source.

### Tests

All four Wave-0 stub tests were replaced with table-driven implementations:

| Test | Cases | Result |
|------|-------|--------|
| TestDesktopValidateMode | 7 (6 mode variants + disabled guard) | PASS |
| TestDesktopValidateBrowsers | 11 (valid/invalid browsers + kiosk-empty + full-empty) | PASS |
| TestDesktopValidateGeometry | 8 (valid/invalid geometry patterns) | PASS |
| TestDesktopValidateUbuntuGuard | 6 (ubuntu slugs, AL2023, empty, raw ID, disabled) | PASS |

Helper functions `makeDesktopProfile`, `makeDisabledDesktopProfile`, and `desktopErrs` were added to the test file to keep individual test cases readable.

## Deviations from Plan

None — plan executed exactly as written. The import-cycle question was resolved as specified in the plan (copy regex locally; do not import pkg/compiler from pkg/profile).

## Self-Check

- [x] `pkg/profile/validate.go` exists and contains `validateDesktop`
- [x] `pkg/profile/validate_test.go` exists and contains all 4 TestDesktopValidate* tests
- [x] Commit cd26fc9c exists
- [x] `go test ./pkg/profile/... -count=1` passes
