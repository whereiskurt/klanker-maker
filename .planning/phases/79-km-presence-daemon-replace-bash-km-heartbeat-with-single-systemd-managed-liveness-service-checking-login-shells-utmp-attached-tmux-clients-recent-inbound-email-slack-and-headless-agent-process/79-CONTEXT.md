# Phase 79: km-presence daemon — replace bash _km_heartbeat with systemd liveness service - Context

**Gathered:** 2026-05-10
**Status:** Ready for planning
**Source:** PRD Express Path (`docs/superpowers/specs/2026-05-10-km-presence-daemon-design.md`)

<domain>
## Phase Boundary

This phase replaces the per-shell bash `_km_heartbeat` function with a single sandbox-side systemd-managed daemon (`km-presence.service`) that becomes the sole emitter of liveness "heartbeat" events into `/run/km/audit-pipe`. The daemon ticks every 60 seconds and emits a heartbeat if any of five concrete signals is positive: (1) login shells via `who`/utmp, (2) attached tmux clients, (3) recent inbound email, (4) recent inbound Slack, (5) headless agent process running.

**In scope:**
- New Go binary `cmd/km-presence/main.go` (~150 LoC) shipped via existing sidecar pipeline
- New systemd unit `/etc/systemd/system/km-presence.service` installed via userdata template
- Removal of `_km_heartbeat()` function and EXIT trap from `pkg/compiler/userdata.go` (lines 1056-1080)
- One-line `touch /run/km/last-slack-inbound` addition in km-slack-inbound-poller after successful SQS dispatch
- New `km doctor` check `presence_daemon_healthy` (5-min staleness threshold)
- Unit + integration tests
- CLAUDE.md documentation update

**Out of scope (deferred):**
- Operator-side enrichment of `km list` to show *which* signal is keeping a sandbox awake
- Per-signal weighting / decay
- Replacing the IdleDetector's CloudWatch poll with local IPC
- Backfilling existing AMIs (matches Phase 63/67/68/73 pattern: only new sandboxes get the daemon; existing ones keep bash heartbeats until `km destroy && km create`)

**Source bug:** Live evidence on 2026-05-10 from sandbox `learn-14853201` (alias L1): IDLE column pegged at full 60m for 3+ hours with no human connected. Process inspection found two orphaned `_km_heartbeat` bash subshells: one from a closed pts/0 SSM session, one from a 2-hour-old `km agent run` tmux that left two nested `exec bash` login shells each running their own heartbeat. The fundamental issue: every login shell forks a heartbeat that survives any bash death bypassing the EXIT trap (SIGKILL, SSM agent crash, network drop), and each `exec bash` inside tmux multiplies the count.

</domain>

<decisions>
## Implementation Decisions

### Architecture (locked)

- **Service name:** `km-presence.service` (operator may bikeshed during planning; trivial)
- **Process owner:** root (needs to read utmp, run `pgrep -af` across all users, run `ss`)
- **Implementation:** Go binary at `cmd/km-presence/main.go`, statically linked, ~150 LoC
- **Distribution:** Built by `make build` into `sidecars/dist/km-presence`, uploaded to S3 by `km init --sidecars` alongside km-slack, km-mail-poller, etc.
- **systemd unit type:** `Type=simple`, `Restart=always`, `RestartSec=5`, `User=root`
- **Tick cadence:** 60 seconds (NOT configurable in v1 — matches existing bash heartbeat exactly to preserve observable behavior)

### Audit pipe contract (locked)

- **Output target:** `/run/km/audit-pipe` (existing FIFO, unchanged)
- **Event JSON shape:** Identical to current bash heartbeat — `{"timestamp":"...","sandbox_id":"...","event_type":"heartbeat","source":"presence","detail":{}}`
- **Diagnostic delta:** `source` field is `"presence"` (NOT `"shell"`) — lets `km otel` and CloudWatch greps distinguish new daemon from old bash at a glance
- **Write semantics:** Use existing `timeout 0.1 tee /run/km/audit-pipe > /dev/null 2>/dev/null || true` pattern from `_km_audit` to bound FIFO write at 100ms (avoids blocking when no reader is connected — Phase 56.1 Bug 2 lesson)

### Five signals (locked, all five required)

