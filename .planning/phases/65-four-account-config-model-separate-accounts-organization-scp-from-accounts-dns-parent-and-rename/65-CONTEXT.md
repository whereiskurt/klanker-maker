# Phase 65: Four-account config model — Context

**Gathered:** 2026-05-01
**Status:** Ready for planning
**Source:** Synthesized from operator conversation (no /gsd:discuss-phase)

<domain>
## Phase Boundary

Decouple "AWS Organizations management account" from "DNS parent zone owner" in `km-config.yaml`. Today's `accounts.management` field does double duty:

1. SCP target — `infra/live/management/scp/terragrunt.hcl` deploys an org-level Service Control Policy against this account.
2. DNS parent zone owner — `ensureSandboxHostedZone` (init.go:1465) looks up the `cfg.Domain` parent zone in this account via the hardcoded `klanker-management` AWS profile.

These are two unrelated AWS concepts conflated under one name. The phase splits them into distinct config fields and updates every consumer to read the correct one.

**What this phase delivers:**

- Operators with a single-account topology (no AWS Organizations access) can run `km bootstrap` and `km init` cleanly by leaving the new `accounts.organization` field blank — bootstrap skips SCP deployment, all other steps work.
- Operators with an org-management account separate from the DNS parent zone owner can configure both independently.
- `km doctor` surfaces SCP enforcement status so operators know when sandbox containment relies on IAM alone.

**What this phase does NOT deliver:**

- AWS profile name changes (`klanker-management`, `klanker-terraform`, `klanker-application` stay verbatim). Single-account installs work by pointing all profiles at the same credentials.
- Alternative SCP backstops (IAM permission boundaries, deny-by-default sandbox role policies). The reduced security posture in single-account installs is documented, not compensated.
- Sandbox runtime changes — `km create`, `km destroy`, `km agent`, `km email`, `km slack` paths don't touch the management account and remain unchanged.

</domain>

<decisions>
## Implementation Decisions

### Schema rename — locked

- New field: `accounts.organization` — AWS Organizations management account ID (SCP target). Optional. Blank → skip SCP deployment.
- New field: `accounts.dns_parent` — Route53 hosted-zone owner for `cfg.Domain` parent zone. Required when `cfg.Domain` is set and DNS bootstrapping is desired.
- Removed field: `accounts.management` — no back-compat alias. Hard cut.
- Existing fields unchanged: `accounts.application`, `accounts.terraform`.

### Go struct — locked

- `internal/app/config/config.go`: drop `ManagementAccountID`, add `OrganizationAccountID` and `DNSParentAccountID` (string fields, omitempty YAML tags).
- All callers in `internal/app/cmd/*.go` migrate to the new fields. No transitional aliases.

### Environment variables — locked

- New: `KM_ACCOUNTS_ORGANIZATION`, `KM_ACCOUNTS_DNS_PARENT` — exported by `init.go` and `bootstrap.go` for Terragrunt's `site.hcl` `get_env()` resolution.
- Removed: `KM_ACCOUNTS_MANAGEMENT` — every reference deleted.

### Bootstrap behavior — locked

- `runBootstrap` SCP gate (bootstrap.go:728): change `if loadedCfg.ManagementAccountID != ""` to `if loadedCfg.OrganizationAccountID != ""`. Blank → skip SCP entirely (no flag needed, no `--skip-scp`).
- `runShowPrereqs` and `runShowSCP`: the `km-org-admin` role and trust policy still reference `OrganizationAccountID` (the org-management side). Update fmt strings and account-id parameters accordingly.
- Dry-run output: rename "Management account" → "Organization account" and add a "DNS parent account" line. Update the SCP-skipped message to reference `accounts.organization`.

### Init DNS behavior — locked

