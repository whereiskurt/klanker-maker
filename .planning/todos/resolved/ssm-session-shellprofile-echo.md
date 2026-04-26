---
title: KM-Sandbox-Session shellProfile literal echoed twice on login
area: ssm
created: 2026-04-25
---

### Problem
On `km shell <id>` (or any `km agent` interactive subcommand), the user sees the raw shellProfile.linux content twice before reaching the bash login prompt:

```
Starting session with SessionId: ...
[ -z "" ] && exec bash -l || bash -lc ""
sh-5.2$ [ -z "" ] && exec bash -l || bash -lc ""
[sandbox@<host> bin]$
```

This is the SSM agent invoking sh in interactive mode and echoing the shellProfile content as it executes. It's purely cosmetic — the conditional works correctly, the user lands in a bash login shell as the sandbox user, eBPF/audit hooks fire as expected.

### Solution
A few options to evaluate:

1. **Use `set +v` style suppression in the shellProfile** — wrap with `{ ... } 2>/dev/null` or stuff it into a function call so the line doesn't echo. Risk: SSM agent might interpret pipes/redirects oddly inside `shellProfile.linux` template substitution.

2. **Use a tiny wrapper script on the sandbox** — drop `/usr/local/bin/km-session-entry` (created at userdata time) that takes a single arg and either execs `bash -l` (empty) or `bash -lc "$1"`. Then shellProfile.linux becomes `/usr/local/bin/km-session-entry "{{ command }}"`. Cleaner and doesn't have the conditional inline. Slight scope expansion (touch userdata.go).

3. **Move the conditional into the SSM document inputs** using two separate documents (one for empty/interactive, one for command). More complex, multiple resources.

Lean: option (2) — a one-line shell script on the sandbox is the right level of abstraction. The current inline conditional is a placeholder pattern that AWS docs use; production should use a proper entry script.

### Files
- `infra/modules/ssm-session-doc/v1.0.0/main.tf` — current parameterized shellProfile
- `pkg/compiler/userdata.go` — would add the wrapper script

### Notes
- Surfaced during Phase 61-03 UAT (2026-04-25). Functional correctness is fine; this is cosmetics.
- Combine with the `_km_heartbeat` SIGINT fix and any future SSM session ergonomics work into a single follow-up phase.
