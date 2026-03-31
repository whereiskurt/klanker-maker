# Phase 36: km-sandbox Base Container Image - Research

**Researched:** 2026-03-29
**Domain:** Container image construction, shell entrypoint scripting, ECS task environment wiring, ECR image build/push pipeline
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Base image & distro:**
- Amazon Linux 2023 minimal (`amazonlinux:2023-minimal`) — matches EC2 AMI exactly, same package manager (dnf), same CA trust paths (`/etc/pki/`), same package names
- Minimal base + initCommands model — image has OS essentials (git, jq, tar, python3, AWS CLI v2, gosu, ca-certificates, sandbox user). Profile-specific tools (Claude Code, Goose, Node.js) installed by initCommands at container start
- No SSM agent — `km shell` uses `docker exec` (Docker) or `kubectl exec` (EKS). No SSM IAM permissions needed
- Shell script helpers — mail poller and artifact upload as bash scripts, not Go binaries. AWS CLI does the heavy lifting

**Entrypoint design:**
- Bash shell script (`entrypoint.sh`) — direct port of EC2 user-data logic. Each section is a function
- Categorized failure handling — critical steps (CA trust, secrets) abort on failure; optional steps (rsync, mail poller, GitHub) warn and continue
- gosu for user drop — `exec gosu sandbox /bin/bash` at the end. Proper PID 1 handoff, no TTY issues, no signal forwarding problems
- SIGTERM trap for graceful shutdown — entrypoint traps SIGTERM/SIGINT, runs artifact upload, then exits

**Build & distribution:**
- Source lives in `containers/sandbox/` — new top-level `containers/` directory alongside `sidecars/`
- `make sandbox-image` Makefile target + integrated into `make ecr-push`
- Same VERSION file as km CLI — `km-sandbox:v0.0.55` matches `km` CLI and sidecar versions. `latest` tag always points to most recent
- Auto-build on first use — `km create --substrate docker` checks for `km-sandbox:latest` locally; if missing, builds automatically

**Credential delivery:**
- Credential refresh sidecar + shared tmpfs volume (Docker substrate) — only `km-cred-refresh` container has operator credentials; writes session creds to tmpfs; others read via `AWS_SHARED_CREDENTIALS_FILE`
- IRSA on EKS (Phase 38) — no refresh sidecar needed
- Two IAM roles (sandbox role vs sidecar role)
- Entrypoint is substrate-agnostic — reads AWS_* env vars regardless of source

### Claude's Discretion
- Exact entrypoint function ordering and error messages
- AWS CLI install method in Dockerfile (zip vs pip)
- gosu version selection
- Dockerfile layer optimization and caching strategy
- tmpfs volume size for credential file

### Deferred Ideas (OUT OF SCOPE)
- Fat image variant with pre-installed agents (Claude Code, Goose, Node.js)
- SSM agent inclusion for unified `km shell` experience
- Go binary entrypoint for better error handling
</user_constraints>

---

## Summary

Phase 36 builds the `km-sandbox` container image and entrypoint script that replaces EC2 user-data for ECS/Docker/EKS substrates. The work is fundamentally a port: take the 600+ line `pkg/compiler/userdata.go` Go template, extract its logic into a Bash entrypoint script, and wire it into the existing ECR build pipeline.

The key insight is that all per-sandbox configuration that was baked into the user-data script (by the Go template renderer) must now be passed at runtime via environment variables. The entrypoint reads those env vars and performs the same setup steps in order: CA trust, secrets injection, OTP injection, profile env vars, GitHub credentials, rsync restore, initCommands, mail poller, artifact-upload trap, and finally `exec gosu sandbox /bin/bash`.

Three Go files need modification: `pkg/compiler/service_hcl.go` to replace `MAIN_IMAGE_PLACEHOLDER` with the real ECR URI and inject the new KM_* entrypoint env vars into the ECS container definition; `internal/app/cmd/init.go` to build and push the sandbox image to ECR; and `Makefile` to add the `sandbox-image` target and `km-sandbox` to `ecr-repos`.

**Primary recommendation:** Port user-data section by section into `entrypoint.sh` functions with matching section numbers and comments, using the existing sidecar Dockerfile pattern as the build template.

---

## Standard Stack

### Core

