# Phase 27: Claude Code OTEL Integration — Research

**Researched:** 2026-03-28
**Domain:** OpenTelemetry collector configuration, Claude Code telemetry env vars, Go profile schema extension, compiler env var injection
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Existing Infrastructure (already deployed)**
- OTel Collector sidecar (`sidecars/tracing/`) runs `otelcol-contrib` on every sandbox
- Listens on gRPC :4317 and HTTP :4318 (standard OTLP receivers)
- Currently exports ONLY traces to S3 at `s3://<OTEL_S3_BUCKET>/traces/<SANDBOX_ID>/`
- HTTP proxy sidecar already injects W3C traceparent headers into outbound requests
- MLflow run tracking (JSON to S3) already wired into km create/destroy

**Claude Code OTEL Env Vars (from official docs)**
The following env vars must be injected into the sandbox environment:
- `CLAUDE_CODE_ENABLE_TELEMETRY=1` — required, enables telemetry
- `OTEL_METRICS_EXPORTER=otlp` — metrics via OTLP
- `OTEL_LOGS_EXPORTER=otlp` — events/logs via OTLP
- `OTEL_EXPORTER_OTLP_PROTOCOL=grpc` — use gRPC to local collector
- `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317` — local collector
- `OTEL_LOG_USER_PROMPTS=1` — opt-in: include actual prompt text (default: only length)
- `OTEL_LOG_TOOL_DETAILS=1` — opt-in: include tool parameters (bash commands, file paths, MCP names)
- `OTEL_RESOURCE_ATTRIBUTES=sandbox_id=<ID>,profile_name=<NAME>,substrate=<ec2|ecs>` — per-sandbox attributes

**Collector Config Extension**
- Current config: only `traces` pipeline (receivers: otlp → processors: batch → exporters: awss3)
- Need to add: `logs` pipeline and `metrics` pipeline with same pattern
- S3 prefix structure: `traces/<SANDBOX_ID>/`, `logs/<SANDBOX_ID>/`, `metrics/<SANDBOX_ID>/`
- All using otlp_json marshaler for human-readable inspection

**Profile Schema Extension**
- Add `spec.observability.claudeTelemetry` section:
  - `enabled: true|false` (default true) — master switch
  - `logPrompts: true|false` (default false) — controls OTEL_LOG_USER_PROMPTS
  - `logToolDetails: true|false` (default false) — controls OTEL_LOG_TOOL_DETAILS
- Hardened/sealed profiles: enabled=true, logPrompts=false, logToolDetails=false
- Open-dev/claude-dev profiles: enabled=true, logPrompts=true, logToolDetails=true

**Env var injection pattern**
Follow the existing pattern for ALLOWED_SUFFIXES / ALLOWED_HOSTS — the compiler already injects env vars from profile fields into both EC2 systemd units and ECS container environments.

**S3 export format**
Use `otlp_json` marshaler (same as traces).

**Network Considerations**
- Claude Code uses OTLP gRPC to localhost:4317 — this is loopback, no proxy involvement
- If Claude Code falls back to HTTP (4318), that's also loopback
- No changes needed to DNS proxy or HTTP proxy allowlists for localhost traffic
- OTEL-07 requirement may be unnecessary if all traffic is loopback — verify during implementation

### Claude's Discretion
- Whether to use separate S3 prefixes per signal type or a single prefix
- Batch size and timeout tuning for logs/metrics pipelines
- Whether to add a `console` exporter option for debugging

