---
phase: 102
slug: github-bridge-agent-verbs-claude-and-codex-select-the-per-thread-agent-in-a-pr-comment-slack-phase-70-analog
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-08
---

# Phase 102 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib) |
| **Config file** | none — `go test ./...` |
| **Quick run command** | `go test ./pkg/github/bridge/... -run TestCommandParse -count=1` |
| **Full suite command** | `go test ./pkg/github/bridge/... ./internal/app/cmd/... -count=1` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/github/bridge/... -run TestCommandParse -count=1`
- **After every plan wave:** Run `go test ./pkg/github/bridge/... ./internal/app/cmd/... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 102-01-01 | 01 | 1 | GH-AGENT-VERB | unit | `go test ./pkg/github/bridge/... -run TestCommandParse -count=1` | ❌ W0 (extend) | ⬜ pending |
| 102-01-02 | 01 | 1 | GH-AGENT-VERB | unit | `go test ./pkg/github/bridge/... -run TestCommandParse -count=1` | ❌ W0 (extend) | ⬜ pending |
| 102-01-03 | 01 | 1 | GH-AGENT-VERB | unit | `go test ./pkg/github/bridge/... -run 'TestHandle_AgentVerb' -count=1` | ❌ W0 (new) | ⬜ pending |
| 102-02-01 | 02 | 1 | GH-AGENT-PERSIST | unit | `go test ./pkg/github/bridge/... -run TestGitHubThreadStore -count=1` | ❌ W0 (extend) | ⬜ pending |
| 102-03-01 | 03 | 2 | GH-AGENT-POLLER | manual | `km create` + inject envelope variants (poller bash) | N/A poller | ⬜ pending |
| 102-03-02 | 03 | 2 | GH-AGENT-SWITCH | manual | `km create` + cross-agent switch comment sequence | N/A poller | ⬜ pending |
| 102-03-03 | 03 | 2 | GH-AGENT-PROFILE | manual | `km create` (github-review) + `/codex` comment → helpful error | N/A poller | ⬜ pending |
| 102-04-01 | 04 | 1 | GH-AGENT-E2E | unit | `go test ./internal/app/cmd/... -run TestDoctorGitHubCommands -count=1` | ❌ W0 (extend) | ⬜ pending |
| 102-04-02 | 04 | 3 | GH-AGENT-E2E | E2E | UAT runbook (`km create` + real PR comment sequence) | N/A | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/github/bridge/commands_test.go` — extend `TestCommandParse` with: `/claude` recognized
  anywhere, `/codex` recognized, two-verb conflict (`AgentVerbConflict=true`), same-verb dedup,
  compose `/codex /patch fix bug` (agent=Codex, cmd=patch, args="fix bug"), code-block suppresses
  agent verb, agent verb stripped from `{{args}}`
- [ ] `pkg/github/bridge/webhook_handler_phase102_test.go` (new) — `TestHandle_AgentVerbConflict`
  (two verbs → PostComment conflict message, no SQS enqueue), `TestHandle_EnvelopeCarriesAgent`
  (single `/claude` → `envelope.Agent="claude"`), `/help` with agent verb → help reply lists agents
- [ ] `pkg/github/bridge/thread_store_test.go` — extend `TestGitHubThreadStore_LookupSandbox_Found`
  to assert returned `agentType`; `TestGitHubThreadStore_UpdateSession` to assert `agent_type`
  written in the UpdateItem expression
- [ ] `internal/app/cmd/doctor_github_commands_test.go` — extend the existing `help`-shadow test
  pattern for reserved `"claude"` and `"codex"` names → WARN

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| `EFFECTIVE_AGENT` precedence (verb > thread agent_type > profile default) | GH-AGENT-POLLER | Poller is bash heredoc in `userdata.go` — no unit harness (same as Phases 97/98/99) | `km create` Codex-capable profile + inject envelope variants; inspect journald + DDB row |
| No-verb + no stored agent_type → profile default (byte-identical to today) | GH-AGENT-SWITCH | Poller bash | `km create`; post no-verb comment; confirm profile-default agent dispatched |
| Cross-agent switch clears `RESUME_ARG` + fresh session + overwrites `agent_type` | GH-AGENT-SWITCH | Poller bash | `/codex` then `/claude` in same thread; confirm new session captured, no `--resume` of other agent |
| `/codex` on Claude-only `github-review` → helpful `km-github comment` (no stranded turn) | GH-AGENT-PROFILE | Poller bash + real comment | `km create profiles/github-review.yaml`; post `/codex …`; confirm helpful comment, no silent fail |
| Full thread continuity: `/codex /patch` dispatches Codex; no-verb follow-up continues Codex; `/claude` switches back | GH-AGENT-E2E | Requires real GitHub PR + bridge round-trip | UAT runbook two-comment sequence |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies (poller tasks documented manual-only with rationale)
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify (bridge/DDB/doctor all unit-tested)
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
