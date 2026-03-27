---
phase: 26
slug: live-operations-hardening-bootstrap-init-create-destroy-ttl-auto-destroy-idle-detection-sidecar-fixes-proxy-enforcement-cli-polish
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-27
---

# Phase 26 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing stdlib |
| **Config file** | none (go test ./...) |
| **Quick run command** | `go test ./internal/app/cmd/... -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~65 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/... -count=1`
- **After every plan wave:** Run `go test ./... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 65 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 26-01-01 | 01 | 1 | test-fix | unit | `go test ./internal/app/cmd/... -run TestRunInitWithRunnerAllModules` | ✅ | ⬜ pending |
| 26-01-02 | 01 | 1 | test-fix | unit | `go test ./internal/app/cmd/... -run TestStatusCmd_Found` | ✅ | ⬜ pending |
| 26-01-03 | 01 | 1 | build-fix | unit | `go test -c ./internal/app/cmd/` | ✅ | ⬜ pending |
| 26-02-01 | 02 | 2 | cli-ux | unit | `go test ./internal/app/cmd/... -run TestAlias` | ❌ W0 | ⬜ pending |
| 26-02-02 | 02 | 2 | cli-ux | unit | `go test ./internal/app/cmd/... -run TestCompletion` | ❌ W0 | ⬜ pending |
| 26-02-03 | 02 | 2 | cli-ux | unit | `go test ./internal/app/cmd/... -run TestHelpText` | ❌ W0 | ⬜ pending |
| 26-03-01 | 03 | 2 | remote-ops | unit | `go test ./internal/app/cmd/... -run TestRemote` | ❌ W0 | ⬜ pending |
| 26-03-02 | 03 | 2 | max-lifetime | unit | `go test ./internal/app/cmd/... -run TestExtend` | ❌ W0 | ⬜ pending |
| 26-03-03 | 03 | 2 | multi-region | unit | `go test ./internal/app/cmd/... -run TestMultiRegion` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Fix `roll_test.go` build failure — blocks all `internal/app/cmd` tests
- [ ] `help/extend.txt` — help text for km extend command
- [ ] `help/stop.txt` — help text for km stop command
- [ ] New test data for alias and completion tests

*Existing test infrastructure covers internal/app/cmd — no framework install needed.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| --remote destroy via EventBridge | remote-ops | Requires live AWS EventBridge + Lambda | Run `km destroy --remote <id>`, verify Lambda receives event |
| Shell completion in terminal | cli-ux | Requires interactive shell | Source completion script, test tab completion |
| km logs --follow live tail | cli-ux | Requires live CloudWatch log group | Run sandbox, `km logs --follow <id>`, verify streaming |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 65s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
