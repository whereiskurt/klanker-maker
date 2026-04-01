---
phase: 40-ebpf-network-enforcement
verified: 2026-04-01T11:54:14Z
status: gaps_found
score: 11/12 requirements verified
gaps:
  - truth: "go generate in pkg/ebpf/ compiles BPF C programs via bpf2go and produces Go loader code embedded in km binary"
    status: failed
    reason: "bpf_bpfel.go and bpf_bpfel.o (the bpf2go-generated Go loader and compiled BPF bytecode) are not present in the repository. The plan listed them as files_modified and the Makefile comment says 'generated files are committed so make build works without clang', but they were never generated and committed. On Linux, go build ./... fails with 'undefined: bpfObjects' and 'undefined: loadBpf'. The same gap exists for pkg/ebpf/sni/sni_bpfel.go (sni package). On darwin/macOS the build passes only because all pkg/ebpf files have //go:build linux guards."
    artifacts:
      - path: "pkg/ebpf/bpf_bpfel.go"
        issue: "File does not exist — bpf2go go generate was never run. enforcer.go references bpfObjects and loadBpf() which are only defined in this generated file."
      - path: "pkg/ebpf/bpf_bpfel.o"
        issue: "Compiled BPF bytecode not present — would be embedded by bpf2go into bpf_bpfel.go."
      - path: "pkg/ebpf/sni/sni_bpfel.go"
        issue: "sni package generated loader not present — sni.go references sniObjects and loadSniObjects() which require generation."
    missing:
      - "Run 'go generate ./pkg/ebpf/...' on a Linux host with clang to produce bpf_bpfel.go, bpf_bpfel.o"
      - "Run 'go generate ./pkg/ebpf/sni/...' to produce sni_bpfel.go"
      - "Commit the generated files so 'make build' works without clang on CI and EC2 instances"
      - "Wire generate-ebpf as a prerequisite of the build target in Makefile (or document that it must be run first)"
human_verification:
  - test: "On an EC2 AL2023 instance with kernel 6.1, run 'km ebpf-attach --sandbox-id test-01 --firewall-mode log' and verify BPF programs attach to cgroup without error"
    expected: "BPF programs load from embedded bytecode, attach to /sys/fs/cgroup/km.slice/km-test-01.scope, pin to /sys/fs/bpf/km/test-01/, DNS resolver starts on :5353"
    why_human: "Requires actual Linux kernel with BPF support, a real cgroup hierarchy, and the km binary compiled with generated BPF bytecode"
  - test: "Inside a sandbox cgroup with eBPF enforcement active, run 'iptables -F -t nat' as root and then attempt 'curl https://8.8.8.8' (not in allowlist)"
    expected: "curl fails with EPERM / connection refused despite iptables flush — proves eBPF enforcement is independent of iptables"
    why_human: "EBPF-NET-12 root bypass verification — requires EC2 instance with attached BPF programs and a process moved into the sandbox cgroup"
  - test: "Verify BPF enforcement survives km process exit: attach programs, kill km ebpf-attach, then attempt connection from sandbox cgroup to blocked IP"
    expected: "EPERM persists after km process exits — bpffs pins keep programs alive"
    why_human: "Requires EC2 instance, actual BPF pin to bpffs, and verification that cgroup enforcement remains after process cleanup"
---

# Phase 40: eBPF cgroup Network Enforcement Layer Verification Report

**Phase Goal:** Replace iptables DNAT + userspace proxy with eBPF cgroup programs for L3/L4 network enforcement. Sandboxed processes cannot bypass enforcement even with root. DNS queries intercepted and resolved by a userspace daemon that populates BPF IP allowlist maps. Profile schema gains `spec.network.enforcement` toggle (ebpf | proxy | both). Programs pinned to bpffs so enforcement survives CLI process exit.

