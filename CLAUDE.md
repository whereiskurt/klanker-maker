# Klanker Maker

This file serves as the Terragrunt repo root anchor for `find_in_parent_folders("CLAUDE.md")`.

## Project

Policy-driven sandbox platform. See `.planning/PROJECT.md` for details.

Multi-instance support: km supports multiple installs in a single AWS account via the `resource_prefix` knob in `km-config.yaml` (default `km`). `km configure` prompts for `resource_prefix` and `email_subdomain` (one-time choices propagated to terragrunt via `KM_RESOURCE_PREFIX` / `KM_EMAIL_SUBDOMAIN`). See `OPERATOR-GUIDE.md` § Multi-instance support and the `klanker:init` skill.

## Where to look

| You want to… | Look at |
|---|---|
| Operator CLI tour | `klanker:user` skill |
| One-time platform setup, `km init`, multi-instance, Slack bootstrap | `klanker:init` skill |
| Send / receive email from inside a sandbox | `klanker:email` skill |
| Inject SOPS-encrypted secrets into a sandbox | `docs/sandbox-secrets.md` (Phase 89) |
| Post to Slack from inside a sandbox (incl. transcript streaming, inbound, attachments) | `klanker:slack` skill |
| Ask the operator to do something via email | `klanker:operator` skill |
| Detect sandbox environment + verify tooling | `klanker:sandbox` skill |
| VS Code Remote-SSH operator workflow | `klanker:vscode` skill |
| Cross-account k8s (IRSA) cluster onboarding | `klanker:cluster` skill |
| Full operator runbook | `OPERATOR-GUIDE.md` |
| Email protocol deep-dive (SES, IAM, signing) | `docs/multi-agent-email.md` |
| Slack runbook (full setup, troubleshooting) | `docs/slack-notifications.md` |
| VS Code runbook | `docs/vscode.md` |
| Snapshot-backed EBS volumes in profiles | `OPERATOR-GUIDE.md` § additionalSnapshots |
| Codex parity, `spec.cli.agent`, Slack prefix routing & agent switching | `docs/codex-parity.md` (Phase 70) |

## CLI

- `km validate <profile.yaml>` — validate a SandboxProfile
- `km create <profile.yaml>` — provision a sandbox (`--no-bedrock`, `--docker`, `--alias`, `--on-demand`, `--prompt <text-or-@file>` repeatable, `--wait`)
- `km destroy <sandbox-id>` — teardown a sandbox (`--remote` by default; `km kill` is an alias)
- `km pause <sandbox-id>` — hibernate/pause an EC2 or Docker instance (preserves infra)
- `km resume <sandbox-id>` — resume a paused or stopped sandbox
- `km lock <sandbox-id>` — safety lock preventing destroy/stop/pause (atomic DynamoDB)
- `km unlock <sandbox-id>` — remove safety lock (requires confirmation or `--yes`)
- `km list` — list sandboxes (narrow default, `--wide` for all columns)
- `km agent <sandbox-id> --claude` — interactive Claude session via SSM (`--no-bedrock` for direct API)
- `km agent run <sandbox-id> --prompt "..."` — fire-and-forget non-interactive Claude in tmux (`--wait`, `--interactive`, `--no-bedrock`, `--auto-start`)
- `km agent attach <sandbox-id>` — attach to a running agent's tmux session (Ctrl-B d to detach)
- `km agent results <sandbox-id>` — fetch latest run output (`--run <id>` for specific run)
- `km agent list <sandbox-id>` — list all agent runs with status and output size (`--queue` to list on-box prompt queue entries instead)
- `km at '<time>' <cmd>` — schedule deferred/recurring operations; supports `create`, `destroy`, `kill`, `stop`, `pause`, `resume`, `extend`, `budget-add`, `agent run` (`km schedule` is an alias)
- `km at list` / `km at cancel <name>` — manage scheduled operations
- `km email send` — send signed email between sandboxes or to/from operator (`--from`, `--to`, `--cc`, `--use-bcc`, `--reply-to`)
- `km email read <sandbox>` — read sandbox mailbox with signature verification and auto-decryption (`--json`, `--mark-read`)
- `km otel <sandbox-id>` — OTEL telemetry + AI spend summary (`--prompts`, `--events`, `--tools`, `--timeline`)
- `km slack init` — bootstrap Slack integration (`--bot-token`, `--invite-email`, `--shared-channel`, `--signing-secret`, `--force`)
- `km slack test` — end-to-end smoke test through the bridge
- `km slack status` — print SSM-backed Slack config
- `km slack invite <email>` — invite an email to a Slack channel; auto-detects native vs Connect (`--channel`, `--external`, `--dry-run`)
- `km slack manifest` — render a deployment-specific Slack App manifest to stdout (`--app-name`)
- `km slack rotate-token --bot-token <new>` — rotate Slack bot token + cold-start the bridge
- `km slack rotate-signing-secret --signing-secret <new>` — rotate Slack App signing secret
- `km vscode start <sandbox-id>` — open SSM port-forward + ssh-config Host entry for VS Code Remote-SSH (`--local-port`)
- `km vscode status <sandbox-id>` — check sshd state + authorized_keys presence
- `km vscode rekey <sandbox-id>` — rotate per-sandbox keypair without `km destroy && km create` (`--force`, `--yes`)
- `km cluster add --name <name> --oidc-provider-arn <arn>` — provision cross-account IRSA role (`--namespace`, `--service-account`, `--aws-profile`, `--region`, `--dry-run`, `--register-oidc-provider`)
- `km cluster list` — show configured cross-account cluster roles
- `km cluster rm <name>` — destroy a cluster IRSA role
- `km init` — initialize regional infrastructure (`--sidecars` for fast binary deploy, `--lambdas` for Lambda-only deploy, `--plan` to preview with destroy-class safety gate, `--dry-run=false` to actually apply)
- `km bootstrap --shared-ses` — provision the shared SES rule set (idempotent; `--plan` previews with destroy-class safety gate)
- `km bootstrap --shared-secrets-key` — provision the shared KMS key for SOPS secret injection (one-time per install; `--plan` previews with destroy-class gate; see `docs/sandbox-secrets.md`)
- `km bootstrap --all` — chain foundation (SCP/KMS/artifacts) + shared SES rule set in one command; mutex with `--shared-ses`; `--plan` honors the destroy-class gate
- `km env [--aws-profile]` — print exportable `KM_*` block for `eval $(km env)` to drive terragrunt directly (excludes `AWS_PROFILE` by default; `KM_ACCOUNTS_TERRAFORM` intentionally omitted)
- `km shell <sandbox-id>` — SSM shell (`--root`, `--ports`, `--no-bedrock`, `--learn`, `--ami`)
- `km ami list` / `km ami bake <sandbox-id>` / `km ami copy <ami-id> --region <dest>` / `km ami delete <ami-id>` — operator-baked AMI lifecycle
- `km info` — platform config, accounts, SES quota, AWS spend, DynamoDB tables
- `km doctor` — validate platform health (config, credentials, SES, Lambda, VPC, stale resources, AMIs, EBS, Slack inbound, presence daemon, etc.; `--all-regions`, `--backfill-tags`)

