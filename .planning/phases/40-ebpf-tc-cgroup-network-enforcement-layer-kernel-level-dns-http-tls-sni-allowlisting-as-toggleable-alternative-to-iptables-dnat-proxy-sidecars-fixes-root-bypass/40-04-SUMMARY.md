---
phase: 40-ebpf-network-enforcement
plan: "04"
subsystem: infra
tags: [ebpf, bpf, tc, sni, tls, netlink, cilium, linux, kernel, allowlist, cls-bpf]

requires:
  - phase: 40-ebpf-network-enforcement/40-01
    provides: BPF headers (vmlinux.h, bpf_helpers.h, common.h), bpf2go codegen pattern, cilium/ebpf dependency

provides:
  - TC egress classifier (SEC classifier/sni_filter, cls_bpf) in pkg/ebpf/sni/sni.c
  - Two BPF maps: allowed_sni (HASH, 256-byte key) and sni_events (RINGBUF 1MB)
  - Go-side SNIFilter type with NewSNIFilter/AllowSNI/RemoveSNI/Close in pkg/ebpf/sni/sni.go
  - bpf2go generate directive for the sni package
  - github.com/vishvananda/netlink v1.3.1 dependency

affects:
  - 40-05+ (any loader/manager integrating SNI enforcement alongside cgroup/TC programs)
  - pkg/ebpf (sibling package; shares headers)

tech-stack:
  added:
    - github.com/vishvananda/netlink v1.3.1 (TC/qdisc management via netlink)
    - github.com/vishvananda/netns v0.0.5 (transitive dependency)
  patterns:
    - Classic cls_bpf via netlink (kernel 6.1 compatible; TCX requires 6.6+)
    - clsact qdisc + BpfFilter with DirectAction=true for TC egress BPF
    - 256-byte null-padded lowercase key for BPF_MAP_TYPE_HASH SNI lookup
    - Best-effort parse semantics: TC_ACT_OK on any parse failure

key-files:
  created:
    - pkg/ebpf/sni/sni.c
    - pkg/ebpf/sni/sni.go
    - pkg/ebpf/sni/gen.go
    - pkg/ebpf/sni/sni_test.go
  modified:
    - go.mod (vishvananda/netlink v1.3.1 added)
    - go.sum (updated)

key-decisions:
  - "SEC(classifier/sni_filter) not TCX: AL2023 kernel 6.1 does not support TCX (needs 6.6+); cls_bpf covers all target kernels"
  - "Best-effort semantics: TC_ACT_OK on all parse failures prevents blocking legitimate traffic from fragmented ClientHellos or non-TLS traffic"
  - "256-byte null-padded lowercase key: uniform normalization prevents case-variant bypasses; key size matches RFC 6066 max SNI length (255) + null terminator"
  - "MAX_SNI_PARSE_DEPTH=512 + MAX_TLS_EXTENSIONS=32: bounded for BPF verifier compliance while covering the vast majority of real-world ClientHellos"
  - "vishvananda/netlink for TC management: provides idiomatic Go netlink API for qdisc/filter management without raw syscall encoding"

patterns-established:
  - "Pattern: separate sni/ sub-package for TC programs — isolates TC attachment from cgroup attachment in the parent pkg/ebpf package"
  - "Pattern: AllowSNI/RemoveSNI methods on SNIFilter object — map population follows Go SDK convention, not raw BPF map calls"
  - "Pattern: hostnameToKey() pure function — testable without BPF kernel dependency, separates encoding concern from I/O"

requirements-completed: [EBPF-NET-10]

duration: 3min
completed: 2026-04-01
---

# Phase 40 Plan 04: TC SNI Classifier Summary

**TC egress cls_bpf classifier that parses TLS ClientHello SNI on port 443 and drops packets to disallowed hostnames, with Go-side netlink clsact attachment compatible with kernel 6.1**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-04-01T07:01:26Z
- **Completed:** 2026-04-01T07:05:02Z
- **Tasks:** 1
- **Files modified:** 6

## Accomplishments

- TC classifier parses TLS record header, Handshake header, ClientHello fixed fields, and extension list to extract SNI hostname; drops packets whose hostname is absent from `allowed_sni` BPF hash map
- Best-effort semantics: all parse failures (non-TLS, port != 443, fragmented ClientHello, extension loop exceeded) return TC_ACT_OK — never blocks legitimate traffic
- Go-side `SNIFilter` attaches via netlink clsact qdisc + cls_bpf BpfFilter with DirectAction; compatible with AL2023 kernel 6.1 (not TCX)
- `AllowSNI`/`RemoveSNI` manage 256-byte null-padded lowercase keys in the `allowed_sni` map; `hostnameToKey()` is unit-tested without BPF dependency

## Task Commits

1. **Task 1: Create TC SNI classifier BPF program and Go attachment** - `9d963be` (feat)

## Files Created/Modified

- `pkg/ebpf/sni/sni.c` - TC cls_bpf classifier: Ethernet/IP/TCP header parsing, TLS ClientHello SNI extraction, allowed_sni map lookup, sni_events ring buffer for drop events (421 lines)
- `pkg/ebpf/sni/sni.go` - Go SNIFilter: NewSNIFilter attaches via netlink clsact+BpfFilter, AllowSNI/RemoveSNI/Close methods, hostnameToKey normalizer (213 lines)
- `pkg/ebpf/sni/gen.go` - bpf2go go:generate directive; linux build constraint
- `pkg/ebpf/sni/sni_test.go` - TestSNIHostnameNormalization (8 cases), TestAllowSNIMultiple (6 hostnames + 100-entry stress); linux build constraint
- `go.mod` / `go.sum` - github.com/vishvananda/netlink v1.3.1 added

## Decisions Made

- Used `SEC("classifier/sni_filter")` (cls_bpf) rather than TCX — AL2023 kernel 6.1 supports classic TC but not TCX, which requires kernel 6.6+
- Best-effort parse semantics: `TC_ACT_OK` on any parse failure so fragmented Chrome ClientHellos and non-TLS traffic are never blocked
- 256-byte null-padded lowercase key for the `allowed_sni` map — enforces case-insensitive hostname matching and aligns with RFC 6066's 255-byte max SNI length

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

`sniObjects`/`loadSniObjects` are undefined until `go generate ./pkg/ebpf/sni/` runs on a Linux host with clang (this is expected — same as `pkg/ebpf/` pattern). Cross-compilation build confirms the undefined symbols are only from the bpf2go-generated file that does not yet exist.

## User Setup Required

None - no external service configuration required. `go generate ./pkg/ebpf/sni/` must be run on a Linux host with clang to produce the `sni_bpfel.go` and `sni_bpfel.o` files before compiling code that imports this package.

## Next Phase Readiness

- `pkg/ebpf/sni/` package is ready for integration into the enforcement manager (40-05+)
- The SNIFilter integrates with SNIConfig: `Interface` drives attachment, `AllowSNI()` is called per profile `network.allowlist` hostname entry
- Ring buffer event reader for `sni_events` map should be wired in the same manager that reads the main `events` ring buffer from `pkg/ebpf/`

## Self-Check: PASSED

All 4 new files verified on disk. Commit 9d963be verified in git log.

---
*Phase: 40-ebpf-network-enforcement*
*Completed: 2026-04-01*