| Library / Tool | Version | Purpose | Why Standard |
|----------------|---------|---------|--------------|
| `amazonlinux:2023-minimal` | latest | Base image | Matches EC2 AMI — identical `dnf`, `/etc/pki/` CA paths, package names |
| AWS CLI v2 | latest | S3, SSM, SES from entrypoint | Already used by existing EC2 user-data; same AWS CLI v2 install pattern |
| gosu | latest stable | Drop from root to sandbox user with proper PID 1 handoff | Standard container pattern; avoids `su -c` TTY issues; handles SIGTERM correctly |
| docker buildx | bundled with Docker | Cross-platform image build | Already used by all sidecar `make ecr-push` targets |

### Supporting

| Tool | Purpose | When to Use |
|------|---------|-------------|
| `dnf` | Package installation | Amazon Linux 2023 native package manager (not `yum`) |
| `update-ca-trust` | System CA bundle refresh | After installing proxy CA cert (AL2023 path: `/etc/pki/ca-trust/source/anchors/`) |
| `base64 -d` | Decode KM_INIT_COMMANDS and KM_PROFILE_ENV | POSIX base64 — available in coreutils |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Bash entrypoint | Go binary entrypoint | Go adds complexity; bash is debuggable with `bash -x`; deferred per CONTEXT.md |
| gosu | `su -c` / `sudo` | gosu correctly handles PID 1 exec and signal forwarding; `su -c` spawns a child process |
| AWS CLI zip install | pip install awscli | zip install is the official AWS method; pip installs CLI v1 by default |
| `exec gosu sandbox /bin/bash` | `exec gosu sandbox "$@"` | `$@` allows Docker CMD override; prefer for flexibility |

**Installation (in Dockerfile):**
```bash
# AWS CLI v2 — official zip method
RUN curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o /tmp/awscliv2.zip \
    && unzip -q /tmp/awscliv2.zip -d /tmp \
    && /tmp/aws/install \
    && rm -rf /tmp/aws /tmp/awscliv2.zip

# gosu — from official GitHub releases
RUN curl -fsSL "https://github.com/tianon/gosu/releases/download/1.17/gosu-amd64" -o /usr/local/bin/gosu \
    && chmod +x /usr/local/bin/gosu \
    && gosu nobody true
```

---

## Architecture Patterns

### Recommended Project Structure

```
containers/
└── sandbox/
    ├── Dockerfile           # amazonlinux:2023-minimal base image
    └── entrypoint.sh        # container entrypoint (port of userdata.go)

sidecars/                    # existing — unchanged
    ├── audit-log/
    ├── dns-proxy/
    ├── http-proxy/
    └── tracing/
```

### Pattern 1: Entrypoint as Sectioned Functions

**What:** Each setup phase is a Bash function named after the user-data section number/name. Functions are called in order at the bottom of the script.

**When to use:** Always — mirrors the EC2 user-data section numbering for easy cross-reference. Makes individual sections independently testable and skippable.

**Example:**
```bash
#!/bin/bash
# km-sandbox entrypoint — generated from pkg/compiler/userdata.go logic
# Run with: bash -x /opt/km/entrypoint.sh  (for debugging)

set -euo pipefail

log() { echo "[km-entrypoint] $*"; }
log_warn() { echo "[km-entrypoint] WARNING: $*" >&2; }
log_fail() { echo "[km-entrypoint] ERROR: $*" >&2; exit 1; }

# ============================================================
# 1. Proxy CA trust (CRITICAL — abort on failure)
# ============================================================
setup_ca_trust() {
    [ -z "${KM_PROXY_CA_CERT_S3:-}" ] && { log "No proxy CA configured (skipped)"; return 0; }
    aws s3 cp "${KM_PROXY_CA_CERT_S3}" /etc/pki/ca-trust/source/anchors/km-proxy-ca.crt \
        || log_fail "Failed to fetch proxy CA cert from ${KM_PROXY_CA_CERT_S3}"
    update-ca-trust
    KM_CA_BUNDLE=/etc/pki/tls/certs/ca-bundle.crt
    export SSL_CERT_FILE="${KM_CA_BUNDLE}"
    export REQUESTS_CA_BUNDLE="${KM_CA_BUNDLE}"
    export CURL_CA_BUNDLE="${KM_CA_BUNDLE}"
    export NODE_EXTRA_CA_CERTS="${KM_CA_BUNDLE}"
    log "Proxy CA trust configured"
}

# ============================================================
# 2. Secret injection (CRITICAL — abort on failure)
# ============================================================
inject_secrets() {
    [ -z "${KM_SECRET_PATHS:-}" ] && { log "No secrets to inject"; return 0; }
    IFS=',' read -ra PATHS <<< "${KM_SECRET_PATHS}"
    for path in "${PATHS[@]}"; do
        val=$(aws ssm get-parameter --name "${path}" --with-decryption \
              --query "Parameter.Value" --output text 2>/dev/null) \
            || { log_fail "Failed to fetch secret ${path}"; }
        env_name=$(basename "${path}" | tr '[:lower:]' '[:upper:]' | tr '-' '_')
        export "${env_name}=${val}"
        log "Injected secret: ${path} -> ${env_name}"
    done
}

# ... more functions ...

# SIGTERM trap: upload artifacts before exit
_shutdown() {
    log "SIGTERM received — uploading artifacts..."
    /opt/km/bin/km-upload-artifacts 2>/dev/null || log_warn "Artifact upload failed"
    exit 0
}
trap '_shutdown' TERM INT

# Main execution order
setup_ca_trust
inject_secrets
inject_otp_secrets
setup_profile_env
setup_github_credentials
setup_git_ref_enforcement
restore_rsync_snapshot
run_init_commands
start_mail_poller

log "Dropping to sandbox user..."
exec gosu sandbox "${@:-/bin/bash}"
```