## Architecture

- `cmd/km/` — CLI entry point
- `internal/app/cmd/` — Cobra commands
- `pkg/profile/` — Schema, validation, inheritance
- `pkg/compiler/` — Profile → Terragrunt artifacts
- `pkg/ebpf/` — eBPF enforcer (cgroup BPF programs, DNS resolver, audit consumer)
- `pkg/terragrunt/` — Terragrunt runner
- `pkg/aws/` — AWS SDK helpers (DynamoDB metadata, S3 artifacts, SES, EC2)
- `sidecars/` — HTTP proxy (MITM), DNS proxy, audit-log, tracing, km-presence
- `infra/modules/` — Terraform modules (`km-operator-policy`, `cluster-irsa`, `create-handler`, `ses`, etc.)
- `infra/live/` — Terragrunt hierarchy
- `profiles/` — Built-in SandboxProfile YAML files
- `skills/` — User-invocable skills (klanker plugin)
- `spec.runtime.additionalSnapshots` — list of snapshot-backed EBS volumes. Each entry materialises a fresh `aws_ebs_volume` from an existing EBS snapshot, attaches on `/dev/sd[f-p]`, mounts with userdata-detected filesystem. Coexists with `additionalVolume` (both can be set). EC2-only. Volume lifecycle = sandbox lifecycle. See `OPERATOR-GUIDE.md` § additionalSnapshots.

## SES per-install rule namespacing

km supports multiple installs in a single AWS account via SES rule namespacing. Each install owns a unique `resource_prefix` and per-prefix SES receipt rules under a single account-shared rule set.

**Operator address format:** `operator-{resource_prefix}@{email_subdomain}.{domain}`
Example: `operator-km@sandboxes.example.com` for the default install; `operator-km2@sandboxes.example.com` for a second install with `resource_prefix: km2`.

**Shared rule set:** `sandbox-email-shared` — account-shared, owned by `infra/modules/ses-shared-rule-set/v1.0.0/`, has `lifecycle.prevent_destroy = true`. Provisioned once per account/region by `km bootstrap --shared-ses`; idempotent on re-apply.

