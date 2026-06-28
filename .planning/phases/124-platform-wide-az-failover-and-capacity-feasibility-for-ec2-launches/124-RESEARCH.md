# Phase 124: Platform-wide AZ Failover and Capacity Feasibility for EC2 Launches — Research

**Researched:** 2026-06-28
**Domain:** AWS EC2 capacity management, Terraform AZ override injection, DynamoDB capacity store, Go AZ-sweep loop
**Confidence:** HIGH (grounded in actual source files + confirmed line numbers)

---

<user_constraints>
## User Constraints (from Design Spec — substitutes for CONTEXT.md)

### Locked Decisions
- Mechanism: orchestrated AZ sweep inside `km create` (not EC2 Fleet / CreateFleet)
- AZ override injected via `network.AvailabilityZones[0]` rotation → existing ec2spot template path
- Error taxonomy: Iterate = ICE / SpotMaxPriceTooLow / MaxSpotInstanceCountExceeded / waiter-timeout; Fail-fast = quota (VcpuLimitExceeded / InstanceLimitExceeded) / auth / invalid
- Shared store: `{prefix}-capacity` DDB table; key = (instanceType, az); TTL ~45min on ICE rows only
- Feasibility signals: DescribeInstanceTypeOfferings (AZ offerings) + Service Quotas L-DB2E81BA (GPU quota); NOT live on-demand prediction
- `km capacity` verdicts: `likely | quota-blocked | not-offered | recently-dry | unknown` — never "available"
- `km create --wait-for-capacity[=30m]` outer backoff for operator-path only
- Bounded spot waiter: `timeouts { create = "3m" }` on `aws_spot_instance_request`
- Lambda cold-create inherits the sweep because it runs `km create` as a subprocess

### Claude's Discretion
- Exact package name for shared classifier: `pkg/capacity/` recommended
- Bounded waiter timeout variable vs hardcoded: operator-config var preferred
- Where `GetCapacityTableName()` lives: follow `GetSlackChannelsTableName()` pattern in `internal/app/config/config.go`
- rankAZs cache duration: short in-process cache (5 min) for DescribeInstanceTypeOfferings results

### Deferred Ideas (OUT OF SCOPE)
- Multi-region failover
- Live on-demand capacity prediction
- Lambda auto-requeue of `nocap` cold-creates
- EC2 Fleet / CreateFleet rewrite
- Spot interruption handling
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| REQ-124-SWEEP | AZ override plumbing compiler→ec2spot single-instance + N>1 spread preserved; classify-and-retry loop in km create; taint/replace between attempts | §AZ Override Injection, §Retry Mechanics |
| REQ-124-CLASSIFY | Shared error taxonomy: ICE/spot-price/waiter-timeout → iterate; quota/auth/invalid → fail-fast with quota-named remediation; create-handler `nocap` refactored onto it | §Error Classification |
| REQ-124-WAITER | Bounded `timeouts.create` on `aws_spot_instance_request`; full 4-AZ sweep fits Lambda 900s budget | §Bounded Spot Waiter |
| REQ-124-RANK | capacity-aware rankAZs: drop non-offering AZs via DescribeInstanceTypeOfferings, regional-quota gate up front, last-success sticky, ICE deprioritize, `spec.runtime.azPreference` override | §rankAZs Implementation |
| REQ-124-STORE | New `{prefix}-capacity` DDB table + TF module + regionalModules() bump + live unit; (instanceType, az) key, TTL'd ICE rows, read/write from operator + Lambda | §Capacity Store |
| REQ-124-CAPCMD | `km capacity <profile\|--type>` feasibility report; verdicts likely/quota-blocked/not-offered/recently-dry/unknown | §km capacity Command |
| REQ-124-SURFACE | `spec.runtime.azPreference` additive schema; `km create --wait-for-capacity[=30m]` opt-in outer backoff; `km doctor` table + GPU-quota=0 WARN | §Profile Schema, §km doctor |
| REQ-124-UAT | Live: GPU launch fails over 1a→1c, quota-0 fail-fast message, `km capacity` report accuracy, all 4 subnets exist | §Validation Architecture |
</phase_requirements>

---

## Summary

Phase 124 upgrades the existing partial AZ-retry loop in `internal/app/cmd/create.go` (lines 746-869) into a full capacity-aware sweep. The infrastructure is 80% there: `runCreate` already rotates `network.AvailabilityZones` on spot-capacity failure and calls `CleanupSandboxDir` between attempts. What is missing is: (1) error taxonomy that distinguishes GPU vCPU quota walls (fail-fast) from plain ICE (iterate), (2) a `rankAZs` function that queries `DescribeInstanceTypeOfferings` to skip non-offering AZs before trying them, (3) a DDB capacity store that remembers recent ICE/success to bias ranking, (4) a bounded spot `timeouts { create }` block so capacity-dry spot AZs error in minutes instead of hanging forever, and (5) the `azPreference` profile field + `km capacity` command + `km doctor` surface.

The AZ override itself is already wired: `ec2spot` uses `effective_azs[0]` for both the instance and `aws_ebs_volume.additional`, so reordering `network.AvailabilityZones` before each compile step naturally moves both. State is wiped by `CleanupSandboxDir` between attempts, so no explicit taint is needed. The `servicequotas` SDK must be added to `go.mod` (not currently present); the `ec2` SDK (v1.296.0) already supports `DescribeInstanceTypeOfferings`.

