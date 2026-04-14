# Docker Compose Local Substrate

Run sandboxes on your local machine using Docker Compose instead of provisioning EC2 instances or ECS tasks on AWS. Same profile YAML, same sidecar enforcement, same audit trail -- just local containers instead of cloud infrastructure.

---

## Quick Start

```bash
# Create a local sandbox (Docker Compose)
km create profiles/goose.yaml --docker

# Connect
km shell 1

# Pause / resume
km pause 1
km resume 1

# Destroy (removes containers, volumes, IAM roles, metadata)
km destroy 1 --yes
```

---

## When to Use Docker vs EC2

| | Docker | EC2 |
|---|---|---|
| **Provisioning time** | ~10 seconds | ~45-90 seconds |
| **Runs on** | Your local machine | AWS |
| **Cost** | Free (local compute) | ~$0.01/hr (spot) |
| **Network enforcement** | Proxy mode only | Proxy, eBPF, or both |
| **eBPF enforcement** | Not available | Full cgroup eBPF |
| **SSM sessions** | N/A (uses `docker exec`) | Full SSM audit trail |
| **Budget metering** | Yes (proxy-based) | Yes (proxy + IAM revocation) |
| **Port forwarding** | `docker port` / compose ports | `km shell --ports` (SSM) |
| **Hibernate/pause** | `docker compose pause` | EC2 hibernation (RAM to EBS) |
| **EFS shared storage** | Not available | Regional EFS mount |
| **Best for** | Development, testing, iterating on profiles | Production, multi-agent, security-sensitive workloads |

---

## How It Works

`km create --docker` brings up a 6-container Docker Compose stack on your local machine:

```
~/.km/sandboxes/{sandboxID}/
├── docker-compose.yml      Generated from profile
├── km-proxy-ca.crt         MITM CA certificate (auto-generated)
├── km-audit-init.sh        Audit hook installer
└── .km-ttl                 TTL expiry timestamp

Docker Compose Stack (km-net bridge: 172.28.0.0/24)
┌─────────────────────────────────────────────────────────┐
│                                                         │
│  km-{id}-main          The sandbox container            │
│  ├── DNS: 172.28.0.10 (km-dns-proxy)                   │
│  ├── HTTP_PROXY: http://km-http-proxy:3128              │
│  ├── Creds: /creds/sandbox (read-only)                  │
│  └── Volume: km-workspace → /workspace                  │
│                                                         │
│  km-{id}-dns-proxy     DNS allowlist (172.28.0.10)      │
│  ├── ALLOWED_SUFFIXES from profile                      │
│  └── Upstream: 8.8.8.8                                  │
│                                                         │
│  km-{id}-http-proxy    HTTP/HTTPS allowlist + MITM      │
│  ├── ALLOWED_HOSTS from profile                         │
│  ├── Budget metering (Bedrock, Anthropic, OpenAI)       │
│  ├── GitHub repo filtering                              │
│  └── Creds: /creds/sidecar (read-only)                  │
│                                                         │
│  km-{id}-audit-log     Command + network logging        │
│  ├── Reads from /run/km/audit-pipe (named pipe)         │
│  └── Writes to S3: s3://{bucket}/audit/{id}/            │
│                                                         │
│  km-{id}-cred-refresh  Credential rotation (60s loop)   │
│  ├── Mounts ~/.aws (read-only from host)                │
│  ├── Assumes sandbox + sidecar IAM roles via STS        │
│  └── Writes to /creds/ shared volume                    │
│                                                         │
│  km-{id}-audit-init    One-shot init container          │
│  └── Creates /run/km/audit-pipe named pipe              │
│                                                         │
│  Volumes: km-workspace, cred-vol, audit-vol             │
│  Network: km-net (bridge, 172.28.0.0/24)                │
└─────────────────────────────────────────────────────────┘
```

### Credential Isolation

Only `km-cred-refresh` has access to the host's `~/.aws` credentials. It runs in a loop:

