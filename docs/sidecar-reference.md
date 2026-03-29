# Klanker Maker Sidecar Reference

Every Klanker Maker sandbox runs four sidecars that enforce network policy,
collect audit telemetry, and export traces. On EC2 they run as systemd
services. On ECS Fargate they run as sidecar containers in the task
definition sharing the `awsvpc` network namespace with the main container.

## Table of Contents

1. [DNS Proxy](#1-dns-proxy)
2. [HTTP Proxy](#2-http-proxy)
3. [Audit Log](#3-audit-log)
4. [Tracing](#4-tracing)
5. [Build and Deployment Pipeline](#5-build-and-deployment-pipeline)
6. [iptables DNAT on EC2](#iptables-dnat-on-ec2)
7. [Container Dependency Ordering on ECS](#container-dependency-ordering-on-ecs)
8. [Debugging Blocked Requests](#debugging-blocked-requests)

---

## 1. DNS Proxy

| | |
|---|---|
| **Binary** | `sidecars/dns-proxy/` (Go, `github.com/miekg/dns`) |
| **Listens** | UDP :53 and TCP :53 (concurrent servers) |
| **Purpose** | Filter DNS queries against an allowlist of domain suffixes |

### Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `ALLOWED_SUFFIXES` | Yes | *(empty -- denies all)* | Comma-separated domain suffixes, e.g. `.amazonaws.com,.github.com` |
| `UPSTREAM_DNS` | No | `169.254.169.253` | Upstream resolver (AWS VPC DNS). Port 53 is appended automatically if omitted. |
| `DNS_PORT` | No | `53` | Listen port for both UDP and TCP servers |
| `SANDBOX_ID` | No | `unknown` | Sandbox identifier attached to every log line |

### Behavior

1. Each incoming query is checked against `ALLOWED_SUFFIXES` using
   case-insensitive suffix matching. A trailing DNS dot is stripped before
   comparison. A name matches if it equals a suffix or ends with `.<suffix>`.
2. **Allowed** -- the query is forwarded to `UPSTREAM_DNS` and the upstream
   response is returned to the client.
3. **Denied** -- an `NXDOMAIN` (RCODE 3) response is returned immediately.
4. If the upstream resolver returns an error, the proxy responds with
   `SERVFAIL` and logs the error.

### Log Format

JSON to stdout via `zerolog`. Every query emits:

```json
{
  "level": "info",
  "sandbox_id": "sb-a1b2c3d4",
  "event_type": "dns_query",
  "domain": "api.github.com.",
  "allowed": true,
  "time": "2026-03-22T10:00:00Z"
}
```

Startup emits `event_type: "dns_proxy_start"` with `addr`, `upstream`, and
`allowed_suffixes`. Upstream errors emit `event_type: "dns_upstream_error"`.

### Health Check

```
dig +short +timeout=2 @127.0.0.1 health.check || exit 1
```

The proxy will respond with `NXDOMAIN` for any name not in the allowlist,
which is sufficient to prove the process is alive and listening. On ECS, use
the `CMD` health check form:

```json
["CMD-SHELL", "dig +short +timeout=2 @127.0.0.1 health.check || exit 1"]
```

### EC2 Deployment

Runs as a systemd service (`km-dns-proxy.service`). An iptables DNAT rule
redirects all outbound DNS traffic through the proxy (see
[iptables DNAT](#iptables-dnat-on-ec2) below).

### ECS Deployment

Sidecar container in the task definition. Because Fargate uses `awsvpc`
network mode, the DNS proxy shares the same network namespace as the main
container. The main container's `/etc/resolv.conf` points to `127.0.0.1`.

---

## 2. HTTP Proxy

| | |
|---|---|
| **Binary** | `sidecars/http-proxy/` (Go, `github.com/elazarl/goproxy`) |
| **Listens** | TCP :3128 (standard Squid-compatible port) |
| **Purpose** | Filter HTTP/HTTPS by host allowlist; Bedrock token metering (Phase 6) |

### Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `ALLOWED_HOSTS` | Yes | *(empty -- denies all)* | Comma-separated hostnames, e.g. `api.anthropic.com,bedrock-runtime.us-east-1.amazonaws.com` |
| `KM_GITHUB_ALLOWED_REPOS` | No | *(empty)* | Comma-separated `owner/repo` patterns for GitHub repo-level filtering (e.g. `myorg/myrepo,myorg/*`) |
| `PROXY_PORT` | No | `3128` | Listen port |
| `SANDBOX_ID` | No | `unknown` | Sandbox identifier attached to every log line |

### Behavior

The proxy handles two request types:

- **HTTPS (CONNECT tunnel)** -- the client issues `CONNECT host:443`. If the
  host is in `ALLOWED_HOSTS` (case-insensitive, port stripped), the tunnel is
  established (`OkConnect`). W3C `traceparent` headers are injected into the
  CONNECT request via the OTel propagation API. Denied hosts receive
  `RejectConnect` (client sees HTTP 403).
- **Plain HTTP (forward proxy)** -- allowed hosts are forwarded normally.
  Denied hosts receive a `403 Forbidden` response with body
  `Blocked by km sandbox policy`.

**Bedrock interception** (Phase 6): The proxy intercepts
`InvokeModel` responses from Bedrock, extracts input/output token counts from
the response body, and increments counters in DynamoDB for quota enforcement.

**GitHub repo-level filtering** (Phase 28): When `KM_GITHUB_ALLOWED_REPOS` is
set, the proxy uses MITM to intercept HTTPS connections to GitHub hosts
(`github.com`, `api.github.com`, `raw.githubusercontent.com`,
`codeload.githubusercontent.com`), extracts `owner/repo` from the URL path,
and checks it against the allowlist. Requests to repos not in the list receive
a 403 JSON response:

```json
{"error":"repo_not_allowed","repo":"torvalds/linux","reason":"repo is not in the sandbox allowedRepos list"}
```

**Implicit host allowing:** When `KM_GITHUB_ALLOWED_REPOS` is non-empty,
GitHub hosts are implicitly allowed through the proxy regardless of
`ALLOWED_HOSTS`. This means profiles do **not** need `github.com`,
`api.github.com`, `.github.com`, or `.githubusercontent.com` in their
`network.egress` configuration -- the presence of `sourceAccess.github.allowedRepos`
is sufficient. Non-repo GitHub URLs (e.g. `api.github.com/rate_limit`,
`github.com/login`) pass through unconditionally.

When `KM_GITHUB_ALLOWED_REPOS` is empty, no GitHub MITM handlers are
registered and GitHub hosts are subject to the normal `ALLOWED_HOSTS` check.

**Allowlist format:**
- `owner/repo` -- exact match (case-insensitive)
- `owner/*` -- org wildcard, matches all repos under that owner
- `github.com/owner/repo` -- the `github.com/` prefix is stripped before comparison

### Log Format

JSON to stdout via `zerolog`. Blocked requests emit:

```json
{
  "level": "info",
  "sandbox_id": "sb-a1b2c3d4",
  "event_type": "http_blocked",
  "host": "evil.example.com:443",
  "time": "2026-03-22T10:00:00Z"
}
```

Startup emits `event_type: "http_proxy_start"` with `addr`, `allowed_hosts`,
and `sandbox_id`. GitHub filtering emits `event_type: "github_repo_allowed"`
or `"github_repo_blocked"` with `repo` and `sandbox_id` fields.

### Health Check

```
curl -sf -o /dev/null -x http://127.0.0.1:3128 http://health.check/ || exit 1
```

The proxy will return 403 for an unrecognized host, but that confirms the
process is alive. On ECS:

```json
["CMD-SHELL", "curl -sf -o /dev/null -x http://127.0.0.1:3128 http://health.check/ || exit 1"]
```

### EC2 Deployment

Runs as a systemd service (`km-http-proxy.service`). An iptables DNAT rule
redirects outbound HTTP (port 80) and HTTPS (port 443) traffic through the
proxy (see [iptables DNAT](#iptables-dnat-on-ec2) below).

### ECS Deployment

Sidecar container in the task definition. The main container has
`HTTP_PROXY=http://127.0.0.1:3128` and `HTTPS_PROXY=http://127.0.0.1:3128`
set as environment variables so all SDK and CLI traffic routes through
the proxy.

---

## 3. Audit Log

| | |
|---|---|
| **Binary** | `sidecars/audit-log/cmd/` (Go) |
| **Input** | JSON-line events from stdin |
| **Purpose** | Route audit events to a configured destination with secret redaction |

### Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `AUDIT_LOG_DEST` | No | `stdout` | Destination: `stdout`, `cloudwatch`, or `s3` |
| `SANDBOX_ID` | No | `unknown` | Sandbox identifier |
| `CW_LOG_GROUP` | No | `/km/sandboxes/{SANDBOX_ID}/` | CloudWatch Logs group name |
| `AWS_REGION` | No | `us-east-1` | AWS region for CloudWatch |

### Event Schema

All events conform to the locked JSON schema:

```json
{
  "timestamp":  "2026-03-22T10:00:00Z",
  "sandbox_id": "sb-a1b2c3d4",
  "event_type": "shell_command",
  "source":     "audit-log",
  "detail":     { "command": "ls -la" }
}
```

`event_type` values: `shell_command`, `dns_query`, `http_request`.
`source` values: `audit-log`, `dns-proxy`, `http-proxy`.

### Destinations

| Destination | Behavior |
|---|---|
| **StdoutDest** | Writes JSON events as newline-delimited lines to stdout |
| **CloudWatchDest** | Batches events (flush threshold: 25) and sends to CloudWatch Logs via `PutLogEvents`. Creates the log group and stream on startup. |
| **S3Dest** | Stub -- falls back to stdout with a warning. Full S3 archive delivery is Phase 4 scope. |

### Secret Redaction

The `RedactingDestination` wrapper scans all string values in the `detail`
map (recursively into nested maps and slices) and replaces matches with
`[REDACTED]`. Structural fields (`sandbox_id`, `event_type`, `timestamp`,
`source`) are never modified.

Built-in patterns:
- AWS access key IDs: `AKIA[A-Z0-9]{16}`
- Bearer tokens: `Bearer [A-Za-z0-9\-._~+/]+=*`
- Hex strings (40+ chars): `[0-9a-f]{40,}`
- Literal SSM parameter values provided at construction time

### Signal Handling

On `SIGTERM` or `SIGINT`, the sidecar flushes all buffered events to the
destination and exits cleanly with code 0. This ensures no audit data is
lost during sandbox teardown.

### Health Check

The audit-log sidecar reads from stdin and has no listen port. Health is
verified by checking the process is alive:

```json
["CMD-SHELL", "pgrep -f audit-log || exit 1"]
```

### EC2 Deployment

Piped from the shell audit hook. A `PROMPT_COMMAND` in
`/etc/profile.d/km-audit.sh` emits JSON events to a named pipe. The
audit-log sidecar reads from that pipe. It also receives forwarded events
from the DNS proxy and HTTP proxy via systemd journal piping.

### ECS Deployment

The `awslogs` log driver captures container stdout for each sidecar. The
audit-log sidecar processes events piped from other containers via shared
volumes or direct stdout aggregation.

---

## 4. Tracing

| | |
|---|---|
| **Directory** | `sidecars/tracing/` (scaffolding phase) |
| **Binary** | `otelcol-contrib` with custom config |
| **Purpose** | OTel trace/span collection from sandbox workloads, export to S3 |

### Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `OTEL_COLLECTOR_ENDPOINT` | No | `0.0.0.0:4317` (gRPC), `0.0.0.0:4318` (HTTP) | Collector listen endpoints |
| `MLFLOW_TRACKING_URI` | Planned | -- | MLflow tracking server URI |
| `MLFLOW_EXPERIMENT_NAME` | Planned | -- | MLflow experiment name |
| `SANDBOX_ID` | Yes | -- | Used for S3 prefix partitioning |
| `AWS_REGION` | Yes | -- | Region for S3 exporter |
| `OTEL_S3_BUCKET` | Yes | -- | S3 bucket for trace export |

### Configuration

The collector runs with `sidecars/tracing/config.yaml`:

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

processors:
  batch:
    timeout: 10s
    send_batch_size: 1024

exporters:
  awss3:
    s3_uploader:
      region: ${AWS_REGION}
      s3_bucket: ${OTEL_S3_BUCKET}
      s3_prefix: "traces/${SANDBOX_ID}"
    marshaler: otlp_json
    timeout: 30s
```

Traces are batched (10s / 1024 spans) and exported as OTLP JSON to
`s3://<bucket>/traces/<sandbox-id>/`.

### Planned Features

- **MLflow run logging**: Each sandbox session creates an MLflow run with
  metadata (profile name, sandbox-id, duration, exit status).

### Health Check

```
curl -sf http://127.0.0.1:13133/health || exit 1
```

The OTel collector exposes a health check extension on port 13133 by default.

### EC2 Deployment

Runs as a standalone `otelcol-contrib` process with the config file at
`/etc/km/tracing/config.yaml`.

### ECS Deployment

OTel sidecar container in the task definition. Shares the network namespace
with the main container so workloads can send traces to `localhost:4317`
(gRPC) or `localhost:4318` (HTTP).

---

## 5. Build and Deployment Pipeline

Sidecars are distributed in two forms depending on the sandbox substrate:

- **EC2**: Pre-compiled Go binaries delivered via S3 and installed as systemd services.
- **ECS Fargate**: Docker images pushed to ECR and referenced in the ECS task definition.

The `Makefile` at the repository root drives both pipelines.

---

### Build Pipeline (`make sidecars`)

`make sidecars` cross-compiles the three Go sidecar binaries for `linux/amd64` and uploads them to S3. It also uploads the tracing OTel collector config file.

```bash
# KM_ARTIFACTS_BUCKET must be set in your environment
export KM_ARTIFACTS_BUCKET=km-sandbox-artifacts-ea554771

make sidecars
```

**What it builds:**

| Sidecar | Source | Output |
|---------|--------|--------|
| `dns-proxy` | `./sidecars/dns-proxy/` | `build/dns-proxy` |
| `http-proxy` | `./sidecars/http-proxy/` | `build/http-proxy` |
| `audit-log` | `./sidecars/audit-log/cmd/` | `build/audit-log` |
| `tracing` | `sidecars/tracing/config.yaml` | config file only (not a Go binary — see below) |

**What it uploads to S3:**

```
s3://{KM_ARTIFACTS_BUCKET}/sidecars/dns-proxy
s3://{KM_ARTIFACTS_BUCKET}/sidecars/http-proxy
s3://{KM_ARTIFACTS_BUCKET}/sidecars/audit-log
s3://{KM_ARTIFACTS_BUCKET}/sidecars/tracing/config.yaml
```

**Local-only build (no S3 upload):**

```bash
make build-sidecars
```

Produces the same binaries in `build/` without requiring AWS credentials or `KM_ARTIFACTS_BUCKET`.

---

### Build Context Notes

**`audit-log`** — The Dockerfile builds from `./sidecars/audit-log/cmd/`, not the package root. The `sidecars/audit-log/` directory is a Go library package (`package auditlog`); the `cmd/` subdirectory holds `package main`. Go prohibits two packages in the same directory, so `cmd/` separates the library from the binary entry point.

```dockerfile
# sidecars/audit-log/Dockerfile
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /sidecar ./sidecars/audit-log/cmd/
```

**`dns-proxy` and `http-proxy`** — Follow the same `cmd/` library-separation pattern. Each sidecar has a `dnsproxy/` or `httpproxy/` subdirectory for the library code and a `main.go` at the sidecar root for the binary entry point.

**`tracing`** — Not a Go binary. The tracing sidecar runs `otelcol-contrib` (the OpenTelemetry Collector Contrib distribution) with a custom YAML configuration. There is no Go compilation step for tracing.

---

### Docker Images for ECS (`make ecr-push`)

`make ecr-push` builds Docker images for all four sidecars and pushes them to ECR in the application account. It depends on `ecr-login` (authenticates Docker to ECR) and `ecr-repos` (ensures the repositories exist).

```bash
# KM_ACCOUNTS_APPLICATION and AWS_DEFAULT_REGION must be set
make ecr-push
```

**ECR URI pattern:**

```
{application-account-id}.dkr.ecr.{region}.amazonaws.com/km-{sidecar-name}:{version}
```

**Repository names:**

| Sidecar | ECR Repository |
|---------|----------------|
| dns-proxy | `km-dns-proxy` |
| http-proxy | `km-http-proxy` |
| audit-log | `km-audit-log` |
| tracing | `km-tracing` |

**Image versioning:**

`KM_SIDECAR_VERSION` controls the Docker tag. When unset it defaults to `latest`. The deploy pipeline always sets an explicit tag (e.g., a Git SHA or semantic version). Local development can omit the variable and use `latest`.

```bash
# Deploy pipeline: set explicit version
KM_SIDECAR_VERSION=v1.2.0 make ecr-push

# Local dev: omit for 'latest'
make ecr-push
```

**Tracing build context:**

The tracing `ecr-push` target uses `sidecars/tracing/` as its Docker build context, not the repo root. This is correct: the tracing Dockerfile only copies `config.yaml` from its own directory and has no dependency on Go source or shared packages.

```dockerfile
# sidecars/tracing/Dockerfile
FROM otel/opentelemetry-collector-contrib:latest
COPY config.yaml /etc/otelcol-contrib/config.yaml
```

Compare with Go sidecar Dockerfiles (e.g., `dns-proxy`), which use the repo root as build context to access `go.mod`, `go.sum`, and all shared packages.

---

### PLACEHOLDER_ECR Prefix

When the compiler generates ECS task definition HCL and `KM_ACCOUNTS_APPLICATION` is unset (for example, during local development or CI without AWS credentials), the ECR URI is prefixed with `PLACEHOLDER_ECR/`:

```hcl
# Example compiler output when KM_ACCOUNTS_APPLICATION is unset
container_image = "PLACEHOLDER_ECR/km-dns-proxy:latest"
```

This produces valid, parseable HCL that is visually distinguishable from a real ECR URI. It is not a special-cased string — the runtime would fail to pull the image, making the misconfiguration obvious at deploy time.

---

### S3 Binary Delivery for EC2

EC2 sandboxes receive sidecar binaries as pre-compiled static files downloaded from S3 during instance startup. There is no Docker daemon on EC2 sandbox instances.

**Delivery flow:**

1. `make sidecars` uploads compiled binaries to `s3://{artifacts-bucket}/sidecars/`.
2. EC2 instance user-data (bootstrap script) downloads binaries on first boot:
   ```bash
   aws s3 cp s3://${KM_ARTIFACTS_BUCKET}/sidecars/dns-proxy  /usr/local/bin/km-dns-proxy
   aws s3 cp s3://${KM_ARTIFACTS_BUCKET}/sidecars/http-proxy /usr/local/bin/km-http-proxy
   aws s3 cp s3://${KM_ARTIFACTS_BUCKET}/sidecars/audit-log  /usr/local/bin/km-audit-log
   aws s3 cp s3://${KM_ARTIFACTS_BUCKET}/sidecars/tracing/config.yaml /etc/km/tracing/config.yaml
   chmod +x /usr/local/bin/km-dns-proxy /usr/local/bin/km-http-proxy /usr/local/bin/km-audit-log
   ```
3. Systemd unit files start each sidecar as a managed service.

**Contrast with ECS:** On ECS Fargate, sidecars are Docker containers defined in the task definition. The ECS scheduler pulls images from ECR automatically — no S3 binary download or systemd unit files are needed.

---

## iptables DNAT on EC2

On EC2 instances, iptables rules transparently redirect traffic through the
DNS and HTTP proxies. This ensures enforcement even if the workload does not
honor proxy environment variables.

```bash
# Redirect all outbound DNS (UDP + TCP port 53) to the DNS proxy
iptables -t nat -A OUTPUT -p udp --dport 53 -j DNAT --to-destination 127.0.0.1:53
iptables -t nat -A OUTPUT -p tcp --dport 53 -j DNAT --to-destination 127.0.0.1:53

# Redirect outbound HTTP and HTTPS to the HTTP proxy
iptables -t nat -A OUTPUT -p tcp --dport 80  -j DNAT --to-destination 127.0.0.1:3128
iptables -t nat -A OUTPUT -p tcp --dport 443 -j DNAT --to-destination 127.0.0.1:3128
```

The DNAT rules are installed during instance bootstrap (user-data) after the
sidecar systemd services have started. Rules exclude traffic originating from
the proxy processes themselves (by UID match) to prevent redirect loops:

```bash
# Exclude proxy UID from DNAT to avoid loops
iptables -t nat -A OUTPUT -m owner --uid-owner km-proxy -j RETURN
```

---

## Container Dependency Ordering on ECS

In the ECS task definition, the `dependsOn` field ensures sidecars are
healthy before the main container starts:

```
dns-proxy    -->  (HEALTHY)  --+
http-proxy   -->  (HEALTHY)  --+--> main container starts
audit-log    -->  (START)    --+
tracing      -->  (HEALTHY)  --+
```

The task definition uses the `dependsOn` directive with `condition` set to
`HEALTHY` for sidecars that expose health checks (dns-proxy, http-proxy,
tracing) and `START` for the audit-log sidecar (which has no listen port).

All four sidecars have `essential: true` by default. If any sidecar exits,
the entire task is stopped -- this prevents unmonitored or unfiltered
workload execution.

---

## Debugging Blocked Requests

When a sandbox user reports that a request is unexpectedly blocked, use the
`km logs` command to stream sidecar logs in real time:

```bash
# Stream DNS proxy logs for a running sandbox
km logs --sandbox sb-a1b2c3d4 --stream dns-proxy

# Stream HTTP proxy logs
km logs --sandbox sb-a1b2c3d4 --stream http-proxy

# Stream all sidecar logs interleaved
km logs --sandbox sb-a1b2c3d4 --stream all
```

### Common issues

| Symptom | Likely cause | Fix |
|---|---|---|
| `NXDOMAIN` for a valid domain | Domain suffix missing from `ALLOWED_SUFFIXES` | Add the suffix to the sandbox profile's `network.dns_allowlist` |
| `403 Forbidden` from HTTP proxy | Host missing from `ALLOWED_HOSTS` | Add the host to the sandbox profile's `network.http_allowlist` |
| Timeouts (no NXDOMAIN or 403) | Upstream DNS unreachable or proxy not running | Check `km logs --stream dns-proxy` for `dns_upstream_error` events; verify sidecar health with `km status sb-a1b2c3d4` |
| Audit events missing | `AUDIT_LOG_DEST` misconfigured or CloudWatch permissions | Check `km logs --stream audit-log` for initialization errors |
| Traces not appearing in S3 | `OTEL_S3_BUCKET` not set or IAM missing `s3:PutObject` | Verify env vars and task role permissions |
