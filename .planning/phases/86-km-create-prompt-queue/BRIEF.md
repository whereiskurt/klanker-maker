# Phase 86: km-create-prompt-queue

**Status:** Not planned yet.
**Source spec:** `docs/superpowers/specs/2026-05-19-km-create-prompt-queue-design.md`.
**Date drafted:** 2026-05-19.

## In scope

1. **CLI surface — `km create --prompt`**
   - Repeatable `--prompt <value>` flag on `km create`.
   - Value can be a literal string OR `@filename` (file is read verbatim, UTF-8).
   - Escape: a literal leading `@` is passed as `@@` (stripped to single `@` before send).
   - `--wait` flag: block `km create` until the queue drains (success) or first failure. Default = queue-and-return.
   - `--no-bedrock` flag: passes through to every queued prompt (writes to entry meta).
   - Exit codes: `--wait` returns 0 on all-success, non-zero on first-failure (propagate the failing run's exit code).

2. **On-box queue layout — `/workspace/.km-agent/queue/`**
   - `NNN.prompt` — raw UTF-8 prompt text.
   - `NNN.meta.json` — `{"no_bedrock": bool, "created_at": ISO8601, "status": "pending|running|done|failed|skipped"}`.
   - Numeric ordering (`001`, `002`, …) defines execution order.
   - Directory created with mode `0700`, owned by sandbox user.

3. **Push path — `internal/app/cmd/create.go`**
   - Parse repeatable `--prompt` after existing flag block. Resolve `@file` references operator-side (error if missing/unreadable). Strip leading `@@` to `@`.
   - After the existing "sandbox reachable via SSM" gate, push all queue files in a single SSM `RunCommand` invocation (atomic from the operator's POV).
   - Kick the queue runner via SSM: `systemctl start km-queue`.
   - If `--wait`: poll `/workspace/.km-agent/queue/*.meta.json` (SSM `RunCommand` ls/cat) until terminal; sleep ~5s between polls.

4. **On-box runner — `km-queue-runner` bash script**
   - ~80-line bash script at `/usr/local/bin/km-queue-runner`.
   - Launched by systemd unit `km-queue.service` (`Restart=always`); unit wraps invocation in `tmux new-session -d -s km-queue …` so `km shell` users can attach.
   - On start: reconcile — any entries in `running` state (left over from reboot) reset to `pending`.
   - Loop:
     1. Wait-for-auth probe (indefinite, 5s poll interval). Log probe outcome to `/workspace/.km-agent/km-queue.log` every 5 minutes.
     2. Pick lowest-numbered `pending` entry.
     3. Mark `running` (atomic write to temp file + rename).
     4. Invoke `claude` the same way `km agent run` does — write to `/workspace/.km-agent/runs/<ts>/output.json`.
     5. On exit 0: mark `done`, continue.
     6. On non-zero: mark this `failed`, mark all remaining `pending` entries `skipped`, exit.

5. **Auth probe (mode-dependent, both indefinite)**
   - **Bedrock mode** (default, no `--no-bedrock`): `aws bedrock-runtime invoke-model --model-id anthropic.claude-haiku-4-5-20251001-v1:0 --body '{"messages":[{"role":"user","content":"hi"}],"max_tokens":1,"anthropic_version":"bedrock-2023-05-31"}' --cli-binary-format raw-in-base64-out /tmp/probe.out` — exit 0 = ready.
   - **Direct-API mode** (`--no-bedrock`): file existence check + JSON parse: `[ -s ~/.claude/credentials.json ] && jq -e . ~/.claude/credentials.json >/dev/null`. Token-freshness NOT probed; if expired, first real run fails and chain stops.

6. **Observability — `km agent list --queue`**
   - New `--queue` flag on the existing `km agent list <sb>` command.
   - When set, lists entries in `/workspace/.km-agent/queue/` with: index, status, timestamp, first ~80 chars of prompt text.
   - Implementation: alternate code path inside the existing `agent list` Cobra command in `internal/app/cmd/agent.go`.

7. **Seed mechanism — `configFiles` blob**
   - Runner script + systemd unit shipped via `spec.execution.configFiles` (existing mechanism). No AMI bake required, no new compile target.
   - Mock implementation: extend the userdata template or the create-handler Lambda to drop these files before `initCommands` run; the unit auto-starts on boot.
   - **Decision deferred to planning:** whether to ship via userdata (simpler, always-on) or `configFiles` (per-profile opt-in). Spec leaned `configFiles`; planner should validate against the create-handler Lambda's current write path.

## Out of scope (deferred per spec § Non-goals)

- Profile-embedded prompts (`spec.agent.prompts: []` in SandboxProfile schema) — spec option B.
- Standalone playbook files (`km play plan.yaml <sb>`) with DAG / conditions — spec option C.
- Per-prompt timeout, per-prompt retry, `when:` conditions, templating, cross-sandbox orchestration.
- `km resume --prompt` to re-arm a paused sandbox.
- `km agent queue clear/retry/pause` subcommands (manual recovery via `km shell` is fine for v1).
- Web/dashboard view across sandboxes.

## Safety guards

- `km create` without `--prompt` MUST be byte-for-byte unchanged in behavior. Pure additive flag.
- Queue files written with mode `0600`, dir `0700`. No world-readable prompt content (may contain secrets).
- Runner reconcile step must be idempotent — surviving multiple reboot cycles cleanly.
- Auth probe must NOT consume real Claude tokens on every poll. Bedrock probe uses `max_tokens: 1` (minimum metered call). Direct-API probe is purely local file check.
- `--wait` polling loop must respect `km create`'s parent context cancellation (SIGINT / timeout) — don't leak SSM RunCommand IDs.

## Acceptance criteria (UAT)

Five real-AWS scenarios from spec § Test plan:

| # | Scenario | Expected |
|---|---|---|
| 1 | `km create profiles/learn.yaml --prompt "echo hello" --wait` | Exit 0; `km agent list <sb>` shows one run with stdout containing `hello`. |
| 2 | `km create profiles/learn.yaml --prompt @plan.txt --prompt "publish step" --wait` | Both runs visible in order; second run starts only after first exits 0. |
| 3 | `km create profiles/learn.yaml --prompt "exit 1; echo never" --prompt "should not run"` (no `--wait`) | `km agent list --queue <sb>` shows entry 1 `failed`, entry 2 `skipped`. |
| 4 | `km create profiles/learn.yaml --no-bedrock --prompt "tell me your model"` against a sandbox without pre-seeded creds; operator `km shell` + `claude auth login`; then watch queue drain. | Queue stays `pending` indefinitely until login completes; then runs to `done`. |
| 5 | Start a long-running prompt (e.g. `sleep 300; echo done`); `km pause <sb>` mid-execution; `km resume <sb>`. | Reconcile resets `running` → `pending`; runner re-executes that entry from scratch; remaining queue continues normally. |

Plus existing-behavior regression check:

| # | Scenario | Expected |
|---|---|---|
| R1 | `km create profiles/learn.yaml` (no `--prompt`) | Provisions correctly: no `/workspace/.km-agent/queue/` directory created (Lambda + create.go push path never invoked). `km-queue.service` IS installed + enabled on the box (unconditional via userdata.go heredoc — revised 2026-05-19 per research) but `systemctl is-active km-queue.service` returns `active (running)` with the runner main loop idle-polling for `pending` entries that will never arrive. No `runs/<ts>/output.json` created. Total CPU/memory impact: bash sleep loop, negligible. |

## TDD test list (for planner to expand into RED-state stubs)

**Unit tests — `internal/app/cmd/create_test.go`:**
- `TestPromptFlag_LiteralString` — single literal value parsed correctly.
- `TestPromptFlag_AtFileReference` — `@file.txt` reads file contents verbatim.
- `TestPromptFlag_AtAtEscape` — `@@literal` produces `@literal` (no file read).
- `TestPromptFlag_AtFile_MissingErrors` — missing file returns clear error before SSM call.
- `TestPromptFlag_Repeated_PreservesOrder` — N prompts come out in declaration order.
- `TestPromptFlag_NoFlag_NoQueueFiles` — `--prompt` absent → zero SSM queue writes (regression guard for R1).
- `TestPromptFlag_QueueMetaJSON_NoBedrock` — `--no-bedrock` propagates to `meta.json`.
- `TestPromptFlag_Wait_PollsUntilTerminal` — `--wait` blocks; returns 0 on all-done; returns non-zero on failure.

**Unit tests — `internal/app/cmd/agent_test.go`:**
- `TestAgentList_QueueFlag_EmptyDir` — `--queue` against sandbox with no queue dir → empty result, no error.
- `TestAgentList_QueueFlag_MixedStatuses` — pending/running/done/failed/skipped all render correctly.
- `TestAgentList_QueueFlag_OrdersByIndex` — sorted ascending.

**Runner script tests — `pkg/profile/configfiles/km-queue-runner_test.sh` (bash test harness):**
- `test_reconcile_running_to_pending` — start with `001.meta.json` status=`running` → after reconcile, status=`pending`.
- `test_lowest_pending_picked_first` — multiple pending → 001 runs before 002.
- `test_failure_marks_remaining_skipped` — entry 1 fails → entry 2 and 3 become `skipped`.
- `test_atomic_status_transition` — concurrent reads during write never see partial JSON.
- `test_auth_probe_bedrock_success_proceeds` — mock `aws` exits 0 → loop proceeds to entry pick.
- `test_auth_probe_bedrock_failure_loops` — mock `aws` exits non-zero → loop sleeps and re-probes (no entry pick).
- `test_auth_probe_direct_api_creds_present` — `~/.claude/credentials.json` exists + valid JSON → proceed.
- `test_auth_probe_direct_api_creds_missing` — file absent → loop waits.

## Plan-breakdown hint for /gsd:plan-phase

Suggested wave structure (planner can revise):

- **Wave 0 (scaffolding):** stub `--prompt` flag (no behavior), `--queue` flag stub, runner script skeleton, RED-state test stubs.
- **Wave 1 (parsing + SSM push):** TDD `--prompt` parser, `@file` handling, `@@` escape, SSM queue-file push. Unit tests GREEN.
- **Wave 2 (runner script + systemd unit):** TDD bash runner with reconcile/loop/auth-probe/status-transitions; bash harness GREEN.
- **Wave 3 (seed mechanism):** wire runner + unit into create-handler Lambda or userdata template; verify files land on a real sandbox.
- **Wave 4 (`--wait` polling + `--queue` view):** `km create --wait` polling loop, `km agent list --queue` view; integration tests GREEN.
- **Wave 5 (UAT, operator-in-loop):** 6 acceptance scenarios (5 functional + R1 regression).

## Risk / unknowns surfaced for planning

- **Create-handler Lambda surface area.** Phase 84 / 84.4 made the Lambda's `km` toolchain refresh non-trivial. The runner script needs to land on the box *before* userdata's `initCommands` (so it can preempt operator-supplied init), which might require either userdata-template injection or a `configFiles` blob written by the create-handler. Planner: confirm which path the create-handler currently uses and pick the minimal-diff option.
- **systemd availability on all sandbox base AMIs.** Amazon Linux 2023 + Ubuntu 24.04/22.04 all ship systemd. Docker substrate (`km create --docker`) does NOT — needs a different launcher (or v1 documents "queue requires EC2 substrate" and `km create --docker --prompt` errors out).
- **`km pause` semantics.** `km pause` of an EC2 sandbox is a stop; resume re-runs userdata `initCommands` only if cloud-init's `runcmd` is configured for `always`. Need to verify the systemd unit auto-starts on resume (it should, since the unit is enabled at install time).
- **`tmux` availability.** The `km-agent` tmux pattern already requires tmux to be present. Confirm `tmux` is in the base AMI dependency list; if not, add to the runner-script install step.
- **Bedrock probe cost.** `max_tokens: 1` Bedrock invoke is ~$0.000003 per probe. At 5s polls + indefinite wait, a stuck queue costs ~$0.05/day per sandbox. Acceptable; document as a known cost.

## Files expected to change

- `internal/app/cmd/create.go` — flag parsing, SSM push, `--wait` polling.
- `internal/app/cmd/create_test.go` — unit tests.
- `internal/app/cmd/agent.go` — `--queue` flag on `agent list`.
- `internal/app/cmd/agent_test.go` — unit tests.
- `pkg/profile/configfiles/km-queue-runner.sh` — bash runner (new file).
- `pkg/profile/configfiles/km-queue.service` — systemd unit (new file).
- `pkg/profile/configfiles/km-queue-runner_test.sh` — bash test harness (new file).
- `infra/modules/create-handler/v1.0.0/lambda/handler.py` (or equivalent) — seed runner files. **Confirm path during planning.**
- `OPERATOR-GUIDE.md` — short section under `km create` documenting `--prompt` usage + recovery.
- `CLAUDE.md` — bullet under "## CLI" for `km create --prompt`.
