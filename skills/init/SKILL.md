---
name: init
description: One-time platform setup for an operator workstation — km configure, km init, multi-instance resource_prefix isolation, Slack/Lambda bootstrap, and rollout sequences after sidecar/Lambda changes
---

# Platform Initialization

This skill covers operator-workstation tasks that set up or upgrade a Klanker Maker install. Most are run once per environment; a few (`km init --sidecars`, `make build`) repeat whenever you edit km source.

**Audience:** the operator running `km` on their workstation. For agent-side workflows inside a sandbox, see `klanker:sandbox`, `klanker:email`, or `klanker:slack`.

## Cross-references

- `klanker:user` — broad operator CLI tour (creating sandboxes, agent runs, lifecycle)
- `klanker:vscode` — VS Code Remote-SSH onboarding (its own one-time `km init --sidecars` requirement)
- `klanker:cluster` — k8s cross-account IRSA (its own provisioning flow)
- `klanker:slack` — agent-side Slack posting (after this skill bootstraps the bridge)

## Step 1: Configure the install

```bash
km configure
```

Prompts for:
- `resource_prefix` (default `km`) — controls every per-install resource name (`{prefix}-sandboxes` table, `{prefix}-sandbox-email` SES rule set, etc.). Set this once; it cannot be safely changed later.
- `email_subdomain` (default `sandboxes`) — DNS subdomain for sandbox email addresses.

The chosen values are persisted to `km-config.yaml` and exported into every `terragrunt` invocation via `KM_RESOURCE_PREFIX` / `KM_EMAIL_SUBDOMAIN`. To reset the prefix back to `km`, use `km configure --reset-prefix` (this is the only way — re-running `km configure` preserves the existing value).

## Step 2: Build the binary

```bash
make build
```

**Always `make build`, never bare `go build`.** The Makefile passes ldflags for the version string; missing them makes `km --version` lie and breaks the `km init --sidecars` upload path (S3 key embeds the version).

## Step 3: Bootstrap or upgrade regional infrastructure

```bash
km init --plan             # Phase 84.2: real terragrunt plan per module + destroy-class safety gate (NEVER applies)
km init --plan --i-accept-destroys   # same plan, but clear exit code if only protected-type destroys are blocking (per-invocation; no auto-apply)
km init --dry-run=true     # static info dump — module ordering + skip annotations (does NOT run terragrunt plan)
km init --dry-run=false    # actually apply
```

Use `--plan` to see what `terragrunt apply` would actually change — it runs a real plan per module and trips on any destroy/replace of a curated protected resource type (Phase 84 incident protection). `--dry-run=true` is the older info-dump that lists module ordering and env-var skip annotations; it does NOT run terragrunt plan.

`km init` defaults to `--dry-run=true`. Forgetting `--dry-run=false` produces a no-op that *looks* like a deploy ran. After a successful apply, verify Lambda config picked up:

```bash
aws lambda get-function-configuration --function-name km-slack-bridge \
  --query '{MemorySize:MemorySize, Timeout:Timeout, Vars:Environment.Variables}'
```

Expect `MemorySize=1024`, `Timeout=60`, and `Vars` containing `KM_ARTIFACTS_BUCKET`. A `Vars` map with only `TOKEN_ROTATION_TS` means `km slack rotate-token` blew away the env vars and Terraform hasn't re-applied — re-run `km init --dry-run=false`.

### Fast-path variants

```bash
km init --sidecars     # rebuild + upload sidecar binaries (km-slack, km-presence, audit-log, etc.) to S3
                       # AND refresh the management Lambda's bundled km binary so new schema fields are recognized
km init --lambdas      # build Lambda zips locally + bump create-handler env stamp
                       # NOTE: --lambdas does NOT actually deploy bridge Lambdas via terragrunt
                       # Use plain `km init --dry-run=false` for full bridge deploy
```

**When to run `km init --sidecars`:**
- After editing any code under `sidecars/` (km-slack, km-presence, audit-log, http-proxy, dns-proxy, tracing)
- After adding a profile schema field that the management Lambda must understand (otherwise remote `km create` fails on unknown field)
- After every km CLI version bump that ships new sidecar binaries

**Existing sandboxes do NOT get new sidecars retroactively.** `km destroy && km create` to roll forward.

## Step 4: Bootstrap Slack (optional)

If sandboxes will post to Slack:

```bash
km slack init                    # interactive bootstrap
# Or non-interactive:
km slack init --bot-token xoxb-... --invite-email ops@example.com
```

`km slack init` validates the bot token (calls `auth.test`), writes SSM parameters, creates the shared channel, sends a Slack Connect invite to `--invite-email`, and deploys the `km-slack-bridge` Lambda.

### Add inbound event webhook

```bash
km slack init --force --signing-secret <signing-secret-from-slack-app>
# Then paste the printed Events URL into:
#   Slack App → Event Subscriptions → Request URL
```

The signing secret enables HMAC-SHA256 verification of inbound `/events` webhooks (Slack message events → SQS FIFO → sandbox poller). Required for bidirectional chat in per-sandbox channels.

### Slack rotations

```bash
km slack rotate-token --bot-token <new-token>             # validate, persist to SSM, force bridge cold-start, smoke test
km slack rotate-signing-secret --signing-secret <new>     # rotate signing secret
```

After `rotate-token`, **re-run `km init --dry-run=false`** to restore Lambda env vars (the rotation force-cold-starts but does not re-apply Terraform env, so `KM_ARTIFACTS_BUCKET` etc. can be lost).

### Required Slack bot scopes

| Scope | Used for |
|---|---|
| `chat:write` | base posting |
| `channels:history`, `groups:history` | inbound message events |
| `reactions:write` | ACK reaction (👀) on inbound messages |
| `files:write` | transcript uploads |
| `files:read` | inbound file attachments (downloaded by the bridge) |

