# Phase 36: km-sandbox Base Container Image

## Problem

The EC2 substrate works end-to-end because user-data (a 600+ line shell script) bootstraps everything at instance boot: creates users, installs sidecars, sets up iptables, injects secrets, configures proxy trust, runs initCommands, restores rsync snapshots, wires GitHub credential helpers, installs OTEL telemetry, and starts the mail poller.

ECS Fargate has none of this. The main container image is literally `MAIN_IMAGE_PLACEHOLDER`. There is no container image that does what user-data does on EC2.

## Goal

A `km-sandbox` base container image (pushed to ECR during `km init`) that provides the same sandbox environment as EC2 user-data, but as a container entrypoint. Profile-specific customization happens at container start via environment variables and an entrypoint script — no per-profile image builds required.

## Design

### Image: `km-sandbox`

```
Base:     amazonlinux:2023-minimal  (matches EC2 AMI)
Registry: {ACCOUNT_ID}.dkr.ecr.{REGION}.amazonaws.com/km-sandbox:latest
Built by: km init (alongside sidecar images)
```

### What the image contains (baked in at build time)

| Component | Purpose |
|-----------|---------|
| `sandbox` user (UID 1000) | Non-root execution, matches EC2 `sandbox` user |
| `/workspace` directory | Default working directory, owned by sandbox |
| System CA trust store | Base Amazon Linux CAs |
| AWS CLI v2 | S3, SSM, SES operations from entrypoint |
| SSM agent | `km shell` access into the container |
| Git | Source access |
| jq, tar, gzip, unzip | Common tooling |
| `/opt/km/entrypoint.sh` | Container entrypoint (see below) |
| `/opt/km/bin/km-mail-poller` | Background mail sync (built from Go, same as EC2) |
| `/opt/km/bin/km-upload-artifacts` | Artifact upload on shutdown |

### What the entrypoint does (at container start, from env vars)

The entrypoint script replaces user-data. It reads configuration from environment variables (set by the ECS task definition, which the compiler generates from the profile):

```
KM_SANDBOX_ID           — sandbox ID
KM_ARTIFACTS_BUCKET     — S3 bucket for artifacts, CA cert, snapshots
KM_STATE_BUCKET         — S3 bucket for metadata
KM_PROXY_CA_CERT_S3     — S3 path to proxy CA cert (sidecars/km-proxy-ca.crt)
KM_PROXY_CA_KEY_S3      — S3 path to proxy CA key (sidecars/km-proxy-ca.key)
KM_SECRET_PATHS         — comma-separated SSM paths to inject as env vars
KM_OTP_PATHS            — comma-separated SSM paths to inject-then-delete
KM_INIT_COMMANDS        — base64-encoded JSON array of shell commands
KM_RSYNC_SNAPSHOT       — name of rsync snapshot to restore from S3
KM_GITHUB_TOKEN_SSM     — SSM path for GitHub token (GIT_ASKPASS wiring)
KM_GITHUB_ALLOWED_REFS  — comma-separated allowed git refs (pre-push hook)
KM_PROFILE_ENV          — base64-encoded JSON object of profile env vars
KM_EMAIL_ADDRESS        — sandbox email address
KM_OPERATOR_EMAIL       — operator notification address
```

Entrypoint execution order (mirrors user-data sections):

1. **Proxy CA trust** — Fetch CA cert from S3, install in `/etc/pki/ca-trust/source/anchors/`, run `update-ca-trust`, export `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`
2. **Secret injection** — Fetch each SSM path in `KM_SECRET_PATHS`, export as env vars
3. **OTP injection** — Fetch each SSM path in `KM_OTP_PATHS`, export as env vars, delete from SSM
4. **Profile env vars** — Decode `KM_PROFILE_ENV`, export each key-value pair
5. **GitHub credential helper** — If `KM_GITHUB_TOKEN_SSM` set, write GIT_ASKPASS script
6. **Git ref enforcement** — If `KM_GITHUB_ALLOWED_REFS` set, install pre-push hook template
7. **Rsync restore** — If `KM_RSYNC_SNAPSHOT` set, download and extract from S3
8. **Init commands** — Decode `KM_INIT_COMMANDS`, execute each as root
9. **Mail poller** — Start `/opt/km/bin/km-mail-poller` in background (if email configured)
10. **Drop to sandbox user** — `exec su - sandbox` or `exec gosu sandbox /bin/bash`

### What the entrypoint does NOT do (handled elsewhere)

| Concern | Handled by |
|---------|------------|
| iptables / DNAT | Not needed — ECS `awsvpc` shares localhost; proxy env vars sufficient |
| IMDSv2 | Not needed — ECS uses task role credentials, no IMDS |
| SSM agent start | ECS exec agent or SSM container (separate concern) |
| Sidecar startup | Separate containers in task definition (already wired) |
| Security groups | Terraform module (already wired) |
| IAM role | Task role in task definition (already wired) |

### Dockerfile

```dockerfile
FROM amazonlinux:2023-minimal AS base

# System packages
RUN dnf install -y \
    shadow-utils tar gzip unzip jq git \
    ca-certificates python3 python3-pip \
    && dnf clean all

# AWS CLI v2
RUN curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o awscliv2.zip \
    && unzip -q awscliv2.zip && ./aws/install && rm -rf aws awscliv2.zip

# Sandbox user (non-root, matches EC2)
RUN useradd -m -d /home/sandbox -s /bin/bash -u 1000 sandbox \
    && mkdir -p /workspace && chown sandbox:sandbox /workspace

# Entrypoint + helpers
COPY entrypoint.sh /opt/km/entrypoint.sh
COPY km-mail-poller /opt/km/bin/km-mail-poller
COPY km-upload-artifacts /opt/km/bin/km-upload-artifacts
RUN chmod +x /opt/km/entrypoint.sh /opt/km/bin/*

WORKDIR /workspace
ENTRYPOINT ["/opt/km/entrypoint.sh"]
```

### Build pipeline

`km init` already builds sidecar binaries and pushes Docker images to ECR. The sandbox base image follows the same pattern:

1. `make sandbox-image` — builds the km-sandbox image locally
2. `km init` pushes to `{ACCOUNT}.dkr.ecr.{REGION}.amazonaws.com/km-sandbox:latest`
3. The compiler references this image in the ECS task definition (replacing `MAIN_IMAGE_PLACEHOLDER`)

### Files to create/modify

| File | Change |
|------|--------|
| `containers/sandbox/Dockerfile` | **New** — base sandbox image |
| `containers/sandbox/entrypoint.sh` | **New** — container entrypoint (replaces user-data) |
| `Makefile` | Add `sandbox-image` target |
| `internal/app/cmd/init.go` | Push km-sandbox image to ECR alongside sidecars |
| `pkg/compiler/service_hcl.go` | Replace `MAIN_IMAGE_PLACEHOLDER` with real ECR URI |
| `pkg/compiler/service_hcl.go` | Add entrypoint env vars to main container definition |

### Testing

1. `docker build` succeeds for the sandbox image
2. Entrypoint handles missing env vars gracefully (skip steps, don't crash)
3. Entrypoint with all env vars set produces the expected /etc/profile.d exports
4. `km validate` still passes for all profiles
5. Compiler generates correct ECR image URI for main container
