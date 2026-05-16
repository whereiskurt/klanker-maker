# Phase 84: SES per-install rule namespacing via operator address prefix — Context

**Gathered:** 2026-05-16
**Status:** Ready for planning
**Source:** Inline discussion with operator (no /gsd:discuss-phase needed — decisions captured during /gsd:plan-phase invocation)

<domain>
## Phase Boundary

**What this phase delivers:**
- Address-namespaced operator inbox: `operator-${resource_prefix}@sandboxes.${domain}` (e.g. `operator-kph@`, `operator-rg@`)
- Account-shared SES receipt rule set owned by a new foundation/bootstrap module, separate from per-install regional resources
- Per-install rules (`${prefix}-operator-inbound`, `${prefix}-sandbox-catchall`) attached to the shared rule set, removable independently
- Hard removal of Phase 82.1's `activate_rule_set` mechanism (variable, resource, env-var opt-out, runbook)
- `km configure` writes `KM_OPERATOR_EMAIL` derived from the prefix
- `email-handler` Lambda resolves `operator-${prefix}@` for inbound dispatch
- New `km doctor` check for SES rules whose prefix is not present in local `km-config.yaml`

**What this phase does NOT deliver:**
- Sandbox-address namespacing (sandbox-to-sandbox emails stay flat at `{sandbox-id}@sandboxes.${domain}` — entropy is sufficient)
- Deprecation shim for `activate_rule_set` — hard removal, no transition period
- Cross-account rule-set sharing (still account-local, AWS SES account boundary unchanged)
- Migration of existing operator inboxes — no live `operator@` inbox exists in any account that uses the platform

</domain>

<decisions>
## Implementation Decisions

### Architecture: where the rule set lives
- **Locked:** New foundation/bootstrap module `infra/modules/ses-shared-rule-set/v1.0.0/`.
- **Why:** The rule set is conceptually account-shared state (analogous to an IAM SAML provider). Putting it in a foundation layer makes ownership unambiguous, prevents first-install-wins coupling, and keeps the regional `ses/` module focused on rules only.
- **Not chosen:** Conditional-create in existing `ses/` module (data-source-then-create-if-missing). Rejected because it couples rule-set lifecycle to whichever install ran first, complicates `km uninit` semantics, and creates a "who owns this resource" ambiguity for Terraform state.

