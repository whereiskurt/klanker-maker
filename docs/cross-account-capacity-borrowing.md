# Cross-account capacity borrowing

**Phase 126.** Let a SandboxProfile launch its EC2 box into a *different*, pre-linked AWS
account to borrow that account's vCPU quota, while the one km control plane — state,
DynamoDB, budget, `km list`, every bridge — stays in the home account. Motivating fact: a
GPU vCPU quota (`L-DB2E81BA`, "Running On-Demand G and VT instances") can be radically larger
in one account than in the account km actually runs in — enough to fit a `g6e.48xlarge`
that is simply impossible under a smaller home-account quota.

Dormant by default: a profile with no `spec.runtime.launchAccount` is byte-identical to
Phase 125. Nothing about this feature is GPU-specific — the launcher role's
`--instance-types` allowlist is the only thing that scopes a given link to GPU families.

## What this buys, and what it deliberately does not

**Buys:**
- One command pair (`km account add` + `km account register`) turns a second AWS account
  into a bounded landing zone for sandbox EC2 instances.
- A profile opts in with one field (`spec.runtime.launchAccount: <link-name>`); everything
  else — creation, capacity checking, destroy, TTL expiry, `km doctor` — follows the link
  automatically.
- The launcher role in the target account is deliberately narrow: it can launch an
  allowlisted instance type into a pre-provisioned subnet with a required tag, and do
  essentially nothing else. See Security model below.

**Does not buy — do not reach for this feature expecting these:**
- **No second control plane.** There is no second `km init`, no second set of DynamoDB
  tables, Lambdas, bridges, or SES setup in the linked account. `km list`, budget metering,
  Slack/GitHub/HackerOne bridges, and email all continue to operate against the home
  account only.
- **No multi-region support.** A link is single-region, matching the rest of km.
- **No home-account metering or messaging on a linked box.** A linked sandbox's Bedrock
  calls (if `--enable-bedrock` was set at enrollment) are **not** metered into the home
  account's budget tables — this is explicit and documented, not an oversight. Slack/GitHub/
  email notification wiring is unaffected either way, since none of it depends on which
  account holds the EC2 instance.
- **No Go-side `RunInstances`.** The actual launch stays terragrunt-driven, exactly like
  every other sandbox — a generated Terraform provider `assume_role` block does the
  cross-account work, not a hand-rolled AWS SDK call.

## Enrollment — two commands, two credential sets

