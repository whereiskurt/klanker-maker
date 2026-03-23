---
phase: 17-sandbox-email-mailbox-access-control-aliases-allow-lists-self-mail-s3-reader
plan: "01"
subsystem: profile-schema, dynamodb-identities
tags: [email, alias, allowedSenders, dynamodb, gsi, schema, types]
dependency_graph:
  requires: []
  provides:
    - EmailSpec.Alias and EmailSpec.AllowedSenders fields in pkg/profile/types.go
    - JSON schema alias pattern validation and allowedSenders array
    - DynamoDB identities v1.1.0 module with alias-index GSI
    - Built-in profile allowedSenders defaults
  affects:
    - Plans 02 and 03 of Phase 17 (build on EmailSpec alias/allowedSenders and alias GSI)
tech_stack:
  added: []
  patterns:
    - TDD (RED→GREEN) for Go type and schema extension
    - Dual schema location (pkg/profile/schemas/ + schemas/) per Phase 01 decision
    - Versioned Terraform module directory (v1.0.0 → v1.1.0, old unchanged)
key_files:
  created:
    - pkg/profile/parse_test.go
    - infra/modules/dynamodb-identities/v1.1.0/main.tf
    - infra/modules/dynamodb-identities/v1.1.0/variables.tf
    - infra/modules/dynamodb-identities/v1.1.0/outputs.tf
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - schemas/sandbox_profile.schema.json
    - infra/live/use1/dynamodb-identities/terragrunt.hcl
    - profiles/open-dev.yaml
    - profiles/restricted-dev.yaml
    - profiles/hardened.yaml
    - profiles/sealed.yaml
decisions:
  - "[Phase 17-01]: EmailSpec.Alias is omitempty string, not pointer — alias is optional at profile level; nil not required since empty string is sufficient sentinel"
  - "[Phase 17-01]: AllowedSenders is omitempty []string — nil means not specified; consistent with Go zero-value pattern used in existing slice fields"
  - "[Phase 17-01]: alias JSON schema pattern ^[a-z][a-z0-9-]*\\.[a-z][a-z0-9-]*$ — requires at least one segment before and after dot, lowercase+digits+hyphens only"
  - "[Phase 17-01]: alias not added to built-in profiles — alias is per-sandbox identity, not per-profile-template"
  - "[Phase 17-01]: v1.0.0 DynamoDB module left unchanged — v1.1.0 created as new versioned directory per existing module versioning pattern"
metrics:
  duration: 220s
  completed: "2026-03-23"
  tasks_completed: 2
  files_changed: 12
---

# Phase 17 Plan 01: Email Schema Alias and AllowedSenders — Summary

**One-liner:** Extended EmailSpec with Alias/AllowedSenders fields, JSON schema dot-notation pattern validation, DynamoDB identities v1.1.0 with alias-index GSI, and allowedSenders defaults in all four built-in profiles.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 (RED) | Failing tests for EmailSpec alias fields | 19de269 | pkg/profile/parse_test.go |
| 1 (GREEN) | Extend EmailSpec type and JSON schema | 840db3a | types.go, 2x schema JSON |
| 2 | DynamoDB v1.1.0 module + profile defaults | f59317c | 3 new TF files, terragrunt.hcl, 4 profiles |

## What Was Built

### Task 1: EmailSpec Extension (TDD)

Added two fields to `EmailSpec` in `pkg/profile/types.go`:
- `Alias string` — human-friendly dot-notation name (e.g. "research.team-a"), omitempty
- `AllowedSenders []string` — sender allowlist patterns ("self", "*", "build.*"), omitempty

Updated both JSON schema locations (`pkg/profile/schemas/` and `schemas/`) to add:
- `alias` property with pattern `^[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*$` — enforces lowercase dot-notation
- `allowedSenders` array property with string items

Four tests in `pkg/profile/parse_test.go`:
- `TestParse_EmailAlias` — round-trip parse with alias + allowedSenders
- `TestParse_EmailAlias_Empty` — backward-compat when fields omitted
- `TestValidateSchema_EmailAlias_Invalid` — uppercase alias rejected
- `TestValidateSchema_EmailAlias_NoDot` — dotless alias rejected

### Task 2: DynamoDB Identities v1.1.0 + Profile Defaults

Created `infra/modules/dynamodb-identities/v1.1.0/` with:
- `alias` attribute (type S) alongside existing `sandbox_id` hash key
- `alias-index` GSI with hash_key="alias", projection_type="ALL"
- Version tag updated to "v1.1.0"
- variables.tf and outputs.tf copied from v1.0.0 unchanged
- v1.0.0 module left untouched

Updated `infra/live/use1/dynamodb-identities/terragrunt.hcl`:
- Source changed from `v1.0.0` to `v1.1.0`

Updated built-in profiles:
- `open-dev.yaml` + `restricted-dev.yaml`: `allowedSenders: ["*"]`
- `hardened.yaml` + `sealed.yaml`: `allowedSenders: ["self"]`
- No alias added to any profile (alias is per-sandbox identity)

## Verification

All checks passed:
- `go test ./pkg/profile/... -v` — 54 tests PASS, no regressions
- `go test ./... ` — 15 packages PASS
- `km validate profiles/open-dev.yaml` — valid
- `km validate profiles/restricted-dev.yaml` — valid
- `km validate profiles/hardened.yaml` — valid
- `km validate profiles/sealed.yaml` — valid
- `infra/modules/dynamodb-identities/v1.1.0/main.tf` contains "alias-index" (2 matches)
- `infra/modules/dynamodb-identities/v1.0.0/main.tf` unchanged

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

Files verified:
- pkg/profile/parse_test.go — FOUND (created)
- pkg/profile/types.go — FOUND (Alias + AllowedSenders added)
- pkg/profile/schemas/sandbox_profile.schema.json — FOUND (allowedSenders added)
- infra/modules/dynamodb-identities/v1.1.0/main.tf — FOUND (alias-index GSI present)
- infra/live/use1/dynamodb-identities/terragrunt.hcl — FOUND (v1.1.0 source)

Commits verified:
- 19de269 — test(17-01): add failing tests
- 840db3a — feat(17-01): extend EmailSpec
- f59317c — feat(17-01): DynamoDB v1.1.0 + profile defaults
