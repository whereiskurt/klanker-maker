# Phase 19: Budget Enforcement Wiring — Research

**Researched:** 2026-03-24
**Domain:** Go compiler templates (Terragrunt HCL generation), Terraform module outputs, EC2 tag filtering
**Confidence:** HIGH

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| BUDG-07 | At 100% compute budget, Lambda calls `StopInstances` on sandbox EC2 instance; at 100% AI budget, Lambda revokes Bedrock IAM permissions from sandbox role. Both actions receive the actual EC2 instance ID and IAM role ARN in the EventBridge payload. | Fix: add `dependency "sandbox"` block in `budgetEnforcerHCLTemplate` and expose `iam_role_arn` output from ec2spot module. |
| BUDG-08 | `km budget add` for a stopped EC2 sandbox finds the instance via correct tag and starts it. | Fix: change tag filter from `tag:sandbox-id` to `tag:km:sandbox-id` in `resumeEC2Sandbox`. |
</phase_requirements>

---

## Summary

Phase 19 closes two precisely-diagnosed cross-phase wiring gaps identified in the v1.0 milestone audit. The gaps are surgical — both root causes are in a few lines of code — but each causes complete silent runtime failure of a critical enforcement path.

**Gap 1 (BUDG-07):** The budget-enforcer Lambda receives `instance_id=""` and `role_arn=""` in every EventBridge invocation. The root cause is that `pkg/compiler/budget_enforcer_hcl.go` generates `budget-enforcer/terragrunt.hcl` with no Terragrunt `dependency` block. Without a dependency on the parent sandbox module (ec2spot), Terragrunt never evaluates module outputs at apply time, so `instance_id` and `role_arn` stay as the hardcoded empty-string literals from `service.hcl`. The Lambda handler at `cmd/budget-enforcer/main.go:343` already guards against empty `instance_id` — it logs a warning and returns — so the no-op is perfectly silent.

**Gap 2 (BUDG-08):** `km budget add` EC2 resume calls `DescribeInstances` with filter `tag:sandbox-id` but EC2 instances are tagged `km:sandbox-id` (confirmed in `infra/modules/ec2spot/v1.0.0/main.tf:344`). Because no instance matches the wrong tag key, `stoppedIDs` is always empty and `StartInstances` is never called.

Both fixes are pure code changes — no AWS infrastructure changes, no new dependencies, no schema changes. The fix to Gap 1 also requires adding one missing Terraform output (`iam_role_arn`) to the ec2spot module.

**Primary recommendation:** Make two targeted edits: (1) add a `dependency "sandbox"` block to `budgetEnforcerHCLTemplate` that wires `instance_id` and `role_arn` from ec2spot outputs; (2) add `iam_role_arn` to `infra/modules/ec2spot/v1.0.0/outputs.tf`; (3) change the tag filter key in `resumeEC2Sandbox` from `tag:sandbox-id` to `tag:km:sandbox-id`. Add unit tests for each fix.

---

## Standard Stack

### Core (already in use — no new dependencies)

| Component | Location | Purpose |
|-----------|----------|---------|
| Go `text/template` | `pkg/compiler/budget_enforcer_hcl.go` | Generates Terragrunt HCL at sandbox creation time |
| Terragrunt `dependency` block | HCL — see pattern below | Wires module outputs as inputs to dependent modules |
| Terraform `output` block | `infra/modules/ec2spot/v1.0.0/outputs.tf` | Exposes module outputs for Terragrunt consumption |
| AWS SDK v2 EC2 `DescribeInstances` | `internal/app/cmd/budget.go:223` | Finds stopped instances to resume |

No new packages or modules are required. This phase is entirely about wiring existing code together correctly.

---

## Architecture Patterns

### Pattern 1: Terragrunt Dependency Block for Inter-Module Output Wiring

**What:** A Terragrunt `dependency` block in `budget-enforcer/terragrunt.hcl` reads outputs from the sibling `../` (parent sandbox) Terragrunt module. The dependency fires at `terragrunt apply` time, so outputs are always real values from the current apply state.

