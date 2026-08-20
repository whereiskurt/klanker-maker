---
phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway
verified: 2026-08-20T03:42:51Z
status: passed
resolved: 2026-08-20T03:50:00Z
score: 9/9 requirements verified (BLOCKER resolved in a54a2800)
behavior_unverified: 0
overrides_applied: 0
gaps:
  - truth: "km init refuses to disable NAT while a private sandbox is running (REQ-125-GUARD)"
    status: resolved
    resolution: "Fixed in a54a2800 — natDisableGuard now blocks unless status is definitively terminal (failed/killed/destroyed/terminated); empty status blocks. Regression test added for the empty-status case; the test that codified the fail-open half corrected; docs/private-subnet-nat.md updated. Full suite re-run: 41 ok, same 5 credential-seam failures, no new regressions."
    reason: >
      natDisableGuard (internal/app/cmd/init_nat_guard.go) blocks a NAT-disable only when
      a sandbox row has NetworkPlacement=="private" AND Status=="running". The phase's own
      live UAT (125-UAT.md, "Defect 2") proved that km create never writes a status
      attribute and that a freshly-created, fully-live, egress-active private sandbox
      (priv-913ff0f3) had Status=="" in DynamoDB the entire time it was running. The
      parallel doctor check with the identical Status=="running" bug (runningPrivateSandboxIDs
      in doctor_network.go) was fixed fail-safe in commit fa43cf4f (exclude only
      Status=="failed", not require Status=="running") — but init_nat_guard.go's
      natDisableGuard was never given the same fix, so it retains the exact defect the
      phase discovered and fixed once already, just in the sibling function. In its
      current form this guard will not fire for the common case (a freshly created,
      running private sandbox), so `km init` disabling NAT will silently orphan that
      sandbox's egress — precisely the failure the guard exists to prevent, and precisely
      the failure mode observed live as Defect 2.
    artifacts:
      - path: "internal/app/cmd/init_nat_guard.go"
        issue: "natDisableGuard line ~37: `if sb.NetworkPlacement == \"private\" && sb.Status == \"running\"` — positive-match on Status, which the UAT proved is empty in practice for a running private sandbox created via the (default) remote path."
    missing:
      - "Apply the same fail-safe fix used in doctor_network.go's runningPrivateSandboxIDs: treat a private sandbox row as NAT-dependent unless its Status is a definitively terminal value (e.g. \"failed\"), not require Status==\"running\"."
      - "Add a table-test case {NetworkPlacement: \"private\", Status: \"\"} asserting the guard blocks (init_nat_guard_test.go currently has no such case — it only exercises \"running\"/\"stopped\"/\"killed\", never the empty-status case that the live UAT proved is the actual runtime value)."
      - "docs/private-subnet-nat.md line 89 currently states the refuse-to-disable behavior as unconditional fact; either fix the code or caveat the doc until fixed."
---

# Phase 125: Per-profile private-subnet sandboxes with per-AZ NAT gateways — Verification Report

**Phase Goal:** Let an operator run public-subnet and private-subnet sandboxes side by side
in the same VPC, via two independent, dormant-by-default toggles: `spec.network.privateSubnet`
(per profile) and `network.nat_gateway` (install-level, one NAT gateway + EIP per AZ).

**Verified:** 2026-08-20T03:42:51Z
**Status:** passed (BLOCKER resolved — see Resolution below)
**Re-verification:** No — initial verification

## Summary

This is a well-executed, well-documented phase with a genuinely live-tested UAT that found
and fixed three real defects the automated suite could not catch. Verification against the
codebase independently confirms every structural claim in the UAT record (Terraform gating,
Go plumbing, module version pin, test-suite state, profile validation). One additional,
codebase-verified gap was found during this verification pass that the UAT did not catch: a
second instance of the exact bug class the UAT's own "Defect 2" already discovered and fixed
once, left unfixed in a sibling function. See Gap 1 below.

## Goal Achievement

### Observable Truths / Requirements

