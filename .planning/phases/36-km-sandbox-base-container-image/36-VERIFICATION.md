---
phase: 36-km-sandbox-base-container-image
verified: 2026-03-30T08:00:00Z
status: passed
score: 12/12 must-haves verified
re_verification: false
---

# Phase 36: km-sandbox Base Container Image Verification Report

**Phase Goal:** A `km-sandbox` base container image that provides the same sandbox environment as EC2 user-data — proxy CA trust, secret injection, GitHub credentials, initCommands, rsync restore, OTEL telemetry, and mail polling — all driven by environment variables via a container entrypoint script. This is the foundation for both Docker local and EKS substrates.
**Verified:** 2026-03-30
**Status:** PASSED
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | A km-sandbox Docker image can be built from containers/sandbox/Dockerfile | VERIFIED | `containers/sandbox/Dockerfile` exists (66 lines), uses `amazonlinux:2023` base, builds to km-sandbox:test (confirmed in SUMMARY-01) |
| 2 | The entrypoint script mirrors EC2 user-data sections: CA trust, secrets, OTP, profile env, GitHub, git ref enforcement, rsync, initCommands, mail poller, user drop | VERIFIED | `containers/sandbox/entrypoint.sh` (429 lines) contains `setup_ca_trust`, `inject_secrets`, `inject_otp_secrets`, `setup_profile_env`, `setup_github_credentials`, `setup_git_ref_enforcement`, `restore_rsync_snapshot`, `run_init_commands`, `start_mail_poller`, `exec gosu sandbox` |
| 3 | Critical steps (CA trust, secrets) abort on failure; optional steps (rsync, mail, GitHub) warn and continue | VERIFIED | CA trust and `inject_secrets` call `log_fail` (abort). All optional steps call `log_warn` and return 0 on failure. Confirmed by grep. |
| 4 | The entrypoint drops from root to sandbox user via gosu before handing off to bash | VERIFIED | Line 429: `exec gosu sandbox "${@:-/bin/bash}"` |
| 5 | SIGTERM triggers artifact upload before exit | VERIFIED | Lines 52-57: `_shutdown()` function calls `upload_artifacts` then `exit 0`; `trap '_shutdown' TERM INT` installed |
| 6 | generateECSServiceHCL emits real km-sandbox ECR URI instead of MAIN_IMAGE_PLACEHOLDER | VERIFIED | `service_hcl.go:670: mainImage := sidecarImage("sandbox")` — no code-level MAIN_IMAGE_PLACEHOLDER remains (only in a comment) |
| 7 | ECS main container environment block includes all KM_* entrypoint env vars needed by entrypoint.sh | VERIFIED | Template lines 153-180 emit KM_SANDBOX_ID, KM_ARTIFACTS_BUCKET, KM_PROXY_CA_CERT_S3, KM_SECRET_PATHS, KM_OTP_PATHS, KM_INIT_COMMANDS, KM_RSYNC_SNAPSHOT, KM_GITHUB_TOKEN_SSM, KM_GITHUB_ALLOWED_REFS, KM_PROFILE_ENV, KM_OPERATOR_EMAIL |
| 8 | Existing ECS compiler tests still pass | VERIFIED | `go test ./pkg/compiler/... -run TestECS` — all 23 tests pass (0.460s) |
| 9 | make sandbox-image builds the km-sandbox Docker image locally | VERIFIED | `Makefile:82-88` defines `sandbox-image` target with `--load`, `containers/sandbox/` context |
| 10 | make ecr-repos creates the km-sandbox ECR repository | VERIFIED | `Makefile:108` adds `km-sandbox` to the `ecr-repos` for-loop |
| 11 | make ecr-push builds and pushes km-sandbox to ECR alongside sidecar images | VERIFIED | `Makefile:184-185` adds km-sandbox to `ecr-push` with versioned and latest tags |
| 12 | km init builds and pushes the sandbox image to ECR as part of regional initialization | VERIFIED | `internal/app/cmd/init.go:210-216` Step 2a calls `buildAndPushSandboxImage` |

