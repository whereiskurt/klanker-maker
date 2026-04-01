---
phase: 40-ebpf-network-enforcement
plan: "06"
subsystem: ebpf
tags: [ebpf, cilium, ringbuf, audit, cobra, bpf, cgroup, security]

# Dependency graph
requires:
  - phase: 40-ebpf-network-enforcement/40-02
    provides: Enforcer, NewEnforcer, Events() ring buffer map, pin.go Cleanup/IsPinned
  - phase: 40-ebpf-network-enforcement/40-03
    provides: resolver.NewResolver, ResolverConfig, MapUpdater interface
  - phase: 40-ebpf-network-enforcement/40-01
    provides: bpfEvent struct layout, action/layer constants, types.go

provides:
  - Ring buffer audit consumer (pkg/ebpf/audit) — reads BPF deny events, emits structured zerolog JSON
  - km ebpf-attach CLI command — orchestrates enforcer + resolver + audit for EC2 user-data bootstrap
  - destroy_ebpf_linux.go / destroy_ebpf_other.go — platform-safe eBPF cleanup in km destroy
  - enforcer_integration_test.go — root bypass verification tests for EBPF-NET-12 security guarantee

affects: [km-destroy, km-ebpf-attach, ec2-bootstrap, audit-pipeline, security-validation]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Platform-safe build tags: _linux.go + _other.go pairs for Linux-only subsystems"
    - "Ring buffer consumer pattern: ringbuf.NewReader + blocking Read() + context cancellation"
    - "Helper extraction: platform-independent helpers in non-tagged file for cross-platform tests"
    - "Internal command pattern: Hidden: true cobra commands for EC2 user-data bootstrap"

key-files:
  created:
    - pkg/ebpf/audit/audit.go
    - pkg/ebpf/audit/helpers.go
    - pkg/ebpf/audit/audit_test.go
    - internal/app/cmd/ebpf_attach.go
    - internal/app/cmd/root_ebpf_linux.go
    - internal/app/cmd/root_ebpf_other.go
    - internal/app/cmd/destroy_ebpf_linux.go
    - internal/app/cmd/destroy_ebpf_other.go
    - pkg/ebpf/enforcer_integration_test.go
  modified:
    - internal/app/cmd/destroy.go
    - internal/app/cmd/root.go

key-decisions:
  - "Extracted helpers (actionString, layerString, uint32ToIP) to helpers.go with no build tag so audit tests run on all platforms including macOS CI"
  - "Used registerEBPFCmds(root, cfg) + platform stubs pattern to add ebpf-attach to root command without build-tagging root.go itself"
  - "cleanupEBPF() in destroy is best-effort no-op on operator laptop (macOS); primary BPF cleanup for remote destroy is EC2 instance termination (bpffs is in-memory)"
  - "Integration tests tagged //go:build linux && integration to exclude from CI; tests serve dual purpose as documentation and verification"

patterns-established:
  - "Build tag pair pattern: internal/app/cmd/destroy_ebpf_linux.go + destroy_ebpf_other.go for Linux-only functionality that must be callable from platform-agnostic code"
  - "registerEBPFCmds indirection pattern: root_ebpf_linux.go + root_ebpf_other.go for conditionally registering Linux-only subcommands"

requirements-completed: [EBPF-NET-08, EBPF-NET-12]

# Metrics
duration: 16min
completed: 2026-04-01
---

# Phase 40 Plan 06: eBPF Audit Consumer, ebpf-attach CLI, and Root Bypass Tests Summary

**Ring buffer audit consumer emitting structured zerolog JSON, km ebpf-attach orchestrating full eBPF enforcement, destroy.go eBPF cleanup via platform-safe build tag stubs, and EBPF-NET-12 root bypass integration test documentation**

## Performance

- **Duration:** 16 min
- **Started:** 2026-04-01T11:29:10Z
- **Completed:** 2026-04-01T11:43:39Z
- **Tasks:** 3
- **Files modified:** 11

## Accomplishments

