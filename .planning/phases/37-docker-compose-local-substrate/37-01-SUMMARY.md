---
phase: 37-docker-compose-local-substrate
plan: "01"
subsystem: compiler
tags: [docker, docker-compose, substrate, schema, validation, compiler, text/template]

# Dependency graph
requires:
  - phase: 36-km-sandbox-base-container-image
    provides: km-sandbox container image pattern and entrypoint env vars

provides:
  - docker substrate accepted by JSON schema validator (schemas/sandbox_profile.schema.json)
  - docker substrate accepted by Go semantic validator (pkg/profile/validate.go)
  - compileDocker() function producing docker-compose.yml via text/template
  - generateDockerCompose() with dockerComposeData struct
  - 6-service docker-compose topology: main, km-dns-proxy, km-http-proxy, km-audit-log, km-tracing, km-cred-refresh
  - Credential isolation: only km-cred-refresh container holds operator AWS credentials
  - DNS proxy with static IP 172.20.0.10 on bridge network km-net (172.20.0.0/24)
  - Budget fields (KM_BUDGET_ENABLED, KM_BUDGET_TABLE) conditionally included in km-http-proxy
  - DockerComposeYAML field on CompiledArtifacts struct

affects:
  - 37-02 (create command — reads DockerComposeYAML to inject real credentials and run docker compose up)
  - any future plan using docker substrate compilation

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Docker substrate follows same compileXxx() pattern as compileEC2() and compileECS()"
    - "Named volumes (not tmpfs) for cred-vol — macOS Docker Desktop does not support tmpfs named volumes"
    - "Placeholder credentials in compose template; real values injected by create.go (plan 02)"
    - "Credential isolation: only cred-refresh service has AWS_ACCESS_KEY_ID; all others use AWS_SHARED_CREDENTIALS_FILE"

key-files:
  created:
    - pkg/compiler/compose.go
    - pkg/compiler/compose_test.go
    - pkg/compiler/testdata/docker-basic.yaml
    - pkg/compiler/testdata/docker-with-budget.yaml
    - testdata/profiles/valid-docker-substrate.yaml
  modified:
    - schemas/sandbox_profile.schema.json
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/validate.go
    - pkg/profile/validate_test.go
    - testdata/profiles/invalid-bad-substrate.yaml
    - pkg/compiler/compiler.go

key-decisions:
  - "Named volumes (not tmpfs) for cred-vol — per research Pitfall 3: macOS Docker Desktop does not support tmpfs named volumes"
  - "Placeholder credentials in compile output; real AWS credentials injected by plan 02 create command"
  - "Budget table name pattern: km-budget-{sandboxID} — consistent with EC2/ECS budget enforcer naming"
  - "Credential isolation enforced at template level: AWS_ACCESS_KEY_ID appears only in km-cred-refresh service block"

patterns-established:
  - "Docker compose template uses same text/template pattern as service_hcl.go EC2/ECS templates"
  - "compileDocker() reuses compileSecrets() and compileIAMPolicy() helper functions"
  - "Test fixtures for docker substrate follow same structure as ec2-basic.yaml"

requirements-completed: []

# Metrics
duration: 6min
completed: 2026-03-31
---

# Phase 37 Plan 01: Docker Substrate Schema Validation and Compiler Summary

**docker substrate added to JSON schema + Go validator; compileDocker() generates 6-service docker-compose.yml with DNS proxy static IP, credential isolation, and conditional budget fields via text/template**

## Performance

- **Duration:** 6 min
- **Started:** 2026-03-31T12:06:35Z
- **Completed:** 2026-03-31T12:12:00Z
- **Tasks:** 2
- **Files modified:** 11

