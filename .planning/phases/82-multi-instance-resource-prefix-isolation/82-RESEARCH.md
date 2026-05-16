# Phase 82: Multi-instance resource_prefix isolation — Research

**Researched:** 2026-05-16
**Domain:** Go CLI hardening + Terraform module parameterization + AWS resource tagging
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **Wave structure:** Three waves — Wave 1 (Go-only, `make build`), Wave 2 (Terraform module additions, `km init --dry-run=true` review), Wave 3 (apply, `km init --dry-run=false`).
- **Tag key:** `km:resource-prefix = ${prefix}` as install discriminator; complementary to `km:role-class` from 2026-05-09 spec.
- **Missing-tag policy:** WARN in doctor (not ERROR, not delete); remediation pointer to `km doctor --backfill-tags`.
- **SES module:** Add `variable "resource_prefix"` to `infra/modules/ses/v1.0.0/variables.tf`; `rule_set_name = "${var.resource_prefix}-sandbox-email"`. Pre-create + flip strategy if `moved {}` doesn't help (open Q1 — resolved below).
- **Email-handler:** Add `variable "state_prefix"` (default `"tf-km"`); replace `tf-km/` literal with `${var.state_prefix}/`; caller passes `state_prefix = "tf-${var.resource_prefix}"`.
- **ECS modules:** Add `variable "resource_prefix"` (or reuse existing `km_label`) to `ecs-task`, `ecs`, `ecs-cluster`; replace `parameter/km/*` literal with `parameter/${var.resource_prefix}/*`.
- **Configure-flow protection:** Preserve `resource_prefix` from existing config on re-run; require `--reset-prefix` flag to change it. Fresh `km configure` still defaults to `"km"`.
- **Fallback hardening:** `cmd/configui/main.go:225` → log + `os.Exit(1)`. `cmd/km-slack-bridge/main.go:175` → log + `os.Exit(1)`. `pkg/compiler/userdata.go:3315,3331,3346` → replace literals with `cfg.GetSlackThreadsTableName()` / `cfg.GetSlackStreamMessagesTableName()`.
- **Rollout pattern:** `make build && km init --sidecars && km init --dry-run=false`. Matches Phase 63/67/68/73/79/80.
- **Existing sandboxes:** Do NOT get retroactive `km:resource-prefix` tag. Use `km doctor --backfill-tags` for infra-level; instance tags ride on next `km destroy && km create`.
- **Docs updates:** `CLAUDE.md` § Multi-instance support, `OPERATOR-GUIDE.md`, spec status line.
- **2026-05-09 platform-discrimination spec status:** Still Proposal — DO NOT implement. Phase 82 ships `--backfill-tags` standalone; future phase extends it with `km:role-class`.

### Claude's Discretion

- Exact PLAN.md decomposition (Wave 1 may be one plan or split per concern).
- `--backfill-tags` UX shape (spec recommends `km doctor --backfill-tags`).
- Test scaffolding beyond the spec-named configure re-run test.
- Whether to include Q3 (`multi_install_collision` doctor check), Q4 (cluster-irsa role naming), Q5 (state-bucket verification).

### Deferred Ideas (OUT OF SCOPE)

- Region-level isolation (already works).
- Account-level isolation (already works).
- VPC sharing between installs.
- Slack app-level isolation.
- Cross-install migration tooling.
- Q3: `multi_install_collision` doctor check.
- Q5: State-bucket layout verification (confirm only, not a code change).
</user_constraints>

---

## Summary

Phase 82 closes the 15% gap in `resource_prefix` isolation that prevents a safe second `km init` in the same AWS account. The work is primarily mechanical: six targeted literal-to-variable substitutions across five Terraform modules, two hard-fail conversions in sidecar binaries, three helper-call replacements in the userdata template compiler, one configure-flow guard, and a new `km doctor --backfill-tags` command plus doctor tag-filter additions. No new AWS services are introduced; no schema changes are needed.

The single technically uncertain item is the SES rule-set rename. Research confirms that Terraform `moved {}` blocks do NOT prevent destroy+create when the resource's API identity (its name) changes — `moved {}` only updates Terraform's state address. The SES module rename will produce a ~10s inbound-mail gap. The correct mitigation is scheduling the Wave 3 apply during a low-traffic window. A two-step pre-create + flip using `create_before_destroy` is possible but requires a separate `aws_ses_receipt_rule_set` resource block for the new name alongside the old, which complicates the module; the single-step rename with a brief gap is recommended.

