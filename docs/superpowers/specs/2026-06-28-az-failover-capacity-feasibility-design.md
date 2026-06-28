# Platform-wide AZ failover + capacity feasibility — design

**Date:** 2026-06-28
**Status:** Approved (brainstorming), pending GSD planning as Phase 124
**Author:** brainstormed with operator

## Problem

EC2 launches are effectively pinned to a single Availability Zone. The
`ec2spot` module assigns `availability_zone = local.effective_azs[instance_idx % len]`
(`infra/modules/ec2spot/v1.2.0/main.tf:124-125`), so a single instance always
lands in `AZ[0]` — `us-east-1a` in API order. GPU capacity (g6/g6e, L40S/L4) is
currently only available in `us-east-1c`, so every GPU launch tries the
capacity-dry `1a` and fails.

Two failure shapes compound the pain:

1. **The forever-loop.** Spot requests use `wait_for_fulfillment = true`
   (`ec2spot/v1.2.0/main.tf:604`) with no timeout, so a capacity-dry AZ leaves
   Terraform polling indefinitely with zero fallback.
2. **No failover, no classification.** There is no AZ iteration anywhere, and
   the only capacity awareness is a substring match in the cold-create Lambda
   (`cmd/create-handler/main.go:341`) that marks the sandbox `nocap` but never
   retries. Critically, a *regional* GPU-vCPU quota wall (`L-DB2E81BA`, default
   **0** per account) looks superficially similar to "no capacity" but iterating
   AZs can never fix it.

A pending todo (`.planning/todos/pending/spot-multi-az.md`, 2026-03-26) already
records this for spot generally; GPU scarcity made it acute.

## Scope

**Platform-wide.** Fix AZ iteration + fail-fast classification for ALL instance
launches — spot and on-demand, GPU and non-GPU. Retires the `spot-multi-az`
todo. Both the operator `km create` path and the cold-create Lambda path are
covered by a single mechanism (the Lambda runs `km create` as a subprocess).

## Non-goals

- **No live on-demand capacity prediction.** AWS has no API that reports
  on-demand capacity; `DryRun` RunInstances does NOT reveal it. The capacity
  command reports *feasibility signals* only, never a false "available."
- **No multi-region failover.** Region stays fixed; only AZ iterates. (Operator
  explicitly does not want the 4 AZs to escalate into cycling regions.)
- **No Lambda auto-requeue** of `nocap` cold-creates (deferred; the opt-in
  operator backoff flag is the chosen pattern instead).
- **No spot-interruption handling** — orthogonal to capacity.
- **No EC2 Fleet / CreateFleet rewrite** — rejected because the EBS-volume AZ
  must be known at plan time, which fights fleet's allocate-at-apply model, and
  km tracks a single instance id.

## Mechanism — orchestrated AZ sweep inside `km create`

The AZ becomes an injectable override threaded
profile → `pkg/compiler/service_hcl.go` → `ec2spot` module, replacing the
hardcoded `effective_azs[idx % len]` selection for the single-instance case
(N>1 spread preserved — see Edge cases). `km create` wraps the existing
`compile → terragrunt apply` pipeline in a classify-and-retry loop:

```
order = rankAZs(instanceType, region)          # capacity-aware (§ Ranking)
for az in order:
    apply(AZ=az)                               # bounded waiter (§ Forever-loop)
    switch classify(result):
        success                -> recordSuccess(type, az); DONE
        ICE | waiter-timeout   -> recordICE(type, az); taint instance+volume; continue
        quota | auth | invalid -> FAIL-FAST   # iterating cannot help
exhausted -> FAIL-FAST summary  (unless --wait-for-capacity: outer backoff loop)
```

Because the Lambda cold-create simply runs `km create`, it inherits the sweep.
The Lambda's existing `nocap` substring classifier is refactored to call the
same shared taxonomy rather than duplicating string matches.

## Error taxonomy (makes "fail-fast" correct)

| Class | Errors | Action |
|---|---|---|
| **Iterate** | `InsufficientInstanceCapacity`, `SpotMaxPriceTooLow`, `MaxSpotInstanceCountExceeded`, bounded-waiter timeout | record + taint + next AZ |
| **Fail-fast** | `VcpuLimitExceeded` / `InstanceLimitExceeded` (regional quota, e.g. `L-DB2E81BA`=0), `AuthFailure` / `UnauthorizedOperation`, `InvalidParameterValue`, `Unsupported`/`UnsupportedOperation` | stop immediately, message names the quota/cause + remediation |

