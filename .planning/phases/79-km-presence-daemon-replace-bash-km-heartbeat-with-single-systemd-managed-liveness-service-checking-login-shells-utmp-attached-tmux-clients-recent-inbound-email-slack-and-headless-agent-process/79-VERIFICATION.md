---
phase: 79-km-presence-daemon
verified: 2026-05-10T00:00:00Z
status: passed
score: 8/8 must-haves verified
re_verification: false
human_verification:
  - test: "km doctor presence_daemon_healthy on a fresh post-Phase-79 sandbox"
    expected: "OK status, not WARN"
    why_human: "Requires live AWS infrastructure — operator confirmed OK on sandbox learn-78ac4247 (UAT PASS, now destroyed)"
  - test: "km shell + Ctrl-D produces zero orphan _km_heartbeat-class processes"
    expected: "pgrep for _km_heartbeat/sleep returns NONE after shell exit"
    why_human: "Requires live sandbox; operator confirmed PASS on learn-78ac4247"
  - test: "km agent run produces zero orphan processes after agent completes"
    expected: "No leaked sandbox-user heartbeat-class processes after agent run"
    why_human: "Requires live sandbox; operator confirmed PASS on learn-78ac4247"
  - test: "km list --wide IDLE column counts down (not pegged at max) when sandbox is truly idle"
    expected: "IDLE column shows decreasing countdown (e.g. 59m7s), not stuck at 60m0s"
    why_human: "Requires live sandbox observation over time; operator confirmed PASS on learn-78ac4247"
---

# Phase 79: km-presence Daemon Verification Report

**Phase Goal:** Replace bash _km_heartbeat with single systemd-managed km-presence liveness service that checks 5 signals (login shells via utmp, attached tmux clients, recent inbound email, recent inbound Slack, headless agent process) and emits to audit pipe iff any signal is positive — fixing the orphaned heartbeat bug observed 2026-05-10.

**Verified:** 2026-05-10
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Single km-presence systemd process replaces per-shell bash _km_heartbeat | VERIFIED | `_km_heartbeat()` absent from EC2 userdata (TestUserdata_NoBashHeartbeat PASS); km-presence.service unit wired in userdata; Docker compose.go untouched (intentional) |
| 2 | km-presence binary evaluates all 5 signals and emits source:"presence" iff any positive | VERIFIED | 13 unit tests in cmd/km-presence/main_test.go all PASS; runner.go emit() produces `"source":"presence"` JSON; TestTick_EmitWhenAnyPositive and TestTick_NoEmitWhenAllNegative both green |
| 3 | Stamp file /run/km/.presence-last-tick touched unconditionally every tick | VERIFIED | TestTick_StampAlwaysTouched PASS; touchStamp() called in tick() regardless of active/emitted |
| 4 | km-presence loops at 60s cadence with SIGTERM/SIGINT graceful shutdown | VERIFIED | main.go: time.NewTicker(60s) + signal.NotifyContext(SIGTERM, SIGINT); min_lines=60 spec met (80 lines) |
| 5 | checkPresenceDaemonHealthy doctor check returns OK/WARN/SKIPPED correctly | VERIFIED | All 4 doctor tests PASS including regression test TestDoctor_PresenceDaemonHealthy_LogGroupAndFilterShape |
| 6 | Doctor check registered in buildChecks() and surfaces in km doctor output | VERIFIED | doctor.go:2695 appends presence check to checks slice; DoctorDeps fields CWFilterClient/PresenceSandboxLister/PresenceLogGroupPrefix all wired at doctor.go:2925-2947 |
| 7 | CLAUDE.md documents daemon, migration, doctor check, rollback (placed Phase 73 → Architecture) | VERIFIED | "## Presence daemon (Phase 79)" at line 367; Architecture at line 442; 5-signal table, migration recipe, doctor check, rollback, PRD link all present |
| 8 | VALIDATION.md nyquist_compliant=true, 10-row Per-Task Map, wave_0_complete=true | VERIFIED | 79-VALIDATION.md frontmatter: nyquist_compliant: true, wave_0_complete: true, status: active; rows 79-00-01..79-05-03 present |

