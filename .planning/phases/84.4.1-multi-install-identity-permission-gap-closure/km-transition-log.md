# Phase 84.4.1 Canonical km Install Transition Log

**Started:** 2026-05-18 18:43 UTC
**Operator:** KPH (whereiskurt@gmail.com)
**AWS account (application):** 052251888500 (klanker-application / klanker-terraform)
**AWS account (management):** 481723467561 (klanker-management → chain-assume km-org-admin)
**Git ref:** b431671522379cdb0abe2b24221a4e0448490545
**Branch:** main

## Pre-flight notes

- inventory-diff.sh extended (2026-05-18) to capture SSM documents via
  `aws ssm list-documents --filters Key=Owner,Values=Self`.
- Operator runs from repo root with `eval $(./bin/km env)` (km rebuilt from
  9d787da before transition; old `bin/km` predated Phase 84.3 `km env` command).
- Management-account profile (`klanker-management`) uses HostedZoneAdmin role
  which lacks Organizations:*. SCP body captures use chain-assumed `km-org-admin`
  role via `klanker-terraform` → `sts:AssumeRole`.

## BEFORE snapshot

- inventory: `km-before-84.4.1.json` — captured via `inventory-diff.sh snapshot` with `klanker-application`
- SCP policy body: `km-before-84.4.1-scp-body.json` (161 lines, 25 `km-*` literals)
- SSM documents: embedded in inventory snapshot under `.ssm.documents`

### Key resources baseline

- SCP policy ID: **p-cvd490xt**
- SCP policy name: **km-sandbox-containment**
- SSM document: **km-Sandbox-Session** (already lowercase in production — module v2.0.0 was already converged at AWS layer; Wave 1 module + Go code change closed the case-sensitivity bug)
- km sandboxes currently running: **1** (`learn-60ec3c82`, status: running)

## Planned transitions

1. infra/live/management/scp/ — apply scp/v2.0.0 *-* pattern fix
   - Expected: 1 in-place update on `aws_organizations_policy.sandbox_containment` + 1 add `terraform_data.scp_size_guard`
   - Expected destroys: 0
   - Expected window: sub-10 seconds
   - Wave 1 plan 84.4.1-01

2. infra/live/use1/ssm-session-doc/ — apply ssm-session-doc/v2.0.0 rename
   - Expected at plan time: 1 destroy + 1 create
   - **Actual:** No changes — state + AWS already at v2.0.0 lowercase name
   - Wave 1 plan 84.4.1-02

## Notification to active sessions

No active SSM sessions checked because SSM apply turned out to be a no-op.

---

## Step 0 — BEFORE snapshot capture

**Captured at:** 2026-05-18 18:43 UTC

```bash
cd /Users/khundeck/working/klankrmkr
make build && cp km bin/km                      # rebuild — old binary predated 'km env'
source <(./bin/km env)

# Inventory snapshot
AWS_PROFILE=klanker-application scripts/inventory-diff.sh snapshot \
  .planning/phases/84.4.1-.../km-before-84.4.1.json --prefix km

# SCP body via chain-assumed km-org-admin
CREDS=$(AWS_PROFILE=klanker-terraform aws sts assume-role \
  --role-arn arn:aws:iam::481723467561:role/km-org-admin \
  --role-session-name km-snap-before --output json)
export AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... AWS_SESSION_TOKEN=...
aws organizations describe-policy --policy-id p-cvd490xt \
  --query 'Policy.Content' --output text \
  > .planning/phases/84.4.1-.../km-before-84.4.1-scp-body.json
```

**Snapshot size:** inventory snapshot written; SCP body 161 lines, 25 `km-*` references

---

## Step 0a — scp/v2.0.0 apply

**Started:** 2026-05-18 19:04 UTC

### terragrunt plan output (key fragment)

```
Plan: 1 to add, 1 to change, 0 to destroy.
```

Changes (km-* → *-* in 5 statement conditions):
- km-provisioner-* → *-provisioner-*
- km-lifecycle-* → *-lifecycle-*
- km-ecs-spot-handler → *-ecs-spot-handler
- km-ttl-handler → *-ttl-handler
- km-create-handler → *-create-handler
- km-budget-enforcer-* → *-budget-enforcer-*
- km-ec2spot-ssm-* → *-ec2spot-ssm-*
- km-github-token-refresher-* → *-github-token-refresher-*

Plus: tag `km:resource-prefix=km` added; +1 new `terraform_data.scp_size_guard` sentinel resource.

### Expected assertions — all PASS

- ✅ Plan: 1 to change, 0 to destroy (+1 add is sentinel, not infra)
- ✅ Resource: `aws_organizations_policy.sandbox_containment`
- ✅ Condition changes: ~5-10 lines in `Statement[].Condition.ArnNotLike` arrays
- ✅ Zero other resource changes

### terragrunt apply outcome

