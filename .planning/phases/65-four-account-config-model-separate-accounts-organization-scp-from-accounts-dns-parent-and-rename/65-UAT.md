---
status: complete
phase: 65-four-account-config-model-separate-accounts-organization-scp-from-accounts-dns-parent-and-rename
source:
  - 65-01-SUMMARY.md
  - 65-02-SUMMARY.md
  - 65-03-SUMMARY.md
  - 65-04-SUMMARY.md
started: 2026-05-02T03:00:00Z
updated: 2026-05-02T03:35:00Z
---

## Current Test

[testing complete]

## Tests

### 1. km doctor — green run with new schema
expected: |
  ./km doctor exits 0, lists 20 checks, includes "Legacy Config Field" (no legacy key) and "SCP Enforcement Config" (organization = 481723467561, SCP enabled).
result: pass
note: |
  Actual run reported 25 checks total (22 passed, 3 warnings unrelated to phase 65 — stale IAM roles, stale schedules, stale Slack channels). Both new phase-65 checks present and green: "Legacy Config Field" (no legacy key) and "SCP Enforcement Config" (accounts.organization = 481723467561, SCP enabled). My pre-test estimate of "20 checks" undercounted; the actual delta from 18 → 25 reflects checks added across multiple recent phases, not just phase 65.

### 2. km bootstrap --dry-run — four-account topology in output
expected: |
  ./km bootstrap --dry-run prints:
  - "Organization account: 481723467561" line
  - "DNS parent account: 481723467561" line
  - "Application account: 052251888500"
  - SCP Policy section that references accounts.organization (not management)
result: pass
note: |
  Output exactly matched. SCP Policy block correctly says "Deploy via: km bootstrap (organization account credentials required)" — vocabulary fully migrated. Three account lines render in the new order (Organization / DNS parent / Application).

### 3. km bootstrap --show-prereqs — works with org set
expected: |
  ./km bootstrap --show-prereqs prints the km-org-admin role + trust policy guidance for the organization account 481723467561. Exits 0. References "organization account" not "management account".
result: pass
note: |
  Output uses "organization account (481723467561)" consistently throughout — both the AWS CLI option and CloudFormation option. Trust policy correctly targets application account 052251888500. Bonus: includes a Step 0 explaining how to enable SCPs in the org. No residual "management account" wording.

### 4. km bootstrap --show-prereqs — graceful when org blank (DISRUPTIVE)
expected: |
  Temporarily edit km-config.yaml: change `organization: "481723467561"` to `organization: ""`.
  Run ./km bootstrap --show-prereqs.
  Output should be a NON-ERROR message (exit 0) explaining SCP is disabled and referencing accounts.organization.
  RESTORE the file: `organization: "481723467561"` after this test.
result: pass
note: |
  Output: "accounts.organization not set — SCP deployment disabled. Set accounts.organization in km-config.yaml to enable org-level sandbox containment via Service Control Policies." Clean non-error message, references the new field name, explains the consequence and the remediation. Exit 0.

### 5. km bootstrap --dry-run — SCP-skipped message when org blank (DISRUPTIVE)
expected: |
  With organization blanked (or use a temporary copy of km-config.yaml), ./km bootstrap --dry-run prints something like "SCP Policy: [SKIPPED — no organization account configured]" referencing accounts.organization, NOT accounts.management.
  RESTORE the file before continuing.
result: pass
note: |
  Header now shows "Organization account: (not set)" — clean handling of blank value (not crash, not weird empty-string artifact). SCP Policy block printed as "[SKIPPED — accounts.organization not set]" with remediation hint "Run 'km configure' and set accounts.organization to enable SCP deployment." DNS parent + Application accounts still rendered correctly. Exit 0.

### 6. km doctor — legacy field FAIL detection (DISRUPTIVE)
expected: |
  Temporarily add a line `management: "999999999999"` to the accounts: block in km-config.yaml.
  Run ./km doctor.
  Output should:
  - Show the "Legacy Config Field" check FAIL
  - Error message names both `accounts.dns_parent` and `accounts.organization` as the new fields
  - Suggests leaving organization blank to skip SCP
  RESTORE the file (remove the management line) after this test.
result: pass

