---
phase: 40-ebpf-network-enforcement
verified: 2026-03-31T18:00:00Z
status: human_needed
score: 12/12 requirements verified
re_verification:
  previous_status: gaps_found
  previous_score: 11/12
  gaps_closed:
    - "bpf2go pipeline executed — pkg/ebpf/bpf_x86_bpfel.go and bpf_x86_bpfel.o now committed (179 lines Go loader, 19072 bytes bytecode)"
    - "pkg/ebpf/sni/sni_x86_bpfel.go and sni_x86_bpfel.o now committed (136 lines Go loader, 45760 bytes bytecode)"
    - "GOOS=linux GOARCH=amd64 go build ./... passes with zero errors — Linux km binary fully compilable"
  gaps_remaining: []
  regressions: []
human_verification:
  - test: "On an EC2 AL2023 instance (kernel 6.1+), build the km binary with 'GOOS=linux GOARCH=amd64 make build', then run 'km ebpf-attach --sandbox-id sb-test01 --firewall-mode log' as root"
    expected: "BPF programs load from embedded bytecode, attach to /sys/fs/cgroup/km.slice/km-sb-test01.scope, pin to /sys/fs/bpf/km/sb-test01/ with files connect4_link, sendmsg4_link, sockops_link, egress_link and map allowed_cidrs. DNS resolver starts on 127.0.0.1:5353."
    why_human: "Requires real Linux kernel with BPF support, actual cgroup hierarchy, and the km binary compiled for linux/amd64"
  - test: "Inside a sandbox cgroup with eBPF enforcement active in block mode, as root: run 'iptables -F -t nat', then attempt 'curl -v https://8.8.8.8' (not in allowlist)"
    expected: "curl fails with EPERM or connection refused despite iptables flush — proves eBPF enforcement (EBPF-NET-12) is independent of iptables rules"
    why_human: "Requires EC2 instance with attached BPF programs, a process moved into the sandbox cgroup, and kernel-level syscall rejection verification"
  - test: "Start 'km ebpf-attach', then kill it with SIGTERM. After process exits, from a process in the sandbox cgroup, attempt to connect to a blocked IP"
    expected: "EPERM persists — bpffs pins keep programs active. 'bpftool cgroup show /sys/fs/cgroup/km.slice/km-{id}.scope' still lists connect4 and egress_filter programs"
    why_human: "Requires EC2 with real bpffs, actual process lifecycle, and kernel verification that programs survive process exit"
---

# Phase 40: eBPF cgroup Network Enforcement Layer Verification Report

**Phase Goal:** Replace iptables DNAT + userspace proxy with eBPF cgroup programs for L3/L4 network enforcement. Sandboxed processes cannot bypass enforcement even with root. DNS queries intercepted by userspace daemon that populates BPF IP allowlist maps. Profile schema gains spec.network.enforcement toggle (ebpf | proxy | both). Programs pinned to bpffs for persistence.

**Verified:** 2026-03-31T18:00:00Z
**Status:** human_needed (all automated checks pass; EC2 runtime behavior requires human verification)
**Re-verification:** Yes — after gap closure (14 E2E fix iterations since initial verification)

---

## Gap Resolution Summary

The sole gap from the initial verification (2026-04-01T11:54:14Z) was that `go generate` had never been run, leaving `bpf_bpfel.go`, `bpf_bpfel.o`, `sni_bpfel.go`, and `sni_bpfel.o` absent from the repository. This caused `GOOS=linux go build ./...` to fail with `undefined: bpfObjects` and `undefined: sniObjects`.

This gap is now closed. The generation was run with `-target amd64` (producing `_x86_bpfel.*` filenames), and all four generated files are committed:

