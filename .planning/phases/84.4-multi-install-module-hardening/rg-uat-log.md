# Phase 84.4 Fresh-Prefix UAT — rg install

**Started:** 2026-05-18T04:28:00Z
**Completed:** 2026-05-18 (full session)
**Fresh prefix:** rg
**AWS account:** 052251888500 (klanker-application)
**km install baseline:** rg-uat-before.json (captured 2026-05-18T04:28:24Z)
**Operator:** KPH
**Verdict:** PARTIAL PASS — resource-naming layer validated; cross-install identity/permission/sharing layer NOT validated

## Pre-UAT km install resources (MUST be unchanged at UAT end)

Captured from rg-uat-before.json:

| Service | Count | Key Resources |
|---------|-------|---------------|
| IAM Roles | 19 | km-budget-enforcer-*, km-create-handler, km-ec2spot-ssm-*, km-email-handler, km-github-token-refresher-*, km-github-token-scheduler-*, km-s3-replication-km-artifacts-12345, km-slack-bridge-role, km-ttl-handler, km-ttl-scheduler |
| S3 Buckets | 2 | km-artifacts-12345, km-artifacts-12345-replica |
| Lambda | 0 | (inventory script prefix alignment — km Lambdas present in AWS) |
| DynamoDB | 0 | (script prefix filter) |
| EFS | 0 | (no km-efs in this snapshot) |
| KMS aliases | 0 | (alias/km-* filter) |
| SES rule sets | 1 | sandbox-email-shared with rules [km-operator-inbound, km-sandbox-catchall] |
| Route53 zones | 2 | Two hosted zones present |

> Note: The inventory script filters by `starts_with(prefix)` — some km resources may have different naming schemes. The before→after diff is the authoritative isolation assertion.

## UAT Steps

### Task 1 — BEFORE snapshot + Clone + Configure (2026-05-18T04:28:00Z)

**Status:** COMPLETE

- km install verified at git head: `2f2747a docs(84.4-07): complete whereiskurt teardown UAT`
- `make build` succeeded: km v0.2.676 (2f2747a)
- rg-uat-before.json captured
- klanker-maker-rg/ cloned from /Users/khundeck/working/klankrmkr at same git head
- klanker-maker-rg/bin/km built via `make build`

---

### Step 2 — km configure (prefix=rg)

**Status:** COMPLETE WITH FRICTION (1 workaround applied)

Operator ran `./bin/km configure` in klanker-maker-rg/. Settings provided:
- `resource_prefix`: rg
- `email_subdomain`: sandboxes (shared domain — same as km install)
- `state_bucket`: tf-rg-state-use1-0123456 (user-supplied; treated the example suffix as literal)
- All other settings matched km install values

**km-config.yaml result (secrets redacted):**
```yaml
resource_prefix: rg
domain: klankermaker.ai
email_subdomain: sandboxes
region: us-east-1
state_bucket: tf-rg-state-use1-0123456   # ← incorrect — see gap below
artifacts_bucket: rg-artifacts-052251888500
operator_email: operator-rg@sandboxes.klankermaker.ai
accounts:
  organization: 481723467561
  dns_parent: 481723467561
  application: 052251888500
  terraform: 481723467561
```

**Gap discovered:** `km configure` showed no computed default for `state_bucket` — operator entered a custom value. Bootstrap then created `tf-rg-state-use1` (site.hcl-derived), leaving km-config.yaml out of sync with actual backend.
**Workaround:** `sed -i '' 's|tf-rg-state-use1-0123456|tf-rg-state-use1|' km-config.yaml`
**Deferred:** Phase 84.4.1 — configure should show computed default `tf-${prefix}-state-${region}`.

---

### Step 3 — km bootstrap --dry-run=false (plain — SCP/KMS/artifacts/state)

**Status:** COMPLETE

```
./bin/km bootstrap --dry-run=false
```

Outputs observed:
- tf-rg-state-use1 S3 bucket created
- alias/rg-platform KMS alias + key created
- rg-artifacts-052251888500 S3 bucket created
- rg-artifacts-052251888500-replica S3 bucket created (replication pair)
- SCP `rg-sandbox-containment` policy created and attached to application account 052251888500

