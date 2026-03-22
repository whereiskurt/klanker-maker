---
phase: 08-sidecar-build-deployment-pipeline
plan: "02"
subsystem: compiler
tags: [ecs, ecr, compiler, hcl, sidecar, fargate, image-uri]

# Dependency graph
requires:
  - phase: 07-unwired-code-paths
    provides: ECS compiler baseline with placeholder image literals

provides:
  - ECR URI computation in generateECSServiceHCL from KM_ACCOUNTS_APPLICATION + region
  - KM_SIDECAR_VERSION env var controls sidecar image tag (defaults to "latest")
  - PLACEHOLDER_ECR/ prefix fallback when KM_ACCOUNTS_APPLICATION is unset
  - No ${var.*_image} literals remain in ecsServiceHCLTemplate

affects:
  - ECS provisioning (km create with ecs substrate now emits runnable service.hcl)
  - Phase 09 live infra (ECR registry must exist in account before km create)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "ECR URI as os.Getenv-driven computed field in ecsHCLParams — keeps template data-only, logic in generateECSServiceHCL"
    - "PLACEHOLDER_ECR/ prefix for missing env vars — HCL parses but clearly non-functional; allows local compiler testing without AWS creds"
    - "sidecarImage() closure inside generateECSServiceHCL captures ecrRegistry and imageTag — avoids repetition for 4 sidecars"

key-files:
  created: []
  modified:
    - pkg/compiler/service_hcl.go
    - pkg/compiler/service_hcl_test.go

key-decisions:
  - "ECR URI computation reads KM_ACCOUNTS_APPLICATION at generateECSServiceHCL call time (not package init) — consistent with KM_ARTIFACTS_BUCKET pattern already in the function"
  - "PLACEHOLDER_ECR/ (no account, no region) used for unset env var — parseable HCL string, distinguishable from real URIs, does not require special-casing in tests"
  - "KM_SIDECAR_VERSION defaults to 'latest' when unset — deploy pipeline sets explicit tag; local dev gets a working default"

patterns-established:
  - "Sidecar image URI: {accountID}.dkr.ecr.{region}.amazonaws.com/km-{sidecar-name}:{version}"

requirements-completed: [PROV-10]

# Metrics
duration: 2min
completed: 2026-03-22
---

# Phase 08 Plan 02: ECS Compiler ECR URI Emission Summary

**ECS service.hcl compiler now emits resolvable ECR image URIs computed from KM_ACCOUNTS_APPLICATION + profile region, replacing broken ${var.*_image} Terraform variable references**

## Performance

- **Duration:** ~2 min
- **Started:** 2026-03-22T22:43:43Z
- **Completed:** 2026-03-22T22:45:15Z
- **Tasks:** 1
- **Files modified:** 2

## Accomplishments

- Replaced all 4 `${var.*_image}` broken literals in `ecsServiceHCLTemplate` with Go template refs (`{{ .DNSProxyImage }}` etc.)
- Added `DNSProxyImage`, `HTTPProxyImage`, `AuditLogImage`, `TracingImage` string fields to `ecsHCLParams`
- ECR URI computed as `{accountID}.dkr.ecr.{region}.amazonaws.com/km-{name}:{tag}` — real URIs that ECS/Fargate can pull
- `PLACEHOLDER_ECR/km-{name}:{tag}` emitted when `KM_ACCOUNTS_APPLICATION` is unset — HCL is parseable but clearly non-functional
- `KM_SIDECAR_VERSION` controls image tag with `latest` default
- 3 new tests added; all 10 ECS compiler tests pass

## Task Commits

1. **Task 1: Fix compiler ECR URI emission + add test** — `ccc5a7b` (feat)

## Files Created/Modified

- `pkg/compiler/service_hcl.go` — Added 4 image fields to `ecsHCLParams`, ECR URI computation in `generateECSServiceHCL`, template literal replacements
- `pkg/compiler/service_hcl_test.go` — Added `TestECSServiceHCLImageURIs`, `TestECSServiceHCLImageURIsPlaceholder`, `TestECSServiceHCLImageVersion`

## Decisions Made

- ECR URI computation reads `KM_ACCOUNTS_APPLICATION` at `generateECSServiceHCL` call time — consistent with the `KM_ARTIFACTS_BUCKET` pattern already in the function
- `PLACEHOLDER_ECR/` prefix (no account ID, no region) chosen for unset env var — parseable HCL string, distinguishable from real URIs, requires no special-casing
- `KM_SIDECAR_VERSION` defaults to `"latest"` when unset — deploy pipeline sets explicit tag; local dev gets a usable default

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required for this compiler change. ECR registries (`km-dns-proxy`, `km-http-proxy`, `km-audit-log`, `km-tracing`) must exist in the application account before `km create` will produce runnable ECS tasks; that is a Phase 09 infrastructure concern.

## Next Phase Readiness

- ECS compiler now emits valid, runnable service.hcl for Fargate tasks — `terragrunt plan` will no longer fail on unresolved variable references
- Phase 09 (live infra) must create ECR repositories named `km-dns-proxy`, `km-http-proxy`, `km-audit-log`, `km-tracing` in the application account
- `KM_ACCOUNTS_APPLICATION` must be set in the operator environment (or km config) for `km create` to produce real URIs

---
*Phase: 08-sidecar-build-deployment-pipeline*
*Completed: 2026-03-22*
