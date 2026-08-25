# gpu-launcher-account/v1.0.0

Provisions the entire **account-B footprint** for cross-account capacity
borrowing (Phase 126): a bounded launcher role, a pre-baked box role capped by
a permissions boundary, a results bucket, and (optionally) a lean per-AZ
network and an EFS filesystem. Applied by the `km account add` enrollment
command using **account B's own admin credentials** — never by the
platform's regional `km init`, and never with standing account-A credentials.

## What this module creates

| Resource | Purpose |
|---|---|
| `aws_iam_role.launcher` + `aws_iam_role_policy.launcher` | `{prefix}-gpu-launcher` — the single door account A knocks on. Can launch an allowlisted instance type into a km subnet with the required tags, tag/stop/start/terminate only km-tagged resources, and read EC2/quota info. Nothing else. |
| `aws_iam_role.box` + `aws_iam_instance_profile.box` + `aws_iam_policy.box_boundary` | `{prefix}-gpu-box` — the pre-baked role attached to every launched instance, capped by a permissions boundary. Read-only into account A's artifacts bucket, read-write on this account's own results bucket, SSM core, own-tag reads. |
| `aws_s3_bucket.results` (+ ownership controls, public-access block, versioning, encryption, policy) | `{prefix}-results-<account-id>` — the B-local, policy-only results bucket account A reads. |
| `aws_vpc.launcher`, `aws_subnet.launcher[*]`, `aws_internet_gateway.launcher`, `aws_route_table.launcher`, `aws_security_group.launcher` (all `count`-gated on `var.provision_network`) | Optional lean, always-public per-AZ network. |
| `aws_efs_file_system.launcher`, `aws_efs_mount_target.launcher[*]`, `aws_security_group_rule.efs_nfs_ingress` (all `count`-gated on `var.provision_efs`) | Optional B-local shared filesystem (e.g. a weights cache for a multi-box GPU fleet). |

## Why the IAM policies here ARE the whole security boundary

Account B is normally the org **management** account, and AWS Service Control
Policies **do not apply** to the management account. Every other AWS account
in the org gets blast-radius containment from km's SCP guardrails
(`infra/modules/scp`); account B gets none of that. So the launcher's
permission policy and the box role's permissions boundary are not
defence-in-depth here — they are the **entire** containment mechanism. Treat
any change to `main.tf`'s IAM policies with the scrutiny that implies, and
re-run the `simulate-principal-policy` matrix in
`.planning/phases/126-.../126-RESEARCH.md` § Security Domain after any edit.

## The three trusted principals, authored up front

The launcher trust policy is meant to name all three account-A principals
from the **first apply**:

1. The operator's own role (local `km create` / `km account add` itself).
2. The `*-create-handler` Lambda role (remote `km create`).
3. The `*-ttl-handler` Lambda role — the **only** thing that auto-reaps an
   expired box in account B; without it, nothing tears down a B-launched
   sandbox on TTL expiry.

All three are deterministic, exact role ARNs in account A (both Lambda role
names are derived from the resource prefix), so they belong in
`var.trusted_principal_arns`. Authoring all three now, rather than
retrofitting the trust policy later, avoids a second privileged trip through
account B's admin credentials. `var.trusted_principal_arn_patterns` is a
separate, opt-in escape hatch for a principal whose exact ARN can't be
predicted (e.g. an IAM Identity Center role) — it adds a broader,
account-root-scoped statement, which is why it defaults to empty and is
always paired with both the external-id condition and an `ArnLike` test on
the calling principal's own ARN.

## The external-id requirement

Every trust statement carries an `sts:ExternalId` condition
(`var.external_id`). This is the standard confused-deputy mitigation for
cross-account AssumeRole and is **not optional** here: the module's
`external_id` variable has a `validation` block that refuses to plan against
an empty value, because an empty external id would silently produce a
condition-free (and therefore unbounded, principal-only) trust statement.
The external id itself is minted once at enrollment and stored **only** as
an SSM SecureString in the trusting install (`/km/launch-accounts/<name>/
external-id`) — it is never a plaintext value in this module's state, and it
is never a module output.

