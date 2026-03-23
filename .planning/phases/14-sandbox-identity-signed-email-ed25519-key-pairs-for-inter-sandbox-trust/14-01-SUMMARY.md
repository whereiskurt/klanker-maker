---
phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust
plan: "01"
subsystem: identity
tags: [dynamodb, terraform, terragrunt, profile-schema, email-policy, ed25519]

# Dependency graph
requires:
  - phase: 06-budget-enforcement-platform-configuration
    provides: BudgetTableName config pattern, dynamodb-budget module structure

provides:
  - EmailSpec struct on Spec with signing/verifyInbound/encryption fields
  - JSON schema spec.email with required|optional|off enum validation
  - IdentityTableName config field defaulting to km-identities
  - DynamoDB identities Terraform module (dynamodb-identities/v1.0.0)
  - Terragrunt live config for km-identities table
  - Built-in profiles with email policy defaults per security tier

affects:
  - 14-02 (key pair generation reads EmailSpec and IdentityTableName)
  - 14-03 (identity store reads DynamoDB identities table)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - EmailSpec pointer field on Spec (same omitempty pattern as Budget/Artifacts)
    - IdentityTableName config field with SetDefault (same pattern as BudgetTableName)
    - dynamodb-identities module uses sandbox_id as sole PK (no sort key unlike budget)

key-files:
  created:
    - pkg/profile/email_test.go
    - infra/modules/dynamodb-identities/v1.0.0/main.tf
    - infra/modules/dynamodb-identities/v1.0.0/variables.tf
    - infra/modules/dynamodb-identities/v1.0.0/outputs.tf
    - infra/live/use1/dynamodb-identities/terragrunt.hcl
  modified:
    - pkg/profile/types.go
    - schemas/sandbox_profile.schema.json
    - pkg/profile/schemas/sandbox_profile.schema.json
    - internal/app/config/config.go
    - internal/app/config/config_test.go
    - profiles/hardened.yaml
    - profiles/sealed.yaml
    - profiles/open-dev.yaml
    - profiles/restricted-dev.yaml

key-decisions:
  - "EmailSpec is a pointer on Spec (same pattern as Budget/Artifacts) — nil means email policy not specified"
  - "dynamodb-identities uses sandbox_id (S) as sole hash key with no sort key — each sandbox has exactly one identity row (unlike budget table which uses PK+SK for multiple spend rows per sandbox)"
  - "No DynamoDB Streams on identities table — identity reads are on-demand lookups, no Lambda trigger needed"
  - "replica_regions variable present but empty by default — single-region v1; matches km-budgets pattern for future multi-region"
  - "IdentityTableName defaults to km-identities in config.go SetDefault — usable without mandatory km-config.yaml configuration"

patterns-established:
  - "Email policy in profile uses three-value enum (required|optional|off) — same enum pattern can be extended to other policy axes"
  - "DynamoDB module key design documented in main.tf header comment — follows km-budgets documentation convention"

requirements-completed:
  - IDENT-SCHEMA
  - IDENT-DYNAMO
  - IDENT-CONFIG

# Metrics
duration: 8min
completed: 2026-03-23
---

# Phase 14 Plan 01: Sandbox Identity Foundations Summary

**EmailSpec on SandboxProfile with signing/verifyInbound/encryption enum fields, DynamoDB km-identities table module, and IdentityTableName config default**

## Performance

- **Duration:** 8 min
- **Started:** 2026-03-23T03:32:00Z
- **Completed:** 2026-03-23T03:40:45Z
- **Tasks:** 2
- **Files modified:** 14

## Accomplishments
- Added EmailSpec struct to SandboxProfile with three enum fields (signing, verifyInbound, encryption) and JSON schema validation using required|optional|off values
- Added IdentityTableName to Config struct defaulting to "km-identities" with full km-config.yaml override support
- Created DynamoDB identities Terraform module with sandbox_id hash key, PAY_PER_REQUEST billing, expiresAt TTL, and Terragrunt live config
- Updated all four built-in profiles with email policy defaults: hardened/sealed get required signing, open-dev/restricted-dev get optional

## Task Commits

Each task was committed atomically:

1. **Task 1: RED phase (failing email tests)** - `b6940a4` (test)
2. **Task 1: GREEN phase (EmailSpec + schema + config + profiles)** - `cb79a90` (feat)
3. **Task 2: DynamoDB identities module + Terragrunt config** - `8e6aac1` (feat)

_Note: TDD task had RED commit (failing tests) then GREEN commit (implementation)_

## Files Created/Modified
- `pkg/profile/types.go` - Added EmailSpec struct and Email *EmailSpec field to Spec
- `schemas/sandbox_profile.schema.json` - Added spec.email property with enum validation
- `pkg/profile/schemas/sandbox_profile.schema.json` - Synced copy for go:embed
- `internal/app/config/config.go` - Added IdentityTableName field with km-identities default
- `internal/app/config/config_test.go` - Added TestLoadIdentityTableDefault and TestLoadIdentityTableFromConfig
- `pkg/profile/email_test.go` - Tests for EmailSpec parse, schema validation, optional field
- `profiles/hardened.yaml` - Added email: signing: required, verifyInbound: required, encryption: off
- `profiles/sealed.yaml` - Added email: signing: required, verifyInbound: required, encryption: off
- `profiles/open-dev.yaml` - Added email: signing: optional, verifyInbound: optional, encryption: off
- `profiles/restricted-dev.yaml` - Added email: signing: optional, verifyInbound: optional, encryption: off
- `infra/modules/dynamodb-identities/v1.0.0/main.tf` - DynamoDB table definition
- `infra/modules/dynamodb-identities/v1.0.0/variables.tf` - table_name, replica_regions, tags variables
- `infra/modules/dynamodb-identities/v1.0.0/outputs.tf` - table_name and table_arn outputs
- `infra/live/use1/dynamodb-identities/terragrunt.hcl` - Terragrunt live config

## Decisions Made
- EmailSpec uses pointer on Spec (nil = not specified) matching Budget and Artifacts pattern
- DynamoDB identities table uses sandbox_id as sole PK with no sort key — one row per sandbox vs budget table's multi-row PK+SK design
- No DynamoDB Streams on identities table — identity reads are on-demand, no Lambda trigger needed
- replica_regions variable present but default empty — single-region v1, matches km-budgets for future multi-region

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- EmailSpec, IdentityTableName, and DynamoDB identities module are ready
- Plans 02 and 03 can build against these type contracts and infrastructure
- No blockers

---
*Phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust*
*Completed: 2026-03-23*
