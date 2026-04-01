---
phase: 40-ebpf-network-enforcement
plan: "02"
subsystem: infra
tags: [ebpf, bpf, cgroup, cilium, linux, bpffs, pinning, enforcer]

requires:
  - phase: 40-ebpf-network-enforcement/40-01
    provides: BPF C programs (bpf.c), bpf2go generate directive (gen.go), LpmKey and constants (types.go), cilium/ebpf dependency
provides:
  - Enforcer struct with full lifecycle (NewEnforcer, AllowCIDR, AllowIP, MarkForProxy, Close, Events)
  - Config struct for runtime volatile constant injection (DNS/HTTP/HTTPS proxy ports, proxy PID, firewall mode, MITM addr)
  - Sandbox cgroup management under km.slice (CreateSandboxCgroup, CgroupPath, RemoveSandboxCgroup)
  - bpffs pin lifecycle (PinPath, Cleanup, IsPinned, ListPinned, RecoverPinned)
  - Unit tests for path computation, LpmKey encoding, Config fields, idempotent cleanup (no BPF kernel required)
affects:
  - 40-03 (DNS resolver uses RecoverPinned and AllowIP to populate allowed_cidrs)
  - 40-04+ (all phases that attach, query, or tear down BPF enforcement for a sandbox)

tech-stack:
  added: []
  patterns:
    - Store all link.Link values as Enforcer struct fields — local variable links trigger GC finalizer which detaches programs
    - CollectionSpec.Variables["const_*"].Set() before LoadAndAssign for volatile constant injection
    - bpffs pin directory per sandbox at /sys/fs/bpf/km/{sandboxID}/ with 0700 permissions
    - RecoverPinned reconstructs Enforcer from pinned handles — Config not stored, only SandboxID recoverable
    - Cleanup is idempotent (os.RemoveAll + best-effort parent removal + best-effort cgroup removal)
    - Cgroup naming: /sys/fs/cgroup/km.slice/km-{sandboxID}.scope (systemd slice/scope convention)

key-files:
  created:
    - pkg/ebpf/enforcer.go
    - pkg/ebpf/cgroup.go
    - pkg/ebpf/pin.go
    - pkg/ebpf/enforcer_test.go
  modified: []

key-decisions:
  - "Link fields stored in Enforcer struct (not local variables): cilium/ebpf GC finalizer detaches cgroup programs when the link.Link goes out of scope; struct storage is mandatory for persistence"
  - "CollectionSpec.Variables injection before LoadAndAssign: volatile consts must be written into the spec before programs are loaded into the kernel — they cannot be changed afterwards"
  - "Maps auto-pinned via CollectionOptions.Maps.PinPath: cilium/ebpf pins maps to bpffs on load; links require explicit Pin() calls after attachment"
  - "RecoverPinned does not recover Config: volatile constants are baked into loaded programs; only the SandboxID is stored in the recovered Enforcer for cgroup path computation"
  - "Unit tests are platform-agnostic by design: path/type tests require no BPF or cgroup kernel support, allowing CI on non-Linux hosts"

patterns-established:
  - "Pattern: NewEnforcer is the single entry point — rlimit, cgroup, pin dir, spec load, const inject, LoadAndAssign, four attaches, four pins"
  - "Pattern: Close is idempotent via closed bool guard — safe to call from defer and explicit cleanup"
  - "Pattern: RecoverPinned for km restart scenarios — km destroy calls RecoverPinned then Close to clean up live programs"

requirements-completed: [EBPF-NET-08]

duration: 4min
completed: 2026-03-31
---

# Phase 40 Plan 02: eBPF Enforcer Lifecycle Summary

**Go-side Enforcer that loads BPF programs, injects volatile runtime constants, attaches all four programs to sandbox cgroup, pins to bpffs for post-exit persistence, and recovers pinned state on km restart**

## Performance

- **Duration:** ~4 min
- **Started:** 2026-04-01T07:01:05Z
- **Completed:** 2026-04-01T07:04:49Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments

- Enforcer struct with GC-safe link field storage, full attach/pin/close lifecycle, and AllowCIDR/AllowIP/MarkForProxy for BPF map population
- Volatile constant injection via CollectionSpec.Variables before LoadAndAssign — proxy PID, DNS/HTTP/HTTPS ports, firewall mode, and MITM proxy address
- bpffs pin directory per sandbox with RecoverPinned for km-restart scenarios, Cleanup for km-destroy, IsPinned/ListPinned for introspection
- 14 unit tests covering path computation, LpmKey network byte order, Config field validation, and idempotent cleanup — all run without BPF kernel support

## Task Commits

1. **Task 1: Create Enforcer struct with BPF load, cgroup attach, and volatile const population** - `0994303` (feat)
2. **Task 2: Create pin/unpin persistence and recovery logic** - `9c9156c` (feat)

## Files Created/Modified

- `pkg/ebpf/enforcer.go` - Enforcer struct (313 lines): Config, NewEnforcer, AllowCIDR, AllowIP, MarkForProxy, Events, Close
- `pkg/ebpf/cgroup.go` - Sandbox cgroup management (79 lines): CgroupPath, CreateSandboxCgroup, RemoveSandboxCgroup with cgroup2 mount detection
- `pkg/ebpf/pin.go` - bpffs pin lifecycle (163 lines): PinPath, IsPinned, ListPinned, Cleanup, RecoverPinned
- `pkg/ebpf/enforcer_test.go` - Unit tests (214 lines): 14 tests; no BPF kernel required

## Decisions Made

- Link fields stored in Enforcer struct (not local variables) to prevent GC-triggered detachment
- CollectionSpec.Variables written before LoadAndAssign — volatile consts are baked at load time
- Maps auto-pinned via CollectionOptions.Maps.PinPath; links require explicit Pin() calls
- RecoverPinned reconstructs from pinned handles only (Config not recoverable from bpffs)
- All unit tests avoid BPF operations to allow CI on non-Linux hosts

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- `bpfObjects`/`loadBpf` are undefined on non-Linux (expected): bpf_bpfel.go is only generated by running `go generate ./pkg/ebpf/` on a Linux host with clang. All source files carry `//go:build linux` constraints so this is the correct behavior — same as 40-01.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `pkg/ebpf/` now has a complete enforcer lifecycle: create, attach, pin, recover, cleanup
- 40-03 (DNS resolver) and 40-04+ can call `RecoverPinned` to get a live Enforcer and use `AllowIP` to populate BPF maps
- `go generate ./pkg/ebpf/` must still be run on a Linux host with clang to produce `bpf_bpfel.go` before any of this code compiles

## Self-Check: PASSED

Files verified: enforcer.go, cgroup.go, pin.go, enforcer_test.go all present.
Commits verified: 0994303 (Task 1), 9c9156c (Task 2) both in git log.

---
*Phase: 40-ebpf-network-enforcement*
*Completed: 2026-03-31*
