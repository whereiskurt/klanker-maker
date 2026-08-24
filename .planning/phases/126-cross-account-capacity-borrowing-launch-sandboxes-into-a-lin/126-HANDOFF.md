# Phase 126 — session handoff (2026-08-24)

## ✅ NOTHING IS RUNNING (verified 2026-08-24)
Account 481723467561 is clean: zero instances, zero volumes, no stale terraform
locks. sb-be0fdcb8 was destroyed; its orphan 300GB volume and both stale lock
items were removed by hand.

Its failure was **"Error acquiring the state lock"** — a leftover lock from an
earlier aborted run, NOT a cross-account defect. The locks are cleared, so a
retry proceeds straight through.

Always re-verify before assuming (km list can lag):
```bash
aws ec2 describe-instances --filters Name=tag:km:managed-by,Values=klankermaker \
  Name=instance-state-name,Values=pending,running,stopping,stopped \
  --region us-east-1 --profile sudo-management \
  --query 'Reservations[].Instances[].[InstanceId,State.Name]' --output text
aws ec2 describe-volumes --region us-east-1 --profile sudo-management \
  --filters Name=tag:km:managed-by,Values=klankermaker \
  --query 'Volumes[].[VolumeId,State]' --output text
```
If a create fails mid-apply, CHECK FOR AND CLEAR STALE LOCKS in
tf-km-locks-use1 (LockID contains the sandbox id) — otherwise the next create
fails with "Error acquiring the state lock".

## Status
Phase 126 is EXECUTED and PROVEN LIVE. Branch `work/next3`, **PR #59**.
Cross-account create AND teardown both verified: a g6e.12xlarge ran in
481723467561 from the home control plane (SSM Online, reused `km-gpu-box`
instance profile), and `km destroy` reaped it across the boundary.
`km capacity` reads **768 vCPU from the target vs 64 home**.

All 12 bugs found by live UAT are FIXED and pushed. Bug 3 (the last one) was
fixed and verified this session: the artifacts-bucket grant now SURVIVES
`km init`, so `km account register` is no longer needed after an apply.

## The ONE thing still open: REQ-126-UAT
**vLLM has never actually served a token.** Every previous box was torn down
before the 55GB weight sync finished. That is the only unproven claim in the phase.

To finish it:
```bash
./km create profiles/gpu-qwen38-oblit-12x.yaml --launch-account gpuman --alias xacct
# wait ~15-25 min (55GB S3 sync in-region, then 27B model across 4x L40S)
aws ssm start-session --target <instance-id> --profile sudo-management --region us-east-1
#   on box: systemctl status vllm ; journalctl -u vllm -f ; curl localhost:8000/v1/models
./km destroy xacct --remote --yes      # ALWAYS, when done
```
Weights are staged + pinned at revision af34629438e091685513fe2f66c0f2918de5734c
(upstream "V3": fixed chat_template.jinja + an eos_token_id bug that caused early
stopping). 28/28 shards in s3://km-artifacts-12345/models/qwen38-oblit-27b/.

## Gotchas that will bite a fresh session
- `km account add --force` is REQUIRED to re-apply an already-enrolled link, and
  for THIS link you must also pass
  `--state-bucket tf-km-linkstate-052251888500-use1` (the bucket-name fix changed
  the derived name; without it terraform sees empty state and recreates).
- The operator SSO principal cannot be derived — always pass
  `--trust-principal arn:aws:iam::052251888500:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_AdministratorAccess_024532ccbde75573`
- A linked box gets the 4-grant `km-gpu-box` role, NOT the 12-grant per-sandbox
  role. Budget metering, GitHub token, Slack/GitHub inbound, transcripts and SOPS
  secrets do NOT work there. Not fixable by widening IAM (SSM Parameter Store and
  DynamoDB have no resource policies). Documented in the runbook.
- Debug IAM denials with `aws sts decode-authorization-message` — AWS reports an
  UNSATISFIABLE CONDITION with the same wording as a MISSING STATEMENT.

## Not done
- `126-UAT.md` still has every "Actual" column blank — fill in after a live run.
- `REQUIREMENTS.md` REQ-126-UAT still "Pending live UAT".
- PR #59 not merged.
