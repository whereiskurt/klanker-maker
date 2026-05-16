# Klanker Maker

This file serves as the Terragrunt repo root anchor for `find_in_parent_folders("CLAUDE.md")`.

## Project

Policy-driven sandbox platform. See `.planning/PROJECT.md` for details.

Multi-instance support: km supports multiple installs in a single AWS account via the `resource_prefix` knob in km-config.yaml (default `km`); see `OPERATOR-GUIDE.md` § Multi-instance support. `km configure` prompts for `resource_prefix` and `email_subdomain` (one-time choices propagated to terragrunt via `KM_RESOURCE_PREFIX` / `KM_EMAIL_SUBDOMAIN` env vars).

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
- `km vscode start <sandbox-id>` — open SSM port-forward + ssh-config Host entry for VS Code Remote-SSH (`--local-port` to override 2222)
- `km vscode status <sandbox-id>` — check sshd state + authorized_keys presence
- `km vscode rekey <sandbox-id>` — rotate per-sandbox VS Code Remote-SSH keypair on a running sandbox without `km destroy && km create` (`--force` to override `km lock`, `--yes` to skip confirmation prompt). Active VS Code sessions stay on the old key until reconnect.
- `km init` — initialize regional infrastructure (`--sidecars` for fast binary deploy, `--lambdas` for Lambda-only deploy)
- `km shell <sandbox-id>` — SSM shell (`--root`, `--ports`, `--no-bedrock`, `--learn` to generate profile from observed traffic, `--ami` to bake the EC2 instance into a custom AMI on exit)
- `km ami list` — list operator-baked AMIs with profile references and size (`--wide` for region/snapshot/encryption columns)
- `km ami bake <sandbox-id>` — snapshot a running sandbox into a custom AMI tagged with sandbox metadata
- `km ami copy <ami-id> --region <dest>` — copy AMI to another region in the same account, re-tagging the destination
- `km ami delete <ami-id>` — deregister an AMI and delete its associated EBS snapshots atomically
- `km info` — platform config, accounts, SES quota, AWS spend, DynamoDB tables
- `km doctor` — validate platform health (22 checks: config, credentials, SES, Lambda, VPC, stale resources, stale AMIs, orphaned EBS volumes + snapshots, etc.; `--all-regions` to scan every active region)

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
| `KM_SLACK_THREAD_TS` | exported by poller into Claude's env BEFORE `claude -p` launches (passed via `--thread` to km-slack post). The Stop hook gates ALL "Claude is waiting"-style notifications on this var — when set, the poller is driving the reply and the Stop hook suppresses both the email branch (6a) and the Slack-root branch (6b). "Claude is waiting" notifications fire only for terminal-initiated sessions (KM_SLACK_THREAD_TS unset). |
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

**Phase 67.2: bounded retry.** Transient failures (HTTP 429 with
`Retry-After`, HTTP 5xx, network errors, and Slack JSON codes
`internal_error` / `service_unavailable` / `fatal_error` /
`request_timeout`, plus any unknown error string per the
default-unknown→transient policy) now retry up to 2× with 200ms→600ms
backoff and ±25% jitter inside `SlackReactorAdapter.Add`. Terminal
auth-class errors (`invalid_auth`, `missing_scope`, `token_expired`,
etc.) log at Error level and do NOT retry. Handler goroutine context
bumped 5s → 10s to fit the retry budget. The existing `events:
reaction failed` Warn line in CloudWatch is preserved on final
exhaustion with a new `attempt=N` field. Bridge-only deploy: `make
build && km init --lambdas`. See
`docs/superpowers/specs/2026-05-14-slack-ack-reaction-bounded-retry-design.md`.

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

**Known Phase 68 limitations (Phase 68.1 follow-ups):**

- **Slack Connect channels reject final file upload.** Per-sandbox
  channels are externally shared via Slack Connect (`is_ext_shared:
  true`). Slack's `files.completeUploadExternal` returns silent
  `internal_error` for these. Per-turn chat lines + auto-thread
  + DDB record-mapping all work; only the `.jsonl.gz` attachment
  is missing. Pull from S3 instead:
  `aws s3 ls s3://${KM_ARTIFACTS_BUCKET}/transcripts/<sandbox-id>/`
