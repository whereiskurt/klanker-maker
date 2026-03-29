---
phase: 34
slug: agent-profiles-agent-orchestrator-goose-and-codex-sandbox-profiles
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-29
---

# Phase 34 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | existing test infrastructure |
| **Quick run command** | `go test ./pkg/profile/... -run TestValidate -count=1` |
| **Full suite command** | `go test ./pkg/profile/... -count=1` |
| **Estimated runtime** | ~10 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/profile/... -run TestValidate -count=1`
- **After every plan wave:** Run `go test ./pkg/profile/... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 10 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 34-01-01 | 01 | 1 | PROF-AO-01 | unit | `km validate profiles/agent-orchestrator.yaml` | ❌ W0 | ⬜ pending |
| 34-01-02 | 01 | 1 | PROF-GS-01 | unit | `km validate profiles/goose.yaml` | ❌ W0 | ⬜ pending |
| 34-01-03 | 01 | 1 | PROF-CX-01 | unit | `km validate profiles/codex.yaml` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Profile YAML files pass `km validate` — existing schema validation covers all fields
- [ ] Existing test infrastructure in `pkg/profile/` covers profile loading and validation

*Existing infrastructure covers all phase requirements.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| agent-orchestrator profile provisions correctly | PROF-AO-01 | Requires live AWS infra | `km create profiles/agent-orchestrator.yaml` and verify ao CLI works |
| goose profile provisions correctly | PROF-GS-01 | Requires live AWS infra | `km create profiles/goose.yaml` and verify goose CLI works |
| codex profile provisions correctly | PROF-CX-01 | Requires live AWS infra | `km create profiles/codex.yaml` and verify codex CLI works |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 10s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
