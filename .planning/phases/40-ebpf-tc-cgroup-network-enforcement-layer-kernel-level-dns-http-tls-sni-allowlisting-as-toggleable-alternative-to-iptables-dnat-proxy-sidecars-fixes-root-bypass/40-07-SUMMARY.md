---
phase: 40-ebpf-network-enforcement
plan: "07"
subsystem: ebpf
tags: [ebpf, bpf2go, code-generation, docker, gap-closure]
dependency_graph:
  requires: []
  provides: [pkg/ebpf/bpf_bpfel.go, pkg/ebpf/bpf_bpfel.o, pkg/ebpf/sni/sni_bpfel.go, pkg/ebpf/sni/sni_bpfel.o]
  affects: [pkg/ebpf, pkg/ebpf/sni, GOOS=linux build]
tech_stack:
  added: [Dockerfile.ebpf-generate, clang-14 via Docker, bpf2go@v0.21.0]
  patterns: [Docker bind-mount code generation, committed generated artifacts]
key_files:
  created:
    - Dockerfile.ebpf-generate
    - pkg/ebpf/bpf_bpfel.go
    - pkg/ebpf/bpf_bpfel.o
    - pkg/ebpf/bpf_bpfeb.go
    - pkg/ebpf/bpf_bpfeb.o
    - pkg/ebpf/sni/sni_bpfel.go
    - pkg/ebpf/sni/sni_bpfel.o
    - pkg/ebpf/sni/sni_bpfeb.go
    - pkg/ebpf/sni/sni_bpfeb.o
    - .gitattributes
  modified:
    - Makefile
    - pkg/ebpf/gen.go
    - pkg/ebpf/bpf.c
    - pkg/ebpf/enforcer.go
    - pkg/ebpf/headers/bpf_helpers.h
    - pkg/ebpf/sni/sni.c
decisions:
  - "Renamed BPF sockops() function to bpf_sockops() to resolve clang-14 symbol/section name collision (symbol and section both named 'sockops' caused duplicate-symbol linker error)"
  - "Removed -type event from bpf2go generate directive: BpfEvent type is not used by Go source (audit/audit.go defines its own bpfEvent mirror); clang-14 does not emit the event struct in BTF when used only via ringbuf reserve pointer"
  - "Big-endian variants (bpf_bpfeb.go, sni_bpfeb.go) committed alongside little-endian — bpf2go always generates both, and they are needed for completeness"
metrics:
  duration: "585s"
  completed_date: "2026-04-01"
  tasks_completed: 2
  files_changed: 16
---

# Phase 40 Plan 07: bpf2go Docker Code Generation Pipeline Summary

Docker-based `make generate-ebpf` pipeline using clang-14 inside golang:1.25-bookworm container to cross-compile BPF C programs from macOS, producing bpf2go-generated Go loader files (bpf_bpfel.go, sni_bpfel.go) committed to the repo so `GOOS=linux go build` works without clang installed.

## Tasks Completed

| Task | Name | Commit | Key Files |
|------|------|--------|-----------|
| 1 | Create Dockerfile and update Makefile generate-ebpf | 6435f32 | Dockerfile.ebpf-generate, Makefile |
| 2 | Run generate-ebpf and commit generated files | 90c58e4 | bpf_bpfel.go, bpf_bpfel.o, sni_bpfel.go, sni_bpfel.o, .gitattributes + BPF C fixes |

## Verification Results

1. All four primary generated files exist: bpf_bpfel.go, bpf_bpfel.o, sni_bpfel.go, sni_bpfel.o — PASS
2. `grep -c bpfObjects pkg/ebpf/bpf_bpfel.go` — returns 4 — PASS
3. `grep -c sniObjects pkg/ebpf/sni/sni_bpfel.go` — returns 4 — PASS
4. `GOOS=linux GOARCH=amd64 go build ./pkg/ebpf/...` — PASS
5. `GOOS=linux GOARCH=amd64 go build ./pkg/ebpf/sni/...` — PASS
6. `go build ./...` macOS build — PASS
7. `make build` km binary — PASS (v0.0.90)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Missing __uint/__type BTF map macros in custom bpf_helpers.h**
- **Found during:** Task 2 (first generation attempt)
- **Issue:** `bpf.c` uses BTF-typed map definitions (`__uint(type, ...)`, `__type(key, ...)`) but the project's custom `pkg/ebpf/headers/bpf_helpers.h` did not define these macros. The system libbpf provides them but the project uses a minimal custom header.
- **Fix:** Added `#define __uint(name, val) int (*name)[val]`, `#define __type(name, val) typeof(val) *name`, `#define __array(name, val) typeof(val) *name[]` matching the libbpf convention.
- **Files modified:** `pkg/ebpf/headers/bpf_helpers.h`
- **Commit:** 90c58e4

**2. [Rule 1 - Bug] sockops function name collision with BPF section name in clang-14**
- **Found during:** Task 2 (second generation attempt)
- **Issue:** clang-14 error "symbol 'sockops' is already defined / sockops changed binding to STB_GLOBAL". When a BPF function is named the same as its section (`SEC("sockops")` with function name `sockops`), the assembler creates a symbol-section name conflict. This is a clang-14 regression on ARM64 (Apple Silicon Docker) with the `-faddrsig` behavior.
- **Fix:** Renamed `int sockops(...)` to `int bpf_sockops(...)` in bpf.c and updated enforcer.go to reference `objs.BpfSockops` (the bpf2go-generated field name).
- **Files modified:** `pkg/ebpf/bpf.c`, `pkg/ebpf/enforcer.go`
- **Commit:** 90c58e4

**3. [Rule 1 - Bug] Removed -type event from bpf2go directive**
- **Found during:** Task 2 (third generation attempt)
- **Issue:** bpf2go error "collect C types: not found" — clang-14 does not emit the `event` struct into BTF when it is only accessed via a `void*` pointer from `bpf_ringbuf_reserve`. The `-type event` flag was aspirational; the `BpfEvent` Go type is not used anywhere in Go source (audit/audit.go defines its own `bpfEvent` mirror struct directly).
- **Fix:** Removed `-type event` from the `//go:generate` directive in `gen.go`.
- **Files modified:** `pkg/ebpf/gen.go`
- **Commit:** 90c58e4

**4. [Rule 1 - Bug] #pragma unroll on 255-iteration loop causes clang-14 BPF failure**
- **Found during:** Task 2 (fourth generation attempt, sni package)
- **Issue:** sni.c error: "loop not unrolled: the optimizer was unable to perform the requested transformation". clang-14 targeting BPF cannot unroll 255 iterations. Kernel 6.1 BPF verifier supports bounded loops without unrolling.
- **Fix:** Removed `#pragma unroll` from the 255-iteration lowercase conversion loop.
- **Files modified:** `pkg/ebpf/sni/sni.c`
- **Commit:** 90c58e4

**5. [Rule 1 - Bug] __builtin_memcpy with variable length not supported in BPF**
- **Found during:** Task 2 (fourth generation attempt, sni package)
- **Issue:** sni.c error: "A call to built-in function 'memcpy' is not supported." BPF programs cannot use variable-length `__builtin_memcpy`. The code was copying the hostname from an already-loaded `key[]` array into the ring buffer event.
- **Fix:** Replaced `__builtin_memcpy(ev->hostname, key, ...)` with a bounded for-loop copy.
- **Files modified:** `pkg/ebpf/sni/sni.c`
- **Commit:** 90c58e4

## Self-Check: PASSED

All files exist. Both commits verified in git log.
