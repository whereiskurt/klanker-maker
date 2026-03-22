---
phase: 06-budget-enforcement-platform-configuration
plan: 02
subsystem: database
tags: [dynamodb, budget, pricing, bedrock, aws-sdk, terraform, tdd]

# Dependency graph
requires:
  - phase: 06-budget-enforcement-platform-configuration
    provides: Phase 6 platform config context and BUDG requirements

provides:
  - BudgetSpec types in pkg/profile/types.go (ComputeBudget, AIBudget, BudgetSpec)
  - BudgetAPI narrow interface with IncrementAISpend, IncrementComputeSpend, GetBudget, SetBudgetLimits
  - PricingAPI narrow interface with GetBedrockModelRates (static fallback) and GetSpotRate
  - DynamoDB global table Terraform module at infra/modules/dynamodb-budget/v1.0.0
  - JSON schema budget section in both schemas/ and pkg/profile/schemas/ copies

affects:
  - 06-04 (proxy interception — needs BudgetAPI to record spend)
  - 06-05 (Lambda enforcement — needs BudgetAPI and DynamoDB module)
  - 06-06 (CLI budget commands — needs BudgetAPI and GetBudget)

# Tech tracking
tech-stack:
  added:
    - github.com/aws/aws-sdk-go-v2/service/dynamodb v1.57.0
    - github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue v1.20.36
    - github.com/aws/aws-sdk-go-v2/service/pricing v1.40.14
  patterns:
    - Narrow DynamoDB interface (BudgetAPI) follows ses.go and artifacts.go pattern
    - DynamoDB key design: PK=SANDBOX#{id}, SK=BUDGET#{type}#{qualifier}
    - Static fallback for Pricing API — budget calculations work without credentials
    - ADD expression for atomic spend increment (no read-modify-write races)

key-files:
  created:
    - pkg/aws/budget.go — BudgetAPI interface, IncrementAISpend, IncrementComputeSpend, GetBudget, SetBudgetLimits
    - pkg/aws/budget_test.go — test doubles via fakeBudgetClient, 4 test cases
    - pkg/aws/pricing.go — PricingAPI interface, GetBedrockModelRates with static fallback, GetSpotRate
    - pkg/aws/pricing_test.go — static fallback and price ordering tests
    - infra/modules/dynamodb-budget/v1.0.0/main.tf — PAY_PER_REQUEST global table, TTL, DDB Streams
    - infra/modules/dynamodb-budget/v1.0.0/variables.tf — table_name, replica_regions, tags
    - infra/modules/dynamodb-budget/v1.0.0/outputs.tf — table_name, table_arn, stream_arn
  modified:
    - pkg/profile/types.go — added BudgetSpec, ComputeBudget, AIBudget; Budget *BudgetSpec to Spec
    - pkg/profile/types_test.go — added TestBudgetSpecParsesFromYAML, TestBudgetSpecWarningThresholdDefault, TestBudgetSpecOptional
    - schemas/sandbox_profile.schema.json — added budget section (not in required list)
    - pkg/profile/schemas/sandbox_profile.schema.json — kept in sync with root schema

key-decisions:
  - "BudgetAPI narrow interface uses UpdateItem+GetItem+Query — matches narrow interface pattern from ses.go; real *dynamodb.Client satisfies it directly"
  - "DynamoDB ADD expression used for atomic spend increment — eliminates read-modify-write races under concurrent sandbox workloads"
  - "GetBedrockModelRates returns static fallback when client=nil or API unreachable — budget calculations work in environments without Pricing API access"
  - "PricingAPI note: GetSpotRate uses GetProducts (on-demand pricing proxy); production spot prices require ec2.DescribeSpotPriceHistory"
  - "DynamoDB TTL on expiresAt — spend records auto-expire after sandbox teardown + retention window without explicit cleanup"
  - "DynamoDB Streams enabled with NEW_AND_OLD_IMAGES — enables Lambda budget enforcement triggers (Plans 05) to read before/after spend values"

patterns-established:
  - "BudgetAPI narrow interface: UpdateItem/GetItem/Query — same pattern as SESV2API, S3PutAPI"
  - "SK design: BUDGET#compute | BUDGET#ai#{modelID} | BUDGET#limits — SK prefix-scan returns all budget rows in one Query"
  - "Static pricing fallback: nil-client guard returns hardcoded rates — feature degrades gracefully without AWS credentials"

requirements-completed: [BUDG-01, BUDG-02, BUDG-05]

# Metrics
duration: 15min
completed: 2026-03-22
---

# Phase 6 Plan 02: Budget Types + DynamoDB Storage Layer Summary

