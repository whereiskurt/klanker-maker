# Operational Gotchas

Hard-won, non-obvious facts about building and operating klanker-maker — the kind
of thing that costs an hour the first time and ten seconds every time after. Curated
from accumulated session memory and pruned to durable engineering knowledge (stale
point-in-time phase notes are intentionally excluded).

> **Scope note.** These are *gotchas*, not reference docs. For the authoritative
> story on any subsystem see the `docs/` page it points to (`github-bridge.md`,
> `slack-notifications.md`, `desktop.md`, etc.) and `CLAUDE.md`. File:line
> references were accurate when written and drift over time — grep the symbol, not
> the line number.

---

## Deploy surface — Lambdas & `km init`

The single richest source of "code is green but nothing deployed" bugs. A
management Lambda only reaches AWS when **every** wiring point lines up.

### A new Lambda needs THREE wiring points, not one

Building the binary + zip target and the `infra/modules/<name>/vX/` TF module is
**not enough** — `km init` will silently never deploy it. Required:

1. **Makefile** `build-lambdas` target builds `build/<name>.zip`.
2. **TF module** `infra/modules/lambda-<name>/vX.Y.Z/` (main/variables/outputs).
3. **Live terragrunt unit** `infra/live/use1/lambda-<name>/terragrunt.hcl` — clone
   `lambda-slack-bridge/terragrunt.hcl`, adapt `dependency` blocks, `inputs`, the
   state-key path, and `terraform.source`. **Without this, terragrunt has nothing
   to apply.**
4. **`init.go` registration** — `km init` applies a *curated, ordered module list*
   in `internal/app/cmd/init.go` (NOT a directory scan). A module absent from this
   list is never applied even if its live unit exists.

This bit Phase 97: the `km-github-bridge` binary + zip + TF module existed but the
live unit and init.go entry were missing. `km init` ran clean yet created no
function (`aws lambda get-function-url-config --function-name km-github-bridge` →
`ResourceNotFoundException`).

**Verifier lesson:** for any "new Lambda/infra module" work, verification must grep
`infra/live/` (excluding `.terragrunt-cache`) for a real unit **and** grep
`init.go`'s module list — confirming `infra/modules/` has the source proves
nothing about reachability.

### `km init` always rebuilds zips — but only from a hardcoded list

`buildLambdaZips` (`init.go`) does `os.Remove(zipPath)` then rebuilds each zip from
current source, so editing a Lambda's Go and running `km init` **does** pick up the
change (no manual clean needed). The old skip-if-exists footgun is gone.

The footgun that remains: the list of Lambdas built is **hardcoded** in
`lambdaBuilds()` (exported for tests as `LambdaBuildNames()`). A Lambda with a live
terragrunt unit but missing from `lambdaBuilds()` is silently never built → its
unit fails at apply on `filebase64sha256(build/<name>.zip): no such file`. Keep it
in lockstep with the `build-lambdas` Makefile target (a `TestLambdaBuilds...` guard
pins membership).

Current zip list: ttl-handler, budget-enforcer, github-token-refresher,
email-create-handler, create-handler, km-slack-bridge, km-github-bridge.

- ttl-handler is a platform Lambda → live immediately after `km init`.
- budget-enforcer / github-token-refresher zips upload to the artifacts bucket →
  only **new** sandboxes (`km create`) pick up the change; existing sandboxes keep
  their old per-sandbox Lambda until recreated.

### `make build` ≠ `make build-lambdas`

`make build` builds only the `km` CLI binary; it produces **no** `build/*.zip`.
Before a real deploy you must run `make build-lambdas` (its `clean bump-version`
prereq wipes `build/` and rebuilds all zips), then `km init --dry-run=false`.

### `km init --lambdas` does NOT upload bridge code

Misleadingly named. It runs `buildLambdaZips` (cross-compiles into `build/*.zip`)
and `forceLambdaColdStart` (only touches the **create-handler** Lambda's
`TOOLCHAIN_VERSION` env var). It calls no `UpdateFunctionCode` API for the
slack-bridge or other Lambdas. The actual zip upload happens via each module's
`aws_lambda_function` (`source_code_hash = filebase64sha256(...)`), which only
fires under a **full `km init`**. For any bridge-only deploy use `make build &&
km init` (full), not `--lambdas`.

### `km init --github` / `--slack` / `--h1` do NOT rebuild the Lambda zip

