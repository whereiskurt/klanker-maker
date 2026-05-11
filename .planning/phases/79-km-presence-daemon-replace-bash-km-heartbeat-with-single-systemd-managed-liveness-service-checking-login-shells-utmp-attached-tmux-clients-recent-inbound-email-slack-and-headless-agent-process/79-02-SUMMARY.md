---
phase: 79-km-presence-daemon
plan: "02"
subsystem: compiler/userdata
tags: [userdata, systemd, presence-daemon, heartbeat-removal, regression-tests]
dependency_graph:
  requires: ["79-00"]
  provides: ["userdata-presence-wiring"]
  affects: ["pkg/compiler/userdata.go", "pkg/compiler/userdata_phase79_test.go"]
tech_stack:
  added: []
  patterns: ["heredoc-systemd-unit", "conditional-systemctl-enable", "touch-stamp-file"]
key_files:
  created:
    - pkg/compiler/userdata_phase79_test.go
  modified:
    - pkg/compiler/userdata.go
decisions:
  - "km-presence systemd unit placed unconditionally (outside {{- if .SlackInboundEnabled }}), joining km-http-proxy/km-audit-log/km-tracing as core sidecars"
  - "km-presence appended before {{- if .SandboxEmail }} template branch so it's always enabled regardless of email/slack flags"
  - "Unit uses 'UNIT' heredoc marker (single-quote so bash doesn't expand env vars); Go template expands {{ .SandboxID }} before bash sees the heredoc"
  - "New tests in separate file (userdata_phase79_test.go) to avoid touching the large existing userdata_test.go"
metrics:
  duration: "174s"
  completed: "2026-05-11"
  tasks: 1
  files: 2
---

# Phase 79 Plan 02: Userdata Heartbeat Removal + Presence Daemon Wiring Summary

One-liner: Removed bash `_km_heartbeat` function/trap from EC2 sandbox userdata and wired systemd `km-presence.service` + S3 fetch + slack-inbound touch stamp into the bootstrap template.

## What Was Built

Applied 5 edits to `pkg/compiler/userdata.go` that collectively replace the bash background heartbeat pattern with a systemd-managed presence daemon:

1. **EDIT 1 (REMOVE heartbeat)** — Deleted the `_km_heartbeat()` function (24 lines: comment block through `trap 'kill -9 $_KM_HEARTBEAT_PID 2>/dev/null' EXIT`). The `HOOK` heredoc closing marker remains, now sitting immediately after the LearnMode `{{- end }}`.

2. **EDIT 2 (S3 fetch)** — Added `aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-presence" /opt/km/bin/km-presence` at **line 898**, after the `km-slack` fetch. The existing `chmod +x /opt/km/bin/km-*` wildcard covers it.

3. **EDIT 3 (systemd unit)** — Added `cat > /etc/systemd/system/km-presence.service << 'UNIT' ... UNIT` heredoc at **lines 1791-1804**, unconditionally between the `{{- end }}` of `{{- if .SlackInboundEnabled }}` and `{{- if .VSCodeEnabled }}`. Mirrors the `km-slack-inbound-poller.service` shape.

4. **EDIT 4 (slack stamp)** — Added `touch /run/km/last-slack-inbound` at **line 1493**, immediately after the "Turn complete" echo inside the `if [ -n "$NEW_SESSION" ]` success branch, before the `else` (agent failure branch). Indented with 4 spaces to match surrounding block.

5. **EDIT 5 (systemctl enable/restart)** — Appended `km-presence` to BOTH `systemctl enable` and `systemctl restart` lines in BOTH enforcement branches (4 lines total):
   - **Line 2367**: eBPF branch `enable`
   - **Line 2368**: eBPF branch `restart`
   - **Line 2374**: proxy branch `enable`
   - **Line 2375**: proxy branch `restart`

## Final Line Numbers (After Edits)

| Change | Location |
|---|---|
| km-presence S3 fetch | line 898 |
| `_km_audit()` function (preserved) | line 1031 |
| `PROMPT_COMMAND="_km_audit` (preserved) | lines 1052, 1054 |
| `touch /run/km/last-slack-inbound` | line 1493 |
| `km-presence.service` heredoc start | line 1791 |
| `km-presence.service installed` echo | line 1804 |
| eBPF `systemctl enable km-presence` | line 2367 |
| eBPF `systemctl restart km-presence` | line 2368 |
| proxy `systemctl enable km-presence` | line 2374 |
| proxy `systemctl restart km-presence` | line 2375 |

## Test Results

New file: `pkg/compiler/userdata_phase79_test.go` (newly created — not an extension of existing files).

| Test | Status | What It Asserts |
|---|---|---|
| `TestUserdata_NoBashHeartbeat` | GREEN | `_km_heartbeat()`, `_KM_HEARTBEAT_PID`, EXIT trap are absent |
| `TestUserdata_KmAuditHookPreserved` | GREEN | `_km_audit()`, PROMPT_COMMAND, audit event still present |
| `TestUserdata_PresenceSidecarInstalled` | GREEN | S3 fetch, unit path, Description, ExecStart, install echo present |
| `TestUserdata_PresenceEnabled_BothBranches` | GREEN | km-presence in `systemctl enable` + `systemctl restart` for proxy/ebpf/both |
| `TestUserdata_SlackInboundTouchesPresenceStamp` | GREEN | touch line exists between Turn complete and agent run failed |

## Open Question Resolution

**Q: systemctl enable block scope (RESEARCH Open Question 1)**

Resolved: `km-presence` appended to existing `systemctl enable`+`restart` unit lists in BOTH enforcement branches (eBPF at lines 2367-2368, proxy at lines 2374-2375), placed BEFORE `{{ if .SandboxEmail }}` and `{{ if .SlackInboundEnabled }}` template branches. This makes km-presence unconditional — it joins km-http-proxy, km-audit-log, km-tracing as a core baseline sidecar. No separate `{{- if }}` block was needed.

## compose.go Status

`pkg/compiler/compose.go` is **unmodified**. Docker substrate retains bash heartbeat per PRD Pitfall 1 (Docker substrate is out of scope for Phase 79). Confirmed by `git diff --name-only` showing only `pkg/compiler/userdata.go`.

## Notes for Plan 79-04 (Doctor Check)

The systemd unit is named **`km-presence`** (as registered with `systemctl enable km-presence`). The unit file lives at `/etc/systemd/system/km-presence.service`. The doctor check's `presence_daemon_healthy` test fixture should verify:
- `systemctl is-active km-presence` returns `active`
- `/run/km/` directory exists (the daemon writes timestamps there)
- Unit name: `km-presence` (not `km-presence-daemon` or `km-heartbeat-daemon`)

## Deviations from Plan

None — plan executed exactly as written. All 5 edits applied at the documented anchor points with no drift from researched line numbers (edits 1-4 shifted by ~24 lines due to EDIT 1 removal; final line numbers above reflect post-edit state).

## Self-Check

Files created/modified:
- [x] `pkg/compiler/userdata.go` — modified
- [x] `pkg/compiler/userdata_phase79_test.go` — created

Commits:
- [x] `485e3d0` — feat(79-02): wire km-presence daemon into sandbox userdata bootstrap

## Self-Check: PASSED