**Status banner:**
```
[km bootstrap] resource_prefix=rg domain=klankermaker.ai region=us-east-1
✓ state bucket ready: tf-rg-state-use1
✓ artifacts bucket ready: rg-artifacts-052251888500
✓ KMS key ready: alias/rg-platform
✓ SCP attached: rg-sandbox-containment → account 052251888500
```

---

### Step 4 — km bootstrap --shared-ses --dry-run=false

**Status:** COMPLETE WITH MANUAL WORKAROUNDS (2 gaps discovered)

**Gap 1 (PRIMARY 84.4.1): Plan 06 auto-import did NOT fire**

Expected: `detectSharedSESState` would see that domain identity `sandboxes.klankermaker.ai` already exists in AWS (from km install) and set `registerID = false`, triggering `autoImportFoundationSESRecords`.

Observed: Bootstrap output showed `Shared SES domain identity: creating`. Terraform then attempted to create 3 DKIM CNAME records at `*._domainkey.sandboxes.klankermaker.ai`. Route53 rejected all 3:
```
Error: [CREATED] InvalidChangeBatch: Tried to create resource record set
[Name: 'aaa._domainkey.sandboxes.klankermaker.ai.', Type: 'CNAME']
but it already exists
```

Root cause: `detectSharedSESState` returned `registerID = true` despite the SES identity already existing in AWS. The auto-import gate is keyed on `registerID`; incorrect gate value → auto-import skipped entirely.

**Workaround:** Manual `terragrunt import` for each DKIM CNAME record:
```bash
cd klanker-maker-rg/infra/live/use1/ses-shared-rule-set/
# get the 3 DKIM tokens from SES
aws ses get-identity-dkim-attributes \
  --identities sandboxes.klankermaker.ai \
  --query 'DkimAttributes.*.DkimTokens[]' --output text

# import each CNAME into rg's foundation state
terragrunt import 'aws_route53_record.dkim[0]' '<zone-id>_<token0>._domainkey.sandboxes.klankermaker.ai_CNAME'
terragrunt import 'aws_route53_record.dkim[1]' '<zone-id>_<token1>._domainkey.sandboxes.klankermaker.ai_CNAME'
terragrunt import 'aws_route53_record.dkim[2]' '<zone-id>_<token2>._domainkey.sandboxes.klankermaker.ai_CNAME'
```

After imports, `km bootstrap --shared-ses --dry-run=false` completed successfully.

**Gap 2 (FRESH-CLONE): region.hcl not present before km init**

```
Error: Call to function "read_terragrunt_config" failed:
Path: "../region.hcl" — file does not exist
```

`region.hcl` is gitignored, only written by `km init`. But `km bootstrap --shared-ses` depends on it for `ses-shared-rule-set/terragrunt.hcl`.

**Workaround:** Manual write:
```bash
cat > infra/live/use1/region.hcl <<EOF
locals {
  region_label = "use1"
  region_full  = "us-east-1"
}
EOF
```

---

### Step 5 — km init --plan (after Lambda zips fix)

**Status:** COMPLETE WITH WORKAROUND (Lambda zip gap)

**Gap discovered: make build-lambdas drifted from init.go**

`./bin/km init --plan` failed immediately:
```
Error: filebase64sha256: open .../build/create-handler.zip: no such file or directory
```

`make build-lambdas` builds only 4 of 6 required Lambda zips (missing create-handler.zip and km-slack-bridge.zip).

**Workaround:**
```bash
./bin/km init --lambdas   # builds all 6 via init.go:buildLambdaZips()
```

After Lambda zips built:

**km init --plan result:**
```
Planning 15 modules...

Module: network            Plan: 8 add, 0 change, 0 destroy — OK
Module: efs                Plan: 3 add, 0 change, 0 destroy — OK
Module: s3-replication     Plan: 7 add, 0 change, 0 destroy — OK
Module: scp                Plan: 2 add, 0 change, 0 destroy — OK
Module: create-handler     Plan: 12 add, 0 change, 0 destroy — OK
Module: ttl-handler        Plan: 8 add, 0 change, 0 destroy — OK
...
Total: 113 add, 0 change, 0 destroy across 15 modules

Destroy-class gate: 0 protected resources in plan — CLEAR
```

**Result:** Destroy-class gate CLEAR. 113 adds / 0 changes / 0 destroys. No protected resources tripped.

---

### Step 6 — km init --dry-run=false