**Per-install rules:** Each install adds exactly two rules to the shared rule set:
- `{prefix}-operator-inbound` — routes `operator-{prefix}@` to the operator Lambda
- `{prefix}-sandbox-catchall` — routes all other `{sandbox-id}@` addresses to sandbox mailboxes

`km uninit` removes only this install's two rules and leaves the shared rule set and sibling installs' rules intact.

**Doctor check:** `km doctor` reports `✓ SES rules healthy` when all rules in the shared rule set map to a known `resource_prefix`, or `⚠ orphan SES rules: <list>` when rules exist for prefixes not in the local `km-config.yaml`. The orphan check is WARN-level — expected when a sibling install is present.

## Plan-before-apply destroy-class gate

`km init --plan` and `km bootstrap --shared-ses --plan` run real `terragrunt plan` per module with a curated destroy-class safety gate that trips on destroy/replace of protected resource types (SES identities, Route53 records, S3 buckets, DynamoDB tables, KMS keys, etc.). `--i-accept-destroys` is the per-invocation override (never persisted; does not auto-apply). `km doctor` nudges toward `--plan` before any apply.

See `OPERATOR-GUIDE.md` § Plan-before-apply for the trip-block format, override flow, and protected-type list.

## Wrapper-level bootstrap UX

The path from `git clone` to first apply is shaped by:

**Configure-time (`km configure`):**
- HeadBucket-checked `state_bucket` with `[Y/edit/abort]` retry UX on globally-taken names.
- Auto-derived `artifacts_bucket = ${prefix}-artifacts-${account_id}`; angle-bracket and literal `km-artifacts-12345` placeholders rejected at load.
- `Next steps:` finale prints the canonical bootstrap sequence to stdout AND embeds it as `#` header comments at the top of the generated yaml.
- Shell-env conflict WARN per conflicting `KM_*` env var.

**Bootstrap-time (`km bootstrap`):**
- Dry-run text correctly says `would run: terragrunt apply`; degrades gracefully when AWS auto-detect is unreachable.
- Status banner WARNs on empty required account IDs and shows `(not set)`.
- `--all` chains the foundation + shared SES rule set subflows.

**Init-time (`km init`):**
- Per-var drift WARN on env-vs-yaml mismatch.
- `km init --plan` skips fresh-install dependents missing upstream `outputs.json` — exit 0, re-runs cleanly once `network` is applied.
- Hard-fail on missing `artifacts_bucket` with recovery commands in error.

**`accounts.*` yaml-authoritative behavior:**
- `accounts.organization`, `accounts.dns_parent`, `accounts.application`: yaml wins, env values do NOT override `cfg`. `KM_ACCOUNTS_*` still exported to terragrunt subprocesses.
- `accounts.terraform`: env wins (asymmetry preserved — operators retain shell-local override for the cross-account terraform role).

## Network Enforcement

Three enforcement modes via `spec.network.enforcement`:
- `proxy` (default) — iptables DNAT → userspace proxy sidecars
- `ebpf` — cgroup BPF programs (connect4, sendmsg4, sockops, egress) with LPM trie allowlist
- `both` — eBPF primary + proxy for L7 inspection (Bedrock metering, Anthropic metering, OpenAI metering, GitHub filtering)

eBPF SSL uprobes provide passive TLS plaintext capture for audit/observability alongside enforcement.

## Budget Metering Coverage (Phase 88)

http-proxy MITM intercepts and meters three AI provider endpoints into the same
`BUDGET#ai#{modelID}` DynamoDB row shape:

- **Bedrock InvokeModel** (`bedrock-runtime.*.amazonaws.com`) — Claude on Bedrock (Phase 6)
- **Anthropic direct API** (`api.anthropic.com`) — Claude Code (Phase 20)
- **OpenAI direct API** (`api.openai.com`) — Codex CLI + raw OpenAI SDK (Phase 88)

L7 proxy hosts are gated per profile:
- `spec.execution.useBedrock: true` → adds `.amazonaws.com,api.anthropic.com`
- `spec.cli.agent: codex` → adds `api.openai.com` (Phase 88)

