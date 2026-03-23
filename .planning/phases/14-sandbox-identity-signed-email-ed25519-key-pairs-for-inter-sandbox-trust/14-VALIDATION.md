---
phase: 14
slug: sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust
status: draft
nyquist_compliant: false
wave_0_complete: false
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
| **Quick run command** | `go test ./pkg/aws/... ./pkg/compiler/... ./internal/app/cmd/...` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/aws/... ./pkg/compiler/... ./internal/app/cmd/...`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 14-01-01 | 01 | 1 | Ed25519 key generation + SSM storage | unit | `go test ./pkg/aws/... -run TestIdentity` | ❌ W0 | ⬜ pending |
| 14-01-02 | 01 | 1 | DynamoDB public key publish | unit | `go test ./pkg/aws/... -run TestIdentity` | ❌ W0 | ⬜ pending |
| 14-02-01 | 02 | 1 | Email signing (Ed25519 + raw MIME) | unit | `go test ./pkg/aws/... -run TestSign` | ❌ W0 | ⬜ pending |
| 14-02-02 | 02 | 1 | Email verification | unit | `go test ./pkg/aws/... -run TestVerify` | ❌ W0 | ⬜ pending |
| 14-03-01 | 03 | 2 | NaCl box encryption/decryption | unit | `go test ./pkg/aws/... -run TestEncrypt` | ❌ W0 | ⬜ pending |
| 14-04-01 | 04 | 2 | Profile schema + compiler integration | unit | `go test ./pkg/compiler/... -run TestEmail` | ❌ W0 | ⬜ pending |
| 14-04-02 | 04 | 2 | km status identity display | unit | `go test ./internal/app/cmd/... -run TestStatus` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Identity package stubs in `pkg/aws/identity.go`
- [ ] DynamoDB `km-identities` table in Terraform module

*Test files created by tasks themselves (TDD pattern).*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| SES raw MIME with custom headers | Signed email delivery | Requires real SES domain + verified identity | Send signed email between two sandboxes, verify X-KM-Signature header present |
| NaCl encrypted email decryption | End-to-end encryption | Requires two running sandboxes with key pairs | Send encrypted email from sandbox A to B, verify B can decrypt and A's signature verifies |
| DynamoDB public key lookup cross-sandbox | Identity discovery | Requires real DynamoDB global table | Publish key from sandbox A, fetch from sandbox B, verify match |

*Crypto operations are testable in-process; delivery and cross-sandbox flows require real AWS.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