### Pattern 2: Critical vs Optional Step Categorization

**What:** Critical steps (CA trust, secret injection) call `log_fail` and `exit 1` on failure. Optional steps (rsync restore, mail poller, GitHub credentials) call `log_warn` and return 0.

**When to use:** Always — sandbox is still useful without email or rsync, but it cannot function without Bedrock access (secrets) or proxy trust (CA cert).

| Step | Category | On Failure |
|------|----------|------------|
| Proxy CA trust | CRITICAL | `exit 1` |
| Secret injection | CRITICAL | `exit 1` |
| OTP injection | OPTIONAL | warn, continue |
| Profile env vars | OPTIONAL | warn, continue |
| GitHub credentials | OPTIONAL | warn, continue |
| Git ref enforcement | OPTIONAL | warn, continue |
| Rsync restore | OPTIONAL | warn, continue |
| Init commands | OPTIONAL | warn, continue |
| Mail poller | OPTIONAL | warn, continue |

### Pattern 3: Env Var-Driven Configuration

**What:** All profile-specific configuration arrives via environment variables set in the ECS task definition (generated by `service_hcl.go`). No per-profile image builds needed.

**Env vars the entrypoint reads:**
```
KM_SANDBOX_ID          — sandbox ID (always set)
KM_ARTIFACTS_BUCKET    — S3 bucket for artifacts, CA cert, snapshots
KM_STATE_BUCKET        — S3 bucket for metadata
KM_PROXY_CA_CERT_S3    — s3://bucket/sidecars/km-proxy-ca.crt
KM_SECRET_PATHS        — comma-separated SSM parameter paths
KM_OTP_PATHS           — comma-separated SSM OTP paths (fetch+delete)
KM_INIT_COMMANDS       — base64-encoded JSON array of shell commands
KM_RSYNC_SNAPSHOT      — rsync snapshot name to restore from S3
KM_GITHUB_TOKEN_SSM    — SSM path for GitHub token
KM_GITHUB_ALLOWED_REFS — comma-separated allowed git refs
KM_PROFILE_ENV         — base64-encoded JSON object {KEY: value}
KM_EMAIL_ADDRESS       — sandbox email address
KM_OPERATOR_EMAIL      — operator notification address
```

