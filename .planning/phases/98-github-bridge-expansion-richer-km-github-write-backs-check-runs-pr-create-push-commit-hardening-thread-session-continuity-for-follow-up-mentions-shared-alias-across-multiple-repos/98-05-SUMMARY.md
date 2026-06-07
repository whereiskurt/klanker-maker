---
phase: 98-github-bridge-expansion
plan: 05
subsystem: github-bridge
tags: [deploy-surface, docs, e2e-gate, phase-gate]
dependency_graph:
  requires: [98-01, 98-02, 98-03, 98-04]
  provides: [GH-X-E2E-gate, deploy-surface-assertions, phase-98-runbook]
  affects: [docs/github-bridge.md, internal/app/cmd/init_test.go, internal/app/cmd/init.go]
tech_stack:
  added: []
  patterns: [table-subtest, exported-struct-extension, file-level-assertions, terraform-fmt]
key_files:
  created: []
  modified:
    - internal/app/cmd/init.go
    - internal/app/cmd/init_test.go
    - infra/modules/lambda-github-bridge/v1.0.0/main.tf
    - infra/modules/lambda-github-bridge/v1.1.0/main.tf
    - docs/github-bridge.md
decisions:
  - "Export EnvReqs field from RegionalModule so tests can directly assert env requirements without behavioral indirection"
  - "5-sub-test table style for deploy surface: one assertion per invariant, fast fail isolation"
  - "terraform fmt applied to both v1.0.0 and v1.1.0 module directories (pre-existing HCL formatting drift)"
  - "Phase 98 doc section appended to existing github-bridge.md rather than a new file (continuity for operators)"
metrics:
  duration: "~26 minutes"
  completed: "2026-06-07T15:45:18Z"
  tasks_completed: 2
  tasks_pending: 1
  files_changed: 5
---

# Phase 98 Plan 05: Deploy-Surface Verification + Docs + E2E Gate Summary

**One-liner:** Deploy-surface assertions (5 sub-tests: module ordering, build lists, envReqs, IAM-runtime grep, live-unit version) + Phase 98 operator runbook in docs/github-bridge.md; E2E gate awaiting operator verification.

## What Was Built

### Task 1: Automated deploy-surface verification pass (COMPLETE — commit 1c7b08c8)

Added `TestDeploySurfaceGitHubBridgePhase98` to `internal/app/cmd/init_test.go` with 5 sub-tests that encode Phase 97/98 deploy footguns as in-process, file-level assertions (no live AWS):

| Sub-test | Assertion | Result |
|---|---|---|
| `dynamodb_github_threads_before_bridge` | `dynamodb-github-threads` in `regionalModules()`, ordered before `lambda-github-bridge` | PASS |
| `build_lists_complete` | `km-github-bridge` in `LambdaBuildNames()`, `km-github` in `SidecarBuildNames()` | PASS |
| `lambda_github_bridge_envreqs_artifacts_bucket` | `lambda-github-bridge` `EnvReqs` includes `KM_ARTIFACTS_BUCKET` | PASS |
| `iam_runtime_cross_check` | `v1.1.0/main.tf` contains `km-github-threads`, `ec2:StartInstances`, `ec2:DescribeInstances`, `dynamodb:GetItem`, `dynamodb:UpdateItem` | PASS |
| `live_unit_sources_v1_1_0` | `infra/live/use1/lambda-github-bridge/terragrunt.hcl` sources `v1.1.0` | PASS |

Also exported `EnvReqs []string` from `RegionalModule` struct (previously unexported — needed for sub-test 3). Applied `terraform fmt` to `v1.0.0` and `v1.1.0` modules (pre-existing HCL formatting drift).

Full suite result: all failures are pre-existing (3 out-of-scope: `TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0`, `TestRunAgentAuthClaude_TeesAndCleans`, `TestAtList_WithRecords`; plus `cmd/ttl-handler` EC2-metadata timeout and `pkg/hygiene` hardcoded-km prefix — all confirmed pre-existing on base). No new failures introduced.

### Task 2: docs/github-bridge.md Phase 98 operator runbook (COMPLETE — commit 93eca8fd)

Added a "Phase 98" section to `docs/github-bridge.md` covering:

