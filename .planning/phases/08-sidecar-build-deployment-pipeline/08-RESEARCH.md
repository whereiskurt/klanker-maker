# Phase 08: Sidecar Build & Deployment Pipeline - Research

**Researched:** 2026-03-22
**Domain:** Go cross-compilation, Docker multi-stage builds, AWS ECR, S3 binary distribution, Makefile build pipelines, Go compiler ECS image URI generation
**Confidence:** HIGH

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| NETW-02 | DNS proxy sidecar filters outbound DNS by allowlisted suffixes (works on both EC2 and ECS substrates) | DNS proxy binary must be compiled for linux/amd64 and uploaded to S3 (EC2) or pushed to ECR as an image (ECS) before NETW-02 can function at runtime |
| NETW-03 | HTTP proxy sidecar filters outbound HTTP/S by allowlisted hosts and methods (works on both EC2 and ECS substrates) | Same as NETW-02 — http-proxy binary/image must be available at sandbox boot |
| OBSV-01 | Audit log sidecar captures command execution logs (works on both EC2 and ECS substrates) | audit-log binary lives at `sidecars/audit-log/cmd/main.go`; needs linux/amd64 build + S3 upload / ECR push |
| OBSV-02 | Audit log sidecar captures network traffic logs (works on both EC2 and ECS substrates) | Same binary as OBSV-01 — all network traffic events flow through audit-log |
| PROV-10 | ECS substrate provisions Fargate task with sidecar containers defined in task definition | ECS service.hcl currently emits `${var.dns_proxy_image}` literal strings; compiler must be changed to emit resolvable ECR URIs |
</phase_requirements>

---

## Summary

Phase 8 closes the single most critical integration gap identified in the v1.0 milestone audit: **sidecars exist as Go source code but no pipeline builds, packages, or uploads them** to the locations where EC2 and ECS sandboxes expect them.

The two substrates have different artifact shapes. EC2 user-data calls `aws s3 cp s3://${KM_ARTIFACTS_BUCKET}/sidecars/dns-proxy ...` — it needs a compiled linux/amd64 static binary in S3. ECS uses Fargate container definitions that reference image URIs — it needs Docker images in ECR. Currently, `pkg/compiler/service_hcl.go` emits literal `${var.dns_proxy_image}` strings in the ECS service.hcl template, which Terraform cannot resolve because no `var.dns_proxy_image` variable exists in the ECS module.

The four sidecar artifacts are:
1. `sidecars/dns-proxy/` — Go binary (entry: `main.go`, lib: `dnsproxy/proxy.go`)
2. `sidecars/http-proxy/` — Go binary (entry: `main.go`, lib: `httpproxy/proxy.go`)
3. `sidecars/audit-log/cmd/` — Go binary (entry: `cmd/main.go`, lib: `auditlog.go`)
4. `sidecars/tracing/config.yaml` — Not a Go binary; YAML config file for `otelcol-contrib`

All three Go sidecars live in the same Go module (`github.com/whereiskurt/klankrmkr`) and import `pkg/aws`, `pkg/lifecycle`, and each other's library packages. They cannot be built in isolation with a standalone `go build ./...` from outside the module root.

The `infra/modules/ecs-task/v1.0.0/main.tf` already contains ECR image-URL construction logic (`local.ecr_registry`), but that module is not used for per-sandbox sandboxes — the per-sandbox ECS module is `infra/modules/ecs/v1.0.0/`. The combined ECS module expects fully-qualified image URIs passed in through the `containers` variable (line 168: `image = c.image`). There is no auto-expansion logic in the sandbox ECS module.

**Primary recommendation:** Add a `Makefile` at the repo root with `sidecars` (cross-compile + S3 upload) and `ecr-push` (Docker build + ECR push) targets, and fix `pkg/compiler/service_hcl.go` to emit real ECR URIs derived from `KM_ACCOUNTS_APPLICATION`, `KM_REGION`, and a config-driven image tag.

---

## Standard Stack

