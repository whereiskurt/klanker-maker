# Phase 3: Sidecar Enforcement & Lifecycle Management - Research

**Researched:** 2026-03-21
**Domain:** Go sidecar processes (DNS proxy, HTTP proxy, audit log, OTel tracing), EC2/ECS traffic interception, AWS EventBridge Scheduler, CloudWatch/S3 log archival, MLflow S3 file store
**Confidence:** HIGH (core stack verified via pkg.go.dev and official AWS docs); MEDIUM (iptables DNAT specifics, MLflow Go integration)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**DNS + HTTP Proxy Behavior**
- HTTPS: SNI-only allowlist — inspect TLS ClientHello SNI field, no decryption, no CA cert needed
- Blocked requests: connection refused — proxy rejects CONNECT tunnel, workload sees connection error, logged in audit
- Crash recovery: systemd Restart=always on EC2, essential=true on ECS. Fail-closed by design — SG blocks all egress if proxy is down
- DNS proxy: forwarding mode — forward allowed queries to VPC resolver (169.254.169.253), deny = NXDOMAIN response. No caching.
- iptables DNAT on EC2: redirect port 53 (DNS) and 80/443 (HTTP) to local sidecar processes. Workload is unaware of proxies.
- ECS: sidecars are containers in the task definition. Traffic interception via environment variable proxy config (http_proxy/https_proxy) since iptables not available in Fargate.

**TTL + Lifecycle Management**
- TTL enforcement: EventBridge Scheduler — `km create` schedules a rule that fires at TTL expiry. More robust than sidecar self-terminate (survives instance issues).
- Idle timeout: detected via audit log sidecar activity — no commands or network events for idleTimeout period = idle
- `km list`: S3 state scan as default (fast). `--tags` flag for AWS tag-based scan. Diff between them detects orphans.
- `km list` output: human-readable table by default (sandbox ID, profile, substrate, region, status, TTL remaining). `--json` flag for programmatic use.
- `km status <sandbox-id>`: detailed sandbox state (resources, metadata, timestamps). No inline audit log — separate `km logs` command.
- `km logs <sandbox-id>`: separate command for tailing/querying audit logs from CloudWatch.
- Teardown policy from profile: destroy (default), stop, retain — honored by TTL and idle timeout handlers.

**Audit Log + Observability**
- Captures: shell command history + HTTP proxy request log (host, method, status) + DNS query log (domain, allowed/denied)
- Format: JSON lines — one JSON object per line with timestamp, sandbox_id, event_type, source, detail
- Destination: CloudWatch Logs real-time + periodic S3 archive for long-term storage
- CloudWatch log group: `/km/sandboxes/<sandbox-id>/`
- S3 archive: `s3://km-sandbox-artifacts-<suffix>/audit/<sandbox-id>/`

**OTel + MLflow Tracing**
- OTel collector: lightweight, exports OTLP JSON to local file, flushes to S3 at sandbox exit or on interval. No live dashboard backend.
- S3 path: `s3://km-sandbox-artifacts-<suffix>/traces/<sandbox-id>/`
- Trace context propagation: HTTP proxy sidecar injects/forwards W3C traceparent headers on outbound requests
- MLflow: S3-backed file store (no tracking server). Each sandbox writes run metadata as JSON to `s3://bucket/mlflow/<experiment>/`. Zero infrastructure.
- MLflow run params: sandbox_id, profile_name, substrate, region, TTL, start_time
- MLflow run metrics: duration, exit_status, commands_executed, bytes_egressed

### Claude's Discretion
- Exact iptables rules for EC2 DNAT configuration
- Sidecar binary packaging (single Go binary vs separate binaries)
- EventBridge Scheduler Lambda implementation details
- CloudWatch → S3 archive mechanism (subscription filter vs periodic export)
- OTel collector configuration format
- Audit log buffering strategy (real-time vs batch)

### Deferred Ideas (OUT OF SCOPE)
None — discussion stayed within phase scope
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| PROV-03 | `km list` — all running sandboxes with status | S3 ListObjectsV2 with `tf-km/sandboxes/` prefix; AWS tagging API for `--tags` mode; table/JSON output in Cobra command |
| PROV-04 | `km status <sandbox-id>` — detailed sandbox state | S3 metadata read + tag API resource list; Cobra command pattern from existing cmd/ |
| PROV-05 | Sandbox auto-destroys after TTL expires | EventBridge Scheduler `at()` expression created in `km create`; Lambda target calls `km destroy` logic; `ActionAfterCompletion: DELETE` |
| PROV-06 | Sandbox auto-destroys after idle timeout with no activity | Audit log sidecar detects absence of events for idleTimeout period; calls same destroy Lambda or EventBridge rule |
| PROV-07 | Teardown policy configurable (destroy/stop/retain) | `LifecycleSpec.TeardownPolicy` already in profile schema; Lambda and idle-timeout handler branch on this field |
| NETW-02 | DNS proxy sidecar filters outbound DNS by allowlisted suffixes | miekg/dns v1.1.72 `ServeMux.HandleFunc(".", ...)`, forward to 169.254.169.253, NXDOMAIN on deny |
| NETW-03 | HTTP proxy sidecar filters outbound HTTP/S by allowlisted hosts and methods | elazarl/goproxy v1.8.2 `OnRequest().HandleConnectFunc(...)` for SNI-only CONNECT intercept; `RejectConnect` for blocked hosts |
| OBSV-01 | Audit log sidecar captures command execution logs | zerolog JSON-lines to stdout (ECS collects via awslogs) or CloudWatch Logs agent (EC2); shell audit hook in user-data |
| OBSV-02 | Audit log sidecar captures network traffic logs | DNS proxy and HTTP proxy sidecars emit JSON events to shared CloudWatch log stream |
| OBSV-03 | Log destination configurable (CloudWatch/S3/stdout) | `AUDIT_LOG_DEST` env var; CloudWatch → S3 via Kinesis Firehose subscription filter; ECS uses awslogs driver |
| OBSV-08 | Tracing sidecar collects OTel traces and spans | OTel Collector Contrib `awss3exporter` with `marshaler: otlp_json`; OTLP receiver on localhost:4317 |
| OBSV-09 | Each sandbox session logged as MLflow run | Write MLflow run JSON directly to S3 using IAM role; no Python/MLflow server needed; pure AWS SDK S3 Put |
| OBSV-10 | OTel trace context propagated through proxy sidecars | HTTP proxy sidecar calls `otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))` on outbound CONNECT |
</phase_requirements>

