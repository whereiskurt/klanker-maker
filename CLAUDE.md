# Klanker Maker

This file serves as the Terragrunt repo root anchor for `find_in_parent_folders("CLAUDE.md")`.

## Project

Policy-driven sandbox platform. See `.planning/PROJECT.md` for details.

Multi-instance support: km supports multiple installs in a single AWS account via the `resource_prefix` knob in km-config.yaml (default `km`); see `OPERATOR-GUIDE.md` § Multi-instance support. `km configure` prompts for `resource_prefix` and `email_subdomain` (one-time choices propagated to terragrunt via `KM_RESOURCE_PREFIX` / `KM_EMAIL_SUBDOMAIN` env vars).

### Phase 82: Full resource-prefix isolation (multi-instance hardening)

Phase 82 closes three infrastructure blockers that previously made a second `km init` unsafe:
- **B1 (SES):** `resource_prefix` variable added to the SES module; rule-set name is now `${var.resource_prefix}-sandbox-email`.
- **B2 (email-handler):** `state_prefix` variable added; IAM ARN + S3 path interpolate the prefix.
- **B3 (ECS modules):** `km_label` variable added; SSM parameter ARN interpolates the prefix.

Every per-install resource now carries a `km:resource-prefix=${prefix}` tag (emitted by the 6 Terraform modules that own platform resources and backfilled onto pre-Phase-82 resources via `km doctor --backfill-tags`).

**km configure preserve behavior:** `km configure` no longer overwrites `resource_prefix` on re-run. The value set at first configure is preserved. To reset to the `km` default, run `km configure --reset-prefix`.

**km doctor --backfill-tags:** One-time retro-tag sweep for pre-Phase-82 installs. Safe to re-run (idempotent — second run reports `Tagged: 0`). Requires `AWS_DEFAULT_REGION` and `AWS_PROFILE` env vars when running without `km configure` context:
```bash
AWS_DEFAULT_REGION=us-east-1 AWS_PROFILE=<your-profile> km doctor --backfill-tags --dry-run=true   # preview
AWS_DEFAULT_REGION=us-east-1 AWS_PROFILE=<your-profile> km doctor --backfill-tags --dry-run=false  # apply
```

**Phase 82 upgrade prerequisites (one-time, existing installs):**
```bash
make build                   # ldflags-versioned km binary (required per feedback_rebuild_km)
km init --sidecars           # refresh management Lambda + sidecar binaries in S3
km init --dry-run=false      # apply Wave 3 Terraform module changes
AWS_DEFAULT_REGION=us-east-1 AWS_PROFILE=<your-profile> \
  km doctor --backfill-tags --dry-run=false   # one-time retro-tag sweep
```

See `docs/superpowers/specs/2026-05-16-multi-instance-resource-prefix-isolation-design.md` for the full design.

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
- `km slack init` — bootstrap Slack integration: validate bot token, write SSM params, create shared channel, send Slack Connect invite, deploy bridge Lambda (`--bot-token`, `--invite-email`, `--shared-channel`, `--signing-secret`, `--force`)
- `km slack test` — end-to-end smoke test through the bridge using operator signing key
- `km slack status` — print SSM-backed Slack config (workspace, channel, bridge URL, last test)
- `km slack rotate-token --bot-token <new-token>` — rotate Slack bot token: validate, persist to SSM, force bridge cold-start, smoke test
- `km slack rotate-signing-secret --signing-secret <new-secret>` — rotate Slack App signing secret in SSM
- `km vscode start <sandbox-id>` — open SSM port-forward + ssh-config Host entry for VS Code Remote-SSH (`--local-port` to override 2222)
- `km vscode status <sandbox-id>` — check sshd state + authorized_keys presence
- `km vscode rekey <sandbox-id>` — rotate per-sandbox VS Code Remote-SSH keypair on a running sandbox without `km destroy && km create` (`--force` to override `km lock`, `--yes` to skip confirmation prompt). Active VS Code sessions stay on the old key until reconnect.
- `km cluster add --name <name> --oidc-provider-arn <arn>` — provision cross-account IRSA role for a k8s cluster (`--namespace`, `--service-account`, `--aws-profile`, `--region`, `--dry-run`, `--register-oidc-provider`)
- `km cluster list` — show configured cross-account cluster roles
- `km cluster rm <name>` — destroy a cluster IRSA role
- `km init` — initialize regional infrastructure (`--sidecars` for fast binary deploy, `--lambdas` for Lambda-only deploy, `--dry-run=false` to actually apply)
- `km shell <sandbox-id>` — SSM shell (`--root`, `--ports`, `--no-bedrock`, `--learn` to generate profile from observed traffic, `--ami` to bake the EC2 instance into a custom AMI on exit)
- `km ami list` — list operator-baked AMIs with profile references and size (`--wide` for region/snapshot/encryption columns)
- `km ami bake <sandbox-id>` — snapshot a running sandbox into a custom AMI tagged with sandbox metadata
- `km ami copy <ami-id> --region <dest>` — copy AMI to another region in the same account, re-tagging the destination
- `km ami delete <ami-id>` — deregister an AMI and delete its associated EBS snapshots atomically
- `km info` — platform config, accounts, SES quota, AWS spend, DynamoDB tables
- `km doctor` — validate platform health (config, credentials, SES, Lambda, VPC, stale resources, stale AMIs, orphaned EBS volumes + snapshots, Slack inbound, presence daemon, etc.; `--all-regions` to scan every active region)

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

