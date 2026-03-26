---
phase: 22
slug: remote-sandbox-creation
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-26
---

# Phase 22 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib) — `go test ./...` |
| **Config file** | none |
| **Quick run command** | `go test ./cmd/create-handler/... ./cmd/email-create-handler/... ./pkg/aws/... -run TestCreate -v` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/aws/... ./cmd/create-handler/... ./cmd/email-create-handler/... -v`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 22-01-01 | 01 | 0 | REMOTE-01 | smoke | `docker run km-create-handler /var/task/km version` | ❌ W0 | ⬜ pending |
| 22-01-02 | 01 | 0 | REMOTE-01 | unit | `go test ./cmd/create-handler/... -run TestHandle_ValidEvent -v` | ❌ W0 | ⬜ pending |
| 22-01-03 | 01 | 0 | REMOTE-02 | unit | `go test ./pkg/aws/... -run TestPutSandboxCreateEvent -v` | ❌ W0 | ⬜ pending |
| 22-01-04 | 01 | 0 | REMOTE-02 | unit | `go test ./internal/app/cmd/... -run TestCreateRemote_PublishesEvent -v` | ❌ W0 | ⬜ pending |
| 22-01-05 | 01 | 0 | REMOTE-03 | unit | `go test ./cmd/email-create-handler/... -run TestHandleEmail_MultipartYAML -v` | ❌ W0 | ⬜ pending |
| 22-01-06 | 01 | 0 | REMOTE-03 | unit | `go test ./cmd/email-create-handler/... -run TestHandleEmail_PlainYAML -v` | ❌ W0 | ⬜ pending |
| 22-01-07 | 01 | 0 | REMOTE-04 | unit | `go test ./cmd/email-create-handler/... -run TestHandleEmail_WrongPhrase -v` | ❌ W0 | ⬜ pending |
| 22-01-08 | 01 | 0 | REMOTE-04 | unit | `go test ./cmd/email-create-handler/... -run TestHandleEmail_CorrectPhrase -v` | ❌ W0 | ⬜ pending |
| 22-01-09 | 01 | 0 | REMOTE-05 | integration | manual — check EventBridge console or CloudTrail | N/A | ⬜ pending |
| 22-01-10 | 01 | 0 | REMOTE-06 | unit | `go test ./cmd/create-handler/... -run TestHandle_SendsNotification -v` | ❌ W0 | ⬜ pending |
| 22-01-11 | 01 | 0 | REMOTE-06 | unit | `go test ./cmd/create-handler/... -run TestHandle_SendsFailureNotification -v` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `cmd/create-handler/main_test.go` — mock S3, SES, exec.Cmd DI; covers Handle, subprocess invocation
- [ ] `cmd/email-create-handler/main_test.go` — mock S3, SSM, EventBridge; covers MIME parsing, safe phrase check
- [ ] `pkg/aws/eventbridge.go` — PutSandboxCreateEvent function + EventBridgeAPI interface
- [ ] `pkg/aws/eventbridge_test.go` — mock EventBridgeAPI; covers PutSandboxCreateEvent happy path + failed entry handling
- [ ] `internal/app/cmd/create_remote_test.go` — verifies `--remote` flag wires up PutSandboxCreateEvent call
- [ ] Makefile additions: `build-create-handler` target (container build), `push-create-handler` target (ECR push)

*(All existing test files remain valid; only additions needed)*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| EventBridge rule routes SandboxCreate to create Lambda | REMOTE-05 | Requires live AWS EventBridge + Lambda deployment | Check EventBridge console for rule target, or publish test event and verify Lambda invocation in CloudTrail |
| Container image Lambda builds and starts | REMOTE-01 | Requires Docker build + ECR push + Lambda deployment | `docker run km-create-handler /var/task/km version` locally; full test requires Lambda invoke |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
