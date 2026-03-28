---
phase: 27-claude-code-otel-integration-sandbox-observability-via-built-in-telemetry
verified: 2026-03-28T22:28:11Z
status: human_needed
score: 9/9 must-haves verified
human_verification:
  - test: "Provision an EC2 sandbox with an open-dev or claude-dev profile, confirm otelcol-contrib starts as km-tracing.service, and confirm Claude Code emits OTLP events to localhost:4317"
    expected: "systemctl status km-tracing shows 'active (running)', otelcol-contrib process is listening on :4317/:4318, and S3 bucket shows objects under traces/<sandbox-id>/, logs/<sandbox-id>/, metrics/<sandbox-id>/ prefixes after Claude Code is run"
    why_human: "End-to-end pipeline requires a live EC2 sandbox with the otelcol-contrib binary uploaded to the artifacts S3 bucket. The binary upload is not part of this phase; if the binary is absent from S3 the systemd unit will fail. Cannot verify the binary exists in S3 without live AWS access."
  - test: "Provision an ECS sandbox with a claude-dev profile, run Claude Code, and confirm OTLP telemetry reaches S3"
    expected: "Claude Code emits to localhost:4317 (the OTel Collector sidecar container on the Fargate task network), S3 shows OTLP JSON objects under logs/ and metrics/ prefixes"
    why_human: "ECS sidecar container network topology and task definition rendering require live Fargate environment. Cannot confirm OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317 resolves correctly within the Fargate task network without deploying."
  - test: "Verify otelcol-contrib binary is present in the artifacts S3 bucket at sidecars/otelcol-contrib"
    expected: "aws s3 ls s3://<account>-km-artifacts/sidecars/otelcol-contrib returns a file"
    why_human: "The binary must have been manually uploaded as a one-time operation. The phase adds the download and systemd unit to user-data, but does not automate the binary upload. If the binary is absent, km-tracing.service will fail at boot."
---

# Phase 27: Claude Code OTEL Integration Verification Report

**Phase Goal:** Claude Code running inside sandboxes exports full OpenTelemetry telemetry (prompts, tool calls, API requests, token usage, cost metrics) through the existing OTel Collector sidecar to S3 — giving operators complete visibility into agent behavior, spend, and performance per sandbox session
**Verified:** 2026-03-28T22:28:11Z
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| #  | Truth | Status | Evidence |
|----|-------|--------|----------|
| 1  | OTel Collector config has traces, logs, and metrics pipelines all exporting to S3 | VERIFIED | `sidecars/tracing/config.yaml` has `awss3/traces`, `awss3/logs`, `awss3/metrics` exporters and three service pipelines |
| 2  | Profile schema accepts `spec.observability.claudeTelemetry` with enabled, logPrompts, logToolDetails | VERIFIED | `ClaudeTelemetrySpec` struct in `pkg/profile/types.go` line 218; `claudeTelemetry` in JSON schema line 339 |
| 3  | Built-in profiles have appropriate claudeTelemetry defaults (open/claude-dev: full; hardened/sealed: no prompt logging) | VERIFIED | All 5 `profiles/` YAMLs and 4 `pkg/profile/builtins/` YAMLs have correct values confirmed by grep |
| 4  | EC2 sandboxes with claudeTelemetry enabled have all Claude Code OTEL env vars in /etc/profile.d/ | VERIFIED | `userdata.go` section 2.9 template confirmed at line 103-118; `ClaudeTelemetryEnabled` field wired from profile at line 798-805 |
| 5  | ECS sandboxes with claudeTelemetry enabled have all Claude Code OTEL env vars in container environment | VERIFIED | `service_hcl.go` conditional block at line 153-168; `ClaudeTelemetryEnabled` populated at line 688-694 |
| 6  | OTEL_RESOURCE_ATTRIBUTES includes sandbox_id, profile_name, and substrate for per-sandbox filtering | VERIFIED | EC2: template line 118 uses `SandboxID`, `ProfileName`, `Substrate` fields; ECS: pre-formatted in `OTELResourceAttributes` field (line 694) |
| 7  | Localhost OTEL endpoints are not blocked by iptables (EC2) or HTTP proxy (ECS) | VERIFIED | EC2: iptables DNAT only redirects 53/80/443 (no 4317/4318 rules); ECS: `NO_PROXY` includes `localhost,127.0.0.1` |
| 8  | EC2 user-data downloads otelcol-contrib binary from S3 and makes it executable | VERIFIED | `userdata.go` line 252-254: `aws s3 cp .../otelcol-contrib` + explicit `chmod +x` |
| 9  | km-tracing.service systemd unit starts otelcol-contrib and is included in systemctl enable/start | VERIFIED | `userdata.go` lines 328-347: unit with correct env vars and ExecStart; lines 446-447: enable/start lines include `km-tracing` |