### Core
| Tool | Version | Purpose | Why Standard |
|------|---------|---------|--------------|
| `go build` with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` | Go 1.25.5 (repo go.mod) | Cross-compile static binaries for EC2 AL2 | Static binaries avoid glibc dependency issues on Amazon Linux; `CGO_ENABLED=0` is mandatory |
| `aws s3 cp` | AWS CLI v2 | Upload binaries to `s3://${KM_ARTIFACTS_BUCKET}/sidecars/` | EC2 user-data calls this exact command; consistent with existing patterns |
| `docker buildx build --platform linux/amd64` | Docker Desktop with buildx | Multi-platform container images for Fargate | Fargate requires linux/amd64; buildx handles cross-platform builds on M-series Macs |
| `aws ecr get-login-password | docker login` | AWS CLI v2 + Docker | ECR authentication | Standard ECR login sequence |
| `aws ecr describe-repositories` / `aws ecr create-repository` | AWS CLI v2 | Ensure ECR repos exist before push | ECR repos are not auto-created on push |
| GNU Make | System-provided | Orchestrate build steps | Existing Go projects in this repo have no Makefile; Makefile is the simplest addition that satisfies `make sidecars` / `make ecr-push` success criteria |

### Supporting
| Tool | Version | Purpose | When to Use |
|------|---------|---------|-------------|
| `docker build` without `buildx` | Docker | Simpler single-platform build | Only if `buildx` unavailable; prefer `buildx` for explicit platform pinning |
| `AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)` | AWS CLI | Derive ECR registry URL at build time | Image URI = `${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com/km-dns-proxy:${VERSION}` |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Makefile | `km build` subcommand | Go CLI subcommand adds ~200 lines of cobra plumbing and means the km binary must be built before sidecars can be built — circular dependency risk; Makefile is simpler and faster |
| Makefile | GitHub Actions CI | CI is not present in this repo; adds infrastructure not in scope for Phase 8 |
| Manual `docker build` per sidecar | Single multi-stage Dockerfile per sidecar | Multi-stage is cleaner and smaller final image; either approach works |

**Installation:**
```bash
# No new dependencies needed — all tools are aws-cli + docker + go
# ECR repos must be created manually or via Makefile target
```

---

## Architecture Patterns

### Recommended Project Structure
```
Makefile                          # repo root — new file
sidecars/
├── dns-proxy/
│   ├── Dockerfile                # new — multi-stage linux/amd64
│   ├── main.go                   # existing
│   └── dnsproxy/                 # existing library
├── http-proxy/
│   ├── Dockerfile                # new — multi-stage linux/amd64
│   ├── main.go                   # existing
│   └── httpproxy/                # existing library
├── audit-log/
│   ├── Dockerfile                # new — multi-stage linux/amd64
│   ├── auditlog.go               # existing library
│   └── cmd/
│       └── main.go               # existing binary entry point
└── tracing/
    └── config.yaml               # existing — no build needed
pkg/compiler/
└── service_hcl.go                # existing — ECR URI fix goes here
```

### Pattern 1: Makefile with S3 and ECR targets

**What:** Root Makefile with `sidecars` and `ecr-push` targets (plus helper targets per sidecar)
**When to use:** Operator runs `make sidecars` to upload to S3, `make ecr-push` to build and push images