Enrollment is deliberately split into two steps because it crosses a trust boundary: the
first step must run with credentials that already exist inside the target account (nothing
in the home account can create a role in an account that doesn't yet trust it); the second
step runs with the home account's own credentials and only ever reads a fragment the first
step already wrote.

### Step 1 — `km account add` (target account's admin credentials)

```bash
km account add mgmt-gpu \
  --aws-profile <target-account-admin-profile> \
  --trust <home-account-id> \
  --instance-types g6e.12xlarge,g6e.48xlarge \
  --provision-network --az-count 2 \
  --dry-run=false
```

This provisions the entire target-account footprint via real Terraform (`terraform apply`,
not a print-only wizard) run with the target account's own admin credentials:

- a bounded **launcher role**, trusted by the three home-account principals that ever need
  to assume it (the operator/local-create identity, the `*-create-handler` Lambda role, and
  the `*-ttl-handler` Lambda role), each trust statement conditioned on `sts:ExternalId`;
- a **permissions-boundaried box role** for the sandbox instance itself;
- a **results bucket** (bucket-owner-enforced, versioned, encrypted) — the box can read and
  write it; the home account can only read it;
- optionally, `--provision-network` — one public subnet **per availability zone** (never a
  single subnet — see below) plus a security group;
- optionally, `--provision-efs` — a target-account-local EFS filesystem for a shared weights
  cache.

`--dry-run` **defaults to true**. A dry run performs `terragrunt init -backend=false` +
`terragrunt validate` against the rendered unit — a real syntactic validation, not a
resource-level plan, because the dry-run path deliberately does not create the state
backend that a real plan would need. Pass `--dry-run=false` to actually apply.

**Why one subnet per AZ, never one subnet total:** a link with a single subnet collapses
the Phase 124 AZ-failover sweep to exactly one attempt, which disables AZ rotation exactly
where GPU capacity exhaustion is worst. `--provision-network --az-count N` always produces
a subnet list, and the link record stores that list — never a single ID.

`km account add` writes a **handoff fragment** to `~/.km/account-links/<name>.link.yaml`
(owner-only permissions, `0600`) carrying every value the second step needs, including the
`ExternalId` in cleartext. Treat this file like a credential — it is one, briefly, until it
is consumed by registration.

**This command's own Terraform state lives in the target account, not the home account.**
`km account add` creates (via the AWS API, before any terragrunt call) an S3 bucket
`tf-{prefix}-linkstate-{target-account-id}-{region-label}` and a DynamoDB lock table
`tf-{prefix}-linklocks-{region-label}`, both in the target account, and stores this link's
state at key `account-links/{name}/terraform.tfstate`. There is **no local Terraform state
anywhere in this project** — enrolling a second link into the same target account reuses the
same bucket and table under a different key. The generated unit directory under
`~/.km/account-links/<name>/` is a regenerable artifact, not the source of truth; the link
record itself carries the backend identifiers, so teardown works even from a fresh clone
with no local unit directory present.

#### If you sign in with IAM Identity Center (SSO), you must pass `--trust-principal`

`km account add` derives the third trusted principal — you, the operator — from the caller
identity it resolves in the target account, re-homing that role name into the home account.
That works for a plain IAM role. It **cannot** work for an IAM Identity Center role, and
enrollment fails fast rather than guessing:

```
cannot derive the operator's principal in account <home>: caller is an IAM Identity Center
(SSO) role (AWSReservedSSO_AdministratorAccess_574bfc731f4810d3); its per-account name hash
and /aws-reserved/sso.amazonaws.com/ path cannot be re-homed into account <home>
```

Two independent reasons, either fatal alone:

1. **The trailing hash is per-account.** The same permission set provisions
   `AWSReservedSSO_AdministratorAccess_<hash>` with a *different* hash in every account, so
   the target account's hash never names a real role in the home account.
2. **The path is dropped.** SSO roles live under `/aws-reserved/sso.amazonaws.com/`; the
   bare `role/<name>` form omits it.

IAM validates principal existence at `CreateRole`, so a guessed ARN doesn't degrade
gracefully — it fails the whole trust policy with `MalformedPolicyDocument`, *after* the
state backend has been provisioned. Hence the fail-fast.

Look up your real home-account ARN:

```bash
aws iam list-roles --profile <home-profile> \
  --path-prefix /aws-reserved/sso.amazonaws.com/ \
  --query "Roles[?starts_with(RoleName,'AWSReservedSSO_<PermissionSet>')].Arn" --output text
```

then add it to the `km account add` invocation:

```bash
--trust-principal arn:aws:iam::<home>:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_AdministratorAccess_<home-hash>
```

`--trust-principal` replaces **only** the derived operator entry — `{prefix}-create-handler`
and `{prefix}-ttl-handler` are still trusted automatically, so you still get all three. It is
repeatable if more than one operator principal needs access.

If your SSO role names are not stable across permission-set changes, use
`--trust-principal-pattern` instead for an `ArnLike` glob (e.g.
`arn:aws:iam::<home>:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_AdministratorAccess_*`).
That is looser than an exact ARN — it delegates to the account root under an `ArnLike`
condition — so prefer the exact ARN when you can.

**Why this is a hard error and not a warning:** dropping the operator principal would still
leave a link a *remote* create could use, because `{prefix}-create-handler` is trusted
independently. Enrollment would look like it worked. But `km create --local` and — far worse
— `km destroy` run as you, and neither could assume the launcher. A link that cannot be torn
down is precisely the failure this feature exists to avoid.

### Step 2 — `km account register` (home account's credentials)

```bash
km account register mgmt-gpu --aws-profile <home-account-profile>
```

By default this reads `~/.km/account-links/mgmt-gpu.link.yaml` (the fragment `km account
add` just wrote). Run entirely with home-account credentials, it:

- writes the `launch_accounts.mgmt-gpu:` entry into `km-config.yaml`;
- stores the external id as an **SSM SecureString** at
  `/{prefix}/launch-accounts/mgmt-gpu/external-id` — the plaintext value never touches
  `km-config.yaml`, only the parameter *path* does;
- appends **exactly one** read-only statement (`s3:GetObject`, `s3:ListBucket` — never
  `s3:PutObject` or any other write action) to the home artifacts bucket policy, scoped to
  the target account's box role, so a linked sandbox can pull sidecars from home.

If the fragment file is lost, `km account register` also accepts every field as an explicit
flag (`--account-id`, `--launcher-arn`, `--box-role-arn`, `--region`, `--subnet` (repeatable),
`--results-bucket`, `--external-id`, `--efs-id`, `--sg`) as a recovery path — but that path
has no `--az` flag, so a link registered this way needs `availabilityZones` filled in by
hand before it satisfies the create-path's subnet/AZ length-parity check. The primary
`--from-fragment` path is always fully populated.

### `km account list` / `km account rm`

```bash
km account list
```

Renders every configured link (name, account id, region, subnet count, results bucket,
external-id **parameter path**) — never the secret value itself.

```bash
km account rm mgmt-gpu --aws-profile <home-account-profile>
```

Home-only by default: removes the artifacts-bucket grant, the SSM parameter, and the
`km-config.yaml` entry, idempotently. It prints a self-contained, pasteable
`terragrunt destroy` command for finishing target-account teardown by hand.

```bash
km account rm mgmt-gpu \
  --aws-profile <home-account-profile> \
  --target-aws-profile <target-account-admin-profile>
```

Adding `--target-aws-profile` also destroys the target-account footprint (launcher role, box
role, network, results bucket) in the same invocation, regenerating the unit directory from
the link record if it's missing locally.

```bash
km account rm mgmt-gpu \
  --aws-profile <home-account-profile> \
  --target-aws-profile <target-account-admin-profile> \
  --purge-backend
```

`--purge-backend` additionally deletes the shared state bucket and lock table — but only
after confirming no *other* configured link still shares them; it refuses and names the
conflicting link otherwise. Reversal (removing a link entirely) is therefore this one
command, run twice at most (once per credential set) if `--target-aws-profile` isn't
supplied up front.

## What the link record contains, and where the external id lives

`km-config.yaml`'s `launch_accounts.<name>:` entry carries only non-secret fields: account
id, region, launcher/box role ARNs, subnet ids + availability zones (index-paired), security
group id, results bucket, EFS id, and the SSM **parameter path** for the external id — never
the external id's value. The value lives exactly one place: an SSM SecureString in the home
account, read at the moment a link is resolved (create, capacity check, destroy, doctor) and
never persisted anywhere else. The handoff fragment `km account add` writes is the one
place the plaintext value ever briefly touches disk, and it is owner-only-permissioned.

## The profile field and the create-time override

```yaml
spec:
  runtime:
    launchAccount: mgmt-gpu
```

`km validate` rejects an unknown link name (naming `km account register`) and rejects
`launchAccount` combined with `spec.network.privateSubnet` in the same profile — a linked
launch's placement always comes from the link, never from the home network's private-subnet
machinery.

```bash
km create profiles/gpu-glm46-48x.yaml --launch-account mgmt-gpu   # override, any name registered
km create profiles/gpu-glm46-48x.yaml --launch-account ""         # explicit empty forces home,
                                                                    # even if the profile names a link
```

`--launch-account` on `km create` and `km capacity` overrides the profile's field; an
explicit empty string forces home placement even against a profile that names a link
(distinguished from the flag simply being absent, which defers to the profile).

## Region, subnets, and security group all come from the link

When `launchAccount` is set, the sandbox's region, subnet list, and availability zones all
come from the link record — **not** from `spec.runtime.region` or the home network's own
subnet resolution. The linked account's VPC id is resolved once per launch (a single
`DescribeSubnets` call against the link's first subnet, using the assumed launcher-role
credentials) so the sandbox's ENI lands in the linked account's real VPC rather than a
disconnected one the launch module would otherwise self-provision.