### Deferred Ideas (OUT OF SCOPE)
- Real-time streaming to Grafana/Honeycomb/Datadog (S3 batch export first, streaming later)
- Dashboard/UI for viewing telemetry data (separate phase)
- Correlation between Claude Code OTEL data and budget-enforcer spend tracking
- Athena table definitions for querying OTLP JSON in S3
- Cost alerting based on claude_code.cost.usage metrics vs budget thresholds
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| OTEL-01 | Claude Code OTEL env vars injected into sandbox via user-data (EC2) and container environment (ECS) | Compiler analysis: EC2 uses `ProfileEnv` map in userDataParams → `/etc/profile.d/km-profile-env.sh`; ECS injects into `containers[main].environment[]` block in ecsServiceHCLTemplate |
| OTEL-02 | OTel Collector sidecar config extended with `logs` and `metrics` pipelines | `sidecars/tracing/config.yaml` currently has only `traces` pipeline; needs two new pipelines using separate awss3 exporters or the same exporter with per-signal prefix |
| OTEL-03 | Claude Code log events (prompt, tool_result, api_request, api_error) flow through collector to S3 | Handled by OTLP logs pipeline once OTEL-01 and OTEL-02 are in place; no additional code needed |
| OTEL-04 | Claude Code metrics (token usage, cost, session count, etc.) flow through collector to S3 | Handled by OTLP metrics pipeline once OTEL-01 and OTEL-02 are in place |
| OTEL-05 | Profile schema supports `spec.observability.claudeTelemetry` with enabled/logPrompts/logToolDetails | Requires Go type addition to `ObservabilitySpec`, JSON schema update, and validation |
| OTEL-06 | OTEL_RESOURCE_ATTRIBUTES includes sandbox_id, profile_name, substrate | Must be constructed dynamically in compiler from sandboxID, profile.Metadata.Name, and substrate |
| OTEL-07 | Collector HTTP endpoint (4318) added to sandbox network allowlist | CONTEXT.md notes this may be unnecessary (loopback traffic); if needed it is a no-op for localhost |
</phase_requirements>

---

## Summary

This phase wires Claude Code's built-in OpenTelemetry support into the existing sandbox observability stack. The OTel Collector sidecar already runs on every sandbox (EC2 and ECS) and listens on both gRPC :4317 and HTTP :4318, but the `sidecars/tracing/config.yaml` only defines a `traces` pipeline. Claude Code can emit logs (events: prompts, tool results, API requests) and metrics (token usage, cost, session counts) via OTLP when the right env vars are set. The two pieces of work are: (1) extend the collector YAML to add `logs` and `metrics` pipelines, and (2) inject the Claude Code env vars from the profile's new `claudeTelemetry` field through the compiler into both EC2 user-data and ECS container definitions.

The implementation is additive and low-risk. The collector config change is backward-compatible (existing `traces` pipeline untouched). The profile schema change introduces an optional field that defaults to a safe behavior (telemetry enabled, prompts not logged). The compiler changes follow the established pattern for `ProfileEnv` (EC2) and `containers[main].environment[]` (ECS).

**Primary recommendation:** Add `claudeTelemetry` to `ObservabilitySpec` in `types.go`, update the JSON schema's `observability` object, add a new S3 exporter per signal in `config.yaml`, and inject the env vars from the compiler — following exact patterns already present in the codebase.

---

## Standard Stack

### Core

| Library / Tool | Version | Purpose | Why Standard |
|----------------|---------|---------|--------------|
| otelcol-contrib | existing | OTel Collector with contrib exporters including awss3exporter | Already deployed on all sandboxes |
| awss3exporter | bundled with otelcol-contrib | Exports OTLP data to S3 with configurable prefix and marshaler | Used for traces today; same config for logs/metrics |
| Go text/template | stdlib | EC2 user-data and ECS HCL template rendering | All compiler output uses this pattern |
| goccy/go-yaml | existing | YAML marshaling for profile types | Project-standard YAML library |
| santhosh-tekuri/jsonschema/v6 | existing | JSON Schema validation | Project-standard schema library |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| Claude Code `@anthropic-ai/claude-code` npm package | latest | The telemetry source; reads OTel env vars at startup | Already installed in claude-dev profile initCommands |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Separate exporters per signal | Single exporter with `s3_prefix` from resource attribute | Separate exporters is simpler — no dynamic prefix templating needed; lock-step with current traces pattern |
| gRPC endpoint (:4317) | HTTP endpoint (:4318) | gRPC is more efficient for high-frequency metric emission; CONTEXT.md locks gRPC as default |