### Cleanup of Phase 82.1
- **Locked:** Hard removal of `activate_rule_set` variable, `aws_ses_active_receipt_rule_set` resource, `KM_SES_ACTIVATE_RULESET` env-var path, and CLAUDE.md "Phase 82.1 follow-up" runbook section.
- **Why:** No live operator inbox in any deployment uses the old design (operator confirmed: the other deployment doesn't have email set up yet). Carrying deprecation shims would be dead code from day 1.
- **Scope of removal:** All references in `infra/modules/ses/v1.0.0/variables.tf`, `infra/modules/ses/v1.0.0/main.tf`, any compiler code that exports `KM_SES_ACTIVATE_RULESET`, CLAUDE.md Phase 82.1 section, and the `82.1-03-PLAN.md` description in ROADMAP.md (mark superseded, do not delete the historical plan file).

### Operator address format
- **Locked:** `operator-${resource_prefix}@sandboxes.${domain}` — single hyphen, prefix appended.
- **Why:** Reads naturally; greppable; aligns with the `${prefix}-operator-inbound` rule name; default install (`prefix=km`) yields `operator-km@`, no special case.
- **Default prefix:** `km` (matches existing `resource_prefix` default per CLAUDE.md). So the default install's operator inbox is `operator-km@sandboxes.{domain}`, NOT the legacy `operator@`.

### Sandbox addresses
- **Locked:** Stay flat — `{sandbox-id}@sandboxes.${domain}`. No prefix injection.
- **Why:** Sandbox IDs already have sufficient entropy (8+ hex chars). Cross-install collision odds are negligible. Avoiding the change keeps `km-send` / `km-recv` and the entire sandbox-side email plumbing untouched.
- **Catch-all rule:** `${prefix}-sandbox-catchall` matches `*@sandboxes.${domain}` for that install's known sandbox IDs (compiler-emitted recipient list) OR a literal wildcard if AWS SES supports per-rule wildcards within a shared rule set. Resolve in research phase.

### km configure wiring
- **Locked:** `km configure` derives `KM_OPERATOR_EMAIL=operator-${resource_prefix}@sandboxes.${email_subdomain}` from the prefix + email subdomain it already prompts for. No new prompt.
- **Preserve-on-rerun:** Inherits Phase 82.1's preserve behavior for `resource_prefix` (locked at first configure; reset via `--reset-prefix`).

### email-handler Lambda resolution
- **Locked:** Lambda reads `${prefix}` from its own env (existing `state_prefix` from Phase 82-B2), constructs the expected operator-address pattern `operator-${prefix}@`, and routes inbound to operator-dispatch path. Other prefixes (foreign operator addresses) are silently dropped (logged at INFO, not delivered cross-install).
- **Why silent drop:** Foreign inbound to `operator-rg@` arriving on the `kph` Lambda is a misconfiguration on the sender side, not a security event. Operator can grep CloudWatch for unexpected drops if cross-install delivery is intended.

### km doctor orphan check
- **Locked:** Add `ses_rules_healthy` check. Lists rules in the shared rule set, parses `${prefix}-` from each rule name, cross-references against the local `km-config.yaml`'s `resource_prefix`. WARN-level on mismatch.
- **Not ERROR-level:** Other installs' rules are *expected* — the WARN exists to surface stale rules from uninstalled prefixes, not to flag normal multi-tenancy.

### Rule naming convention
- **Locked:** `${prefix}-operator-inbound`, `${prefix}-sandbox-catchall`. Two rules per install in the shared rule set.
- **Rule ordering:** Operator-inbound BEFORE catchall (AWS SES evaluates rules in order; specific recipient match should win). Each install owns its two rules; insertion order across installs doesn't matter as long as each install's pair is ordered correctly.

### Foundation module bootstrap surface
- **Locked:** Bootstrapped via the existing foundation/bootstrap layer (same layer that owns the foundation S3 buckets, KMS keys, etc.). Idempotent re-apply is required — second install's `km init` MUST NOT recreate the rule set.
- **Naming:** Rule set is named `sandbox-email-shared` (no prefix — it's shared). Resource is `aws_ses_receipt_rule_set` with `lifecycle.prevent_destroy = true` to guard against accidental teardown.

### Active rule set
- **Locked:** Foundation module also owns `aws_ses_active_receipt_rule_set` pointing at `sandbox-email-shared`. There is exactly ONE active rule set, owned by the foundation, activated once at bootstrap, never touched by regional installs.
- **Why this works where 82.1 didn't:** The activation lives in foundation Terraform state, not per-install state. No install can deactivate another install's rules because no install has the resource in scope.

</decisions>

<specifics>
## Specific Ideas

- Phase 82.1's `activate_rule_set` plan file at `.planning/phases/82.1-multi-instance-polish-bare-configure-preserve-service_hcl-prefix-aware-ses-activate_rule_set/82.1-03-*-PLAN.md` should be referenced but NOT modified — historical record stays intact. Phase 84 closeout updates ROADMAP.md to mark 82.1-03 superseded.
- The current SES module is `infra/modules/ses/v1.0.0/` — Phase 84 either bumps to `v2.0.0` (breaking — rule set removed from this module) OR creates a new `v2.0.0` alongside and leaves `v1.0.0` in tree for historical reference. Versioning decision to be made in research.
- `km-config.yaml` schema may need an explicit field (or comment) for `operator_email` so `km` binaries can resolve it without re-deriving from prefix every call. Keep it derived for v1; cache if needed.
- The doctor check should reuse the existing AWS SDK SES client wiring; look at the existing `aws_ses_quota` check for pattern.
- Cross-install reference: `pkg/aws/ses.go` and `infra/modules/email-handler/v1.0.0/` are the load-bearing pieces.

</specifics>

<deferred>
## Deferred Ideas

- Sandbox-address namespacing (per above; if cross-install sandbox ID collision is ever observed, address in a follow-up phase)
- Operator orphan-rule auto-cleanup (`km doctor --backfill-tags` style) — defer until WARN-level check identifies real-world need
- Cross-account SES rule sharing — not on roadmap; SES is account-local by AWS design
- Migration shim for legacy `operator@` addresses — not needed (no live deployments use it)

</deferred>

## Additional Decisions (resolved 2026-05-16 from research open questions)

### Sandbox catchall recipient strategy
- **Locked:** Whole-domain match. Each install's `${prefix}-sandbox-catchall` rule has recipient `sandboxes.${domain}` (the whole domain, no wildcards needed). Both installs' catchall rules fire on every sandbox-inbound email; each writes to its own S3 prefix (`mail/${prefix}/`).
- **Why:** AWS SES does not support wildcards in recipient lists (confirmed in research). Compiler-enumerated recipient lists would force Terraform re-apply on every `km create`/`km destroy` — unacceptable churn. Per-install email subdomain would break the locked operator address format.
- **Cost:** Wasted S3 puts on the sibling install (mail for kph sandbox writes to both `mail/kph/` and `mail/rg/`). The mail-handler Lambda silently drops unknown sandbox IDs, so cross-contamination is read-only and bounded by sandbox email volume. Acceptable.
- **Rule ordering within the shared rule set:** Each install owns two rules — operator-inbound BEFORE catchall — but rule order across installs doesn't matter under "all matching rules fire" semantics.

### SES domain identity, DKIM, MX, verification records
- **Locked:** Moves to the foundation module. `ses-shared-rule-set/v1.0.0/` owns:
  - `aws_ses_receipt_rule_set` (the shared rule set)
  - `aws_ses_active_receipt_rule_set` (activation, always-active)
  - `aws_ses_domain_identity` (for `sandboxes.${domain}`)
  - `aws_ses_domain_dkim` (3 DKIM CNAMEs)
  - `aws_route53_record.mx` (MX → SES inbound)
  - `aws_route53_record.ses_verification` (DKIM verification TXT/CNAME)
- **Regional `ses` module shrinks to:** `aws_ses_receipt_rule.operator_inbound` + `aws_ses_receipt_rule.sandbox_catchall` only, both with `rule_set_name = "sandbox-email-shared"`. Consumes `aws_ses_domain_identity` via data source.
- **Why:** Shared domain means only one Terraform state can own the domain identity. Moving to foundation eliminates `EntityAlreadyExists` failures on second-install apply.
- **Versioning:** Regional `infra/modules/ses/` bumps to `v2.0.0`. `v1.0.0` stays in tree for historical reference (not deleted).

### Foundation module idempotency
- **Locked:** Auto-detect pattern (Phase 80 `cluster-irsa` precedent). Foundation module accepts two flags:
  - `register_shared_rule_set` (bool) — default behavior via `km bootstrap` auto-detect
  - `register_domain_identity` (bool) — default behavior via `km bootstrap` auto-detect
- **`km bootstrap` workflow:**
  1. `aws ses list-receipt-rule-sets` — if `sandbox-email-shared` exists, set `register_shared_rule_set=false`
  2. `aws ses list-identities --identity-type Domain` — if the target domain exists, set `register_domain_identity=false`
  3. Generate terragrunt inputs with computed flags
  4. `terragrunt apply` against the foundation module
- **Terraform pattern:** Resources are `count = var.register_X ? 1 : 0`; data sources are `count = var.register_X ? 0 : 1` (mirror pattern from `cluster-irsa/v1.0.0/`).
- **Why:** Safe to re-apply from any install context. Operator coordination would be fragile across concurrent operators or state drift.

### Phase 82.1 migration runbook
- **Locked:** No live migration needed. Operator confirmed no deployment uses the old `aws_ses_active_receipt_rule_set.km_sandbox` resource with live inbound email. Phase 84 upgrade for greenfield-or-unused installs:
  1. `make build`
  2. `km init --sidecars` (refresh sidecar binaries)
  3. `km bootstrap --shared-ses` (NEW — provisions foundation rule set + domain identity, auto-detect flags decide whether to create or reference)
  4. `km init --dry-run=true` to inspect (Phase 84 plan will show: REMOVE `aws_ses_active_receipt_rule_set.km_sandbox`, REMOVE `aws_ses_receipt_rule_set.km_sandbox`, REMOVE `aws_ses_domain_identity.sandbox`, REMOVE DKIM/MX/verification, ADD 2 prefix-named rules in the shared rule set)
  5. `km init --dry-run=false` to apply
  6. `km configure` (idempotent re-run derives `KM_OPERATOR_EMAIL=operator-${prefix}@...`)
- **CLAUDE.md "Phase 82.1 follow-up: SES active-rule-set handoff" section:** DELETED outright in the docs plan. No deprecation note — the design is replaced wholesale.

### Scope additions discovered during research
The planner MUST cover these code locations beyond what CONTEXT.md originally listed:
- `pkg/compiler/userdata.go:1621` and `pkg/compiler/userdata.go:1653` — sandbox-side `km-send` default `--to` literal `operator@` must become `${KM_OPERATOR_EMAIL}` env-var reference
- `pkg/aws/ses.go:271` — operator-notification email body `From`/`To` literal must interpolate the derived operator address
- `cmd/email-create-handler/main.go:861` — outbound-reply `From` literal must use the derived operator address
- `OPERATOR-GUIDE.md:646-677` — Phase 82.1 SES handoff section deleted; replaced with shared-rule-set + auto-detect runbook

### Test scope additions
The planner's Wave 0 must add (per research Validation Architecture):
- `internal/app/cmd/configure_test.go` — operator-email derivation tests (3 functions)
- `cmd/email-create-handler/main_test.go` — own-prefix vs foreign-prefix routing tests (2 functions)
- `internal/app/cmd/doctor_test.go` + `internal/app/cmd/doctor_ses_rules_test.go` — orphan-detection unit tests with mock SES API
- `pkg/compiler/userdata_84_test.go` — env-var reference assertion (no `operator@` literal in generated userdata)
- `pkg/aws/ses_test.go` — `TestSendCreateNotification_OperatorAddressUsesPrefix`
- Makefile grep-based check for Phase 82.1 leftovers (`KM_SES_ACTIVATE_RULESET`, `activate_rule_set`)

### Go module dependency
Production wiring of `km doctor` SES rule check requires `aws-sdk-go-v2/service/ses` (classic v1 SES SDK — distinct from existing `sesv2` dependency). Add via `go get github.com/aws/aws-sdk-go-v2/service/ses`. Unit tests gate behind a narrow interface so test code does not pull the dependency.

---

*Phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix*
*Context gathered: 2026-05-16 via inline /gsd:plan-phase discussion*
*Open-question resolutions appended: 2026-05-16 after gsd-phase-researcher surfaced shared-domain implications*
