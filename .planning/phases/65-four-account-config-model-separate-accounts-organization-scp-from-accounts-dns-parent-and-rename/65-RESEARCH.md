# Phase 65: Four-account config model — Research

**Researched:** 2026-05-01
**Domain:** Go config struct rename, Cobra CLI, Viper YAML, Terragrunt HCL, km doctor
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- New field: `accounts.organization` — AWS Organizations management account ID (SCP target). Optional. Blank → skip SCP deployment.
- New field: `accounts.dns_parent` — Route53 hosted-zone owner for `cfg.Domain` parent zone. Required when `cfg.Domain` is set and DNS bootstrapping is desired.
- Removed field: `accounts.management` — no back-compat alias. Hard cut.
- Existing fields unchanged: `accounts.application`, `accounts.terraform`.
- Go struct: drop `ManagementAccountID`, add `OrganizationAccountID` and `DNSParentAccountID` (string fields, omitempty YAML tags).
- All callers in `internal/app/cmd/*.go` migrate to the new fields. No transitional aliases.
- New: `KM_ACCOUNTS_ORGANIZATION`, `KM_ACCOUNTS_DNS_PARENT` — exported by `init.go` and `bootstrap.go`.
- Removed: `KM_ACCOUNTS_MANAGEMENT` — every reference deleted.
- `runBootstrap` SCP gate: change `if loadedCfg.ManagementAccountID != ""` to `if loadedCfg.OrganizationAccountID != ""`. Blank → skip SCP entirely.
- `runShowPrereqs` and `runShowSCP`: update fmt strings to use `OrganizationAccountID`.
- Dry-run output: rename "Management account" → "Organization account" and add "DNS parent account" line.
- `ensureSandboxHostedZone`: docstring/comments update only; profile string `klanker-management` stays unchanged.
- If `accounts.dns_parent` is blank, init logs `[warn]` and skips DNS delegation.
- `runInitDryRun` env-presence check: replace `KM_ACCOUNTS_MANAGEMENT` with `KM_ACCOUNTS_ORGANIZATION`, add `KM_ACCOUNTS_DNS_PARENT`.
- `infra/live/site.hcl`: add `organization` and `dns_parent`, drop `management`.
- `infra/live/management/scp/terragrunt.hcl`: `local.accounts.management` → `local.accounts.organization`.
- `km doctor` WARN when `accounts.organization` is blank: "SCP enforcement disabled — sandbox containment relies on IAM policies only". Severity: WARN.
- `km doctor` FAIL when `accounts.management` is present: "accounts.management has been split — rename to accounts.dns_parent and add accounts.organization (or leave blank to skip SCP)". Severity: FAIL (ERROR).
- Pre-1.0 hard rename. No alias period. Doctor error provides migration guidance.
- Tests: `config_test.go`, `bootstrap_test.go`, `configure_test.go`, `doctor_test.go`. No test references to `KM_ACCOUNTS_MANAGEMENT` or `ManagementAccountID` after this phase.
- Update `km-config.yaml` example. Update bootstrap help text and output strings. No CLAUDE.md changes needed.

### Claude's Discretion
- File-by-file ordering of edits within the implementation plan.
- Whether to split by concern (suggested: config struct + tests → bootstrap → init → doctor → HCL).
- Exact wording of doctor messages within the constraints.
- Whether to add `KM_ACCOUNTS_DNS_PARENT_PROFILE`. Lean: no — out of scope.

### Deferred Ideas (OUT OF SCOPE)
- AWS profile renames (`klanker-management`, `klanker-terraform`, `klanker-application`).
- `KM_ACCOUNTS_DNS_PARENT_PROFILE` env var for overriding the hardcoded profile name.
- Splitting `klanker-management` profile into two profiles.
- IAM permission boundary as SCP alternative.
- Backwards-compat alias for `accounts.management`.
- Auto-detection of single-account topology.
- Dry-run / migration helper (`km configure --migrate-management`).
</user_constraints>

---

## Summary

Phase 65 is a hard rename of a single config field (`accounts.management`) into two semantically distinct fields (`accounts.organization` for SCP, `accounts.dns_parent` for DNS). The codebase impact is 44 references across 13 files, all in well-understood code paths. No business logic changes — only field access renamings, env var renamings, and two new doctor checks.

