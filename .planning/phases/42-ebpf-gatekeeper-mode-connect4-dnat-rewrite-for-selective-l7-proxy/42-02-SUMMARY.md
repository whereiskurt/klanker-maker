---
phase: 42-ebpf-gatekeeper-mode-connect4-dnat-rewrite-for-selective-l7-proxy
plan: "02"
subsystem: compiler/userdata
tags: [ebpf, gatekeeper, both-mode, userdata, connect4, iptables, dns]
requirements: [EBPF-NET-03, EBPF-NET-09]

dependency_graph:
  requires: [42-01]
  provides: [both-mode-gatekeeper-userdata]
  affects: [pkg/compiler/userdata.go, pkg/compiler/userdata_test.go]

tech_stack:
  added: []
  patterns:
    - "eBPF gatekeeper mode: connect4 replaces iptables DNAT in both enforcement"
    - "PID file coordination: ExecStartPost writes MAINPID, enforcer reads at startup"
    - "TDD red-green: failing tests committed before implementation"

key_files:
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go

decisions:
  - "both-mode skips km-dns-proxy; eBPF resolver handles DNS on :53"
  - "both-mode uses --firewall-mode block (kernel gatekeeper, not passive log)"
  - "both-mode keeps HTTP_PROXY/HTTPS_PROXY as belt-and-suspenders alongside connect4"
  - "Updated TestUserDataEnforcementBoth to reflect gatekeeper behavior (pre-Phase-42 expectation removed)"

metrics:
  duration: 395s
  completed: "2026-04-03"
  tasks_completed: 2
  files_modified: 2
---

# Phase 42 Plan 02: Both-Mode Gatekeeper Userdata Template Summary

Flipped the `both` enforcement mode userdata template from passive eBPF + iptables to active eBPF gatekeeper: block firewall, dns-port 53, no iptables DNAT, no km-dns-proxy, resolv.conf override, proxy PID coordination, and L7 proxy env vars as belt-and-suspenders.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Userdata template gatekeeper mode for both enforcement | 34fa457 | pkg/compiler/userdata.go, pkg/compiler/userdata_test.go |
| 2 | Unit tests for both-mode gatekeeper userdata output | a3fb9ed, 34fa457 | pkg/compiler/userdata_test.go |

## What Was Built

### Template Changes (pkg/compiler/userdata.go)

**Change 1 — Skip km-dns-proxy for both mode:**
The `{{- if eq .Enforcement "ebpf" }}` condition that skips km-dns-proxy was expanded to `{{- if or (eq .Enforcement "ebpf") (eq .Enforcement "both") }}`. Both mode now starts only `km-http-proxy km-audit-log km-tracing` (no dns-proxy).

**Change 2 — Remove iptables DNAT for both mode:**
Changed `{{- if or (eq .Enforcement "proxy") (eq .Enforcement "both") }}` to `{{- if eq .Enforcement "proxy" }}`. iptables DNAT rules are now emitted only for pure `proxy` mode. In `both` mode, connect4 BPF program handles kernel-level routing.

**Change 3 — DNS port 53 for both mode:**
Expanded `{{- if eq .Enforcement "ebpf" }}` to `{{- if or (eq .Enforcement "ebpf") (eq .Enforcement "both") }}` for the `--dns-port 53` flag. Both mode runs the eBPF resolver on :53.

**Change 4 — Firewall mode block for both mode:**
Expanded `{{- if eq .Enforcement "ebpf" }}` to `{{- if or (eq .Enforcement "ebpf") (eq .Enforcement "both") }}` for `--firewall-mode block`. Both mode enforces at kernel level.

**Change 6 — --proxy-pid flag:**
Added `{{- if eq .Enforcement "both" }} --proxy-pid "$(cat /run/km/http-proxy.pid 2>/dev/null || echo 0)" {{- end }}` after `--proxy-hosts` in the km-ebpf-enforcer systemd ExecStart. The enforcer reads the proxy PID to exempt it from BPF redirection.

**Change 7 — Write proxy PID file:**
Added `ExecStartPost=/bin/bash -c 'echo $MAINPID > /run/km/http-proxy.pid'` to the km-http-proxy.service unit. Written before the enforcer starts (sidecars start first, enforcer starts after).

**Change 8 — resolv.conf override for both mode:**
Expanded the `{{- if eq .Enforcement "ebpf" }}` block to `{{- if or (eq .Enforcement "ebpf") (eq .Enforcement "both") }}`. Both mode overrides resolv.conf to `nameserver 127.0.0.1`, routing all DNS to the eBPF resolver.

**Change 9 (added) — Proxy env vars for both mode:**
Added a new `{{- if eq .Enforcement "both" }}` block that writes `HTTP_PROXY/HTTPS_PROXY/http_proxy/https_proxy` env vars to `/etc/profile.d/km-audit.sh` as belt-and-suspenders. Apps that respect proxy env vars will route through the L7 proxy for Bedrock/GitHub inspection.

**Change 5 — Already done by Plan 01:**
Confirmed `--proxy-hosts "{{ .L7ProxyHosts }}"` was already in place from Plan 01 Task 2. No change needed.

### Unit Tests (pkg/compiler/userdata_test.go)

Added `bothProfile()` helper and 10 test functions:

1. `TestBothModeGatekeeperFirewallBlock` — PASS
2. `TestBothModeGatekeeperDNSPort53` — PASS
3. `TestBothModeGatekeeperNoDNSProxy` — PASS
4. `TestBothModeGatekeeperNoIptables` — PASS
5. `TestBothModeGatekeeperResolvConf` — PASS
6. `TestBothModeGatekeeperProxyHosts` — PASS
7. `TestBothModeGatekeeperProxyPID` — PASS
8. `TestBothModeGatekeeperKeepsProxyEnvVars` — PASS
9. `TestProxyModeUnchanged` — PASS
10. `TestEbpfModeUnchanged` — PASS

## Verification

```
go test ./pkg/compiler/... -run "TestBothMode|TestProxyMode|TestEbpfMode" -v
PASS (10/10)

go test ./pkg/compiler/... 
ok github.com/whereiskurt/klankrmkr/pkg/compiler

go build ./...
(clean)
```

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Updated pre-existing TestUserDataEnforcementBoth for new gatekeeper behavior**
- **Found during:** Task 1 GREEN phase (running full test suite)
- **Issue:** `TestUserDataEnforcementBoth` expected `iptables -t nat` in both-mode output — this was the pre-Phase-42 behavior being intentionally replaced.
- **Fix:** Updated test assertions: removed `iptables -t nat` requirement, changed comment to document gatekeeper mode, kept all other assertions (eBPF cgroup, ebpf-attach, km.slice, HTTP_PROXY).
- **Files modified:** pkg/compiler/userdata_test.go
- **Commit:** 34fa457

**2. [Rule 2 - Missing functionality] Added belt-and-suspenders proxy env vars block for both mode**
- **Found during:** Task 1 implementation (Change 9 analysis)
- **Issue:** After moving proxy env vars to be inside `{{- if eq .Enforcement "proxy" }}` (Change 2), both mode had no HTTP_PROXY/HTTPS_PROXY env vars. The plan requires both mode keeps these as belt-and-suspenders.
- **Fix:** Added a separate `{{- if eq .Enforcement "both" }}` block that writes proxy env vars to `/etc/profile.d/km-audit.sh`.
- **Files modified:** pkg/compiler/userdata.go
- **Commit:** 34fa457

## Self-Check: PASSED

- pkg/compiler/userdata.go — FOUND
- pkg/compiler/userdata_test.go — FOUND
- Commit 34fa457 — FOUND
- Commit a3fb9ed — FOUND
