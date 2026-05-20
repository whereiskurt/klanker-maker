# Phase 86: km-create-prompt-queue — Context

**Gathered:** 2026-05-19
**Status:** Ready for planning
**Source:** Brainstorming session → `docs/superpowers/specs/2026-05-19-km-create-prompt-queue-design.md` (PRD-equivalent).

<domain>
## Phase Boundary

Add a repeatable `--prompt <text-or-@file>` flag to `km create` and a thin on-box queue runner that drains those prompts sequentially once Claude auth is available. Composition over existing `km agent run` primitives — explicitly NOT a schema change, Lambda change, or Terragrunt change. Also adds a `km agent list --queue` view for visibility.

What's IN this phase:
- CLI flag parsing on `km create` (repeatable `--prompt`, `@file` reads, `@@` escape, `--wait`, `--no-bedrock` passthrough).
- On-box queue file layout under `/workspace/.km-agent/queue/`.
- Bash runner script + systemd unit that drains the queue with reconcile-on-start + indefinite auth wait.
- Auth probe semantics (Bedrock invoke-model ping vs. `~/.claude/credentials.json` file check).
- `km agent list --queue` observability view.
- Seed mechanism (default: `configFiles` blob — runner + unit dropped per-create).
- Regression guard: `km create` without `--prompt` is byte-for-byte unchanged.

What's OUT (deferred to future specs):
- `spec.agent.prompts: []` in SandboxProfile schema (option B).
- Standalone `km play plan.yaml` playbooks with DAG/conditions/templating (option C).
- Per-prompt timeout, per-prompt retry, `when:` conditions, cross-sandbox orchestration.
- `km resume --prompt`, `km agent queue retry|clear|pause` subcommands.

</domain>

<decisions>
## Implementation Decisions

These are LOCKED — chosen during brainstorming with the user (2026-05-19). Planner: do NOT revisit these. Only revisit if a hard constraint surfaces during research that makes one unimplementable.

### CLI surface
- `--prompt` is repeatable. Single occurrence = one chained run. Multiple occurrences = N chained runs in declaration order.
- `@filename` prefix means "read this file verbatim, UTF-8". Missing file is an error before any SSM call (fail fast, operator-side).
- `@@` is the escape: `--prompt @@literal-at` becomes the literal string `@literal-at`.
- `--wait` is opt-in. Default behavior: `km create` returns as soon as the sandbox is reachable AND queue files are written AND the runner is kicked. Subsequent queue failures don't retroactively fail the exit code.
- `--wait` exit codes: 0 on all-done; otherwise the exit code of the first failing prompt's `claude` invocation.
- `--no-bedrock` flag on `km create` propagates to every queued prompt's meta.json (per-entry, not globally on the runner).

### Queue layout (on-box)
- Directory: `/workspace/.km-agent/queue/` — mode `0700`, owned by sandbox user.
- Filename format: `NNN.prompt` + `NNN.meta.json`. Zero-padded 3-digit index; numeric order = execution order.
- File permissions: `0600` (prompt may contain secrets — no world-readability).
- meta.json schema: `{"no_bedrock": bool, "created_at": ISO8601, "status": "pending|running|done|failed|skipped"}`.
- Existing `/workspace/.km-agent/runs/<ts>/output.json` layout is unchanged — queued prompts populate it the same way `km agent run` does today.

