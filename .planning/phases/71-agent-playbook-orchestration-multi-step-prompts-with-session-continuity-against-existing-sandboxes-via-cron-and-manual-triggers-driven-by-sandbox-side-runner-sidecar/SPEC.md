# Phase 71 — Agent playbook orchestration (multi-step prompts on existing sandboxes)

**Status:** draft (pre-plan). Hand to `/gsd:plan-phase` once the operator approves the brief.
**Author:** brainstormed 2026-05-05
**Depends on:** Phase 50 (`km agent run` non-interactive Claude execution), Phase 51 (tmux-backed agent sessions), Phase 62 (operator-notify hook), Phase 63 (Slack notify hook + km-slack sidecar), Phase 67 (Slack inbound poller pattern + per-sandbox SQS FIFO + DDB session-id map), the existing `km at` EventBridge Scheduler integration

## Notation

A **playbook** is a YAML artifact (`kind: Playbook`) that declares an ordered list of prompts to fire as `claude -p --resume <session>` against an existing sandbox, all sharing a single durable Claude session. A **run** is one execution of a playbook (a row in `playbook-runs` DDB). A **step** is one prompt within a run; it succeeds iff its `claude -p` subprocess exits 0. The **runner** is a new sandbox-side systemd unit (`km-playbook-runner.service`) that drains the per-sandbox SQS FIFO and walks runs locally. "v1" means everything in this phase's success criteria; deferred items are listed in § Out of scope.

## Goal

An operator can declare a multi-step Claude prompt sequence as a YAML file, register it with `km playbook apply`, and either fire it manually (`km playbook run`) or schedule it (`km at '0 8 * * 1-5 *' playbook run morning-ops --sandbox sb-ops`). Each scheduled fire walks the steps in order against an existing sandbox, resuming the same Claude session across steps and across runs (so day N continues the conversation from day N−1). If the sandbox is paused/stopped at fire time, it auto-resumes; if it's destroyed, the run fails clearly. Step failures abort the run and notify the operator via the existing Phase 62/63 hooks. The implementation reuses every Phase 67 pattern (per-sandbox FIFO, sidecar poller, DDB session-id map keyed by a thread-like tuple) so the new code surface is minimal.

## Why

`km at agent run` (the existing minimal scheduler) only fires single-prompt, fire-and-forget Claude invocations. Real operator workflows are multi-step ("audit, then summarize, then email me") and benefit from a continuing conversation rather than three independent sessions that each have to re-derive context. The operator currently composes such workflows by stuffing all the steps into one giant prompt — which trades multi-step structure (clear failure isolation, observable per-step output, the model getting to "complete" each subtask before moving on) for monolith readability.

A small declarative playbook layer fixes this without inventing a workflow engine. Phase 67 already proved out the operationally-critical pieces: per-sandbox FIFO queue, a sandbox-side polling sidecar, a DDB `(channel, thread_ts) → claude_session_id` map that persists session continuity across reboots and SQS deliveries. A playbook is just "pre-author N messages, enqueue them at fire time, key the session map on `(playbook, sandbox)` instead of `(channel, thread)`." The Lambda stays dumb (no YAML parsing, no step expansion); the runner is a new sidecar shaped exactly like `km-slack-inbound-poller`.

The unified primitive is intentional. v1 ships cron-and-manual triggers only, but the data model leaves clean room for event triggers (email arrival, Slack reaction) and ephemeral-sandbox lifecycle workflows (create → run → destroy) to be added without re-shaping any of the v1 surface.

## Success criteria

A reviewer can verify each as TRUE end-to-end on a real EC2 sandbox.

1. `km playbook validate playbooks/morning-ops.yaml` reports OK on a well-formed file with a `metadata.name`, a `spec.sandbox.mode: existing`, a `spec.session.scope: [playbook, sandbox]`, and 3 steps each with a unique `name` and a non-empty `prompt`. Mutating any single rule (empty prompt, duplicate step names, missing name, unknown `sandbox.mode`) makes validation fail with a specific error pointing at the offending field.

2. `km playbook apply playbooks/morning-ops.yaml` against a configured km install (a) writes the YAML content to `s3://{artifacts}/playbooks/morning-ops/<sha256>.yaml`, (b) writes a row to the new `{prefix}-playbooks` DDB table with `name`, `s3_uri`, `applied_at`, and (c) is idempotent — re-applying the same content reuses the same S3 key and overwrites the DDB row's `applied_at` only.