---

## Summary

Phase 3 builds four Go sidecar processes (DNS proxy, HTTP proxy, audit-log, tracing) that run on both EC2 (as systemd services) and ECS (as additional containers in the task definition). The sidecars enforce network policy, produce auditable JSON-line logs, collect OTel traces, and log MLflow run metadata to S3. Lifecycle management (TTL, idle timeout, teardown policy) is implemented via EventBridge Scheduler with a Lambda function that calls the existing `km destroy` logic.

The split between EC2 and ECS substrates is the most significant design challenge. On EC2, iptables DNAT in the OUTPUT chain redirects all DNS and HTTP traffic to local sidecar processes transparently. On ECS/Fargate, iptables is unavailable (NET_ADMIN not granted); instead the main container must be launched with `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` environment variables pointing to the sidecar containers at `localhost:<port>`, and the DNS proxy must be explicitly set as the resolver. This means the ECS main-container image is not fully "unaware" of the proxy — the task definition must inject proxy env vars into the main container.

Sidecar binaries are simple, single-purpose Go programs in `sidecars/` subdirectories. They share the project's zerolog dependency for JSON output and are compiled at build time. On EC2 they are downloaded from S3 or embedded in user-data; on ECS they are built into container images pushed to ECR.

**Primary recommendation:** Build four focused Go sidecar binaries under `sidecars/{dns-proxy,http-proxy,audit-log,tracing}/main.go`. Extend `pkg/compiler/userdata.go` with a sidecar installation section for EC2, and populate the placeholder container image references in `pkg/compiler/service_hcl.go` for ECS. Add EventBridge Scheduler calls to `km create`/`km destroy`. Add `km list`, `km status`, and `km logs` as standard Cobra commands following the established `NewXxxCmd(cfg)` pattern.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/miekg/dns` | v1.1.72 (Jan 2026) | DNS proxy server + client | Only full-featured Go DNS library; ServeMux pattern mirrors net/http; NXDOMAIN via `dns.RcodeNameError` |
| `github.com/elazarl/goproxy` | v1.8.2 | HTTP/HTTPS CONNECT proxy with SNI inspection | Handles CONNECT tunnel interception at HTTP layer before TLS; `HandleConnectFunc` for allowlist decisions; no MITM/decryption required |
| `github.com/rs/zerolog` | v1.33.0 (already in go.mod) | JSON-line audit log output | Zero-allocation; already used in CLI; structured fields map directly to audit log schema |
| `github.com/aws/aws-sdk-go-v2/service/scheduler` | v1.x (latest in v2 suite) | EventBridge Scheduler: create/delete TTL rules | Native Go v2 SDK; `CreateSchedule` with `at()` expression; `ActionAfterCompletion: DELETE` for self-cleanup |
| `github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs` | v1.x | Create log groups, put log events | Already pulled in transitively; used for audit log real-time path |
| `github.com/aws/aws-sdk-go-v2/service/s3` | v1.x | State scan (ListObjectsV2), artifact upload, MLflow JSON put | Already used for Terraform state; S3 paginator pattern available |
| `github.com/aws/aws-sdk-go-v2/service/lambda` | v1.x | Deploy and invoke TTL handler Lambda | Create/Update function with Go bootstrap binary zip; invoke on destroy |
| OTel Collector Contrib binary | latest | Collect OTLP traces, export to S3 as JSON | `awss3exporter` (alpha) with `marshaler: otlp_json`; runs as sidecar process/container; config YAML embedded |
| `go.opentelemetry.io/otel` | v1.x | W3C traceparent header injection in HTTP proxy | `otel.GetTextMapPropagator().Inject()` into outbound CONNECT headers |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/aws/aws-sdk-go-v2/service/firehose` | v1.x | CW → S3 subscription filter delivery stream | Used when `AUDIT_LOG_DEST=s3` to stream real-time CW logs to S3 |
| `github.com/aws/aws-sdk-go-v2/service/iam` | v1.x | Create EventBridge Scheduler execution role | Needed once at `km init` time, not per sandbox |
| `text/tabwriter` (stdlib) | Go stdlib | Human-readable `km list` table output | Standard Go tabwriter; already used pattern in ecosystem |
| `encoding/json` (stdlib) | Go stdlib | MLflow run JSON serialization; `km list --json` | No external dependency needed for simple JSON output |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `elazarl/goproxy` | `net/http` manual CONNECT handling | goproxy provides structured intercept API; manual is ~300 lines of boilerplate |
| `miekg/dns` | CoreDNS (as subprocess) | miekg is a library, no subprocess management; CoreDNS is overkill for a simple allowlist |
| OTel Collector binary sidecar | Go OTel SDK exporting directly to S3 | Collector is the standard approach; SDK direct S3 export requires custom exporter code |
| EventBridge Scheduler + Lambda | Cron in audit sidecar + SDK call | EventBridge survives instance failure; sidecar-based TTL is unreliable if instance dies |
| Kinesis Firehose for CW→S3 | `CreateExportTask` (CW direct) | Firehose is real-time continuous; `CreateExportTask` is async, one-at-a-time per account |

