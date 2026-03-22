---
phase: 09-live-infrastructure-operator-docs
plan: "03"
subsystem: docs
tags: [operator-guide, infr-01, infr-02, aws-sso, multi-account, documentation]

dependency_graph:
  requires:
    - "09-01: Makefile build-lambdas target and Terragrunt live configs (ttl-handler, dynamodb-budget, ses)"
    - "09-02: budget-enforcer HCL generator and km create/destroy wiring"
  provides:
    - "OPERATOR-GUIDE.md: complete first-time setup and deployment guide"
    - "INFR-01 satisfied: AWS multi-account setup procedure documented"
    - "INFR-02 satisfied: SSO configuration and km configure steps documented"
  affects:
    - "operator onboarding"
    - "Phase 09 phase gate"

tech-stack:
  added: []
  patterns:
    - "Ops runbook style: numbered steps, plain markdown, no emojis, AWS CLI verification commands"

key-files:
  created:
    - OPERATOR-GUIDE.md
  modified: []

key-decisions:
  - "OPERATOR-GUIDE.md documents km configure as the SSO/config entry point (INFR-02) — implements no new code, satisfies requirement through documentation"
  - "Deployment ordering documented explicitly: network first, then dynamodb-budget/ses/ttl-handler in any order, all before km create"
  - "budget-enforcer described as automatically deployed by km create (non-fatal) — consistent with Plan 09-02 implementation"

metrics:
  duration: "94s"
  completed_date: "2026-03-22"
  tasks_completed: 1
  files_changed: 1
---

# Phase 09 Plan 03: Operator Guide Summary

**One-liner:** Root-level OPERATOR-GUIDE.md covering AWS multi-account setup, SSO configuration, Lambda build, shared infrastructure deployment order, first sandbox creation, verification checklist, and troubleshooting — satisfying INFR-01 and INFR-02.

## Performance

- **Duration:** ~94s
- **Started:** 2026-03-22T23:12:20Z
- **Completed:** 2026-03-22T23:13:54Z
- **Tasks:** 1
- **Files modified:** 1

## Accomplishments

- OPERATOR-GUIDE.md written (457 lines) covering all 7 required sections
- Section 1 (Prerequisites): 3-account AWS Organizations structure, SSO setup, Route53 zone requirement, CLI tools table, state backend creation, KM_* env vars reference table
- Section 2 (Initial Configuration): `km configure` prompt documentation, AWS access verification, shell export commands
- Section 3 (Build Artifacts): `make build-lambdas` (arm64 Lambda zips), `make sidecars` (ECS sidecar binaries), `make ecr-push` (Docker images to ECR)
- Section 4 (Deploy Shared Infrastructure): explicit ordering — network, then dynamodb-budget/ses/ttl-handler, with SES DKIM propagation note
- Section 5 (First Sandbox): `km validate`, `km create`, `km list`, `km status`, `km destroy` workflow
- Section 6 (Verification Checklist): 5 numbered AWS CLI verification commands covering DynamoDB, SES, Lambda, S3 sidecars, ECR images
- Section 7 (Troubleshooting): exec format error, DKIM pending, state bucket missing, KM_ROUTE53_ZONE_ID missing, zip not found, DynamoDB not found, budget enforcer missing

## Task Commits

1. **Task 1: Write OPERATOR-GUIDE.md** - `fb483c0` (feat)

## Files Created/Modified

- `OPERATOR-GUIDE.md` - 457-line operator runbook at repo root

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- `/Users/khundeck/working/klankrmkr/OPERATOR-GUIDE.md` — FOUND (457 lines)
- Contains "km configure" — VERIFIED
- Contains "build-lambdas" — VERIFIED
- Contains "terragrunt apply" — VERIFIED
- Contains "KM_ROUTE53_ZONE_ID" — VERIFIED
- Line count >= 100 — VERIFIED (457 lines)
- Commit fb483c0 — VERIFIED
