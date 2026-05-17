---
phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
plan: 07
subsystem: infra
tags: [ses, aws-sdk-go-v2, doctor, bootstrap, km-init, preflight]

requires:
  - phase: 84-02
    provides: infra/live/use1/ses-shared-rule-set/ Terraform module (sandbox-email-shared rule set)
  - phase: 84-04
    provides: SESReceiptRuleAPI empty stub + checkSESRules skeleton in doctor.go

provides:
  - SESReceiptRuleAPI interface with real DescribeReceiptRuleSet method
  - checkSESRules function (CheckOK / CheckWarn orphan detection / CheckSkipped nil-client)
  - SESIdentityLister interface + detectSharedSESState helper (auto-detect for bootstrap)
  - km bootstrap --shared-ses flag driving infra/live/use1/ses-shared-rule-set/ apply
  - InitSESPreflight gate in RunInitWithRunner blocking ses module apply when shared rule set missing
  - aws-sdk-go-v2/service/ses classic v1 dependency added to go.mod
  - W0-06, W0-07, W0-08 Wave 0 stubs all GREEN

affects: [84-08, km-doctor, km-init, km-bootstrap]

tech-stack:
  added: [github.com/aws/aws-sdk-go-v2/service/ses v1.34.24]
  patterns:
    - "SESIdentityLister interface follows Phase 80 OidcProviderLister narrow-interface pattern"
    - "SESPreflightFunc package-level variable for inject-able init preflight (matches ApplyTerragruntFunc pattern)"
    - "DetectSharedSESState exported wrapper over unexported impl for cmd_test access"

key-files:
  created:
    - internal/app/cmd/bootstrap_ses_test.go
    - internal/app/cmd/init_ses_preflight_test.go
  modified:
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_ses_rules_test.go
    - internal/app/cmd/doctor_test.go
    - internal/app/cmd/bootstrap.go
    - internal/app/cmd/init.go
    - go.mod
    - go.sum

key-decisions:
  - "SESIdentityLister combines ListReceiptRuleSets (ses classic v1) + ListEmailIdentities (sesv2) into one interface for auto-detect — simpler than splitting"
  - "InitSESPreflight is a package-level SESPreflightFunc var (not interface method) so RunInitWithRunner stays testable without AWS config injection"
  - "defaultSESPreflight treats AWS config load errors as skip-not-fail — Terraform apply will surface a more specific error if rule set is truly absent"
  - "DetectSharedSESState exported (capitalized) so cmd_test package can call it directly; internal impl stays unexported"
  - "W0-06 and W0-07 tests in doctor_test.go updated to use mockSESReceiptRuleAPI with real rule names — nil was never a valid test for the happy path"

patterns-established:
  - "Pre-apply module gate: if mod.name == 'ses' && InitSESPreflight != nil { check } — only fires for the ses module"
  - "Auto-detect returns (bool, bool, error): true means create, false means reuse — matches Terraform variable semantics"

requirements-completed: [SES-SHARED-RULESET, SES-DOCTOR-ORPHANS]

duration: 45min
completed: 2026-05-16
---

# Phase 84 Plan 07: km bootstrap --shared-ses, checkSESRules, and ses preflight gate Summary

**SES classic v1 SDK added; checkSESRules implements orphan detection; km bootstrap --shared-ses auto-detects and applies ses-shared-rule-set/; km init fails fast with actionable error when shared rule set is missing**

## Performance

- **Duration:** 45 min
- **Started:** 2026-05-16T00:00:00Z
- **Completed:** 2026-05-16T00:45:00Z
- **Tasks:** 3
- **Files modified:** 9

## Accomplishments

- Replaced the empty `SESReceiptRuleAPI` stub with a real interface (DescribeReceiptRuleSet), implemented `checkSESRules` with full orphan-detection logic, and wired it into `km doctor` with a new `SESRulesClient` field on `DoctorDeps`
- Added `km bootstrap --shared-ses` that auto-detects shared rule set + domain identity state via `SESIdentityLister`, sets `KM_REGISTER_SHARED_RULESET` and `KM_REGISTER_DOMAIN_IDENTITY` env vars, and runs `terragrunt apply` against `infra/live/use1/ses-shared-rule-set/`
- Added `InitSESPreflight` gate in `RunInitWithRunner` that blocks the ses module apply with a clear "run km bootstrap --shared-ses first" error when the shared rule set is absent; all W0-06, W0-07, W0-08 Wave 0 stubs are now GREEN

## Task Commits

1. **Task 1: SESReceiptRuleAPI + checkSESRules + wire into doctor** - `bb5474c` (feat)
2. **Task 2: km bootstrap --shared-ses flag with auto-detect** - `39d77b1` (feat)
3. **Task 3: ses preflight gate in km init** - `efa6b64` (feat)