**Primary recommendation:** Extract the sweep loop into a new `pkg/capacity/` package (classifier + rankAZs + store client), upgrade the `create.go` loop to call it, add the DDB table + TF module, then layer `km capacity` and `km doctor` on top.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/ec2` | v1.296.0 (already in go.mod) | `DescribeInstanceTypeOfferings` for AZ offering check | Already used for AMI/BDM lookups in `pkg/aws/ec2_ami.go` |
| `github.com/aws/aws-sdk-go-v2/service/servicequotas` | must add | `GetServiceQuota` for GPU vCPU quota check (L-DB2E81BA) | Part of AWS SDK v2 suite; not yet in go.mod |
| `github.com/aws/aws-sdk-go-v2/service/dynamodb` | v1.57.0 (already in go.mod) | Capacity store read/write | Used throughout `pkg/aws/` |
| Terraform `timeouts` meta-arg | built-in HCL | Bounded spot `aws_spot_instance_request` create timeout | Provider-native; no extra library |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `pkg/terragrunt.Runner` | internal | Apply + ApplyWithStderr wrappers | All terragrunt calls from Go |
| `pkg/aws.EC2API` interface pattern | internal | Narrow EC2 interface for testability | Follow `spot.go` pattern for `EC2CapacityAPI` |
| `internal/app/config.Config.GetCapacityTableName()` | internal | Derive `{prefix}-capacity` table name | Follow `GetSlackChannelsTableName()` pattern |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `DescribeInstanceTypeOfferings` | Hardcoded AZ-to-GPU mapping | Hardcoding breaks when AWS adds GPU to new AZs; API is the correct source |
| DDB capacity store | In-memory / SSM | DDB is shared by operator binary + Lambda subprocess; SSM is slower + write-heavy |
| `timeouts { create = "3m" }` | Go-side apply context deadline | TF-native timeout is cleaner; a Go deadline would also kill the runner before TF can clean up |

**Installation:**
```bash
go get github.com/aws/aws-sdk-go-v2/service/servicequotas
```

---

## Architecture Patterns

### AZ Override Injection — Full Data Flow

The AZ flows operator → compiler → module — no new injection seams needed:

```
spec.runtime.azPreference: [us-east-1c, us-east-1b]
    ↓
rankAZs(instanceType, region, azPreference, capacityStore, ec2Client, sqClient)
    → ordered []string of AZ names (non-offering AZs dropped)
    ↓
network.AvailabilityZones = rankedAZs  (replaces round-robin rotation)
    ↓
compiler.Compile(resolvedProfile, sandboxID, onDemand, network, amiBDMDevices)
    → ec2ServiceHCLTemplate: availability_zones = ["us-east-1c", "us-east-1b", "us-east-1a"]
    ↓
ec2spot/v1.2.0/main.tf local.effective_azs = var.availability_zones
    instance:      availability_zone = local.effective_azs[0]   ← "us-east-1c"
    aws_ebs_volume: availability_zone = local.effective_azs[0]  ← same AZ, automatically
    N>1 spread:    availability_zone = local.effective_azs[idx % len]  ← preserved
```

**Key insight:** `aws_ebs_volume.additional` (`main.tf:733`) and `aws_ebs_volume.snapshot` (`main.tf:762`) both use `local.effective_azs[0]`. When the AZ list is pre-ordered, EBS volume co-location is automatic. No module change needed for EBS coupling.

### Between-Attempt State Cleanup

The existing `terragrunt.CleanupSandboxDir(sandboxDir)` call already handles "taint":

```go
// create.go (existing pattern — Phase 124 extends, not replaces)
for az := range rankedAZs {
    network.AvailabilityZones = reorderWith(az, allAZs)  // az first
    artifacts, _ = compiler.Compile(...)
    sandboxDir, _ = terragrunt.CreateSandboxDir(...)
    terragrunt.PopulateSandboxDir(sandboxDir, ...)

    var applyStderr strings.Builder
    applyErr := runner.ApplyWithStderr(ctx, sandboxDir, &applyStderr)

    if applyErr == nil {
        capacityStore.RecordSuccess(instanceType, az)
        break
    }

    // CleanupSandboxDir wipes state — fresh apply on next iteration
    // No explicit -replace needed (state gone)
    terragrunt.CleanupSandboxDir(sandboxDir)

    switch classifyError(applyStderr.String(), applyErr) {
    case ClassICE, ClassWaiterTimeout:
        capacityStore.RecordICE(instanceType, az)
        continue  // try next AZ
    case ClassQuota, ClassAuth, ClassInvalid:
        return fmt.Errorf("provisioning failed (non-retriable): %w", applyErr)
    }
}
```

Note: `ApplyWithReplace` is NOT needed because state is wiped. The new `pkg/terragrunt.Runner` does not need a new method for Phase 124.

### Recommended Project Structure

```
pkg/capacity/
├── classifier.go        # classifyError(stderr, err) → ErrorClass; exported consts
├── classifier_test.go   # table-driven tests: 15+ real error strings → expected class
├── rankaz.go            # rankAZs(ctx, instanceType, region, prefs, store, ec2c, sqc) []string
├── rankaz_test.go       # unit tests: mocked offerings + quota + ICE store
├── store.go             # CapacityStore interface + DynamoDB impl; RecordICE/RecordSuccess/Get
└── store_test.go        # mock store tests
```

### Pattern 1: Error Classification

```go
// pkg/capacity/classifier.go
type ErrorClass int
const (
    ClassSuccess      ErrorClass = iota
    ClassICE                     // InsufficientInstanceCapacity → iterate
    ClassSpotPrice               // SpotMaxPriceTooLow → iterate
    ClassSpotLimit               // MaxSpotInstanceCountExceeded → iterate
    ClassWaiterTimeout           // context.DeadlineExceeded from bounded spot waiter → iterate
    ClassQuota                   // VcpuLimitExceeded / InstanceLimitExceeded → fail-fast
    ClassAuth                    // AuthFailure / UnauthorizedOperation → fail-fast
    ClassInvalid                 // InvalidParameterValue / Unsupported / UnsupportedOperation → fail-fast
    ClassUnknown
)

