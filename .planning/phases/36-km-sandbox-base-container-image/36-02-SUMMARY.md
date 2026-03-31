---
phase: 36-km-sandbox-base-container-image
plan: "02"
subsystem: compiler
tags: [ecs, container-image, entrypoint, env-vars, tdd]
dependency_graph:
  requires:
    - "36-01: km-sandbox container image and entrypoint.sh"
  provides:
    - "ECS task definition with real sandbox image URI and entrypoint env vars"
  affects:
    - "pkg/compiler/service_hcl.go"
    - "pkg/compiler/service_hcl_test.go"
tech_stack:
  added: ["encoding/base64", "encoding/json (in service_hcl.go)"]
  patterns: ["TDD red-green", "conditional HCL template blocks", "base64-encoded JSON env vars"]
key_files:
  modified:
    - path: "pkg/compiler/service_hcl.go"
      role: "ECS HCL template — replaced MAIN_IMAGE_PLACEHOLDER, added KM_* entrypoint env vars"
    - path: "pkg/compiler/service_hcl_test.go"
      role: "New TDD tests for sandbox image URI and KM_* env vars"
decisions:
  - "Moved mainImage assignment to after sidecarImage closure definition so sidecarImage('sandbox') can be called"
  - "KM_SANDBOX_ID and KM_ARTIFACTS_BUCKET are always emitted; all other KM_* vars use conditional {{- if .Field }} blocks"
  - "KM_INIT_COMMANDS and KM_PROFILE_ENV are base64-encoded JSON (array and object respectively)"
  - "KM_GITHUB_TOKEN_SSM and KM_GITHUB_ALLOWED_REFS reuse the existing HasGitHub/GitHubSSMPath fields already populated"
metrics:
  duration: "136s"
  completed_date: "2026-03-31"
  tasks_completed: 2
  files_modified: 2
requirements_satisfied:
  - PROV-09
  - PROV-10
---

# Phase 36 Plan 02: ECS Compiler — Sandbox Image URI and KM_* Env Vars Summary

**One-liner:** ECS task compiler now emits real km-sandbox ECR URI and all KM_* entrypoint env vars required by the Phase 36 container entrypoint.sh.

## What Was Built

The ECS service.hcl compiler (`generateECSServiceHCL`) had a placeholder `MAIN_IMAGE_PLACEHOLDER` for the main container image and no mechanism to pass configuration to the entrypoint script. This plan fixed both:

1. **Real image URI:** `mainImage` now calls `sidecarImage("sandbox")` — the same ECR registry/tag pattern used by all other sidecars (dns-proxy, http-proxy, audit-log, tracing). The image is `{accountId}.dkr.ecr.{region}.amazonaws.com/km-sandbox:{KM_SIDECAR_VERSION}`.

2. **KM_* entrypoint env vars:** 11 new fields added to `ecsHCLParams`, populated in `generateECSServiceHCL`, and emitted in the HCL template's main container `environment` block:
   - `KM_SANDBOX_ID`, `KM_ARTIFACTS_BUCKET` — always present
   - `KM_PROXY_CA_CERT_S3` — when artifacts bucket is known
   - `KM_SECRET_PATHS` — from `identity.allowedSecretPaths`
   - `KM_OTP_PATHS` — from `otp.secrets`
   - `KM_INIT_COMMANDS` — base64-encoded JSON array from `execution.initCommands`
   - `KM_RSYNC_SNAPSHOT` — snapshot name (if set)
   - `KM_GITHUB_TOKEN_SSM`, `KM_GITHUB_ALLOWED_REFS` — when GitHub access configured
   - `KM_PROFILE_ENV` — base64-encoded JSON map from `execution.env`
   - `KM_OPERATOR_EMAIL` — from env var

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Add failing tests (TDD RED) | 1f68b1b | pkg/compiler/service_hcl_test.go |
| 2 | Replace placeholder and add KM_* vars (GREEN) | 1eca416 | pkg/compiler/service_hcl.go |

## Verification

- `go test ./pkg/compiler/... -run TestECS` — 23 tests pass (all existing + 5 new)
- `go build -o /dev/null ./cmd/km/` — compiles successfully
- `grep -c "MAIN_IMAGE_PLACEHOLDER" pkg/compiler/service_hcl.go` — returns 1 (only in comment, not code)
- `grep "km-sandbox" pkg/compiler/service_hcl.go` — shows image reference
- `grep "KM_PROXY_CA_CERT_S3" pkg/compiler/service_hcl.go` — env var in template

## Deviations from Plan

### Auto-fixed Issues

None — plan executed exactly as written, with one minor ordering adjustment: the plan suggested moving `mainImage` after `sidecarImage`, which was done by removing the old `mainImage` block and placing `mainImage := sidecarImage("sandbox")` after the `sidecarImage` closure definition.

## Self-Check: PASSED

Files exist:
- `pkg/compiler/service_hcl.go` — FOUND
- `pkg/compiler/service_hcl_test.go` — FOUND

Commits exist:
- `1f68b1b` — FOUND (TDD RED tests)
- `1eca416` — FOUND (GREEN implementation)
