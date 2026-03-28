---
phase: 27
slug: claude-code-otel-integration-sandbox-observability-via-built-in-telemetry
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-28
---

# Phase 27 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib) |
| **Config file** | none — `go test ./...` |
| **Quick run command** | `go test ./pkg/profile/... ./pkg/compiler/...` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/profile/... ./pkg/compiler/...`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 27-01-01 | 01 | 1 | OTEL-05 | unit | `go test ./pkg/profile/... -run TestClaudeTelemetrySchema` | ❌ W0 | ⬜ pending |
| 27-01-01 | 01 | 1 | OTEL-05 | unit | `go test ./pkg/profile/... -run TestClaudeTelemetrySchemaRejectsUnknown` | ❌ W0 | ⬜ pending |
| 27-01-02 | 01 | 1 | OTEL-02 | unit | `python3 -c "import yaml; yaml.safe_load(open('sidecars/tracing/config.yaml'))"` | ✅ | ⬜ pending |
| 27-01-02 | 01 | 1 | OTEL-05 | integration | `go test ./pkg/profile/... -run TestBuiltinProfiles` | ✅ | ⬜ pending |
| 27-02-01 | 02 | 2 | OTEL-01 | unit | `go test ./pkg/compiler/... -run TestClaudeTelemetryEC2` | ❌ W0 | ⬜ pending |
| 27-02-01 | 02 | 2 | OTEL-01 | unit | `go test ./pkg/compiler/... -run TestClaudeTelemetryEC2Absent` | ❌ W0 | ⬜ pending |
| 27-02-01 | 02 | 2 | OTEL-06 | unit | `go test ./pkg/compiler/... -run TestOTELResourceAttributes` | ❌ W0 | ⬜ pending |
| 27-02-02 | 02 | 2 | OTEL-01 | unit | `go test ./pkg/compiler/... -run TestClaudeTelemetryECS` | ❌ W0 | ⬜ pending |
| 27-02-02 | 02 | 2 | OTEL-07 | unit | `go test ./pkg/compiler/... -run TestECSNoProxyIncludesLocalhost` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/profile/claude_telemetry_test.go` — stubs for OTEL-05 schema accept/reject
- [ ] `pkg/compiler/claude_otel_test.go` — stubs for OTEL-01 (EC2 + ECS), OTEL-06 resource attributes, OTEL-07 NO_PROXY

*Existing `builtins_test.go` will catch built-in profile breakage automatically once the schema and YAML are updated.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| OTEL data appears in S3 bucket | OTEL-03, OTEL-04 | Requires live sandbox with Claude Code running | 1. `km create profiles/claude-dev.yaml` 2. Run Claude Code in sandbox 3. Check S3 bucket for logs/ and metrics/ prefixes |
| Collector starts on EC2 | OTEL-02 | Requires live EC2 instance | 1. SSH into sandbox 2. `systemctl status km-tracing` 3. Verify otelcol-contrib is running |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
