---
phase: 56-learn-mode-ami-snapshot-and-lifecycle-management
plan: 03
subsystem: infra
tags: [scp, iam, ec2, ami, bootstrap, aws-organizations]

# Dependency graph
requires:
  - phase: 56-learn-mode-ami-snapshot-and-lifecycle-management
    provides: "Phase 56 CONTEXT.md and RESEARCH.md — IAM gap analysis (Pitfall 6) and SCP trustedBase principals"

provides:
  - "Updated km-sandbox-containment SCP DenyInfraAndStorage with ec2:DeregisterImage, ec2:DeleteSnapshot, ec2:CreateTags added (trustedBase exempt)"
  - "BuildSCPPolicy() extracted pure helper testable without AWS access"
  - "WriteOperatorIAMGuidance() emitting Phase 56 AMI-lifecycle positive-allow requirements block"
  - "4 passing unit tests covering SCP additions, Describe* exclusion, trustedBase preservation, guidance text"

affects: [56-04, 56-05, 56-06, 56-07, 56-08, bootstrap-operations]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Extracted SCP policy builder (BuildSCPPolicy) as pure function for unit testing without AWS credentials"
    - "Exported io.Writer-based guidance emitter (WriteOperatorIAMGuidance) for test injection"
    - "Inline local type definitions promoted to package-level exported types (SCPStatement, SCPPolicyDoc)"

key-files:
  created:
    - "internal/app/cmd/bootstrap_test.go (4 new tests added to existing file)"
  modified:
    - "internal/app/cmd/bootstrap.go — BuildSCPPolicy extracted, 3 AMI ops added to DenyInfraAndStorage, WriteOperatorIAMGuidance added, runShowSCP updated"

key-decisions:
  - "ec2:DeregisterImage, ec2:DeleteSnapshot, ec2:CreateTags added to SCP DenyInfraAndStorage Deny list with existing trustedBase ArnNotLike exemption — makes the IAM contract explicit"
  - "ec2:DescribeImages and ec2:DescribeSnapshots NOT added to SCP — read-only ops should not be SCP-gated; documented in positive-allow guidance instead"
  - "Operator IAM guidance emitted as text block (not programmatic IAM policy) — operator SSO permission sets are out of scope for bootstrap.go to mutate"
  - "BuildSCPPolicy and WriteOperatorIAMGuidance exported (capital B/W) so test package (cmd_test) can call them directly without AWS access"
  - "infra/modules/scp/v1.0.0/main.tf NOT updated — bootstrap.go is the canonical SCP source for the km bootstrap command; Terraform module mirrors it but is managed separately"

patterns-established:
  - "SCP policy construction: extract into pure BuildSCPPolicy() for testability; call from runShowSCP"
  - "Operator guidance: use WriteOperatorIAMGuidance(w io.Writer) pattern for io.Writer injection in tests"

requirements-completed: [P56-10]

# Metrics
duration: 12min
completed: 2026-04-26
---

# Phase 56 Plan 03: SCP DenyInfraAndStorage AMI Ops + Operator IAM Positive-Allow Guidance Summary

**ec2:DeregisterImage/DeleteSnapshot/CreateTags added to SCP DenyInfraAndStorage with trustedBase exemption; BuildSCPPolicy extracted for pure unit testing; WriteOperatorIAMGuidance emits Phase 56 AMI-lifecycle positive-allow requirements in km bootstrap show-scp output**

## Performance

- **Duration:** ~12 min
- **Started:** 2026-04-26T08:27:25Z
- **Completed:** 2026-04-26T08:39:00Z
- **Tasks:** 1 of 1
- **Files modified:** 2

## Accomplishments

- Added 3 AMI-lifecycle mutating ops (`ec2:DeregisterImage`, `ec2:DeleteSnapshot`, `ec2:CreateTags`) to the `DenyInfraAndStorage` SCP Deny statement alongside the pre-existing `ec2:CreateImage`/`ec2:CopyImage`/`ec2:ExportImage` entries; trustedBase ArnNotLike exemption is unchanged
- Extracted `BuildSCPPolicy()` as an exported pure function (no AWS calls) from the inline `runShowSCP` construction, enabling Tests 1-3 to run without any AWS credentials
- Added `WriteOperatorIAMGuidance(w io.Writer)` that emits a full IAM positive-allow requirements block documenting all 5 ops (3 mutating + 2 read-only Describe*) with the SCP-vs-IAM-allow distinction rationale; called from `runShowSCP` after SCP JSON output
- All 4 required tests pass: `TestBootstrapSCP_IncludesAMILifecycleMutatingOps`, `TestBootstrapSCP_DescribeOpsNotInDeny`, `TestBootstrapSCP_TrustedBaseUnchanged`, `TestBootstrapShowSCP_EmitsOperatorPositiveAllowGuidance`
- `go build ./...` clean

## Task Commits

1. **Task 1: Add AMI lifecycle ops to SCP + emit operator IAM guidance** - `dcd4c00` (feat)

**Plan metadata:** (docs commit — see below)

## Files Created/Modified

- `internal/app/cmd/bootstrap.go` — SCPStatement/SCPPolicyDoc types promoted to package level; BuildSCPPolicy() extracted; 3 AMI ops added to DenyInfraAndStorage; WriteOperatorIAMGuidance() added; runShowSCP updated to use both helpers
- `internal/app/cmd/bootstrap_test.go` — testTrustedBase() helper + 4 new SCP/guidance tests added

## Decisions Made