The 2026-05-09 platform-discrimination spec (`km:role-class`) remains at Proposal status. Phase 82 ships its own `--backfill-tags` standalone; the future phase will extend the command to also apply `km:role-class`. The Phase 80 cluster-irsa module already uses `${var.resource_prefix}-cluster-${var.cluster_name}` — Q4 is already resolved, nothing to fold in.

**Primary recommendation:** Ship Wave 1 immediately (pure Go, zero infra risk), then review Wave 2 plan output before committing Wave 3.

---

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `aws-sdk-go-v2/service/ec2` | existing in go.mod | EC2 CreateTags, DescribeImages with tag filters | Already in use for BakeAMI / ListBakedAMIs |
| `aws-sdk-go-v2/service/resourcegroupstaggingapi` | may need adding | `tag:GetResources` + `tag:TagResources` for backfill sweep | Official AWS tagging API — covers cross-service resources in one call |
| `github.com/spf13/cobra` | existing | `km doctor --backfill-tags` flag addition | Project-standard CLI framework |
| `gopkg.in/yaml.v3` | existing | Reading existing `km-config.yaml` in configure preserve-on-re-run | Already used in configure.go / configure_test.go |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `aws-sdk-go-v2/service/iam` | existing | TagRole for backfill of IAM roles | When backfill covers IAM resources |
| `aws-sdk-go-v2/service/dynamodb` | existing | TagResource for DDB tables | Backfill only |
| `aws-sdk-go-v2/service/lambda` | existing | TagResource for Lambda functions | Backfill only |
| `aws-sdk-go-v2/service/sqs` | existing | TagQueue for SQS queues | Backfill for Slack-inbound queues |

**Installation:** No new dependencies expected. `resourcegroupstaggingapi` may already be in go.mod (check before adding). All other services are already imported.

---

## Architecture Patterns

### Q1 — SES Rule-Set Rename: `moved {}` Does NOT Apply

**Finding (HIGH confidence, verified via AWS provider docs + Terraform semantics):**

Terraform `moved {}` blocks update the *state address* of a resource — the HCL reference name (e.g., `aws_ses_receipt_rule_set.km_sandbox`). They do NOT change the underlying AWS resource's identity. For `aws_ses_receipt_rule_set`, the Terraform import ID IS the rule_set_name string (confirmed by official provider docs). When `rule_set_name` changes from `"km-sandbox-email"` to `"km-sandbox-email"` ... that's unchanged for the existing `km` install (prefix `"km"` produces the same name), but for a future second install with prefix `"rg"` the new module produces `"rg-sandbox-email"`.

**Implication for the existing `km` install:**

When the existing `km` operator runs Wave 3 `km init --dry-run=false`, Terraform will evaluate `"${var.resource_prefix}-sandbox-email"` = `"km-sandbox-email"` — IDENTICAL to the current literal. There is **no destroy+create** for the existing install. The rename only matters when a second install with `resource_prefix = "rg"` runs `km init`.

**Conclusion:** The inbound-mail gap concern is only relevant for the second install's initial provisioning (creating `"rg-sandbox-email"` from scratch), not for the existing `km` install's upgrade. Zero downtime for Wave 3 on the current install.

A `moved {}` block is still harmless and useful: add one from `aws_ses_receipt_rule_set.km_sandbox` to the same address to make the plan output explicit (though in this case from == to, so omit it). No `create_before_destroy` lifecycle logic needed.

### Q4 — Phase 80 Cluster-IRSA: Already Resolved

**Finding (HIGH confidence, code inspection):**

`infra/modules/cluster-irsa/v1.0.0/main.tf:78` already reads:
```hcl
name = "${var.resource_prefix}-cluster-${var.cluster_name}"
```

Q4 is closed. Nothing to fold into Phase 82.

### Q5 — State-Bucket Layout: Already Correct

`infra/live/site.hcl:5` reads:
```hcl
tf_state_prefix = "tf-${get_env("KM_RESOURCE_PREFIX", "km")}"
```
`infra/live/site.hcl:43`:
```hcl
bucket = "${local.site.tf_state_prefix}-state-${local.region.label}"
```

New installs automatically use `tf-${prefix}-state-${region}`. No code change needed. Operator task: confirm by echoing `KM_RESOURCE_PREFIX=rg terragrunt plan` produces the right bucket name in Wave 2 review.