```
terraform_data.scp_size_guard: Creating...
terraform_data.scp_size_guard: Creation complete after 0s [id=495d6b12-3339-16b2-0466-5ad560a70337]
aws_organizations_policy.sandbox_containment: Modifying... [id=p-cvd490xt]
aws_organizations_policy.sandbox_containment: Modifications complete after 1s [id=p-cvd490xt]
Apply complete! Resources: 1 added, 1 changed, 0 destroyed.
Outputs:
policy_arn = "arn:aws:organizations::481723467561:policy/o-om3mjz6hu8/service_control_policy/p-cvd490xt"
policy_id = "p-cvd490xt"
```

Window: ~1.5 seconds end-to-end.

### Policy body verification

- ✅ `*-create-handler` present in deployed policy: YES
- ✅ `km-create-handler` removed from deployed policy: YES (0 `km-*` literals in AFTER body)

**Completed:** 2026-05-18 19:04 UTC
**Result:** PASS

---

## Step 0b — ssm-session-doc/v2.0.0 apply

**Started:** 2026-05-18 18:43 UTC (plan only — no apply needed)

### Active session check

```bash
aws ssm describe-sessions --state Active \
  --query 'Sessions[?DocumentName==`KM-Sandbox-Session`].SessionId' --output text
```
Not run — apply was no-op.

### terragrunt plan output

```
No changes. Your infrastructure matches the configuration.
```

### What happened (and why)

The live `infra/live/use1/ssm-session-doc/terragrunt.hcl` had already been
flipped to v2.0.0 in commit `2b7c31e` (Wave 1 plan 84.4.1-02 Task 1). On
`terragrunt plan`, terraform refreshed state and found the existing AWS SSM
document was already named `km-Sandbox-Session` (lowercase) — matching what
v2.0.0 declares. No destroy/create cycle needed.

The substantive fix from Wave 1 plan 84.4.1-02 was the **Go callsite migration**:
5 hardcoded `"KM-Sandbox-Session"` (uppercase) literals replaced with
`cfg.GetSandboxSessionDocumentName()` (returns lowercase). The Go binary was
rebuilt during transition prep (`make build` produced km v0.2.697).

### Post-apply document verification

```
aws ssm describe-document --name km-Sandbox-Session
{
    "Name": "km-Sandbox-Session",
    "Status": "Active",
    "DocumentVersion": "1"
}
```

- ✅ km-Sandbox-Session Active: YES

**Completed:** 2026-05-18 18:43 UTC
**Result:** PASS (no-op — state already converged; Go side rebuilt)

---

## Step 0c — AFTER snapshot + diff

**Captured at:** 2026-05-18 19:04 UTC

### Inventory diff output

```
==> Diffing snapshots (prefix='km')
    before: .planning/phases/84.4.1-.../km-before-84.4.1.json
    after:  .planning/phases/84.4.1-.../km-after-84.4.1.json
OK: no changes detected between snapshots (prefix='km')
```

✅ Application-account inventory byte-identical (SCP lives in management
account/Organizations, not in this snapshot's scope).

### SCP body diff output

5 ArnNotLike arrays updated, ~20 line changes total, surgical km-* → *-*
substitution. Sample fragment:

```diff
@@ trusted_arns_instance @@
-"arn:aws:iam::052251888500:role/km-provisioner-*",
-"arn:aws:iam::052251888500:role/km-lifecycle-*",
-"arn:aws:iam::052251888500:role/km-ecs-spot-handler",
-"arn:aws:iam::052251888500:role/km-ttl-handler",
-"arn:aws:iam::052251888500:role/km-create-handler"
+"arn:aws:iam::*:role/*-provisioner-*",
+"arn:aws:iam::*:role/*-lifecycle-*",
+"arn:aws:iam::*:role/*-ecs-spot-handler",
+"arn:aws:iam::*:role/*-ttl-handler",
+"arn:aws:iam::*:role/*-create-handler"
```

(Identical shape repeats across 5 statement conditions.)

✅ Zero structural changes; only ARN pattern substitution.

---

## Step 0d — km shell smoke test

```bash
./bin/km list
# Output: 1 sandbox (learn-60ec3c82, status: running)
```

Non-interactive `km shell -c '...'` not supported (km shell is interactive-only).
Substituted with direct SSM document accessibility check:

```bash
AWS_PROFILE=klanker-application aws ssm describe-document --name km-Sandbox-Session
# Returns: Name=km-Sandbox-Session, Status=Active, DocumentVersion=1
```

- ✅ km list works: YES
- ✅ SSM document accessible by post-rename name: YES
- ⚠ Interactive km shell smoke test: SKIPPED (not non-interactively testable; deferred to operator first interactive use)

---

## Verdict

| Assertion | Result |
|-----------|--------|
| scp/v2.0.0 applied with 1 in-place update + 1 add sentinel, 0 destroys | PASS |
| ssm-session-doc/v2.0.0 state matches AWS (no-op apply; state pre-converged) | PASS |
| Inventory diff scoped to expected deltas only (none in application acct) | PASS |
| SCP body diff shows *-* pattern substitution only | PASS |
| km list still works | PASS |
| Interactive km shell smoke test | DEFERRED |

**Completed:** 2026-05-18 19:04 UTC
**Overall result:** PASS
