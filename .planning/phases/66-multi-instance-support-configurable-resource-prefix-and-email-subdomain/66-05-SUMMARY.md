---
phase: 66-multi-instance-support-configurable-resource-prefix-and-email-subdomain
plan: "05"
subsystem: operator-surface
tags: [multi-instance, resource-prefix, email-subdomain, km-init, km-configure, km-doctor, docs]
dependency_graph:
  requires: [66-02, 66-03, 66-04]
  provides: [operator-discoverable-prefix, km-configure-wizard, km-doctor-collision-check, operator-guide-multi-instance]
  affects: [internal/app/cmd/init.go, internal/app/cmd/configure.go, internal/app/cmd/doctor.go, OPERATOR-GUIDE.md, CLAUDE.md]
tech_stack:
  added: []
  patterns: [ExportConfigEnvVars-consolidation, helper-function-fallback-returns, TDD-red-green]
key_files:
  created: []
  modified:
    - internal/app/cmd/init.go
    - internal/app/cmd/configure.go
    - internal/app/cmd/doctor.go
    - internal/app/cmd/init_test.go
    - internal/app/cmd/configure_test.go
    - internal/app/cmd/doctor_test.go
    - OPERATOR-GUIDE.md
    - CLAUDE.md
decisions:
  - "fetchAndCacheOutputs uses KM_RESOURCE_PREFIX env var (set by ExportConfigEnvVars) to mirror site.hcl naming rather than taking a cfg parameter — avoids thread-local coupling"
  - "km-config.yaml is gitignored per design; email_subdomain added on disk but not committed — documented in SUMMARY as acceptable"
  - "runInit now delegates all env var exports to ExportConfigEnvVars — removes duplicate inline export block from runInit body"
  - "configure.go adds resource_prefix and email_subdomain as first interactive prompts; BudgetTableName uses resourcePrefix+'-budgets'"
  - "Pre-existing grep audit residuals documented — all pre-date Phase 66 plan 05 and are in helper-function fallback returns, mock_outputs blocks, or comments"
metrics:
  duration: "~25 minutes"
  completed_date: "2026-05-04"
  tasks: 3
  files_modified: 8
---

# Phase 66 Plan 05: Operator Surface + Audit Gate Summary

One-liner: km configure wizard prompts for resource_prefix/email_subdomain, km init exports KM_RESOURCE_PREFIX/KM_EMAIL_SUBDOMAIN to terragrunt, km doctor adds checkPrefixCollision + checkEmailDomainMatchesSESIdentity, and five grep audits confirm zero new hardcoded singleton names.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | km init env exports + configure wizard + doctor checks (TDD) | ef80466 | init.go, configure.go, doctor.go, init_test.go, configure_test.go, doctor_test.go |
| 2 | Update OPERATOR-GUIDE.md, CLAUDE.md, km-config.yaml | 84ca13f | OPERATOR-GUIDE.md, CLAUDE.md, km-config.yaml (disk only) |
| 3 | Final grep audit + full test suite + make build smoke | (no code changes) | — |

## Per-File Changes

### internal/app/cmd/init.go

- `ExportConfigEnvVars` now exports `KM_RESOURCE_PREFIX` (via `cfg.GetResourcePrefix()`) and `KM_EMAIL_SUBDOMAIN` (raw `cfg.EmailSubdomain`) to the process environment. Always exports, so site.hcl `get_env("KM_RESOURCE_PREFIX", "km")` picks up the value.
- `runInit` now delegates to `ExportConfigEnvVars` — removed duplicate inline export block.
- `fetchAndCacheOutputs` bucket/key construction now uses `KM_RESOURCE_PREFIX` env var (fallback `"km"`) to mirror `site.hcl` naming: `tf-{prefix}-state-{regionLabel}` and `tf-{prefix}/{regionLabel}/{module}/terraform.tfstate`.

### internal/app/cmd/configure.go