### ECS Modules Already Have `km_label`

All three ECS modules (`ecs-task`, `ecs`, `ecs-cluster`) already define `variable "km_label"` and use it throughout. The SSM ARN literal at `ecs-task/v1.0.0/main.tf:156`, `ecs/v1.0.0/main.tf:126`, `ecs-cluster/v1.0.0/main.tf:132` hardcode `parameter/km/*` instead of `parameter/${var.km_label}/*`. The fix is to replace the literal with the already-available variable — no new variable needed.

### Email-Handler Already Has `resource_prefix`

`infra/modules/email-handler/v1.0.0/variables.tf` already declares `variable "resource_prefix"`. The S3 IAM policy at `main.tf:75` hardcodes `tf-km/` but the module already has `var.resource_prefix` in scope. The simplest fix: add `variable "state_prefix"` with `default = "tf-km"` and replace the literal. The live terragrunt passes `state_prefix = "tf-${var.resource_prefix}"`. Backward-compat: `default = "tf-km"` means existing callers that don't pass the variable continue working.

### Rollout Binary Classification

| Binary / Change | Deploy Method | Why |
|---|---|---|
| `cmd/km` (configure.go, doctor.go, doctor_backfill_tags.go, ec2_ami.go) | `make build` only | CLI binary — no Lambda or sidecar dependency |
| `cmd/configui` (main.go) | `km init --sidecars` | Sidecar Lambda uploaded to S3 by the sidecars target |
| `cmd/km-slack-bridge` (main.go) | `km init --lambdas` or `km init` | Bridge Lambda — uploaded via Terraform apply |
| `pkg/compiler/userdata.go` | `make build` + `km init --sidecars` | Userdata template changes ride in the management Lambda's km binary |
| Terraform modules | `km init --dry-run=false` | Standard Wave 2+3 flow |

**Clarification on `km init --sidecars` vs `km init`:**
- `km init --sidecars`: uploads sidecar binaries to S3 (km-presence, km-slack, etc.) and refreshes the management Lambda's km binary (which contains the userdata templates). Use after changes to `pkg/compiler/userdata.go` or any `cmd/km-*` sidecar.
- `km init` (full): runs Terraform apply, which deploys Lambda zips and Terraform module changes. Required for Wave 3.
- `cmd/configui` and `cmd/km-slack-bridge` changes need `km init --sidecars` (configui is a sidecar Lambda) and `km init` (bridge Lambda via Terraform), respectively.

### `km doctor --backfill-tags` Architecture

New file: `internal/app/cmd/doctor_backfill_tags.go`

Anatomy:
1. Accept `--dry-run` flag (default true — safe by default, matches km init pattern).
2. Call `tag:GetResources` (AWS Resource Groups Tagging API) filtered by `tag:km:sandbox-id=*` to find all install-owned sandbox resources lacking `km:resource-prefix`.
3. For each resource, call the service-specific tagging API (`ec2:CreateTags`, `iam:TagRole`, `dynamodb:TagResource`, `lambda:TagResource`, `sqs:TagQueue`, `kms:TagResource`, `ssm:AddTagsToResource`) with `km:resource-prefix = cfg.GetResourcePrefix()`.
4. Report count of resources tagged / skipped / errored.
5. On `--dry-run=true`, print what would be tagged without calling tagging APIs.

IAM permissions required (operator profile already has broad policies via `km-operator-policy`; these are all covered):
- `tag:GetResources`
- `tag:TagResources`
- `ec2:CreateTags`
- `iam:TagRole`
- `dynamodb:TagResource`
- `lambda:TagResource`
- `kms:TagResource`
- `sqs:TagQueue`
- `ssm:AddTagsToResource`

### Terraform Tag Additions: In-Place Updates Only

**Finding (HIGH confidence — AWS provider behavior):**

Adding `tags = { "km:resource-prefix" = var.resource_prefix }` to an existing deployed resource causes an in-place update (`~` in plan output), never a destroy+create. This is true for: `aws_iam_role`, `aws_dynamodb_table`, `aws_lambda_function`, `aws_security_group`, `aws_instance`, `aws_kms_key`, `aws_sqs_queue`, `aws_vpc`, `aws_subnet`, `aws_internet_gateway`. No resources are recreated; no downtime.

