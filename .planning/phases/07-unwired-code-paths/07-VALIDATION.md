---
phase: 07
slug: unwired-code-paths
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-22
---

# Phase 07 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — standard Go test tooling |
| **Quick run command** | `go test ./... -count=1 -short` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~35 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./... -count=1 -short`
- **After every plan wave:** Run `go test ./... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 35 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 07-01-01 | 01 | 1 | OBSV-07 | unit | `go test ./sidecars/audit-log/... -v -count=1` | ✅ | ⬜ pending |
| 07-01-02 | 01 | 1 | PROV-06 | unit | `go test ./sidecars/audit-log/... -v -count=1` | ✅ | ⬜ pending |
| 07-02-01 | 02 | 1 | OBSV-09 | unit | `go test ./internal/app/cmd/... -run TestMLflow -v -count=1` | ✅ (planned in task) | ⬜ pending |
| 07-02-02 | 02 | 1 | CONF-03 | integration | `grep -c 'get_env.*KM_ACCOUNTS' infra/live/site.hcl` | ✅ | ⬜ pending |
| 07-02-03 | 02 | 1 | SCHM-04 | existing | `go test ./pkg/profile/... -run TestResolve -v -count=1` | ✅ | ⬜ pending |
| 07-02-04 | 02 | 1 | SCHM-05 | existing | `go test ./pkg/profile/... -run TestBuiltin -v -count=1` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

*Existing infrastructure covers all phase requirements. All plans use TDD inline.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| IdleDetector polling in live sandbox | PROV-06 | Requires running EC2/ECS sandbox | Deploy sandbox, wait for idle timeout, verify teardown fires |
| Secret redaction in CloudWatch logs | OBSV-07 | Requires live CloudWatch log group | Run sandbox with secrets, check logs for redacted values |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 35s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