Phase 105 adds sugar aliases (`--github`, `--slack`, `--h1`, `--email`) and the
generic `--only <module>` flag for a **scoped single-module apply** (env block + IAM
only). These are config-key-push shortcuts — they apply one terragrunt module in
seconds but do NOT call `buildLambdaZips` and do NOT upload new Lambda code.

When to use each path:

| Change | Path |
|--------|------|
| Config-key edit in `km-config.yaml` (env var only) | `km init --github/--slack/--h1 --dry-run=false` |
| Lambda code change (Go source in `pkg/github/`, etc.) | `make build-lambdas` + `km init --dry-run=false` |
| Schema field addition (SandboxProfile types.go) | `km init --sidecars` |
| New TF resource / table / queue | `km init --dry-run=false` (full) |

The no-drift invariant: the scoped apply derives from the same `km-config.yaml → KM_*
→ terragrunt` pipeline as a full apply, so a subsequent `km init --plan` shows the
targeted module as a no-op.

### Schema changes require `km init --sidecars`

Any change to the SandboxProfile schema (`pkg/profile/types.go` +
`sandbox_profile.schema.json`) must be followed by `km init --sidecars` before
`km create --remote` accepts profiles using the new fields. The
`km-create-handler` Lambda downloads `toolchain/km` from S3 at cold start and
re-validates the profile; a stale binary rejects the new key with
`ERROR: spec.X: additional properties 'Y' not allowed` and the DynamoDB record
sticks at `status: failed`. Local `./km validate` passing tells you nothing about
Lambda behavior.

- Diagnose: `aws logs tail /aws/lambda/km-create-handler --since 30m | grep <id>`.
- Recover: `km destroy <id> --remote --yes` to clear the stub, then re-create.

### Lambdas need `replace_triggered_by = [role]`

All management Lambda modules wire `lifecycle { replace_triggered_by =
[aws_iam_role.X] }`. Without it, when terragrunt recreates an IAM role the Lambda's
cached KMS grant on the AWS-managed `aws/lambda` key stays bound to the **old**
role's unique ID (`AROA…`). The Lambda keeps its name but loses decrypt ability —
cold starts fail silently with `KMSAccessDeniedException` before writing any logs.
Invocations succeed at the AWS API level (metrics show 0 errors) but the runtime
never starts; events pile up with no visible failure.

Debugging "Lambda doesn't fire": `aws kms list-grants --key-id alias/aws/lambda`
and look for grantee principals with unique-IDs (`AROA…`) rather than role ARNs.
If a role was recently replaced, only `aws lambda delete-function` +
`terragrunt apply` recovers a Lambda missing `replace_triggered_by`.

---

## Terragrunt & Terraform

### Any km command that runs terragrunt must call `ExportConfigEnvVars(cfg)` first

`site.hcl` reads `get_env("KM_RESOURCE_PREFIX", "km")` to construct the backend
state bucket name (`tf-{prefix}-state-{regionLabel}`). Without the export,
terragrunt resolves to the default `km` prefix and tries `tf-km-state-use1` — a
bucket that doesn't exist on a non-default-prefix install, manifesting as
`403 Forbidden` from HeadBucket during `terraform init`.

When adding/auditing any km subcommand that builds a `terragrunt.NewRunner(...)`,
grep for it in the command file and add `ExportConfigEnvVars(cfg)` right before the
runner is built. Already fixed in init.go, slack.go, uninit.go, destroy.go,
budget.go, create.go (local path). The remote-substrate path of `km create`
(Lambda dispatch) runs no terragrunt locally and doesn't need it.

### New modules must NOT declare `required_providers`

`infra/live/root.hcl`'s `generate "provider"` stanza writes a
`terraform { required_providers { ... } }` block into every module's working dir at
init time. A second `required_providers` block in module sources fails with "only
one required_providers block per module".

- If a new module needs a provider root.hcl doesn't declare (`tls`, `random`,
  `external`, …), add it to **root.hcl's generate block**, not the module.
- A module's `versions.tf`, if present, should carry only `required_version`.
- Smoke-test fixtures run outside terragrunt still work (Terraform infers providers
  from resource types).

### `dependency` blocks require `"show"` in the mock-commands whitelist

Any `terragrunt.hcl` with `dependency` blocks **must** include `"show"` in
`mock_outputs_allowed_terraform_commands`. The destroy-class plan gate
(`pkg/terragrunt/planreport/`) runs `terragrunt show -json <planfile>` after every
plan. On a fresh install, dependency `outputs.json` doesn't exist yet so terragrunt
falls back to mocks; without `"show"` whitelisted the fallback is rejected and HCL
re-evaluation fails with `Unknown variable; There is no variable named
"dependency"`. The gate then conservative-trips on phantom destroys and blocks
`km init --plan`. Write the whitelist as
`["validate", "plan", "destroy", "init", "apply", "show"]`.

