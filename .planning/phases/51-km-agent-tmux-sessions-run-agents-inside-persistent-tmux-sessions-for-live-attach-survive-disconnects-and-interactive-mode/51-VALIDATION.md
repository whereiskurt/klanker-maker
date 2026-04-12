---
phase: 51
slug: km-agent-tmux-sessions
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-12
---

# Phase 51 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib) |
| **Config file** | internal/app/cmd/agent_test.go |
| **Quick run command** | `go test ./internal/app/cmd/ -run TestAgent -count=1 -v` |
| **Full suite command** | `go test ./internal/app/cmd/ -count=1 -v` |
| **Estimated runtime** | ~20 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/ -run TestAgent -count=1 -v`
- **After every plan wave:** Run `go test ./internal/app/cmd/ -count=1 -v`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 20 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 51-01-01 | 01 | 1 | TMUX-01 | unit | `go test ./internal/app/cmd/ -run TestBuildAgentShellCommands -v` | Needs update | ⬜ pending |
| 51-01-02 | 01 | 1 | TMUX-02 | unit | `go test ./internal/app/cmd/ -run TestRunID -v` | ❌ W0 | ⬜ pending |
| 51-01-03 | 01 | 1 | TMUX-05 | unit | `go test ./internal/app/cmd/ -run TestAgentWait -v` | Needs update | ⬜ pending |
| 51-02-01 | 02 | 2 | TMUX-03 | unit | `go test ./internal/app/cmd/ -run TestAgentAttach -v` | ❌ W0 | ⬜ pending |
| 51-02-02 | 02 | 2 | TMUX-04 | unit | `go test ./internal/app/cmd/ -run TestAgentInteractive -v` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Update TestBuildAgentShellCommands to verify tmux wrapping
- [ ] Add TestAgentAttach for attach subcommand
- [ ] Add TestAgentInteractive for --interactive flag

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Live tmux attach while agent runs | TMUX-03 | Requires live sandbox + SSM | `km agent run g1 --prompt "..."` then `km agent attach g1` |
| Interactive mode opens tmux | TMUX-04 | Requires live sandbox + SSM | `km agent run g1 --prompt "..." --interactive` |
| Agent survives SSM disconnect | TMUX-01 | Requires live SSM session drop | Start agent, kill SSM session, verify tmux still running |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 20s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
