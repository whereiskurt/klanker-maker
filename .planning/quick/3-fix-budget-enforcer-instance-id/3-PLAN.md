---
phase: quick
plan: 3
type: execute
wave: 1
depends_on: []
files_modified:
  - pkg/compiler/budget_enforcer_hcl.go
  - pkg/compiler/budget_enforcer_hcl_test.go
  - pkg/compiler/service_hcl.go
  - pkg/compiler/service_hcl_test.go
autonomous: true
requirements: [budget-enforcer-instance-id]

must_haves:
  truths:
    - "Budget enforcer terragrunt.hcl declares a dependency on the parent sandbox module"
    - "instance_id is wired from ec2spot module output via dependency block, not hardcoded empty"
    - "role_arn is wired from ec2spot module output via dependency block, not statically constructed"
    - "All existing compiler tests pass"
  artifacts:
    - path: "pkg/compiler/budget_enforcer_hcl.go"
      provides: "HCL template with dependency block and output-based wiring"
      contains: "dependency"
    - path: "pkg/compiler/budget_enforcer_hcl_test.go"
      provides: "Tests verifying dependency block and instance_id wiring"
      contains: "dependency"
  key_links:
    - from: "budget_enforcer_hcl.go template"
      to: "ec2spot module outputs"
      via: "dependency.sandbox.outputs"
      pattern: "dependency\\.sandbox\\.outputs"
---

<objective>
Fix budget-enforcer instance_id wiring so compute budget exhaustion can trigger EC2 StopInstances.

Purpose: The budget enforcer's instance_id defaults to empty string because the dependency block
that wired it from ec2spot module output was removed for Terragrunt 0.99 compatibility. Without
instance_id, the Lambda cannot stop EC2 instances when compute budget is exhausted.

Output: Updated HCL template with dependency block, wiring instance_id and role_arn from the
parent sandbox module's terraform outputs instead of static/empty values.
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@pkg/compiler/budget_enforcer_hcl.go
@pkg/compiler/budget_enforcer_hcl_test.go
@pkg/compiler/service_hcl.go
@infra/modules/ec2spot/v1.0.0/outputs.tf
@infra/modules/budget-enforcer/v1.0.0/variables.tf
@infra/modules/budget-enforcer/v1.0.0/main.tf

<interfaces>
<!-- Reference implementation: manually patched sandbox sb-e38fb038 -->
<!-- This is the exact pattern to replicate in the Go template -->

From infra/live/use1/sandboxes/sb-e38fb038/budget-enforcer/terragrunt.hcl (lines 22-31):
```hcl
dependency "sandbox" {
  config_path = "${get_terragrunt_dir()}/.."

  mock_outputs = {
    ec2spot_instances         = {}
    iam_instance_profile_name = ""
    iam_role_arn              = ""
  }
  mock_outputs_allowed_on_destroy = true
}
```

And the inputs wiring (lines 67-69):
```hcl
    # Wired from ec2spot module outputs via dependency block
    instance_id = try(values(dependency.sandbox.outputs.ec2spot_instances)[0].instance_id, "")
    role_arn    = dependency.sandbox.outputs.iam_role_arn
```

