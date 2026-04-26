---
title: km agent --claude leaves a residual sh prompt after Claude exits
area: cli
created: 2026-04-25
---

### Problem
After running `km agent --claude <id>` and exiting Claude (`/exit` or Ctrl-D), the SSM session does not close cleanly. Instead the user lands at a `sh-5.2$` prompt and must type `exit` multiple times to get back to their local shell.

Reproduction (observed during Phase 61-03 UAT, 2026-04-25):

```
[claude session]
> /exit
Resume this session with:
claude --resume 4567bb09-3ef0-40dd-a8e1-42cbc7fd833a
sh-5.2$ exit
exit
exit
^C
```

Expected: Claude exit → SSM session "Exiting session..." message → back to local prompt in one step.

### Root Cause Hypothesis
The KM-Sandbox-Session shellProfile runs `bash -lc "source ...; cd /workspace; exec claude"` (per `internal/app/cmd/agent.go:305-307`). When claude exits, `bash -lc` exits, and the parent sh that was evaluating the shellProfile conditional should also exit. But the SSM agent's Standard_Stream session apparently spawns a fallback interactive `sh` after the shellProfile command terminates — that's the `sh-5.2$` prompt the user lands at.

This is an interaction between:
- The KM-Sandbox-Session shellProfile conditional (`[ -z "{{ command }}" ] && exec bash -l || bash -lc "{{ command }}"`)
- AWS SSM agent behavior for Standard_Stream sessions when `shellProfile.linux` content exits

Worth confirming with a small spike: run a custom Standard_Stream doc with `shellProfile.linux: "echo done; exit 0"` and see if the session closes immediately or drops to a fallback shell.

### Solution
Two candidate fixes:

1. **Force session termination at end of shellProfile.** Append `&& exit 0` or use `exec ... ; exit 0` patterns inside the shellProfile so when the inner command exits, sh exits too. May need testing — depends on whether the SSM agent honors the exit.

2. **Drop the conditional pattern in favor of a wrapper script.** Add `/usr/local/bin/km-session-entry` to userdata; shellProfile.linux becomes `exec /usr/local/bin/km-session-entry "{{ command }}"`. The wrapper handles the empty/non-empty branches AND ensures clean exit semantics. (Already proposed in the related todo `ssm-session-shellprofile-echo.md` for cosmetic reasons; would solve both at once.)

Lean: option (2). Combine with the shellProfile-echo cleanup todo into a single follow-up phase.

### Files
- `internal/app/cmd/agent.go:300-323` — `--claude` callsite
- `internal/app/cmd/agent.go:373-404` — `attach` callsite (likely same issue when tmux attach exits)
- `internal/app/cmd/agent.go:557-571` — `run --interactive` callsite (likely same issue when tmux session ends)
- `infra/modules/ssm-session-doc/v1.0.0/main.tf` — current parameterized shellProfile
- `pkg/compiler/userdata.go` — would add the wrapper script

### Notes
- Surfaced during Phase 61-03 UAT Task 3 (km agent --claude). Functional correctness fine; user can still exit. Cosmetic + ergonomic.
- Worth verifying behavior of `km agent attach` (Task 4) and `km agent run --interactive` (Task 5) — likely have the same residual-sh issue when tmux exits.