1. **Mutating ops added to SCP Deny + trustedBase exemption (not just documented):** `ec2:DeregisterImage`, `ec2:DeleteSnapshot`, `ec2:CreateTags` are now explicitly in the deny list. This makes the IAM contract machine-readable and prevents unintentional access by non-trusted principals. The trustedBase ArnNotLike exemption (AWSReservedSSO + km-provisioner + km-lifecycle + km-ecs-spot-handler + km-ttl-handler + km-create-handler) was not modified.

2. **Read-only ops excluded from SCP Deny:** `ec2:DescribeImages` and `ec2:DescribeSnapshots` are NOT added to `DenyInfraAndStorage`. Read-only ops should never be SCP-gated in this model — they are documented in the positive-allow guidance as ops the operator role must explicitly allow in its IAM policy.

3. **Guidance emitted as text block, not programmatic IAM:** `WriteOperatorIAMGuidance` prints a documentation block to the output writer rather than programmatically mutating any IAM policy. The operator's SSO permission set is managed via AWS IAM Identity Center (SSO), which is entirely outside bootstrap.go's scope. The guidance text gives the operator an exact IAM JSON snippet to copy.

4. **infra/modules/scp/v1.0.0/main.tf NOT updated:** The Terraform SCP module in `infra/modules/scp/v1.0.0/main.tf` mirrors the bootstrap.go SCP but is managed separately via Terragrunt apply. Adding the new actions to bootstrap.go's SCP JSON is sufficient for the `km bootstrap` code path. Keeping the Terraform module in sync is a deferred operator action (see Operator Action Items below).

5. **Types exported for test access:** `SCPStatement`, `SCPPolicyDoc`, `BuildSCPPolicy`, `WriteOperatorIAMGuidance` are all exported (capitalized) so the external `cmd_test` package can call them directly in Tests 1-4 without needing real AWS credentials or `km-config.yaml`.

## Deviations from Plan

None — plan executed exactly as written. All four extractions (`BuildSCPPolicy`, `WriteOperatorIAMGuidance`) were performed as specified. Types were promoted to package-level and exported for test access (required by the plan's test approach, not a deviation).

## Issues Encountered

None.

## Operator Action Items (REQUIRED)

### Action 1: Re-run `km bootstrap` to apply updated SCP

**Required to activate the new SCP entries.** The three new AMI-lifecycle operations (`ec2:DeregisterImage`, `ec2:DeleteSnapshot`, `ec2:CreateTags`) are now in bootstrap.go's SCP JSON but are not in the deployed AWS SCP until `km bootstrap` is re-run against the application account.

```bash
# Re-run bootstrap to deploy the updated SCP to the application account
km bootstrap --dry-run=false
```

Without this, `km ami delete` and `km ami copy` (Phase 56 Wave 2) may hit `UnauthorizedOperation` for non-trustedBase principals attempting these operations.

**If `infra/modules/scp/v1.0.0/main.tf` is also used as a Terraform source of truth:** also run `terragrunt apply` in `infra/live/management/scp/` to keep the Terraform-managed SCP in sync with the bootstrap.go version. The new actions must be added manually to the Terraform module's action list if Terraform is the deployed source.

### Action 2: Update operator SSO permission set (or klanker-terraform role) with IAM positive-allow grants

**Required for `km ami list`, `km ami delete`, `km doctor` stale-AMI to work.** The SCP exemption un-blocks these operations for trustedBase principals — but un-blocking is NOT granting. The operator's IAM allow policy must affirmatively include these actions.

Run `km bootstrap --scp` to see the full guidance block, then add the following statement to the operator's SSO permission set or the `klanker-terraform` role inline policy:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "KMAMILifecycle",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateImage",
        "ec2:CopyImage",
        "ec2:DeregisterImage",
        "ec2:DeleteSnapshot",
        "ec2:CreateTags",
        "ec2:DescribeImages",
        "ec2:DescribeSnapshots"
      ],
      "Resource": "*"
    }
  ]
}
```

Without `ec2:DescribeImages` and `ec2:DescribeSnapshots`, `km ami list` will fail with `UnauthorizedOperation` even after the SCP is re-deployed — because IAM is default-deny and the SCP only removes the ceiling, it does not grant.

## Test Results

```
=== RUN   TestBootstrapSCP_IncludesAMILifecycleMutatingOps
--- PASS: TestBootstrapSCP_IncludesAMILifecycleMutatingOps (0.00s)
=== RUN   TestBootstrapSCP_DescribeOpsNotInDeny
--- PASS: TestBootstrapSCP_DescribeOpsNotInDeny (0.00s)
=== RUN   TestBootstrapSCP_TrustedBaseUnchanged
--- PASS: TestBootstrapSCP_TrustedBaseUnchanged (0.00s)
=== RUN   TestBootstrapShowSCP_EmitsOperatorPositiveAllowGuidance
--- PASS: TestBootstrapShowSCP_EmitsOperatorPositiveAllowGuidance (0.00s)
PASS
ok  github.com/whereiskurt/klankrmkr/internal/app/cmd  4.746s
```

All 8 bootstrap tests pass (4 new + 4 pre-existing). `go build ./...` clean.

## Next Phase Readiness

- Phase 56 Wave 2 (`km ami list`, `km ami delete`, `km ami bake`, `km ami copy`) can now proceed with confidence that the SCP IAM contract is explicit and documented
- Operator must complete both Action Items above before `km ami delete` or `km ami list` will work in production
- `checkStaleAMIs` (`km doctor`) similarly depends on operator IAM allow for `ec2:DescribeImages`

---
*Phase: 56-learn-mode-ami-snapshot-and-lifecycle-management*
*Completed: 2026-04-26*