**Note:** The ECS task definition already sets `SANDBOX_ID`, `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, `CLAUDE_CODE_ENABLE_TELEMETRY`, and OTEL vars. The entrypoint must NOT overwrite these — only add what's missing.

### Pattern 4: ECR Build and Push (matches existing sidecar pattern)

**What:** `docker buildx build --platform linux/amd64` with `--push` to ECR, tagged with `VERSION` file contents. Identical to the sidecar `ecr-push` targets.

**Why linux/amd64:** ECS Fargate tasks run on x86_64. Docker local substrate on Mac uses `--platform linux/amd64` for consistency. The AL2023 minimal base image supports amd64.

**ECR repository name:** `km-sandbox` — follows the existing `km-{name}` convention.

### Anti-Patterns to Avoid

- **Writing profile-specific data into the image at build time:** All profile data must come from env vars. Building a separate image per profile would break the single-image model.
- **Running as root in the final exec:** The entrypoint runs as root for setup, but must `exec gosu sandbox` before handing off. Leaving the agent process running as root defeats sandbox user isolation.
- **Using `set -e` globally without per-step error handling:** `set -e` is set at the top, but optional steps must use `|| { log_warn ...; return 0; }` patterns to allow graceful degradation.
- **Hardcoding region:** Region must come from `AWS_DEFAULT_REGION` env var (set by ECS from the task definition), not hardcoded in the entrypoint.
- **Duplicating env vars already set by ECS task definition:** `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, and OTEL vars are set by the task definition (in `service_hcl.go`). The entrypoint should not reset them.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| User drop with PID 1 handoff | Custom `su` wrapper | `gosu` | gosu handles exec semantics, signal forwarding, and TTY correctly — su spawns a child |
| Base64 JSON decode | Custom parser | `base64 -d` + `jq` | Both are in the image; jq already a required package |
| CA bundle path detection | Platform detection logic | Try known paths in order: `/etc/pki/tls/certs/ca-bundle.crt` (AL2023), fall back to `/etc/ssl/certs/ca-certificates.crt` | Already validated in Phase 35 for AL2023 |
| Docker image version tagging | Custom versioning | Read from `VERSION` file via `$(cat VERSION)` | Consistent with existing sidecar pipeline |
| ECR authentication | Custom auth flow | `aws ecr get-login-password \| docker login` | Already implemented in `make ecr-login` |

**Key insight:** The entrypoint is not inventing new functionality — it is a faithful port of `userdata.go` logic. Every pattern there has already been tested on EC2.

---

## Common Pitfalls

### Pitfall 1: env var quoting with commas in values
**What goes wrong:** `IFS=',' read -ra PATHS <<< "${KM_SECRET_PATHS}"` splits on all commas, including commas inside individual values. SSM paths don't contain commas, but profile env var values might.
**Why it happens:** Comma-delimited encoding for multi-value env vars doesn't handle embedded commas.
**How to avoid:** Use `KM_PROFILE_ENV` as base64-encoded JSON (already decided in CONTEXT.md). For paths and refs, validate they don't contain commas (SSM paths and git refs never do).
**Warning signs:** Env var injection silently injecting wrong key/value pairs.

### Pitfall 2: AL2023 uses dnf, not yum
**What goes wrong:** initCommands that call `yum install` fail on the container image.
**Why it happens:** Amazon Linux 2023 replaced yum with dnf as the primary package manager. `yum` is a compatibility symlink but not guaranteed in `-minimal` variant.
**How to avoid:** Install `yum` compatibility or document that initCommands targeting AL2023 must use `dnf`. The base image Dockerfile installs packages with `dnf`.
**Warning signs:** initCommands that install packages hanging or failing with "command not found".

