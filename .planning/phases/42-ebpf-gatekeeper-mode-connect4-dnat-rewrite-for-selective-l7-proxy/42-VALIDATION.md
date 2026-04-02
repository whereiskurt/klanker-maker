---
phase: 42
slug: ebpf-gatekeeper-mode-connect4-dnat-rewrite-for-selective-l7-proxy
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-02
---

# Phase 42 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go test (`go test ./...`), E2E loops via `km create`/`km destroy` |
| **Config file** | None (standard Go testing) |
| **Quick run command** | `go test ./pkg/compiler/... -run TestUserdata -v` |
| **Full suite command** | `go test ./pkg/ebpf/... ./pkg/compiler/... ./sidecars/http-proxy/...` |
| **Estimated runtime** | ~30 seconds (unit), ~10 min (E2E loop) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/compiler/... -run TestUserdata -v`
- **After every plan wave:** Run `go test ./pkg/ebpf/... ./pkg/compiler/... ./sidecars/http-proxy/...`
- **Before `/gsd:verify-work`:** Full suite must be green + successful E2E loop (cold start + idle/resume)
- **Max feedback latency:** 30 seconds (unit tests)

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 42-01-01 | 01 | 0 | Wave 0 test stubs | unit | `go test ./pkg/compiler/... -run TestUserdata` | ✅ | ⬜ pending |
| 42-02-01 | 02 | 1 | `both` mode firewall-mode block | unit | `go test ./pkg/compiler/... -run TestUserdata` | ✅ | ⬜ pending |
| 42-02-02 | 02 | 1 | `both` mode dns-port 53 | unit | `go test ./pkg/compiler/... -run TestUserdata` | ✅ | ⬜ pending |
| 42-02-03 | 02 | 1 | `both` mode no iptables DNAT | unit | `go test ./pkg/compiler/... -run TestUserdata` | ✅ | ⬜ pending |
| 42-02-04 | 02 | 1 | `both` mode no km-dns-proxy | unit | `go test ./pkg/compiler/... -run TestUserdata` | ✅ | ⬜ pending |
| 42-03-01 | 03 | 1 | L7 proxy hosts derivation | unit | `go test ./pkg/compiler/... -run TestL7ProxyHosts` | ❌ W0 | ⬜ pending |
| 42-04-01 | 04 | 2 | Resolver marks L7 IPs | unit | `go test ./pkg/ebpf/resolver/... -run TestProxyHosts` | ✅ | ⬜ pending |
| 42-05-01 | 05 | 3 | E2E: DNS resolves allowed | E2E | `km create --remote` + SSM | N/A | ⬜ pending |
| 42-05-02 | 05 | 3 | E2E: blocked domains EPERM | E2E | `km create --remote` + SSM: `curl blocked.com` | N/A | ⬜ pending |
| 42-05-03 | 05 | 3 | E2E: GitHub repo filtering (403) | E2E | `km create --remote` + SSM: `git clone blocked-repo` | N/A | ⬜ pending |
| 42-05-04 | 05 | 3 | E2E: non-L7 hosts direct | E2E | `km create --remote` + SSM: `pip install requests` | N/A | ⬜ pending |
| 42-05-05 | 05 | 3 | E2E: Bedrock metered | E2E | `km create --remote` + `km otel` | N/A | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/compiler/userdata_test.go` — add test cases for `both` mode: assert `--firewall-mode block`, `--dns-port 53`, no iptables DNAT, no km-dns-proxy in emitted script
- [ ] `pkg/compiler/userdata_test.go` — add `TestL7ProxyHosts` verifying correct host derivation from profile fields

*Existing `pkg/ebpf/resolver/resolver_test.go` and `pkg/ebpf/enforcer_test.go` already cover the BPF map path — no new test files needed.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| DNS → BPF map → connect4 redirect | Full pipeline | Requires live EC2 with eBPF | `km create --remote`, SSM in, `curl github.com`, check eBPF enforcer logs |
| Blocked domains return EPERM | Kernel enforcement | Requires live cgroup BPF | SSM: `curl blocked.example.com` should fail immediately |
| GitHub repo filtering (403) | L7 proxy enforcement | Requires live proxy + eBPF | SSM: `git clone` blocked repo, expect 403 not timeout |
| Non-L7 hosts bypass proxy | Selective routing | Requires live network path | SSM: `pip install requests`, verify no proxy in path via tcpdump/strace |
| Bedrock metered through proxy | Token counting | Requires live Bedrock call | SSM: invoke model, then `km otel` to check spend |
| Idle/resume lifecycle | State persistence | Requires EC2 hibernate cycle | `km pause` + `km resume` + re-verify all above |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