3. `km playbook run morning-ops --sandbox sb-ops` against a *running* sandbox with `spec.cli.playbookEnabled: true`: invokes the TTL Lambda directly, the Lambda enqueues exactly one SQS FIFO message to `{prefix}-playbook-sb-ops.fifo` with `MessageGroupId=playbook:morning-ops`, the runner picks it up within 30s, walks all 3 steps in order, and the final DDB `playbook-runs` row has `status=completed`, `current_step=3`, `started_at < ended_at`, and `output_s3_prefix` populated. Each step's stdout is captured at `s3://{artifacts}/playbook-runs/sb-ops/{run_id}/step-{n}-{name}.json`.

4. After the run in (3), the `{prefix}-playbook-sessions` row keyed `(morning-ops, sb-ops)` contains a `claude_session_id` and `last_run_id`. A second invocation of the same playbook resumes that session: the runner observes a non-empty session-map entry and invokes `claude -p --resume <session>` for step 1; the model demonstrably has memory of the prior run's last turn (verifiable by a step prompt like "what did you summarize yesterday?" returning a non-empty answer that references the prior run's content).

5. `km at '*/5 * * * ? *' playbook run morning-ops --sandbox sb-ops` creates an EventBridge schedule with target=TTL Lambda whose `Input` JSON includes `kind: playbook-run`, the playbook name, the playbook S3 URI, and the resolved sandbox-id. `km at list` displays the schedule with the new command label. At the next 5-minute boundary the schedule fires and produces the same end-state as success criterion (3).

6. Sandbox readiness: when sb-ops is in EC2 state `stopped` at fire time, the Lambda calls `StartInstances` and enqueues the run message immediately (does not wait on boot). The run's `playbook-runs` row begins life with `status=queued`; once the runner ack's the message it transitions to `status=running`. Total wall-clock from cron fire to first step prompt firing is ≤ 120 s on a hibernated sandbox. When sb-ops is `terminated` or missing, the Lambda writes a `playbook-runs` row with `status=failed` and `error_msg` referencing the missing sandbox, fires the operator-notify hook with `kind: playbook-run-completed status=failed`, sends no SQS message, and exits non-zero so EventBridge logs the failure.

7. Step-failure abort: a playbook whose middle step instructs Claude to do something that exits non-zero (e.g., a step that runs a shell command guaranteed to fail and returns the failing exit code) leaves the run in `status=failed`, `current_step` pinned at the failing step, the failing step's stderr captured to S3, no subsequent steps executed, and operator-notify fires with `kind: playbook-run-completed status=failed steps_completed=N steps_total=M error_msg="step <n> '<name>' exit <code>"`.

8. Concurrent-fire serialization: two manual `km playbook run morning-ops --sandbox sb-ops` invocations within 5 s of each other produce two distinct `run_id` values (each Lambda invocation mints a fresh ULID); the SQS FIFO MessageGroupId (`playbook:morning-ops`) causes the runner to process them strictly sequentially. The second run's `started_at` is ≥ the first run's `ended_at`. A different playbook fired against the same sandbox in the same window processes in parallel (different MessageGroupId), demonstrably overlapping in DDB timestamps.

9. Crash-mid-step idempotency: SIGKILL'ing the runner partway through a step causes systemd to restart it; the SQS visibility timeout (30 s) re-delivers the in-flight message; the runner observes the existing `playbook-runs` row with `status=running`, replays the current step (re-issuing `claude -p --resume <session>` with the same prompt), and the run eventually completes with `status=completed`. No duplicate run row is created.

10. `km doctor` runs three new checks green on a healthy install and red on a broken one: `playbook_runner_service_active` (every `playbookEnabled: true` sandbox has the systemd unit active), `playbook_queue_exists` (every enabled sandbox has a healthy SQS FIFO + DLQ), and `playbook_dlq_depth` (DLQ depth across all sandboxes is 0).

11. `km destroy sb-ops` atomically deletes the per-sandbox FIFO queue, the DLQ, and the `/sandbox/sb-ops/playbook-queue-url` SSM parameter. The `{prefix}-playbook-runs` and `{prefix}-playbook-sessions` rows for that sandbox are *retained* (operator history value); `km playbook delete morning-ops` is the way to clear playbook-scoped state.