**Status:** COMPLETE WITH IMPORT WORKAROUND (ssm-session-doc gap)

```
./bin/km init --dry-run=false 2>&1 | tee /tmp/rg-init-apply.log
```

**Gap discovered (TIER-1): ssm-session-doc not migrated to v2.0.0**

Apply failed at ssm-session-doc module:
```
Error: creating SSM Document (KM-Sandbox-Session): DocumentAlreadyExists
  status code: 400
```

`infra/modules/ssm-session-doc/v1.0.0/main.tf` hardcodes `KM-Sandbox-Session`. Both installs tried to create the same document name.

**Workaround:**
```bash
cd klanker-maker-rg/infra/live/use1/ssm-session-doc/
terragrunt import 'aws_ssm_document.km_sandbox_session' 'KM-Sandbox-Session'
```

After import, apply continued. All 16 regional modules applied successfully:

| Module | Result |
|--------|--------|
| network | APPLIED — VPC vpc-074cc99447b8c02f6 created |
| efs | APPLIED — fs-06dcf5e3b8acc3a27 created |
| s3-replication | APPLIED — replication rules configured |
| scp | APPLIED — rg-sandbox-containment policy live |
| create-handler | APPLIED — rg-create-handler Lambda deployed |
| ttl-handler | APPLIED — rg-ttl-handler Lambda deployed |
| email-create-handler | APPLIED — rg-email-create-handler Lambda deployed |
| ses | APPLIED — rg-operator-inbound + rg-sandbox-catchall rules added |
| ssm-session-doc | APPLIED (via import) — KM-Sandbox-Session already exists |
| ...other modules | APPLIED — all green |

**SSM key:** `/rg/config/remote-create/safe-phrase` created.
**Total:** 16/16 modules green (1 required import workaround).

---

### Step 7 — km create rg2 + Terraform version error + fix + create rg3 + SCP design gap

**Status:** PARTIAL — sandbox created, SCP design gap discovered

**Sub-step 7a: Terraform version error (now fixed)**

First create attempt:
```
./bin/km create profiles/learn.yaml
```

Sandbox rg2 failed in create-handler Lambda:
```
Unsupported Terraform Core version. This configuration does not support
Terraform version 1.6.6. root.hcl requires >= 1.7.0
```

Root cause: `init.go:1684` hardcoded `tfVersion := "1.6.6"` but root.hcl requires >= 1.7.0.

**Fix landed in-session as commit d5d554b:** Bumped tfVersion to 1.9.8. Operator applied fix:
```bash
# In klankrmkr/ (the source):
# d5d554b fix(84.4): bump bundled terraform from 1.6.6 to 1.9.8 to match root.hcl
rm -f build/terraform
make build
./bin/km init --sidecars   # re-upload toolchain
```

Applied same fix to klanker-maker-rg/ clone via sed and rebuild.

**Sub-step 7b: Create rg3 — SCP design gap (CO-PRIMARY 84.4.1)**

Second create attempt (rg3):
```
./bin/km create profiles/learn.yaml
```

Sandbox rg3 failed at first AWS create call:
```
api error UnauthorizedOperation: You are not authorized to perform:
ec2:CreateSecurityGroup on resource: arn:aws:ec2:us-east-1:052251888500:vpc/...
with an explicit deny in a service control policy:
arn:aws:organizations::481723467561:policy/o-om3mjz6hu8/service_control_policy/p-cvd490xt

User: arn:aws:sts::052251888500:assumed-role/rg-create-handler/rg-create-handler
```

Also denied: `iam:CreateRole` for `rg-ec2spot-ssm-learn-a21eddb6-use1`.

Root cause: The canonical km install's SCP `p-cvd490xt` (km-sandbox-containment) only trusts `km-*` role ARNs in its ArnNotLike allowlist. When rg install tries to create resources, the km SCP fires — the two installs' SCPs compose by AND-intersection, so rg's create-handler is denied by km's SCP even though rg's own SCP doesn't deny it.

Plan 02 solved per-install SCP *naming* and *templating*, but did not address cross-install SCP AND-composition behavior.

**Workaround applied:** Manually added `arn:aws:iam::052251888500:role/rg-create-handler` to km's deployed SCP `p-cvd490xt` allowlist via AWS console. Also needed to add rg-ec2spot-ssm-* role pattern.

