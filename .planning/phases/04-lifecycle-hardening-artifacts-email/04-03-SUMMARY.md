---
phase: 04-lifecycle-hardening-artifacts-email
plan: 03
subsystem: compiler
tags: [ec2, ecs, fargate, spot, filesystem, bind-mount, terraform, lambda, eventbridge, userdata, teardown]

requires:
  - phase: 04-01
    provides: ArtifactsSpec, UploadArtifacts, S3PutAPI — wired into compiler templates and teardown
  - phase: 03-04
    provides: TeardownCallbacks, ExecuteTeardown — extended with UploadArtifacts callback

provides:
  - EC2 user-data with bind mounts (section 2.5), spot poll loop (section 6.5), artifact upload script
  - ECS service.hcl with readonlyRootFilesystem=true, named volumes, auto-injected /tmp
  - TeardownCallbacks.UploadArtifacts callback called before Destroy/Stop for all policies
  - ECS Fargate spot interruption handler: EventBridge rule + Lambda (infra/modules/ecs-spot-handler/v1.0.0)

affects: [phase-04-04, phase-05]

tech-stack:
  added: [hashicorp/archive provider (Lambda zip packaging), Python 3.12 Lambda runtime]
  patterns:
    - ECS named volumes with scope=task for writable paths (Fargate tmpfs workaround)
    - Two-step bind mount (mount --bind then remount,bind,ro) for Linux read-only enforcement
    - IMDS token TTL 21600s to survive spot poll loop duration
    - Best-effort artifact upload before all teardown policies (log warning, never block)
    - ECS Exec to trigger artifact upload in stopping Fargate container

key-files:
  created:
    - pkg/compiler/userdata_test.go
    - pkg/compiler/service_hcl_test.go
    - pkg/lifecycle/teardown_test.go
    - infra/modules/ecs-spot-handler/v1.0.0/main.tf
    - infra/modules/ecs-spot-handler/v1.0.0/variables.tf
    - infra/modules/ecs-spot-handler/v1.0.0/outputs.tf
    - infra/modules/ecs-spot-handler/v1.0.0/lambda/handler.py
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/service_hcl.go
    - pkg/compiler/compiler.go
    - pkg/lifecycle/teardown.go

key-decisions:
  - "IMDS token TTL changed from 60s to 21600s — spot poll loop runs for hours, 60s token would expire"
  - "Two-step bind mount required: Linux kernel disallows ro flag on initial mount --bind; remount,bind,ro is required second step"
  - "ECS Fargate writable volumes use scope=task named volumes, not linuxParameters.tmpfs — Fargate does not support tmpfs"
  - "UploadArtifacts called for ALL teardown policies including retain — data preservation is always desired"
  - "ECS spot handler uses ECS Exec (not marker file) to trigger upload — reuses same km-upload-artifacts script as EC2"
  - "volumeName() template function converts /path/to/dir to vol-path-to-dir for valid ECS volume name"

patterns-established:
  - "Filesystem enforcement: bind mounts in section 2.5 (before sidecars) ensure ro is applied before any process starts"
  - "Spot handling: section 6.5 background poll loop with IMDS termination-time endpoint, uploads artifacts on detection"
  - "ECS filesystem: HasFilesystemPolicy bool controls readonlyRootFilesystem + EffectiveWritablePaths slice (includes auto-injected /tmp)"
  - "Teardown best-effort: UploadArtifacts failure logged via zerolog Warn, never returned as error"

requirements-completed: [OBSV-04, PROV-13]

duration: 5min
completed: 2026-03-22
---

# Phase 04 Plan 03: Filesystem Enforcement, Spot Handling, and ECS Spot Handler Summary

**EC2 bind-mount enforcement (section 2.5), spot poll loop with artifact upload (section 6.5), ECS readonlyRootFilesystem + named volumes, UploadArtifacts callback in teardown, and EventBridge+Lambda ECS Fargate spot handler module**

## Performance