**Installation (new dependencies to add to go.mod):**
```bash
go get github.com/miekg/dns@v1.1.72
go get github.com/elazarl/goproxy@v1.8.2
go get github.com/aws/aws-sdk-go-v2/service/scheduler
go get github.com/aws/aws-sdk-go-v2/service/lambda
go get github.com/aws/aws-sdk-go-v2/service/firehose
go get go.opentelemetry.io/otel
go get go.opentelemetry.io/otel/propagation
```

---

## Architecture Patterns

### Recommended Project Structure
```
sidecars/
├── dns-proxy/
│   └── main.go              # miekg/dns ServeMux; listens :53 UDP+TCP; forward/NXDOMAIN
├── http-proxy/
│   └── main.go              # elazarl/goproxy; listens :3128; SNI allowlist on CONNECT; OTel inject
├── audit-log/
│   └── main.go              # zerolog; reads stdin (piped from shell AUDIT_PIPE or log tail)
└── tracing/
    └── config.yaml          # OTel Collector Contrib config; OTLP receiver; awss3exporter

pkg/
├── compiler/
│   ├── userdata.go          # EXTEND: add sidecar install + iptables section for EC2
│   ├── service_hcl.go       # EXTEND: replace IMAGE_PLACEHOLDER with real ECR URIs
│   └── lifecycle.go         # NEW: BuildEventBridgeSchedule(), BuildTTLLambdaPayload()
├── aws/
│   ├── scheduler.go         # NEW: CreateTTLSchedule(), DeleteTTLSchedule() wrapping EventBridge SDK
│   ├── discover.go          # EXTEND: ListAllSandboxes() via S3 prefix scan
│   └── cloudwatch.go        # NEW: GetLogEvents(), TailLogs() for `km logs`
└── lifecycle/               # NEW package
    ├── idle.go              # IdleDetector: poll CW log group, fire destroy after idleTimeout
    └── teardown.go          # TeardownPolicy executor: destroy/stop/retain dispatch

internal/app/cmd/
├── list.go                  # NEW: km list (S3 scan default, --tags flag, --json flag)
├── status.go                # NEW: km status <sandbox-id>
└── logs.go                  # NEW: km logs <sandbox-id> [--follow]

infra/
├── modules/
│   └── scheduler-role/      # NEW Terraform module: IAM role for EventBridge → Lambda
└── live/use1/
    └── scheduler-role/      # NEW Terragrunt config: provision scheduler IAM role once per region
```

### Pattern 1: DNS Proxy with miekg/dns
**What:** ServeMux on port 53 UDP+TCP. HandleFunc for "." (all queries). Check question Name against allowlist suffixes. Forward allowed → VPC resolver. Deny → NXDOMAIN.
**When to use:** EC2 substrate (systemd service). ECS substrate (DNS container in task).

```go
// Source: pkg.go.dev/github.com/miekg/dns v1.1.72
func handleDNS(allowed []string, upstream string) dns.HandlerFunc {
    return func(w dns.ResponseWriter, r *dns.Msg) {
        if !isAllowed(r.Question[0].Name, allowed) {
            m := new(dns.Msg)
            m.SetRcode(r, dns.RcodeNameError) // NXDOMAIN
            w.WriteMsg(m)
            return
        }
        client := &dns.Client{}
        resp, _, err := client.Exchange(r, upstream+":53")
        if err != nil {
            m := new(dns.Msg)
            m.SetRcode(r, dns.RcodeServerFailure)
            w.WriteMsg(m)
            return
        }
        w.WriteMsg(resp)
    }
}

mux := dns.NewServeMux()
mux.HandleFunc(".", handleDNS(allowlist, "169.254.169.253"))
server := &dns.Server{Addr: ":53", Net: "udp", Handler: mux}
server.ListenAndServe()
```

### Pattern 2: HTTP/HTTPS Proxy with elazarl/goproxy (SNI-only, no MITM)
**What:** goproxy on port 3128. For CONNECT requests, inspect `host` parameter (this is the SNI hostname). If not in allowlist, `RejectConnect`. Never use `MitmConnect` — no CA cert generation.
**When to use:** Both substrates. On EC2 traffic arrives via iptables DNAT. On ECS the main container must have `HTTP_PROXY=http://localhost:3128` in its environment.

```go
// Source: pkg.go.dev/github.com/elazarl/goproxy v1.8.2
proxy := goproxy.NewProxyHttpServer()

proxy.OnRequest().HandleConnectFunc(
    func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
        hostname := strings.Split(host, ":")[0]
        if !isAllowed(hostname, allowedHosts) {
            // Log blocked attempt via zerolog before rejecting
            log.Warn().Str("host", host).Msg("blocked CONNECT")
            return goproxy.RejectConnect, host
        }
        // Inject OTel traceparent header before allowing CONNECT
        // (header injection happens in outbound request handler)
        return goproxy.OkConnect, host
    })

http.ListenAndServe(":3128", proxy)
```

### Pattern 3: EventBridge Scheduler — TTL auto-destroy
**What:** After `km create` succeeds and Terragrunt apply is done, call `CreateTTLSchedule`. Uses `at(YYYY-MM-DDTHH:MM:SS)` expression. Target is a Lambda function that calls `km destroy` internals. `ActionAfterCompletion: DELETE` so the schedule self-removes.
**When to use:** Every `km create` call when `lifecycle.ttl != ""`.

