---
phase: 79
slug: km-presence-daemon
status: active
nyquist_compliant: true
wave_0_complete: true
created: 2026-05-10
---

# Phase 79 — Validation Strategy

> Per-phase validation contract for the km-presence daemon implementation. The Validation Architecture in 79-RESEARCH.md is the source of truth for what gets tested at which layer; this file pins the concrete commands and per-task map.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (standard library, table-driven) — matches `cmd/km-slack/`, `sidecars/audit-log/` |
| **Config file** | none — uses `go.mod` at repo root |
| **Quick run command** | `go test ./cmd/km-presence/...` |
| **Full suite command** | `go test ./cmd/km-presence/... ./pkg/compiler/... ./internal/app/cmd/...` |
| **Estimated runtime** | ~15s for `cmd/km-presence` alone, ~90s for the touched packages |

---

## Sampling Rate

- **After every task commit:** Run `go test ./cmd/km-presence/...` (the local package)
- **After every plan wave:** Run `go test ./cmd/km-presence/... ./pkg/compiler/... ./internal/app/cmd/...`
- **Before `/gsd:verify-work`:** Full project test suite must be green (`make test` if it exists, otherwise `go test ./...`)
- **Max feedback latency:** 90 seconds for full touched-package suite

---

## Per-Task Verification Map

The planner will populate this table during plan creation. Format:

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 79-00-01 | 00 | 0 | PHASE-79 stubs | unit | `go build ./cmd/km-presence/...` | ✅ | ⬜ pending |
| 79-00-02 | 00 | 0 | PHASE-79 stubs | unit | `go test ./cmd/km-presence/... 2>&1 \| grep FAIL` | ✅ | ⬜ pending |
| 79-00-03 | 00 | 0 | PHASE-79 stubs | unit | `go test ./internal/app/cmd/... -run 'TestDoctor_PresenceDaemonHealthy' 2>&1 \| grep FAIL` | ✅ | ⬜ pending |
| 79-01-01 | 01 | 1 | Signal checks + tick + emit | unit | `go test ./cmd/km-presence/... -v -count=1 -run 'TestSignal_\|TestTick_\|TestFakeRunner_'` | ✅ | ⬜ pending |
| 79-01-02 | 01 | 1 | Daemon main loop | build | `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o /tmp/km-presence-linux ./cmd/km-presence/ && test -f /tmp/km-presence-linux` | ✅ | ⬜ pending |
| 79-02-01 | 02 | 1 | userdata.go edits | unit | `go test ./pkg/compiler/... -v -count=1 -run 'TestUserdata_(NoBashHeartbeat\|KmAuditHookPreserved\|PresenceSidecarInstalled\|PresenceEnabled_BothBranches\|SlackInboundTouchesPresenceStamp)'` | ✅ | ⬜ pending |
| 79-03-01 | 03 | 1 | Makefile sidecar pipeline | build | `make build-sidecars && test -f build/km-presence` | ✅ | ⬜ pending |
| 79-04-01 | 04 | 2 | Doctor check implementation | unit | `go test ./internal/app/cmd/... -v -count=1 -run 'TestDoctor_PresenceDaemonHealthy_(OK\|Stale\|Skipped)'` | ✅ | ⬜ pending |
| 79-04-02 | 04 | 2 | Doctor check registration | unit | `go build ./... && go test ./internal/app/cmd/... -count=1 -run 'TestDoctor' && grep -q 'checkPresenceDaemonHealthy' internal/app/cmd/doctor.go` | ✅ | ⬜ pending |
| 79-05-03 | 05 | 3 | Bug-fix regression UAT | manual | (operator drives — see Manual-Only Verifications) | ✅ | ⬜ pending (gates phase) |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

**Coverage targets the planner must hit:**
- Each of the 5 signal checks gets at least one positive + one negative test
- Emit logic gets a test for each combination class (no signals → no emit, single signal → emit, multiple signals → single emit)
- Stamp file semantics: missing-stamp first-tick test, stamp-newer-than-signal test, signal-newer-than-stamp test
- Doctor check `presence_daemon_healthy`: returns OK with fresh CW event, FAIL with stale, SKIP with non-running sandbox

---

## Wave 0 Requirements

Stub files needed before any implementation begins:

- [ ] `cmd/km-presence/main.go` — package skeleton with empty `main()` and unexported `run()` for testability
- [ ] `cmd/km-presence/main_test.go` — failing test stubs for each signal + emit logic
- [ ] `cmd/km-presence/runner.go` — `commandRunner` interface (injectable for `who`/`pgrep`/`tmux` mocking)
- [ ] `cmd/km-presence/runner_test.go` — mock runner test fixture
- [ ] Verify `go test ./cmd/km-presence/...` runs (compiles, tests fail with clear "not implemented" messages)

The doctor check stub (in `internal/app/cmd/doctor.go` or whichever file the planner identifies):

- [ ] `presence_daemon_healthy` check function stub returning a hard-coded failure
- [ ] Test stub for the new check with mocked CloudWatch client

---

## Manual-Only Verifications

These cannot be automated in CI and require operator-driven sandbox provisioning:

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Each signal triggers a `source:"presence"` CloudWatch event within 90s on a live sandbox | Layer 2 integration | Requires real EC2 + AWS API + CloudWatch | Provision a sandbox with new userdata; for each of the 5 signals (open SSM session, attach tmux, send email, dispatch Slack, run `claude -p`), trigger in isolation; query CloudWatch logs for `source:"presence"` event in the audit stream within 90s |
| Daemon survives `systemctl restart km-presence` | Resilience | Requires a real systemd | `aws ssm send-command` with `systemctl restart km-presence`; verify next tick fires within 90s |
| Daemon auto-restarts on `kill -9` | Resilience | Requires a real systemd | `aws ssm send-command` with `pkill -9 km-presence`; verify `pgrep km-presence` returns a new PID within 10s |
| **Bug-fix regression test** — no orphaned heartbeats after `km shell` exit | Source of phase | Requires SSM session lifecycle | Provision new sandbox; `km shell <id>`, `Ctrl-D` to exit; `aws ssm send-command` with `pgrep -af '_km_heartbeat\|sleep 60'` filtered to sandbox user — must return zero heartbeat-class processes (legitimate poller sleeps from km-mail-poller and km-slack-inbound-poller will appear, but they're root-owned) |
| Bug-fix regression after `km agent run` | Source of phase | Requires agent lifecycle | Provision new sandbox; `km agent run <id> --prompt "echo hi" --wait`; after completion, `pgrep -afu sandbox '_km_heartbeat\|sleep 60'` returns zero |
| `km doctor` `presence_daemon_healthy` check passes for healthy sandbox, fails for old AMI | Layer 3 E2E | Requires both new + legacy sandbox | Run `km doctor` against a sandbox provisioned post-rollout AND one provisioned pre-rollout; verify check status accordingly |

---

## Validation Sign-Off

- [x] All implementation tasks have `<automated>` verify command or Wave 0 stub dependency
- [x] Sampling continuity: no 3 consecutive tasks without automated verify (each task verifies)
- [x] Wave 0 covers all MISSING references (4 stub files: main.go, runner.go, doctor_presence.go, doctor_presence_test.go)
- [x] No watch-mode flags (`go test` is one-shot)
- [x] Feedback latency < 90 seconds for touched-package suite
- [x] All 5 signals have unit-test coverage (positive + negative): see `cmd/km-presence/main_test.go`
- [x] Doctor check has unit-test coverage (3 cases: OK / Stale / Skipped)
- [x] Manual verification table populated with concrete commands (see existing Manual-Only Verifications block)
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** active
