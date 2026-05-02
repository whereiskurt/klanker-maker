---
phase: "65-four-account-config-model-separate-accounts-organization-scp-from-accounts-dns-parent-and-rename"
plan: "03"
subsystem: "cmd"
tags: ["rename", "doctor", "config-provider", "yaml-parsing", "scp-warn", "legacy-check"]
dependency_graph:
  requires:
    - phase: "65-02"
      provides: "plan 02 shims: GetManagementAccountID() returning '' on appConfigAdapter + testConfig/testDoctorConfig"
  provides:
    - "DoctorConfigProvider interface post-rename (GetOrganizationAccountID + GetDNSParentAccountID)"
    - "checkOrganizationAccountBlank: WARN when org blank, OK when set"
    - "checkLegacyManagementField: FAIL when accounts.management in raw YAML, OK/SKIP otherwise"
    - "Both checks registered in buildChecks"
    - "6 VALIDATION.md 65-03-* test stubs filled and passing"
    - "go build ./... green project-wide"
  affects: ["plan 04 (HCL + docs)"]
tech_stack:
  added: ["gopkg.in/yaml.v3 import in doctor.go (raw YAML parsing for legacy-field check)"]
  patterns:
    - "injectable path parameter for testability (checkLegacyManagementField(kmConfigPath string))"
    - "raw YAML read via os.ReadFile + yaml.Unmarshal to detect keys Viper silently drops"
key_files:
  created: []
  modified:
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go
key_decisions:
  - "checkLegacyManagementField reads raw YAML (not Viper-loaded config) because Viper silently ignores unknown keys after the rename"
  - "Remediation text uses 'rename' not 'replace' — test assertion requirement from plan behavior block"
  - "findKMConfigPath() returns single string (not (string, error)) — confirmed from bootstrap.go signature"
  - "Upgraded _testConfigPostRename compile shim to full DoctorConfigProvider interface assertions"
  - "Operator-visible doctor check count increases by 2 (was 18, now 20) — flag for CLAUDE.md follow-up (out of scope here per CONTEXT.md)"
requirements-completed: []
duration: "8min"
completed: "2026-05-02"
---

# Phase 65 Plan 03: Doctor Checks + Interface Cleanup Summary

**DoctorConfigProvider interface fully post-renamed with two new checks: WARN for blank org (SCP disabled) and FAIL for stale accounts.management in raw YAML — go build green project-wide for first time since plan 01.**

## Performance

- **Duration:** ~8 minutes
- **Started:** 2026-05-02T02:08Z (approx)
- **Completed:** 2026-05-02T02:16Z (approx)
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- Removed all plan-02 shims from `DoctorConfigProvider` interface, `appConfigAdapter`, and test stubs (`testConfig`, `testDoctorConfig`, `doctorStaleAMIConfig`)
- Added `GetOrganizationAccountID()` and `GetDNSParentAccountID()` to the interface and all three adapter/stub implementations
- Fixed `initRealDepsWithExisting` to use `GetOrganizationAccountID()` for the km-org-admin Organizations API role ARN
- Added `checkOrganizationAccountBlank` (WARN severity) and `checkLegacyManagementField` (FAIL severity) with both registered in `buildChecks`
- Filled all 6 VALIDATION.md 65-03-01..06 test stubs — zero SKIP remaining

## Interface Diff (DoctorConfigProvider before/after)

**Before (post plan 02 shims):**
```go
GetManagementAccountID() string  // returns "" (shim)
// GetOrganizationAccountID and GetDNSParentAccountID NOT in interface
```

**After (plan 03 final):**
```go
GetOrganizationAccountID() string  // declared in interface + implemented in all adapters
GetDNSParentAccountID() string     // declared in interface + implemented in all adapters
// GetManagementAccountID removed entirely
```

## New Check Function Summaries

### checkOrganizationAccountBlank(cfg DoctorConfigProvider) CheckResult
- Calls `cfg.GetOrganizationAccountID()`
- Non-empty → `CheckOK` with `"accounts.organization = {id} (SCP enabled)"`
- Empty → `CheckWarn` with `"SCP enforcement disabled — sandbox containment relies on IAM policies only"`
- No AWS calls; runs synchronously

### checkLegacyManagementField(kmConfigPath string) CheckResult
- Empty path → delegates to `findKMConfigPath()` (single-return bootstrap.go helper)
- File missing or unreadable → `CheckSkipped`
- YAML unparseable → `CheckSkipped`
- `accounts.management` key present → `CheckError` with remediation containing "rename"
- `accounts.management` absent → `CheckOK`
- No AWS calls; reads raw YAML via `os.ReadFile` + `yaml.Unmarshal`

## buildChecks Registration Order

1. `checkConfig` (always first)
2. `checkLegacyManagementField("")` — immediately after config (raw YAML, no AWS)
3. ... credential, s3, dynamo, kms checks ...
4. `checkOrganizationAccountBlank(cfg)` — immediately BEFORE `checkSCP` (config-level WARN before runtime check)
5. `checkSCP` — Organizations API runtime check

