---
phase: 92
slug: profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-31
---

# Phase 92 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing package (stdlib) — no external test framework |
| **Config file** | none (`go test ./...`) |
| **Quick run command** | `go test ./pkg/profile/... ./pkg/compiler/...` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~60 seconds (quick), ~3–5 minutes (full) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/profile/... ./pkg/compiler/...`
- **After every plan wave:** Run `go test ./...` + `scripts/validate-all-profiles.sh`
- **Before `/gsd:verify-work`:** Full suite must be green AND all 20 profile YAMLs pass `km validate`
- **Max feedback latency:** ~60 seconds (quick), ~5 minutes (full + profile sweep)

---

## Per-Task Verification Map

This map evolves during planning — each PLAN.md task should reference one of the VC# entries below (e.g. `verifies: VC-3`) and the planner fills in the per-task row.

| VC# | Criterion | Test Type | Target File | Gating Wave | Failure Mode | Success Mode |
|-----|-----------|-----------|-------------|-------------|--------------|--------------|
| VC-1 | `go test ./...` passes across all waves | build + unit | — | All waves | compile error or test failure | zero failures |
| VC-2 | `km validate` passes on all 20 fixtures; rejects stale-key fixtures | unit | `pkg/profile/validate_test.go` + new `pkg/profile/validate_legacy_keys_test.go` | Wave 1 (reject), Waves 1/3/5 (pass) | validator accepts legacy key OR rejects valid new key | clear error on `identity:` / `agent.maxConcurrentTasks:` / `cli.notifySlackEnabled:` |
| VC-3 | Userdata byte-identity: `profiles/learn.v2.yaml` pre- vs post-Wave-5 | golden | `pkg/compiler/userdata_phase92_byte_identity_test.go` | Wave 0 (RED capture), Wave 3+ (GREEN) | compiled userdata differs from pre-phase baseline | string comparison passes |
| VC-4 | IAM Terraform byte-identity post-Wave-1 | golden | `pkg/compiler/security_phase92_byte_identity_test.go` | Wave 0 (RED capture), Wave 1 (GREEN) | `aws_iam_role.max_session_duration` or region_lock HCL differs | string comparison passes |
| VC-5 | Synthesizer golden tests: learn.v2, dc34, locked, codex fixtures | golden | `pkg/compiler/agent_claude_golden_test.go`, `pkg/compiler/agent_codex_golden_test.go` | Wave 0 (RED), Wave 5 (GREEN) | synthesized settings.json or config.toml differs from fixture | byte-identical to golden file |
| VC-6 | Mixed-mode rejection: `agent.claude.tools.autoApprove` + `configFiles[".claude/settings.json"]` → error | unit | `pkg/profile/validate_mixed_settings_test.go` | Wave 0 (RED), Wave 4 (GREEN) | validator accepts conflicting fields | `ValidationError` with clear path |
| VC-7 | Inheritance fix: child-only-transcript flag inherits parent perSandbox | unit | `pkg/profile/inherit_notification_test.go` | Wave 0 (RED), Wave 2 (GREEN) | child's `notification.slack.perSandbox` is zero/false | merged result has parent's `notification.slack.perSandbox: true` |
| VC-8 | `km doctor` passes | integration/UAT | — | Wave 6 | any doctor check fails | all checks green |
| VC-9 | `make build && km init --sidecars` succeeds | integration | — | Wave 6 | build error or Lambda deploy fails | sidecars deployed, km binary updated |
| VC-10 | Wave 6 UAT scenarios (9 scenarios) all pass | UAT | `92-06-UAT.md` | Wave 6 | any scenario fails | all 9 scenarios verified |
| VC-11 | All 20 profile YAMLs pass `km validate` via `scripts/validate-all-profiles.sh` | script | `scripts/validate-all-profiles.sh` | Wave 1 (script added), enforced every wave | any profile fails km validate | script exits 0, zero failures |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

The planner expands this into per-task rows during plan creation. Each task that produces verifiable code MUST cite at least one VC#.

---

## Wave 0 Requirements

Wave 0 creates RED test stubs and captures golden baselines from pre-change main. Wave 1 onward turns them GREEN.

- [ ] `pkg/compiler/userdata_phase92_byte_identity_test.go` — RED stub + golden capture for `profiles/learn.v2.yaml` userdata (VC-3)
- [ ] `pkg/compiler/security_phase92_byte_identity_test.go` — RED stub + golden capture for IAM HCL output (VC-4)
- [ ] `pkg/compiler/agent_claude_golden_test.go` — RED stubs for `synthesizeClaudeSettings()` across 4+ representative fixtures (learn.v2, dc34, locked, codex) (VC-5)
- [ ] `pkg/compiler/agent_codex_golden_test.go` — RED stub for `synthesizeCodexConfig()` (VC-5)
- [ ] `pkg/profile/inherit_notification_test.go` — RED stub: child-only transcript flag must inherit parent `perSandbox` (VC-7)
- [ ] `pkg/profile/validate_mixed_settings_test.go` — RED stub: autoApprove + inlined configFiles must error (VC-6)
- [ ] `.planning/research/codex-config-toml.md` — research spike output (already created)

`scripts/validate-all-profiles.sh` is intentionally a Wave 1 deliverable per PRD §Profile Inventory (lives in the wave that introduces the first stale-key risk).

---

## Manual-Only Verifications

| Behavior | Criterion | Why Manual | Test Instructions |
|----------|-----------|------------|-------------------|
| Real AWS apply for IAM role | VC-9 | requires AWS account credentials + terragrunt state | `make build && km init --sidecars --dry-run=false` against operator's account |
| Real Slack notify-hook firing on idle event | VC-10 (UAT #6) | requires live Slack workspace + bridge Lambda | `km create profiles/learn.v2.yaml`; trigger idle event; observe Slack post |
| Slack inbound mention-only filtering in shared channel | VC-10 (UAT #7) | requires live Slack workspace + Phase 91 bridge | post in `#km-notifications` without mention → no agent dispatch; with mention → dispatch |
| Claude Code denied-tool refusal in sandbox | VC-10 (UAT #5) | requires live Claude Code agent in SSM session | `km agent run <id> --prompt "use WebFetch..."` against profile with `agent.claude.tools.deny: [WebFetch]` |
| Per-message `codex:` prefix routing in `agent.default: claude` profile | VC-10 (UAT #8) | requires live Slack inbound → SSM dispatch | Post `codex: ls` in `#sb-<id>` channel; observe Codex agent answer |
| `km create` real provisioning + SSM cat of `~/.claude/settings.json` | VC-10 (UAT #4) | requires EC2 launch + SSM session | `km create profiles/learn.v2.yaml --no-bedrock`; `km shell <id>` → `cat /home/sandbox/.claude/settings.json` matches synthesizer output |

All other phase behaviors have automated verification via `go test ./...`, `km validate`, and `scripts/validate-all-profiles.sh`.

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (6 test stubs above)
- [ ] No watch-mode flags (one-shot `go test` only)
- [ ] Feedback latency < 60s (quick), < 5min (full + profile sweep)
- [ ] `nyquist_compliant: true` set in frontmatter after Wave 0 lands

**Approval:** pending
