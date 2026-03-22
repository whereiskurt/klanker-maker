---
phase: 08-sidecar-build-deployment-pipeline
plan: 01
subsystem: infra
tags: [docker, makefile, ecr, s3, go, cross-compile, otelcol]

requires:
  - phase: 03-sidecar-enforcement-lifecycle-management
    provides: Go sidecar source code (dns-proxy, http-proxy, audit-log, tracing)

provides:
  - Makefile with sidecars (cross-compile + S3 upload) and ecr-push (Docker build + ECR push) targets
  - Multi-stage Dockerfiles for dns-proxy, http-proxy, audit-log (golang:1.25-alpine + scratch)
  - Tracing Dockerfile based on otelcol-contrib
  - build/ directory gitignored

affects:
  - EC2 sandbox boot (user-data downloads binaries from S3)
  - ECS sandbox task definitions (pull images from ECR)
  - CI/CD integration

tech-stack:
  added: []
  patterns:
    - "Multi-stage Docker build: golang:1.25-alpine builder + scratch final for minimal Go images"
    - "Repo root as Docker build context so sidecar Dockerfiles can access shared pkg/ imports"
    - "Makefile guard pattern: fail fast with clear error if required env var (KM_ARTIFACTS_BUCKET) is unset"

key-files:
  created:
    - Makefile
    - sidecars/dns-proxy/Dockerfile
    - sidecars/http-proxy/Dockerfile
    - sidecars/audit-log/Dockerfile
    - sidecars/tracing/Dockerfile
  modified:
    - .gitignore

key-decisions:
  - "tracing ecr-push uses sidecars/tracing/ as Docker build context (not repo root) — tracing is not a Go binary, no shared pkg/ imports needed"
  - "audit-log Dockerfile builds from ./sidecars/audit-log/cmd/ not ./sidecars/audit-log/ — cmd/ holds package main; root is package auditlog library"
  - "build-sidecars target added for local cross-compilation without S3 credentials"

patterns-established:
  - "CGO_ENABLED=0 GOOS=linux GOARCH=amd64: all Go sidecar binaries must be statically linked linux/amd64"
  - "ECR naming: km-{sidecar-name} (km-dns-proxy, km-http-proxy, km-audit-log, km-tracing)"

requirements-completed: [NETW-02, NETW-03, OBSV-01, OBSV-02]

duration: 2min
completed: 2026-03-22
---

# Phase 08 Plan 01: Sidecar Build and Deployment Pipeline Summary

**Root Makefile with sidecars/ecr-push targets and multi-stage Dockerfiles for all 4 sidecars enabling EC2 S3 binary delivery and ECS ECR image delivery**

## Performance

- **Duration:** 2 min
- **Started:** 2026-03-22T22:43:42Z
- **Completed:** 2026-03-22T22:44:59Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments

- Created multi-stage Dockerfiles for all 4 sidecars: dns-proxy, http-proxy, audit-log (golang:1.25-alpine + scratch) and tracing (otelcol-contrib)
- Created root Makefile with `sidecars` (cross-compile + S3 upload), `ecr-push` (Docker build + ECR push), `ecr-login`, `ecr-repos`, and `build-sidecars` targets
- Verified `make build-sidecars` successfully cross-compiles 3 linux/amd64 ELF binaries without Docker
- Updated .gitignore to exclude `build/` directory

## Task Commits

1. **Task 1: Create Dockerfiles for all 4 sidecars** - `d59cf7a` (feat)
2. **Task 2: Create root Makefile with sidecars and ecr-push targets** - `bc8bc8f` (feat)

## Files Created/Modified

- `Makefile` - Root build pipeline with sidecars, ecr-push, ecr-login, ecr-repos, build-sidecars targets
- `sidecars/dns-proxy/Dockerfile` - Multi-stage build: golang:1.25-alpine builder + scratch final, builds ./sidecars/dns-proxy/
- `sidecars/http-proxy/Dockerfile` - Multi-stage build: golang:1.25-alpine builder + scratch final, builds ./sidecars/http-proxy/
- `sidecars/audit-log/Dockerfile` - Multi-stage build: golang:1.25-alpine builder + scratch final, builds ./sidecars/audit-log/cmd/
- `sidecars/tracing/Dockerfile` - FROM otel/opentelemetry-collector-contrib:latest with config.yaml
- `.gitignore` - Added build/ directory entry

## Decisions Made

- Tracing `ecr-push` uses `sidecars/tracing/` as build context (not repo root) — tracing is an OTel config-only image, no Go source needed
- audit-log Dockerfile target is `./sidecars/audit-log/cmd/` not `./sidecars/audit-log/` — the library root is `package auditlog`, only `cmd/main.go` is `package main`
- `build-sidecars` target added for local development/testing without AWS credentials

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required for build pipeline itself. S3 upload and ECR push require `KM_ARTIFACTS_BUCKET` env var and AWS credentials at runtime.

## Next Phase Readiness

- EC2 sandboxes can now obtain sidecar binaries via `make sidecars` + S3 download in user-data
- ECS sandboxes can now use sidecar container images via `make ecr-push` + ECR pull in task definitions
- `make build-sidecars` verifies Go sidecar source compiles correctly without Docker or AWS access

---
*Phase: 08-sidecar-build-deployment-pipeline*
*Completed: 2026-03-22*