**Score:** 9/9 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `sidecars/tracing/config.yaml` | OTel Collector config with traces + logs + metrics pipelines | VERIFIED | Three named S3 exporters (`awss3/traces`, `awss3/logs`, `awss3/metrics`), three pipelines, all routing to OTLP receiver |
| `pkg/profile/types.go` | `ClaudeTelemetrySpec` struct on `ObservabilitySpec` | VERIFIED | Struct at line 218, `IsEnabled()` helper at line 225, field on `ObservabilitySpec` at line 236 |
| `pkg/profile/schemas/sandbox_profile.schema.json` | JSON schema for `claudeTelemetry` | VERIFIED | `claudeTelemetry` property at line 339 confirmed present |
| `pkg/compiler/userdata.go` | Claude Code OTEL env var injection for EC2 | VERIFIED | Section 2.9 template at line 103, params struct at line 700-704, population at line 797-805 |
| `pkg/compiler/service_hcl.go` | Claude Code OTEL env var injection for ECS | VERIFIED | Template block at line 153, params fields at line 408-411, population at line 687-694 |
| `pkg/compiler/userdata_test.go` | Test coverage for OTEL env var injection on EC2 | VERIFIED | 7 OTEL env var tests + 3 otelcol-contrib download tests + 7 km-tracing unit tests = 17 passing tests |
| `pkg/compiler/service_hcl_test.go` | Test coverage for OTEL env var injection on ECS | VERIFIED | 5 ECS OTEL tests all passing including `TestECSNOProxyIncludesLocalhost` |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `pkg/profile/types.go` | `pkg/profile/schemas/sandbox_profile.schema.json` | Go struct mirrors JSON schema for `claudeTelemetry` | VERIFIED | Both define `enabled`, `logPrompts`, `logToolDetails`; schema found at line 339 |
| `profiles/*.yaml` | `pkg/profile/types.go` | YAML unmarshals into Go struct via `claudeTelemetry` field | VERIFIED | All 5 profile YAMLs have `claudeTelemetry:` key matching `*ClaudeTelemetrySpec yaml:"claudeTelemetry,omitempty"` tag |
| `pkg/profile/types.go` | `pkg/compiler/userdata.go` | `ClaudeTelemetrySpec` read during user-data generation | VERIFIED | `p.Spec.Observability.ClaudeTelemetry` accessed at line 798, `IsEnabled()` called at line 799 |
| `pkg/profile/types.go` | `pkg/compiler/service_hcl.go` | `ClaudeTelemetrySpec` read during ECS HCL generation | VERIFIED | `p.Spec.Observability.ClaudeTelemetry` accessed at line 688, `IsEnabled()` called at line 689 |
| `pkg/compiler/userdata.go` | `sidecars/tracing/config.yaml` | `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317` points to collector | VERIFIED | Template line 109 sets endpoint; config.yaml listens on `0.0.0.0:4317` |
| `pkg/compiler/userdata.go` | S3 artifacts bucket | `aws s3 cp` for `otelcol-contrib` binary at startup | VERIFIED | Line 252: `aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/otelcol-contrib" /opt/km/bin/otelcol-contrib` |

### Requirements Coverage

OTEL-01 through OTEL-07 are defined in ROADMAP.md (phase-specific requirements) and are not registered in REQUIREMENTS.md (which tracks global v1 requirements). All seven are covered by the phase plans and verified below.

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| OTEL-01 | 27-02, 27-03 | Claude Code OTEL env vars injected into EC2 user-data and ECS container env | SATISFIED | `CLAUDE_CODE_ENABLE_TELEMETRY`, `OTEL_METRICS_EXPORTER`, `OTEL_LOGS_EXPORTER`, `OTEL_EXPORTER_OTLP_PROTOCOL`, `OTEL_EXPORTER_OTLP_ENDPOINT` present in both substrates; 12 passing tests |
| OTEL-02 | 27-01 | OTel Collector config extended with logs and metrics pipelines | SATISFIED | `sidecars/tracing/config.yaml` has `logs` and `metrics` pipelines alongside existing `traces` pipeline; all export to S3 |
| OTEL-03 | 27-02, 27-03 | Claude Code events flow through collector to S3 in OTLP JSON format | SATISFIED (config-level) | Collector config uses `marshaler: otlp_json`, pipelines handle all signal types; actual event flow requires live sandbox (see human verification) |
| OTEL-04 | 27-02, 27-03 | Claude Code metrics flow through collector to S3 | SATISFIED (config-level) | `metrics` pipeline active in collector config; `OTEL_METRICS_EXPORTER=otlp` injected; actual metric flow requires live sandbox |
| OTEL-05 | 27-01 | Profile schema supports operator control over telemetry via `spec.observability.claudeTelemetry` | SATISFIED | `ClaudeTelemetrySpec` with `enabled`, `logPrompts`, `logToolDetails`; JSON schema validates; all built-in profiles have defaults |
| OTEL-06 | 27-02 | `OTEL_RESOURCE_ATTRIBUTES` includes sandbox_id, profile_name, substrate | SATISFIED | EC2: template renders `sandbox_id={{ .SandboxID }},profile_name={{ .ProfileName }},substrate={{ .Substrate }}`; ECS: pre-formatted string with `substrate=ecs`; 2 passing tests |
| OTEL-07 | 27-02 | Collector endpoint accessible without proxy blocking | SATISFIED | EC2: iptables DNAT has no rule for 4317/4318 (`TestUserDataIPTablesNoDNATForOTLP` passes); ECS: `NO_PROXY` includes `localhost,127.0.0.1` (`TestECSNOProxyIncludesLocalhost` passes) |