```go
// Source: pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/scheduler
client := scheduler.NewFromConfig(awsCfg)

expiresAt := time.Now().Add(parsedTTL)
expr := fmt.Sprintf("at(%s)", expiresAt.UTC().Format("2006-01-02T15:04:05"))

_, err = client.CreateSchedule(ctx, &scheduler.CreateScheduleInput{
    Name:               aws.String("km-ttl-" + sandboxID),
    ScheduleExpression: aws.String(expr),
    FlexibleTimeWindow: &schedulertypes.FlexibleTimeWindow{
        Mode: schedulertypes.FlexibleTimeWindowModeOff,
    },
    ActionAfterCompletion: schedulertypes.ActionAfterCompletionDelete,
    Target: &schedulertypes.Target{
        Arn:     aws.String(lambdaARN),
        RoleArn: aws.String(schedulerRoleARN),
        Input:   aws.String(`{"sandbox_id":"` + sandboxID + `","teardown_policy":"` + teardownPolicy + `"}`),
    },
})
```

### Pattern 4: S3 State Scan for `km list`
**What:** ListObjectsV2 with `Prefix: "tf-km/sandboxes/"` and `Delimiter: "/"`. Each CommonPrefix is one sandbox directory key. Parse sandbox ID from key. Optionally read metadata JSON file in each prefix for status/profile.
**When to use:** Default path for `km list`.

```go
// Source: pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/s3
paginator := s3.NewListObjectsV2Paginator(s3Client, &s3.ListObjectsV2Input{
    Bucket:    aws.String(stateBucket),
    Prefix:    aws.String("tf-km/sandboxes/"),
    Delimiter: aws.String("/"),
})
for paginator.HasMorePages() {
    page, err := paginator.NextPage(ctx)
    // each page.CommonPrefixes entry is one sandbox
}
```

### Pattern 5: iptables DNAT on EC2 (for user-data.sh extension)
**What:** In the OUTPUT chain (covers locally-generated traffic), redirect DNS and HTTP/HTTPS to sidecar processes. Exempt the sidecar user from redirection to avoid redirect loops.
**When to use:** EC2 substrate only. Added in user-data.sh during instance bootstrap.

```bash
# Create dedicated user for sidecar processes (exempt from DNAT)
useradd -r -s /usr/sbin/nologin km-sidecar

# DNS: redirect UDP/TCP port 53 to local DNS proxy on :5353
# Use OUTPUT chain for locally-initiated traffic (workload processes)
iptables -t nat -A OUTPUT -p udp --dport 53 ! -m owner --uid-owner km-sidecar -j REDIRECT --to-ports 5353
iptables -t nat -A OUTPUT -p tcp --dport 53 ! -m owner --uid-owner km-sidecar -j REDIRECT --to-ports 5353

# HTTP: redirect TCP port 80 to HTTP proxy on :3128
iptables -t nat -A OUTPUT -p tcp --dport 80  ! -m owner --uid-owner km-sidecar -j REDIRECT --to-ports 3128

# HTTPS: redirect TCP port 443 to HTTP proxy on :3128
iptables -t nat -A OUTPUT -p tcp --dport 443 ! -m owner --uid-owner km-sidecar -j REDIRECT --to-ports 3128

# Exempt IMDS (169.254.169.254) — must not be redirected
iptables -t nat -I OUTPUT -d 169.254.169.254 -j RETURN
```

**CRITICAL:** The IMDS exemption rule (`-I OUTPUT` = insert at top) must come BEFORE the HTTP/HTTPS redirect rules. IMDSv2 uses port 80 for token requests. Without this exemption, IMDSv2 token requests would be redirected to the HTTP proxy and fail.

### Pattern 6: ECS proxy injection via environment variables
**What:** ECS/Fargate does not support iptables (NET_ADMIN not available). The main container must use explicit proxy env vars. The DNS sidecar container's IP is `localhost` (all containers share the task network namespace in Fargate). Set `NAMESERVER` to localhost:5353 or override `/etc/resolv.conf` at container startup.
**When to use:** ECS substrate only. Set in the ECS task definition's main container environment.

```hcl
# In ecsServiceHCLTemplate — main container environment block
environment = [
  { name = "SANDBOX_ID",    value = "{{ .SandboxID }}" },
  { name = "HTTP_PROXY",    value = "http://localhost:3128" },
  { name = "HTTPS_PROXY",   value = "http://localhost:3128" },
  { name = "NO_PROXY",      value = "169.254.169.254,169.254.170.2,localhost,127.0.0.1" },
]
```

**DNS on ECS:** The DNS sidecar listens on `localhost:5353`. The task's `/etc/resolv.conf` must be rewritten to point to `127.0.0.1` port 5353, or the DNS sidecar listens on port 53 directly. On Fargate, an init container or entrypoint script in the main container can rewrite `/etc/resolv.conf`. Alternatively, the DNS proxy can listen on `0.0.0.0:53` if the container runs as root or with `cap_net_bind_service`.

### Pattern 7: OTel Collector config (sidecar tracing)
**What:** Minimal OTel Collector Contrib config. OTLP receiver for traces from workload apps. AWS S3 exporter with `otlp_json` marshaler.
**When to use:** Both substrates. On EC2 the collector binary runs as a systemd service. On ECS it runs in the `tracing` container.

```yaml
# Source: github.com/open-telemetry/opentelemetry-collector-contrib awss3exporter README
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: "localhost:4317"
      http:
        endpoint: "localhost:4318"

processors:
  batch:
    timeout: 10s

exporters:
  awss3:
    s3uploader:
      region: "${AWS_REGION}"
      s3_bucket: "${KM_ARTIFACTS_BUCKET}"
      s3_prefix: "traces/${SANDBOX_ID}"
      marshaler: "otlp_json"

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [awss3]
```

### Pattern 8: MLflow run JSON — pure S3, no server
**What:** MLflow S3 file store format. Write a `meta.yaml` (run metadata) and `params/<key>` files directly to S3 using the AWS SDK. No Python, no MLflow tracking server. The S3 key structure must match MLflow's file store convention so MLflow CLI can read it later.
**When to use:** At sandbox start (run creation) and sandbox end (update final metrics).