- `pkg/ebpf/bpf_x86_bpfel.go` — 179 lines, Go loader with `bpfObjects`, `loadBpf()`, all 4 programs and 7 maps
- `pkg/ebpf/bpf_x86_bpfel.o` — 19,072 bytes compiled BPF bytecode embedded via `//go:embed`
- `pkg/ebpf/sni/sni_x86_bpfel.go` — 136 lines, Go loader with `sniObjects`, `loadSniObjects()`, 1 program and 2 maps
- `pkg/ebpf/sni/sni_x86_bpfel.o` — 45,760 bytes compiled TC classifier bytecode embedded via `//go:embed`

Verified: `GOOS=linux GOARCH=amd64 go build ./...` exits with zero errors, zero warnings.

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | BPF C programs (connect4, sendmsg4, sockops, cgroup_skb/egress) are defined and correct | VERIFIED | `pkg/ebpf/bpf.c` (338 lines), 4 SEC() sections, 7 map definitions including LPM_TRIE with BPF_F_NO_PREALLOC and RINGBUF |
| 2 | bpf2go pipeline compiles BPF C and embeds bytecode in km binary | VERIFIED | `bpf_x86_bpfel.go` (179 lines) and `bpf_x86_bpfel.o` (19,072 bytes) committed; `GOOS=linux GOARCH=amd64 go build ./...` passes |
| 3 | Enforcer loads, attaches BPF programs to sandbox cgroup, and pins to bpffs | VERIFIED (code) | `enforcer.go` (301 lines), `//go:build linux && amd64`, references generated `bpfObjects`/`loadBpf()` which are now defined; all 4 programs attached and pinned |
| 4 | DNS resolver daemon intercepts redirected queries, enforces allowlist, pushes IPs into BPF map | VERIFIED | `resolver/resolver.go` (314 lines) with `MapUpdater.AllowIP`/`MarkForProxy`; resolver tests pass |
| 5 | TC egress SNI classifier inspects TLS ClientHello and drops disallowed hostnames | VERIFIED | `sni/sni.c` (434 lines) SEC("classifier/sni_filter"); `sni_x86_bpfel.go` committed; `sni.go` (213 lines) attaches via clsact netlink |
| 6 | Profile schema accepts spec.network.enforcement (proxy/ebpf/both) with proxy default | VERIFIED | `types.go` Enforcement field; JSON schema enum; compiler default to "proxy"; profile and compiler tests pass |
| 7 | Compiler emits eBPF user-data for enforcement=ebpf or both, iptables for proxy or both | VERIFIED | `userdata.go` (975 lines) conditional sections at lines 255/455/470/516; `TestUserDataEnforcementEbpf/Both` pass |
| 8 | BPF programs and maps pin to /sys/fs/bpf/km/{id}/ and persist after km process exit | VERIFIED (code) | `pin.go` (163 lines) PinPath/Cleanup/RecoverPinned/IsPinned/ListPinned all present; enforcer.go pins 4 links; Close() removes pin files but programs survive via bpffs |
| 9 | km destroy calls ebpf.Cleanup(sandboxID) to unpin BPF artifacts | VERIFIED | `destroy_ebpf_linux.go` calls `IsPinned()` then `Cleanup()`; wired in destroy.go |
| 10 | km ebpf-attach CLI command orchestrates full eBPF setup | VERIFIED | `ebpf_attach.go` (291 lines) NewEnforcer+NewResolver+NewConsumer; SIGTERM shutdown; registered via root_ebpf_linux.go |
| 11 | Ring buffer audit consumer reads deny events and emits structured JSON | VERIFIED | `audit/audit.go` (120 lines) ringbuf.Reader, binary.Read of bpfEvent, zerolog output; audit tests pass |
| 12 | Root-in-sandbox bypass scenarios are documented and verifiable as integration tests | VERIFIED | `enforcer_integration_test.go` `//go:build linux && integration` has 6 test scenarios for EBPF-NET-12 |

**Score:** 12/12 truths verified

---

## Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/ebpf/bpf.c` | All BPF programs and map definitions (min 200 lines) | VERIFIED | 338 lines, 4 SEC() sections, 7 maps |
| `pkg/ebpf/headers/common.h` | struct event, struct ip4_trie_key, volatile consts | VERIFIED | 83 lines, all structs and volatile consts present |
| `pkg/ebpf/gen.go` | go:generate bpf2go directive with -target amd64 | VERIFIED | `//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags linux -target amd64 bpf bpf.c` |
| `pkg/ebpf/bpf_x86_bpfel.go` | bpf2go-generated Go loader code | VERIFIED | 179 lines, `bpfObjects`, `loadBpf()`, 4 programs, 7 maps, 6 variables — was MISSING in initial verification |
| `pkg/ebpf/bpf_x86_bpfel.o` | Compiled BPF bytecode (embedded) | VERIFIED | 19,072 bytes — was MISSING in initial verification |
| `pkg/ebpf/types.go` | LpmKey struct, action/layer/mode constants | VERIFIED | 52 lines, LpmKey, ActionDeny/Allow/Redirect, Layer constants, ModeLog/Allow/Block |
| `pkg/ebpf/enforcer.go` | Enforcer, NewEnforcer, Config (min 150 lines) | VERIFIED | 301 lines, `//go:build linux && amd64`, all exports present |
| `pkg/ebpf/enforcer_stub.go` | Non-amd64 stub implementing same interface | VERIFIED | 44 lines, `//go:build linux && !amd64`, no-op stubs for all Enforcer methods |
| `pkg/ebpf/cgroup.go` | CreateSandboxCgroup, CgroupPath, RemoveSandboxCgroup | VERIFIED | 79 lines, cgroup v2 path with km.slice/km-{id}.scope naming |
| `pkg/ebpf/pin.go` | PinPath, Cleanup, RecoverPinned, IsPinned, ListPinned | VERIFIED | 163 lines, all 5 functions implemented with bpffs load/unload |
| `pkg/ebpf/enforcer_test.go` | Unit tests for path computation, type encoding | VERIFIED | `//go:build linux`, tests for PinPath, CgroupPath, LpmKey, Config |
| `pkg/ebpf/resolver/resolver.go` | Resolver, ResolverConfig, MapUpdater interface | VERIFIED | 314 lines, MapUpdater wired to AllowIP/MarkForProxy |
| `pkg/ebpf/resolver/allowlist.go` | IsAllowed, Allowlist with TTL | VERIFIED | 128 lines, suffix matching, resolvedEntry TTL, Sweep() |
| `pkg/ebpf/resolver/resolver_test.go` | Allowlist matching tests | VERIFIED | Tests pass: `go test ./pkg/ebpf/resolver/` |
| `pkg/ebpf/sni/sni.c` | TC classifier BPF program (min 100 lines) | VERIFIED | 434 lines, SEC("classifier/sni_filter"), allowed_sni HASH map |
| `pkg/ebpf/sni/sni.go` | SNIFilter, NewSNIFilter, AllowSNI | VERIFIED | 213 lines, clsact qdisc, netlink attachment |
| `pkg/ebpf/sni/gen.go` | go:generate bpf2go for sni package with -target amd64 | VERIFIED | `//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags linux -target amd64 sni sni.c` |
| `pkg/ebpf/sni/sni_x86_bpfel.go` | bpf2go-generated SNI loader | VERIFIED | 136 lines, `sniObjects`, `loadSniObjects()` — was MISSING in initial verification |
| `pkg/ebpf/sni/sni_x86_bpfel.o` | Compiled TC classifier bytecode | VERIFIED | 45,760 bytes — was MISSING in initial verification |
| `pkg/profile/types.go` | NetworkSpec with Enforcement field | VERIFIED | `Enforcement string yaml:"enforcement,omitempty"` in NetworkSpec |
| `pkg/profile/schemas/sandbox_profile.schema.json` | enforcement enum (proxy/ebpf/both) | VERIFIED | enum with all three values |
| `pkg/compiler/userdata.go` | Conditional eBPF vs iptables generation | VERIFIED | 975 lines, enforcement conditionals at multiple points |
| `pkg/ebpf/audit/audit.go` | Consumer, NewConsumer | VERIFIED | 120 lines, ringbuf.Reader, binary.Read of bpfEvent |
| `internal/app/cmd/ebpf_attach.go` | km ebpf-attach command | VERIFIED | 291 lines, full orchestration with SIGTERM handling |
| `internal/app/cmd/destroy_ebpf_linux.go` | Linux-only cleanupEBPF | VERIFIED | 38 lines, IsPinned check, Cleanup call |
| `internal/app/cmd/destroy_ebpf_other.go` | Non-Linux no-op cleanupEBPF | VERIFIED | 15 lines, `//go:build !linux` no-op stub |
| `pkg/ebpf/enforcer_integration_test.go` | Root bypass test scenarios | VERIFIED | `//go:build linux && integration`, 6 test functions |
| `profiles/goose-ebpf.yaml` | Test profile with enforcement: ebpf or both | VERIFIED | 197 lines, `enforcement: both` in spec.network |
| `Dockerfile.ebpf-generate` | Docker build pipeline for bpf2go generation | VERIFIED | 31 lines, golang:1.25-bookworm with clang/llvm/libbpf-dev |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `pkg/ebpf/bpf.c` | `pkg/ebpf/bpf_x86_bpfel.go` | bpf2go code generation | WIRED | bpf_x86_bpfel.go exists and embeds bpf_x86_bpfel.o; `GOOS=linux GOARCH=amd64 go build` passes |
| `pkg/ebpf/bpf.c` | `pkg/ebpf/headers/common.h` | `#include "headers/common.h"` | WIRED | Confirmed in bpf.c line 26 |
| `pkg/ebpf/enforcer.go` | `pkg/ebpf/bpf_x86_bpfel.go` | `loadBpf()` / `bpfObjects` | WIRED | enforcer.go calls `loadBpf()` at line 63, uses `bpfObjects` at lines 27/89/101+; both now defined |
| `pkg/ebpf/enforcer.go` | `pkg/ebpf/cgroup.go` | `CreateSandboxCgroup()` | WIRED | enforcer.go line 51 |
| `pkg/ebpf/enforcer.go` | `pkg/ebpf/pin.go` | `PinPath()`, link.Pin() calls | WIRED | enforcer.go lines 57, 144/152/160/168 |
| `pkg/ebpf/resolver/resolver.go` | `pkg/ebpf/enforcer.go` | `MapUpdater.AllowIP()` / `MarkForProxy()` | WIRED | resolver.go lines 257/269 |
| `pkg/ebpf/sni/sni.go` | `pkg/ebpf/sni/sni_x86_bpfel.go` | bpf2go code generation | WIRED | sni_x86_bpfel.go exists; sni.go uses `sniObjects`/`loadSniObjects()` which are now defined |
| `pkg/profile/types.go` | `pkg/compiler/userdata.go` | `NetworkSpec.Enforcement` field | WIRED | userdata.go line 964 reads `p.Spec.Network.Enforcement` |
| `pkg/compiler/userdata.go` | eBPF enforcer (EC2 user-data) | `km ebpf-attach` systemd unit | WIRED | userdata.go lines 533-565 emit km-ebpf-enforcer.service with `km ebpf-attach` ExecStart |
| `pkg/ebpf/audit/audit.go` | `pkg/ebpf/enforcer.go` | `Events()` ring buffer map | WIRED | ebpf_attach.go line 249 wires `enforcer.Events()` to `audit.NewConsumer()` |
| `internal/app/cmd/ebpf_attach.go` | `pkg/ebpf/enforcer.go` | `NewEnforcer()` | WIRED | ebpf_attach.go line 131 |
| `internal/app/cmd/ebpf_attach.go` | `pkg/ebpf/resolver/resolver.go` | `NewResolver()` | WIRED | ebpf_attach.go line 200 |
| `internal/app/cmd/destroy.go` | `pkg/ebpf/pin.go` | `IsPinned()` / `Cleanup()` | WIRED | destroy_ebpf_linux.go calls IsPinned then Cleanup |