### Pitfall 3: CA trust env vars must be in environment before exec gosu
**What goes wrong:** `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, etc. are exported in the entrypoint but not visible to the sandbox user's processes after `exec gosu sandbox`.
**Why it happens:** `exec gosu sandbox /bin/bash` replaces the process image but inherits the environment. The vars ARE inherited if exported before the exec. But if they're set inside a subshell or function that returns without exporting globally, they're lost.
**How to avoid:** `export` all CA-related vars at the top level of the entrypoint, not inside subshells. Functions should set vars via `export VARNAME=value` (not local).
**Warning signs:** Python requests SSL errors despite CA cert installed; `curl -v` shows custom CA but Python does not.

### Pitfall 4: gosu binary not available for linux/amd64 in Dockerfile
**What goes wrong:** Build fails because gosu GitHub release URL for the wrong architecture is used.
**Why it happens:** gosu releases separate binaries per arch: `gosu-amd64`, `gosu-arm64`. The URL must match `--platform linux/amd64`.
**How to avoid:** Hardcode `gosu-amd64` URL since sandbox image is always built for `linux/amd64`. Verify with `gosu nobody true` after install.
**Warning signs:** `exec format error` when entrypoint tries to invoke gosu.

### Pitfall 5: MAIN_IMAGE_PLACEHOLDER never replaced at provision time
**What goes wrong:** ECS task definition is deployed with literal `MAIN_IMAGE_PLACEHOLDER` as the container image, causing task launch failure.
**Why it happens:** `service_hcl.go` line 596 sets `mainImage := "MAIN_IMAGE_PLACEHOLDER"` — it is never subsequently updated.
**How to avoid:** In Phase 36, `generateECSServiceHCL` must compute the real `km-sandbox` ECR URI using the same `ecrRegistry` and `imageTag` pattern already used for sidecar images (`sidecarImage()` helper).
**Warning signs:** ECS task failing to start with "CannotPullContainerError: invalid reference format".

### Pitfall 6: entrypoint KM_* env vars not wired in ECS task definition
**What goes wrong:** Entrypoint exits gracefully but sandbox has no secrets, no CA trust, and no proxy configuration because the env vars were never set in the task definition.
**Why it happens:** `service_hcl.go` generates the ECS task definition. The `KM_SECRET_PATHS`, `KM_PROXY_CA_CERT_S3`, `KM_INIT_COMMANDS`, etc. env vars are new and must be explicitly added to the `environment` block for the `main` container.
**How to avoid:** Treat the env var list in `DESCRIPTION.md` as the complete contract. Add a test in `service_hcl_test.go` verifying these vars appear in the main container's environment block.
**Warning signs:** Container starts but `[km-entrypoint]` log shows "No secrets to inject" and "No proxy CA configured" even when the profile has secrets and budget enforcement.

### Pitfall 7: SIGTERM handler races with ECS task shutdown
**What goes wrong:** ECS sends SIGTERM to the entrypoint (PID 1), but `exec gosu sandbox /bin/bash` replaced the entrypoint process with bash. The trap is gone.
**Why it happens:** `exec` replaces the process — the trap set before exec is not inherited.
**How to avoid:** The SIGTERM trap must be installed in the sandbox user's shell session, or the entrypoint must NOT exec and instead wait on a child process. The simpler option: run `gosu sandbox /bin/bash` as a background child, wait on it, and handle SIGTERM in the parent entrypoint. The CONTEXT.md design (gosu + trap before exec) works if artifact upload runs before exec — at shutdown the ECS agent sends SIGTERM to PID 1 which is the bash that replaced the entrypoint. Alternative: use `exec gosu sandbox "$@"` where `$@` defaults to `/bin/bash`, so the artifact-upload trap must be set inside the sandbox shell profile.

**Recommended resolution:** Run artifact upload script in the entrypoint before exec (spot interruption / container stop), then exec. For ECS SIGTERM during running session, install a SIGTERM handler in `/etc/profile.d/km-shutdown.sh` that runs artifact upload when the shell exits.

---

## Code Examples

Verified patterns from existing codebase:

### Existing sidecar Dockerfile pattern (source: `sidecars/http-proxy/Dockerfile`)
```dockerfile
# Stage 1: Builder
FROM golang:1.25-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /sidecar ./sidecars/http-proxy/

# Stage 2: Final image
FROM scratch
COPY --from=builder /sidecar /sidecar
ENTRYPOINT ["/sidecar"]
```
The sandbox Dockerfile follows the same multi-stage pattern but uses `amazonlinux:2023-minimal` instead of `scratch` (needs a shell and package manager).

### Existing ecr-push Makefile pattern (source: `Makefile`)
```makefile
ecr-push: ecr-login ecr-repos
    docker buildx build --platform linux/amd64 \
      --file sidecars/dns-proxy/Dockerfile \
      --tag $(ECR_REGISTRY)/km-dns-proxy:$(VERSION) \
      --push .
```
Add `sandbox-image` as a new target that follows this exact pattern with `containers/sandbox/Dockerfile`.

### Existing sidecarImage helper (source: `pkg/compiler/service_hcl.go:626`)
```go
sidecarImage := func(name string) string {
    return ecrRegistry + "/km-" + name + ":" + imageTag
}
```
Replace `mainImage := "MAIN_IMAGE_PLACEHOLDER"` with `mainImage := sidecarImage("sandbox")`.

### Existing ecr-repos target (source: `Makefile:94-97`)
```makefile
ecr-repos:
    @for name in km-dns-proxy km-http-proxy km-audit-log km-tracing km-create-handler; do \
      aws ecr describe-repositories --region $(REGION) --repository-names $$name 2>/dev/null || \
      aws ecr create-repository --region $(REGION) --repository-name $$name; \
    done
