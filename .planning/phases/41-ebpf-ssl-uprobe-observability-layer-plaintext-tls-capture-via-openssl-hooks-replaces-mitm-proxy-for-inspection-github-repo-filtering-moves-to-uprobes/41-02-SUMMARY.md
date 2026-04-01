---
phase: 41-ebpf-ssl-uprobe-observability
plan: 02
subsystem: ebpf
tags: [bpf, uprobe, openssl, tls, ringbuf, consumer, discovery, proc-maps]

requires:
  - phase: 41-ebpf-ssl-uprobe-observability
    plan: 01
    provides: "BPF C programs (openssl.bpf.c, connect.bpf.c), generated bpf2go loader code, TLSEvent type"
provides:
  - "OpenSSL uprobe attach/detach lifecycle (AttachOpenSSL, Close)"
  - "Library discovery via /proc/pid/maps scanning (DiscoverLibraries)"
  - "Ring buffer consumer with handler dispatch (Consumer, AddHandler, Run)"
  - "Per-library enable/disable via BPF map (SetLibraryEnabled)"
  - "FindSystemLibssl fallback for common system paths"
affects: [41-03, 41-04, 41-05, tls-observer, github-filtering]

tech-stack:
  added: []
  patterns:
    - "uprobe attachment via cilium/ebpf link.OpenExecutable + Uprobe/Uretprobe"
    - "Map sharing between BPF object collections via MapReplacements"
    - "Ring buffer consumer with context cancellation for clean shutdown"
    - "Testable /proc scanner via dependency-injected directory path"

key-files:
  created:
    - pkg/ebpf/tls/openssl.go
    - pkg/ebpf/tls/openssl_stub.go
    - pkg/ebpf/tls/discovery.go
    - pkg/ebpf/tls/discovery_test.go
    - pkg/ebpf/tls/consumer.go
    - pkg/ebpf/tls/consumer_test.go
  modified: []

key-decisions:
  - "Shared BPF maps between openssl and connect objects via CollectionOptions.MapReplacements to avoid duplicate map instances"
  - "Optional uprobe attach for SSL_write_ex/SSL_read_ex — gracefully skip on OpenSSL 1.1.x which lacks _ex variants"
  - "EventHandler type reused from existing github.go rather than redeclared in consumer.go"

patterns-established:
  - "uprobe optional attach: try symbol, log+skip on ErrNotSupported for version compatibility"
  - "Testable proc scanner: discoverLibrariesFromDir(dir) accepts arbitrary dir for test fixtures"
  - "Ring buffer consumer pattern: context goroutine closes reader to unblock Read loop"

requirements-completed: [EBPF-TLS-02, EBPF-TLS-12, EBPF-TLS-13]

duration: 4min
completed: 2026-04-01
---

# Phase 41 Plan 02: OpenSSL Uprobe Module, Library Discovery, and Ring Buffer Consumer Summary

**OpenSSL uprobe attach/detach lifecycle with /proc/pid/maps library discovery and ring buffer event consumer dispatching to pluggable handlers**

## Performance

- **Duration:** 4 min
- **Started:** 2026-04-01T20:59:43Z
- **Completed:** 2026-04-01T21:03:13Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments
- OpenSSL uprobe module attaches to SSL_write, SSL_read (required) and _ex variants (optional) with kprobe connection correlation
- Library discovery scans /proc/pid/maps to find loaded libssl/libgnutls/libnspr4, deduplicates by path, collects PIDs
- Ring buffer consumer drains TLS events, deserializes into TLSEvent, dispatches to registered handlers with error isolation
- Per-library toggle via lib_enabled BPF map allows enabling/disabling capture without detaching probes
- arm64 stub compiles for Lambda compatibility

## Task Commits

Each task was committed atomically:

1. **Task 1: OpenSSL uprobe module and library discovery scanner** - `ac4c495` (feat)
2. **Task 2: Ring buffer consumer with handler dispatch and metrics** - `b8163ea` (feat)

## Files Created/Modified
- `pkg/ebpf/tls/openssl.go` - OpenSSL uprobe attach/detach lifecycle with map sharing
- `pkg/ebpf/tls/openssl_stub.go` - arm64 stub returning "uprobe only supported on amd64"
- `pkg/ebpf/tls/discovery.go` - /proc/pid/maps scanner with library classification and deduplication
- `pkg/ebpf/tls/discovery_test.go` - Tests for classifyLibrary, discovery with mock proc dirs, FindSystemLibssl
- `pkg/ebpf/tls/consumer.go` - Ring buffer consumer with handler dispatch and atomic metrics
- `pkg/ebpf/tls/consumer_test.go` - Tests for event parsing, multi-handler dispatch, error resilience, struct size

## Decisions Made
- Shared BPF maps between openssl and connect objects via MapReplacements to avoid duplicate map instances for conn_map, lib_enabled, ssl_read_args_map, and tls_events
- Optional uprobe attach for SSL_write_ex/SSL_read_ex gracefully skips on OpenSSL 1.1.x
- Reused existing EventHandler type from github.go instead of redeclaring in consumer.go

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Resolved EventHandler type redeclaration conflict**
- **Found during:** Task 2 (consumer build verification)
- **Issue:** EventHandler type was already declared in pkg/ebpf/tls/github.go (from a later plan executed previously). Redeclaring in consumer.go caused compilation failure.
- **Fix:** Removed duplicate EventHandler declaration from consumer.go, reusing the existing definition from github.go
- **Files modified:** pkg/ebpf/tls/consumer.go
- **Verification:** go build and go vet pass for linux/amd64 and linux/arm64
- **Committed in:** b8163ea (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** Necessary fix for compilation with existing code. No scope creep.

## Issues Encountered
- Tests cannot run natively on macOS due to `//go:build linux` tags. Verified via cross-compilation (GOOS=linux go build/vet) instead.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- OpenSSL probe ready for integration into TLS observer (plan 41-03)
- Consumer ready to receive handlers for HTTP parsing, GitHub audit, Bedrock metering
- Discovery scanner ready to find system libssl.so.3 on AL2023 EC2 instances

## Self-Check: PASSED

All 6 created files verified present. Both task commits (ac4c495, b8163ea) verified in git log.

---
*Phase: 41-ebpf-ssl-uprobe-observability*
*Completed: 2026-04-01*
