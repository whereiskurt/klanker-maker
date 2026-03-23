---
phase: 15
slug: km-doctor-platform-health-check-and-bootstrap-verification
status: draft
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-23
---

# Phase 15 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — standard Go test infrastructure |
| **Quick run command** | `go test ./internal/app/cmd/... -run "TestDoctor\|TestConfigureGitHub"` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/... -run "TestDoctor\|TestConfigureGitHub"`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

All plans use TDD (`tdd="true"` on tasks). Tests are created before implementation within each task's RED-GREEN-REFACTOR cycle. No separate Wave 0 plan required.

---

## Wave 0 Requirements

None. All plans create test files inline (TDD pattern).

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Real AWS credential validation | STS GetCallerIdentity | Requires live AWS SSO session | Run `km doctor` with valid SSO profiles, verify identity output |
| GitHub App manifest browser flow | Manifest exchange | Requires browser + GitHub account | Run `km configure github --setup`, authorize in browser, verify App created |
| SCP attachment check | Organizations API | Requires management account access | Run `km doctor` with management credentials, verify SCP status |

*All checks are DI-injectable — unit tests verify logic with mocked AWS clients.*

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or TDD creates tests inline
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Nyquist compliance via TDD pattern
- [x] No watch-mode flags
- [x] Feedback latency < 30s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
