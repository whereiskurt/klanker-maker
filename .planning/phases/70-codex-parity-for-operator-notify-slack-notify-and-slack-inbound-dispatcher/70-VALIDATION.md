---
phase: 70
slug: codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-22
---

# Phase 70 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` (Go 1.x — repo standard) + bash heredoc smoke tests inlined in Go test files (existing pattern: `internal/app/cmd/shell_learn_test.go`) |
| **Config file** | none — Go testing is built-in |
| **Quick run command** | `go test ./pkg/profile/... ./pkg/compiler/... ./internal/app/cmd/... -run '70' -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~120 seconds full suite; ~15 seconds Phase-70-scoped quick run |

---

## Sampling Rate

- **After every task commit:** Run the Phase-70-scoped quick command (touches the packages this phase modifies — profile schema, compiler, doctor checks, agent run, km-slack)
- **After every plan wave:** Run full suite
- **Before `/gsd:verify-work`:** Full suite must be green AND the manual UAT in Plan 70-09 must produce a signed `70-VERIFY.md`
- **Max feedback latency:** 15 seconds (quick run) / 120 seconds (full suite)

---

## Per-Task Verification Map

This phase has 10 plans (70-00 through 70-09). Verification maps to plans, not individual tasks; each plan's task list lives in its PLAN.md. The map below summarises how each Success Criterion (SC) lands a verification surface.

| Plan | Wave | SC covered | Test Type | Automated Command | File Exists | Status |
|------|------|------------|-----------|-------------------|-------------|--------|
| 70-00 | 0 | spike — gates SC-1..6 | manual spike | (operator runs spike steps; no committed test) | N/A spike | ⬜ pending |
| 70-01 | 1 | SC-1 (schema bit), validation | unit | `go test ./pkg/profile -run TestCLISpec_Agent -count=1` | ❌ W0 — new test in `pkg/profile/types_test.go` | ⬜ pending |
| 70-02 | 2 | SC-1 (config.toml file emitted), partial SC-4..6 (env var emitted) | compiler unit / golden | `go test ./pkg/compiler -run 'TestUserdata_CodexConfig\|TestUserdata_KMAgentEnv' -count=1` | ❌ W0 — new test in `pkg/compiler/userdata_codex_test.go` | ⬜ pending |
| 70-03 | 2 | SC-2, SC-3 (hook behavior) | bash smoke (Go-wrapped) | `go test ./pkg/compiler -run 'TestNotifyHook_CodexPermission\|TestNotifyHook_LastAssistantMessage' -count=1` | ❌ W0 — new test in `pkg/compiler/notify_hook_test.go` | ⬜ pending |
| 70-04 | 2 | (sidecar layer; supports SC-10) | unit | `go test ./sidecars/km-slack/... ./pkg/slack/... -run '70' -count=1` | ❌ W0 — new tests in `sidecars/km-slack/main_test.go` + `pkg/slack/payload_test.go` (bridge action) | ⬜ pending |
| 70-05 | 3 | SC-4, SC-5, SC-6, partial SC-1 (toml present) | compiler unit + DDB integration | `go test ./pkg/compiler -run 'TestPoller_CodexDispatch\|TestPoller_AgentTypeWriteback\|TestPoller_LastAssistantMsg' -count=1` | ❌ W0 — new test in `pkg/compiler/userdata_slack_inbound_test.go` (extend existing) | ⬜ pending |
| 70-06 | 4 | SC-8, SC-9, SC-10 | compiler unit (table-driven parser) + DDB integration | `go test ./pkg/compiler -run 'TestPoller_PrefixParser\|TestPoller_CrossAgentSwitch' -count=1` | ❌ W0 — new test in `pkg/compiler/userdata_prefix_test.go` | ⬜ pending |
| 70-07 | 4 | SC-7 | unit | `go test ./internal/app/cmd -run 'TestDoctor_CodexHookPresent\|TestDoctor_AgentTypeConsistency' -count=1` | ❌ W0 — new test in `internal/app/cmd/doctor_codex_test.go` | ⬜ pending |
| 70-08 | 5 | (docs — manual review) | none (doc verification) | `test -f docs/codex-parity.md && grep -q "spec.cli.agent" CLAUDE.md` | N/A — bash one-liner | ⬜ pending |
| 70-09 | 5 | All 10 SCs (E2E) | manual UAT | (operator runs the 9 demo flows; produces `70-VERIFY.md`) | N/A — manual | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Each plan's first task creates the test file with a single passing stub (e.g., `TestAgent_Stub` returning early) so subsequent task commits have a green-baseline file to extend. This follows the Phase 67 / Phase 69 pattern.

Wave 0 test stubs to create:

- [ ] `pkg/profile/types_test.go` — extend existing file with `TestCLISpec_Agent_Stub` (covers SC-1 schema bit)
- [ ] `pkg/compiler/userdata_codex_test.go` — new file with `TestUserdata_CodexConfig_Stub` (covers SC-1 file emission, SC-4..6 env var)
- [ ] `pkg/compiler/notify_hook_test.go` — new file with `TestNotifyHook_Stub` (covers SC-2, SC-3)
- [ ] `sidecars/km-slack/main_test.go` — extend or create with `TestKMSlack_NewMessage_Stub`, `TestKMSlack_Permalink_Stub`, `TestKMSlack_Update_Stub` (covers SC-10 dependencies)
- [ ] `pkg/slack/payload_test.go` — extend existing with `TestPayload_PermalinkAction_Stub`, `TestPayload_UpdateAction_Stub` (bridge action constants; covers SC-10)
- [ ] `pkg/compiler/userdata_slack_inbound_test.go` — extend existing file with `TestPoller_CodexDispatch_Stub` (covers SC-4..6)
- [ ] `pkg/compiler/userdata_prefix_test.go` — new file with `TestPoller_PrefixParser_Stub` (covers SC-8, SC-9, SC-10)
- [ ] `internal/app/cmd/doctor_codex_test.go` — new file with `TestDoctor_CodexHookPresent_Stub` (covers SC-7)

All stubs land in Plan 70-01..70-07's first task (per plan). Plan 70-00 (spike) and 70-08 (docs) and 70-09 (UAT) have no Wave 0 stub deliverables — spike is throwaway, docs are manually reviewed, UAT is manual.

---

## Manual-Only Verifications

| Behavior | SC | Why Manual | Test Instructions |
|----------|----|------------|-------------------|
| Operator email arrives with assistant reply | SC-2 | SES email delivery requires live AWS + mailbox; can't be mocked end-to-end | Plan 70-09 UAT step 2 — operator runs `km agent run --codex --prompt "What model are you?"` and checks inbox |
| Slack post appears in `#sb-x` channel | SC-2, SC-4, SC-5, SC-8, SC-9, SC-10 | Requires live Slack workspace + km-slack bridge | Plan 70-09 UAT steps 2, 4, 5, 7, 8, 9 — operator visually inspects Slack threads |
| Codex auto-approves with `--dangerously-bypass-approvals-and-sandbox` | SC-3 | Codex CLI behavior; only verifiable end-to-end | Plan 70-09 UAT step 3 — operator triggers a `PermissionRequest` and confirms run completes |
| Cross-agent thread permalink renders correctly in Slack | SC-10 | Slack's permalink rendering is platform-side | Plan 70-09 UAT step 9 — operator inspects the new top-level message and the handoff post |
| Codex resume preserves session context across turns | SC-5 | Requires live Codex CLI session state | Plan 70-09 UAT step 5 — operator's second prompt references prior turn's content |
| `km doctor` checks return green/red as expected | SC-7 | Requires real running sandboxes with DDB rows | Plan 70-09 UAT step 6 — operator runs `km doctor` on both agent-types |
| Cross-agent old-thread resumability after switch | SC-10 | Requires the full two-thread DDB state | Plan 70-09 UAT step 9 — operator posts a reply in the old thread (no prefix) and confirms claude resumes |

---

## Validation Sign-Off

- [ ] All plans have `<automated>` verify or Wave 0 dependencies (UAT plan is manual by design)
- [ ] Sampling continuity: no 3 consecutive plans without automated verify (70-00 spike + 70-08 docs + 70-09 UAT are the only manual ones; not consecutive in execution wave order)
- [ ] Wave 0 covers all MISSING references (8 stub files listed)
- [ ] No watch-mode flags (all commands use `-count=1`)
- [ ] Feedback latency < 15s (Phase-70-scoped quick run)
- [ ] `nyquist_compliant: true` set in frontmatter after planner produces the per-plan task lists

**Approval:** pending
