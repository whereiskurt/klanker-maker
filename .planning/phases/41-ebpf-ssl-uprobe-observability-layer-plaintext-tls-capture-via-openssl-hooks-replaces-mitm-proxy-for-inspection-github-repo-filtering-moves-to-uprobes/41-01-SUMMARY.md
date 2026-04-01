---
phase: 41-ebpf-ssl-uprobe-observability
plan: 01
subsystem: ebpf
tags: [bpf, uprobe, openssl, tls, bpf2go, ringbuf, kprobe]

requires:
  - phase: 40-ebpf-network-enforcement
    provides: "eBPF package patterns, bpf2go build pipeline, Docker-based generation"
provides:
  - "BPF C programs for OpenSSL uprobe TLS plaintext capture (ssl_common.h, openssl.bpf.c)"
  - "Connection correlation kprobes for fd-to-endpoint mapping (connect.bpf.c)"
  - "Ring buffer event struct (ssl_event) and Go types (TLSEvent)"
  - "Generated bpf2go loader code committed (opensslbpf, connectbpf)"
  - "arm64 stub for Lambda compilation compatibility"
affects: [41-02, 41-03, 41-04, 41-05, tls-observer, github-filtering]

tech-stack:
  added: []
  patterns:
    - "uprobe BPF programs with PT_REGS macros for function arg capture"
    - "Ring buffer for high-throughput TLS event streaming"
    - "Connection correlation via kprobe on __sys_connect"
    - "ssl_read entry/return probe pair for read-after-return capture"

key-files:
  created:
    - pkg/ebpf/tls/bpf/ssl_common.h
    - pkg/ebpf/tls/bpf/openssl.bpf.c
    - pkg/ebpf/tls/bpf/connect.bpf.c
    - pkg/ebpf/tls/gen.go
    - pkg/ebpf/tls/types.go
    - pkg/ebpf/tls/types_stub.go
    - pkg/ebpf/tls/opensslbpf_x86_bpfel.go
    - pkg/ebpf/tls/opensslbpf_x86_bpfel.o
    - pkg/ebpf/tls/connectbpf_x86_bpfel.go
    - pkg/ebpf/tls/connectbpf_x86_bpfel.o
  modified:
    - Dockerfile.ebpf-generate
    - Makefile

key-decisions:
  - "Defined x86_64 pt_regs struct inline in ssl_common.h rather than using vmlinux.h — system ptrace.h conflicted with bpf_tracing.h forward declarations"
  - "Used 16MB ring buffer for TLS events — matches TLS max fragment size of 16KB with room for concurrent connections"
  - "SSL_read_ex return probe reads MAX_PAYLOAD_LEN since actual bytes-read count is in an out-param not accessible from uretprobe"

patterns-established:
  - "uprobe BPF programs: define pt_regs before bpf includes for PT_REGS macro compatibility"
  - "ssl_read entry/return split: stash buf pointer in ssl_read_args_map, read payload on return"
  - "Connection correlation: kprobe on __sys_connect populates conn_map[pid+fd] -> remote endpoint"

requirements-completed: [EBPF-TLS-01, EBPF-TLS-08, EBPF-TLS-07]

duration: 5min
completed: 2026-04-01
---

# Phase 41 Plan 01: BPF Programs and Go Types for TLS Uprobe Observability Summary

**OpenSSL uprobe BPF programs with ring buffer events, connection correlation kprobes, and Go type foundations compiled via bpf2go**

## Performance

- **Duration:** 5 min
- **Started:** 2026-04-01T20:51:00Z
- **Completed:** 2026-04-01T20:56:00Z
- **Tasks:** 2
- **Files modified:** 12

## Accomplishments
- BPF C programs for SSL_write/SSL_read uprobe capture with 6 SEC programs (write, read entry/return, write_ex, read_ex entry/return)
- Connection correlation kprobe on __sys_connect for fd-to-endpoint mapping via conn_map
- Ring buffer event struct (ssl_event) with 16KB payload, timestamp, pid/tid/fd, remote endpoint, direction, library type
- Generated bpf2go Go loader code and compiled BPF objects committed for both openssl and connect programs
- Go TLSEvent type with PayloadBytes(), RemoteAddr(), LibraryName() helper methods
- arm64 stub for Lambda compatibility, Makefile/Dockerfile extended for tls subpackage

## Task Commits

Each task was committed atomically:

1. **Task 1: BPF C programs and shared headers** - `ecadb0a` (feat)
2. **Task 2: Go types, bpf2go gen, arm64 stubs, build integration** - `6831ceb` (feat)

## Files Created/Modified
- `pkg/ebpf/tls/bpf/ssl_common.h` - Shared BPF event struct, maps, constants, pt_regs definition
- `pkg/ebpf/tls/bpf/openssl.bpf.c` - OpenSSL uprobe programs (6 SEC programs)
- `pkg/ebpf/tls/bpf/connect.bpf.c` - Connect/accept kprobes for connection correlation
- `pkg/ebpf/tls/gen.go` - bpf2go generate directives for openssl and connect programs
- `pkg/ebpf/tls/types.go` - Go TLSEvent struct and helper methods
- `pkg/ebpf/tls/types_stub.go` - arm64 no-op stub
- `pkg/ebpf/tls/opensslbpf_x86_bpfel.{go,o}` - Generated OpenSSL BPF loader + object
- `pkg/ebpf/tls/connectbpf_x86_bpfel.{go,o}` - Generated connect BPF loader + object
- `Dockerfile.ebpf-generate` - Extended to generate tls subpackage
- `Makefile` - Updated generate-ebpf target for tls subpackage

## Decisions Made
- Defined x86_64 pt_regs struct inline in ssl_common.h because system linux/ptrace.h conflicted with bpf_tracing.h forward declarations. The BPF tracing macros expect register names like rdi/rsi/rdx which are the kernel struct field names.
- Used 16MB ring buffer for TLS events to handle concurrent SSL connections without drops.
- SSL_read_ex return probe reads MAX_PAYLOAD_LEN since the actual byte count is in an out-param not accessible from uretprobe context.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed pt_regs struct definition for BPF compilation**
- **Found during:** Task 2 (bpf2go generation)
- **Issue:** BPF programs failed to compile because bpf_tracing.h PT_REGS macros need full struct pt_regs definition with kernel register names (rdi, rsi, rdx, rax, etc). System ptrace.h conflicted with bpf_helper_defs.h forward declaration.
- **Fix:** Defined x86_64 struct pt_regs inline in ssl_common.h with correct kernel register field names, before bpf includes
- **Files modified:** pkg/ebpf/tls/bpf/ssl_common.h
- **Verification:** make generate-ebpf completed successfully
- **Committed in:** 6831ceb (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 bug)
**Impact on plan:** Essential fix for BPF compilation. No scope creep.

## Issues Encountered
- Initial BPF compilation failed with "incomplete definition of type struct pt_regs" — resolved by defining pt_regs inline with correct x86_64 register names before bpf includes.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- BPF programs compiled and Go types ready for the TLS observer (plan 41-02)
- Ring buffer map (tls_events) and connection map (conn_map) ready for userspace consumer
- Generated loader code provides LoadOpensslBpf() and LoadConnectBpf() for program attachment

## Self-Check: PASSED

All 10 created files verified present. Both task commits (ecadb0a, 6831ceb) verified in git log.

---
*Phase: 41-ebpf-ssl-uprobe-observability*
*Completed: 2026-04-01*