Sandboxes call a `km-slack-bridge` Lambda with Ed25519-signed payloads (same trust model as `km-send`); operators are invited to channels via Slack Connect.

### One-time setup

```bash
make build               # Always rebuild km after edits
km init --sidecars       # Upload km-slack binary to S3
km init --dry-run=false  # Deploy bridge Lambda + nonce DynamoDB table
km slack init            # Interactive bootstrap (or pass --bot-token + --invite-email)
```

### Profile fields (`spec.cli`)

| Field | Type | Default | Purpose |
|---|---|---|---|
| `notifyEmailEnabled` | bool* | true | Set false to skip email when Slack is on |
| `notifySlackEnabled` | bool* | false | Enable Slack delivery |
| `notifySlackPerSandbox` | bool | false | Create `#sb-{id}` channel; archive at destroy |
| `notifySlackChannelOverride` | string | empty | Pin to channel ID (`^C[A-Z0-9]+$`) |
| `slackArchiveOnDestroy` | bool* | true | Per-sandbox only; false preserves channel |
| `notifySlackInboundEnabled` | bool | false | Provision per-sandbox SQS FIFO queue, install systemd poller, subscribe to channel events (requires `notifySlackEnabled` + `notifySlackPerSandbox`; incompatible with `notifySlackChannelOverride`) |
| `notifySlackTranscriptEnabled` | bool | false | Stream + upload transcripts to per-sandbox Slack thread (same requirements as inbound) |

### Sandbox env vars

| Variable | Source |
|---|---|
| `KM_NOTIFY_EMAIL_ENABLED` | profile `spec.cli.notifyEmailEnabled` (omit = default 1) |
| `KM_NOTIFY_SLACK_ENABLED` | profile `spec.cli.notifySlackEnabled` (omit = default 0) |
| `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED` | profile `spec.cli.notifySlackTranscriptEnabled` (omit ⇒ 0) |
| `KM_SLACK_CHANNEL_ID` | runtime, injected by km create |
| `KM_SLACK_BRIDGE_URL` | runtime, injected by km create |
| `KM_SLACK_INBOUND_QUEUE_URL` | poller reads `/sandbox/{id}/slack-inbound-queue-url` from SSM at boot when env var is empty (an org-level SCP blocks SSM SendCommand for the application account, so the value cannot be injected directly into the env file) |
| `KM_SLACK_THREAD_TS` | exported by poller into Claude's env BEFORE `claude -p` launches. The Stop hook gates ALL "Claude is waiting"-style notifications on this var — when set, the poller is driving the reply and the Stop hook suppresses email + Slack-root branches |
| `KM_SLACK_THREADS_TABLE` | DDB table name for session-id persistence, injected by km create |
| `KM_SLACK_STREAM_TABLE` | runtime, injected by `km create` |
| `KM_SLACK_RENDER` | `plain` \| `mrkdwn` \| `blocks` — per-sandbox render-mode safety valve (default `blocks`) |