The scope is fully enumerable from a grep audit. All 44 references outside `.planning/` and `.terragrunt-cache/` are identified below, categorized by plan concern. The `.terragrunt-cache/` directory is gitignored and carries no committed state — no cleanup action required.

**Primary recommendation:** Split into 4 plans: (1) config struct + tests, (2) bootstrap + init + uninit + create + info, (3) doctor checks, (4) HCL + docs. Plans 1 and 2 must complete before plan 3 (doctor uses the interface). Plans can otherwise proceed sequentially.

---

## Complete Touch List (verified by grep)

**44 total references** outside `.planning/`, `.git/`, `.terragrunt-cache/`:

### config.go (5 references — plan 1)
| File | Lines | What changes |
|------|-------|--------------|
| `internal/app/config/config.go` | 50–52 | Drop `ManagementAccountID` field + comment |
| `internal/app/config/config.go` | 214 | Drop `"accounts.management"` from viper key merge list; add `"accounts.organization"`, `"accounts.dns_parent"` |
| `internal/app/config/config.go` | 254 | Replace `ManagementAccountID: v.GetString("accounts.management")` with two new fields |

Two new fields to add to the struct (after `Domain`):
```go
// OrganizationAccountID is the AWS account ID for the AWS Organizations management account.
// SCP target. Maps to km-config.yaml key accounts.organization. Optional: blank skips SCP.
OrganizationAccountID string

// DNSParentAccountID is the AWS account ID where the parent Route53 hosted zone lives.
// Maps to km-config.yaml key accounts.dns_parent. Blank skips DNS delegation.
DNSParentAccountID string
```

Two new keys in the viper merge list: `"accounts.organization"`, `"accounts.dns_parent"`.

Two new assignments in cfg struct initialization:
```go
OrganizationAccountID: v.GetString("accounts.organization"),
DNSParentAccountID:    v.GetString("accounts.dns_parent"),
```

### bootstrap.go (11 references — plan 2)
| Lines | Current | After |
|-------|---------|-------|
| 287–288 | `ManagementAccountID == ""` → error "no management account" | `OrganizationAccountID == ""` — **delete this guard entirely** (organization is now optional; `runShowPrereqs` only makes sense if org is set; return a descriptive message about what org is for if blank) |
| 300 | `mgmtAccount := loadedCfg.ManagementAccountID` | `orgAccount := loadedCfg.OrganizationAccountID` |
| 482–484 | `mgmtAccount := loadedCfg.ManagementAccountID`; nil-check + error | Same pattern, variable renamed to `orgAccount`; guard becomes `if orgAccount == ""` return error about org account |
| 625 | `cfg.ManagementAccountID != ""` in `loadBootstrapConfig` | `cfg.OrganizationAccountID != ""` — also add `DNSParentAccountID` to the non-nil check |
| 654 | `"Management account: %s\n"` | `"Organization account: %s\n"` (prints OrganizationAccountID) + add `"DNS parent account: %s\n"` (prints DNSParentAccountID) |
| 694–706 | `if loadedCfg.ManagementAccountID != ""` dry-run SCP block | `if loadedCfg.OrganizationAccountID != ""` |
| 705 | `"set accounts.management to enable SCP"` | `"set accounts.organization to enable SCP"` |
| 728–730 | `if ManagementAccountID != ""` + `os.Setenv("KM_ACCOUNTS_MANAGEMENT", ...)` | `if OrganizationAccountID != ""` + `os.Setenv("KM_ACCOUNTS_ORGANIZATION", ...)`; add separate `os.Setenv("KM_ACCOUNTS_DNS_PARENT", loadedCfg.DNSParentAccountID)` (always exported, may be empty) |

**Critical behavior detail at line 287–288:** `runShowPrereqs` currently errors if ManagementAccountID is empty because showing IAM prereqs without an org account ID is useless. After rename, the guard should change to: if `OrganizationAccountID == ""` → return a message explaining "accounts.organization not set — SCP deployment disabled. Set it to enable org-level sandbox containment." This is NOT a fatal error; it's a descriptive skip. Matches the intent of making org optional.