**Installation:** No new dependencies. All required components are already present.

---

## Architecture Patterns

### Current Collector Config Structure

```yaml
# sidecars/tracing/config.yaml (current)
receivers:
  otlp:
    protocols:
      grpc: { endpoint: 0.0.0.0:4317 }
      http: { endpoint: 0.0.0.0:4318 }

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

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [awss3]
```

### Extended Collector Config (after this phase)

```yaml
# sidecars/tracing/config.yaml (after phase 27)
receivers:
  otlp:
    protocols:
      grpc: { endpoint: 0.0.0.0:4317 }
      http: { endpoint: 0.0.0.0:4318 }

processors:
  batch:
    timeout: 10s
    send_batch_size: 1024

exporters:
  awss3/traces:
    s3_uploader:
      region: ${AWS_REGION}
      s3_bucket: ${OTEL_S3_BUCKET}
      s3_prefix: "traces/${SANDBOX_ID}"
    marshaler: otlp_json
    timeout: 30s

  awss3/logs:
    s3_uploader:
      region: ${AWS_REGION}
      s3_bucket: ${OTEL_S3_BUCKET}
      s3_prefix: "logs/${SANDBOX_ID}"
    marshaler: otlp_json
    timeout: 30s

  awss3/metrics:
    s3_uploader:
      region: ${AWS_REGION}
      s3_bucket: ${OTEL_S3_BUCKET}
      s3_prefix: "metrics/${SANDBOX_ID}"
    marshaler: otlp_json
    timeout: 30s

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [awss3/traces]
    logs:
      receivers: [otlp]
      processors: [batch]
      exporters: [awss3/logs]
    metrics:
      receivers: [otlp]
      processors: [batch]
      exporters: [awss3/metrics]
```

**Key insight:** otelcol-contrib supports named exporter instances (`awss3/traces`, `awss3/logs`, `awss3/metrics`) using the `component/name` syntax. This is the standard way to have multiple instances of the same exporter type with different configs. (HIGH confidence — standard OTel Collector pattern from official docs.)

### Pattern 1: ProfileEnv Injection (EC2)

EC2 user-data already has a dedicated section for profile env vars:

```go
// pkg/compiler/userdata.go — template section 2.8 (already exists)
{{- if .ProfileEnv }}
cat > /etc/profile.d/km-profile-env.sh << 'PROFILE_ENV'
{{- range $key, $value := .ProfileEnv }}
export {{ $key }}="{{ $value }}"
{{- end }}
PROFILE_ENV
{{- end }}
```

The `userDataParams.ProfileEnv` field is `map[string]string` populated from `p.Spec.Execution.Env`. For Phase 27, the compiler constructs the Claude OTEL env vars from the profile's `claudeTelemetry` fields and merges them into this map — or adds them to a separate block in the tracing sidecar section.

**Recommended approach:** Add a new template section in user-data (section 5.5 — after sidecar binary install) that writes Claude OTEL env vars to `/etc/profile.d/km-claude-otel.sh`. This keeps Claude telemetry vars separate from user-defined profile env vars and makes them easy to identify in bootstrap logs.

### Pattern 2: ECS Container Environment Injection

ECS container definitions inject env vars as an array in the HCL template:

```hcl
# In ecsServiceHCLTemplate, containers[main].environment
environment = [
  { name = "SANDBOX_ID",    value = "{{ .SandboxID }}" },
  { name = "KM_LABEL",      value = "km" },
  { name = "HTTP_PROXY",    value = "http://localhost:3128" },
  ...
{{- if .HasEmail }}
  { name = "KM_EMAIL_ADDRESS", value = "{{ .SandboxEmail }}" },
{{- end }}
]
```

For Phase 27, a similar conditional block is added for Claude telemetry vars:

