# Klanker Maker

This file serves as the Terragrunt repo root anchor for `find_in_parent_folders("CLAUDE.md")`.

## Project

Policy-driven sandbox platform. See `.planning/PROJECT.md` for details.

Multi-instance support: km supports multiple installs in a single AWS account via the `resource_prefix` knob in `km-config.yaml` (default `km`). `km configure` prompts for `resource_prefix` and `email_subdomain` (one-time choices propagated to terragrunt via `KM_RESOURCE_PREFIX` / `KM_EMAIL_SUBDOMAIN`). See `OPERATOR-GUIDE.md` § Multi-instance support and the `klanker:init` skill.

**Wake-up re-credential + `km create` positional alias (2026-08-26):**
- **`km resume` now forces a GitHub installation-token re-mint** instead of waiting up to 45
  minutes for the per-sandbox refresher's next tick. `awspkg.ForceGitHubTokenRefresh` reads the
  refresher's own EventBridge schedule (`{prefix}-github-token-{id}`), takes its `Target.Input`
  **verbatim**, and hands it to `{prefix}-github-token-refresher-{id}` via a synchronous
  `RequestResponse` invoke. Wired into BOTH resume paths: `runResume` (local) and the ttl-handler's
  `handleResume` (`km resume --remote`, `km at … resume`).
- **Reading the payload back off the schedule is load-bearing, not convenience.** It carries
  `installation_id`, the per-sandbox KMS key ARN, `allowed_repos` and the *compiled* permissions
  map — all decided at create time. Rebuilding it here is exactly the duplication that (per
  `pkg/github/token.go` `TokenRefreshEvent`) kept this feature silently broken for its entire
  lifetime once already.
- **The real value is surfacing, not re-minting.** The 45-minute schedule is never stopped by
  pause/stop — only destroy removes it — so a healthy install already has a ≤45-min-old token at
  wake-up, and the on-box helpers (`km-git-askpass`, `km-git-credential-helper`, `km-github`) read
  SSM at call time and never cache. The failure mode this catches is a refresher that has been
  erroring on **every** tick: today those errors reach only CloudWatch and the operator meets them
  later as a git 401. The synchronous invoke turns that into an error printed at the moment the box
  is woken, naming the log group.
- **Best-effort by construction.** Both call sites are non-fatal (the instance is already starting;
  failing the command afterwards would leave a running box and a red exit code) and bounded at 30s.
  A sandbox with no `sourceAccess.github` has no schedule — `ErrNoGitHubRefresher` is **silent**,
  not a warning, so the line that matters doesn't get trained out of operators.
- **Known gap (deliberate, not an oversight):** the Phase 109/114 bridge auto-resume path calls
  `EC2Resumer.StartSandbox` directly in four packages and does NOT go through `handleResume`, so a
  Slack/GitHub @-mention that wakes a box is not covered yet. Deferred until the surfaced errors
  show whether it's needed.
- **`km create <profile.yaml> [alias]`** — the alias may now be a second positional instead of
  `--alias`. Whichever argument ends in `.yaml`/`.yml` is the profile, so order is irrelevant.
  Ambiguity is always an error, never a guess: two YAML-looking args, zero YAML-looking args, or a
  positional alias combined with `--alias` all fail with a message naming the conflict — silent
  precedence between two alias sources would create the sandbox under a name nobody typed. The
  one-positional form is deliberately extension-agnostic so every path shape that worked before
  still works.
- **Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`.** NOT `--sidecars`:
  the ttl-handler role gains a new `GitHubTokenRefreshOnResume` statement
  (`lambda:InvokeFunction` on `{prefix}-github-token-refresher-*`), which needs a full terragrunt
  apply. The matching `scheduler:GetSchedule` was already granted by `SchedulerCleanup`, and the
  operator policy already holds `lambda:*` + `scheduler:GetSchedule` on `{prefix}-*`, so local
  `km resume` needs no IAM change. No SandboxProfile schema change, no userdata change, **no
  sandbox recreate** — this is entirely control-plane.

**Phase 129 (2026-08-26) — Declarative MITM intercepts: profile-declared host→action rules replace the hardcoded rickroll (complete):**
- `spec.network.mitm.intercepts` replaces the compiled-in, unconditional `google.com` → Rickroll
  easter egg (`sidecars/http-proxy/httpproxy/proxy.go`, deleted) with a declarative list of named
  host→action rules an operator authors: `redirect` (301 + `Location`) or `respond` (canned
  status/body/contentType). **Opt-in, off by default** — this is the one place the repo departs
  from its usual "dormant ⇒ byte-identical" rule; a profile with no `mitm:` block gets zero
  interception, and the Rickroll stops firing fleet-wide on the next `km create`. Accepted and
  deliberate. `apiVersion` stays unbumped (purely additive).
- **Migration path:** the egg survives as `profiles/base/mitm-rickroll.yaml`, an abstract fragment
  any profile opts into with one `extends:` line. `profiles/mitm-demo.yaml` is the worked
  example — it extends the fragment, adds its own `respond` rule, and shows the off-switch
  (`- name: rickroll` / `enabled: false`) commented out.
- **Precedence (unconditional, cannot be overridden by a profile):** deny gate →
  Bedrock/Anthropic/OpenAI metering + GitHub repo filter → operator intercepts → general
  allowlist. An intercept naming a metering/GitHub host is silently dead (`km validate` WARNs);
  an intercept for a host absent from the allowlist still fires — the useful case for a canned
  error on a host the sandbox can't otherwise reach. No `block` action: `deniedHosts` already
  blocks with strictly stronger semantics (ahead of everything, broader subdomain match,
  runtime-appendable via `km-netpolicy`) — a second, weaker way to block would be a footgun.
- **Inheritance resolves by name, last-wins, whole-entry** (not a field merge) — Phase 117 unions
  lists, so without whole-entry override a leaf could never turn an inherited rule off.
- **Matcher narrowing is a bug fix.** The deleted regex (`^(www\.)?google\.com`) was unanchored at
  the end, so `google.com.evil.example` was also Rickrolled. The new matcher reuses
  `IsHostAllowed` semantics (case-insensitive, port-stripped, leading-dot subdomain match, no
  regex, no `*`) and rejects the lookalike; pinned by a unit test.
- **Transport is base64 JSON** in a conditional `/etc/systemd/system/km-http-proxy.service.d/mitm.conf`
  drop-in (`KM_MITM_INTERCEPTS`) — load-bearing, since an unquoted systemd `Environment=` splits on
  whitespace and a `respond.body` has spaces. The sidecar fails toward **zero intercepts** on a
  decode/parse error, never a partial rule set. Under `enforcement: ebpf`/`both`, intercept hosts
  are threaded into `--proxy-hosts` but DNS is deliberately NOT widened — a host outside
  `allowedDNSSuffixes` never resolves and the rule silently never fires (`km validate` warns).
- **Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`.** All three, because
  three things land together: the sidecar binary, the profile schema field the create-handler's
  bundled `toolchain/km` validates, and the userdata drop-in the create-handler zip renders.
  **`km init --sidecars` alone is actively harmful mid-rollout** — the new sidecar binary has no
  rule set to read yet, so a sidecar-only refresh drops the Rickroll on a live box while the
  profile still cannot declare it back until the full sequence completes. No Terraform module,
  DynamoDB table, IAM policy, Lambda bridge, or SES resource changes; userdata goldens are
  untouched in the dormant case. Existing sandboxes keep today's behaviour until
  `km destroy && km create`.
- See `docs/mitm-intercepts.md` for the full operator runbook and
  `docs/superpowers/specs/2026-08-25-mitm-intercepts-design.md` for the design.

**Phase 128 (2026-08-26) — Wiz Runtime Sensor on EC2 sandboxes + `iam.allowedSecretPaths` made real (complete; live UAT pending):**
- Opt-in `profiles/base/security/wiz.yaml` fragment installs the **Wiz Runtime Sensor** as a root
  systemd daemon, so an autonomous agent's process/file/network activity is visible in the
  operator's Wiz tenant. **Dormant by default** — a profile that does not `extends:` it is
  byte-identical to Phase 127. Composes via Phase 117 inheritance; lists union.
- **The enabling fix: `iam.allowedSecretPaths` never worked on EC2.** It rendered an
  `aws ssm get-parameter` loop into userdata (`userdata.go:495`) but was NEVER plumbed into the
  ec2spot role — which granted `ssm:GetParameter` on two hardcoded resources only. **It does not
  degrade: the bootstrap runs under `set -euo pipefail` (`userdata.go:36`) and the fetch is a
  standalone assignment, so a denied call ABORTS THE BOOT** before the `if [ $? -eq 0 ]` guard,
  which is dead code on the failure path. `profiles/base/gpu/serve.yaml:214` had already recorded
  this empirically. Two shipped profiles (`gpu-llama-{12x,48x}`) declared a path no module version
  ever granted; both are stripped.
- **`{{prefix}}` token is a SECURITY GUARD, not ergonomics.** Every `allowedSecretPaths` entry must
  start with `{{prefix}}/`; anything else is a hard `km validate` error. Requiring prefix-relative
  paths makes it structurally impossible for a profile to grant the sandbox role SSM access outside
  its own install's namespace. Validation is **shape-only** (works with no configured install);
  expansion happens at compile time and **fails loud** — an unset `KM_RESOURCE_PREFIX` errors rather
  than defaulting to `km`, which on a non-default install would render IAM against a SIBLING
  install's namespace. **`cmd/create-handler` never calls `profile.Validate`**, so on the remote
  path the compiler-side `HasPrefix` guard in `InterpolateSecretPaths` is the ONLY barrier — it is
  deliberately exactly as strict as the validator.
- **`infra/modules/ec2spot/v1.4.0`** (new immutable dir): additive `secret_paths` var (`list(string)`,
  default `[]`) + a count-gated `aws_iam_role_policy` granting `ssm:GetParameter` on exactly those
  paths. Empty list creates NO resource. Pin bumped in `locals.substrate_module_versions`. The
  compiler emits `secret_paths` into `module_inputs` **only when non-empty**.
- **DNS is the exception to the root-bypass rule — the trap that nearly shipped.** Root bypasses the
  iptables DNAT (`userdata.go:4532`, uid-0 RETURN) and the eBPF cgroup, so the fragment needs no
  `allowedHosts` — **but DNS is neither uid- nor cgroup-scoped.** `userdata.go:4737` rewrites
  `/etc/resolv.conf` to `127.0.0.1` and the resolver NXDOMAINs non-allowlisted names for everyone,
  root included. The fragment therefore declares `allowedDNSSuffixes: [".wiz.io"]`. Symptom when
  missing is **misleading**: `km-init.sh` is `set -e`, so a blocked lookup aborts the remaining
  initCommands and userdata reports `No init script found in S3 (skipped)`.
- **Credential naming is load-bearing, not cosmetic.** userdata derives an env var as
  `basename | upper | '-'→'_'`, so `{{prefix}}/wiz/wiz-api-client-id` yields exactly
  `WIZ_API_CLIENT_ID` — the name Wiz's installer reads. No glue between fetch and install, and the
  credential never lands in `/etc/sandbox-secrets.env` (`0440 root:sandbox`), which the monitored
  agent could read. Use a **`SENSOR`**-type Wiz service account, not a general API client.
- **NOT tamper-proof, deliberately.** On `privileged: true` the agent has sudo and can stop, mask, or
  `chattr -i` anything. L0 hardening is a systemd drop-in (`Restart=always`) plus `chattr +i` — the
  installer owns `wiz-sensor.service`, so km adds an override, which also survives package upgrades.
  **The real control is `privileged: false`**; detection belongs off-box in the Wiz tenant.
- **Three Wiz integrations now exist and point in three directions** — km pulls (`checks.triggers`,
  Phase 116), Wiz pushes (`webhooks:`, Phase 127), and a sandbox reports to Wiz (this phase). They
  share no code or config. See `docs/wiz-sensor.md` § 7.
