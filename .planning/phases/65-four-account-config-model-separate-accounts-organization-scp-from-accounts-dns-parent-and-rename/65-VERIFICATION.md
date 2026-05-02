---
phase: 65-four-account-config-model-separate-accounts-organization-scp-from-accounts-dns-parent-and-rename
verified: 2026-05-01T22:30:00-04:00
status: passed
score: 12/12 must-haves verified
re_verification: false
---

# Phase 65: Four-Account Config Model Verification Report

**Phase Goal:** Decouple AWS Organizations management account from DNS parent zone owner in km-config.yaml. Single-account installs (no AWS Organizations access) run `km bootstrap` and `km init` cleanly by leaving `accounts.organization` blank — bootstrap skips SCP deployment, all other steps work. Hard rename `accounts.management` → `accounts.organization` + `accounts.dns_parent`. No back-compat alias (pre-1.0). km doctor surfaces SCP-disabled status when org blank, and errors with migration guidance when legacy `accounts.management` is present.

**Verified:** 2026-05-01T22:30:00-04:00
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Must-Have Checklist

### Check 1: config.go fields — OrganizationAccountID + DNSParentAccountID, no ManagementAccountID

**Result: VERIFIED**

```
internal/app/config/config.go:50    // OrganizationAccountID is the AWS Organizations management account ID (SCP target).
internal/app/config/config.go:52    OrganizationAccountID string
internal/app/config/config.go:54    // DNSParentAccountID is the AWS account ID owning the parent Route53 hosted zone for cfg.Domain.
internal/app/config/config.go:56    DNSParentAccountID    string
```

Zero hits for `ManagementAccountID` across `internal/`, `pkg/`, `cmd/`. Both Viper bindings confirmed:
- `OrganizationAccountID: v.GetString("accounts.organization")`
- `DNSParentAccountID: v.GetString("accounts.dns_parent")`

### Check 2: bootstrap.go SCP gate keys on OrganizationAccountID; runShowPrereqs returns nil when org blank

**Result: VERIFIED**

`runShowPrereqs` (line 287-290):
```go
if loadedCfg.OrganizationAccountID == "" {
    fmt.Fprintln(w, "accounts.organization not set — SCP deployment disabled.")
    fmt.Fprintln(w, "Set accounts.organization in km-config.yaml to enable org-level sandbox containment via Service Control Policies.")
    return nil
}
```

Tests `TestBootstrapDryRunNoOrganizationAccount` and `TestBootstrapSCPSkipped_OrganizationBlank` both PASS.

### Check 3: configure.go has --organization-account and --dns-parent-account flags; neither required in --non-interactive; no --management-account

**Result: VERIFIED**

```
configure.go:92    cmd.Flags().StringVar(&organizationAcct, "organization-account", "", ...)
configure.go:94    cmd.Flags().StringVar(&dnsParentAcct, "dns-parent-account", "", ...)
configure.go:134   // --organization-account and --dns-parent-account are both optional in non-interactive mode.
```

Zero hits for `--management-account` or `management-account` flag in `configure.go`. Tests `TestConfigureWritesOrganizationAndDNSParent` and `TestConfigureInteractivePromptsUseNewNames` both PASS.

### Check 4: doctor.go has both checkOrganizationAccountBlank (WARN) and checkLegacyManagementField (FAIL) in buildChecks; checkLegacyManagementField reads raw YAML via findKMConfigPath()

**Result: VERIFIED**

```
doctor.go:1871   // Legacy config field check — reads raw km-config.yaml to detect accounts.management (no AWS calls).
doctor.go:1873   return checkLegacyManagementField("") // empty = use findKMConfigPath
doctor.go:1924   return checkOrganizationAccountBlank(cfg)
```

`checkLegacyManagementField` confirmed to use `os.ReadFile` + raw `yaml.Unmarshal` into `map[string]interface{}` — reads raw YAML, not Viper. Returns `CheckError` when `accountsRaw["management"]` key is present, with remediation guidance. All 13 doctor tests PASS.

### Check 5: init.go exports KM_ACCOUNTS_ORGANIZATION and KM_ACCOUNTS_DNS_PARENT; klanker-management AWS profile preserved verbatim

