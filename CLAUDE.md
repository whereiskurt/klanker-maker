# Klanker Maker

This file serves as the Terragrunt repo root anchor for `find_in_parent_folders("CLAUDE.md")`.

## Project

Policy-driven sandbox platform. See `.planning/PROJECT.md` for details.

## CLI

- `km validate <profile.yaml>` — validate a SandboxProfile
- `km create <profile.yaml>` — provision a sandbox (`--no-bedrock`, `--docker`, `--alias`, `--on-demand`)
- `km destroy <sandbox-id>` — teardown a sandbox (--remote by default; `km kill` is an alias)
- `km pause <sandbox-id>` — hibernate/pause an EC2 or Docker instance (preserves infra)
- `km resume <sandbox-id>` — resume a paused or stopped sandbox
- `km lock <sandbox-id>` — safety lock preventing destroy/stop/pause (atomic DynamoDB)
- `km unlock <sandbox-id>` — remove safety lock (requires confirmation or --yes)
- `km list` — list sandboxes (narrow default, --wide for all columns)
- `km agent <sandbox-id> --claude` — interactive Claude session via SSM (`--no-bedrock` for direct API)
- `km agent run <sandbox-id> --prompt "..."` — fire-and-forget non-interactive Claude in tmux (`--wait`, `--interactive`, `--no-bedrock`, `--auto-start`)
- `km agent attach <sandbox-id>` — attach to a running agent's tmux session (Ctrl-B d to detach)
- `km agent results <sandbox-id>` — fetch latest run output (`--run <id>` for specific run)
- `km agent list <sandbox-id>` — list all agent runs with status and output size
- `km at '<time>' <cmd>` — schedule deferred/recurring operations; supports `create`, `destroy`, `kill`, `stop`, `pause`, `resume`, `extend`, `budget-add`, `agent run` (`km schedule` is an alias)
- `km at list` / `km at cancel <name>` — manage scheduled operations
- `km email send` — send signed email between sandboxes or to/from operator (`--from`, `--to`, `--cc`, `--use-bcc`, `--reply-to`)
- `km email read <sandbox>` — read sandbox mailbox with signature verification and auto-decryption (`--json`, `--mark-read`)
- `km otel <sandbox-id>` — OTEL telemetry + AI spend summary (--prompts, --events, --tools, --timeline)
- `km slack init` — bootstrap Slack integration: validate bot token, write SSM params, create shared channel, send Slack Connect invite, deploy bridge Lambda (`--bot-token`, `--invite-email`, `--shared-channel`, `--force`)
- `km slack test` — end-to-end smoke test through the bridge using operator signing key
- `km slack status` — print SSM-backed Slack config (workspace, channel, bridge URL, last test)
- `km slack rotate-token --bot-token <new-token>` — rotate Slack bot token: validate, persist to SSM, force bridge cold-start, smoke test
- `km init` — initialize regional infrastructure (`--sidecars` for fast binary deploy, `--lambdas` for Lambda-only deploy)
- `km shell <sandbox-id>` — SSM shell (`--root`, `--ports`, `--no-bedrock`, `--learn` to generate profile from observed traffic, `--ami` to bake the EC2 instance into a custom AMI on exit)
- `km ami list` — list operator-baked AMIs with profile references and size (`--wide` for region/snapshot/encryption columns)
- `km ami bake <sandbox-id>` — snapshot a running sandbox into a custom AMI tagged with sandbox metadata
- `km ami copy <ami-id> --region <dest>` — copy AMI to another region in the same account, re-tagging the destination
- `km ami delete <ami-id>` — deregister an AMI and delete its associated EBS snapshots atomically
- `km info` — platform config, accounts, SES quota, AWS spend, DynamoDB tables
- `km doctor` — validate platform health (20 checks: config, credentials, SES, Lambda, VPC, stale resources, stale AMIs, etc.; `--all-regions` to scan every active region)

## Email

### Operator-side (from your workstation)

```bash
# Send email from operator to a sandbox
km email send --to sb-abc123 --subject "task spec" --body spec.md

# Send between sandboxes
km email send --from sb-abc123 --to sb-def456 --subject "results" --attach output.tar.gz

# Read a sandbox's mailbox
km email read sb-abc123              # table format with signature verification
km email read sb-abc123 --json       # JSON for scripting
km email read sb-abc123 --mark-read  # mark as processed
```

