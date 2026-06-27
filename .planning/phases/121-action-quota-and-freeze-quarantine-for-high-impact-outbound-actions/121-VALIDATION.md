---
phase: 121
slug: action-quota-and-freeze-quarantine-for-high-impact-outbound-actions
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-27
---

# Phase 121 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` package (standard) |
| **Config file** | none — per-package, `go test ./...` |
| **Quick run command** | `go test ./pkg/quota/... ./pkg/aws/... ./sidecars/http-proxy/httpproxy/... -count=1 -timeout 60s` |
| **Full suite command** | `go test ./... -count=1 -timeout 600s` |
| **Estimated runtime** | quick ~30s · full ~300–500s |

> Capture the command's OWN exit code (redirect + `$?`, `PIPESTATUS`, or `set -o pipefail`) — `go test | tail` masks a real FAIL with tail's 0 (memory `feedback_check_go_test_exit_not_pipe`). Always pass `-timeout 600s` on the full suite.

---

## Sampling Rate

- **After every task commit:** Run the quick run command.
- **After every plan wave:** Run the full suite command.
- **Before `/gsd:verify-work`:** Full suite green + live UAT items 1–6 below executed.
- **Max feedback latency:** ~30 seconds (quick), ~500 seconds (full).

---

## Per-Task Verification Map

Requirement IDs derived from CONTEXT.md success criteria (see RESEARCH.md § Validation Architecture). Plan/Wave/Task columns are filled by the planner.

| Req ID | Behavior | Test Type | Automated Command | File Exists | Status |
|--------|----------|-----------|-------------------|-------------|--------|
| QUO-01 | `pkg/quota.Record` fixed-window bucket math (`epoch/3600`, `epoch/86400`) | unit | `go test ./pkg/quota/... -run TestRecord` | ❌ W0 | ⬜ pending |
| QUO-02 | Resolution: profile → install default → unlimited (per-window precedence) | unit | `go test ./pkg/quota/... -run TestResolveLimits` | ❌ W0 | ⬜ pending |
| QUO-03 | `tripped=true` when any window exceeds its limit | unit | `go test ./pkg/quota/... -run TestDecision` | ❌ W0 | ⬜ pending |
| QUO-04 | Atomic `ADD` (not read-modify-write) in the UpdateItem expression | unit | `go test ./pkg/quota/... -run TestAtomicADD` | ❌ W0 | ⬜ pending |
| QUO-05 | `lifetime` rows have no TTL; `hour`/`day` rows TTL ~2h/~2d | unit | `go test ./pkg/quota/... -run TestTTL` | ❌ W0 | ⬜ pending |
| PRX-01 | Proxy classifies `POST api.github.com /repos/*/pulls` → `github_pr` (+ comments/reviews) | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestClassifyGitHub` | ❌ W0 | ⬜ pending |
| PRX-02 | Proxy classifies SES `SendEmail`/`SendRawEmail*` → `email_send` | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestClassifySES` | ❌ W0 | ⬜ pending |
| PRX-03 | Proxy does NOT count bridge Function URL POST (`*.lambda-url.*.on.aws`) as `slack_post` | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestNoDoubleCount` | ❌ W0 | ⬜ pending |
| BRG-01 | Slack bridge calls `quota.Record` for `ActionPost`/`ActionUpload`; 429 in BLOCK mode | unit | `go test ./pkg/slack/bridge/... -run TestQuotaRecord` | ❌ W0 | ⬜ pending |
| BRG-02 | Bridge checks `action_frozen`; refuses dispatch + posts in-thread frozen notice | unit | `go test ./pkg/slack/bridge/... -run TestFrozenDispatch` | ❌ W0 | ⬜ pending |
| BRG-03 | `FetchByChannel` reads `action_limits` + `action_frozen` from the DDB item | unit | `go test ./pkg/slack/bridge/... -run TestFetchByChannel` | ✅ extend | ⬜ pending |
| H1-01 | H1 bridge calls `quota.Record` for `h1_comment`; same frozen/notice path | unit | `go test ./pkg/h1/bridge/... -run TestQuotaRecord` | ❌ W0 | ⬜ pending |
| META-01 | `SandboxMetadata` round-trip: new attrs survive marshal→unmarshal | unit | `go test ./pkg/aws/... -run TestSandboxMetadataRoundTrip` | ✅ extend | ⬜ pending |
| META-02 | `marshalSandboxItem` emits `action_frozen`/`frozen_*`/`action_limits` | unit | `go test ./pkg/aws/... -run TestMarshalFrozen` | ❌ W0 | ⬜ pending |
| ALR-01 | Alerter fires once per (sandbox,action,window) — `alert_sent` conditional guard | unit | `go test ./cmd/km-quota-alerter/... -run TestIdempotentAlert` | ❌ W0 | ⬜ pending |
| INIT-01 | `regionalModules()` includes the new table + alerter (before `ses`) | unit | `go test ./internal/app/cmd/... -run TestRunInitPlan_ModuleOrder` | ✅ update count | ⬜ pending |
| INIT-02 | `lambdaBuilds()` includes `km-quota-alerter` | unit | `go test ./internal/app/cmd/... -run TestQuotaAlerterBuildListMembership` | ❌ W0 | ⬜ pending |
| CFG-01 | `limits:` km-config key NOT silently dropped (v2→v merge-list) | unit | `go test ./internal/app/config/... -run TestLimitsConfigLoaded` | ❌ W0 | ⬜ pending |
| PROF-01 | `spec.limits` parses + validates; JSON schema `additionalProperties:false` | unit | `go test ./pkg/profile/... -run TestSpecLimits` | ❌ W0 | ⬜ pending |
| CMP-01 | Compiler emits resolved limits into userdata (proxy env) + `action_limits` attr | unit | `go test ./pkg/compiler/... -run TestActionLimitsEmission` | ❌ W0 | ⬜ pending |
| CLI-01 | `km freeze <id>` writes `action_frozen=true` + `frozen_*` via atomic UpdateItem | unit | `go test ./internal/app/cmd/... -run TestRunFreeze` | ❌ W0 | ⬜ pending |
| CLI-02 | `km unlock <id>` clears `action_frozen` alongside the safety-lock | unit | `go test ./internal/app/cmd/... -run TestRunUnlockLatchAware` | ❌ W0 | ⬜ pending |
| CLI-03 | `km list`/`km status` render `FROZEN`; `km doctor` surfaces frozen + new table | unit | `go test ./internal/app/cmd/... -run 'TestList.*Frozen|TestDoctor.*Frozen'` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/quota/quota_test.go` — stubs for QUO-01…QUO-05 (window math, resolution, atomicity, TTL)
- [ ] `pkg/quota/` DDB mock — reuse the `pkg/aws/budget_test.go` `ADD count 1` mock pattern
- [ ] `internal/app/cmd/init_plan_test.go:439` — bump hardcoded module count `24` → final count (table + alerter ⇒ likely `26`); **must update in lockstep with the `regionalModules()` edit or the test reds on every run**
- [ ] `sidecars/http-proxy/httpproxy/*_test.go` — classification stubs (PRX-01…PRX-03), incl. the double-count exclusion
- [ ] `pkg/slack/bridge/events_handler_frozen_test.go` — BRG-02 frozen-dispatch
- [ ] `cmd/km-quota-alerter/*_test.go` — ALR-01 idempotency
- [ ] `internal/app/cmd/freeze_test.go` — CLI-01 + CLI-02