### SSM parameters

| Parameter | Purpose |
|---|---|
| `/km/slack/bot-token` (SecureString) | KMS-encrypted bot token; bridge Lambda + operator only |
| `/km/slack/signing-secret` (SecureString) | Slack App signing secret for HMAC-SHA256 verification of /events webhooks |
| `/km/slack/workspace` | JSON: `{"team_id":"...","team_name":"..."}` |
| `/km/slack/invite-email` | Email for Slack Connect invites |
| `/km/slack/shared-channel-id` | Default shared channel ID |
| `/km/slack/bridge-url` | Lambda Function URL |
| `/sandbox/{sandbox-id}/slack-inbound-queue-url` (String) | Per-sandbox SQS FIFO queue URL written by `km create`, read by the sandbox-side poller, deleted by `km destroy` |

### DynamoDB tables

- `{prefix}-km-slack-threads` — `(channel_id, thread_ts) → claude_session_id` map. TTL 30 days from `last_turn_ts`.
- `{prefix}-slack-stream-messages` — `(channel_id, slack_ts) → {sandbox_id, session_id, transcript_offset, ttl_expiry}`. TTL 30 days.
- GSI on `{prefix}-sandboxes`: `slack_channel_id-index`.

### SQS (per-sandbox, runtime-provisioned)

- `{prefix}-slack-inbound-{sandbox-id}.fifo` — FIFO queue, 14d retention, 30s VisibilityTimeout, ContentBasedDeduplication=false.

### S3 transcript layout

`transcripts/{sandbox_id}/{session_id}.jsonl.gz` in `KM_ARTIFACTS_BUCKET`.

### Inbound webhook bootstrap

```bash
km slack init --force --signing-secret <signing-secret-from-slack-app>
# Then: paste printed Events URL into Slack App → Event Subscriptions → Request URL
```

### Render modes

`km-slack post --render plain|mrkdwn|blocks`. Default for no-flag callers stays `plain`. Streaming + inbound reply paths pass `--render "${KM_SLACK_RENDER:-blocks}"`, so new sandboxes render as Block Kit by default. Operator safety valve to fall back:

```bash
km shell <sandbox-id>
echo 'KM_SLACK_RENDER=plain' | sudo tee -a /etc/km/notify.env
```

### Required Slack bot scopes

- `chat:write`, `channels:history`, `groups:history` — base posting + inbound
- `reactions:write` — ACK reaction (👀) on inbound messages (configurable via `KM_SLACK_ACK_EMOJI` bridge env var, default `eyes`, no colons)
- `files:write` — transcript uploads
- `files:read` — inbound file attachments

### Inbound file attachments

Per-sandbox channels accept file_share uploads (images, PDFs, etc.). Bridge Lambda downloads with bot token, stages to S3 under `slack-inbound/<sandbox-id>/<thread_ts>/`, sandbox poller mirrors to `/workspace/.km-slack/attachments/<thread_ts>/`, a natural-language wrapper prepended to the prompt lists absolute paths + MIME types. Caps: 25 files/msg, 100 MB/file. Over-cap → thread-reply warning.

### Important workflow notes

- **`km init --sidecars` is required** when sandbox-side code changes so management Lambdas pick up schema additions and new sidecar binaries land in S3.
- **`km init` defaults to `--dry-run=true`.** Forgetting `--dry-run=false` produces a no-op that *looks* like a deploy ran. After a successful apply, verify:

  ```bash
  aws lambda get-function-configuration --function-name km-slack-bridge \
    --query '{MemorySize:MemorySize, Timeout:Timeout, Vars:Environment.Variables}'
  ```

  Expect `MemorySize=1024`, `Timeout=60`, and `Vars` containing `KM_ARTIFACTS_BUCKET`. A `Vars` map with only `TOKEN_ROTATION_TS` means `km slack rotate-token` blew away the env vars and Terraform hasn't re-applied — re-run `km init --dry-run=false`.
