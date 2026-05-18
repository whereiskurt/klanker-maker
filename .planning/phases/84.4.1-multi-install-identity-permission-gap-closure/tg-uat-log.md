# Phase 84.4.1 Fresh-Prefix UAT-2 — tg install

**Started:** 2026-05-18T23:08:44Z
**Fresh prefix:** tg
**AWS account (application):** <fill in — same as km install>
**AWS account (management):** <fill in — same as km install>
**km install baseline:** tg-uat-before.json (post Phase 84.4.1 Wave 3 transition; equals km-after-84.4.1.json)
**Operator:** <fill in>
**Working directory for tg install:** klanker-maker-tg/ (sibling to klankrmkr/)

## Pre-UAT canonical km install resources

Resources listed in tg-uat-before.json that MUST be unchanged at UAT end:

**IAM roles (km-*):**
- km-budget-enforcer-learn-60ec3c82
- km-budget-scheduler-learn-60ec3c82
- km-create-handler
- km-ec2spot-ssm-learn-60ec3c82-use1
- km-email-handler
- km-github-token-refresher-learn-60ec3c82
- km-github-token-scheduler-learn-60ec3c82
- km-s3-replication-km-artifacts-12345
- km-slack-bridge-role
- km-ttl-handler
- km-ttl-scheduler

**S3 buckets:**
- km-artifacts-12345 (created 2026-03-24)
- km-artifacts-12345-replica (created 2026-03-25)

**Route53 hosted zones:**
- /hostedzone/Z07395732OFT84NYHBE8C — sandboxes.test.example.com. (2 records)
- /hostedzone/Z08522462XE7ANTIK5FTX — sandboxes.klankermaker.ai. (7 records)

**SSM documents (post Wave 3):**
- km-Sandbox-Session (Active, lowercase — verified by Wave 3 plan 05)

**SCP (post Wave 3):**
- km-sandbox-containment — policy id p-cvd490xt, *-* pattern allowlist applied

**Sandboxes currently running:** <count — fill in before UAT>
**km DynamoDB tables:** <list — fill in before UAT>

---

## Pre-requisite: Verify canonical km install at Phase 84.4.1 state

Before starting the tg UAT, verify the canonical install is at the correct deployed state:

```bash
cd /Users/khundeck/working/klankrmkr
git log -1 --oneline
# Expected: head of phase-84.4.1 branch (or main if merged)

make build

# Verify SCP has *-* pattern (not km-*)
# Requires klanker-terraform profile with km-org-admin assume-role
POLICY_ID=p-cvd490xt
aws --profile klanker-terraform organizations describe-policy \
  --policy-id $POLICY_ID \
  --query 'Policy.Content' --output text | grep '\*-create-handler' \
  && echo "OK: SCP at 84.4.1 state"

# Verify SSM document is km-Sandbox-Session (lowercase)
aws ssm describe-document --name km-Sandbox-Session \
  --query 'Document.Status' --output text
# Expected: Active
```

**Verdict:** PASS / FAIL — <fill in>

---

## Step 1: Clone + build klanker-maker-tg/

```bash
cd /Users/khundeck/working   # parent of klankrmkr/ — NOT inside it
git clone /Users/khundeck/working/klankrmkr klanker-maker-tg
cd klanker-maker-tg
git log -1 --oneline   # must match klankrmkr/
make build
ls build/*.zip | wc -l   # expect 6 (make build-lambdas parity — Wave 2 plan 04)
```

**Output:** <paste output>
**Verdict:** PASS / FAIL

---

## Step 2: km configure (resource_prefix: tg)

```bash
cd /Users/khundeck/working/klanker-maker-tg
./bin/km configure
```

Operator answers:
- resource_prefix: **tg**
- email_subdomain: <operator chooses — shared domain is fine, auto-import handles DKIM/MX/TXT>
- state_bucket: should display default `[tf-tg-state-use1]` (Wave 2 plan 04 UX fix — verify default appears)
- Accept or edit; HeadBucket retry should engage on 403

Expected: `km-config.yaml` written with `resource_prefix: tg`, `artifacts_bucket: tg-artifacts-<account_id>` (auto-derived).

**km-config.yaml (redacted):**
```yaml
# <paste contents with secrets redacted>
```

**State bucket default displayed:** YES / NO
**HeadBucket retry engaged:** YES / NO / N/A
**Verdict:** PASS / FAIL