**Score:** 8/8 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `cmd/km-presence/main.go` | Daemon main loop with 60s ticker + signal handlers | VERIFIED | 80 lines; time.NewTicker; SIGTERM/SIGINT via signal.NotifyContext; zerolog structured output |
| `cmd/km-presence/runner.go` | 5 signal checks + tick() + emit() + commandRunner interface | VERIFIED | 181 lines; all 5 signal functions + tick + touchStamp + emit; source:"presence" JSON; timeout 0.1 tee audit-pipe pattern |
| `cmd/km-presence/main_test.go` | 13 tests covering all signals + tick logic, all GREEN | VERIFIED | All 13 tests PASS: 2 login shell, 2 tmux, 2 email, 2 slack, 2 agent, 3 tick/emit/stamp |
| `cmd/km-presence/runner_test.go` | fakeRunner test fixture + sanity test | VERIFIED | fakeRunner struct with responses/errors maps; TestFakeRunner_ReturnsConfiguredOutput PASS |
| `internal/app/cmd/doctor_presence.go` | checkPresenceDaemonHealthy + CWLogsFilterAPI interface, full implementation | VERIFIED | 144 lines; full CloudWatch query logic with trailing-slash fix and JSON filter syntax fix (commit 061c041) |
| `internal/app/cmd/doctor_presence_test.go` | 4 tests: OK/Stale/Skipped + regression | VERIFIED | All 4 PASS; gotLogGroup/gotFilterPattern capture fields enable regression test |
| `internal/app/cmd/doctor.go` | checkPresenceDaemonHealthy registered in buildChecks | VERIFIED | Line 2695: appended to checks; lines 2925-2947: CWFilterClient + PresenceSandboxLister + PresenceLogGroupPrefix initialized |
| `pkg/compiler/userdata.go` | km-presence S3 fetch + systemd unit + enable/restart; no _km_heartbeat | VERIFIED | Line 898: S3 copy; lines 1791-1804: km-presence.service unit; lines 2367-2375: systemctl enable+restart in both branches; _km_heartbeat: 0 matches |
| `pkg/compiler/userdata_phase79_test.go` | 5 Phase 79 userdata regression tests | VERIFIED | TestUserdata_NoBashHeartbeat, KmAuditHookPreserved, PresenceSidecarInstalled, PresenceEnabled_BothBranches (3 sub-tests), SlackInboundTouchesPresenceStamp all PASS |
| `Makefile` | km-presence in sidecars + build-sidecars targets | VERIFIED | Line 91: sidecars target builds km-presence; line 97: uploads to S3; line 131: build-sidecars includes km-presence |
| `CLAUDE.md` | Phase 79 section between VS Code Remote-SSH and Architecture | VERIFIED | Section at line 367; Architecture at line 442; covers 5 signals, migration, docker constraint, doctor check, observability, rollback |
| `.planning/.../79-VALIDATION.md` | nyquist_compliant: true; 10-row verification map | VERIFIED | nyquist_compliant: true; wave_0_complete: true; status: active; 10 rows (79-00-01 through 79-05-03) |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| cmd/km-presence/runner.go emit() | /run/km/audit-pipe | `printf ... \| timeout 0.1 tee /run/km/audit-pipe` bash command | WIRED | Line 177: matches the Phase 56.1 Bug 2 fix pattern used throughout userdata |
| cmd/km-presence/runner.go tick() | /run/km/.presence-last-tick | touchStamp(presenceStampPath) unconditional at end of tick | WIRED | Lines 130-163: touchStamp called after active/emit logic |
| cmd/km-presence/main_test.go | cmd/km-presence/runner.go fakeRunner | fakeRunner satisfies commandRunner interface | WIRED | runner_test.go defines fakeRunner; main_test.go uses it in all signal tests |
| internal/app/cmd/doctor.go buildChecks | internal/app/cmd/doctor_presence.go checkPresenceDaemonHealthy | checks = append(checks, func(ctx) { return checkPresenceDaemonHealthy(...) }) | WIRED | Lines 2688-2701 in doctor.go |
| internal/app/cmd/doctor.go initRealDepsWithExisting | cloudwatchlogs.NewFromConfig | deps.CWFilterClient = cloudwatchlogs.NewFromConfig(awsCfg) | WIRED | Line 2925 |
| internal/app/cmd/doctor.go initRealDepsWithExisting | PresenceLogGroupPrefix | "/" + cfg.GetResourcePrefix() + "/sandboxes/" | WIRED | Line 2947 — trailing slash included per regression test |
| pkg/compiler/userdata.go | km-presence.service systemd unit | S3 fetch + unit heredoc + systemctl enable+restart | WIRED | Lines 898, 1791-1804, 2367-2375 |
| pkg/compiler/userdata.go (slack inbound poller) | /run/km/last-slack-inbound | touch /run/km/last-slack-inbound in success branch | WIRED | Line 1493; TestUserdata_SlackInboundTouchesPresenceStamp verifies ordering |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| PHASE-79-PRESENCE-DAEMON | 79-00, 79-01, 79-02, 79-03, 79-04, 79-05 | Replace bash _km_heartbeat with systemd-managed liveness daemon checking 5 signals | SATISFIED | All artifacts implemented; all unit tests GREEN; full repo builds cleanly; live UAT PASS on sandbox learn-78ac4247 (8/8 must-haves) |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| (none) | — | No TODOs, FIXMEs, placeholder returns, or stub bodies found in implementation files | — | — |