12. The operator-notify hook payload gains a new `kind: playbook-run-completed` case with fields `playbook`, `sandbox_id`, `run_id`, `status`, `steps_completed`, `steps_total`, `duration_seconds`, `error_msg`. The existing notify-hook formatter (Phase 62/63) routes this to email and Slack with a one-line subject/headline and the full structured fields in the body; no new transport code.

## Approach

### Profile schema additions

One new field under `spec.cli`:

```yaml
spec:
  cli:
    playbookEnabled: true   # default false; when true, km create provisions
                            # the per-sandbox SQS FIFO + DLQ and installs
                            # km-playbook-runner.service via userdata
```

Validation rules: boolean only; no cross-field constraints. Existing sandboxes are unaffected (default false). Schema lives in `pkg/profile/types.go` (`CLISpec`) and the embedded JSON schema.

### Playbook YAML schema (new package `pkg/playbook`)

```yaml
apiVersion: km/v1
kind: Playbook
metadata:
  name: morning-ops             # required, [a-z0-9-]+, ≤ 64 chars; durable session-map key
  description: Daily ops audit  # optional, free text
spec:
  sandbox:
    mode: existing              # required; v1 only honors "existing"
  session:
    scope: [playbook, sandbox]  # required; v1 only honors this exact value
  steps:
    - name: audit               # required, unique within playbook, [a-z0-9-]+
      prompt: |                 # required, non-empty
        Check overnight CI on github.com/foo/bar.
        List failures since 6pm yesterday with one-line cause.
    - name: summarize
      prompt: |
        Group failures by root cause. Output as bullets.
    - name: email
      prompt: |
        Use km-send to email the summary to the operator with
        subject "ops digest YYYY-MM-DD".
```

`pkg/playbook` mirrors `pkg/profile` shape: `Parse([]byte) (*Playbook, error)`, `Validate(*Playbook) error`, table-driven validation. Pure Go, no AWS deps. Unit-tested with golden valid + invalid YAMLs.

### CLI surface (new command group, `internal/app/cmd/playbook.go`)

```
km playbook validate <file.yaml>                  schema check, no AWS calls
km playbook apply <file.yaml>                     validate + S3 upload + DDB register
km playbook list                                  registered playbooks (DDB scan)
km playbook show <name>                           YAML, applied_at, last 5 runs
km playbook run <name> --sandbox <id> [--detach]  manual fire (lambda:Invoke);
                                                  default streams, --detach exits after enqueue
km playbook list-runs <name> [--sandbox <id>] [--limit N]
km playbook show-run <run_id>
km playbook logs <run_id> [--follow] [--step N]   streams S3 step outputs
km playbook cancel-run <run_id>                   SSM SIGTERM the running claude subprocess
km playbook delete <name>                         removes DDB row + S3 object;
                                                  preserves playbook-runs history
```

DI follows `at_test.go` convention: `NewPlaybookCmdWithDeps(cfg, sched, dynamo, s3, lambda)` for tests, lazy-init real clients in production path.

### `km at` extension (one new entry in `schedulableCommands` map at `internal/app/cmd/at.go:40`)

```go
"playbook-run": {targetARNField: "ttl", eventType: "playbook-run"},
```

`buildTargetInput` gets one new branch that marshals `{playbook, s3_uri, sandbox_id}`. The two-word command merge already handles `playbook run` → `playbook-run` (mirrors the existing `agent run` → `agent-run` collapse). `km at list` and `km at cancel` work unchanged.

### TTL Lambda extension (one new event handler)

In the existing TTL Lambda's event-type switch, add a `playbook-run` branch:

1. `ec2:DescribeInstances` on `sandbox_id`.
2. If state is `terminated` or missing: `dynamodb:PutItem` to `playbook-runs` with `status=failed`, fire operator-notify, exit non-zero.
3. If state is `stopped` or `stopping`: `ec2:StartInstances`.
4. `dynamodb:PutItem` to `playbook-runs`: `{run_id=ULID, playbook, sandbox_id, status=queued, current_step=0, started_at=now}`.
5. `sqs:SendMessage` to `{prefix}-playbook-{sandbox-id}.fifo`:
   - `MessageGroupId` = `playbook:{playbook_name}` (serializes same-playbook runs)
   - `MessageDeduplicationId` = `run_id`
   - `Body` = `{run_id, playbook, s3_uri}`