```makefile
# Source: standard Go cross-compilation pattern
GOOS       := linux
GOARCH     := amd64
CGO_ENABLED := 0
VERSION    ?= latest
REGION     ?= $(shell aws configure get region)
ACCOUNT_ID := $(shell aws sts get-caller-identity --query Account --output text)
ECR_REGISTRY := $(ACCOUNT_ID).dkr.ecr.$(REGION).amazonaws.com
KM_ARTIFACTS_BUCKET ?= $(shell grep artifacts_bucket km-config.yaml | awk '{print $$2}')

SIDECARS := dns-proxy http-proxy audit-log

.PHONY: sidecars ecr-push ecr-login ecr-repos

# Cross-compile all sidecars and upload binaries to S3
sidecars: $(addprefix build/,$(SIDECARS))
	@echo "Uploading sidecar binaries to s3://$(KM_ARTIFACTS_BUCKET)/sidecars/"
	aws s3 cp build/dns-proxy  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/dns-proxy
	aws s3 cp build/http-proxy s3://$(KM_ARTIFACTS_BUCKET)/sidecars/http-proxy
	aws s3 cp build/audit-log  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/audit-log
	aws s3 cp sidecars/tracing/config.yaml s3://$(KM_ARTIFACTS_BUCKET)/sidecars/tracing/config.yaml

build/dns-proxy:
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) \
	  go build -o build/dns-proxy ./sidecars/dns-proxy/

build/http-proxy:
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) \
	  go build -o build/http-proxy ./sidecars/http-proxy/

build/audit-log:
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) \
	  go build -o build/audit-log ./sidecars/audit-log/cmd/

# Build Docker images and push to ECR
ecr-push: ecr-login ecr-repos
	docker buildx build --platform linux/amd64 \
	  -t $(ECR_REGISTRY)/km-dns-proxy:$(VERSION) \
	  --push sidecars/dns-proxy/
	docker buildx build --platform linux/amd64 \
	  -t $(ECR_REGISTRY)/km-http-proxy:$(VERSION) \
	  --push sidecars/http-proxy/
	docker buildx build --platform linux/amd64 \
	  -t $(ECR_REGISTRY)/km-audit-log:$(VERSION) \
	  --push sidecars/audit-log/
	docker buildx build --platform linux/amd64 \
	  -t $(ECR_REGISTRY)/km-tracing:$(VERSION) \
	  --push sidecars/tracing/

ecr-login:
	aws ecr get-login-password --region $(REGION) | \
	  docker login --username AWS --password-stdin $(ECR_REGISTRY)

ecr-repos:
	aws ecr describe-repositories --repository-names km-dns-proxy  || \
	  aws ecr create-repository --repository-name km-dns-proxy
	aws ecr describe-repositories --repository-names km-http-proxy || \
	  aws ecr create-repository --repository-name km-http-proxy
	aws ecr describe-repositories --repository-names km-audit-log  || \
	  aws ecr create-repository --repository-name km-audit-log
	aws ecr describe-repositories --repository-names km-tracing    || \
	  aws ecr create-repository --repository-name km-tracing
```

### Pattern 2: Multi-stage Dockerfile for Go sidecars

**What:** Each Go sidecar gets a `Dockerfile` that copies the entire module and builds only that sidecar. Multi-stage keeps the final image minimal.
**When to use:** `make ecr-push` invokes Docker build for each Dockerfile

```dockerfile
# Source: standard Go multi-stage Dockerfile for module-based projects
# Place at sidecars/dns-proxy/Dockerfile (adjust BINARY_PATH per sidecar)

FROM golang:1.25-alpine AS builder
WORKDIR /build
# Copy the full module — sidecars import pkg/aws, pkg/lifecycle, etc.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Build only this sidecar binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -o /sidecar ./sidecars/dns-proxy/

FROM scratch
COPY --from=builder /sidecar /sidecar
ENTRYPOINT ["/sidecar"]
```

**Critical:** The `COPY . .` must copy the **entire module root** (not just the sidecar subdirectory) because sidecars import `github.com/whereiskurt/klankrmkr/pkg/aws` and other internal packages. The Dockerfile context must be the repo root, not the sidecar subdirectory. Invoke with `docker build -f sidecars/dns-proxy/Dockerfile .` from the repo root.

**audit-log special case:** The binary entry point is `sidecars/audit-log/cmd/`, not `sidecars/audit-log/`. The Dockerfile for audit-log must build `./sidecars/audit-log/cmd/`.

**tracing special case:** Tracing is `otelcol-contrib`, not a Go binary. The tracing Dockerfile uses the upstream `otel/opentelemetry-collector-contrib` base image and copies `sidecars/tracing/config.yaml`. EC2 user-data already downloads this via `aws s3 cp`.

### Pattern 3: Compiler ECR URI fix in service_hcl.go

**What:** Replace the four `${var.dns_proxy_image}` literals in the ECS service.hcl template with real ECR URIs computed from environment variables.
**When to use:** Every call to `generateECSServiceHCL()`

Current broken template strings (lines 153, 169, 185, 211 of `service_hcl.go`):
```
image = "${var.dns_proxy_image}"
image = "${var.http_proxy_image}"
image = "${var.audit_log_image}"
image = "${var.tracing_image}"
```

