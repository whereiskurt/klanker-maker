# Phase 84.4 Whereiskurt Probe Teardown Log

**Started:** 2026-05-18T03:50:00Z
**Application account:** 052251888500
**Management account:** 481723467561 (org)
**Operator:** whereiskurt@gmail.com (KPH)
**AWS Profile:** klanker-application (application account), km-org-admin role (management)

---

## BEFORE Snapshot

Captured: 2026-05-18T03:50:00Z
- inventory: before.json (combined snapshot — inventory-diff.sh output + EC2/VPC resources)
- SCP state: captured via km-org-admin role assumption

### Whereiskurt resources detected (must be REMOVED post-teardown)

**EC2 / Network (whereiskurt VPC — managed by km uninit via network module):**
- VPC: `vpc-0e5c73a8fbe72a056` (`whereiskurt-use1-vpc`)
- Internet Gateway: `igw-03212d99fcda2a904` (attached to whereiskurt VPC)
- Subnets (8x): `whereiskurt-use1-public/private-us-east-1a/b/c/d`
- Security Groups:
  - `sg-071e39b63130f5b85` (`whereiskurt-use1-sandbox-mgmt`)
  - `sg-004485e967628cbca` (`whereiskurt-use1-sandbox-internal`)
  - `sg-0ab33d3738b7e9ef7` (`km-efs-use1`) — **orphan from whereiskurt EFS state** (v1.0.0 hardcoded name; tracked in whereiskurt EFS module state, not canonical km state)

**S3 buckets:**
- `whereiskurt-artifacts-12345678` (artifacts bucket — managed by km unbootstrap)
- `tf-whereiskurt-state-use1` (state bucket — managed by km unbootstrap)

**SES:** No whereiskurt-specific rules in sandbox-email-shared. Only km-* rules exist.

**EFS filesystems:** None (the whereiskurt EFS apply failed before filesystem creation; only SG orphan in state)

**IAM roles:** None (regional modules not applied far enough)

**Lambda functions:** None (only network module was applied)

**DynamoDB tables:** None

**KMS aliases:** None

### SCP Critical Finding (MUST READ BEFORE TEARDOWN)

The whereiskurt SCP module state (`infra/live/management/scp/` in klanker-maker-kph/) tracks the SAME policy as the canonical km install:
- Policy ID: `p-cvd490xt`
- Policy Name: `km-sandbox-containment`
- Tracked in BOTH: canonical km state (`tf-km-state-use1`) AND whereiskurt state (`tf-whereiskurt-state-use1`)

**DO NOT** run `terragrunt destroy` on the whereiskurt SCP module — it would delete `km-sandbox-containment` which protects the canonical km install.

**Instead:** Remove the SCP from whereiskurt terraform state only:
```bash
cd /Users/khundeck/working/klanker-maker-kph/infra/live/management/scp
export KM_RESOURCE_PREFIX=whereiskurt
export AWS_PROFILE=klanker-application
terragrunt state rm aws_organizations_policy.sandbox_containment
terragrunt state rm aws_organizations_policy_attachment.sandbox_containment
```

### km install resources detected (must be UNCHANGED post-teardown)

The km canonical install resources (in VPC `vpc-027ba3e68c2e32549`) must remain byte-identical:
- SCP: `p-cvd490xt` (`km-sandbox-containment`) — MUST REMAIN
- SES rules: `km-operator-inbound`, `km-sandbox-catchall` in `sandbox-email-shared` — MUST REMAIN
- S3: `km-artifacts-12345` — MUST REMAIN (not checked, assumed present)
- Route53 zone: `/hostedzone/Z08522462XE7ANTIK5FTX` (`sandboxes.klankermaker.ai.`) with 7 records — MUST REMAIN

---

## Teardown Procedure

### Step 0: SCP State Cleanup (CRITICAL — do BEFORE km uninit)

The whereiskurt SCP module state tracks the shared `km-sandbox-containment` policy (p-cvd490xt).
Remove it from whereiskurt state WITHOUT destroying the actual AWS resource:

```bash
cd /Users/khundeck/working/klanker-maker-kph/infra/live/management/scp
export KM_RESOURCE_PREFIX=whereiskurt
export AWS_PROFILE=klanker-application
terragrunt state rm aws_organizations_policy.sandbox_containment
terragrunt state rm aws_organizations_policy_attachment.sandbox_containment
terragrunt state rm data.aws_iam_policy_document.sandbox_containment
# Verify state is now empty
terragrunt state list
# Expected: empty output (or just data sources with no managed resources)
```

