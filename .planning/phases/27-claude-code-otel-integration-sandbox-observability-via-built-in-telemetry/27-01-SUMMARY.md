---
phase: 27-claude-code-otel-integration-sandbox-observability-via-built-in-telemetry
plan: "01"
subsystem: profile-schema, otel-collector
tags: [otel, telemetry, profile-schema, collector, observability]
dependency_graph:
  requires: []
  provides:
    - ClaudeTelemetrySpec struct in profile types
    - claudeTelemetry JSON schema definition
    - OTel Collector config with traces/logs/metrics pipelines
    - claudeTelemetry defaults in all built-in profiles
  affects:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - sidecars/tracing/config.yaml
    - all built-in profile YAMLs
tech_stack:
  added: []
  patterns:
    - pointer-bool for tri-state (nil=default, true, false) on ClaudeTelemetrySpec.Enabled
    - awss3 named exporter pattern (awss3/traces, awss3/logs, awss3/metrics) for multi-pipeline OTel config
key_files:
  created: []
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - sidecars/tracing/config.yaml
    - profiles/open-dev.yaml
    - profiles/claude-dev.yaml
    - profiles/restricted-dev.yaml
    - profiles/hardened.yaml
    - profiles/sealed.yaml
    - pkg/profile/builtins/open-dev.yaml
    - pkg/profile/builtins/restricted-dev.yaml
    - pkg/profile/builtins/hardened.yaml
    - pkg/profile/builtins/sealed.yaml
decisions:
  - "*bool for Enabled field: enables nil-means-default semantic (true), allowing profiles to opt-out explicitly with false without YAML omitempty ambiguity"
  - "Named OTel exporters (awss3/traces, awss3/logs, awss3/metrics): required because otelcol-contrib does not allow duplicate exporter type keys; named instance syntax enables separate S3 prefixes per signal type"
  - "claudeTelemetry is optional (no required field) so existing profiles without it continue to validate"
metrics:
  duration: 130s
  completed_date: "2026-03-28"
  tasks_completed: 2
  files_modified: 12
---

# Phase 27 Plan 01: Claude Telemetry Schema and OTel Collector Multi-Pipeline Summary

**One-liner:** ClaudeTelemetrySpec struct + JSON schema property added to ObservabilitySpec; OTel Collector extended to three named S3 exporters (traces/logs/metrics); all built-in profiles updated with security-appropriate claudeTelemetry defaults.

## What Was Built

### Task 1: ClaudeTelemetrySpec in profile types and schema

Added `ClaudeTelemetrySpec` struct to `pkg/profile/types.go`:
- `Enabled *bool` — master on/off switch using pointer-bool so nil defaults to true
- `LogPrompts bool` — controls OTEL_LOG_USER_PROMPTS
- `LogToolDetails bool` — controls OTEL_LOG_TOOL_DETAILS
- `IsEnabled()` helper method returns true when pointer is nil

Updated `ObservabilitySpec` to include `ClaudeTelemetry *ClaudeTelemetrySpec` as optional field.

Updated `pkg/profile/schemas/sandbox_profile.schema.json` to add `claudeTelemetry` as an optional object in the `observability` property with `enabled`, `logPrompts`, and `logToolDetails` boolean fields.

### Task 2: Collector config and built-in profiles

Updated `sidecars/tracing/config.yaml`:
- Renamed single `awss3` exporter to `awss3/traces`
- Added `awss3/logs` exporter with `logs/${SANDBOX_ID}` S3 prefix
- Added `awss3/metrics` exporter with `metrics/${SANDBOX_ID}` S3 prefix
- Added `logs` and `metrics` service pipelines, each receiving from `otlp` and exporting to their respective S3 exporter

Updated all 5 `profiles/` YAML files and 4 `pkg/profile/builtins/` YAML files with `claudeTelemetry` under `observability`:

| Profile | logPrompts | logToolDetails | Security posture |
|---------|-----------|----------------|-----------------|
| open-dev | true | true | Development — full visibility |
| claude-dev | true | true | Development — full visibility |
| restricted-dev | false | true | Development — tool details only |
| hardened | false | false | Production — metrics only |
| sealed | false | false | Production — metrics only |

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check

Files exist:
- pkg/profile/types.go: FOUND
- pkg/profile/schemas/sandbox_profile.schema.json: FOUND
- sidecars/tracing/config.yaml: FOUND

Commits exist:
- 07109e0: feat(27-01): add ClaudeTelemetrySpec to profile schema and types
- 77d43a8: feat(27-01): update collector config and add claudeTelemetry to all built-in profiles
