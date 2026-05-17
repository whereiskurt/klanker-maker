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
| Post to Slack from inside a sandbox (incl. transcript streaming, inbound, attachments) | `klanker:slack` skill |
| Ask the operator to do something via email | `klanker:operator` skill |
| Detect sandbox environment + verify tooling | `klanker:sandbox` skill |
| VS Code Remote-SSH operator workflow | `klanker:vscode` skill |
| Cross-account k8s (IRSA) cluster onboarding | `klanker:cluster` skill |
| Full operator runbook | `OPERATOR-GUIDE.md` |
| Email protocol deep-dive (SES, IAM, signing) | `docs/multi-agent-email.md` |
| Slack runbook (full setup, troubleshooting) | `docs/slack-notifications.md` |
| VS Code runbook | `docs/vscode.md` |

## CLI

- `km validate <profile.yaml>` — validate a SandboxProfile
- `km create <profile.yaml>` — provision a sandbox (`--no-bedrock`, `--docker`, `--alias`, `--on-demand`)
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
- `km agent list <sandbox-id>` — list all agent runs with status and output size
- `km at '<time>' <cmd>` — schedule deferred/recurring operations; supports `create`, `destroy`, `kill`, `stop`, `pause`, `resume`, `extend`, `budget-add`, `agent run` (`km schedule` is an alias)
- `km at list` / `km at cancel <name>` — manage scheduled operations
- `km email send` — send signed email between sandboxes or to/from operator (`--from`, `--to`, `--cc`, `--use-bcc`, `--reply-to`)
- `km email read <sandbox>` — read sandbox mailbox with signature verification and auto-decryption (`--json`, `--mark-read`)
- `km otel <sandbox-id>` — OTEL telemetry + AI spend summary (`--prompts`, `--events`, `--tools`, `--timeline`)
- `km slack init` — bootstrap Slack integration (`--bot-token`, `--invite-email`, `--shared-channel`, `--signing-secret`, `--force`)
- `km slack test` — end-to-end smoke test through the bridge
- `km slack status` — print SSM-backed Slack config
- `km slack rotate-token --bot-token <new>` — rotate Slack bot token + cold-start the bridge
- `km slack rotate-signing-secret --signing-secret <new>` — rotate Slack App signing secret
- `km vscode start <sandbox-id>` — open SSM port-forward + ssh-config Host entry for VS Code Remote-SSH (`--local-port`)
- `km vscode status <sandbox-id>` — check sshd state + authorized_keys presence
- `km vscode rekey <sandbox-id>` — rotate per-sandbox keypair without `km destroy && km create` (`--force`, `--yes`)
- `km cluster add --name <name> --oidc-provider-arn <arn>` — provision cross-account IRSA role (`--namespace`, `--service-account`, `--aws-profile`, `--region`, `--dry-run`, `--register-oidc-provider`)
- `km cluster list` — show configured cross-account cluster roles
- `km cluster rm <name>` — destroy a cluster IRSA role
- `km init` — initialize regional infrastructure (`--sidecars` for fast binary deploy, `--lambdas` for Lambda-only deploy, `--plan` to preview with destroy-class safety gate, `--dry-run=false` to actually apply)
- `km bootstrap --shared-ses` — provision foundation SES rule set (idempotent; `--plan` previews with destroy-class safety gate)
- `km bootstrap --all` — Phase 84.3 chain: foundation (SCP/KMS/artifacts) + shared SES rule set in one command; mutex with `--shared-ses`; `--plan` honors the 84.2 destroy-class gate
- `km env [--aws-profile]` — Phase 84.3 helper: print exportable `KM_*` block for `eval $(km env)` (excludes `AWS_PROFILE` by default; `KM_ACCOUNTS_TERRAFORM` intentionally omitted)
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

## Phase 84: SES per-install rule namespacing via operator address prefix

Phase 84 (2026-05-16) introduced per-install SES rule namespacing so a second `km init` in the same AWS account never touches the first install's inbound email path.