func ClassifyError(stderr string, err error) ErrorClass {
    if err == nil {
        return ClassSuccess
    }
    // Iterate cases
    if strings.Contains(stderr, "InsufficientInstanceCapacity") ||
        strings.Contains(stderr, "no Spot capacity") ||
        strings.Contains(stderr, "capacity-not-available") {
        return ClassICE
    }
    if strings.Contains(stderr, "SpotMaxPriceTooLow") {
        return ClassSpotPrice
    }
    if strings.Contains(stderr, "MaxSpotInstanceCountExceeded") {
        return ClassSpotLimit
    }
    if errors.Is(err, context.DeadlineExceeded) {
        return ClassWaiterTimeout
    }
    // Fail-fast cases
    if strings.Contains(stderr, "VcpuLimitExceeded") ||
        strings.Contains(stderr, "InstanceLimitExceeded") ||
        strings.Contains(stderr, "vCPU limit") ||
        strings.Contains(stderr, "You have requested more vCPU capacity") {
        return ClassQuota
    }
    if strings.Contains(stderr, "AuthFailure") ||
        strings.Contains(stderr, "UnauthorizedOperation") {
        return ClassAuth
    }
    if strings.Contains(stderr, "InvalidParameterValue") ||
        strings.Contains(stderr, "UnsupportedOperation") ||
        strings.Contains(stderr, "Unsupported") {
        return ClassInvalid
    }
    return ClassUnknown
}
```

### Pattern 2: Bounded Spot Waiter in ec2spot TF module

```hcl
# infra/modules/ec2spot/v1.2.0/main.tf — add to aws_spot_instance_request resource
resource "aws_spot_instance_request" "ec2spot" {
  # ... existing fields ...
  wait_for_fulfillment = true

  timeouts {
    create = var.spot_create_timeout   # new variable; default "3m"
    delete = "10m"
  }
}
```

```hcl
# variables.tf — add:
variable "spot_create_timeout" {
  type        = string
  description = "Timeout for spot instance fulfillment. Keep <=3m so a 4-AZ sweep fits the Lambda 900s budget."
  default     = "3m"
}
```

The `spot_create_timeout` does NOT need to be wired through `service.hcl` template for Phase 124 — the default `"3m"` is fine. If operator tuning is desired later, a `spec.runtime.spotCreateTimeout` can be added.

### Pattern 3: DynamoDB Capacity Store

```go
// pkg/capacity/store.go
type CapacityStore interface {
    RecordICE(ctx context.Context, instanceType, az string) error
    RecordSuccess(ctx context.Context, instanceType, az string) error
    Get(ctx context.Context, instanceType, az string) (*CapacityEntry, error)
}

type CapacityEntry struct {
    InstanceType  string
    AZ            string
    LastICEAt     *time.Time  // nil if no recent ICE
    LastSuccessAt *time.Time  // nil if never succeeded
}