- **Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`** (NOT `--sidecars` — no
  sidecar binary changed, and the new IAM statement needs a full terragrunt apply). Existing
  sandboxes keep the old role until `km destroy && km create`. **Prerequisite ordering is
  load-bearing:** create the two SSM parameters BEFORE any profile extends the fragment, or every
  sandbox using it fails to boot.
- **Residual risk (accepted):** live UAT is not covered — above all Wiz-sensor/km-eBPF coexistence
  (km loads cgroup BPF + SSL uprobes; the sensor loads its own kprobes), which no code review
  settles. Two tenant unknowns remain open: `WIZ_DOMAIN`/region, and the connector's inventory sync
  cadence, which bounds whether a short-lived sandbox is ever tag-correlated.
- See `docs/wiz-sensor.md` for the operator runbook and
  `docs/superpowers/specs/2026-08-25-wiz-sensor-design.md` for the design.

**Phase 127 (2026-08-25) — Generic webhook ingress bridge, Wiz first source (complete):**
- A third-party SaaS POSTs a payload to a new km-owned Lambda (`km-webhook-bridge`,
  Function URL), which authenticates it, drops replays, matches operator-declared rules, and
  dispatches a prompt to a named sandbox alias — warm via a per-sandbox SQS FIFO queue, cold via
  EventBridge `SandboxCreate`. First source: **Wiz** — an Automation Rule fires on an Issue or
  Threat and the `ir-bot` sandbox runs `/triage` against the payload. This is the **push**
  counterpart to the existing Phase 116 **pull** ingress (`checks.triggers`); the two share the
  identical `alias`/`profile`/`on_absent`/`cooldown_seconds`/`prompt` vocabulary by design, so an
  operator learns one dialect for both directions.
- **Dormant by default.** Absent `webhooks:` in `km-config.yaml` ⇒ `KM_WEBHOOK_SOURCES` unset ⇒
  the bridge drops everything and `km doctor` skips the whole group with zero AWS calls.
- **No CLI verb, by explicit decision.** Configuration is `km-config.yaml` only — the opposite of
  `km slack`/`km github`/`km h1`, which exist because of App manifests, OAuth, and token rotation.
  A webhook has a URL and a shared secret; neither needs a verb.
- **Wiz payloads are ours to define, not Wiz's.** Wiz Automation Rules author their own request
  body as an operator-editable Mustache template, so km publishes a canonical
  `docs/webhook-templates/wiz.{issue,threat}.v1.json` contract for the operator to paste in,
  rather than reverse-engineering a fixed Wiz schema. A wrong leaf variable is fixed by editing
  the pasted template against Wiz's own variable picker, never by a Go patch.
- **Wiz offers no HMAC/signature and no delivery-id header** — confirmed against several
  third-party integration guides. The template-injected
  `"delivery_key": "{{issue.id}}:{{triggerType}}:{{issue.updatedAt}}"` is the deliberate
  replacement for a missing delivery id; the bearer token minted into SSM is therefore the
  **entire** auth boundary. Wiz also offers a client-certificate option that is unusable here — a
  Lambda Function URL cannot terminate mTLS — and the operator has confirmed mTLS is never
  required, so no ALB/API Gateway fronting is planned.
- **Four storm-control layers, cheapest first:** replay nonce (keyed on `delivery_key`) → cooldown
  (`group_by`, the coalescing lever — first alert of a burst wins, the rest suppressed for the
  window; this is dedup-by-grouping, NOT true batching, which stays out of scope) → an
  install-wide rate ceiling shared across every configured source (counted only AFTER replay and
  cooldown, so suppressed duplicates cost nothing) → the Phase 121 per-sandbox action-quota gate
  (`onBreach: freeze`). The first three fail OPEN on a store error (never strand a real alert on a
  transient DynamoDB blip); the quota gate is the one layer that actually stops runaway spend when
  it trips, and it is **warm-path only** — a cold-create dispatch has no sandbox yet to have a
  usage history against, so it is quota-ungated by construction, not an oversight.
- **The `{{...}}` non-collision:** Wiz renders its own Mustache variables before sending; km then
  expands its own `{{severity}}`/`{{entity.name}}`/`{{raw}}` syntax against the already-rendered
  envelope when building the agent prompt. The two never see each other's raw tokens despite
  looking identical — worth stating explicitly, or it costs someone an hour of confused staring.
- **No threads table, no reply-back leg, no `km-webhook` sandbox-side helper** — unlike GitHub/H1,
  a webhook source is one-way by design (confirmed with the operator, 2026-08-25): nothing to
  reply to, no conversation to persist. The agent reports out via `km-slack` instead if needed.
- **`km init` mints the bearer token once and never overwrites it.** The absence check is narrow
  (only a genuine "parameter not found" counts as safe-to-mint; any other SSM read error is
  surfaced, never treated as absence) so a transient read failure can't silently overwrite a live
  token out from under a configured Wiz integration. Deliberate rotation is an explicit
  `aws ssm put-parameter --overwrite` plus a Wiz-side paste — see `docs/webhook-ingress.md` § Token
  rotation.
- **Deploy surface, order load-bearing:** `make build` FIRST (the binary carries the new
  `regionalModules()` entries — `lambda-webhook-bridge` plus a `webhook-inbound` variant of the
  shared inbound DLQ; a stale binary silently skips them, `km doctor` stays green while nothing was
  provisioned) → `make build-lambdas` (new bridge zip, and refreshes the create-handler for the new
  `notification.webhook.inbound` profile field + poller userdata) → `km init --dry-run=false`
  (**not** `--sidecars` — Lambda/Function URL/IAM/DLQ/env block all need a full terragrunt apply; a
  scoped `km init --webhooks` sugar alias covers a fast env+IAM-only refresh once the module
  already exists). Existing `ir-bot` needs **one** `km destroy && km create` to gain the
  per-sandbox queue and the `km-webhook-inbound-poller`.
- **Reused, not rebuilt:** the shared nonces table + `CheckAndStore`, the `alias-index` GSI, the
  Phase 99.1 shared inbound DLQ pattern, the EventBridge cold-create path, `WireActionQuota`, and
  the Phase 109 self-heal (a terminal `ErrNoResumableInstance` deletes the stale DynamoDB row and
  falls through to cold-create, so an `ir-bot` terminated out from under km doesn't shadow creation
  forever).
- See `docs/webhook-ingress.md` for the full operator runbook (Wiz setup, Automation Rule wiring,
  the full `webhooks:` config surface, storm control, deploy surface, token rotation,
  troubleshooting, and known limits).

**Sandbox liveness: VNC + SSH presence signals (2026-08-23):**
- `km-presence` gained **signal 6 (KasmVNC viewer attached)** and **signal 7 (SSH session
  established)**, closing a gap where an interactively-used sandbox was reaped by
  `spec.lifecycle.idleTimeout` while someone was working in it. Both were live-verified on a
  desktop sandbox; the daemon now ORs seven signals instead of five.
- **Root cause is utmp, not the timer.** Signal 1 reads `who`, and `sshd` writes a utmp record
  ONLY for PTY sessions. KasmVNC runs as a systemd service (never a login session), and VS Code
  Remote-SSH runs `vscode-server` over a **non-PTY** exec channel (`sshd: user@notty`). Measured
  live: non-PTY session ⇒ `who=0`, PTY session ⇒ `who=1`. So a connected user looked idle on all
  five original signals, with an **empty CloudWatch audit stream** as the only trace (the daemon
  emits a heartbeat only when a signal is active — an empty stream is proof of zero signals, not
  of a broken pipe).
- **The VS Code case was INTERMITTENT**, which is what made it hard to see: opening VS Code's
  integrated terminal allocates a PTY and makes the same session visible to signal 1. Whether a
  box survived depended on an unrelated UI panel being open.
- **Both signals detect the live socket, never the process.** `pgrep vscode-server` is the
  obvious check and is WRONG — VS Code deliberately leaves that server running after a client
  disconnects so reconnects are fast, so it would latch the sandbox awake forever. Likewise VNC
  is matched by owning process (`Xvnc`), NOT by port: the profile sets
  `network.websocket_port: auto`, so 8444 is observed behaviour, not a contract.
- **Both fail idle** (a missing/erroring `ss` reports no session). Deliberate: a reaped desktop
  costs one `km resume`, whereas a signal that can never go negative silently disables idle
  teardown fleet-wide and leaks instances. Every negative path is unit-tested for this reason.
- **Signal 7 matches BOTH `(("sshd",` and `(("sshd-session",`** — OpenSSH 9.8+ split the session
  process out and renamed it. Matching only the first would silently stop firing on an AMI bump.
  The box runs 9.6 today, so the second form is forward-compat and covered by a test.
- Signals 6 and 7 are kept **independent** (each runs its own `ss`; ~4ms/60s tick) so a future
  `km doctor` can report WHICH session type is holding a sandbox awake. A cross-independence test
  pins that they never trigger each other.
- **Consequence:** an open browser tab or VS Code window now pins a sandbox alive indefinitely —
  same tradeoff as leaving a `km shell` open. `spec.lifecycle.ttl` is unaffected and is now the
  real backstop against forgotten sessions.
- **Pure `cmd/km-presence` sidecar change.** No SandboxProfile schema change, no TF/DDB/Lambda/IAM
  change, no userdata change (the km-presence.service unit already runs `User=root`, which signal
  6/7 need for `ss -p` to resolve process names).
- **Deploy = `make build` + `km init --sidecars`** (rebuilds + uploads the `km-presence` sidecar;
  NOT `--dry-run=false`). Existing sandboxes keep the five-signal daemon until
  `km destroy && km create` — the binary is fetched at boot. Interim mitigation on a live box:
  raise `idleTimeout`, `km extend <id> --idle <dur>`, or keep a `km shell` open.
- See `docs/desktop.md` § Idle timeout and desktop sessions and `docs/vscode.md` § Idle timeout
  and Remote-SSH sessions.

**Phase 126 (2026-08-22) — Cross-account capacity borrowing: launch sandboxes into a linked capacity account (live-verified 2026-08-24, except vLLM serving):**
- A SandboxProfile can launch its EC2 box into a *different*, pre-linked AWS account to
  borrow that account's vCPU quota, while the one km control plane — state, DynamoDB, budget,
  `km list`, every bridge — stays home. Dormant by default: absent `spec.runtime.launchAccount`
  ⇒ byte-identical to Phase 125. No `apiVersion` bump. Motivating fact: a GPU vCPU quota
  (`L-DB2E81BA`) can be far larger in a second account than in the account km actually runs
  in. Nothing about the mechanism is GPU-specific — the launcher role's `--instance-types`
  allowlist is the only thing that scopes a given link to GPU families.
- **Enrollment is two commands, two credential sets.** `km account add <name> --aws-profile
  <target-admin> --trust <home-account-id> --instance-types ... [--provision-network
  --az-count N] [--dry-run=false]` runs real Terraform against the TARGET account (its own
  admin creds), provisioning a bounded launcher role (trusted by three home-side principals —
  operator/local-create, `*-create-handler`, `*-ttl-handler` — each `sts:ExternalId`-gated), a
  permissions-boundaried box role, a results bucket, and optionally one subnet **per AZ**
  (never a single subnet — that collapses the Phase 124 AZ sweep to one attempt) + EFS. It
  writes an owner-only handoff fragment to `~/.km/account-links/<name>.link.yaml`. `km account
  register <name> --from-fragment <path> --aws-profile <home>` then runs with HOME creds:
  writes the `km-config.yaml` link entry, the external id as an SSM SecureString, and exactly
  one read-only (`s3:GetObject`/`s3:ListBucket`, never `PutObject`) grant on the home
  artifacts bucket for the box role. `km account list` / `km account rm [--target-aws-profile]
  [--purge-backend]` round it out.
- **The enrollment unit's Terraform state lives IN THE TARGET ACCOUNT** — `km account add`
  creates an S3 bucket (`tf-{prefix}-linkstate-{target-acct}-{regionLabel}`) + DynamoDB lock
  table via the AWS API before the first terragrunt init, shared per (prefix, account, region)
  and keyed per link. There is no local Terraform state anywhere in this project.
- **The load-bearing terragrunt fix:** the sandbox template's `include "root"` needed
  `merge_strategy = "deep"` — without it, a child `generate "provider"` block hard-errors
  every sandbox render (`Detected generate blocks with the same name: [provider]`), not just
  cross-account ones. The child block reproduces root's full generated output (incl. both
  `required_providers` pins, which deep-merge would otherwise drop) plus a conditionally
  interpolated `assume_role` stanza, proven byte-identical to root's own output in the
  dormant case against the real pinned terragrunt v0.99.1 binary.
- **A genuine apply-time bug found and fixed along the way:** the link record carries subnet
  ids but no VPC id anywhere. Without resolving it, the EC2 module would self-provision a
  disconnected VPC and then try to place the ENI in a subnet from the link's real, different
  VPC — a hard `terraform apply` failure. Fixed via one `DescribeSubnets` call against the
  assumed-role config, resolved once per launch before any artifact is compiled.
  **Known residual, non-blocking gap:** the link's pre-provisioned `SecurityGroupID` goes
  unused — the EC2 module always self-creates its own per-sandbox SG regardless of account,
  matching home-launch behavior (there is no "reuse an existing SG" input anywhere today).
- **The capacity/quota gate reads the LINKED account**, via a second, fail-closed assumed-role
  AWS config — a failed assume aborts the create/report rather than silently falling back to
  the home account's headroom. The `{prefix}-capacity` DynamoDB table stays in the home
  account for every launch; only the row key is namespaced by account id for a linked launch
  (home stays permanently un-namespaced — success rows carry no TTL, so "namespacing home for
  symmetry" would silently orphan months of sticky-AZ history).
- **Both teardown paths are account-aware.** `km destroy` (including its cold-clone fallback)
  reads the sandbox's DynamoDB row for its `launch_account` BEFORE the home-account tag scan,
  which can never see a resource in a different account. The expiry Lambda renders a
  conditional `assume_role` provider block sourced from the link's own region (never its own
  cold-start env vars, which are Lambda-instance-wide). Both fail closed — hard error — on an
  unknown link or a failed external-id read, never a silent fall-through to a home-account
  provider that would report success while the linked instance kept billing.
- **`km doctor` gains four read-only checks**, all silently skipped with zero AWS calls when
  no `launch_accounts` are configured: link structurally well-formed; launcher role actually
  assumable (forces real credential resolution, not just config construction); artifacts
  grant present and not widened beyond the exact read-only pair; no orphaned linked-account
  EC2 instances (a leaked box `km list` cannot see, billing silently).
- **Security model:** the target account is exempt from Service Control Policies, so the
  launcher role's own IAM policy is the ENTIRE containment boundary — no second layer. The
  external id is the confused-deputy protection. The box role is capped by a permissions
  boundary. Every cross-account data path is read-only in both directions (box→home artifacts:
  read-only; home→box results: read-only; box→its own results bucket: read-write, same
  account only). **Operator-safety note:** enrolling an org **management** account as the
  target is possible but discouraged — SCPs never apply there, so this feature's usual "SCP is
  a second layer" assumption doesn't hold; prefer a dedicated member account.
- **Deploy surface (three independent paths — do not conflate):** `km account add/register/
  list/rm` + the profile field + local create-flow wiring are pure operator-binary changes
  (`make build`). `spec.runtime.launchAccount` reaching a **remote** `km create` additionally
  needs `make build-lambdas` + `km init --dry-run=false` (the create-handler's bundled
  `toolchain/km` must be refreshed). The expiry Lambda's cross-account teardown path
  (`KM_LAUNCH_ACCOUNTS` env + the `sts:AssumeRole`/`ssm:GetParameter`+`kms:Decrypt` IAM
  grants) needs the same `make build-lambdas` + `km init --dry-run=false`. The target
  account's own roles/network/bucket are provisioned by `km account add` running terraform
  directly against that account — **never** by `km init`, which never touches the target
  account at all (same standalone shape as `km cluster add`). `--sidecars` is not sufficient
  for any of the above — nothing in this phase touches a sandbox-side helper binary.
- **Live UAT 2026-08-24 (`126-UAT.md`):** cross-account create AND teardown proven; `km capacity`
  reads the target (768 vCPU vs 64 home); `km doctor`'s four checks green incl. the artifacts
  grant surviving `km init`; containment matrix 8/9 (Finding L1: `ec2:RunInstances` is not
  tag-gated, so an untagged box is un-reapable AND invisible to the orphan check — **mitigated
  2026-08-25 by detection**, because tag-gating it is IMPOSSIBLE: for the `instance/*` resource
  of a `RunInstances` call AWS populates only `ec2:AvailabilityZone` and `ec2:InstanceType`, so
  an `aws:RequestTag` condition there is unsatisfiable and breaks every launch — see the
  `LaunchOnlyGpuBoxes` comment and commit `0d362066`. **`simulate-principal-policy` will
  evaluate a key AWS never populates at call time, so a simulator verdict proves what the
  policy SAYS, not what AWS can ENFORCE.**). **vLLM serving
  a token is still unproven** — every allowlisted GPU shape probed `InsufficientInstanceCapacity`
  in every us-east-1 AZ (Finding L4), incl. the FP8 single-GPU fallback; ca-central-1 has no L40S
  at all and its L4s are dry; us-west-2/us-east-2 are quota-walled at 0 (requests pending).
- **Two Phase 124 defects found here, not Phase 126 bugs — they bite home launches too:**
  **L3 (root cause) — FIXED 2026-08-25** `rankScore` (`pkg/capacity/rankaz.go`) checked
  `LastSuccessAt` before the fresh-ICE branch and returned unconditionally; success rows carry no
  TTL, so an AZ that ever succeeded out-ranked every other AZ forever however often it later ICEd
  — and `km capacity` disagreed with the ranker, showing the AZ as `recently-dry` while the launch
  path still picked it. **Freshness is now evaluated before stickiness** (an EXPIRED ICE still
  lets stickiness apply), `ComputeCapacityVerdict` carries a cross-reference so the two cannot
  drift, and two regression tests pin it. NOTE: the pre-fix `TestRankAZs_ICEStickySuccess` gave
  only success-ONLY and ICE-ONLY AZs, never one holding both — which is how a green suite hid
  this. Any new ranking rule needs the both-attributes case. **L2 — FIXED 2026-08-25** terraform retried ICE internally, so km never saw the error, the
  sweep could not rotate, and the Lambda died at 900s leaving the stale lock the next run
  reports. `aws_instance.ec2_ondemand` now has `timeouts { create = var.ondemand_create_timeout }`
  (3m) — the on-demand twin of Phase 124's spot waiter; ICE and WaiterTimeout are both
  iterate-class so either error shape rotates. **Uncovered two worse problems:** the guard test
  asserted the defect (`_OnDemandNoTimeout` required NO timeouts block, on a rationale CloudTrail
  disproves), and `pkg/compiler/ec2spot_timeout_test.go` had been reading `ec2spot/v1.2.0` while
  the live pin was `v1.3.0` since Phase 125 — every assertion in it was inert. Retargeted + a
  drift guard (`TestEC2SpotModuleDir_TracksLivePin`) parses the pin from the sandbox template.
  **L5 — FIXED 2026-08-25** a create killed mid-apply leaks its 300GB volume: `km destroy`
  reports success (the volume never reached state) and `km doctor` stayed green (its EBS check
  is scoped to *untagged* volumes, and these are correctly tagged). New
  `checkLaunchAccountOrphanVolumes` reports unattached volumes in a linked account that home
  does not know about, read-only by construction (`LaunchAccountVolumeAPI` has no
  `DeleteVolume`).
- See `docs/cross-account-capacity-borrowing.md` for the full operator runbook.

**Egress deny lists + runtime narrowing (2026-08-21):**
- **`spec.network.egress.deniedDNSSuffixes` / `deniedHosts`** block destinations outright, ahead of
  the allowlist. A deny beats every allow — including the `*` wildcard, the GitHub repo-filter
  carve-out, the OpenAI budget path, and the Bedrock/SES/Anthropic MITM interceptors. The HTTP deny
  gate is registered FIRST because goproxy dispatches first-match and every later handler is a
  carve-out; a deny evaluated after them would be silently bypassable.
- **Deny matching is deliberately BROADER than allow matching** — a bare entry covers subdomains
  (`evil.example.com` also blocks `api.evil.example.com`). Strictness on an allowlist permits less;
  the same strictness on a denylist fails OPEN.
- **`spec.network.egress.runtimeDeny: true`** lets a RUNNING sandbox append denies to itself from
  user-land via **`km-netpolicy deny|list`** (`/opt/km/bin`), effective within ~1s, no restart.
  Narrow-only is enforced twice: the only operation is *append* (there is no removal verb, and a
  test fails if one is added), and the file carries `chattr +a` so the kernel refuses truncate,
  unlink, rename, and attribute-clear. Lives under `/var/lib`, NOT `/run` — a reboot that dropped
  accumulated denies would WIDEN the policy.
- **`pkg/netpolicy`** is now the single canonical deny matcher, replacing three near-copies in
  `sidecars/dns-proxy`, `sidecars/http-proxy`, and `pkg/ebpf/resolver`. All three consult it per
  decision rather than snapshotting, which is what makes runtime denies take effect live.
- **The eBPF resolver reads the runtime file too.** In `ebpf`/`both` mode the bootstrap deliberately
  leaves `km-dns-proxy` disabled and the resolver serves DNS — so without this, `km-netpolicy deny x`
  would report success while `x` stayed resolvable.
- **Guard:** `/etc/km/netpolicy.env` mirrors the profile-baked lists, because those otherwise live
  only in the proxy units' `Environment` blocks and `km-netpolicy list` would report `(none)`.
- **Limit:** on `privileged: true` the sandbox has sudo and can clear `+a` — but it can equally stop
  the proxies, so the guarantee is meaningful on unprivileged boxes.
- **Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`** (NOT `--sidecars` —
  that does not refresh the create-handler zip that renders userdata). Dormant by default; existing
  sandboxes keep their config until `km destroy && km create`. Live UAT passed.
- See `docs/egress-deny-lists.md`.

**Phase 125 (2026-08-19) — Per-profile private-subnet sandboxes with per-AZ NAT gateways (code-complete; live UAT pending):**
- Two decoupled, dormant-by-default toggles: install-level `network.nat_gateway`
  (`km-config.yaml`) controls whether NAT/EIP infrastructure **exists**; per-profile
  `spec.network.privateSubnet` (SandboxProfile) controls whether **this** sandbox's ENI lands
  private. Both absent ⇒ byte-identical to Phase 124. No `apiVersion` bump.
- **One NAT gateway + one EIP per AZ** (4 total), each sitting in the public subnet of the AZ
  it serves — not one shared NAT — because Phase 124's AZ-failover sweep rotates launches
  across all 4 AZs, and a shared NAT would add cross-AZ data-transfer charges to every byte and
  make one AZ a SPOF for the whole install's internet access.
- **`infra/modules/network/v1.1.0`** (new immutable dir): `aws_route_table.private` becomes
  `count`-based (one per private subnet — a single route table cannot hold four different
  `0.0.0.0/0` NAT targets), with `moved` blocks so the first apply against an existing install
  shows moves, not destroy+create. Plural outputs (`private_route_table_ids`,
  `nat_gateway_ids`, `nat_eip_public_ips`) replace the old singular ones.
  **The route-table split is UNCONDITIONAL** — it fires the moment the live unit sources
  v1.1.0, independent of the NAT toggle, so the first `km init` after this phase shows a
  non-empty (additive, non-destructive) plan even for an operator who touches neither toggle.
- **`infra/modules/ec2spot/v1.3.0`**: `public_subnets` renamed to `sandbox_subnets` (truthful
  for either placement) and a new `associate_public_ip` bool (default `true`, `false` for
  private placement — a public IPv4 in a private subnet is both unroutable and billable).
- **REQ-125-SUBPIN:** `infra/templates/sandbox/terragrunt.hcl`'s single shared module-version
  literal is now a per-substrate `locals.substrate_module_versions` map + lookup — this also
  repairs a pre-existing bug where the shared literal pointed the (never-tested) ECS substrate
  at a nonexistent module directory. Scope boundary: this fixed the PIN only, not ECS substrate
  functionality — ECS is not in use and remains unproven end to end.
- Go plumbing: `NetworkOutputs` gains `PrivateSubnets`/`NATGatewayIDs` (both the local
  `outputs.json` path and the S3 tfstate fallback used by `--remote`); placement is resolved
  **once**, into `compiler.NetworkConfig.SandboxSubnets`, before the Phase 124 AZ sweep — the
  sweep itself was retargeted (not forked) to read/write `SandboxSubnets` exclusively;
  `capacity.RankAZs` drops any AZ with no NAT gateway for a private sandbox.
- **Guards:** `km create` fails fast (before any artifact/upload) when a private profile hits a
  NAT-less install, naming `network.nat_gateway` and the fix command; `km init` **refuses** to
  disable NAT while any sandbox row is `network_placement=private` and `status=running` (no
  override flag, by design); two `km doctor` WARNs — NAT enabled but idle (cost + "safe to
  disable"), and a running private sandbox with NAT off. `network_placement` is a
  schema-on-write DynamoDB attribute on `{prefix}-sandboxes`, threaded through the shared
  marshal/unmarshal chokepoint so it survives pause/resume/extend/ttl-handler.
- **Cost:** ~$132/month baseline for 4 AZs plus $0.045/GB processed — e.g. a GPU profile
  pulling 300GB of weights costs ~$13.50 in NAT processing alone. This is the entire reason the
  toggle is reversible: disabling NAT leaves the private subnets as free, routeless islands.
- **Residual risk (accepted pending live UAT):** `cmd/ttl-handler/main.go` renders its
  destroy-placeholder `main.tf` against a frozen `ec2spot/v1.0.0` pin regardless of which
  version a sandbox was created against; an automatic TTL expiry of a v1.3.0-created private
  sandbox is the only path that actually crosses this version boundary (`km destroy` reuses the
  create-time `terragrunt.hcl` and never crosses versions). Resolved analytically at HIGH
  confidence in `RESEARCH.md` Finding 1 (`terraform destroy` operates on state, not
  destroy-time config, and the pin has already survived two prior bumps); live UAT step 6
  proves it. See `docs/private-subnet-nat.md` for the current UAT status.
- **Deploy = `make build` (BEFORE `km init` — binary carries the new `NetworkOutputs`
  extraction and the `KM_NAT_GATEWAY_ENABLED` export; a stale binary silently skips both) +
  `make build-lambdas` + `km init --dry-run=false`.** NOT `--sidecars` (no sidecar binary
  changed; the NAT/EIP/route resources need a full terragrunt apply). Existing sandboxes are
  unaffected and keep public placement until `km destroy && km create`.
- See `docs/private-subnet-nat.md` for the full operator runbook (toggles, cost, the one-time
  route-table split, reversal, guards, deploy surface, scope fences) and
  `.planning/phases/125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway/125-UAT.md`
  for the live UAT record.

**`km-github commit` — verified, bot-attributed signed commits from a sandbox (2026-07-02):**
- New `km-github commit` sidecar verb creates a **GitHub-SIGNED, `klanker-maker[bot]`-attributed** commit (committer `GitHub`, `verified:true reason:valid`) via the GraphQL **`createCommitOnBranch`** mutation. This is the ONLY commit path that auto-signs: a sandbox's local `git commit` is unsigned, and the low-level REST `POST /git/commits` is bot-attributed but `verified:false reason:unsigned`. The mutation signs with GitHub's key and attributes the commit to the **token's own identity** (no hardcoded bot email → portable across installs) and supports multi-file commits in one commit.
- **Usage:** `km-github commit --repo O/R --branch BR [--parent SHA] --message-file MSG -- <repo-relative files...>` → prints the new commit OID to **stdout**, the `verified=… reason=… author=… committer=…` line to **stderr**. Then sync the local worktree: `git fetch origin && git reset --hard origin/BR`. The message file's first line is the headline, the rest (leading blank lines stripped) the body.
- **Precondition — `contents:write`:** the mutation needs a token with `contents:write`, minted only when the sandbox profile's GitHub permissions include **`push`** (`CompilePermissions`, `pkg/github/token.go`). Without it the mutation 403s. See `docs/github-app-permissions.md` § `contents`.
- **`--parent` gotcha:** `--parent` force-resets the branch to SHA first — if a PR is already open and the head is reset to exactly the base SHA, GitHub auto-closes it ("no commits between base and head"). `gh pr reopen`, or create signed commits BEFORE opening the PR.
- **Durable Go form (not the interim bash helper the originating spec proposed):** `cmd/km-github` already has SSM token-fetch + subcommand dispatch + test seams, so the Go verb reuses all of it and drops the bash/jq/gh/base64 + exported-`gh`-function fragility — and avoids churning the byte-identity userdata goldens. New `commit.go` + `commit_test.go`; dispatch case + usage line in `main.go`.
- **Pure `cmd/km-github` sidecar change.** No SandboxProfile schema change, no userdata change (userdata already downloads `km-github` from `s3://…/sidecars/km-github`), no Lambda/IAM/DDB change, no golden change.
- **Deploy = `make build` + `km init --sidecars`** (rebuilds + uploads the `km-github` sidecar; NOT `--dry-run=false`, NOT `--lambdas`). Existing sandboxes gain the verb on `km destroy && km create` (the sidecar is fetched at boot).

**Phase 124 (2026-06-28) — Platform-wide AZ failover + capacity feasibility for EC2 launches (code-complete; live GPU UAT pending G-quota):**
- **AZ sweep with classify-and-retry:** `km create` pre-orders the region's AZs via `capacity.RankAZs` (drops non-offering AZs, checks GPU quota, sorts by historical success) then loops classify-and-retry: ICE / SpotPrice / SpotLimit / WaiterTimeout → rotate to next AZ; Quota / Auth / Invalid → **fail-fast** (no AZ rotation helps a quota wall). `sweepDecision(class, attempt, maxAttempts)` is the single classification callsite.
- **GPU quota fail-fast gate in `RankAZs`:** for GPU families (`g4dn`, `g5`, `g6`, `g6e`, `p3`, `p4`, `p5`), `RankAZs` calls Service Quotas for `L-DB2E81BA` ("Running On-Demand G and VT instances") **before** the sweep. Headroom=0 → immediate `*QuotaError` with the quota code and increase URL; this is NOT an ICE error and never triggers AZ rotation. The quota defaults to 0 per-account; verify in the TARGET application account/region, not the org management account.
- **`km capacity [profile | --type t]`:** per-AZ feasibility table. Verdicts: `likely` (offered + quota OK + success signal or no fresh ICE), `recently-dry` (ICE within 45-min window), `not-offered` (absent from `DescribeInstanceTypeOfferings`), `quota-blocked` (GPU + headroom=0), `unknown` (no store signal). "available" is never printed — capacity is probabilistic.
- **`km create --wait-for-capacity[=30m]`:** outer backoff loop — re-sweeps all AZs every 5 minutes until capacity or deadline. Fail-fast errors exit immediately; ICE-class exhaustion waits and re-sweeps. Dormant by default (absent flag = single-sweep, exit on exhaustion).
- **`{prefix}-capacity` DynamoDB table:** records `last_ice_at` (TTL 45 min via `expires_at`) and `last_success_at` per (instance_type, az) pair. `RankAZs` reads it for sticky AZ preference (last-success → prefer; recent ICE → deprioritize). `km doctor` checks table presence and GPU vCPU quota.
- **Deploy-surface order is load-bearing:** `make build` (operator binary, includes new `dynamodb-capacity` entry in `regionalModules()`) MUST run BEFORE `km init --dry-run=false`. A stale binary silently skips the capacity table and IAM — `km capacity` and RankAZs store writes fail at runtime with no obvious error. Correct order: `make build` → `make build-lambdas` (create-handler with nocap classifier) → `km init --dry-run=false`. NOT `--sidecars`. No SandboxProfile schema change, no bridge Lambda change, no sandbox recreate needed. See `docs/operational-gotchas.md` § AZ failover + capacity feasibility.
- **Phase 124.07 (gap-closure) — capacity store feedback loop closed:** `bestEffortRecordCapacity` (in `create.go`) is now wired into the AZ sweep loop — `RecordSuccess` fires on a successful apply; `RecordICE` fires on `ClassICE` failures. Both are best-effort (5s timeout, log.Warn on error, never blocks the create). The create-handler Lambda role is granted `dynamodb:GetItem/PutItem/UpdateItem/DescribeTable` on `{prefix}-capacity` via a new `dynamodb_capacity` policy in `km-operator-policy`. Deploy this gap-closure with `make build-lambdas` + `km init --dry-run=false` (the IAM grant is in the create-handler terragrunt module, not `--sidecars`).

**Phase 122 (2026-06-27) — GPU vLLM model-serving profiles + local-model chat (code-complete; live UAT pending G-quota):**
- 7 GPU SandboxProfiles serve 70B-class local models via **vLLM** on a Deep Learning AMI (Ubuntu 24.04, `ami-0a9d213b92dabc044`) base, weights cached on a 300GB `additionalVolume` (`/data`, `HF_HOME`), served as `--served-model-name local`. Composed from a NEW abstract fragment `profiles/base/gpu/serve.yaml` (Phase 117 inheritance): `gpu-{qwen,llama}-{12x,48x}` + `gpu-glmair-12x` + `gpu-kimidev-12x` + `gpu-glm46-48x` (g6e.12xlarge/48xlarge = 4/8× L40S). Kimi K2 (~1T) out of scope (needs gated P-family).
- **On-box Bifrost gateway (`:8001`) is a CORE multi-provider ROUTER**, not a shim. **O7 discovery:** Codex requires the OpenAI **Responses API** (Feb 2026) and vLLM serves only Chat Completions, so codex cannot point straight at vLLM — Bifrost (**Docker image `maximhq/bifrost:v1.6.0`**, run via a docker systemd unit; LiteLLM = documented fallback) translates Responses→vLLM, serves Anthropic Messages for Claude Code, and routes by the **`provider/model` string the caller sends** (no named-route config): `vllm-local/local` / `bedrock/us.anthropic.claude-sonnet-4-6` (instance-role SigV4, keyless) / `anthropic/claude-sonnet-4-6` (`ANTHROPIC_API_KEY` via SOPS) / `bedrock/openai.gpt-oss-120b` (keyless — GA Aug 2025; **drop the `-1:0` suffix or Bifrost's catalog 404s**) / `openai/gpt-5` (dormant). Cloud routes flow through the MITM proxy → metered automatically. OTLP export is env-driven → `:4318` (`km otel`). The on-box gateway config + codex `localBaseURL: http://localhost:8001/openai/v1` were **validated live against Bedrock in a GPU-free Docker rehearsal** (`.planning/phases/122-*/122-BIFROST-VALIDATION.md`) — the originally-authored config was an invented schema that would have failed every route.
- **Codex repoint is box-global** (`spec.agent.default: codex`, `agent.codex.localBaseURL` → new `synthesizeCodexConfig` `[model_providers.local]` emission): the local model is reachable via VS Code (Continue), Slack chat-with-resume (`codex exec resume`), on-box terminal/headless codex, AND laptop **`km model start <id> [--anthropic]`** (new `internal/app/cmd/model.go`, reuses `runReconnectingPortForward`; `--anthropic` forwards `:8001` and sets `ANTHROPIC_BASE_URL=…/anthropic` for local Claude Code). **claude stays cloud-pointed on-box** → `/claude` (cloud) vs default `/codex` (local 70B) A/B in one channel.
- **Deploy = `make build` (operator binary) + `make build-lambdas` + `km init --dry-run=false`** (remote create — the new `agent.codex.localBaseURL` schema field needs the create-handler `toolchain/km` refreshed, linux/arm64; NOT `--sidecars`). `km create … --local` works with the operator binary directly. No new TF module / DDB table / bridge change.
- **Prerequisite gotcha:** the "Running On-Demand G and VT instances" vCPU quota (`L-DB2E81BA`) defaults to **0** per-account — verify against the TARGET application account/region, not the org management account. Waves 1–2 are committed and green (`go test ./...` 41 ok / 0 FAIL, 20/20 profiles validate); the live UAT (G3–G9) resumes once the G-quota lands. See `docs/gpu-model-serving.md` and the design spec `docs/superpowers/specs/2026-06-27-gpu-vllm-serving-profiles-design.md`.

**Phase 92 (2026-05-31) — SandboxProfile spec restructure (complete):**
- `spec.identity:` → `spec.iam:` (with `allowedSecretPaths` declared).
- `spec.cli.notify*` → `spec.notification:` (typed `events` / `email` / `slack` sub-blocks).
- `spec.cli.vscodeEnabled` → `spec.runtime.vscode.enabled`.
- `spec.cli.{agent,claudeArgs,codexArgs}` → `spec.agent:` block (`default` / `claude.args` / `codex.args`). `spec.cli` is now `noBedrock`-only.
- Inlined `configFiles["/home/sandbox/.claude/settings.json"]` REMOVED everywhere; settings.json is synthesized from `spec.agent.claude.tools.*` + `trustedDirectories` (canonical `permissions.allow` / `permissions.deny`). Codex `config.toml` is synthesized too — Codex has no native tool gating, so it ships inert hooks + an asymmetry note. See `docs/agent-tool-gating.md`.
- Mixed mode (typed `agent.claude.*` + inlined settings.json) is now SUPPORTED via deep-merge: the compiler merges synthesized typed keys (permissions/trustedDirectories win) ON TOP of the inlined file, preserving operator keys like `enabledPlugins`/`env`/`model` (`compiler.mergeSynthesizedClaudeSettings`). It was previously a hard `km validate` error (the Phase 92 Wave 4 "no-merge" decision), which silently dropped those keys whenever a typed field was set. `km-notify` hooks now include `SubagentStop` (drains a finished Task subagent's transcript into the Slack thread) alongside `Notification`/`Stop`/`PostToolUse`. See `docs/agent-tool-gating.md`.
- Sandbox-side env var names (`KM_NOTIFY_*`, `KM_SLACK_*`, `KM_AGENT`) are UNCHANGED; `apiVersion` stays `klankermaker.ai/v1alpha2`.
- Post-merge: `make build && km init --sidecars` to refresh the management Lambdas.

**Phase 118 (2026-06-24) — Slack trigger allowlist + private per-sandbox channels (complete):**
- Two independent, composable Slack additions; both **dormant by default** (neither set ⇒ byte-identical to Phase 117).
- **Feature A — private channel.** `notification.slack.private` (bool, default `false`) creates the per-sandbox channel as `is_private:true` (was hardcoded public at `pkg/slack/client.go:606`). Threaded a `private bool` through `CreateChannel` + both `SlackAPI`/`SlackInitAPI` interfaces + all call sites (init/rotate pass `false`). Invites unchanged; **no new OAuth scopes** (`groups:*` already requested). Effective only with `perSandbox:true`; create-only (a Phase-104 reused channel is NOT converted public→private). `private:true` + `perSandbox:false` ⇒ `km validate` WARNS.
- **Feature B — Uxxxx trigger allowlist (`allow`).** Gate *who can trigger the bot* by Slack user ID. Install-level `slack.allow` (km-config.yaml → `KM_SLACK_ALLOW` bridge env) and per-sandbox `notification.slack.inbound.allow` (profile → `km-sandboxes` row attr `slack_allow`, comma-joined S → bridge `FetchByChannel` → `SandboxRoutingInfo.Allow`). **Resolution:** non-empty per-sandbox **replaces** install-level; else install-level; else empty ⇒ **everyone allowed** (backward-compatible). Enforced in `events_handler.go` on `event.User`; reject = **silent ignore** (no 👀, no reply, no dispatch; 200). Always enforced, **before** the mention-only filter AND the Phase 91.3 thread-bypass (a non-listed user can't hijack an active thread). Handler order: channel-ownership → allowlist → mention/thread → dedup → dispatch.
- **INVERTED semantics vs GitHub/H1 bridges:** empty `allow` = everyone (allow-by-default), NOT deny-by-default. `isInSlackAllowlist` is only consulted after a `len(allow)>0` guard.
- **Four known-gotcha plumbing items addressed:** config v2→v merge-list entry for `slack.allow` (else silently dropped — with a regression-guard test); `slack_allow` round-trips through `SandboxMetadata` (survives pause/resume/extend); `FetchByChannel` attr-name parity verified against real write/read sites; `km validate` WARNS (not errors). JSON schema gained `private` (slack block) + `allow` (inbound block) — both are `additionalProperties:false`.
- **Deploy:** install-level `allow` → `KM_SLACK_ALLOW` is an env-block change → `km init --dry-run=false` (NOT `--sidecars`; `km init --slack` also covers it), takes effect on the next webhook. Per-sandbox `allow`+`private` → profile schema + create-handler + bridge → `make build-lambdas` + `km init --dry-run=false`; existing sandboxes need `km destroy && km create`. **No apiVersion bump.** See `docs/slack-notifications.md` § Phase 118.

**Phase 119 (2026-06-25) — Slack inbound per-thread parallelism (complete):**
- The per-sandbox inbound poller can now dispatch **multiple distinct Slack threads concurrently** while keeping turns **within a single thread strictly serial** (parallel-across-threads, serial-within-thread). **Dormant by default** (cap unset/`1` ⇒ byte-identical to Phase 118).
- **Two layers.** (1) **Bridge** enqueues to the per-sandbox FIFO using the **thread timestamp** as `MessageGroupId` (was the sandbox id) at both Send sites — FIFO orders within a group, parallelises across groups; behavior-neutral when cap=1. (2) **Poller** rewritten as a **bounded-concurrent ack-after dispatcher**: reads `KM_SLACK_MAX_CONCURRENCY` (default 1), long-polls up to `--max-number-of-messages MAX` (clamped 10), backgrounds each turn into its own RUN_DIR (`…Z-$$-$RANDOM`), and enforces a counting semaphore (`while [ "$inflight" -ge "$MAX" ]; do wait -n; done`).
- **Field:** `notification.slack.inbound.maxConcurrentThreads` (`*int`, default `1` = serial = dormant). **Emit-only-when->1:** the compiler writes `KM_SLACK_MAX_CONCURRENCY` into `/etc/km/notify.env` only when cap>1 (a cap=1 box has NO such line — verified live). **Poller-only, no DDB attr** (bridge doesn't read it; groups by threadTS unconditionally). **`km validate` WARNS** when cap>1 without both `perSandbox:true` AND `inbound.enabled:true`.
- **Ack-after-completion + visibility heartbeat + idempotency guard.** Message deleted only after the turn finishes. Base queue `VisibilityTimeout` raised **30s → 1800s for NEW queues** (existing 30s queues covered by a 120s `ChangeMessageVisibility` heartbeat ticker, torn down post-turn). Crash-redelivery dup window closed by writing `last_processed_event_ts` to the thread DDB row + a poller guard that skips re-dispatch of `event_ts <= last_processed` (second layer behind the bridge nonce-dedup).
- **/workspace shared-mutation caveat:** parallel turns share `/workspace` — suited for conversational/read-mostly fan-out; concurrent repo-mutating turns are operator responsibility (per-thread worktree isolation deferred). Demo: `profiles/learn.v2.parallel.yaml` (cap=3).
- **Deploy:** bridge (threadTS grouping) → `make build-lambdas` + `km init --slack`. Profile field + poller → `make build-lambdas` + `km init --dry-run=false` (renders the new poller userdata — NOT `--sidecars`); existing sandboxes need `km destroy && km create`. Queue base raise applies to **new** queues only. **No apiVersion bump.** Live synthetic-HMAC E2E proved all six assertions (`.planning/phases/119-*/119-UAT.md`). See `docs/slack-notifications.md` § Phase 119.

**Phase 115 (2026-06-16) — Generic GitHub webhook event→prompt router (complete):**
- The `km-github-bridge` Lambda gains an **autonomous, event-driven ingress path** alongside the human-gated `issue_comment` flow. Any non-`issue_comment` webhook (`repository`/`created`, `push`, `release`, …) is matched against **first-match** rules in a new `github.events:` config block and dispatched to a sandbox with the expanded prompt template as the agent's task. First use case: a new repo created in your org → onboarding/audit prompt.
- **Dormant by default.** Absent `github.events:` in `km-config.yaml` → `KM_GITHUB_EVENTS` unset → byte-identical to Phase 114. Each rule: `on` (event type) + optional `actions:` + `match:` (repo glob, required) + optional `exclude:` globs + `profile:` (cold-create) / `alias:` (warm: FIFO enqueue to a running/stopped box; cold-create when absent) + optional `agent:` + `cooldownSeconds:` storm guard. Template vars: `{{repo}} {{event}} {{action}} {{sender}} {{default_branch}} {{html_url}}`.
- **Flow:** HMAC verify → delivery-GUID dedup (BEFORE match) → `MatchEventRule` first-match → optional per-(event,repo,action) cooldown via nonces table → `ExpandEventTemplate` → `GitHubEnvelope{Kind:<event>, Number:0, Action:<action>, Body:<prompt>}` → warm SQS enqueue (alias set) or cold `PutSandboxCreate` (alias empty). No reaction posted (autonomous events have no originating comment). The poller renders a `[GitHub Event Trigger]` preamble (`<event> / <action>`), NOT the `PR: #0` comment preamble.
- **Manifest:** `km github manifest` emits `default_events` as the **union** of `issue_comment` + every configured `on:` type, and adds `metadata:read` when `repository` is present. **`repository`/`created` is only delivered to a GitHub App installed on the org with "All repositories" access** — a brand-new repo is never in a "selected repositories" set (the OPPOSITE of the `issue_comment` recommendation).
- **Pure bridge-Lambda + config + manifest + poller change.** `lambda-github-bridge/v1.1.0` edited in place (additive `github_events_json` var, no version bump). No SandboxProfile schema change ⇒ no sandbox recreate (cold-created boxes get the new poller free; long-lived alias boxes need `km destroy && km create`).
- **Deploy = `make build-lambdas` + `km init --github`** (or full `km init --dry-run=false`; NOT `--sidecars`, which doesn't update the env block) + `km github manifest` → re-subscribe + re-install the App when adding a new event type.
- Live E2E verified on `slacktest03` (warm/alias path): positive match → warm enqueue → poller → agent; `exclude:` glob drop; caught + fixed a blank-`action` envelope bug (`Action` field was absent from `GitHubEnvelope`). See `docs/github-bridge.md` § Phase 115 and `.planning/phases/115-*/115-UAT.md`.

**Phase 114 (2026-06-15) — Slack bridge auto-resume on @-mention of a stopped/paused sandbox (complete):**
- A Slack @-mention targeting a sandbox whose EC2 instance is stopped/paused now auto-resumes it (resume-or-cold-create), mirroring the GitHub Phase 109 self-heal. **Fixed a latent bug:** the Slack bridge's `FetchByChannel` read the `state` attribute but `km` writes `status` on the `{prefix}-sandboxes` row — so `info.Paused` was always false since Phase 67 (pause-hint dead, blocked resume). Verify DDB attr names against a real row, not the test mock. See [[project_slack_bridge_inbound_e2e_and_status_attr]].

**Phase 113 (2026-06-14) — Sandbox self-awareness: on-box profile + capability/network/privilege self-census in klanker:sandbox (complete):**
- Every EC2 sandbox now writes its rendered profile to **`/opt/km/.km-profile.yaml`** at boot (mode 0644, owned sandbox:sandbox). The file is produced by `yaml.Marshal` of the `userDataParams` struct immediately before `tmpl.Execute` in `generateUserData()` (section 2.10 heredoc with single-quoted `'KM_PROFILE_EOF'` sentinel to prevent shell expansion). Written **VERBATIM — no redaction** (secrets are SSM paths, not values; the sandbox role already grants those reads). The on-box copy is semantically equivalent to `artifacts/{id}/.km-profile.yaml` in S3 but NOT byte-identical (re-serialized YAML; field ordering/comments may differ; also captures post-resolution mutations like `--no-bedrock` not present in the raw S3 bytes).
- **`klanker:sandbox` SKILL.md** rewritten from 106-line "detect env + email tooling" into a 361-line **six-section self-census** (A–F): (A) Identity & agent, (B) capability census (sidecar binaries + systemctl probes for pollers + runtime features from profile), (C) network position (enforcement mode inferred from `km-ebpf-enforcer` unit + iptables DNAT count + exactly two safe-active curls: one allowed host → must connect, one `evil.example.com` → must block), (D) privilege & restrictions (`sudo -n true` cross-checked against `spec.execution.privileged` with WHY explanations so the agent stops fighting locked-down behavior), (E) Slack publish-back readiness (env checks, cross-links `klanker:slack`), (F) posture summary paragraph. The two Section C curls are the **only traffic-generating step** and will appear in `km otel`.
- **Graceful degradation:** on a pre-Phase-113 box (`/opt/km/.km-profile.yaml` absent), every profile-derived check is gated on `PROFILE_AVAILABLE=0` — the skill falls back to env-var census + live probes and never errors.
- **Plugin version bump: `0.4.10`** in both `.claude-plugin/plugin.json` and `.claude-plugin/marketplace.json` (Phase 111 held 0.4.9). Required for `klanker:sandbox` skill content change to reach clients that cache the old version.
- **Pure `pkg/compiler/userdata.go` + `skills/sandbox/SKILL.md` change.** No SandboxProfile schema change, no new TF resource, no new DDB column, no bridge Lambda change, no IAM change.
- **Deploy = `make build-lambdas` + `km init --dry-run=false`** (create-handler carries the new userdata section 2.10 — NOT `--sidecars`, which does not rebuild the create-handler zip). Plugin version bump in `.claude-plugin/plugin.json` + `marketplace.json`. Existing sandboxes gain the file only on `km destroy && km create`; the census degrades gracefully until then.
- See `klanker:sandbox` skill and `docs/operational-gotchas.md` § Sandbox self-awareness.

**Phase 112 (2026-06-16) — Flip the default Slack render mode to `blocks-rich` (complete):**
- After the Phase 111 soak, `blocks-rich` (Tier-3: native `markdown` + `table` blocks) is now the **default** render mode for sandbox-originated Slack output. Both sandbox-side post sites flipped their fallback in `pkg/compiler/userdata.go`: `_km_stream_drain` (per-turn streaming) and the Slack-inbound poller reply post both go from `--render "${KM_SLACK_RENDER:-blocks}"` → `${KM_SLACK_RENDER:-blocks-rich}`. Default-path goldens (frozen in Phase 111) were updated in lockstep.
- **Fails soft (unchanged):** an overflowing rich render (12K markdown cap / 50-block cap) demotes Tier-3 → Tier-2 (`blocks`) → Tier-1 (`mrkdwn`) automatically.
- **Operator override unchanged:** `KM_SLACK_RENDER=blocks|mrkdwn|plain` in `/etc/km/notify.env` pins a sandbox back to the old default or lower. It is a sandbox-side knob only (NOT compiler-emitted) → does not affect userdata byte-identity.
- **Pure `pkg/compiler/userdata.go` change.** No SandboxProfile schema change, no new TF resource, no DDB column, no bridge Lambda change. The `km-slack` renderer (`pkg/slack`) is untouched — Phase 111 already shipped the Tier-3 code; this only flips which tier is the default.
- **Byte-identity note:** the frozen `userdata_learn_v2_pre92_baseline.golden.sh` baseline was patched **surgically** (render token + comment lines only) — NOT re-captured via `CAPTURE_PRE92_BASELINE=1`, which would have folded the post-baseline SubagentStop script into the frozen baseline that the byte-identity test deliberately strips. The `additional_volume_only` + `h1_byte_identity` goldens (full-output snapshots with sanctioned capture flags) were regenerated normally.
- **Deploy = `make build-lambdas` + `km init --dry-run=false`** (the create-handler zip renders userdata; NOT `--sidecars`, which does not rebuild that zip). Existing sandboxes keep their baked-in `blocks` default until `km destroy && km create`.
- See `docs/slack-notifications.md` § Phase 112.

**Phase 112.1 (2026-06-24) — Fix: `blocks-rich` silently dropped replies on `invalid_blocks` (empty table cell) (complete):**
- A GFM table with a **blank cell** (the "row-label" leading-column idiom, e.g. `| | Customer | Admin |`) made `km-slack` emit a `rich_text`/`raw_text` cell with an **empty `text` string**. Slack rejects an empty text element with `invalid_blocks`, dropping the entire `chat.postMessage` — the agent's reply was silently lost (reply survived only in `/workspace/.km-agent/runs/*/output.json`). Surfaced once Phase 112 made `blocks-rich` the default. Incident: install `sec`, sandbox `learn-8a08070e`.
- **Two layers:** (1) `pkg/slack/table.go` — `makeBoldCell`/`classifyCell` substitute `" "` for a blank cell (renders visually-empty but schema-valid); covers both the header path AND the ragged-row body-padding path (same empty-text vector — `TestRichTable_RaggedPad` had codified the buggy empty-text padding and was corrected). (2) `cmd/km-slack/main.go` `runWith` — defense in depth: if a block payload is rejected with `invalid_blocks`, re-post **once** without blocks using the already-computed mrkdwn/plain fallback. Builds a **fresh envelope** (new nonce) since the bridge already reserved the first one (`replayed_nonce` guard); single attempt, WARN-logged.
- **Pure `pkg/slack` + `cmd/km-slack` sidecar change.** No Terraform / DDB / SandboxProfile schema change; no bridge Lambda change. Mitigation without deploy: `KM_SLACK_RENDER=mrkdwn` in `/etc/km/notify.env` pins a box off the rich path.
- **Deploy = `make build && km init --sidecars`** (rebuilds + uploads the `km-slack` sidecar). Existing sandboxes pick it up on a sidecar refresh / `km destroy && km create`.

**Phase 111 (2026-06-14) — Rich Slack rendering: markdown + table blocks (opt-in) (complete):**
- New Tier-3 `renderRich` (`pkg/slack/rich.go` + `table.go`) selected by `KM_SLACK_RENDER=blocks-rich`. Default stays `blocks` (Tier-2) — **opt-in**, so default-path golden fixtures stay frozen. Flipping the default is the separate **Phase 112** (gated on this soak).
- Prose → Slack **`markdown` block** (GA Feb 2025): native GFM headings/bold/italic/code/lists + **clickable `[label](url)` anchors** (rendered natively, not `<url|label>`). 12,000-char cumulative cap with chunking; on overflow `ok=false` → Tier-2 `RenderBlocks` → Tier-1 `Mrkdwnify` (existing tiers DEMOTED, not deleted; fail-soft `recover()`). Leading H1 still → `header` block; 🔧 tool lines → `context` block; plain `text` fallback unchanged.
- GFM pipe tables → dedicated **`table` block** (GA Aug 2025): alignment from the delimiter row; >20 cols / >100 rows → existing `fencePipeTables` monospace fallback. **Live-UAT schema fix (`f8271c17`):** header cells are `rich_text` bold wrapped in the MANDATORY `rich_text_section` (a flat element list is rejected with `invalid_blocks`); body cells are all `raw_text` (`raw_number`'s value-field is undocumented and was rejected — deferred; numeric columns still right-align via `column_settings`). See [[project_slack_blockkit_schema_validation]] for the bot-token validation technique.
- `KM_SLACK_AI_FOOTER=true` (default off) appends a "Generated by AI" `context` footer. Both `KM_SLACK_RENDER` and `KM_SLACK_AI_FOOTER` are **sandbox-side env knobs only** (NOT compiler-emitted) → userdata/byte-identity goldens untouched.
- **Pure `pkg/slack` + `cmd/km-slack` sidecar change.** No Terraform / DDB / SandboxProfile schema change; no bridge Lambda change (the bridge forwards the pre-serialized block array verbatim via `json.RawMessage`). Live E2E on `slacktest01` passed: headings, link anchors, tables (bold header + L/C/R alignment), wide-table + 12K-cap fallbacks, code-fence guard, AI footer.
- **Deploy = `make build` + `km init --sidecars`** (rebuilds + uploads the `km-slack` sidecar binary; NOT a full apply). Existing sandboxes need `km destroy && km create` (or sidecar refresh) to gain the new renderer.
- See `docs/slack-notifications.md` § Phase 111 and `.planning/research/slack-markdown-block.md`.

**Phase 109 (2026-06-12) — GitHub bridge self-heals orphaned `stopped` alias rows (resume-or-cold-create) (complete):**
- An orphaned `{prefix}-sandboxes` row (`status=stopped` but the EC2 instance is **gone** — terminated out from under km) used to permanently shadow cold-create: the resume branch logged a non-fatal error and enqueued to a per-sandbox FIFO with **no live poller**, so the comment stranded and the bot silently no-op'd on cold start. `ResolveByAliasWithStatus` returned `(id,"stopped",nil)` so `err==nil` skipped cold-create.
- **Fix:** `EC2Resumer.StartSandbox` now wraps an exported sentinel `ErrNoResumableInstance` on the terminal `len(found)==0` path (transient `DescribeInstances`/`StartInstances` errors are **not** wrapped — they keep log-non-fatal + enqueue). `WebhookHandler.Handle` branches the resume failure with `errors.Is`: terminal ⇒ `StatusWriter.DeleteSandboxRow(id)` (clears the stale row so the alias becomes absent — dodges the **ambiguous-alias trap**) then `Publisher.PutSandboxCreate` (cold-create), **no enqueue, no thread upsert**; transient ⇒ unchanged enqueue; success ⇒ unchanged `SetStatusRunning` + enqueue. Genuinely stopped/paused (hibernated) instances still resume exactly as before.
- **Pure bridge-Lambda code + one IAM statement.** `SandboxStatusWriter` extended with `DeleteSandboxRow`; `DynamoSandboxStatusWriter.DeleteSandboxRow` is a single `DeleteItem` keyed by `sandbox_id` (`DynamoUpdateItemClient` widened with `DeleteItem`); `infra/modules/lambda-github-bridge/v1.1.0` adds `dynamodb:DeleteItem` to the existing `DDBSandboxesUpdateItem` statement (still no `PutItem`). No SandboxProfile schema change, no new TF resource, no new DDB column.
- **Ported to the H1 bridge in lockstep (parity):** identical fix in `pkg/h1/bridge/{aws_adapters,interfaces,webhook_handler}.go` (resume branch is `dispatchTarget`); `infra/modules/lambda-h1-bridge/v1.0.0` gets the same `dynamodb:DeleteItem` grant. Slack bridge has no resume-or-cold-create path (no change).
- **Deploy = `make build-lambdas` + `km init --dry-run=false`** (NOT `--sidecars` — the IAM/env block updates only on a full terragrunt apply; `km init --github` / `--h1` also cover the env+IAM change for the respective bridge). **No sandbox recreate.**
- Out of scope (note): the orphaned FIFO queue + per-sandbox Lambdas are not GC'd (only the DDB row); they're harmless garbage flagged by `km doctor`, cleaned by `km destroy <id>`.
- See `docs/github-bridge.md` § Phase 109 and `docs/h1-bridge.md` § Phase 109.

**Phase 108 (2026-06-12) — GitHub bridge per-turn idempotency guard: no more duplicate PR comments (complete):**
- A single @-mention could make the sandbox agent post **two byte-identical** PR comments/reviews seconds apart — the agent invoked `km-github comment`/`review` twice in one turn (the poller's hard "post your reply (REQUIRED)" mandate colliding with an invoked skill's own post instruction, or a self-retry of a call that secretly succeeded). GitHub issue comments/reviews are NOT idempotent.
- **Fix at the helper chokepoint** (`cmd/km-github/`): every post now embeds an invisible per-turn marker `<!-- km-turn:$KM_GITHUB_TURN_ID -->` and, before posting, scans the issue's existing comments / the PR's existing reviews for that same marker — if present, it no-ops (exit 0, logs `duplicate suppressed`). The marker is keyed on the poller's `RUN_ID`, so two *separate* legitimate mentions on the same PR each still post.
- **Fail-open:** if the pre-post duplicate-check GET errors, the helper posts anyway (never strand a legitimate reply because a read failed) — but logs it. The check paginates (`per_page=100`, follows `Link: rel="next"`) so a just-posted comment that sorts last is still found.
- **`KM_GITHUB_TURN_ID`** is exported inline into all four agent-dispatch `sudo` blocks of the github inbound poller (codex resume/first, claude main/retry), mirroring `KM_GITHUB_REPLY_AGENT`. Empty for manual `km-github` invocations ⇒ marker + duplicate-check disabled ⇒ byte-identical to pre-Phase-108.
- **Pure helper + `pkg/compiler/userdata.go` change.** New `pkg/github/marker.go` (`TurnMarker` / `CommentMarkerExists` / `ReviewMarkerExists`). No SandboxProfile schema change, no new TF resource, no new DDB column, no bridge Lambda or IAM change. Slack/H1 pollers untouched.
- **Deploy = `make build-lambdas` + `km init --dry-run=false`** (NOT `--sidecars`): the create-handler zip carries the new userdata, AND the full `km init` rebuilds+uploads the `km-github` sidecar binary via `buildAndUploadSidecars` (the helper is delivered to the box from `s3://.../sidecars/km-github`, NOT by `make sidecars`). Existing sandboxes need `km destroy && km create` to gain the new userdata env-var export + the new sidecar.
- See `docs/github-bridge.md` § Phase 108.

**Phase 106 (2026-06-11) — Session-resume hint on GitHub + HackerOne bridge replies (post-on-mint) (complete):**
- After a bridge agent turn, the sandbox-side poller posts ONE collapsed `<details>` resume-hint comment carrying the sandbox id, run-from `/workspace`, and the agent-correct resume command: `claude --resume <id>` (Claude) or `codex exec resume <id>` (Codex), branched on `EFFECTIVE_AGENT`, plus the freshly minted session id.
- **Post-on-mint:** fires only when the session id is new or changed (first turn or Gap-E cross-box re-mint) ⇒ exactly once per stable thread. Best-effort `|| true` — never blocks the SQS ack.
- **H1 hint is INTERNAL by default** (`km-h1 comment`, no `--reply-to-researcher`) — it lands on the internal/team comment track, NEVER visible to the external HackerOne researcher.
- **GitHub + HackerOne pollers only.** Slack poller deliberately EXCLUDED — byte-identical to pre-Phase-106.
- **Pure `pkg/compiler/userdata.go` change.** No SandboxProfile schema change, no new TF resource, no new DDB column, no bridge Lambda or IAM change.
- **Deploy = `make build-lambdas` + `km init --dry-run=false`** (NOT `--sidecars` — `--sidecars` only re-uploads sidecar binaries, not the create-handler zip; NOT `km init --github`/`--h1` — those refresh bridge env+IAM only). Existing sandboxes need `km destroy && km create`.
- See `docs/github-bridge.md` § Phase 106 and `docs/h1-bridge.md` § Phase 106.

**Phase 105 (2026-06-11) — Scoped `km init` for bridge config (complete):**
- `km init --only <module>` applies a **single** terragrunt module instead of the full ~27-module fleet. Refreshes the Lambda env block + IAM for that module only. Does NOT rebuild a stale code zip (still `make build-lambdas` + `km init --lambdas`) and does NOT provision new resources/tables/queues (still full `km init --dry-run=false`).
- **Sugar aliases (tier-1, no confirmation):** `--github` → `lambda-github-bridge`; `--slack` → `lambda-slack-bridge`; `--h1` → `lambda-h1-bridge`; `--email` → `email-handler`. All four are pure env+IAM targets — fast, no destroy-class risk.
- **Tier-2 gated (no cheap alias):** `--only ses` — routes through the destroy-class safety gate (same curated trip-block as `km init --plan`) as a pre-apply gate; refuses a protected destroy/replace without `--i-accept-destroys`. The `ses` module owns SES domain identities, Route53 records, and the consolidated S3 bucket policy.
- **SSM side-effect:** scoped `--github`/`--h1`/--slack` also republishes SSM command maps / bot-user-id (same as a full apply; no separate step required).
- **Mutual exclusion:** `--only`/`--github`/`--slack`/`--h1`/`--email` are mutually exclusive with `--sidecars`, `--lambdas`, and `--plan`.
- **`--dry-run` honored:** defaults true (shows what would apply); `--dry-run=false` applies.
- **No-drift invariant:** scoped apply derives from the identical `km-config.yaml → KM_* → terragrunt` pipeline as a full apply; a subsequent `km init --plan` is a no-op.
- **Deploy = `make build`** (operator-side binary only; no Lambda zip rebuild, no new TF resource).
- See `klanker:init` skill § Fast-path variants and `OPERATOR-GUIDE.md` § Scoped init.

**Phase 104 (2026-06-10) — O(1) Slack channel resolution on alias reuse (complete):**
- `km create` on a reused `--alias` (profile has `notification.slack.perSandbox: true` + `archiveOnDestroy: false`) now resolves the existing channel in **bounded, O(1)** time — never the unbounded `conversations.list` workspace scan that previously wedged the create-handler Lambda for the full 900 s.
- **Root cause fixed:** any transient `conversations.info` error (ratelimited / 5xx / context blip) previously fell through to an unbounded paginated scan. Phase 104 classifies the error: only a definitive `channel_not_found` invalidates the stored mapping; every other error triggers a bounded retry (2× / 500 ms) then optimistic use of the stored ID — never a scan.
- **Three layers:** P0 wall-clock sub-context bounded by `KM_SLACK_RESOLVE_BUDGET` (default 45 s); P1 lookup-first with transient-error classification; P2 durable `km-slack-channels` DDB table (hash key `alias`, no TTL) as the authoritative store.
- **New `km-slack-channels` table** (`{prefix}-slack-channels`, PAY_PER_REQUEST, SSE on). Existing SSM by-name cache kept as back-compat read/write fallback; both stores written through on every successful resolution. `km doctor` checks table existence only — NOT orphan-row scan (alias rows are not per-sandbox and must never be auto-deleted).
- **`km slack adopt <alias> <channelID>`** — seeds the DDB store for channels created outside km or before Phase 104. Validates `^C[A-Z0-9]+$` format + bot membership; writes through to DDB + SSM. Find the channel ID in Slack → channel name (top bar) → About → Channel ID.
- **Observability:** `slack_resolve path=cache_hit|cache_optimistic|created|scan_capped|failfast ms=… id=…` — one INFO log per Mode-2 resolution at create time.
- **No SandboxProfile schema change ⇒ no `--sidecars`, existing sandboxes unaffected** (create-time fix; running sandboxes do not need to be recreated).
- **No `lambda-slack-bridge` change.** Bridge is NOT a consumer of the `km-slack-channels` table.
- **Deploy = `make build` (BEFORE `km init` — binary carries the new `dynamodb-slack-channels` regionalModules entry; a stale binary silently skips it → `AccessDenied` at runtime) + `make build-lambdas` + `km init --dry-run=false`** (new table + create-handler IAM require a full terragrunt apply, NOT `--sidecars`).
- See `docs/slack-notifications.md` § Phase 104 for incident root-cause analysis, env knobs, adopt runbook, and troubleshooting.

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
| Push webhook ingress — `webhooks:` block, Wiz Automation Rule setup, canonical km payload template, storm control (replay/cooldown/group_by/rate ceiling), deploy surface | `docs/webhook-ingress.md` |
| Why an interactively-used sandbox got reaped as idle — the utmp/PTY root cause, the seven `km-presence` signals, why VNC/SSH are matched by socket not process, and the fail-idle rule | `docs/desktop.md` § Idle timeout + `docs/vscode.md` § Idle timeout |
| Egress deny lists — `spec.network.egress.deniedDNSSuffixes` / `deniedHosts`, deny-beats-allow (incl. `*` and the GitHub/OpenAI/MITM carve-outs), the deliberately-broader deny matching, the `*`-allowlist-under-eBPF limitation, deploy surface | `docs/egress-deny-lists.md` |
| Declarative MITM intercepts — `spec.network.mitm.intercepts`, `redirect`/`respond` actions, precedence (deny → metering/GitHub → intercepts → allowlist), by-name last-wins override, the `profiles/base/mitm-rickroll.yaml` migration fragment, deploy surface | `docs/mitm-intercepts.md` (Phase 129) |
| Cross-account capacity borrowing — `km account add/register/list/rm`, `spec.runtime.launchAccount`, the two-credential enrollment sequence, the launcher-role security model, capacity/teardown/doctor cross-account wiring, deploy surface | `docs/cross-account-capacity-borrowing.md` (Phase 126) |
| Private-subnet sandboxes + per-AZ NAT gateways — `network.nat_gateway` / `spec.network.privateSubnet` toggles, cost, the one-time route-table split, reversal, guards, deploy surface | `docs/private-subnet-nat.md` (Phase 125) |
| Sizing the private topology to cut the NAT bill — `network.private_subnet_count` (1–4, absent = all 4), the AZ-rotation tradeoff it buys, and the subnet destroys on lowering it | `docs/private-subnet-nat.md` § Paying for fewer AZs |
| AZ failover + capacity feasibility — `km create` classify-and-retry sweep, GPU quota wall (`L-DB2E81BA`), `km capacity` verdicts, `--wait-for-capacity`, `{prefix}-capacity` DDB table, deploy-surface order | `docs/operational-gotchas.md` § AZ failover + capacity feasibility (Phase 124) |
| Action quotas + freeze quarantine — `spec.limits`/install `limits:`, windows (lifetime/perHour/perDay), `onBreach` warn/block/freeze, zero=hard-deny, `km freeze`/`km unlock`, `km status` Quotas section, the `km-quota-alerter`, deploy surface | `docs/action-quotas.md` (Phase 121) |
| Operator CLI tour | `klanker:user` skill |
| One-time platform setup, `km init`, multi-instance, Slack bootstrap | `klanker:init` skill |
| Send / receive email from inside a sandbox | `klanker:email` skill |
| Inject SOPS-encrypted secrets into a sandbox | `docs/sandbox-secrets.md` (Phase 89) |
| Serverless `km check` runner (deploy/run/ls/sync/rm, KM_CHECK_TRIGGER, CheckDispatch) | `docs/check-runner.md` (Phase 116) |
| Inject secrets into a `km check` Lambda — `--secret <ssm-path>` + `--sops <file>` (deploy-time unpack to per-check SSM SecureString params; no Lambda KMS) | `docs/check-runner.md` § Secrets |
| Post to Slack from inside a sandbox (incl. transcript streaming, inbound, attachments) | `klanker:slack` skill |
| Polite-bot mode, `KM_SLACK_MENTION_ONLY`, per-channel @-mention-only inbound | `docs/slack-notifications.md` § Phase 91 |
| Federated bridge relay — one Slack App across multiple km installs | `docs/slack-notifications.md` § Phase 95 |
| Default router: orphan-channel @-mention reply, `slack.default_router`, cooldown | `docs/slack-notifications.md` § Phase 96 |
| Private per-sandbox Slack channel (`notification.slack.private`, `is_private:true`) | `docs/slack-notifications.md` § Phase 118 |
| Slack trigger allowlist (`slack.allow` / `notification.slack.inbound.allow`, `KM_SLACK_ALLOW`, `slack_allow` attr, resolution order, silent-drop) | `docs/slack-notifications.md` § Phase 118 |
| Slack inbound per-thread parallelism (`notification.slack.inbound.maxConcurrentThreads`, `KM_SLACK_MAX_CONCURRENCY`, threadTS FIFO grouping, bounded-concurrent poller, ack-after + 1800s visibility heartbeat, `last_processed_event_ts` dedup, /workspace caveat) | `docs/slack-notifications.md` § Phase 119 |
| Alias reuse vs archived channels — unarchive-on-reuse (`path=cache_unarchived`), why a renamed `channelName` never takes effect on a mapped alias, `km slack forget-channel` / `km slack adopt` as the escape hatches | `docs/slack-notifications.md` § Alias reuse and archived channels |
| Verified, `klanker-maker[bot]`-attributed **signed** commits from a sandbox — `km-github commit` (GraphQL `createCommitOnBranch`), `contents:write`/`push` precondition, `--parent` PR-auto-close gotcha | CLAUDE.md § `km-github commit` + `docs/github-app-permissions.md` |
| Sandbox-side helper binaries convention — everything a klanker does for GitHub/Slack/Email/HackerOne starts at `/opt/km/bin` | CLAUDE.md § Sandbox-side helper binaries |
| GitHub comment-trigger bridge — `@km-bot review this PR` → sandbox agent → PR review | `docs/github-bridge.md` (Phase 97) |
| GitHub bridge federated relay — one GitHub App across multiple km installs (`github.peer_bridges`) | `docs/github-bridge.md` § Phase 100 |
| GitHub App permissions reference — every scope/event + why, `contents:write` push opt-in | `docs/github-app-permissions.md` |
| Slack App permissions reference — every bot scope/event + why | `docs/slack-app-permissions.md` |
| GitHub bridge agent verbs — `/claude` / `/codex` per-thread agent select in PR comments | `docs/github-bridge.md` § Phase 102 |
| GitHub generic event→prompt router — `github.events:` rules fire a sandbox agent on `repository`/`push`/`release` webhooks (warm `alias:` or cold-create); "All repositories" install gotcha | `docs/github-bridge.md` § Phase 115 |
| HackerOne comment-trigger bridge — program webhook → sandbox agent → report comment (auto-triage + `@`-handle, multi-target fanout, internal-by-default replies) | `docs/h1-bridge.md` (Phase 103) |
| O(1) Slack channel resolution on alias reuse, `km-slack-channels` table, `km slack adopt`, `KM_SLACK_RESOLVE_BUDGET` | `docs/slack-notifications.md` § Phase 104 |
| Scoped `km init` — `--only <module>` / `--github` / `--slack` / `--h1` / `--email` (tier-1 env+IAM fast-path) + `--only ses` (tier-2 destroy-class gated) | `klanker:init` skill § Fast-path variants |
| Ask the operator to do something via email | `klanker:operator` skill |
| Detect sandbox environment + verify tooling | `klanker:sandbox` skill |
| Sandbox self-census — identity, capabilities, network boundary, privilege/restrictions, Slack readiness, posture summary | `klanker:sandbox` skill (Phase 113) |
| On-box profile file (`/opt/km/.km-profile.yaml`), deploy surface, graceful degradation on pre-Phase-113 boxes | `docs/operational-gotchas.md` § Sandbox self-awareness |
| VS Code Remote-SSH operator workflow | `klanker:vscode` skill |
| Run a remote browser/desktop in a sandbox via km desktop | `klanker:desktop` skill |
| Serve a local 70B model on a GPU sandbox (vLLM + Bifrost multi-provider router), reach it via VS Code / Slack codex / `km model start [--anthropic]`; G-quota + DLAMI prereqs, secret/route wiring, deploy surface | `docs/gpu-model-serving.md` (Phase 122) |
| Cross-account k8s (IRSA) cluster onboarding | `klanker:cluster` skill |
| Non-obvious operational footguns (deploy surface, terragrunt, teardown, Ubuntu userdata) | `docs/operational-gotchas.md` |
| `km shell` → "km-session-entry: not found" on Ubuntu; apt mirror host rewrite (`ec2.archive.ubuntu.com` → `archive.ubuntu.com`), diagnosing a still-running cloud-init without a shell | `docs/operational-gotchas.md` § km shell → "km-session-entry: not found" |
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
| Composable multi-parent profile inheritance — `extends:` list, deep-merge, `profiles/base/` fragments, `initCommandsAppend`, v1 narrowing limitation | `OPERATOR-GUIDE.md` § Composable inheritance |
| Wiz Runtime Sensor on a sandbox — opt-in `base/security/wiz` fragment, the prefix-relative SSM-path rule, why it is not tamper-proof, deploy surface | `docs/wiz-sensor.md` |
| Which Wiz integration is which — km **pulls** from Wiz (`checks.triggers`, Phase 116), Wiz **pushes** to km (`webhooks:`, Phase 127), and a sandbox **reports to** Wiz (the runtime sensor). Three directions, independent, composable | `docs/wiz-sensor.md` § Relationship to the other Wiz integrations |

## Sandbox-side helper binaries (`/opt/km/bin`)

**Everything a sandbox agent ("klanker") does to act on the outside world starts at `/opt/km/bin`.** These are the `km-*` helper binaries km bakes onto every EC2 sandbox — not the operator `km` CLI (that runs on the operator's workstation). They are cross-compiled `linux/amd64` sidecars uploaded to `s3://{artifacts}/sidecars/km-*` by `km init --sidecars` (`buildAndUploadSidecars` → `sidecarBuilds()` in `internal/app/cmd/init.go`), then fetched at boot by userdata (`aws s3 cp …/sidecars/… /opt/km/bin/…`) and symlinked into `/usr/local/bin`, so a bare `km-slack`/`km-github`/`km-send` resolves on `PATH`. Each reads its per-sandbox GitHub/Slack/SES/HackerOne credentials from SSM itself (`/{prefix}/sandbox/{id}/…`) — the agent never handles a raw token.

**When a klanker needs to act, reach for the matching helper before anything hand-rolled:**

| Channel | Helper(s) in `/opt/km/bin` | Verbs / use |
|---|---|---|
| **GitHub** | `km-github` | `comment` / `review` / `check` / `pr create` / **`commit`** (verified signed commit via GraphQL `createCommitOnBranch`) — all keyless (token from SSM). Prefer this over raw `gh`/`git push`. |
| **Slack** | `km-slack` | post status/progress/threaded replies + Block-Kit rich rendering (see `klanker:slack` skill). |
| **Email** | `km-send`, `km-recv` (+ `km-mail-poller`) | signed inter-sandbox / operator email (see `klanker:email` skill). |
| **HackerOne** | `km-h1` | comment on reports (internal-by-default). |
| **Git auth** | `km-git-askpass`, `km-git-credential-helper` | inject the installation token into plain `git` operations (baked-in sandbox id, works in any subprocess). |

Infra sidecars also live here (`km-http-proxy`, `km-dns-proxy`, `km-audit-log`, `km-presence`, `km-queue-runner`, `km-notify-hook`, `km-upload-artifacts`, `otelcol-contrib`, `sops`) plus the source-aware inbound pollers (`km-{github,slack,h1}-inbound-poller`, `km-mail-poller`) — an agent rarely calls these directly, but their presence is how the box self-censuses its capabilities (see the `klanker:sandbox` skill + `/opt/km/.km-profile.yaml`, Phase 113). New sandbox-side capability ⇒ new `km-*` binary in this dir + a `sidecarBuilds()` entry + a userdata `s3 cp`, delivered by `km init --sidecars`.

## CLI

- `km validate <profile.yaml>` — validate a SandboxProfile
- `km create <profile.yaml> [alias]` — provision a sandbox (`--no-bedrock`, `--docker`, `--alias`, `--on-demand`, `--prompt <text-or-@file>` repeatable, `--wait`). The alias may be a second positional instead of `--alias`; whichever argument ends in `.yaml`/`.yml` is the profile, so order does not matter.
- `km clone <source> [new-alias]` — duplicate one or more running sandboxes from the source's stored profile (`--count`, `--alias`, `--no-copy` to skip the `$HOME` copy, `--ai`/`--compute`/`--ttl`/`--idle`/`--on-demand`/`--no-bedrock` overrides)
- `km destroy <sandbox-id>` — teardown a sandbox (`--remote` by default, `--yes` to skip confirmation; `km kill` is an alias)
- `km pause <sandbox-id>` — hibernate/pause an EC2 or Docker instance (preserves infra)
- `km resume <sandbox-id>` — resume a paused or stopped sandbox
- `km lock <sandbox-id>` — safety lock preventing destroy/stop/pause (atomic DynamoDB)
- `km unlock <sandbox-id>` — remove safety lock (requires confirmation or `--yes`)
- `km stop <sandbox-id>` — stop the EC2 instance, preserving infrastructure for restart (`--remote`); distinct from `km pause` (hibernate)
- `km extend <sandbox-id> <duration>` — extend a sandbox's TTL or change its idle timeout (`--idle`, `--remote`)
- `km list` — list sandboxes (narrow default, `--wide` for all columns, `--auth` for per-sandbox agent-login state, `--json`, `--tags`, `--reset` to reset local numbering)
- `km status <sandbox-id>` — detailed state, resources, and budget for a single sandbox
- `km logs <sandbox-id>` — tail CloudWatch audit logs for a sandbox (`--follow`, `--stream <name>`)
- `km agent <sandbox-id> --claude` — interactive Claude session via SSM (`--no-bedrock` for direct API)
- `km agent run <sandbox-id> --prompt "..."` — fire-and-forget non-interactive Claude in tmux (`--wait`, `--interactive`, `--no-bedrock`, `--auto-start`)
- `km agent attach <sandbox-id>` — attach to a running agent's tmux session (Ctrl-B d to detach)
- `km agent results <sandbox-id>` — fetch latest run output (`--run <id>` for specific run)
- `km agent list <sandbox-id>` — list all agent runs with status and output size (`--queue` to list on-box prompt queue entries instead)
- `km agent auth <sandbox-id> --claude|--codex` — authenticate the claude/codex CLI inside a sandbox via SSM (interactive login flow over the SSM session)
- `km at '<time>' <cmd>` — schedule deferred/recurring operations; supports `create`, `destroy`, `kill`, `stop`, `pause`, `resume`, `extend`, `budget-add`, `agent run` (`km schedule` is an alias)
- `km at list` / `km at cancel <name>` — manage scheduled operations
- `km email send` — send signed email between sandboxes or to/from operator (`--from`, `--to`, `--cc`, `--use-bcc`, `--reply-to`)
- `km email read <sandbox>` — read sandbox mailbox with signature verification and auto-decryption (`--json`, `--mark-read`)
- `km otel <sandbox-id>` — OTEL telemetry + AI spend summary (`--prompts`, `--events`, `--tools`, `--timeline`)
- `km budget add <sandbox-id>` — top up a sandbox's compute or AI budget; auto-resumes the sandbox if it was budget-suspended
- `km slack init` — bootstrap Slack integration (`--bot-token`, `--invite-email`, `--shared-channel`, `--signing-secret`, `--force`); also caches `bot_user_id` at `{prefix}/slack/bot-user-id` (Phase 91)
- `km slack test` — end-to-end smoke test through the bridge
- `km slack status` — print SSM-backed Slack config
- `km slack invite <email>` — invite an email to a Slack channel; auto-detects native vs Connect (`--channel`, `--external`, `--dry-run`)
- `km slack manifest` — render a deployment-specific Slack App manifest to stdout (`--app-name`)
- `km slack rotate-token --bot-token <new>` — rotate Slack bot token + cold-start the bridge
- `km slack rotate-signing-secret --signing-secret <new>` — rotate Slack App signing secret
- `km slack adopt <alias> <channelID>` — seed the alias→channel DDB mapping for an externally-created channel so the next `km create` resolves it O(1) (Phase 104)
- `km github init` / `km github manifest` / `km github status` — manage the GitHub App bridge: cache bot-login + record bridge-url in SSM, render the App manifest JSON, print SSM-backed config
- `km h1 init` / `km h1 status` — manage the HackerOne webhook bridge: mint webhook secret + capture Basic-Auth creds, print SSM-backed config (no `manifest` subcommand — H1 has no App-manifest concept)
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
- `km account add <name> --aws-profile <target-admin> --trust <home-account-id> --instance-types ...` — enroll a target AWS account as a capacity-borrowing link (target-account admin creds; `--provision-network --az-count N`, `--provision-efs`, `--enable-bedrock`, `--dry-run` defaults true)
- `km account register <name> --from-fragment <path> --aws-profile <home>` — register a link into the home account (home creds; writes `km-config.yaml` entry + SSM external-id + one read-only artifacts-bucket grant)
- `km account list` — list configured cross-account capacity links (never prints the external id)
- `km account rm <name> [--target-aws-profile <profile>] [--purge-backend]` — remove a link (home-only by default; `--target-aws-profile` also destroys the target-account footprint; `--purge-backend` deletes the shared state bucket/lock table)
- `km init` — initialize regional infrastructure (`--sidecars` for fast binary deploy, `--lambdas` for Lambda-only deploy, `--plan` to preview with destroy-class safety gate, `--dry-run=false` to actually apply, `--only <module>` for a scoped single-module apply; sugar: `--github` / `--slack` / `--h1` / `--email` (tier-1, env+IAM, no confirmation); `--only ses` (tier-2, destroy-class gated))
- `km bootstrap --shared-ses` — provision the shared SES rule set (idempotent; `--plan` previews with destroy-class safety gate)
- `km bootstrap --shared-secrets-key` — provision the shared KMS key for SOPS secret injection (one-time per install; `--plan` previews with destroy-class gate; see `docs/sandbox-secrets.md`)
- `km bootstrap --all` — chain foundation (SCP/KMS/artifacts) + shared SES rule set in one command; mutex with `--shared-ses`; `--plan` honors the destroy-class gate
- `km env [--aws-profile]` — print exportable `KM_*` block for `eval $(km env)` to drive terragrunt directly (excludes `AWS_PROFILE` by default; `KM_ACCOUNTS_TERRAFORM` intentionally omitted)
- `km shell <sandbox-id>` — SSM shell (`--root`, `--ports`, `--no-bedrock`, `--learn`, `--ami`)
- `km ami list` / `km ami bake <sandbox-id>` / `km ami copy <ami-id> --region <dest>` / `km ami delete <ami-id>` — operator-baked AMI lifecycle
- `km rsync save|load|list|view|delete <sandbox-id>` — snapshot and restore the sandbox user's `$HOME` to/from S3
- `km roll creds` — rotate all platform + sandbox credentials
- `km configure` — interactive wizard to generate `km-config.yaml` (`km configure github` configures the GitHub App)
- `km uninit` — tear down all shared regional infrastructure for a region (`--yes`, `--force` if active sandboxes exist, `--include-scp`; NO `--dry-run` — confirmation is `--yes`)
- `km unbootstrap` — tear down platform-foundational resources: SSM params, buckets, KMS key (`--include-zone` to also delete the Route53 zone, `--kms-deletion-window`, `--yes`)
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

**Phase 117 (2026-06-24) — Composable multi-parent profile inheritance (complete):**
- `extends:` now accepts a **string** (back-compat) OR an **ordered `[]string` list** of parent names. Bases resolve left→right; child applies last.
- **deepMerge semantics (uniform):** maps recurse at every depth (key-union), scalars child/right-parent wins, lists concat+dedup (order-preserving, first occurrence kept). Union applies to every list field: `initCommands`, `allowedDNSSuffixes`/`allowedHosts`, `allowedRepos`/`allowedRefs`, `email.allowedSenders`, `agent.claude.tools.autoApprove`, `rsyncPaths`, `artifacts.paths`. Object lists (`additionalSnapshots`) de-dup by deep equality.
- The old hand-written typed merger zoo (`mergeNotificationSpec` / `mergeAgentSpec` / etc.) was replaced by a generic `deepMerge(map[string]any)` over a YAML round-trip. Thin wrappers kept for backward-compat test paths.
- **Abstract fragments** (`metadata.abstract: true`) live in `profiles/base/`. `km validate` skips them (exit 0 + SKIP message); `km create` rejects them. `validate-all-profiles.sh` excludes `profiles/base/` automatically.
- **`execution.initCommandsAppend`** appends leaf-specific install steps after the merged `initCommands`; `initCommands` itself always replaces (union of all bases + child).
- **Diamond inheritance** is memoized (each node resolved once). Max depth: 10; cycles rejected.
- **v1 narrowing limitation:** a child CANNOT shrink a base's list (lists union). To use a narrower allowlist, compose from a narrower base or keep the field in-leaf. A `!replace` directive is a deferred v2 follow-up.
- **Bool zero-value trap:** fragments that write full blocks with non-pointer bools push their zero-values (`false`) onto children — keep mixed-bool blocks like `spec.runtime` in the leaf.
- Shipped fragment library: `profiles/base/{safenetwork,sidecars-all,observability-learn,budget-standard,artifacts-workspace,iam-us-east-1,agent-claude-all-tools,email-strict}.yaml`. `learn.v2.*` and `dc34.yaml` compose 6 of these (email-strict kept in-leaf per narrowing limitation).
- `km validate` and `km create` both resolve the full DAG and validate the **merged bytes** (not raw child bytes — child alone fails required-field checks for partial profiles).
- **Deploy = `make build` only** (operator binary). No Lambda rebuild, no schema-on-write DDB change, no `km init`. Existing sandboxes unaffected (inheritance is resolved at `km create` time, not baked into infra).
- See `OPERATOR-GUIDE.md` § Composable inheritance for the full authoring guide, worked dc34 example, and v1 limitation details.

## Releases

Tag-driven via goreleaser + GH Actions. The `VERSION` file is the dev-build counter (auto-bumped by every `make build`); git tags (`vX.Y.Z`) are the release identity.

**Artifacts produced per release:** four tarballs (`km_vX.Y.Z_{darwin,linux}_{amd64,arm64}.tar.xz`), each bundling `km` + `terraform` v1.9.8 + `terragrunt` v0.99.1 + `LICENSE` + `README.md` + `OPERATOR-GUIDE.md` + `THIRD-PARTY-LICENSES.txt`. Plus a SHA256 checksums file. Operators still provide `aws` CLI + `session-manager-plugin` themselves.

**Cut-a-release workflow:**

1. **Pre-flight:** verify `main` is green, GSD milestone is at a clean checkpoint, `CHANGELOG`-worthy commits use conventional-commit prefixes (`feat:`, `fix:`, `docs:` — goreleaser groups by these). **Update `docs/RELEASE-HIGHLIGHTS.md`** with this release's curated "✨ Major additions highlighted" items — auto-draft a starting point with `scripts/draft-release-highlights.sh` (pulls new phase blocks from CLAUDE.md since the last tag + lists non-phase feat/fix candidates), then trim/add emoji. It's injected verbatim into the release notes above the auto-changelog (workflow loads it → `$KM_RELEASE_HIGHLIGHTS` → `.goreleaser.yaml` `release.header`). The mechanism is always-on; a stale file = stale highlights. See `docs/release.md` § Release highlights.
2. **Local sanity check (no tag):**
   ```bash
   goreleaser check
   goreleaser release --snapshot --clean
   ls dist/ && tar -tJf dist/km_v*_darwin_arm64.tar.xz
   ```
3. **Tag and push:**
   ```bash
   git tag vX.Y.Z              # or vX.Y.Z-rc1 for prerelease (auto-flagged)
   git push origin vX.Y.Z
   ```
4. **GH Actions runs `.github/workflows/release.yml`** → cuts a **Draft** release. Review assets, then publish manually from the GH UI.
5. **Post-release:** bump the klanker plugin version (`plugin.json` + `marketplace.json`) in lockstep if any skill content changed — clients cache the old version otherwise (see [[project_plugin_version_gates_cache]]).

**Pinned bundled-tool versions:** `terraform` 1.9.8, `terragrunt` 0.99.1. Bumping these is a one-line edit to `.goreleaser.yaml` `before.hooks` args + the cache-key in the workflow.

**Files:** `.goreleaser.yaml` (release config), `scripts/fetch-bundled-tools.sh` (per-platform tool fetcher, cached at `~/.cache/km-bundle/`), `scripts/draft-release-highlights.sh` (auto-draft highlights from CLAUDE.md phase blocks since last tag), `docs/RELEASE-HIGHLIGHTS.md` (injected into release notes), `.github/workflows/release.yml` (tag-triggered).

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
