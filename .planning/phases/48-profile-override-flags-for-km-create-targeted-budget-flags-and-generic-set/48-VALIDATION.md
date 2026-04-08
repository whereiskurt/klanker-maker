---
phase: 48
slug: profile-override-flags-for-km-create-targeted-budget-flags-and-generic-set
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-07
---

# Phase 48 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing + testify (stdlib `testing` package) |
| **Config file** | none — `go test ./...` |
| **Quick run command** | `go test ./internal/app/cmd/ ./pkg/compiler/ ./pkg/lifecycle/ -run TestCreate -v` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/ ./pkg/compiler/ ./pkg/lifecycle/ -run TestCreate -v`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 48-01-01 | 01 | 1 | --ttl flag | unit | `go test ./internal/app/cmd/ -run TestCreateTTLOverride -v` | ❌ W0 | ⬜ pending |
| 48-01-02 | 01 | 1 | --idle flag | unit | `go test ./internal/app/cmd/ -run TestCreateIdleOverride -v` | ❌ W0 | ⬜ pending |
| 48-01-03 | 01 | 1 | --ttl 0 no-schedule | unit | `go test ./internal/app/cmd/ -run TestCreateTTLZeroNoSchedule -v` | ❌ W0 | ⬜ pending |
| 48-01-04 | 01 | 1 | conflict guard | unit | `go test ./internal/app/cmd/ -run TestCreateOverrideConflict -v` | ❌ W0 | ⬜ pending |
| 48-01-05 | 01 | 1 | profile S3 upload | unit | `go test ./internal/app/cmd/ -run TestCreateMutatedProfileS3Upload -v` | ❌ W0 | ⬜ pending |
| 48-01-06 | 01 | 1 | sidecar idle action | unit | `go test ./sidecars/audit-log/... -run TestIdleActionHibernate -v` | ❌ W0 | ⬜ pending |
| 48-01-07 | 01 | 1 | compiler IdleAction | unit | `go test ./pkg/compiler/ -run TestIdleActionParam -v` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/app/cmd/create_override_test.go` — covers --ttl, --idle, TTL=0, conflict guard
- [ ] `pkg/compiler/userdata_idle_action_test.go` — covers IdleAction param in userDataParams
- [ ] `sidecars/audit-log/cmd/idle_action_test.go` — covers IDLE_ACTION=hibernate loop

*Existing infrastructure covers test framework — only test files need creation.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end TTL=0 hibernate | --ttl 0 hibernates on idle | Requires live EC2 + EventBridge | `km create profiles/claude.yaml --ttl 0 --idle 5m`, wait for idle, verify instance hibernated |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