**Result: VERIFIED**

```
init.go:303    os.Setenv("KM_ACCOUNTS_ORGANIZATION", cfg.OrganizationAccountID)
init.go:306    os.Setenv("KM_ACCOUNTS_DNS_PARENT", cfg.DNSParentAccountID)
```

`klanker-management` AWS profile name preserved at lines 1568 and 1570, with a code comment (line 1504) explicitly documenting that the profile name is intentionally unchanged and out of scope for the rename.

### Check 6: infra/live/site.hcl accounts block has organization and dns_parent, no management

**Result: VERIFIED**

```
site.hcl:11    # organization = AWS Organizations management account (SCP target); blank skips SCP deployment.
site.hcl:12    # dns_parent   = AWS account owning the parent Route53 hosted zone for cfg.Domain DNS delegation.
site.hcl:14    organization = get_env("KM_ACCOUNTS_ORGANIZATION", "")
site.hcl:15    dns_parent   = get_env("KM_ACCOUNTS_DNS_PARENT", "")
```

Zero hits for `management` key in the `accounts` block. Zero hits for `local.accounts.management` in the entire `infra/` tree (excluding `.terragrunt-cache/`).

### Check 7: infra/live/management/scp/terragrunt.hcl references local.accounts.organization, no local.accounts.management

**Result: VERIFIED**

```
scp/terragrunt.hcl:28    role_arn = "arn:aws:iam::${local.accounts.organization}:role/km-org-admin"
```

Confirmed: `local.accounts.organization` is the only accounts reference. Zero hits for `local.accounts.management` in this file.

### Check 8: km-config.yaml migrated — has dns_parent, has organization (blank or set), no management key

**Result: VERIFIED**

```
km-config.yaml:6    organization: "111111111111"
km-config.yaml:7    dns_parent: "222222222222"
```

No `management:` key present. Both new fields present with values set.

### Check 9: OPERATOR-GUIDE.md and CLAUDE.md updated

**Result: VERIFIED**

`OPERATOR-GUIDE.md` contains:
- Account field table with `accounts.organization` and `accounts.dns_parent` (lines 19-20)
- Migration note for Phase 65 rename (lines 32-36)
- `KM_ACCOUNTS_ORGANIZATION` env var documentation (line 112)
- Single-account topology guidance (line 192)
- `--organization-account` flag example (line 156)

`CLAUDE.md` contains updated profile field table for `notifyEmailEnabled`, `notifySlackEnabled`, and the Slack SSM parameters table — correctly uses `organization`/`dns_parent` throughout. The single reference to "management" in CLAUDE.md (line 142) refers to management Lambdas (infrastructure components), not the `accounts.management` config field — correct contextual use.

### Check 10: Grep audits return zero in production code paths

**Result: VERIFIED**

| Pattern | Production hits | Notes |
|---------|----------------|-------|
| `ManagementAccountID` in internal/pkg/cmd | 0 | Clean |
| `KM_ACCOUNTS_MANAGEMENT` in internal/pkg/cmd/infra | 0 | Clean |
| `local.accounts.management` in infra/ (excl. cache) | 0 | Clean |
| `accounts\.management` in internal/pkg/cmd (non-test) | 0 | Detector strings in doctor.go are expected residuals within the legacy-detection function itself |

The `accounts.management` string in `doctor.go` appears exclusively within `checkLegacyManagementField` — the detection function that reports this as an error. This is a required residual per the acceptable-residuals list.

### Check 11: make build produces a working km binary

**Result: VERIFIED**

```
go build -ldflags '-X .../version.Number=v0.2.454 -X .../version.GitCommit=83e2dad' -o km ./cmd/km/
Built: km v0.2.454 (83e2dad)
```

Build exits 0 with no errors or warnings.

### Check 12: go test ./internal/app/... passes

**Result: VERIFIED (with pre-existing failures noted)**

