---
phase: 103-hackerone-comment-trigger-bridge
plan: 10
subsystem: testing
tags: [hackerone, deploy-surface, guard-tests, e2e, regional-modules, lambda-builds, merge-list, uat]

# Dependency graph
requires:
  - phase: 103-hackerone-comment-trigger-bridge
    plan: 08
    provides: "regionalModules() dynamodb-h1-threads + lambda-h1-bridge; lambdaBuilds() km-h1-bridge; sidecarBuilds() km-h1; LambdaBuildNames()/SidecarBuildNames()/RegionalModules() test-seam accessors; TestRegionalModulesIncludesH1Bridge + TestH1BridgeBuildListMembership"
  - phase: 103-hackerone-comment-trigger-bridge
    plan: 02
    provides: "config.H1Config + v2->v merge-list 'h1' entry; config_h1_test.go round-trip + TestLoadH1_MergeListRegression"
  - phase: 103-hackerone-comment-trigger-bridge
    plan: 09
    provides: "notification.h1.inbound schema field + profiles/h1-triage.yaml + km-h1-inbound-poller userdata"
provides:
  - "TestLambdaBuildsIncludesH1Bridge: membership-map guard (km-h1-bridge in lambdaBuilds, km-h1 in sidecarBuilds) — the H1 analog of TestLambdaBuildsIncludesGitHubBridge"
  - "TestRegionalModulesH1BridgeOrdering: single-occurrence + full ordering chain guard (threads<bridge, shared-deps<bridge, github-bridge<bridge, bridge<ses)"
  - "config_h1_test.go MERGE-LIST GUARD: explicit nested-target round-trip assertion proving the v2->v 'h1' merge entry survives (half-merged struct fails)"
  - "test/e2e/h1/e2e_test.go: RUN_H1_E2E=1-gated live harness (skips cleanly when unset; Sandbox-program tripwire; read-only km h1 status + km-h1 read slices; manual reply-visibility documented)"
  - "103-UAT.md: tokenless operator deploy + six-step reply-visibility UAT runbook"
affects: [103-phase-completion, future-deploy-surface-refactors]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Deploy-surface GUARD tests lock the three Phase-97 footguns against silent regression: lambdaBuilds membership (zip never built), regionalModules ordering (mock ARN baked in), config merge-list (block silently dropped). A future refactor that re-opens any gap goes RED."
    - "Gated live E2E harness (RUN_H1_E2E=1, mirror RUN_SLACK_E2E) carries only the automatable, non-mutating slices (km h1 status, km-h1 read); the safety-critical reply VISIBILITY (internal vs researcher-visible) is manual-only because it is observable only in the live HackerOne UI (Phase-97/SLCK-09 precedent)."
    - "Sandbox-program tripwire: the E2E config refuses to run unless KM_H1_E2E_PROGRAM contains 'test'/'sandbox' — a code-level guardrail against accidentally pointing the live comment path at a production HackerOne program."

key-files:
  created:
    - test/e2e/h1/e2e_test.go
    - .planning/phases/103-hackerone-comment-trigger-bridge-km-h1-bridge-lambda-dispatches-hackerone-webhook-events-to-per-program-sandbox-agents-auto-triage-comment-keyword-internal-by-default-replies/103-UAT.md
  modified:
    - internal/app/cmd/init_test.go
    - internal/app/config/config_h1_test.go

key-decisions:
  - "TestLambdaBuildsIncludesH1Bridge is added alongside (not replacing) Plan 08's TestH1BridgeBuildListMembership — the plan's verify command greps -run 'TestLambdaBuildsIncludesH1Bridge|RegionalModules', so the explicitly-named guard mirrors the GitHub naming convention and makes the H1 family unmistakable in the build-list guard set."
  - "The config merge-list guard is REINFORCED (not duplicated): the existing TestLoadH1_MergeListRegression already proves 'h1' is in the merge-loop; Plan 10 adds an explicit nested-target round-trip assertion so a HALF-merged struct (program loads, nested fields empty) also fails — closing the subtle partial-merge gap."
  - "The live E2E harness automates only non-mutating paths (status, read); posting comments + reply-visibility are deferred to the manual 103-UAT.md because visibility semantics are unobservable outside the HackerOne UI. This matches the Slack/GitHub precedent and avoids any live mutation in `go test`."
  - "103-UAT.md is pre-filled with the operator's validated HackerOne Sandbox org + two test programs + API username, but the API token is deliberately referenced as operator-supplied-at-init-time and NEVER written to the repo."