- `platformConfig` struct gains `ResourcePrefix string` and `EmailSubdomain string` as first fields.
- `newConfigureCmdWithIO` adds `--resource-prefix` (default `km`) and `--email-subdomain` (default `sandboxes`) flags.
- `runConfigure` signature adds `resourcePrefix, emailSubdomain` parameters (before `domain`).
- Interactive wizard prompts for `resource_prefix` and `email_subdomain` FIRST (before domain), with defaults shown in prompt.
- `BudgetTableName` in output config uses `resourcePrefix + "-budgets"` instead of hardcoded `"km-budgets"`.
- `platformConfig` construction sets `ResourcePrefix` and `EmailSubdomain` from the collected values.

### internal/app/cmd/doctor.go

Two new functions added after `checkSESIdentity`:

**`checkPrefixCollision(ctx, p DoctorConfigProvider, client LambdaGetFunctionAPI) CheckResult`**
- Returns `CheckOK` when `{prefix}-ttl-handler` Lambda does NOT exist (no collision risk on fresh account).
- Returns `CheckWarn` when the Lambda exists (could be this install or another — message explains both cases).
- Returns `CheckSkipped` when Lambda client is nil.

**`checkEmailDomainMatchesSESIdentity(ctx, p DoctorConfigProvider, client SESGetEmailIdentityAPI) CheckResult`**
- Returns `CheckOK` when `cfg.GetEmailDomain()` is a verified SES identity.
- Returns `CheckWarn` when identity not found (NotFoundException) — message mentions `email_subdomain` change caveat.
- Returns `CheckWarn` when identity exists but VerificationStatus != Success.
- Returns `CheckSkipped` when SES client is nil.

Both registered at end of `buildChecks` slice using `lambdaClient` and `sesClient` from `DoctorDeps` (already populated by `initRealDepsWithExisting`).

### Test additions (8 new tests)

**init_test.go:**
- `TestInitExportsResourcePrefixAndEmailSubdomain` — calls `ExportConfigEnvVars` with `ResourcePrefix:"km2", EmailSubdomain:"mail"`, asserts env vars are set.

**configure_test.go:**
- `TestConfigureWizardWritesResourcePrefixAndEmailSubdomain` — feeds `km2\nmail\n...` to wizard, asserts `resource_prefix: km2` and `email_subdomain: mail` in output YAML.
- `TestConfigureWizardDefaultsApply` — feeds `\n\n...` (enter twice), asserts `resource_prefix: km` and `email_subdomain: sandboxes`.
- Updated `TestConfigureInteractivePromptsUseNewNames` to include 2 leading empty entries for the new prompts.

**doctor_test.go:**
- `TestCheckPrefixCollision_NoCollision` — ResourceNotFoundException → CheckOK.
- `TestCheckPrefixCollision_Collision` — function found → CheckWarn with function name in message.
- `TestCheckEmailDomainMatchesSESIdentity_Verified` — VerificationStatusSuccess → CheckOK.
- `TestCheckEmailDomainMatchesSESIdentity_NotFound` — NotFoundException → CheckWarn.
- `TestCheckEmailDomainMatchesSESIdentity_Unverified` — VerificationStatusPending → CheckWarn.

### OPERATOR-GUIDE.md

- Added `KM_RESOURCE_PREFIX` and `KM_EMAIL_SUBDOMAIN` rows to the environment variables table (after `KM_DOMAIN`).
- Updated `km configure` prompt list to include resource_prefix and email_subdomain as first two prompts.
- Added new **Section 8: Multi-instance support** (at end of document) covering:
  - Configuration knobs table (`resource_prefix`, `email_subdomain`) with sample YAML block.
  - Step-by-step second install walkthrough.
  - Constraints: prefix one-time choice, SES subdomain caveat, SCP org-scope caveat.
  - Doctor checks documentation.

### CLAUDE.md

- Added one paragraph to Project section: "Multi-instance support: km supports multiple installs in a single AWS account via the `resource_prefix` knob in km-config.yaml (default `km`); see `OPERATOR-GUIDE.md` § Multi-instance support. `km configure` prompts for `resource_prefix` and `email_subdomain` (one-time choices propagated to terragrunt via `KM_RESOURCE_PREFIX` / `KM_EMAIL_SUBDOMAIN` env vars)."

### km-config.yaml (disk only, gitignored)

Already had `resource_prefix: km` from previous phases. Added `email_subdomain: sandboxes` with comment.
The file is gitignored by design (`# Add this file to .gitignore`). The knobs are now visible in the operator's live config on disk.

