---
phase: 41
slug: ebpf-ssl-uprobe-observability-layer-plaintext-tls-capture-via-openssl-hooks-replaces-mitm-proxy-for-inspection-github-repo-filtering-moves-to-uprobes
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-01
---

# Phase 41 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — uses existing Go test infrastructure |
| **Quick run command** | `go test ./pkg/ebpf/tls/... -timeout 30s` |
| **Full suite command** | `go test ./pkg/ebpf/... -timeout 120s` |
| **Estimated runtime** | ~30 seconds (unit), ~120 seconds (full with integration) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/ebpf/tls/... -timeout 30s`
- **After every plan wave:** Run `go test ./pkg/ebpf/... -timeout 120s`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 41-01-01 | 01 | 1 | EBPF-TLS-01 | unit | `go test ./pkg/ebpf/tls/... -run TestAttach` | ❌ W0 | ⬜ pending |
| 41-01-02 | 01 | 1 | EBPF-TLS-08 | unit | `go test ./pkg/ebpf/tls/... -run TestRingBuffer` | ❌ W0 | ⬜ pending |
| 41-02-01 | 02 | 1 | EBPF-TLS-02 | integration | `go test ./pkg/ebpf/tls/... -run TestOpenSSL` | ❌ W0 | ⬜ pending |
| 41-02-02 | 02 | 1 | EBPF-TLS-07 | unit | `go test ./pkg/ebpf/tls/... -run TestConnectionCorrelation` | ❌ W0 | ⬜ pending |
| 41-03-01 | 03 | 2 | EBPF-TLS-14 | unit | `go test ./pkg/ebpf/tls/... -run TestGitHubPathExtract` | ❌ W0 | ⬜ pending |
| 41-03-02 | 03 | 2 | EBPF-TLS-11 | unit | `go test ./pkg/profile/... -run TestTlsCapture` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/ebpf/tls/` — new package directory for TLS uprobe modules
- [ ] `pkg/ebpf/tls/bpf/` — BPF C source directory for uprobe programs
- [ ] Test stubs for OpenSSL attach, ring buffer drain, connection correlation, GitHub path extraction
- [ ] eBPF generate pipeline extension in Makefile/Dockerfile.ebpf-generate

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Uprobe attaches to live libssl.so.3 on AL2023 | EBPF-TLS-02 | Requires real kernel + library | Deploy to EC2, run `km ebpf-attach --tls`, verify events via `cat /var/log/km/tls-events.log` |
| Go crypto/tls uprobe on unstripped binary | EBPF-TLS-05 | Requires Go binary + kernel | Build test Go binary, attach probes, verify no crash + events captured |
| BoringSSL offset discovery for Bun binary | EBPF-TLS-04 (research Q4) | Requires specific Bun binary version | Run offset finder against Claude Code binary, verify SSL_write offset matches |
| Agent process survives uprobe attachment | All | Requires live process | Attach probes to running Claude Code / Goose, verify no crash |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
