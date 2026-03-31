---
phase: 36-km-sandbox-base-container-image
plan: 03
subsystem: infra
tags: [docker, ecr, makefile, build-pipeline, km-sandbox, ecs]

requires:
  - phase: 36-01
    provides: containers/sandbox/Dockerfile and entrypoint.sh built in plan 01

provides:
  - Makefile sandbox-image target for local docker builds
  - km-sandbox added to ecr-repos (repository creation) and ecr-push (image push)
  - buildAndPushSandboxImage function in km init for automated ECR distribution

affects:
  - 36-km-sandbox-base-container-image
  - Any ECS/Docker/EKS phases that reference the km-sandbox ECR image

tech-stack:
  added: []
  patterns:
    - "sandbox-image Makefile target follows --load pattern (local) vs --push (ECR)"
    - "km init Step 2a inserts sandbox image push between sidecar upload and toolchain upload"
    - "buildAndPushSandboxImage follows warn-and-continue error pattern matching buildAndUploadSidecars"

key-files:
  created: []
  modified:
    - Makefile
    - internal/app/cmd/init.go

key-decisions:
  - "Used containers/sandbox/ as docker build context (not repo root) since Dockerfile only COPYs from its own directory"
  - "Used config.ApplicationAccountID and config.PrimaryRegion (not plan's placeholder field names AccountsApplication/Region)"
  - "buildAndPushSandboxImage accepts *config.Config directly — no intermediate PlatformConfig abstraction needed"

patterns-established:
  - "sandbox-image Makefile target: --load for local, ecr-push uses --push with versioned + latest tags"
  - "km init step naming: 2a for sandbox image between step 2 (sidecars) and 2b (toolchain)"

requirements-completed:
  - PROV-09
  - PROV-10

duration: 15min
completed: 2026-03-31
---

# Phase 36 Plan 03: Build Pipeline Integration Summary

**km-sandbox container image wired into Makefile (sandbox-image, ecr-repos, ecr-push targets) and km init (Step 2a: buildAndPushSandboxImage) for full ECR distribution**

## Performance

- **Duration:** 15 min
- **Started:** 2026-03-31T01:47:57Z
- **Completed:** 2026-03-31T01:54:00Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- Added `sandbox-image` Makefile target for local builds using `--load` (linux/amd64, versioned + latest tags, context: containers/sandbox/)
- Added `km-sandbox` to `ecr-repos` repository list and `ecr-push` docker build sequence
- Implemented `buildAndPushSandboxImage` in init.go as Step 2a, matching the warn-and-continue error pattern of existing sidecar functions

## Task Commits

Each task was committed atomically:

1. **Task 1: Add sandbox-image Makefile target and update ecr-repos/ecr-push** - `e9536bf` (feat)
2. **Task 2: Add sandbox image build+push to km init** - `f28912e` (feat)

**Plan metadata:** (docs commit below)

## Files Created/Modified

- `Makefile` - Added sandbox-image target, km-sandbox to ecr-repos/ecr-push
- `internal/app/cmd/init.go` - Added Step 2a and buildAndPushSandboxImage function

## Decisions Made

- Used `containers/sandbox/` as Docker build context (not repo root) — Dockerfile only COPYs from its own directory, consistent with the plan spec
- Corrected plan's placeholder field names (`AccountsApplication`, `Region`) to actual config fields (`ApplicationAccountID`, `PrimaryRegion`) — Rule 1 auto-fix
- Function accepts `*config.Config` directly — plan referenced non-existent `*config.PlatformConfig` type

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Corrected config field names in buildAndPushSandboxImage**
- **Found during:** Task 2 (implement buildAndPushSandboxImage)
- **Issue:** Plan spec used `cfg.AccountsApplication` and `cfg.Region` but config struct fields are `cfg.ApplicationAccountID` and `cfg.PrimaryRegion`; plan also referenced `*config.PlatformConfig` which does not exist
- **Fix:** Used correct field names and `*config.Config` as function parameter type
- **Files modified:** internal/app/cmd/init.go
- **Verification:** `go build -o /dev/null ./cmd/km/` succeeds
- **Committed in:** f28912e (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 bug — wrong field names from plan spec)
**Impact on plan:** Necessary for compilation. No scope creep.

## Issues Encountered

None beyond the config field name correction above.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- km-sandbox build pipeline complete; image can be built locally with `make sandbox-image` and distributed to ECR via `make ecr-push` or `km init`
- ECS task definitions can reference `<account>.dkr.ecr.<region>.amazonaws.com/km-sandbox:<version>` or `:latest`
- Phase 36 plans 01-03 complete; ECS substrate can now use the sandbox base image

---
*Phase: 36-km-sandbox-base-container-image*
*Completed: 2026-03-31*
