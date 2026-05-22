# `km create --prompt` — on-box prompt queue at provision time — design

**Status:** Drafted from brainstorming session — awaiting user review before plan-out.
**Author:** brainstorming session, 2026-05-19.
**Date:** 2026-05-19.

## Problem

`km agent run <sb> --prompt "..." --auto-start --wait` already exists for one-off agent invocations, and `km at` already schedules them. What's missing is the common operator pattern *"spin up a sandbox and immediately give it a multi-step plan to execute as soon as Claude is ready"* — for example:

1. Clone repo X and pull the latest main.
2. Open a PR with the linter fix.
3. Publish the release once CI is green.

Today the operator has to: run `km create`, wait for the sandbox to be reachable, optionally `claude login` (direct-API mode), then issue three separate `km agent run` calls in sequence — manually waiting on each. The composition is fully manual, error-prone, and the queue lives in the operator's terminal history.

This spec introduces a thin operator-side shortcut that bundles those `km agent run` calls into `km create` itself, queues them on the box, and drains them in order once Claude is authenticated. It is *deliberately* the smallest possible composition over existing primitives — no schema changes, no Lambda changes, no Terragrunt churn. If the pattern hardens into something more structured (declarative profile-embedded plans, reusable playbooks with conditions and DAGs), those would be follow-on designs (sketched in § Future work).

## Goals

- One CLI shortcut: `km create profile.yaml --prompt … [--prompt …]…` provisions the sandbox and queues one or more prompts that execute in order once Claude auth is available.
- `--prompt` supports both literal strings and `@filename` file references (curl convention).
- Queue lives on-box in `/workspace/.km-agent/queue/` so it survives `km create` exiting, operator network drops, sandbox reboots, and operator-machine reboots.
- Auth wait is **indefinite** — the queue patiently waits until Claude can answer. No timeout, no retry, no abandonment.
- Prompt failure stops the chain. Remaining prompts are marked `skipped` and visible in the queue listing.
- Each queued prompt becomes a normal `km agent run` invocation on the box, so it appears in `km agent list <sb>` and is attachable via `km agent attach`.

## Non-goals (YAGNI cuts deferred to future specs)

- **No declarative `spec.agent.prompts: []` in SandboxProfile.** That was option B in brainstorming; if operators end up always running the same prompt sequence with a given profile, *then* it earns a schema field. Not before.
- **No reusable playbook file format** (`km play plan.yaml <sb>`). That was option C; same justification.
- **No DAG, no `when:` conditions, no `needs:` dependencies.** It's a linear chain.
- **No per-prompt timeout, no per-prompt retry.** Claude's own behavior governs each run; failure stops the chain.
- **No templating / variable substitution** inside prompt text. `--prompt @file.txt` is a verbatim file read.
- **No new authentication mode**. Bedrock IAM and direct-API `claude login` both work unchanged; the queue runner just waits for whichever auth the profile expects.
- **No `km resume --prompt` in v1.** Could be added trivially later (the queue dir is already on the persistent volume), but out of scope for the initial shortcut.

## CLI surface

```text
km create profile.yaml \
  --prompt "clone repo X and pull main" \
  --prompt @open-pr.txt \
  --prompt @publish.txt \
  [--wait] [--no-bedrock] [--auto-start-deps-of-prompts]
```