**When to use:** Any time one Terragrunt module needs a value that another module owns and creates at apply time (instance IDs, role ARNs, ARNs in general).

**Example — what the fixed template must emit:**

```hcl
# In budget-enforcer/terragrunt.hcl
dependency "sandbox" {
  config_path = "${get_terragrunt_dir()}/.."

  # Graceful degradation when sandbox hasn't been applied yet
  mock_outputs = {
    ec2spot_instances           = {}
    iam_instance_profile_name   = ""
    iam_role_arn                = ""
  }
  mock_outputs_allowed_on_destroy = true
}

inputs = merge(
  local.be_inputs,
  {
    lambda_zip_path   = "${local.repo_root}/build/budget-enforcer.zip"
    budget_table_name = "${local.site_vars.locals.site.label}-budgets"
    budget_table_arn  = local.budget_table_arn
    state_bucket      = get_env("KM_ARTIFACTS_BUCKET", "")
    email_domain      = "sandboxes.${local.site_vars.locals.site.domain}"
    operator_email    = get_env("KM_OPERATOR_EMAIL", "")

    # Wired from ec2spot module outputs via dependency block
    instance_id = try(values(dependency.sandbox.outputs.ec2spot_instances)[0].instance_id, "")
    role_arn    = dependency.sandbox.outputs.iam_role_arn
  }
)
```

**How `instance_id` is extracted:** The ec2spot module outputs `ec2spot_instances` as a map keyed by `"${region}-${idx}-${instance_idx}"`. For the standard single-instance sandbox, `values(map)[0].instance_id` retrieves the sole entry. The `try(…, "")` guard handles the ECS case (empty map) or pre-apply state gracefully.

**Confidence:** HIGH — this is standard Terragrunt practice. The `github-token` module in this codebase uses the same `read_terragrunt_config` pattern for sibling config; the dependency block is the Terragrunt-idiomatic way to read outputs rather than locals.

### Pattern 2: New Terraform Output — `iam_role_arn`

**What:** The ec2spot module creates `aws_iam_role.ec2spot_ssm` but does not expose its ARN as an output. The budget-enforcer dependency block requires it.

**Fix:** Add to `infra/modules/ec2spot/v1.0.0/outputs.tf`:

```hcl
output "iam_role_arn" {
  description = "ARN of the IAM role attached to sandbox EC2 instances"
  value       = try(aws_iam_role.ec2spot_ssm[0].arn, "")
}
```

The `try(…, "")` guard mirrors the existing `iam_instance_profile_name` output pattern already present in the file and handles the zero-instance edge case.

**Confidence:** HIGH — the role resource already exists (`aws_iam_role.ec2spot_ssm[0]`). The `.arn` attribute is a standard Terraform attribute on every `aws_iam_role` resource.

### Pattern 3: EC2 Tag Filter Key Correction

**What:** `resumeEC2Sandbox` in `internal/app/cmd/budget.go:226` filters with `tag:sandbox-id`. All EC2 instances are tagged `km:sandbox-id` (verified in `infra/modules/ec2spot/v1.0.0/main.tf:344`, `aws_ec2_tag.ec2spot_sandbox_id`).

**Fix:** One-line change:

```go
// Before (broken)
Name: awssdk.String("tag:sandbox-id"),

// After (correct)
Name: awssdk.String("tag:km:sandbox-id"),
```

**Confidence:** HIGH — the tag key `km:sandbox-id` is confirmed in the Terraform module source. The AWS EC2 filter format `tag:<key>` is well-established.

### Recommended Structure for Template Changes

The `budgetEnforcerHCLTemplate` in `pkg/compiler/budget_enforcer_hcl.go` is a Go `text/template` const string. The dependency block must be inserted after the `include "root"` block and before the `remote_state` block (or after — order does not matter in HCL, but before is conventional):