1. Reads operator's AWS credentials from `~/.aws/` (host mount, read-only)
2. Assumes the sandbox IAM role (`km-docker-{id}-{region}`) via STS
3. Assumes the sidecar IAM role (`km-sidecar-{id}-{region}`) via STS
4. Writes temporary credentials to the `/creds/` shared volume
5. All other containers read from `/creds/sandbox` or `/creds/sidecar` (read-only mount)

If the sandbox container is compromised, it cannot access the operator's credentials -- only the scoped sandbox role credentials.

**Note:** STS temporary credentials expire after 1 hour. The `km-cred-refresh` container re-assumes roles before expiry to maintain continuous access.

### MITM CA Certificate

At create time, `km` generates an ephemeral ECDSA P-256 CA certificate (valid 24h). This CA signs leaf certificates for HTTPS MITM inspection in the HTTP proxy. The cert is:

- Written to `{sandboxDir}/km-proxy-ca.crt` (mounted into the main container)
- Base64-encoded and passed as `KM_PROXY_CA_CERT` env var to the HTTP proxy
- Installed into the main container's OS trust store via init commands

---

## Prerequisites

1. **Docker Desktop** or **Docker Engine** with Docker Compose v2
2. **AWS CLI** configured with named profiles (for IAM role creation and ECR login)
3. **ECR access** (optional) -- if using ECR-hosted sidecar images. Without ECR, local images named `km-sandbox:latest`, `km-dns-proxy:latest`, etc. must be available.

### Building Local Images

If you don't have ECR access or prefer local images:

```bash
# Build the sandbox base image locally
make sandbox-image

# Or build individually
docker buildx build --platform linux/amd64 -t km-sandbox:latest -f containers/sandbox/Dockerfile containers/sandbox/
docker buildx build --platform linux/amd64 -t km-dns-proxy:latest -f sidecars/dns-proxy/Dockerfile .
docker buildx build --platform linux/amd64 -t km-http-proxy:latest -f sidecars/http-proxy/Dockerfile .
docker buildx build --platform linux/amd64 -t km-audit-log:latest -f sidecars/audit-log/Dockerfile .
```

If `KM_ACCOUNTS_APPLICATION` is set (your AWS account ID), `km create --docker` pulls images from ECR: `{accountID}.dkr.ecr.{region}.amazonaws.com/km-{name}:latest`. Otherwise it uses local image names.

---

## Create

```bash
# Using --docker shortcut
km create profiles/goose.yaml --docker

# Using --substrate flag
km create profiles/goose.yaml --substrate docker

# With alias
km create profiles/goose.yaml --docker --alias mybot

# Disable Bedrock (use direct API keys)
km create profiles/goose.yaml --docker --no-bedrock

# Verbose output (shows docker compose logs)
km create profiles/goose.yaml --docker --verbose
```

**What happens:**

1. Creates sandbox directory at `~/.km/sandboxes/{sandboxID}/`
2. Creates two IAM roles:
   - `km-docker-{sandboxID}-{region}` -- sandbox role (Bedrock, SSM, S3)
   - `km-sidecar-{sandboxID}-{region}` -- sidecar role (DynamoDB budgets, S3 audit)
3. Waits for IAM propagation (~20 seconds)
4. Generates MITM CA certificate
5. Writes `docker-compose.yml` with all profile settings
6. Runs `docker compose up -d`
7. Stores metadata in DynamoDB (visible via `km list`)

**Docker creates always run locally** -- the Docker substrate defaults to local execution and does not support Lambda dispatch. The `--remote` flag has no effect for Docker sandboxes.

---

## Shell

```bash
km shell 1                  # restricted sandbox user
km shell 1 --root           # root access
km shell goose-abc123       # by sandbox ID
```

Uses `docker exec -it km-{sandboxID}-main bash --login` (no SSM session). The `--login` flag ensures `/etc/profile.d/` scripts run for environment setup.

