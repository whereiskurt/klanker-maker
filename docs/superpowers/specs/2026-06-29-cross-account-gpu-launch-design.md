# Cross-Account Capacity Borrowing (GPU-motivated) — Design Spec

**Date:** 2026-06-29
**Status:** Draft for review
**Author:** brainstorming session (operator: whereiskurt@gmail.com)
**Scope:** Let specific SandboxProfiles launch their EC2 box into a *different* AWS account — to **borrow that account's vCPU quota / capacity** — via a pre-provisioned, tightly-bounded launcher role. Single km control plane stays home. GPU is the motivating first use; the mechanism is instance-type-agnostic.

## Positioning — this borrows capacity, it does not stand up a control plane

The point of this feature is **access to vCPU quota capacity that lives in another account**, *without* the cost of a full second install there. A full `km init` in account B would buy a whole second control plane (its own DynamoDB tables, management Lambdas, bridges, SES, network, `km doctor` surface) plus two installs to operate and a second pane of glass — an enormous amount of machinery when all you want is the quota. The launcher shim gets you the capacity with **one bounded role + a results bucket + (optional) network/EFS**, while `km list`, budget, and everything else stay on your one home control plane.

Nothing here is GPU-specific: the launcher's `--instance-types` allowlist is the *only* thing scoping it to GPU families. Point it at `c7i`/`m7i`/etc. and the identical mechanism borrows **general vCPU pockets**. GPU is wave-1's motivating case because that's where scarcity bites. The model generalizes to **multiple registered capacity accounts** (`mgmt-gpu`, `prod-spare`, …), each a borrowable pocket targeted per-profile, all visible in one `km list`; a future `km capacity` could survey headroom across all of them.

---

## Problem

The operator's GPU/EC2 capacity lives in the **org management account** (call it **account B**), not the **application account** (**account A**) where km launches sandboxes today. Two hard facts shape the design:

1. **Sandboxes launch into whatever `--aws-profile` resolves to.** The generated sandbox AWS provider (`infra/live/root.hcl:46-55`) has **no `assume_role` block** — it uses ambient credentials. `accounts.application` is only string-interpolated into ARNs, never used to target the provider. (recon: `pkg/terragrunt/runner.go:420`, `internal/app/cmd/create.go:302-303`.)
2. **The management account is SCP-exempt.** AWS Service Control Policies do **not** apply to the org management account. So the blast-radius containment km normally gets from its SCP guardrails (`infra/modules/scp`) is **unavailable** in B. Containment must live entirely in the IAM role we pre-deploy in B.

A secondary, already-known footgun: the Phase-124 capacity/quota check (`pkg/capacity/RankAZs`, GPU quota `L-DB2E81BA`) runs in the **same account as the launch** because its clients all derive from one `awsCfg` (`internal/app/cmd/create.go:802-814`, `capacity.go:122-173`). If capacity lives in B but the check runs in A, it checks the **wrong account** — exactly the warning in `CLAUDE.md:13,25`.

## Non-goals (YAGNI)

- **Multi-region.** Explicitly a separate, larger project (config `regions` list, per-region init loop, region-scoped DynamoDB names, per-region Lambda deploy). Not in this spec.
- **Full-featured cross-account sandboxes.** GPU boxes in B are **lean model-serving boxes** (Phase 122 usage: reach via SSM port-forward / `km model start` / VS Code Continue / Slack codex). We do **not** wire home-account budget metering, Slack, or email into B in this phase. They can be added later, per-feature, if a real need appears.
- **Go-side AssumeRole launch.** The launch stays terragrunt-driven; cross-account targeting is a generated provider `assume_role` block (the proven SCP pattern), not a net-new Go `RunInstances`.
- **A second km install in B.** The obvious alternative — `km init`/`make` a full install in B — is **deliberately rejected**: it solves the same problem by standing up an entire second control plane (tables, Lambdas, bridges, SES, doctor surface) and a second pane of glass, when the actual want is *just the vCPU quota*. The launcher shim is the right-sized tool: borrow capacity, keep one control plane. Revisit only if the lean cross-account surface proves insufficient.
- **General multi-account capacity survey / scheduling.** v1 ships the *mechanism* (register N accounts, target per-profile). A cross-account `km capacity` survey and any automatic "pick whichever account has headroom" scheduling are natural follow-ons, not in this spec.