**Operator address format:** `operator-{resource_prefix}@{email_subdomain}.{domain}`
Example: `operator-km@sandboxes.example.com` for the default install; `operator-km2@sandboxes.example.com` for a second install with `resource_prefix: km2`.

**Shared rule set:** `sandbox-email-shared` — account-shared, owned by `infra/modules/ses-shared-rule-set/v1.0.0/`, has `lifecycle.prevent_destroy = true`. Provisioned once per account/region by `km bootstrap --shared-ses`; idempotent on re-apply.

**Per-install rules:** Each install adds exactly two rules to the shared rule set:
- `{prefix}-operator-inbound` — routes `operator-{prefix}@` to the operator Lambda
- `{prefix}-sandbox-catchall` — routes all other `{sandbox-id}@` addresses to sandbox mailboxes

`km uninit` removes only this install's two rules and leaves the shared rule set and sibling installs' rules intact.

**Bootstrap:** `km bootstrap --shared-ses` provisions the foundation (idempotent auto-detect via `SESIdentityLister` — Phase 80 cluster-irsa pattern). Must run once before `km init` on a fresh account, or after upgrading from Phase 82.

**Doctor check:** `km doctor` reports `✓ SES rules healthy` when all rules in the shared rule set map to a known `resource_prefix`, or `⚠ orphan SES rules: <list>` when rules exist for prefixes not in the local `km-config.yaml`. The orphan check is WARN-level — expected when a sibling install is present.

**Phase 84 upgrade procedure (one-time, existing installs):**

```bash
make build
km init --sidecars
km bootstrap --shared-ses
km init --dry-run=true
km init --dry-run=false
km configure
km doctor
```

See `OPERATOR-GUIDE.md` § Phase 84 upgrade for the detailed runbook and two-install coexistence scenario.

### Phase 84.1: Upgrade-safety gap closure (2026-05-16)

Phase 84.1 closes 8 gaps from Phase 84 UAT without changing the Phase 84 runtime design:

- `ExportTerragruntEnvVars` (renamed from `ExportConfigEnvVars`) exports the full env-var set including `KM_ROUTE53_ZONE_ID` and `KM_ARTIFACTS_BUCKET`; every km command that invokes terragrunt calls it exactly once (GAP-1, GAP-7).
- Terragrunt runner is bounded by per-module context timeouts (default 5–10 min) and emits a quiet-mode heartbeat every 15s — wedged applies no longer hang silently (GAP-4, GAP-5).
- `km doctor` includes `Terraform state lock digest` check that detects S3-vs-DynamoDB drift and prints an exact `aws dynamodb update-item` recovery command (GAP-8). See OPERATOR-GUIDE.md § State-digest mismatch recovery.
- Foundation `ses-shared-rule-set/v1.0.0/` register_* flags now mean "manage this resource", not "create only on first apply". Re-running `km bootstrap --shared-ses` is a true no-op (GAP-2).
- Foundation auto-detect prefers foundation tfstate ownership over AWS reality, preventing the in-place-upgrade data-loss scenario (GAP-3).
- Foundation main.tf ships with `import {}` blocks and regional `ses/v2.0.0/main.tf` ships with `removed { lifecycle { destroy = false } }` blocks — the v1.0.0 → v2.0.0 cutover destroys zero shared AWS resources (GAP-6, the highest-impact gap).
- `lifecycle.prevent_destroy = true` on the shared rule set is preserved as a safety net for the new register_*=manage semantics.

See `OPERATOR-GUIDE.md` § Phase 84.1 upgrade safety for the in-place upgrade runbook.

### Phase 84.2: km init --plan flag with destroy-class gate (2026-05-16)

Phase 84.2 adds `km init --plan` and `km bootstrap --shared-ses --plan` — real `terragrunt plan` per module with a curated destroy-class safety gate that trips on destroy/replace of protected resource types (SES identities, Route53 records, S3 buckets, DynamoDB tables, KMS keys, etc.). `--i-accept-destroys` is the per-invocation override (never persisted; does not auto-apply). `km doctor` nudges operators toward `--plan` before any future apply.

