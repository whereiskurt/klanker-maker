---
phase: 86
slug: km-create-prompt-queue
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-19
---

# Phase 86 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` (stdlib) — `go test ./...` |
| **Config file** | none — standard `go test` |
| **Quick run command** | `go test ./internal/app/cmd/ -run TestCreate -count=1 -v` |
| **Full suite command** | `make build && go test ./... -count=1` |
| **Estimated runtime** | ~45 seconds (incremental) / ~3 minutes (full suite per CLAUDE.md) |

`make build` is required (per `feedback_rebuild_km` memory) because version ldflags are injected — never use bare `go build`.

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/ -run TestCreate -count=1` AND `go test ./internal/app/cmd/ -run TestAgentList -count=1` (when applicable)
- **After every plan wave:** Run `make build && go test ./... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green AND `make build` must succeed
- **Max feedback latency:** ~45 seconds (incremental scope)

---

## Per-Task Verification Map

Tracks each acceptance criterion → automated test → status. Updated as Wave 0 lands its stubs.

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 86-01-01 | 01 | 0 | PQ-01 (`--prompt` repeatable) | unit | `go test ./internal/app/cmd/ -run TestCreatePromptFlag -v` | ❌ W0 | ⬜ pending |
| 86-01-02 | 01 | 0 | PQ-02 (`@file` + `@@` escape + missing-file error) | unit | `go test ./internal/app/cmd/ -run TestResolvePrompts -v` | ❌ W0 | ⬜ pending |
| 86-01-03 | 01 | 0 | PQ-03 (`--prompt + --docker` reject) | unit | `go test ./internal/app/cmd/ -run TestCreatePromptDockerReject -v` | ❌ W0 | ⬜ pending |
| 86-02-01 | 02 | 1 | PQ-04 (SSM queue-file push + meta.json shape) | unit (mock SSM) | `go test ./internal/app/cmd/ -run TestPushQueueFiles -v` | ❌ W0 | ⬜ pending |
| 86-02-02 | 02 | 1 | PQ-05 (`--wait` polls until all `done`, exit 0) | unit (mock SSM) | `go test ./internal/app/cmd/ -run TestCreatePromptWait -v` | ❌ W0 | ⬜ pending |
| 86-02-03 | 02 | 1 | PQ-06 (`--wait` exit non-zero on fail, remaining `skipped`) | unit (mock SSM) | `go test ./internal/app/cmd/ -run TestCreatePromptWaitFail -v` | ❌ W0 | ⬜ pending |
| 86-03-01 | 03 | 1 | PQ-07 (`km agent list --queue` view) | unit (mock SSM) | `go test ./internal/app/cmd/ -run TestAgentListQueue -v` | ❌ W0 | ⬜ pending |
| 86-04-01 | 04 | 2 | PQ-08 (runner reconcile + state machine) | unit (table-test) | `go test ./internal/app/cmd/ -run TestQueueRunnerStateMachine -v` | ❌ W0 | ⬜ pending |
| 86-05-01 | 05 | 3 | PQ-09 (single-prompt happy path) | operator UAT | real-AWS, manual | n/a | ⬜ pending |
| 86-05-02 | 05 | 3 | PQ-10 (two-prompt chain) | operator UAT | real-AWS, manual | n/a | ⬜ pending |
| 86-05-03 | 05 | 3 | PQ-11 (fail-stops-chain) | operator UAT | real-AWS, manual | n/a | ⬜ pending |
| 86-05-04 | 05 | 3 | PQ-12 (pause/resume reconcile) | operator UAT | real-AWS, manual | n/a | ⬜ pending |
| 86-05-05 | 05 | 3 | PQ-13 (direct-API auth wait) | operator UAT | real-AWS, manual | n/a | ⬜ pending |
| 86-05-06 | 05 | 3 | R1 (regression: no `--prompt` = unchanged) | operator UAT | real-AWS, manual | n/a | ⬜ pending |

*Status legend: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

Task IDs are tentative — planner may renumber when producing PLAN.md files.

---

## Wave 0 Requirements

Test stubs that must be created (RED state) before implementation begins:

- [ ] `internal/app/cmd/create_prompt_test.go` (new file) — stubs for PQ-01..PQ-06 and PQ-08:
  - `TestCreatePromptFlag` — verifies `--prompt` is `StringArrayVar` (preserves commas), repeatable.
  - `TestResolvePrompts` — `@file` reads UTF-8 verbatim, `@@literal` escape, missing-file returns clear error.
  - `TestCreatePromptDockerReject` — combining `--prompt` with `--docker` returns hard error before any AWS call.
  - `TestPushQueueFiles` — mock SSM API; verify base64-encoded `.prompt` content and meta.json structure.
  - `TestCreatePromptWait` — mock SSM polling; returns 0 when all entries become `done`.
  - `TestCreatePromptWaitFail` — mock SSM; first entry `failed`, asserts non-zero exit + others `skipped`.
  - `TestQueueRunnerStateMachine` — table-driven test of the Go-side helper that interprets meta.json transitions (the bash runner itself tested in Wave 2 via bash harness).
- [ ] Augment `internal/app/cmd/agent_test.go` with:
  - `TestAgentListQueue` — mock SSM listing `/workspace/.km-agent/queue/`; render with statuses + truncated prompt preview.
- [ ] No new test framework installs needed — `go test` already covers the project.

For the on-box bash runner (Wave 2): a separate bash test harness at `pkg/profile/configfiles/km-queue-runner_test.sh` will be created in that plan. Not part of Wave 0 (the Go stubs are sufficient to gate planning).

---

## Manual-Only Verifications

Behaviors that require real AWS infrastructure and operator-in-loop verification:

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Single-prompt happy path end-to-end | PQ-09 | Requires SSM agent on real EC2 + Bedrock IAM | `km create profiles/learn.yaml --prompt "echo hello" --wait` → expect exit 0 + run visible in `km agent list <sb>` with `hello` in output. |
| Multi-prompt linear chain | PQ-10 | Order-of-execution depends on real systemd timing | `km create profiles/learn.yaml --prompt @plan.txt --prompt "publish step" --wait` → both runs visible in order, second start time > first end time. |
| Fail-stops-chain | PQ-11 | Exit-code propagation across runs | `km create profiles/learn.yaml --prompt "exit 1" --prompt "should not run"` (no `--wait`); after ~2 min, `km agent list --queue <sb>` shows 001=failed, 002=skipped. |
| Direct-API indefinite auth wait | PQ-13 | Requires interactive `claude auth login` flow | `km create profiles/learn.yaml --no-bedrock --prompt "tell me your model"` on a sandbox without seeded creds. Confirm queue stays pending. `km shell <sb>` + `claude auth login`. Confirm queue drains. |
| Pause/resume reconcile | PQ-12 | Requires real EC2 stop/start cycle | `km create --prompt "sleep 300; echo done"`. Mid-execution: `km pause <sb>`. After confirmed stopped: `km resume <sb>`. Verify runner restarts the entry from scratch + chain continues. |
| Regression: empty queue baseline | R1 | Negative test against full sandbox lifecycle | `km create profiles/learn.yaml` (no `--prompt`) → `km shell <sb>` → confirm `/workspace/.km-agent/queue/` does NOT exist + `/workspace/.km-agent/runs/` is empty (no `<ts>/output.json`) + `systemctl is-enabled km-queue.service` → `enabled` + `systemctl is-active km-queue.service` → `active (running)` (the runner idle-polls for `pending` entries that never arrive) + `top -bn1 -p $(systemctl show -p MainPID --value km-queue.service)` → CPU near 0%. Note: revised 2026-05-19 — the unit is unconditionally installed via `userdata.go` heredoc (NOT `configFiles`), so it is always present and enabled regardless of `--prompt` usage. |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (`create_prompt_test.go`, augmented `agent_test.go`)
- [ ] No watch-mode flags (CI-friendly only)
- [ ] Feedback latency < 60s for incremental scope
- [ ] `nyquist_compliant: true` set in frontmatter after Wave 0 lands

**Approval:** pending — flips to approved after Wave 0 stubs are merged in RED state.