- **`km init --lambdas` builds zips but does NOT deploy them.** Use `km init --dry-run=false` for full bridge deploy.
- Existing sandboxes do NOT get new sidecars retroactively — `km destroy && km create` to provision with new binaries.
- Slack Connect (`conversations.inviteShared`) requires a **Pro Slack workspace** (free tier returns `not_allowed_token_type`).
- Bot token rotation: `km slack rotate-token --bot-token <new>` (validates, persists to SSM, force-cold-starts the bridge Lambda, smoke tests).
- **systemd EnvironmentFile gotcha:** `systemd` does NOT accept shell-style `export VAR=val` lines in `/etc/profile.d/*.sh` — it silently rejects them. The userdata template writes a parallel `/etc/km/notify.env` (no `export` prefix, systemd-format) and `km-slack-inbound-poller.service` points `EnvironmentFile=/etc/km/notify.env`. Both files are kept in sync at cloud-init time; `/etc/profile.d/km-notify-env.sh` remains the source of truth for shell sessions and Claude's bash env.
- **Slack Connect channels reject final transcript file upload.** Per-sandbox channels are externally shared (`is_ext_shared: true`); Slack's `files.completeUploadExternal` returns silent `internal_error`. Per-turn chat lines + auto-thread + DDB record-mapping all work; only the `.jsonl.gz` attachment is missing. Pull from S3 instead: `aws s3 ls s3://${KM_ARTIFACTS_BUCKET}/transcripts/<sandbox-id>/`
- **`km agent run` (non-interactive `claude -p`) skips PostToolUse hooks** per Claude Code platform behavior. Use interactive `km shell` for transcript streaming today.
- Subagent fan-out creates one Slack thread per `session_id`. `/clone` and Task-tool spawns each fire their own auto-thread-parent.
- Stale Anthropic OAuth tokens (`noBedrock: true` profiles with expired `~/.claude/.credentials.json`) produce `api_error_status: 401` in `claude -p` output, which the inbound poller logs as `WARN: agent run failed (exit 1)`. Fix: `km shell` → `claude login`.

See `docs/slack-notifications.md` for the full operator guide.

## VS Code Remote-SSH

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

- Existing sandboxes provisioned without `vscodeEnabled:true` do NOT get sshd retroactively — `km destroy && km create` to provision.
- Cross-machine portability: keys live on the creation machine only. Operators who want to `km vscode start` from a different laptop must run `km vscode rekey` there, OR manually copy `~/.km/keys/<sandbox-id>*`.
- One operator per sandbox (single authorized_keys entry).
- `km vscode start` and `km vscode status` accept the same identifier formats as other `km` subcommands: full sandbox ID (`lrn2-ee9499b5`), alias (`my-poc`), or list-row number.
- `km vscode rekey` pre-flight gates (any failure = no key changes): EC2 instance must be `running`; `km lock` must not block (override with `--force`); sandbox must have been created with `vscodeEnabled:true`.

See `docs/vscode.md` for the full operator guide.

## Presence daemon

Per-sandbox systemd-managed liveness daemon (replaces the legacy bash `_km_heartbeat`). One daemon per sandbox, root-owned, ticks every 60s.

`km-presence.service` checks five signals each tick and emits a single `source:"presence"` heartbeat event to `/run/km/audit-pipe` if **any** is positive (boolean OR):

| # | Signal | Source |
|---|---|---|
| 1 | Login shells (SSM + SSH) | `who` (utmp) |
| 2 | Attached tmux clients | `tmux list-clients` (as sandbox user) |
| 3 | Recent inbound email | New file in `/var/mail/km/new/` since last tick |
| 4 | Recent inbound Slack | `/run/km/last-slack-inbound` newer than stamp |
| 5 | Headless agent process | `pgrep` for claude/codex/km-agent-run.sh |

Stamp file `/run/km/.presence-last-tick` is touched unconditionally at end of every tick (in tmpfs; intentionally lost on reboot).

### Rollout

```bash
make build               # rebuild km CLI + km-presence binary
make sidecars            # upload km-presence to S3 alongside other sidecars
km init --sidecars       # refresh management Lambda's userdata template
```

**Docker substrate retains the bash heartbeat** — Docker sandboxes cannot run systemd, so only EC2 sandboxes get the daemon.

### Doctor check

`km doctor` includes `presence_daemon_healthy`. For each running sandbox, the check queries CloudWatch FilterLogEvents for a `source:"presence"` event in the last 5 minutes; any sandbox with no recent event is reported as WARN.