---

## Pause, Stop, Resume

```bash
# Pause (freeze containers in place)
km pause 1
# Equivalent to: docker compose -p km-{id} pause

# Stop (shut down containers, preserve volumes)
km stop 1
# Equivalent to: docker compose -p km-{id} stop

# Resume a paused or stopped sandbox
km resume 1
```

---

## Destroy

```bash
km destroy 1 --yes
# Or: km kill 1 --yes
```

**What happens:**

1. Verifies `docker-compose.yml` exists locally (Docker sandboxes are host-specific)
2. Runs `docker compose down -v` (removes containers AND volumes)
3. Deletes IAM roles (`km-docker-*`, `km-sidecar-*`)
4. Deletes SSM GitHub token parameter (if any)
5. Deletes DynamoDB metadata
6. Removes local sandbox directory

**Important:** Docker sandboxes can only be destroyed from the machine that created them. If `docker-compose.yml` is not found, `km destroy` returns: *"docker sandbox {id} is not running on this host (no {path} found). This sandbox may be running on another machine. Use km list to check."*

---

## Profile Constraints

Docker substrate enforces the following constraints:

| Feature | Docker behavior |
|---------|----------------|
| `spec.network.enforcement` | Always `proxy` -- eBPF enforcement is EC2-only |
| `spec.runtime.spot` | Ignored (no spot/on-demand concept) |
| `spec.runtime.instanceType` | Ignored (uses local Docker resources) |
| `spec.runtime.hibernation` | N/A (use `km pause` / `docker compose pause`) |
| `spec.runtime.mountEFS` | Not available (EC2-only) |
| `spec.runtime.additionalVolume` | N/A (use Docker volumes) |
| `spec.lifecycle.ttl` | Written to `.km-ttl` file; no automatic enforcement (operator's responsibility) |
| `spec.sidecars` | DNS proxy, HTTP proxy, and audit log run as containers; tracing is optional |

Any profile can run on Docker -- unsupported fields are silently ignored. To validate a profile specifically for Docker:

```bash
km validate my-profile.yaml   # schema validation works for all substrates
```

---

## Networking

All containers share the `km-net` bridge network (172.28.0.0/24):

| Container | IP | Port |
|-----------|-----|------|
| km-dns-proxy | 172.28.0.10 | 53 (DNS) |
| km-http-proxy | dynamic | 3128 (HTTP proxy) |
| main | dynamic | -- |

The main container's DNS is set to 172.28.0.10 (km-dns-proxy), and `HTTP_PROXY` / `HTTPS_PROXY` point to `http://km-http-proxy:3128`. This ensures all DNS and HTTP traffic flows through the enforcement sidecars.

---

## Environment Variables

### Set by the operator

| Variable | Description |
|----------|-------------|
| `KM_ACCOUNTS_APPLICATION` | AWS account ID for ECR image URIs (optional) |
| `KM_AWS_PROFILE` | Operator's AWS profile for cred-refresh (default: `klanker-terraform`) |

### Injected into containers

| Variable | Container | Description |
|----------|-----------|-------------|
| `KM_SANDBOX_ID` | all | Sandbox identifier |
| `KM_REGION` | all | AWS region |
| `KM_ARTIFACTS_BUCKET` | main, sidecars | S3 artifacts bucket |
| `ALLOWED_SUFFIXES` | dns-proxy | Space-separated DNS suffix allowlist |
| `ALLOWED_HOSTS` | http-proxy | Space-separated HTTP host allowlist |
| `KM_PROXY_CA_CERT` | http-proxy | Base64-encoded MITM CA cert+key |
| `KM_BUDGET_TABLE` | http-proxy | DynamoDB budget table name |
| `KM_INIT_COMMANDS` | main | Base64-encoded init commands from profile |
| `KM_PROFILE_ENV` | main | Base64-encoded profile environment variables |
| `AWS_SHARED_CREDENTIALS_FILE` | main, sidecars | Path to credential file (`/creds/sandbox` or `/creds/sidecar`) |

---

## Troubleshooting

### Containers not starting

```bash
# Check container status
docker compose -p km-{sandboxID} ps

# Check logs for a specific service
docker compose -p km-{sandboxID} logs km-http-proxy
docker compose -p km-{sandboxID} logs km-cred-refresh

# Check if credentials are being refreshed
docker exec km-{sandboxID}-main cat /creds/sandbox
```

### DNS not resolving

```bash
# Check DNS proxy logs
docker compose -p km-{sandboxID} logs km-dns-proxy

# Test DNS from inside the sandbox
docker exec km-{sandboxID}-main nslookup github.com 172.28.0.10
```

### HTTP proxy blocking requests

```bash
# Check proxy logs
docker compose -p km-{sandboxID} logs km-http-proxy

# Test from inside the sandbox (should show ALLOWED_HOSTS)
docker exec km-{sandboxID}-main env | grep PROXY
docker exec km-{sandboxID}-main curl -v https://api.github.com
```

### Credential errors (403 / expired)

The `km-cred-refresh` container assumes IAM roles every 60 seconds. If credentials are stale:

```bash
# Check cred-refresh logs
docker compose -p km-{sandboxID} logs km-cred-refresh

# Verify IAM role exists
aws iam get-role --role-name km-docker-{sandboxID}-us-east-1 --profile klanker-terraform

# Force credential refresh (restart the container)
docker restart km-{sandboxID}-cred-refresh
```

### ECR login failures

```bash
# Manual ECR login
aws ecr get-login-password --region us-east-1 --profile klanker-terraform | \
  docker login --username AWS --password-stdin {accountID}.dkr.ecr.us-east-1.amazonaws.com
```

If ECR login fails, `km create --docker` continues and tries to use local images. Build them locally with `make sandbox-image` and the individual sidecar `docker buildx build` commands shown above.

### Sandbox on a different machine

Docker sandboxes are host-local. If `km list` shows a Docker sandbox but `km destroy` says it's not on this host, the sandbox was created on a different machine. Either destroy from that machine or manually clean up:

```bash
# Manual cleanup (from any machine with AWS access)
aws iam delete-role-policy --role-name km-docker-{id}-us-east-1 --policy-name km-sandbox-inline
aws iam delete-role --role-name km-docker-{id}-us-east-1
aws iam delete-role-policy --role-name km-sidecar-{id}-us-east-1 --policy-name km-sidecar-inline
aws iam delete-role --role-name km-sidecar-{id}-us-east-1
# Then delete metadata from DynamoDB
```

---

## Comparison with EC2 Substrate

### What's the same

- Profile YAML schema (same profiles work on both substrates)
- DNS allowlist enforcement (km-dns-proxy sidecar)
- HTTP allowlist enforcement (km-http-proxy sidecar with MITM)
- Budget metering (Bedrock, Anthropic, OpenAI token counting)
- GitHub repo filtering (allowedRepos in proxy)
- Audit logging to S3
- Metadata in DynamoDB (`km list` shows both Docker and EC2 sandboxes)
- `km shell`, `km pause`, `km stop`, `km destroy` all work

### What's different

- **No Terragrunt** -- Docker skips all infrastructure provisioning
- **No SSM** -- shell uses `docker exec` instead of SSM sessions
- **No eBPF** -- enforcement is proxy-only (eBPF requires Linux kernel cgroups on EC2)
- **No EFS** -- shared filesystem is EC2-only
- **No hibernation** -- `km pause` freezes containers (no RAM-to-disk)
- **No spot pricing** -- runs on local Docker resources
- **No TTL enforcement** -- `.km-ttl` is written but not automatically enforced
- **Host-local** -- sandbox only accessible from the creating machine
- **Credential refresh** -- uses STS assume-role loop instead of EC2 instance profiles
