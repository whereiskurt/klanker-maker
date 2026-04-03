---
phase: 31
slug: allowlist-profile-generator
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-03
---

# Phase 31 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib) |
| **Config file** | none — `go test ./...` |
| **Quick run command** | `go test ./pkg/allowlistgen/... -v -count=1` |
| **Full suite command** | `go test ./pkg/allowlistgen/... ./internal/app/cmd/ -v -count=1` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/allowlistgen/ -v -count=1`
- **After every plan wave:** Run `go test ./pkg/allowlistgen/... ./internal/app/cmd/ -v -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 31-01-01 | 01 | 1 | TBD-01 | unit | `go test ./pkg/allowlistgen/ -run TestRecorderDNS` | ❌ W0 | ⬜ pending |
| 31-01-02 | 01 | 1 | TBD-02 | unit | `go test ./pkg/allowlistgen/ -run TestRecorderTLS` | ❌ W0 | ⬜ pending |
| 31-01-03 | 01 | 1 | TBD-03 | unit | `go test ./pkg/allowlistgen/ -run TestNormalizeSuffixes` | ❌ W0 | ⬜ pending |
| 31-01-04 | 01 | 1 | TBD-04 | unit | `go test ./pkg/allowlistgen/ -run TestGenerate` | ❌ W0 | ⬜ pending |
| 31-01-05 | 01 | 1 | TBD-05 | integration | `go test ./pkg/allowlistgen/ -run TestGenerateValidates` | ❌ W0 | ⬜ pending |
| 31-01-06 | 01 | 1 | TBD-06 | unit | `go test ./pkg/allowlistgen/ -run TestDedup` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/allowlistgen/recorder.go` — Recorder struct and handlers
- [ ] `pkg/allowlistgen/recorder_test.go` — unit tests for recording
- [ ] `pkg/allowlistgen/generator.go` — Generate() → SandboxProfile
- [ ] `pkg/allowlistgen/generator_test.go` — golden-file YAML test
- [ ] `pkg/allowlistgen/normalize.go` — DNS suffix normalization
- [ ] `internal/app/cmd/observe.go` — km observe / km profile generate CLI

*Existing infrastructure covers framework installation.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Learning mode captures real traffic | TBD-07 | Requires live sandbox with eBPF | Run `km create --learn`, generate traffic, check observed.json |
| Generated profile works end-to-end | TBD-08 | Requires sandbox lifecycle | Apply generated profile, verify sandbox boots with it |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
