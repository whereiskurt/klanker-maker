# Doctor: tag-based platform-resource discrimination — design note

**Status:** Proposal — DO NOT implement without operator sign-off.
**Author:** doctor-cleanup-hardening pass (`/Users/khundeck/Downloads/doctor-cleanup-hardening.md` Task 4).
**Date:** 2026-05-09.

## Problem

`km doctor`'s stale-resource checks rely on a hand-maintained `platformPrefixes` denylist (`internal/app/cmd/doctor.go:1217`) plus a per-sandbox component allowlist in `doctor_lambdas.go`. Every new platform resource created by a future Terraform-module PR is one renamed-prefix away from being misclassified as a sandbox orphan and deleted.

This is exactly how today's `kph-slack-bridge-role` outage happened: the role had no allowlist entry, was classified stale, and got deleted on the next `--dry-run=false` doctor run. We added it after the incident; the next gap is waiting for the next outage.

The denylist is also incomplete by construction: the s3-replication module hardcodes `km-s3-replication-{bucket}` regardless of `var.resource_prefix`, so multi-instance installs need a literal `"km-"` exception alongside the rolePrefix-aware ones (see fix(doctor): expand platform-role allowlist for stale IAM check, commit `1f441c9`).

## Proposed approach

Replace prefix-based discrimination with tag-based discrimination at the Terraform-module level.

1. **Tag every platform resource** at creation time with `km:role-class = platform`. Update each module under `infra/modules/`:
    - `lambda-slack-bridge`, `email-handler`, `ecs-spot-handler`, `ttl-handler`, `s3-replication`, `email-create-handler`, `create-handler` IAM roles, KMS keys, SSM parameters, SQS queues, DDB tables.
2. **Tag every per-sandbox resource** (already tagged with `km:sandbox-id` in most modules) additionally with `km:role-class = sandbox`.
3. **Replace `platformPrefixes`** (and the per-sandbox component allowlist in `doctor_lambdas.go`) with a tag check:
    - `km:role-class = platform` → unconditionally skipped, no further checks.
    - `km:role-class = sandbox` → matched against the active sandbox set; orphan if absent.
    - Resource lacking either tag → `CheckWarn` with remediation "retag manually or run `km init` to backfill". **Never deleted.**
4. **Backfill migration**: a one-shot `km doctor --backfill-tags` command tags any existing resource whose name matches a known platform prefix (using the current `platformPrefixes`/component-prefix list as the seed). Operators run it once after upgrading.

## Why this is safer

- New platform Lambdas/roles ship safely as long as the module sets the tag. No `doctor.go` edit required — the failure mode degrades to "I see this and don't recognise it" rather than "I delete this on a `--dry-run=false` sweep".
- The hardcoded `km-s3-replication-` exception goes away — the s3-replication module would just set `km:role-class = platform` and the prefix becomes irrelevant.
- Operator-created debug resources with `km-`-shaped names (`km-debug-thing`, `km-test-foo`) get a `WARN` instead of silent deletion.

## Open questions

1. **IAM roles** — `Tags` is a top-level attribute on `aws_iam_role`. Trivial to add. The tag is a separate IAM API call (`ListRoleTags`) per role during enumeration; for ~50-role installs the latency cost is bounded.
2. **KMS aliases** — aliases have NO tags. Workaround: tag the underlying KMS *key* (which DOES support tags), and have the doctor check `aws kms list-resource-tags --key-id $(aws kms describe-key --key-id alias/foo --query 'KeyMetadata.KeyId')`. One extra round-trip per alias. Acceptable.
3. **EventBridge schedules** — `Tags` is supported by `aws_scheduler_schedule`. Easy.
4. **SSM parameters** — `Tags` is supported via `aws ssm add-tags-to-resource`. Existing parameters won't have it, so the backfill migration is mandatory before this rollout. Without backfill, all existing platform SSM params would WARN on the first post-upgrade doctor run.
5. **SQS queues** — `sqs.TagQueue`. Easy.
6. **DDB tables** — `aws_dynamodb_table.tags`. Easy.
7. **Lambda functions** — `aws_lambda_function.tags`. Easy.

## Migration playbook (if approved)

1. PR 1: add `km:role-class = platform` to every Terraform module under `infra/modules/`. No code change in `doctor.go` yet. Land + deploy via `km init --sidecars && km init`.
2. PR 2: implement `km doctor --backfill-tags`. Operators run it once per install; tags are persisted into the live state.
3. PR 3: switch `doctor.go` checks from `platformPrefixes` to tag lookups. Keep the denylist as a fallback for resources lacking either tag (ship with a deprecation warning). Delete the denylist after one full release cycle.

## Out of scope for this design note

- ECR repositories (no per-sandbox use today).
- Route53 records (managed centrally; not enumerable per-resource).
- IAM policies (always paired with a role; the role's tag covers the policy).

## Decision needed

Operator approval to start implementation, then create a phase via `/gsd:add-phase`. As written this is **3 PRs and a one-shot backfill**, with a single soak window (PR 1 ships → wait for next provision cycle so all new resources are tagged → PR 2 → PR 3).