```hcl
{{- if .ClaudeTelemetryEnabled }}
  { name = "CLAUDE_CODE_ENABLE_TELEMETRY", value = "1" },
  { name = "OTEL_METRICS_EXPORTER",        value = "otlp" },
  { name = "OTEL_LOGS_EXPORTER",           value = "otlp" },
  { name = "OTEL_EXPORTER_OTLP_PROTOCOL",  value = "grpc" },
  { name = "OTEL_EXPORTER_OTLP_ENDPOINT",  value = "http://localhost:4317" },
  { name = "OTEL_RESOURCE_ATTRIBUTES",     value = "{{ .OTELResourceAttributes }}" },
{{- if .ClaudeLogPrompts }}
  { name = "OTEL_LOG_USER_PROMPTS",        value = "1" },
{{- end }}
{{- if .ClaudeLogToolDetails }}
  { name = "OTEL_LOG_TOOL_DETAILS",        value = "1" },
{{- end }}
{{- end }}
```

### Pattern 3: Profile Types Extension

The existing `ObservabilitySpec` in `pkg/profile/types.go`:

```go
type ObservabilitySpec struct {
    CommandLog LogDestination `yaml:"commandLog"`
    NetworkLog LogDestination `yaml:"networkLog"`
}
```

Extend to:

```go
type ObservabilitySpec struct {
    CommandLog       LogDestination       `yaml:"commandLog"`
    NetworkLog       LogDestination       `yaml:"networkLog"`
    ClaudeTelemetry *ClaudeTelemetrySpec  `yaml:"claudeTelemetry,omitempty"`
}

// ClaudeTelemetrySpec controls Claude Code built-in OpenTelemetry export behavior.
// When nil, telemetry is disabled. When non-nil, telemetry is exported via OTLP
// to the local OTel Collector sidecar (gRPC localhost:4317).
type ClaudeTelemetrySpec struct {
    // Enabled is the master switch for Claude Code OTEL export.
    // Default false when field is absent.
    Enabled bool `yaml:"enabled"`
    // LogPrompts controls whether OTEL_LOG_USER_PROMPTS=1 is set.
    // When true, full prompt text is included in telemetry events.
    // Default false (only prompt length is captured).
    LogPrompts bool `yaml:"logPrompts"`
    // LogToolDetails controls whether OTEL_LOG_TOOL_DETAILS=1 is set.
    // When true, tool parameters (bash commands, file paths) are captured.
    // Default false.
    LogToolDetails bool `yaml:"logToolDetails"`
}
```

**Design choice:** `ClaudeTelemetry` is a pointer (`*ClaudeTelemetrySpec`) so that nil = not configured = telemetry disabled. Profiles that don't include the field get no Claude telemetry injection. This is backward-compatible.

### Pattern 4: JSON Schema Extension

The `observability` object in `sandbox_profile.schema.json` currently uses `additionalProperties: false`. Adding `claudeTelemetry` requires updating this object definition. Since the schema is embedded via `//go:embed`, schema and Go types must stay in sync.

```json
"observability": {
  "type": "object",
  "required": ["commandLog", "networkLog"],
  "additionalProperties": false,
  "properties": {
    "commandLog": { "$ref": "#/$defs/LogDestination" },
    "networkLog": { "$ref": "#/$defs/LogDestination" },
    "claudeTelemetry": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "enabled":        { "type": "boolean", "description": "Master switch for Claude Code OTLP export" },
        "logPrompts":     { "type": "boolean", "description": "Include full prompt text in telemetry (OTEL_LOG_USER_PROMPTS)" },
        "logToolDetails": { "type": "boolean", "description": "Include tool parameters in telemetry (OTEL_LOG_TOOL_DETAILS)" }
      }
    }
  }
}
```

### Pattern 5: OTEL_RESOURCE_ATTRIBUTES Construction

The resource attribute string must be constructed at compile time by the compiler from sandboxID, profile name, and substrate:

```go
// In compiler, for both EC2 and ECS
func claudeResourceAttributes(sandboxID, profileName, substrate string) string {
    return fmt.Sprintf("sandbox_id=%s,profile_name=%s,substrate=%s",
        sandboxID, profileName, substrate)
}
```

This string is then injected as the value of `OTEL_RESOURCE_ATTRIBUTES`.

### EC2: Tracing Sidecar Startup Gap

**Critical finding:** The EC2 user-data does NOT currently start `km-tracing` as a systemd service. The config file is downloaded (`aws s3 cp ... /etc/km/tracing/config.yaml`) but no systemd unit is written and no `systemctl enable/start` call includes it.

The ECS path has a km-tracing container that runs `otelcol-contrib`. The EC2 path is missing the systemd unit for `km-tracing`.

For Phase 27 to work on EC2, a km-tracing systemd unit must be added to user-data. The `otelcol-contrib` binary must also be downloaded alongside the config. This is a prerequisite that may have been handled in Phase 26, but the current code does not show it.

**Action:** Add `km-tracing.service` systemd unit to user-data template, downloading `otelcol-contrib` from S3 and starting it with `--config /etc/km/tracing/config.yaml`.

### Anti-Patterns to Avoid

- **Don't add Claude telemetry vars to `spec.execution.env`:** Profile YAML would expose them and users would have to maintain them. Compiler derives them from `claudeTelemetry` spec — users only set the semantic flags.
- **Don't use a single S3 exporter with a single prefix for all signals:** Mix of traces/logs/metrics in one prefix makes tooling harder. Separate prefixes per signal is standard practice.
- **Don't make `claudeTelemetry.enabled` default to true via schema default:** Go bool zero-value is false; pointer nil = absent = no injection. Profiles that want telemetry must explicitly set it. Use built-in profile YAML files to opt in each profile appropriately.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Multi-signal OTLP export | Custom S3-write code | otelcol-contrib awss3exporter | Battle-tested, handles batching, retries, file rotation |
| OTLP format serialization | Custom JSON serializer for spans/logs/metrics | `otlp_json` marshaler built into awss3exporter | Ensures standard OTLP JSON format for downstream compatibility |
| Named exporter instances | Single exporter with conditional logic | OTel Collector `component/name` syntax | Standard pattern, supported by otelcol-contrib |

---

## Common Pitfalls

### Pitfall 1: OTEL Collector Named Exporter Syntax

**What goes wrong:** Using `awss3` as the exporter key for all three pipelines causes the last definition to override the earlier ones — all signals go to the same S3 prefix.
**Why it happens:** YAML last-key-wins semantics.
**How to avoid:** Use the `component/name` naming convention: `awss3/traces`, `awss3/logs`, `awss3/metrics`. This is standard otelcol syntax for multiple instances of the same component type.
**Warning signs:** All three pipelines emit to the same S3 prefix.

### Pitfall 2: EC2 Missing km-tracing Systemd Unit

**What goes wrong:** The tracing config is downloaded to the EC2 instance but `otelcol-contrib` is never started, so Claude Code's OTLP export goes nowhere.
**Why it happens:** The current user-data template downloads the config but has no systemd unit for tracing (unlike dns-proxy, http-proxy, audit-log). The ECS substrate has a container for this; EC2 relies on user-data.
**How to avoid:** Add a `km-tracing.service` systemd unit to the user-data template. Must also download the `otelcol-contrib` binary from S3 (alongside the config).
**Warning signs:** No OTEL data in S3 after Claude Code runs on EC2; `systemctl status km-tracing` not found.

### Pitfall 3: JSON Schema `additionalProperties: false` Blocks New Field