patterns-established:
  - "Pattern: deploy-surface regression guards live in init_test.go (build lists + module ordering) AND the package's own config test (merge-list) — the two halves of H1-DEPLOY-WIRING are co-located with the code they guard."

requirements-completed: [H1-DEPLOY-WIRING]

# Metrics
duration: 5min
completed: 2026-06-10
---

# Phase 103 Plan 10: H1 deploy-surface guards + gated E2E harness + UAT runbook Summary

**Deploy-surface guard tests that go RED if km-h1-bridge is dropped from lambdaBuilds/regionalModules or km-h1 from sidecarBuilds or the `h1` merge-list entry is removed, plus a RUN_H1_E2E-gated live harness and a tokenless operator UAT runbook — Tasks 1 & 2 complete; Task 3 (live deploy + reply-visibility UAT) awaits the operator's HackerOne Sandbox.**

## Performance

- **Duration:** ~5 min (autonomous portion)
- **Started:** 2026-06-10T05:09:37Z
- **Completed:** 2026-06-10T05:13:45Z
- **Tasks:** 2 of 3 autonomous (Task 3 is an awaiting-operator checkpoint)
- **Files created:** 2; **modified:** 2

## Accomplishments
- **TestLambdaBuildsIncludesH1Bridge** — the H1 analog of `TestLambdaBuildsIncludesGitHubBridge`: asserts `km-h1-bridge` in `lambdaBuilds()` and `km-h1` in `sidecarBuilds()`. A drop = the zip/helper is silently never built (Phase-97 footgun).
- **TestRegionalModulesH1BridgeOrdering** — single-occurrence + full ordering chain: `dynamodb-h1-threads < lambda-h1-bridge`, bridge AFTER `dynamodb-sandboxes`/`dynamodb-slack-nonces`/`lambda-github-bridge`, bridge BEFORE `ses`. A mis-order bakes a mock dependency ARN (account `000000000000`) into the bridge unit at apply.
- **config merge-list guard reinforced** — explicit nested-target round-trip assertion so a half-merged `h1:` struct also fails, not just a fully-missing one.
- **test/e2e/h1/e2e_test.go** — `RUN_H1_E2E=1`-gated harness; skips cleanly without the env var; carries the non-mutating live slices (`km h1 status`, `km-h1 read`) + a Sandbox-program tripwire; the safety-critical reply-visibility steps are documented as manual-only.
- **103-UAT.md** — the tokenless operator runbook: deploy sequence (make build BEFORE km init footgun #6; `--dry-run=false` NOT `--sidecars` footgun #7), Webhooks UI paste location, the six-step reply-visibility checklist, multi-target fanout expectation (N internal + exactly 1 external), and the 429/loop-guard watch notes.

## Task Commits

1. **Task 1: Deploy-surface guard tests + gated E2E harness** — `23659c6e` (test)
2. **Task 2: Full-suite green + write 103-UAT.md runbook** — `9af1fbd9` (docs)
3. **Task 3: Live deploy + UAT — reply visibility** — NOT executed (awaiting-operator checkpoint; see below)

**Plan metadata:** (this SUMMARY + STATE/ROADMAP) committed separately.

## Files Created/Modified
- `internal/app/cmd/init_test.go` — added `TestLambdaBuildsIncludesH1Bridge` + `TestRegionalModulesH1BridgeOrdering`.
- `internal/app/config/config_h1_test.go` — reinforced `TestLoadH1_MergeListRegression` with the explicit nested-target merge-list guard.
- `test/e2e/h1/e2e_test.go` — new gated live E2E harness.
- `103-UAT.md` — new operator deploy + reply-visibility UAT runbook.

## Decisions Made
See frontmatter `key-decisions`. In short: name the build-list guard `TestLambdaBuildsIncludesH1Bridge` to match the verify grep + GitHub convention; reinforce (not duplicate) the merge-list guard; automate only non-mutating live slices; keep the API token out of the repo.

## Deviations from Plan

None - plan executed exactly as written for the autonomous scope (Tasks 1 & 2). Task 3 was intentionally NOT performed — it is a `checkpoint:human-verify` live deploy + UAT that the operator runs against their HackerOne Sandbox (see "Awaiting Operator" below).

**Total deviations:** 0.
**Impact on plan:** None.

## Issues Encountered

**Pre-existing, out-of-scope `make test` failure (NOT fixed — logged to deferred-items.md):**
`pkg/hygiene` `TestGoSourceNamesUseResourcePrefix` flags 5 hardcoded `km-` name-construction sites in `internal/app/cmd/doctor_artifacts.go:351` and `internal/app/cmd/doctor_log_groups.go:{62,68,74,80}`. These were introduced in **Phase 94** (commits `5bdd4153` / `af50bb69`), verified RED on `HEAD~1` (before this plan's commit) — entirely unrelated to Plan 103-10. Per the executor scope boundary (pre-existing failure in unrelated files), it is logged to `deferred-items.md`, not fixed. Recommended fix: derive the prefix via `resourcePrefix()`/`cfg.GetResourcePrefix()` in those four doctor sites, or add `relpath:literal` allow-list entries. The Task-1 verify command and all H1/config/e2e packages are green.

## Awaiting Operator (Task 3 — live deploy + reply-visibility UAT)

Task 3 is a blocking `checkpoint:human-verify`. The full H1 bridge (Lambda + module + helper + poller + profile) is wired into `km init` and the guard tests are green; the ONLY thing left is the live confirmation of reply visibility, observable only in the real HackerOne UI. The operator runs `103-UAT.md`:
1. Deploy: `make build` → `make build-lambdas` → `km init --dry-run=false` → `km init --sidecars` → `km h1 init` → `km create profiles/h1-triage.yaml`.
2. Paste the printed Function URL + secret into the HackerOne Sandbox program Webhooks UI; fire a Test request → 200 in Recent-Deliveries.
3. `report_created` → INTERNAL triage comment (NOT researcher-visible).
4. Allowlisted `@handle /reply_to_researcher` → researcher-visible, posted exactly ONCE (primary target only) despite N targets.
5. Non-allowlisted `@handle /reply_to_researcher` → downgraded/blocked (NOT researcher-visible — a researcher-visible reply here is a P0 safety bug).
6. No repeated identical internal comments (loop guard).

Target: the operator's HackerOne Sandbox org `prodsec_klanker_maker_test_pro_demo` (programs `prodsec_klanker_maker_test_h1b` / `prodsec_klanker_maker_test_h1r`). No production program is ever a target.

## Next Phase Readiness
- Deploy surface locked by automated guards; no silent Phase-97-style regression possible.
- The safety-critical reply-visibility verification remains the operator's live UAT (Task 3) before the phase is fully signed off.
- After the operator confirms all six UAT checks, Phase 103 is complete and ready for `/gsd:verify-work 103`.

## Self-Check: PASSED

- Created files all exist on disk: `test/e2e/h1/e2e_test.go`, `103-UAT.md`, `103-10-SUMMARY.md`; modified `internal/app/cmd/init_test.go` + `internal/app/config/config_h1_test.go`.
- Commits `23659c6e` (Task 1, test) + `9af1fbd9` (Task 2, docs) present in git history.
- Task-1 verify green: `go test ./internal/app/cmd -run 'TestLambdaBuildsIncludesH1Bridge|RegionalModules'`, `go test ./internal/app/config -run H1`, `go vet ./test/e2e/h1/...` all pass; the gated harness skips cleanly without `RUN_H1_E2E`.
- `make test`: the only failure is the PRE-EXISTING `pkg/hygiene` check (Phase-94 doctor sites, RED on `HEAD~1`, unrelated) — logged to deferred-items.md, not fixed per scope boundary.
- Task 3 intentionally NOT executed — awaiting-operator `checkpoint:human-verify`.

---
*Phase: 103-hackerone-comment-trigger-bridge*
*Completed (autonomous Tasks 1 & 2): 2026-06-10 · Task 3 awaiting operator*
