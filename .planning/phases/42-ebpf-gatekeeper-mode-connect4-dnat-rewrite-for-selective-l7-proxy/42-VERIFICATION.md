---
phase: 42-ebpf-gatekeeper-mode-connect4-dnat-rewrite-for-selective-l7-proxy
verified: 2026-04-02T00:00:00Z
status: human_needed
score: 7/7 must-haves verified
human_verification:
  - test: "E2E gatekeeper mode reliability (repeat cycle)"
    expected: "Sandbox boots with enforcement:both, eBPF block mode active, no iptables DNAT, km-dns-proxy not running, GitHub repo filtering (allowed clone succeeds, blocked clone gets 403), evil.com blocked with EPERM-equivalent, DNS resolves via 127.0.0.1:53"
    why_human: "Full lifecycle deployment required; automated tests validate template output and unit logic but cannot verify kernel-level BPF program behavior, proxy PID exemption preventing infinite redirect loops, or DNS resolver functioning on a live EC2 instance"
---

# Phase 42: eBPF Gatekeeper Mode Verification Report

**Phase Goal:** eBPF gatekeeper mode — connect4 DNAT rewrite for selective L7 proxy. In "both" enforcement mode, connect4 becomes the primary enforcer (block mode), with selective DNAT rewrite to the HTTP proxy only for L7-required hosts (GitHub repo filtering, Bedrock token metering).
**Verified:** 2026-04-02
**Status:** human_needed
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | HTTP proxy process is exempt from connect4/sendmsg4 BPF interception (prevents infinite redirect loop) | VERIFIED | `const_http_proxy_pid` volatile const in `common.h:49`; dual-PID check in `bpf.c:153` (connect4) and `bpf.c:234` (sendmsg4); enforcer wires it at `enforcer.go:76-78`; `--proxy-pid` flag at `ebpf_attach.go:80-81`; Config field at `types.go:13` |
| 2 | L7 proxy host list is derived from profile fields (GitHub + Bedrock), not GitHub repo names | VERIFIED | `buildL7ProxyHosts()` at `userdata.go:1011` derives 4 GitHub domains + 2 Bedrock domains from `p.Spec.SourceAccess.GitHub` and `p.Spec.Execution.UseBedrock`; template line `userdata.go:653` uses `{{ .L7ProxyHosts }}`; 4 unit tests pass (TestL7ProxyHostsWithGitHub, TestL7ProxyHostsWithBedrock, TestL7ProxyHostsEmpty, TestL7ProxyHostsBedrockOnly) |
| 3 | km ebpf-attach accepts --proxy-pid flag for dual PID exemption | VERIFIED | `ebpf_attach.go:80-81` defines `--proxy-pid` uint32 flag; `ebpf_attach.go:135` sets `cfg.HTTPProxyPID = httpProxyPID`; block-mode warning at `ebpf_attach.go:143-144` |
| 4 | Non-L7 allowed hosts connect directly without proxy (L7-flagged IPs get proxy_hosts map entries, non-L7 IPs do not) | VERIFIED | `TestProxyHosts` in `pkg/ebpf/resolver/resolver_test.go:184` with 8 subtests passes — L7 domains (github.com, api.anthropic.com, bedrock endpoints) are marked for proxy, non-L7 domain (pypi.org) is not |
| 5 | both mode uses --firewall-mode block, --dns-port 53, no iptables DNAT, no km-dns-proxy, resolv.conf 127.0.0.1 | VERIFIED | Template conditionals confirmed at `userdata.go:536,551,640,646,703`; 10 gatekeeper tests pass (TestBothModeGatekeeperFirewallBlock through TestEbpfModeUnchanged) |
| 6 | both mode passes proxy PID via pidfile + --proxy-pid flag with correct ExecStartPost/ExecStartPre/EnvironmentFile pattern | VERIFIED | `userdata.go:376` writes PID in `ExecStartPost`; `userdata.go:635-636` reads it in `ExecStartPre` → `EnvironmentFile`; `userdata.go:655` passes `${KM_HTTP_PROXY_PID}`; `/run/km` chowned to km-sidecar at `userdata.go:385-386` |
| 7 | profiles/goose-ebpf-gatekeeper.yaml exists with enforcement: both for E2E testing | VERIFIED | File exists; `grep "enforcement: both"` confirms the field is set |

