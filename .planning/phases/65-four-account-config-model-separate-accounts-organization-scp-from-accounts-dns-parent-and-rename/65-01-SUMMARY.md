---
phase: "65-four-account-config-model-separate-accounts-organization-scp-from-accounts-dns-parent-and-rename"
plan: "01"
subsystem: "config"
tags: ["rename", "config-struct", "wave-0", "test-stubs"]
dependency_graph:
  requires: []
  provides: ["OrganizationAccountID field", "DNSParentAccountID field", "Wave 0 test stubs"]
  affects: ["bootstrap.go callers (plan 02)", "doctor.go (plan 03)"]
tech_stack:
  added: []
  patterns: ["viper merge key mapping", "t.Skip stub pattern"]
key_files:
  created:
    - internal/app/cmd/info_test.go
  modified:
    - internal/app/config/config.go
    - internal/app//config/config_test.go
    - internal/app/cmd/doctor_test.go
    - internal/app/cmd/bootstrap_test.go
    - internal/app/cmd/configure_test.go
    - internal/app/cmd/init_test.go
decisions:
  - "Hard rename with no back-compat alias — ManagementAccountID removed entirely"
  - "Wave 1 exit state: cmd package intentionally broken until plan 02 migrates callers"
  - "doctorStaleAMIConfig compile-check updated to _testConfigPostRename shim (not old DoctorConfigProvider)"
  - "TestBootstrapSCPApplyPath not re-stubbed (real body migrated, plan 02 fixes production caller)"
metrics:
  duration: "5 minutes"
  completed: "2026-05-02T01:25:26Z"
  tasks_completed: 3
  files_modified: 7
---

# Phase 65 Plan 01: Config Struct Rename + Wave 0 Test Stubs Summary

**One-liner:** Hard rename of ManagementAccountID into OrganizationAccountID (SCP, optional) + DNSParentAccountID (DNS parent zone) with all Wave 0 test stubs in place for plans 02 and 03.

## What Was Done

### Task 1: Rename config struct + viper bindings + add round-trip tests (TDD)

**Commit:** 8001cad

**Changes to `internal/app/config/config.go`:**
- Removed `ManagementAccountID string` field and its comment (line 50-52)
- Added two new fields after `Domain`:
  ```go
  // OrganizationAccountID is the AWS Organizations management account ID (SCP target).
  // Maps to km-config.yaml key accounts.organization. Optional: blank skips SCP deployment.
  OrganizationAccountID string

  // DNSParentAccountID is the AWS account ID owning the parent Route53 hosted zone for cfg.Domain.
  // Maps to km-config.yaml key accounts.dns_parent. Blank skips DNS delegation in km init.
  DNSParentAccountID    string
  ```
- Viper merge key list: removed `"accounts.management"`, added `"accounts.organization"` and `"accounts.dns_parent"`
- cfg struct initialization: replaced `ManagementAccountID: v.GetString("accounts.management")` with:
  ```go
  OrganizationAccountID: v.GetString("accounts.organization"),
  DNSParentAccountID:    v.GetString("accounts.dns_parent"),
  ```

**Changes to `internal/app/config/config_test.go`:**
- Migrated `TestLoadPlatformFields`: YAML fixture changed from `accounts.management` to `accounts.dns_parent`; assertion changed from `ManagementAccountID` to `DNSParentAccountID`
- Migrated `TestLoadBackwardCompat`: assertions changed from `ManagementAccountID` to `DNSParentAccountID` + `OrganizationAccountID`
- Added `TestLoadOrganizationAndDNSParentFields`: writes km-config.yaml with both `accounts.organization` and `accounts.dns_parent`, asserts both fields load correctly
- Added `TestLoadBlankOrganizationIsValid`: writes km-config.yaml without `accounts.organization`, asserts no error and `OrganizationAccountID == ""`

### Task 2: Update DoctorConfigProvider stubs in doctor_test.go (Wave 0 deliverable)

**Commit:** aa724ac

