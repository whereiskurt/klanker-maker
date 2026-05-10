# km-presence daemon — replace bash _km_heartbeat with a single sandbox-side liveness service

**Date:** 2026-05-10
**Author:** brainstormed with Claude
**Status:** Draft — pending GSD phase intake

## Why now

Live evidence from sandbox `learn-14853201` (alias L1) on 2026-05-10:
the `IDLE` column in `km list --wide` was pegged at the full 60m
even though no human had been connected for 3+ hours. Process
inspection (via `aws ssm send-command`, no Session) revealed two
orphaned `_km_heartbeat` bash subshells: one from a closed pts/0
SSM session, one from a 2-hour-old `km agent run` tmux that left
two nested `exec bash` login shells each running their own
heartbeat. The audit-pipe was receiving heartbeats every ~7-30s,
which kept the `IdleDetector` (`sidecars/audit-log/cmd/main.go:327`)
from ever firing — defeating idleTimeout entirely.

The bug is in the design, not a single line: `_km_heartbeat` is
forked by every login shell that sources `/etc/profile.d/km-audit.sh`
(`pkg/compiler/userdata.go:1056-1080`), and survives any bash
death that bypasses the EXIT trap (SIGKILL, SSM agent crash,
network drop). It also fans out to N processes per `km agent run`
because each `exec bash` is a new login shell. The intended
"keepalive while session open" guarantee silently became
"keepalive forever once any shell ever opened."

## Goal

Move "is this sandbox actually being used?" from "self-reported
by every bash subshell" to "introspected once per minute by a
single systemd-managed daemon looking at concrete evidence."

## Non-goals

- Changing the IdleDetector, EventBridge, ttl-handler Lambda,
  or `km list` IDLE-column code. They consume the audit pipe;
  who emits is none of their concern.
- Adding new IAM. The daemon runs locally and reads only local
  files / process tables.
- Backfilling existing AMIs. Like every prior sidecar-touching
  phase (63, 67, 68, 73), only sandboxes created after the
  rollout get the daemon; existing ones keep bash heartbeats
  until `km destroy && km create`.

## Design

### Architecture

A new systemd service, **`km-presence.service`**, becomes the
sole emitter of "heartbeat" events into `/run/km/audit-pipe`.
The bash `_km_heartbeat` function and its EXIT trap are deleted
from `/etc/profile.d/km-audit.sh`. The per-command `_km_audit`
hook stays — discrete commands are still useful audit-trail
input.

| Component | Owner | Notes |
|---|---|---|
| `cmd/km-presence/main.go` | new | Tiny Go binary. ~150 LoC. Statically linked, ships via existing sidecar pipeline. |
| `sidecars/dist/km-presence` | new | Built into S3 by `make build` + `km init --sidecars`. Joins km-slack, km-mail-poller, etc. |
| `/etc/systemd/system/km-presence.service` | new | Installed by userdata template. `Type=simple`, `Restart=always`, `User=root`. |
| `pkg/compiler/userdata.go` lines 1056-1080 | deleted | The `_km_heartbeat()` function and `trap '...' EXIT`. |
| km-slack-inbound-poller (`pkg/compiler/userdata.go:1792`) | one-line addition | After successful SQS dispatch, `touch /run/km/last-slack-inbound`. |

The audit pipe contract is unchanged. The daemon writes the
same JSON event the bash function did, with one diagnostic
delta: `source` is `"presence"` instead of `"shell"`. That
lets `km otel` and CloudWatch greps tell new vs old apart at
a glance.

### Signals

Per 60-second tick, the daemon checks all five signals.
Emit one heartbeat event if **any** is positive (boolean OR).

| # | Signal | Check | Cost | Notes |
|---|---|---|---|---|
| 1 | Login shells (SSM + SSH) | `who` (reads `/var/run/utmp`); non-empty → active | <1ms | Single source covers SSM and SSH-via-Phase-73-port-forward both. |
| 2 | tmux clients attached | `runuser -u sandbox -- tmux list-clients -t '' 2>/dev/null \| wc -l` > 0 | ~5ms | Catches "user attached but quietly watching." Detached tmux returns 0 (correct — covered by signal 5 if work is in progress). |
| 3 | Recent inbound email | `find /var/mail/km/new/ -newer /run/km/.presence-last-tick -type f \| head -1` non-empty | <2ms | km-mail-poller drops new files atomically every 60s. Stamp-vs-mtime, no parsing. |
| 4 | Recent inbound Slack | `[ /run/km/last-slack-inbound -nt /run/km/.presence-last-tick ]` | <1ms | Requires the one-line `touch` addition in km-slack-inbound-poller. |
| 5 | Headless agent process | `pgrep -af '(^\|/)claude( \|$)\|(^\|/)codex( \|$)\|km-agent-run\.sh'` | ~3ms | Catches `km agent run` (detached tmux + `claude -p`) and future codex headless. Word-boundary regex avoids false positives like `claudia`. |

**Stamp-file pattern (signals 3+4):** Each tick ends by
`touch /run/km/.presence-last-tick`. Next tick, "new since last
tick" means "anyone touched a watched file or stamp during the
last interval." This is more robust than a fixed
"is mtime within last N seconds" window — we never miss a
one-shot signal even if a tick takes longer than 60s, and the
daemon is stateless across restarts (a fresh stamp at boot
means "no chat-since-last-tick yet," which is correct).

### Emit logic (one tick)