## Guiding principle

**You deploy the door; A only knocks.** Account A holds *no* standing power in B. The only thing that lets A create anything in B is a launcher role you provision yourself, up front, scoped so tightly it can build exactly one box-shaped thing and nothing else. This is the cluster-enrollment model (`km cluster add`) applied to "lend me your capacity account."

---

## Architecture

### Component map

| Unit | Lives in | Provisioned by | Purpose |
|---|---|---|---|
| **Box instance role** `{prefix}-gpu-box` | B | `km account add` (B creds) | The EC2 instance profile the GPU box runs under. Pre-baked + permissions-boundaried so the launcher never needs `iam:CreateRole`. |
| **Permissions boundary** `{prefix}-gpu-box-boundary` | B | `km account add` (B creds) | Caps the box role: it can never escalate beyond its declared runtime perms. |
| **Launcher role** `{prefix}-gpu-launcher` | B | `km account add` (B creds) | The single cross-account door. Trusts A's create principal + ExternalId. Its policy IS the containment. |
| **Minimal network** (VPC/subnet/SG) | B | `km account add` (B creds) `--provision-network`, or `--subnet`/`--sg` to reuse | Where the box lands. Lean by default: egress-only for the allowlist, no EFS. `--provision-efs` adds the EFS below. |
| **B results bucket** `{prefix}-results-{B-account}` | B | `km account add` (B creds) | The B-box **durable, cross-account-readable** write target (results, execution output, `km rsync save`). Bucket-owner-enforced. Policy: B box role `Get/Put/List`; A principals `Get/List` (cross-account **read**). The egress path for results A needs. |
| **B-local EFS** (optional) | B | `km account add --provision-efs` (B creds) | **Live shared POSIX filesystem, B-internal only** — mounted concurrently by B boxes for a shared `HF_HOME`/model-weights cache (download 70B once, mount fleet-wide) and shared scratch. Mount targets in B's subnet + NFS(2049) ingress on the box SG. **Not** cross-account (A reads results via the bucket, not EFS). Off by default — earns its keep with a fleet. |
| **Account link record** | A (`km-config.yaml`) | `km account register` (A creds) | Records launcher ARN, ExternalId, subnet, SG, region, **results-bucket name**, **EFS id (if any)** for the linked account; keyed by a short name (e.g. `mgmt-gpu`). |
| **Artifacts read grant** | A | `km account register` (A creds) | The one home-side grant: lets B's box role **read** `s3://{artifacts}` for sidecars/toolchain. Read-only. |
| **Profile field** `spec.runtime.launchAccount` | profile YAML | operator | Opt-in: names the linked account. Absent ⇒ home account ⇒ byte-identical to today. |

### Data / control flow

```
operator (B creds)            operator (A creds)                 km create (A creds)
─────────────────             ─────────────────                 ───────────────────
km account add        ──►     km account register      ──►      profile has launchAccount: mgmt-gpu
  provisions in B:              writes link to                     │
   • gpu-box role +             km-config.yaml                     ▼
     boundary                   adds S3 bucket-policy        assume {prefix}-gpu-launcher (ExternalId)
   • gpu-launcher               stmt for gpu-box role              │
     (trust A + ExternalId)                                        ▼
   • VPC/subnet/SG                                          terragrunt provider assume_role ─► RunInstances in B
   • prints wiring ─────────────► (paste/auto-write)               │
                                                                   ▼
                                                            capacity/quota check uses
                                                            SECOND assumed-role awsCfg (account B)
```

State always lives in **A's** home S3 backend (`infra/live/root.hcl:8-23`) — terragrunt uses A creds for the backend and the assumed-role creds only for the AWS provider. This is exactly how the SCP stack already operates cross-account (`infra/live/management/scp/terragrunt.hcl:28-60`).

---

## The IAM containment (the part to scrutinize)

These are the durable artifacts you deploy in B. They are the entire security boundary.