---

## Step 3: km bootstrap (plain — SCP/KMS/artifacts/state)

```bash
cd /Users/khundeck/working/klanker-maker-tg
eval $(./bin/km env)
./bin/km bootstrap --dry-run=false 2>&1 | tee /tmp/tg-bootstrap.log
```

Expected:
- `region.hcl` auto-written as prerequisite (Wave 2 plan 04 fix — no manual write needed)
- tg state bucket created: `tf-tg-state-use1`
- tg KMS key + alias created
- tg artifacts bucket created: `tg-artifacts-<account_id>`
- tg SCP created: `tg-sandbox-containment` (with *-* pattern from scp/v2.0.0)

Sanity:
```bash
aws s3 ls | grep 'tg-'
```

**Log excerpt:** <paste key lines from /tmp/tg-bootstrap.log>
**region.hcl auto-written:** YES / NO
**tg state bucket:** <name>
**tg SCP created:** YES / NO
**Verdict:** PASS / FAIL

---

## Step 4: km bootstrap --shared-ses (auto-import load-bearing assertion)

This is the **Wave 1 plan 03 live verification** — the auto-import gate must fire
regardless of whether `registerID` is true or false.

```bash
cd /Users/khundeck/working/klanker-maker-tg
./bin/km bootstrap --shared-ses --dry-run=false 2>&1 | tee /tmp/tg-bootstrap-ses.log
```

Expected:
- Auto-import logic fires for pre-existing DKIM/MX/TXT records
- Stderr shows: "Auto-importing pre-existing DKIM/MX/TXT Route53 records..."
- 3x DKIM CNAME imports: `aws_route53_record.dkim[0]`, `[1]`, `[2]`
- Optional: MX and TXT imports if those records exist from km install
- NO manual `terragrunt import` required
- Apply ends successfully

**HALT if:** `InvalidChangeBatch: already exists` error appears — that means the Wave 1
plan 03 fix (autoImportFoundationSESRecords gate separation) did NOT fire.

Auto-import sanity check:
```bash
grep -E '(importing aws_route53_record|already in state|idempotent|Auto-importing)' \
  /tmp/tg-bootstrap-ses.log
```

**Log excerpt:** <paste auto-import lines from /tmp/tg-bootstrap-ses.log>
**Auto-import fired:** YES / NO
**DKIM imports seen:** <count: 0-3>
**MX import:** YES / NO / N/A
**TXT import:** YES / NO / N/A
**InvalidChangeBatch errors:** YES (HALT) / NO (PASS)
**Verdict:** PASS / FAIL

---

## Step 5: km init --plan (destroy-class gate)

```bash
cd /Users/khundeck/working/klanker-maker-tg
./bin/km init --plan 2>&1 | tee /tmp/tg-init-plan.log
```

Expected:
- All 17 regional modules pass plan
- ZERO protected resources in destroy-class trip list
- If gate trips on SES identity / Route53 record / S3 bucket / DynamoDB table / KMS key:
  HALT — that is the v1.0.0→v2.0.0 cutover hazard signal

**Modules planned:** <count>
**Destroy-class gate tripped:** YES (HALT) / NO (PASS)
**Protected resources in trip list:** <list or "none">
**Verdict:** PASS / FAIL

---

## Step 6: km init --dry-run=false (Wave 1 plan 02 live verification)

This is the **ssm-session-doc/v2.0.0 live verification** — tg-Sandbox-Session must
be created as a NEW document, NOT by colliding with km-Sandbox-Session.

```bash
cd /Users/khundeck/working/klanker-maker-tg
./bin/km init --dry-run=false 2>&1 | tee /tmp/tg-init-apply.log
```

Expected:
- All 17 regional modules green
- ssm-session-doc module creates `tg-Sandbox-Session` (not KM-Sandbox-Session)
- NO manual `terragrunt import 'aws_ssm_document.km_sandbox_session' 'KM-Sandbox-Session'` needed
  (that was the rg UAT-1 failure that triggered this phase)
- Total runtime: 10-30 min

Verify tg SSM document:
```bash
aws ssm describe-document --name tg-Sandbox-Session \
  --query 'Document.Status' --output text
# Expected: Active
```

