# Phase 84.4.1 Canonical km Install Transition Log

**Started:** (operator fills in — start of transition window)
**Operator:** <fill in>
**AWS account (application):** <fill in>
**AWS account (management):** <fill in>
**Git ref:** b431671522379cdb0abe2b24221a4e0448490545
**Branch:** main

## Pre-flight notes

- inventory-diff.sh extended (2026-05-18) to capture SSM documents via
  `aws ssm list-documents --filters Key=Owner,Values=Self`. The new `ssm.documents`
  key in the snapshot JSON will show the KM-Sandbox-Session -> km-Sandbox-Session
  rename in the inventory diff output.
- Operator must have AWS_PROFILE set to the application account profile and a
  separate management-account profile for the SCP policy body captures.
- Run `eval $(./bin/km env)` before any direct terragrunt invocations.

## BEFORE snapshot

- inventory: km-before-84.4.1.json  (operator captures: scripts/inventory-diff.sh snapshot ...)
- SCP policy body: km-before-84.4.1-scp-body.json  (operator captures: aws organizations describe-policy ...)
- SSM documents: embedded in km-before-84.4.1.json under .ssm.documents

### Key resources baseline

- SCP policy ID: <fill from list-policies>
- SCP policy name: km-sandbox-containment
- SSM document: KM-Sandbox-Session (will become km-Sandbox-Session after Step 0b)
- km sandboxes currently running: <run `./bin/km list` and capture count + IDs>

## Planned transitions

1. infra/live/management/scp/ — apply scp/v2.0.0 *-* pattern fix
   - Expected: ~5-10 in-place updates on aws_organizations_policy.sandbox_containment
   - Expected destroys: 0
   - Expected window: sub-10 seconds
   - Wave 1 plan 84.4.1-01

2. infra/live/use1/ssm-session-doc/ — apply ssm-session-doc/v2.0.0 rename
   - Expected: 1 destroy + 1 create (KM-Sandbox-Session -> km-Sandbox-Session)
   - Expected window: 1-2 seconds (new sessions during this window will retry)
   - Wave 1 plan 84.4.1-02

## Notification to active sessions

Operator: terminate any active `km shell` sessions before running Step 0b.

Check for active sessions:
```bash
aws ssm describe-sessions --state Active \
  --query 'Sessions[?DocumentName==`KM-Sandbox-Session`].SessionId' --output text
```

---

## Step 0 — BEFORE snapshot capture

**Operator commands:**

```bash
cd /Users/khundeck/working/klankrmkr
eval $(./bin/km env)

# Capture BEFORE inventory snapshot (now includes ssm.documents)
AWS_PROFILE=<application-profile> scripts/inventory-diff.sh snapshot \
  .planning/phases/84.4.1-multi-install-identity-permission-gap-closure/km-before-84.4.1.json \
  --prefix km

# Capture SCP policy body baseline (management account)
POLICY_ID=$(aws --profile <management-profile> organizations list-policies \
  --filter SERVICE_CONTROL_POLICY \
  --query 'Policies[?Name==`km-sandbox-containment`].Id' \
  --output text)
aws --profile <management-profile> organizations describe-policy \
  --policy-id $POLICY_ID \
  --query 'Policy.Content' \
  --output text > .planning/phases/84.4.1-multi-install-identity-permission-gap-closure/km-before-84.4.1-scp-body.json

# Capture running sandbox count
./bin/km list
```

**Captured at:** <fill in timestamp>
**Snapshot size:** <fill in>

---

## Step 0a — scp/v2.0.0 apply

**Started:** <fill in>

### terragrunt plan output (key fragment)

```
<paste Plan: N to add, N to change, N to destroy line here>
```

### Expected assertions
- Plan: 0 to add, 1 to change, 0 to destroy
- Resource: aws_organizations_policy.sandbox_containment
- Condition changes: ~5-10 lines in Statement[].Condition.ArnNotLike arrays
  (km-create-handler -> *-create-handler etc.)
- Zero other resource changes

### terragrunt apply outcome

```
<paste tail of apply output here>
```

### Policy body verification

```bash
POLICY_ID=$(aws --profile <management-profile> organizations list-policies \
  --filter SERVICE_CONTROL_POLICY \
  --query 'Policies[?Name==`km-sandbox-containment`].Id' --output text)
aws --profile <management-profile> organizations describe-policy \
  --policy-id $POLICY_ID --query 'Policy.Content' --output text \
  | grep -q '\*-create-handler' && echo "OK: *-create-handler present in deployed policy"
aws --profile <management-profile> organizations describe-policy \
  --policy-id $POLICY_ID --query 'Policy.Content' --output text \
  | grep -q 'km-create-handler' && echo "ERROR: km-create-handler still present" || echo "OK: km-create-handler removed"
```