**Known deferred item (not a blocker):** `internal/app/cmd/init.go` `buildAndUploadSidecars` does not include km-presence in its binary list. The `make sidecars` Makefile target is the correct upload path and is documented in CLAUDE.md. Logged in `deferred-items.md` as DEFERRED-79-01. No impact on phase goal: the orphaned-heartbeat bug is fixed; new EC2 sandboxes provision with km-presence when `make sidecars` is used for S3 upload.

### Human Verification Required

The following truths cannot be verified programmatically and were validated by the operator on live sandbox learn-78ac4247 (2026-05-10, now destroyed):

#### 1. Source bug fixed: no orphan heartbeats after km shell exit

**Test:** `km shell $SB` then immediately Ctrl-D; wait 30s; `pgrep -afu sandbox '_km_heartbeat|sleep 60'`
**Expected:** Output is `NONE` — no orphan sandbox-user heartbeat-class processes
**Why human:** Requires live EC2 sandbox with SSM access; cannot simulate shell attach/detach in unit tests
**UAT result:** PASS — `_km_heartbeat` function fully removed from /etc/profile.d/*.sh; pgrep returned NONE

#### 2. Source bug fixed: no orphan heartbeats after km agent run

**Test:** `km agent run $SB --prompt "echo hello" --wait`; wait 30s; repeat pgrep check
**Expected:** Output is `NONE`
**Why human:** Requires live agent execution pipeline
**UAT result:** PASS

#### 3. CloudWatch receives source:"presence" events at ~60s cadence when signal active

**Test:** Open km shell (signal 1 active); wait 3+ minutes; query CloudWatch FilterLogEvents for `{ $.source = "presence" }`
**Expected:** At least 2-3 events in last 5 minutes
**Why human:** Requires live CloudWatch log ingestion
**UAT result:** PASS — ticks 9, 13, 22 emitted=true when signals active

#### 4. km doctor presence_daemon_healthy returns OK on fresh post-Phase-79 sandbox

**Test:** `km doctor 2>&1 | grep -A1 "Presence daemon healthy"`
**Expected:** OK status
**Why human:** Requires live CloudWatch + running sandbox
**UAT result:** PASS (after two bug fixes committed as 061c041 — log group trailing slash + filter pattern syntax)

#### 5. km list --wide IDLE column counts down (not pegged at 60m)

**Test:** With no shells/agents — wait 5 minutes; `km list --wide | grep $SB`
**Expected:** IDLE column shows decreasing countdown (e.g. 59m7s), not stuck at 60m0s
**Why human:** Requires live sandbox in idle state over multiple minutes
**UAT result:** PASS — observed 59m7s during UAT

### Gaps Summary

No gaps. All 8 phase-level must-haves verified. The phase goal is achieved: the orphaned `_km_heartbeat` bash function has been removed from EC2 userdata, replaced by a single root-owned systemd-managed `km-presence.service` that checks 5 liveness signals per tick and emits `source:"presence"` heartbeat events to the audit pipe when any signal is positive. The source bug (orphaned heartbeats pegging IDLE column at full timeout) is provably fixed on Phase-79-provisioned sandboxes. Docker substrate is intentionally unchanged (systemd not available in Docker).

---

_Verified: 2026-05-10_
_Verifier: Claude (gsd-verifier)_