### init.go (3 references — plan 2)
| Lines | Change |
|-------|--------|
| 258–259 | `"KM_ACCOUNTS_MANAGEMENT": cfg.ManagementAccountID != ""` → two entries: `"KM_ACCOUNTS_ORGANIZATION": cfg.OrganizationAccountID != ""` and `"KM_ACCOUNTS_DNS_PARENT": cfg.DNSParentAccountID != ""` |
| 301–302 | `if cfg.ManagementAccountID != "" && os.Getenv("KM_ACCOUNTS_MANAGEMENT") == ""` → split into two separate if-blocks for organization and dns_parent |
| 1462–1465 (comment) | `ensureSandboxHostedZone` docstring: update "management account" semantic reference to `accounts.dns_parent`. Add explicit note that `klanker-management` AWS profile name is unchanged. |

**DNS gate change (locked by CONTEXT.md):** Add explicit gate before `ensureSandboxHostedZone` call at init.go:326–343: if `cfg.DNSParentAccountID == ""`, log `[warn]` and skip the zone setup block. Currently the code already skips if `cfg.Domain == ""` — the new gate is: `cfg.DNSParentAccountID == ""` also skips (with a different warning message).

### create.go (2 references — plan 2)
| Lines | Change |
|-------|--------|
| 359–360 | `ManagementAccountID != ""` + `KM_ACCOUNTS_MANAGEMENT` → split: export `KM_ACCOUNTS_ORGANIZATION` from `OrganizationAccountID` and `KM_ACCOUNTS_DNS_PARENT` from `DNSParentAccountID` |

### uninit.go (2 references — plan 2)
| Lines | Change |
|-------|--------|
| 89–90 | Same pattern as init.go — split into two env exports |

### info.go (1 reference — plan 2)
| Lines | Change |
|-------|--------|
| 57 | `"  Management:       %s\n"` using `ManagementAccountID` → two lines: `"  Organization:     %s\n"` (OrganizationAccountID) and `"  DNS parent:       %s\n"` (DNSParentAccountID) |

### configure.go (8 references — plan 2)
| Location | Change |
|----------|--------|
| `accountsConfig` struct (line 37–41) | Drop `Management string`; add `Organization string` (yaml: `"organization"`) and `DNSParent string` (yaml: `"dns_parent"`) |
| `managementAcct` variable (line 59) | Split into `organizationAcct` and `dnsParentAcct` |
| `runConfigure` signature (line 120) | Replace `managementAcct` param with `organizationAcct, dnsParentAcct` |
| `--management-account` flag (line 90–91) | Replace with `--organization-account` and `--dns-parent-account` |
| Non-interactive required fields validation (line 130–131) | `--management-account` required → `--dns-parent-account` required (org is optional) |
| Interactive wizard prompt (line 160) | Replace with two prompts |
| DNS delegation message (line 226) | Update from `management account (%s)` to `DNS parent account (%s)` using `dnsParentAcct` |
| `accountsConfig` population (line 234) | `Organization: organizationAcct`, `DNSParent: dnsParentAcct` |

**Important:** `--management-account` in `--non-interactive` validation was previously required. After the rename, only `--dns-parent-account` is required (or optional if domain is not set). `--organization-account` is always optional.

### doctor.go (5 references — plan 3)
| Lines | Change |
|-------|--------|
| 150 | `GetManagementAccountID() string` in `DoctorConfigProvider` interface → replace with `GetOrganizationAccountID() string` and `GetDNSParentAccountID() string` |
| 174 | `appConfigAdapter.GetManagementAccountID()` → `GetOrganizationAccountID()` + `GetDNSParentAccountID()` |
| 241 | `{"management_account_id", cfg.GetManagementAccountID()}` in `checkConfig` required list → **remove this entry** (organization is now optional, DNS parent is optional too — neither should be in required list) |
| 2032 | `mgmtAccountID := cfg.GetManagementAccountID()` in `initRealDepsWithExisting` → `orgAccountID := cfg.GetOrganizationAccountID()` (controls Organizations API assume-role) |

**Two new check functions to add (plan 3):**