- Ring buffer consumer (pkg/ebpf/audit) reads bpfEvent structs from cilium ringbuf, converts to zerolog Warn/Debug JSON with sandbox_id, pid, src_ip, dst_ip, dst_port, action, layer, comm fields
- km ebpf-attach internal command orchestrates NewEnforcer + AllowCIDR (IMDS/VPC) + NewResolver goroutine + NewConsumer goroutine, blocks on SIGTERM/SIGINT for EC2 user-data lifecycle
- km destroy calls cleanupEBPF(sandboxID) early in teardown; no-op on macOS; on Linux checks IsPinned and calls ebpf.Cleanup() — non-fatal warning if cleanup fails
- Five root bypass test scenarios document EBPF-NET-12 security guarantee: iptables flush irrelevant, direct connect blocked, BPF detach requires CAP_BPF, DNS blocked domain, hardcoded IP blocked

## Task Commits

1. **Task 1: Ring buffer audit consumer and km ebpf-attach command** - `b66cf17` (feat)
2. **Task 2: Wire ebpf.Cleanup() into km destroy** - `6dbf653` (feat)
3. **Task 3: Root bypass verification integration tests** - `956d48b` (feat)

## Files Created/Modified

- `pkg/ebpf/audit/audit.go` - Linux-only Consumer struct with NewConsumer, Run, Close using cilium/ebpf ringbuf
- `pkg/ebpf/audit/helpers.go` - Platform-independent actionString, layerString, uint32ToIP, nullTermString helpers (testable on macOS)
- `pkg/ebpf/audit/audit_test.go` - TestActionString, TestLayerString, TestUint32ToIP, TestNullTermString — pass on all platforms
- `internal/app/cmd/ebpf_attach.go` - km ebpf-attach cobra command (Hidden:true), NewEBPFAttachCmd, runEbpfAttach
- `internal/app/cmd/root_ebpf_linux.go` - Linux: registerEBPFCmds adds ebpf-attach to root
- `internal/app/cmd/root_ebpf_other.go` - Non-Linux: registerEBPFCmds no-op stub
- `internal/app/cmd/destroy_ebpf_linux.go` - Linux: cleanupEBPF checks IsPinned, calls ebpf.Cleanup
- `internal/app/cmd/destroy_ebpf_other.go` - Non-Linux: cleanupEBPF no-op
- `internal/app/cmd/destroy.go` - Added cleanupEBPF call after lock check in runDestroy
- `internal/app/cmd/root.go` - Added registerEBPFCmds(root, cfg) call
- `pkg/ebpf/enforcer_integration_test.go` - 5 root bypass integration tests tagged linux && integration

## Decisions Made

- Helpers extracted to `helpers.go` (no build tag) so audit package tests run on macOS CI without a Linux kernel
- Used `registerEBPFCmds` indirection pattern to add Linux-only commands to root without build-tagging root.go
- cleanupEBPF in destroy is best-effort: primary cleanup for remote destroy is EC2 instance termination (bpffs is an in-memory filesystem that disappears with the instance)
- Integration tests serve dual purpose: verification on EC2 AND documentation of the security model

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Extracted audit helpers to platform-independent file**
- **Found during:** Task 1 (audit package tests)
- **Issue:** plan specified `audit_test.go` with no build tag but all constants and helpers were in `audit.go` with `//go:build linux`, causing test compilation failure on macOS
- **Fix:** Extracted actionString, layerString, uint32ToIP, nullTermString to `helpers.go` with no build tag; removed them from audit.go
- **Files modified:** pkg/ebpf/audit/helpers.go (created), pkg/ebpf/audit/audit.go (trimmed)
- **Verification:** `go test ./pkg/ebpf/audit/` passes on macOS
- **Committed in:** b66cf17 (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 missing critical — test compilability on non-Linux platforms)
**Impact on plan:** Fix was required for CI correctness. Resulted in a cleaner package structure (helpers.go separates platform-independent conversion logic from the Linux-only ringbuf consumer).

## Issues Encountered

None — all three tasks executed without blocking issues.

## Next Phase Readiness

- Complete eBPF enforcement layer: BPF programs (40-02), DNS resolver (40-03), SNI parser (40-04), tc/attach (40-05), audit consumer + CLI + destroy cleanup (40-06)
- km ebpf-attach is ready to be invoked from EC2 user-data bootstrap scripts
- Integration tests ready to run on EC2 (AL2023, kernel 6.1+) with `sudo go test -v -tags integration -run TestRoot ./pkg/ebpf/`

---
*Phase: 40-ebpf-network-enforcement*
*Completed: 2026-04-01*
