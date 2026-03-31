---
phase: 36-km-sandbox-base-container-image
plan: "01"
subsystem: infra
tags: [docker, amazonlinux, containers, entrypoint, bash, gosu, aws-cli]

# Dependency graph
requires:
  - phase: pkg/compiler/userdata.go
    provides: EC2 bootstrap template — source of truth for entrypoint.sh sections

provides:
  - containers/sandbox/Dockerfile — buildable km-sandbox image on amazonlinux:2023
  - containers/sandbox/entrypoint.sh — container entrypoint porting all EC2 user-data sections

affects:
  - 36-02 (ECS service compiler references MAIN_IMAGE_PLACEHOLDER — resolved by this image)
  - 37-docker-compose (local sandbox testing uses this image)
  - 38-eks (Kubernetes pod spec uses this image)

# Tech tracking
tech-stack:
  added:
    - amazonlinux:2023 (Docker base image)
    - gosu 1.17 (privilege drop from root to UID 1000 sandbox user)
    - AWS CLI v2 (official zip install)
  patterns:
    - Container entrypoint mirrors EC2 user-data section numbering for cross-reference
    - Critical sections (CA trust, secrets) use log_fail to abort; optional sections use log_warn
    - env vars exported at root level then saved to /etc/profile.d/km-env.sh for sandbox user inheritance
    - exec gosu sandbox as final instruction replaces PID 1 with user process

key-files:
  created:
    - containers/sandbox/Dockerfile
    - containers/sandbox/entrypoint.sh
  modified: []

key-decisions:
  - "Used amazonlinux:2023 (not 2023-minimal): 2023-minimal tag is not available on Docker Hub"
  - "Used --allowerasing in dnf install: amazonlinux:2023 ships curl-minimal which conflicts with curl package"
  - "No SSM agent in container: km shell uses docker exec / kubectl exec per locked decision"
  - "Mail poller and upload-artifacts are bash functions in entrypoint.sh, not Go binaries per locked decision"

patterns-established:
  - "Section ordering in entrypoint.sh mirrors userdata.go section numbers for easy cross-reference"
  - "Critical steps (CA trust, inject_secrets) call log_fail to abort; optional steps call log_warn and return 0"
  - "KM_* env vars written to /etc/profile.d/km-env.sh so interactive shells started by gosu inherit them"

requirements-completed:
  - PROV-09
  - PROV-10

# Metrics
duration: 4min
completed: 2026-03-31
---

# Phase 36 Plan 01: km-sandbox Base Container Image Summary

**amazonlinux:2023 Docker image with AWS CLI v2, gosu, and 429-line entrypoint.sh that ports all EC2 user-data sections (CA trust, secrets, OTP, profile env, GitHub, git ref enforcement, rsync, initCommands, mail poller, user drop)**

## Performance

- **Duration:** 4 min
- **Started:** 2026-03-31T01:40:24Z
- **Completed:** 2026-03-31T01:44:24Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments
- Buildable `km-sandbox:test` Docker image on `amazonlinux:2023` with sandbox user (UID 1000), `/workspace`, AWS CLI v2, gosu, git, and jq
- 429-line `entrypoint.sh` faithfully porting all EC2 user-data sections as discrete bash functions driven by KM_* env vars
- Verified: `docker run --rm km-sandbox:test whoami` outputs `sandbox`, `which aws` outputs `/usr/local/bin/aws`
- SIGTERM handler uploads /workspace artifacts to S3 before exit, mirroring spot interruption handler from userdata.go

## Task Commits

Each task was committed atomically:

1. **Task 1: Create containers/sandbox/Dockerfile** - `de1a376` (feat)
2. **Task 2: Create containers/sandbox/entrypoint.sh** - `b75dc12` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified
- `containers/sandbox/Dockerfile` - amazonlinux:2023 base image with system packages, AWS CLI v2, gosu, sandbox user, and entrypoint
- `containers/sandbox/entrypoint.sh` - Container entrypoint with 10 sections porting EC2 user-data logic

## Decisions Made
- `amazonlinux:2023` used instead of `amazonlinux:2023-minimal` — the `-minimal` tag is not published to Docker Hub
- `dnf install --allowerasing` used — the base image ships `curl-minimal` which conflicts with the full `curl` package
- No SSM agent included — per locked decision, `km shell` uses `docker exec` / `kubectl exec`
- Mail poller and artifact upload are bash functions, not Go binaries — per locked decision

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Changed base image tag from `amazonlinux:2023-minimal` to `amazonlinux:2023`**
- **Found during:** Task 1 (Dockerfile build verification)
- **Issue:** `amazonlinux:2023-minimal` tag does not exist on Docker Hub; build failed with "not found"
- **Fix:** Changed `FROM amazonlinux:2023-minimal` to `FROM amazonlinux:2023`
- **Files modified:** containers/sandbox/Dockerfile
- **Verification:** `docker build --platform linux/amd64 -t km-sandbox:test` succeeded
- **Committed in:** de1a376 (Task 1 commit)

**2. [Rule 3 - Blocking] Added `--allowerasing` to dnf install**
- **Found during:** Task 1 (Dockerfile build, second attempt)
- **Issue:** `amazonlinux:2023` base ships `curl-minimal` which conflicts with the `curl` package; build failed with package conflict error
- **Fix:** Added `--allowerasing` flag to `dnf install -y` to allow replacement of `curl-minimal` with full `curl`
- **Files modified:** containers/sandbox/Dockerfile
- **Verification:** `docker build` succeeded; `docker run --rm km-sandbox:test which aws` confirmed AWS CLI installed
- **Committed in:** de1a376 (Task 1 commit)

---

**Total deviations:** 2 auto-fixed (2 blocking)
**Impact on plan:** Both fixes necessary to build the image at all. No scope creep — only corrected the base image tag and package conflict.

## Issues Encountered
- Docker Hub does not host `amazonlinux:2023-minimal`; `amazonlinux:2023` is the correct publicly available tag. The `-minimal` variant may be available on ECR Public (`public.ecr.aws/amazonlinux/amazonlinux:2023-minimal`) but plan specified building locally.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- `containers/sandbox/Dockerfile` and `entrypoint.sh` are ready for Phase 36-02 (ECS service compiler) to reference as the main sandbox image
- Image builds successfully with `docker build --platform linux/amd64 -t km-sandbox:test -f containers/sandbox/Dockerfile containers/sandbox/`
- entrypoint.sh passes bash syntax check and contains all required KM_* env var contract sections

---
*Phase: 36-km-sandbox-base-container-image*
*Completed: 2026-03-31*