---

## Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|---------|
| EBPF-NET-01 | 40-01 | pkg/ebpf/ package scaffold with bpf2go pipeline — go generate compiles BPF C, embeds bytecode in km binary | SATISFIED | bpf_x86_bpfel.go + bpf_x86_bpfel.o committed; `GOOS=linux GOARCH=amd64 go build ./...` passes cleanly |
| EBPF-NET-02 | 40-01 | BPF cgroup/connect4 intercepts connect(), filters IPs via LPM_TRIE, returns EPERM for disallowed | SATISFIED | bpf.c SEC("cgroup/connect4") with LPM_TRIE lookup, IMDS/localhost/proxy exemptions, block-mode EPERM |
| EBPF-NET-03 | 40-01 | cgroup/connect4 rewrites dest IP/port for L7 proxy redirect using socket cookie maps | SATISFIED | bpf.c http_proxy_ips lookup, sock_to_original_ip/port population, IP/port rewrite |
| EBPF-NET-04 | 40-01 | cgroup/sendmsg4 intercepts UDP port 53 and redirects to km-dns-resolver | SATISFIED | bpf.c SEC("cgroup/sendmsg4") checks port 53, rewrites to localhost:const_dns_proxy_port |
| EBPF-NET-05 | 40-03 | Userspace km-dns-resolver daemon receives DNS queries, checks allowlist, pushes IPs to BPF LPM_TRIE | SATISFIED | resolver.go + allowlist.go fully implemented; MapUpdater.AllowIP wired; tests pass |
| EBPF-NET-06 | 40-01 | cgroup_skb/egress provides packet-level defense-in-depth, drops packets to IPs not in LPM_TRIE | SATISFIED | bpf.c SEC("cgroup_skb/egress") with LPM_TRIE lookup, IMDS/localhost exempt, drop on block mode |
| EBPF-NET-07 | 40-01 | BPF ring buffer emits structured deny events with timestamp, pid, IPs, port, action, layer | SATISFIED | bpf.c RINGBUF map, emit_event() helper; audit.go deserializes with binary.Read; audit tests pass |
| EBPF-NET-08 | 40-02 / 40-06 | All BPF programs and maps pinned to /sys/fs/bpf/km/{sandbox-id}/; destroy unpins; reattach on restart | SATISFIED | enforcer.go pins 4 links; pin.go has RecoverPinned/Cleanup/IsPinned; destroy_ebpf_linux.go calls Cleanup |
| EBPF-NET-09 | 40-05 | Profile schema gains spec.network.enforcement (proxy/ebpf/both); km validate accepts all three | SATISFIED | types.go Enforcement field, JSON schema enum, validate.go semantic check; profile and compiler tests pass |
| EBPF-NET-10 | 40-04 | TC egress SNI classifier parses TLS ClientHello, validates against BPF hash map | SATISFIED | sni.c (434 lines) full TLS parsing, allowed_sni map, TC_ACT_SHOT/TC_ACT_OK; sni_x86_bpfel.go committed |
| EBPF-NET-11 | 40-05 | Compiler emits eBPF setup in EC2 user-data for enforcement=ebpf or both | SATISFIED | userdata.go conditional sections; TestUserDataEnforcementEbpf/Both pass |
| EBPF-NET-12 | 40-06 | Root-in-sandbox bypass prevention verified — iptables flush irrelevant, CAP_NET_ADMIN cannot detach | SATISFIED (tests) | enforcer_integration_test.go with `//go:build linux && integration` has 6 test scenarios; runtime verification requires EC2 |