**Score:** 7/7 truths verified

---

## Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/ebpf/headers/common.h` | volatile const declaration for http proxy PID | VERIFIED | `const_http_proxy_pid` at line 49 with comment |
| `pkg/ebpf/bpf.c` | Dual PID exemption in connect4 and sendmsg4 hooks | VERIFIED | Second PID check at line 153 (connect4) and line 234 (sendmsg4) |
| `pkg/ebpf/types.go` | HTTPProxyPID field in Config struct | VERIFIED | `HTTPProxyPID uint32` at line 13 with gatekeeper mode comment |
| `pkg/ebpf/enforcer.go` | Wiring of const_http_proxy_pid volatile variable | VERIFIED | `spec.Variables["const_http_proxy_pid"].Set(cfg.HTTPProxyPID)` at lines 76-78 |
| `pkg/ebpf/bpf_x86_bpfel.go` | Regenerated Go bindings with ConstHttpProxyPid | VERIFIED | `ConstHttpProxyPid *ebpf.VariableSpec` at line 89 and `*ebpf.Variable` at line 143 |
| `internal/app/cmd/ebpf_attach.go` | --proxy-pid CLI flag, reads PID and passes to Config | VERIFIED | Flag defined at line 80-81; `cfg.HTTPProxyPID = httpProxyPID` at line 135 |
| `pkg/compiler/userdata.go` | buildL7ProxyHosts helper + L7ProxyHosts template field | VERIFIED | `buildL7ProxyHosts()` at line 1011; `L7ProxyHosts string` field at line 974; wired at line 1129 |
| `pkg/compiler/userdata_test.go` | Unit tests for L7 proxy host derivation and both-mode gatekeeper | VERIFIED | TestL7ProxyHosts* (4 tests) + TestBothModeGatekeeper* (8 tests) + TestProxyModeUnchanged + TestEbpfModeUnchanged — all 14 pass |
| `profiles/goose-ebpf-gatekeeper.yaml` | Test profile with enforcement: both | VERIFIED | File exists with `enforcement: both` |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `pkg/ebpf/bpf.c` | `pkg/ebpf/headers/common.h` | volatile const declaration | WIRED | `const_http_proxy_pid` used in bpf.c lines 153, 234; declared in common.h line 49 |
| `pkg/ebpf/enforcer.go` | `pkg/ebpf/types.go` | Config.HTTPProxyPID field | WIRED | enforcer.go reads `cfg.HTTPProxyPID` at line 77; field defined in types.go line 13 |
| `internal/app/cmd/ebpf_attach.go` | `pkg/ebpf/types.go` | --proxy-pid flag populates Config.HTTPProxyPID | WIRED | ebpf_attach.go line 135: `HTTPProxyPID: httpProxyPID`; `httpProxyPID` bound to `--proxy-pid` flag at line 80 |
| `pkg/compiler/userdata.go` | `internal/app/cmd/ebpf_attach.go` | systemd unit passes --proxy-pid via ${KM_HTTP_PROXY_PID} | WIRED | Template line 655: `--proxy-pid ${KM_HTTP_PROXY_PID}`; ExecStartPre at line 635 populates env; enforcer accepts the flag |
| `pkg/compiler/userdata.go` | `pkg/ebpf/resolver/resolver.go` | --proxy-hosts passes L7ProxyHosts domain suffixes | WIRED | Template line 653: `--proxy-hosts "{{ .L7ProxyHosts }}"` passes derived suffixes; resolver TestProxyHosts confirms suffix matching |

---

## Requirements Coverage

