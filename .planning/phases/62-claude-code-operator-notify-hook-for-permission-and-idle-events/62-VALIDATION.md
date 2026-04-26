---
phase: 62
slug: claude-code-operator-notify-hook-for-permission-and-idle-events
status: planned
nyquist_compliant: true
wave_0_complete: false
created: 2026-04-26
updated: 2026-04-26
---

# Phase 62 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go test (`testing` stdlib), `package compiler` and `package compiler_test`, plus `package cmd` |
| **Config file** | None — standard `go test ./...` |
| **Quick run command** | `go test ./pkg/compiler/... ./internal/app/cmd/... -run "TestNotify\|TestUserDataNotify\|TestBuildAgentShellCommands_Notify\|TestBuildNotifySendCommands\|TestResolveNotifyFlags\|TestParse_CLISpec_Notify\|TestValidate_NotifyFields" -v` |
| **Full suite command** | `go test ./pkg/profile/... ./pkg/compiler/... ./internal/app/cmd/... -v` |
| **Estimated runtime** | ~30–60 seconds (compiler tests dominate; CLI tests are fast unit tests with no network) |

**Hook script test pattern:** Go test that shells out via `exec.Command("bash", hookScriptPath, "Notification")` with a stub `km-send` (PATH override to a temp script) and synthetic stdin/environment. No bats framework — matches existing Go-shell-out smoke-test convention.

---

## Sampling Rate

- **After every task commit:** Run quick run command (covers all `TestNotify*` and related tests, ~5–10s)
- **After every plan wave:** Run full suite command (~30–60s)
- **Before `/gsd:verify-work`:** Full repo suite green (`go test ./...`)
- **Max feedback latency:** ~10 seconds for per-task; ~60 seconds for per-wave

---

## Per-Task Verification Map

> Task IDs finalized 2026-04-26 by `/gsd:plan-phase 62`.

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 62-01-T1 | 01 | 1 | HOOK-01..05 (schema surface) | unit | `go test ./pkg/profile/... -run "TestParse_CLISpec_Notify\|TestValidate_NotifyFields" -v` | ❌ W0 | ⬜ pending |
| 62-01-T2 | 01 | 1 | HOOK-01..05 (REQUIREMENTS.md) | docs | `grep -c "HOOK-0" .planning/REQUIREMENTS.md` (expect ≥10) | ✅ existing file | ⬜ pending |
| 62-02-T1 | 02 | 2 | HOOK-01, HOOK-03 | unit | `go test ./pkg/compiler/... -run "TestUserDataNotifyHook\|TestUserDataNotifyEnvVars" -v` | ❌ W0 | ⬜ pending |
| 62-02-T2 | 02 | 2 | HOOK-02 | unit | `go test ./pkg/compiler/... -run TestUserDataNotifySettingsJSON -v` | ❌ W0 | ⬜ pending |
| 62-02-T3 | 02 | 2 | HOOK-05 (testdata only) | shell-syntax | `bash -n pkg/compiler/testdata/notify-hook-stub-km-send.sh` | ❌ W0 | ⬜ pending |
| 62-03-T1 | 03 | 3 | HOOK-05 (fixtures) | jq parse | `jq -e . pkg/compiler/testdata/notify-hook-fixture-{notification,stop}.json` | ❌ W0 | ⬜ pending |
| 62-03-T2 | 03 | 3 | HOOK-05 a–e + recipient + body-file | hook script (bash shelled out) | `go test ./pkg/compiler/... -run "TestNotifyHook_" -v` | ❌ W0 | ⬜ pending |
| 62-04-T1 | 04 | 2 | HOOK-04 (agent run path) | unit | `go test ./internal/app/cmd/... -run "TestBuildAgentShellCommands_Notify" -v` | ❌ W0 | ⬜ pending |
| 62-04-T2 | 04 | 2 | HOOK-04 (km shell path) | unit | `go test ./internal/app/cmd/... -run "TestBuildNotifySendCommands\|TestResolveNotifyFlags" -v` | ❌ W0 | ⬜ pending |
| 62-04-T3 | 04 | 2 | HOOK-04 (binary build) | smoke | `make build && ./km shell --help \| grep -cE 'notify-on-(permission\|idle)'` | n/a | ⬜ pending |
| 62-05-T1 | 05 | 4 | n/a (pre-flight) | smoke | `make build && ./km validate profiles/notify-test.yaml` | needs Plan 04 done | ⬜ pending |
| 62-05-T2..T7 | 05 | 4 | HOOK-01..05 (live UAT) | manual | (operator-driven; see plan) | n/a | ⬜ pending |
| 62-05-T8 | 05 | 4 | n/a (UAT log) | docs | `grep -q "UAT Outcomes" 62-05-SUMMARY.md` | n/a | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

**Requirement IDs (registered in REQUIREMENTS.md by Plan 01 Task 2):**