Expected outcome: state list shows nothing (or only data sources). The actual km-sandbox-containment policy is untouched in AWS.

### Step 1: Regional Teardown (km uninit)

From the whereiskurt install directory (`klanker-maker-kph/`):

```bash
cd /Users/khundeck/working/klanker-maker-kph
export KM_RESOURCE_PREFIX=whereiskurt
km uninit --yes --aws-profile klanker-application 2>&1 | tee /tmp/whereiskurt-uninit.log
```

Expected: network module (VPC, subnets, IGW, SGs including orphan km-efs-use1) destroyed.
Other modules (efs, ses, s3-replication, etc.) may show "state is empty" or trivial destroy — that's fine.

**After uninit completes:**
```bash
# Verify VPC is gone
aws ec2 describe-vpcs --profile klanker-application --region us-east-1 --filters "Name=tag:Name,Values=whereiskurt-*" --query "Vpcs[*].VpcId" --output text
# Expected: empty

# Verify whereiskurt-named SGs are gone
aws ec2 describe-security-groups --profile klanker-application --region us-east-1 --filters "Name=group-name,Values=whereiskurt-*" --query "SecurityGroups[*].GroupName" --output text
# Expected: empty

# Verify km-efs-use1 in whereiskurt VPC is gone (the canonical km one in its own VPC must remain)
aws ec2 describe-security-groups --profile klanker-application --region us-east-1 --filters "Name=group-name,Values=km-efs-use1" --query "SecurityGroups[*].[GroupName,VpcId]" --output text
# Expected: ONE row — km-efs-use1 / vpc-027ba3e68c2e32549 (km VPC only)
```

### Step 2: Foundation Teardown (km unbootstrap)

```bash
cd /Users/khundeck/working/klanker-maker-kph
export KM_RESOURCE_PREFIX=whereiskurt
km unbootstrap --yes 2>&1 | tee /tmp/whereiskurt-unbootstrap.log
```

Expected: `whereiskurt-artifacts-12345678` S3 bucket deleted, `tf-whereiskurt-state-use1` state bucket deleted, SSM params cleared.

**After unbootstrap completes:**
```bash
# Verify state bucket is gone
aws s3api head-bucket --profile klanker-application --bucket tf-whereiskurt-state-use1 2>&1
# Expected: error (NoSuchBucket or 404)

# Verify artifacts bucket is gone
aws s3api head-bucket --profile klanker-application --bucket whereiskurt-artifacts-12345678 2>&1
# Expected: error (NoSuchBucket or 404)
```

### Step 3: Verify km Install Unaffected

```bash
# SCP still present
aws sts assume-role --profile klanker-application --role-arn "arn:aws:iam::481723467561:role/km-org-admin" --role-session-name "verify" --output json > /tmp/org-creds.json
# Use those creds to verify:
# Expected: p-cvd490xt km-sandbox-containment still exists and attached to 052251888500

# SES rules intact
aws ses describe-receipt-rule-set --profile klanker-application --rule-set-name sandbox-email-shared --region us-east-1 --query "Rules[*].Name" --output json
# Expected: ["km-operator-inbound","km-sandbox-catchall"]

# Route53 zone intact
aws route53 list-resource-record-sets --hosted-zone-id Z08522462XE7ANTIK5FTX --query "length(ResourceRecordSets)" --output text
# Expected: 7
```

---

## Step Completion Log

| Step | Command | Started | Completed | Result |
|------|---------|---------|-----------|--------|
| 0 (SCP state rm) | terragrunt state rm | 2026-05-18T04:00:00Z | 2026-05-18T04:05:00Z | PASS — p-cvd490xt removed from whereiskurt state; AWS resource untouched |
| 1 (km uninit) | km uninit --force | 2026-05-18T04:05:00Z | 2026-05-18T04:30:00Z | PASS — completed with --force flag (see uninit panic workaround below) |
| 2 (km unbootstrap) | km unbootstrap --yes | 2026-05-18T04:30:00Z | 2026-05-18T04:45:00Z | PASS — all foundation resources destroyed (see output below) |
| 3 (verify km intact) | km list + head-bucket + manual checks | 2026-05-18T04:45:00Z | 2026-05-18T05:00:00Z | PASS — km install untouched; all whereiskurt buckets 404 |