### Inside a sandbox (km-send / km-recv)

Two bash utilities at `/opt/km/bin/` handle Ed25519-signed email:

```bash
# Send to operator (default recipient)
km-send --subject "task complete" --body results.txt

# Send to another sandbox with attachment
km-send --to sb-x9y8z7w6@sandboxes.klankermaker.ai \
  --subject "results" --body results.json --attach output.tar.gz

# Read inbox
km-recv                  # table with signature verification
km-recv --json           # JSON for agent parsing
km-recv --watch          # poll every 5s for new messages
km-recv --mark-read      # mark as processed
```

**km-send flags:** `--subject` (required), `--to` (default: operator), `--body` (file path or `-` for stdin), `--attach`, `--cc`, `--use-bcc`, `--reply-to`

**How it works:** km-send fetches the sandbox's Ed25519 signing key from SSM (`/sandbox/{id}/signing-key`), signs the body, builds a raw MIME message with `X-KM-Sender-ID` and `X-KM-Signature` headers, and sends via SES. Inbound email is synced from S3 by `km-mail-poller` (every 60s) to `/var/mail/km/new/`.

**For AI agents:** Use `km-send --body <file>` (not stdin) for reliable signing on OpenSSL 3.5+. Agents can read the docs at `docs/multi-agent-email.md` on the sandbox for the full protocol.

### Key environment variables

| Variable | Description |
|----------|-------------|
| `KM_EMAIL_ADDRESS` | This sandbox's email address (`{sandbox-id}@sandboxes.{domain}`) |
| `KM_SANDBOX_FROM_EMAIL` | Alias for `KM_EMAIL_ADDRESS` (same value) |
| `KM_SANDBOX_ID` | Sandbox identifier |
| `KM_SANDBOX_ALIAS` | Display name for From header (if alias is set) |
| `KM_SANDBOX_DOMAIN` | Email domain (e.g. `sandboxes.klankermaker.ai`) |
| `KM_OPERATOR_EMAIL` | Operator inbox address |
| `KM_ARTIFACTS_BUCKET` | S3 bucket backing the mail poller |

See `docs/multi-agent-email.md` for full details on SES setup, IAM policy, signing protocol, and cross-sandbox orchestration.

## Slack Notifications

Phase 63 extends Phase 62's operator-notify hook with parallel Slack delivery. Sandboxes call a `km-slack-bridge` Lambda with Ed25519-signed payloads (same trust model as `km-send`); operators are invited to channels via Slack Connect.

### One-time setup

```bash
make build               # Always rebuild km after edits (memory: feedback_rebuild_km)
km init --sidecars       # Upload km-slack binary to S3 (required after Phase 63 ships)
km init                  # Deploy bridge Lambda + nonce DynamoDB table
km slack init            # Interactive bootstrap (or pass --bot-token + --invite-email)
```

### Profile fields (`spec.cli`)

| Field | Type | Default | Purpose |
|---|---|---|---|
| `notifyEmailEnabled` | bool* | true | Phase 62 compat; set false to skip email when Slack is on |
| `notifySlackEnabled` | bool* | false | Enable Slack delivery |
| `notifySlackPerSandbox` | bool | false | Create `#sb-{id}` channel; archive at destroy |
| `notifySlackChannelOverride` | string | empty | Pin to channel ID (`^C[A-Z0-9]+$`) |
| `slackArchiveOnDestroy` | bool* | true | Per-sandbox only; false preserves channel |

### Sandbox env vars

| Variable | Source |
|---|---|
| `KM_NOTIFY_EMAIL_ENABLED` | profile `spec.cli.notifyEmailEnabled` (omit = default 1) |
| `KM_NOTIFY_SLACK_ENABLED` | profile `spec.cli.notifySlackEnabled` (omit = default 0) |
| `KM_SLACK_CHANNEL_ID` | runtime, injected by km create |
| `KM_SLACK_BRIDGE_URL` | runtime, injected by km create |

### SSM parameters

