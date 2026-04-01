---
phase: 40-ebpf-network-enforcement
plan: "03"
subsystem: infra
tags: [ebpf, bpf, dns, resolver, allowlist, cilium, fqdn, networking]

requires:
  - 40-01 (BPF C programs: sendmsg4 DNS intercept map)
provides:
  - DNS resolver daemon (pkg/ebpf/resolver/resolver.go): intercepts BPF-redirected DNS, enforces allowlist, pushes IPs into BPF maps
  - Domain allowlist (pkg/ebpf/resolver/allowlist.go): suffix matching with TTL-based IP expiry
  - MapUpdater interface: decouples BPF map access for unit testing
affects:
  - 40-02 (Enforcer.AllowIP/MarkForProxy methods implement MapUpdater interface)
  - 40-04+ (resolver wired into sandbox lifecycle start/stop)

tech-stack:
  added: []
  patterns:
    - Cilium FQDN model: intercept -> allowlist check -> upstream forward -> BPF map populate
    - MapUpdater interface: enables resolver unit testing without loaded BPF program
    - TTL-based IP expiry with background sweep goroutine (30s default)
    - miekg/dns UDP+TCP dual-server binding (existing dependency, same as dns-proxy sidecar)
    - AAAA refused (NOERROR + empty answer) — IPv4-only BPF enforcement simplification
    - ProxyHosts suffix matching triggers MarkForProxy in addition to AllowIP

key-files:
  created:
    - pkg/ebpf/resolver/allowlist.go
    - pkg/ebpf/resolver/resolver.go
    - pkg/ebpf/resolver/resolver_test.go
  modified: []

decisions:
  - "MapUpdater interface instead of direct Enforcer dependency: allows resolver tests to run without BPF (no kernel requirement in CI)"
  - "AAAA refused with NOERROR+empty (not NXDOMAIN): resolver-side IPv6 block without confusing callers that the domain is nonexistent"
  - "TTL floor of 5 seconds: prevents overly-frequent BPF map churn from CDN records with zero TTL"
  - "isProxyHost uses same suffix algorithm as IsAllowed: consistent matching semantics, no extra dependency"
  - "Sweep returns evicted IPs: caller can revoke them from BPF map (future use by 40-04 enforcer wiring)"

metrics:
  duration: "~3 min"
  completed: "2026-04-01"
  tasks: 2
  files: 3
---

# Phase 40 Plan 03: DNS Resolver Daemon Summary

**Userspace DNS resolver daemon (pkg/ebpf/resolver) bridges BPF sendmsg4 DNS interception to domain allowlist enforcement: checks queries, resolves allowed domains via upstream, pushes IPs into BPF LPM_TRIE via MapUpdater interface, with TTL-based expiry and ProxyHosts marking**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-04-01T07:01:03Z
- **Completed:** 2026-04-01T07:03:53Z
- **Tasks:** 2
- **Files:** 3 created

## Accomplishments

- `pkg/ebpf/resolver/allowlist.go`: Allowlist with suffix-matching (same algorithm as sidecars/dns-proxy), TTL-tracked resolved IP entries, Sweep() for expired entry eviction, IsResolved() for IP lookup
- `pkg/ebpf/resolver/resolver.go`: DNS daemon with miekg/dns UDP+TCP servers, allowlist check before forwarding, AAAA refused (IPv4-only simplification), per-IP AllowIP + MarkForProxy calls, background sweep goroutine, graceful Stop()
- `pkg/ebpf/resolver/resolver_test.go`: 13 test cases covering all allowlist behavior from plan spec
- MapUpdater interface decouples BPF map access — resolver tests run on macOS/CI without kernel/BPF

## Task Commits

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 (TDD RED) | Failing tests for Allowlist | a108db9 | pkg/ebpf/resolver/resolver_test.go |
| 1 (TDD GREEN) | Allowlist implementation | 409312a | pkg/ebpf/resolver/allowlist.go |
| 2 | DNS resolver daemon | 381414b | pkg/ebpf/resolver/resolver.go |

## Files Created

- `pkg/ebpf/resolver/allowlist.go` — Allowlist struct with suffix matching, TTL tracking, Sweep, IsResolved
- `pkg/ebpf/resolver/resolver.go` — Resolver, ResolverConfig, MapUpdater; miekg/dns UDP+TCP server; BPF map population
- `pkg/ebpf/resolver/resolver_test.go` — 13 allowlist tests (suffix, case, TTL, sweep)

## Decisions Made

- MapUpdater interface: test without BPF (Rule 2 — enables correctness without kernel dependency)
- AAAA refused with NOERROR+empty: avoids false "domain not found" for IPv6 addresses
- TTL floor 5s: prevents BPF map churn from CDN zero-TTL records
- Sweep returns evicted IPs: future Enforcer can revoke them from LPM_TRIE

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None. Note: enforcer.go (referenced in plan context for AllowIP/MarkForProxy signatures) was not yet present on disk (40-02 plan not yet executed), but the MapUpdater interface in resolver.go is self-contained — enforcer.go will implement the interface when 40-02 runs.

## User Setup Required

None.

## Next Phase Readiness

- 40-02 (Enforcer/loader): Enforcer must implement MapUpdater (AllowIP + MarkForProxy) to wire the resolver to BPF maps
- 40-04 (sandbox lifecycle): Resolver.Start(ctx) wired to sandbox start; cfg.ListenAddr passed to BPF sendmsg4 redirect target

## Self-Check: PASSED

All 3 files verified present on disk. All 3 commits (a108db9, 409312a, 381414b) in git log. `go test ./pkg/ebpf/resolver/` passes all 13 tests.

---
*Phase: 40-ebpf-network-enforcement*
*Completed: 2026-04-01*