Unknown model IDs in any provider write rows with `spentUSD=0` and log
`event_type=*_unknown_model` so operators see the gap in `km status`.

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
km validate learned.*.yaml
km create learned.*.yaml               # spin up a new sandbox from the baked AMI
```

`--ami` requires `--learn`. The bake fires before the SIGUSR1 flush so the AMI ID can be embedded in the generated profile. AMIs are private to the application AWS account, tagged with sandbox metadata, and tracked by `km ami list` / `km doctor` (stale check). `spec.runtime.ami` accepts both slugs (`amazon-linux-2023`, `ubuntu-24.04`, `ubuntu-22.04`) and raw AMI IDs. When launching from a baked AMI that already declares `/dev/sdf` in its block device mappings, the compiler auto-rotates `additionalVolume` onto the next free device (`/dev/sdg`..`/dev/sdp`) so launches don't collide.

## Agent Execution

Run AI agents non-interactively inside sandboxes. Agents run in persistent tmux sessions that survive SSM disconnects.

```bash
km agent run <sandbox> --prompt "fix the failing tests"                          # fire-and-forget
km agent run <sandbox> --prompt "What model are you?" --wait                     # blocking, prints JSON
km agent run <sandbox> --prompt "refactor the auth module" --interactive         # attach live
km agent attach <sandbox>                                                         # attach to existing
km agent results <sandbox>                                                        # latest run
km agent results <sandbox> --run 20260410T143000Z | jq '.result'                  # specific run
km agent list <sandbox>                                                           # all runs
km agent run <sandbox> --prompt "..." --no-bedrock --wait                         # direct API (needs claude login)
km at '5pm tomorrow' agent run <sandbox> --prompt "..." --auto-start              # scheduled
```

Output lands at `/workspace/.km-agent/runs/<timestamp>/output.json`. Detach from interactive with `Ctrl-B d` — the agent keeps running.

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

- `spec.execution.configFiles` — pre-seed tool config files (written after `initCommands`, owned by sandbox user)
- `spec.cli.noBedrock` — operator-side default; doesn't affect sandbox provisioning, only CLI behavior when connecting

### Agent: claude | codex (Phase 70)

`spec.cli.agent` selects the default agent for `km shell` / `km agent run` /
Slack inbound dispatch:

```yaml
spec:
  cli:
    agent: codex  # or "claude"; default claude; absence ≡ claude
```

The compiler writes `KM_AGENT` to `/etc/profile.d/km-notify-env.sh` and
`/etc/km/notify.env`. It also writes `~/.codex/config.toml` on every sandbox
regardless of value — Claude-default sandboxes have an inert config (forward-
compat for when Codex ships a Claude-Code-style hook API).

Per-turn override via Slack: a message starting with `claude:` or `codex:`
selects the agent for that turn (case-insensitive, anchored at start, zero or
one space after colon). Inside an existing thread, naming the *other* agent
triggers an 8-step clean handoff to a new top-level message. See
`docs/codex-parity.md` for the full switch sequence.

**`km init --sidecars` is required** after this phase ships so management
Lambdas pick up the schema addition. Existing sandboxes don't pick up
`agent: codex` retroactively — `km destroy && km create`.

### DDB column hangover (Phase 70)

The `km-slack-threads.claude_session_id` column (Phase 67) now stores
agent-agnostic session IDs — either a Claude session ID or a Codex session ID,
based on the row's `agent_type`. The column name is a Phase 67 hangover;
renaming would require a migration job we chose not to run (cosmetic only).

Future agents (Goose etc.) slot in as new `agent_type` enum values without
further DDB schema work.

### Phase 72: Corporate workspace support — auto-detect invite + manifest generator

Adds three capabilities for installing klankermaker into corporate Slack workspaces (where
invitees are native workspace members rather than external collaborators):

- `km slack manifest` — generates a deployment-specific Slack App manifest including the new
  `users:read.email` scope. Pipe to a file and paste into Slack admin "From manifest" UI.
- `km slack invite <email> [--channel <name|id>] [--external] [--dry-run]` — ad-hoc command to
  add people to channels post-install. Auto-detects whether to use `conversations.invite`
  (native) or `conversations.inviteShared` (Slack Connect, requires Pro tier). `--dry-run` is a
  read-only probe: classifies the address without sending any invite or joining any channel.
- `spec.cli.notifySlackInviteEmails: []string` — profile field that auto-invites ADDITIONAL
  people (beyond the always-invited primary operator) to the per-sandbox `#sb-{id}` channel after
  `km create` succeeds. Auto-detects native vs Connect.
- `spec.cli.useSlackConnect: *bool` (default true) — gates the Connect fallback for the
  `notifySlackInviteEmails` loop only. True: external addresses auto-Connected. False:
  external addresses skipped with a fail-soft warning + follow-up command. Does NOT affect the
  primary operator invite (always invited) or `km slack invite`/`km slack init`.

**New required Slack bot scope:** `users:read.email`. Existing PoC installs need a one-time
manifest update + reinstall + token rotation:

```bash
km slack manifest > /tmp/km-app.json
# Paste into Slack admin → Apps → existing app → App Manifest → Save → Reinstall
km slack rotate-token --bot-token <new-token>
km doctor
```

**`km doctor` adds:** `slack_users_read_email_scope` (WARN if missing).

See `docs/slack-notifications.md` § Phase 72 for the full operator guide.