```go
last_tick := mtime("/run/km/.presence-last-tick")  // zero on first tick

active := checkLoginShells()           // signal 1
       || checkTmuxClients()           // signal 2
       || checkInboundEmail(last_tick) // signal 3
       || checkInboundSlack(last_tick) // signal 4
       || checkAgentProcess()          // signal 5

if active {
    writeAuditEvent(source: "presence", event_type: "heartbeat")
}

touch("/run/km/.presence-last-tick")  // unconditional
log.Info().
    Int("tick", tickNum).
    Strs("signals", positiveSignals).
    Bool("emitted", active).
    Msg("presence tick")
```

### Tick cadence

60 seconds. Matches the existing bash heartbeat exactly to
preserve observable behavior in CloudWatch and `km list`. Not
configurable in v1 — the IdleDetector polls CW every 60s
anyway, so finer-grained ticks add cost without value.

### Failure modes considered

- **Daemon crashes silently.** `Restart=always` brings it back
  in 5s. Worst case: <1 missed tick. New `km doctor` check
  `presence_daemon_healthy` (per running sandbox: was a
  `source:"presence"` event emitted in the last 5 minutes?)
  flags persistent failure.
- **Stamp file deleted.** `touch` recreates it. Next tick
  treats all signal-3/4 events as new (false positive at
  most one tick — harmless).
- **Long-running orphaned `claude` process** (the new mirror
  of the old `_km_heartbeat` orphan bug, at the signal-5 layer).
  Accepted risk for v1: agent runs sometimes legitimately
  exceed 2h; aggressive `--older` filtering would silently
  kill them. If this proves an issue in prod, signal 5 can
  later add `--older 7200` as a follow-up.
- **`who` returns stale entries** (utmp is occasionally not
  cleaned up after abrupt SSH disconnects). Mitigation:
  signal 1 alone returning true won't keep an unused sandbox
  alive forever — a real operator would either reconnect
  (regenerating fresh entries) or eventually `km destroy`.
  The IdleDetector is a soft kill anyway; TTL is the hard cap.

### Observability

- **CloudWatch:** events with `"source":"presence"` distinguish
  new daemon from old bash. Existing `km otel` flags
  (`--events`, `--prompts`) Just Work.
- **journald on the sandbox:** structured one-liner per tick:
  `tick=N signals=ssm,email emitted=1`. Operator can
  `journalctl -u km-presence` and immediately see why a
  sandbox isn't idling.
- **`km doctor`:** new check `presence_daemon_healthy` — for
  each running sandbox in scope, query CloudWatch for the
  most recent `source:"presence"` event; flag if older than
  5 minutes. Catches "daemon crashed" and "old AMI without
  daemon."

### Migration story

Same shape as Phase 63, 67, 68, 73:

1. `make build` rebuilds km + new km-presence binary.
2. `km init --sidecars` uploads km-presence to S3 and
   refreshes the management Lambdas with the new userdata
   template.
3. New sandboxes (`km create` after step 2) get the daemon.
4. Existing sandboxes keep bash heartbeats until
   `km destroy && km create`.
5. CLAUDE.md gets a paragraph documenting the cutover and
   the "existing sandboxes don't get it retroactively"
   constraint.

### Roll back

Revert the `pkg/compiler/userdata.go` diff and re-run
`km init --sidecars`. New sandboxes from that point are
born with the old bash heartbeat; existing sandboxes are
unaffected (they have whichever pattern they were born with).
The km-presence binary in S3 can stay — it's harmless when
nothing references it.

## Testing

- **Unit tests** (`cmd/km-presence/main_test.go`): table-driven
  per signal. Mock `who` output via injectable command runner;
  fake `/var/mail/km/new/` and `/run/km/last-slack-inbound`
  with `t.TempDir()`; mock `pgrep` output. Cover positive,
  negative, and "stamp file missing" cases for each.
- **Integration test on a live sandbox**: provision with new
  code, then for each of the 5 signals: trigger it in
  isolation, verify a `source:"presence"` event lands in
  CloudWatch within 90s, verify journald shows the right
  signal label.
- **Resilience smoke**: `systemctl restart km-presence`
  mid-flight; verify next tick fires with reset stamp file
  semantics. `kill -9` the daemon; verify auto-restart < 10s.
- **Bug-fix regression**: provision new-style sandbox, open
  `km shell`, `Ctrl-D`, then `aws ssm send-command` to check
  `pgrep -f km-presence` count (must be 1) and `pgrep -f
  '_km_heartbeat\|sleep 60'` for sandbox-user processes
  (must be 0 ignoring real pollers' sleeps). Confirms the
  orphan multiplication is gone.

## Out of scope (deferred)

- **Operator-side enrichment of `km list`.** The daemon
  emits `source:"presence"`; `computeIdleRemaining` could
  later display *which* signal is keeping a sandbox awake
  (e.g., "IDLE: 60m (presence: ssm,agent)"). Useful but
  separate from the reliability fix. File as a follow-up.
- **Per-signal weighting / decay.** Current emit rule is
  boolean OR. If we later find one signal is too noisy
  (most likely candidate: utmp stale entries), we can
  add per-signal cooldowns without changing the daemon's
  external contract.
- **Replacing the IdleDetector's CloudWatch poll with
  local IPC.** Long-term win for cost and latency, but
  this PRD is scoped to the emitter side. Filed as
  potential follow-up phase.

## Open questions

None at PRD time. Daemon name `km-presence` is suggested;
operator may prefer `km-liveness` or `km-keepalive`. Trivial
to bikeshed during planning.
