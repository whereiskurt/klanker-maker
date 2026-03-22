---
phase: 09
slug: live-infrastructure-operator-docs
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-22
---

# Phase 09 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | terragrunt validate + go test |
| **Config file** | infra/live/site.hcl |
| **Quick run command** | `go test ./... -count=1 -short` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~35 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick command
- **After every plan wave:** Run full suite
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 35 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 09-01-01 | 01 | 1 | PROV-05, BUDG-02, MAIL-01 | file check | `test -f infra/live/use1/ttl-handler/terragrunt.hcl && test -f infra/live/use1/dynamodb-budget/terragrunt.hcl && test -f infra/live/use1/ses/terragrunt.hcl` | ❌ (created) | ⬜ pending |
| 09-01-02 | 01 | 1 | BUDG-06, BUDG-07 | file + build | `make build-lambdas && ls build/ttl-handler.zip build/budget-enforcer.zip` | ❌ (created) | ⬜ pending |
| 09-02-01 | 02 | 1 | INFR-01, INFR-02 | file check | `test -f OPERATOR-GUIDE.md && grep -c "km configure" OPERATOR-GUIDE.md` | ❌ (created) | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

*Existing infrastructure covers all phase requirements. Plans create new files.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| TTL Lambda fires on schedule and destroys sandbox | PROV-05 | Requires live AWS + deployed Lambda | Create sandbox with TTL, wait for expiry, verify destroyed |
| DynamoDB budget table accepts writes | BUDG-02 | Requires live AWS | `km create` with budget profile, check DynamoDB for record |
| SES sends lifecycle email | MAIL-01 | Requires live AWS + verified domain | Create sandbox, check operator inbox for notification |
| Budget enforcer suspends sandbox at limit | BUDG-06, BUDG-07 | Requires live AWS + running sandbox | Set low budget, wait for enforcement |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 35s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
