---
phase: 37-docker-compose-local-substrate
plan: "02"
subsystem: cli-cmd
tags: [docker, docker-compose, substrate, create, destroy, iam, sts, lifecycle]

# Dependency graph
requires:
  - phase: 37-docker-compose-local-substrate
    plan: "01"
    provides: compileDocker() producing DockerComposeYAML with PLACEHOLDER_* values

provides:
  - runCreateDocker() function in create.go — docker substrate create path
  - runDestroyDocker() function in destroy.go — docker substrate destroy path
  - docker_helpers.go — shared dockerComposePath() and runDockerCompose() utilities
  - DockerComposeExecFunc package-level var for test injection of docker compose exec
  - km create with docker substrate: skips Terragrunt, creates IAM roles via SDK,
    assumes roles for scoped credentials, writes docker-compose.yml, runs docker compose up -d
  - km destroy with docker substrate: routes via S3 metadata Substrate field, runs
    docker compose down -v, deletes IAM roles, cleans S3 metadata, removes local dir

affects:
  - km create — docker substrate now fully wired (no Terragrunt involvement)
  - km destroy — docker substrate detected via S3 metadata read before tag lookup
  - future stop/pause commands can reuse runDockerCompose() from docker_helpers.go

# Tech tracking
tech-stack:
  added:
    - os/exec (added to create.go for docker compose up -d)
    - sts.AssumeRole (added to create.go for scoped credential generation)
    - iampkg (added to destroy.go for role deletion)
  patterns:
    - "Docker compose exec injected via DockerComposeExecFunc package-level var (testable)"
    - "IAM role names: km-docker-{sandboxID}-{region} and km-sidecar-{sandboxID}-{region}"
    - "Placeholder injection via strings.ReplaceAll on PLACEHOLDER_* constants from compose template"
    - "Destroy routing: read S3 metadata before tag-based lookup to detect docker substrate"
    - "Each destroy step is independent with warn-not-fail pattern (idempotent cleanup)"

key-files:
  created:
    - internal/app/cmd/docker_helpers.go
    - internal/app/cmd/create_docker_test.go
    - internal/app/cmd/destroy_docker_test.go
  modified:
    - internal/app/cmd/create.go
    - internal/app/cmd/destroy.go

key-decisions:
  - "DockerComposeExecFunc package-level var for test injection — avoids actually running docker in unit tests"
  - "Destroy substrate detection via S3 metadata read (Step 2c) before tag-based lookup — docker sandboxes have no AWS-tagged resources"
  - "IAM role propagation: poll GetRole + 5s sleep before AssumeRole (Pitfall 4 from research)"
  - "stateBucket declared once in Step 2c of runDestroy() — reused (not re-declared) in Step 12"

patterns-established:
  - "docker_helpers.go provides shared runDockerCompose() for use by future stop/pause commands"
  - "Non-docker path in create.go unchanged — docker dispatch is an early return before the Terragrunt AZ-retry loop"

requirements-completed: []

# Metrics
duration: 7min
completed: 2026-03-31
---

# Phase 37 Plan 02: Docker Substrate Create/Destroy CLI Paths Summary

**runCreateDocker() and runDestroyDocker() implement full docker sandbox lifecycle via Docker Compose with IAM SDK role creation, STS credential injection, and S3 metadata write — no Terragrunt involvement**

## Performance

- **Duration:** ~7 min
- **Started:** 2026-03-31T12:15:15Z
- **Completed:** 2026-03-31T12:22:00Z
- **Tasks:** 3
- **Files modified:** 5 (create.go, destroy.go modified; docker_helpers.go, create_docker_test.go, destroy_docker_test.go created)

## Accomplishments

