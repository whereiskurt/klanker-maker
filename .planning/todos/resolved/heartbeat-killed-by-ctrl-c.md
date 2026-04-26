---
title: _km_heartbeat killed by Ctrl+C at idle bash prompt
area: userdata
created: 2026-04-25
---

### Problem
`_km_heartbeat` (defined in `pkg/compiler/userdata.go:504-513` and the docker substrate equivalent in `pkg/compiler/compose.go:430-437`) is a background bash job started from `/etc/profile.d/km-audit.sh`. It pings the audit pipe every 60s to keep the sandbox alive against the idle-kill timer.

When the user presses Ctrl+C at an idle bash prompt (no foreground process), the shell's process group receives SIGINT. Bash itself ignores SIGINT at the prompt, but the backgrounded heartbeat function doesn't — it dies. The `trap 'kill $_KM_HEARTBEAT_PID 2>/dev/null' EXIT` in the parent shell can no longer kill it (already dead).

Net effect: the user keeps working in their interactive shell, but the idle-kill timer is no longer being reset. The sandbox can be auto-killed mid-session.

This bug was hidden before Phase 61 because Ctrl+C used to tear down the SSM session entirely (the `AWS-StartInteractiveCommand` doc semantics). Surfaced as a UAT artifact during Phase 61-03 verification.

### Solution
Make the heartbeat function ignore SIGINT/SIGTERM:

```bash
_km_heartbeat() {
  trap '' INT TERM
  while true; do
    sleep 60
    printf '...heartbeat...' > /run/km/audit-pipe 2>/dev/null
  done
}
_km_heartbeat &
_KM_HEARTBEAT_PID=$!
trap 'kill $_KM_HEARTBEAT_PID 2>/dev/null' EXIT
```

The parent shell's EXIT trap still kills the heartbeat when the shell terminates normally (which is what we actually want).

### Files
- `pkg/compiler/userdata.go:504-513` (EC2 substrate)
- `pkg/compiler/compose.go:430-437` (docker substrate — likely same fix)

### Notes
- Applies to new sandboxes only (userdata runs at instance create); existing sandboxes keep the broken hook until re-created.
- Worth a userdata test to verify Ctrl+C at idle prompt doesn't kill the heartbeat.
