---
phase: 98-github-bridge-expansion
plan: 06
subsystem: infra
tags: [aws, dynamodb, iam, ec2, github-bridge, resume, gap-closure]

# Dependency graph
requires:
  - phase: 98-05
    provides: deploy-surface test baseline and docs for github-bridge
  - phase: 98-04
    provides: auto-resume EC2 path and SandboxAliasResolverWithStatus interface
provides:
  - EC2 resume IAM condition regression guard (deploy-surface test sub-test)
  - DDB status write-back after auto-resume (SetStatusRunning via UpdateItem)
  - dynamodb:UpdateItem IAM grant on km-sandboxes for the bridge Lambda role
affects: [98-github-bridge-expansion, km-github-bridge-lambda, km-operator-deploy]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "UpdateItem-only for status write-back — never PutItem to avoid SandboxMetadata lossy round-trip"
    - "Non-fatal status write: log on error, enqueue always continues"
    - "Deploy-surface regression guard: assert IAM tag condition in .tf file as a unit test"

key-files:
  created:
    - .planning/phases/98-github-bridge-expansion-.../98-06-SUMMARY.md
  modified:
    - infra/modules/lambda-github-bridge/v1.1.0/main.tf
    - pkg/github/bridge/interfaces.go
    - pkg/github/bridge/aws_adapters.go
    - pkg/github/bridge/webhook_handler.go
    - pkg/github/bridge/webhook_handler_phase98_04_test.go
    - internal/app/cmd/init_test.go

key-decisions:
  - "UpdateItem only for SetStatusRunning — PutItem would strip un-marshalled attributes (SandboxMetadata lossy round-trip footgun)"
  - "StatusWriter is non-fatal: a DDB error logs a Warn but enqueue always continues so the prompt is never lost"
  - "StatusWriter called only after StartSandbox nil return — not called on StartSandbox error to avoid misleading running status"
  - "Deploy-surface regression guard checks for absence of aws:ResourceTag/km:managed (the broken tag) as well as presence of km:resource-prefix"

requirements-completed: [GH-X-RESUME, GH-X-E2E]

# Metrics
duration: 8min
completed: 2026-06-07
---

# Phase 98 Plan 06: GH-X-RESUME Gap Closure Summary

**EC2 StartInstances IAM condition fixed + km-sandboxes status write-back wired: auto-resume now grants and records running state via UpdateItem (non-fatal)**

## Performance

- **Duration:** ~8 min
- **Started:** 2026-06-07T17:30:00Z
- **Completed:** 2026-06-07T17:38:50Z
- **Tasks:** 2 of 3 (Task 3 is a checkpoint:human-verify, paused awaiting operator redeploy + E2E)
- **Files modified:** 6

## Accomplishments

- Added `ec2_resume_condition_uses_resource_prefix` sub-test to `TestDeploySurfaceGitHubBridgePhase98`: asserts `aws:ResourceTag/km:resource-prefix` is present and `aws:ResourceTag/km:managed` is absent — the test will catch any future regression to the broken tag that caused every auto-resume to 403 in live UAT
- Added `SandboxStatusWriter` interface and `DynamoSandboxStatusWriter` adapter (`SetStatusRunning` via `dynamodb:UpdateItem` on km-sandboxes — UpdateItem only, never PutItem)
- Wired `StatusWriter.SetStatusRunning` into `WebhookHandler.Handle()` resume branch after `StartSandbox` nil return (non-fatal: Warn log on error, enqueue always continues)
- Added `dynamodb:UpdateItem` statement (`DDBSandboxesUpdateItem`) to `dynamodb_sandboxes` IAM policy in `v1.1.0/main.tf`
- Three new unit tests: `TestHandle_AutoResume_WritesStatusRunning`, `TestHandle_AutoResume_StatusWriteError_EnqueueContinues`, `TestHandle_AutoResume_NilStatusWriter_EnqueueContinues`

## Task Commits

1. **Task 1: IAM condition fix + regression test** - `e57ff4ba` (test)
2. **Task 2: DDB status write-back on resume + IAM** - `1eda6f0e` (feat)
3. **Task 3: Redeploy + manual E2E re-verify** - PAUSED (checkpoint:human-verify)

## Files Created/Modified

