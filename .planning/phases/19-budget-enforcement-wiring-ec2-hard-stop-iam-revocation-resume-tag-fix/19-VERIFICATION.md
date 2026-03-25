---
phase: 19-budget-enforcement-wiring-ec2-hard-stop-iam-revocation-resume-tag-fix
verified: 2026-03-24T00:00:00Z
status: passed
score: 7/7 must-haves verified
re_verification: false
---

# Phase 19: Budget Enforcement Wiring — EC2 Hard-Stop / IAM Revocation / Resume Tag Fix

**Phase Goal:** Fix two cross-phase wiring gaps that cause budget hard enforcement to silently fail at runtime: (1) budget-enforcer Lambda receives empty `instance_id`/`role_arn` because its Terragrunt config has no dependency on the ec2spot module, and (2) `km budget add` EC2 resume uses the wrong tag key (`tag:sandbox-id` instead of `tag:km:sandbox-id`).
**Verified:** 2026-03-24
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Budget-enforcer terragrunt.hcl contains a dependency block that reads ec2spot outputs | VERIFIED | `dependency "sandbox"` block present at line 41 of `pkg/compiler/budget_enforcer_hcl.go` template |
| 2 | Budget-enforcer inputs include instance_id wired from dependency.sandbox.outputs.ec2spot_instances | VERIFIED | `instance_id = try(values(dependency.sandbox.outputs.ec2spot_instances)[0].instance_id, "")` at line 87 |
| 3 | Budget-enforcer inputs include role_arn wired from dependency.sandbox.outputs.iam_role_arn | VERIFIED | `role_arn = dependency.sandbox.outputs.iam_role_arn` at line 88 |
| 4 | ec2spot module exposes iam_role_arn as a Terraform output | VERIFIED | `output "iam_role_arn"` with `try(aws_iam_role.ec2spot_ssm[0].arn, "")` in `infra/modules/ec2spot/v1.0.0/outputs.tf` lines 39-42 |
| 5 | resumeEC2Sandbox uses tag:km:sandbox-id filter to find stopped instances | VERIFIED | `awssdk.String("tag:km:sandbox-id")` at line 226 of `internal/app/cmd/budget.go` |
| 6 | km budget add for a stopped EC2 sandbox finds and starts the instance | VERIFIED | `TestBudgetAdd_ResumesStoppedEC2` passes; full DescribeInstances + StartInstances flow implemented and wired |
| 7 | Unit test verifies the exact tag key used in DescribeInstances filter | VERIFIED | `TestResumeEC2Sandbox_UsesCorrectTagKey` passes — source-level check using `os.ReadFile` + `strings.Contains` pattern |

**Score:** 7/7 truths verified

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/compiler/budget_enforcer_hcl.go` | Budget-enforcer HCL template with dependency block | VERIFIED | Contains `dependency "sandbox"`, `mock_outputs`, `instance_id`, `role_arn`, `dependency.sandbox.outputs`; substantive (130 lines); wired via `generateBudgetEnforcerHCL` used by `compiler.Compile()` |
| `infra/modules/ec2spot/v1.0.0/outputs.tf` | IAM role ARN output for budget-enforcer consumption | VERIFIED | Contains `output "iam_role_arn"` with `try()` guard; substantive (43 lines); consumed by dependency block in budget-enforcer HCL |
| `pkg/compiler/budget_enforcer_hcl_test.go` | Tests verifying dependency block and wired outputs | VERIFIED | `TestGenerateBudgetEnforcerHCL_EC2` checks 5 dependency-related strings; `TestGenerateBudgetEnforcerHCL_ECS` checks 2; all pass |
| `internal/app/cmd/budget.go` | Corrected tag filter for EC2 instance lookup | VERIFIED | Line 226: `awssdk.String("tag:km:sandbox-id")`; substantive; wired into `resumeEC2Sandbox` called from `runBudgetAdd` |
| `internal/app/cmd/budget_test.go` | Tag key verification test | VERIFIED | `TestResumeEC2Sandbox_UsesCorrectTagKey` present; passes; also asserts absence of broken pattern `awssdk.String("tag:sandbox-id")` |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `pkg/compiler/budget_enforcer_hcl.go` | `infra/modules/ec2spot/v1.0.0/outputs.tf` | `dependency "sandbox"` block reads `iam_role_arn` and `ec2spot_instances` outputs | VERIFIED | Template references `dependency.sandbox.outputs.iam_role_arn` and `dependency.sandbox.outputs.ec2spot_instances`; ec2spot exposes both outputs |
| `internal/app/cmd/budget.go` | `infra/modules/ec2spot/v1.0.0/main.tf` | tag key must match: `km:sandbox-id` | VERIFIED | `budget.go` line 226 uses `tag:km:sandbox-id`; ec2spot `main.tf` applies tag `"km:sandbox-id" = var.sandbox_id` on all EC2 resources including `aws_ec2_tag.ec2spot_sandbox_id` (line 343) |

---

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| BUDG-07 | 19-01-PLAN.md | Budget-enforcer Lambda must receive real `instance_id` and `role_arn` via EventBridge payload (not empty strings) so EC2 StopInstances and IAM revocation execute at runtime | SATISFIED | Dependency block wires real values from ec2spot module outputs into budget-enforcer inputs at apply time; `mock_outputs` provide safe fallback on first apply and destroy |
| BUDG-08 | 19-02-PLAN.md | `km budget add` EC2 resume path must find and start stopped instances using the correct `km:sandbox-id` tag key | SATISFIED | `resumeEC2Sandbox` corrected from `tag:sandbox-id` to `tag:km:sandbox-id`; matches tag key applied by ec2spot module; source-level test locks the contract |

No orphaned requirements — both IDs declared in plan frontmatter map to Phase 19 in REQUIREMENTS.md, both marked Complete.

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/app/cmd/budget.go` | 216 | `return nil` | Info | Legitimate control-flow early return in `runBudgetAdd` — not a stub |

No blockers or warnings found.

---

### Human Verification Required

None. All verification is programmatic:

- Tag key correctness: source-level test asserts exact string
- Dependency block: template string checked by compiler tests
- ec2spot output: Terraform output declaration directly inspected
- Test suite: all 9 budget tests and 57+ compiler tests pass

---

### Test Run Results

```
pkg/compiler (TestGenerateBudgetEnforcerHCL_EC2, TestGenerateBudgetEnforcerHCL_ECS): PASS
internal/app/cmd (all 9 budget tests including TestResumeEC2Sandbox_UsesCorrectTagKey): PASS
internal/app/cmd (full suite, 100+ tests): PASS — no regressions
```

---

### Summary

Phase 19 achieves its goal. Both wiring gaps are closed:

**Gap 1 (BUDG-07):** The budget-enforcer HCL template now emits a `dependency "sandbox"` block that reads `ec2spot_instances` and `iam_role_arn` from the sibling sandbox module at Terragrunt apply time. The `mock_outputs` block provides safe defaults on first apply and destroy. The ec2spot module has a new `iam_role_arn` output exposing the SSM instance role ARN. Compiler tests verify all dependency-related strings are present in generated HCL.

**Gap 2 (BUDG-08):** The `resumeEC2Sandbox` function now uses `tag:km:sandbox-id` (matching the actual tag key applied by the ec2spot Terraform module) instead of the incorrect `tag:sandbox-id`. A source-level test locks this contract permanently, using the same `os.ReadFile + strings.Contains` pattern established in prior phases.

No placeholders, stubs, or incomplete implementations found.

---

_Verified: 2026-03-24_
_Verifier: Claude (gsd-verifier)_