These contain Go template delimiters `${}` that Go's `text/template` passes through verbatim (since `$` is not the template action delimiter), which means the final HCL contains literal `${var.dns_proxy_image}` strings. Terraform treats these as variable interpolations but no matching `var.dns_proxy_image` variable is declared in the ECS module — this causes a Terraform plan failure.

**Fix approach:** Add ECR URI computation to `ecsHCLParams` and `generateECSServiceHCL()`. The ECR base is `{account_id}.dkr.ecr.{region}.amazonaws.com`. Account ID should be read from `KM_ACCOUNTS_APPLICATION` env var (matches `site.hcl` accounts.application pattern). Image tag should default to `latest` with optional version override.

```go
// In generateECSServiceHCL, derive ECR URI fields:
accountID := os.Getenv("KM_ACCOUNTS_APPLICATION")
ecrRegistry := fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com", accountID, p.Spec.Runtime.Region)
imageTag := os.Getenv("KM_SIDECAR_VERSION")
if imageTag == "" {
    imageTag = "latest"
}
// Then pass to template:
params.DNSProxyImage   = fmt.Sprintf("%s/km-dns-proxy:%s", ecrRegistry, imageTag)
params.HTTPProxyImage  = fmt.Sprintf("%s/km-http-proxy:%s", ecrRegistry, imageTag)
params.AuditLogImage   = fmt.Sprintf("%s/km-audit-log:%s", ecrRegistry, imageTag)
params.TracingImage    = fmt.Sprintf("%s/km-tracing:%s", ecrRegistry, imageTag)
```

Then the template uses `{{ .DNSProxyImage }}` instead of `${var.dns_proxy_image}`.

**Fallback behavior:** When `KM_ACCOUNTS_APPLICATION` is empty (local dev/test), the compiler should emit a `PLACEHOLDER_ECR_URI/km-dns-proxy:latest` string — clearly non-functional but parseable HCL (avoids Terraform variable interpolation errors). This is consistent with the existing `MAIN_IMAGE_PLACEHOLDER` pattern.

### Anti-Patterns to Avoid

- **Building from the sidecar subdirectory:** `cd sidecars/dns-proxy && go build .` fails because go.mod is at the repo root. Always `go build ./sidecars/dns-proxy/` from the repo root.
- **Using Docker build context from the sidecar subdirectory:** `docker build sidecars/dns-proxy/` would fail to COPY `pkg/aws` etc. Use the repo root as context with `-f sidecars/dns-proxy/Dockerfile`.
- **Hardcoding ECR account ID in service_hcl.go:** Use `KM_ACCOUNTS_APPLICATION` env var, consistent with `site.hcl` `accounts.application = get_env("KM_ACCOUNTS_APPLICATION", "")` pattern.
- **Using `go build -o bin/dns-proxy ./sidecars/dns-proxy` without CGO_ENABLED=0:** CGO produces dynamically linked binaries; Amazon Linux 2 EC2 instances may not have the same glibc version as the build host.
- **Forgetting the audit-log cmd/ subdirectory:** The audit-log binary is at `sidecars/audit-log/cmd/main.go` (package main), not `sidecars/audit-log/main.go` (package auditlog, not buildable as binary). This is due to Go's single-package-per-directory rule.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| ECR authentication | Custom credential logic | `aws ecr get-login-password \| docker login` | AWS CLI handles credential refresh, MFA, SSO profiles |
| Image URI construction | Terraform variable input | Template string in Go compiler with env var derivation | The ECS module has no `var.dns_proxy_image` variable; adding one would require module changes for every sandbox; compiler-side derivation is consistent with how all other inputs are compiled |
| Cross-platform Go builds | QEMU or buildx emulation | `GOOS=linux GOARCH=amd64 go build` | Native cross-compilation in Go toolchain; no emulation needed for binaries |
| Sidecar versioning | Git SHA tagging infrastructure | `VERSION ?= latest` with `KM_SIDECAR_VERSION` env override | Simple is correct here; operators can pin a version when needed |

**Key insight:** The Go toolchain's built-in cross-compilation (`GOOS`/`GOARCH`) eliminates the need for Docker cross-compile emulation for the S3 binary path. Docker multi-stage builds with `--platform linux/amd64` handles the ECR path cleanly.