| Parameter | Purpose |
|---|---|
| `/km/slack/bot-token` (SecureString) | KMS-encrypted bot token; bridge Lambda + operator only |
| `/km/slack/workspace` | JSON: `{"team_id":"...","team_name":"..."}` |
| `/km/slack/invite-email` | Email for Slack Connect invites |
| `/km/slack/shared-channel-id` | Default shared channel ID |
| `/km/slack/bridge-url` | Lambda Function URL |

### Important workflow notes

- **`km init --sidecars` is required** after Phase 63 ships so management Lambdas pick up the schema additions and the new `km-slack` sidecar binary lands in S3.
- Existing sandboxes do NOT get km-slack retroactively — `km destroy` + `km create` to provision with the binary.
- Slack Connect (`conversations.inviteShared`) requires a **Pro Slack workspace** (free tier returns `not_allowed_token_type`).
- Bot token rotation: `km slack rotate-token --bot-token <new>` (validates, persists to SSM, force-cold-starts the bridge Lambda, smoke tests). Legacy path: `km slack init --force --bot-token <new>` (persists but does NOT force cold start).

See `docs/slack-notifications.md` for the full operator guide.

### Slack inbound (Phase 67)

Bidirectional chat: Slack messages in `#sb-{id}` channels become Claude
turns inside the sandbox via SQS FIFO dispatch.

**Profile field (under `spec.cli`):**

| Field | Type | Default | Effect |
|---|---|---|---|
| `notifySlackInboundEnabled` | bool | false | Provision per-sandbox SQS FIFO queue, install systemd poller, subscribe to channel events |

Requires `notifySlackEnabled: true` AND `notifySlackPerSandbox: true`.
Incompatible with `notifySlackChannelOverride`.

**Sandbox env vars added at runtime:**

| Variable | Source |
|---|---|
| `KM_SLACK_INBOUND_QUEUE_URL` | poller reads `/sandbox/{id}/slack-inbound-queue-url` from SSM Parameter Store at boot when the env var is empty (an org-level SCP blocks SSM SendCommand for the application account, so the value cannot be injected directly into the env file) |
| `KM_SLACK_THREAD_TS` | exported by poller into Claude's env BEFORE `claude -p` launches (passed via `--thread` to km-slack post). The Stop hook gates its Slack branch on this var — when set, the poller is driving the reply and the Stop hook stays out of the way. |
| `KM_SLACK_THREADS_TABLE` | DDB table name for session-id persistence, injected by km create |

**systemd EnvironmentFile gotcha:** `systemd` does NOT accept the shell-style
`export VAR=val` lines in `/etc/profile.d/*.sh` — it silently rejects them.
The userdata template writes a parallel `/etc/km/notify.env` (no `export`
prefix, systemd-format) and `km-slack-inbound-poller.service` points
`EnvironmentFile=/etc/km/notify.env`. Both files are kept in sync at
cloud-init time; `/etc/profile.d/km-notify-env.sh` remains the source of
truth for shell sessions and Claude's bash env.

**SSM parameters added in Phase 67:**

| Parameter | Purpose |
|---|---|
| `/km/slack/signing-secret` (SecureString) | Slack App signing secret for HMAC-SHA256 verification of /events webhooks |
| `/sandbox/{sandbox-id}/slack-inbound-queue-url` (String) | Per-sandbox SQS FIFO queue URL written by `km create` and read by the sandbox-side poller. Deleted by `km destroy`. |

**DynamoDB tables added in Phase 67:**

- `{prefix}-km-slack-threads` — `(channel_id, thread_ts) → claude_session_id` map. TTL 30 days from `last_turn_ts`.
- New GSI on `{prefix}-sandboxes`: `slack_channel_id-index` (additive, no PK/SK changes).

**SQS resources (per-sandbox, runtime-provisioned):**

- `{prefix}-slack-inbound-{sandbox-id}.fifo` — FIFO queue, 14d retention, 30s VisibilityTimeout, ContentBasedDeduplication=false.

**One-time operator setup:**

```bash
km slack init --force --signing-secret <signing-secret-from-slack-app>
# Then: paste printed Events URL into Slack App → Event Subscriptions → Request URL
```

**Signing secret rotation:**

```bash
km slack rotate-signing-secret --signing-secret <new-secret>
# Or: km slack init --force --signing-secret <new-secret>
```

