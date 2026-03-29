---
phase: 30
slug: sandbox-lifecycle-commands-km-pause-km-lock-km-unlock
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-29
---

# Phase 30 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — standard Go testing |
| **Quick run command** | `go test ./internal/app/cmd/... -run "Pause\|Lock\|Unlock" -count=1` |
| **Full suite command** | `go test ./internal/app/cmd/... -count=1` |
| **Estimated runtime** | ~5 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/... -run "Pause\|Lock\|Unlock" -count=1`
- **After every plan wave:** Run `go test ./internal/app/cmd/... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 5 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 30-01-01 | 01 | 1 | pause-cmd | unit | `go test ./internal/app/cmd/... -run Pause -count=1` | ❌ W0 | ⬜ pending |
| 30-01-02 | 01 | 1 | lock-cmd | unit | `go test ./internal/app/cmd/... -run Lock -count=1` | ❌ W0 | ⬜ pending |
| 30-01-03 | 01 | 1 | unlock-cmd | unit | `go test ./internal/app/cmd/... -run Unlock -count=1` | ❌ W0 | ⬜ pending |
| 30-02-01 | 02 | 1 | lock-guard | unit | `go test ./internal/app/cmd/... -run "Locked" -count=1` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/app/cmd/pause_test.go` — stubs for pause command tests
- [ ] `internal/app/cmd/lock_test.go` — stubs for lock/unlock command tests
- [ ] `internal/app/cmd/help/pause.txt` — help text (required at startup)
- [ ] `internal/app/cmd/help/lock.txt` — help text (required at startup)
- [ ] `internal/app/cmd/help/unlock.txt` — help text (required at startup)

*Existing Go test infrastructure covers all phase requirements.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| EC2 hibernate API call | pause-cmd | Requires real AWS EC2 instance | `km pause <sandbox-id>` on running sandbox, verify instance state in AWS console |
| Lock persists in S3 | lock-cmd | Requires real S3 metadata | `km lock <sandbox-id>`, then verify metadata.json in S3 |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 5s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
