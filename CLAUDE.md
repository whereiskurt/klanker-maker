# Klanker Maker

This file serves as the Terragrunt repo root anchor for `find_in_parent_folders("CLAUDE.md")`.

## Project

Policy-driven sandbox platform. See `.planning/PROJECT.md` for details.

Multi-instance support: km supports multiple installs in a single AWS account via the `resource_prefix` knob in `km-config.yaml` (default `km`). `km configure` prompts for `resource_prefix` and `email_subdomain` (one-time choices propagated to terragrunt via `KM_RESOURCE_PREFIX` / `KM_EMAIL_SUBDOMAIN`). See `OPERATOR-GUIDE.md` § Multi-instance support and the `klanker:init` skill.

**Phase 92 (2026-05-31) — SandboxProfile spec restructure (complete):**
- `spec.identity:` → `spec.iam:` (with `allowedSecretPaths` declared).
- `spec.cli.notify*` → `spec.notification:` (typed `events` / `email` / `slack` sub-blocks).
- `spec.cli.vscodeEnabled` → `spec.runtime.vscode.enabled`.
- `spec.cli.{agent,claudeArgs,codexArgs}` → `spec.agent:` block (`default` / `claude.args` / `codex.args`). `spec.cli` is now `noBedrock`-only.
- Inlined `configFiles["/home/sandbox/.claude/settings.json"]` REMOVED everywhere; settings.json is synthesized from `spec.agent.claude.tools.*` + `trustedDirectories` (canonical `permissions.allow` / `permissions.deny`). Codex `config.toml` is synthesized too — Codex has no native tool gating, so it ships inert hooks + an asymmetry note. See `docs/agent-tool-gating.md`.
- Mixed mode (typed `agent.claude.tools.*` + inlined settings.json) is a hard `km validate` error.
- Sandbox-side env var names (`KM_NOTIFY_*`, `KM_SLACK_*`, `KM_AGENT`) are UNCHANGED; `apiVersion` stays `klankermaker.ai/v1alpha2`.
- Post-merge: `make build && km init --sidecars` to refresh the management Lambdas.

**Phase 102 (2026-06-08) — GitHub bridge agent verbs: /claude and /codex select the per-thread agent in a PR comment (complete):**
- An @-mention in a PR/issue comment may include `/claude` or `/codex` anywhere in the body (code-stripped, ≤1 agent verb per comment; two distinct verbs → error reply, no dispatch). The verb selects the agent for the turn and is persisted as `agent_type` in `km-github-threads`; subsequent turns in the same thread inherit it. GitHub analog of the Slack Phase 70 per-thread agent-verb (`/claude:` / `codex:`).
- **Precedence:** explicit verb > thread `agent_type` row > profile `spec.agent.default` (default: `claude`).
- **Codex precondition:** `/codex` routes to the sandbox; if the sandbox profile has no Codex installed, the poller posts a helpful error comment ("This sandbox's profile has no Codex; /codex is unavailable here.") and acks the queue message without dispatching.
- **Reserved tokens + km doctor:** `help`, `claude`, `codex` are reserved in `github.commands`. Defining a command entry with one of these names → `km doctor WARN`. Extended from "help-only" in Phase 99 to include `claude` and `codex` in Phase 102.
- **`/help` extension:** the built-in `/help` reply now prepends an "Available agents" block listing `/claude` and `/codex`, and appends "Current thread agent: `<type>`" when the thread has a stored `agent_type`.
- **Back-compat:** a comment with no agent verb is byte-identical to Phase 101 behavior. No new Terraform resources. No SandboxProfile schema change. `agent_type` is schema-on-write (DDB column added in Phase 102 Plan 02; no TF migration).
- **Deploy = `make build-lambdas` (clean) + `km init --dry-run=false` (NOT `--sidecars`)** — bridge + create-handler Lambdas updated together. Existing sandboxes need `km destroy && km create` to gain the new Phase 102 poller (D6 guard + `THREAD_AGENT_TYPE` env var). Bridge agent-verb parsing fires on the next webhook delivery without sandbox recreate.
- See `docs/github-bridge.md` § Phase 102 for the full operator runbook, Codex precondition, reserved tokens, and two-install/one-App UAT.

