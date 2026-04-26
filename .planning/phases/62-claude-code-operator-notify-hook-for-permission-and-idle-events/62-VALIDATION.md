---
phase: 62
slug: claude-code-operator-notify-hook-for-permission-and-idle-events
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-26
---

# Phase 62 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go test (`testing` stdlib), `package compiler` and `package compiler_test`, plus `package cmd` |
| **Config file** | None — standard `go test ./...` |
| **Quick run command** | `go test ./pkg/compiler/... ./internal/app/cmd/... -run "TestNotify\|TestUserDataNotify\|TestBuildAgentShellCommands_Notify\|TestShellCmd_Notify" -v` |
| **Full suite command** | `go test ./pkg/compiler/... ./internal/app/cmd/... -v` |
| **Estimated runtime** | ~30–60 seconds (compiler tests dominate; CLI tests are fast unit tests with no network) |

**Hook script test pattern:** Go test that shells out via `exec.Command("bash", hookScriptPath, "Notification")` with a stub `km-send` (PATH override to a temp script) and synthetic stdin/environment. No bats framework needed — matches existing Go-shell-out smoke-test convention.

---

## Sampling Rate

- **After every task commit:** Run quick run command (covers all `TestNotify*` and related tests, ~5–10s)
- **After every plan wave:** Run full suite command (~30–60s)
- **Before `/gsd:verify-work`:** Full repo suite green (`go test ./...`)
- **Max feedback latency:** ~10 seconds for per-task; ~60 seconds for per-wave

---

## Per-Task Verification Map

> Plan IDs (XX-YY) are placeholders until `/gsd:plan-phase` produces PLAN.md files. Updated by planner during plan creation.

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 62-01-XX | 01 | 1 | HOOK-01 | unit | `go test ./pkg/compiler/... -run TestUserDataNotifyHook -v` | ❌ W0 | ⬜ pending |
| 62-01-XX | 01 | 1 | HOOK-02 | unit | `go test ./pkg/compiler/... -run TestUserDataNotifySettingsJSON -v` | ❌ W0 | ⬜ pending |
| 62-01-XX | 01 | 1 | HOOK-03 | unit | `go test ./pkg/compiler/... -run TestUserDataNotifyEnvVars -v` | ❌ W0 | ⬜ pending |
| 62-02-XX | 02 | 2 | HOOK-04a | unit | `go test ./internal/app/cmd/... -run TestShellCmd_NotifyFlags -v` | ❌ W0 | ⬜ pending |
| 62-02-XX | 02 | 2 | HOOK-04b | unit | `go test ./internal/app/cmd/... -run TestBuildAgentShellCommands_Notify -v` | ❌ W0 | ⬜ pending |
| 62-03-XX | 03 | 2 | HOOK-05a | hook script (bash shelled out) | `go test ./pkg/compiler/... -run TestNotifyHook_GatedOff -v` | ❌ W0 | ⬜ pending |
| 62-03-XX | 03 | 2 | HOOK-05b | hook script (bash shelled out) | `go test ./pkg/compiler/... -run TestNotifyHook_Notification -v` | ❌ W0 | ⬜ pending |
| 62-03-XX | 03 | 2 | HOOK-05c | hook script (bash shelled out) | `go test ./pkg/compiler/... -run TestNotifyHook_Stop -v` | ❌ W0 | ⬜ pending |
| 62-03-XX | 03 | 2 | HOOK-05d | hook script (bash shelled out) | `go test ./pkg/compiler/... -run TestNotifyHook_Cooldown -v` | ❌ W0 | ⬜ pending |
| 62-03-XX | 03 | 2 | HOOK-05e | hook script (bash shelled out) | `go test ./pkg/compiler/... -run TestNotifyHook_SendFailure -v` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

**Requirement IDs (proposed — to be added to REQUIREMENTS.md by Plan 01 or 02):**
- **HOOK-01** — Compiler unconditionally writes `/opt/km/bin/km-notify-hook` script to sandbox during user-data execution
- **HOOK-02** — Compiler merges `Notification` and `Stop` hook entries into `~/.claude/settings.json`, preserving any user-supplied entries from `spec.execution.configFiles`
- **HOOK-03** — Compiler writes `/etc/profile.d/km-notify-env.sh` with `KM_NOTIFY_ON_PERMISSION` / `KM_NOTIFY_ON_IDLE` / `KM_NOTIFY_COOLDOWN_SECONDS` / `KM_NOTIFY_EMAIL` only when corresponding `spec.cli.notify*` profile fields are set
- **HOOK-04** — `km shell` and `km agent run` honor `--notify-on-permission` / `--notify-on-idle` (and `--no-*`) CLI flags, overriding profile defaults via env vars injected at SSM-launch time
- **HOOK-05** — `/opt/km/bin/km-notify-hook` honors gate env vars, cooldown, builds correct subjects/bodies for both events, calls km-send with `--body <file>`, never blocks Claude on send failure

---

## Wave 0 Requirements

- [ ] `pkg/compiler/userdata_notify_test.go` — new file, covers HOOK-01/02/03 following `pkg/compiler/userdata_test.go` pattern (read `baseProfile()` helper and `generateUserData()` test conventions)
- [ ] `internal/app/cmd/shell_notify_test.go` (or extend `shell_test.go`) — covers HOOK-04 for `km shell` flag plumbing, mirrors `TestShellCmd_NoBedrock` pattern
- [ ] `internal/app/cmd/agent_notify_test.go` (or extend `agent_test.go`) — covers HOOK-04 for `km agent run` env var injection, mirrors `TestAgentNonInteractive_NoBedrock` pattern
- [ ] `pkg/compiler/testdata/notify-hook-stub-km-send.sh` — stub `km-send` script, used by hook tests via PATH override; records args + body file contents for assertion
- [ ] `pkg/compiler/notify_hook_script_test.go` — Go test driving bash-shelled hook script with synthetic env + stdin payloads; covers HOOK-05 a–e
- [ ] No framework install needed — `go test` already in CI; OpenSSL present on test runner for `km-send` stub realism (the stub doesn't need to actually sign, just record)

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end signed email reaches operator inbox | HOOK-01..05 (composite) | Requires real SES, real sandbox provision, real Claude session triggering Notification or Stop | (1) `km create profiles/notify-test.yaml` (profile with `notifyOnPermission: true`); (2) `km agent run <id> --prompt "rm -rf /etc"` (or any prompt that triggers permission prompt); (3) confirm signed email arrives in `KM_OPERATOR_EMAIL` inbox; (4) repeat with `notifyOnIdle: true` and a benign prompt to confirm idle email |
| `notificationEmailAddress` override routes correctly | HOOK-03 (recipient field) | Requires real SES delivery to verify recipient, not just env var presence | Set `notificationEmailAddress: "alt@example.com"` in profile, provision, trigger event, confirm email arrives at the override address (and NOT at operator default) |
| Cooldown behaves correctly across event types in real time | HOOK-05d | Test uses synthetic time; real-world clock-tick edge cases (NTP skew, container time drift) only surface in production | After live E2E above, configure `notifyCooldownSeconds: 60`, trigger Notification then Stop within 60s; confirm only one email received |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (5 new test files / testdata stubs)
- [ ] No watch-mode flags (Go test runs synchronously)
- [ ] Feedback latency < 60s for full suite
- [ ] `nyquist_compliant: true` set in frontmatter (after planner finalizes plan/task IDs)

**Approval:** pending (planner to finalize task IDs and flip `nyquist_compliant: true`)
