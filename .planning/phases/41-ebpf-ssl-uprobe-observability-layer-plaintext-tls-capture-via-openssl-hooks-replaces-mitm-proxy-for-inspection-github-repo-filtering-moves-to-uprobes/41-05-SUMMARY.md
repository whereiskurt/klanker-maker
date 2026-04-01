---
phase: 41-ebpf-ssl-uprobe-observability
plan: 05
subsystem: ebpf
tags: [ebpf, tls, uprobe, openssl, cli, compiler, user-data]

# Dependency graph
requires:
  - phase: 41-02
    provides: "OpenSSL uprobe BPF programs and AttachOpenSSL() API"
  - phase: 41-03
    provides: "TLS ring buffer consumer, EventHandler, GitHub/Bedrock audit handlers"
  - phase: 41-04
    provides: "TLS library discovery (FindSystemLibssl)"
provides:
  - "km ebpf-attach --tls flag wiring TLS uprobes into sandbox lifecycle"
  - "Compiler user-data conditional --tls emission for EC2 bootstrap"
affects: [41-06, 41-07]

# Tech tracking
tech-stack:
  added: []
  patterns: ["conditional CLI flag wiring for optional eBPF subsystems", "template conditional for user-data flag emission"]

key-files:
  created: []
  modified:
    - internal/app/cmd/ebpf_attach.go
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go

key-decisions:
  - "TLS probe failure is non-fatal -- warns and continues network enforcement"
  - "AllowedRepos for TLS audit reuses existing GitHub AllowedRepos from profile sourceAccess"

patterns-established:
  - "Optional eBPF subsystem wiring: flag-gated, non-fatal on attach failure, explicit cleanup in shutdown path"

requirements-completed: [EBPF-TLS-01, EBPF-TLS-12]

# Metrics
duration: 3min
completed: 2026-04-01
---

# Phase 41 Plan 05: CLI + Compiler Integration Summary

**km ebpf-attach gains --tls flag wiring OpenSSL uprobes with GitHub/Bedrock audit handlers; compiler emits --tls in EC2 user-data when tlsCapture enabled**

## Performance

- **Duration:** 3 min
- **Started:** 2026-04-01T21:06:49Z
- **Completed:** 2026-04-01T21:10:11Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments
- Extended km ebpf-attach with --tls and --allowed-repos flags for TLS uprobe lifecycle
- TLS probe discovery, attachment, consumer startup, and graceful shutdown all wired into existing SIGTERM handler
- Compiler conditionally emits --tls and --allowed-repos in EC2 user-data systemd service when profile has tlsCapture.enabled=true
- 4 new test cases covering enabled, disabled, explicitly disabled, and repo list scenarios

## Task Commits

Each task was committed atomically:

1. **Task 1: Extend km ebpf-attach with --tls flag** - `4e83712` (feat)
2. **Task 2: Compiler user-data emits --tls flag** - `403c89a` (feat)

## Files Created/Modified
- `internal/app/cmd/ebpf_attach.go` - Added --tls/--allowed-repos flags, TLS uprobe lifecycle in runEbpfAttach
- `pkg/compiler/userdata.go` - Added TLSEnabled/TLSAllowedRepos fields and conditional template emission
- `pkg/compiler/userdata_test.go` - 4 test cases for TLS capture user-data generation

## Decisions Made
- TLS probe failures are non-fatal: log warning and continue with network enforcement only
- Reuse existing GitHub AllowedRepos from profile sourceAccess for --allowed-repos flag
- No stub file needed for ebpf_attach since the entire file is gated by linux && amd64 build tag

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- TLS uprobe lifecycle fully wired into CLI and compiler pipeline
- Ready for end-to-end testing (Plan 06) and profile schema documentation (Plan 07)

---
*Phase: 41-ebpf-ssl-uprobe-observability*
*Completed: 2026-04-01*