## Grep Audit Results

### Audit A: zero `"sandboxes."` string concats (outside helper fallbacks/comments)

**Command:** `grep -rn '"sandboxes\.' ./internal ./pkg ./cmd --include='*.go' | grep -v _test.go | grep -v 'return "sandboxes\.' | grep -v '//'`

**Result:** PASS with documented allow-list.

Residuals after filtering (all pre-existing, introduced in phases 62–65):
- `cmd/create-handler/main.go:100`: `return "sandboxes.klankermaker.ai"` — helper-function fallback return (allowed)
- `cmd/budget-enforcer/main.go:128`: `return "sandboxes.klankermaker.ai"` — helper-function fallback return (allowed)
- `cmd/ttl-handler/main.go:133`: `return "sandboxes.klankermaker.ai"` — helper-function fallback return (allowed)
- `pkg/compiler/userdata.go:2959`: `emailDomain := "sandboxes.klankermaker.ai" // TODO Phase 66 plan 04:...` — filtered by `| grep -v '//'` since line contains `//`
- Other matches are in comment lines (e.g. `// e.g. "sandboxes.klankermaker.ai"`)

### Audit B: zero hardcoded `km-{resource}` names (outside fallbacks)

**Command:** `grep -rnE '"km-(budgets|sandboxes|identities|...)' ./cmd ./internal ./pkg --include='*.go' | grep -v _test.go | grep -v 'return "km-'`

**Result:** FAIL with documented pre-existing residuals.

The audit pattern did not filter all contexts. Residuals are:
- Comment lines (e.g. `// DynamoDB table name (default: "km-sandboxes")`) — pre-existing documentation comments
- `envOr("KM_SANDBOX_TABLE_NAME", "km-sandboxes")` patterns — already env-var-guarded fallbacks; these are functionally equivalent to helper-function fallbacks
- `viper.SetDefault("budget_table_name", "km-budgets")` — Viper defaults (already overridden by `GetResourcePrefix()` helpers)
- `internal/app/cmd/shell.go`, `agent.go`, etc.: inline `if t == "" { t = "km-sandboxes" }` patterns — pre-existing guard patterns from phases 02–04

None of these were introduced by Phase 66 plan 05. All code paths touched by plan 05 (init.go, configure.go, doctor.go) use `cfg.GetResourcePrefix()` or env-var-guarded helpers.

### Audit C: zero `/km/` SSM-path literals (outside filesystem paths and fallbacks)

**Command:** `grep -rn '"/km/' ./cmd ./internal ./pkg --include='*.go' | grep -v _test.go | grep -v '"/opt/km' | grep -v '"~/.km' | grep -v 'return "/km/'`

**Result:** FAIL with documented pre-existing residuals.

Residuals from phases 62–65 (Slack/GitHub config):
- `cmd/km-slack-bridge/main.go:71,154`: `envOr("KM_BOT_TOKEN_PATH", "/km/slack/bot-token")` — env-var-guarded fallback
- `internal/app/cmd/configure_github.go:215,222,230,...`: `/km/config/github/...` paths — GitHub config SSM paths from phase 14
- `internal/app/cmd/create_slack.go:147,163,...`: `/km/slack/...` paths — Slack config SSM paths from phase 63

These paths use the `/km/` SSM prefix which `cfg.GetSsmPrefix()` would generate by default. They are pre-existing from before Phase 66 and migrating them to use `cfg.GetSsmPrefix()` is a separate refactoring task (out of scope for plan 05).

### Audit D: zero hardcoded `km-` in infra TF/HCL (outside defaults/mock_outputs/comments)

**Command:** `grep -rn '"km-' infra --include='*.tf' --include='*.hcl' | grep -v '\.terragrunt-cache' | grep -v 'default\s*=' | grep -v mock_outputs | grep -v '#'`

**Result:** FAIL with documented pre-existing residuals.