// DDB key shape:
// hash key: "instanceType" (S)  e.g. "g6e.12xlarge"
// range key: "az" (S)           e.g. "us-east-1c"
// attrs: last_ice_at (N epoch), last_success_at (N epoch), ttl (N epoch)
// TTL on: only ICE rows (last_ice_at + 2700s = 45min expiry)
// Success rows: no TTL (persist indefinitely for sticky last-success ranking)
```

### Pattern 4: rankAZs

```go
// pkg/capacity/rankaz.go
func RankAZs(ctx context.Context, instanceType, region string, azPreference []string,
    store CapacityStore, ec2Client EC2OfferingsAPI, sqClient ServiceQuotasAPI,
    allAZs []string) ([]string, error) {

    // Step 1: Filter to AZs that offer this instance type
    offering, err := describeOfferings(ctx, ec2Client, instanceType, allAZs)
    if err != nil { /* warn, use allAZs as fallback */ }
    offered := filterOffered(allAZs, offering)  // drops non-offering AZs

    // Step 2: Regional quota gate up front
    if isGPUFamily(instanceType) {
        headroom, err := getGPUQuotaHeadroom(ctx, sqClient, region)
        if err == nil && headroom == 0 {
            return nil, &QuotaError{QuotaCode: "L-DB2E81BA", Headroom: 0}
        }
    }

    // Step 3: Merge azPreference ahead of the sorted list (spec.runtime.azPreference)
    // azPreference acts as a "prefer these first" hint; non-preference AZs follow
    remaining := subtract(offered, azPreference)

    // Step 4: Sort remaining by:
    //   a. Last-success AZ first (sticky)
    //   b. Deprioritize AZs with fresh ICE (last_ice_at within 45 min)
    //   c. Alphabetical for stable ordering
    sort.Slice(remaining, func(i, j int) bool {
        return rankScore(ctx, store, instanceType, remaining[i]) >
               rankScore(ctx, store, instanceType, remaining[j])
    })

    return append(intersect(azPreference, offered), remaining...), nil
}
```

### Anti-Patterns to Avoid

- **Hardcoding GPU AZ mapping**: AZ availability changes; use `DescribeInstanceTypeOfferings`.
- **Adding a `-replace=` flag to runner.Apply**: State is wiped by `CleanupSandboxDir` before each attempt, making explicit taint redundant and risky (wrong resource address if key changes).
- **Adding `azPreference` to service.hcl HCL output**: `azPreference` affects ORDERING of `network.AvailabilityZones` before compile, it does NOT appear as a new HCL field. The template already emits `availability_zones = [{{ joinStrings .AvailabilityZones }}]` — no template change for this field.
- **Calling `DescribeInstanceTypeOfferings` inside the retry loop**: Call once before the loop, cache for the session. The result is stable within a create call.
- **Testing `ClassifyError` with live AWS output**: Use recorded real error strings in table-driven tests; AWS error strings are stable.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| AZ-to-offering mapping | Static table in code | `ec2.DescribeInstanceTypeOfferings` | Table becomes stale as AWS expands capacity |
| GPU quota check | Parse IAM errors manually | `servicequotas.GetServiceQuota(L-DB2E81BA)` | Authoritative quota value; error parsing is fragile |
| DDB conditional TTL write | Home-grown item expiry | DDB native TTL attribute | TTL is built-in, server-side, free |
| Spot waiter bounded timeout | Go context + cancel on apply | Terraform `timeouts { create = "3m" }` | TF-native; integrates with state cleanup on timeout |

---

## Common Pitfalls

### Pitfall 1: `make build` Before `km init` (module-order gotcha)
**What goes wrong:** Adding `dynamodb-capacity` to `regionalModules()` but running `km init --dry-run=false` with the old binary silently skips the new module. The create-handler Lambda's IAM policy for the capacity table is never created. At runtime: `AccessDenied` on DDB writes.
**Why it happens:** `km init` is driven by the binary's `regionalModules()` list. A stale binary has no entry.
**How to avoid:** Phase 124 plan must explicitly say: `make build` (bump operator binary) then `km init --dry-run=false`. Documented in design spec §Deploy surface.
**Warning signs:** `km doctor` WARN on missing `{prefix}-capacity` table; `km status` silent failures.

### Pitfall 2: Module Count Test Must Be Bumped
**What goes wrong:** `TestRunInitPlan_ModuleOrder` in `internal/app/cmd/init_plan_test.go:440` hardcodes `len(mods) != 26`. Adding `dynamodb-capacity` without bumping to 27 fails the test.
**Why it happens:** The test guards the contract that the count stays known. See memory `project_module_order_test_count_debt`.
**How to avoid:** Wave 0 task: bump 26→27 in the test assertion at the same time `dynamodb-capacity` is added to `regionalModules()`.

### Pitfall 3: Service Quotas SDK Not in go.mod
**What goes wrong:** `servicequotas` is NOT in `go.mod` (confirmed by inspection). Importing it without `go get` causes a build failure.
**Why it happens:** The repo uses the ec2 SDK for AMI/BDM work but has never needed quotas before.
**How to avoid:** Wave 0: `go get github.com/aws/aws-sdk-go-v2/service/servicequotas` + commit updated `go.mod`/`go.sum`.

### Pitfall 4: Byte-Identity Golden Traps
**What goes wrong:** `azPreference` is a profile field that affects AZ ordering but should NOT inject new HCL tokens into `service.hcl`. If the template emits anything new when `azPreference` is empty, all existing golden fixtures break.
**Why it happens:** See `project_frozen_byte_identity_golden_capture_trap` memory — the frozen pre-92 baseline strips `SubagentStop` and re-capturing corrupts it; hand-patching is required.
**How to avoid:** `azPreference` only affects `network.AvailabilityZones` ordering BEFORE compile. No new template field. The compiled `availability_zones = [...]` list already exists; when `azPreference` is absent and ranking produces the same AZ order, output is byte-identical.

### Pitfall 5: Quota Check Scope — Regional, Not Per-AZ
**What goes wrong:** GPU quota (`L-DB2E81BA`) is regional. Iterating AZs cannot fix a 0-quota wall. The classifier must detect this and fail-fast rather than exhausting all AZs with the same quota error.
**Why it happens:** The `VcpuLimitExceeded` error from AWS looks similar to `InsufficientInstanceCapacity` in Terraform output — both are "can't launch this instance."
**How to avoid:** `rankAZs` calls `GetServiceQuota` once before the AZ loop. If headroom=0 for the instance family, return `QuotaError` immediately. The fail-fast error message must name the quota code (`L-DB2E81BA`) and the increase URL.
**Warning signs:** All AZs returning the same error in sequence; `km doctor` GPU quota=0 WARN.

### Pitfall 6: `allModuleNames` in init_plan_test.go Is Stale (Only 17 Entries)
**What goes wrong:** `allModuleNames` in `init_plan_test.go:133` is documented as "17-module list" but `TestRunInitPlan_ModuleOrder` asserts 26. The `allModuleNames` variable is a mock directory list for plan tests — it is NOT the full regional module list. Adding `dynamodb-capacity` to the mock list is required so plan tests don't fail on missing dirs.
**How to avoid:** Check every plan test that calls `makeRegionDirs(t, repoRoot, "us-east-1", allModuleNames)` — those dirs must exist for the mock runner to find them.

### Pitfall 7: on-demand Does Not Need AZ Rotation
**What goes wrong:** Current code sets `maxAttempts = 1` when `onDemand = true`. Phase 124 maintains this: on-demand instances return `InsufficientInstanceCapacity` rarely and fast (not the forever-loop problem). The bounded waiter fix is spot-only.
**How to avoid:** `--wait-for-capacity` ONLY applies to the outer backoff loop. The inner sweep loop for on-demand: still iterate (ICE is retriable), but `timeouts { create }` is only added to the `aws_spot_instance_request` resource, not `aws_instance`. On-demand `aws_instance` errors fast on ICE already.

---

## Code Examples

### DescribeInstanceTypeOfferings API Shape

```go
// Source: AWS SDK v2 ec2 package — already in go.mod at v1.296.0
import (
    "github.com/aws/aws-sdk-go-v2/service/ec2"
    ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
    awssdk "github.com/aws/aws-sdk-go-v2/aws"
)