- *-create-handler present: <YES / NO>
- km-create-handler absent: <YES / NO>

**Completed:** <fill in>
**Result:** <PASS / FAIL>

---

## Step 0b — ssm-session-doc/v2.0.0 apply

**Started:** <fill in>
**Active sessions terminated/waited:** <fill in count>

### Active session check
```bash
aws ssm describe-sessions --state Active \
  --query 'Sessions[?DocumentName==`KM-Sandbox-Session`].SessionId' --output text
```
Result: <paste output — "None" is the desired state>

### terragrunt plan output (key fragment)

```
<paste Plan: N to add, N to change, N to destroy line here>
```

### Expected assertions
- Plan: 1 to add, 0 to change, 1 to destroy
- Destroy: aws_ssm_document.km_sandbox_session (KM-Sandbox-Session)
- Create: aws_ssm_document.sandbox_session (km-Sandbox-Session)
- No other resources touched

### terragrunt apply outcome

```
<paste tail of apply output here>
```

### Post-apply document verification

```bash
aws ssm describe-document --name km-Sandbox-Session \
  --query 'Document.{Name:Name,Status:Status,CreatedDate:CreatedDate}'
# Expected: Name=km-Sandbox-Session, Status=Active

aws ssm describe-document --name KM-Sandbox-Session 2>&1 \
  | grep -q 'InvalidDocument\|does not exist' && echo "OK: KM-Sandbox-Session removed"
```

- km-Sandbox-Session Active: <YES / NO>
- KM-Sandbox-Session removed: <YES / NO>

**Completed:** <fill in>
**Result:** <PASS / FAIL>

---

## Step 0c — AFTER snapshot + diff

**Captured at:** <fill in>

### Operator commands

```bash
cd /Users/khundeck/working/klankrmkr

# AFTER inventory snapshot
AWS_PROFILE=<application-profile> scripts/inventory-diff.sh snapshot \
  .planning/phases/84.4.1-multi-install-identity-permission-gap-closure/km-after-84.4.1.json \
  --prefix km

# AFTER SCP policy body
POLICY_ID=$(aws --profile <management-profile> organizations list-policies \
  --filter SERVICE_CONTROL_POLICY \
  --query 'Policies[?Name==`km-sandbox-containment`].Id' --output text)
aws --profile <management-profile> organizations describe-policy \
  --policy-id $POLICY_ID --query 'Policy.Content' --output text \
  > .planning/phases/84.4.1-multi-install-identity-permission-gap-closure/km-after-84.4.1-scp-body.json

# Run inventory diff (expected: SSM rename only in inventory; SCP body changes separate)
AWS_PROFILE=<application-profile> scripts/inventory-diff.sh diff \
  .planning/phases/84.4.1-multi-install-identity-permission-gap-closure/km-before-84.4.1.json \
  .planning/phases/84.4.1-multi-install-identity-permission-gap-closure/km-after-84.4.1.json \
  --prefix km

# SCP body diff
diff \
  .planning/phases/84.4.1-multi-install-identity-permission-gap-closure/km-before-84.4.1-scp-body.json \
  .planning/phases/84.4.1-multi-install-identity-permission-gap-closure/km-after-84.4.1-scp-body.json
```

### Inventory diff output

```
<paste inventory-diff.sh diff output here>
```

Expected: SSM rename visible under ssm.documents; everything else byte-identical.

### SCP body diff output

```
<paste diff output here (expected ~5-10 lines changing in ArnNotLike arrays)>
```

---

## Step 0d — km shell smoke test

```bash
cd /Users/khundeck/working/klankrmkr
make build   # ensure binary is post-84.4.1

./bin/km list 2>&1
# Expected: lists existing km sandboxes, no errors

SANDBOX_ID=$(./bin/km list --wide | tail -n +2 | head -1 | awk '{print $1}')
if [ -n "$SANDBOX_ID" ] && [ "$SANDBOX_ID" != "ID" ]; then
    timeout 30 ./bin/km shell $SANDBOX_ID -c 'echo SMOKE_TEST_OK; exit' 2>&1
fi
```

- km list works: <YES / NO>
- km shell smoke test: <PASS / SKIP (no sandboxes) / FAIL>

---

## Verdict

| Assertion | Result |
|-----------|--------|
| scp/v2.0.0 applied with ~5-10 in-place updates, 0 destroys | <PASS / FAIL> |
| ssm-session-doc/v2.0.0 applied with 1 destroy + 1 create | <PASS / FAIL> |
| Inventory diff scoped to expected deltas only (SSM rename) | <PASS / FAIL> |
| SCP body diff shows *-* pattern substitution only | <PASS / FAIL> |
| km list still works | <PASS / FAIL> |
| km shell smoke test | <PASS / SKIP / FAIL> |

**Completed:** <fill in>
**Overall result:** <PASS / FAIL>