```go
// checkOrganizationAccountBlank warns when accounts.organization is blank.
func checkOrganizationAccountBlank(cfg DoctorConfigProvider) CheckResult {
    if cfg.GetOrganizationAccountID() != "" {
        return CheckResult{
            Name:    "SCP Enforcement Config",
            Status:  CheckOK,
            Message: fmt.Sprintf("accounts.organization = %s (SCP enabled)", cfg.GetOrganizationAccountID()),
        }
    }
    return CheckResult{
        Name:    "SCP Enforcement Config",
        Status:  CheckWarn,
        Message: "SCP enforcement disabled — sandbox containment relies on IAM policies only",
        Remediation: "Set accounts.organization in km-config.yaml to enable org-level SCP sandbox containment",
    }
}

// checkLegacyManagementField detects a stale accounts.management key in km-config.yaml.
// Returns CheckError when found; CheckOK when absent.
func checkLegacyManagementField() CheckResult {
    // reads km-config.yaml raw YAML to detect presence of accounts.management key
    // (viper silently ignores unknown keys after the rename, so we must read raw YAML)
    ...
    return CheckResult{
        Name:        "Legacy Config Field",
        Status:      CheckError,
        Message:     "accounts.management has been split — rename to accounts.dns_parent and add accounts.organization (or leave blank to skip SCP)",
        Remediation: "Edit km-config.yaml: replace accounts.management with accounts.dns_parent (same value) and add accounts.organization (blank for single-account topology)",
    }
}
```

Wire both into `buildChecks`: `checkOrganizationAccountBlank` takes only `cfg` (no AWS calls — runs always). `checkLegacyManagementField` reads the km-config.yaml path — also no AWS calls.

**Critical implementation note for `checkLegacyManagementField`:** Viper silently ignores unknown YAML keys. After the rename, if an operator has `accounts.management` in their km-config.yaml, `config.Load()` will produce a config where `OrganizationAccountID == ""` and `DNSParentAccountID == ""`. No error is raised by the loader. The doctor check MUST read the raw YAML file to detect the legacy key. Use `findKMConfigPath()` (exported from `bootstrap.go`) or a similar path-finding function. Read the file with `os.ReadFile` + `gopkg.in/yaml.v3`. The check should be CheckSkipped when the file doesn't exist (fresh install before configure), CheckOK when `accounts.management` is absent, CheckError when present.

### HCL files (2 references — plan 4)
| File | Change |
|------|--------|
| `infra/live/site.hcl` (line 12) | Drop `management = get_env("KM_ACCOUNTS_MANAGEMENT", "")` from accounts block; add `organization = get_env("KM_ACCOUNTS_ORGANIZATION", "")` and `dns_parent = get_env("KM_ACCOUNTS_DNS_PARENT", "")` |
| `infra/live/management/scp/terragrunt.hcl` (line 28) | `local.accounts.management` → `local.accounts.organization` in the assume_role.role_arn |