Verify km SSM document still untouched:
```bash
aws ssm describe-document --name km-Sandbox-Session \
  --query 'Document.Status' --output text
# Expected: Active (unchanged)
```

**All 17 modules green:** YES / NO
**tg-Sandbox-Session created:** YES / NO
**km-Sandbox-Session still Active:** YES / NO
**DocumentAlreadyExists error seen:** YES (HALT) / NO (PASS)
**Verdict:** PASS / FAIL

---

## Step 7: km create + sandbox healthy (Wave 1 plan 01 live verification)

This is the **SCP cross-install trust live verification** — tg-create-handler must
be trusted by the canonical km-sandbox-containment SCP via the *-* pattern.

```bash
cd /Users/khundeck/working/klanker-maker-tg
./bin/km create profiles/learn.yaml 2>&1 | tee /tmp/tg-create.log
```

Capture sandbox-id:
```bash
TG_SANDBOX=$(grep -oE 'sandbox[a-z0-9]+' /tmp/tg-create.log | head -1)
echo "tg sandbox: $TG_SANDBOX"
```

Wait for healthy state, then verify runtime:
```bash
./bin/km otel $TG_SANDBOX --prompts | head -50
./bin/km shell $TG_SANDBOX
# Inside sandbox:
# - verify initCommands ran
# - verify /etc/profile.d/ files present
# - exit
```

The `km shell` call should use `tg-Sandbox-Session` SSM document
(Wave 1 plan 02 fix — `cfg.GetSandboxSessionDocumentName()` returns prefix-aware name).

**HALT if:** "explicit deny in a service control policy" errors in CloudTrail or create log —
that means the Wave 1 plan 01 fix (SCP *-* pattern) + Wave 3 plan 05 apply did NOT work.

**Sandbox ID:** <fill in>
**Sandbox reached healthy state:** YES / NO
**SCP-deny errors in create log:** YES (HALT) / NO (PASS)
**km shell used tg-Sandbox-Session:** YES / NO
**otel shows traffic:** YES / NO
**Verdict:** PASS / FAIL

---

## Step 8: km destroy (sandbox teardown)

```bash
cd /Users/khundeck/working/klanker-maker-tg
./bin/km destroy $TG_SANDBOX --remote --yes 2>&1 | tee /tmp/tg-destroy.log
```

**Log excerpt:** <paste key lines>
**Clean teardown:** YES / NO
**Verdict:** PASS / FAIL

---

## Steps 3-8 verdict

| Step | Description | Result | Notes |
|------|-------------|--------|-------|
| 3 | km bootstrap (plain) | PASS / FAIL | region.hcl auto-written: YES/NO |
| 4 | km bootstrap --shared-ses | PASS / FAIL | auto-imports fired: YES/NO; no manual import |
| 5 | km init --plan | PASS / FAIL | destroy-class gate clean |
| 6 | km init apply | PASS / FAIL | 17 modules + tg-Sandbox-Session created |
| 7 | km create + sandbox healthy | PASS / FAIL | no SCP-deny errors; otel works |
| 8 | km destroy --remote --yes | PASS / FAIL | clean |

**Steps 3-8 overall:** PASS / FAIL (fill in; must be all PASS to continue)

---

## Step 9: km uninit (tg regional teardown)

```bash
cd /Users/khundeck/working/klanker-maker-tg
./bin/km uninit --dry-run=true 2>&1 | tee /tmp/tg-uninit-dryrun.log
# Inspect: ONLY tg-* resources scheduled for destroy

./bin/km uninit --dry-run=false 2>&1 | tee /tmp/tg-uninit-apply.log
```

Expected:
- All 17 regional modules destroyed cleanly
- `tg-Sandbox-Session` SSM document destroyed (its own document — correct)
- `km-Sandbox-Session` NOT touched (different document — per-install isolation)

Verify after uninit:
```bash
aws ssm describe-document --name tg-Sandbox-Session 2>&1 | grep -q 'InvalidDocument' \
  && echo "OK: tg-Sandbox-Session destroyed"
aws ssm describe-document --name km-Sandbox-Session \
  --query 'Document.Status' --output text
# Expected: Active (MUST NOT be affected by tg teardown)
```

**tg-Sandbox-Session destroyed:** YES / NO
**km-Sandbox-Session still Active:** YES / NO (CRITICAL assertion)
**Verdict:** PASS / FAIL

---