### Launcher role trust policy (`{prefix}-gpu-launcher`)

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": { "AWS": "arn:aws:iam::<A>:role/<create-handler-or-operator>" },
    "Action": "sts:AssumeRole",
    "Condition": { "StringEquals": { "sts:ExternalId": "<generated-secret>" } }
  }]
}
```

Trusts **only** A's named create principal (the `*-create-handler` Lambda role for remote create, and/or the operator SSO role for local create), gated by an ExternalId minted at enrollment.

### Launcher role permission policy — this is the door's shape

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "LaunchOnlyGpuBoxes",
      "Effect": "Allow",
      "Action": "ec2:RunInstances",
      "Resource": "arn:aws:ec2:<region>:<B>:instance/*",
      "Condition": {
        "StringEquals": {
          "ec2:InstanceType": ["g6e.12xlarge","g6e.48xlarge","g6.12xlarge", "..."],
          "aws:RequestTag/km:managed-by": "klankermaker"
        },
        "ArnEquals":  { "ec2:Subnet": "arn:aws:ec2:<region>:<B>:subnet/<km-subnet>" },
        "StringLike": { "aws:RequestTag/km:sandbox-id": "*" }
      }
    },
    { "Sid": "SupportingRunInstancesResources", "Effect": "Allow", "Action": "ec2:RunInstances",
      "Resource": ["...volume/*","...network-interface/*","...security-group/<km-sg>","...subnet/<km-subnet>","...image/*","...key-pair/*"] },
    { "Sid": "PassOnlyBoxRole", "Effect": "Allow", "Action": "iam:PassRole",
      "Resource": "arn:aws:iam::<B>:role/{prefix}-gpu-box",
      "Condition": { "StringEquals": { "iam:PassedToService": "ec2.amazonaws.com" } } },
    { "Sid": "LifecycleTaggedOnly", "Effect": "Allow",
      "Action": ["ec2:CreateTags","ec2:TerminateInstances","ec2:StopInstances","ec2:StartInstances","ec2:CreateVolume","ec2:AttachVolume","ec2:DeleteVolume"],
      "Resource": "*",
      "Condition": { "StringEquals": { "aws:ResourceTag/km:managed-by": "klankermaker" } } },
    { "Sid": "ReadOnlyForCapacityCheck", "Effect": "Allow",
      "Action": ["ec2:Describe*","servicequotas:GetServiceQuota","servicequotas:ListServiceQuotas"],
      "Resource": "*" }
  ]
}
```

**What this role can do:** launch a GPU-family instance, of an approved type, into the one km subnet, carrying the required tags, with only the pre-baked box role attached; tag/stop/start/terminate only km-tagged resources; create/attach the `/data` EBS; read EC2/quota info. **What it cannot do:** create IAM, touch untagged or non-GPU resources, read org/Organizations APIs, reach other accounts, or launch anywhere but the km subnet. Even compromised, the worst case is "a tagged GPU box appears in one subnet."

### Box role + boundary (`{prefix}-gpu-box`)

