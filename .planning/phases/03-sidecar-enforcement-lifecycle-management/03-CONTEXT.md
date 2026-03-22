# Phase 3: Sidecar Enforcement & Lifecycle Management - Context

**Gathered:** 2026-03-21
**Status:** Ready for planning

<domain>
## Phase Boundary

Build and deploy four sidecars (DNS proxy, HTTP proxy, audit log, tracing) on both EC2 and ECS substrates. Implement TTL auto-destroy via EventBridge Scheduler, idle timeout via audit log activity detection. Add `km list` (S3 state scan default, `--tags` for tag-based with orphan detection), `km status`, and `km logs` commands. OTel traces and MLflow runs stored to S3 for later analysis. No filesystem policy, artifact upload, or email — those are Phase 4.

</domain>

<decisions>
## Implementation Decisions

### DNS + HTTP Proxy Behavior
- HTTPS: SNI-only allowlist — inspect TLS ClientHello SNI field, no decryption, no CA cert needed
- Blocked requests: connection refused — proxy rejects CONNECT tunnel, workload sees connection error, logged in audit
- Crash recovery: systemd Restart=always on EC2, essential=true on ECS. Fail-closed by design — SG blocks all egress if proxy is down
- DNS proxy: forwarding mode — forward allowed queries to VPC resolver (169.254.169.253), deny = NXDOMAIN response. No caching.
- iptables DNAT on EC2: redirect port 53 (DNS) and 80/443 (HTTP) to local sidecar processes. Workload is unaware of proxies.
- ECS: sidecars are containers in the task definition. Traffic interception via environment variable proxy config (http_proxy/https_proxy) since iptables not available in Fargate.

### TTL + Lifecycle Management
- TTL enforcement: EventBridge Scheduler — `km create` schedules a rule that fires at TTL expiry. More robust than sidecar self-terminate (survives instance issues).
- Idle timeout: detected via audit log sidecar activity — no commands or network events for idleTimeout period = idle
- `km list`: S3 state scan as default (fast). `--tags` flag for AWS tag-based scan. Diff between them detects orphans.
- `km list` output: human-readable table by default (sandbox ID, profile, substrate, region, status, TTL remaining). `--json` flag for programmatic use.
- `km status <sandbox-id>`: detailed sandbox state (resources, metadata, timestamps). No inline audit log — separate `km logs` command.
- `km logs <sandbox-id>`: separate command for tailing/querying audit logs from CloudWatch.
- Teardown policy from profile: destroy (default), stop, retain — honored by TTL and idle timeout handlers.

### Audit Log + Observability
- Captures: shell command history + HTTP proxy request log (host, method, status) + DNS query log (domain, allowed/denied)
- Format: JSON lines — one JSON object per line with timestamp, sandbox_id, event_type, source, detail
- Destination: CloudWatch Logs real-time + periodic S3 archive for long-term storage
- CloudWatch log group: `/km/sandboxes/<sandbox-id>/`
- S3 archive: `s3://km-sandbox-artifacts-<suffix>/audit/<sandbox-id>/`

### OTel + MLflow Tracing
- OTel collector: lightweight, exports OTLP JSON to local file, flushes to S3 at sandbox exit or on interval. No live dashboard backend. Cheap storage for later analysis.
- S3 path: `s3://km-sandbox-artifacts-<suffix>/traces/<sandbox-id>/`
- Trace context propagation: HTTP proxy sidecar injects/forwards W3C traceparent headers on outbound requests
- MLflow: S3-backed file store (no tracking server). Each sandbox writes run metadata as JSON to `s3://bucket/mlflow/<experiment>/`. Zero infrastructure.
- MLflow run params: sandbox_id, profile_name, substrate, region, TTL, start_time
- MLflow run metrics: duration, exit_status, commands_executed, bytes_egressed
- Can query later via MLflow CLI or import into a tracking server if needed

### Claude's Discretion
- Exact iptables rules for EC2 DNAT configuration
- Sidecar binary packaging (single Go binary vs separate binaries)
- EventBridge Scheduler Lambda implementation details
- CloudWatch → S3 archive mechanism (subscription filter vs periodic export)
- OTel collector configuration format
- Audit log buffering strategy (real-time vs batch)

</decisions>

<specifics>
## Specific Ideas

- DNS proxy uses miekg/dns library (from stack research)
- HTTP proxy uses elazarl/goproxy library (from stack research)
- Audit log uses zerolog for JSON line output (same as CLI)
- Sidecar processes run as systemd services on EC2 with Restart=always
- EC2 user-data.sh (from Phase 2) gets extended to install and start sidecars + configure iptables
- ECS container images need to be built and pushed to ECR (or use placeholder images for now)

</specifics>

<code_context>
## Existing Code Insights

### Reusable Assets
- `pkg/compiler/userdata.go`: user-data.sh template — extend with sidecar install + iptables rules
- `pkg/compiler/service_hcl.go`: ECS container definitions already have 5 container slots (main + 4 sidecars)
- `infra/modules/ecs/v1.0.0/main.tf`: ECS task definition accepts container list — sidecar images go here
- `infra/modules/ec2spot/v1.0.0/main.tf`: EC2 user-data already bootstrap-ready
- `internal/app/cmd/`: Cobra command pattern established — follow for list, status, logs

### Established Patterns
- Cobra command constructor: `NewXxxCmd(cfg *config.Config)` → register in root.go
- AWS SDK helpers in `pkg/aws/`: tag-based discovery, credential validation
- Terragrunt runner in `pkg/terragrunt/`: Apply/Destroy/Output streaming
- Region-based layout: `infra/live/<region_label>/sandboxes/<sandbox-id>/`

### Integration Points
- `km create` user-data needs sidecar setup injected
- `km create` needs EventBridge Scheduler rule creation after Terragrunt apply
- `km destroy` needs EventBridge rule cleanup
- New commands: `km list`, `km status`, `km logs` → register in root.go
- New sidecar source: `sidecars/dns-proxy/`, `sidecars/http-proxy/`, `sidecars/audit-log/`, `sidecars/tracing/`

</code_context>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 03-sidecar-enforcement-lifecycle-management*
*Context gathered: 2026-03-21*
