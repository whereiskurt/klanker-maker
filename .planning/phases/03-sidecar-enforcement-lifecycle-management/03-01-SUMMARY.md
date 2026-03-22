---
phase: 03-sidecar-enforcement-lifecycle-management
plan: "01"
subsystem: infra
tags: [dns-proxy, http-proxy, goproxy, miekg-dns, otel, zerolog, sidecar, netw-02, netw-03]

requires:
  - phase: 02-core-provisioning-security-baseline
    provides: EC2/ECS substrate where sidecars are deployed as OS-level processes or containers

provides:
  - "DNS allowlist proxy binary (miekg/dns) — NXDOMAIN for non-allowlisted queries"
  - "HTTP/HTTPS CONNECT proxy binary (elazarl/goproxy) — 403 for non-allowlisted hosts, traceparent injection on allowed"
  - "sidecars/dns-proxy/dnsproxy package: IsAllowed(), NewHandler() (exportable)"
  - "sidecars/http-proxy/httpproxy package: IsHostAllowed(), InjectTraceContext(), NewProxy() (exportable)"

affects:
  - 03-sidecar-enforcement-lifecycle-management
  - phase 4 (sidecar integration into ECS task definitions and EC2 user-data)

tech-stack:
  added:
    - "github.com/miekg/dns v1.1.72 — DNS server and client library"
    - "github.com/elazarl/goproxy v1.8.2 — HTTP CONNECT proxy library"
    - "go.opentelemetry.io/otel v1.42.0 — OTel SDK for W3C trace context propagation"
  patterns:
    - "Sidecar split: library in pkg/ subdir (package dnsproxy/httpproxy) + package main in parent dir"
    - "External test package (_test suffix) alongside library to avoid circular imports"
    - "Zerolog JSON-to-stdout for all sidecar audit events (sandbox_id, event_type fields)"
    - "Config via env vars: ALLOWED_SUFFIXES/ALLOWED_HOSTS, DNS_PORT/PROXY_PORT, SANDBOX_ID, UPSTREAM_DNS"

key-files:
  created:
    - "sidecars/dns-proxy/dnsproxy/proxy.go — IsAllowed() + NewHandler() DNS enforcement library"
    - "sidecars/dns-proxy/dnsproxy/dns_proxy_test.go — 5 unit tests for DNS proxy"
    - "sidecars/dns-proxy/main.go — DNS proxy binary entry point"
    - "sidecars/http-proxy/httpproxy/proxy.go — IsHostAllowed() + InjectTraceContext() + NewProxy() HTTP enforcement library"
    - "sidecars/http-proxy/httpproxy/http_proxy_test.go — 5 unit tests for HTTP proxy"
    - "sidecars/http-proxy/main.go — HTTP proxy binary entry point"
  modified:
    - "go.mod / go.sum — added miekg/dns, elazarl/goproxy, otel dependencies"

key-decisions:
  - "DNS proxy split into dnsproxy/ subdirectory package to avoid Go package conflict between library (package dnsproxy) and binary (package main)"
  - "HTTP proxy exposes InjectTraceContext() as exportable function enabling direct unit testing of OTel propagation without full proxy round-trip"
  - "goproxy CONNECT handler chain breaks on first non-nil action — single handler pattern used; second handler registration cannot observe injected headers"
  - "W3C traceparent injection is a no-op without an active OTel span (by design); proxy calls Inject unconditionally, value present only with active tracing backend"

patterns-established:
  - "Sidecar library pattern: split into pkg/ subdir for testability, main.go wires env vars and starts server"
  - "Raw TCP dial used in tests to send CONNECT requests — http.Client does not support manual CONNECT"

requirements-completed:
  - NETW-02
  - NETW-03
  - OBSV-10

duration: 8min
completed: "2026-03-22"
---

# Phase 03 Plan 01: DNS and HTTP Proxy Sidecars Summary

**DNS allowlist proxy (miekg/dns, NXDOMAIN-on-deny) and HTTP/HTTPS CONNECT proxy (elazarl/goproxy, 403-on-deny) with W3C traceparent injection via OTel propagation API**

## Performance

- **Duration:** 8 min
- **Started:** 2026-03-22T04:44:43Z
- **Completed:** 2026-03-22T04:52:59Z
- **Tasks:** 2
- **Files modified:** 6 created + go.mod/go.sum

## Accomplishments

- DNS proxy sidecar enforces ALLOWED_SUFFIXES allowlist — forwards allowed queries to VPC resolver (169.254.169.253), returns NXDOMAIN for blocked queries, logs JSON audit events via zerolog
- HTTP/HTTPS CONNECT proxy enforces ALLOWED_HOSTS allowlist — blocks non-listed hosts with 403, injects W3C traceparent header via `otel.GetTextMapPropagator().Inject` on allowed CONNECT requests
- Both binaries compile as standalone Go programs with env-var-only configuration; all 10 unit tests pass in under 2 seconds