After adding scopes to the Slack App, re-install the app and `km slack rotate-token` with the new token.

### Slack Connect channels

Per-sandbox channels are externally shared (`is_ext_shared: true`) via Slack Connect (`conversations.inviteShared`). This requires a **Pro Slack workspace** (free tier returns `not_allowed_token_type`).

Slack Connect channels reject final transcript file uploads (`files.completeUploadExternal` returns silent `internal_error`). Per-turn chat lines + auto-thread + DDB record-mapping still work; pull the `.jsonl.gz` transcript from S3 instead:

```bash
aws s3 ls s3://${KM_ARTIFACTS_BUCKET}/transcripts/<sandbox-id>/
```

## Step 5: Verify health

```bash
km doctor
km doctor --all-regions     # if you have sandboxes in multiple regions
```

`km doctor` runs ~22 checks. Critical ones after init:

| Check | Means |
|---|---|
| `presence_daemon_healthy` | each running sandbox emitted a `source:"presence"` event in the last 5 minutes |
| `slack_inbound_queue_exists` | every inbound-enabled sandbox has a healthy SQS queue |
| `slack_inbound_stale_queues` | orphan SQS queues with no DDB sandbox row |
| `slack_app_events_subscription` | bot has `channels:history` + `groups:history` + `reactions:write` |
| `slack_transcript_table_exists` | DDB table provisioned + ACTIVE |
| `slack_files_write_scope` / `slack_files_read_scope` | bot has the required file scopes |

## Multi-instance support

A single AWS account can host multiple km installs distinguished by `resource_prefix` (default `km`, common alternates: `rg`, `dev`, `staging`). Every per-install resource carries a `km:resource-prefix=${prefix}` tag.

### Second install on the same account

```bash
# 1. Configure with a distinct prefix
export KM_RESOURCE_PREFIX=rg
export KM_EMAIL_SUBDOMAIN=rg-sandboxes
km configure   # accept the prefix/subdomain prompts

# 2. CRITICAL — do not steal SES rule-set activation from the primary install
export KM_SES_ACTIVATE_RULESET=false

# 3. Build + init
make build
km init --sidecars
km init --plan            # verify plan: no aws_ses_active_receipt_rule_set in destroy list
km init --dry-run=false
```

AWS SES allows only ONE active receipt rule set per account/region. The second install's rule set is created but **not activated** — the primary install keeps inbound email. To hand off inbound email to the second install (a deliberate operator action):

```bash
aws ses list-receipt-rule-sets --query 'RuleSets[*].Name'
aws ses set-active-receipt-rule-set --rule-set-name rg-sandbox-email
```

Rollback (restore primary):

```bash
aws ses set-active-receipt-rule-set --rule-set-name km-sandbox-email
```

### Tag backfill for pre-isolation installs

```bash
AWS_DEFAULT_REGION=us-east-1 AWS_PROFILE=<your-profile> \
  km doctor --backfill-tags --dry-run=true     # preview

AWS_DEFAULT_REGION=us-east-1 AWS_PROFILE=<your-profile> \
  km doctor --backfill-tags --dry-run=false    # apply
```

Idempotent. Second run reports `Tagged: 0`. Required when running outside `km configure` context because the tag emitter reads `AWS_DEFAULT_REGION` + `AWS_PROFILE` directly.

## Rollout sequence template

When you edit km source (CLI, sidecar, userdata template, or Lambda code):

```bash
make build                                  # always first
km init --sidecars                          # if sidecars/* or userdata template changed
km init --dry-run=false                     # if Terraform modules or Lambda code changed
km doctor                                   # confirm health
# Existing sandboxes do NOT get new sidecars — km destroy && km create to roll forward
```

`km init --lambdas` alone is **insufficient** for bridge Lambda deploys — it builds the zip locally but does not upload it via terragrunt. Use `km init --dry-run=false`.

## Common gotchas

- **`km configure` no longer overwrites `resource_prefix`** on re-run. To reset, use `km configure --reset-prefix`.
- **systemd EnvironmentFile gotcha:** systemd does NOT accept shell-style `export VAR=val` lines. The userdata template writes a parallel `/etc/km/notify.env` (no `export` prefix) and points systemd units at it. `/etc/profile.d/km-notify-env.sh` remains the source of truth for shell sessions.
- **Audit pipe on resumed sandboxes:** `/run` is tmpfs and is wiped on every boot; cloud-init does NOT re-run on second boot. `/usr/lib/tmpfiles.d/km.conf` uses `p+` to recreate `/run/km/audit-pipe` at every boot, before `sysinit.target`. Pre-fix sandboxes that have already tripped the bug can be recovered manually:
  ```bash
  km shell <sandbox-id>
  sudo rm /run/km/audit-pipe && \
    sudo mkfifo /run/km/audit-pipe && \
    sudo chown km-sidecar:km-sidecar /run/km/audit-pipe && \
    sudo chmod 666 /run/km/audit-pipe && \
    sudo systemctl restart km-audit-log
  ```
- **Stale Anthropic OAuth tokens** (`noBedrock: true` profiles with expired `~/.claude/.credentials.json`) produce `api_error_status: 401` in `claude -p` output. Fix: `km shell` → `claude login`.

## Teardown

Two-layer, must be in this order:

```bash
km uninit         # regional infra (Lambda, DynamoDB, SES, VPC)
km unbootstrap    # foundation (S3 state bucket — destructive)
```

Always `km uninit` first; `km unbootstrap` deletes the state bucket and any orphaned regional state becomes unrecoverable.

See `OPERATOR-GUIDE.md` for the full operator runbook.