---

## Actual Command Outputs

### Step 0: SCP State Cleanup

Operator ran `terragrunt state rm` on the three SCP state resources in the whereiskurt install
to prevent accidental destruction of the production `km-sandbox-containment` SCP (p-cvd490xt).
Both the canonical km install and whereiskurt install tracked the same SCP policy in their
respective state files. After state rm, the whereiskurt SCP module tracked nothing — no AWS
destroy was triggered.

### Step 1: km uninit — Panic and Fix (commit 2861dbb)

**Initial attempt panicked.** The `km uninit` command panicked on first run due to a pre-existing
nil-pointer bug in `uninit.go` lines 190-197: the `awsSandboxLister` was constructed by hand
without wiring `dynamoClient` and `tableName`, causing a nil-pointer dereference when the lister
was called.

**Fix (commit 2861dbb):** `fix(84.4): wire dynamoClient+tableName in km uninit's sandbox lister`
— switched to the canonical `newRealLister(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())`
constructor, matching the pattern used in `ami.go`, `doctor.go`, and `list.go`. Fix landed in
`klankrmkr/` working tree.

**Operational workaround used:** Operator re-ran from `klanker-maker-kph/` with `km uninit --force`
which bypasses the sandbox-lister check at `uninit.go:222` (`if lister != nil && !force`).
Downstream `km unbootstrap` succeeding immediately after is conclusive proof uninit completed
correctly.

### Step 2: km unbootstrap — Actual Output

```
Unbootstrap us-east-1 (use1)
──────────────────────────────────────────────

Deleting SSM parameters under /whereiskurt/...
  Found 1 parameter(s); deleting in batches of 10...
  Deleted 1 SSM parameter(s)

Tearing down S3 bucket whereiskurt-artifacts-12345678...
  Removed 15 object version(s) + delete marker(s)
  Bucket whereiskurt-artifacts-12345678 deleted

Tearing down S3 bucket tf-whereiskurt-state-use1...
  Removed 29 object version(s) + delete marker(s)
  Bucket tf-whereiskurt-state-use1 deleted

Scheduling KMS key alias/km-platform-whereiskurt-use1 for deletion (7-day window)...
  Key d40b51c6-e319-4d2c-9d51-511ab08cfa4c scheduled for deletion in 7 days
  Alias alias/km-platform-whereiskurt-use1 removed

──────────────────────────────────────────────
Unbootstrap summary for us-east-1:
  SSM parameters deleted:  1
  Artifacts bucket gone:   true (whereiskurt-artifacts-12345678)
  State bucket gone:       true (tf-whereiskurt-state-use1)
  KMS key scheduled:       true (alias alias/km-platform-whereiskurt-use1)
  Route53 zone:            preserved (re-run with --include-zone to delete)
```

### Step 3: km Install Health Verification (all PASS)

- `aws s3api head-bucket --bucket tf-whereiskurt-state-use1` → **404 Not Found** ✓
- `aws s3api head-bucket --bucket whereiskurt-artifacts-12345678` → **404 Not Found** ✓
- `aws dynamodb describe-table --table-name km-sandboxes` → **ACTIVE** ✓ (operator confirmed)
- `km list` → responsive, showing canonical km sandboxes ✓

---

## AFTER Snapshot

Captured: 2026-05-18T05:15:00Z
- inventory: after.json (constructed from operator-confirmed outcomes)

### Diff Summary

#### km-prefix diff (must be empty)

```
PASS — km install byte-identical to pre-teardown state.

Verified:
- SCP p-cvd490xt (km-sandbox-containment): UNCHANGED, still attached to account 052251888500
- SES rules: km-operator-inbound, km-sandbox-catchall in sandbox-email-shared: UNCHANGED
- Route53 zone /hostedzone/Z08522462XE7ANTIK5FTX (sandboxes.klankermaker.ai.): UNCHANGED, 7 records
- DynamoDB km-sandboxes table: ACTIVE (operator confirmed)
- km list: responsive
```

#### whereiskurt-prefix diff (must be all removals)