- **New verbs:** `km-github check --name --conclusion --summary --head-sha` and `km-github pr create --title --base --head --body`; worktree-per-PR pattern for concurrent review sandboxes.
- **Thread continuity:** `km-github-threads` DDB table schema; first-dispatch creates record; follow-up bypass mirrors Slack Phase 91.3; `agent_session_id` carries context across turns.
- **Shared alias:** multiple `github.repos` entries → one sandbox; `km doctor` warns on alias collision and match overlap; intentional shared alias requires explicit `alias:` on both entries.
- **Auto-resume:** bridge `ec2:StartInstances`; configure-once/stop/GitHub-wakes pattern; enqueue-then-drain flow; CloudWatch log evidence.
- **Cold-create:** `PreStageGitHubProfiles` S3 staging; `spec.secrets.sopsFile` SOPS bundle; operator step to encrypt Claude OAuth creds; artifact_prefix double-slash fix.
- **Deploy sequence:** encodes `make build-lambdas` (not `make build`) footgun, `km init --dry-run=false` for new DDB table + IAM, `km init --sidecars` for binary refresh, existing sandbox recreate.
- **Troubleshooting + E2E checklist:** 5 scenarios (continuity, write-backs, shared-alias, auto-resume, cold-create).

All plan verification checks pass:
- `grep -q "Phase 98"` ✓
- `grep -qiE "km-github check|pr create"` ✓
- `grep -qiE "auto-resume|StartInstances"` ✓
- `grep -qiE "SOPS|sopsFile"` ✓
- `grep -qiE "make build-lambdas"` ✓

### Task 3: Manual E2E verification (GH-X-E2E) — PENDING OPERATOR

This is a `checkpoint:human-verify` gate. The operator must deploy to real AWS + GitHub and run the 5 E2E scenarios before the phase is considered complete.

See the `GH-X-E2E` checkpoint details below for the exact deploy steps and verification scenarios.

## Commits

| Task | Commit | Description |
|---|---|---|
| Task 1 | `1c7b08c8` | feat(98-05): deploy-surface verification pass for Phase 98 GitHub bridge |
| Task 2 | `93eca8fd` | docs(98-05): Phase 98 operator runbook in docs/github-bridge.md |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing critical functionality] Exported `EnvReqs` from `RegionalModule`**
- **Found during:** Task 1
- **Issue:** `RegionalModule` only exported `Name` and `Dir`; the `envReqs` field was unexported so the deploy-surface test could not directly assert that `lambda-github-bridge` requires `KM_ARTIFACTS_BUCKET`.
- **Fix:** Added `EnvReqs []string` to `RegionalModule` and populated it in `RegionalModules()`.
- **Files modified:** `internal/app/cmd/init.go`
- **Commit:** `1c7b08c8`

**2. [Rule 1 - Bug / formatting] terraform fmt on v1.0.0 and v1.1.0 modules**
- **Found during:** Task 1 (plan requires `terraform fmt` clean as part of done criteria)
- **Issue:** Both `infra/modules/lambda-github-bridge/v1.0.0/main.tf` and `v1.1.0/main.tf` had HCL formatting drift.
- **Fix:** Applied `terraform fmt` to both module directories.
- **Files modified:** `infra/modules/lambda-github-bridge/v1.0.0/main.tf`, `infra/modules/lambda-github-bridge/v1.1.0/main.tf`
- **Commit:** `1c7b08c8`

## Pre-existing Out-of-Scope Failures (not fixed, logged)

The following test failures exist on the base (pre-Phase-98) and are confirmed not introduced by this plan:
- `cmd/km-slack: TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0` — mentioned in plan context_note as out-of-scope
- `internal/app/cmd: TestRunAgentAuthClaude_TeesAndCleans` — requires live OAuth browser flow
- `internal/app/cmd: TestAtList_WithRecords` — requires live DynamoDB
- `cmd/ttl-handler` — EC2 metadata credential timeout (no AWS profile in test env)
- `pkg/hygiene: TestGoSourceNamesUseResourcePrefix` — pre-existing hardcoded `km-` strings in doctor files

## GH-X-E2E Checkpoint Status

**Status:** PENDING OPERATOR ACTION

The automated deploy-surface verification and docs are complete. The operator must now:

1. Deploy (see `docs/github-bridge.md` § Phase 98 deploy sequence)
2. Run the 5 E2E scenarios from `docs/github-bridge.md` § Phase 98 E2E verification checklist

After all scenarios pass, respond with "approved" to mark this plan complete.

## Self-Check: PASSED

All files verified present. Both commits verified in git log.

| Item | Status |
|---|---|
| `internal/app/cmd/init_test.go` | FOUND |
| `internal/app/cmd/init.go` | FOUND |
| `docs/github-bridge.md` | FOUND |
| `98-05-SUMMARY.md` | FOUND |
| commit `1c7b08c8` | FOUND |
| commit `93eca8fd` | FOUND |