**Verified:** 2026-04-01T11:54:14Z
**Status:** gaps_found
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | BPF C programs (connect4, sendmsg4, sockops, cgroup_skb/egress) are defined and correct | VERIFIED | `pkg/ebpf/bpf.c` (335 lines) has all four SEC() program sections and seven map definitions including LPM_TRIE with BPF_F_NO_PREALLOC and RINGBUF |
| 2 | bpf2go pipeline compiles BPF C and embeds bytecode in km binary | FAILED | `pkg/ebpf/bpf_bpfel.go` and `bpf_bpfel.o` are absent. go generate was never run. `GOOS=linux go build ./...` fails with `undefined: bpfObjects` |
| 3 | Enforcer loads, attaches BPF programs to sandbox cgroup, and pins to bpffs | PARTIAL | `pkg/ebpf/enforcer.go` (313 lines) is fully implemented with correct lifecycle; blocked only by absent generated types on Linux |
| 4 | DNS resolver daemon intercepts redirected queries, enforces allowlist, pushes IPs into BPF map | VERIFIED | `pkg/ebpf/resolver/resolver.go` (314 lines) uses miekg/dns, implements MapUpdater interface, AllowIP/MarkForProxy calls verified wired; allowlist tests pass |
| 5 | TC egress SNI classifier inspects TLS ClientHello and drops disallowed hostnames | VERIFIED | `pkg/ebpf/sni/sni.c` (421 lines) has SEC("classifier/sni_filter"), allowed_sni map, correct TC return codes; Go attachment via clsact netlink present in `sni.go` (213 lines) |
| 6 | Profile schema accepts spec.network.enforcement (proxy/ebpf/both) with proxy default | VERIFIED | `pkg/profile/types.go` has Enforcement field; JSON schema has enum; semantic validation in validate.go; all compiler enforcement tests pass |
| 7 | Compiler emits eBPF user-data for enforcement=ebpf or both, iptables for proxy or both | VERIFIED | `pkg/compiler/userdata.go` has conditional eBPF section; TestUserDataEnforcementDefault/Proxy/Ebpf/Both all pass |
| 8 | BPF programs and maps pin to /sys/fs/bpf/km/{id}/ and persist after km process exit | VERIFIED (code) | `pkg/ebpf/pin.go` (163 lines) has full PinPath/Cleanup/RecoverPinned/IsPinned/ListPinned; enforcer.go calls Pin() on all four links |
| 9 | km destroy calls ebpf.Cleanup(sandboxID) to unpin BPF artifacts | VERIFIED | `destroy.go` calls cleanupEBPF() at line 161; platform-safe via `destroy_ebpf_linux.go` (real cleanup) / `destroy_ebpf_other.go` (no-op) |
| 10 | km ebpf-attach CLI command orchestrates full eBPF setup | VERIFIED | `internal/app/cmd/ebpf_attach.go` (237 lines) wires NewEnforcer + NewResolver + NewConsumer; registered via root_ebpf_linux.go → registerEBPFCmds → root.go:83 |
| 11 | Ring buffer audit consumer reads deny events and emits structured JSON | VERIFIED | `pkg/ebpf/audit/audit.go` (120 lines) has Consumer, NewConsumer, Run() with binary.Read of bpfEvent; audit tests TestActionString/TestLayerString/TestUint32ToIP pass |
| 12 | Root-in-sandbox bypass scenarios are documented and verifiable as integration tests | VERIFIED | `pkg/ebpf/enforcer_integration_test.go` with `//go:build linux && integration` has TestRootCannotBypassEBPF, TestRootIPTablesFlushIrrelevant, TestRootDirectConnectBlocked, TestRootCannotDetachBPF, TestDNSBlockedDomain, TestHardcodedIPBlocked |

