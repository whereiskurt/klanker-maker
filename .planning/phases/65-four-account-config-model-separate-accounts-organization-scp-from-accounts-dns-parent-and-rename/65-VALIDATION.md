---
phase: 65
slug: four-account-config-model-separate-accounts-organization-scp-from-accounts-dns-parent-and-rename
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-01
---

# Phase 65 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go standard `testing` (no external test runner) |
| **Config file** | none — built into `go test` |
| **Quick run command** | `go test ./internal/app/config/... ./internal/app/cmd/... -run 'TestLoad\|TestBootstrap\|TestConfigure\|TestCheck' -count=1` |
| **Full suite command** | `go test ./internal/app/...` |
| **Estimated runtime** | ~30 seconds (full suite); ~5 seconds (quick) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/config/... ./internal/app/cmd/... -count=1`
- **After every plan wave:** Run `go test ./internal/app/...`
- **Before `/gsd:verify-work`:** Full suite must be green AND grep audit zero-count
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|----------|-----------|-------------------|-------------|--------|
| 65-01-01 | 01 | 1 | `accounts.organization` and `accounts.dns_parent` load from km-config.yaml | unit | `go test ./internal/app/config/... -run TestLoadOrganizationAndDNSParentFields` | ⬜ Wave 0 | ⬜ pending |
| 65-01-02 | 01 | 1 | Blank `accounts.organization` is valid (no validation error) | unit | `go test ./internal/app/config/... -run TestLoadBlankOrganizationIsValid` | ⬜ Wave 0 | ⬜ pending |
| 65-01-03 | 01 | 1 | `ManagementAccountID` field removed from struct (compile-fail elsewhere) | unit | `go build ./...` | ✅ existing | ⬜ pending |
| 65-01-04 | 01 | 1 | `testConfig` and `testDoctorConfig` stubs gain new accessors | unit | `go test ./internal/app/cmd/...` (compile check) | ⬜ Wave 0 | ⬜ pending |
| 65-02-01 | 02 | 2 | bootstrap dry-run shows "Organization account:" line | unit | `go test ./internal/app/cmd/... -run TestBootstrapDryRunShowsOrganizationAccount` | ⬜ Wave 0 | ⬜ pending |
| 65-02-02 | 02 | 2 | bootstrap dry-run shows SCP-skipped message when org blank | unit | `go test ./internal/app/cmd/... -run TestBootstrapDryRunNoOrganizationAccount` | ⬜ Wave 0 | ⬜ pending |
| 65-02-03 | 02 | 2 | bootstrap non-dry-run invokes terragrunt apply when org set | unit | `go test ./internal/app/cmd/... -run TestBootstrapSCPApplyPath` | ⬜ Wave 0 | ⬜ pending |
| 65-02-04 | 02 | 2 | bootstrap non-dry-run skips terragrunt when org blank | unit | `go test ./internal/app/cmd/... -run TestBootstrapSCPSkipped_OrganizationBlank` | ⬜ Wave 0 | ⬜ pending |
| 65-02-05 | 02 | 2 | `runShowPrereqs` prints non-error message when org blank | unit | `go test ./internal/app/cmd/... -run TestShowPrereqsNoOrganizationAccount` | ⬜ Wave 0 | ⬜ pending |
| 65-02-06 | 02 | 2 | `km configure --non-interactive` writes both new fields | unit | `go test ./internal/app/cmd/... -run TestConfigureWritesOrganizationAndDNSParent` | ⬜ Wave 0 | ⬜ pending |
| 65-02-07 | 02 | 2 | `km configure` interactive prompts use new field names | unit | `go test ./internal/app/cmd/... -run TestConfigureInteractivePromptsUseNewNames` | ⬜ Wave 0 | ⬜ pending |
| 65-02-08 | 02 | 2 | `km info` prints Organization account + DNS parent account | unit | `go test ./internal/app/cmd/... -run TestInfoShowsNewAccountFields` | ⬜ Wave 0 | ⬜ pending |
| 65-02-09 | 02 | 2 | `km init` exports `KM_ACCOUNTS_ORGANIZATION` and `KM_ACCOUNTS_DNS_PARENT` | unit | `go test ./internal/app/cmd/... -run TestInitExportsNewAccountEnvVars` | ⬜ Wave 0 | ⬜ pending |
| 65-03-01 | 03 | 3 | doctor WARN when `accounts.organization` blank | unit | `go test ./internal/app/cmd/... -run TestCheckOrganizationAccountBlank_BlankReturnsWarn` | ⬜ Wave 0 | ⬜ pending |
| 65-03-02 | 03 | 3 | doctor OK when `accounts.organization` set | unit | `go test ./internal/app/cmd/... -run TestCheckOrganizationAccountBlank_SetReturnsOK` | ⬜ Wave 0 | ⬜ pending |
| 65-03-03 | 03 | 3 | doctor ERROR when legacy `accounts.management` present in raw YAML | unit | `go test ./internal/app/cmd/... -run TestCheckLegacyManagementField_FieldPresent` | ⬜ Wave 0 | ⬜ pending |
| 65-03-04 | 03 | 3 | doctor OK when `accounts.management` absent | unit | `go test ./internal/app/cmd/... -run TestCheckLegacyManagementField_FieldAbsent` | ⬜ Wave 0 | ⬜ pending |
| 65-03-05 | 03 | 3 | doctor SKIP when no km-config.yaml exists | unit | `go test ./internal/app/cmd/... -run TestCheckLegacyManagementField_NoConfigFile` | ⬜ Wave 0 | ⬜ pending |
| 65-03-06 | 03 | 3 | `management_account_id` removed from `checkConfig` required list | unit | `go test ./internal/app/cmd/... -run TestCheckConfigDoesNotRequireManagement` | ⬜ Wave 0 | ⬜ pending |
| 65-04-01 | 04 | 4 | Zero remaining `ManagementAccountID` Go references | integration | `! grep -rn ManagementAccountID ./internal ./pkg ./cmd` | ✅ grep | ⬜ pending |
| 65-04-02 | 04 | 4 | Zero remaining `KM_ACCOUNTS_MANAGEMENT` references | integration | `! grep -rn KM_ACCOUNTS_MANAGEMENT ./internal ./pkg ./cmd ./infra ./docs` | ✅ grep | ⬜ pending |
| 65-04-03 | 04 | 4 | Zero remaining `accounts.management` references | integration | `! grep -rn 'accounts\.management\|accounts\\.management' ./internal ./pkg ./cmd ./infra ./docs` | ✅ grep | ⬜ pending |
| 65-04-04 | 04 | 4 | Zero remaining `local.accounts.management` HCL references | integration | `! grep -rn 'local\.accounts\.management' ./infra` | ✅ grep | ⬜ pending |
| 65-04-05 | 04 | 4 | Repo root `km-config.yaml` migrated to new schema | manual | `grep -q 'dns_parent' km-config.yaml && ! grep -q '^\s*management:' km-config.yaml` | ✅ grep | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/app/config/config_test.go` — add `TestLoadOrganizationAndDNSParentFields` and `TestLoadBlankOrganizationIsValid` (stub asserts to be filled in plan 1)
- [ ] `internal/app/cmd/doctor_test.go` — extend `testConfig` and `testDoctorConfig` stubs with `GetOrganizationAccountID()` and `GetDNSParentAccountID()` accessors so plan 3's new doctor tests compile (must land with plan 1, before plan 3 touches doctor.go)
- [ ] `internal/app/cmd/bootstrap_test.go` — add stub functions for `TestBootstrapSCPSkipped_OrganizationBlank`, `TestBootstrapSCPApplyPath`, `TestBootstrapDryRunShowsOrganizationAccount`, `TestBootstrapDryRunNoOrganizationAccount`, `TestShowPrereqsNoOrganizationAccount`
- [ ] `internal/app/cmd/configure_test.go` — add stubs for `TestConfigureWritesOrganizationAndDNSParent`, `TestConfigureInteractivePromptsUseNewNames`
- [ ] `internal/app/cmd/info_test.go` (or wherever info is tested) — add stub for `TestInfoShowsNewAccountFields`
- [ ] `internal/app/cmd/init_test.go` — add stub for `TestInitExportsNewAccountEnvVars`

---

## Manual-Only Verifications

| Behavior | Why Manual | Test Instructions |
|----------|------------|-------------------|
| `km bootstrap --dry-run` output reads correctly with org blank | Output is operator-facing; snapshot tests catch wording but not pacing/ergonomics | Run on a config with `accounts.organization: ""`. Verify "SCP Policy: [SKIPPED — no organization account configured]" message and that "DNS parent account: …" line appears. |
| `km bootstrap --show-prereqs` with org blank | New behavior path; ensure non-error message reads naturally | Run with org blank: should print explanation + exit 0, not error. |
| `km doctor` legacy-field error message remediation hint | Operator-facing remediation text; verify it's actionable | Add `accounts.management: "111"` to a config and run `km doctor`. Confirm error names both new fields in the fix instructions. |
| Repo's own `km-config.yaml` post-migration apply | Smoke test the rename on the operator's actual config | After plan 4: `km bootstrap --dry-run` should succeed and show "Organization account: " (blank or whatever value operator chose). |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (test stubs land in plan 1)
- [ ] No watch-mode flags (Go `-count=1` instead)
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter (after planner sign-off)

**Approval:** pending
