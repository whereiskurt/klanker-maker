---
phase: 16-documentation-refresh-operator-guide-user-manual-and-docs-for-phases-6-15-features
plan: 01
subsystem: docs
tags: [operator-guide, user-manual, readme, km-bootstrap, km-doctor, scp, sidecars, github-app, budget, identity, email]

requires:
  - phase: 15-km-doctor-platform-health-check-and-bootstrap-verification
    provides: km doctor command and check framework
  - phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust
    provides: km-identities table and signed/encrypted email
  - phase: 13-github-app-token-integration-scoped-repo-access-for-sandboxes
    provides: GitHub App SSM layout and token refresh Lambda
  - phase: 10-scp-sandbox-containment-org-level-ec2-breakout-prevention
    provides: SCP deny statements and trusted role carve-outs
  - phase: 08-sidecar-build-deployment-pipeline
    provides: make sidecars, make ecr-push, make build-lambdas targets
  - phase: 06-budget-enforcement-platform-configuration
    provides: km-budgets DynamoDB table, budget-enforcer Lambda, km budget add

provides:
  - Updated operator guide with km bootstrap, budget enforcement, SCP, sidecar build pipeline, GitHub App, km-identities, and km doctor sections
  - Updated user manual with km doctor, km configure github, km budget add, spec.email, and sourceAccess.github sections
  - Updated README roadmap table showing phases 1-15 Complete and phases 16-17 tracked
  - Fixed stale infra/templates/network.terragrunt.hcl path reference in operator guide

affects:
  - future-operators: new sections are the primary onboarding reference for Phase 6-15 features
  - docs-maintenance: all three files are now current with the full v1 feature set

tech-stack:
  added: []
  patterns:
    - "Operator guide: numbered sections (11-17) for post-Phase-5 features, preserving existing section numbering"
    - "User manual: new command sections inserted before walkthroughs; new profile subsections appended to Profile Authoring Guide"

key-files:
  created: []
  modified:
    - docs/operator-guide.md
    - docs/user-manual.md
    - README.md

key-decisions:
  - "Operator guide sections 11-17 added at end of document, numbered sequentially after existing section 10 (Multi-Region)"
  - "User manual new command sections (km doctor, km configure github, km budget add) placed between km logs and the walkthroughs"
  - "Profile spec.email and sourceAccess.github documented as subsections of Profile Authoring Guide"
  - "README roadmap table uses **Complete** formatting consistent with existing Phase 2 entry"
  - "stale path infra/templates/network.terragrunt.hcl corrected to infra/templates/network/ (directory, not file)"

patterns-established:
  - "Source-verified docs: all flag names, parameter paths, and behavior documented from reading actual Go source and Terraform modules"

requirements-completed:
  - DOC-OPERATOR
  - DOC-USER
  - DOC-README

duration: 12min
completed: 2026-03-23
---

# Phase 16 Plan 01: Documentation Refresh Summary

**Operator guide, user manual, and README updated with accurate Phase 6-15 feature docs covering km bootstrap, budget enforcement, SCP (8 deny statements), sidecar build pipeline, GitHub App setup, km-identities table, and km doctor.**

## Performance

- **Duration:** ~12 min
- **Started:** 2026-03-23T05:35:00Z
- **Completed:** 2026-03-23T05:47:00Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments

- Operator guide expanded from 10 to 17 sections covering all Phase 6-15 operator-facing infrastructure
- User manual gained 3 new commands (km doctor, km configure github, km budget add) and 2 new profile sections (spec.email, sourceAccess.github) with updated ToC
- README roadmap table updated from a 6-row stale table to a complete 17-phase table, phases 1-15 marked Complete
- Stale template path (`infra/templates/network.terragrunt.hcl`) corrected to `infra/templates/network/`
- All new content verified against actual Go source files and Terraform modules (not guessed)

## Task Commits

1. **Task 1: Update operator guide with Phase 6-15 features** - `067430d` (docs)
2. **Task 2: Update user manual and README roadmap** - `70e8c4f` (docs)

## Files Created/Modified

- `docs/operator-guide.md` — Added sections 11-17: km bootstrap, budget enforcement, SCP containment, sidecar build pipeline, GitHub App setup, km-identities table, km doctor; fixed stale template path
- `docs/user-manual.md` — Added km doctor, km configure github, km budget add command sections; added spec.email and sourceAccess.github profile sections; updated ToC
- `README.md` — Updated roadmap table from 6 rows to 17 rows, all phases through 15 marked Complete

## Decisions Made

- Operator guide new sections numbered 11-17 sequentially, preserving existing 1-10 numbering
- km doctor, km configure github, and km budget add placed in Command Reference before the walkthroughs so the manual remains organized as reference-then-walkthrough
- SCP deny statements documented in a table with carve-out column for quick operator reference
- Budget enforcer Lambda documented with the exact naming convention (`km-budget-enforcer-{sandbox-id}`) and `make build-lambdas` output paths verified from the Makefile
- GitHub token refresh Lambda documented with the 45-minute EventBridge schedule interval, verified from Phase 13 summaries and source
- spec.email encryption notes that SES `Content.Raw` (not `Content.Simple`) is required to preserve custom `X-KM-*` headers — sourced from Phase 14 decisions

## Deviations from Plan

None - plan executed exactly as written. Stale template path fix was explicitly called out in the plan task 1 action.

## Issues Encountered

None.

## Next Phase Readiness

- Phase 16 plan 02 (if any) can reference this plan's docs as current
- All docs now accurate for v1 milestone; ready for operator onboarding
- No blockers

---
*Phase: 16-documentation-refresh-operator-guide-user-manual-and-docs-for-phases-6-15-features*
*Completed: 2026-03-23*