Note: OTEL-01 through OTEL-07 are phase-scoped requirements defined in ROADMAP.md. They do not appear in REQUIREMENTS.md, which tracks global v1 requirements using OBSV-/PROV-/NETW- prefixes. This is expected — Phase 27 did not claim any global REQUIREMENTS.md IDs. No orphaned global requirements found.

### Anti-Patterns Found

No anti-patterns detected. Scanned `pkg/compiler/userdata.go`, `pkg/compiler/service_hcl.go`, `pkg/profile/types.go`, and all profile YAML files. No TODO/FIXME/placeholder comments, no stub implementations, no empty handlers.

### Human Verification Required

#### 1. EC2 end-to-end pipeline verification

**Test:** Provision an EC2 sandbox using `km create profiles/claude-dev.yaml`. SSH in and run `systemctl status km-tracing`. Then run `claude-code` briefly and check S3 for telemetry output.
**Expected:** `km-tracing.service` is `active (running)`. `otelcol-contrib` is listening on ports 4317 and 4318 (`ss -tlnp | grep 4317`). After running Claude Code, S3 bucket shows objects under `logs/<sandbox-id>/` and `metrics/<sandbox-id>/` prefixes alongside the existing `traces/<sandbox-id>/` prefix.
**Why human:** The `aws s3 cp .../sidecars/otelcol-contrib` download in user-data requires the binary to have been pre-uploaded to the artifacts S3 bucket. This upload is a one-time operational step not automated by this phase. If the binary is missing from S3, `km-tracing.service` will fail at boot silently. Cannot verify binary existence or successful startup without live AWS access.

#### 2. ECS end-to-end pipeline verification

**Test:** Provision an ECS sandbox using `km create profiles/claude-dev.yaml` (with `runtime.substrate: ecs`). Run Claude Code briefly and check S3 for telemetry output.
**Expected:** ECS task definition contains the OTEL env vars in the main container environment. Claude Code emits to `http://localhost:4317`. S3 shows OTLP JSON objects under `logs/<sandbox-id>/` and `metrics/<sandbox-id>/` prefixes.
**Why human:** ECS does not use user-data or systemd. The OTel Collector runs as a sidecar container. Cannot verify the Fargate task network allows localhost communication between the main container and the tracing sidecar container without deploying. ECS container-to-container `localhost` resolution depends on the task networking mode (`awsvpc` with `localhost` sharing), which must be confirmed against the actual task definition template.

#### 3. otelcol-contrib binary presence in S3

**Test:** Run `aws s3 ls s3://<account>-km-artifacts/sidecars/otelcol-contrib`
**Expected:** File exists and is a valid otelcol-contrib binary (non-zero size, executable)
**Why human:** This is a one-time operational prerequisite. The phase adds the download line to user-data but does not provision the binary. Without this file, all EC2 OTEL telemetry silently fails.

### Gaps Summary

No functional gaps found in the codebase implementation. All nine observable truths are fully verified at the code level. All OTEL-01 through OTEL-07 requirements are satisfied by the implementation.

The three human verification items are operational prerequisites and live-runtime behaviors that cannot be confirmed programmatically. They do not represent code gaps — the code is correct. They represent:
1. A one-time binary upload that must precede first use (otelcol-contrib to S3)
2. Live-runtime behavior that requires an actual sandbox deployment to confirm end-to-end

---

_Verified: 2026-03-28T22:28:11Z_
_Verifier: Claude (gsd-verifier)_