| Flag | Behavior |
|---|---|
| `--prompt <text-or-@file>` | Repeatable. Bare string is used literally. `@filename` reads the file verbatim — same convention as `curl -d @file`. If the literal first character of an intended prompt happens to be `@`, the operator escapes by passing `@@literal-at-symbol-prompt`. |
| `--wait` | `km create` blocks until the queue drains or the first failure. Default: `km create` exits as soon as the sandbox is reachable AND the queue files are written AND the queue runner is started. |
| `--no-bedrock` | Passed through to every queued run (writes to each entry's `meta.json`). |

**Exit semantics:**

- Default (no `--wait`): `km create` exits `0` once queue is enqueued + runner is started. Subsequent queue failures don't retroactively fail the `km create` call.
- `--wait`: `km create` exits `0` if all prompts succeeded, non-zero with the first failed prompt's exit code if any failed.

**Backward compatibility:** `km create` without `--prompt` is unchanged. No existing flag/behavior moves.

## Architecture

The data plane is three files in `/workspace/.km-agent/queue/` per prompt, and a single bash script that drains the queue. Everything else is reuse of existing primitives.

### Queue layout (on-box)

```text
/workspace/.km-agent/
├── queue/
│   ├── 001.prompt           # raw prompt text, UTF-8
│   ├── 001.meta.json        # { "no_bedrock": false, "created_at": "...", "status": "pending" }
│   ├── 002.prompt
│   ├── 002.meta.json
│   ├── 003.prompt
│   └── 003.meta.json
├── runs/                    # existing — populated by the queue runner via `km agent run`
│   └── <ts>/output.json
└── km-queue.log             # runner stdout/stderr (for `km agent list --queue` debugging)
```

- Filename ordering (`001`, `002`, `003`) defines execution order.
- `status` is one of `pending | running | done | failed | skipped`.
- The queue dir mirrors the existing `/workspace/.km-agent/runs/` layout for consistency.
- `/workspace` is the persistent volume (already mounted, used by `runs/`), so the queue survives reboot/pause/resume.

### Push path: `km create` → SSM → on-box files

Touch points in `internal/app/cmd/create.go`:

1. Parse repeatable `--prompt` flag. For each value: if it starts with `@`, read the file (error if missing/unreadable); else use as literal. Strip leading `@@` → `@` for the escape case.
2. After the existing "sandbox is reachable via SSM" gate, push each prompt as two files via SSM `RunCommand` (one SSM invocation that writes the whole batch atomically): `001.prompt`, `001.meta.json`, etc.
3. Kick the queue runner with a one-shot SSM command: `systemctl start km-queue` (the systemd unit wraps the runner in a tmux session so `km shell` users can `tmux attach -t km-queue` to watch).
4. If `--wait`: poll `/workspace/.km-agent/queue/*.meta.json` via SSM until all are `done | failed | skipped`. Exit with the appropriate code.

No new infra. Uses the existing SSM client + the existing `cfg`-bound sandbox lookup.

### Queue runner (on-box bash)

A ~80-line bash script `/usr/local/bin/km-queue-runner` seeded onto the box, launched by a systemd unit `km-queue.service` that wraps it in a tmux session named `km-queue`. The systemd unit gives us auto-restart on reboot (`Restart=always`); the tmux wrapper mirrors the existing `km-agent` tmux pattern so `km shell` users can `tmux attach -t km-queue` to watch the loop. Loop:

```text
on start:
    Reconcile: any entries in `running` state (left over from a reboot) are
    reset to `pending` so the loop picks them up again.

while pending entries exist:
    1. Wait-for-auth probe (indefinite — see § Auth probe).
    2. Pick lowest-numbered `pending` entry.
    3. Mark it `running` (atomic file replace).
    4. Invoke the existing on-box `claude` binary the same way `km agent run` does
       (writes to /workspace/.km-agent/runs/<ts>/output.json).
    5. On exit 0:   mark entry `done`. Continue.
    6. On non-zero: mark entry `failed`. Mark all remaining pending entries `skipped`. Exit.
done
```

The runner is intentionally bash, not a Go binary, so it doesn't need to be cross-compiled and shipped alongside `km`. It's small enough to fit in a `configFiles` blob.

### Auth probe (indefinite wait, mode-dependent)

The runner asks one of two questions in a 5-second poll loop:

| Mode | Probe |
|---|---|
| **Bedrock** (default, no `--no-bedrock`) | `aws bedrock-runtime invoke-model --model-id anthropic.claude-haiku-4-5-20251001-v1:0 --body '{"messages":[{"role":"user","content":"hi"}],"max_tokens":1,"anthropic_version":"bedrock-2023-05-31"}' --cli-binary-format raw-in-base64-out /tmp/probe.out` — exit 0 = ready. |
| **Direct API** (`--no-bedrock`) | `[ -s ~/.claude/credentials.json ] && jq -e . ~/.claude/credentials.json > /dev/null` — file exists, non-empty, parses as JSON. Token-freshness is *not* probed; if the token is expired, the first real `claude` invocation will fail loudly and stop the chain (per the operator's chosen failure policy). |

Both probes wait forever. The runner logs the probe result every 5 minutes to `km-queue.log` so an attached operator can see what it's waiting for.

The user's `project_claude_oauth_two_files` memory is relevant for direct-API mode: the operator must either pre-seed *both* `~/.claude/credentials.json` *and* the `hasCompletedOnboarding` / `lastOnboardingVersion` fields in `~/.claude.json` (via `spec.execution.configFiles`), or `km shell` in and complete `claude auth login` interactively before the queue can drain. The runner cannot do this for them.

## Failure handling

Per brainstorming choice "Auth: wait forever. Fail: stop the chain":

| Scenario | Behavior |
|---|---|
| Sandbox unreachable via SSM after timeout | `km create` errors out before any queue files are written — no partial state. Existing behavior. |
| Queue file push fails (SSM error mid-push) | `km create` errors out; operator can re-run `km create` (will overwrite — fresh sandbox path) or `km shell` and inspect partial state. |
| Auth never lands | Queue waits indefinitely. Operator recovery options below. |
| Prompt 1 succeeds, prompt 2 fails | Prompt 2 marked `failed`, prompt 3 marked `skipped`. Runner exits. `km agent list --queue` shows the final state. |
| Sandbox reboots mid-queue | tmux session dies; queue files survive. `km-queue.service` (`Restart=always`) re-launches the runner on boot; the runner's start-time reconcile step resets any `running` entry back to `pending` so it gets re-executed from scratch. |
| Operator wants to abandon a stalled queue | `km shell <sb>` and `rm /workspace/.km-agent/queue/*` (no separate `km agent queue clear` subcommand in v1). |
| Operator wants to retry a failed entry | `km shell <sb>` and edit the `meta.json` status back to `pending`, then restart the runner. (v1 manual; if this becomes common, add `km agent queue retry <sb> <n>` later.) |

## Observability

- `km agent list <sb>` already lists every entry in `/workspace/.km-agent/runs/`. Once a queued prompt starts executing, its run appears here unchanged.
- **New:** `km agent list <sb> --queue` — additional view that lists the entries in `/workspace/.km-agent/queue/` with their statuses (`pending | running | done | failed | skipped`) and a snippet of the prompt text. Implemented in `internal/app/cmd/agent.go` as an alternate code path inside the existing `agent list` command.
- `km agent attach <sb>` works unchanged — attaches to whichever `km-agent` tmux session is active. To watch the *runner* loop itself (not the active prompt), `km shell <sb>` and `tmux attach -t km-queue`.
- The runner's `km-queue.log` is the source of truth for "why is the queue not progressing" — it logs auth probe attempts, entry transitions, and the exit code of each `claude` invocation.

## Implementation breakdown

Estimated half-day to a day of work, mostly in five files:

| Piece | File | Rough size |
|---|---|---|
| Repeatable `--prompt` flag + `@file` parsing (incl. `@@` escape) | `internal/app/cmd/create.go` | ~40 lines + test |
| Push queue files via SSM after sandbox-reachable gate | `internal/app/cmd/create.go` | ~30 lines |
| `--wait` polling loop (SSM `ListInventoryEntries` or `RunCommand` ls/cat) | `internal/app/cmd/create.go` | ~25 lines |
| `km-queue-runner` bash script + systemd unit (or tmux launcher) | `pkg/profile/configfiles/` or AMI bake | ~80 lines bash |
| `km agent list --queue` view | `internal/app/cmd/agent.go` | ~40 lines |
| Unit tests: `@file` parsing, `@@` escape, meta.json schema, runner state-machine table | `create_test.go`, `agent_test.go`, `runner_test.sh` | ~120 lines |

**Build dependency:** `make build` per `feedback_rebuild_km` — no new toolchain or module dependencies.

**Test plan (real-AWS UAT):**

1. `km create profiles/learn.yaml --prompt "echo hello" --wait` — exits 0, `km agent list <sb>` shows one run with `"hello"` output.
2. `km create profiles/learn.yaml --prompt @plan.txt --prompt "publish" --wait` — both run in order, second run only after first succeeds.
3. `km create profiles/learn.yaml --prompt "exit 1" --prompt "should not run"` (no `--wait`) — first marked `failed`, second marked `skipped`. Verify via `km agent list --queue`.
4. `km create profiles/learn.yaml --no-bedrock --prompt "..."` against a sandbox without pre-seeded credentials — queue waits indefinitely; `km shell` + `claude auth login`; queue then drains. (Manual UAT, operator-in-loop.)
5. `km pause <sb>` mid-queue; `km resume <sb>`; verify the running entry restarts and chain continues. (Tests systemd-unit-on-boot path.)

## Out of scope (revisit if patterns emerge)

- Profile-embedded prompt sequences (`spec.agent.prompts: []`) — brainstorming option B.
- Standalone playbook files (`km play plan.yaml <sb>`) with conditions/DAG/templating — brainstorming option C.
- Cross-sandbox orchestration (prompt N depends on prompt M from a sibling sandbox).
- `km resume --prompt` to re-arm a paused sandbox with a fresh queue.
- Web-UI / dashboard view of queue state across all sandboxes.

## Open questions for v1

The following were flagged for operator sanity-check at the end of brainstorming; **the user said "yes write it up" without explicitly answering them, so this spec proceeds with the defaults below and notes the alternatives**:

1. **Bash runner vs. real Go binary.**
   *Default chosen:* bash script seeded via `configFiles`. ~80 lines, no new build/distribution path, easy to inspect with `cat`.
   *Alternative:* compile `cmd/km-queue-runner/` as a Go binary, ship it via S3 like other sidecars. Heavier, but earns its keep if the runner ever needs typed state, retries, structured logging.
   *Decision needed:* operator confirmation before plan-out.

2. **Seed via `initCommands` vs. bake into AMI.**
   *Default chosen:* `spec.execution.configFiles` writes `/usr/local/bin/km-queue-runner` + systemd unit `/etc/systemd/system/km-queue.service` on every fresh sandbox. Works on any base image. Slightly slower first boot (one extra file write) but zero AMI maintenance burden.
   *Alternative:* bake into the operator-baked AMI (`km ami bake`). Faster boot, but pins the runner version to the AMI bake date — operator updates to `km` won't propagate to running AMIs.
   *Decision needed:* operator confirmation before plan-out.

3. **`--prompt` on `km resume` too?**
   *Default chosen:* `km create` only in v1. The queue dir is on the persistent volume so technically `km resume --prompt X` would work the same way, but it's out of scope until a real use case asks for it.
   *Alternative:* add it in v1 for ~10 extra lines in `internal/app/cmd/resume.go`.
   *Decision needed:* operator confirmation before plan-out.

## Future work (sketches, not commitments)

- **Profile-embedded prompts (option B).** Once a few profiles want the same canned prompt sequence on every `km create`, add `spec.agent.prompts: [string]` to the SandboxProfile schema. The compiler bakes prompts into a queue-init file dropped at provision time; the on-box runner picks them up the same way `--prompt` flags do today. CLI `--prompt` flags would *append* to the profile sequence (or `--prompt-replace` to override).
- **Playbook files (option C).** If operators want conditions, DAGs, or shared plans across sandboxes, introduce `km play <plan.yaml> <sb>` with a small DSL (`steps:`, `needs:`, `when:`). Initially operator-side composer over `km agent run`; later first-class queue object if it sticks.
- **`km agent queue` subcommand.** Promote the `--queue` view + add `clear`, `retry`, `pause` subcommands once manual operator workflows in `km shell` indicate the patterns.
- **Templating.** Variable substitution (`${SANDBOX_ID}`, `${OPERATOR_EMAIL}`, …) inside prompt text. Trivial once a real use case names which variables matter.
