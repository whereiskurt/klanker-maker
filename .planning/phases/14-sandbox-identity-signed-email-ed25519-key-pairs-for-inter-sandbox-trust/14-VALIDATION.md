---
phase: 14
slug: sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust
status: draft
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-22
---

# Phase 14 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — standard Go test infrastructure |
| **Quick run command** | `go test ./pkg/aws/... ./pkg/profile/... ./internal/app/cmd/... ./internal/app/config/...` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/aws/... ./pkg/profile/... ./internal/app/cmd/... ./internal/app/config/...`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | Status |
|---------|------|------|-------------|-----------|-------------------|--------|
| 14-01-01 | 01 | 1 | EmailSpec schema + config + built-in profiles | unit (TDD) | `go test ./pkg/profile/... -v -count=1 && go test ./internal/app/config/... -v -count=1` | pending |
| 14-01-02 | 01 | 1 | DynamoDB identities Terraform module | terraform validate | `cd infra/modules/dynamodb-identities/v1.0.0 && terraform init -backend=false && terraform validate` | pending |
| 14-02-01 | 02 (TDD) | 2 | Identity library: keygen, SSM, DynamoDB, sign/verify, encrypt, send, cleanup | unit (TDD) | `go test ./pkg/aws/... -run TestIdentity -v -count=1` | pending |
| 14-03-01 | 03 | 3 | Wire identity into km create + km destroy | unit (TDD) | `go test ./internal/app/cmd/... -run "TestCreate\|TestDestroy" -v -count=1` | pending |
| 14-03-02 | 03 | 3 | IdentityFetcher in km status | unit (TDD) | `go test ./internal/app/cmd/... -run TestStatus -v -count=1` | pending |

*Status: pending / green / red / flaky*

**Notes:**
- Plan 14-02 is type `tdd` — test files are created inline as part of the RED-GREEN-REFACTOR cycle; no separate Wave 0 test scaffold needed.
- Plans 14-01 and 14-03 have `tdd="true"` on tasks, meaning tests are written before implementation within each task.
- All tasks have inline `<automated>` verify blocks. No MISSING Wave 0 dependencies.

---

## Wave 0 Requirements

None. All plans create test files inline (TDD pattern). No separate test scaffolding step required.

- Plan 14-01 Task 1: TDD — creates tests in pkg/profile and internal/app/config test suites
- Plan 14-02: Full TDD plan — creates pkg/aws/identity_test.go as part of RED phase
- Plan 14-03: TDD tasks — creates tests in internal/app/cmd test suites

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| SES raw MIME with custom headers | Signed email delivery | Requires real SES domain + verified identity | Send signed email between two sandboxes, verify X-KM-Signature header present |
| NaCl encrypted email decryption | End-to-end encryption | Requires two running sandboxes with key pairs | Send encrypted email from sandbox A to B, verify B can decrypt and A's signature verifies |
| DynamoDB public key lookup cross-sandbox | Identity discovery | Requires real DynamoDB table | Publish key from sandbox A, fetch from sandbox B, verify match |

*Crypto operations are testable in-process; delivery and cross-sandbox flows require real AWS.*

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify commands
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references (none — all inline TDD)
- [x] No watch-mode flags
- [x] Feedback latency < 30s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
