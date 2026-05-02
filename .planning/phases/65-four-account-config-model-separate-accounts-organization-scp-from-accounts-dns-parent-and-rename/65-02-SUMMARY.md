---
phase: "65-four-account-config-model-separate-accounts-organization-scp-from-accounts-dns-parent-and-rename"
plan: "02"
subsystem: "cmd"
tags: ["rename", "bootstrap", "init", "configure", "info", "env-exports", "scp-gate"]
dependency_graph:
  requires: ["65-01 (OrganizationAccountID + DNSParentAccountID fields)"]
  provides: ["bootstrap.go migrated", "init.go migrated", "configure.go migrated", "info.go migrated", "create.go migrated", "uninit.go migrated", "9 test stubs filled"]
  affects: ["doctor.go (plan 03)", "HCL files (plan 04)"]
tech_stack:
  added: []
  patterns: ["t.Setenv + os.Unsetenv for subprocess env isolation", "ExportConfigEnvVars exported helper for testability"]
key_files:
  created: []
  modified:
    - internal/app/cmd/bootstrap.go
    - internal/app/cmd/bootstrap_test.go
    - internal/app/cmd/init.go
    - internal/app/cmd/init_test.go
    - internal/app/cmd/create.go
    - internal/app/cmd/uninit.go
    - internal/app/cmd/info.go
    - internal/app/cmd/info_test.go
    - internal/app/cmd/configure.go
    - internal/app/cmd/configure_test.go
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go
decisions:
  - "runShowPrereqs returns nil + descriptive message when OrganizationAccountID blank (not error) — per locked decision"
  - "--dns-parent-account and --organization-account both optional in --non-interactive mode — per locked decision"
  - "ExportConfigEnvVars exported as testable helper to avoid requiring real AWS credentials in init tests"
  - "doctor.go appConfigAdapter.GetManagementAccountID() returns '' shim to fix compile (plan 03 removes from interface)"
  - "doctor.go checkConfig drops management_account_id from required list (both new fields are optional)"
  - "TestInfoShowsNewAccountFields unsets KM_ACCOUNTS_ORGANIZATION/DNS_PARENT before subprocess to prevent bootstrap test env leakage"
metrics:
  duration: "26 minutes"
  completed: "2026-05-02T02:06:45Z"
  tasks_completed: 3
  files_modified: 12
---

# Phase 65 Plan 02: Migrate cmd callers of ManagementAccountID Summary

**One-liner:** Fan-out migration of ManagementAccountID callers to OrganizationAccountID (SCP) + DNSParentAccountID (DNS) across bootstrap.go/init.go/configure.go/info.go/create.go/uninit.go with all 9 plan-02 test stubs filled.

## What Was Done

### Task 1: bootstrap.go + bootstrap_test.go

**Commit:** 31ea79b

**Changes to `internal/app/cmd/bootstrap.go`:**
- `runShowPrereqs`: blank `OrganizationAccountID` returns `nil` + descriptive message (not error). Message names `accounts.organization` field. Per locked open-question resolution.
- `runShowSCP`: blank `OrganizationAccountID` returns `nil` + message (not error).
- `loadBootstrapConfig`: adds `DNSParentAccountID != ""` to the populated-cfg detection check.
- Dry-run output: `"Management account: %s"` → two lines: `"Organization account: %s"` + `"DNS parent account: %s"` (shows `(not set)` when blank).
- Dry-run SCP block: gated on `OrganizationAccountID != ""`, skip message references `accounts.organization`.
- Non-dry-run SCP gate: `OrganizationAccountID != ""` guard; `KM_ACCOUNTS_ORGANIZATION` exported inside gate; `KM_ACCOUNTS_DNS_PARENT` exported unconditionally (outside gate).
- All `mgmtAccount` local variables renamed to `orgAccount` throughout `runShowPrereqs` and `runShowSCP`.