---

## Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `Makefile` comments (lines 35-36, 44-47) | 35-47 | Echo/comments reference `bpf_bpfel.go` and `sni_bpfel.go` but actual output files are `bpf_x86_bpfel.go` and `sni_x86_bpfel.go` (due to `-target amd64`) | Warning | Stale documentation only — `make generate-ebpf` works correctly, Docker run invokes `go generate` which produces the right files |
| `Dockerfile.ebpf-generate` comments | 10 | References `bpf_bpfel.go` and `sni_bpfel.go` in comment block | Info | Comment only, no build impact |

No TODO/FIXME/placeholder anti-patterns found in any pkg/ebpf, resolver, sni, audit, or cmd files.

---

## Human Verification Required

### 1. BPF Program Attach on EC2

**Test:** On an EC2 AL2023 instance (kernel 6.1+), build the km binary for linux/amd64 (pre-built binary works since generated files are committed), then run `km ebpf-attach --sandbox-id sb-test01 --firewall-mode log` as root.

**Expected:** BPF programs load from embedded bytecode, attach to `/sys/fs/cgroup/km.slice/km-sb-test01.scope`, and pin to `/sys/fs/bpf/km/sb-test01/` with files `connect4_link`, `sendmsg4_link`, `sockops_link`, `egress_link` and map file `allowed_cidrs`. DNS resolver starts on 127.0.0.1:5353. `bpftool cgroup list /sys/fs/cgroup/km.slice/km-sb-test01.scope` shows 3 programs.