MLflow file store S3 key structure:
```
mlflow/<experiment-id>/
  <run-id>/
    meta.yaml          # run metadata (status, start_time, end_time, artifact_uri)
    params/
      sandbox_id       # file content = sandbox ID value
      profile_name     # file content = profile name value
      substrate        # file content = "ec2" or "ecs"
    metrics/
      duration         # file content = "1234.5"
      exit_status      # file content = "0"
```

Write these files using `s3Client.PutObject()`. Parse TTL duration from profile, store as param. Update `meta.yaml` with `end_time` and `status: FINISHED` on sandbox exit.

### Anti-Patterns to Avoid
- **MITM proxy for HTTPS:** Using `MitmConnect` in goproxy requires CA certificate generation and distribution to the workload's trust store. The locked decision is SNI-only (`OkConnect` after allowlist check). Never set `MitmConnect`.
- **DNS caching in proxy:** Locked decision says no caching. Adding a cache creates TTL inconsistencies with the VPC resolver and masks resolution failures.
- **Redirect IMDS traffic:** Always exempt `169.254.169.254` from iptables rules. Redirecting IMDSv2 PUT token requests breaks SSM agent and credential refresh.
- **Running sidecars as root on EC2:** Sidecar processes must run as `km-sidecar` user. Root-owned processes would be exempt from iptables owner matching and could bypass the proxy.
- **ECS: relying on iptables REDIRECT for Fargate:** Fargate does not grant NET_ADMIN. iptables changes in an init container will fail silently or error. Use env-var proxy config instead.
- **Lambda polling for idle detection:** Idle detection belongs in the audit-log sidecar that already sees all events. A separate Lambda polling CW Logs on a schedule is expensive and slow.
- **One EventBridge rule per sandbox for idle timeout:** EventBridge Scheduler minimum granularity is 1 minute; polling-based idle detection via the audit-log sidecar is more accurate and cheaper.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| DNS packet parsing and response construction | Custom UDP listener + DNS wire format | `miekg/dns` v1.1.72 | DNS has 30+ edge cases (EDNS0, TSIG, compression, truncation); miekg handles all of them |
| HTTP CONNECT tunnel interception | Raw TCP proxy with net.Dial | `elazarl/goproxy` v1.8.2 | CONNECT handling, hop-by-hop header stripping, and HTTP/1.1 upgrade negotiation are non-trivial |
| OTLP trace format serialization | Custom JSON marshaling of trace spans | OTel Collector Contrib binary | OTLP format has required fields that are easy to omit; official collector handles versioning |
| EventBridge one-time schedule with self-delete | Step Functions, cron expressions, manual cleanup | `scheduler.CreateSchedule` with `ActionAfterCompletion: DELETE` | Self-deleting at-expression schedules are a first-class feature |
| CloudWatch → S3 real-time streaming | Periodic Lambda export tasks | Kinesis Firehose subscription filter | `CreateExportTask` is async and limited to one task per account at a time; Firehose is continuous |
| W3C traceparent header format | `fmt.Sprintf("00-%s-%s-01", ...)` | `otel.GetTextMapPropagator().Inject()` | TraceContext format has version, flags, and sampling fields; use the spec implementation |
| S3 paginated list | Manual ContinuationToken loop | `s3.NewListObjectsV2Paginator` | SDK paginator handles truncation and token passing |

**Key insight:** Sidecar processes are small (200-400 lines each) because they delegate complex protocol work to purpose-built libraries.

---

## Common Pitfalls

### Pitfall 1: IMDSv2 intercepted by HTTP proxy redirect
**What goes wrong:** iptables DNAT rule for port 80 intercepts the IMDSv2 PUT token request to `169.254.169.254:80`, routing it to the HTTP proxy. The proxy rejects it (not an allowed host), breaking SSM agent, credential refresh, and instance metadata access.
**Why it happens:** IMDSv2 uses HTTP PUT on port 80 to `169.254.169.254`. A blanket port 80 DNAT rule catches this.
**How to avoid:** Insert IMDS exemption rule at the TOP of the OUTPUT chain before any redirect rules: `iptables -t nat -I OUTPUT -d 169.254.169.254 -j RETURN`. Also exempt the task metadata endpoint `169.254.170.2` (ECS agent).
**Warning signs:** SSM agent fails to connect; AWS credential errors in sandbox; instance appears provisioned but `SANDBOX_READY` signal never logged.

### Pitfall 2: Redirect loop — sidecar process redirected to itself
**What goes wrong:** The DNS proxy sidecar listens on :5353. It needs to forward queries to `169.254.169.253` on port 53. If the iptables rule catches traffic from all users including the sidecar, its own forwarding query gets redirected back to port 5353, creating an infinite loop.
**Why it happens:** iptables OUTPUT chain applies to all locally-generated packets unless a user owner exception is added.
**How to avoid:** Add `! -m owner --uid-owner km-sidecar` to every DNAT rule. Run all sidecar processes as the `km-sidecar` system user.
**Warning signs:** DNS proxy CPU spikes to 100%; logs show rapid repeated queries to the same domain; no actual DNS resolution succeeds.

### Pitfall 3: ECS Fargate DNS proxy not intercepting queries
**What goes wrong:** The DNS sidecar container is running but the main container still resolves via AWS VPC DNS directly. Blocked domains resolve successfully.
**Why it happens:** On Fargate, `/etc/resolv.conf` is written by AWS to point to `169.254.169.253`. The DNS sidecar's port 53 is not automatically used. Without explicitly overriding the resolver, the main container bypasses the sidecar.
**How to avoid:** The DNS sidecar container must listen on `0.0.0.0:53`. In the main container's entrypoint, rewrite `/etc/resolv.conf` to `nameserver 127.0.0.1`. Or use ECS `dnsServers` task definition field. Since all Fargate containers share the task network namespace (loopback), `127.0.0.1` on port 53 reaches the DNS sidecar.
**Warning signs:** DNS allowlist appears to have no effect in ECS sandboxes; blocked domains accessible from main container.