## Task Commits

1. **Task 1: Update DoctorConfigProvider interface + adapter + initRealDepsWithExisting** - `d3e5710` (feat)
2. **Task 2: Add checkOrganizationAccountBlank + checkLegacyManagementField + 6 tests** - `5741660` (feat)

## Verification Results

All 6 VALIDATION.md task IDs for plan 03 pass:

| Task ID | Test | Result |
|---------|------|--------|
| 65-03-01 | TestCheckOrganizationAccountBlank_BlankReturnsWarn | PASS |
| 65-03-02 | TestCheckOrganizationAccountBlank_SetReturnsOK | PASS |
| 65-03-03 | TestCheckLegacyManagementField_FieldPresent | PASS |
| 65-03-04 | TestCheckLegacyManagementField_FieldAbsent | PASS |
| 65-03-05 | TestCheckLegacyManagementField_NoConfigFile | PASS |
| 65-03-06 | TestCheckConfigDoesNotRequireManagement | PASS |

## Grep Audit

- `grep -n 'ManagementAccountID\|GetManagementAccountID\|mgmtAccount' doctor.go` → 0 matches (CLEAN)
- `grep -n 'GetOrganizationAccountID\|GetDNSParentAccountID' doctor.go` → 7 matches (interface declaration ×2, adapter ×2, initRealDeps ×1, buildChecks closure ×1, check function ×1)
- `grep -n 'management_account_id' doctor.go` → 1 match (comment only, not in required list)
- `grep -n '"gopkg.in/yaml.v3"' doctor.go` → 1 match (confirmed import)

## Doctor Check Count Change

The operator-visible `km doctor` check count increased from 18 → 20 (two new checks added). `CLAUDE.md` currently advertises "18 checks". This is out of scope for plan 03 per CONTEXT.md — flagged here for plan 04 or follow-up to update to "20 checks".

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] findKMConfigPath() returns single string, not (string, error)**
- **Found during:** Task 2 (adding checkLegacyManagementField)
- **Issue:** Plan action block showed `p, err := findKMConfigPath()` but the actual signature in bootstrap.go is `func findKMConfigPath() string` (single return)
- **Fix:** Changed to `p := findKMConfigPath()` with nil-check on empty string
- **Files modified:** `internal/app/cmd/doctor.go`
- **Commit:** 5741660

**2. [Rule 1 - Bug] Remediation text used "replace" but test asserted "rename"**
- **Found during:** Task 2 (running test suite)
- **Issue:** `TestCheckLegacyManagementField_FieldPresent` asserts `strings.Contains(result.Remediation, "rename")`. Plan action said "replace 'management' key" but plan behavior said Remediation contains "rename"
- **Fix:** Changed remediation text to "rename 'management' to 'dns_parent'"
- **Files modified:** `internal/app/cmd/doctor.go`
- **Commit:** 5741660

**3. [Rule 3 - Blocking] fmt import unused after removing var _ = fmt.Sprintf guard**
- **Found during:** Task 2 (running tests)
- **Issue:** Removing the `var _ = fmt.Sprintf` import guard exposed that `fmt` was no longer used in doctor_test.go
- **Fix:** Removed `"fmt"` from doctor_test.go imports
- **Files modified:** `internal/app/cmd/doctor_test.go`
- **Commit:** 5741660

---

**Total deviations:** 3 auto-fixed (2 Rule 1 bugs, 1 Rule 3 blocking)
**Impact on plan:** All auto-fixes required for correctness. No scope creep.

## Self-Check: PASSED

- `internal/app/cmd/doctor.go` exists: CONFIRMED
- `internal/app/cmd/doctor_test.go` exists: CONFIRMED
- Task commits d3e5710 and 5741660: CONFIRMED
- `go build ./...` green: CONFIRMED
- All 6 plan-03 VALIDATION tests pass: CONFIRMED
- Zero GetManagementAccountID references in doctor.go: CONFIRMED

## Next Phase Readiness

Plan 03 is the gating condition for plan 04 (HCL + docs + repo km-config migration). The whole Go tree is now green:
- `go build ./...` succeeds project-wide
- `go test ./internal/app/cmd/... -run TestCheck|TestDoctor|TestBootstrap|TestConfigure|TestInfo` passes
- Zero remaining `ManagementAccountID` or `GetManagementAccountID` references in any Go source file

Plan 04 can proceed with: `infra/live/site.hcl`, `infra/live/management/scp/terragrunt.hcl`, `OPERATOR-GUIDE.md`, and the repo-root `km-config.yaml` migration.

---
*Phase: 65-four-account-config-model-separate-accounts-organization-scp-from-accounts-dns-parent-and-rename*
*Completed: 2026-05-02*