## Step 10: km unbootstrap (tg foundation teardown — Wave 2 plan 04 live verification)

This is the **DDB lock table cleanup live verification** — `tf-tg-locks-use1` must
be deleted by km unbootstrap (was orphaned in the rg UAT-1).

```bash
cd /Users/khundeck/working/klanker-maker-tg
./bin/km unbootstrap --dry-run=true 2>&1 | tee /tmp/tg-unbootstrap-dryrun.log
# Inspect: scope includes DDB lock table tf-tg-locks-use1

./bin/km unbootstrap --dry-run=false 2>&1 | tee /tmp/tg-unbootstrap-apply.log
```

Expected:
- tg state bucket deleted
- tg KMS alias deleted
- tg artifacts bucket deleted
- **DDB lock table `tf-tg-locks-use1` deleted** (Wave 2 plan 04 fix)

SCP cleanup (km unbootstrap may not auto-detach SCP — verify and do manually if needed):
```bash
# If SCP still exists after unbootstrap:
TG_POLICY_ID=$(aws --profile klanker-terraform organizations list-policies \
  --filter SERVICE_CONTROL_POLICY \
  --query 'Policies[?Name==`tg-sandbox-containment`].Id' --output text)

aws --profile klanker-terraform organizations detach-policy \
  --policy-id $TG_POLICY_ID --target-id <application-account-id>
aws --profile klanker-terraform organizations delete-policy \
  --policy-id $TG_POLICY_ID
```

Verify DDB lock table gone:
```bash
aws dynamodb describe-table --table-name tf-tg-locks-use1 2>&1 \
  | grep -q 'ResourceNotFoundException' \
  && echo "OK: DDB lock table deleted"
```

**tg state bucket deleted:** YES / NO
**DDB lock table tf-tg-locks-use1 deleted:** YES / NO (Wave 2 plan 04 verification)
**tg SCP cleaned up:** YES / NO (manually if needed)
**Verdict:** PASS / FAIL

---

## Step 11: Post-teardown canonical km install smoke (LOAD-BEARING closure criterion i)

This is the **most important assertion** — proves per-install isolation actually works.
After the rg UAT-1, the canonical km-Sandbox-Session was destroyed by rg uninit.
The 84.4.1 fix (ssm-session-doc/v2.0.0 per-install naming) must prevent this.

```bash
cd /Users/khundeck/working/klankrmkr  # canonical km install

# 11a — SSM document isolation (the thesis)
aws ssm describe-document --name km-Sandbox-Session \
  --query 'Document.Status' --output text
# MUST be: Active

# 11b — km shell works on canonical install (no InvalidDocument)
./bin/km shell <one-of-the-pre-uat-km-sandbox-ids> -c 'echo SMOKE_OK; exit' \
  2>&1 | tee /tmp/km-shell-post-tg.log
grep -q SMOKE_OK /tmp/km-shell-post-tg.log \
  && echo "OK: km shell on canonical install still works after tg teardown"

# 11c — km list shows all pre-UAT sandboxes (no losses)
./bin/km list 2>&1 | tee /tmp/km-list-post-tg.log

# 11d — Optional: create + destroy a new km sandbox to prove SCP still trusts km-* roles
./bin/km create profiles/learn.yaml 2>&1 | tee /tmp/km-create-post-tg.log
NEW_KM_SANDBOX=$(grep -oE 'sandbox[a-z0-9]+' /tmp/km-create-post-tg.log | head -1)
./bin/km destroy $NEW_KM_SANDBOX --remote --yes 2>&1 | tee /tmp/km-destroy-post-tg.log
```

**HALT if:**
- km-Sandbox-Session is NOT Active (tg teardown destroyed it — the exact failure that motivated Phase 84.4.1)
- km shell fails with InvalidDocument
- km list shows missing sandboxes

**km-Sandbox-Session still Active after tg uninit:** YES / NO (CRITICAL)
**km shell on canonical install works:** YES / NO
**km list shows all pre-UAT sandboxes:** YES / NO
**km create + destroy on canonical install:** PASS / FAIL / SKIPPED
**Verdict:** PASS / FAIL

---

## Step 12: AFTER inventory snapshot + load-bearing diff