- `runCreateDocker()` in create.go: skips LoadNetworkOutputs for docker substrate (minimal NetworkConfig from cfg fields), compiles profile once and dispatches early (before AZ-retry loop), creates IAM roles via SDK with trust policies + inline policies, polls for IAM propagation + 5s delay before AssumeRole, injects real credentials via strings.ReplaceAll on PLACEHOLDER_* values, writes docker-compose.yml to `~/.km/sandboxes/{id}/`, runs `docker compose up -d`, writes S3 metadata with Substrate=docker, writes MLflow run record
- `runDestroyDocker()` in destroy.go: docker substrate detected via S3 metadata read (Step 2c) before tag-based lookup, runs `docker compose down -v`, deletes IAM roles via SDK (inline policies first), cleans SSM GitHub token parameter, deletes S3 metadata, removes local directory
- `docker_helpers.go`: shared `dockerComposePath()` and `runDockerCompose()` utilities for use by destroy and future stop/pause commands
- `DockerComposeExecFunc` package-level var enables test injection without actually running docker
- 6 unit tests verify substrate routing, NetworkConfig construction, compose file writing, idempotent destroy, helper function behavior

## Task Commits

Each task was committed atomically:

1. **Task 1: Implement runCreateDocker() in create.go** - `d864dd6` (feat)
2. **Task 2: Implement runDestroyDocker() and docker_helpers.go** - `e9f5884` (feat)
3. **Task 3: Write unit tests for docker create/destroy** - `55c2036` (test)

## Files Created/Modified

- `internal/app/cmd/create.go` — docker substrate branch (NetworkConfig, early dispatch, runCreateDocker(), DockerComposeExecFunc, runDockerComposeUp())
- `internal/app/cmd/destroy.go` — Step 2c metadata read for docker routing; runDestroyDocker(); iampkg import
- `internal/app/cmd/docker_helpers.go` — dockerComposePath(), runDockerCompose()
- `internal/app/cmd/create_docker_test.go` — TestCreateDockerSubstrateRouting, TestCreateDockerNetworkConfig, TestCreateDockerWritesComposeFile
- `internal/app/cmd/destroy_docker_test.go` — TestDestroyDockerSubstrateRouting, TestDestroyDockerIdempotent, TestDockerComposePath, TestRunDockerComposeMissingFile

## Decisions Made

- DockerComposeExecFunc package-level var for test injection — follows the RemoteCommandPublisher injection pattern from destroy.go (NewDestroyCmdWithPublisher)
- Destroy substrate detection via S3 metadata read (Step 2c) before tag-based lookup — docker sandboxes have no AWS-tagged EC2/ECS resources (per Pitfall 7 in research)
- IAM role propagation: poll GetRole + 5s sleep before AssumeRole (Pitfall 4 from 37-RESEARCH.md)
- stateBucket declared once in Step 2c — reused in Step 12 to avoid re-declaration compile error

## Deviations from Plan

**1. [Rule 1 - Bug] stateBucket re-declaration conflict in runDestroy()**
- **Found during:** Task 2
- **Issue:** Adding `stateBucket :=` in Step 2c caused a compile error because Step 12 already had `stateBucket :=`. Go doesn't allow re-declaration with `:=` in the same scope.
- **Fix:** Changed Step 12's `:=` to `=` (simple assignment, reusing the variable declared in Step 2c).
- **Files modified:** `internal/app/cmd/destroy.go`
- **Committed in:** `e9f5884` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking bug — variable re-declaration)
**Impact on plan:** Necessary correctness fix; no scope creep.

## Issues Encountered

- The `NetworkConfig` struct does not have a `StateBucket` field (only `ArtifactsBucket`). The docker NetworkConfig construction was adjusted to omit `StateBucket` (which isn't needed by the compiler — it's only used by create.go directly from `cfg.StateBucket`).

## User Setup Required

None - no external service configuration required for this plan.

## Next Phase Readiness

- `km create` with docker substrate fully wired: creates IAM roles, assumes scoped creds, writes compose YAML, runs docker compose up -d
- `km destroy` detects docker sandboxes via S3 metadata and routes to runDestroyDocker()
- `docker_helpers.go` ready for reuse by stop/pause commands in subsequent plans
- Plan 03 can implement `km shell`, `km stop`, `km pause`, `km resume` for docker substrate

---
*Phase: 37-docker-compose-local-substrate*
*Completed: 2026-03-31*