- `ensureSandboxHostedZone` (init.go:1465): the parent zone lookup currently uses `awsconfig.WithSharedConfigProfile("klanker-management")`. The semantic owner shifts to `accounts.dns_parent`, but the AWS profile name stays `klanker-management` (out of scope to rename). The function still works without code change to its profile string — only its docstring/comments update to reflect that `dns_parent` (not `organization`) is what determines parent-zone existence.
- If `accounts.dns_parent` is blank, init logs a `[warn]` and skips DNS delegation, mirroring today's behavior when `cfg.Domain` is unset. (Today's code does not gate DNS on the management field; this phase makes the gate explicit on `dns_parent`.)
- `runInitDryRun` env-presence check (init.go:251–260): replace `KM_ACCOUNTS_MANAGEMENT` with `KM_ACCOUNTS_ORGANIZATION` and add `KM_ACCOUNTS_DNS_PARENT`.

### Terragrunt/HCL — locked

- `infra/live/site.hcl` accounts block: add `organization = get_env("KM_ACCOUNTS_ORGANIZATION", "")` and `dns_parent = get_env("KM_ACCOUNTS_DNS_PARENT", "")`. Drop `management = get_env("KM_ACCOUNTS_MANAGEMENT", "")`.
- `infra/live/management/scp/terragrunt.hcl`: every `local.accounts.management` reference becomes `local.accounts.organization`. The `assume_role.role_arn` for the SCP provider, the trusted_role_arns, and the inputs all read from `organization`.
- The SCP module itself (`infra/modules/scp/v1.0.0/`) is unchanged — it takes `var.application_account_id`, which still maps from `local.accounts.application`.

### Doctor check — locked

- New check in `km doctor`: warn when `accounts.organization` is blank with message "SCP enforcement disabled — sandbox containment relies on IAM policies only". Severity: WARN, not FAIL (single-account installs are a legitimate topology).
- New check in `km doctor`: error when `accounts.management` is present in `km-config.yaml` with message "accounts.management has been split — rename to accounts.dns_parent and add accounts.organization (or leave blank to skip SCP)". Severity: FAIL with remediation hint.
- Both checks integrate into the existing 18-check doctor output.

### Backwards compatibility — locked

- Pre-1.0; only existing config is the operator's own local one (`km-config.yaml` not committed). Hard rename. No deprecation warning, no alias period, no transitional `management` → `organization` auto-mapping.
- The doctor error provides the migration guidance instead.

### Test coverage — locked

- `internal/app/config/config_test.go`: cover YAML round-trip for both new fields, including blank `organization`.
- `internal/app/cmd/bootstrap_test.go`: cover SCP-skip path when `OrganizationAccountID == ""`, cover SCP-deploy path when set, cover dry-run output for both states.
- `internal/app/cmd/configure_test.go`: cover writing/reading both new fields.
- `internal/app/cmd/doctor_test.go`: cover the two new checks (blank-organization warning, legacy-management error).
- No test should reference `KM_ACCOUNTS_MANAGEMENT` or `ManagementAccountID` after this phase ships.

### Documentation — locked