Exception: KMS aliases do not support tags directly. Tag the KMS **key** resource instead (the key supports tags). Doctor checks should follow `aws kms list-resource-tags` on the key ID, not the alias.

### Configure-Flow Preserve-on-Re-Run: Implementation Pattern

**File:** `internal/app/cmd/configure.go`

**Current behavior (the footgun):**
- Lines 139, 177, 184: `resourcePrefix = "km"` unconditionally when unset or empty-input.

**Required behavior:**
1. Load existing `km-config.yaml` from `outputDir` (or `./km-config.yaml`) before prompting.
2. If existing file found AND `existing.resource_prefix != ""`, use that as the pre-filled default for the prompt instead of `"km"`.
3. A blank Enter at the prompt preserves the existing value (because the existing value IS the prompt default).
4. Add `--reset-prefix` flag (bool, default false). When set, revert to `"km"` default.

**Test scaffolding needed:**
```go
// TestConfigureRerunPreservesResourcePrefix:
// 1. Write km-config.yaml with resource_prefix: rg to tempdir
// 2. Run km configure --non-interactive --output-dir tempdir (no --resource-prefix flag)
// 3. Assert km-config.yaml still has resource_prefix: rg
```

The configure_test.go pattern (binary-based integration tests, `buildKM()` helper, `runKMArgsInDir()`) is the right approach — no unit-test mocking needed since configure writes a file.

### ec2_ami.go Tag Addition Pattern

**Current:** `KMBakeTags()` in `pkg/aws/ec2_ami.go` returns tags for CreateImage. Add `"km:resource-prefix"` to the tag list alongside existing tags.

**Function signature change needed:** `KMBakeTags` needs the resource prefix as a parameter. Currently called from `pkg/aws/ec2_ami.go` bake path. The caller (`internal/app/cmd`) has access to `cfg.GetResourcePrefix()`.

**Existing test at `TestKMBakeTags_IncludesAllRequiredKeys`:** Add `"km:resource-prefix"` to the `required` slice after changing the function to include it.

**`ListBakedAMIs` tag filter addition:** Add a second filter `tag:km:resource-prefix = cfg.GetResourcePrefix()` alongside existing `tag:km:sandbox-id = *`. `ListBakedAMIs` currently takes no `cfg` parameter — add it or use a functional option. Check callers before changing signature.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Cross-service resource tagging | Custom per-service loops | `tag:GetResources` (AWS Resource Groups Tagging API) | Single API returns all tagged resources across EC2, IAM, Lambda, DDB, SQS — covers entire account in one paginated call |
| Tag update per resource | Service-by-service client calls for backfill sweep | `tag:TagResources` (batch up to 20 ARNs per call) | One API, multiple services; reduces API call count |
| Preserve YAML on re-run | Regex parsing of km-config.yaml | `os.ReadFile` + `yaml.Unmarshal` into `platformConfig` struct (already used in configure.go) | The struct already exists; just read before prompting |

---

## Common Pitfalls

### Pitfall 1: SES Rename Confusion for Existing Installs

**What goes wrong:** Planning team thinks Wave 3 will break the existing `km` install's SES because the rule_set_name literal changes.
**Why it happens:** The literal `"km-sandbox-email"` becomes `"${var.resource_prefix}-sandbox-email"`. With `resource_prefix = "km"`, this evaluates to `"km-sandbox-email"` — identical to today. Terraform sees no diff for the existing install.
**How to avoid:** Include this explanation in Wave 2 plan so the operator doesn't schedule unnecessary maintenance windows.
**Warning signs:** `km init --dry-run=true` output showing SES rule set as `must replace` — that would only happen if the new variable evaluation produced a different name.

### Pitfall 2: ECS `km_label` vs `resource_prefix` Naming

**What goes wrong:** Adding a new `variable "resource_prefix"` to ECS modules when `km_label` already exists and carries the same value.
**Why it happens:** The spec text says "add `variable "resource_prefix"`" but the ECS modules use `km_label` as their equivalent. Adding a redundant variable creates confusion.
**How to avoid:** Replace `parameter/km/*` with `parameter/${var.km_label}/*` — no new variable needed in the three ECS modules.

### Pitfall 3: `moved {}` Misunderstanding