See `OPERATOR-GUIDE.md` § Phase 84.2 plan-before-apply for the full runbook (when to use, trip-block format, override flow, bootstrap parity, and protected-type list).

### Phase 84.3: Wrapper-level bootstrap UX (2026-05-17)

Phase 84.3 tightens the operator path from `git clone` to first apply with eight wrapper-level closures (a–h). None change runtime; all change what the operator sees and types.

**New commands & flags:**
- `km env [--aws-profile]` — print exportable `KM_*` block; use with `eval $(km env)` for direct terragrunt invocation. Excludes `AWS_PROFILE` by default (operator-shell-local); `KM_ACCOUNTS_TERRAFORM` intentionally omitted (env-precedence asymmetry preserved per CONTEXT.md).
- `km bootstrap --all` — single command chains foundation (SCP/KMS/artifacts) → shared SES rule set; mutex with `--shared-ses`; `--plan` honors the 84.2 destroy-class gate.

**Configure-time changes (`km configure`):**
- HeadBucket-checked `state_bucket` with `[Y/edit/abort]` retry UX on globally-taken names (closure a).
- Auto-derived `artifacts_bucket = ${prefix}-artifacts-${account_id}`; angle-bracket and literal `km-artifacts-12345` placeholders rejected at load (closure e).
- `Next steps:` finale prints the canonical bootstrap sequence to stdout AND embeds it as `#` header comments at the top of the generated yaml (closure f).
- Shell-env conflict WARN per conflicting `KM_*` env var (closure h-shell).

**Bootstrap-time changes (`km bootstrap`):**
- Dry-run text now correctly says `would run: terragrunt apply` (closure b); degrades gracefully when AWS auto-detect is unreachable.
- Status banner WARNs on empty required account IDs and shows `(not set)` (closure h-banner).
- `--all` flag chains the two subflows (closure f).

**Init-time changes (`km init`):**
- Per-var drift WARN on env-vs-yaml mismatch (closure c) — see Partial-pass note below.
- `km init --plan` skips fresh-install dependents missing upstream `outputs.json` (closure d) — exit 0, re-runs cleanly once `network` is applied.
- Hard-fail on missing `artifacts_bucket` with recovery commands in error (closure f.6).

**Behavior change — `accounts.*` yaml-authoritative (closure h):**
- `accounts.organization`, `accounts.dns_parent`, `accounts.application`: yaml wins, env values do NOT override `cfg`. `KM_ACCOUNTS_*` still exported to terragrunt subprocesses.
- `accounts.terraform`: env wins (asymmetry preserved — operators retain shell-local override for the cross-account terraform role).

**Phase 84.3 UAT partial-pass — gap-closure pending (84.3.1):**
- `gap-drift-warn-viper-binding-masks-env-bound-keys`: drift WARN doesn't fire for `KM_REGION`/`KM_DOMAIN`/`KM_ARTIFACTS_BUCKET` etc. because viper binds env → cfg before the check runs. Works for yaml-authoritative keys.
- `gap-drift-warn-runBootstrap-missing`: `runBootstrap` (default `km bootstrap` without `--shared-ses`) doesn't invoke `ExportTerragruntEnvVars`; drift WARN only fires via `--shared-ses` path today.
- `gap-validate-artifacts-bucket-not-wired-into-load`: `validateArtifactsBucket` exists and is unit-tested but no command calls it during `config.Load()` — placeholder values silently pass through.
- `gap-init-hardfail-not-triggered-on-placeholder`: `km init --dry-run=true` succeeds against a placeholder bucket; hard-fail check needs to fire at config load, before any plan/apply.

See `OPERATOR-GUIDE.md` § Phase 84.3 wrapper-level UX for the full runbook (configure-time, bootstrap-time, init-time changes, `km env` use cases, cross-references to Phase 84.4).

Cross-reference: Phase 84.4 (module-level hard-coded `km-` prefix fixes) is required for full multi-install runtime parity — Phase 84.3 closures + Phase 84.4 module fixes together close the joint third-install scenario (i) in the 84.3 UAT.

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