**BudgetSpec types with DynamoDB atomic-increment storage layer, PricingAPI with static Bedrock fallback, and PAY_PER_REQUEST global table Terraform module**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-03-22T19:40:00Z
- **Completed:** 2026-03-22T19:55:00Z
- **Tasks:** 2
- **Files modified:** 11

## Accomplishments

- BudgetSpec (ComputeBudget + AIBudget + WarningThreshold) added to profile types; both JSON schema copies updated; profiles with/without budget both validate
- BudgetAPI with DynamoDB ADD expression for atomic spend increment — IncrementAISpend, IncrementComputeSpend, GetBudget, SetBudgetLimits
- PricingAPI with GetBedrockModelRates returning static rates for claude-3-haiku, claude-sonnet-4-5, claude-opus-4-5 when API unavailable
- DynamoDB Terraform module: PAY_PER_REQUEST, global table replicas, TTL, Streams (NEW_AND_OLD_IMAGES)

## Task Commits

Each task was committed atomically:

1. **Task 1: BudgetSpec types + JSON Schema update** - `5be2d7b` (feat)
2. **Task 2: BudgetAPI + PricingAPI packages + DynamoDB Terraform module** - `8a3c375` (feat)

_Note: TDD tasks — tests written before implementation in each task._

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/profile/types.go` - Added BudgetSpec, ComputeBudget, AIBudget structs; Budget *BudgetSpec pointer in Spec
- `/Users/khundeck/working/klankrmkr/pkg/profile/types_test.go` - Added 3 BudgetSpec test cases
- `/Users/khundeck/working/klankrmkr/schemas/sandbox_profile.schema.json` - Added budget section (optional, not in required list)
- `/Users/khundeck/working/klankrmkr/pkg/profile/schemas/sandbox_profile.schema.json` - Kept in sync with root schema
- `/Users/khundeck/working/klankrmkr/pkg/aws/budget.go` - BudgetAPI interface + 4 helper functions
- `/Users/khundeck/working/klankrmkr/pkg/aws/budget_test.go` - 4 test cases with fakeBudgetClient
- `/Users/khundeck/working/klankrmkr/pkg/aws/pricing.go` - PricingAPI interface, static fallback, GetSpotRate
- `/Users/khundeck/working/klankrmkr/pkg/aws/pricing_test.go` - 3 pricing test cases
- `/Users/khundeck/working/klankrmkr/infra/modules/dynamodb-budget/v1.0.0/main.tf` - DynamoDB global table resource
- `/Users/khundeck/working/klankrmkr/infra/modules/dynamodb-budget/v1.0.0/variables.tf` - table_name, replica_regions, tags
- `/Users/khundeck/working/klankrmkr/infra/modules/dynamodb-budget/v1.0.0/outputs.tf` - table_name, table_arn, stream_arn

## Decisions Made

- BudgetAPI uses ADD expression (not SET) for atomic increment — prevents race conditions under concurrent sandbox AI traffic
- PricingAPI GetSpotRate uses GetProducts (on-demand data) as proxy; real spot prices require ec2.DescribeSpotPriceHistory — noted in code comment for Plan 06-04
- Static Bedrock rates use nil-client guard so budget calculations degrade gracefully without Pricing API permissions

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Fixed pricing.Filter type — must use pricingtypes.Filter from service/pricing/types package**
- **Found during:** Task 2 (PricingAPI implementation)
- **Issue:** `pricing.Filter` does not exist; Filter type is in `pricing/types` subpackage
- **Fix:** Added `pricingtypes` import alias for `github.com/aws/aws-sdk-go-v2/service/pricing/types`; used `pricingtypes.Filter` and `pricingtypes.FilterTypeTermMatch`
- **Files modified:** pkg/aws/pricing.go
- **Verification:** `go test ./pkg/aws/` passed
- **Committed in:** 8a3c375 (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** Single SDK type fix required. No scope creep.

## Issues Encountered

- AWS Pricing SDK types are in a `types` subpackage, not the main service package — this is consistent with other SDK services but not documented in the plan. Auto-fixed via Rule 3.

## User Setup Required

None — this plan creates library packages and infrastructure modules only. No external services require configuration at this stage.

## Next Phase Readiness

- BudgetSpec ready for km validate/create integration (Plan 06-03 or later)
- BudgetAPI ready for proxy interception recording (Plan 06-04)
- DynamoDB module ready for live deployment via Terragrunt (Plan 06-05)
- PricingAPI with static fallback ready for Lambda enforcement calculations (Plan 06-05)

---
*Phase: 06-budget-enforcement-platform-configuration*
*Completed: 2026-03-22*
