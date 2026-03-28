---
phase: 27-claude-code-otel-integration-sandbox-observability-via-built-in-telemetry
plan: "02"
subsystem: compiler, otel-injection
tags: [otel, telemetry, compiler, ec2, ecs, user-data, container-env]
dependency_graph:
  requires:
    - 27-01 (ClaudeTelemetrySpec in profile types)
  provides:
    - EC2 user-data section 2.9 with conditional OTEL env var injection
    - ECS main container environment with conditional OTEL env var injection
    - OTEL_RESOURCE_ATTRIBUTES with sandbox_id/profile_name/substrate in both substrates
  affects:
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go
    - pkg/compiler/service_hcl.go
    - pkg/compiler/service_hcl_test.go
tech_stack:
  added: []
  patterns:
    - TDD RED/GREEN cycle for template-driven code generation tests
    - Conditional template blocks controlled by userDataParams/ecsHCLParams bool fields
    - IsEnabled() nil-default pattern for ClaudeTelemetrySpec (nil=enabled)
key_files:
  created: []
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go
    - pkg/compiler/service_hcl.go
    - pkg/compiler/service_hcl_test.go
decisions:
  - "EC2 OTEL section uses cat >> /etc/profile.d/km-profile-env.sh with >> so it appends regardless of whether ProfileEnv section 2.8 ran (>> creates file if absent)"
  - "OTEL_RESOURCE_ATTRIBUTES uses non-quoted EOF heredoc on EC2 so SandboxID/ProfileName/Substrate are interpolated by Go template; static vars use 'CLAUDE_OTEL' quoted heredoc"
  - "ECS OTELResourceAttributes is a pre-formatted Go string field rather than computed in template, matching the existing pattern for ecsHCLParams"
  - "OTEL-07 EC2 confirmed by test asserting no REDIRECT rules target ports 4317/4318; OTEL-07 ECS confirmed by test asserting localhost in NO_PROXY"
metrics:
  duration: 202s
  completed_date: "2026-03-28"
  tasks_completed: 2
  files_modified: 4
---

# Phase 27 Plan 02: Claude Code OTEL Env Var Injection Summary

**One-liner:** Claude Code OTEL env vars (CLAUDE_CODE_ENABLE_TELEMETRY, OTLP exporters, OTEL_RESOURCE_ATTRIBUTES) injected into EC2 user-data and ECS container definitions driven by profile ClaudeTelemetrySpec; 12 new tests covering all 7 EC2 and 5 ECS behaviors.

## What Was Built

### Task 1: EC2 user-data OTEL injection (TDD)

Added section 2.9 "Claude Code OpenTelemetry environment" to `userDataTemplate` in `userdata.go`. The section is guarded by `{{- if .ClaudeTelemetryEnabled }}` and appends to `/etc/profile.d/km-profile-env.sh`.

New `userDataParams` fields:
- `ClaudeTelemetryEnabled bool` — master on/off, populated via `ct.IsEnabled()` (nil defaults to true)
- `ClaudeTelemetryLogPrompts bool` — controls `OTEL_LOG_USER_PROMPTS=1`
- `ClaudeTelemetryLogToolDetails bool` — controls `OTEL_LOG_TOOL_DETAILS=1`
- `ProfileName string` — from `p.Metadata.Name`
- `Substrate string` — from `p.Spec.Runtime.Substrate`

Template section writes:
- `CLAUDE_CODE_ENABLE_TELEMETRY=1`
- `OTEL_METRICS_EXPORTER=otlp`
- `OTEL_LOGS_EXPORTER=otlp`
- `OTEL_EXPORTER_OTLP_PROTOCOL=grpc`
- `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317`
- (conditionally) `OTEL_LOG_USER_PROMPTS=1`
- (conditionally) `OTEL_LOG_TOOL_DETAILS=1`
- `OTEL_RESOURCE_ATTRIBUTES="sandbox_id=X,profile_name=Y,substrate=ec2"`

7 tests added in `userdata_test.go`:
1. Default nil claudeTelemetry → all 5 base OTEL vars present
2. logPrompts=true → OTEL_LOG_USER_PROMPTS=1 present
3. logPrompts=false → OTEL_LOG_USER_PROMPTS absent
4. logToolDetails=true → OTEL_LOG_TOOL_DETAILS=1 present
5. enabled=false → NO OTEL vars present
6. OTEL_RESOURCE_ATTRIBUTES includes sandbox_id, profile_name, substrate=ec2
7. iptables DNAT rules: no REDIRECT rule targets ports 4317 or 4318 (OTEL-07)

### Task 2: ECS container environment OTEL injection (TDD)

Updated `ecsServiceHCLTemplate` to add a conditional OTEL env var block in the main container `environment` array, guarded by `{{- if .ClaudeTelemetryEnabled }}`. An OTEL-07 comment near the block notes that NO_PROXY already includes localhost so OTLP traffic bypasses the HTTP proxy.

New `ecsHCLParams` fields:
- `ClaudeTelemetryEnabled bool`
- `ClaudeTelemetryLogPrompts bool`
- `ClaudeTelemetryLogToolDetails bool`
- `OTELResourceAttributes string` — pre-formatted `"sandbox_id=X,profile_name=Y,substrate=ecs"`

`generateECSServiceHCL` populates all new fields from `p.Spec.Observability.ClaudeTelemetry`.

5 tests added in `service_hcl_test.go`:
1. Default nil claudeTelemetry → all 5 base OTEL vars in container env
2. logPrompts=true → OTEL_LOG_USER_PROMPTS in container env
3. enabled=false → NO OTEL vars in container env
4. OTEL_RESOURCE_ATTRIBUTES includes sandbox_id, profile_name, substrate=ecs
5. NO_PROXY already contains "localhost" (OTEL-07: OTLP bypasses HTTP proxy)

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check

Files exist:
- pkg/compiler/userdata.go: FOUND
- pkg/compiler/userdata_test.go: FOUND
- pkg/compiler/service_hcl.go: FOUND
- pkg/compiler/service_hcl_test.go: FOUND

Commits exist:
- fc04dc5: test(27-02): add failing OTEL env var tests for EC2 user-data
- 3624064: feat(27-02): inject Claude Code OTEL env vars in EC2 user-data
- 33f1900: test(27-02): add failing OTEL env var tests for ECS container definition
- ac73bda: feat(27-02): inject Claude Code OTEL env vars in ECS container definition

## Self-Check: PASSED
