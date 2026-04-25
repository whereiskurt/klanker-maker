---
title: SSM-initiated sandbox sessions never land in km.slice cgroup
area: ebpf
created: 2026-04-25
---

### Problem
The `/usr/local/bin/km-sandbox-shell` wrapper and `/etc/profile.d/km-cgroup.sh` (both from `pkg/compiler/userdata.go`) are intended to place sandbox-user processes into `/sys/fs/cgroup/km.slice/km-<sandbox-id>.scope` so the eBPF enforcer's cgroup-attached BPF programs (connect4, sendmsg4, sockops, egress) apply. They don't work for SSM-initiated sessions:

- `sudo -u sandbox -i` path (today's `km shell` non-root and `km agent` paths via `AWS-StartInteractiveCommand`): wrapper IS invoked, but `echo $$ > km.slice/.../cgroup.procs` fails silently. cgroup v2 delegation requires write on the common ancestor of source (`user.slice/user-1001.slice/session-X.scope`, set up by pam_systemd) and target (`km.slice/...`); sandbox user has neither. Process ends up in `user.slice/user-1001.slice/session-X.scope`.
- `runAsDefaultUser: sandbox` path (proposed Standard_Stream doc, separate phase): SSM agent does setuid + spawns `sh` directly, never invoking the user's login shell. Process inherits `system.slice/amazon-ssm-agent.service`.

Verified empirically on `learn-d8530b6b` (2026-04-25): `cat /sys/fs/cgroup/km.slice/km-learn-d8530b6b.scope/cgroup.procs` is empty while a sandbox session is active. eBPF enforcer is healthy and TLS uprobes still capture (uprobes attach to library symbols, not cgroups), but cgroup-attached BPF programs do not apply.

Impact: profiles using `enforcement: ebpf` (no iptables fallback) get no kernel-level network enforcement for SSM-initiated shells. `proxy` and `both` profiles are unaffected because iptables DNAT operates at netfilter level regardless of cgroup membership.

### Solution
Two candidates; needs a quick spike to choose:

1. **systemd-run cgroup placement.** Wrap session entry in `systemd-run --scope --slice=km.slice --unit=km-<id>-session-<uuid> --uid=sandbox -- /bin/bash --login`. systemd has the privileges to move processes across slices, and `--scope` keeps the spawned process attached to the caller's stdio/PTY. Likely needs root to invoke (so SSM-side, before drop to sandbox).

2. **Setuid helper.** Small C binary `/usr/local/sbin/km-cgroup-join` that writes `$$` into the target scope. Owned root, mode 4755, tightly scoped (only writes into `/sys/fs/cgroup/km.slice/km-*.scope/cgroup.procs`). Lower-tech, more code to audit.

Either way, also rework SSM session entry so sandbox user IS placed before the user's shell runs — current wrapper-after-the-fact is structurally the wrong moment.

### Files
- `pkg/compiler/userdata.go:1437-1459` — wrapper + profile.d hooks (the broken mechanism)
- `pkg/compiler/userdata.go:1357-1365` — cgroup creation + chown sandbox group on cgroup.procs (well-intentioned but cgroup v2 won't honor it for cross-slice moves)
- `internal/app/cmd/shell.go`, `internal/app/cmd/agent.go` — session entry callsites that would invoke the new placement helper

### Notes
- Surfaced during design of the `km shell` Ctrl+C fix (Standard_Stream doc with `runAsDefaultUser: sandbox`). That fix is at parity with current eBPF behavior — neither path lands in km.slice — so it's safe to ship the Ctrl+C fix without solving this first.
- Don't forget docker substrate path: same cgroup model? Probably handled differently (container = cgroup) but worth confirming.