```
Add `km-sandbox` to this list.

### Existing CA trust pattern (source: `pkg/compiler/userdata.go:560-597`)
```bash
# Amazon Linux 2023 uses update-ca-trust
cp /usr/local/share/ca-certificates/km-proxy-ca.crt /etc/pki/ca-trust/source/anchors/
update-ca-trust
KM_CA_BUNDLE=/etc/pki/tls/certs/ca-bundle.crt
export SSL_CERT_FILE=${KM_CA_BUNDLE}
export REQUESTS_CA_BUNDLE=${KM_CA_BUNDLE}
export CURL_CA_BUNDLE=${KM_CA_BUNDLE}
export NODE_EXTRA_CA_CERTS=${KM_CA_BUNDLE}
```
The entrypoint mirrors this exactly, fetching the cert from `KM_PROXY_CA_CERT_S3` first.

### Existing OTP secret injection pattern (source: `pkg/compiler/userdata.go:158-168`)
```bash
OTP_VAL=$(aws ssm get-parameter --name "{{ .Path }}" --with-decryption \
    --query Parameter.Value --output text 2>/dev/null)
if [ -n "$OTP_VAL" ]; then
    export {{ .EnvName }}="$OTP_VAL"
    aws ssm delete-parameter --name "{{ .Path }}" 2>/dev/null || true
fi
```
In the entrypoint, `{{ .Path }}` is replaced by iterating `KM_OTP_PATHS` (comma-separated); `{{ .EnvName }}` is derived from the path basename.

### Existing mail poller script (source: `pkg/compiler/userdata.go:388-429`)
The full bash mail poller script in userdata.go is the reference implementation. It is copied verbatim into `containers/sandbox/entrypoint.sh` and started as a background process if `KM_EMAIL_ADDRESS` is set.

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `yum` on Amazon Linux | `dnf` on Amazon Linux 2023 | AL2023 release | Must use `dnf` in Dockerfile and init commands |
| Per-profile Docker images | Single base image + env var configuration | Container-native design (Phase 36) | No per-profile builds; profile config arrives via task definition env vars |
| EC2 user-data script | Container entrypoint script | Phase 36 | Same logic, different delivery mechanism |
| `MAIN_IMAGE_PLACEHOLDER` in ECS task | Real `km-sandbox` ECR URI | Phase 36 | First time ECS substrate is actually runnable end-to-end |

**Deprecated/outdated:**
- `MAIN_IMAGE_PLACEHOLDER` in `service_hcl.go:596`: placeholder has existed since ECS was added; Phase 36 replaces it with the real ECR URI.

---

## Open Questions

1. **SIGTERM / artifact upload during live container session**
   - What we know: `exec gosu sandbox /bin/bash` replaces PID 1 with bash; the trap set before exec is gone; ECS sends SIGTERM to PID 1 on task stop
   - What's unclear: Whether ECS sends SIGTERM to the bash process (new PID 1) or to the original entrypoint PID
   - Recommendation: ECS sends SIGTERM to PID 1 (the exec'd bash). Install a SIGTERM handler in `/home/sandbox/.bash_profile` that triggers artifact upload. Also add a `km-shutdown.sh` profile.d script. This is sufficient for graceful shutdown.

2. **initCommands encoding: base64 JSON array vs newline-separated**
   - What we know: CONTEXT.md specifies `KM_INIT_COMMANDS` as base64-encoded JSON array; EC2 path uploads a shell script to S3 (`km-init.sh`)
   - What's unclear: Whether the compiler already encodes init commands as a JSON array or as a shell script
   - Recommendation: Check `pkg/compiler/userdata.go:648-660` — it downloads `km-init.sh` from S3. For the container path, the compiler should build a base64-encoded JSON array of strings and set it as `KM_INIT_COMMANDS`. The entrypoint decodes and executes each command.

3. **`km create --substrate docker` auto-build scope**
   - What we know: CONTEXT.md specifies auto-build when `km-sandbox:latest` is missing locally
   - What's unclear: Phase 37 (Docker substrate) is where `km create --substrate docker` is implemented; Phase 36 only provides the image
   - Recommendation: Phase 36 does NOT need to implement auto-build detection in `km create`. That belongs to Phase 37. Phase 36 only adds `make sandbox-image` and the ECR push to `km init`.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go test (stdlib) — `go test ./...` |
| Config file | none — Go test discovery |
| Quick run command | `go test ./pkg/compiler/... -run TestECS` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map

| Behavior | Test Type | Automated Command | File Exists? |
|----------|-----------|-------------------|-------------|
| `generateECSServiceHCL` emits real `km-sandbox` ECR URI (not `MAIN_IMAGE_PLACEHOLDER`) | unit | `go test ./pkg/compiler/... -run TestECSServiceHCLSandboxImage` | Wave 0 |
| `generateECSServiceHCL` includes `KM_PROXY_CA_CERT_S3` env var in main container | unit | `go test ./pkg/compiler/... -run TestECSMainContainerEntrypointEnvVars` | Wave 0 |
| `generateECSServiceHCL` includes `KM_SECRET_PATHS` env var in main container | unit | `go test ./pkg/compiler/... -run TestECSMainContainerEntrypointEnvVars` | Wave 0 |
| `generateECSServiceHCL` includes `KM_INIT_COMMANDS` (base64) in main container | unit | `go test ./pkg/compiler/... -run TestECSMainContainerEntrypointEnvVars` | Wave 0 |
| `docker build containers/sandbox/` succeeds | smoke | `docker build --platform linux/amd64 containers/sandbox/` | manual — Wave 0 |
| entrypoint exits 0 with no env vars (all optional) | smoke | `docker run --rm km-sandbox:latest env` | manual |
| entrypoint writes CA trust vars to environment before user drop | shell unit | `bash -c 'source entrypoint.sh; [[ -n "$SSL_CERT_FILE" ]]'` | Wave 0 |
| `make ecr-repos` creates `km-sandbox` repository | smoke | `make ecr-repos` (integration) | manual |
| `km init` pushes `km-sandbox` image to ECR | integration | `km init` (live AWS) | manual |

### Sampling Rate

- **Per task commit:** `go test ./pkg/compiler/... -run TestECS`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/compiler/service_hcl_test.go` — add `TestECSServiceHCLSandboxImage` test verifying `km-sandbox` ECR URI replaces `MAIN_IMAGE_PLACEHOLDER`
- [ ] `pkg/compiler/service_hcl_test.go` — add `TestECSMainContainerEntrypointEnvVars` test verifying `KM_PROXY_CA_CERT_S3`, `KM_SECRET_PATHS`, `KM_INIT_COMMANDS`, `KM_RSYNC_SNAPSHOT`, `KM_EMAIL_ADDRESS` appear in main container environment block
- [ ] `containers/sandbox/` directory — does not exist yet, created in Wave 1