- Update `km-config.yaml` example (wherever it lives — operator README or docs/ reference).
- Update bootstrap help text and output strings.
- No changes to CLAUDE.md required (it doesn't reference the management account today).

### Claude's Discretion

- File-by-file ordering of edits within the implementation plan (which file to touch first).
- Whether to do the rename in a single mega-commit or split by concern (suggest: split — config struct + tests, then bootstrap, then init, then doctor, then HCL).
- Exact wording of doctor messages within the constraints above.
- Whether to add a `KM_ACCOUNTS_DNS_PARENT_PROFILE` env var to optionally override the hardcoded `klanker-management` AWS profile name. Lean: no — out of scope per operator decision.

</decisions>

<specifics>
## Specific Ideas

### Touch list (concrete files)

**Go code (7 files):**
- `internal/app/config/config.go` — struct fields, YAML tags, getters if any
- `internal/app/cmd/bootstrap.go` — SCP gate (line 728), runShowPrereqs (line 281), runShowSCP (line 472), dry-run output (lines 654, 694–706, 730–737)
- `internal/app/cmd/init.go` — env exports (lines 301–306), dry-run env-presence map (lines 258–259), ensureSandboxHostedZone docstring (line 1465)
- `internal/app/cmd/create.go` — uses ManagementAccountID (1 ref per grep)
- `internal/app/cmd/doctor.go` — new checks, plus existing references
- `internal/app/cmd/info.go` — surfaces account IDs in `km info` output
- `internal/app/cmd/uninit.go` — references management account

**Tests (4 files):**
- `internal/app/config/config_test.go`
- `internal/app/cmd/bootstrap_test.go`
- `internal/app/cmd/configure_test.go`
- `internal/app/cmd/doctor_test.go`

**Infra (2 files):**
- `infra/live/site.hcl` — accounts block
- `infra/live/management/scp/terragrunt.hcl` — provider assume_role, trusted_role_arns, inputs

**Total scope:** ~158 references across ~13 files (per `grep -rn` count from operator's pre-plan analysis).

### Behavioral diff table (consumer-by-consumer)

| Place | Today | After |
|---|---|---|
| `runBootstrap` SCP gate (bootstrap.go:728) | `if ManagementAccountID != ""` | `if OrganizationAccountID != ""` |
| `runShowPrereqs` km-org-admin role account | `loadedCfg.ManagementAccountID` | `loadedCfg.OrganizationAccountID` |
| `ensureSandboxHostedZone` semantic owner | `accounts.management` (conflated) | `accounts.dns_parent` (explicit) |
| `infra/live/site.hcl` accounts block | exposes `management` | exposes `organization` + `dns_parent` |
| `infra/live/management/scp/terragrunt.hcl` | `local.accounts.management` | `local.accounts.organization` |
| `runInitDryRun` env-presence check | `KM_ACCOUNTS_MANAGEMENT` | `KM_ACCOUNTS_ORGANIZATION` (and add DNS_PARENT) |
| `km doctor` SCP-status check | (none) | WARN when `organization` blank |
| `km doctor` legacy-field check | (none) | FAIL when `accounts.management` present |

### Migration path for operator's own config

Operator's local `km-config.yaml` migration:
```yaml
# Before
accounts:
  management:  "111111111111"
  application: "111111111111"
  terraform:   "111111111111"

# After
accounts:
  organization: ""              # blank — single-account, no AWS Organizations access
  dns_parent:   "111111111111"  # was 'management'
  application:  "111111111111"
  terraform:    "111111111111"
```

For the upcoming single-account install: same as above with all three IDs identical.

</specifics>

<deferred>
## Deferred Ideas

- **AWS profile renames** — `klanker-management`, `klanker-terraform`, `klanker-application` stay as-is. Renaming to `klanker-organization` / `klanker-dns-parent` is a follow-up phase. Single-account installs work today by pointing all profiles at the same credentials.
- **Profile-name override env var** (`KM_ACCOUNTS_DNS_PARENT_PROFILE`) — out of scope. Hardcoded `klanker-management` profile name in `init.go:1473/1529` stays unchanged.
- **Splitting `klanker-management` profile in two** — out of scope. Cross-account deployments would benefit from this; current code expects one profile.
- **IAM permission boundary as SCP alternative** — out of scope. Operators in single-account topologies lose the SCP backstop; this phase documents that fact in `km doctor` rather than offering a replacement.
- **Backwards-compat alias for `accounts.management`** — explicitly rejected. Pre-1.0 hard rename with doctor error guidance instead.
- **Auto-detection of single-account topology** — out of scope. Operator opts in by setting `accounts.organization` to blank.
- **Dry-run / migration helper** (e.g. `km configure --migrate-management`) — out of scope. Manual edit guided by the doctor error message.

</deferred>

---

*Phase: 65-four-account-config-model-separate-accounts-organization-scp-from-accounts-dns-parent-and-rename*
*Context gathered: 2026-05-01 — synthesized from operator conversation*