| Requirement | Source Plans | Description | Status | Evidence |
|-------------|-------------|-------------|--------|---------|
| EBPF-NET-03 | 42-01, 42-02, 42-03 | connect4 rewrites dest IP/port for L7 inspection hosts, redirects to 127.0.0.1:{proxy_port} | SATISFIED | connect4 dual-PID exemption + `proxy_hosts` BPF map populated by resolver for L7 domains; template emits `--firewall-mode block` for both mode so connect4 actively enforces; connect4 DNAT rewrite code predates Phase 42 (Phase 40 foundation), Phase 42 activates it in gatekeeper mode |
| EBPF-NET-09 | 42-01, 42-02, 42-03 | Profile schema `spec.network.enforcement` field with proxy/ebpf/both values | SATISFIED | `enforcement: both` active in `goose-ebpf-gatekeeper.yaml`; template branches tested for all three enforcement values; Phase 40 added the schema field, Phase 42 fully activates the `both` mode behavior |

**Note on phase assignment:** REQUIREMENTS.md maps both IDs to Phase 40. Phase 40 built the connect4 infrastructure (EBPF-NET-03 foundation) and enforcement field schema (EBPF-NET-09). Phase 42 activates gatekeeper mode — making `both` enforcement use block mode connect4 as primary enforcer. Both phases legitimately advance these requirements; the REQUIREMENTS.md table is an approximation of first-delivery phase.

---

## Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `pkg/compiler/userdata.go` | 1005 | Comment typo: `/ Only` (missing second `/` for `//`) | Info | No functional impact; doc comment formatting only |

No stub implementations, empty handlers, or placeholder returns found in any phase-modified files. All packages compile cleanly. `go vet` passes with no warnings.

---

## Human Verification Required

### 1. E2E Gatekeeper Lifecycle Loop

**Test:** Run `km create profiles/goose-ebpf-gatekeeper.yaml --remote`, wait for SANDBOX_READY, shell in via SSM, and verify all enforcement behaviors. Run at least 2 full cycles.

**Expected:**
- `journalctl -u km-ebpf-enforcer` shows `firewall_mode=block`, `dns_port=53`
- `systemctl status km-dns-proxy` is inactive/not-found
- `sudo iptables -t nat -L` shows no DNAT rules to proxy ports
- `/etc/resolv.conf` contains `nameserver 127.0.0.1`
- `cat /run/km/http-proxy.pid` returns a valid PID matching km-http-proxy
- `curl https://evil.com` fails immediately (not timeout — EPERM from BPF)
- `git clone https://github.com/<allowed-repo>` succeeds through proxy L7 path
- `git clone https://github.com/<blocked-repo>` receives 403 from proxy
- `km-http-proxy` logs show no redirect loops (proxy's own connections not intercepted)
- `km destroy <sandbox-id> --remote --yes` cleans up with no orphaned resources

**Why human:** BPF kernel enforcement, proxy redirect loop prevention, and DNS resolver behavior on a live EC2 instance cannot be verified by static code analysis or unit tests. The E2E fix for `/run/km` ownership (Bug 1) and `--proxy-pid` shell expansion (Bug 2) were found during live testing and fixed in-place — these fixes need re-confirmation that no regressions occurred.

---

## Build Verification

```
go build ./pkg/ebpf/... ./internal/app/cmd/... ./pkg/compiler/...   OK
go vet ./pkg/ebpf/... ./pkg/compiler/... ./internal/app/cmd/...     OK (no warnings)
go test ./pkg/compiler/... -run "TestL7ProxyHosts|TestBothMode|TestProxyMode|TestEbpfMode"
  14 tests PASS
go test ./pkg/ebpf/resolver/... -run TestProxyHosts
  TestProxyHosts (8 subtests) PASS
  TestProxyHostsMockUpdater PASS
```

---

## Gaps Summary

No gaps. All automated checks pass. The only item requiring human validation is the E2E deployment behavior — which by definition of the test type (live kernel BPF enforcement) cannot be verified statically. The 42-03 SUMMARY documents that 2 full E2E cycles were completed successfully with all expected behaviors confirmed, and two bugs were found and fixed (PID file ownership, systemd shell expansion). The automated test suite fully covers the template output and unit logic.

---

_Verified: 2026-04-02_
_Verifier: Claude (gsd-verifier)_