A quota wall must never masquerade as "no capacity." The fail-fast message for a
quota class names the specific quota code and the increase path.

## AZ ranking — capacity-aware with ICE memory

`rankAZs(instanceType, region)` orders by, in priority:

1. **Drop** AZs that do not *offer* the instance type
   (`DescribeInstanceTypeOfferings`, `LocationType=availability-zone`). This
   alone eliminates the wasted attempt on `1a` for GPU.
2. **Quota gate up front** — if regional quota headroom is 0 for the family,
   fail-fast before any apply.
3. **Last-success AZ** for this instance type first (sticky).
4. **Deprioritize** AZs with a fresh cached ICE failure.
5. **Operator override:** optional `spec.runtime.azPreference: [..]` merges
   ahead of the learned ranking.

## `km capacity` command + `{prefix}-capacity` table

`km capacity <profile.yaml>` or `km capacity --type g6e.12xlarge [--region]`
prints an honest per-AZ feasibility report:

| AZ | offered? | quota headroom | last ICE | last success | verdict |
|---|---|---|---|---|---|
| us-east-1a | no | — | — | — | not-offered |
| us-east-1c | yes | 96 vCPU | — | 2h ago | likely |
| ... | | | | | quota-blocked / recently-dry / unknown |

Verdicts: `likely | quota-blocked | not-offered | recently-dry | unknown`.
Never `available` (no API can promise it).

**Shared store:** new DynamoDB table `{prefix}-capacity`:
- key = `(instanceType, az)` (or `instanceType` hash + `az` range)
- attrs: `last_ice_at`, `last_success_at`
- TTL ~45min on ICE rows so a transient dry spell auto-clears
- read/written by both the operator binary and the cold-create Lambda
- feeds both `rankAZs` and the report

Live signals (offerings, quota) are API calls, not stored. Only the learned
ICE/last-success memory persists.

## Killing the forever-loop

Add bounded `timeouts { create = <N>m }` to the `aws_spot_instance_request` so a
capacity-dry spot AZ errors in minutes instead of hanging, letting the loop
advance. On-demand `aws_instance` already errors fast on ICE — it only needs the
classify-and-retry wrapper. The per-attempt waiter cap must keep a full 4-AZ
sweep comfortably under the Lambda's 900s budget.

## EBS / AZ coupling (must get right)

GPU `additionalVolume` (`/data` weights) is an `aws_ebs_volume` whose AZ must
equal the instance's. The AZ override drives **both** the instance and the
volume, keeping them co-located. Changing the override forces replacement of
both, so a plain re-apply per attempt is clean; an explicit taint of
instance+volume between attempts is the safety net against partial state.
Snapshot-backed `additionalSnapshots` are region-scoped and materialize fine in
any chosen AZ — no coupling concern there.

## Surface summary

- `spec.runtime.azPreference: []string` — optional, additive (no apiVersion bump).
- `km create --wait-for-capacity[=30m]` — opt-in outer backoff: re-sweep all AZs
  every ~5min until the deadline (unattended/scheduled provisioning). Default off
  = fail-fast.
- `km capacity …` — feasibility report.
- `km doctor` — surfaces the new `{prefix}-capacity` table and WARNs on a
  GPU-family quota of 0.

## Deploy surface

- New `regionalModules()` entry (the table) ⇒ **`make build` the operator binary
  BEFORE `km init` full apply** (the stale-binary-silently-skips-module gotcha),
  then full `km init --dry-run=false` to create the table.
- `make build-lambdas` for the refactored create-handler classifier.
- `make build` for the operator-side sweep loop + `km capacity`.
- No SandboxProfile apiVersion bump (additive `azPreference` only). Existing
  sandboxes unaffected — this is launch-time behavior.

## Edge cases / what else

- **Lambda 900s budget:** 4 AZs × bounded waiter must fit; cap per-attempt
  waiter (~2-3min). `--wait-for-capacity` is operator-path only.
- **`availability_zone_count: 4`** (`infra/live/use1/network/terragrunt.hcl:47`)
  must actually provision a subnet per AZ, or there is nothing to fail over to —
  verify all 4 subnets exist before relying on them.
- **`km clone --count N`** must preserve AZ spread across replicas (the existing
  `idx % len`), not collapse all replicas into one AZ. The override applies to
  the single-instance case; N>1 keeps spread (possibly biased toward
  capacity-likely AZs).
- **Cleanup between attempts:** rely on AZ-change forced replacement plus an
  explicit taint of instance + volume; never leave a stranded volume in a dead AZ.
```