---

## Common Pitfalls

### Pitfall 1: Docker build context excludes internal packages
**What goes wrong:** `docker build sidecars/dns-proxy/` — Docker cannot COPY `pkg/aws` because the context root is `sidecars/dns-proxy/`, which only contains `main.go` and `dnsproxy/`.
**Why it happens:** Docker restricts file access to the build context path.
**How to avoid:** Always run `docker build -f sidecars/dns-proxy/Dockerfile .` from the repo root. The Makefile `ecr-push` target must `cd` to repo root before invoking Docker.
**Warning signs:** `COPY go.mod go.sum ./` succeeds but `RUN go mod download` or `go build` fails with "package not found".

### Pitfall 2: audit-log binary path is cmd/main.go not main.go
**What goes wrong:** `go build -o build/audit-log ./sidecars/audit-log/` fails because `sidecars/audit-log/` is `package auditlog` (library package), not `package main`.
**Why it happens:** Phase 3 established the cmd/ subdirectory pattern to separate library from binary (Phase 3-02 decision in STATE.md: "Package layout: auditlog.go (package auditlog) + cmd/main.go (package main)").
**How to avoid:** Build target is `./sidecars/audit-log/cmd/` not `./sidecars/audit-log/`. Similarly, the Dockerfile for audit-log must run `go build -o /sidecar ./sidecars/audit-log/cmd/`.
**Warning signs:** `go build: cannot find main package in directory`.

### Pitfall 3: ECR image URI includes $ interpolation that Terraform misinterprets
**What goes wrong:** The compiler emits `image = "${var.dns_proxy_image}"`. Terraform treats this as a variable reference; the ECS module has no such variable; `terragrunt apply` fails with "An argument named dns_proxy_image is not expected here" or similar.
**Why it happens:** Go's `text/template` passes `${...}` through literally (it only processes `{{...}}`). The original intent was probably for Terraform to interpolate these, but the ECS module (`infra/modules/ecs/v1.0.0`) does not declare `var.dns_proxy_image`.
**How to avoid:** Fix `service_hcl.go` to emit real URIs like `123456789012.dkr.ecr.us-east-1.amazonaws.com/km-dns-proxy:latest`. Never emit Terraform variable interpolation for values that the module does not declare.
**Warning signs:** `terragrunt plan` fails with "An argument named X is not expected" or "Variables not allowed" errors.

### Pitfall 4: S3 binary not yet uploaded when sandbox boots
**What goes wrong:** EC2 user-data runs `aws s3 cp s3://${KM_ARTIFACTS_BUCKET}/sidecars/dns-proxy ...` but the binary was never uploaded. The `aws s3 cp` exits non-zero, and with `set -euo pipefail` at the top of user-data, the entire bootstrap script exits early — sidecars never start.
**Why it happens:** Phase 8 does not yet exist, so the operator never had a `make sidecars` command to run.
**How to avoid:** Document the pre-provision checklist: run `make sidecars` before the first `km create` against an empty bucket. The Makefile `sidecars` target should print a clear success message with the S3 path.
**Warning signs:** EC2 instance reaches SSM reachability but sandbox never signals `SANDBOX_READY`.

### Pitfall 5: KM_ARTIFACTS_BUCKET not set in Makefile
**What goes wrong:** `make sidecars` uploads to an empty string bucket or fails silently.
**Why it happens:** `KM_ARTIFACTS_BUCKET` is read from env at compile time in `pkg/compiler/userdata.go` but not validated at Makefile execution time.
**How to avoid:** Add a guard at the top of the `sidecars` Makefile target: `@test -n "$(KM_ARTIFACTS_BUCKET)" || (echo "ERROR: KM_ARTIFACTS_BUCKET is not set"; exit 1)`.
**Warning signs:** `make sidecars` reports success but `aws s3 ls` shows nothing uploaded.

---

## Code Examples

Verified patterns from official sources and the existing codebase:

### Go cross-compilation command (HIGH confidence — standard Go toolchain)
```bash
# From repo root
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -o build/dns-proxy \
  ./sidecars/dns-proxy/

GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -o build/http-proxy \
  ./sidecars/http-proxy/

# audit-log: binary is in cmd/ subdirectory (Phase 3-02 pattern)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -o build/audit-log \
  ./sidecars/audit-log/cmd/
```

### ECR login and push (HIGH confidence — AWS docs pattern)
```bash
AWS_REGION=us-east-1
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
ECR_REGISTRY="${ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"

aws ecr get-login-password --region "${AWS_REGION}" \
  | docker login --username AWS --password-stdin "${ECR_REGISTRY}"

docker buildx build --platform linux/amd64 \
  --tag "${ECR_REGISTRY}/km-dns-proxy:latest" \
  --push \
  --file sidecars/dns-proxy/Dockerfile \
  .
```

### Compiler fix: ECR URI derivation in generateECSServiceHCL (HIGH confidence — pattern consistent with existing KM_* env var usage in service_hcl.go)
```go
// In pkg/compiler/service_hcl.go generateECSServiceHCL()
accountID := os.Getenv("KM_ACCOUNTS_APPLICATION")
imageTag := os.Getenv("KM_SIDECAR_VERSION")
if imageTag == "" {
    imageTag = "latest"
}
ecrRegistry := ""
if accountID != "" {
    ecrRegistry = fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com", accountID, p.Spec.Runtime.Region)
}
sidecarImage := func(name string) string {
    if ecrRegistry == "" {
        return "PLACEHOLDER_ECR/" + name + ":" + imageTag
    }
    return ecrRegistry + "/km-" + name + ":" + imageTag
}

// In ecsHCLParams struct, add fields:
// DNSProxyImage, HTTPProxyImage, AuditLogImage, TracingImage string

// In params assignment:
params.DNSProxyImage  = sidecarImage("dns-proxy")
params.HTTPProxyImage = sidecarImage("http-proxy")
params.AuditLogImage  = sidecarImage("audit-log")
params.TracingImage   = sidecarImage("tracing")

// In ecsServiceHCLTemplate, replace:
//   image = "${var.dns_proxy_image}"
// with:
//   image = "{{ .DNSProxyImage }}"
```

### tracing Dockerfile (using upstream otelcol-contrib image)
```dockerfile
# sidecars/tracing/Dockerfile
# Tracing sidecar uses upstream OTel collector — not a Go build
FROM otel/opentelemetry-collector-contrib:latest
COPY config.yaml /etc/otelcol-contrib/config.yaml
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Manual `go build` per developer | `make sidecars` one-liner | Phase 8 (new) | Reproducible builds; operators don't need to know Go build flags |
| No sidecar deployment pipeline | `make sidecars` + `make ecr-push` | Phase 8 (new) | Enables first working `km create` for both EC2 and ECS substrates |
| Literal `${var.dns_proxy_image}` in generated HCL | Real ECR URIs from compiler | Phase 8 (new) | ECS sandboxes can provision for the first time |

**Deprecated/outdated:**
- `${var.dns_proxy_image}` template placeholders in `ecsServiceHCLTemplate`: replaced with compiler-computed ECR URIs in Phase 8

---

## Open Questions

1. **ECR repository naming convention**
   - What we know: `infra/modules/ecs-task/v1.0.0/main.tf` uses pattern `${km_label}-${container.name}` → e.g. `km-dns-proxy`
   - What's unclear: Whether to use that naming in the per-sandbox ECS module, or a different convention
   - Recommendation: Use `km-{sidecar-name}` (e.g., `km-dns-proxy`, `km-http-proxy`, `km-audit-log`, `km-tracing`) — consistent with the ecs-task module pattern, no per-sandbox naming needed since sidecars are shared infrastructure

2. **KM_SIDECAR_VERSION env var vs hardcoded `latest`**
   - What we know: The Makefile success criteria in ROADMAP.md says "build Docker images... and push to ECR" — no version management requirement specified
   - What's unclear: Whether operators need to pin sidecar versions per-sandbox or per-deployment
   - Recommendation: Default to `latest`; add `KM_SIDECAR_VERSION` env var override in the Makefile and compiler for future pinning

3. **Whether `build/` directory should be gitignored**
   - What we know: No `.gitignore` inspection done; the `build/` output directory for compiled binaries should not be committed
   - Recommendation: Add `build/` to `.gitignore` in the Makefile or as a separate task

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (`testing` package, `go test`) |
| Config file | none — standard `go test ./...` |
| Quick run command | `go test ./pkg/compiler/...` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| PROV-10 | ECS service.hcl emits valid ECR image URIs (not `${var.*}` literals) | unit | `go test ./pkg/compiler/ -run TestECSServiceHCL` | ❌ Wave 0 |
| NETW-02 | DNS proxy binary compiles for linux/amd64 | build smoke | `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./sidecars/dns-proxy/ && echo OK` | ❌ Wave 0 (Makefile) |
| NETW-03 | HTTP proxy binary compiles for linux/amd64 | build smoke | `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./sidecars/http-proxy/ && echo OK` | ❌ Wave 0 (Makefile) |
| OBSV-01/02 | audit-log binary compiles for linux/amd64 | build smoke | `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./sidecars/audit-log/cmd/ && echo OK` | ❌ Wave 0 (Makefile) |

### Sampling Rate
- **Per task commit:** `go test ./pkg/compiler/ -run TestECSServiceHCL`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green + `make sidecars` dry-run before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/compiler/service_hcl_ecs_test.go` — test that generated ECS HCL contains ECR URI pattern (not `${var.*}`) for REQ PROV-10
- [ ] `Makefile` — build, S3 upload, and ECR push targets for REQ NETW-02, NETW-03, OBSV-01, OBSV-02
- [ ] `sidecars/dns-proxy/Dockerfile` — multi-stage linux/amd64 image
- [ ] `sidecars/http-proxy/Dockerfile` — multi-stage linux/amd64 image
- [ ] `sidecars/audit-log/Dockerfile` — multi-stage linux/amd64 image (entry: `./sidecars/audit-log/cmd/`)
- [ ] `sidecars/tracing/Dockerfile` — otelcol-contrib based image