// EC2OfferingsAPI — narrow interface for testability (follow pkg/aws/ec2_ami.go pattern)
type EC2OfferingsAPI interface {
    DescribeInstanceTypeOfferings(ctx context.Context,
        params *ec2.DescribeInstanceTypeOfferingsInput,
        optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error)
}

func DescribeAZOfferings(ctx context.Context, client EC2OfferingsAPI, instanceType string, azs []string) ([]string, error) {
    azFilter := make([]string, len(azs))
    copy(azFilter, azs)
    out, err := client.DescribeInstanceTypeOfferings(ctx, &ec2.DescribeInstanceTypeOfferingsInput{
        LocationType: ec2types.LocationTypeAvailabilityZone,
        Filters: []ec2types.Filter{
            {Name: awssdk.String("location"), Values: azFilter},
            {Name: awssdk.String("instance-type"), Values: []string{instanceType}},
        },
    })
    if err != nil {
        return nil, fmt.Errorf("DescribeInstanceTypeOfferings: %w", err)
    }
    offered := make([]string, 0, len(out.InstanceTypeOfferings))
    for _, o := range out.InstanceTypeOfferings {
        offered = append(offered, string(o.Location))
    }
    return offered, nil
}
```

### Service Quotas GetServiceQuota API Shape

```go
// Source: must add — go get github.com/aws/aws-sdk-go-v2/service/servicequotas
import "github.com/aws/aws-sdk-go-v2/service/servicequotas"

type ServiceQuotasAPI interface {
    GetServiceQuota(ctx context.Context, params *servicequotas.GetServiceQuotaInput,
        optFns ...func(*servicequotas.Options)) (*servicequotas.GetServiceQuotaOutput, error)
}

// L-DB2E81BA = "Running On-Demand G and VT instances" (vCPU-denominated)
// Service code = "ec2"
const GPUVCPUQuotaCode = "L-DB2E81BA"
const GPUQuotaServiceCode = "ec2"