| # | Requirement | Status | Evidence |
|---|---|---|---|
| 1 | REQ-125-NETMOD — network v1.1.0: per-AZ EIP+NAT+route gated on `nat_gateway.enabled`, private subnets/route tables unconditional, plural outputs, `moved` blocks | ✓ VERIFIED | `infra/modules/network/v1.1.0/main.tf`: `aws_eip.nat`/`aws_nat_gateway.nat`/`aws_route.private_nat_gateway` all `count = var.nat_gateway.enabled ? length(...) : 0`; `aws_subnet.private_subnet` and `aws_route_table.private` unconditional (`count = length(var.vpc.private_subnets_cidr)`, no enabled gate). Plural outputs (`nat_gateway_ids`, `nat_eip_public_ips` at outputs.tf:56) confirmed. `moved` blocks present for `aws_route_table.private`, `aws_eip.nat`, `aws_nat_gateway.nat`. Live UAT Step 2 independently confirms the plan is additive-only (15 add / 4 change / 0 destroy) and Step 7 confirms private subnets survive as routeless islands with NAT off. |
| 2 | REQ-125-EC2SPOT — ec2spot v1.3.0: `public_subnets`→`sandbox_subnets`, new `associate_public_ip` bool default `true` | ✓ VERIFIED | `diff infra/modules/ec2spot/v1.2.0 v1.3.0`: variable renamed, `associate_public_ip` added with `default = true`, both `associate_public_ip_address = true` literals replaced with `var.associate_public_ip`. Default `true` preserves pre-v1.3.0 (dormant) behavior. |
| 3 | REQ-125-SUBPIN — per-substrate module version map (`ec2spot→v1.3.0`, `ecs→v1.0.0`) + regression test that every declared substrate/version resolves to an existing module dir | ✓ VERIFIED | `infra/templates/sandbox/terragrunt.hcl` has `substrate_module_versions = {ec2spot = "v1.3.0", ecs = "v1.0.0"}` with `terraform.source` interpolating the resolved local. `pkg/terragrunt/substrate_version_pin_test.go` exists with the described `TestSubstrateVersionPinPointsAtExistingModules` assertion. `infra/modules/ecs/v1.0.0/` and `infra/modules/ec2spot/v1.3.0/` both exist on disk. `git status --porcelain infra/modules/ecs/` is empty — scope boundary held, ECS not touched beyond the version-pin fix. |
| 4 | REQ-125-TOGGLE — `network.nat_gateway`→`KM_NAT_GATEWAY_ENABLED`→`get_env`, merge-list entry, `spec.network.privateSubnet` in JSON schema, no apiVersion bump | ✓ VERIFIED | `infra/live/use1/network/terragrunt.hcl:60`: `tobool(get_env("KM_NAT_GATEWAY_ENABLED", "false"))` — dormant default. `internal/app/config/config.go:918` has `"network.nat_gateway"` in the merge-list; drift WARN at `init.go:1883`. `sandbox_profile.schema.json:454-457` declares `privateSubnet` as boolean. apiVersion unchanged (v1alpha2, confirmed no schema-version bump anywhere in the diff). |
| 5 | REQ-125-PLUMB — `NetworkOutputs` gains `PrivateSubnets`/`NATGatewayIDs`; placement resolved once before the Phase 124 AZ sweep (sweep not forked); consumers updated; `RankAZs` drops NAT-less AZs | ✓ VERIFIED | `init.go:454-455` field additions + extraction at `init.go:2939-2940`. `awk` scoped to the RankAZs-call-through-retry block in create.go: 0 occurrences of `network.PublicSubnets` — confirms the sweep was retargeted, not forked; `grep -c 'capacity.RankAZs('` = 1 (single call site, unchanged). `pkg/capacity/rankaz.go:185-194` implements the NAT-AZ filter with a distinct, actionable error when zero AZs have NAT. Consumers `create.go:621/2431`(-equivalent), `budget.go` NetworkConfig, and `service_hcl.go` (`SandboxSubnets`/`AssociatePublicIP` fields, `EffectiveSandboxSubnets()` fallback-to-public safety net) all present. |
| 6 | REQ-125-REMOTE — create-handler S3-cached outputs carry the new fields; ttl-handler v1.0.0-pin risk resolved | ✓ VERIFIED (live) | `PrivateSubnets`/`NATGatewayIDs` carry `json` tags (`init.go:454-455`), so they round-trip through the S3-cached `outputs.json` the create-handler Lambda reads. Live UAT Step 3 proves this end-to-end: `km create` (defaults `--remote`) placed `priv-913ff0f3` correctly (no public IP, correct private subnet, correct same-AZ NAT route) via that exact path. `cmd/ttl-handler/main.go:1230` remains pinned to `ec2spot/v1.0.0` (unchanged, per design — RESEARCH.md Finding 1). Live UAT Step 6 destroyed a v1.3.0-created sandbox through the v1.0.0-pinned stub with zero orphans (instances/ENIs/EBS all "none") — the phase's own highest-risk unknown proven live, not just analytically. |
| 7 | REQ-125-GUARD — `network_placement` round-trips through `SandboxMetadata`; `km create` fail-fast on private+no-NAT; `km init` refuses to disable NAT while private sandboxes run | ⚠️ PARTIAL — one sub-requirement FAILED | Round-trip: VERIFIED (`pkg/aws/metadata.go:123` field + `sandbox_dynamo.go:338-339` unmarshal + `:591-592` marshal). Fail-fast: VERIFIED — `checkPrivateSubnetGuard` (create.go:121) takes `natGatewayIDs`, both call sites (`create.go:707`, `create.go:2549`) pass `networkOutputs.NATGatewayIDs`; live UAT Step 8 proves the fixed guard fails fast with the exact named message before any artifact write. **Refuse-to-disable: FAILED** — see Gap 1 below; `natDisableGuard` (`init_nat_guard.go`) still requires `Status == "running"`, which the phase's own live UAT (Defect 2) proved is never true for a freshly-created running private sandbox row. |
| 8 | REQ-125-DOCTOR — WARN on NAT-enabled-unused (with cost), WARN on private-without-NAT, `km init` prints cost on enable | ✓ VERIFIED | `doctor.go:4645/4655` wire `checkNATIdle`/`checkPrivateWithoutNAT`; `init.go:2376-2377` prints the `$132/month` / `$0.045/GB` cost notice. Live UAT spot-checks confirm both WARNs fire correctly at each stage, including the Defect-2 fix verified live (`✓ NAT gateway idle … 1 private sandbox(es) depend on it`). |
| 9 | REQ-125-UAT — live 7(8)-step UAT | ⚠️ PARTIAL (documented) | 6/8 steps PASS, 2 steps (Step 1 baseline, Step 5 ICE rotation) explicitly recorded NOT EXERCISED with stated reasons, not glossed as passed. Three real defects found and fixed live; Step 6 (highest-risk unknown) proven live. See "Honesty of the UAT record" below. |

