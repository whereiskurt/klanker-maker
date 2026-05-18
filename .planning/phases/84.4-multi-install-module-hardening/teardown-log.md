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

*(To be filled in by operator as each step completes)*

| Step | Command | Started | Completed | Result |
|------|---------|---------|-----------|--------|
| 0 (SCP state rm) | terragrunt state rm | - | - | - |
| 1 (km uninit) | km uninit --yes | - | - | - |
| 2 (km unbootstrap) | km unbootstrap --yes | - | - | - |
| 3 (verify km intact) | manual checks | - | - | - |

---

## AFTER Snapshot

*(To be filled in after teardown completes)*

---

## Diff Summary

*(To be filled in after AFTER snapshot captured)*

### km-prefix diff (must be empty)

```
(pending)
```

### whereiskurt-prefix diff (must be all removals)

```
(pending)
```

**Completed:** (pending)
**Result:** (pending — PASS / FAIL)

---

## Notes / Workarounds

- **SCP dual-tracking:** Both the canonical km install and the whereiskurt probe install tracked the same `km-sandbox-containment` SCP (p-cvd490xt) in their respective terraform state files. This occurred because the whereiskurt install imported the existing SCP from the canonical km install during its setup. Resolution: remove from whereiskurt state only (no AWS destroy).
- **EFS state orphan:** The whereiskurt EFS apply failed during creation_token collision with the km install (Plan 05 finding). Only the security group `km-efs-use1` (sg-0ab33d3738b7e9ef7) was created — no actual EFS filesystem. The SG was in the whereiskurt VPC (`vpc-0e5c73a8fbe72a056`) and will be destroyed by `km uninit` via the network module's VPC teardown.
- **km-efs-use1 name collision:** After teardown, only one `km-efs-use1` SG should remain — the one in the canonical km VPC (`vpc-027ba3e68c2e32549`, sg-02d2969a20a296248).