**Changes to `internal/app/cmd/bootstrap_test.go`:**
- `TestBootstrapDryRunShowsOrganizationAccount`: dry-run with both fields set asserts "Organization account: 111111111111" + "DNS parent account: 222222222222" in output.
- `TestBootstrapDryRunNoOrganizationAccount`: dry-run with org blank asserts "SKIPPED" + "accounts.organization" in output.
- `TestBootstrapSCPSkipped_OrganizationBlank`: non-dry-run with org blank asserts zero terragrunt apply calls.
- `TestShowPrereqsNoOrganizationAccount`: `--show-prereqs` with org blank asserts nil error AND "accounts.organization" in output.

**Blocking deviation (Rule 3):** `doctor.go appConfigAdapter.GetManagementAccountID()` changed to return `""` (temporary shim — plan 03 removes from interface). `doctor_test.go` testConfig/testDoctorConfig given `GetManagementAccountID()` shim methods to satisfy the interface until plan 03 removes it.

### Task 2: init.go + create.go + uninit.go + info.go + tests

**Commit:** e13d079

**Changes to `internal/app/cmd/init.go`:**
- Env-presence map (`runInitDryRun`): `KM_ACCOUNTS_MANAGEMENT` → `KM_ACCOUNTS_ORGANIZATION` + `KM_ACCOUNTS_DNS_PARENT` as two entries.
- Env exports (`runInit`): single management block → two independent if-blocks for org and dns_parent.
- DNS gate: `if cfg.Domain != "" && cfg.DNSParentAccountID == ""` → print `[warn]` and skip `ensureSandboxHostedZone`. Both conditions (domain blank AND dns_parent blank) now have distinct warning messages.
- `ensureSandboxHostedZone` docstring: updated to reference `accounts.dns_parent`; explicit note that `klanker-management` AWS profile name is unchanged (out of scope).
- `ExportConfigEnvVars`: new exported helper function that performs the same env export logic as `runInit`, without requiring real AWS credentials. Used by tests.

**Changes to `internal/app/cmd/create.go`:**
- `ManagementAccountID` + `KM_ACCOUNTS_MANAGEMENT` → two-block pattern with `OrganizationAccountID`/`KM_ACCOUNTS_ORGANIZATION` + `DNSParentAccountID`/`KM_ACCOUNTS_DNS_PARENT`.

**Changes to `internal/app/cmd/uninit.go`:**
- Same two-block pattern as create.go.

**Changes to `internal/app/cmd/info.go`:**
- `"  Management:       %s\n"` → `"  Organization:     %s\n"` + `"  DNS parent:       %s\n"` (column-aligned with adjacent Application/Terraform lines).

**Changes to test files:**
- `init_test.go TestInitExportsNewAccountEnvVars`: real body using `cmd.ExportConfigEnvVars`. Uses `t.Setenv` + `os.Unsetenv` pattern for env cleanup.
- `info_test.go TestInfoShowsNewAccountFields`: real body using km binary. Asserts "Organization:" + "111111111111" present, "DNS parent:" + "222222222222" present, "Management:" absent. Unsets env vars before subprocess call to prevent bootstrap test leakage.

### Task 3: configure.go + configure_test.go

**Commit:** 0b609fb

**Changes to `internal/app/cmd/configure.go`:**
- `accountsConfig` struct: dropped `Management string`; added `Organization string` (yaml:organization,omitempty) + `DNSParent string` (yaml:dns_parent,omitempty).
- Variable declarations: `managementAcct` → `organizationAcct` + `dnsParentAcct`.
- Cobra flags: `--management-account` → `--organization-account` (optional, blank skips SCP) + `--dns-parent-account` (optional).
- `runConfigure` signature: `organizationAcct, dnsParentAcct` replace `managementAcct`.
- Non-interactive required-list: `--management-account` removed; neither new flag added (both optional per locked open-question resolution).
- Interactive wizard: single "Management AWS account ID" prompt → two prompts: "Organization account ID (optional — leave blank to skip SCP)" + "DNS parent zone account ID (optional if no DNS)".
- DNS delegation message: `"management account (%s)"` → `"DNS parent account (%s)"` using `dnsParentAcct`.
- `accountsConfig` population: `Organization: organizationAcct, DNSParent: dnsParentAcct`.