**Changes to `internal/app/cmd/doctor_test.go`:**
- `testConfig` struct: removed `mgmtAcct string` field; added `orgAcct string` and `dnsParentAcct string`
- `testConfig` methods: removed `GetManagementAccountID()`; added `GetOrganizationAccountID()` and `GetDNSParentAccountID()`
- `testDoctorConfig` struct: same field renames as `testConfig`
- `testDoctorConfig` methods: same method renames as `testConfig`
- `TestCheckConfig_OK`: `mgmtAcct: "111111111111"` → `dnsParentAcct: "111111111111"`
- `minimalConfig()`: `mgmtAcct: "111111111111"` → `dnsParentAcct: "111111111111"`
- Old compile-time check `var _ DoctorConfigProvider = (*testConfig)(nil)` replaced with local shim:
  ```go
  type _testConfigPostRename interface {
      GetOrganizationAccountID() string
      GetDNSParentAccountID() string
  }
  var _ _testConfigPostRename = (*testConfig)(nil)
  var _ _testConfigPostRename = (*testDoctorConfig)(nil)
  ```
- `doctorStaleAMIConfig` compile check updated to `_testConfigPostRename` shim

### Task 3: Stub Wave 0 test functions in cmd test files

**Commit:** 615d48b

**New stubs (all use `t.Skip("Plan 0X — implement in Phase 65 plan 0X")`):**

| Function | File | Plan |
|----------|------|------|
| `TestBootstrapDryRunShowsOrganizationAccount` | bootstrap_test.go | 02 |
| `TestBootstrapDryRunNoOrganizationAccount` | bootstrap_test.go | 02 |
| `TestBootstrapSCPSkipped_OrganizationBlank` | bootstrap_test.go | 02 |
| `TestShowPrereqsNoOrganizationAccount` | bootstrap_test.go | 02 |
| `TestConfigureWritesOrganizationAndDNSParent` | configure_test.go | 02 |
| `TestConfigureInteractivePromptsUseNewNames` | configure_test.go | 02 |
| `TestInfoShowsNewAccountFields` | info_test.go (NEW) | 02 |
| `TestInitExportsNewAccountEnvVars` | init_test.go | 02 |
| `TestCheckOrganizationAccountBlank_BlankReturnsWarn` | doctor_test.go | 03 |
| `TestCheckOrganizationAccountBlank_SetReturnsOK` | doctor_test.go | 03 |
| `TestCheckLegacyManagementField_FieldPresent` | doctor_test.go | 03 |
| `TestCheckLegacyManagementField_FieldAbsent` | doctor_test.go | 03 |
| `TestCheckLegacyManagementField_NoConfigFile` | doctor_test.go | 03 |
| `TestCheckConfigDoesNotRequireManagement` | doctor_test.go | 03 |

**Migrations in existing tests (no new stubs needed, real bodies migrated):**
- `TestBootstrapSCPApplyPath`: `ManagementAccountID` → `OrganizationAccountID`
- `TestBootstrapDryRunShowsSCP`: `ManagementAccountID` → `OrganizationAccountID`
- `TestBootstrapDryRunNoManagementAccount`: `ManagementAccountID` → `OrganizationAccountID`
- All `configure_test.go` tests: `--management-account` → `--dns-parent-account`; `accounts["management"]` → `accounts["dns_parent"]`
- `init_test.go` and `configure_test.go` km-config.yaml fixtures: `accounts.management` → `accounts.dns_parent`

## Verification Results

- `go test ./internal/app/config/... -count=1` — **15/15 PASS** including 2 new tests
- `grep -rn 'ManagementAccountID' internal/app/config/` — **0 matches** (clean)
- `grep -rn 'GetManagementAccountID' internal/app/cmd/doctor_test.go` — **0 matches** (clean)
- All 15 Wave 0 stub test functions exist in their assigned files
- `go build ./...` — **intentionally broken** (plan 02 exits this state by migrating bootstrap.go, create.go, configure.go, init.go, uninit.go, info.go)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] doctorStaleAMIConfig compile-check referenced old interface**
- **Found during:** Task 2
- **Issue:** Line `var _ DoctorConfigProvider = (*doctorStaleAMIConfig)(nil)` required `GetManagementAccountID()` but the embedded `testDoctorConfig` no longer has it
- **Fix:** Updated to `var _ _testConfigPostRename = (*doctorStaleAMIConfig)(nil)` matching the post-rename shim
- **Files modified:** `internal/app/cmd/doctor_test.go`
- **Commit:** aa724ac

None otherwise — plan executed exactly as designed.

## Self-Check: PASSED

- All 7 files exist on disk: CONFIRMED
- All 3 task commits exist: 8001cad, aa724ac, 615d48b — CONFIRMED
- `go test ./internal/app/config/...` green: CONFIRMED
- 15 Wave 0 stub functions present: CONFIRMED
- No `ManagementAccountID` in config/: CONFIRMED
- No `GetManagementAccountID` in doctor_test.go: CONFIRMED