### Pitfall 4: EventBridge Scheduler role missing permissions
**What goes wrong:** `km create` completes successfully but the TTL schedule never fires. The sandbox runs indefinitely past TTL.
**Why it happens:** EventBridge Scheduler requires an IAM execution role with `lambda:InvokeFunction` permission on the TTL handler Lambda. This role must exist before any schedule can be created.
**How to avoid:** Provision the scheduler execution role as part of `km init` (or a new `infra/live/use1/scheduler-role/` Terragrunt config). Store the role ARN in Terraform output, read it in `km create` alongside VPC/subnet outputs.
**Warning signs:** EventBridge console shows schedule in `FAILED` state; CloudTrail shows `AccessDenied` on `lambda:InvokeFunction` from the Scheduler service principal.

### Pitfall 5: `essential: true` on audit-log sidecar causes task failure on log write errors
**What goes wrong:** ECS audit-log container is marked `essential: true`. If the CloudWatch Logs agent or log destination has a transient error, the audit-log container exits, causing the entire ECS task to stop (including the main workload container).
**Why it happens:** In ECS, any essential container exiting stops all containers in the task.
**How to avoid:** Based on the locked decision, audit-log has `essential: false` in the task definition (see existing `service_hcl.go` template — this is already set correctly). Verify this is not accidentally changed. DNS-proxy and HTTP-proxy are `essential: true` (fail-closed) but audit-log is not.
**Warning signs:** ECS tasks stop unexpectedly during log write bursts; CloudWatch shows audit-log container exit code 1 followed by main container stop.

### Pitfall 6: MLflow run JSON format incompatibility
**What goes wrong:** MLflow CLI or tracking server cannot read the run data from S3 because the JSON structure doesn't match MLflow's file store format.
**Why it happens:** MLflow file store uses a specific directory structure and YAML format for `meta.yaml`, not arbitrary JSON. The `artifact_uri` field must point back to the S3 prefix.
**How to avoid:** Write `meta.yaml` in MLflow's expected YAML format (not JSON). Each param is a separate file under `params/<key>` with the value as the file content. Each metric is a TSV file under `metrics/<key>`. If only S3 inspection (not MLflow CLI compatibility) is needed, a simpler custom JSON format is fine — but document the choice.
**Warning signs:** `mlflow runs list --experiment-id X` returns empty or fails when pointed at S3 backend.

### Pitfall 7: OTel awss3exporter stability (alpha)
**What goes wrong:** `awss3exporter` API or configuration keys change between OTel Collector Contrib releases, breaking the tracing sidecar config.
**Why it happens:** The exporter is marked "alpha" stability. Breaking changes are possible.
**How to avoid:** Pin the OTel Collector Contrib binary version in the sidecar Dockerfile and user-data script. Test the config against the pinned version. Do not auto-upgrade.
**Warning signs:** Collector exits with config validation errors after a version bump; S3 bucket empty despite traces being emitted.

---

## Code Examples

Verified patterns from official sources:

### DNS NXDOMAIN response
```go
// Source: pkg.go.dev/github.com/miekg/dns v1.1.72
m := new(dns.Msg)
m.SetRcode(r, dns.RcodeNameError) // 3 = NXDOMAIN
w.WriteMsg(m)
```

### goproxy SNI allowlist CONNECT handler
```go
// Source: pkg.go.dev/github.com/elazarl/goproxy v1.8.2
proxy.OnRequest().HandleConnectFunc(
    func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
        h := strings.Split(host, ":")[0] // strip port
        for _, allowed := range allowedHosts {
            if h == allowed || strings.HasSuffix(h, "."+allowed) {
                return goproxy.OkConnect, host
            }
        }
        return goproxy.RejectConnect, host
    },
)
```

### EventBridge Scheduler one-time at() expression
```go
// Source: pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/scheduler
expiresAt := time.Now().UTC().Add(parsedTTL)
expr := "at(" + expiresAt.Format("2006-01-02T15:04:05") + ")"

_, err = schedulerClient.CreateSchedule(ctx, &scheduler.CreateScheduleInput{
    Name:                  aws.String("km-ttl-" + sandboxID),
    ScheduleExpression:    aws.String(expr),
    FlexibleTimeWindow:    &schedulertypes.FlexibleTimeWindow{Mode: schedulertypes.FlexibleTimeWindowModeOff},
    ActionAfterCompletion: schedulertypes.ActionAfterCompletionDelete,
    Target: &schedulertypes.Target{
        Arn:     aws.String(lambdaARN),
        RoleArn: aws.String(schedulerRoleARN),
        Input:   aws.String(`{"sandbox_id":"` + sandboxID + `"}`),
    },
})
```

### S3 paginated sandbox list
```go
// Source: pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/s3
p := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
    Bucket:    aws.String(bucket),
    Prefix:    aws.String("tf-km/sandboxes/"),
    Delimiter: aws.String("/"),
})
var ids []string
for p.HasMorePages() {
    page, _ := p.NextPage(ctx)
    for _, cp := range page.CommonPrefixes {
        // key = "tf-km/sandboxes/<sandbox-id>/"
        parts := strings.Split(strings.TrimSuffix(*cp.Prefix, "/"), "/")
        ids = append(ids, parts[len(parts)-1])
    }
}
```

