---
phase: 23
slug: credential-rotation
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-26
---

# Phase 23 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing + testify (existing in repo) |
| **Config file** | none (standard `go test ./...`) |
| **Quick run command** | `go test ./internal/app/cmd/ -run TestRoll -v` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~60 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/ -run TestRoll -v && go test ./pkg/aws/ -run TestRotation -v`
- **After every plan wave:** Run `go test ./... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 60 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 23-01-01 | 01 | 0 | CRED-04 | unit | `go test ./pkg/aws/ -run TestWriteRotationAudit -v` | ❌ W0 | ⬜ pending |
| 23-01-02 | 01 | 0 | CRED-02 | unit | `go test ./pkg/aws/ -run TestUpdateIdentityPublicKey -v` | ❌ W0 | ⬜ pending |
| 23-01-03 | 01 | 0 | CRED-01 | unit | `go test ./internal/app/cmd/ -run TestRollCredsAll -v` | ❌ W0 | ⬜ pending |
| 23-01-04 | 01 | 0 | CRED-02 | unit | `go test ./internal/app/cmd/ -run TestRollCredsSandbox -v` | ❌ W0 | ⬜ pending |
| 23-01-05 | 01 | 0 | CRED-03 | unit | `go test ./internal/app/cmd/ -run TestRollCredsPlatform -v` | ❌ W0 | ⬜ pending |
| 23-01-06 | 01 | 0 | CRED-05 | unit | `go test ./internal/app/cmd/ -run TestRollCredsRestart -v` | ❌ W0 | ⬜ pending |
| 23-01-07 | 01 | 0 | CRED-06 | unit | `go test ./internal/app/cmd/ -run TestCheckCredentialRotationAge -v` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/app/cmd/roll_test.go` — covers CRED-01, CRED-02, CRED-03, CRED-05
- [ ] `pkg/aws/rotation_test.go` — covers CRED-04 (audit), CRED-02 (identity update)
- [ ] `internal/app/cmd/doctor_test.go` additions — covers CRED-06 (check-rotation case)

*(All existing test files remain valid; only additions needed)*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| SSM SendCommand restarts proxy on live EC2 sandbox | CRED-05 | Requires running EC2 sandbox | `km roll creds --sandbox <id>`, verify proxy restarts via CloudWatch |
| KMS RotateKeyOnDemand triggers rotation | CRED-01 | Requires live KMS key | Run `km roll creds --platform`, verify new key version in KMS console |
| ECS StopTask triggers task replacement for proxy CA | CRED-05 | Requires running ECS sandbox | `km roll creds --sandbox <id>`, verify new task starts with new CA |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 60s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