**Score:** 8/9 requirements fully verified; 1 (REQ-125-GUARD) partially verified with one FAILED sub-requirement.

### Priority-by-priority findings (per the verification request)

1. **Dormancy** — CONFIRMED. `KM_NAT_GATEWAY_ENABLED` defaults `"false"` in the live unit; `Network.NATGateway` is a nil `*bool` when absent from yaml (`GetNATGatewayEnabled()` returns false); `spec.network.privateSubnet` omitted ⇒ `AssociatePublicIP = !false = true` (pre-v1.3.0 behavior) and `EffectiveSandboxSubnets()` falls back to `PublicSubnets`. Byte-identical to Phase 124 when both toggles absent.
2. **Reversibility** — CONFIRMED. In `infra/modules/network/v1.1.0/main.tf`, `nat_gateway.enabled` gates only `aws_eip.nat`, `aws_nat_gateway.nat`, `aws_route.private_nat_gateway` (all `count = enabled ? N : 0`). `aws_subnet.private_subnet` and `aws_route_table.private` are unconditional. This is exactly the property Step 7 of the live UAT exercised and confirmed (private subnets + per-AZ route tables survived with zero `0.0.0.0/0` routes after NAT teardown).
3. **AZ sweep not forked** — CONFIRMED. `awk` over the RankAZs-through-retry block in `create.go` finds zero `network.PublicSubnets` references; exactly one `capacity.RankAZs(` call site exists in the file.
4. **REQ-125-SUBPIN scope boundary** — CONFIRMED. `git status --porcelain infra/modules/ecs/` is empty; `ecs/v1.0.0` and `ec2spot/v1.3.0` both exist on disk; the regression test (`pkg/terragrunt/substrate_version_pin_test.go`) asserts this generally.
5. **Three live-UAT fixes actually in the tree**:
   - `km env` emits `KM_NAT_GATEWAY_ENABLED` — CONFIRMED (`internal/app/cmd/env.go:62`).
   - `runningPrivateSandboxIDs` no longer requires `Status=="running"` — CONFIRMED (`doctor_network.go:33-37`, excludes only `Status=="failed"`).
   - `checkPrivateSubnetGuard` takes NAT gateway IDs, both call sites pass `networkOutputs.NATGatewayIDs` — CONFIRMED (`create.go:121,707,2549`).