## The no-cross-account-write invariant

No cross-account writes, ever. Each account writes only its own buckets;
each account reads the other's read-only:

| Direction | Access | What |
|---|---|---|
| B → A | read-only | The box role's `GetObject` grant against `var.artifacts_bucket_arn` — sidecars/toolchain. |
| B → B | read-write (same account) | The box role's grant against this module's own results bucket. |
| A → B | read-only | The results bucket policy's `TrustingAccountReadOnly` statement — `GetObject`/`ListBucket` only, never `PutObject`. |

A compromised box on either side of the boundary can only ever **read**
across it — never write.

## Why the subnet output is a list

`subnet_ids` and `availability_zones` are both lists, index-parallel
(`subnet_ids[i]` lives in `availability_zones[i]`). Provisioning one subnet
per availability zone — rather than a single shared subnet — is what lets
the Phase 124 AZ-failover sweep retain more than one attempt when this
account reports capacity exhaustion for a GPU family, which is exactly where
capacity errors are worst. A single-subnet enrollment would collapse the
sweep to one attempt; re-provisioning later would need another privileged
trip through account B's admin credentials, so the per-AZ network is
provisioned up front even though it is optional (`var.provision_network`).

## Applied by `km account add`, not `km init`

This module is a standalone Terraform unit applied directly with account B's
own admin credentials (`--aws-profile <B-admin>`), by the `km account add`
enrollment command. It is **not** part of the platform's regional
`infra/live/` terragrunt hierarchy and is never touched by `km init`. Its
outputs become the `launch_accounts.<name>:` link record that `km account
register` (run with account A credentials) writes into `km-config.yaml`.

## Field-to-output mapping

Every field of `internal/app/config.LaunchAccountConfig` maps to exactly one
of this module's outputs, except the four fields sourced elsewhere:

| `LaunchAccountConfig` field | Module output | Notes |
|---|---|---|
| `AccountID` | `account_id` | |
| `LauncherRoleARN` | `launcher_role_arn` | |
| `BoxRoleARN` | `box_role_arn` | |
| `Region` | `region` | |
| `SubnetIDs` | `subnet_ids` | index-parallel with `AvailabilityZones` |
| `AvailabilityZones` | `availability_zones` | index-parallel with `SubnetIDs` |
| `SecurityGroupID` | `security_group_id` | |
| `ResultsBucket` | `results_bucket` | |
| `EFSID` | `efs_id` | empty string when `provision_efs = false` |
| `ExternalIDSSM` | *(no module output)* | the SSM parameter **path**, not the secret; written by `km account add` after this module applies, never derived from module state. |
| `InstanceTypes` | *(no module output)* | this is an **input** (`var.instance_types`) the operator supplies via `--instance-types`; the command already has the value it passed in and does not need it echoed back. |
| `StateBucket` / `LockTable` / `StateKey` | *(no module output)* | describe where **this module's own** Terraform state lives in account B — set by the enrollment command's backend configuration, not produced by anything this module creates. |

## Variables of note

- `trusted_principal_arns` / `trusted_principal_arn_patterns` — see "the
  three trusted principals" above.
- `external_id` — sensitive, validated non-empty.
- `provision_network` / `subnet_id` / `security_group_id` / `az_count` /
  `vpc_cidr` — the optional lean network; when off, `subnet_id` and
  `security_group_id` pass straight through to the outputs so an existing
  network can be reused.
- `provision_efs` — optional B-local shared filesystem; never crosses the
  account boundary.
- `enable_bedrock` — off by default; grants the box role Bedrock invoke in
  account B. Unmetered by design — there is no http-proxy MITM sidecar /
  budget table in account B the way there is at home.
- `artifacts_bucket_arn` — account A's artifacts bucket ARN, for the box
  role's read-only grant.
