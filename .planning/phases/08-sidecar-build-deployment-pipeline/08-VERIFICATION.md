---
phase: 08-sidecar-build-deployment-pipeline
verified: 2026-03-22T23:00:00Z
status: passed
score: 8/8 must-haves verified
re_verification: false
human_verification:
  - test: "Run make sidecars with a real KM_ARTIFACTS_BUCKET"
    expected: "3 linux/amd64 ELF binaries + tracing/config.yaml uploaded to s3://<bucket>/sidecars/"
    why_human: "Requires live AWS credentials and an existing S3 bucket — cannot verify programmatically"
  - test: "Run make ecr-push with a live AWS account"
    expected: "4 Docker images (km-dns-proxy, km-http-proxy, km-audit-log, km-tracing) pushed to ECR"
    why_human: "Requires Docker daemon, live AWS credentials, and ECR endpoint"
---

# Phase 8: Sidecar Build & Deployment Pipeline — Verification Report

**Phase Goal:** Sidecar binaries and container images are buildable and deployable via a single command — EC2 sandboxes can download sidecars from S3 at boot, ECS sandboxes pull sidecar images from ECR.
**Verified:** 2026-03-22T23:00:00Z
**Status:** PASSED
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (from ROADMAP.md Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `make sidecars` cross-compiles all sidecar artifacts for linux/amd64 and uploads to S3 | VERIFIED | Makefile lines 26-44: 3 Go binaries cross-compiled with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`; tracing config.yaml also uploaded; KM_ARTIFACTS_BUCKET guard present |
| 2 | `make ecr-push` builds Docker images for each sidecar and pushes to ECR | VERIFIED | Makefile lines 65-82: 4 `docker buildx build --platform linux/amd64 --push` invocations for dns-proxy, http-proxy, audit-log, tracing; depends on ecr-login + ecr-repos |
| 3 | Compiler emits resolvable ECR image URIs in ECS service.hcl (not literal `${var.*}` strings) | VERIFIED | `grep '${var.*_image}' service_hcl.go` returns no matches; template uses `{{ .DNSProxyImage }}` etc.; ECR URIs computed from `KM_ACCOUNTS_APPLICATION + region + KM_SIDECAR_VERSION`; all 10 ECS compiler tests pass |
| 4 | EC2 sandbox user-data can download sidecar binaries from S3 at boot | VERIFIED | Makefile uploads to `s3://<bucket>/sidecars/dns-proxy`, `http-proxy`, `audit-log`, `tracing/config.yaml`; userdata.go downloads from identical paths (`s3://${KM_ARTIFACTS_BUCKET}/sidecars/dns-proxy` etc.) — paths are aligned |
| 5 | Each Dockerfile uses multi-stage build with repo root as context (Go sidecars) | VERIFIED | All 3 Go sidecar Dockerfiles: `FROM golang:1.25-alpine AS builder`, `COPY . .`, final stage `FROM scratch`; Makefile ecr-push uses `-f sidecars/{name}/Dockerfile .` (repo root context) for Go sidecars |
| 6 | build/ directory is gitignored | VERIFIED | `.gitignore` line 23: `build/` |
| 7 | Compiler PLACEHOLDER_ECR fallback when KM_ACCOUNTS_APPLICATION is unset | VERIFIED | `service_hcl.go` lines 524-528: `ecrRegistry = "PLACEHOLDER_ECR"` when accountID is empty; `TestECSServiceHCLImageURIsPlaceholder` passes |
| 8 | Existing ECS compiler tests continue to pass | VERIFIED | `go test ./pkg/compiler/ -run TestECS -v`: all 10 tests PASS including 3 new image URI tests |

**Score:** 8/8 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `Makefile` | sidecars and ecr-push targets | VERIFIED | 82 lines; `sidecars`, `ecr-push`, `ecr-login`, `ecr-repos`, `build-sidecars` targets; `CGO_ENABLED=0`, KM_ARTIFACTS_BUCKET guard |
| `sidecars/dns-proxy/Dockerfile` | Multi-stage linux/amd64 Docker image | VERIFIED | 12 lines; golang:1.25-alpine builder + scratch final; builds `./sidecars/dns-proxy/` |
| `sidecars/http-proxy/Dockerfile` | Multi-stage linux/amd64 Docker image | VERIFIED | 12 lines; golang:1.25-alpine builder + scratch final; builds `./sidecars/http-proxy/` |
| `sidecars/audit-log/Dockerfile` | Multi-stage, builds from cmd/ | VERIFIED | 12 lines; golang:1.25-alpine builder + scratch final; builds `./sidecars/audit-log/cmd/` (correct — cmd/ holds package main) |
| `sidecars/tracing/Dockerfile` | OTel collector image with config | VERIFIED | 2 lines; `FROM otel/opentelemetry-collector-contrib:latest`; `COPY config.yaml /etc/otelcol-contrib/config.yaml` |
| `sidecars/tracing/config.yaml` | OTel collector configuration | VERIFIED | 803 bytes; referenced by tracing Dockerfile and uploaded by `make sidecars` |
| `pkg/compiler/service_hcl.go` | ECR URI computation in ecsHCLParams | VERIFIED | Fields DNSProxyImage/HTTPProxyImage/AuditLogImage/TracingImage added at line 338-341; computation via os.Getenv at lines 519-532 |
| `pkg/compiler/service_hcl_test.go` | TestECSServiceHCLImageURIs test | VERIFIED | 3 new tests at lines 203/230/256: TestECSServiceHCLImageURIs, TestECSServiceHCLImageURIsPlaceholder, TestECSServiceHCLImageVersion |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| Makefile `sidecars` target | `go build ./sidecars/{name}/` | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` | WIRED | Lines 32-34; cross-compiles dns-proxy, http-proxy, audit-log (audit-log correctly uses `./sidecars/audit-log/cmd/`) |
| Makefile `sidecars` target | `s3://<bucket>/sidecars/` | `aws s3 cp` | WIRED | Lines 35-38; uploads 3 binaries + tracing config.yaml |
| Makefile `ecr-push` target | `sidecars/*/Dockerfile` | `docker buildx build --file` | WIRED | Lines 67-82; all 4 Dockerfiles referenced; `--platform linux/amd64 --push` |
| `service_hcl.go generateECSServiceHCL` | `KM_ACCOUNTS_APPLICATION` env var | `os.Getenv` | WIRED | Line 519: `accountID := os.Getenv("KM_ACCOUNTS_APPLICATION")` |
| `ecsServiceHCLTemplate` | `ecsHCLParams` image fields | Go template interpolation | WIRED | Lines 153/169/185/210: `{{ .DNSProxyImage }}`, `{{ .HTTPProxyImage }}`, `{{ .AuditLogImage }}`, `{{ .TracingImage }}` |
| Makefile S3 upload paths | userdata.go S3 download paths | path alignment | WIRED | Upload: `sidecars/dns-proxy` / Download: `sidecars/dns-proxy` — paths identical for all 4 artifacts |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| NETW-02 | 08-01-PLAN.md | DNS proxy sidecar filters outbound DNS | SATISFIED | `sidecars/dns-proxy/Dockerfile` provides buildable+deployable DNS proxy image; `make sidecars` uploads binary to S3 for EC2 boot |
| NETW-03 | 08-01-PLAN.md | HTTP proxy sidecar filters outbound HTTP/S | SATISFIED | `sidecars/http-proxy/Dockerfile` provides buildable+deployable HTTP proxy image; `make sidecars` uploads binary to S3 |
| OBSV-01 | 08-01-PLAN.md | Audit log sidecar captures command execution logs | SATISFIED | `sidecars/audit-log/Dockerfile` builds from correct `cmd/` entry point; `make sidecars` uploads binary |
| OBSV-02 | 08-01-PLAN.md | Audit log sidecar captures network traffic logs | SATISFIED | Same artifact as OBSV-01 — audit-log binary covers both command and network log capture |
| PROV-10 | 08-02-PLAN.md | ECS substrate provisions Fargate task with sidecar containers | SATISFIED | ECS service.hcl now emits real ECR URIs (`{accountID}.dkr.ecr.{region}.amazonaws.com/km-{name}:{tag}`) — Fargate can pull and run sidecar containers; no more broken `${var.*_image}` literals |

All 5 phase requirement IDs accounted for. No orphaned requirements.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `pkg/compiler/service_hcl.go` | 497 | `mainImage := "MAIN_IMAGE_PLACEHOLDER"` | Info | Pre-existing placeholder for the user's main application image — NOT a sidecar concern; present before this phase, intentional design (real image set at provision time per comment at line 500) |
| `pkg/compiler/service_hcl.go` | 528 | `ecrRegistry = "PLACEHOLDER_ECR"` | Info | Intentional fallback for local development when KM_ACCOUNTS_APPLICATION is unset; documented, tested, parseable HCL |

No blocker or warning anti-patterns introduced by this phase.

### Human Verification Required

#### 1. Live S3 Upload (`make sidecars`)

**Test:** Set `KM_ARTIFACTS_BUCKET=<real-bucket>` with AWS credentials and run `make sidecars`
**Expected:** Three linux/amd64 ELF binaries uploaded to `s3://<bucket>/sidecars/dns-proxy`, `s3://<bucket>/sidecars/http-proxy`, `s3://<bucket>/sidecars/audit-log`; tracing config at `s3://<bucket>/sidecars/tracing/config.yaml`
**Why human:** Requires live AWS credentials and an existing S3 bucket

#### 2. Live ECR Push (`make ecr-push`)

**Test:** With Docker daemon running and AWS credentials set, run `make ecr-push`
**Expected:** Four images pushed: `<account>.dkr.ecr.<region>.amazonaws.com/km-dns-proxy:latest`, `km-http-proxy:latest`, `km-audit-log:latest`, `km-tracing:latest`
**Why human:** Requires Docker daemon, live AWS credentials, and ECR endpoint accessible

### Notable: ROADMAP SC1 Wording vs Implementation

The ROADMAP success criterion 1 reads "cross-compiles **all 4** sidecar binaries." In practice, tracing is not a Go binary — it is an OTel Collector config file. The Makefile correctly cross-compiles 3 Go binaries (dns-proxy, http-proxy, audit-log) and uploads the tracing `config.yaml`. This is the correct interpretation; the ROADMAP wording is imprecise but the implementation intent is fulfilled. All 4 sidecar *artifacts* (3 binaries + 1 config) are delivered to S3.

## Gaps Summary

No gaps. All automated checks passed:

- All 5 artifact files exist and are substantive (not stubs)
- All 6 key links are wired (upload paths match download paths, template fields populated, env var reads present)
- All 5 requirements have implementation evidence
- All 3 new compiler tests pass (plus 7 pre-existing ECS tests)
- No blocker anti-patterns introduced
- 3 git commits (d59cf7a, bc8bc8f, ccc5a7b) verified in repo history

The two human verification items (live S3 upload, live ECR push) are runtime concerns requiring AWS credentials — they do not block goal achievement from a code correctness standpoint.

---

_Verified: 2026-03-22T23:00:00Z_
_Verifier: Claude (gsd-verifier)_