- **HOOK-01** — Compiler unconditionally writes `/opt/km/bin/km-notify-hook` script to sandbox during user-data execution
- **HOOK-02** — Compiler merges `Notification` and `Stop` hook entries into `~/.claude/settings.json`, preserving any user-supplied entries from `spec.execution.configFiles`
- **HOOK-03** — Compiler writes `/etc/profile.d/km-notify-env.sh` with `KM_NOTIFY_ON_PERMISSION` / `KM_NOTIFY_ON_IDLE` / `KM_NOTIFY_COOLDOWN_SECONDS` / `KM_NOTIFY_EMAIL` (with documented Plan 02 deviation: gates emit `0` or `1` whenever `Spec.CLI` is non-nil; cooldown and email are conditional on the matching field being set)
- **HOOK-04** — `km shell` and `km agent run` honor `--notify-on-permission` / `--notify-on-idle` (and `--no-*`) CLI flags, overriding profile defaults via env vars injected at SSM-launch time
- **HOOK-05** — `/opt/km/bin/km-notify-hook` honors gate env vars, cooldown, builds correct subjects/bodies for both events, calls km-send with `--body <file>`, never blocks Claude on send failure

---

## Wave 0 Requirements

These test files / testdata stubs do NOT exist yet. Each plan's first code task creates them BEFORE writing production code (TDD).

- [ ] `pkg/profile/types_test.go` (Plan 01 Task 1) — extend with `TestParse_CLISpec_Notify*` and `TestValidate_NotifyFields*` tests; covers schema parsing for HOOK-* fields.
- [ ] `pkg/compiler/userdata_notify_test.go` (Plan 02 Tasks 1+2) — new file, covers HOOK-01/02/03 following `pkg/compiler/userdata_test.go` pattern.
- [ ] `pkg/compiler/testdata/notify-hook-stub-km-send.sh` (Plan 02 Task 3) — stub `km-send` script, used by hook script tests via PATH override; records args + body file contents for assertion.
- [ ] `pkg/compiler/testdata/notify-hook-fixture-notification.json` (Plan 03 Task 1) — sample Notification stdin payload.
- [ ] `pkg/compiler/testdata/notify-hook-fixture-stop.json` (Plan 03 Task 1) — sample Stop stdin payload.
- [ ] `pkg/compiler/testdata/notify-hook-fixture-transcript.jsonl` (Plan 03 Task 1) — sample JSONL transcript with multiple assistant entries; the LAST one is the expected body.
- [ ] `pkg/compiler/notify_hook_script_test.go` (Plan 03 Task 2) — Go test driving bash-shelled hook script; covers HOOK-05 a–e + recipient override + body-file invariant.
- [ ] `internal/app/cmd/agent_test.go` (Plan 04 Task 1) — extend with `TestBuildAgentShellCommands_Notify*` tests; covers HOOK-04 agent run path.
- [ ] `internal/app/cmd/shell_test.go` (Plan 04 Task 2) — extend (or create) with `TestBuildNotifySendCommands*` and `TestResolveNotifyFlags*` tests; covers HOOK-04 km shell path.
- [ ] No framework install needed — `go test`, `bash`, `jq`, `make` already in CI.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end signed email reaches operator inbox | HOOK-01..05 (composite) | Requires real SES, real sandbox provision, real Claude session triggering Notification or Stop | (1) `km create profiles/notify-test.yaml`; (2) `km agent run <id> --prompt "rm -rf /etc/passwd" --wait`; (3) confirm signed email arrives in `KM_OPERATOR_EMAIL` inbox; (4) repeat with benign prompt to confirm idle email — full procedure in 62-05-PLAN.md Tasks 2–4 |
| `notificationEmailAddress` override routes correctly | HOOK-03 (recipient field) | Requires real SES delivery to verify recipient, not just env var presence | Procedure: 62-05-PLAN.md Task 5 |
| Cooldown behaves correctly across event types in real time | HOOK-05d | Test uses synthetic time; real-world clock-tick edge cases (NTP skew, container time drift) only surface in production | Procedure: 62-05-PLAN.md Task 6 |
| CLI flag injection actually flips env var inside running Claude process | HOOK-04 | Test mocks ssm.SendCommand; only a real SSM session against a real instance proves the env var reaches the running Claude subprocess | Procedure: 62-05-PLAN.md Task 7 (inspects `/proc/<PID>/environ`) |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify (or are explicit manual checkpoints in Plan 05).
- [x] Sampling continuity: no 3 consecutive automated tasks without verify (Plan 05 is mostly manual but is the FINAL wave; pre-Plan-05 waves are 100% automated-verified).
- [x] Wave 0 covers all MISSING references (8 new test files / testdata stubs listed above).
- [x] No watch-mode flags (Go test runs synchronously).
- [x] Feedback latency < 60s for full suite.
- [x] `nyquist_compliant: true` set in frontmatter.

**Approval:** approved (planner finalized task IDs and flipped `nyquist_compliant: true` on 2026-04-26)