```
locals { ... }
include "root" { ... }
dependency "sandbox" { ... }   ← INSERT HERE
remote_state { ... }
terraform { ... }
inputs = merge( ... )          ← ADD instance_id, role_arn here
```

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Reading module outputs into another module | A custom SSM/S3 state-sharing mechanism | Terragrunt `dependency` block | Terragrunt reads Terraform state directly — no extra AWS calls, strongly typed, automatic plan ordering |
| Instance ID lookup at Lambda runtime | A `DescribeInstances` call inside the Lambda | Pass `instance_id` in the EventBridge payload at creation time | The Lambda's one-minute invocation budget is tight; runtime lookups add latency and IAM complexity |

---

## Common Pitfalls

### Pitfall 1: `mock_outputs` Required for `dependency` Block on First Apply

**What goes wrong:** When `budget-enforcer/terragrunt.hcl` is applied before (or in the same run as) the parent sandbox module, Terragrunt cannot read outputs from a not-yet-applied module and errors out.

**Why it happens:** Terragrunt evaluates `dependency` outputs before planning. On the very first apply (or when running `terragrunt apply --terragrunt-non-interactive`), the parent state may not exist.

**How to avoid:** Always include `mock_outputs` in the dependency block. The `km create` workflow applies the sandbox module first, then applies `budget-enforcer/` — so in practice the parent state exists. But `mock_outputs` is required by Terragrunt even when `--terragrunt-non-interactive` is not used, as a matter of Terragrunt convention.

**Warning signs:** `Error: Call to function "values" failed` or `dependency outputs are not available` during `terragrunt apply` of the budget-enforcer module.

### Pitfall 2: `try()` vs Direct Map Access for `ec2spot_instances`

**What goes wrong:** Using `dependency.sandbox.outputs.ec2spot_instances["use1-0-0"].instance_id` hardcodes the map key, which is computed from region label and indices. On ECS sandboxes, the map is empty and direct access panics Terraform.

**How to avoid:** Use `try(values(dependency.sandbox.outputs.ec2spot_instances)[0].instance_id, "")`. The `try()` wrapper returns `""` for ECS (empty map) or pre-apply (mock_outputs `= {}`). The Lambda already handles `instance_id = ""` gracefully by logging and skipping.

### Pitfall 3: Tag Filter Typo Regression

**What goes wrong:** The existing `TestBudgetAdd_ResumesStoppedEC2` test passes today because `fakeEC2StartAPI.DescribeInstances` ignores the filter and always returns the fake instance. The test does not catch the wrong tag key.

**How to avoid:** Add a new test `TestBudgetAdd_ResumeEC2_TagFilter` (or update the existing one) that inspects the `DescribeInstancesInput.Filters` slice and asserts the `Name` field is `"tag:km:sandbox-id"`. This is a source-level verification pattern used by Phase 7 and Phase 12 tests in this codebase.

### Pitfall 4: ec2spot `iam_role_arn` Output Missing from Existing Sandboxes

**What goes wrong:** Live sandbox Terraform state (e.g., `infra/live/use1/sandboxes/sb-c5b51762/`) was applied without the new `iam_role_arn` output. On next apply, Terraform adds the output to state — this is additive and non-destructive. No existing resources change.

**How to avoid:** No action needed. Adding a Terraform output is always non-destructive. The next `terragrunt apply` of the sandbox module will populate the output in state, after which the budget-enforcer dependency can read it.

### Pitfall 5: Template String Tests Must Check Dependency Block

**What goes wrong:** The existing `TestGenerateBudgetEnforcerHCL_EC2` checks for the presence of strings like `"budget_enforcer_inputs"` and `"read_terragrunt_config"`. It does not check for `dependency`. After the fix, the new dependency block must be verified by tests.

**How to avoid:** Add assertions: `dependency "sandbox"`, `mock_outputs`, `instance_id`, `role_arn` to `budget_enforcer_hcl_test.go`.

---

## Code Examples

### Gap 1: Dependency Block in budget_enforcer_hcl.go Template

Source: `pkg/compiler/budget_enforcer_hcl.go` — current template, with insertion point shown:

```hcl
include "root" {
  path = find_in_parent_folders("root.hcl")
}

dependency "sandbox" {
  config_path = "${get_terragrunt_dir()}/.."

  mock_outputs = {
    ec2spot_instances         = {}
    iam_instance_profile_name = ""
    iam_role_arn              = ""
  }
  mock_outputs_allowed_on_destroy = true
}

remote_state { ... }

terraform { ... }

inputs = merge(
  local.be_inputs,
  {
    lambda_zip_path   = "${local.repo_root}/build/budget-enforcer.zip"
    budget_table_name = "${local.site_vars.locals.site.label}-budgets"
    budget_table_arn  = local.budget_table_arn
    state_bucket      = get_env("KM_ARTIFACTS_BUCKET", "")
    email_domain      = "sandboxes.${local.site_vars.locals.site.domain}"
    operator_email    = get_env("KM_OPERATOR_EMAIL", "")
    instance_id       = try(values(dependency.sandbox.outputs.ec2spot_instances)[0].instance_id, "")
    role_arn          = dependency.sandbox.outputs.iam_role_arn
  }
)
```

### Gap 1: New Terraform Output in ec2spot outputs.tf

Source: `infra/modules/ec2spot/v1.0.0/outputs.tf` — append after existing outputs:

```hcl
output "iam_role_arn" {
  description = "ARN of the IAM role attached to sandbox EC2 instances"
  value       = try(aws_iam_role.ec2spot_ssm[0].arn, "")
}
```

### Gap 2: Tag Key Fix in budget.go

Source: `internal/app/cmd/budget.go:226`:

```go
// Fixed: use the canonical tag key applied by ec2spot module
Filters: []ec2types.Filter{
    {
        Name:   awssdk.String("tag:km:sandbox-id"),
        Values: []string{sandboxID},
    },
},
```

### New Test: Compiler Emits Dependency Block

Source: `pkg/compiler/budget_enforcer_hcl_test.go` — add to existing test checks slice:

```go
// Verify dependency block wires ec2spot outputs into budget-enforcer inputs
checks := []string{
    ...existing checks...,
    `dependency "sandbox"`,       // dependency block present
    `mock_outputs`,               // graceful degradation on first apply
    `instance_id`,                // EC2 instance ID wired from dependency
    `role_arn`,                   // IAM role ARN wired from dependency
    `dependency.sandbox.outputs`, // reference to dependency outputs
}
```

### New Test: Tag Key Assertion

Source: `internal/app/cmd/budget_test.go` — new test verifying filter key:

```go
func TestResumeEC2Sandbox_UsesCorrectTagKey(t *testing.T) {
    // Capture the DescribeInstancesInput to assert filter key
    var capturedInput *ec2.DescribeInstancesInput
    fakeEC2 := &capturingEC2Client{
        captureInput: &capturedInput,
        instanceState: ec2types.InstanceStateNameStopped,
    }
    // call resumeEC2Sandbox (exported or tested via budget add)
    // assert capturedInput.Filters[0].Name == "tag:km:sandbox-id"
}
```

---

## State of the Art

| Old (Broken) Approach | Correct Approach | Impact |
|-----------------------|-----------------|--------|
| `role_arn = ""` hardcoded in service.hcl budget_enforcer_inputs | Wired via Terragrunt `dependency` block from ec2spot output | Lambda receives real role ARN; IAM revocation works |
| `instance_id = ""` hardcoded in service.hcl budget_enforcer_inputs | Wired via `try(values(dependency.sandbox.outputs.ec2spot_instances)[0].instance_id, "")` | Lambda receives real instance ID; StopInstances works |
| `tag:sandbox-id` filter in DescribeInstances | `tag:km:sandbox-id` filter | Stopped instances found; StartInstances called on resume |

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` (stdlib) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./pkg/compiler/... ./internal/app/cmd/... -run TestBudget -v` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| BUDG-07 | Compiler emits dependency block with `instance_id` and `role_arn` | unit | `go test ./pkg/compiler/... -run TestGenerateBudgetEnforcerHCL -v` | ✅ (update existing test) |
| BUDG-07 | ec2spot module exposes `iam_role_arn` output | source-level | checked in compiler test or dedicated tf-lint | ❌ Wave 0: add check |
| BUDG-08 | `resumeEC2Sandbox` uses `tag:km:sandbox-id` filter | unit | `go test ./internal/app/cmd/... -run TestBudget -v` | ❌ Wave 0: add test |