| # | Signal | Check | Cost |
|---|---|---|---|
| 1 | Login shells (SSM + SSH) | `who` (reads `/var/run/utmp`); non-empty stdout → active | <1ms |
| 2 | tmux clients attached | `runuser -u sandbox -- tmux list-clients -t '' 2>/dev/null \| wc -l` > 0 | ~5ms |
| 3 | Recent inbound email | `find /var/mail/km/new/ -newer /run/km/.presence-last-tick -type f \| head -1` non-empty | <2ms |
| 4 | Recent inbound Slack | `[ /run/km/last-slack-inbound -nt /run/km/.presence-last-tick ]` | <1ms |
| 5 | Headless agent process | `pgrep -af '(^\|/)claude( \|$)\|(^\|/)codex( \|$)\|km-agent-run\.sh'` (word-boundary regex avoids false positives like "claudia") | ~3ms |

### Emit logic (locked)

```
last_tick = mtime("/run/km/.presence-last-tick")  // zero on first tick
active = signal1() || signal2() || signal3(last_tick) || signal4(last_tick) || signal5()
if active:
    write_audit_event(source: "presence", event_type: "heartbeat")
touch("/run/km/.presence-last-tick")  // unconditional, end of tick
log structured one-liner: tick=N signals=ssm,email emitted=1
```

Boolean OR — no weighting, no tiebreakers in v1.

### Stamp file pattern (locked)

- **Stamp path:** `/run/km/.presence-last-tick`
- **Update timing:** Unconditional `touch` at end of every tick (NOT only when emitted)
- **Rationale:** Newer-than-stamp comparison guarantees we never miss a one-shot signal even if a tick takes >60s. Daemon is stateless across restarts (a fresh stamp at boot means "no chat-since-last-tick yet" — correct).

### Removed from `pkg/compiler/userdata.go` (locked)

- Lines 1056-1080: the entire `_km_heartbeat()` function definition AND the `trap 'kill -9 $_KM_HEARTBEAT_PID 2>/dev/null' EXIT` line
- Lines 1063-1077 specifically: function body, background invocation, PID capture
- **Kept:** lines 1030-1054 (the `_km_audit` per-command hook) — discrete commands are still useful audit-trail input

### Required addition to km-slack-inbound-poller (locked)

- **File:** `pkg/compiler/userdata.go` ~line 1792 (the SLACKINBOUNDUNIT heredoc)
- **Change:** After successful SQS message dispatch to Claude (after the bash that actually invokes `claude -p`), add: `touch /run/km/last-slack-inbound`
- **Why here:** Avoids new IAM, no DDB read, no daemon-to-poller IPC. Single line change.

### km doctor enhancement (locked)