**Score:** 11/12 truths verified (EBPF-NET-01's bpf2go generation pipeline is the single gap)

---

## Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/ebpf/bpf.c` | All BPF programs and map definitions (min 200 lines) | VERIFIED | 335 lines, 4 SEC() sections, 7 maps |
| `pkg/ebpf/headers/common.h` | struct event, struct ip4_trie_key, volatile consts (min 30 lines) | VERIFIED | 83 lines, all structs and volatile consts present |
| `pkg/ebpf/gen.go` | go:generate bpf2go directive | VERIFIED | `//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags linux -type event bpf bpf.c` |
| `pkg/ebpf/types.go` | LpmKey struct, action/layer/mode constants | VERIFIED | LpmKey, ActionDeny/Allow/Redirect, Layer constants, Mode constants |
| `pkg/ebpf/bpf_bpfel.go` | bpf2go-generated Go loader code | MISSING | File does not exist — go generate was never run |
| `pkg/ebpf/bpf_bpfel.o` | Compiled BPF bytecode | MISSING | File does not exist |
| `pkg/ebpf/enforcer.go` | Enforcer, NewEnforcer, Config (min 150 lines) | VERIFIED | 313 lines, all exports present, volatile constant wiring via spec.Variables |
| `pkg/ebpf/cgroup.go` | CreateSandboxCgroup, CgroupPath (min 40 lines) | VERIFIED | 79 lines, both functions present |
| `pkg/ebpf/pin.go` | PinPath, Cleanup, RecoverPinned, IsPinned, ListPinned (min 60 lines) | VERIFIED | 163 lines, all 5 functions implemented |
| `pkg/ebpf/enforcer_test.go` | Unit tests for path computation, type encoding | VERIFIED | 14 test functions covering TestPinPath, TestCgroupPath, TestLpmKey, TestConfigDefaults, TestCleanup, TestIsPinned, mode/action/layer constants |
| `pkg/ebpf/resolver/resolver.go` | Resolver, ResolverConfig, MapUpdater interface (min 120 lines) | VERIFIED | 314 lines, MapUpdater wired to AllowIP/MarkForProxy at lines 257/269 |
| `pkg/ebpf/resolver/allowlist.go` | IsAllowed, Allowlist with TTL (min 40 lines) | VERIFIED | 128 lines, suffix matching, resolvedEntry TTL, Sweep() |
| `pkg/ebpf/resolver/resolver_test.go` | Allowlist matching tests (min 60 lines) | VERIFIED | Tests pass: `go test ./pkg/ebpf/resolver/ -run TestAllowlist` |
| `pkg/ebpf/sni/sni.c` | TC classifier BPF program (min 100 lines) | VERIFIED | 421 lines, SEC("classifier/sni_filter"), allowed_sni HASH map, SNI parsing |
| `pkg/ebpf/sni/sni.go` | SNIFilter, NewSNIFilter, AllowSNI (min 60 lines) | VERIFIED | 213 lines, clsact qdisc, netlink attachment, AllowSNI method |
| `pkg/ebpf/sni/gen.go` | go:generate bpf2go for sni package | VERIFIED | `//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags linux sni sni.c` |
| `pkg/ebpf/sni/sni_bpfel.go` | bpf2go-generated SNI loader | MISSING | File does not exist — sni.go references sniObjects/loadSniObjects, fails on Linux |
| `pkg/profile/types.go` | NetworkSpec with Enforcement field | VERIFIED | `Enforcement string yaml:"enforcement,omitempty"` present |
| `pkg/profile/schemas/sandbox_profile.schema.json` | enforcement enum (proxy/ebpf/both) | VERIFIED | enum with all three values, no required constraint |
| `pkg/compiler/userdata.go` | Conditional eBPF vs iptables generation | VERIFIED | Enforcement field, ebpf-attach invocation, km.slice cgroup creation |
| `pkg/ebpf/audit/audit.go` | Consumer, NewConsumer (min 60 lines) | VERIFIED | 120 lines, ringbuf.Reader, binary.Read of bpfEvent, zerolog structured output |
| `internal/app/cmd/ebpf_attach.go` | km ebpf-attach command (min 80 lines) | VERIFIED | 237 lines, NewEnforcer+NewResolver+NewConsumer orchestration, registered via root_ebpf_linux.go |
| `internal/app/cmd/destroy_ebpf_linux.go` | Linux-only cleanupEBPF with IsPinned+Cleanup | VERIFIED | IsPinned check, Cleanup call, debug/info/warn logging |
| `internal/app/cmd/destroy_ebpf_other.go` | Non-Linux no-op cleanupEBPF | VERIFIED | `//go:build !linux` no-op stub |
| `pkg/ebpf/enforcer_integration_test.go` | Root bypass test scenarios (min 40 lines) | VERIFIED | `//go:build linux && integration`, 6 test functions |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `pkg/ebpf/bpf.c` | `pkg/ebpf/bpf_bpfel.go` | bpf2go code generation | NOT WIRED | bpf_bpfel.go does not exist; go generate never executed |
| `pkg/ebpf/bpf.c` | `pkg/ebpf/headers/common.h` | `#include "common.h"` | WIRED | `#include "headers/common.h"` confirmed in bpf.c |
| `pkg/ebpf/enforcer.go` | `pkg/ebpf/bpf_bpfel.go` | `loadBpfObjects()` / `loadBpf()` | BROKEN | loadBpf() referenced at line 75, bpfObjects referenced at lines 39/101; both undefined without generated file |
| `pkg/ebpf/enforcer.go` | `pkg/ebpf/cgroup.go` | `CreateSandboxCgroup()` | WIRED | enforcer.go calls CreateSandboxCgroup(cfg.SandboxID) |
| `pkg/ebpf/enforcer.go` | `pkg/ebpf/pin.go` | `PinPath()` / `Cleanup()` | WIRED | enforcer.go references pinPath via PinPath(); .Pin() called on all 4 links at lines 156/164/172/180 |
| `pkg/ebpf/resolver/resolver.go` | `pkg/ebpf/enforcer.go` | `MapUpdater.AllowIP()` | WIRED | MapUpdater interface called at lines 257 (AllowIP) and 269 (MarkForProxy) |
| `pkg/ebpf/resolver/allowlist.go` | `sidecars/dns-proxy/dnsproxy/proxy.go` | Same suffix matching algorithm | WIRED (by logic) | IsAllowed implementation matches dnsproxy.IsAllowed semantics; same test cases verified |
| `pkg/ebpf/sni/sni.go` | `pkg/ebpf/sni/sni.c` | bpf2go code generation | NOT WIRED | sni_bpfel.go does not exist; sni.go references sniObjects/loadSniObjects() which are undefined |
| `pkg/profile/types.go` | `pkg/compiler/userdata.go` | `NetworkSpec.Enforcement` field | WIRED | userdata.go reads Enforcement field, emits conditional eBPF vs iptables sections |
| `pkg/compiler/userdata.go` | eBPF enforcer (EC2 user-data) | `km ebpf-attach` command | WIRED | Template emits `/usr/local/bin/km ebpf-attach` invocation in eBPF section |
| `pkg/ebpf/audit/audit.go` | `pkg/ebpf/enforcer.go` | `Events()` ring buffer map | WIRED | audit.go takes `*ebpf.Map` parameter (returned by Enforcer.Events()); ebpf_attach.go wires them at runtime |
| `internal/app/cmd/ebpf_attach.go` | `pkg/ebpf/enforcer.go` | `NewEnforcer()` | WIRED | `ebpf.NewEnforcer(cfg)` call present in ebpf_attach.go |
| `internal/app/cmd/ebpf_attach.go` | `pkg/ebpf/resolver/resolver.go` | `NewResolver()` | WIRED | `resolver.NewResolver(resolverCfg)` call present in ebpf_attach.go |
| `internal/app/cmd/destroy.go` | `pkg/ebpf/pin.go` | `IsPinned()` / `Cleanup()` | WIRED | cleanupEBPF(sandboxID) called at destroy.go:161; destroy_ebpf_linux.go calls IsPinned then Cleanup |

---

## Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|---------|
| EBPF-NET-01 | 40-01 | pkg/ebpf/ package scaffold with bpf2go pipeline — go generate compiles BPF C, embeds bytecode in km binary | BLOCKED | bpf.c, gen.go, and go:generate directive exist and are correct; bpf_bpfel.go and bpf_bpfel.o absent — go generate never run; Linux build fails |
| EBPF-NET-02 | 40-01 | BPF cgroup/connect4 intercepts connect(), filters IPs via LPM_TRIE, returns EPERM for disallowed | VERIFIED (C source) | bpf.c SEC("cgroup/connect4") with LPM_TRIE lookup, IMDS/localhost/proxy exemptions, block-mode EPERM |
| EBPF-NET-03 | 40-01 | cgroup/connect4 rewrites dest IP/port for L7 proxy redirect using socket cookie maps | VERIFIED (C source) | bpf.c has http_proxy_ips lookup, sock_to_original_ip/port population, IP/port rewrite |
| EBPF-NET-04 | 40-01 | cgroup/sendmsg4 intercepts UDP port 53 and redirects to km-dns-resolver | VERIFIED (C source) | bpf.c SEC("cgroup/sendmsg4") checks port 53, rewrites to localhost:const_dns_proxy_port |
| EBPF-NET-05 | 40-03 | Userspace km-dns-resolver daemon receives DNS queries, checks allowlist, pushes IPs to BPF LPM_TRIE | VERIFIED | resolver.go + allowlist.go fully implemented; MapUpdater.AllowIP wired; tests pass |
| EBPF-NET-06 | 40-01 | cgroup_skb/egress provides packet-level defense-in-depth, drops packets to IPs not in LPM_TRIE | VERIFIED (C source) | bpf.c SEC("cgroup_skb/egress") with LPM_TRIE lookup, IMDS/localhost exempt, drop on block mode |
| EBPF-NET-07 | 40-01 | BPF ring buffer emits structured deny events with timestamp, pid, IPs, port, action, layer | VERIFIED (C source) | bpf.c RINGBUF map, emit_event() helper with all required fields; audit.go deserializes with binary.Read |
| EBPF-NET-08 | 40-02 / 40-06 | All BPF programs and maps pinned to /sys/fs/bpf/km/{sandbox-id}/; destroy unpins; reattach on restart | VERIFIED (code) | enforcer.go pins 4 links; pin.go has RecoverPinned/Cleanup/IsPinned; destroy_ebpf_linux.go calls Cleanup |
| EBPF-NET-09 | 40-05 | Profile schema gains spec.network.enforcement (proxy/ebpf/both); km validate accepts all three | VERIFIED | types.go, JSON schema enum, validate.go semantic check; tests pass |
| EBPF-NET-10 | 40-04 | TC egress SNI classifier parses TLS ClientHello, validates against BPF hash map | VERIFIED (C source) | sni.c (421 lines) has full TLS parsing, allowed_sni map, TC_ACT_SHOT/TC_ACT_OK; sni.go attaches via clsact |
| EBPF-NET-11 | 40-05 | Compiler emits eBPF setup in EC2 user-data for enforcement=ebpf or both | VERIFIED | userdata.go template conditional sections; TestUserDataEnforcementEbpf/Both pass |
| EBPF-NET-12 | 40-06 | Root-in-sandbox bypass prevention verified — iptables flush irrelevant, CAP_NET_ADMIN cannot detach | VERIFIED (test scenarios) | enforcer_integration_test.go with `//go:build linux && integration` documents and tests all 5 scenarios |

---

## Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `pkg/ebpf/bpf_bpfel.go` | — | Missing generated file | Blocker | `GOOS=linux go build ./...` fails with `undefined: bpfObjects` — km binary cannot be built for Linux target without running go generate first |
| `pkg/ebpf/sni/sni_bpfel.go` | — | Missing generated file | Blocker | `GOOS=linux go build ./...` fails with `undefined: sniObjects` — same root cause as above |
| `Makefile` build target | — | build does not depend on generate-ebpf | Warning | Developers and CI can run `make build` without knowing they need `make generate-ebpf` first on Linux; easy to miss |

No TODO/FIXME/placeholder comments found in any eBPF package files. No stub return patterns found in key files.

---

## Human Verification Required

### 1. BPF Program Attach on EC2

**Test:** On an EC2 AL2023 instance (kernel 6.1+), after building with `make generate-ebpf && make build`, run `km ebpf-attach --sandbox-id sb-test01 --firewall-mode log` as root.
**Expected:** BPF programs load and attach without error. `/sys/fs/bpf/km/sb-test01/` contains connect4_link, sendmsg4_link, sockops_link, egress_link and allowed_cidrs map files. DNS resolver starts on 127.0.0.1:5353.
**Why human:** Requires real Linux kernel with BPF support, actual cgroup hierarchy, and compiled BPF bytecode (bpf_bpfel.go/o must be generated first).

### 2. Root Bypass Verification (EBPF-NET-12)

**Test:** Inside sandbox cgroup with eBPF enforcement in block mode, as root: (a) run `iptables -F -t nat`, then `curl -v https://8.8.8.8`; (b) run `bpftool prog detach` on the cgroup/connect4 program.
**Expected:** (a) curl fails with EPERM or connection refused — iptables flush has no effect on eBPF enforcement. (b) bpftool detach fails with EPERM — no CAP_BPF in host namespace available to sandbox root.
**Why human:** Requires EC2 instance with attached BPF programs, a process actually moved into the sandbox cgroup, and verifying kernel-level syscall rejection.

### 3. Enforcement Persistence After Process Exit

**Test:** Start `km ebpf-attach`, then kill it with SIGTERM. After process exits, from a process in the sandbox cgroup, attempt to connect to a blocked IP.
**Expected:** EPERM persists — bpffs pins keep programs active. Programs remain in `bpftool cgroup show /sys/fs/cgroup/km.slice/km-{id}.scope`.
**Why human:** Requires EC2 with real bpffs, actual process lifecycle, and kernel-level verification that programs survive process exit.

---

## Gaps Summary

**One gap blocks full goal achievement on Linux:** The bpf2go code generation step was not executed, so `bpf_bpfel.go` (Go loader code) and `bpf_bpfel.o` (compiled BPF bytecode) are absent from the repository. The same issue affects `pkg/ebpf/sni/` where `sni_bpfel.go` is missing.

The plan explicitly stated these generated files "are committed to the repo so that `make build` works without clang on CI/target machines" — this was not done. On darwin (the development host), the build succeeds because all eBPF package files carry `//go:build linux` guards. On Linux (the target platform for deployment), `go build ./...` fails with `undefined: bpfObjects` and `undefined: loadBpf`.

**Root cause:** `go generate` requires `clang` with Linux BPF target support installed. The plan explicitly prohibited running it during execution ("Do NOT run `go generate` during this plan execution — it requires clang and a Linux-compatible BPF toolchain"). The intent was to run it separately and commit the output, but this final step was not completed.

**Everything else is correctly implemented:** All BPF C programs (connect4, sendmsg4, sockops, cgroup_skb/egress), the Go enforcer lifecycle, DNS resolver daemon, TC SNI classifier, profile schema enforcement field, compiler conditional user-data generation, ring buffer audit consumer, km ebpf-attach command, destroy cleanup wiring, and root bypass integration test scenarios are all substantively implemented and wired correctly. Resolver allowlist tests, audit helper tests, profile validation tests, and compiler enforcement tests all pass.

**Fix required:** On a Linux host with clang installed, run `make generate-ebpf` (which runs `go generate ./pkg/ebpf/` and `go generate ./pkg/ebpf/sni/`), then commit `bpf_bpfel.go`, `bpf_bpfel.o`, `sni_bpfel.go`, and `sni_bpfel.o` to the repository.

**Pre-existing issue (not introduced by phase 40):** `TestUnlockCmd_RequiresStateBucket` in `internal/app/cmd/unlock_test.go` fails — introduced by phase 39's DynamoDB switchover (commit 90efc1a modified unlock.go). Phase 40 did not touch this file.

---

_Verified: 2026-04-01T11:54:14Z_
_Verifier: Claude (gsd-verifier)_