**Changes to `internal/app/cmd/configure_test.go`:**
- `TestConfigureWritesOrganizationAndDNSParent`: km binary test with `--organization-account 111111111111 --dns-parent-account 222222222222`; asserts YAML has `accounts.organization`, `accounts.dns_parent`; asserts `accounts.management` is ABSENT.
- `TestConfigureInteractivePromptsUseNewNames`: stdin-driven interactive test; asserts output contains "Organization" and "DNS parent" (case-insensitive); asserts "Management AWS account" prompt is absent.

### Deviation Fixes

**Commit:** dbe5817

- `doctor.go checkConfig`: removed `management_account_id` from required fields list. `GetManagementAccountID()` returns `""` (shim) causing all doctor tests to fail with "missing required config fields: management_account_id". Per RESEARCH.md: delete, not rename, this entry. Plan 03 handles full interface migration.
- `info_test.go`: unset `KM_ACCOUNTS_ORGANIZATION/DNS_PARENT` before subprocess call to prevent env leakage from in-process bootstrap tests.
- `init_test.go`: `t.Setenv` + `os.Unsetenv` pattern for all env vars set by `ExportConfigEnvVars` test.

## Verification Results

All 9 VALIDATION.md test IDs for plan 02 pass:

| Task ID | Test | Result |
|---------|------|--------|
| 65-02-01 | TestBootstrapDryRunShowsOrganizationAccount | PASS |
| 65-02-02 | TestBootstrapDryRunNoOrganizationAccount | PASS |
| 65-02-03 | TestBootstrapSCPApplyPath | PASS |
| 65-02-04 | TestBootstrapSCPSkipped_OrganizationBlank | PASS |
| 65-02-05 | TestShowPrereqsNoOrganizationAccount | PASS |
| 65-02-06 | TestConfigureWritesOrganizationAndDNSParent | PASS |
| 65-02-07 | TestConfigureInteractivePromptsUseNewNames | PASS |
| 65-02-08 | TestInfoShowsNewAccountFields | PASS |
| 65-02-09 | TestInitExportsNewAccountEnvVars | PASS |

## Grep Audit

- `grep -n 'ManagementAccountID' bootstrap.go` → 0 matches (CLEAN)
- `grep -n 'KM_ACCOUNTS_MANAGEMENT' bootstrap.go` → 0 matches (CLEAN)
- `grep -rn 'ManagementAccountID' init.go create.go uninit.go info.go` → 0 matches (CLEAN)
- `grep -rn 'KM_ACCOUNTS_MANAGEMENT' init.go create.go uninit.go` → 0 matches (CLEAN)
- `grep -n 'managementAcct|--management-account|management:' configure.go` → 0 matches (CLEAN)
- `grep -n 'klanker-management' init.go` → 4 matches (CORRECT: AWS profile name unchanged, out of scope)
- Remaining `ManagementAccountID` references → doctor.go only (plan 03 territory)

## Handoff to Plan 03

`doctor.go` is the last remaining file with stale references:
- Line 150: `GetManagementAccountID() string` in `DoctorConfigProvider` interface
- Line 174: `GetManagementAccountID()` → `""` (shim, not real field)
- Line 2033: `mgmtAccountID := cfg.GetManagementAccountID()` for Organizations API role

Plan 03 will:
1. Replace `GetManagementAccountID()` with `GetOrganizationAccountID()` + `GetDNSParentAccountID()` in the interface
2. Update `appConfigAdapter` to remove the shim
3. Fix `initRealDepsWithExisting` to use `GetOrganizationAccountID()`
4. Remove the temporary shim methods from `testConfig` and `testDoctorConfig`
5. Add `checkOrganizationAccountBlank` and `checkLegacyManagementField` new checks
6. Fill in the 6 doctor-specific test stubs from VALIDATION.md rows 65-03-*

## Self-Check: PASSED

- All 12 modified files exist on disk: CONFIRMED
- Task commits exist: 31ea79b, e13d079, 0b609fb, dbe5817 — CONFIRMED
- All 9 plan-02 VALIDATION tests pass: CONFIRMED
- `go build ./internal/app/cmd/` succeeds: CONFIRMED
- `klanker-management` profile string unchanged in init.go: CONFIRMED (4 occurrences)
- `runShowPrereqs` returns nil when org blank: CONFIRMED
- Neither --organization-account nor --dns-parent-account is required in non-interactive: CONFIRMED