### Provider lock drift after a root.hcl provider change

Adding a provider to `root.hcl`'s `required_providers` (e.g. the Phase 80 `tls`
addition) regenerates every module's `provider.tf` to list it, but each committed
`.terraform.lock.hcl` still lacks it. First `terragrunt apply` after the change
errors with `Inconsistent dependency lock file … required by this configuration but
no version is selected`. Remediate (with `AWS_PROFILE` exported):

```bash
cd infra/live/use1
terragrunt run --all --queue-exclude-dir 'sandboxes/**' init -- -upgrade
git add infra/live/use1/*/.terraform.lock.hcl
git commit -m "chore(infra): refresh regional lock files for new provider"
```

The `--queue-exclude-dir 'sandboxes/**'` skip is essential — per-sandbox modules
need IMDS credentials for their S3 backends and fail with "no EC2 IMDS role found"
on a laptop shell. They're frozen post-creation and don't need the upgrade.

### Plugin-cache checksum errors on operator workstations

`km init --dry-run=false` can abort mid-apply with `Required plugins are not
installed … the cached package for hashicorp/aws X (in .terraform/providers) does
not match any of the checksums recorded in the dependency lock file`.

Root cause: `~/.terraformrc` sets `plugin_cache_dir`. If the cache once held a
stale/non-canonical extraction of a provider, lock-regen at that moment recorded
the bogus `h1:` hash into every (gitignored, per-machine) lock. When the cache
later self-heals to the canonical hash, all locks are orphaned. `terraform init` is
lenient; `terraform apply` (what km runs) is strict → reject.

Two traps when fixing:
1. `init -upgrade` is the **wrong** heal — it can record a registry/foreign hash,
   not the locally-installed dirhash that `apply` verifies.
2. `terragrunt run --all init` is **parallel** and Terraform doesn't lock the
   shared plugin cache → concurrent inits race it → transient checksum errors.
   `km init` itself is **serial** (ranges modules in dependency order) so it never
   races — only `run --all` does.

Correct remediation: purge the suspect provider version from the plugin cache so it
re-extracts canonically, delete stale locks, then regenerate **serially**
(`terragrunt run --all --parallelism 1 … init`), then re-run `km init`.

---

## Per-sandbox state (DynamoDB)

### New per-sandbox DDB attributes must round-trip through the struct

`km resume` (TTL recreation), `km at extend`, and the ttl-handler Lambda all do a
read-modify-write on the `km-sandboxes` row: `ReadSandboxMetadataDynamo` → mutate →
`WriteSandboxMetadataDynamo` (a **full-row PutItem** built by `marshalSandboxItem`).
Any DDB attribute not carried by the `SandboxMetadata` struct +
`marshalSandboxItem` + `unmarshalSlackFields` is silently dropped on the next
lifecycle write.

This bit `slack_inbound_queue_url`, and again the per-sandbox `slack_mention_only` /
`slack_react_always` overrides: a sandbox with `inbound.mentionOnly: true` reverted
to install defaults after a pause/resume cycle, because the bridge reads "attribute
absent" as "fall back to install default".

**Rule:** when adding a per-sandbox DDB attribute, add it to the struct
(`pkg/aws/metadata.go`) **and** `marshalSandboxItem` / `unmarshalSlackFields`
(`pkg/aws/sandbox_dynamo.go`). Tri-state `*bool` overrides follow the
`slack_archive_on_destroy` pattern (write BOOL when non-nil, omit when nil). Prefer
a targeted `UpdateItem` (like `UpdateSandboxTTLDynamo`) over a full-row rewrite when
only one attribute changes.

---

## Config & CLI

### New `km-config.yaml` keys need a THIRD edit — the merge-list

`config.Load()` (`internal/app/config/config.go`) reads `km-config.yaml` into a
second viper instance `v2`, then merges only an **explicit allow-list** of keys into
the primary `v`. The struct is built from `v`. So adding a new config key needs
three edits, not two:

1. Struct field (+ getter) in config.go.
2. Construction line `Field: v.GetX("my_key")` in the cfg literal.
3. **Add `"my_key"` to the v2→v merge-list** (near `doctor_stale_ami_days`).