6. **Test suite state** — INDEPENDENTLY RE-RUN, not just trusted from the record. `go test ./... -count=1 -timeout 600s`: exit 1 (captured via `$?`, not piped), 41 packages `ok`, exactly the same 5 subtests failing (`TestBootstrapSCPApplyPath`, `TestBootstrapSCPSkipped_OrganizationBlank`, `TestClusterAdd`, `TestClusterRm`, `TestClusterAddPersistFailure`), all with the "no real AWS credentials (fast-fail seam)" message. No other `FAIL` anywhere. `bash scripts/validate-all-profiles.sh`: exit 0, 21 profiles, matches the record exactly.
7. **Honesty of the UAT record** — CONFIRMED. Steps 1 and 5 are explicitly labeled NOT EXERCISED with specific, credible reasons (no public-profile create was needed since dormancy is covered by compiler tests + the untouched pre-existing public sandbox; no ICE condition occurred or was forced). No other step overclaims — each PASS is backed by concrete AWS-observed evidence (instance IDs, route table IDs, NAT IDs, IP addresses) rather than assertion. Step 6's method deviation (destroy via `km destroy --remote` through the ttl-handler Lambda rather than waiting for TTL expiry) is explained and is a legitimate equivalent: the profile's `teardown_policy: stop` means TTL expiry would never have reached the terraform destroy path at all, and `km destroy --remote` is documented elsewhere in this repo as routing through the exact same ttl-handler Lambda event path. `docs/private-subnet-nat.md`'s residual-risk section appropriately hedges ("if UAT step 6 has not yet been exercised... treat as accepted risk") and points to the UAT record rather than asserting a blanket guarantee — consistent with the record.

   One accuracy note, not a UAT-honesty issue: `docs/private-subnet-nat.md:89` documents the refuse-to-disable guard's behavior as unconditional ("km init refuses to disable NAT while any sandbox row is network_placement=private and running") without the caveat that this is currently unreliable for freshly-created sandboxes (Gap 1). This is a documentation/code mismatch introduced by Gap 1, not a UAT overclaim — the UAT record itself never exercised or claimed this specific guard path (Step 7's NAT-off flip happened only after the private sandbox from Step 3 had already been destroyed in Step 6).

## Gap 1 (BLOCKER) — `km init` refuse-to-disable guard does not actually block the common case

**Requirement affected:** REQ-125-GUARD ("`km init` refuses to disable NAT while private sandboxes run").