### Sampling Rate

- **Per task commit:** `go test ./pkg/compiler/... ./internal/app/cmd/... -run TestBudget -v`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] Add `dependency "sandbox"` assertions to `pkg/compiler/budget_enforcer_hcl_test.go`
- [ ] Add `TestResumeEC2Sandbox_UsesCorrectTagKey` to `internal/app/cmd/budget_test.go`
- [ ] Add `iam_role_arn` output to `infra/modules/ec2spot/v1.0.0/outputs.tf` (Terraform change, not a Go test, but needed before compiler test can assert the HCL reference is sound)

---

## Open Questions

1. **ECS `role_arn` wiring**
   - What we know: The ECS substrate has the same `role_arn = ""` problem in `service.hcl`. The budget-enforcer template is shared between EC2 and ECS.
   - What's unclear: The ECS module output name for the task role ARN. Checking `infra/modules/` ECS outputs is needed — likely `task_role_arn` or similar.
   - Recommendation: Scope Phase 19 to EC2 only for the dependency wiring (matches the requirement language: "EC2 hard stop"). The ECS path has `task_arn = ""` too but the audit gap description focuses on EC2 and IAM. Check ECS module outputs and address if outputs exist; otherwise flag as tech debt for a future phase.

2. **`created_at` in EventBridge payload**
   - What we know: `created_at = ""` is also hardcoded in `service.hcl`'s `budget_enforcer_inputs`. The Lambda needs this to calculate elapsed cost.
   - What's unclear: Whether the audit identified this as a gap or whether it is being set somewhere else (e.g., via Terraform `timestamp()` in the budget-enforcer module inputs).
   - Recommendation: Check `infra/modules/budget-enforcer/v1.0.0/main.tf` — the EventBridge payload is `jsonencode({ created_at = var.created_at ... })`. If `created_at` is also always empty, `calculateComputeCost` returns 0 and the Lambda silently uses 0 cost. The audit only calls out `instance_id` and `role_arn` — treat `created_at` as out-of-scope unless the planner finds it in the same root cause chain.

---

## Sources

### Primary (HIGH confidence)

- `pkg/compiler/budget_enforcer_hcl.go` — template source, confirmed `role_arn = ""` and `instance_id = ""` are hardcoded
- `pkg/compiler/service_hcl.go` — EC2 and ECS templates, confirmed `budget_enforcer_inputs` block with empty strings
- `cmd/budget-enforcer/main.go` — Lambda handler, confirmed `instance_id == ""` guard at line 343
- `internal/app/cmd/budget.go:226` — confirmed `tag:sandbox-id` filter key
- `infra/modules/ec2spot/v1.0.0/main.tf:344` — confirmed `km:sandbox-id` tag key applied to instances
- `infra/modules/ec2spot/v1.0.0/outputs.tf` — confirmed no `iam_role_arn` output exists
- `.planning/v1.0-MILESTONE-AUDIT.md` — root cause statements for both gaps
- `go test ./pkg/compiler/... ./internal/app/cmd/... -run TestBudget` — all existing tests pass (verified 2026-03-24)

### Secondary (MEDIUM confidence)

- Terragrunt `dependency` block pattern — consistent with how `github-token/terragrunt.hcl` consumes sibling config in this codebase; standard pattern documented at terragrunt.io

---

## Metadata

**Confidence breakdown:**
- Root causes: HIGH — confirmed by reading the actual source files and the audit
- Standard stack: HIGH — no new libraries, pure in-codebase changes
- Architecture: HIGH — Terragrunt dependency block is established pattern; `try()` guard mirrors existing output patterns in ec2spot
- Pitfalls: HIGH — verified by reading existing test structure and Terraform/Terragrunt semantics

**Research date:** 2026-03-24
**Valid until:** Stable — no external dependencies; internal code changes only