## Task Commits

Each task was committed atomically:

1. **Task 1: DNS proxy sidecar (NETW-02)** - `419572e` (feat)
2. **Task 2: HTTP/HTTPS proxy sidecar with OTel injection (NETW-03, OBSV-10)** - `53fad2e` (committed as part of docs(03-02) by prior phase execution)

## Files Created/Modified

- `sidecars/dns-proxy/dnsproxy/proxy.go` — IsAllowed() predicate and NewHandler() DNS HandlerFunc with forwarding and NXDOMAIN logic
- `sidecars/dns-proxy/dnsproxy/dns_proxy_test.go` — In-process DNS server tests with mock upstream
- `sidecars/dns-proxy/main.go` — Binary: reads env vars, starts UDP+TCP dns.Server
- `sidecars/http-proxy/httpproxy/proxy.go` — IsHostAllowed(), InjectTraceContext(), NewProxy() with goproxy CONNECT and plain HTTP enforcement
- `sidecars/http-proxy/httpproxy/http_proxy_test.go` — httptest.Server + raw TCP CONNECT tests
- `sidecars/http-proxy/main.go` — Binary: reads env vars, starts http.ListenAndServe

## Decisions Made

- Package layout: library code in `dnsproxy/` and `httpproxy/` subdirs (not root) to avoid Go package conflict between `package main` and `package dnsproxy` in the same directory
- `InjectTraceContext()` exported function allows direct unit testing of OTel propagation call without requiring a full proxy CONNECT round-trip
- goproxy CONNECT handler chain stops at first non-nil result; single handler owns both allow/deny decision and header injection

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Go package layout restructured to resolve package conflict**
- **Found during:** Task 1 (DNS proxy sidecar)
- **Issue:** Plan specified `proxy.go (package dnsproxy)` and `main.go (package main)` in the same directory — Go forbids two different non-test package names in one directory
- **Fix:** Moved library files to `dnsproxy/` and `httpproxy/` subdirectories; moved test files to same subdirs as external test packages (`_test` suffix)
- **Files modified:** sidecars/dns-proxy/dnsproxy/proxy.go, sidecars/http-proxy/httpproxy/proxy.go (new paths)
- **Verification:** `go build ./sidecars/dns-proxy/ ./sidecars/http-proxy/` passes; all tests pass
- **Committed in:** 419572e (Task 1 commit)

**2. [Rule 1 - Bug] TestHTTPProxy_TraceparentInjected rewritten to use raw TCP CONNECT**
- **Found during:** Task 2 (HTTP proxy sidecar)
- **Issue:** `http.Client.Do` refuses to send manual CONNECT requests (`Request.RequestURI can't be set in client requests`); test was skipping
- **Fix:** Replaced with raw `net.Dial` + manual `CONNECT` request write; added exported `InjectTraceContext()` for direct unit-level testing of OTel inject call
- **Files modified:** sidecars/http-proxy/httpproxy/http_proxy_test.go, sidecars/http-proxy/httpproxy/proxy.go
- **Verification:** `TestHTTPProxy_TraceparentInjected` passes (no skip)
- **Committed in:** 53fad2e

---

**Total deviations:** 2 auto-fixed (2 Rule 1 - Bug)
**Impact on plan:** Both fixes necessary for correct Go package structure and test coverage. No scope creep.

## Issues Encountered

- goproxy CONNECT handler chain terminates on first non-nil action — a second registered handler to capture headers in tests is never called. Resolved by exporting `InjectTraceContext()` for direct testing.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- DNS and HTTP proxy binaries are ready to be packaged into container images or installed via user-data.sh
- EC2 integration requires iptables DNAT rules (TCP 443 → :3128, UDP 53 → :5353); ECS integration uses http_proxy/https_proxy env vars
- Both sidecars are fail-closed: SG blocks all egress if proxy is down (verified by design in 03-CONTEXT.md)

## Self-Check: PASSED

- sidecars/dns-proxy/dnsproxy/proxy.go: FOUND
- sidecars/dns-proxy/dnsproxy/dns_proxy_test.go: FOUND
- sidecars/dns-proxy/main.go: FOUND
- sidecars/http-proxy/httpproxy/proxy.go: FOUND
- sidecars/http-proxy/httpproxy/http_proxy_test.go: FOUND
- sidecars/http-proxy/main.go: FOUND
- .planning/phases/03-sidecar-enforcement-lifecycle-management/03-01-SUMMARY.md: FOUND
- Commit 419572e: FOUND
- Commit 53fad2e: FOUND

---
*Phase: 03-sidecar-enforcement-lifecycle-management*
*Completed: 2026-03-22*