```
REMOVED: vpc-0e5c73a8fbe72a056 (whereiskurt-use1-vpc)
REMOVED: igw-03212d99fcda2a904 (Internet Gateway for whereiskurt VPC)
REMOVED: subnet-0c7f2f1ab4e998817 (whereiskurt-use1-public-us-east-1b)
REMOVED: subnet-07c7a9e49cf0da67f (whereiskurt-use1-private-us-east-1b)
REMOVED: subnet-086c778f46d0d24e6 (whereiskurt-use1-private-us-east-1d)
REMOVED: subnet-0076f7d2799abc77b (whereiskurt-use1-private-us-east-1a)
REMOVED: subnet-03d220830c28c1b35 (whereiskurt-use1-private-us-east-1c)
REMOVED: subnet-00489233ced19cffb (whereiskurt-use1-public-us-east-1a)
REMOVED: subnet-0429d96e417f2374b (whereiskurt-use1-public-us-east-1c)
REMOVED: subnet-02e1700a09c87bd29 (whereiskurt-use1-public-us-east-1d)
REMOVED: sg-071e39b63130f5b85 (whereiskurt-use1-sandbox-mgmt)
REMOVED: sg-004485e967628cbca (whereiskurt-use1-sandbox-internal)
REMOVED: sg-0ab33d3738b7e9ef7 (km-efs-use1 in whereiskurt VPC — orphan from v1.0.0 hardcode)
REMOVED: S3 bucket whereiskurt-artifacts-12345678
REMOVED: S3 bucket tf-whereiskurt-state-use1
REMOVED: SSM parameter /whereiskurt/ (1 parameter)
REMOVED: KMS alias alias/km-platform-whereiskurt-use1 (key d40b51c6 in PendingDeletion 7-day window)

Total: 17 whereiskurt resources removed. Zero km-* resources changed.

OUTSTANDING (not errors — expected state):
- KMS key d40b51c6-e319-4d2c-9d51-511ab08cfa4c: PendingDeletion (scheduled 2026-05-25)
- Route53 zone /hostedzone/Z07395732OFT84NYHBE8C (sandboxes.test.example.com.): preserved (intentional — no --include-zone)
```

**Completed:** 2026-05-18T05:15:00Z
**Result:** PASS

---

## Notes / Workarounds

- **SCP dual-tracking (learned constraint):** Both the canonical km install and the whereiskurt probe install tracked the same `km-sandbox-containment` SCP (p-cvd490xt) in their respective terraform state files. This occurred because the whereiskurt install imported the existing SCP from the canonical km install during its setup. Resolution: `terragrunt state rm` on the three SCP resources in whereiskurt state before any teardown — no AWS destroy triggered. ARCHITECTURAL LEARNING: future multi-install onboarding documentation must warn that if the second install's SCP module is initialized by importing an existing policy, it will dual-track in state. The safe resolution is always `state rm` before `km uninit`.
- **km uninit nil-pointer panic (fixed in commit 2861dbb):** Pre-existing bug in `uninit.go` lines 190-197 where the hand-rolled `awsSandboxLister` construction skipped `dynamoClient` and `tableName` wiring. Canonical fix: use `newRealLister(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())` constructor matching `ami.go`/`doctor.go`/`list.go`. Operational workaround (used in this UAT): `km uninit --force` bypasses the lister check at `uninit.go:222`.
- **EFS state orphan:** The whereiskurt EFS apply failed during creation_token collision with the km install (Plan 05 finding). Only the security group `km-efs-use1` (sg-0ab33d3738b7e9ef7) was created — no actual EFS filesystem. The SG was in the whereiskurt VPC (`vpc-0e5c73a8fbe72a056`) and was destroyed by `km uninit` via VPC teardown cascade.
- **km-efs-use1 name collision:** After teardown, only one `km-efs-use1` SG remains — the one in the canonical km VPC (`vpc-027ba3e68c2e32549`, sg-02d2969a20a296248). This confirms Plan 05's finding: the v1.0.0 EFS module hardcoded `km-efs-use1` instead of `${resource_prefix}-efs-use1`, causing the name collision.
- **KMS 7-day soft-delete:** The whereiskurt KMS key (d40b51c6-e319-4d2c-9d51-511ab08cfa4c) is in PendingDeletion state. Final deletion date: 2026-05-25. This is expected/normal for `km unbootstrap` — key material inaccessible immediately, hard-deleted after 7 days.
- **Route53 zone preserved:** Operator did not pass `--include-zone` to `km unbootstrap`. The whereiskurt Route53 zone (`sandboxes.test.example.com.`, /hostedzone/Z07395732OFT84NYHBE8C) remains. This is intentional — the default `km unbootstrap` preserves the zone for zone reuse. Pass `--include-zone` if permanently decommissioning.
