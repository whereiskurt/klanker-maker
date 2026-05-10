---
phase: 78
slug: km-agent-auth-ssm-mediated-oauth-login-for-claude-and-codex-clis-inside-sandboxes-paste-code-for-claude-port-forward-1455-for-codex
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-10
---

# Phase 78 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go stdlib `testing` (project standard — no external test framework) |
| **Config file** | none — `go test ./...` |
| **Quick run command** | `go test ./internal/app/cmd/... -run TestAgentAuth -v` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~5s for quick run; ~30s for full suite |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/... -run TestAgentAuth -v`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 78-01-01 | 01 | 1 | AUTH-01 | unit | `go test ./internal/app/cmd/... -run TestAgentAuth_FlagParsing` | ❌ W0 | ⬜ pending |
| 78-01-02 | 01 | 1 | AUTH-02 | unit | `go test ./internal/app/cmd/... -run TestAgentAuth_MutuallyExclusive` | ❌ W0 | ⬜ pending |
| 78-01-03 | 01 | 1 | AUTH-03 | unit | `go test ./internal/app/cmd/... -run TestAgentAuth_DefaultClaude` | ❌ W0 | ⬜ pending |
| 78-01-04 | 01 | 1 | AUTH-04 | unit | `go test ./internal/app/cmd/... -run TestAgentAuth_SandboxIDResolution` | ❌ W0 | ⬜ pending |
| 78-01-05 | 01 | 1 | AUTH-05 | unit | `go test ./internal/app/cmd/... -run TestAgentAuth_ConflictRefuse` | ❌ W0 | ⬜ pending |
| 78-01-06 | 01 | 1 | AUTH-06 | unit | `go test ./internal/app/cmd/... -run TestVerifyCredentialsWritten_Success` | ❌ W0 | ⬜ pending |
| 78-01-07 | 01 | 1 | AUTH-07 | unit | `go test ./internal/app/cmd/... -run TestVerifyCredentialsWritten_Missing` | ❌ W0 | ⬜ pending |
| 78-01-08 | 01 | 1 | AUTH-13 | unit | `go test ./internal/app/cmd/... -run TestBuildClaudeAuthArgs` | ❌ W0 | ⬜ pending |
| 78-01-09 | 01 | 1 | AUTH-11 | unit | `go test ./internal/app/cmd/... -run TestShellCmd_NoBedrock_CredentialsMissingHint` | ❌ W0 | ⬜ pending |
| 78-01-10 | 01 | 1 | AUTH-12 | unit | `go test ./internal/app/cmd/... -run TestAgentRun_NoBedrock_CredentialsMissingHint` | ❌ W0 | ⬜ pending |
| 78-02-01 | 02 | 2 | AUTH-08 | unit | `go test ./internal/app/cmd/... -run TestProbeCodexPort_Primary` | ❌ W0 | ⬜ pending |
| 78-02-02 | 02 | 2 | AUTH-09 | unit | `go test ./internal/app/cmd/... -run TestProbeCodexPort_Fallback` | ❌ W0 | ⬜ pending |
| 78-02-03 | 02 | 2 | AUTH-10 | unit | `go test ./internal/app/cmd/... -run TestProbeCodexPort_BothInUse` | ❌ W0 | ⬜ pending |
| 78-02-04 | 02 | 2 | INT-01 | manual | manual UAT Scenario B | manual-only | ⬜ pending |
| 78-02-05 | 02 | 2 | INT-02 | manual | manual UAT Scenario A | manual-only | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

*Task IDs are placeholders — final IDs assigned by gsd-planner. Mapping is illustrative: AUTH-01..AUTH-07/AUTH-11..AUTH-13 cluster in Plan 01 (Wave 1 — `--claude` path); AUTH-08..AUTH-10 cluster in Plan 02 (Wave 2 — `--codex` path).*

---

## Wave 0 Requirements

- [ ] `internal/app/cmd/agent_auth.go` — production code created in Wave 1 (Plan 01)
- [ ] `internal/app/cmd/agent_auth_test.go` — unit tests for AUTH-01 through AUTH-13 (created alongside agent_auth.go in Wave 1; AUTH-08..AUTH-10 added in Wave 2)
- [ ] No framework install needed — `go test` already works in project

*Existing infrastructure (`mockAgentSSM`, `fakeFetcher`, `vsCodeSSMMock`) covers SSM mocking needs — no new test scaffolding.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end `--claude` happy path: SSM session → URL → paste code → token written | INT-02 | Requires real AWS, EC2, browser, and OAuth code exchange. No feasible mock. | UAT Scenario A (see RESEARCH.md § Manual UAT Scenarios) |
| End-to-end `--codex` happy path: port-forward → SSM exec → browser → token written | INT-01 | Requires real AWS, EC2, browser, and codex OAuth callback through SSM tunnel. | UAT Scenario B (see RESEARCH.md § Manual UAT Scenarios) |
| Missing-credentials hint surfaces in `km shell --no-bedrock` | AUTH-11 (auto) + manual confirm | Unit test verifies hint logic; manual confirms operator-facing wording is clear | UAT Scenario C (see RESEARCH.md § Manual UAT Scenarios) |
| Port-collision graceful error when 1455 + 1457 in use locally | AUTH-10 (auto) + manual confirm | Unit test verifies error path; manual confirms operator can recover | UAT Scenario D (see RESEARCH.md § Manual UAT Scenarios) |
| Stopped/paused sandbox produces clear error | (covered by `ResolveSandboxID` + state check) | Reuses existing pre-flight pattern from `km shell`/`km vscode start` — manual confirms inheritance is correct | UAT Scenario E (see RESEARCH.md § Manual UAT Scenarios) |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (`agent_auth.go` + `agent_auth_test.go`)
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter (after planner finalizes task IDs)

**Approval:** pending
