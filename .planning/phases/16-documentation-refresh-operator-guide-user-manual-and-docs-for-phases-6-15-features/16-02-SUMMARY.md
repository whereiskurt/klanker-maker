---
phase: 16-documentation-refresh-operator-guide-user-manual-and-docs-for-phases-6-15-features
plan: "02"
subsystem: docs
tags: [budget-enforcer, lambda, eventbridge, scp, github-app, ed25519, nacl, dynamodb, security]

requires:
  - phase: 06-budget-enforcement-platform-configuration
    provides: budget-enforcer Lambda, EventBridge Scheduler, DynamoDB spend tracking
  - phase: 10-scp-sandbox-containment-org-level-ec2-breakout-prevention
    provides: SCP module with 8 deny statements and carve-out locals
  - phase: 13-github-app-token-integration-scoped-repo-access-for-sandboxes
    provides: github-token-refresher Lambda, KMS encryption, SSM token storage
  - phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust
    provides: Ed25519 identity, NaCl encryption, DynamoDB km-identities table

provides:
  - Updated docs/budget-guide.md with Lambda architecture, compute/AI enforcement, top-up flow, per-model breakdown
  - Updated docs/security-model.md with SCP layer (14), GitHub App security (15), sandbox identity (16)

affects:
  - future operator onboarding — new sections document security and budget enforcement precisely
  - Phase 17 — mailbox access control builds on identity layer now documented

tech-stack:
  added: []
  patterns:
    - "Source-verified documentation: all technical details verified against infra/modules/ and pkg/ source before writing"

key-files:
  created: []
  modified:
    - docs/budget-guide.md
    - docs/security-model.md

key-decisions:
  - "Budget enforcer Lambda uses SET (not ADD) for compute spend — idempotent absolute calculation from created_at"
  - "SCP DenyOrganizationsDiscovery has no carve-out condition — applies to all roles in Application account"
  - "km-ecs-task-* not carved out from DenyInstanceMutation SCP — task role is the sandbox workload, must stay contained"
  - "GitHub App tokens stored at /sandbox/{sandbox-id}/github-token, read by GIT_ASKPASS at git time not at boot"
  - "Ed25519 signs body only (not headers) — simpler verification, headers may change in SES transit"
  - "NaCl box.SealAnonymous used for encryption — sender identity in X-KM-Sender-ID header, not ciphertext"

requirements-completed:
  - DOC-BUDGET
  - DOC-SECURITY

duration: 3min
completed: 2026-03-23
---

# Phase 16 Plan 02: Budget Guide and Security Model Refresh Summary

**Budget guide updated with budget-enforcer Lambda architecture, compute/AI dual-layer enforcement, and top-up restoration flow; security model expanded with SCP deny statements, GitHub App token lifecycle, and Ed25519 sandbox identity**

## Performance

- **Duration:** 3 min
- **Started:** 2026-03-23T06:18:04Z
- **Completed:** 2026-03-23T06:21:19Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- Budget guide gains five new sections: Lambda architecture with per-sandbox naming and EventBridge Scheduler, compute enforcement semantics (EC2 suspend-and-resume vs ECS stop-and-reprovision), AI dual-layer enforcement (proxy 403 + IAM revocation), top-up sequence per pool, and per-model AI breakdown with static pricing fallback.
- Security model gains three new numbered sections (14-16): SCP with all 8 deny statements and precise carve-out locals, GitHub App token security with the full 45-minute refresh lifecycle, and sandbox identity with Ed25519 signing, X25519 encryption, and profile controls.
- All technical details verified against source: `infra/modules/budget-enforcer/v1.0.0/main.tf`, `infra/modules/scp/v1.0.0/main.tf`, `infra/modules/github-token/v1.0.0/main.tf`, `pkg/github/token.go`, `pkg/aws/identity.go`.

## Task Commits

Each task was committed atomically:

1. **Task 1: Update budget guide with Lambda architecture and enforcement details** - `f2c3a55` (docs)
2. **Task 2: Update security model with SCP, GitHub App, and identity layers** - `706b1b3` (docs)

**Plan metadata:** (this commit) (docs: complete plan)

## Files Created/Modified

- `docs/budget-guide.md` - Added Budget-Enforcer Lambda Architecture, Compute Budget Enforcement Details, AI Budget Dual-Layer Enforcement, Top-Up Flow Details, Per-Model AI Breakdown sections; updated Table of Contents
- `docs/security-model.md` - Added Section 14 (Service Control Policies), Section 15 (GitHub App Token Security), Section 16 (Sandbox Identity and Email Signing); renumbered Threat Model to Section 17; updated Table of Contents

## Decisions Made

None — documentation written to match the existing implementation decisions recorded in STATE.md. All decisions were made during the respective implementation phases.

## Deviations from Plan

None — plan executed exactly as written. All source references (infra modules, pkg/ packages) were readable and accurate.

## Issues Encountered

None.

## User Setup Required

None — documentation update only, no external service configuration required.

## Next Phase Readiness

- docs/budget-guide.md and docs/security-model.md are now complete references for their respective domains
- Phase 17 (sandbox email mailbox) can reference the identity section (Section 16) as the authoritative source for signing/allow-list behavior
- No blockers

---
*Phase: 16-documentation-refresh-operator-guide-user-manual-and-docs-for-phases-6-15-features*
*Completed: 2026-03-23*