---

## Sources

### Primary (HIGH confidence)
- Go toolchain cross-compilation: `GOOS`/`GOARCH`/`CGO_ENABLED` are standard Go env vars, stable since Go 1.5
- `/Users/khundeck/working/klankrmkr/pkg/compiler/service_hcl.go` — confirmed `${var.dns_proxy_image}` literals at lines 153, 169, 185, 211
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go` — confirmed S3 download path: `s3://${KM_ARTIFACTS_BUCKET}/sidecars/{name}`
- `/Users/khundeck/working/klankrmkr/infra/modules/ecs/v1.0.0/main.tf` — confirmed image is passed through `c.image` from containers variable, no auto-ECR expansion
- `/Users/khundeck/working/klankrmkr/infra/modules/ecs/v1.0.0/variables.tf` — confirmed no `dns_proxy_image` variable exists
- `/Users/khundeck/working/klankrmkr/infra/modules/ecs-task/v1.0.0/main.tf` — confirmed ECR URI pattern: `{account_id}.dkr.ecr.{region}.amazonaws.com`
- `/Users/khundeck/working/klankrmkr/sidecars/audit-log/cmd/main.go` — confirmed binary entry at `cmd/main.go`, library at `auditlog.go` (package main vs package auditlog)
- `/Users/khundeck/working/klankrmkr/go.mod` — confirmed single-module repo at `github.com/whereiskurt/klankrmkr`, Go 1.25.5

### Secondary (MEDIUM confidence)
- AWS ECR login pattern (`aws ecr get-login-password | docker login`) — widely documented AWS CLI pattern, stable across CLI v2
- `docker buildx build --platform linux/amd64` for cross-platform images — verified against Docker buildx documentation patterns

### Tertiary (LOW confidence)
- None — all critical claims verified from codebase inspection

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — toolchain versions confirmed from go.mod; Makefile pattern is standard
- Architecture: HIGH — compiler fix approach confirmed by reading both service_hcl.go and ECS module variables.tf
- Pitfalls: HIGH — all pitfalls derived directly from code inspection, not speculation

**Research date:** 2026-03-22
**Valid until:** 2026-04-22 (stable toolchain; expires if ECS module or compiler templates are modified)
