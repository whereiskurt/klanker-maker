---
phase: 37-docker-compose-local-substrate
plan: "03"
subsystem: cli-cmd
tags: [docker, docker-compose, substrate, shell, stop, pause, roll, lifecycle]

# Dependency graph
requires:
  - phase: 37-docker-compose-local-substrate
    plan: "02"
    provides: docker_helpers.go with runDockerCompose() and dockerComposePath()

provides:
  - execDockerShell() in shell.go — docker exec -it km-{id}-main /bin/bash with optional -u root
  - case "docker": in shell.go substrate switch
  - docker substrate detection in stop.go — routes to docker compose stop via S3 metadata read
  - docker substrate detection in pause.go — routes to docker compose pause via S3 metadata read
  - case "docker": in roll.go restartProxiesForSandboxes — skips with info message
  - shell_docker_test.go — 4 unit tests for docker exec arg construction and routing
  - stop_pause_docker_test.go — 4 unit tests for stop/pause source-level routing verification

affects:
  - km shell — docker substrate now fully wired (no SSM or ECS Exec involvement)
  - km stop — docker substrate detected via S3 metadata read, routes to docker compose stop
  - km pause — docker substrate detected via S3 metadata read, routes to docker compose pause
  - km roll proxies — docker sandboxes gracefully skipped with informational message

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "execDockerShell uses ShellExecFunc DI — same pattern as execSSMSession and execECSCommand"
    - "Container name derived as km-{sandboxID}-main — matches compose template container_name convention"
    - "Docker substrate detection in stop/pause: read S3 metadata early, return before EC2 API calls"
    - "Roll proxies skip pattern: case docker in substrate switch with non-fatal skip message"

key-files:
  created:
    - internal/app/cmd/shell_docker_test.go
    - internal/app/cmd/stop_pause_docker_test.go
  modified:
    - internal/app/cmd/shell.go
    - internal/app/cmd/stop.go
    - internal/app/cmd/pause.go
    - internal/app/cmd/roll.go

key-decisions:
  - "execDockerShell uses ShellExecFunc injection — consistent with EC2 and ECS exec patterns, fully testable"
  - "Container name km-{sandboxID}-main hardcoded — matches fixed container_name set in compose template by Plan 01"
  - "stop/pause detect substrate via S3 metadata read (same pattern as destroy.go Step 2c) — docker sandboxes have no tagged EC2 resources"
  - "roll proxies skips docker with info message — no SSM command to send to local containers"
  - "Tests use source-inspection pattern for stop/pause (consistent with destroy_docker_test.go) and DI pattern for shell"

requirements-completed: []

# Metrics
duration: ~8min
completed: 2026-03-31
---

# Phase 37 Plan 03: Docker Substrate Shell/Stop/Pause/Roll CLI Paths Summary

**execDockerShell() routes km shell to docker exec, and stop/pause detect docker substrate via S3 metadata to call docker compose stop/pause instead of EC2 APIs — no AWS calls for local docker sandboxes**

## Performance

- **Duration:** ~8 min
- **Completed:** 2026-03-31
- **Tasks:** 3
- **Files modified:** 6 (shell.go, stop.go, pause.go, roll.go modified; shell_docker_test.go, stop_pause_docker_test.go created)

## Accomplishments

- `execDockerShell()` in shell.go: builds `docker exec -it [(-u root)] km-{sandboxID}-main /bin/bash`, uses ShellExecFunc DI for testability, container name derived from sandbox ID using `km-{id}-main` convention
- `case "docker":` added to substrate switch in `runShell()` — routes before the default error case
- `runStop()` in stop.go: reads S3 metadata early using `awspkg.ReadSandboxMetadata()` (same as destroy.go pattern), routes to `runDockerCompose(ctx, sandboxID, "stop")` and returns before EC2 DescribeInstances/StopInstances calls
- `runPause()` in pause.go: same S3 metadata routing pattern, calls `runDockerCompose(ctx, sandboxID, "pause")`, returns before EC2 hibernate path
- `restartProxiesForSandboxes()` in roll.go: `case "docker":` added to substrate switch, logs skip message and continues (non-fatal, no SSM command needed for local containers)
- 8 unit tests verify substrate routing: 4 shell tests using ShellExecFunc DI capture docker exec args, 4 stop/pause tests using source inspection verify routing patterns and shared helper usage

## Task Commits

Each task was committed atomically:

1. **Task 1: Add docker substrate to shell.go** - `830f1bf` (feat)
2. **Task 2: Add docker substrate to stop.go, pause.go, and roll.go** - `deac062` (feat)
3. **Task 3: Write unit tests for docker shell/stop/pause substrate routing** - `5df66cb` (test)

## Files Created/Modified

- `internal/app/cmd/shell.go` — case "docker" in substrate switch, execDockerShell() function
- `internal/app/cmd/stop.go` — S3 metadata check + docker routing before EC2 path; added os and s3 imports
- `internal/app/cmd/pause.go` — S3 metadata check + docker routing before EC2 path; added os import
- `internal/app/cmd/roll.go` — case "docker" in restartProxiesForSandboxes switch with skip message
- `internal/app/cmd/shell_docker_test.go` — TestShellDockerContainerName, TestShellDockerRootFlag, TestShellDockerNoRootFlag, TestShellDockerRouting
- `internal/app/cmd/stop_pause_docker_test.go` — TestStopDockerRouting, TestPauseDockerRouting, TestStopDockerUsesSharedHelper, TestPauseDockerUsesSharedHelper

## Decisions Made

- execDockerShell uses ShellExecFunc injection — consistent with EC2 and ECS exec patterns, allows test capture of docker exec args without actually running docker
- Container name `km-{sandboxID}-main` hardcoded — matches fixed `container_name` set in compose template by Plan 01 compiler output
- stop/pause detect substrate via S3 metadata read using same pattern as destroy.go Step 2c — docker sandboxes have no AWS-tagged EC2 resources, so DescribeInstances would always return 0 results
- roll proxies skips docker with info message — no SSM command dispatch is possible for local containers; operator can manually restart if needed
- Tests use source-inspection for stop/pause (consistent with existing destroy_docker_test.go) and ShellExecFunc DI for shell tests (consistent with existing shell_test.go)

## Deviations from Plan

None - plan executed exactly as written. The implementation followed the specified patterns without requiring any auto-fixes or architectural changes.

## Self-Check: PASSED

- FOUND: internal/app/cmd/shell_docker_test.go
- FOUND: internal/app/cmd/stop_pause_docker_test.go
- FOUND commit 830f1bf: feat(37-03): shell.go docker case
- FOUND commit deac062: feat(37-03): stop/pause/roll docker cases
- FOUND commit 5df66cb: test(37-03): unit tests
