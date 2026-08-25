# Phase 126 — session handoff (2026-08-24)

## ✅ NOTHING IS RUNNING (verified 2026-08-24)
Account 481723467561 is clean: zero instances, zero volumes, no stale terraform
locks. sb-be0fdcb8 was destroyed; its orphan 300GB volume and both stale lock
items were removed by hand.

Its failure was **"Error acquiring the state lock"** — a leftover lock from an
earlier aborted run, NOT a cross-account defect. The locks are cleared, so a
retry proceeds straight through.

> **CORRECTED 2026-08-24 — the lock is a symptom, not the cause.** Reproduced
> exactly with `sb-ceffcb7c`. The create-handler Lambda picks the sticky AZ
> (`us-east-1a`), that AZ is capacity-dry for `g6e.12xlarge`, and **terraform
> retries `RunInstances` there internally** instead of returning the ICE error to
> km. The Phase 124 AZ sweep therefore never rotates, the Lambda burns all 900s on
> one dry AZ and is killed mid-apply, and the lock it leaves behind is what the
> next two EventBridge retries report. Log signature is unmistakable:
> `1 Status: timeout`, `3 START RequestId`, `4 Error acquiring the state lock`,
> and no instance ever created.
>
> **Clearing the lock alone does not help** — the retry lands on the same dry AZ
> and times out again. Demote the AZ first (write the ICE row `RecordICE` would
> have written; `km capacity` should then show it `recently-dry`), *then* clear
> the lock and re-create. Full analysis: `126-UAT.md` Finding L2.
>
> Diagnose with CloudTrail in the **target** account, but parse
> `Events[].CloudTrailEvent` — the `--query 'Events[].[EventTime,Username,ErrorCode]'`
> projection prints `ErrorCode: None` for these events because `ErrorCode` is not a
> top-level `lookup-events` field, which reads as "RunInstances is succeeding" when
> it is failing every time.

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

**Two corrections to that, learned the hard way 2026-08-24:**

1. **Wait for the EventBridge retries to drain before clearing the lock.** A
   timed-out create is retried twice (~+53s and ~+160s after the 900s ceiling).
   Clearing the lock while a retry is still pending just lets that retry
   re-acquire it, and you get to watch the same failure again.
2. **Clearing the lock alone fixes nothing** if the cause was a dry AZ — the
   retry lands on the same AZ and times out again. Demote the AZ *first* (see
   Finding L3 above), *then* clear the lock. The lock is a symptom.

Filtering the lock table: rows whose `LockID` ends in `-md5` are digest records,
not locks. Only the bare `...terraform.tfstate` rows are real locks — a scan that
does not filter them out shows ~40 "locks" that are not locks. As of this writing
four genuinely stale locks exist for long-dead sandboxes (`sb-f1f2a37e`,
`sb-daf475f0`, `sb-e7796e54`, `sb-8d5820ce`); they are harmless (those ids are
gone) but they are NOT "no stale locks", contrary to the header of this file.

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
**vLLM has never actually served a token.** That is the only unproven claim in the phase.

> **UPDATED 2026-08-24 — the cause is no longer "torn down too early."** A full
> session was spent on this and the box never launched at all. Three creates
> (`sb-ceffcb7c`, `sb-74999eaf`, `sb-17753fa7`) each burned the create-handler's
> full 900s ceiling without ever creating an instance.
>
> **Two code defects and one external wall, all documented in `126-UAT.md`:**
>
> - **Finding L3 (ROOT CAUSE, Phase 124 not 126).** `rankScore`
>   (`pkg/capacity/rankaz.go:129`) checks `LastSuccessAt` *before* the fresh-ICE
>   branch and returns 2 unconditionally. Success rows carry no TTL, so **once an
>   AZ has ever succeeded it outranks every other AZ forever, no matter how often
>   it later ICEs.** The Phase 124.07 feedback loop is inert in exactly the case it
>   was built for. Confirmed experimentally: removing the stale `last_success_at`
>   was the *only* change between two creates, and the AZ moved `1a -> 1b`.
>   **`km capacity` will not show you this** — its verdict column said
>   `recently-dry` for the very AZ the ranker kept choosing. The report and the
>   ranker are separate implementations that disagree.
> - **Finding L2 (Phase 124 not 126).** Terraform retries ICE *internally* in the
>   same AZ, so km never receives the error, `sweepDecision` is never reached, and
>   the sweep cannot rotate — `maxAttempts` was 4 and it still never advanced past
>   attempt 1. The Lambda dies at 900s and the lock it leaves is what the next run
>   reports.
> - **Finding L4 (external, temporary).** Every allowlisted GPU shape was probed
>   for real (10 probes, each tagged and terminated immediately):
>   `g6e.4xlarge`, `g6e.12xlarge`, `g6e.48xlarge`, `g6.12xlarge` — **all
>   `InsufficientInstanceCapacity`, in every us-east-1 AZ.** The FP8 single-GPU
>   escape hatch (`gpu-qwen38-oblit-fp8-4x.yaml`, weights already staged at
>   `models/qwen38-oblit-27b-fp8/`, 29.9 GB) is dry too. There was no way to launch
>   any GPU box in this account/region at that time.
>
> **Before the next attempt:** re-probe capacity first (a real `run-instances`,
> terminated at once — there is no read-only availability API and `km capacity`'s
> "likely" is the *absence of evidence*, not evidence). If something is available,
> check the `km-capacity` row for that `{account}#{type}` and make sure the AZ you
> want is not being out-ranked by a stale `last_success_at`. Budget one full 900s
> timeout per dry AZ until L2 is fixed.

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
