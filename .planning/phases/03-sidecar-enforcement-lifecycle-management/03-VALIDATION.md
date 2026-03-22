---
phase: 3
slug: sidecar-enforcement-lifecycle-management
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-21
---

# Phase 3 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing stdlib (no external test framework) |
| **Config file** | none — `go test ./...` from repo root |
| **Quick run command** | `go test ./sidecars/... ./pkg/compiler/... ./pkg/aws/... ./pkg/lifecycle/... ./internal/app/cmd/... -timeout 30s` |
| **Full suite command** | `go test ./... -timeout 120s` |
| **Estimated runtime** | ~30s quick / ~120s full |

---

## Sampling Rate

- **After every task commit:** Run `go test ./sidecars/... ./pkg/compiler/... ./pkg/aws/... ./pkg/lifecycle/... ./internal/app/cmd/... -timeout 30s`
- **After every plan wave:** Run `go test ./... -timeout 120s`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 3-W0-01 | Wave0 | 0 | NETW-02, OBSV-02 | unit | `go test ./sidecars/dns-proxy/... -run TestDNSProxy` | ❌ W0 | ⬜ pending |
| 3-W0-02 | Wave0 | 0 | NETW-03, OBSV-02, OBSV-10 | unit | `go test ./sidecars/http-proxy/... -run TestHTTPProxy` | ❌ W0 | ⬜ pending |
| 3-W0-03 | Wave0 | 0 | OBSV-01, OBSV-03 | unit | `go test ./sidecars/audit-log/... -run TestAuditLogFormat` | ❌ W0 | ⬜ pending |
| 3-W0-04 | Wave0 | 0 | PROV-05 | unit | `go test ./pkg/aws/... -run TestCreateTTLSchedule` | ❌ W0 | ⬜ pending |
| 3-W0-05 | Wave0 | 0 | PROV-06, PROV-07 | unit | `go test ./pkg/lifecycle/... -run TestIdleDetector` | ❌ W0 | ⬜ pending |
| 3-W0-06 | Wave0 | 0 | OBSV-09 | unit | `go test ./pkg/aws/... -run TestMLflowRun` | ❌ W0 | ⬜ pending |
| 3-W0-07 | Wave0 | 0 | PROV-03 | unit | `go test ./internal/app/cmd/... -run TestListCmd` | ❌ W0 | ⬜ pending |
| 3-W0-08 | Wave0 | 0 | PROV-04 | unit | `go test ./internal/app/cmd/... -run TestStatusCmd` | ❌ W0 | ⬜ pending |
| 3-01-01 | 01 | 1 | NETW-02 | unit | `go test ./sidecars/dns-proxy/... -run TestDNSProxy` | ❌ W0 | ⬜ pending |
| 3-01-02 | 01 | 1 | NETW-03, OBSV-10 | unit | `go test ./sidecars/http-proxy/... -run TestHTTPProxy` | ❌ W0 | ⬜ pending |
| 3-02-01 | 02 | 1 | OBSV-01, OBSV-02, OBSV-03 | unit | `go test ./sidecars/audit-log/... -run TestAuditLogFormat` | ❌ W0 | ⬜ pending |
| 3-03-01 | 03 | 1 | OBSV-08 | unit | `go test ./pkg/compiler/... -run TestOTelConfig` | ❌ W0 | ⬜ pending |
| 3-03-02 | 03 | 1 | OBSV-09 | unit | `go test ./pkg/aws/... -run TestMLflowRun` | ❌ W0 | ⬜ pending |
| 3-04-01 | 04 | 2 | PROV-05, PROV-06, PROV-07 | unit | `go test ./pkg/lifecycle/... ./pkg/aws/... -run "TestCreateTTLSchedule|TestIdleDetector|TestTeardownPolicy"` | ❌ W0 | ⬜ pending |
| 3-05-01 | 05 | 2 | PROV-03, PROV-04 | unit | `go test ./internal/app/cmd/... -run "TestListCmd|TestStatusCmd"` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `sidecars/dns-proxy/dns_proxy_test.go` — stubs for NETW-02, OBSV-02
- [ ] `sidecars/http-proxy/http_proxy_test.go` — stubs for NETW-03, OBSV-02, OBSV-10
- [ ] `sidecars/audit-log/audit_log_test.go` — stubs for OBSV-01, OBSV-03
- [ ] `pkg/aws/scheduler_test.go` — stubs for PROV-05 (mock scheduler client interface)
- [ ] `pkg/lifecycle/idle_test.go` — stubs for PROV-06, PROV-07
- [ ] `pkg/aws/mlflow_test.go` — stubs for OBSV-09 (mock S3 client)
- [ ] `internal/app/cmd/list_test.go` — stubs for PROV-03
- [ ] `internal/app/cmd/status_test.go` — stubs for PROV-04

Pattern: all new pkg/aws tests use mock interfaces (same pattern as `discover_test.go`). All sidecar tests start an in-process server on a random port.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| EC2 iptables DNAT redirect — traffic intercept on real EC2 instance | NETW-02, NETW-03 | Requires real EC2; iptables cannot be unit tested | Provision EC2 sandbox, run `curl http://blocked-domain.com` from inside, verify proxy error response |
| ECS Fargate sidecar DNS override via resolv.conf rewrite | NETW-02 | Requires real Fargate task execution; no iptables available | Deploy ECS task, exec into container, check `/etc/resolv.conf`, attempt blocked DNS lookup |
| CloudWatch log delivery end-to-end | OBSV-03 | Requires real CloudWatch; AWS SDK integration | Provision sandbox with CloudWatch dest, run commands, verify log group/stream in CloudWatch console |
| MLflow experiment visible in MLflow UI | OBSV-09 | Requires real S3 + MLflow server pointed at same bucket | After sandbox run, point MLflow at the S3 artifact bucket, verify experiment/run appears |
| OTel traces appear in collector endpoint | OBSV-08 | Requires running OTel collector | Start sandbox with trace_collector_endpoint set, run workload, verify spans in collector |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
