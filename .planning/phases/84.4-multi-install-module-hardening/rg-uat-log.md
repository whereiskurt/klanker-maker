# Phase 84.4 Fresh-Prefix UAT — rg install

**Started:** 2026-05-18T04:28:00Z
**Fresh prefix:** rg
**AWS account:** 052251888500 (klanker-application)
**km install baseline:** rg-uat-before.json (captured 2026-05-18T04:28:24Z)
**Operator:** KPH

## Pre-UAT km install resources (MUST be unchanged at UAT end)

Captured from rg-uat-before.json — these are the km-* resources that MUST be byte-identical after the rg UAT:

| Service | Count | Key Resources |
|---------|-------|---------------|
| IAM Roles | 19 | km-budget-enforcer-*, km-create-handler, km-ec2spot-ssm-*, km-email-handler, km-github-token-refresher-*, km-github-token-scheduler-*, km-s3-replication-km-artifacts-12345, km-slack-bridge-role, km-ttl-handler, km-ttl-scheduler |
| S3 Buckets | 2 | km-artifacts-12345, km-artifacts-12345-replica |
| Lambda | 0 | (km- lambdas query returned 0 — inventory script may need prefix alignment) |
| DynamoDB | 0 | (km- tables returned 0 — script queries `km-` prefix) |
| EFS | 0 | (no km-efs filesystems in this snapshot) |
| KMS aliases | 0 | (alias/km-* returned 0 — verify km-platform key is present) |
| SES rule sets | 0 | (list-receipt-rule-sets returned empty — check SES config) |
| Route53 zones | 2 | Two hosted zones present |

> Note: The inventory script filters by `starts_with(prefix)` — some km resources (KMS key, DynamoDB sandbox table, Lambda functions) may be named differently. The before→after diff is the authoritative isolation assertion; individual counts are for orientation.

## UAT Steps

### Task 1 — BEFORE snapshot + Clone + Configure (2026-05-18T04:28:00Z)

**Status:** COMPLETE (automated — Task 1 type="auto")

- km install verified at git head: `2f2747a docs(84.4-07): complete whereiskurt teardown UAT`
- `make build` succeeded: km v0.2.676 (2f2747a)
- rg-uat-before.json captured (see above)
- klanker-maker-rg/ clone pending (operator action — see Task 2 checkpoint below)
- km configure for prefix=rg pending (operator action)

### Task 2 — Operator: rg bootstrap + apply + sandbox lifecycle (Steps 3-8)

**Status:** PENDING — awaiting operator execution

See checkpoint below for exact commands.

### Task 3 — Operator: rg uninit + unbootstrap + AFTER snapshot (Steps 9-11)

**Status:** PENDING — awaiting Task 2 completion