From infra/modules/ec2spot/v1.0.0/outputs.tf:
```hcl
output "ec2spot_instances" {
  value = { for k, v in aws_spot_instance_request.ec2spot : k => { instance_id = v.spot_instance_id, ... } }
}
output "iam_role_arn" {
  value = try(aws_iam_role.ec2spot_ssm[0].arn, "")
}
```
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Add dependency block and output-based wiring to budget enforcer HCL template</name>
  <files>pkg/compiler/budget_enforcer_hcl.go, pkg/compiler/budget_enforcer_hcl_test.go</files>
  <behavior>
    - Test: Generated HCL contains `dependency "sandbox"` block
    - Test: Generated HCL contains `config_path = "${get_terragrunt_dir()}/.."` in the dependency
    - Test: Generated HCL contains `mock_outputs` with ec2spot_instances, iam_instance_profile_name, iam_role_arn
    - Test: Generated HCL contains `mock_outputs_allowed_on_destroy = true`
    - Test: Generated HCL contains `instance_id = try(values(dependency.sandbox.outputs.ec2spot_instances)[0].instance_id, "")`
    - Test: Generated HCL contains `role_arn    = dependency.sandbox.outputs.iam_role_arn`
    - Test: Generated HCL does NOT contain the old static role_arn construction pattern `km-ec2spot-ssm-`
  </behavior>
  <action>
    1. In budget_enforcer_hcl.go, add a `dependency "sandbox"` block to the template between the
       `include "root"` block and the `remote_state` block. Use exactly this pattern (matching the
       working reference in sb-e38fb038):

       ```
       dependency "sandbox" {
         config_path = "${get_terragrunt_dir()}/.."

         mock_outputs = {
           ec2spot_instances         = {}
           iam_instance_profile_name = ""
           iam_role_arn              = ""
         }
         mock_outputs_allowed_on_destroy = true
       }
       ```

    2. In the `inputs = merge(...)` block, REPLACE the static role_arn construction line:
       ```
       role_arn = "arn:aws:iam::${local.site_vars.locals.accounts.application}:role/km-ec2spot-ssm-${local.be_inputs.sandbox_id}-${local.site_vars.locals.region.label}"
       ```
       with output-based wiring:
       ```
       # Wired from ec2spot module outputs via dependency block
       instance_id = try(values(dependency.sandbox.outputs.ec2spot_instances)[0].instance_id, "")
       role_arn    = dependency.sandbox.outputs.iam_role_arn
       ```

    3. Update the template comment at the top to mention the dependency block approach.

    4. In budget_enforcer_hcl_test.go, update TestGenerateBudgetEnforcerHCL_EC2:
       - Add checks for: `dependency "sandbox"`, `mock_outputs`, `dependency.sandbox.outputs.ec2spot_instances`,
         `dependency.sandbox.outputs.iam_role_arn`, `mock_outputs_allowed_on_destroy`
       - REMOVE the `km-ec2spot-ssm` check (static ARN construction is gone)
       - Keep all other existing checks

    5. Update TestGenerateBudgetEnforcerHCL_ECS similarly: keep existing checks, remove
       static role_arn check if present, add dependency block checks.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go test ./pkg/compiler/ -run TestGenerateBudgetEnforcerHCL -v</automated>
  </verify>
  <done>Budget enforcer HCL template includes dependency block wiring instance_id and role_arn from ec2spot outputs. All budget enforcer HCL tests pass including new dependency block assertions.</done>
</task>

<task type="auto">
  <name>Task 2: Clean up stale instance_id and role_arn placeholders from service.hcl template</name>
  <files>pkg/compiler/service_hcl.go, pkg/compiler/service_hcl_test.go</files>
  <action>
    1. In service_hcl.go, in the ec2ServiceHCLTemplate's `budget_enforcer_inputs` block (around line 75-76),
       REMOVE these two lines:
       ```
       role_arn       = "" # populated at apply time: IAM role ARN from ec2spot module output
       instance_id    = "" # populated at apply time: EC2 instance ID from ec2spot module output
       ```
       These are now wired via the dependency block in budget_enforcer_hcl.go and are misleading
       as empty placeholders. The `merge()` in the budget enforcer HCL will override them anyway,
       but removing avoids confusion.

    2. Check the ecsServiceHCLTemplate for similar placeholders (task_arn, cluster_arn) and leave
       them as-is for now since ECS substrate is not the focus of this fix.

    3. If any service_hcl_test.go tests assert the presence of `instance_id` or `role_arn` in
       the budget_enforcer_inputs block, update them to no longer expect those lines.

    4. Run the full compiler test suite to ensure nothing breaks.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go test ./pkg/compiler/ -v -count=1</automated>
  </verify>
  <done>service.hcl template no longer includes misleading empty instance_id/role_arn in budget_enforcer_inputs. Full compiler test suite passes.</done>
</task>

</tasks>

<verification>
Run full compiler test suite:
```bash
cd /Users/khundeck/working/klankrmkr && go test ./pkg/compiler/ -v -count=1
```

Verify generated HCL matches the working reference pattern:
```bash
cd /Users/khundeck/working/klankrmkr && go test ./pkg/compiler/ -run TestGenerateBudgetEnforcerHCL -v 2>&1 | grep -E "PASS|FAIL"
```
</verification>

<success_criteria>
- Budget enforcer HCL template generates a `dependency "sandbox"` block
- instance_id is wired via `try(values(dependency.sandbox.outputs.ec2spot_instances)[0].instance_id, "")`
- role_arn is wired via `dependency.sandbox.outputs.iam_role_arn`
- No static ARN construction remains in the budget enforcer template
- service.hcl no longer has misleading empty placeholders for instance_id/role_arn
- All compiler tests pass: `go test ./pkg/compiler/ -v -count=1`
</success_criteria>

<output>
After completion, create `.planning/quick/3-fix-budget-enforcer-instance-id/3-SUMMARY.md`
</output>