```bash
cd /Users/khundeck/working/klankrmkr
scripts/inventory-diff.sh snapshot \
  .planning/phases/84.4.1-multi-install-identity-permission-gap-closure/tg-uat-after.json \
  --prefix km

# Load-bearing assertion: km install resources unchanged
scripts/inventory-diff.sh diff \
  .planning/phases/84.4.1-multi-install-identity-permission-gap-closure/tg-uat-before.json \
  .planning/phases/84.4.1-multi-install-identity-permission-gap-closure/tg-uat-after.json \
  --prefix km > /tmp/tg-uat-diff-km.txt

# Sanity: tg resources all gone
scripts/inventory-diff.sh diff \
  .planning/phases/84.4.1-multi-install-identity-permission-gap-closure/tg-uat-before.json \
  .planning/phases/84.4.1-multi-install-identity-permission-gap-closure/tg-uat-after.json \
  --prefix tg > /tmp/tg-uat-diff-tg.txt

cat /tmp/tg-uat-diff-km.txt
cat /tmp/tg-uat-diff-tg.txt
```

Expected:
- `/tmp/tg-uat-diff-km.txt` is EMPTY (km install byte-identical — closure criterion i)
- `/tmp/tg-uat-diff-tg.txt` is all-removals (tg install fully torn down)

**km diff (must be empty):**
```
<paste /tmp/tg-uat-diff-km.txt>
```

**tg diff (must be all-removals):**
```
<paste /tmp/tg-uat-diff-tg.txt | head -40>
```

**km install byte-identical:** YES / NO (closure criterion i)
**tg install fully removed:** YES / NO
**Verdict:** PASS / FAIL

---

## Steps 9-11 verdict

| Step | Description | Result | Notes |
|------|-------------|--------|-------|
| 9 | km uninit on tg | PASS / FAIL | 17 modules destroyed, tg-Sandbox-Session destroyed (km untouched) |
| 10 | km unbootstrap on tg | PASS / FAIL | DDB lock table tf-tg-locks-use1 deleted |
| 11a | canonical km-Sandbox-Session still Active | PASS / FAIL | per-install isolation proven |
| 11b | km shell on canonical install works | PASS / FAIL | no InvalidDocument |
| 11c | km list shows all pre-UAT sandboxes | PASS / FAIL | no losses |
| 11d | km create + destroy on canonical install | PASS / FAIL / SKIPPED | SCP still trusts km-* roles |

---

## Phase 84.4.1 Closure Criteria Verification

| Criterion | Status | Evidence |
|-----------|--------|----------|
| (a) ssm-session-doc/v2.0.0 with per-install rename | PASS / FAIL | Wave 1 plan 02 + Wave 3 plan 05 + tg-Sandbox-Session created in Step 6 |
| (b) SCP cross-install design + canonical apply clean | PASS / FAIL | Wave 1 plan 01 + Wave 3 plan 05 + tg sandbox created without SCP edit (Step 7) |
| (c) SES auto-import on shared-domain second install | PASS / FAIL | tg-bootstrap-ses.log shows auto-imports fired (Step 4) |
| (d) make build-lambdas builds 6 zips | PASS / FAIL | Wave 2 plan 04 + verified in Step 1 |
| (e) km bootstrap writes region.hcl as prereq | PASS / FAIL | Wave 2 plan 04 + Step 3 (fresh clone, no manual region.hcl) |
| (f) km configure state_bucket UX | PASS / FAIL | Wave 2 plan 04 + Step 2 (default displayed) |
| (g) km unbootstrap deletes DDB lock table | PASS / FAIL | Step 10 verification |
| (h) downloadTerraform invalidates stale cache | PASS (unit test) | Wave 2 plan 04 unit test |
| (i) tg UAT-2 ends green + km shell on canonical still works | PASS / FAIL | Step 11 + Step 12 verification |
| (j) OPERATOR-GUIDE.md gaps+workarounds prose removed | PASS / FAIL | Task 4 (OPERATOR-GUIDE.md update — committed separately) |

**Completed:** <fill in>
**Overall result:** PASS / FAIL

---

## OPERATOR-GUIDE.md update

- Replaced Phase 84.4 multi-install gaps+workarounds prose (lines ~1006-1166)
- New Phase 84.4.1 multi-install runbook documents the working happy path
- Security trade-off + 5-SCP limit + 1-2s SSM window documented
- Generic placeholders used (example.com, Corporate)

**UAT-2 final verdict:** <fill in after all steps complete>
