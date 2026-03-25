---
phase: 20
slug: anthropic-api-metering-claude-code-ai-spend-tracking
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-24
---

# Phase 20 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib) |
| **Config file** | none — standard `go test ./...` |
| **Quick run command** | `go test ./sidecars/http-proxy/httpproxy/... ./pkg/terragrunt/... -v` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~20 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./sidecars/http-proxy/httpproxy/... ./pkg/terragrunt/... ./internal/app/cmd/... -v`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 20 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 20-01-01 | 01 | 1 | BUDG-10 | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestAnthropicAPI -v` | ❌ W0 | ⬜ pending |
| 20-01-02 | 01 | 1 | BUDG-10 | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestAnthropicRate -v` | ❌ W0 | ⬜ pending |
| 20-02-01 | 02 | 1 | OPER-01 | unit | `go test ./pkg/terragrunt/... -run TestRunner -v` | ❌ W0 | ⬜ pending |
| 20-02-02 | 02 | 1 | OPER-01 | unit | `go test ./internal/app/cmd/... -run TestVerbose -v` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `sidecars/http-proxy/httpproxy/anthropic_test.go` — test file for Anthropic response parsing (non-streaming, SSE, model ID extraction, blocked response)
- [ ] `pkg/terragrunt/runner_test.go` — tests for quiet/verbose output modes (if not existing)
- [ ] Additional test cases in `internal/app/cmd/` for --verbose flag propagation

*(Framework install not needed — Go stdlib `testing` already in use)*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Anthropic API MITM intercepts real Claude Code calls | BUDG-10 | Requires running sandbox with Claude Code + Anthropic API key | Run Claude Code in sandbox, verify DynamoDB spend increments |
| Terragrunt output suppressed in real km create | OPER-01 | Requires real AWS + terragrunt | Run `km create` without --verbose, verify clean summary output |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 20s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