**What goes wrong:** Adding a `moved {}` block expecting it to prevent destroy+create during an SES rule_set_name rename.
**Why it happens:** `moved {}` only updates Terraform state addresses, not API resource IDs.
**How to avoid:** Don't add `moved {}` for the SES rename. The existing install sees no rename (same evaluates to same); a new second install creates the new name from scratch.

### Pitfall 4: `backfill-tags` Tags Resources from Other Installs

**What goes wrong:** `--backfill-tags` uses `tag:GetResources(tag:km:sandbox-id=*)` and tags ALL resources account-wide with the current install's prefix.
**Why it happens:** If a second install already exists, its resources also have `km:sandbox-id` tags and would get the wrong prefix.
**How to avoid:** The backfill command should cross-reference the tagged resource's sandbox-id against the current install's DDB table. Only tag resources whose sandbox-id appears in this install's DDB. Document this constraint clearly.

### Pitfall 5: Configure `--output-dir` vs CWD Config Location

**What goes wrong:** Preserve-on-re-run logic reads from `./km-config.yaml` but the operator is running from a different directory than `--output-dir`.
**Why it happens:** The current `runConfigure` function writes to `outputDir` but reads initial values from the function parameters, not from the existing file.
**How to avoid:** Read from `filepath.Join(outputDir, "km-config.yaml")` first (when `outputDir` is set); fall back to `./km-config.yaml`. Match the write path.

### Pitfall 6: `configui` Hard-Fail is a Sidecar Lambda

**What goes wrong:** Adding `os.Exit(1)` to `cmd/configui/main.go:225` makes configui crash during local development or testing when `KM_BUDGET_TABLE` isn't set.
**Why it happens:** Configui runs as a Lambda sidecar in production (env always injected), but developers may run it locally without env vars.
**How to avoid:** Add a `--dev-mode` flag (or `KM_DEV_MODE` env var) that falls back to the `"km-budgets"` default. Or just log a warning and continue in local mode. The spec says "configui runs only as a sidecar Lambda" — but verify this before hard-failing.

---

## Code Examples

### Preserve-on-re-run in configure.go

```go
// Source: internal/app/cmd/configure.go pattern (new logic)
// Read existing config before prompting (preserve-on-re-run, Phase 82).
existingPrefix := ""
if cfgPath := filepath.Join(outputDir, "km-config.yaml"); outputDir != "" {
    if data, err := os.ReadFile(cfgPath); err == nil {
        var existing platformConfig
        if yaml.Unmarshal(data, &existing) == nil && existing.ResourcePrefix != "" {
            existingPrefix = existing.ResourcePrefix
        }
    }
}
// Use existingPrefix as the prompt default; "km" only for fresh installs.
defaultPrefix := "km"
if existingPrefix != "" {
    defaultPrefix = existingPrefix
}
if resourcePrefix == "" {
    resourcePrefix = defaultPrefix
}
```

### SES variables.tf addition

```hcl
# Source: infra/modules/ses/v1.0.0/variables.tf (new variable)
variable "resource_prefix" {
  type        = string
  description = "Install-level resource prefix (e.g. 'km', 'rg'). Ensures SES rule sets are namespaced per install."
  default     = "km"
}
```

### SES main.tf literal replacement

```hcl
# Source: infra/modules/ses/v1.0.0/main.tf:62 (before/after)
# Before:
resource "aws_ses_receipt_rule_set" "km_sandbox" {
  rule_set_name = "km-sandbox-email"
}
# After:
resource "aws_ses_receipt_rule_set" "km_sandbox" {
  rule_set_name = "${var.resource_prefix}-sandbox-email"
}
```

### ECS SSM ARN literal replacement (ecs-task as example)

```hcl
# Source: infra/modules/ecs-task/v1.0.0/main.tf:156 (before/after)
# Before:
Resource = "arn:aws:ssm:...:parameter/km/*"
# After:
Resource = "arn:aws:ssm:...:parameter/${var.km_label}/*"
```

### Fallback hard-fail (configui)

```go
// Source: cmd/configui/main.go:220-226 (modified)
func budgetTableName() string {
    v := os.Getenv("KM_BUDGET_TABLE")
    if v != "" {
        return v
    }
    // configui runs as a sidecar Lambda — missing env = configuration bug.
    slog.Error("KM_BUDGET_TABLE not set; cannot determine budget table name")
    os.Exit(1)
    return "" // unreachable
}
```

### userdata.go literal replacement

