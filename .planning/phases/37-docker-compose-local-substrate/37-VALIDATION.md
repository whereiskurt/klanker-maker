---
phase: 37
slug: docker-compose-local-substrate
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-31
---

# Phase 37 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — standard Go test infrastructure |
| **Quick run command** | `go test ./pkg/compiler/ ./pkg/profile/ -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/compiler/ ./pkg/profile/ -count=1`
- **After every plan wave:** Run `go test ./... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 37-01-01 | 01 | 1 | PROV-09 | unit | `go test ./pkg/profile/ -run TestSubstrate -count=1` | ❌ W0 | ⬜ pending |
| 37-02-01 | 02 | 1 | PROV-09 | unit | `go test ./pkg/compiler/ -run TestDocker -count=1` | ❌ W0 | ⬜ pending |
| 37-03-01 | 03 | 2 | PROV-09 | unit | `go test ./internal/app/cmd/ -run TestCreate -count=1` | ❌ W0 | ⬜ pending |
| 37-04-01 | 04 | 3 | PROV-09 | integration | `scripts/smoke-test-docker-substrate.sh` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Test stubs for Docker substrate validation in `pkg/profile/`
- [ ] Test stubs for Docker Compose compiler in `pkg/compiler/`
- [ ] Test stubs for `km create --substrate docker` in `internal/app/cmd/`

*Existing Go test infrastructure covers framework needs.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Docker Compose sandbox boots on macOS | PROV-09 | Requires Docker Desktop | Run `km create profiles/goose.yaml --substrate docker`, verify 5 containers running |
| Sandbox connects to AWS services | PROV-09 | Requires AWS credentials | Verify SSM secrets injected, DynamoDB budget tracking works |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