### OTel traceparent injection in HTTP proxy outbound handler
```go
// Source: pkg.go.dev/go.opentelemetry.io/otel v1.x + otel/propagation
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/propagation"
)

// In goproxy OnRequest handler (for plain HTTP requests):
proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
    otel.GetTextMapPropagator().Inject(req.Context(), propagation.HeaderCarrier(req.Header))
    return req, nil
})
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| AWS App Mesh + Envoy for Fargate proxy injection | ECS Service Connect (for service-to-service) or explicit env-var proxy config | Sep 2024: App Mesh no longer accepts new customers | Do not use App Mesh; use explicit proxy env vars in task definition |
| EventBridge Rules (legacy) for one-time schedules | EventBridge Scheduler with `at()` expression | Nov 2022: Scheduler launched | Scheduler is the right tool for one-time TTL; Rules are for pattern-based events |
| AWS SDK Go v1 | AWS SDK Go v2 (`aws-sdk-go-v2`) | v2 GA 2021; v1 EOL Jul 2025 | Project already uses v2; never introduce v1 |
| miekg/dns v1 → v2 migration pending | v1.1.72 is current stable; v2 at Codeberg | Dec 2025: v2 announced | Use v1.1.72 for this phase; v2 migration is a future concern |
| CloudWatch `CreateExportTask` for S3 archive | Kinesis Firehose subscription filter for continuous streaming | Ongoing — ExportTask limited to 1/account | Use Firehose for any real-time S3 archival requirement |

**Deprecated/outdated:**
- AWS App Mesh: Deprecated Sep 2024. Do not use for proxy injection.
- AWS SDK Go v1: EOL July 31, 2025. Not used in this project — maintain v2 only.
- `miekg/dns` v2: Not yet released on pkg.go.dev; use v1.1.72 from `github.com/miekg/dns`.

---

## Open Questions

1. **Sidecar binary delivery to EC2 instances**
   - What we know: User-data installs Go binaries. Binaries must come from somewhere.
   - What's unclear: Whether to cross-compile sidecar binaries into the `km` release artifact and embed them (base64 in user-data) vs. upload to S3 and download at boot vs. build a separate sidecar Docker image for EC2 (unlikely).
   - Recommendation (Claude's discretion): Compile sidecar binaries as part of `go build`, upload to a versioned S3 prefix (`s3://km-artifacts/sidecars/v<version>/`), download in user-data. This avoids base64-encoding multi-MB binaries in user-data and makes version upgrades possible without AMI changes.