**km doctor adds three new checks:**

- `slack_inbound_queue_exists` — every inbound-enabled sandbox has a healthy queue
- `slack_inbound_stale_queues` — orphan SQS queues with no DDB sandbox row
- `slack_app_events_subscription` — bot has channels:history + groups:history + reactions:write scopes

#### ACK reaction (Phase 67.1)

When the bridge enqueues an inbound message to SQS, it adds a 👀 reaction
to the originating Slack message via `reactions.add` (fire-and-forget,
~1s round-trip). Bot needs `reactions:write` scope (added via Slack App
config → OAuth & Permissions → reinstall app). Bridge-global emoji is
configurable via `KM_SLACK_ACK_EMOJI` Lambda env var (default `eyes`,
no colons). Bridge-only change — deploy with `make build && km init --lambdas`;
no sandbox redeploy needed. See `docs/slack-notifications.md` § ACK reaction.

See `docs/slack-notifications.md` for the full operator guide including setup steps, troubleshooting, and security model.

### Slack transcript streaming (Phase 68)

Per-turn assistant text + tool one-liners stream to per-sandbox channel thread;
final gzipped JSONL transcript uploaded as Slack file at Stop. Opt-in.

**Profile field (under `spec.cli`):**

| Field | Type | Default | Effect |
|---|---|---|---|
| `notifySlackTranscriptEnabled` | bool | false | Stream + upload transcripts to per-sandbox Slack thread |

Requires `notifySlackEnabled: true` AND `notifySlackPerSandbox: true`.
Incompatible with `notifySlackChannelOverride`.

**CLI overrides:** `--transcript-stream` / `--no-transcript-stream` on `km agent run` and `km shell`.

**Sandbox env vars added at runtime:**

| Variable | Source |
|---|---|
| `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED` | profile `spec.cli.notifySlackTranscriptEnabled` (omit ⇒ 0) |
| `KM_SLACK_STREAM_TABLE` | runtime, injected by `km create` |

**DynamoDB table added in Phase 68:**

- `{prefix}-slack-stream-messages` — `(channel_id, slack_ts) → {sandbox_id, session_id, transcript_offset, ttl_expiry}`. TTL 30 days. Phase B (reaction-triggered fork) will consume this table; Phase 68 only writes.

**S3 layout:** `transcripts/{sandbox_id}/{session_id}.jsonl.gz` in `KM_ARTIFACTS_BUCKET`.

**Slack bot scope required:** `files:write` (one-time re-auth via Slack App admin).

**km doctor adds three new checks:**

- `slack_transcript_table_exists` — DDB table is provisioned + ACTIVE
- `slack_files_write_scope` — bot has `files:write`
- `slack_transcript_stale_objects` — S3 cleanup advisory for destroyed sandboxes

**One-time operator setup:**

```bash
# 1. Add `files:write` scope to Slack App, re-install, rotate token
km slack rotate-token --bot-token <new-token>

# 2. Provision new DDB table + sidecar + bridge
make build && km init --sidecars && km init

# 3. Verify
km doctor
```

See `docs/slack-notifications.md` for full operator guide.

## Architecture

- `cmd/km/` — CLI entry point
- `internal/app/cmd/` — Cobra commands
- `pkg/profile/` — Schema, validation, inheritance
- `pkg/compiler/` — Profile → Terragrunt artifacts
- `pkg/ebpf/` — eBPF enforcer (cgroup BPF programs, DNS resolver, audit consumer)
- `pkg/terragrunt/` — Terragrunt runner
- `pkg/aws/` — AWS SDK helpers (DynamoDB metadata, S3 artifacts, SES, EC2)
- `sidecars/` — HTTP proxy (MITM), DNS proxy, audit-log, tracing
- `infra/modules/` — Terraform modules
- `infra/live/` — Terragrunt hierarchy
- `profiles/` — Built-in SandboxProfile YAML files

## Network Enforcement

Three enforcement modes via `spec.network.enforcement`:
- `proxy` (default) — iptables DNAT → userspace proxy sidecars
- `ebpf` — cgroup BPF programs (connect4, sendmsg4, sockops, egress) with LPM trie allowlist
- `both` — eBPF primary + proxy for L7 inspection (Bedrock metering, GitHub filtering)

