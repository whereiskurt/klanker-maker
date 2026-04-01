---
phase: 41-ebpf-ssl-uprobe-observability
plan: 04
subsystem: profile
tags: [ebpf, tls, uprobe, openssl, yaml-schema, json-schema]

# Dependency graph
requires:
  - phase: 41-01
    provides: eBPF enforcer foundation and cgroup attach infrastructure
provides:
  - TlsCaptureSpec Go type with IsEnabled() and EffectiveLibraries() methods
  - JSON Schema validation for tlsCapture field under observability
  - Forward-compatible library enum (openssl, gnutls, nss, go, rustls, all)
  - hardened and sealed built-in profiles with tlsCapture enabled
affects: [41-05, 41-06, compiler, ebpf-uprobe-attach]

# Tech tracking
tech-stack:
  added: []
  patterns: [optional-pointer-spec-with-IsEnabled-pattern, schema-forward-compatibility]

key-files:
  created: []
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/builtins/hardened.yaml
    - pkg/profile/builtins/sealed.yaml
    - pkg/profile/types_test.go
    - pkg/profile/validate_test.go

key-decisions:
  - "Only openssl implemented this phase; gnutls/nss/go/rustls accepted by schema for forward compatibility but no-op at runtime"
  - "TlsCaptureSpec follows existing optional pointer pattern (like ClaudeTelemetrySpec) with IsEnabled() method"
  - "Default libraries when empty list with enabled=true is openssl-only, not all"

patterns-established:
  - "TLS library enum pattern: schema accepts all library names for forward compatibility; runtime logs 'not yet implemented' for deferred libraries"

requirements-completed: [EBPF-TLS-11, EBPF-TLS-03, EBPF-TLS-04, EBPF-TLS-05, EBPF-TLS-06]

# Metrics
duration: 2min
completed: 2026-04-01
---

# Phase 41 Plan 04: Profile Schema TLS Capture Summary

**TlsCaptureSpec added to SandboxProfile with JSON Schema validation, supporting openssl with forward-compatible enum for gnutls/nss/go/rustls**

## Performance

- **Duration:** 2 min
- **Started:** 2026-04-01T20:59:45Z
- **Completed:** 2026-04-01T21:01:56Z
- **Tasks:** 1 (TDD: RED + GREEN)
- **Files modified:** 6

## Accomplishments
- Added TlsCaptureSpec type with Enabled, Libraries, CapturePayloads fields and helper methods
- Extended JSON Schema with tlsCapture under observability (enum-validated library names, required enabled field)
- Updated hardened and sealed built-in profiles to enable TLS capture with openssl
- Full backwards compatibility verified -- profiles without tlsCapture parse cleanly
- EBPF-TLS-03/04/05/06 satisfied at schema level (deferred libraries accepted by enum)

## Task Commits

Each task was committed atomically:

1. **Task 1 (RED): Failing tests for TlsCaptureSpec** - `17bbe4a` (test)
2. **Task 1 (GREEN): Implement TlsCaptureSpec and schema** - `cef563e` (feat)

## Files Created/Modified
- `pkg/profile/types.go` - Added TlsCaptureSpec type, IsEnabled(), EffectiveLibraries(), extended ObservabilitySpec
- `pkg/profile/schemas/sandbox_profile.schema.json` - Added tlsCapture object under observability with enum validation
- `pkg/profile/builtins/hardened.yaml` - Added tlsCapture enabled with openssl library
- `pkg/profile/builtins/sealed.yaml` - Added tlsCapture enabled with openssl library
- `pkg/profile/types_test.go` - Tests for parsing, IsEnabled(), EffectiveLibraries(), backwards compatibility
- `pkg/profile/validate_test.go` - JSON Schema validation tests for valid/invalid tlsCapture configs

## Decisions Made
- Only openssl is implemented this phase; gnutls, nss, go, rustls are accepted by schema but deferred (per research findings: fragile, per-binary, high maintenance)
- TlsCaptureSpec uses the existing optional pointer pattern (like ClaudeTelemetrySpec) with IsEnabled() method for nil-safe checking
- Default libraries when list is empty with enabled=true is openssl-only, not all supported libraries
- open-dev and restricted-dev profiles do not get tlsCapture (not needed for dev profiles)

## Deviations from Plan
None - plan executed exactly as written.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Profile schema ready for compiler integration (41-05 or later plans)
- TlsCaptureSpec.IsEnabled() and EffectiveLibraries() ready for uprobe attach code to consume
- Deferred libraries documented; future phases can implement without schema changes

---
*Phase: 41-ebpf-ssl-uprobe-observability*
*Completed: 2026-04-01*