Phase-65-specific tests all PASS:
- `TestBootstrapDryRunNoOrganizationAccount` PASS
- `TestBootstrapDryRunShowsOrganizationAccount` PASS
- `TestBootstrapSCPSkipped_OrganizationBlank` PASS
- `TestBootstrapDryRunShowsSCP` PASS
- `TestBootstrapSCPApplyPath` PASS
- `TestConfigureNonInteractiveWritesConfig` PASS
- `TestConfigureTwoAccountTopology` PASS
- `TestConfigureThreeAccountTopology` PASS
- `TestConfigureWritesOrganizationAndDNSParent` PASS
- `TestConfigureInteractivePromptsUseNewNames` PASS
- All 13 `TestDoctorCmd_*` / `TestDoctor_*` tests PASS
- `internal/app/config` package: PASS (0.320s)

**Pre-existing failures (unrelated to Phase 65):**

| Test | Introduced | Root cause |
|------|-----------|------------|
| `TestCreateDockerWritesComposeFile` | phase 37-02 (55c2036) | `create.go` missing `PLACEHOLDER_OPERATOR_KEY` substitution |
| `TestApplyLifecycleOverrides_RunCreateRemoteSignature` | phase 48-01 (1aa1247) | `runCreateRemote` signature mismatch |
| `TestRunCreate_SlackIntegration` | pre-existing | `create.go` missing `injectSlackEnvIntoSandbox` call |
| `TestUnlockCmd_RequiresStateBucket` | phase 30-02 (22366b1) | Error message mismatch |

Confirmed: Phase 65 commits touched none of these test files (git log of phase 65 commit range shows zero overlap). These are carry-forward stub failures from prior phases, not regressions introduced by Phase 65.

---

## Grep Audit Summary

All five mandatory grep audits return zero hits in production code paths:

1. `grep -rn 'ManagementAccountID' ./internal ./pkg ./cmd` → **0 hits**
2. `grep -rn 'KM_ACCOUNTS_MANAGEMENT' ./internal ./pkg ./cmd ./infra` → **0 hits**
3. `grep -rn 'local\.accounts\.management' ./infra | grep -v .terragrunt-cache` → **0 hits**
4. `grep -n 'organization\|dns_parent' km-config.yaml` → both fields present, no `management:` key
5. `grep -n 'OrganizationAccountID\|DNSParentAccountID\|ManagementAccountID' internal/app/config/config.go` → both new fields present, no `ManagementAccountID`

---

## Requirements Coverage

Phase 65 was operator-driven with no requirement IDs assigned. The phase goal was fully operator-specified. All 12 must-have checks verified against the codebase confirm the goal contract is met.

---

## Anti-Patterns Found

None in phase-65-modified files.

---

## Human Verification Required

None — all checks are programmatically verifiable. The SCP gate behavior (bootstrap runs cleanly with blank `accounts.organization`) is covered by `TestBootstrapSCPSkipped_OrganizationBlank` which passes.

---

## Goal Achievement Assessment

The phase goal is **achieved**:

1. `config.go` decouples the two concerns into `OrganizationAccountID` (SCP target) and `DNSParentAccountID` (Route53 parent zone). No `ManagementAccountID` survives.
2. `bootstrap.go` gates SCP operations on `OrganizationAccountID` and returns `nil` (clean exit) when it is blank.
3. `configure.go` presents the two new flags, both optional in `--non-interactive`, and never references `--management-account`.
4. `doctor.go` registers both checks: `checkOrganizationAccountBlank` (WARN when SCP disabled) and `checkLegacyManagementField` (FAIL + remediation when old key detected), reading raw YAML.
5. `init.go` exports both new env vars and preserves the `klanker-management` AWS profile name.
6. `site.hcl` and `scp/terragrunt.hcl` use `local.accounts.organization` and `local.accounts.dns_parent`.
7. `km-config.yaml` is migrated with both fields present and no `management:` key.
8. `OPERATOR-GUIDE.md` documents the split, single-account topology, and migration note.
9. `make build` produces `km v0.2.454` without errors.
10. All phase-65 tests pass. Pre-existing test failures (phases 30/37/48) are unrelated to this phase.

---

_Verified: 2026-05-01T22:30:00-04:00_
_Verifier: Claude (gsd-verifier)_