**Score:** 12/12 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `containers/sandbox/Dockerfile` | AL2023 base image with git, jq, AWS CLI v2, gosu, sandbox user | VERIFIED | 66 lines; `FROM amazonlinux:2023`; installs shadow-utils, tar, gzip, unzip, jq, git, curl, AWS CLI v2, gosu 1.17; creates sandbox UID 1000; `/workspace`; ENTRYPOINT set |
| `containers/sandbox/entrypoint.sh` | Container entrypoint replacing EC2 user-data bootstrap | VERIFIED | 429 lines (exceeds min_lines: 200); contains `[km-entrypoint]` log prefix; all 10 sections present; passes `bash -n` syntax check |
| `pkg/compiler/service_hcl.go` | ECS task definition with real sandbox image URI and entrypoint env vars | VERIFIED | `sidecarImage("sandbox")` at line 670; 11 KM_* env var fields in struct and template |
| `pkg/compiler/service_hcl_test.go` | Tests verifying sandbox image URI and KM_* env vars in ECS output | VERIFIED | Contains `TestECSServiceHCLSandboxImage`, `TestECSMainContainerEntrypointEnvVars`, `TestECSMainContainerInitCommands`, `TestECSMainContainerGitHubEnvVars`, `TestECSMainContainerSecretPaths` |
| `Makefile` | sandbox-image target and km-sandbox in ecr-repos and ecr-push | VERIFIED | Lines 82-92 (sandbox-image, smoke-test-sandbox), line 108 (ecr-repos), lines 184-185 (ecr-push); `sandbox-image` in .PHONY |
| `internal/app/cmd/init.go` | Sandbox image build+push in km init pipeline | VERIFIED | `buildAndPushSandboxImage` function at line 714; Step 2a at lines 210-216 |
| `scripts/smoke-test-sandbox.sh` | Automated smoke test for km-sandbox container image | VERIFIED | 213 lines (exceeds min_lines: 30); executable; contains all 6 tests; references `km-sandbox` |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `containers/sandbox/entrypoint.sh` | `pkg/compiler/userdata.go` | functional port — same section numbering and logic | WIRED | `setup_ca_trust`, `inject_secrets`, `run_init_commands` all present; section comments reference userdata.go section numbers |
| `pkg/compiler/service_hcl.go` | `containers/sandbox/entrypoint.sh` | KM_* environment variables in ECS task definition consumed by entrypoint | WIRED | `KM_PROXY_CA_CERT_S3`, `KM_SECRET_PATHS`, `KM_INIT_COMMANDS` confirmed present in both HCL template and entrypoint.sh |
| `Makefile` | `containers/sandbox/Dockerfile` | `docker buildx build --file containers/sandbox/Dockerfile` | WIRED | `containers/sandbox/` build context confirmed in sandbox-image and ecr-push targets |
| `internal/app/cmd/init.go` | `containers/sandbox/Dockerfile` | `buildAndPushSandboxImage` function | WIRED | Function builds from `containers/sandbox/Dockerfile` path at line 749; called in Step 2a |
| `scripts/smoke-test-sandbox.sh` | `containers/sandbox/Dockerfile` | Builds and runs the image to verify entrypoint behavior | WIRED | Line 28: `docker build --platform linux/amd64 -t "${IMAGE}" containers/sandbox/` |

### Requirements Coverage

| Requirement | Source Plans | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| PROV-09 | 36-01, 36-02, 36-03, 36-04 | Operator can specify substrate (ec2 or ecs) in the profile's runtime.substrate field and km create provisions the corresponding infrastructure | SATISFIED | km-sandbox image enables ECS substrate; ECS compiler now emits real image URI instead of placeholder; both ec2 and ecs substrates have working main container definitions |
| PROV-10 | 36-01, 36-02, 36-03, 36-04 | ECS substrate provisions an AWS Fargate task with sidecar containers for enforcement (DNS proxy, HTTP proxy, audit log) defined in the task definition | SATISFIED | service_hcl.go updated to include km-sandbox main container with all KM_* env vars; sidecar containers remain in task definition; MAIN_IMAGE_PLACEHOLDER removed so task definitions are now launchable |

Note: REQUIREMENTS.md maps PROV-09 and PROV-10 to Phase 2 (Complete) — Phase 36 extends and completes the container substrate aspect of these requirements. No orphaned requirements found for Phase 36.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `pkg/compiler/service_hcl.go` | 414, 661 | `PLACEHOLDER_ECR` | INFO | Expected fallback when `KM_ACCOUNTS_APPLICATION` env var is unset (used in tests without real AWS account). Not a stub — intentional graceful degradation. |

No blocker or warning anti-patterns found.

### Human Verification Required

#### 1. Docker Build on linux/amd64

**Test:** `docker build --platform linux/amd64 -t km-sandbox:test -f containers/sandbox/Dockerfile containers/sandbox/`
**Expected:** Build succeeds; `docker run --rm km-sandbox:test whoami` outputs `sandbox`
**Why human:** Requires Docker daemon with buildx and linux/amd64 platform support. Can be confirmed by running `make sandbox-image` locally.

#### 2. Full Smoke Test Suite

**Test:** `make smoke-test-sandbox`
**Expected:** All 6 tests pass (minimal boot, env passthrough, initCommands, SIGTERM, user/workspace, tools)
**Why human:** Tests run Docker containers; SIGTERM test requires a running container. The SUMMARY-04 reports all 6 passed on the author's machine.

#### 3. km init ECR Push Integration

**Test:** Run `km init` in a configured environment and verify `km-sandbox:latest` appears in ECR
**Expected:** ECR repository `km-sandbox` exists with pushed image after `km init`
**Why human:** Requires real AWS credentials and an ECR registry. Code path is wired (Step 2a in init.go) but live execution cannot be verified statically.

### Gaps Summary

No gaps found. All 12 must-have truths are verified with substantive, wired artifacts. The go build passes, compiler tests all pass, and key links are confirmed present at both ends. The only deviation from plan specs (Dockerfile uses `amazonlinux:2023` instead of `amazonlinux:2023-minimal`) is correctly documented in SUMMARY-01 as a necessary auto-fix because the `-minimal` tag is not published to Docker Hub.

---

_Verified: 2026-03-30_
_Verifier: Claude (gsd-verifier)_