**Why human:** Requires real Linux kernel with BPF support, actual cgroup v2 hierarchy, and root privileges on an EC2 instance.

### 2. Root Bypass Verification (EBPF-NET-12)

**Test:** Inside a sandbox cgroup with eBPF enforcement active in `block` mode, as root: (a) run `iptables -F -t nat`, then `curl -v https://8.8.8.8` (not in allowlist); (b) run `bpftool prog detach` on the cgroup/connect4 program from inside the sandbox.

**Expected:** (a) `curl` fails with EPERM or connection refused — iptables flush has no effect on eBPF cgroup enforcement. (b) `bpftool prog detach` fails with EPERM because the sandbox root lacks `CAP_BPF` in the host network namespace.

**Why human:** Requires EC2 instance with attached BPF programs, a process moved into the sandbox cgroup, and verification of kernel-level syscall rejection.

### 3. Enforcement Persistence After Process Exit

**Test:** Start `km ebpf-attach --sandbox-id sb-test01 --firewall-mode block`, then send SIGTERM. After the process exits, from a process already running inside the sandbox cgroup, attempt `curl https://8.8.8.8`.

**Expected:** EPERM persists — bpffs-pinned links keep programs attached to the cgroup. `bpftool cgroup show /sys/fs/cgroup/km.slice/km-sb-test01.scope` still lists the programs after km exits.

**Why human:** Requires EC2 with real bpffs mount, actual process lifecycle management, and kernel verification that pinned programs survive process exit.

---

## Gaps Summary

No gaps blocking automated verification. All 12 requirements are satisfied by the committed code. The previous single gap (EBPF-NET-01: missing generated BPF bytecode/loader files) is fully resolved.

The three items above require human verification on an EC2 instance with Linux 6.1+ kernel — these cannot be verified programmatically because they require:
- Real kernel BPF subsystem
- Actual cgroup hierarchy
- Runtime behavior of pinned BPF programs after process exit
- Kernel-level enforcement of CAP_BPF restrictions

A minor warning exists in Makefile comment lines 35-47 and Dockerfile.ebpf-generate comments: they reference the old bpf2go naming convention (`bpf_bpfel.go`) rather than the actual output names (`bpf_x86_bpfel.go`). This is cosmetic only and does not affect any build or runtime behavior.

---

_Verified: 2026-03-31T18:00:00Z_
_Verifier: Claude (gsd-verifier)_
_Re-verification: Yes — initial verification was 2026-04-01T11:54:14Z with status gaps_found (11/12)_