## Accomplishments
- JSON schema and Go semantic validator now accept `substrate: docker` profiles; existing tests updated to use `kubernetes` as the invalid substrate
- `compileDocker()` produces valid docker-compose.yml with 6 services via text/template (main, km-dns-proxy, km-http-proxy, km-audit-log, km-tracing, km-cred-refresh)
- Credential isolation enforced at template level: only `km-cred-refresh` contains `AWS_ACCESS_KEY_ID`; all other containers use `AWS_SHARED_CREDENTIALS_FILE`
- DNS proxy gets static IP `172.20.0.10` on bridge network `172.20.0.0/24`; main container uses `dns: [172.20.0.10]`
- Budget fields (`KM_BUDGET_ENABLED`, `KM_BUDGET_TABLE`) conditionally included in km-http-proxy when profile has budget section

## Task Commits

Each task was committed atomically:

1. **Task 1: Add docker to substrate enum and update schema validation** - `f44ce6b` (feat)
2. **Task 2: Implement compileDocker() and docker-compose.yml template** - `d304d04` (feat)

_Note: TDD tasks — tests written first (RED confirmed), then implementation (GREEN), all in single atomic commits_

## Files Created/Modified
- `pkg/compiler/compose.go` - compileDocker(), generateDockerCompose(), dockerComposeTemplate, dockerComposeData struct
- `pkg/compiler/compose_test.go` - 6 unit tests covering all must-haves (containers, DNS, cred isolation, budget, network)
- `pkg/compiler/testdata/docker-basic.yaml` - minimal docker profile test fixture
- `pkg/compiler/testdata/docker-with-budget.yaml` - docker profile with budget section test fixture
- `testdata/profiles/valid-docker-substrate.yaml` - valid docker substrate profile fixture for validator tests
- `schemas/sandbox_profile.schema.json` - enum extended to include "docker"
- `pkg/profile/schemas/sandbox_profile.schema.json` - embedded schema updated (same change)
- `pkg/profile/validate.go` - Rule 2 substrate check now accepts "docker"
- `pkg/profile/validate_test.go` - updated substrate tests + added TestValidateDockerSubstrate
- `testdata/profiles/invalid-bad-substrate.yaml` - changed substrate from docker to kubernetes (truly invalid)
- `pkg/compiler/compiler.go` - added DockerComposeYAML field to CompiledArtifacts; case "docker" in Compile() switch

## Decisions Made
- Named volumes (not tmpfs) for cred-vol — per research Pitfall 3: macOS Docker Desktop does not support tmpfs named volumes
- Placeholder credentials in compiled output (`PLACEHOLDER_SANDBOX_ROLE_ARN`, `PLACEHOLDER_OPERATOR_KEY`); real AWS credentials injected by plan 02 `runCreateDocker()` in create.go
- Budget table name follows pattern `km-budget-{sandboxID}` consistent with EC2/ECS budget enforcer

## Deviations from Plan

**1. [Rule 3 - Blocking] Two separate schema files needed updating**
- **Found during:** Task 1 (schema update)
- **Issue:** Plan referenced `schemas/sandbox_profile.schema.json` but the actual embedded file is `pkg/profile/schemas/sandbox_profile.schema.json` (via `//go:embed`). Updating only the root-level file had no effect.
- **Fix:** Updated both files to match. The root-level file appears to be a documentation/reference copy; the embedded file in `pkg/profile/schemas/` is what the validator actually uses.
- **Files modified:** Both `schemas/sandbox_profile.schema.json` and `pkg/profile/schemas/sandbox_profile.schema.json`
- **Verification:** TestValidateDockerSubstrate passes after updating both files
- **Committed in:** `f44ce6b` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking — dual schema file discovery)
**Impact on plan:** Necessary fix for correctness; no scope creep.

## Issues Encountered
- The schema `sync.Once` cache means tests compile the schema once per process; discovering the embedded schema path was blocking until identified by examining `schema.go`'s `//go:embed` directive.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- `substrate: docker` is fully validated and compileable
- `Compile()` dispatches to `compileDocker()` for docker substrate
- `DockerComposeYAML` populated on `CompiledArtifacts` for plan 02 to consume
- Plan 02 can implement `runCreateDocker()`: inject real credentials into placeholders, write docker-compose.yml, run `docker compose up -d`

---
*Phase: 37-docker-compose-local-substrate*
*Completed: 2026-03-31*