**What goes wrong:** Adding `claudeTelemetry` to the Go types but forgetting to update the JSON schema causes `km validate` to reject profiles with the new field.
**Why it happens:** The `observability` schema object has `"additionalProperties": false`.
**How to avoid:** Update both Go types and the JSON schema in the same plan step. Run `go test ./pkg/profile/...` to verify schema and types stay in sync.
**Warning signs:** `km validate profiles/claude-dev.yaml` reports "additionalProperties" error for `claudeTelemetry`.

### Pitfall 4: ECS NO_PROXY Must Include localhost

**What goes wrong:** Claude Code OTLP export to `http://localhost:4317` is intercepted by the HTTP proxy because the ECS `NO_PROXY` env var doesn't include `localhost` or `127.0.0.1`.
**Why it happens:** ECS container env currently sets `NO_PROXY=169.254.169.254,169.254.170.2,localhost,127.0.0.1` — this is already correct.
**Status:** Already handled in ECS template. Verify EC2 also includes localhost in the iptables exemption — current iptables rules only exempt `169.254.169.254` and root UID from DNAT. Port 4317 (gRPC) is not in the DNAT redirect rules (only 80 and 443), so localhost:4317 traffic is not affected.

### Pitfall 5: Claude Code Telemetry Requires `CLAUDE_CODE_ENABLE_TELEMETRY=1`

**What goes wrong:** Setting `OTEL_METRICS_EXPORTER=otlp` and `OTEL_LOGS_EXPORTER=otlp` without `CLAUDE_CODE_ENABLE_TELEMETRY=1` produces no telemetry data.
**Why it happens:** Claude Code has a master opt-in gate separate from the standard OTel env vars.
**How to avoid:** Always inject `CLAUDE_CODE_ENABLE_TELEMETRY=1` when `claudeTelemetry.enabled=true`.

### Pitfall 6: OTEL Batch Timeout Tradeoff