**Root cause:** `natDisableGuard` (`internal/app/cmd/init_nat_guard.go:37`) requires
`sb.NetworkPlacement == "private" && sb.Status == "running"`. The live UAT's own "Defect 2"
finding proved that `km create` never writes a `status` attribute to the `{prefix}-sandboxes`
row, and that a fully live, actively-egressing private sandbox (`priv-913ff0f3`) had
`Status == ""` for its entire lifetime. That finding was used to fix the *sibling* function
`runningPrivateSandboxIDs` in `doctor_network.go` (commit `fa43cf4f`, "count as NAT-dependent
unless status is definitively terminal") — but `natDisableGuard` was never revisited with the
same fix, despite implementing the identical predicate on the identical field. It was written
in Plan 06, before Defect 2 was discovered in Plan 09's live UAT, and nothing in the phase's
subsequent commits touched `init_nat_guard.go` again (`git log` shows exactly one commit
touching that file).

**Impact:** In its current form, an operator who runs `km init` to set `network.nat_gateway:
false` while a freshly-created private sandbox is genuinely running will **not** be blocked —
the guard silently no-ops and NAT is torn down out from under the running sandbox's only
egress path, exactly the failure this guard exists to prevent. This is not a hypothetical: it
is the exact scenario the phase's own live UAT already demonstrated is the real-world DynamoDB
state for a running private sandbox.

**Why this wasn't caught by the live UAT:** Step 7 (NAT teardown) ran after Step 6 had already
destroyed the only private sandbox in play (`priv-913ff0f3`), so at the moment `network.nat_gateway:
false` was applied, zero private sandbox rows existed and the guard had nothing to block —
its (broken) logic was never exercised against a live, running private sandbox.

**Recommended fix:** Apply the same fail-safe predicate used in `runningPrivateSandboxIDs` —
treat a private-placed row as NAT-dependent unless `Status` is definitively terminal (e.g.
`"failed"`), not require `Status == "running"`. Add a regression test case
`{NetworkPlacement: "private", Status: ""}` to `init_nat_guard_test.go` (currently absent —
existing cases only cover `"running"`/`"stopped"`/`"killed"`, never the empty-status value the
live UAT proved is the actual runtime state). Update `docs/private-subnet-nat.md:89` if needed
once fixed.

This does not look like an intentional deviation (no alternative implementation achieves the
same intent) — it looks like a fix that was applied to one of two symmetric call sites and
missed the other. No override is suggested; this should be fixed, not accepted.

## Anti-Patterns Found

None of the debt-marker classes (`TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER`) introduced
by this phase's changed files were found. The few `PLACEHOLDER_*`/`TODO` hits in
`create.go`/`service_hcl.go` are pre-existing (Docker substrate template placeholders, a
Phase-66 TODO), untouched by Phase 125.

## Requirements Coverage

All 9 phase-local requirement IDs (REQ-125-NETMOD, EC2SPOT, SUBPIN, TOGGLE, PLUMB, REMOTE,
GUARD, DOCTOR, UAT) were traced to concrete code/infra artifacts and cross-checked against
ROADMAP.md's full text. 8 fully satisfied; REQ-125-GUARD partially satisfied (round-trip and
fail-fast sub-requirements verified; refuse-to-disable sub-requirement FAILED — see Gap 1).

## Human Verification Required

None beyond what's already recorded. The two NOT-EXERCISED UAT steps (baseline, ICE rotation)
are informational gaps in live coverage, not ambiguous/judgment items — they are adequately
covered by indirect evidence (dormancy tests, unchanged pre-existing public sandbox, unit
tests for the NAT-aware `RankAZs` filter) per the UAT record's own honest framing, and do not
block phase completion on their own.

## Resolution (2026-08-20, commit `a54a2800`)

The BLOCKER below is FIXED. `natDisableGuard` no longer requires `Status == "running"`; it
blocks a NAT-disable for any `network_placement=private` row whose status is not definitively
terminal (`failed`/`killed`/`destroyed`/`terminated`). Empty status — the state an actually
running sandbox has, since `km create` never writes the attribute — now blocks, which is the
whole point. A stopped private sandbox blocks too: it needs egress again the moment it resumes.

Three supporting changes landed with it: a regression test pinning the empty-status case
(named for the real sandbox, `priv-913ff0f3`), a correction to the existing test that had
asserted stopped/killed both pass through (codifying the fail-open half), and a fix to
`docs/private-subnet-nat.md`, which had restated the old `status=running` behaviour as fact.

Full suite re-run after the fix: 41 packages ok, exactly the same 5 documented
credential-seam failures, no new regressions.

**This verification's most valuable finding.** The live UAT could not have caught it: Step 7
(NAT teardown) ran only after Step 6 had already destroyed the sole private sandbox, so the
guard had nothing live to block and passed by accident. It also makes the phase's real pattern
explicit — four defects, three sharing one root cause: *code reasoning about sandbox state
from DynamoDB attributes that are never written.* Fixing the first instance (`doctor_network.go`)
without sweeping for siblings left this one behind.

## Gaps Summary

**RESOLVED** — see Resolution above. Original finding retained for the record:

One BLOCKER: the `km init` NAT-disable refuse guard (`natDisableGuard`) has the same
Status-field bug that this phase's own live UAT already found and fixed once in a sibling
function, but left unfixed here. This is a small, well-scoped fix (mirror the
`runningPrivateSandboxIDs` fail-safe logic + one missing test case) — recommend a quick
in-place follow-up commit rather than replanning the phase.

Everything else — Terraform gating/reversibility, the Go plumbing and AZ-sweep integration,
the module version pin and its scope boundary, the config/schema toggles, doctor WARNs, the
independently-reproduced test-suite and profile-validation state, and the honesty of the live
UAT record — is verified against the codebase, not merely asserted by SUMMARY.md claims.

---

_Verified: 2026-08-20T03:42:51Z_
_Verifier: Claude (gsd-verifier)_