**After workaround:** rg3 sandbox creation succeeded.
- Sandbox rg3 reached healthy state
- initCommands ran
- `/workspace` mounted (EFS fs-06dcf5e3b8acc3a27)

```
./bin/km list
ID          PREFIX  STATUS   SUBSTRATE  AGE
rg3-*       rg      healthy  ec2        3m
```

**km otel rg3:**
```
./bin/km otel rg3-<id> --prompts
[AI spend entries visible — sandbox Bedrock calls logged]
```

**km shell rg3:**
```
./bin/km shell rg3-<id>
# Inside sandbox:
sandbox@rg3:~$ ls /workspace    # EFS mount present
sandbox@rg3:~$ exit
```

---

### Step 8 — km destroy rg3 --remote --yes

**Status:** COMPLETE

```
./bin/km destroy rg3-<id> --remote --yes
```

Clean teardown. All rg3 sandbox resources removed. rg install Lambdas and regional infrastructure intact.

---

### Step 9 — km uninit (teardown rg regional infrastructure)

**Status:** COMPLETE WITH FIX (km uninit nil-pointer panic fixed in-session)

**km uninit nil-pointer panic fix (commit 2861dbb):**

`km uninit` panicked on the sandbox lister call due to `dynamoClient` and `tableName` not being wired into the `awsSandboxLister` construction (uninit.go hand-rolled constructor skipped both fields, causing nil-pointer dereference on first lister call).

**Fix landed in-session as commit 2861dbb:** Use canonical `newRealLister()` constructor. Operator used `--force` flag as workaround for the active session.

```
./bin/km uninit --force
```

Teardown progress:
```
Destroying 16 modules...
Module: ses              — destroyed (rg-operator-inbound, rg-sandbox-catchall rules removed)
Module: ssm-session-doc  — destroyed (KM-Sandbox-Session DELETED from AWS — see TIER-1 gap)
Module: create-handler   — destroyed (rg-create-handler Lambda deleted)
Module: ttl-handler      — destroyed
Module: email-create-handler — destroyed
Module: efs              — destroyed (fs-06dcf5e3b8acc3a27 deleted)
Module: s3-replication   — destroyed
Module: network          — destroyed (vpc-074cc99447b8c02f6 deleted)
...
Total: 16/16 modules destroyed
```

**TIER-1 gap triggered:** `km uninit` destroyed the `ssm-session-doc` module, which deleted the shared `KM-Sandbox-Session` SSM document from AWS. The km install's state still referenced it.

**Symptom:** After rg teardown, `km shell kmsmoke` failed:
```
InvalidDocument: Document with name KM-Sandbox-Session does not exist
```

**Recovery:**
```bash
cd /Users/khundeck/working/klankrmkr/infra/live/use1/ssm-session-doc/
terragrunt apply -auto-approve
# Re-creates KM-Sandbox-Session under km's state
```

After recovery, km install fully functional.

---

### Step 10 — km unbootstrap

**Status:** COMPLETE WITH MANUAL CLEANUP

```
./bin/km unbootstrap
```

Removed:
- tf-rg-state-use1 S3 bucket
- rg-artifacts-052251888500 S3 bucket
- rg-artifacts-052251888500-replica S3 bucket
- /rg/ SSM parameters
- alias/rg-platform KMS alias (key scheduled for deletion 2026-05-25)
- rg SES rules confirmed absent from shared rule set

**NOT removed by km unbootstrap (gap):**
- tf-rg-locks-use1 DynamoDB lock table (unbootstrap gap — documented in deferred-items.md)
- rg-sandbox-containment SCP (manual detach required)

**Manual cleanup:**
```bash
# Delete orphan DynamoDB lock table
aws dynamodb delete-table --table-name tf-rg-locks-use1

# SCP detach and delete (via management account profile)
AWS_PROFILE=management aws organizations detach-policy \
  --policy-id <rg-scp-id> --target-id 052251888500
AWS_PROFILE=management aws organizations delete-policy \
  --policy-id <rg-scp-id>
```

Also: state rm for SCP resources before uninit (preventing production SCP destruction):
```bash
terragrunt state rm aws_organizations_policy_attachment.scp
terragrunt state rm aws_organizations_policy.scp
terragrunt state rm data.aws_iam_policy_document.scp
```

---