### Runner
- Bash script, ~80 lines, at `/usr/local/bin/km-queue-runner`. NOT a Go binary — explicit choice to avoid a new build/distribution target.
- Launched by systemd unit `/etc/systemd/system/km-queue.service` with **`Restart=on-failure`** (revised 2026-05-19 from spec's `Restart=always` after research surfaced a busy-loop hazard: with `Restart=always`, normal clean exit triggers immediate restart → CPU loop. `on-failure` restarts only on crash; on reboot/resume the unit auto-starts fresh from systemd's enabled state. Idle is handled by the runner's main loop sleeping while polling for `pending` entries, not by exiting). Unit's `ExecStart` wraps the script in `tmux new-session -d -s km-queue …` so `km shell` users can `tmux attach -t km-queue` to watch.
- Reconcile step on every start: any entry with `status="running"` is reset to `status="pending"` before the main loop begins. This is the reboot/pause-resume recovery mechanism.
- Failure policy is LINEAR-CHAIN: first non-zero exit marks that entry `failed`, marks all remaining `pending` entries `skipped`, and exits the runner. No retries.

### Auth probe (indefinite wait — by user's explicit choice)
- Bedrock mode (default): `aws bedrock-runtime invoke-model --model-id anthropic.claude-haiku-4-5-20251001-v1:0 --body '{"messages":[{"role":"user","content":"hi"}],"max_tokens":1,"anthropic_version":"bedrock-2023-05-31"}' --cli-binary-format raw-in-base64-out /tmp/probe.out` — exit 0 = ready. ~$0.000003 per probe.
- Direct-API mode (`--no-bedrock`): `[ -s ~/.claude/credentials.json ] && jq -e . ~/.claude/credentials.json >/dev/null`. Pure local check, no API call, no token consumption.
- Probe interval: 5 seconds.
- Probe-status logging: every 5 minutes, write a one-liner to `/workspace/.km-agent/km-queue.log` so an attached operator can see why the queue isn't progressing.
- No timeout. Queue waits forever. Operator recovery via `km shell` + `rm queue/*` if abandonment needed.

### Seed mechanism
- **Revised 2026-05-19 (research-driven):** seed runner script + systemd unit via **inline `userdata.go` heredoc blocks** alongside the existing `km-mail-poller.service` / `km-presence.service` precedent (around line 1831 of `pkg/compiler/userdata.go`). The originally-chosen `spec.execution.configFiles` mechanism was rejected after research surfaced a hard constraint: the `configFiles` write path runs `chown -R sandbox:sandbox` on the parent directory of every file it writes, which would corrupt ownership on `/etc/systemd/system/` if used to drop the unit file. AMI bake was the other alternative — also deferred per the same minimal-diff principle.
- **Behavioral consequence (acknowledged):** the runner script + systemd unit are now installed unconditionally on every EC2 sandbox, not per-profile-opt-in. The unit is `enabled` (auto-start on boot) but only `active` if there are queue entries to process — the runner's main loop sleeps on `pending`-watching when the queue is empty. R1 regression is therefore "unit installed + enabled, but inactive/idle (no `running`/`done`/`failed` entries) when no `--prompt` was used"; **NOT** "unit absent." BRIEF.md R1 is updated by this revision.
- **AMI bake** (the alternative noted in the original spec) is deferred — not in v1.

### Observability
- New flag: `km agent list --queue` (boolean, default false). When set, lists queue dir entries with: index, status, ISO timestamp, first ~80 chars of prompt text.
- Implementation: alternate code path inside the existing `agent list` Cobra command in `internal/app/cmd/agent.go`.

### Substrate scope
- EC2 substrate: full support.
- Docker substrate (`km create --docker`): NOT supported in v1. If `--prompt` is combined with `--docker`, return a clear error before provisioning. Document as "queue requires EC2 substrate (systemd needed)."

### Backward compatibility / safety guards
- `km create` without any `--prompt` flag MUST produce identical sandbox state to pre-Phase-86. No queue dir, no `km-queue.service`, no behavioral changes.
- Prompt-file reads happen operator-side BEFORE any SSM call — failure mode is "km create exits with file-not-found before touching AWS."

### Claude's Discretion (not specified by user, planner picks)
- Exact 3-digit padding format for queue filenames (`001`, `002`) is implementation detail — planner may choose 4-digit or other if there's reason.
- Bash test harness mechanics (bats, shellspec, plain bash + asserts) — planner picks the lightest fit.
- Exact format of `km agent list --queue` table columns — planner picks something that aligns with the existing `agent list` view.
- Whether systemd unit ships as separate file or inlined into bash runner via `cat <<EOF` — planner picks; both meet the spec.
- Whether queue-poll interval in `--wait` mode is 5s (matching runner's auth probe) or different — planner picks.

</decisions>

<specifics>
## Specific Files / Components

- `internal/app/cmd/create.go` — repeatable `--prompt` flag, `@file` parsing, `@@` escape, SSM push, `--wait` polling.
- `internal/app/cmd/create_test.go` — unit tests for flag parsing + file reads + escape + meta.json shape.
- `internal/app/cmd/agent.go` — `--queue` flag on `agent list` subcommand.
- `internal/app/cmd/agent_test.go` — unit tests for `--queue` view rendering.
- `pkg/profile/configfiles/km-queue-runner.sh` (new) — bash runner.
- `pkg/profile/configfiles/km-queue.service` (new) — systemd unit file.
- `pkg/profile/configfiles/km-queue-runner_test.sh` (new) — bash test harness.
- `pkg/compiler/userdata.go` OR `infra/modules/create-handler/v1.0.0/lambda/handler.py` — the seed-into-sandbox path. **Planner must pick during research** — minimal-diff option preferred.
- `OPERATOR-GUIDE.md` — section under `km create` documenting `--prompt`, `--wait`, recovery procedure.
- `CLAUDE.md` — one-line CLI bullet for the new `--prompt` flag.

## Reference patterns to follow

- `km agent run --auto-start --wait` (in `internal/app/cmd/agent.go`) — same SSM-push + poll structure that `--prompt` should mirror.
- `km-agent` tmux session pattern (existing) — `km-queue` follows the same conventions.
- `aws-services-overview` for the SSM RunCommand wiring already used by `km agent run`.
- Existing `spec.execution.configFiles` write path in the compiler — where the runner script + unit slot in.

</specifics>

<deferred>
## Deferred Ideas

Carried over from spec § Non-goals and § Future work — explicitly NOT in this phase:

- Profile-embedded `spec.agent.prompts: []` (spec option B). Revisit when several profiles want the same canned sequence.
- Reusable playbook files (spec option C) — `km play plan.yaml <sb>` with steps/needs/when/templating. Revisit when DAG semantics are actually needed.
- Per-prompt timeout, per-prompt retry, conditional execution.
- `km resume --prompt` to re-arm a paused sandbox.
- `km agent queue` subcommand (clear, retry, pause).
- Cross-sandbox orchestration ("prompt N runs only after sibling sandbox finishes prompt M").
- Web/dashboard view across sandboxes.
- Docker substrate support — out of v1 scope, document as EC2-only.

</deferred>

---

*Phase: 86-km-create-prompt-queue*
*Context derived from spec: docs/superpowers/specs/2026-05-19-km-create-prompt-queue-design.md*