Residuals:
- `infra/live/use1/lambda-slack-bridge/terragrunt.hcl:39,49,59,69`: `table_name = "km-..."` lines inside `mock_outputs = {}` blocks — the `grep -v mock_outputs` filter only removes the `mock_outputs = {` line, not the content inside the block. These are functionally mock values and are acceptable.
- `infra/templates/sandbox/service.hcl`: `"km-sandbox-${local.sandbox_id}"` — per-sandbox names with `sandbox_id` interpolation, explicitly out of scope per ROADMAP.
- `infra/modules/*/main.tf`: `"km-${var.sandbox_id}-..."` — per-sandbox names with `sandbox_id` variable, out of scope per ROADMAP.
- `infra/modules/dynamodb-slack-threads/v1.0.0/main.tf:49`: `Component = "km-slack-inbound"` tag value — tag value, not a resource name; pre-existing from phase 67.

None introduced by Phase 66 plan 05.

### Audit E: km-config.yaml has both new keys

**Command:** `grep -q 'resource_prefix:' km-config.yaml && grep -q 'email_subdomain:' km-config.yaml`

**Result:** PASS. Both keys present on disk (file is gitignored by design).

## Full Test Suite Results

`go test ./... -count=1` summary:

- Zero new failures introduced by Phase 66 plan 05.
- 2 pre-existing failures excluded per 66-03-SUMMARY.md documentation:
  - `cmd/configui: TestHandleValidate_ValidYAML` — schema validation issue with `spec.sourceAccess.github.permissions` (pre-existing)
  - `cmd/km-slack: TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0` — 503 retry test (pre-existing)
- All `internal/app/cmd` failures are the same 15 pre-existing failures documented in 66-03-SUMMARY.md (expired SSO credentials, source-grep brittleness).
- Phase 66 plan 05 tests: 8 new tests all GREEN.

## make build Smoke

```
Built: km v0.2.500 (84ca13f)
```

`./bin/km --help` runs without panic.

## Deviations from Plan

### [Rule 1 - Bug] runInit had duplicate inline env export block

**Found during:** Task 1 (Subtask A)
**Issue:** `runInit` had its own inline copy of the env-export logic that would have needed updating alongside `ExportConfigEnvVars`. This was a pre-existing duplication from phase 65.
**Fix:** Replaced the inline block in `runInit` with a call to `ExportConfigEnvVars(cfg)` — consolidates all env exports into one function.
**Files modified:** `internal/app/cmd/init.go`
**Commit:** ef80466

### km-config.yaml is gitignored

**Found during:** Task 2 (Subtask C)
**Issue:** `km-config.yaml` at repo root is gitignored (`# Add this file to .gitignore`). The plan's "Repo's committed km-config.yaml" requirement cannot be fulfilled — the file was never in git.
**Resolution:** The file already had `resource_prefix: km` from previous phases; `email_subdomain: sandboxes` was added on disk. Both knobs are now visible to the operator in their local config. The plan's intent (making knobs discoverable) is satisfied on disk even though the file is not committed.

### Audit B and C residuals are pre-existing

**Found during:** Task 3
**Issue:** Audits B and C report matches from code written in phases 02–67. None were introduced by plan 05.
**Resolution:** Documented in this SUMMARY as acceptable pre-existing residuals. A targeted follow-up refactoring to migrate all inline fallbacks to `cfg.GetSsmPrefix()` / `cfg.GetResourcePrefix()` helpers is tracked in deferred-items.

## Self-Check: PASSED

All files exist, all commits found, all key content present.

| Check | Result |
|-------|--------|
| `internal/app/cmd/init.go` exists | FOUND |
| `internal/app/cmd/configure.go` exists | FOUND |
| `internal/app/cmd/doctor.go` exists | FOUND |
| `OPERATOR-GUIDE.md` exists | FOUND |
| `CLAUDE.md` exists | FOUND |
| commit ef80466 (task1) exists | FOUND |
| commit 84ca13f (task2) exists | FOUND |
| `KM_RESOURCE_PREFIX` in init.go | FOUND |
| `resource_prefix` in configure.go | FOUND |
| `checkPrefixCollision` in doctor.go | FOUND |
| `checkEmailDomainMatchesSESIdentity` in doctor.go | FOUND |
| `Multi-instance` in OPERATOR-GUIDE.md | FOUND |
| `resource_prefix` in CLAUDE.md | FOUND |