**What goes wrong:** Using the same 10s batch timeout for metrics that Claude Code emits at low frequency means S3 files may not be written for several minutes after session ends.
**Why it happens:** The batch processor holds data until `timeout` OR `send_batch_size` is reached.
**How to avoid:** Consider a shorter timeout (5s) for logs/metrics pipelines since Claude Code events are low-volume but time-sensitive for cost visibility. The traces pipeline can keep 10s.
**Recommendation (Claude's discretion):** Use 5s batch timeout for logs/metrics pipelines; keep 10s for traces.

---

## Code Examples

### EC2: km-tracing Systemd Unit (new section in user-data)

```bash
# Section to add in user-data.go template (after sidecar binary download)
# Download otelcol-contrib binary for tracing
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/otelcol-contrib" /opt/km/bin/otelcol-contrib
chmod +x /opt/km/bin/otelcol-contrib

cat > /etc/systemd/system/km-tracing.service << 'UNIT'
[Unit]
Description=Klankrmkr OTel Collector sidecar
After=network.target
[Service]
User=km-sidecar
Environment=SANDBOX_ID={{ .SandboxID }}
Environment=OTEL_S3_BUCKET={{ .KMArtifactsBucket }}
Environment=AWS_REGION={{ .AWSRegion }}
ExecStart=/opt/km/bin/otelcol-contrib --config /etc/km/tracing/config.yaml
Restart=always
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT
```

Then add `km-tracing` to the `systemctl enable` and `systemctl start` lines.

### EC2: Claude OTEL Env Var Section (new section in user-data)

```bash
# Section to add in user-data.go template (conditional on ClaudeTelemetryEnabled)
{{- if .ClaudeTelemetryEnabled }}
cat > /etc/profile.d/km-claude-otel.sh << 'CLAUDE_OTEL'
export CLAUDE_CODE_ENABLE_TELEMETRY=1
export OTEL_METRICS_EXPORTER=otlp
export OTEL_LOGS_EXPORTER=otlp
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
export OTEL_RESOURCE_ATTRIBUTES="{{ .OTELResourceAttributes }}"
{{- if .ClaudeLogPrompts }}
export OTEL_LOG_USER_PROMPTS=1
{{- end }}
{{- if .ClaudeLogToolDetails }}
export OTEL_LOG_TOOL_DETAILS=1
{{- end }}
CLAUDE_OTEL
chmod 644 /etc/profile.d/km-claude-otel.sh
{{- end }}
```

### Built-in Profile YAML Example (claude-dev.yaml addition)

```yaml
# Add to spec.observability section of profiles/claude-dev.yaml
observability:
  commandLog:
    destination: cloudwatch
    logGroup: /klankrmkr/sandboxes
  networkLog:
    destination: cloudwatch
    logGroup: /klankrmkr/network
  claudeTelemetry:
    enabled: true
    logPrompts: true
    logToolDetails: true
```

For hardened/sealed profiles:
```yaml
  claudeTelemetry:
    enabled: true
    logPrompts: false
    logToolDetails: false
```

---

## Files to Modify

| File | Change |
|------|--------|
| `sidecars/tracing/config.yaml` | Add `awss3/logs` and `awss3/metrics` exporters; add `logs` and `metrics` pipelines; rename `awss3` to `awss3/traces` |
| `pkg/profile/types.go` | Add `ClaudeTelemetrySpec` struct; add `ClaudeTelemetry *ClaudeTelemetrySpec` to `ObservabilitySpec` |
| `pkg/profile/schemas/sandbox_profile.schema.json` | Add `claudeTelemetry` to `observability` object definition |
| `pkg/compiler/userdata.go` | Add `ClaudeTelemetryEnabled`, `ClaudeLogPrompts`, `ClaudeLogToolDetails`, `OTELResourceAttributes` to `userDataParams`; add km-tracing systemd unit; add Claude OTEL profile.d section; add km-tracing to systemctl enable/start |
| `pkg/compiler/service_hcl.go` | Add `ClaudeTelemetryEnabled`, `ClaudeLogPrompts`, `ClaudeLogToolDetails`, `OTELResourceAttributes` to ECS params struct; add conditional env var block to main container |
| `profiles/claude-dev.yaml` | Add `spec.observability.claudeTelemetry: {enabled: true, logPrompts: true, logToolDetails: true}` |
| `profiles/open-dev.yaml` | Add `spec.observability.claudeTelemetry: {enabled: true, logPrompts: true, logToolDetails: true}` |
| `profiles/restricted-dev.yaml` | Add `spec.observability.claudeTelemetry: {enabled: true, logPrompts: false, logToolDetails: false}` |
| `profiles/hardened.yaml` | Add `spec.observability.claudeTelemetry: {enabled: true, logPrompts: false, logToolDetails: false}` |
| `profiles/sealed.yaml` | Add `spec.observability.claudeTelemetry: {enabled: true, logPrompts: false, logToolDetails: false}` |

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Single OTLP pipeline (traces only) | Three pipelines: traces, logs, metrics | This phase | Claude Code log events and metrics flow to S3 |
| No Claude Code telemetry config | `spec.observability.claudeTelemetry` profile field | This phase | Operator controls per-profile what Claude Code captures |

---

## Open Questions

1. **Is otelcol-contrib binary already in S3 artifacts bucket for EC2?**
   - What we know: The config is downloaded from S3; the ECS image runs otelcol-contrib as a container.
   - What's unclear: Whether the EC2 path ever downloads and starts otelcol-contrib. Current user-data template has no systemd unit for tracing.
   - Recommendation: Plan 27-01 should include adding the otelcol-contrib binary download and systemd unit to user-data. If Phase 26 added it (the sidecar fixes phase), verify by checking 26-SUMMARY files.

2. **OTEL-07: Is port 4318 allowlist actually needed?**
   - What we know: CONTEXT.md notes this may be unnecessary since localhost traffic bypasses proxy.
   - What's unclear: Whether the iptables DNAT rules on EC2 intercept port 4318. Current rules only redirect 80 and 443. Port 4317 and 4318 are not redirected.
   - Recommendation: Confirm OTEL-07 is a no-op (nothing to change) during implementation; document the finding.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./pkg/profile/... ./pkg/compiler/...` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| OTEL-01 (EC2) | `generateUserData` emits CLAUDE_CODE_ENABLE_TELEMETRY when claudeTelemetry.enabled=true | unit | `go test ./pkg/compiler/... -run TestClaudeTelemetryEC2` | ❌ Wave 0 |
| OTEL-01 (EC2 negative) | `generateUserData` does NOT emit Claude vars when claudeTelemetry is nil | unit | `go test ./pkg/compiler/... -run TestClaudeTelemetryEC2Absent` | ❌ Wave 0 |
| OTEL-01 (ECS) | `generateECSServiceHCL` emits CLAUDE_CODE_ENABLE_TELEMETRY when claudeTelemetry.enabled=true | unit | `go test ./pkg/compiler/... -run TestClaudeTelemetryECS` | ❌ Wave 0 |
| OTEL-01 logPrompts | `generateUserData` emits OTEL_LOG_USER_PROMPTS=1 when logPrompts=true | unit | `go test ./pkg/compiler/... -run TestClaudeLogPrompts` | ❌ Wave 0 |
| OTEL-01 logToolDetails | `generateUserData` emits OTEL_LOG_TOOL_DETAILS=1 when logToolDetails=true | unit | `go test ./pkg/compiler/... -run TestClaudeLogToolDetails` | ❌ Wave 0 |
| OTEL-05 | JSON schema accepts claudeTelemetry field | unit | `go test ./pkg/profile/... -run TestClaudeTelemetrySchema` | ❌ Wave 0 |
| OTEL-05 | JSON schema rejects unknown fields under claudeTelemetry | unit | `go test ./pkg/profile/... -run TestClaudeTelemetrySchemaRejectsUnknown` | ❌ Wave 0 |
| OTEL-06 | OTEL_RESOURCE_ATTRIBUTES contains sandbox_id, profile_name, substrate | unit | `go test ./pkg/compiler/... -run TestOTELResourceAttributes` | ❌ Wave 0 |
| Built-in profiles | claude-dev, open-dev, hardened, sealed validate after claudeTelemetry addition | integration | `go test ./pkg/profile/... -run TestBuiltinProfiles` | ✅ (builtins_test.go) |

### Sampling Rate
- **Per task commit:** `go test ./pkg/profile/... ./pkg/compiler/...`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/compiler/claude_otel_test.go` — covers OTEL-01 (EC2 + ECS), logPrompts, logToolDetails, OTEL-06
- [ ] `pkg/profile/claude_telemetry_test.go` — covers OTEL-05 schema accept/reject

*(Existing `builtins_test.go` will catch built-in profile breakage automatically once the schema and YAML are updated.)*

---

## Sources

### Primary (HIGH confidence)
- Codebase direct inspection — `pkg/compiler/userdata.go`, `pkg/compiler/service_hcl.go`, `pkg/profile/types.go`, `sidecars/tracing/config.yaml`, `profiles/*.yaml`, `pkg/profile/schemas/sandbox_profile.schema.json`
- CONTEXT.md — locked decisions with official Claude Code OTEL env var names sourced from Anthropic docs

### Secondary (MEDIUM confidence)
- OTel Collector contrib naming convention (`component/name` syntax for multiple instances) — standard pattern in official OTel documentation; consistent with otelcol-contrib component model

### Tertiary (LOW confidence)
- None — all key claims verified against codebase or CONTEXT.md

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all components already deployed; no new dependencies
- Architecture: HIGH — patterns directly observed in existing compiler code
- Pitfalls: HIGH — most derive from direct code inspection (EC2 tracing gap, schema additionalProperties)
- Validation arch: HIGH — test patterns match existing compiler test files exactly

**Research date:** 2026-03-28
**Valid until:** 2026-06-28 (stable domain — OTel Collector config syntax and Claude Code env vars are not fast-moving)
