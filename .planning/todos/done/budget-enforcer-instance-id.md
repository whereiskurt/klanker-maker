---
title: Budget enforcer missing instance_id for compute stop
area: budget
created: 2026-03-26
---

### Problem
The budget-enforcer module's instance_id variable defaults to empty string. Without it, compute budget exhaustion can't trigger EC2 StopInstances. Only AI budget enforcement (proxy 403 + IAM revocation) works. The dependency block that wired instance_id was removed for Terragrunt 0.99 compatibility.

### Solution
Construct instance_id from terraform output or tag lookup at apply time, similar to how sandbox_iam_role_arn is constructed from the sandbox_id + region pattern. Or have km create pass it as an input after the main apply completes.

### Files
- pkg/compiler/budget_enforcer_hcl.go
- infra/modules/budget-enforcer/v1.0.0/variables.tf
