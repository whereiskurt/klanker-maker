# Phase 27: Claude Code OTEL Integration - Context

**Gathered:** 2026-03-28
**Status:** Ready for planning
**Source:** Conversation research (Claude Code OTEL docs, existing codebase analysis)

<domain>
## Phase Boundary

This phase wires Claude Code's built-in OpenTelemetry support into the existing sandbox observability stack. The OTel Collector sidecar already runs on every sandbox (EC2 and ECS) listening on gRPC :4317 and HTTP :4318, but currently only handles traces. Claude Code can export prompts, tool calls, API requests, token usage, and cost metrics via OTLP — we just need to:
1. Set the right env vars on the sandbox
2. Extend the collector config to handle logs and metrics (not just traces)
3. Add profile-level control over what gets captured

</domain>

<decisions>
## Implementation Decisions

### Existing Infrastructure (already deployed)
- OTel Collector sidecar (`sidecars/tracing/`) runs `otelcol-contrib` on every sandbox
- Listens on gRPC :4317 and HTTP :4318 (standard OTLP receivers)
- Currently exports ONLY traces to S3 at `s3://<OTEL_S3_BUCKET>/traces/<SANDBOX_ID>/`
- HTTP proxy sidecar already injects W3C traceparent headers into outbound requests
- MLflow run tracking (JSON to S3) already wired into km create/destroy

### Claude Code OTEL Env Vars (from official docs)
The following env vars must be injected into the sandbox environment:
- `CLAUDE_CODE_ENABLE_TELEMETRY=1` — required, enables telemetry
- `OTEL_METRICS_EXPORTER=otlp` — metrics via OTLP
- `OTEL_LOGS_EXPORTER=otlp` — events/logs via OTLP
- `OTEL_EXPORTER_OTLP_PROTOCOL=grpc` — use gRPC to local collector
- `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317` — local collector
- `OTEL_LOG_USER_PROMPTS=1` — opt-in: include actual prompt text (default: only length)
- `OTEL_LOG_TOOL_DETAILS=1` — opt-in: include tool parameters (bash commands, file paths, MCP names)
- `OTEL_RESOURCE_ATTRIBUTES=sandbox_id=<ID>,profile_name=<NAME>,substrate=<ec2|ecs>` — per-sandbox attributes

### Claude Code Telemetry Events (what we'll capture)
- `claude_code.user_prompt` — prompt text or length, with prompt.id for correlation
- `claude_code.tool_result` — tool name, success/fail, duration_ms, error, decision_type; with OTEL_LOG_TOOL_DETAILS: bash commands, file paths
- `claude_code.api_request` — model, cost_usd, duration_ms, input/output/cache tokens, fast/normal mode
- `claude_code.api_error` — model, error, HTTP status code, attempt number
- `claude_code.tool_decision` — accept/reject with source

### Claude Code Metrics (what we'll capture)
- `claude_code.token.usage` — by type (input/output/cacheRead/cacheCreation) and model
- `claude_code.cost.usage` — estimated USD by model
- `claude_code.session.count` — sessions started
- `claude_code.lines_of_code.count` — added/removed
- `claude_code.commit.count`, `claude_code.pull_request.count`
- `claude_code.active_time.total` — user vs CLI active time
- `claude_code.code_edit_tool.decision` — accept/reject by tool and language

### Collector Config Extension
- Current config: only `traces` pipeline (receivers: otlp → processors: batch → exporters: awss3)
- Need to add: `logs` pipeline and `metrics` pipeline with same pattern
- S3 prefix structure: `traces/<SANDBOX_ID>/`, `logs/<SANDBOX_ID>/`, `metrics/<SANDBOX_ID>/`
- All using otlp_json marshaler for human-readable inspection

### Profile Schema Extension
- Add `spec.observability.claudeTelemetry` section:
  - `enabled: true|false` (default true) — master switch
  - `logPrompts: true|false` (default false) — controls OTEL_LOG_USER_PROMPTS
  - `logToolDetails: true|false` (default false) — controls OTEL_LOG_TOOL_DETAILS
- Hardened/sealed profiles: enabled=true, logPrompts=false, logToolDetails=false (observe but don't capture sensitive prompt content)
- Open-dev/claude-dev profiles: enabled=true, logPrompts=true, logToolDetails=true (full visibility)

### Network Considerations
- Claude Code uses OTLP gRPC to localhost:4317 — this is loopback, no proxy involvement
- If Claude Code falls back to HTTP (4318), that's also loopback
- No changes needed to DNS proxy or HTTP proxy allowlists for localhost traffic
- OTEL-07 requirement may be unnecessary if all traffic is loopback — verify during implementation

### Claude's Discretion
- Whether to use separate S3 prefixes per signal type or a single prefix
- Batch size and timeout tuning for logs/metrics pipelines
- Whether to add a `console` exporter option for debugging

</decisions>

<specifics>
## Specific Ideas

### Files to modify
1. `sidecars/tracing/config.yaml` — add logs + metrics pipelines
2. `pkg/compiler/userdata.go` — inject Claude OTEL env vars into EC2 user-data
3. `pkg/compiler/service_hcl.go` — inject Claude OTEL env vars into ECS container definition
4. `pkg/profile/types.go` — add ClaudeTelemetrySpec to ObservabilitySpec
5. `pkg/profile/schemas/sandbox_profile.schema.json` — schema for new fields
6. `profiles/*.yaml` — set claudeTelemetry defaults per built-in profile

### Env var injection pattern
Follow the existing pattern for ALLOWED_SUFFIXES / ALLOWED_HOSTS — the compiler already injects env vars from profile fields into both EC2 systemd units and ECS container environments.

### S3 export format
Use `otlp_json` marshaler (same as traces) — this produces human-readable JSON that can be:
- Inspected directly via `aws s3 cp` + `jq`
- Imported into any OTLP-compatible backend later (Grafana, Honeycomb, Datadog)
- Queried via Athena if needed

</specifics>

<deferred>
## Deferred Ideas

- Real-time streaming to Grafana/Honeycomb/Datadog (S3 batch export first, streaming later)
- Dashboard/UI for viewing telemetry data (separate phase)
- Correlation between Claude Code OTEL data and budget-enforcer spend tracking
- Athena table definitions for querying OTLP JSON in S3
- Cost alerting based on claude_code.cost.usage metrics vs budget thresholds

</deferred>

---

*Phase: 27-claude-code-otel-integration-sandbox-observability-via-built-in-telemetry*
*Context gathered: 2026-03-28 via conversation research*