Miss step 3 and the YAML value never reaches `v`, so the getter returns empty even
though the file has it — and a file-override test fails with the field unset.
`v.SetDefault` alone does **not** surface file values; the merge-list is the gate.
Keys are flat snake_case unless mirroring the nested `slack.mention_only` style
(those nested keys are also in the merge-list).

---

## Teardown & orphan resources

### Teardown is two-layer — uninit before unbootstrap

km install is two stages, and teardown mirrors them:

- **Foundation:** `km bootstrap` creates SCP, artifacts bucket, platform KMS key +
  alias, operator identity. Teardown: `km unbootstrap` — removes SSM params under
  `/{prefix}/`, artifacts + terraform-state S3 buckets, schedules the platform KMS
  key for deletion, optionally the Route53 zone with `--include-zone`.
- **Regional:** `km init` applies terragrunt for all modules. Teardown: `km uninit`
  — destroys modules in reverse-dependency order plus the ECR repos.

```bash
./km uninit --region=us-east-1 --force --yes      # regional FIRST
./km unbootstrap --region=us-east-1 --yes         # then foundation
```

Order matters: the state bucket (deleted by unbootstrap) holds active state for
regional modules that uninit needs to read. KMS deletion is staged (7-day minimum);
the alias is removed immediately so re-bootstrap doesn't trip on a pending alias.

### `km doctor` orphans = remote-vs-local teardown asymmetry

`km doctor`'s stale/orphan checks are a continuous audit of the gap between what
`km create` makes and what `km destroy` deletes. Orphans are a deterministic
per-cycle residue (N create/kill cycles ≈ N copies of each leak). Root causes:

1. **Remote ≠ local teardown.** `km destroy` defaults `--remote=true` → teardown
   runs in the TTL Lambda (`cmd/ttl-handler/main.go`), a strict **subset** of the
   local `runDestroy` (`internal/app/cmd/destroy.go`). The Lambda omits
   `CleanupSandboxEmail` (leaks per-sandbox SES identity) and `drainSlackInbound`
   (leaks SQS FIFO queue + `km-slack-threads` rows + queue-url SSM param). Local
   `--remote=false` does both.
2. **No teardown anywhere** for `km-budgets` rows, `agent-runs/` S3, `artifacts/{id}/`
   S3 (the S3 lifecycle rule only covers `slack-inbound/` + `sandboxes/`).
3. **Hardcoded `km-` literals in Lambda Go SDK teardown** broke non-default
   `resource_prefix` installs (fixed to use `resourcePrefix()`). The module-hygiene
   test (`pkg/terragrunt/modulehygiene_test.go`) audits literal `km-` in `*.tf`
   **only** — it does not cover Go binaries, so Go-side hardcoded `km-` in
   resource-name construction is the prime suspect when a non-default-prefix install
   leaks.

### `teardownPolicy: stop` should preserve the DDB record

Known design gap: when `teardownPolicy: stop`, TTL expiry currently stops the EC2
instance **and** removes the DynamoDB record, leaving orphaned resources (stopped
EC2, stale IAM roles/KMS keys/schedules) that can't be resumed because the record is
gone and the sandbox is invisible to `km list`. Intended behavior: on `stop`, stop
the instance and set `status: stopped` but keep the record (and clear/extend the TTL
attribute); delete only on explicit `km destroy`.

---

## Sandbox runtime & OS bootstrap

### Ubuntu userdata constraints (vs Amazon Linux)

The EC2 userdata bootstrap (`pkg/compiler/userdata.go` + the remote-create stub in
`pkg/compiler/compiler.go`) was historically Amazon-Linux-only and was made
OS-aware for Ubuntu (Phase 93 `km desktop` — KasmVNC ships Ubuntu builds). The
non-obvious constraints, each discovered one boot at a time:

- **The sandbox SG allows only 443/tcp + 53/udp egress — no port 80.** On Ubuntu,
  `apt` must use HTTPS: rewrite sources `http://`→`https://`, and set
  `Acquire::ForceIPv4` (the EC2 mirror advertises unroutable IPv6). The whole
  userdata runs under `set -euo pipefail`, so **one** `apt-get update` timing out on
  `:80` aborts the **entire** bootstrap — silently killing everything downstream,
  not just the one package. This bites any third-party apt repo a feature adds (a
  Chrome `deb http://dl.google.com/...` source killed the whole desktop bootstrap).
  Rule: every apt source line emitted into userdata must be `https://`; check any
  new browser/tool repo.