- **Duration:** 5 min
- **Started:** 2026-03-22T13:44:06Z
- **Completed:** 2026-03-22T13:49:06Z
- **Tasks:** 3 (Task 1 TDD: 2 commits; Task 2 TDD: 1 commit; Task 3: 1 commit)
- **Files modified:** 10 (4 new test files, 4 modified source files, 4 new Terraform/Lambda files)

## Accomplishments

- EC2 user-data now enforces read-only bind mounts before sidecars start, spot poll loop detects IMDS termination signal every 5s with artifact upload on detection, IMDS token TTL corrected to 21600s
- ECS service.hcl sets readonlyRootFilesystem=true on the main container when FilesystemPolicy is configured, adds named volumes with scope=task for each writable path, auto-injects /tmp as writable
- TeardownCallbacks extended with UploadArtifacts (best-effort, called for all teardown policies before dispatch)
- ECS Fargate spot interruption handler Terraform module: EventBridge rule watching ECS TASK_STOPPING+SpotInterruption, Lambda (Python 3.12) calling ECS Exec to trigger km-upload-artifacts inside stopping container

## Task Commits

1. **Test RED: Filesystem enforcement + spot + teardown** - `64b8832` (test)
2. **Task 1+2 GREEN: userdata.go, service_hcl.go, teardown.go** - `cdc73f7` (feat)
3. **Task 3: ECS spot handler Terraform module** - `333f0c3` (feat)

## Files Created/Modified

- `pkg/compiler/userdata.go` - IMDS TTL 21600, section 2.5 bind mounts, section 6.5 spot handler, `useSpot` param
- `pkg/compiler/service_hcl.go` - `HasFilesystemPolicy`, `EffectiveWritablePaths`, `volumeName()` template fn, ECS volumes/mountPoints/readonlyRootFilesystem
- `pkg/compiler/compiler.go` - pass `useSpot` to `generateUserData`
- `pkg/lifecycle/teardown.go` - `UploadArtifacts` field in `TeardownCallbacks`, best-effort call before policy dispatch
- `pkg/compiler/userdata_test.go` - 8 tests for IMDS TTL, bind mounts, spot loop, artifact script
- `pkg/compiler/service_hcl_test.go` - 4 tests for ECS readonlyRootFilesystem, volumes, /tmp auto-injection
- `pkg/lifecycle/teardown_test.go` - 9 tests for upload ordering, best-effort, all policies
- `infra/modules/ecs-spot-handler/v1.0.0/main.tf` - EventBridge rule, Lambda function, IAM role/policies, Lambda permission
- `infra/modules/ecs-spot-handler/v1.0.0/variables.tf` - ecs_cluster_arn, artifact_bucket_name, artifact_bucket_arn
- `infra/modules/ecs-spot-handler/v1.0.0/outputs.tf` - event_rule_arn, lambda_function_arn, lambda_role_arn
- `infra/modules/ecs-spot-handler/v1.0.0/lambda/handler.py` - Python Lambda handler using ECS Exec

## Decisions Made

- IMDS token TTL changed from 60s to 21600s: spot poll loop runs for hours, 60s token would expire mid-loop
- Two-step bind mount required (`mount --bind` then `mount -o remount,bind,ro`): Linux kernel constraint, ro cannot be set on initial bind
- ECS Fargate writable volumes use `scope=task` named volumes, not `linuxParameters.tmpfs`: Fargate does not support tmpfs mounts
- `UploadArtifacts` called for ALL teardown policies including retain: data preservation is always desired regardless of teardown intent
- ECS spot handler uses ECS Exec (not marker file or separate mechanism): reuses same `km-upload-artifacts` script from EC2 path

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required. The ECS spot handler module is deployed at cluster setup time (once per cluster).

## Next Phase Readiness

- Filesystem enforcement (PROV-13), spot artifact upload (OBSV-04) complete for both EC2 and ECS substrates
- ECS spot handler module ready to be wired into live Terragrunt hierarchy in Phase 5
- The `TeardownCallbacks.UploadArtifacts` callback is ready for wiring in the `km destroy` command path

---
*Phase: 04-lifecycle-hardening-artifacts-email*
*Completed: 2026-03-22*