### Docs (2 references — plan 4)
| File | Location | Change |
|------|----------|--------|
| `OPERATOR-GUIDE.md` | Line 109 (env var table) | `KM_ACCOUNTS_MANAGEMENT` → `KM_ACCOUNTS_ORGANIZATION` and `KM_ACCOUNTS_DNS_PARENT` (two rows) |
| `OPERATOR-GUIDE.md` | Line 186 (shell export example) | `export KM_ACCOUNTS_MANAGEMENT=...` → `export KM_ACCOUNTS_ORGANIZATION=...` + `export KM_ACCOUNTS_DNS_PARENT=...` |
| `OPERATOR-GUIDE.md` | Sections 1 and 2 | Update "Management account" topology tables and guide text to reflect new field names |
| `km-config.yaml` (operator's live config at repo root) | Line 6 | `management: "481723467561"` → `organization: ""` + `dns_parent: "481723467561"` |

---

## Architecture Patterns

### checkConfig Required List — Important Change

Currently `checkConfig` lists `management_account_id` as a required field (doctor.go:241). After the rename, **neither `organization_account_id` nor `dns_parent_account_id` should be in the required list** — both are optional. The `checkOrganizationAccountBlank` WARN check handles the no-org case explicitly. Remove `management_account_id` from the required fields slice; do NOT add organization or dns_parent to required.

### Doctor Check Pattern

All existing checks follow this pattern — closures appended to the `checks` slice in `buildChecks`:

```go
checks = append(checks, func(ctx context.Context) CheckResult {
    return checkFoo(ctx, someClient, someParam)
})
```

For the two new checks that need no AWS client:
```go
// Organization account blank check — no AWS client, reads cfg only
checks = append(checks, func(_ context.Context) CheckResult {
    return checkOrganizationAccountBlank(cfg)
})

// Legacy field check — reads km-config.yaml raw YAML, no AWS client
checks = append(checks, func(_ context.Context) CheckResult {
    return checkLegacyManagementField()
})
```

Place the org-blank check BEFORE the SCP check (which checks whether the SCP is actually deployed to the app account). Place the legacy field check AFTER the config check near the top of `buildChecks`.

### DoctorConfigProvider Interface Expansion

Two new methods on the interface and the three implementations:

```go
// DoctorConfigProvider interface — add:
GetOrganizationAccountID() string
GetDNSParentAccountID() string

// appConfigAdapter — add:
func (a *appConfigAdapter) GetOrganizationAccountID() string { return a.cfg.OrganizationAccountID }
func (a *appConfigAdapter) GetDNSParentAccountID() string    { return a.cfg.DNSParentAccountID }

// testConfig (doctor_test.go) — add:
func (c *testConfig) GetOrganizationAccountID() string { return c.orgAcct }
func (c *testConfig) GetDNSParentAccountID() string    { return c.dnsParentAcct }

// testDoctorConfig (doctor_test.go) — add:
func (c *testDoctorConfig) GetOrganizationAccountID() string { return c.orgAcct }
func (c *testDoctorConfig) GetDNSParentAccountID() string    { return c.dnsParentAcct }
```

Remove `GetManagementAccountID()` from the interface and all implementations.

### configure.go Topology Detection

The `twoAccount` detection at configure.go:219 (`terraformAcct == applicationAcct`) is unchanged — it still controls whether DNS delegation guidance prints. After the rename, the DNS delegation message at line 226 uses `dnsParentAcct` (the new name for what was `managementAcct`). The guidance text updates from "management account" to "DNS parent account" since that's the accurate semantic name.

### loadBootstrapConfig Detection

`loadBootstrapConfig` at bootstrap.go:625 checks whether the injected `cfg` is non-nil and "real" (has account IDs or domain set) to avoid re-loading from disk. The current check is `cfg.ManagementAccountID != "" || cfg.ApplicationAccountID != "" || cfg.Domain != ""`. After rename, use `cfg.OrganizationAccountID != "" || cfg.DNSParentAccountID != "" || cfg.ApplicationAccountID != "" || cfg.Domain != ""`.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead |
|---------|-------------|-------------|
| Unknown YAML key detection | Custom YAML parser | `gopkg.in/yaml.v3` — already imported in configure.go and configure_test.go |
| Raw YAML reading in doctor check | Subprocess or regex grep | `os.ReadFile` + `yaml.Unmarshal` into `map[string]interface{}` |

---

## Common Pitfalls

### Pitfall 1: configure.go `--management-account` required flag

`runConfigure` in non-interactive mode currently requires `--management-account`. After rename, only `--dns-parent-account` should be required for the three-account bootstrap flow. `--organization-account` is always optional. Failing to update the required-flag list breaks `km configure --non-interactive` for single-account users.

**How to avoid:** Update the missing-flags slice at configure.go:130–131. Only `--dns-parent-account` should be required if a domain is set (or if DNS bootstrapping is desired). Given the locked decision that org is optional, the simplest approach: make both new flags optional in non-interactive mode too and document that org being blank means no SCP.

### Pitfall 2: checkConfig still listing management_account_id as required

`checkConfig` (doctor.go:241) currently validates that `management_account_id` is non-empty. If this entry is left unchanged (or converted to `organization_account_id`), single-account operators running `km doctor` with a blank `accounts.organization` will get a FAIL instead of the intended WARN. The required list must drop this entry entirely; the WARN is handled by the new `checkOrganizationAccountBlank` check instead.

**How to avoid:** When editing doctor.go:241, delete the entry rather than renaming it.

### Pitfall 3: initRealDepsWithExisting still uses GetManagementAccountID for Organizations API assume-role

`initRealDepsWithExisting` at doctor.go:2032 uses `GetManagementAccountID()` to build the `km-org-admin` role ARN for the Organizations API client. If this reference is missed, `km doctor` will not assume the org-admin role, causing the SCP check to demote to WARN (access denied fallback). It must be updated to `GetOrganizationAccountID()`.

**How to avoid:** When renaming the interface method, search all usages, including the `initRealDepsWithExisting` function.

### Pitfall 4: Viper silently drops unknown YAML keys

After the rename, an operator with `accounts.management` in km-config.yaml will silently get `OrganizationAccountID == ""` and `DNSParentAccountID == ""` — no error, no warning from the loader. This is expected behavior but means:
- `checkOrganizationAccountBlank` will fire WARN (correct — they have no SCP)
- `checkLegacyManagementField` will fire ERROR (correct — migration needed)
- All `km init`, `km bootstrap`, `km create` commands will silently skip org-dependent paths

The doctor checks together give the operator the right signal. The legacy field check is the critical companion to the blank-org warn.

### Pitfall 5: configure_test.go TestConfigureThreeAccountTopology

The three-account topology test passes `--management-account 111111111111` and checks that DNS delegation guidance is shown. After rename, this test must pass `--dns-parent-account 111111111111` instead (organization account is now optional and separate). The topology-detection logic in `runConfigure` has not changed — it's still based on `terraformAcct == applicationAcct` — but the DNS delegation message references the new field name.

### Pitfall 6: operator's live km-config.yaml at repo root

The file `km-config.yaml` at repo root contains `accounts.management: "481723467561"`. This is the operator's live config. It must be updated as part of plan 4 (docs/cleanup). If forgotten, `km doctor` will emit the legacy-field ERROR on every `km doctor` run until fixed.

---

## Test Patterns

### config_test.go pattern (plan 1)

Tests use `writeKMConfig(t, dir, yaml_string)` + `os.Chdir(dir)` to create a temporary km-config.yaml and load it. The pattern is purely in-process with no AWS calls. New tests follow identical structure:

```go
func TestLoadOrganizationAndDNSParentFields(t *testing.T) {
    dir := t.TempDir()
    writeKMConfig(t, dir, `
accounts:
  organization: "111111111111"
  dns_parent: "222222222222"
  application: "333333333333"
  terraform: "333333333333"
`)
    // chdir + config.Load() + assert OrganizationAccountID == "111111111111"
    // assert DNSParentAccountID == "222222222222"
}

func TestLoadBlankOrganizationIsValid(t *testing.T) {
    // accounts.organization absent → OrganizationAccountID == ""
    // no error from Load()
}
```

### bootstrap_test.go pattern (plan 2)

Tests use `runBootstrapCmd(t, cfg, dryRun, applyFn)` with a `*config.Config` struct literal. No AWS calls. New tests:

```go
// TestBootstrapDryRunShowsOrganizationAccount — cfg.OrganizationAccountID set → dry-run shows "Organization account:"
// TestBootstrapDryRunNoOrganizationAccount — cfg.OrganizationAccountID == "" → "SKIPPED" text appears
// TestBootstrapSCPApplyPath_OrganizationSet — non-dry-run with OrganizationAccountID set → apply fires
// TestBootstrapSCPSkipped_OrganizationBlank — non-dry-run with OrganizationAccountID == "" → apply NOT called
```

Existing tests `TestBootstrapDryRunShowsSCP` and `TestBootstrapDryRunNoManagementAccount` must be updated: rename field in struct literal from `ManagementAccountID` to `OrganizationAccountID`.

### configure_test.go pattern (plan 2)

Tests use `buildKM(t)` to build the km binary, run it via exec.Command with `--non-interactive` flags, then parse the output YAML. New tests:

```go
// TestConfigureWritesOrganizationAndDNSParent — pass --organization-account, --dns-parent-account flags
// assert km-config.yaml has accounts.organization and accounts.dns_parent
// assert accounts.management is ABSENT
```

Existing tests pass `--management-account 111111111111` and check `accounts["management"]`. These must be updated to pass `--dns-parent-account 111111111111` and check `accounts["dns_parent"]`.

### doctor_test.go pattern (plan 3)

The doctor test file is in `package cmd` (not `cmd_test`) — it tests unexported functions directly. The two new check functions (`checkOrganizationAccountBlank`, `checkLegacyManagementField`) are unit-tested directly. New tests:

```go
// TestCheckOrganizationAccountBlank_BlankReturnsWarn
func TestCheckOrganizationAccountBlank_BlankReturnsWarn(t *testing.T) {
    cfg := &testDoctorConfig{orgAcct: ""}
    result := checkOrganizationAccountBlank(cfg)
    if result.Status != CheckWarn {
        t.Errorf("expected WARN, got %s", result.Status)
    }
    // assert message contains "IAM policies only"
}

// TestCheckOrganizationAccountBlank_SetReturnsOK
// TestCheckLegacyManagementField_FieldPresent_ReturnsError
// TestCheckLegacyManagementField_FieldAbsent_ReturnsOK
// TestCheckLegacyManagementField_NoConfigFile_ReturnsSkipped
```

For `checkLegacyManagementField`, tests write a temp km-config.yaml with/without `accounts.management` and verify the check result. The function needs the km-config path — inject it via parameter or use `findKMConfigPath()` (which is already exported from bootstrap.go into the same package).

**`testConfig` and `testDoctorConfig` structs in doctor_test.go must be updated** to satisfy the new interface: remove `GetManagementAccountID()`, add `GetOrganizationAccountID()` and `GetDNSParentAccountID()`.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go standard `testing` (no external test runner) |
| Config file | none — built into `go test` |
| Quick run command | `go test ./internal/app/config/... ./internal/app/cmd/... -run TestLoad\|TestBootstrap\|TestConfigure\|TestCheck -v -count=1` |
| Full suite command | `go test ./internal/app/...` |

### Phase Requirements → Test Map

| Behavior | Test Type | Automated Command |
|----------|-----------|-------------------|
| `accounts.organization` loads from km-config.yaml | unit | `go test ./internal/app/config/... -run TestLoadOrganizationAndDNSParentFields` |
| `accounts.dns_parent` loads from km-config.yaml | unit | `go test ./internal/app/config/... -run TestLoadOrganizationAndDNSParentFields` |
| Blank `accounts.organization` is valid (no error) | unit | `go test ./internal/app/config/... -run TestLoadBlankOrganizationIsValid` |
| `accounts.management` absent from config struct | unit | existing tests compile-fail if `ManagementAccountID` referenced |
| bootstrap dry-run shows "Organization account:" | unit | `go test ./internal/app/cmd/... -run TestBootstrapDryRunShowsOrganizationAccount` |
| bootstrap dry-run skips SCP when org blank | unit | `go test ./internal/app/cmd/... -run TestBootstrapDryRunNoOrganizationAccount` |
| bootstrap non-dry-run invokes terragrunt with org set | unit | `go test ./internal/app/cmd/... -run TestBootstrapSCPApplyPath` |
| bootstrap non-dry-run skips terragrunt when org blank | unit | `go test ./internal/app/cmd/... -run TestBootstrapSCPSkipped_OrganizationBlank` |
| `km configure --non-interactive` writes org + dns_parent | integration (km binary) | `go test ./internal/app/cmd/... -run TestConfigureWritesOrganizationAndDNSParent` |
| doctor WARN when org blank | unit | `go test ./internal/app/cmd/... -run TestCheckOrganizationAccountBlank_BlankReturnsWarn` |
| doctor OK when org set | unit | `go test ./internal/app/cmd/... -run TestCheckOrganizationAccountBlank_SetReturnsOK` |
| doctor ERROR when `accounts.management` present in YAML | unit | `go test ./internal/app/cmd/... -run TestCheckLegacyManagementField_FieldPresent` |
| doctor OK when `accounts.management` absent | unit | `go test ./internal/app/cmd/... -run TestCheckLegacyManagementField_FieldAbsent` |
| doctor SKIP when no km-config.yaml | unit | `go test ./internal/app/cmd/... -run TestCheckLegacyManagementField_NoConfigFile` |
| Zero remaining `ManagementAccountID` references | integration (grep) | `grep -rn ManagementAccountID ./internal ./pkg ./cmd ./infra \| wc -l` → must be 0 |
| Zero remaining `KM_ACCOUNTS_MANAGEMENT` references | integration (grep) | `grep -rn KM_ACCOUNTS_MANAGEMENT ./internal ./pkg ./cmd ./infra \| wc -l` → must be 0 |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/config/... ./internal/app/cmd/...`
- **Per wave merge:** `go test ./internal/app/...`
- **Phase gate:** Full suite green + grep audit zero-count before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `internal/app/cmd/doctor_test.go` — add `GetOrganizationAccountID()` and `GetDNSParentAccountID()` to `testConfig` and `testDoctorConfig` stubs (must happen in plan 1 along with config struct, before plan 3 touches doctor)
- [ ] New test functions listed in test map above — all in existing test files, no new files needed

---

## Scope Confirmation

**Verified by grep: 44 references** outside `.planning/`, `.git/`, `.terragrunt-cache/`. Broken out by plan:

| Plan | Files | References |
|------|-------|------------|
| Plan 1: config struct + tests | `config.go`, `config_test.go`, `doctor_test.go` (stub methods) | 9 |
| Plan 2: bootstrap + init + uninit + create + info + configure (+ tests) | `bootstrap.go`, `init.go`, `uninit.go`, `create.go`, `info.go`, `configure.go`, `bootstrap_test.go`, `configure_test.go` | 25 |
| Plan 3: doctor + tests | `doctor.go`, `doctor_test.go` | 6 |
| Plan 4: HCL + docs + live config | `site.hcl`, `scp/terragrunt.hcl`, `OPERATOR-GUIDE.md`, `km-config.yaml` | 4 |

**Terragrunt cache:** `.terragrunt-cache/` is gitignored (confirmed in `.gitignore`). The cached `terragrunt.hcl` inside it is auto-generated at apply time and will be regenerated from the canonical `site.hcl` on the next `terragrunt apply`. No manual purge needed.

**No shell scripts or Makefile references** — grep found zero results for `KM_ACCOUNTS_MANAGEMENT` in scripts, Makefile, or CI configs.

**Operator's live `km-config.yaml`** at repo root currently contains `accounts.management: "481723467561"`. This file is listed in the operator's README as something to add to `.gitignore`, but it IS committed in this repo (confirmed by file existence). Plan 4 must update it to `accounts.dns_parent: "481723467561"` and `accounts.organization: ""`.

---

## State of the Art

| Old | New | Impact |
|-----|-----|--------|
| `accounts.management` (conflated) | `accounts.organization` (SCP) + `accounts.dns_parent` (DNS) | Single-account operators no longer blocked |
| `KM_ACCOUNTS_MANAGEMENT` | `KM_ACCOUNTS_ORGANIZATION` + `KM_ACCOUNTS_DNS_PARENT` | Env var set must change in any shell scripts operators maintain |
| SCP skip gated on presence of management account | SCP skip gated on `accounts.organization` being blank | New topology class: dns_parent set but org blank |

---

## Open Questions

1. **`runShowPrereqs` guard at bootstrap.go:287–288**
   - What we know: currently errors hard if `ManagementAccountID == ""` because printing IAM prereqs for an unknown account is useless
   - What's unclear: after rename, should `runShowPrereqs` print a "SCP disabled — no organization account" message instead of an error, or should the subcommand be hidden when org is blank?
   - Recommendation: change to a non-error message (print explanation and return nil) — consistent with the overall "org blank is valid" design

2. **`configure.go` required flags for `--non-interactive`**
   - What we know: currently `--management-account` is required; after rename it maps to `dns_parent`
   - What's unclear: should `--dns-parent-account` be required, or optional (with a warning if domain is set but dns_parent is blank)?
   - Recommendation: make `--dns-parent-account` optional in non-interactive mode; let the doctor check surface the missing config rather than blocking configure

3. **`checkLegacyManagementField` path discovery**
   - What we know: `findKMConfigPath()` exists in `bootstrap.go` (same package as `doctor.go`)
   - What's unclear: should the check function accept a path parameter for testability, or call `findKMConfigPath()` directly?
   - Recommendation: accept an optional path parameter (empty = use `findKMConfigPath()`), consistent with how other doctor checks accept injected clients for testability

---

## Sources

### Primary (HIGH confidence)
- Direct code inspection: `internal/app/config/config.go` — struct fields, viper load pattern, merge key list
- Direct code inspection: `internal/app/cmd/bootstrap.go` — all 11 reference sites confirmed
- Direct code inspection: `internal/app/cmd/doctor.go` — interface, adapter, check pattern, buildChecks wiring
- Direct code inspection: `infra/live/site.hcl`, `infra/live/management/scp/terragrunt.hcl` — HCL structure confirmed
- Direct code inspection: all test files — test patterns confirmed, stub interfaces confirmed
- Grep audit: 44 references counted and categorized

### Secondary (MEDIUM confidence)
- `.gitignore` confirms `.terragrunt-cache/` is not committed — cache cleanup not required
- `km-config.yaml` at repo root — operator's live config confirmed to need update

## Metadata

**Confidence breakdown:**
- Touch list completeness: HIGH — grep audit done across all source trees
- Architecture patterns: HIGH — read actual source code, not inferred
- Pitfalls: HIGH — identified from actual code reading, not speculation
- Test patterns: HIGH — all test files read directly

**Research date:** 2026-05-01
**Valid until:** 2026-06-01 (stable Go codebase, no external dependencies to track)