- `internal/app/cmd/init_test.go` - Added `ec2_resume_condition_uses_resource_prefix` sub-test to `TestDeploySurfaceGitHubBridgePhase98`
- `infra/modules/lambda-github-bridge/v1.1.0/main.tf` - Added `DDBSandboxesUpdateItem` statement to `dynamodb_sandboxes` policy
- `pkg/github/bridge/interfaces.go` - Added `SandboxStatusWriter` interface with `SetStatusRunning`
- `pkg/github/bridge/aws_adapters.go` - Added `DynamoUpdateItemClient` and `DynamoSandboxStatusWriter` implementing `SetStatusRunning`
- `pkg/github/bridge/webhook_handler.go` - Added `StatusWriter SandboxStatusWriter` field; wired call in resume branch
- `pkg/github/bridge/webhook_handler_phase98_04_test.go` - Added mock + 3 new test cases for status write-back behavior

## Decisions Made

- **UpdateItem only**: PutItem is intentionally excluded — full-row replaces strip attributes not in the SandboxMetadata struct (the lossy round-trip footgun). The `DDBSandboxesUpdateItem` IAM statement grants only `dynamodb:UpdateItem`, not `dynamodb:PutItem`.
- **Non-fatal status write**: `SetStatusRunning` error logs a Warn and continues. The prompt enqueue is never blocked by a DDB error.
- **Nil StatusWriter = backward compat**: existing Lambda deployments without `StatusWriter` wired work identically to pre-98-06 (no panics, resume + enqueue still work).
- **IAM regression guard asserts absence**: the test checks `!strings.Contains(src, "aws:ResourceTag/km:managed")` — this format only appears in IAM conditions, not in the resource tag blocks that use `"km:managed" = "true"` without the `aws:ResourceTag/` prefix.

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None — both tasks were clean. The existing test baseline was green before changes.

## Checkpoint Status

**Task 3 paused:** Operator must redeploy and run live E2E to verify the two gap fixes end-to-end.

Deploy sequence (operator workstation, `AWS_PROFILE=klanker-application`):
```
make build
make build-lambdas
km init --dry-run=false
```

Verify:
```
aws iam get-role-policy --role-name km-github-bridge-role \
  --policy-name km-github-bridge-ec2-resume \
  --query 'PolicyDocument.Statement[?Sid==`EC2StartInstances`].Condition'
# → aws:ResourceTag/km:resource-prefix, NOT km:managed
```

Then drive the @-mention scenario (paused aliased box) and confirm no 403 in CloudWatch, status flips to running in `km list`, and the agent posts the review.

**Resume signal:** `approved`

## Next Phase Readiness

- All code and IAM changes are committed and tested
- Only the live redeploy + E2E verification remains (Task 3, operator action)
- Once operator types "approved" the plan is complete

---

## Update (2026-06-07, second pass) — Tasks 3–5 complete; checkpoint is now Task 6

After live UAT, three more defects (Gaps C/D/E) were folded into this plan as Tasks 3–5 and
implemented in a second execution pass. The sections above reflect the FIRST pass (Tasks 1–2 +
the old Task-3 checkpoint); the current state is:

| Task | Gap | Commit | Status |
|------|-----|--------|--------|
| 1 | A — IAM condition `km:managed`→`km:resource-prefix` + regression test | `50e6c9b7`, `e57ff4ba` | ✅ done, deployed, live-validated |
| 2 | B — DDB status write-back on resume + IAM + main.go wiring | `1eda6f0e`, `94722eb9` | ✅ done, deployed, live-validated |
| 3 | C — `EC2Resumer` tolerates `stopping` (pause→mention race) | `22a0ab45` | ✅ done, tests green |
| 4 | D — token-mint robustness: granted-perms-only + non-empty refresher input | `c9c37739` | ✅ done, tests green |
| 5 | E — poller fresh-session fallback + cross-box continuity-row invalidation (`InvalidateStaleSession`) | `af8c97cb` | ✅ done, tests green (userdata golden re-captured) |
| 6 | — Redeploy + unattended E2E re-verify | — | ⏸ checkpoint:human-verify (operator) |

Verification at hand-off: `go build ./...` clean; `pkg/github/bridge`, `pkg/compiler`,
`internal/app/cmd` (incl. `TestDeploySurfaceGitHubBridge`) test suites green; the pre-existing
unrelated `cmd/km-slack` 503 test failure is out of scope. The second-pass executor hit its
context limit after committing Tasks 3–5, so this update + STATE.md were finalized by the
orchestrator. **Remaining: Task 6** — `make build && make build-lambdas && km init --dry-run=false`,
then re-run the paused-box @-mention E2E *unattended* (the token should mint itself; no manual
mint / row delete / poller restart) and approve. Full live-UAT context: `98-UAT.md`.

---
*Phase: 98-github-bridge-expansion*
*Completed: 2026-06-07 (Tasks 1–5 done; Task 6 checkpoint pending operator E2E)*