- **New check name:** `presence_daemon_healthy`
- **Logic:** For each running sandbox in scope, query CloudWatch logs for the most recent `source:"presence"` event in the audit log group; flag if older than 5 minutes
- **Skip conditions:** Sandbox status != "running" → skip (paused/stopped sandboxes don't run the daemon)
- **Failure message format:** Match existing doctor check style (e.g., `slack_inbound_queue_exists` pattern)

### Migration story (locked)

Same shape as Phase 63, 67, 68, 73:

1. `make build` rebuilds km + new km-presence binary
2. `km init --sidecars` uploads km-presence to S3 and refreshes management Lambdas with new userdata template
3. New sandboxes (`km create` after step 2) get the daemon
4. Existing sandboxes keep bash heartbeats until `km destroy && km create` — accepted, documented in CLAUDE.md

### Roll back (locked)

- Revert `pkg/compiler/userdata.go` diff and re-run `km init --sidecars`
- New sandboxes from that point are born with old bash heartbeat
- Existing sandboxes unaffected (they have whichever pattern they were born with)
- km-presence binary in S3 can stay (harmless when nothing references it)

### CLAUDE.md update (locked)

Add a section near the existing "Phase 73 VS Code Remote-SSH" / "Phase 63 Slack Notifications" sections documenting:
- The new daemon and what it does
- The `make build && km init --sidecars` migration requirement
- The "existing sandboxes don't get retroactively" constraint
- The `presence_daemon_healthy` doctor check

### Claude's Discretion (planner / implementer choices)

- **Go module structure:** Whether `cmd/km-presence/main.go` includes everything inline or splits signal checks into separate files. Implementer's call — preference for testability.
- **Signal check abstraction:** Whether each signal is a `func() bool` or a more elaborate interface. Lean toward simple functions unless tests demand otherwise.
- **Logging library:** Match existing sidecars (zerolog, used in `sidecars/audit-log/cmd/main.go`).
- **Test framework:** Standard `testing` package; table-driven tests with injectable command runner for mocking `who`/`pgrep`/`tmux` output.
- **Exact systemd unit text:** Mirror existing `km-mail-poller.service` and `km-slack-inbound-poller.service` patterns (line ~1775 and ~1792 in `pkg/compiler/userdata.go`). EnvironmentFile NOT needed unless tests reveal a need.

</decisions>

<specifics>
## Specific Ideas

### Code touchpoints (concrete file paths from PRD)

- **NEW:** `cmd/km-presence/main.go` — daemon entry point
- **NEW:** `cmd/km-presence/main_test.go` — unit tests, table-driven per signal
- **NEW:** Makefile rule + `sidecars/dist/km-presence` build artifact (mirror existing sidecar build patterns)
- **MODIFY:** `pkg/compiler/userdata.go` — REMOVE lines 1056-1080 (`_km_heartbeat`); ADD systemd unit heredoc + S3 fetch in sidecar block; ADD one-line `touch /run/km/last-slack-inbound` in km-slack-inbound-poller body (~line 1792)
- **MODIFY:** `internal/app/cmd/doctor.go` (or wherever doctor checks live) — ADD `presence_daemon_healthy` check
- **MODIFY:** `CLAUDE.md` — ADD section describing the new daemon + migration requirement
- **REFERENCE (do not modify):** `sidecars/audit-log/cmd/main.go:327` (IdleDetector — consumes the audit pipe)
- **REFERENCE (do not modify):** `internal/app/cmd/status.go:532` (computeIdleRemaining — operator-side IDLE column)

### Distribution pipeline reference

The `make build && km init --sidecars` pattern is documented in CLAUDE.md under multiple existing sections (Slack Notifications, VS Code Remote-SSH, Slack inbound). The Makefile already handles `km-slack`, `km-slack-bridge`, `km-mail-poller`-style sidecars. Follow the closest existing pattern (`km-slack` is similar in being a sandbox-side binary).

### Test fixtures

- Unit tests: inject a `commandRunner` interface so `who`, `pgrep`, `tmux list-clients` can be faked in tests
- For signals 3 + 4: use `t.TempDir()` to fake `/var/mail/km/new/` and `/run/km/last-slack-inbound`
- For signal 5: fake `pgrep` output via the runner
- Integration test: provision a sandbox with new code, exercise each signal in isolation, verify CloudWatch event delivery within 90s

### Known process patterns to NOT touch

- `_km_audit` PROMPT_COMMAND hook (different mechanism — per-command, not periodic)
- `km-audit-log` sidecar (consumer of `/run/km/audit-pipe`, agnostic to who emits)
- IdleDetector (consumes CloudWatch, agnostic to source)
- ttl-handler Lambda (consumes EventBridge, agnostic to source)
- `computeIdleRemaining` (consumes CloudWatch, agnostic to source)

</specifics>

<deferred>
## Deferred Ideas

The following are explicitly OUT of scope per the PRD:

1. **Operator-side enrichment of `km list`** — showing which signal is keeping a sandbox awake (e.g., "IDLE: 60m (presence: ssm,agent)"). Useful but separate from the reliability fix. File as a follow-up phase.

2. **Per-signal weighting / decay** — current emit rule is boolean OR. If utmp stale entries prove noisy in production, per-signal cooldowns can be added without changing the daemon's external contract.

3. **Replacing IdleDetector's CloudWatch poll with local IPC** — long-term cost/latency win, but scoped out here. Filed as potential follow-up phase.

4. **Aggressive `--older 7200` filter on signal 5** — would prevent orphaned-claude-process false positives but risks killing legitimate long agent runs. Defer until production evidence shows a problem.

5. **Backfilling existing AMIs** — same constraint as Phases 63, 67, 68, 73. Documented in CLAUDE.md, not solved here.

6. **Configurable tick cadence** — fixed at 60s in v1. The IdleDetector polls CloudWatch every 60s anyway; finer-grained ticks add cost without value.

</deferred>

---

*Phase: 79-km-presence-daemon*
*Context gathered: 2026-05-10 via PRD Express Path*
