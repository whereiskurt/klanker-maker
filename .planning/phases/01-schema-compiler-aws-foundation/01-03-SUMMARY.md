---
plan: 01-03
phase: 01-schema-compiler-aws-foundation
status: complete
started: 2026-03-21
completed: 2026-03-21
duration: "merged into 01-04 execution"
tasks_completed: 2
tasks_total: 2
---

# Plan 01-03: Profile Inheritance + Built-in Profiles

## What Was Built

- **pkg/profile/inherit.go**: Profile inheritance resolver with cycle detection, max depth 3, name-based lookup. Child-wins-replaces semantics for all fields; metadata.labels are merged additively. Resolve() does NOT call Validate() — callers handle separately.
- **pkg/profile/builtins.go**: Built-in profile loader using go:embed. ListBuiltins(), LoadBuiltin(), IsBuiltin() functions.
- **profiles/**: Four built-in profiles with graduated security:
  - open-dev (24h TTL, broad allowlists)
  - restricted-dev (8h TTL, curated allowlists)
  - hardened (4h TTL, minimal egress — AWS APIs only)
  - sealed (1h TTL, zero egress)
- All four profiles have all 4 sidecars enabled (DNS proxy, HTTP proxy, audit log, tracing)

## Key Files

### Created
- `pkg/profile/inherit.go` (~120 lines)
- `pkg/profile/inherit_test.go` (~110 lines)
- `pkg/profile/builtins.go` (~50 lines)
- `pkg/profile/builtins_test.go` (~120 lines)
- `profiles/open-dev.yaml`
- `profiles/restricted-dev.yaml`
- `profiles/hardened.yaml`
- `profiles/sealed.yaml`
- `pkg/profile/builtins/` (embedded copies of profiles)
- `testdata/profiles/child-extends-open-dev.yaml`
- `testdata/profiles/circular-a.yaml`
- `testdata/profiles/circular-b.yaml`
- `testdata/profiles/deep-inheritance.yaml`

## Deviations

- **01-04 executed 01-03's work**: The 01-04 executor detected that Plan 01-03 hadn't completed (due to subagent permission issues) and implemented the inheritance + builtins code as a prerequisite. This is a valid Rule 3 auto-fix for a blocking dependency.
- **go:embed path**: Profiles embedded via `pkg/profile/builtins/` subdirectory since `go:embed` cannot traverse `../../` paths.

## Requirements Completed

- SCHM-04: Profile inheritance via extends field
- SCHM-05: Four built-in profiles ship with Klanker Maker

## Self-Check: PASSED
- [x] All 19 inheritance + builtin tests pass
- [x] All four profiles pass Validate()
- [x] Correct TTLs (24h/8h/4h/1h)
- [x] All sidecars enabled on all profiles
- [x] Network graduation from permissive to zero