### Step 11 — Post-teardown snapshot + km install verification

**Status:** COMPLETE (see rg-uat-after.json)

**SES rule set verification:**
```bash
aws ses describe-receipt-rule-set --rule-set-name sandbox-email-shared \
  --query 'Rules[].Name'
["km-operator-inbound","km-sandbox-catchall"]
```

Only km-* rules remain. No rg-* orphans. ✓

**km install health verification:**
```
km list           → km install sandboxes listed correctly
km create ...     → new km sandbox created successfully
km shell kmsmoke  → SSM session opens (after ssm-session-doc re-apply)
km rsync list     → artifacts bucket accessible
```

km install fully healthy after recovery.

---

## Phase 84.4 Closure Criteria Verification

| Criterion | Status | Evidence |
|-----------|--------|----------|
| (a) zero km- literals in v2.0.0 modules | PASS | pkg/terragrunt TestModuleNamesUseResourcePrefix passes for efs/v2.0.0 |
| (b) resource_prefix wired via inputs={} | PASS | Plan 05 live wiring flip — all v2.0.0 modules receive resource_prefix from site.hcl |
| (c) DKIM auto-import on second install | FAIL | detectSharedSESState returned registerID=true despite domain existing → manual terragrunt import required |
| (d) SCP per-install architecture shipped | PARTIAL | Module ships, naming works — but cross-install AND-composition breaks sandbox create |
| (e) EFS per-install architecture shipped | PASS | rg EFS fs-06dcf5e3b8acc3a27 applied cleanly, no creation_token collision |
| (f) whereiskurt probe cleanly torn down | PASS | Plan 07 SUMMARY — teardown verified, closure criterion (f) satisfied |
| (g) rg fresh-prefix UAT end-to-end + clean removal | PARTIAL PASS | Resource naming layer: PASS. Cross-install identity/permission/sharing layer: FAIL. km isolation: PASS after manual recovery. |

**Overall verdict: PARTIAL PASS**

Resource-naming layer of Phase 84.4's thesis is validated:
- v2.0.0 modules template all names from var.resource_prefix ✓
- km init --plan shows 0 destroy on fresh install ✓
- All 16 regional modules applied green ✓
- EFS per-install (no creation_token collision) ✓
- km install isolation maintained (with manual recovery after ssm-session-doc gap) ✓

Cross-install identity/permission/sharing layer is NOT validated:
- SES auto-import (Plan 06 criterion c): requires manual terragrunt import — not frictionless ✗
- SCP AND-composition (Plan 02 design): second install's create-handler denied by first install's SCP ✗
- ssm-session-doc shared resource: teardown of second install deletes shared document (TIER-1) ✗

**Phase 84.4.1 required** before the multi-install thesis can be declared fully proven.

**Completed:** 2026-05-18

## In-Session Fix Summary

| Commit | Fix | Impact |
|--------|-----|--------|
| 2861dbb | Wire dynamoClient+tableName in km uninit's sandbox lister | Fixes nil-pointer panic in uninit |
| d5d554b | Bump bundled terraform 1.6.6 → 1.9.8 to match root.hcl | Fixes sandbox creation failure on all fresh installs |
| d551bba | isPlaceholderBucket no longer treats "km-artifacts-12345" as fake | Fixes cfg.Load() false-positive hard-fail for legacy bucket names |

## 84.4.1 Gap Priority

| Gap | Priority | Description |
|-----|----------|-------------|
| ssm-session-doc cross-install destruction | TIER-1 (blocking) | Import workaround is unsafe — teardown of second install deletes shared doc |
| SCP AND-composition | TIER-1 (blocking) | km SCP denies rg create-handler — sandbox lifecycle broken |
| Plan 06 auto-import not firing | PRIMARY | detectSharedSESState gate logic incorrect for shared-domain scenario |
| km unbootstrap leaves DynamoDB lock table | HIGH | Manual cleanup required after every unbootstrap |
| downloadTerraform stale binary cache | HIGH | Must `rm -f build/terraform` after tfVersion bump |
| region.hcl prereq missing for fresh bootstrap --shared-ses | HIGH | Manual write required on fresh clones |
| km configure state_bucket missing default | MEDIUM | Operator entered non-matching value |
| make build-lambdas drifted from init.go | MEDIUM | 2 of 6 zips not built by Makefile target |
