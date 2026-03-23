---
phase: 11
slug: sandbox-auto-destroy-metadata-wiring
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-23
---

# Phase 11 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — existing infrastructure |
| **Quick run command** | `go test ./internal/app/cmd/... ./cmd/ttl-handler/... ./sidecars/audit-log/... ./pkg/aws/... -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~10 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick command
- **After every plan wave:** Run full suite
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 10 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 11-01-01 | 01 | 1 | PROV-03,PROV-04 | unit | `go test ./internal/app/cmd/... -run TestListCmd\|TestStatusCmd -count=1` | partial | ⬜ pending |
| 11-02-01 | 02 | 1 | PROV-05 | unit | `go test ./cmd/ttl-handler/... -run TestHandleTTLEvent\|TestDestroySandboxResources -count=1` | ❌ W0 | ⬜ pending |
| 11-02-02 | 02 | 1 | PROV-06 | unit | `go test ./pkg/aws/... -run TestPublishSandboxIdleEvent -count=1` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] TTL handler teardown test stubs (TeardownFunc called, nil guard, error return)
- [ ] DestroySandboxResources test stubs (tag discovery, EC2 terminate)
- [ ] Idle detector EventBridge test stubs (PublishSandboxIdleEvent)

*Existing test infrastructure covers list/status tests.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| TTL Lambda destroys real AWS resources | PROV-05 | Requires live AWS environment | Deploy TTL handler, create sandbox with short TTL, verify resources destroyed after expiry |
| Idle timeout destroys real sandbox | PROV-06 | Requires running EC2 instance | Create sandbox, wait for idle timeout, verify instance stopped/terminated |
| EventBridge rule routes SandboxIdle to Lambda | PROV-06 | Requires deployed infra | Publish test SandboxIdle event, verify Lambda invoked in CloudWatch logs |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 10s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
