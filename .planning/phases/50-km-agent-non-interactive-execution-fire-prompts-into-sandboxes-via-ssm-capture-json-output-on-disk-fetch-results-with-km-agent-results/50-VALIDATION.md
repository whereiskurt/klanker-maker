---
phase: 50
slug: km-agent-non-interactive-execution
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-10
---

# Phase 50 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib) |
| **Config file** | None (Go convention) |
| **Quick run command** | `go test ./internal/app/cmd/ -run TestAgent -count=1 -v` |
| **Full suite command** | `go test ./internal/app/cmd/ -count=1 -v` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/ -run TestAgent -count=1 -v`
- **After every plan wave:** Run `go test ./internal/app/cmd/ -count=1 -v`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 50-01-01 | 01 | 1 | AGENT-01 | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_SendCommand -v` | ❌ W0 | ⬜ pending |
| 50-01-02 | 01 | 1 | AGENT-02 | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_CommandConstruction -v` | ❌ W0 | ⬜ pending |
| 50-01-03 | 01 | 1 | AGENT-03 | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_PromptEscaping -v` | ❌ W0 | ⬜ pending |
| 50-02-01 | 02 | 1 | AGENT-04 | unit | `go test ./internal/app/cmd/ -run TestAgentResults -v` | ❌ W0 | ⬜ pending |
| 50-02-02 | 02 | 1 | AGENT-05 | unit | `go test ./internal/app/cmd/ -run TestAgentList -v` | ❌ W0 | ⬜ pending |
| 50-01-04 | 01 | 1 | AGENT-06 | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_IdleReset -v` | ❌ W0 | ⬜ pending |
| 50-01-05 | 01 | 1 | AGENT-07 | unit | `go test ./internal/app/cmd/ -run TestShellCmd -v` | ✅ | ⬜ pending |
| 50-01-06 | 01 | 1 | AGENT-08 | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_StoppedSandbox -v` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/app/cmd/agent_test.go` — test stubs for AGENT-01 through AGENT-08
- [ ] Mock SSM client for SendCommand/GetCommandInvocation (pattern exists in roll_test.go)
- [ ] Mock EventBridge client for idle-reset heartbeat testing

*Existing infrastructure covers AGENT-07 (shell_test.go).*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| E2E prompt execution on live sandbox | AGENT-01 | Requires live EC2 + SSM | `km agent <sandbox> --claude --prompt "echo hello"` then `km agent results <sandbox>` |
| Idle reset prevents sandbox teardown | AGENT-06 | Requires live TTL Lambda | Run long prompt, verify sandbox stays alive past idle timeout |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
