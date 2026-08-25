<!--
  MAINTAINER NOTE — update this file BEFORE tagging each release.

  Its contents are injected VERBATIM into every GitHub release's notes,
  between the install header and goreleaser's auto-generated changelog.
  Wiring: .github/workflows/release.yml ("Load release highlights" step) →
  $KM_RELEASE_HIGHLIGHTS → .goreleaser.yaml `release.header` template.

  Keep it to the few MAJOR, human-curated additions for THIS release (the
  auto-changelog already lists every commit). HTML comments like this one
  are hidden in GitHub's rendered view. If this file is empty/absent the
  section is omitted gracefully.
-->
## ✨ Major additions highlighted

This release is about **one thing: running sandboxes on GPU capacity your main account doesn't
have.** A sandbox profile can now launch its EC2 box into a *different*, pre-linked AWS account
and borrow that account's vCPU quota — while the single km control plane stays exactly where it
is.

### 🏦 Cross-account capacity borrowing

GPU quota is per-account and per-region, and `L-DB2E81BA` ("Running On-Demand G and VT
instances") **defaults to 0** in every account you have never asked about. So the quota you need
is often sitting in a *different* account from the one km runs in. Measured on a real link
during this release's UAT:

```
$ km capacity profiles/gpu-qwen38-oblit-12x.yaml --launch-account gpuman
Capacity report: g6e.12xlarge  region: us-east-1  account: gpuman (481723467561)
us-east-1a  yes  768 vCPU  ...

$ km capacity profiles/gpu-qwen38-oblit-12x.yaml
Capacity report: g6e.12xlarge  region: us-east-1  account: home
us-east-1a  yes   64 vCPU  ...
```

Same profile, same region — **12x the headroom**, because the gate reads the *linked* account.

**Only the box moves.** State, DynamoDB, budget, `km list`, `km destroy`, and every bridge stay
in the home account. A linked sandbox is an ordinary sandbox that happens to bill someone else's
quota.

**Dormant by default.** Absent `spec.runtime.launchAccount` (or `--launch-account`), behaviour is
byte-identical to v0.7.x. No `apiVersion` bump, no profile migration, no change to existing
sandboxes.

**Enrollment is two commands with two different credential sets** — this is deliberate, and the
order matters:

```bash
# 1. TARGET-account admin creds — provisions the bounded launcher role, a
#    permissions-boundaried box role, a results bucket, and one subnet PER AZ.
km account add gpuman --aws-profile <target-admin> --trust <home-account-id> \
    --instance-types g6e.12xlarge,g6e.4xlarge --provision-network --az-count 4 --dry-run=false

# 2. HOME creds — writes the km-config.yaml link, the external id as an SSM
#    SecureString, and exactly one read-only grant on the home artifacts bucket.
km account register gpuman --from-fragment ~/.km/account-links/gpuman.link.yaml \
    --aws-profile <home>
```

Then `km account list` / `km account rm` round it out. The enrollment unit's **Terraform state
lives in the target account** — there is no local state anywhere in this project.

### 🔐 The launcher role is the entire containment boundary

A linked target account is typically **exempt from your SCPs**, so unlike every other km IAM
path there is no second layer behind it. That role's policy was verified live with
`aws iam simulate-principal-policy`, each denial paired with a positive or negative control so a
deny caused by a malformed simulation can't be mistaken for a deny caused by policy:

- `RunInstances` is confined to an **explicit instance-type allowlist** and to the link's own
  subnets — a foreign subnet is denied, the link's own subnet is allowed.
- `iam:PassRole` is scoped to the **single** box role; passing any other role to EC2 is denied.
- No `iam:CreateRole`/`PutRolePolicy`/`CreateUser` — the launcher cannot self-escalate.
- Lifecycle actions (`TerminateInstances`, `StopInstances`, …) are gated on
  `aws:ResourceTag/km:managed-by`.
- No `organizations:*`, and no S3 beyond the one explicit read-only grant.
- The external id (an SSM SecureString) is the confused-deputy protection.

Every cross-account data path is read-only in both directions. **Enrolling an org *management*
account as the target is possible but discouraged** — SCPs never apply there, so the usual "SCP
is a second layer" assumption doesn't hold. Prefer a dedicated member account.

### 🩺 `km doctor` gains four cross-account checks

Link well-formed · launcher role **actually assumable** (forces real credential resolution, not
just config construction) · artifacts grant present **and not widened** beyond the exact
read-only pair · **no orphaned linked-account instances** — a leaked box `km list` cannot see,
billing silently.

All four are **silently skipped with zero AWS calls** when no `launch_accounts` are configured.

### 🧹 Teardown is account-aware and fails closed

`km destroy` (including its cold-clone fallback) reads the sandbox's `launch_account` **before**
the home-account tag scan, which could never see a resource in another account. The expiry
Lambda renders a conditional `assume_role` provider sourced from the link's own region. Both
**hard-error** on an unknown link or an unreadable external id rather than silently falling
through to a home-account provider that would report success while the linked instance kept
billing.

### ⚠️ Known limitations — read before enrolling

- **A linked box gets a smaller IAM role.** It runs with the 4-grant shared box role, *not* the
  12-grant per-sandbox role. **Budget metering, GitHub token minting, Slack/GitHub inbound,
  transcript upload, and SOPS secret injection do not work on a linked sandbox.** This is not
  fixable by widening IAM — SSM Parameter Store and DynamoDB have no resource policies — so
  treat linked sandboxes as compute, not as full-featured agent boxes.
- **The link's pre-provisioned security group is unused.** The EC2 module always self-creates a
  per-sandbox SG, matching home-launch behaviour.
- **A link is region-locked.** Every `RunInstances` resource ARN in the launcher policy is
  pinned to the enrolled region; another region needs its own `km account add` *and* its own
  quota increase.
- **Two pre-existing capacity-sweep defects are worth knowing about**, since this feature is
  usually reached while chasing scarce GPU capacity. Neither is new in this release and both
  affect home-account launches identically: an AZ that has ever succeeded currently out-ranks
  every other AZ even after it goes capacity-dry (the sticky-AZ score ignores a fresh
  insufficient-capacity signal, and `km capacity` reports the AZ as `recently-dry` while the
  launch path still selects it); and terraform retries insufficient-capacity errors internally,
  so a remote create can consume its full Lambda budget on one dry AZ, time out, and leave a
  state lock plus an unattached data volume behind. If a GPU create fails with `Error acquiring
  the state lock`, that timeout is the cause — clearing the lock alone will not help.

Full operator runbook: `docs/cross-account-capacity-borrowing.md`.

### 🔧 Also in this release

- The GHCR package-visibility note from the v0.7.1 container work was corrected — no manual
  step is needed to make the published image public.