- **`km agent run` (non-interactive `claude -p`) skips PostToolUse
  hooks** per Claude Code platform behavior. Use interactive
  `km shell` for transcript streaming today.
- **Subagent fan-out creates one Slack thread per `session_id`.**
  `/clone` and Task-tool spawns each fire their own auto-thread-parent.
- **Operator warning at `km create` only fires on `--local`** path,
  not `--remote` (default for EC2 substrates).

See `docs/slack-notifications.md` for full operator guide and
`.planning/phases/68-…/deferred-items.md` for fix paths.

### Slack inbound file attachments (Phase 75)

Per-sandbox channels now accept file_share uploads (images, PDFs, etc.).
Bridge Lambda downloads with bot token, stages to S3 under
`slack-inbound/<sandbox-id>/<thread_ts>/`, sandbox poller mirrors to
`/workspace/.km-slack/attachments/<thread_ts>/`, a natural-language
wrapper prepended to the prompt lists absolute paths + MIME types.
Caps: 25 files/msg, 100 MB/file. Over-cap → thread-reply warning.

New bot scope: `files:read`. Operator path: re-install the Slack app
with the new scope, run `km slack rotate-token --bot-token <new>`,
then `make build && km init --dry-run=false` (NOT `km init --lambdas`,
which builds the zip but doesn't deploy it).

**Critical:** `km init` defaults to `--dry-run=true`. Forgetting
`--dry-run=false` produces a no-op that *looks* like a deploy ran.
After a successful apply, verify:

```bash
aws lambda get-function-configuration --function-name km-slack-bridge \
  --query '{MemorySize:MemorySize, Timeout:Timeout, Vars:Environment.Variables}'
```

Expect `MemorySize=1024`, `Timeout=60`, and `Vars` containing
`KM_ARTIFACTS_BUCKET`. A `Vars` map with only `TOKEN_ROTATION_TS`
means `km slack rotate-token` blew away the env vars and Terraform
hasn't re-applied — re-run `km init --dry-run=false`.

Lambda config bumps: memory_size 256 → 1024 (Plan 04, fits 100MB
in-memory file buffer for SDK PutObject retry-rewindability),
timeout 15s → 60s (75.2 hotfix, fits synchronous file download).

### Phase 75 hotfix lessons

The end-to-end pipeline shipped through three follow-on hotfixes
after the initial UAT exposed gaps the design RESEARCH missed:

- **75.1** — Modern Slack workspaces deliver **stub file objects** in
  `file_share` event payloads (only `id` populated; `url_private_download`
  absent). The bridge must call `files.info` per file ID to enrich
  before issuing the GET. Symptom pre-fix:
  `Get "": unsupported protocol scheme ""` in thread-reply warning.
- **75.2** — Original design ran the file download in a goroutine so
  the handler could return 200 within Slack's 3-second ack window. This
  is unsound on AWS Lambda: the runtime freezes once the handler returns
  and the in-flight HTTP deadline elapses during freeze. 75.2 made the
  `file_share` path synchronous. The handler may now exceed 3s and
  Slack will retry, but the existing `event_id` nonce dedup absorbs the
  retry as a no-op 200. Log marker changed: `events: enqueued (files-fork)`
  → `events: enqueued (files-sync)`.
- **75.3** — The `km-slack-inbound-poller.service` systemd unit was
  missing `Environment=KM_ARTIFACTS_BUCKET={{ .KMArtifactsBucket }}`.
  Plan 03 added bash that references `${KM_ARTIFACTS_BUCKET}` but the
  sibling `km-mail-poller` unit had it and the inbound-poller unit
  was simply missed in the template. Symptom pre-fix: poller journal
  shows `KM_ARTIFACTS_BUCKET: unbound variable` and Claude replies
  "I don't see any file path attached" because the bash bailed out of
  the `aws s3 cp` mirror loop.

See `docs/slack-notifications.md` § Slack inbound file attachments
(Phase 75) for the full operator runbook + troubleshooting table.
Authoritative design:
`docs/superpowers/specs/2026-05-15-slack-inbound-file-attachments-design.md`.

## VS Code Remote-SSH (Phase 73)

Connect local desktop VS Code to a sandbox via SSM port-forward + per-sandbox ed25519 keypair.

### Profile field (`spec.cli`)

| Field | Type | Default | Purpose |
|---|---|---|---|
| `vscodeEnabled` | bool* | true | Provision sshd + authorized_keys at sandbox boot. Set false to skip. |

### Operator state

| Path | Purpose |
|---|---|
| `~/.km/keys/<sandbox-id>` | Private key (mode 0600) generated by `km create` |
| `~/.km/keys/<sandbox-id>.pub` | Public key (mode 0644) shipped via userdata |
| `~/.ssh/config` (managed block) | Host entries between `# BEGIN km vscode hosts` markers |

### One-time setup

```bash
# Install the Remote - SSH extension in VS Code (Microsoft, free)

# Schema change → refresh management Lambda
make build && km init --sidecars
```

### Per-sandbox workflow

```bash
km create profiles/<your-profile>.yaml --alias my-poc   # keypair generated locally
SB=$(km list | awk '/my-poc/ {print $1}')
km vscode start $SB                                      # opens tunnel, blocks until Ctrl-C
# In VS Code: F1 → "Remote-SSH: Connect to Host..." → km-$SB
km vscode rekey $SB --yes                                # rotate keypair without destroy/create
km destroy $SB --remote --yes                            # cleans up keys + ssh-config block
```

### Important workflow notes

- **`km init --sidecars` is required** after Phase 73 ships so the management Lambda's km
  binary recognizes the new `VSCodeSSHPubKey` userdata field. Without it, `km create
  --remote` produces a sandbox with broken authorized_keys (silent SSH failure).
- Existing sandboxes created before Phase 73 do NOT get sshd provisioning retroactively —
  `km destroy && km create` to provision.
- Cross-machine portability: keys live on the creation machine only. Operators who want to
  `km vscode start` from a different laptop must manually copy `~/.km/keys/<sandbox-id>*`.
- One operator per sandbox in v1 (single authorized_keys entry).
- `km vscode start` and `km vscode status` accept the same identifier formats as other
  `km` subcommands: full sandbox ID (`lrn2-ee9499b5`), alias (`my-poc`), or list-row number.

### Rotating a sandbox key (Phase 76)

Solves three pain points: (1) baked-AMI relaunch carries stale `authorized_keys`,
(2) cross-laptop portability — `km vscode rekey` on a second laptop bootstraps a fresh
key without manual file copy, (3) post-incident rotation if a private key is suspected
compromised. See `docs/vscode.md` § Rotating a sandbox key for full operator walkthrough.

Pre-flight gates (any failure = no key changes): EC2 instance must be `running`,
`km lock` must not block (override with `--force`), sandbox must have been created with
`vscodeEnabled:true` (pre-Phase-73 sandboxes get a clear hard error pointing at
`km destroy && km create`).

See `docs/vscode.md` for the full operator guide.

## Presence daemon (Phase 79)

Per-sandbox systemd-managed liveness daemon. Replaces the legacy bash `_km_heartbeat`
function (a per-shell background loop that historically orphaned itself, pegging the
`IDLE` column at full timeout for hours). One daemon per sandbox, root-owned, ticks
every 60s.

### What it does

`km-presence.service` checks five signals each tick and emits a single
`source:"presence"` heartbeat event to `/run/km/audit-pipe` if **any** is positive
(boolean OR):

| # | Signal | Source |
|---|---|---|
| 1 | Login shells (SSM + SSH) | `who` (utmp) |
| 2 | Attached tmux clients | `tmux list-clients` (as sandbox user) |
| 3 | Recent inbound email | New file in `/var/mail/km/new/` since last tick |
| 4 | Recent inbound Slack | `/run/km/last-slack-inbound` newer than stamp |
| 5 | Headless agent process | `pgrep` for claude/codex/km-agent-run.sh |

The daemon writes to `/run/km/audit-pipe` using the same `timeout 0.1 tee` pattern as
the per-command `_km_audit` hook (Phase 56.1 Bug 2 fix). Stamp file
`/run/km/.presence-last-tick` is touched unconditionally at end of every tick (in
tmpfs; intentionally lost on reboot).

### Migration

Following the Phase 63 / 67 / 68 / 73 pattern:

```bash
make build               # rebuild km CLI + km-presence binary
make sidecars            # upload km-presence to S3 alongside other sidecars
km init --sidecars       # refresh management Lambda's userdata template
```

**Existing sandboxes do NOT get km-presence retroactively** — they keep their bash
heartbeat until `km destroy && km create`. This is intentional and matches every
prior sidecar phase.

**Docker substrate retains the bash heartbeat** — Docker sandboxes cannot run systemd,
so `pkg/compiler/compose.go` is intentionally unchanged. Only EC2 sandboxes get the
daemon.

### Doctor check

`km doctor` adds `presence_daemon_healthy` (Phase 79). For each running sandbox, the
check queries CloudWatch FilterLogEvents for a `source:"presence"` event in the last
5 minutes; any sandbox with no recent event is reported as WARN (not ERROR — same
"opt-in feature can't be a hard failure" rationale as the Slack inbound checks).

A WARN typically means one of:
- Sandbox was provisioned BEFORE Phase 79 rollout (`km destroy && km create` to fix)
- The km-presence daemon crashed (check `journalctl -u km-presence` on the sandbox)
- CloudWatch logs ingestion is delayed (transient — re-run `km doctor`)

### Observability

```bash
# On the sandbox: see daemon's per-tick decisions
sudo journalctl -u km-presence -f

# Operator-side: distinguish new daemon events from legacy shell heartbeats
km otel <sandbox-id> --events  # filter for source:"presence" vs source:"shell"
```

### Roll back

Revert the `pkg/compiler/userdata.go` diff and re-run `km init --sidecars`. New
sandboxes from that point are born with the legacy bash heartbeat. Existing sandboxes
are unaffected (they keep whichever pattern they were born with). The km-presence
binary in S3 is harmless to leave in place when nothing references it.

See `docs/superpowers/specs/2026-05-10-km-presence-daemon-design.md` for the full PRD.

## Cross-account k8s integrations (Phase 80)

Provisions IAM roles in the klanker AWS account that trust k8s clusters in *other* AWS accounts. Pods authenticate via projected ServiceAccount tokens (IRSA) — no static IAM user keys, auto-rotating 3600s session tokens. Both the IRSA role and the create-handler Lambda role consume the shared `km-operator-policy/v1.0.0/` Terraform module so the two surfaces can never drift.

### What it does

`km cluster add` generates a per-cluster `infra/live/{region-label}/cluster-{name}/terragrunt.hcl`, runs `terragrunt apply` against `infra/modules/cluster-irsa/v1.0.0/`, captures the role ARN output, and persists the cluster metadata to `km-config.yaml`. The trust policy permits `sts:AssumeRoleWithWebIdentity`, scoped to a single namespace + ServiceAccount (wildcards allowed). The role is attached to the same 14 inline policies as the create-handler Lambda role via the shared `km-operator-policy/v1.0.0/` module.

**OIDC provider is account-local.** AWS STS validates web-identity tokens against an OIDC provider in the *same* account as the IAM role being assumed — it cannot reach across accounts to the cluster's own provider. The `cluster-irsa` module therefore mirrors the remote cluster's issuer URL into a new `aws_iam_openid_connect_provider` registered in the klanker account, then references that local provider as the trust Principal. The `--oidc-provider-arn` flag names the *remote* cluster's provider only to derive its issuer URL — the account portion of that ARN is informational.

### CLI

```bash
km cluster add --name <name> --oidc-provider-arn <arn> [flags]   # provision
km cluster list                                                   # show configured roles
km cluster rm <name> [flags]                                      # destroy
```

| Flag | Default | Required |
|---|---|---|
| `--name` | (none) | yes |
| `--oidc-provider-arn` | (none) | yes |
| `--namespace` | `*` | no |
| `--service-account` | `km` | no |
| `--aws-profile` | `klanker-application` | no |
| `--region` | `us-east-1` | no |
| `--verbose` | `false` | no |
| `--dry-run` | `true` | no |
| `--register-oidc-provider` | `auto` | no — `auto` detects from `aws iam list-open-id-connect-providers`; `true` always creates a new provider; `false` always references an existing one |

`--dry-run=true` runs `terragrunt plan` only; `--dry-run=false` runs `terragrunt apply --auto-approve`.

### km-config.yaml schema

`km cluster add` appends to `clusters:` in `km-config.yaml`:

```yaml
clusters:
  - name: dev-use1-0
    oidc_provider_arn: arn:aws:iam::874364631781:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE
    namespace: "*"
    service_account: km
    role_arn: arn:aws:iam::052251888500:role/km-cluster-dev-use1-0
```

Absent `clusters:` key is treated as empty slice — existing installs need no migration.

### One-time setup

```bash
make build       # always required after CLI edits (ldflags version embed)
# No km init --sidecars needed — Phase 80 does NOT modify the management Lambda's
# userdata template or sandbox-side code. The km-operator-policy / cluster-irsa
# modules are operator-applied (terragrunt apply from the operator workstation),
# not Lambda-applied.
```

### Important workflow notes

- **Zero-diff refactor of create-handler:** Plan 80-02 extracted 14 inline IAM policies from `infra/modules/create-handler/v1.0.0/main.tf` into the shared module via `moved {}` blocks. The first time an operator runs `terragrunt apply` in `infra/live/use1/create-handler/`, Terraform performs an address-only state move (no IAM mutations). Subsequent applies see no changes.
- **Idempotency:** `km cluster add --name foo ...` returns the existing role ARN if `foo` already exists in `km-config.yaml` — safe to re-run.
- **Rollback on persist failure:** if `terragrunt apply` succeeds but writing `km-config.yaml` fails, the IAM role is left in place. Run `km cluster rm <name>` (using the role name from terraform state) to clean up.
- **Wildcard trust:** `--namespace=*` makes the role assumable by the named ServiceAccount in any namespace. Specify a literal namespace for tighter scoping.
- **No `--sidecars` propagation required:** Unlike Phase 63/67/68/73/79, Phase 80 ships no sandbox-side or Lambda-side code. Operators only need a fresh `km` binary.
- **OIDC provider auto-detect (Phase 80.1):** Before generating the per-cluster terragrunt.hcl, `km cluster add` calls `aws iam list-open-id-connect-providers` against the target account. If the cluster's issuer URL is already registered (same-account EKS, a second stack against the same EKS cluster, or `eksctl`/Terraform-EKS auto-registered the provider), the module sets `register_oidc_provider = false` and references the existing provider via a Terraform data source. If no match → creates a fresh `aws_iam_openid_connect_provider`. The log line `OIDC provider auto-detected: [creating | reusing existing arn:...]` reports which branch was taken. Override with `--register-oidc-provider=true|false`. `km cluster rm` only destroys providers that this stack registered — pre-existing providers (the `register=false` path) are left intact.

### Handoff to k8s operators

On successful `km cluster add --dry-run=false`, the command prints a ready-to-paste ServiceAccount manifest:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: km
  namespace: <your-namespace>
  annotations:
    eks.amazonaws.com/role-arn: <role-arn-printed-above>
    eks.amazonaws.com/token-expiration: "3600"
```

Apply it with `kubectl apply -f sa.yaml`; pods annotated `serviceAccountName: km` will pick up the role automatically.

See `docs/superpowers/specs/2026-05-11-km-cluster-cross-account-irsa-design.md` for the full design spec.

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