eBPF SSL uprobes provide passive TLS plaintext capture for audit/observability alongside enforcement.

## Learn Mode

Generate a minimal SandboxProfile from observed traffic:

```bash
km create profiles/learn.yaml          # wide-open sandbox with learnMode + privileged
km shell --learn <sandbox-id>          # observe traffic + commands, generate profile on exit
cat learned.*.yaml                     # annotated profile with DNS suffixes, initCommands
km validate learned.*.yaml             # validate before use
```

- `profiles/learn.yaml` — permissive profile with broad TLD suffixes, `enforcement: both`, `privileged: true`, `learnMode: true`
- `spec.execution.privileged` — grants sandbox user wheel/sudo access (any profile)
- `spec.observability.learnMode` — enables eBPF traffic recording (`--observe` on enforcer)
- `--learn` triggers SIGUSR1 flush on the enforcer to snapshot observations to S3

### Bake AMI on exit

```bash
km shell --learn --ami <sandbox-id>    # observe traffic + snapshot to AMI on exit
cat learned.*.yaml                     # generated profile now includes spec.runtime.ami: ami-xxxxxxxx
km validate learned.*.yaml             # validate before reuse
km create learned.*.yaml               # spin up a new sandbox from the baked AMI
```

`--ami` requires `--learn`. The bake fires before the SIGUSR1 flush so the AMI ID can be embedded in the generated profile. AMIs are private to the application AWS account, tagged with sandbox metadata, and tracked by `km ami list` / `km doctor` (stale check). `spec.runtime.ami` accepts both slugs (`amazon-linux-2023`, `ubuntu-24.04`, `ubuntu-22.04`) and raw AMI IDs (`ami-xxxxxxxx`). When launching from a baked AMI that already declares `/dev/sdf` in its block device mappings, the compiler auto-rotates `additionalVolume` onto the next free device (`/dev/sdg`..`/dev/sdp`) so launches don't collide.

## Agent Execution

Run AI agents non-interactively inside sandboxes. Agents run in persistent tmux sessions that survive SSM disconnects.

### Fire-and-forget

```bash
km agent run <sandbox> --prompt "fix the failing tests"
```

Returns immediately. Agent runs in a tmux session on the sandbox. Output lands at `/workspace/.km-agent/runs/<timestamp>/output.json`.

### Wait for completion

```bash
km agent run <sandbox> --prompt "What model are you?" --wait
```

Blocks until done, prints JSON result including `result`, `total_cost_usd`, token usage.

### Interactive (live attach)

```bash
km agent run <sandbox> --prompt "refactor the auth module" --interactive
```

Creates the tmux session and attaches you to watch Claude work in real-time. Detach with `Ctrl-B d` — the agent keeps running.

### Attach to a running agent

```bash
km agent attach <sandbox>
```

Connects to the latest running tmux agent session. Useful after fire-and-forget.

### Fetch results

```bash
km agent results <sandbox>                          # latest run (S3 fast path)
km agent results <sandbox> --run 20260410T143000Z   # specific run
km agent results <sandbox> | jq '.result'           # just the answer
km agent results <sandbox> | jq '.total_cost_usd'   # cost
```

### List runs

```bash
km agent list <sandbox>
```

### Direct API (skip Bedrock)

```bash
km agent run <sandbox> --prompt "..." --no-bedrock --wait
```

Requires `claude login` on the sandbox first (stores OAuth token in `~/.claude/.credentials.json`). Or set `spec.cli.noBedrock: true` in the profile to make it the default.

### Schedule agent runs

```bash
km at '5pm tomorrow' agent run <sandbox> --prompt "run nightly tests" --auto-start
```

`--auto-start` resumes the sandbox if paused/hibernated before running the agent.

### Profile configuration

```yaml
spec:
  execution:
    configFiles:
      "/home/sandbox/.claude/settings.json": |
        {"trustedDirectories":["/home/sandbox","/workspace"]}
  cli:
    noBedrock: true    # default to direct API for km shell / km agent run
```

- `spec.execution.configFiles` — pre-seed tool config files (written after initCommands, owned by sandbox user)
- `spec.cli.noBedrock` — operator-side default; doesn't affect sandbox provisioning, only CLI behavior when connecting