2. **ECR image build and push for ECS sidecar containers**
   - What we know: The ECS template has `DNS_PROXY_IMAGE_PLACEHOLDER` etc. These must become real ECR image URIs.
   - What's unclear: Whether image build is part of `km init`, a CI step, or pre-built and shipped as part of the project.
   - Recommendation (Claude's discretion): Add a `km build-sidecars` command (or Makefile target) that builds each sidecar Dockerfile, tags, and pushes to ECR. Store ECR URIs in the existing `network` Terragrunt outputs or a new `sidecars.json` config file. `km create` reads these URIs at compile time.

3. **Lambda function for TTL handler — packaging and provisioning**
   - What we know: EventBridge Scheduler needs a Lambda target ARN. Lambda Go functions use `bootstrap` binary name, zip deployment.
   - What's unclear: Whether the Lambda is provisioned once per region (Terraform module) or per-sandbox (dynamic Lambda creation in `km create`). Per-region is strongly preferred.
   - Recommendation (Claude's discretion): One Lambda function per region named `km-sandbox-ttl-handler`. Provisioned via a new `infra/live/use1/ttl-handler/` Terragrunt config during `km init`. The Lambda receives `{"sandbox_id": "...", "teardown_policy": "..."}` and calls the same destroy logic as `pkg/compiler` + `pkg/terragrunt`. ARN stored in Terragrunt outputs alongside VPC outputs.

4. **Idle timeout detection mechanism**
   - What we know: Idle = no command or network events for `idleTimeout` duration. Audit log sidecar sees all events.
   - What's unclear: How the audit-log sidecar triggers destroy. It cannot call Terraform directly. Options: (a) emit a CloudWatch metric alarm that triggers Lambda, (b) audit-log sidecar calls the TTL Lambda directly via AWS SDK, (c) audit-log sidecar uses EventBridge Scheduler to reschedule TTL forward, advancing it to "now" when idle detected.
   - Recommendation (Claude's discretion): Option (b) — audit-log sidecar holds an AWS SDK client and directly invokes the `km-sandbox-ttl-handler` Lambda with `{"sandbox_id": "...", "reason": "idle"}` when idle timeout elapses. Simplest, no additional AWS resources needed.

5. **DNS sidecar on ECS — port 53 binding**
   - What we know: Non-root processes cannot bind to ports < 1024 by default. Fargate containers can be root or have `cap_net_bind_service`.
   - What's unclear: Whether the project's ECS containers run as root or need capability grants.
   - Recommendation (Claude's discretion): Run the DNS sidecar container as root (or grant `NET_BIND_SERVICE` capability in the container definition) so it can bind port 53. Alternatively, bind on port 5353 and configure the main container's entrypoint to rewrite `/etc/resolv.conf` to `nameserver 127.0.0.1` + `options port:5353`. Port 5353 approach avoids root requirement.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing stdlib (no external test framework) |
| Config file | none — `go test ./...` from repo root |
| Quick run command | `go test ./sidecars/... ./pkg/compiler/... ./pkg/aws/... ./internal/app/cmd/... -run TestXxx -timeout 30s` |
| Full suite command | `go test ./... -timeout 120s` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| PROV-03 | `km list` Cobra command registered and S3 scan returns sandbox IDs | unit | `go test ./internal/app/cmd/... -run TestListCmd` | ❌ Wave 0 |
| PROV-04 | `km status` Cobra command registered and reads sandbox metadata | unit | `go test ./internal/app/cmd/... -run TestStatusCmd` | ❌ Wave 0 |
| PROV-05 | `CreateTTLSchedule` produces correct `at()` expression from TTL duration | unit | `go test ./pkg/aws/... -run TestCreateTTLSchedule` | ❌ Wave 0 |
| PROV-06 | `IdleDetector` fires destroy after idleTimeout with no events | unit (mock) | `go test ./pkg/lifecycle/... -run TestIdleDetector` | ❌ Wave 0 |
| PROV-07 | Teardown policy `stop`/`retain` branches taken correctly | unit | `go test ./pkg/lifecycle/... -run TestTeardownPolicy` | ❌ Wave 0 |
| NETW-02 | DNS proxy forwards allowed domain, returns NXDOMAIN for blocked | unit (in-process DNS server) | `go test ./sidecars/dns-proxy/... -run TestDNSProxy` | ❌ Wave 0 |
| NETW-03 | HTTP proxy rejects CONNECT to blocked host, allows listed host | unit (in-process proxy) | `go test ./sidecars/http-proxy/... -run TestHTTPProxy` | ❌ Wave 0 |
| OBSV-01 | Audit log sidecar emits valid JSON-line events for command capture | unit | `go test ./sidecars/audit-log/... -run TestAuditLogFormat` | ❌ Wave 0 |
| OBSV-02 | DNS + HTTP proxy sidecars emit JSON audit events (domain, method, status) | unit | covered by NETW-02/NETW-03 tests with log output verification | ❌ Wave 0 |
| OBSV-03 | Log destination env var switches output target (stdout/CloudWatch/S3) | unit | `go test ./sidecars/audit-log/... -run TestLogDest` | ❌ Wave 0 |
| OBSV-08 | OTel collector config YAML renders correctly from template | unit | `go test ./pkg/compiler/... -run TestOTelConfig` | ❌ Wave 0 |
| OBSV-09 | MLflow run JSON/YAML written to correct S3 key structure | unit (mock S3) | `go test ./pkg/aws/... -run TestMLflowRun` | ❌ Wave 0 |
| OBSV-10 | HTTP proxy injects traceparent header on outbound requests | unit (in-process proxy + mock upstream) | `go test ./sidecars/http-proxy/... -run TestTraceParentInjection` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./sidecars/... ./pkg/compiler/... ./pkg/aws/... ./pkg/lifecycle/... ./internal/app/cmd/... -timeout 30s`
- **Per wave merge:** `go test ./... -timeout 120s`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `sidecars/dns-proxy/dns_proxy_test.go` — covers NETW-02, OBSV-02
- [ ] `sidecars/http-proxy/http_proxy_test.go` — covers NETW-03, OBSV-02, OBSV-10
- [ ] `sidecars/audit-log/audit_log_test.go` — covers OBSV-01, OBSV-03
- [ ] `pkg/aws/scheduler_test.go` — covers PROV-05 (mock scheduler client interface)
- [ ] `pkg/lifecycle/idle_test.go` — covers PROV-06, PROV-07
- [ ] `pkg/aws/mlflow_test.go` — covers OBSV-09 (mock S3 client)
- [ ] `internal/app/cmd/list_test.go` — covers PROV-03
- [ ] `internal/app/cmd/status_test.go` — covers PROV-04

Pattern: all new pkg/aws tests use mock interfaces (same pattern as `discover_test.go`). All sidecar tests start an in-process server on a random port.

---

## Sources

### Primary (HIGH confidence)
- `pkg.go.dev/github.com/miekg/dns` v1.1.72 — ServeMux, HandlerFunc, Server, Client.Exchange, RcodeNameError
- `pkg.go.dev/github.com/elazarl/goproxy` v1.8.2 — HandleConnectFunc, RejectConnect, OkConnect, ConnectAction
- `pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/scheduler` — CreateSchedule, DeleteSchedule, CreateScheduleInput, ActionAfterCompletion, at() expression format
- `docs.aws.amazon.com/scheduler/latest/APIReference/API_CreateSchedule.html` — official CreateSchedule API spec
- `pkg.go.dev/github.com/rs/zerolog` v1.33.0 — already in go.mod; structured JSON output
- `github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/exporter/awss3exporter/README.md` — awss3exporter config, marshaler options, stability (alpha)
- `docs.aws.amazon.com/AmazonECS/latest/developerguide/fargate-security-considerations.html` — Fargate NET_ADMIN constraints

### Secondary (MEDIUM confidence)
- Multiple WebSearch results confirming Fargate does not support iptables/NET_ADMIN for custom proxy injection — consistent across AWS re:Post, community guides, App Mesh deprecation notice (Sep 2024)
- iptables OUTPUT chain with `-m owner --uid-owner` for redirect loop prevention — confirmed in DigitalOcean and nixCraft iptables guides; standard Linux pattern
- CloudWatch → S3 via Kinesis Firehose subscription filter — confirmed by AWS docs and multiple community articles (2024)
- MLflow file store S3 key structure — inferred from MLflow docs; direct Go implementation without Python client is custom but the format is stable
- OTel Go SDK `otel.GetTextMapPropagator().Inject()` for W3C traceparent — confirmed by uptrace.dev and otel.io official docs

### Tertiary (LOW confidence)
- MLflow `meta.yaml` exact field schema for file store — inferred from MLflow source code references and old docs; should be verified against `mlflow/mlflow` GitHub source before implementation
- IMDS exemption for `169.254.170.2` (ECS task metadata endpoint) — mentioned in general ECS docs; specific iptables interaction not officially documented

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all core libraries verified via pkg.go.dev with version numbers; AWS SDK scheduler confirmed via official API reference
- Architecture: HIGH for EC2 patterns; MEDIUM for ECS DNS interception (confirmed limitation, recommended workaround is reasonable but not officially documented by AWS)
- Pitfalls: HIGH for IMDS/iptables (officially documented); MEDIUM for MLflow format compatibility (inferred from docs structure)

**Research date:** 2026-03-21
**Valid until:** 2026-04-21 (stable libraries); OTel awss3exporter alpha stability should be re-verified before implementation given rapid release cadence