func GetGPUVCPUQuota(ctx context.Context, client ServiceQuotasAPI) (float64, error) {
    out, err := client.GetServiceQuota(ctx, &servicequotas.GetServiceQuotaInput{
        ServiceCode: awssdk.String(GPUQuotaServiceCode),
        QuotaCode:   awssdk.String(GPUVCPUQuotaCode),
    })
    if err != nil {
        return 0, fmt.Errorf("GetServiceQuota %s: %w", GPUVCPUQuotaCode, err)
    }
    if out.Quota == nil || out.Quota.Value == nil {
        return 0, fmt.Errorf("quota %s has nil value", GPUVCPUQuotaCode)
    }
    return *out.Quota.Value, nil
}
```

### DDB Capacity Table TF Module (follows dynamodb-slack-nonces pattern)

```hcl
# infra/modules/dynamodb-capacity/v1.0.0/main.tf
resource "aws_dynamodb_table" "capacity" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "instanceType"
  range_key    = "az"

  attribute {
    name = "instanceType"
    type = "S"
  }
  attribute {
    name = "az"
    type = "S"
  }

  # TTL: only ICE rows set this (last_ice_at + 2700s). Success rows omit ttl.
  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  server_side_encryption {
    enabled = true
  }

  tags = merge(var.tags, {
    "km:component" = "km-capacity"
    "km:purpose"   = "az-ice-and-success-memory"
    "km:managed"   = "true"
  })
}
```

```hcl
# infra/live/use1/dynamodb-capacity/terragrunt.hcl — follows dynamodb-slack-nonces pattern
inputs = {
  table_name = "${local.site_vars.locals.site.label}-capacity"
  tags = {
    "km:component" = "km-capacity"
    "km:managed"   = "true"
  }
}
```

### Profile Schema Addition (JSON schema)

```json
// pkg/profile/schemas/sandbox_profile.schema.json — add inside runtime.properties
"azPreference": {
  "type": "array",
  "items": { "type": "string", "minLength": 1 },
  "description": "Preferred AZ order for this profile (e.g. [\"us-east-1c\", \"us-east-1b\"]). Merges ahead of capacity-aware ranking; does not add AZs not offered. EC2 only.",
  "examples": [["us-east-1c", "us-east-1b"]]
}
```

```go
// pkg/profile/types.go — add to RuntimeSpec
// AZPreference is an optional ordered list of preferred AZs for EC2 launches.
// Phase 124: merged ahead of capacity-aware rankAZs; AZs not offered by the
// instance type are silently dropped. EC2 substrate only.
AZPreference []string `yaml:"azPreference,omitempty" json:"azPreference,omitempty"`
```

### checkDynamoTable Pattern for km doctor (follows existing pattern exactly)

```go
// internal/app/cmd/doctor.go — in the checks slice, follow existing checkDynamoTable call pattern:
checkDynamoTable(ctx, dynamoClient, cfg.GetCapacityTableName(), "capacity table"),