**Known residual gap:** the link's `SecurityGroupID` (from `km account add
--provision-network`) is not currently reused by the launch path — the EC2 module always
creates its own per-sandbox security group inside whatever VPC it's given, for both home and
linked launches. This is a pre-existing behavior for home launches too (there is no "reuse an
existing SG" input anywhere), so a linked launch behaves consistently, but an operator who
goes looking for their pre-provisioned SG being reused will not find it in use. Non-blocking;
flagged for a future plan.

## Capacity and quota — the gate reads the linked account

`km capacity <profile> --launch-account mgmt-gpu` and the `km create` AZ-ranking sweep both
build a **second**, assumed-role AWS config scoped to the linked account and check *that*
account's GPU vCPU quota and instance-type offerings — not the home account's. A failed
role-assume aborts the check entirely; it never silently falls back to reporting the home
account's headroom. The `km capacity` report header always names which account it checked
(`"home"` or `"<link> (<account-id>)"`), so a linked report can never be misread as a home
one.

The `{prefix}-capacity` DynamoDB table (sticky-AZ history, ICE tracking) stays in the home
account for every launch, home or linked — only the row *key* is namespaced by account id
for a linked launch. Home rows keep their pre-Phase-126 bare key forever; this is
deliberate, because success rows carry no TTL and re-namespacing home "for symmetry" would
orphan months of accumulated AZ-preference history with no way to recover it.

## Teardown — both paths are account-aware

**Operator-initiated (`km destroy`):** before running the home account's own resource-tag
discovery (which structurally cannot see a resource in a different account), `km destroy`
reads the sandbox's DynamoDB row for its `launch_account`. For a linked sandbox, an empty
result from the home-account tag scan is expected, not fatal; the cold-clone fallback path
(used when the local sandbox directory is missing) synthesizes its minimal Terraform
configuration with the link's `assume_role` locals so the destroy still reaches the linked
account. Use `km destroy <id> --remote --yes` — there is no `--force` flag on this command.

**Unattended (TTL expiry):** the expiry Lambda reads the same DynamoDB row and, for a linked
sandbox, renders its destroy-time `main.tf` with a conditional `assume_role` provider block
sourced from the link's own region and the SSM-stored external id — never from the Lambda's
own cold-start environment variables, which are instance-wide and cannot carry a per-sandbox
linked region.

Both paths fail closed on an unknown link name or a failed external-id read (a hard,
logged error) rather than silently rendering a home-account provider that would report
success while the linked instance kept running and billing.

## `km doctor` — four new checks

All four are read-only, and all four **skip silently with zero AWS calls** when no
`launch_accounts` links are configured:

| Check | What it means when it fails |
|---|---|
| **Launch Account Links** | A configured link is structurally incomplete (missing launcher ARN, region, subnets, external-id parameter path, box role ARN, or results bucket), or has a subnet/AZ list length mismatch, or has exactly one subnet (which collapses AZ rotation to a single attempt). Pure config check, no AWS call. |
| **Launch Account Assumable** | The launcher role in the target account cannot actually be assumed — a broken trust policy, an expired/rotated external id, or a deleted role. This check forces real credential resolution (not just config construction) so a lazily-cached, never-actually-contacted credentials provider can't report a false healthy. A failed external-id SSM read is reported distinctly from a failed role-assume, so an operator can immediately tell which one to fix. |
| **Launch Account Artifacts Grant** | The home artifacts bucket's read-only grant for a link's box role is missing, or — more seriously — has been widened beyond exactly `s3:GetObject`/`s3:ListBucket`. Any other action present on that statement is flagged, whether or not it looks like an obvious write verb. |
| **Launch Account Orphan Instances** | A linked-account EC2 instance carrying the `km:sandbox-id` tag has no matching row in the home account's sandbox inventory — a leaked/unreaped instance that `km list` cannot see and that is billing silently. A link that can't be reached (broken assume, describe failure) is reported as its own distinct "could not check" case, never folded into a false "no orphans found." |

## Security model, stated plainly

- **The target account is exempt from Service Control Policies.** SCPs attach at the
  organization or organizational-unit level and simply do not apply to an org **management**
  account, and in general do not constrain a designated target account the way they would a
  normal member account under an SCP-enforcing OU. km's own SCP guardrails (`km bootstrap
  --shared-secrets-key`-style protections, the `--include-scp` machinery elsewhere in this
  project) provide **zero** additional containment here.
- **The launcher role's own IAM policy is therefore the entire security boundary.** There is
  no second layer to catch a policy defect — this is the single most important fact about
  this feature's security posture. The policy is deliberately narrow: it can launch an
  allowlisted instance type into a specific tagged subnet with a required resource tag, pass
  only the box role, and manage the lifecycle only of resources it tagged itself. It has no
  `iam:CreateRole`/`iam:PutRolePolicy` (cannot self-escalate), no `organizations:*` access
  (relevant specifically because a management-account target has that API surface to
  accidentally grant), and no S3 access beyond the one narrow cross-account artifacts grant
  (which lives on the home bucket's policy, not the launcher's own policy).
- **The external id is the confused-deputy protection**, required on every `AssumeRole`
  call into the launcher role, exactly matching AWS's documented purpose for it.
- **The box role is capped by a permissions boundary** that shares its statement list with
  the role's own inline policy by construction, so the boundary cannot silently drift looser
  or tighter than the policy it's meant to cap.
- **Every cross-account data path is read-only in both directions.** Box → home: read-only
  on the artifacts bucket (no `PutObject`). Home → box: read-only on the linked account's
  results bucket. The only read-write path is box → its own results bucket, entirely within
  the linked account.

### Operator-safety note: avoid enrolling an organization management account

Enrolling an organization **management** account as the target is technically possible but
**discouraged**. Because SCPs never apply to the management account, the "SCP is a second
containment layer" assumption that holds elsewhere in this project's threat model does not
hold here at all — the launcher role's own policy becomes not just *the* primary boundary
but the *only* one, with no structural backstop if that policy is ever widened by mistake.
AWS's own guidance is to keep workloads out of the management account entirely. km's current
self-enrollment guard only catches the degenerate case of `--trust` naming the same account
`km account add` is being run against; nothing today warns an operator that their chosen
target happens to be the management account. **Prefer a dedicated member account** for
capacity borrowing whenever one is available. This is documented here as an explicit
operator caveat, not resolved in code by this phase — a follow-up warning in `km account
add`/`km doctor` is a reasonable future addition.

## Cost

An unreaped GPU box in the linked account bills continuously in an account the home
inventory's `km list` does not enumerate at all — this is exactly what the **Launch Account
Orphan Instances** doctor check exists to catch, and exactly why REQ-126-TEARDOWN (both the
operator destroy path and unattended TTL expiry being fully account-aware) is treated in this
project as non-optional rather than a nice-to-have. There is no cost surfaced by `km status`
or budget tooling for a linked box today — home-account budget metering explicitly does not
extend across the account boundary — so `km doctor`'s orphan check and disciplined TTLs are
the only safety nets.

## Deploy surface

Get this right — deploy-surface mistakes are this project's single most common source of
"code is correct but the running install doesn't reflect it" incidents.

| Change | Command(s) | Why |
|---|---|---|
| `km account add/register/list/rm`, the `--launch-account` flags, and the create-flow wiring | `make build` | Pure operator-binary changes — no Lambda, no schema-on-write DDB change, no Terraform module change to apply from the home side. |
| `spec.runtime.launchAccount` reaching a **remote** `km create` | `make build` **then** `make build-lambdas` **then** `km init --dry-run=false` | The create-handler Lambda validates and compiles the profile using its own bundled `toolchain/km` binary — a stale bundle silently rejects or ignores the new field on remote creates even though local creates already work. This is a full apply, not `--sidecars` (which does not refresh the create-handler's bundled toolchain). |
| The expiry-Lambda cross-account teardown path (`KM_LAUNCH_ACCOUNTS`, the `sts:AssumeRole` and `ssm:GetParameter`/`kms:Decrypt` grants) | `make build-lambdas` **then** `km init --dry-run=false` | This changes both a Lambda environment block and an IAM policy on the ttl-handler role — both require a full terragrunt apply. `--sidecars` updates neither. |
| The target account's launcher/box roles, network, and results bucket | `km account add ... --dry-run=false` (run once, at enrollment) | Provisioned by terraform running directly against the target account, driven entirely by `km account add` — **never** by `km init`, which never touches the target account at all. This is the same standalone-provisioning shape `km cluster add` already uses. |

**Ordering constraint:** rebuild the operator binary (`make build`) before running any
`km init` apply in this phase — `km init` itself is driven by the operator binary, so a
stale binary silently skips whatever it doesn't yet know to emit.

**Sidecar-only (`km init --sidecars`) is not sufficient for any of the above.** Nothing in
this phase changed a sandbox-side helper binary; every change here is either operator-binary,
Lambda + IAM, or target-account Terraform.

Existing sandboxes are unaffected by any of the above — this phase only changes what a
**new** `km create`/`km destroy` does, not anything about an already-running box.

## See also

- `docs/superpowers/specs/2026-06-29-cross-account-gpu-launch-design.md` — the original design
  spec and its 2026-08-22 re-verification (corrections C1–C5).
- `.planning/phases/126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin/126-UAT.md`
  — the pre-flight gate record and the live verification checklist/results.
- `docs/operational-gotchas.md` § AZ failover + capacity feasibility — the Phase 124 sweep
  this phase's capacity gate reuses.
