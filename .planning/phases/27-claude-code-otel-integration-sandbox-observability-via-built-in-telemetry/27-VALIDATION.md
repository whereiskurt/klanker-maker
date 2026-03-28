---
phase: 27
slug: claude-code-otel-integration-sandbox-observability-via-built-in-telemetry
status: draft
nyquist_compliant: true
wave_0_complete: true
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

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | Inline TDD | Status |
|---------|------|------|-------------|-----------|-------------------|------------|--------|
| 27-01-01 | 01 | 1 | OTEL-05 | unit | `go test ./pkg/profile/... -run TestClaudeTelemetrySchema` | yes, in `pkg/profile/*_test.go` | ⬜ pending |
| 27-01-01 | 01 | 1 | OTEL-05 | unit | `go test ./pkg/profile/... -run TestClaudeTelemetrySchemaRejectsUnknown` | yes, in `pkg/profile/*_test.go` | ⬜ pending |
| 27-01-02 | 01 | 1 | OTEL-02 | unit | `python3 -c "import yaml; yaml.safe_load(open('sidecars/tracing/config.yaml'))"` | n/a (config file) | ⬜ pending |
| 27-01-02 | 01 | 1 | OTEL-05 | integration | `go test ./pkg/profile/... -run TestBuiltinProfiles` | yes, existing test | ⬜ pending |
| 27-02-01 | 02 | 2 | OTEL-01 | unit | `go test ./pkg/compiler/... -run TestClaudeTelemetryEC2` | yes, in `pkg/compiler/userdata_test.go` | ⬜ pending |
| 27-02-01 | 02 | 2 | OTEL-01 | unit | `go test ./pkg/compiler/... -run TestClaudeTelemetryEC2Absent` | yes, in `pkg/compiler/userdata_test.go` | ⬜ pending |
| 27-02-01 | 02 | 2 | OTEL-06 | unit | `go test ./pkg/compiler/... -run TestOTELResourceAttributes` | yes, in `pkg/compiler/userdata_test.go` | ⬜ pending |
| 27-02-01 | 02 | 2 | OTEL-07 | unit | `go test ./pkg/compiler/... -run TestEC2IptablesNoOTELPorts` | yes, in `pkg/compiler/userdata_test.go` | ⬜ pending |
| 27-02-02 | 02 | 2 | OTEL-01 | unit | `go test ./pkg/compiler/... -run TestClaudeTelemetryECS` | yes, in `pkg/compiler/service_hcl_test.go` | ⬜ pending |
| 27-02-02 | 02 | 2 | OTEL-07 | unit | `go test ./pkg/compiler/... -run TestECSNoProxyIncludesLocalhost` | yes, in `pkg/compiler/service_hcl_test.go` | ⬜ pending |
| 27-03-01 | 03 | 3 | OTEL-01 | unit | `go test ./pkg/compiler/... -run TestOtelcolBinaryDownload` | yes, in `pkg/compiler/userdata_test.go` | ⬜ pending |
| 27-03-02 | 03 | 3 | OTEL-03 | unit | `go test ./pkg/compiler/... -run TestKmTracingSystemdUnit` | yes, in `pkg/compiler/userdata_test.go` | ⬜ pending |
| 27-03-02 | 03 | 3 | OTEL-04 | unit | `go test ./pkg/compiler/... -run TestKmTracingOtelS3Bucket` | yes, in `pkg/compiler/userdata_test.go` | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

All plans use inline TDD (`tdd="true"` on tasks). Tests are written inside the same test files that the tasks modify — no separate Wave 0 stub files needed.

- `pkg/profile/*_test.go` — schema accept/reject tests created inline by Plan 01 Task 1
- `pkg/compiler/userdata_test.go` — EC2 OTEL env var, iptables, binary download, and systemd unit tests created inline by Plans 02-03
- `pkg/compiler/service_hcl_test.go` — ECS OTEL env var and NO_PROXY tests created inline by Plan 02 Task 2

Existing `builtins_test.go` will catch built-in profile breakage automatically once the schema and YAML are updated.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| OTEL data appears in S3 bucket | OTEL-03, OTEL-04 | Requires live sandbox with Claude Code running | 1. `km create profiles/claude-dev.yaml` 2. Run Claude Code in sandbox 3. Check S3 bucket for logs/ and metrics/ prefixes |
| Collector starts on EC2 | OTEL-02 | Requires live EC2 instance | 1. SSH into sandbox 2. `systemctl status km-tracing` 3. Verify otelcol-contrib is running |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify commands
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covered by inline TDD (no separate stub files needed)
- [x] No watch-mode flags
- [x] Feedback latency < 15s
- [x] `nyquist_compliant: true` set in frontmatter
- [x] `wave_0_complete: true` set in frontmatter

**Approval:** pending