The box's runtime perms — kept minimal for a lean serve box:
- **SSM** managed-instance core (shell, port-forward, `km model start`).
- **S3 read** on `arn:aws:s3:::{artifacts}/*` in A (sidecars/toolchain — the one cross-account dependency, read-only).
- **S3 read/write** on the B-local `{prefix}-results-{B-account}` bucket (same-account write target for results/execution/rsync — substitutes for the EFS B doesn't have).
- Read its own instance tags / metadata.
- **Optional, enrollment-gated:** `bedrock:InvokeModel` against B's Bedrock when `km account add --enable-bedrock` is set (Q4) — off by default.
- *(deferred, opt-in later)* home DynamoDB budget writes, SES, Slack.

All wrapped in `{prefix}-gpu-box-boundary` so even if the box role's policy is later widened by mistake, the boundary caps effective permissions.

### The one home-side grant (`km account register`, A creds) — READ-ONLY

Append to the artifacts bucket policy in A:
```json
{ "Sid": "AllowGpuBoxReadArtifacts-<linkname>", "Effect": "Allow",
  "Principal": { "AWS": "arn:aws:iam::<B>:role/{prefix}-gpu-box" },
  "Action": "s3:GetObject", "Resource": "arn:aws:s3:::{artifacts}/*" }
```
That is the **entire** cross-account data-plane surface for a lean serve box. **`GetObject` only — no `PutObject`.**

### Storage model — the no-cross-account-write invariant

B boxes have **no EFS / shared persistent storage** (the lean network in B skips EFS by design). So a **B-local results bucket** is the persistence substitute. This yields a clean, symmetric boundary:

> **No cross-account writes, ever. Each account writes only its own buckets; each reads the other read-only.**

| Direction | Access | What |
|---|---|---|
| **B → A** | read-only | B box fetches sidecars / `toolchain/km` from A's artifacts bucket |
| **B → B** | read-write (**same-account**) | B box writes results / execution output / `km rsync save` to `{prefix}-results-{B-account}` |
| **A → B** | read-only | A klankers (operator + A instance roles) read B-box results from B's bucket via B's bucket policy |

A compromised box on either side can only ever *read* across the boundary — never write. The rendered profile `artifacts/{id}/.km-profile.yaml` is still written **to A by the A-side create process** (A creds); the box gets its own copy from the userdata heredoc `/opt/km/.km-profile.yaml` (Phase 113), so even sandbox metadata stays single-pane in A with no B write.

**B results bucket** (`km account add`, B creds): `{prefix}-results-{B-account}`, bucket-owner-enforced (ACLs disabled — access is policy-only). Bucket policy: B box role `s3:Get/Put/List`; A's principals `s3:Get/List` (cross-account read — no assume-role needed, plain bucket-policy cross-account GET). `km account register` records the bucket name into the link so `km rsync load` / `km agent results` resolve B-launched sandboxes to it.

**B-local EFS (optional, `--provision-efs`):** a *live shared POSIX filesystem* for the B fleet — the complement to the bucket, not a replacement. The bucket is durable cross-account egress; EFS is fast B-internal shared working storage. Primary use: a shared `HF_HOME`/weights cache so a multi-box GPU fleet downloads 70B weights **once** and mounts them everywhere (today Phase 122 re-caches per box on EBS). EFS **never crosses the account boundary** — it's mounted only by B boxes in B's VPC, so it doesn't affect the no-cross-account-write invariant at all (A still reads results via the bucket). Reuses the existing `spec.runtime.mountEFS` field: for a B-launched box the EFS id resolves from the **link record** instead of home's `efs/outputs.json`. Off by default (cost + only worthwhile for a fleet); weight-load-from-EFS vs local-EBS throughput is the operator's tradeoff.

**Still B-local, still deferred to the "full sandbox in B" path:** `km logs` for B boxes — the audit sidecar ships to **B's** CloudWatch, not A's (an observability question, not a storage one).

---

## CLI surface

Mirrors `km cluster {add,list,rm}` (cross-account enrollment precedent: `internal/app/cmd/cluster.go`).

```
km account add <name> --trust <A-account-id> \
    [--region us-east-1] \
    [--provision-network | --subnet <id> --sg <id>] \
    [--provision-efs] \                         # optional B-local shared EFS (weights cache / scratch); off by default
    [--instance-types g6e.12xlarge,g6e.48xlarge,...] \
    [--enable-bedrock] \                        # grant box role bedrock:InvokeModel in B + enable model access (Q4)
    [--external-id <secret> | --sops <file>]    # run with B admin creds (--aws-profile <B>)
                                                # ExternalId auto-generated if neither given
# Provisions launcher role + box role + boundary + (optional) network + results bucket in B.
# Prints the link block + writes a local fragment for `km account register`.

km account register <name> --launcher-arn <arn> --external-id <secret> \
    --subnet <id> --sg <id> --region <r> --box-role-arn <arn>    # run with A creds
# Records the link in km-config.yaml and adds the artifacts S3 bucket-policy grant.

km account list                     # show linked launch accounts (name, account id, region, subnet)
km account rm <name>                # tear down B-side roles/network (B creds) + remove link (A creds)
```

Naming alternatives considered: `km capacity account add` (collides conceptually with the existing `km capacity` feasibility command), `km cluster`-style `km launch-account`. **`km account`** chosen for brevity + the `add/list/rm` symmetry with `km cluster`.

### Credential model (which account each command authenticates as)

`--aws-profile` is the account selector — the established km pattern (`km cluster add` already takes `--aws-profile` for its target; km injects it as `AWS_PROFILE` into the terragrunt subprocess at `runner.go:420`, overriding any ambient shell `AWS_PROFILE`).

| Command | Authenticates as | How the *other* account is referenced |
|---|---|---|
| `km account add` | **Account B** (`--aws-profile <B-admin>`) — it provisions roles/network *in* B | Account A by **string only**: `--trust <A-account-id>` baked into the launcher trust policy. No A creds needed. |
| `km account register` | **Account A** (`--aws-profile klanker-application` / your A default) — writes the link + S3 grant in A | Account B by string (launcher ARN, box-role ARN, subnet/SG) from the `add` output. |
| `km create … launchAccount:` | **Account A** only — state, DynamoDB, artifacts are all home | Account B reached *inside terragrunt* via the generated provider `assume_role` → launcher role. **You never hold standing B creds at launch.** |

This split is the security property in action: B credentials are used **once, by you, at enrollment**; every launch thereafter runs as A and can only knock on the one bounded launcher door.

`km-config.yaml` gains (must be added to the v2→v merge-list in `config.Load()`, per `[[project_config_key_merge_list]]`):
```yaml
launch_accounts:
  mgmt-gpu:
    account_id: "481723467561"
    launcher_role_arn: "arn:aws:iam::481723467561:role/km-gpu-launcher"
    box_role_arn:      "arn:aws:iam::481723467561:role/km-gpu-box"
    external_id_ssm:   "/km/launch-accounts/mgmt-gpu/external-id"   # SSM SecureString path; never plaintext
    region:            us-east-1
    subnet_id:         subnet-xxxx
    security_group_id: sg-xxxx
    results_bucket:    km-results-481723467561   # B-local write target; A reads it cross-account
    efs_id:            fs-0abc...                # optional; only if enrolled with --provision-efs
```

## Profile surface

New field on `RuntimeSpec` (`pkg/profile/types.go:465`):
```go
// LaunchAccount names a linked launch account (km-config.yaml launch_accounts.<name>)
// to provision this sandbox's EC2 into a different AWS account via a pre-provisioned
// bounded launcher role. Empty ⇒ home application account (byte-identical to today).
LaunchAccount string `yaml:"launchAccount,omitempty" json:"launchAccount,omitempty"`
```
Usage:
```yaml
spec:
  runtime:
    instanceType: g6e.12xlarge
    region: us-east-1
    launchAccount: mgmt-gpu     # ← opt-in; absent = home account
```
`km validate` rejects `launchAccount` referencing an unknown link, and warns if set on a non-GPU instance type (the launcher only permits GPU families). Added to the JSON schema (`additionalProperties:false`).

### Selecting the launch account at create time

**Profile-primary + CLI override** (idiomatic with km's existing `--no-bedrock`/`--on-demand` create flags):

- The profile field is the **default** — GPU profiles carry `launchAccount: mgmt-gpu` permanently, so `km create profiles/gpu-qwen-12x.yaml` lands in B with no flag to remember. Where a workload runs is a property of the workload, like its instance type.
- `km create <profile> --launch-account <name>` **overrides** the profile (CLI wins) — redirect any profile to B ad hoc. `--launch-account ""` forces the **home** account even if the profile declares one (useful for testing a B profile against home).
- Whichever source supplies the name, `km create` resolves it against `km-config.yaml launch_accounts.<name>`; an unknown name is a **hard error before any launch**.
- **The account carries its region.** When a launch account is in effect, the subnet, SG, **and region** come from the link record (where the launcher + network actually live) — *not* from `spec.runtime.region`. Picking the account picks its region; a conflicting `spec.runtime.region` is ignored (warned). This is the network-resolution branch in create-flow step 2.

## Create-flow changes

1. **Provider generation** (`pkg/compiler`, the sandbox provider template that derives from `infra/live/root.hcl:46-55`): when the resolved profile has `launchAccount` set, emit an `assume_role { role_arn = <launcher-arn>; external_id = <ext-id> }` block — copying `infra/live/management/scp/terragrunt.hcl:31-33`. Absent ⇒ unchanged (no assume_role).
2. **Network resolution** (`internal/app/cmd/create.go:601-644`): when `launchAccount` is set, source subnet/SG/region from the link record instead of `infra/live/<region>/network/outputs.json`. If the profile sets `mountEFS: true`, the EFS id also resolves from the link record (`efs_id`) instead of home's `efs/outputs.json`; `km validate` errors if `mountEFS` is set against a link enrolled without `--provision-efs`.
3. **Capacity/quota check** (`create.go:802-814`, `capacity.go:122-173`): build a **second `awsCfg`** with `stscreds.AssumeRoleProvider` for the launcher role (the first net-new Go AssumeRole helper in `pkg/aws`), and pass its EC2 + ServiceQuotas clients into `RankAZs`. The `L-DB2E81BA` gate now checks **account B**. The capacity DynamoDB store stays home (A creds), but its partition-key value is namespaced by the resolved launch account (`<account_id>#<instance_type>`) so per-account AZ history is self-consistent — see Q3.
4. **Remote create** (`runCreateRemote`, `create.go:2325`): the create-handler Lambda role in A must be the trusted principal in the launcher trust policy (or assume into it). Local create uses the operator SSO role as the trusted principal.

## Deploy surface

- **`km account add/register/list/rm` + the profile field + create-flow:** operator binary → `make build`.
- **The `launchAccount` schema field reaching remote create:** the create-handler Lambda's bundled `toolchain/km` must be refreshed → `make build-lambdas` + `km init --dry-run=false` (per `[[project_schema_change_requires_km_init]]` and `[[project_remote_create_flattens_extends]]`). Local `km create … ` with the operator binary works without this.
- **B-side roles/network:** provisioned by `km account add` (terragrunt against B), **not** by `km init`. Standalone, like `km cluster add`.
- **No new home-account TF module, no new DynamoDB table.** The capacity store is account-namespaced via its partition-key *value* (Q3) — schema-on-write, no table change.

---

## Wave breakdown (proposed GSD phase)

A new GSD phase, sketched as waves so it can be planned/executed incrementally. Each wave is independently testable.

- **Wave 1 — Config + profile plumbing (no AWS).** `launch_accounts` config block + merge-list entry + getters; `RuntimeSpec.LaunchAccount` field + JSON schema + `km validate` rules. Unit-tested, zero infra. *Exit:* a profile with `launchAccount` validates against a configured link and rejects an unknown one.
- **Wave 2 — B-side enrollment (`km account add`).** New TF module(s) for the launcher role + box role + boundary + optional minimal network + the `{prefix}-results-{B-account}` bucket (bucket-owner-enforced, policy: B box RW / A read) + optional `--provision-efs` (EFS + mount targets + NFS ingress); the `km account add` command (terragrunt against B). *Exit:* run with B creds, the bounded roles + subnet/SG + results bucket (+ EFS if requested) exist in B; `aws iam simulate-principal-policy` confirms the launcher can't do anything but the GPU launch.
- **Wave 3 — A-side register (`km account register` + `list`/`rm`).** Writes the link (incl. `results_bucket`) to `km-config.yaml`; adds the read-only artifacts grant for B's box role. *Exit:* `km account list` shows the link; B's box role can `GetObject` from the home artifacts bucket; A principals can read the B results bucket.
- **Wave 4 — Full create path (local + remote) + capacity-in-B.** Compiler emits the provider `assume_role` block; create-flow sources network from the link; new `pkg/aws` AssumeRole helper; second assumed-role `awsCfg` so `RankAZs`/`km capacity` check B's `L-DB2E81BA`; **both** trusted principals wired (operator SSO for local create, `*-create-handler` Lambda role + its `sts:AssumeRole` grant for remote create). Local and remote land together because the capacity-check-in-B is indivisible from the launch (the GPU quota gate fail-fasts *before* the sweep — checking A would abort every GPU create). *Exit:* `km capacity <gpu-profile>` reads B's quota; `km create` (local **and** remote) lands a GPU instance in B, reachable via `km shell`/`km model start`.
- **Wave 5 — Teardown + `km doctor` + docs.** Cross-account teardown: `km account rm`, `km destroy` assumes the launcher to terminate the B instance, **ttl-handler** cross-account auto-reap (else linked-account sandboxes never expire). `km doctor` checks (link reachable, launcher assumable, S3 grant present, no orphaned B instances). `docs/` runbook + `klanker:` skill updates. Live GPU UAT.

---

## Open questions

- **Q1 — Trusted principal for remote create. RESOLVED.** Both principals trusted from the start: operator SSO role (local create) **and** `*-create-handler` Lambda role + its `sts:AssumeRole` grant (remote create). Folded into Wave 4 — remote create is cheap on top of the launch, and capacity-in-B (required for any GPU create) lands in the same wave regardless.
- **Q2 — ExternalId storage. RESOLVED.** Runtime source of truth = SSM SecureString at `{prefix}/launch-accounts/<name>/external-id`; `km-config.yaml` stores only the SSM path. Auto-generated by `km account add`, or seeded via `--external-id <v>` / `--sops <file>` (Phase-89 SOPS→SSM pattern, `docs/sandbox-secrets.md`).
- **Q3 — Capacity store for linked accounts. RESOLVED (account-scoped store from v1).** Sticky-AZ memory pays off *most* exactly where this design points it: repeated GPU capacity-hunting in B (ICE → retry). So we build it in from the start rather than bypassing. **Approach:** fold the launch account into the **partition-key value** — `<account_id>#<instance_type>` (e.g. `481723467561#g6e.12xlarge`) — instead of bare `<instance_type>`. This is **schema-on-write** (the PK attribute is still a string; only its constructed value changes), so **no table rebuild**. Both home and linked launches share the one `{prefix}-capacity` table in **A**, but each account's rows are namespaced, so per-account AZ history stays self-consistent — which *solves* (not works around) the AZ-name-instability problem: `us-east-1a` history from B is only ever read back in B's context. Writes stay home-account (the create process holds A creds for DynamoDB and only assumes the launcher for the EC2 provider — no cross-account DynamoDB). `RankAZs` and `bestEffortRecordCapacity` thread the resolved launch account into the key. *Back-compat is a non-issue:* B has no production dependencies and is freely clean-slated; existing bare-keyed home rows age out via the 45-min TTL (or get wiped) — the operator explicitly accepts this.
- **Q4 — Bedrock from B. RESOLVED (opt-in at enrollment; default = API keys via SOPS).** **Common case:** the lean GPU box serves **vLLM-local** and receives **direct API keys** (Anthropic/OpenAI, via SOPS) for any cloud model — box role needs **no** `bedrock:InvokeModel`. **Opt-in case:** `km account add --enable-bedrock` (or an interactive prompt during enrollment — "Enable Bedrock model access in this account for GPU boxes?") makes Bedrock a first-class capability *of the linked account*: it grants the box role `bedrock:InvokeModel` against **B's** Bedrock and attempts to enable the relevant model access in B (the Bedrock model-access enablement is partly account/region-gated and may still require a console confirm — `km account add` grants the IAM and prints a reminder + the exact models if it can't fully self-serve). This lets Bifrost's Bedrock routes (Phase 122: `bedrock/us.anthropic.claude-sonnet-4-6`, `bedrock/openai.gpt-oss-120b`) work keyless on a B box. **Caveat (documented, not blocked):** Bedrock calls via B are **unmetered** in lean mode — the MITM proxy meters into home DynamoDB, which lean boxes don't reach. Wiring metering home is the deferred "full sandbox" path. So `--enable-bedrock` is an explicit, eyes-open choice; the box role stays minimal by default.

---

## Risks

- **SCP-exempt account.** Mitigated entirely by the boundaried launcher role; documented as the central security property. The role is the only door.
- **Terragrunt cross-account state.** Backend in A, provider assumes into B — proven by the SCP stack, but the sandbox state-key path (`cmd/ttl-handler` region/account labeling) must stay home-account-scoped. Verify teardown (`km destroy`) assumes the launcher role too.
- **`km destroy` / ttl-handler.** Teardown must assume the launcher role to terminate the B instance. The ttl-handler Lambda (auto-expiry) needs the same cross-account assume path, or linked-account sandboxes won't auto-reap. *(Flag for Wave 5/6.)*
- **Artifacts bucket policy sprawl.** One statement per linked account; `km account rm` must remove its statement.