A WARN typically means one of:
- Sandbox was provisioned before presence daemon support (`km destroy && km create` to fix)
- The km-presence daemon crashed (check `journalctl -u km-presence` on the sandbox)
- CloudWatch logs ingestion is delayed (transient — re-run `km doctor`)

### Observability

```bash
sudo journalctl -u km-presence -f                  # on the sandbox: per-tick decisions
km otel <sandbox-id> --events                      # operator-side: filter source:"presence"
```

### Audit pipe on resumed sandboxes

`/run` is tmpfs and is wiped on every boot; cloud-init does NOT re-run on second boot. To survive `km pause` + `km resume`:

- `/usr/lib/tmpfiles.d/km.conf` uses `p+` to recreate `/run/km/audit-pipe` at every boot, before `sysinit.target`, with `km-sidecar:km-sidecar 0666` ownership. `p+` (not `p`) is critical — `p` is a silent no-op when a regular file occupies the path; `p+` clobbers it.
- `openAuditPipeWithRetry` (in `sidecars/audit-log/cmd/main.go`) detects a non-FIFO path, unlinks it, and recreates the FIFO before retrying.

Manual recovery for sandboxes that lack this protection and have already tripped the bug:

```bash
km shell <sandbox-id>
sudo rm /run/km/audit-pipe && \
  sudo mkfifo /run/km/audit-pipe && \
  sudo chown km-sidecar:km-sidecar /run/km/audit-pipe && \
  sudo chmod 666 /run/km/audit-pipe && \
  sudo systemctl restart km-audit-log
```

Validation: `journalctl -u km-audit-log` should show `reading from audit pipe pipe=/run/km/audit-pipe` (not `permission denied`), and `km doctor` reports `✓ Presence daemon healthy` within ~5 minutes.

## Cross-account k8s integrations

Provisions IAM roles in the klanker AWS account that trust k8s clusters in *other* AWS accounts. Pods authenticate via projected ServiceAccount tokens (IRSA) — no static IAM user keys, auto-rotating 3600s session tokens. Both the IRSA role and the create-handler Lambda role consume the shared `km-operator-policy/v1.0.0/` Terraform module so the two surfaces can never drift.

`km cluster add` generates a per-cluster `infra/live/{region-label}/cluster-{name}/terragrunt.hcl`, runs `terragrunt apply` against `infra/modules/cluster-irsa/v1.0.0/`, captures the role ARN output, and persists the cluster metadata to `km-config.yaml`. The trust policy permits `sts:AssumeRoleWithWebIdentity`, scoped to a single namespace + ServiceAccount (wildcards allowed).

**OIDC provider is account-local.** AWS STS validates web-identity tokens against an OIDC provider in the *same* account as the IAM role being assumed. The `cluster-irsa` module mirrors the remote cluster's issuer URL into a new `aws_iam_openid_connect_provider` registered in the klanker account, then references that local provider as the trust Principal. The `--oidc-provider-arn` flag names the *remote* cluster's provider only to derive its issuer URL — the account portion of that ARN is informational.

### CLI flags

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

### Important workflow notes

- **Idempotency:** `km cluster add --name foo ...` returns the existing role ARN if `foo` already exists in `km-config.yaml` — safe to re-run.
- **Rollback on persist failure:** if `terragrunt apply` succeeds but writing `km-config.yaml` fails, the IAM role is left in place. Run `km cluster rm <name>` (using the role name from terraform state) to clean up.
- **Wildcard trust:** `--namespace=*` makes the role assumable by the named ServiceAccount in any namespace. Specify a literal namespace for tighter scoping.
- **No `--sidecars` propagation required:** Cluster IRSA ships no sandbox-side or Lambda-side code. Operators only need a fresh `km` binary.
- **OIDC provider auto-detect:** Before generating the per-cluster terragrunt.hcl, `km cluster add` calls `aws iam list-open-id-connect-providers` against the target account. If the cluster's issuer URL is already registered, the module sets `register_oidc_provider = false` and references the existing provider via a Terraform data source. Otherwise it creates a fresh provider. Override with `--register-oidc-provider=true|false`. `km cluster rm` only destroys providers that this stack registered — pre-existing providers are left intact.

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