// For GPU quota WARN (new check function):
func checkGPUQuotaHeadroom(ctx context.Context, client ServiceQuotasAPI) CheckResult {
    quota, err := capacity.GetGPUVCPUQuota(ctx, client)
    if err != nil {
        return CheckResult{Name: "GPU vCPU quota (L-DB2E81BA)", Status: CheckSkipped,
            Message: fmt.Sprintf("quota check unavailable: %v", err)}
    }
    if quota == 0 {
        return CheckResult{
            Name:   "GPU vCPU quota (L-DB2E81BA)",
            Status: CheckWarn,
            Message: "Running On-Demand G and VT instances quota is 0 vCPUs — GPU launches will fail-fast",
            Remediation: "Request increase at https://console.aws.amazon.com/servicequotas (L-DB2E81BA, service ec2)",
        }
    }
    return CheckResult{Name: "GPU vCPU quota (L-DB2E81BA)", Status: CheckOK,
        Message: fmt.Sprintf("%.0f vCPUs available", quota)}
}
```

### Config.GetCapacityTableName() (follows GetSlackChannelsTableName pattern)

```go
// internal/app/config/config.go — add after GetSlackChannelsTableName:
func (c *Config) GetCapacityTableName() string {
    if c == nil {
        return "km-capacity"
    }
    return c.GetResourcePrefix() + "-capacity"
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| No AZ iteration — single-AZ spot forever-loop | Partial retry loop (rotate AZs on ICE) | Already in create.go ~Phase 33 area | Phase 124 upgrades to capacity-aware sweep |
| `nocap` label on all capacity errors in create-handler | Shared classifier taxonomy | Phase 124 | Quota walls → fail-fast with named remediation |
| No GPU AZ awareness | `DescribeInstanceTypeOfferings` drops non-offering AZs | Phase 124 | GPU launch tries 1c first, skips 1a entirely |
| `wait_for_fulfillment = true` with no timeout | `timeouts { create = "3m" }` | Phase 124 | 4-AZ sweep fits 900s Lambda budget |
| No ICE memory | `{prefix}-capacity` DDB store | Phase 124 | Recently-dry AZ deprioritized on next create |

**Deprecated/outdated:**
- The four-string substring match in `cmd/create-handler/main.go:341` (`nocap` classification) is superseded by `pkg/capacity.ClassifyError()`. The `nocap` status code is preserved but now correctly gated.

---

## Open Questions

1. **`spot_create_timeout` as TF variable vs hardcoded**
   - What we know: Design spec says "~2-3min" per-attempt; 4×3min = 12min < Lambda 900s budget.
   - What's unclear: Should operators be able to tune per-profile? E.g. `spec.runtime.spotCreateTimeout`?
   - Recommendation: Hardcode `"3m"` as default in `variables.tf` for Phase 124. Adding per-profile tuning is a follow-on only if operators request it. Keeps this phase simple.

2. **GPU family detection for quota check scope**
   - What we know: `L-DB2E81BA` covers G and VT families. G = g4dn, g5, g6, g6e. Kimi K2 (P-family) is explicitly out of scope.
   - What's unclear: Is the quota check limited to `isGPUFamily(instanceType)` or all instance types?
   - Recommendation: Gate the `GetServiceQuota` call on instance type prefix `g` or `vt` (check `strings.HasPrefix(strings.ToLower(instanceType), "g")` or `"vt"`). Non-GPU instance types don't hit this quota; the check is wasted noise.

3. **`--wait-for-capacity` in Lambda cold-create path**
   - What we know: Design spec says `--wait-for-capacity` is "operator-path only." The Lambda has a 900s budget.
   - What's unclear: Should the create-handler Lambda ever pass `--wait-for-capacity` to the `km create` subprocess?
   - Recommendation: Never. The Lambda should not pass `--wait-for-capacity`. The flag is for `km at '...' create` scheduled/unattended operator operations only.

---

## Validation Architecture

> `workflow.nyquist_validation: true` in `.planning/config.json` — this section is required.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go standard `testing` package |
| Config file | none — in-tree Go tests |
| Quick run command | `go test ./pkg/capacity/... -timeout 60s` |
| Full suite command | `go test ./... -timeout 600s` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| REQ-124-CLASSIFY | `ClassifyError` maps 15+ real error strings to correct class (ICE/quota/auth/invalid/timeout) | unit | `go test ./pkg/capacity/... -run TestClassifyError -timeout 30s` | ❌ Wave 0 |
| REQ-124-CLASSIFY | create-handler `nocap` uses `ClassifyError(ClassICE\|ClassSpotPrice\|ClassSpotLimit)` | unit | `go test ./cmd/create-handler/... -run TestNocapClassifier -timeout 30s` | ❌ Wave 0 |
| REQ-124-SWEEP | AZ override: `network.AvailabilityZones[0]` is the ranked AZ fed to compile; N>1 spread unchanged | unit | `go test ./pkg/capacity/... -run TestRankAZs -timeout 30s` | ❌ Wave 0 |
| REQ-124-SWEEP | `CleanupSandboxDir` called between attempts; loop exits on first success | unit | `go test ./internal/app/cmd/... -run TestAZSweepLoop -timeout 60s` | ❌ Wave 0 |
| REQ-124-WAITER | `timeouts { create }` appears in compiled ec2spot Terraform | golden | `go test ./pkg/compiler/... -run TestEC2ServiceHCL_SpotTimeout -timeout 30s` | ❌ Wave 0 |
| REQ-124-WAITER | `timeouts { create }` absent when `use_spot = false` (on-demand) | golden | `go test ./pkg/compiler/... -run TestEC2ServiceHCL_OnDemandNoTimeout -timeout 30s` | ❌ Wave 0 |
| REQ-124-RANK | `RankAZs` drops non-offering AZs (DescribeInstanceTypeOfferings mock) | unit | `go test ./pkg/capacity/... -run TestRankAZs_DropsNonOffering -timeout 30s` | ❌ Wave 0 |
| REQ-124-RANK | `RankAZs` returns `QuotaError` when GPU quota=0 (ServiceQuotas mock) | unit | `go test ./pkg/capacity/... -run TestRankAZs_GPUQuotaBlock -timeout 30s` | ❌ Wave 0 |
| REQ-124-RANK | `azPreference` AZs appear first in ranked list when offered | unit | `go test ./pkg/capacity/... -run TestRankAZs_AZPreference -timeout 30s` | ❌ Wave 0 |
| REQ-124-RANK | Last-success AZ ranks first; fresh-ICE AZ ranks last | unit | `go test ./pkg/capacity/... -run TestRankAZs_ICEStickySuccess -timeout 30s` | ❌ Wave 0 |
| REQ-124-STORE | DDB capacity store RecordICE writes correct TTL (now+2700s) | unit | `go test ./pkg/capacity/... -run TestCapacityStore_RecordICE -timeout 30s` | ❌ Wave 0 |
| REQ-124-STORE | DDB capacity store RecordSuccess writes no TTL | unit | `go test ./pkg/capacity/... -run TestCapacityStore_RecordSuccess -timeout 30s` | ❌ Wave 0 |
| REQ-124-SURFACE | `spec.runtime.azPreference` validates as `[]string` in schema | unit | `go test ./pkg/profile/... -run TestValidate_AZPreference -timeout 30s` | ❌ Wave 0 |
| REQ-124-SURFACE | `azPreference` absent → service.hcl byte-identical to pre-124 output | golden | `go test ./pkg/compiler/... -run TestEC2ServiceHCL_AZPreferenceAbsent -timeout 30s` | ❌ Wave 0 |
| REQ-124-SURFACE | `km create --wait-for-capacity` flag registered on create cobra command | unit | `go test ./internal/app/cmd/... -run TestNewCreateCmd_WaitForCapacityFlag -timeout 30s` | ❌ Wave 0 |
| REQ-124-SURFACE | `km doctor` checks capacity table + GPU quota WARN registered | unit | `go test ./internal/app/cmd/... -run TestDoctor_CapacityChecks -timeout 30s` | ❌ Wave 0 |
| REQ-124-SURFACE | `TestRunInitPlan_ModuleOrder` expects 27 modules (26+dynamodb-capacity) | unit | `go test ./internal/app/cmd/... -run TestRunInitPlan_ModuleOrder -timeout 60s` | ✅ exists (must update count) |
| REQ-124-CAPCMD | `km capacity --type g6e.12xlarge` prints per-AZ report with 5 columns | manual/smoke | `km capacity --type g6e.12xlarge` | ❌ Wave 0 stub |
| REQ-124-UAT | GPU sandbox launch 1a→1c failover (g6e.12xlarge) | live UAT | manual: `km create profiles/gpu-qwen-12x-l4.yaml` | ❌ Wave 6 (G-quota gated) |
| REQ-124-UAT | quota=0 produces fail-fast error naming L-DB2E81BA | live UAT | manual: simulate with 0-quota account | ❌ Wave 6 |
| REQ-124-UAT | `km capacity g6e.12xlarge` shows accurate offered/not-offered per AZ | live UAT | manual | ❌ Wave 6 |
| REQ-124-UAT | all 4 public subnets provisioned (verify `availability_zone_count: 4` → 4 subnets) | live UAT | `aws ec2 describe-subnets --filters Name=vpc-id,Values=$VPC_ID` | ❌ Wave 6 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/capacity/... -timeout 60s`
- **Per wave merge:** `go test ./... -timeout 600s`
- **Phase gate:** Full suite green + `scripts/validate-all-profiles.sh` (20/20) before `/gsd:verify-work`

### Wave 0 Gaps (files that must exist before RED stubs turn GREEN)
- [ ] `pkg/capacity/classifier.go` — exports `ClassifyError` + `ErrorClass` consts
- [ ] `pkg/capacity/classifier_test.go` — table-driven; 15+ real AWS error string fixtures
- [ ] `pkg/capacity/rankaz.go` — exports `RankAZs`, `EC2OfferingsAPI`, `ServiceQuotasAPI`
- [ ] `pkg/capacity/rankaz_test.go` — mocked offerings + quota + store
- [ ] `pkg/capacity/store.go` — `CapacityStore` interface + DynamoDB impl + `CapacityEntry`
- [ ] `pkg/capacity/store_test.go` — mock store TTL write assertions
- [ ] `infra/modules/dynamodb-capacity/v1.0.0/main.tf` — new DDB module
- [ ] `infra/modules/dynamodb-capacity/v1.0.0/variables.tf` — `table_name`, `tags`
- [ ] `infra/modules/dynamodb-capacity/v1.0.0/outputs.tf` — `table_name`, `table_arn`
- [ ] `infra/live/use1/dynamodb-capacity/terragrunt.hcl` — live unit
- [ ] Framework install: `go get github.com/aws/aws-sdk-go-v2/service/servicequotas` — if not already done by Wave 0

*(All other stubs are in existing test files; test cases added in-place)*

---

## Sources

### Primary (HIGH confidence)
- Source code: `infra/modules/ec2spot/v1.2.0/main.tf` lines 124-125, 583-637, 731-769 — AZ selection, spot request, EBS volume
- Source code: `pkg/compiler/service_hcl.go` lines 93-174, 684-725 — template + NetworkConfig struct
- Source code: `internal/app/cmd/create.go` lines 704-869 — existing AZ retry loop (confirmed working)
- Source code: `internal/app/cmd/init.go` lines 509-718 — `regionalModules()` current 26 entries
- Source code: `internal/app/cmd/init_plan_test.go` line 440 — `len(mods) != 26` (must bump to 27)
- Source code: `cmd/create-handler/main.go` lines 341-344 — existing `nocap` 4-string classifier
- Source code: `infra/modules/network/v1.0.0/main.tf` lines 1-13 + `outputs.tf` — AZ discovery
- Source code: `infra/live/use1/network/terragrunt.hcl` line 47 — `availability_zone_count: 4`
- Source code: `infra/modules/dynamodb-action-quota/v1.0.0/main.tf` — DDB module pattern to follow
- Source code: `internal/app/config/config.go` lines 1339-1351 — `GetSlackChannelsTableName` pattern
- Source code: `internal/app/cmd/doctor.go` lines 756-782 — `checkDynamoTable` pattern
- Source code: `pkg/profile/types.go` lines 464-500 — `RuntimeSpec` struct
- Source code: `pkg/profile/schemas/sandbox_profile.schema.json` lines 175-310 — runtime schema
- Source code: `go.mod` — AWS SDK versions (ec2 v1.296.0 present; servicequotas absent)
- Source code: `pkg/aws/spot.go` — `EC2API` narrow-interface pattern for testability

### Secondary (MEDIUM confidence)
- AWS docs: `DescribeInstanceTypeOfferings` filter `LocationType=availability-zone` is a stable API; `location` and `instance-type` filter names are documented in the EC2 API reference
- AWS docs: Service Quotas quota code `L-DB2E81BA` = "Running On-Demand G and VT instances" in vCPUs; service-code = `ec2`; verified against CLAUDE.md Phase 122 notes

### Tertiary (LOW confidence)
- GPU AZ availability (`g6e` in us-east-1c, not 1a): reported from Phase 122 live UAT notes in `.planning/phases/122-*/122-BIFROST-VALIDATION.md` (not re-verified here)

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — confirmed from go.mod, existing SDK usage patterns
- Architecture: HIGH — grounded in actual source files with verified line numbers
- Pitfalls: HIGH — derived from project memory files + direct source inspection
- Validation: HIGH — test file locations and count confirmed from source

**Research date:** 2026-06-28
**Valid until:** 2026-07-28 (30 days — stable AWS SDK APIs)

---

## RESEARCH COMPLETE