6. Return success. Lambda total runtime ≤ ~2 s; never waits on boot.

### Sandbox-side runner (new sidecar, `sidecars/playbook-runner/`)

Single Go binary, single goroutine main loop. Modeled on `sidecars/slack-inbound-poller/`. Responsibilities, in order per message:

1. `sqs:ReceiveMessage` (long-poll 20 s) on `KM_PLAYBOOK_QUEUE_URL`. (Env var absent ⇒ poller reads `/sandbox/{id}/playbook-queue-url` from SSM at startup; same SCP workaround as Phase 67's slack-inbound poller.)
2. Parse the message body. Idempotency: `dynamodb:GetItem` on `playbook-runs` by `run_id`. If `status=completed` or `failed`, ack and skip (stale redelivery). If `status=running`, replay the current step (crash recovery).
3. `s3:GetObject` the playbook YAML, parse with `pkg/playbook`.
4. `dynamodb:GetItem` on `playbook-sessions` by `(playbook, sandbox_id)`. If present, retain `claude_session_id` for use with `--resume` on every step of this run. If absent: step 1 invokes `claude -p` *without* `--resume`; the runner parses the resulting JSON output (which includes `session_id`) to capture the fresh ID, then uses it with `--resume` for steps 2..N of this run, and writes it to the session-map at run end so subsequent runs of the same playbook resume it.
5. `dynamodb:UpdateItem` on `playbook-runs`: `status=running`.
6. Per step (in order):
   - `dynamodb:UpdateItem`: `current_step=n`.
   - exec: `claude -p --resume <session>` (or no `--resume` for the very first step of the very first run) with the step's prompt as stdin.
   - tee stdout to `s3://{artifacts}/playbook-runs/{sandbox_id}/{run_id}/step-{n}-{name}.json` and to systemd journal.
   - On exit 0: continue. On exit ≠ 0: break out of the loop with the failure context.
7. `dynamodb:UpdateItem` on `playbook-runs`: terminal `status` (`completed` or `failed`), `ended_at`, `error_msg`, `output_s3_prefix`.
8. On success: `dynamodb:UpdateItem` on `playbook-sessions`: latest `claude_session_id`, `last_run_id`, `last_run_ts`.
9. Fire operator-notify hook (existing Phase 62 entry-point) with the new `kind: playbook-run-completed` payload.
10. `sqs:DeleteMessage`. Loop.

SQS visibility timeout: 30 s, matching `slack-inbound-{sandbox-id}.fifo`. Long-running steps require periodic `sqs:ChangeMessageVisibility` extension — the runner extends in 60 s slices while a step is in flight. (Same pattern the slack-inbound poller already uses for long Claude turns.)

### DynamoDB tables (three new)

| Table | PK | SK | Attrs | Notes |
|---|---|---|---|---|
| `{prefix}-playbooks` | `name` | – | `s3_uri`, `description`, `applied_at`, `applied_by` | written by `km playbook apply`; read by `km playbook list/show` |
| `{prefix}-playbook-sessions` | `playbook` | `sandbox_id` | `claude_session_id`, `last_run_id`, `last_run_ts`, TTL 90d from `last_run_ts` | mirrors the `slack-threads` table; `(playbook, sandbox_id) → session_id` |
| `{prefix}-playbook-runs` | `run_id` | – | `playbook`, `sandbox_id`, `status`, `current_step`, `started_at`, `ended_at`, `error_msg`, `output_s3_prefix` | GSI on `(playbook, started_at)` for `km playbook list-runs` |

All three are pay-per-request, encrypted with the platform CMK. Provisioned by Terragrunt (`infra/modules/dynamodb/playbook_*.tf`).

### SQS resources (per-sandbox, runtime-provisioned)

- `{prefix}-playbook-{sandbox_id}.fifo` — FIFO, 14d retention, 30 s VisibilityTimeout, ContentBasedDeduplication=false, KMS encrypted.
- `{prefix}-playbook-{sandbox_id}-dlq.fifo` — FIFO DLQ; main queue's RedrivePolicy `maxReceiveCount=5`. Provisioned only when `spec.cli.playbookEnabled: true`. `km destroy` deletes both.

### S3 layout

- `s3://{artifacts}/playbooks/{name}/{sha256-of-yaml}.yaml` — content-addressed; `apply` writes a new object only when content changes (so prior runs remain reproducible against the YAML they were fired with).
- `s3://{artifacts}/playbook-runs/{sandbox_id}/{run_id}/step-{n}-{name}.json` — per-step capture; one object per step.

### km init / km doctor extensions

- `km init --sidecars` adds `km-playbook-runner` to the binaries pushed to the sidecar S3 bucket (mirrors km-slack/km-slack-inbound-poller pattern).
- `km init` provisions the three new DDB tables and the operator-notify-hook formatter case for the new payload kind.
- `km doctor` adds three checks: `playbook_runner_service_active`, `playbook_queue_exists`, `playbook_dlq_depth`. Each follows the `slack_inbound_*` check shape.

### km create / km destroy extensions

- `km create`: when profile has `spec.cli.playbookEnabled: true`, Terragrunt provisions the per-sandbox FIFO + DLQ; userdata installs the runner binary from the sidecar bucket and enables `km-playbook-runner.service`; queue URL written to `/sandbox/{id}/playbook-queue-url` SSM (SCP workaround).
- `km destroy`: deletes queue, DLQ, SSM param atomically (alongside existing inbound-queue cleanup if applicable).

### Notification payload extension

The existing operator-notify hook (Phase 62) gains one new `kind` case in its formatter (the only Lambda code change beyond the TTL handler):

```json
{ "kind": "playbook-run-completed",
  "playbook": "morning-ops", "sandbox_id": "sb-ops", "run_id": "pr-01HXYZ...",
  "status": "completed",
  "steps_completed": 3, "steps_total": 3,
  "duration_seconds": 184,
  "error_msg": null }
```

Email subject: `[km] playbook morning-ops on sb-ops: completed (3/3)`. Slack message: same one-liner, full JSON in a code block.

### Testing strategy

| Layer | Approach |
|---|---|
| `pkg/playbook` parse/validate | Pure-Go table-driven unit tests; golden valid + every invalid case. ~100% coverage achievable. |
| `km playbook` CLI | DI pattern from `at_test.go`: inject SchedulerAPI, SandboxMetadataAPI, fake S3, fake Lambda. |
| TTL Lambda new handler | Existing Lambda has table-driven event tests; add cases for `playbook-run` × {running, stopped, missing} sandbox states. |
| `km-playbook-runner` sidecar | Unit tests with fake SQS/S3/DDB + a `claude` shim binary that echoes prompts and exits configurably; one localstack-backed integration; one E2E that creates a real sandbox, applies a 2-step playbook, fires it, asserts DDB end state + S3 outputs. |
| Crash-mid-step idempotency | Focused test: SIGKILL the runner between SQS receive and step completion, restart, assert step replays, no duplicate run row. (This is the most novel surface; gets explicit coverage.) |
| Concurrent serialization | Test that two `km playbook run` invocations of the same playbook within 1 s produce strictly serial DDB timestamps; two different playbooks produce overlapping timestamps. |

## Out of scope (deferred, structurally provisioned for)

- **Ephemeral-sandbox mode** (`spec.sandbox.mode: ephemeral`, create → run → destroy lifecycle workflow). Schema field reserved; v1 only honors `existing`.
- **Event triggers** (email arrival, Slack reaction emoji, S3 object). The data model leaves room — schedulable commands are a routing table — but no event sources wired in v1.
- **Per-step `timeout`, `retries`, `onFailure: abort | continue | jumpTo:`, `optional: true`.** Each is a single new YAML field; v1 semantic is "exit 0 abort on fail" only.
- **Prompt templating** (`{{ trigger.body }}`, `{{ steps.audit.result }}`). Not needed under same-session continuity; opt-in as `prompt-template:` field later.
- **Typed step kinds** (`kind: shell`, `kind: notify`, `kind: wait`). v1 every step is a Claude prompt; agent can already shell out and notify by being told to.
- **Cross-sandbox shared session** (`session.scope: [playbook]` only). Field exists on the schema; v1 only honors `[playbook, sandbox]`.
- **Codex playbooks.** Phase 70 (Codex parity) is parallel work. Once both pollers exist, this phase's runner gets an `agent: claude | codex` step field — purely additive.
- **Web UI for playbook authoring/observability.** Pure addition; doesn't affect any v1 surface.