*Existing infrastructure (Go `testing`) covers the rest; no framework install needed.*

---

## Manual-Only Verifications

These require a live sandbox + live Slack/GitHub/SES integration — Go unit tests verify the code calls the right paths, but only live UAT confirms the rendered external behavior. SKILL.md-embedded bash and live Slack/HMAC behaviors need live UAT, not Go goldens (memories `project_skill_bash_needs_live_uat`, `project_slack_bridge_inbound_e2e_and_status_attr`).

| Behavior | Req | Why Manual | Test Instructions |
|----------|-----|------------|-------------------|
| In-thread trip notice (bridge, chat trip) | BRG-01/02 | Rendered Slack message in the live thread | Post Slack messages to trip `slack_post.perHour` → verify a threaded "⚠️ Quota reached" notice in the same thread |
| Channel-level notice (alerter, proxy trip) | ALR-01 | DDB-Stream→Lambda→Slack, observe live lag | `km-github comment` until `github_comment` trips → alerter posts to the sandbox's main channel |
| Freeze gate — inbound dispatch refused | CLI-01/BRG-02 | End-to-end latch + Slack content | `km freeze <id>` → send Slack msg → bridge refuses dispatch + posts control-plane frozen notice |
| SES MITM new path | PRX-02 | SES not previously MITM'd; confirm AWS CLI honors proxy CA | `km-send` from a live sandbox with `email_send` limit=1 → 2nd send blocked + counted |
| `km unlock` clears freeze | CLI-02 | Cross-attr clear + dispatch resume | `km freeze <id>` then `km unlock <id>` → both `locked` and `action_frozen` cleared; Slack dispatch resumes |
| Alerter idempotency | ALR-01 | Conditional-write race under live Streams | Trip a limit twice rapidly → exactly ONE operator SES email |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (incl. the module-count bump)
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s (quick) / 500s (full)
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