## Files Created/Modified

- `internal/app/cmd/doctor.go` — Added `ses` classic SDK import, `SESRulesClient` to DoctorDeps, real `SESReceiptRuleAPI` interface + `checkSESRules` implementation, wired into runDoctor check pipeline
- `internal/app/cmd/doctor_ses_rules_test.go` — Build-tag removed; mockSESReceiptRuleAPI now implements real `DescribeReceiptRuleSet`; W0-08 GREEN
- `internal/app/cmd/doctor_test.go` — W0-06 and W0-07 updated to use mockSESReceiptRuleAPI with actual rule names
- `internal/app/cmd/bootstrap.go` — Added `SESIdentityLister` interface, `realSESLister` adapter, `detectSharedSESState`, `runBootstrapSharedSES`, `DetectSharedSESState` export, `--shared-ses` flag
- `internal/app/cmd/bootstrap_ses_test.go` — 4 unit tests for all (true/false) combinations of detectSharedSESState
- `internal/app/cmd/init.go` — Added `ses` + `sesv2` imports, `InitSESPreflight` variable, `defaultSESPreflight` implementation, preflight gate in RunInitWithRunner
- `internal/app/cmd/init_ses_preflight_test.go` — Tests for missing-rule-set (error path) and present-rule-set (ses applied)
- `go.mod` / `go.sum` — Added `github.com/aws/aws-sdk-go-v2/service/ses v1.34.24`

## Decisions Made

- Combined `ListReceiptRuleSets` + `ListEmailIdentities` into a single `SESIdentityLister` interface (no need to split — both are readonly detection ops)
- `InitSESPreflight` is a package-level function variable (not embedded in InitRunner) to keep `RunInitWithRunner`'s signature stable for existing tests
- `defaultSESPreflight` treats AWS config load errors as skip (returns nil) to avoid blocking operators whose profiles differ; Terraform will surface the real error if the rule set is truly absent
- W0-06 and W0-07 were originally written with `nil` client (placeholder for wave 0) — updated them to pass real mocks since `nil` client now correctly returns `CheckSkipped`, not the test's expected `CheckOK`/`CheckWarn`

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] W0-06 and W0-07 tests used nil client expecting CheckOK/CheckWarn**
- **Found during:** Task 1 (running TestCheckSESRules)
- **Issue:** The Wave 0 stubs in doctor_test.go called `checkSESRules(ctx, nil, "kph")` and expected CheckOK or CheckWarn — but nil client correctly returns CheckSkipped per the real implementation
- **Fix:** Updated W0-06 and W0-07 to use `mockSESReceiptRuleAPI` with real rule names (matching the test intent described in comments)
- **Files modified:** internal/app/cmd/doctor_test.go
- **Verification:** All 5 TestCheckSESRules* tests now pass
- **Committed in:** bb5474c (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 — bug in wave 0 test stubs)
**Impact on plan:** Necessary correction — the stubs were never going to pass with real checkSESRules behavior; test intent was clear from comments.

## Issues Encountered

None — plan executed cleanly. `go mod tidy` removed the ses SDK until source code referenced it (expected Go module behavior), re-added with `go get`.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `km bootstrap --shared-ses` can drive Plan 84-02's `ses-shared-rule-set` Terraform module
- `km doctor` now reports `SES rules healthy` or `orphan SES rules: ...`
- `km init` will fail fast with a clear error if operator hasn't run `km bootstrap --shared-ses` first
- Plan 84-08 (or remaining wave 2 plans) can reference these interfaces as foundation

---
*Phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix*
*Completed: 2026-05-16*

## Phase 84.1 drift

Two inline UAT-time fixes modified the Plan 84-07 deliverables and should be considered authoritative:

- `143798d fix(84-07): run terragrunt init -reconfigure before apply in km bootstrap --shared-ses` — `defaultApplyTerragrunt` now calls `runner.Reconfigure(ctx, dir)` before `Apply` so a fresh terragrunt cache dir gets its backend initialized.
- `80b59a3 fix(84-07): reconfigure ses backend before apply in km init regional loop` — `InitRunner` interface gained `Reconfigure(ctx, dir) error`, and `RunInitWithRunner` calls it before the regional ses apply for the same reason (v1.0.0 → v2.0.0 source flip creates a new cache dir).

Additionally, the `InitSESPreflight` block-until-shared-rule-set-exists path was NOT exercised in Phase 84 UAT (rule set was always present). Plan 84.1-05 UAT exercises this path by deliberately destroying the foundation rule set and confirming `km init` fails fast.