```go
// Source: pkg/compiler/userdata.go:3312-3317 (modified)
// Before:
if threadsTable == "" {
    threadsTable = "km-slack-threads"
}
// After (cfg is available in Compile, thread it through):
if threadsTable == "" {
    threadsTable = cfg.GetSlackThreadsTableName()
}
```

### KMBakeTags signature with resource prefix

```go
// Source: pkg/aws/ec2_ami.go — KMBakeTags (modified signature)
func KMBakeTags(sandboxID, profile, alias, instanceType, region, kmVersion, resourcePrefix string) []types.Tag {
    tags := []types.Tag{
        // ... existing tags ...
        {Key: awssdk.String("km:resource-prefix"), Value: awssdk.String(resourcePrefix)},
    }
    // ...
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `KMBakeTags` without install discriminator | Add `km:resource-prefix` tag at bake time | Phase 82 | Doctor can now filter AMIs by install |
| `ListBakedAMIs` scans all `km:sandbox-id=*` AMIs across installs | Add `tag:km:resource-prefix=${prefix}` filter | Phase 82 | Each install only sees its own AMIs |
| `checkOrphanedEC2` declares foreign-install instances as orphans | Filter by `km:resource-prefix` tag; WARN on untagged | Phase 82 | No cross-install false-positive orphan detection |
| `km configure` re-run silently resets prefix to `"km"` | Preserve existing prefix; `--reset-prefix` to change | Phase 82 | Safe for operators who re-run configure for other fields |
| SES rule set name is a literal `"km-sandbox-email"` | `"${var.resource_prefix}-sandbox-email"` | Phase 82 | Second install can have its own rule set |

---

## Open Questions

1. **configui hard-fail scope** — Does configui ever run locally (non-Lambda) during development? If yes, `os.Exit(1)` needs a `KM_DEV_MODE` escape hatch. If no (Lambda-only), hard-fail is safe. Recommend: check `Makefile` targets and any local-run scripts before landing the hard-fail.

2. **`backfill-tags` cross-install safety** — The command must not tag another install's resources with the wrong prefix. The safeguard is: only tag resources whose `km:sandbox-id` appears in this install's DDB `GetSandboxTableName()`. Planner should make this cross-reference explicit in the task.

3. **`ListBakedAMIs` signature change** — Currently `ListBakedAMIs(ctx, ec2client)` takes no config. Adding a prefix filter requires passing either `cfg` or a `string` prefix. Callers: `internal/app/cmd/doctor.go` (at least one call site). Planner should decide: functional option (backward compat) vs adding parameter (simpler, update all callers).

---

## Validation Architecture

> nyquist_validation is enabled (config.json: `"nyquist_validation": true`)

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib), integration tests via `buildKM()` binary runner |
| Config file | None (`go test ./...`) |
| Quick run command | `go test ./internal/app/cmd/... ./pkg/aws/...` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map

| Behavior | Test Type | Automated Command | File Exists? |
|----------|-----------|-------------------|-------------|
| `km configure` re-run preserves non-default `resource_prefix` | Integration | `go test ./internal/app/cmd/... -run TestConfigureRerunPreservesResourcePrefix` | ❌ Wave 0 |
| `km configure --reset-prefix` re-defaults to `"km"` | Integration | `go test ./internal/app/cmd/... -run TestConfigureResetPrefixFlag` | ❌ Wave 0 |
| `KMBakeTags` includes `km:resource-prefix` tag | Unit | `go test ./pkg/aws/... -run TestKMBakeTags_IncludesAllRequiredKeys` | ✅ (update existing) |
| `ListBakedAMIs` applies prefix filter when prefix provided | Unit | `go test ./pkg/aws/... -run TestListBakedAMIs_PrefixFilter` | ❌ Wave 0 |
| `checkOrphanedEC2` skips instances with different prefix tag | Unit | `go test ./internal/app/cmd/... -run TestCheckOrphanedEC2_SkipsForeignPrefix` | ❌ Wave 0 |
| `checkOrphanedEC2` warns on untagged instances | Unit | `go test ./internal/app/cmd/... -run TestCheckOrphanedEC2_WarnsUntagged` | ❌ Wave 0 |
| `userdata.go` uses `GetSlackThreadsTableName()` (not literal) | Unit | `go test ./pkg/compiler/... -run TestCompile_SlackInboundTableName` | ❌ Wave 0 |
| Wave 2 plan shows zero recreations for `km` install | Manual | `km init --dry-run=true` — review output | Manual only |
| Wave 3 SES rule set name evaluates correctly | Manual | `aws ses describe-active-receipt-rule-set` post-apply | Manual only |
| `km doctor --backfill-tags` is idempotent | Integration (optional) | Manual re-run + AWS console tag verification | Manual only |

### Sampling Rate

- **Per task commit:** `go test ./internal/app/cmd/... ./pkg/aws/... -count=1`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `internal/app/cmd/configure_test.go` — add `TestConfigureRerunPreservesResourcePrefix`, `TestConfigureResetPrefixFlag`
- [ ] `pkg/aws/ec2_ami_test.go` — update `TestKMBakeTags_IncludesAllRequiredKeys` to include `km:resource-prefix`; add `TestListBakedAMIs_PrefixFilter`
- [ ] `internal/app/cmd/doctor_test.go` — add `TestCheckOrphanedEC2_SkipsForeignPrefix`, `TestCheckOrphanedEC2_WarnsUntagged`
- [ ] `pkg/compiler/` — add `TestCompile_SlackInboundTableName` (or equivalent) if a compiler test file exists

---

## Sources

### Primary (HIGH confidence)

- Code inspection: `infra/modules/ses/v1.0.0/main.tf` — confirmed `rule_set_name = "km-sandbox-email"` literal at line 62
- Code inspection: `infra/modules/email-handler/v1.0.0/main.tf` — confirmed `tf-km/` literal at line 75
- Code inspection: `infra/modules/ecs-task/v1.0.0/main.tf` — confirmed `parameter/km/*` literal at line 156
- Code inspection: `infra/modules/ecs/v1.0.0/main.tf` — confirmed `parameter/km/*` literal at line 126
- Code inspection: `infra/modules/ecs-cluster/v1.0.0/main.tf` — confirmed `parameter/km/*` literal at line 132
- Code inspection: `infra/modules/cluster-irsa/v1.0.0/main.tf:78` — confirms `${var.resource_prefix}-cluster-${var.cluster_name}` (Q4 closed)
- Code inspection: `infra/live/site.hcl:5,43` — confirms `tf-${KM_RESOURCE_PREFIX}` templating (Q5 confirmed)
- Code inspection: `internal/app/config/config.go:387-563` — full catalog of prefix-aware helpers
- Code inspection: `internal/app/cmd/configure.go:139,177,184` — three footgun locations confirmed
- Code inspection: `cmd/configui/main.go:220-225` — fallback literal confirmed
- Code inspection: `cmd/km-slack-bridge/main.go:169-176` — fallback literal confirmed
- Code inspection: `pkg/compiler/userdata.go:3313-3346` — three literal sites confirmed
- Code inspection: `infra/modules/create-handler/v1.0.0/main.tf:182+` — confirms `moved {}` blocks are already used in this codebase
- AWS SES provider docs (via WebFetch): `aws_ses_receipt_rule_set` import ID = rule_set_name; `moved {}` doesn't prevent recreate on name change
- Code inspection: `internal/app/cmd/configure_test.go` — binary-runner integration test pattern (`buildKM()`, `runKMArgsInDir()`)
- Code inspection: `pkg/aws/ec2_ami_test.go` — mock-based unit test pattern for EC2 APIs
- Code inspection: `internal/app/cmd/doctor_test.go` — mock-based unit test pattern for doctor checks

### Secondary (MEDIUM confidence)

- Terraform documentation (`developer.hashicorp.com/terraform/language/block/moved`): `moved {}` blocks update state addresses only, not API resource identities — WebSearch verified
- AWS Resource Groups Tagging API: `tag:GetResources` + `tag:TagResources` supports cross-service tagging in one API surface — standard AWS pattern

### Tertiary (LOW confidence)

- None — all key claims are verified by code inspection or official docs.

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries already in use; no new dependencies
- Architecture (SES rename, ECS km_label, configure preserve): HIGH — verified by code inspection + Terraform semantics
- Architecture (backfill-tags): MEDIUM — design sound; cross-install safety guard needs planner attention
- Pitfalls: HIGH — identified from code inspection of the exact line numbers
- Test patterns: HIGH — existing test files read and patterns confirmed

**Research date:** 2026-05-16
**Valid until:** 2026-06-16 (stable Terraform + AWS provider behavior; Go patterns stable)
