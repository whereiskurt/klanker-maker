---
phase: 40-ebpf-network-enforcement
plan: "01"
subsystem: infra
tags: [ebpf, bpf, cgroup, networking, cilium, linux, kernel, allowlist]

requires: []
provides:
  - BPF C programs (connect4, sendmsg4, sockops, cgroup_skb/egress) in pkg/ebpf/bpf.c
  - Seven BPF maps: events (RINGBUF), allowed_cidrs (LPM_TRIE), http_proxy_ips, sock_to_original_ip/port, src_port_to_sock, socket_pid_map
  - Shared BPF headers: common.h (event struct, ip4_trie_key, volatile consts), bpf_helpers.h, vmlinux.h
  - bpf2go generate directive in gen.go for Linux-only code generation
  - Go types in types.go (LpmKey, action/layer/mode constants)
  - Makefile generate-ebpf target
  - github.com/cilium/ebpf v0.21.0 dependency
affects:
  - 40-02 (loader package will use generated bpf_bpfel.go and these types)
  - 40-03+ (all phases consuming the ebpf package)

tech-stack:
  added:
    - github.com/cilium/ebpf v0.21.0
    - clang BPF toolchain (build-time only, not runtime)
  patterns:
    - Single-file BPF C compilation via bpf2go (no split header/implementation)
    - Volatile const pattern for runtime configuration without map overhead
    - LPM_TRIE with BPF_F_NO_PREALLOC for CIDR allowlist (mandatory flag)
    - Socket cookie as cross-layer correlation key (connect4 -> sockops -> proxy)
    - Defense-in-depth: connect4 (socket-level) + cgroup_skb/egress (packet-level)

key-files:
  created:
    - pkg/ebpf/bpf.c
    - pkg/ebpf/headers/common.h
    - pkg/ebpf/headers/bpf_helpers.h
    - pkg/ebpf/headers/vmlinux.h
    - pkg/ebpf/gen.go
    - pkg/ebpf/types.go
  modified:
    - Makefile (generate-ebpf target added)
    - go.mod / go.sum (cilium/ebpf dependency)

key-decisions:
  - "Single bpf.c file pattern (not split): bpf2go compiles one C file; splitting requires multiple go:generate directives and adds complexity"
  - "Minimal vmlinux.h instead of full BTF-generated one: avoids 200KB file, sufficient for the three struct types we reference (bpf_sock_addr, __sk_buff, bpf_sock_ops)"
  - "Volatile const pattern for runtime config: allows CollectionSpec.RewriteConstants() to inject values at load time without map overhead or verifier complexity"
  - "BPF_F_NO_PREALLOC is mandatory for LPM_TRIE: omitting it causes ENOMEM at load time"
  - "IMDS (169.254.169.254) and localhost (127.0.0.0/8) hard-coded exemptions in both connect4 and egress_filter to prevent sandbox metadata and loopback breakage"
  - "Proxy PID exemption via volatile const_proxy_pid to prevent infinite redirect loops when the sidecar proxy itself makes connections"

patterns-established:
  - "Pattern: LPM_TRIE key with prefixlen=32 for exact IP match (single /32 lookup covers both CIDR prefix and exact-host lookups in one map)"
  - "Pattern: emit_event() always called before returning 0 (deny) so the ring buffer event precedes the EPERM"
  - "Pattern: socket cookie as map key for cross-layer state (connect4 stores IP/port, sockops stores port->cookie, proxy reads both)"

requirements-completed: [EBPF-NET-01, EBPF-NET-02, EBPF-NET-03, EBPF-NET-04, EBPF-NET-06, EBPF-NET-07]

duration: 4min
completed: 2026-03-31
---

# Phase 40 Plan 01: eBPF Package Scaffold Summary

**BPF C programs (connect4 CIDR enforcement + proxy redirect, sendmsg4 DNS intercept, sockops port mapping, cgroup_skb/egress packet-level drop) with seven maps, volatile runtime config, and bpf2go codegen pipeline**

## Performance

- **Duration:** ~4 min
- **Started:** 2026-04-01T06:54:57Z
- **Completed:** 2026-04-01T06:59:00Z
- **Tasks:** 2
- **Files modified:** 8

## Accomplishments

- Four BPF programs covering full network enforcement surface: TCP connect interception, UDP/DNS interception, transparent proxy socket tracking, and packet-level egress enforcement
- Seven BPF maps with correct types and flags (LPM_TRIE with mandatory BPF_F_NO_PREALLOC, RINGBUF for events, HASH for all cookie/IP lookups)
- Volatile const pattern enables runtime configuration injection via cilium/ebpf CollectionSpec.RewriteConstants() without map overhead
- IMDS + localhost exemptions in both connect4 and egress_filter prevent platform breakage
- bpf2go pipeline configured with -type event for type-safe ring buffer deserialization in Go

## Task Commits

1. **Task 1: Create BPF C programs, headers, and map definitions** - `10c9422` (feat)
2. **Task 2: Create bpf2go generate directive, Go types, and Makefile integration** - `c3df8c1` (feat)

## Files Created/Modified

- `pkg/ebpf/bpf.c` - All BPF programs and map definitions (~280 lines; connect4, sendmsg4, sockops, egress_filter)
- `pkg/ebpf/headers/common.h` - struct event, struct ip4_trie_key, volatile const declarations, helper macros
- `pkg/ebpf/headers/bpf_helpers.h` - Minimal BPF helper function prototypes (no libbpf dependency)
- `pkg/ebpf/headers/vmlinux.h` - Minimal kernel type subset (bpf_sock_addr, __sk_buff, bpf_sock_ops, iphdr)
- `pkg/ebpf/gen.go` - bpf2go go:generate directive; linux build constraint
- `pkg/ebpf/types.go` - LpmKey, action/layer/mode constants; linux build constraint
- `Makefile` - generate-ebpf target added
- `go.mod` / `go.sum` - github.com/cilium/ebpf v0.21.0 added

## Decisions Made

- Single bpf.c file: bpf2go compiles one file; splitting requires multiple generate directives
- Minimal vmlinux.h: avoids 200KB generated file; we only need three kernel structs
- Volatile const pattern for runtime config rather than a configuration map
- BPF_F_NO_PREALLOC mandatory on LPM_TRIE (verifier rejects without it)
- Hard-coded IMDS and localhost exemptions to prevent platform breakage

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `pkg/ebpf/` scaffold is complete; ready for Plan 40-02 which implements the Go userspace loader (CollectionSpec load, RewriteConstants, cgroup attachment, ring buffer reader)
- `go generate ./pkg/ebpf/` must be run on a Linux host with clang to produce `bpf_bpfel.go` and `bpf_bpfel.o` before the loader package can be compiled

## Self-Check: PASSED

All 6 files verified present on disk. Both commits (10c9422, c3df8c1) verified in git log.

---
*Phase: 40-ebpf-network-enforcement*
*Completed: 2026-03-31*