**Phase 101 (2026-06-08) — GitHub bridge orphan-repo helpful reply (complete):**
- When the shared bot is @-mentioned in a PR or issue comment on a repo **no install owns**, the front-door install posts ONE guidance comment naming `github.repos:` wiring and `km init`. Previously (Phase 100) the event was silently dropped (`github_relay_no_owner`); Phase 101 closes that gap with claim-aware scatter-gather — each relayed peer returns `{"claimed": bool}`; zero claims ⇒ true orphan ⇒ one comment. GitHub analog of the Slack Phase 96 default router (`docs/slack-notifications.md` § Phase 96).
- **Dormant by default.** Set `github.default_router: true` in `km-config.yaml` on the **front-door install only**. Absent or false → byte-identical to Phase 100 (no comment, no claim-gather overhead). `github.default_router` → `KM_GITHUB_DEFAULT_ROUTER`; dormancy = `"false"`.
- **Rollout-safe mixed fleet.** A peer still on Phase-100 code returns plain 200 (no body) — tallied as `claimed:true`. **No false "nobody owns this"** until peers upgrade. Upgrade peers in any order.
- **Per-(repo, number) cooldown** (3600s) via the nonces table (key `gh-router-cooldown:{owner}/{repo}#{number}`) — no new infrastructure.
- **No SandboxProfile schema change ⇒ no `km init --sidecars`, no sandbox recreate.**
- **Deploy = `make build-lambdas` (clean) + `km init --dry-run=false` (NOT `--sidecars`)** on the **front-door install**. `KM_GITHUB_DEFAULT_ROUTER` is an env-block change; `--sidecars` does not update the env block.
- See `docs/github-bridge.md` § Phase 101 for the full operator runbook + the two-install/one-App/unowned-repo UAT.