- **Ubuntu base AMI lacks the AWS CLI and `unzip`.** The remote-create stub does
  `aws s3 cp` → install AWS CLI v2 first and extract its zip with **`python3`**
  (present on both OSes), not `unzip` (absent; apt is dpkg-lock-contended at early
  boot).
- **SSH unit is `ssh.service`** on Ubuntu (`sshd.service` on AL); openssh-server may
  be absent.
- `fonts-dejavu` → use `fonts-dejavu-core` on noble. `software-properties-common`,
  `gnupg`, `curl` are not preinstalled.
- `amazon-ssm-agent` is preinstalled via **snap** on Canonical EC2 images (already
  running — that's how SSM reaches the box); don't reinstall under the AL unit name.
- **Privileged profiles:** the admin group is `wheel` on AL/RHEL but `sudo` on
  Ubuntu/Debian. `useradd -G wheel sandbox` fails on Ubuntu; with `|| true` the
  sandbox user is silently never created → the next `chown sandbox:sandbox
  /workspace` aborts cloud-init at ~20s and nothing downstream runs. Fix: `useradd`
  first, then `usermod -aG wheel sandbox || usermod -aG sudo sandbox`.

Helpers in the userdata prelude: `km_pkg_install` (dnf/yum/apt), `km_ensure_awscli`
(python3 extract), `km_apt_https`.

KasmVNC desktop quirks (all in the `spec.runtime.desktop` block): `vncserver` needs
`-select-de manual` (else it prompts for a DE and exits 2); Xvnc needs a readable
TLS cert even with `require_ssl:false` (generate snakeoil + add `sandbox` to the
`ssl-cert` group); set `kasmvnc.yaml network.udp.public_ip: 127.0.0.1` to skip a
~70s STUN hang; the systemd unit must create `/run/user/<uid>` via
`ExecStartPre=+…` (root) since the sandbox user has no logind session. KasmVNC binds
loopback `127.0.0.1:8444` — access only via `km desktop start`'s SSM port-forward.

Firefox must be the **Mozilla PPA deb**, never the snap (snap Firefox refuses to
launch under the kasmvnc systemd cgroup). On noble the archive `firefox` is a
snap-transitional deb with an **epoch** (`1:1snap1`) that outranks the PPA at equal
priority, and `apt -t 'o=LP-PPA-mozillateam'` is a no-op (target-release matches
suites, not origins) — so a naive install silently gets the snap. Fix: write
`/etc/apt/preferences.d/mozilla-firefox` pinning `o=LP-PPA-mozillateam` at
`Pin-Priority: 1001`, then `apt-get install -y --allow-downgrades firefox` (the
`--allow-downgrades` is required — `-y` alone refuses the epoch downgrade), then
`snap remove firefox`.

Browser rendering needs two more things or you get a **black desktop**: (1) set
`kernel.apparmor_restrict_unprivileged_userns=0` (noble defaults it to 1, which
breaks the browser content-sandbox), proc-path-guarded; (2) `~/.config` must be
owned by the sandbox user (the desktop block's `mkdir` runs as root; if `~/.config`
stays root-owned Firefox can't create `~/.config/mozilla` → "Your profile cannot be
loaded"). "kasmvnc active + :8444 bound" does **not** mean the browser renders —
verify the actual screen, or you'll miss a black-screen/crash.

The desktop browser escapes network enforcement by default: the eBPF cgroup
programs attach to `km.slice/km-{id}.scope` and only the sandbox **shell** pid is
enrolled there; the browser runs under `system.slice/kasmvnc.service` and is never
seen by connect4/egress. And `enforcement: both` emits no transparent iptables DNAT
(that's `proxy`-mode only), so nothing routes the browser to the MITM proxy; even in
`proxy` mode Firefox rejects the MITM cert (own NSS store). Full-parity fix:
`~/.vnc/xstartup` writes its pid into the enforced cgroup's `cgroup.procs` before
launching the WM/browser so children inherit enforcement; and for firefox +
`(proxy|both)`, write `/etc/firefox/policies/policies.json` with the proxy +
`Certificates.ImportEnterpriseRoots:true` + the km-proxy CA. (Firefox-only so far;
Chromium/Chrome/Brave not yet MITM-wired.)

eBPF on Ubuntu: the enforcer is portable (embedded bpf2go bytecode, no
clang/CO-RE/libbpf) but its DNS resolver binds `127.0.0.1:53`, colliding with
Ubuntu's **systemd-resolved**. The eBPF userdata block stops+disables
systemd-resolved and breaks the `/etc/resolv.conf` symlink before the enforcer
starts (no-op on AL). x86_64 only (enforcer is amd64-build-tagged).

Remote-create desktop credentials: km uploads `desktop-creds.txt` to
`remote-create/<id>/` and `GenerateDesktopCredential` honors
`KM_DESKTOP_KASM_USER/PASS`, but the create-handler Lambda
(`cmd/create-handler/main.go`) must **read** that artifact and **export** those env
vars before the km subprocess (mirrors `vscode-pubkey.txt` →
`KM_VSCODE_SSH_PUBKEY`). Otherwise the Lambda's km regenerates a fresh password into
the box's `~/.kasmpasswd`, mismatching the operator's local copy → KasmVNC login
401s. Immediate recovery: `km desktop rekey <id>`.

**Deploy note:** the create-handler Lambda compiles the userdata as a subprocess, so
compiler changes need a Lambda redeploy (`make build-lambdas` clean +
`km init --dry-run=false`) for **remote** create. Local create exercises the same
stub on the real EC2 with the local binary.

### Docker substrate STS credentials expire after ~1h

The Docker substrate injects STS temporary credentials at create time (1h TTL). The
cred-refresh sidecar can't renew them because it holds only the same short-lived
tokens (no instance role, unlike EC2/ECS). Fix: mount the operator's `~/.aws/` into
the cred-refresh container so it can use the host SSO session to refresh scoped
credentials. Until then, Docker sandboxes work for ~1h before needing
`km destroy && km create` (~15s).

### Claude Code OAuth needs two files on a fresh sandbox

`claude auth login --claudeai` writes OAuth tokens to `~/.claude/.credentials.json`
but does **not** mark Claude Code's first-launch wizard complete. The interactive
REPL then ignores the OAuth tokens, re-runs the wizard, and overwrites the
credentials. The wizard gates on two keys in `~/.claude.json`:
`hasCompletedOnboarding: true` and `lastOnboardingVersion: "<claude --version>"`.
`km agent auth --claude` writes both via SSM after login succeeds. If the wizard
reappears after a `claude` self-update (version mismatch on `lastOnboardingVersion`),
or after claude adds a new gating key, re-run a before/after diff of
`~/.claude.json` to find the new key.

---

## Plugin distribution (klanker plugin)

### The version field gates client-side cache invalidation

Skills under `skills/<name>/SKILL.md` are auto-discovered (no enumeration in
plugin.json), but the **`version` field in both `.claude-plugin/plugin.json` and
`.claude-plugin/marketplace.json`** gates cache invalidation. Add a new skill
without bumping version and clients running `/plugin update` keep the cached old copy
and never see it (cache lives at
`~/.claude/plugins/cache/<marketplace>/<plugin>/<version>/`). Whenever
`skills/`, `commands/`, `hooks/`, or any plugin component changes, bump `version` in
**both** files (plugin.json takes priority; keep them in sync), then `/plugin
update`.

### Pre-seeding plugin files doesn't fully register the plugin

Seeding `known_marketplaces.json`, `installed_plugins.json`, and git-cloning the
plugin cache during sandbox create is not enough — Claude Code writes additional
internal state during `/plugin install` that the seed doesn't replicate, so the user
still has to run `/plugin install klanker` manually. To close this: on a working
sandbox after `/plugin install`, diff `~/.claude/plugins/` to find the missing
files/state and replicate them in `configFiles`/`initCommands`.

---

## DNS & accounts

### Multi-account DNS delegation topology

DNS delegation is conditional on account layout:

- **Management account ≠ application account:** management owns the apex Route53
  zone; the application account needs its own zone created first; management gets NS
  delegation records pointing at the application zone. (This is how the primary
  install is set up.)
- **Management account = application account:** no delegation — single zone.
  `km configure` should detect this and skip delegation.

The three-account model (management / terraform / application) often collapses to two
in practice (terraform and application are frequently the same). Config must handle
both 2- and 3-account topologies.

### AWS profile selection for the default install

For the default `km` install (resource_prefix `km`), AWS operations run against the
**Application account** — export `AWS_PROFILE=<install>-application` (e.g. the SSO
profile set `<install>-management` / `<install>-application` / `<install>-terraform`;
a separate set exists per sibling install). A bare `aws`/`km` call with no
`AWS_PROFILE` finds no credentials. For terragrunt-driving km commands also
`eval $(km env)` first (see the env-export gotcha above); the terraform cross-account
role uses the `-terraform` profile.
