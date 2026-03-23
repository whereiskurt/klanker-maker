---
phase: 17
slug: sandbox-email-mailbox-access-control-aliases-allow-lists-self-mail-s3-reader
status: draft
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-23
---

# Phase 17 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — standard Go test infrastructure |
| **Quick run command** | `go test ./pkg/aws/... ./pkg/profile/... ./internal/app/cmd/... -run "TestMailbox\|TestAlias\|TestAllowList\|TestIdentity"` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick command above
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

All plans use TDD. Tests are created before implementation within each task. No separate Wave 0 plan required.

---

## Wave 0 Requirements

None. All plans create test files inline (TDD pattern).

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| SES-stored email read from S3 | Mailbox reader | Requires real SES + S3 | Send email to sandbox, verify ListMailboxMessages returns it |
| DynamoDB GSI alias lookup | Alias resolution | Requires real DynamoDB with GSI | Publish identity with alias, query by alias, verify match |
| Allow-list rejection of unauthorized sender | Access control | Requires two sandboxes | Send from sandbox A to B where B's allow-list excludes A |

*Core logic is testable in-process with mocks; end-to-end flows require real AWS.*

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or TDD creates tests inline
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Nyquist compliance via TDD pattern
- [x] No watch-mode flags
- [x] Feedback latency < 30s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