### 7. km info — new account fields displayed
expected: |
  ./km info displays "Organization account: 481723467561" and "DNS parent account: 481723467561" alongside Application/Terraform accounts. No "Management account" label.
result: pass
note: |
  Output uses "AWS Accounts" section with shorter labels: Organization / DNS parent / Terraform / Application. No "Management" label anywhere — fully migrated. Organization field displayed as "-" because km-config.yaml currently has organization blank (carry-over from tests 4–6). DNS parent (481723467561), Terraform (052251888500), Application (052251888500) all rendered correctly. Blank field renders as "-" not as crash or weird empty space — graceful.

### 8. km configure --help — new flags documented
expected: |
  ./km configure --help lists `--organization-account` and `--dns-parent-account` flags.
  No `--management-account` flag.
  Help text mentions four-account topology or organization/dns_parent semantics.
result: issue
reported: |
  The flag list at the bottom is migrated correctly (--organization-account, --dns-parent-account, no --management-account).
  But the Long help text shown above the Usage line is STALE:
    1. Examples block still shows `--management-account 111111111111` — flag no longer exists
    2. Inline flag list under Long description still lists `--management-account` and "AWS management account ID"
    3. "Account Topologies" section describes "2-account" / "3-account" via management/terraform/application — the new 4-account split (organization vs dns_parent) is not reflected
  Operator copy-pasting the example will hit `unknown flag: --management-account`.
severity: major
location: |
  internal/app/cmd/configure.go — the cobra command's Long string (the prose containing "Examples:", "Flags:", and "Account Topologies:" sections). Plan 02 migrated the actual flag definitions but missed the Long help text.

### 9. OPERATOR-GUIDE.md — four-account topology coherent
expected: |
  Open OPERATOR-GUIDE.md. Verify:
  - Topology table now shows organization + dns_parent + application + terraform (not management)
  - Env var section lists KM_ACCOUNTS_ORGANIZATION and KM_ACCOUNTS_DNS_PARENT (not KM_ACCOUNTS_MANAGEMENT)
  - Phase 65 migration note exists and is readable for someone with a stale config
  - No leftover references to accounts.management or --management-account in operator-facing prose (legacy-detection error strings in code are fine)
result: pass
note: |
  20+ organization/dns_parent/ORGANIZATION/DNS_PARENT mentions across the doc: topology table rows (line 19-20), Phase 65 migration note (line 32-36), env var table (line 112-113), non-interactive example with --organization-account (line 156), shell exports (line 192-193), bootstrap prereq (line 205-207), troubleshooting (line 459-465). The 10 remaining "management" mentions are all descriptive prose about the AWS concept "AWS Organizations management account" — not stale references to the deprecated config field. Initial concern after closer read: confirmed pass.

## Summary

total: 9
passed: 8
issues: 1
resolved: 1
pending: 0
skipped: 0

## Gaps

- truth: "km configure --help displays migrated four-account vocabulary throughout, no references to --management-account or 2-account/3-account topology framing"
  status: resolved
  reason: "User reported: configure command's Long help text contains stale --management-account flag in the Examples block, lists --management-account in the inline flag descriptions under Long, and the Account Topologies section still describes 2-account/3-account framing instead of the 4-account model. The actual cobra flag definitions ARE correct (--organization-account, --dns-parent-account, no --management-account). The drift is purely in the Long string."
  severity: major
  test: 8
  location: "internal/app/cmd/help/configure.txt (embedded via helpText() — not the Go Long: literal as initially suspected)"
  root_cause: "Phase 65 plan 02 migrated the cobra flag definitions in configure.go but the embedded help file at internal/app/cmd/help/configure.txt was authored pre-phase-65 and was not updated. The help text is loaded via helpText(\"configure\") at configure.go:78."
  resolution: "Rewrote internal/app/cmd/help/configure.txt: Examples block uses --organization-account + --dns-parent-account, added single-account install example showing how to omit --organization-account, regrouped flag list by purpose (account IDs / required / optional), replaced 2-account/3-account topology section with four-role description (organization / dns_parent / terraform / application) including single-account vs cross-account guidance."
  fix_commit: "9456512"
  fix_pushed: "origin/main 9456512"
  fix_verified: "./km configure --help | grep -c 'management-account\\|2-account\\|3-account' returns 0"