**Phase 100 (2026-06-08) — GitHub bridge federated relay: one GitHub App serving many installs (complete):**
- A single GitHub App can serve multiple `resource_prefix` installs. GitHub delivers every `issue_comment` webhook to one **front-door** install; its bridge runs `Resolve(owner/repo)` against its own `github.repos:` and on a **miss** relays the raw webhook verbatim (body + `X-Hub-Signature-256` + `X-GitHub-Event` + `X-GitHub-Delivery`, adding `X-KM-Relayed: 1`) to every peer in `github.peer_bridges`. The install whose `github.repos:` owns the repo processes it and posts the **single** 👀 (the front door reacts none). GitHub analog of the Slack Phase 95 relay, simplified to fire-and-forget (orphan-repo reply deferred to Phase 101).
- **Dormant by default.** Add `github.peer_bridges:` (list of *other* installs' GitHub bridge Function URLs, `km github status` → `bridge-url`) to `km-config.yaml`. Absent/empty → `KM_GITHUB_PEER_BRIDGES` empty, relayer nil → **byte-identical to Phase 97/98**. `km doctor` SKIPs the peer check silently.
- **`X-KM-Relayed: 1` is the entire single-hop loop guard.** A relayed request is terminal: process if owned, else drop (`github_relay_no_owner` log) — never re-relayed. Each install dedupes the forwarded `X-GitHub-Delivery` in its **own** nonces store; each peer re-verifies HMAC with its own copy of the same App webhook secret (GitHub sigs are timestamp-free → no skew window).
- **`Resolve()` reorder** (moved ahead of the thread-lookup + @-mention filter) is an unconditional scale fix — byte-identical dispatch, and it skips a wasted `LookupSandbox` DDB read per PR comment on unowned repos (the 700-repo fix). Federation off or on, dispatch outcomes are identical.
- **`km doctor` adds:** `GitHub peer bridges` (malformed URL / self-loop → WARN; empty → SKIP), mirroring `checkSlackPeerBridges`; own bridge-url from SSM `{prefix}config/github/bridge-url`.
- **Correctness invariant (documented, not enforced):** each repo owned by exactly one install across the fleet; two owners ⇒ double-processing.
- **Deploy = `make build-lambdas` (clean) + `km init --dry-run=false` (NOT `--sidecars`)** on each affected install: `KM_GITHUB_PEER_BRIDGES` is an env-block change that needs a full terragrunt apply (`--sidecars` rebuilds the zip + cold-starts but does NOT update the env block). The `lambda-github-bridge` module is edited **in place at `v1.1.0`** (additive `default=""` var, no version bump). **No SandboxProfile schema change ⇒ no sandbox recreate.**
- See `docs/github-bridge.md` § Phase 100 for the full operator runbook + the two-install/one-App E2E UAT.

**Phase 97 (2026-06-06) — GitHub comment-trigger bridge: km-github-bridge Lambda (complete):**
- When an allowlisted GitHub login @-mentions the bot in a PR comment, the km-github-bridge Lambda HMAC-verifies the webhook, dedupes by `X-GitHub-Delivery` GUID, resolves `owner/repo` → `{alias, profile, allow}` from `km-config.yaml github.repos:`, emits 👀 ACK, and dispatches to a per-repo sandbox (warm: FIFO enqueue; cold: EventBridge SandboxCreate).
- **Dormant by default.** Add `github.repos:` to `km-config.yaml` to activate. Absent → byte-identical to pre-Phase-97 behavior. `km doctor` skips the GitHub group silently when unconfigured.
- New CLI: `km github init` (cache bot-login from GitHub App), `km github manifest` (generate App JSON), `km github status` (print SSM-backed config). New sandbox-side helper `km-github comment|review|pr-files`.
- New profile: `profiles/github-review.yaml` — lean 2h/20m-idle spot t3.medium with `notification.github.inbound.enabled: true` (provisions the per-sandbox github-inbound FIFO queue).
- Source-aware userdata poller drains github-inbound envelopes and dispatches Claude turns; agent posts PR reviews via `km-github review`.
- **Deploy:** `make build-lambdas` (clean) + `km init --dry-run=false` (new Lambda + EventBridge + env block, NOT `--sidecars`) + `km init --sidecars` (schema field) + `km github manifest` to update App scopes + `km github init` to cache bot-login. Existing sandboxes need `km destroy && km create` to gain the queue + poller.
- See `docs/github-bridge.md` for the full operator runbook, deploy sequence, and troubleshooting.

**Phase 96 (2026-06-05) — Slack default router: orphan-channel @-mention reply (complete):**
- When the shared bot is @-mentioned in a channel no install owns, the front-door install posts ONE threaded reply naming the `#sb-{alias}-{profile}` convention and listing running sandbox channels across all installs as `<#CID>` Slack mentions. Empty aggregate list → guidance-only variant.
- **Dormant by default.** Set `slack.default_router: true` in `km-config.yaml` on the **front-door install only**. Absent or false → byte-identical to Phase 95 (no reply, no extra calls).
- Mechanism: claim-aware scatter-gather (Phase 95 relay upgraded); zero claims = true orphan; any `claimed:true` from any peer → owner handled it, no reply. Legacy Phase-95 peer responses treated as `claimed:true` (rollout-safe mixed fleet).
- Cooldown: 3600 s per channel via the nonces table (`router-cooldown:{channel}` TTL key) — no new infrastructure.
- No new Slack OAuth scopes. No SandboxProfile schema change. No sandbox recreate needed.
- **Deploy:** `make build-lambdas` (clean) + `km init --dry-run=false` on **ALL installs** (NOT `--sidecars` — env-block + IAM require a full terragrunt apply). See `docs/slack-notifications.md` § Phase 96.
- Deferred: agentic self-serve create, non-member channels (`app_mention` + `chat:write.public`), DM fallback (`im:write`).
- See `docs/slack-notifications.md` § Phase 96 for the full operator guide, deploy instructions, and troubleshooting.

**Phase 93 (2026-06-02) — `km desktop` (KasmVNC remote browser/XFCE) + Ubuntu OS-aware bootstrap (complete):**
- `km desktop start|status <id>` — KasmVNC graphical session in the operator's local browser over an SSM port-forward (loopback `127.0.0.1:8444`, SSM-only, no public/SG change). Mirrors `km vscode`. New `spec.runtime.desktop` block (`enabled` default **false**, `mode: kiosk|full`, `browsers ⊆ {firefox,chromium,chrome,brave}`, `geometry`). Engine: KasmVNC. **Ubuntu 24.04/22.04 only** (`km validate` errors on desktop+non-Ubuntu AMI). See `docs/desktop.md` / `klanker:desktop` skill.
- **The EC2 userdata bootstrap is now OS-aware** (`pkg/compiler/userdata.go` + the >12KB stub in `compiler.go`): was Amazon-Linux-only; Ubuntu needs apt-over-HTTPS (SG allows only 443, not port 80), `ForceIPv4`, AWS-CLI install via python3 (no `unzip`), `ssh.service`, and `systemd-resolved` stopped for the eBPF resolver on `:53`. AL2023 path unchanged. Both `proxy` and `ebpf`/`both` enforcement verified on Ubuntu. Desktop install runs BEFORE network enforcement, so the `spec.network` allowlist does not gate it. Details: `.planning/phases/93-*/93-UAT.md` and [[project_ubuntu_userdata_constraints]].
- `km desktop start`/`km vscode start` now **auto-reconnect** the SSM port-forward on drop (Ctrl-C to quit), with a liveness probe that recycles a silently-hung plugin — KasmVNC/sshd survive server-side. `runReconnectingPortForward` in `internal/app/cmd/shell.go`.
- The unused `cmd/configui` web dashboard was **removed**.
- **Deploy:** desktop schema + OS-aware userdata are compiled by the create-handler Lambda, so for remote `km create` redeploy with `make build-lambdas` (clean) + `km init --dry-run=false`. The reconnect wrapper is operator-side (local binary only). Existing sandboxes need `km destroy && km create`.

## Where to look

| You want to… | Look at |
|---|---|
| Operator CLI tour | `klanker:user` skill |
| One-time platform setup, `km init`, multi-instance, Slack bootstrap | `klanker:init` skill |
| Send / receive email from inside a sandbox | `klanker:email` skill |
| Inject SOPS-encrypted secrets into a sandbox | `docs/sandbox-secrets.md` (Phase 89) |
| Post to Slack from inside a sandbox (incl. transcript streaming, inbound, attachments) | `klanker:slack` skill |
| Polite-bot mode, `KM_SLACK_MENTION_ONLY`, per-channel @-mention-only inbound | `docs/slack-notifications.md` § Phase 91 |
| Federated bridge relay — one Slack App across multiple km installs | `docs/slack-notifications.md` § Phase 95 |
| Default router: orphan-channel @-mention reply, `slack.default_router`, cooldown | `docs/slack-notifications.md` § Phase 96 |
| GitHub comment-trigger bridge — `@km-bot review this PR` → sandbox agent → PR review | `docs/github-bridge.md` (Phase 97) |
| GitHub bridge federated relay — one GitHub App across multiple km installs (`github.peer_bridges`) | `docs/github-bridge.md` § Phase 100 |
| GitHub bridge agent verbs — `/claude` / `/codex` per-thread agent select in PR comments | `docs/github-bridge.md` § Phase 102 |
| Ask the operator to do something via email | `klanker:operator` skill |
| Detect sandbox environment + verify tooling | `klanker:sandbox` skill |
| VS Code Remote-SSH operator workflow | `klanker:vscode` skill |
| Run a remote browser/desktop in a sandbox via km desktop | `klanker:desktop` skill |
| Cross-account k8s (IRSA) cluster onboarding | `klanker:cluster` skill |
| Non-obvious operational footguns (deploy surface, terragrunt, teardown, Ubuntu userdata) | `docs/operational-gotchas.md` |
| Full operator runbook | `OPERATOR-GUIDE.md` |
| Email protocol deep-dive (SES, IAM, signing) | `docs/multi-agent-email.md` |
| Slack runbook (full setup, troubleshooting) | `docs/slack-notifications.md` |
| VS Code runbook | `docs/vscode.md` |
| Remote browser/desktop runbook (KasmVNC, kiosk, full XFCE, AMI-bake) | `docs/desktop.md` (Phase 93) |
| Snapshot-backed EBS volumes in profiles | `OPERATOR-GUIDE.md` § additionalSnapshots |
| Codex parity, `spec.agent.default`, Slack prefix routing & agent switching | `docs/codex-parity.md` (Phase 70) |
| Structured Claude/Codex tool gating via `spec.agent:`, synthesizers, asymmetry note | `docs/agent-tool-gating.md` (Phase 92) |
| Cut a release (goreleaser + GH Actions, tag-driven) | `docs/release.md` |
| SOPS / SSM allowlist via `iam.allowedSecretPaths` | `docs/sandbox-secrets.md` (Phase 89, renamed `identity:`→`iam:` in Phase 92) |

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
- `km slack init` — bootstrap Slack integration (`--bot-token`, `--invite-email`, `--shared-channel`, `--signing-secret`, `--force`); also caches `bot_user_id` at `{prefix}/slack/bot-user-id` (Phase 91)
- `km slack test` — end-to-end smoke test through the bridge
- `km slack status` — print SSM-backed Slack config
- `km slack invite <email>` — invite an email to a Slack channel; auto-detects native vs Connect (`--channel`, `--external`, `--dry-run`)
- `km slack manifest` — render a deployment-specific Slack App manifest to stdout (`--app-name`)
- `km slack rotate-token --bot-token <new>` — rotate Slack bot token + cold-start the bridge
- `km slack rotate-signing-secret --signing-secret <new>` — rotate Slack App signing secret
- `km vscode start <sandbox-id>` — open SSM port-forward + ssh-config Host entry for VS Code Remote-SSH (`--local-port`)
- `km vscode status <sandbox-id>` — check sshd state + authorized_keys presence
- `km vscode rekey <sandbox-id>` — rotate per-sandbox keypair without `km destroy && km create` (`--force`, `--yes`)
- `km desktop start <sandbox-id>` — open SSM port-forward to KasmVNC graphical session; prints `https://localhost:8444/` + credential (`--local-port`)
- `km desktop status <sandbox-id>` — check KasmVNC unit state on the sandbox
- `km desktop rekey <sandbox-id>` — rotate the per-sandbox KasmVNC password on a running sandbox (no restart / no session interruption; `--force`, `--yes`)
- `km desktop restart <sandbox-id>` — force a server-side restart of the KasmVNC session (Xvnc + WM + browser, like logging out of XFCE and back in) for a frozen/wedged desktop or stuck input; drops the live session (`--yes`)
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
- `km doctor` — validate platform health (config, credentials, SES, Lambda, VPC, stale resources, AMIs, EBS, Slack inbound, presence daemon, etc.; `--all-regions`, `--backfill-tags`, `--ignore-prefix=<csv>` to treat sibling installs' cross-install resources as known)

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

## Profile spec — Phase 92 structural cleanup

- **apiVersion bumped `v1alpha1` → `v1alpha2` (STRICT).** Profiles must declare `apiVersion: klankermaker.ai/v1alpha2`; `v1alpha1` is now rejected by the schema. No backwards compatibility (zero running sandboxes at cutover).
- **`spec.identity:` → `spec.iam:`.** The IAM/session block moved out of the `identity:` namespace. `iam.{roleSessionDuration, allowedRegions, allowedSecretPaths}` are the surviving fields.
- **`identity.sessionPolicy` removed** without replacement (it was never read by any code path).
- **`iam.allowedSecretPaths`** (Phase 89 SOPS SSM allowlist) is now declared in the JSON schema (closes the Phase 89 schema drift).
- **Dead top-level `spec.agent:` block removed** (`maxConcurrentTasks`, `taskTimeout`, `allowedTools` — never read). A new `agent:` block with structured tool-gating semantics is re-introduced later in Phase 92 (Waves 4/5).
- **`spec.cli.notify*` → `spec.notification:` (Wave 3, 2026-05-31).** The 15 notify/Slack fields moved out of `spec.cli` into a structured `spec.notification:` block: `notification.events.{onPermission,onIdle,cooldownSeconds}`, `notification.email.{enabled,address}`, `notification.slack.{enabled,perSandbox,channelOverride,archiveOnDestroy}` plus `slack.inbound.{enabled,mentionOnly,reactAlways}`, `slack.transcript.enabled`, and `slack.invites.{emails,useConnect}`. Surviving `spec.cli` fields: `noBedrock`, `agent`, `claudeArgs`, `codexArgs`. **Sandbox-side env var names (`KM_NOTIFY_*`, `KM_SLACK_*`) are UNCHANGED** — only the YAML surface changed; userdata output is byte-identical.
- **`spec.cli.vscodeEnabled` → `spec.runtime.vscode.enabled` (Wave 3).** `IsVSCodeEnabled` now takes a `*RuntimeVSCodeSpec`.
- `scripts/validate-all-profiles.sh` is the single-source-of-truth gate that runs `km validate` over the 20-file profile inventory (local-only; exits non-zero on any failure).

## Releases

Tag-driven via goreleaser + GH Actions. The `VERSION` file is the dev-build counter (auto-bumped by every `make build`); git tags (`vX.Y.Z`) are the release identity.

**Artifacts produced per release:** four tarballs (`km_vX.Y.Z_{darwin,linux}_{amd64,arm64}.tar.gz`), each bundling `km` + `terraform` v1.9.8 + `terragrunt` v0.99.1 + `LICENSE` + `README.md` + `OPERATOR-GUIDE.md` + `THIRD-PARTY-LICENSES.txt`. Plus a SHA256 checksums file. Operators still provide `aws` CLI + `session-manager-plugin` themselves.

**Cut-a-release workflow:**

1. **Pre-flight:** verify `main` is green, GSD milestone is at a clean checkpoint, `CHANGELOG`-worthy commits use conventional-commit prefixes (`feat:`, `fix:`, `docs:` — goreleaser groups by these).
2. **Local sanity check (no tag):**
   ```bash
   goreleaser check
   goreleaser release --snapshot --clean
   ls dist/ && tar -tzf dist/km_v*_darwin_arm64.tar.gz
   ```
3. **Tag and push:**
   ```bash
   git tag vX.Y.Z              # or vX.Y.Z-rc1 for prerelease (auto-flagged)
   git push origin vX.Y.Z
   ```
4. **GH Actions runs `.github/workflows/release.yml`** → cuts a **Draft** release. Review assets, then publish manually from the GH UI.
5. **Post-release:** bump the klanker plugin version (`plugin.json` + `marketplace.json`) in lockstep if any skill content changed — clients cache the old version otherwise (see [[project_plugin_version_gates_cache]]).

**Pinned bundled-tool versions:** `terraform` 1.9.8, `terragrunt` 0.99.1. Bumping these is a one-line edit to `.goreleaser.yaml` `before.hooks` args + the cache-key in the workflow.

**Files:** `.goreleaser.yaml` (release config), `scripts/fetch-bundled-tools.sh` (per-platform tool fetcher, cached at `~/.cache/km-bundle/`), `.github/workflows/release.yml` (tag-triggered).

Full runbook + troubleshooting: `docs/release.md`.

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
- `spec.agent.default: codex` → adds `api.openai.com` (Phase 88; field re-homed from `spec.cli.agent` in Phase 92)

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
  # Phase 92: Claude/Codex tool gating + trustedDirectories are TYPED here, not
  # inlined as configFiles JSON. The compiler synthesizes ~/.claude/settings.json
  # (canonical permissions.allow/deny) + ~/.codex/config.toml. See docs/agent-tool-gating.md.
  agent:
    claude:
      trustedDirectories: [/home/sandbox, /workspace]
      tools:
        autoApprove: [Bash, Read, Write, Edit, Glob, Grep]
  execution:
    configFiles:
      # OTHER config files are still inlined here; only the Claude settings.json
      # key is forbidden (it is synthesized from spec.agent.claude).
      "/home/sandbox/.claude/plugins/known_marketplaces.json": |
        {"...": "..."}
  cli:
    noBedrock: true    # default to direct API for km shell / km agent run
```

- `spec.agent.claude.tools.*` / `trustedDirectories` — typed Claude tool gating; synthesized into `~/.claude/settings.json` (Phase 92). Inlining `configFiles["/home/sandbox/.claude/settings.json"]` alongside these is a hard validation error.
- `spec.execution.configFiles` — pre-seed OTHER tool config files (written after `initCommands`, owned by sandbox user)
- `spec.cli.noBedrock` — operator-side default; doesn't affect sandbox provisioning, only CLI behavior when connecting

### Agent: claude | codex (Phase 70; restructured Phase 92)

`spec.agent.default` selects the default agent for `km shell` / `km agent run` /
Slack inbound dispatch:

```yaml
spec:
  agent:
    default: codex  # or "claude"; default claude; absence ≡ claude
```

The compiler writes `KM_AGENT` to `/etc/profile.d/km-notify-env.sh` and
`/etc/km/notify.env`. It also synthesizes `~/.codex/config.toml` on every sandbox
regardless of value (via `synthesizeCodexConfig`, Phase 92) — Claude-default
sandboxes get an inert config (forward-compat for when Codex ships a
Claude-Code-style hook API).

Per-turn override via Slack: a message starting with `claude:` or `codex:`
selects the agent for that turn (case-insensitive, anchored at start, zero or
one space after colon). Inside an existing thread, naming the *other* agent
triggers an 8-step clean handoff to a new top-level message. See
`docs/codex-parity.md` for the full switch sequence.

**`km init --sidecars` is required** after this phase ships so management
Lambdas pick up the schema addition. Existing sandboxes don't pick up
`agent.default: codex` retroactively — `km destroy && km create`.

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
- `spec.notification.slack.invites.emails: []string` — profile field that auto-invites ADDITIONAL
  people (beyond the always-invited primary operator) to the per-sandbox `#sb-{id}` channel after
  `km create` succeeds. Auto-detects native vs Connect.
- `spec.notification.slack.invites.useConnect: *bool` (default true) — gates the Connect fallback for the
  `notification.slack.invites.emails` loop only. True: external addresses auto-Connected. False:
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

### Phase 91: Polite-bot — @-mention-only inbound mode

Introduces polite-bot mode so the bridge does not react to every message in shared team
channels. The effective behaviour is determined by the channel mode + optional profile override:

| Mode | Channel | Default |
|------|---------|---------|
| 1 | Shared (e.g. `#km-notifications`) | mention-only |
| 2 | Per-sandbox `#sb-{id}` | every-message (back-compat) |
| 3 | Operator override (`notification.slack.channelOverride`) | mention-only |

- `spec.notification.slack.inbound.mentionOnly: *bool` — per-profile tri-state override: nil = mode
  default, `true` = force polite, `false` = force chatty. Omit for smart per-mode behaviour.
- `KM_SLACK_MENTION_ONLY` — install-level Lambda env var (`"true"`/`"false"`; default `"false"`).
  **Phase 91.1:** populated from `km-config.yaml` key `slack.mention_only` automatically by
  `km init`. No `export` required; env-wins drift WARN preserved.
- `KM_SLACK_BOT_USER_ID` — bot user ID for the mention scan. **Phase 91.1:** auto-read from
  SSM `{prefix}slack/bot-user-id` by `km init` (populated by `km slack init`); no `export`
  required. First-install skips silently with `[info]` line.
- **`km doctor` adds:** `slack_bot_user_id_cached` (WARN if missing when mention-only is active).
- **Phase 91.3 (km v0.3.772+):** thread-bypass for the mention scan. Once the bot dispatches
  a turn in a thread (sandbox_id is written to km-slack-threads at upsert time), every
  subsequent reply in that thread bypasses the mention requirement. Threads are 1:1
  conversations with the bot — re-@-mentioning was unnatural. Top-level messages and
  replies in unknown threads still require mention.
- **Phase 91.4 (km v0.3.773+):** first-only reactor toggle. `slack.react_always: false`
  in km-config.yaml flips the install-level default so the bridge posts 👀 only on
  top-level engagement messages — thread replies dispatch silently. Profile field
  `notification.slack.inbound.reactAlways *bool` shipped for forward-compat with future
  per-sandbox routing; runtime behaviour today is install-level.
- **Phase 91.5 (km v0.3.776+):** per-sandbox `notification.slack.inbound.reactAlways` override.
  When the profile sets the field explicitly, `km create` writes `slack_react_always`
  to the sandbox's `km-sandboxes` row; the bridge's `FetchByChannel` reads it and the
  per-sandbox value wins over the install-level default at step 10. Profile field is
  now first-class — set it on individual profiles to deviate from `slack.react_always`.
  Top-level engagement messages still always 👀-react regardless (the signal can't be
  silenced).

**Why `km init` and not `km init --sidecars`:** the bridge Lambda's `environment.variables`
block is owned by the `lambda-slack-bridge` Terraform module, which only updates on full
terragrunt apply (`km init`). `--sidecars` rebuilds binaries and forces a Lambda cold-start
but does NOT update the env block — so flipping `slack.mention_only` requires
`km init --dry-run=false`.

Rollout after upgrading to a Phase 91.1 build:

```bash
make build
km slack init --force          # re-runs auth.test, caches bot_user_id at SSM
# Edit km-config.yaml — add:
#   slack:
#       mention_only: true
km init --dry-run=false        # km auto-reads slack.mention_only + SSM bot_user_id; terragrunt apply
km doctor
km destroy <sandbox-id> --remote --yes && km create <profile>   # existing sandboxes pick up new field
```

See `docs/slack-notifications.md` § Phase 91 for the full operator guide and troubleshooting.

### Phase 95: Federated bridge relay — one Slack App, many km installs

Adds opt-in static relay so a single Slack App registration serves multiple km installs.
Slack delivers all events to one "front door" install; that install broadcasts unknown-channel
events to peer bridges until the owning install processes them.

- `slack.peer_bridges: []string` — list of sibling install `/events` URLs in `km-config.yaml`.
  Absent or empty = federation off = byte-identical behaviour to pre-Phase-95.
- `KM_SLACK_PEER_BRIDGES` — comma-joined Lambda env var written by `km init`. Only updated on
  a full `km init --dry-run=false` (NOT `km init --sidecars` — env-block change requires full
  terragrunt apply).
- **Deploy sequence:** `make build-lambdas` (clean) then `km init --dry-run=false` on each
  install where `slack.peer_bridges` changed.
- **Loop guard:** `X-KM-Relayed: 1` header makes relayed requests terminal — never re-relayed.
- **`km doctor`:** `slack peer bridges` warns on malformed peer URL or self-loop (peer URL ==
  own bridge URL); skips when the list is absent/empty.
- **Correctness invariant:** channel name/alias uniqueness across all installs is required.
  Per-sandbox `#sb-{id}` channels are safe by construction; shared named channels must be
  registered on exactly one install.

See `docs/slack-notifications.md` § Phase 95 for the full operator guide, setup flow, and troubleshooting.