---

## Sources

### Primary (HIGH confidence)

- Codebase: `pkg/compiler/userdata.go` — the 666-line EC2 bootstrap template that is the direct source for `entrypoint.sh`
- Codebase: `pkg/compiler/service_hcl.go` — ECS task definition template; integration points for `MainImage` and env vars
- Codebase: `internal/app/cmd/init.go` — `buildAndUploadSidecars` function at line 589; pattern for adding sandbox image to init pipeline
- Codebase: `Makefile` — `ecr-push`, `ecr-repos`, `ecr-login` targets; exact pattern for sandbox image target
- Codebase: `sidecars/http-proxy/Dockerfile` — multi-stage Dockerfile pattern used by all sidecar images

### Secondary (MEDIUM confidence)

- Amazon Linux 2023 documentation — `dnf` as primary package manager; `/etc/pki/ca-trust/source/anchors/` CA path; `update-ca-trust` command (confirmed by Phase 35 implementation in userdata.go)
- gosu 1.17 release page (GitHub: tianon/gosu) — standard container user-drop tool; `gosu-amd64` binary for x86_64
- AWS CLI v2 installation guide — official zip download method; `awscli-exe-linux-x86_64.zip` URL pattern

### Tertiary (LOW confidence)

- None

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all decisions locked in CONTEXT.md; patterns verified directly from codebase
- Architecture: HIGH — entrypoint design is a direct port of verified EC2 code; integration points are identified to the line
- Pitfalls: HIGH — most derive from auditing existing code; SIGTERM race condition is architectural (MEDIUM for the specific resolution path)

**Research date:** 2026-03-29
**Valid until:** 2026-06-01 (stable domain; AL2023 package manager and CA trust paths are stable)
