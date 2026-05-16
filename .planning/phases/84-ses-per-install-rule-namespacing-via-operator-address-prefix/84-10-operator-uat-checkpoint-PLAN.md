---
phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
plan: 10
type: execute
wave: 4
depends_on: [02, 03, 04, 05, 06, 07, 08, 09]
files_modified:
  - .planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-10-UAT.md
  - .planning/STATE.md
autonomous: false
requirements:
  - SES-PREFIX-ADDRESS
  - SES-SHARED-RULESET
  - SES-PER-INSTALL-RULES
  - SES-82.1-REMOVAL
  - SES-CONFIGURE-WIRING
  - SES-HANDLER-LOOKUP
  - SES-DOCTOR-ORPHANS

must_haves:
  truths:
    - "Operator (KPH) has run the Phase 84 upgrade procedure end-to-end against a real AWS account"
    - "`km bootstrap --shared-ses` reported 'creating' on the fresh account (auto-detect saw no pre-existing rule set / identity)"
    - "`km init --dry-run=true` planned to REMOVE old SES resources and ADD 2 prefix-named rules to `sandbox-email-shared`"
    - "`km init --dry-run=false` applied cleanly with no AWS errors"
    - "`km configure` re-derived `KM_OPERATOR_EMAIL` to `operator-{prefix}@{subdomain}.{parent}` without prompting (idempotent)"
    - "A test sandbox was provisioned; `km-send` from the sandbox reached the operator inbox; `km email read` displayed it"
    - "`km doctor` reports `✓ SES rules healthy` for this install"
    - "An operator UAT record is captured at `.planning/phases/84-.../84-10-UAT.md` with command outputs and final status (`status: passed` or `status: diagnosed`)"
    - "STATE.md is updated to reflect Phase 84 completion"
    - "closure_a_verified: A second install (different `KM_RESOURCE_PREFIX`) ran `km init --dry-run=true` and the plan showed NO destroy of the first install's rules — only ADD of the second install's two prefix-named rules. `km init --dry-run=false` then applied without any opt-out env var. (Covers ROADMAP Phase 84 closure criterion (a).)"
    - "closure_c_verified: After the second-install setup, `terragrunt destroy` in the SECOND install's regional `infra/live/use1/ses/` left the first install's two prefix-named rules intact in the shared rule set (verified via `aws ses describe-receipt-rule-set --rule-set-name sandbox-email-shared`). (Covers criterion (c).)"
    - "closure_d_verified: After step `km init --dry-run=false`, re-running `km bootstrap --shared-ses --dry-run=true` reported `register_shared_rule_set=false` and `register_domain_identity=false`, with the terragrunt plan showing a NO-OP. (Covers criterion (d).)"
  artifacts:
    - path: ".planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-10-UAT.md"
      provides: "UAT log with all command outputs, observed behavior, deviations from expected"
    - path: ".planning/STATE.md"
      provides: "Phase 84 marked complete"
  key_links:
    - from: "Operator's terminal session"
      to: "Real AWS SES state"
      via: "km bootstrap --shared-ses + km init"
      pattern: "sandbox-email-shared"
---

<objective>
Operator UAT checkpoint — NOT autonomous. The operator (KPH) executes the Phase 84 upgrade procedure against a real AWS account, validates every must-have, and writes a UAT log. Then a small autonomous closeout task updates STATE.md and ROADMAP.md.

Mirrors the rebuild-step UAT pattern from Phase 82-10 / Phase 83-07.

Per CONTEXT.md "Operator UAT checkpoint plan" critical constraint #7: this plan is the closeout for Phase 84. The checkpoint blocks waiting for operator confirmation before Phase 84 can be marked complete.

Output:
- 1 UAT record at `.planning/phases/84-.../84-10-UAT.md`
- STATE.md + ROADMAP.md updated to reflect Phase 84 completion
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-CONTEXT.md
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-RESEARCH.md
@CLAUDE.md
@OPERATOR-GUIDE.md
</context>

<tasks>

<task type="checkpoint:human-verify" gate="blocking">
  <name>Task 1: Operator UAT — Phase 84 end-to-end against real AWS</name>
  <files>.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-10-UAT.md</files>
  <action>
The operator (KPH) runs the full Phase 84 upgrade procedure against a real AWS account and captures the session into `.planning/phases/84-.../84-10-UAT.md`.

**Pre-flight (verify Wave 1-2 outputs):**

```bash
cd /Users/khundeck/working/klankrmkr
make test-no-82.1-leftovers                # must exit 0 (GREEN gate)
go test ./... -count=1 2>&1 | tail -20     # full Go suite GREEN
grep -rn "operator@" pkg/ cmd/ | grep -v _test.go | grep -v vendor
# expected: only acceptable matches (comments, doc strings)
ls infra/modules/ses-shared-rule-set/v1.0.0/   # confirm 3 files exist (Plan 84-02)
ls infra/modules/ses/v2.0.0/                   # confirm 3 files exist (Plan 84-03)
ls infra/modules/ses/v1.0.0/                   # still exists (historical)
```

**Procedure (operator runs interactively, captures output):**

```bash
# Step 1: Build and refresh
make build

# Step 2: Refresh sidecars and management Lambdas
km init --sidecars

# Step 3: Bootstrap the foundation module (NEW)
km bootstrap --shared-ses --dry-run=true
# expect on stderr: "Shared SES rule set: creating" + "Shared SES domain identity: creating"
km bootstrap --shared-ses --dry-run=false

# Verify foundation state:
aws ses describe-active-receipt-rule-set --query 'Name' --output text
# expected: sandbox-email-shared
aws ses list-identities --identity-type Domain --query 'Identities'
# expected: includes the configured email domain

# Step 4: Regional cutover
km init --dry-run=true
# expected plan: REMOVE aws_ses_active_receipt_rule_set.km_sandbox (Phase 82.1 legacy),
#                REMOVE aws_ses_receipt_rule_set.km_sandbox (regional v1.0.0 legacy),
#                REMOVE aws_ses_domain_identity.sandbox / aws_ses_domain_dkim / aws_route53_record.mx + verification
#                  (moved to foundation),
#                ADD aws_ses_receipt_rule.operator_inbound (kph-operator-inbound, rule_set_name = sandbox-email-shared),
#                ADD aws_ses_receipt_rule.sandbox_catchall (kph-sandbox-catchall, rule_set_name = sandbox-email-shared)
km init --dry-run=false

# Step 5: Re-configure (idempotent)
km configure
# verify on prompt: KM_OPERATOR_EMAIL default shows as "operator-kph@sandboxes.<your-domain>"
# accept default (press Enter)
grep operator_email ~/.km/km-config.yaml

# Step 6: Provision a test sandbox
km create profiles/open-dev.yaml --alias phase84-uat
km list   # wait for status running

# Step 7: Email round-trip — sandbox → operator
SB=$(km list | awk '/phase84-uat/ {print $1}')
km shell $SB
# inside the sandbox:
echo "test body from phase 84 uat" > /tmp/uat-body.txt
km-send --subject "phase 84 uat" --body /tmp/uat-body.txt
exit

# Step 8: Read operator inbox (operator-side)
sleep 30
km email read $SB
# verify: a message with subject "phase 84 uat" appears

# Step 9: Doctor check
km doctor 2>&1 | grep -A1 "SES rules"
# expected line: "✓ SES rules healthy (2 rules for prefix \"kph\")"

# Step 10: Cleanup test sandbox
km destroy $SB --remote --yes
```

**Closure-criterion verification (per plan-checker iteration 1, Blocker 2):**

The happy-path steps above cover criteria (b), (e), (f). The following NEW steps cover (a), (c), (d) — they correspond directly to entries in 84-VALIDATION.md "Manual-Only Verifications" table.

```bash
# ============================================================================
# Closure (d): Foundation module idempotency on re-apply
# (84-VALIDATION.md row: "Foundation module plan is idempotent on second km bootstrap")
# ============================================================================

km bootstrap --shared-ses --dry-run=true
# EXPECTED stderr output:
#   Shared SES rule set: reusing existing
#   Shared SES domain identity: reusing existing
# EXPECTED terragrunt plan: "No changes. Your infrastructure matches the configuration."
# IF DRIFT: investigate — register_X=false should map to count=0 resources, so plan is no-op.
# Paste the full plan output into the UAT log.

# ============================================================================
# Closure (a): Second-install setup without opt-out env var
# (84-VALIDATION.md row: "Two installs in same account/region coexist without colliding")
# ============================================================================

# Step a1: Switch to a second prefix. EITHER use a sibling checkout OR override env vars
# from this session. Use a prefix distinct from the first install's (e.g., rg).
export KM_RESOURCE_PREFIX=rg
# DO NOT set KM_SES_ACTIVATE_RULESET anywhere — that env var is gone in Phase 84.

# Step a2: re-configure for the second prefix
km configure
# EXPECTED: prompts derive operator email as operator-rg@sandboxes.<your-domain>
# Accept default; verify in km-config.yaml:
grep operator_email ~/.km/km-config.yaml
# EXPECTED: operator_email: operator-rg@sandboxes.<your-domain>

# Step a3: Plan the second install's km init
km init --dry-run=true
# EXPECTED terragrunt plan:
#   - foundation `ses-shared-rule-set` module: NO-OP (rule set + domain identity already present)
#   - regional `ses` module: ADD aws_ses_receipt_rule.operator_inbound (name "rg-operator-inbound", rule_set_name "sandbox-email-shared")
#                            ADD aws_ses_receipt_rule.sandbox_catchall (name "rg-sandbox-catchall", rule_set_name "sandbox-email-shared")
#   - NO aws_ses_active_receipt_rule_set in the plan
#   - NO destroy of any kph-* rules
# Paste the full plan output into the UAT log.

# Step a4: Apply
km init --dry-run=false

# Step a5: Verify both installs' rules coexist
aws ses describe-receipt-rule-set --rule-set-name sandbox-email-shared \
  --query 'Rules[*].Name' --output table
# EXPECTED: 4 rules total — kph-operator-inbound, kph-sandbox-catchall, rg-operator-inbound, rg-sandbox-catchall

# Step a6: km doctor from the second install — should report kph rules as orphans (expected)
km doctor 2>&1 | grep -A1 "SES rules"
# EXPECTED: "⚠ orphan SES rules: kph-operator-inbound, kph-sandbox-catchall"
# This WARN is normal multi-tenancy — it surfaces the sibling install's rules.

# ============================================================================
# Closure (c): km uninit isolation — sibling rules survive
# (84-VALIDATION.md row: "km uninit on one prefix leaves sibling install's rules intact")
# ============================================================================

# From the second install (KM_RESOURCE_PREFIX=rg still set):
cd infra/live/use1/ses && terragrunt destroy --auto-approve
# EXPECTED: only rg-* rules destroyed; foundation rule set untouched; kph-* rules untouched

# Verify
aws ses describe-receipt-rule-set --rule-set-name sandbox-email-shared \
  --query 'Rules[*].Name' --output table
# EXPECTED: 2 rules — kph-operator-inbound, kph-sandbox-catchall

# Foundation must still be intact:
aws ses describe-active-receipt-rule-set --query 'Name' --output text
# EXPECTED: sandbox-email-shared
aws ses list-identities --identity-type Domain --query 'Identities'
# EXPECTED: includes the configured email domain

# ============================================================================
# Restore the test back to first install context
# ============================================================================
unset KM_RESOURCE_PREFIX     # or whatever value the first install needs
```


**Write the UAT log** at `.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-10-UAT.md` with this structure:

```markdown
---
phase: 84
status: passed         # or "diagnosed" if anything failed
date: <YYYY-MM-DD>
operator: kph
---

# Phase 84 UAT

## Procedure

(Reproduce each step's command + actual output; paste full output for the dry-run plans.)

## Observations

(Any deviations from expected — extra resources planned, AWS errors, unexpected log lines.)

## Result

PASS / FAIL / DIAGNOSED — with explicit reference to each must_have.
```
  </action>
  <verify>
    <automated>test -f .planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-10-UAT.md && grep -q "^status: " .planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-10-UAT.md && grep -q "^## Result" .planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-10-UAT.md && echo "UAT log present with required structure"</automated>
  </verify>
  <done>UAT log exists with frontmatter (phase, status, date, operator) and the three sections (Procedure, Observations, Result). Operator types `passed` or `diagnosed: <gaps>` at the resume prompt.</done>
</task>

<task type="auto">
  <name>Task 2: Closeout — STATE.md + ROADMAP.md final updates + git commit</name>
  <files>.planning/STATE.md, .planning/ROADMAP.md</files>
  <action>
ONLY runs if Task 1's resume signal was `passed`. If `diagnosed: <gaps>` was returned, a follow-up `/gsd:plan-phase 84 --gaps` will close residual issues; this closeout is deferred to that follow-up.

When `passed`:

1. Open `.planning/STATE.md`. Locate the "Current Position" section. Update:
   - `Phase:` → "84 (ses-per-install-rule-namespacing-via-operator-address-prefix) — ALL 10 PLANS COMPLETE"
   - `stopped_at:` (frontmatter) → "Completed Phase 84 — SES per-install rule namespacing via operator address prefix"
   - `last_updated:` → current ISO timestamp
   - Increment `completed_phases` in the progress block by 1.

2. Open `.planning/ROADMAP.md`. In the Phase 84 entry's Plans list, mark each `- [ ]` → `- [x]` for plans 84-01..84-10. Update the count line to `**Plans:** 10/10 plans complete`.

3. Stage and commit:
   ```bash
   node "$HOME/.claude/get-shit-done/bin/gsd-tools.cjs" commit "docs(phase-84): complete phase execution — SES shared rule set + per-install address namespacing landed" --files .planning/STATE.md .planning/ROADMAP.md .planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-10-UAT.md .planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-10-SUMMARY.md
   ```

4. Confirm git status is clean for the planning files.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && grep -q "ALL 10 PLANS COMPLETE\|Completed Phase 84" .planning/STATE.md && echo "STATE updated" && grep -q "10/10 plans complete" .planning/ROADMAP.md && echo "ROADMAP updated" && git log --oneline -1 | grep -q "phase-84" && echo "commit landed"</automated>
  </verify>
  <done>STATE.md reflects Phase 84 complete; ROADMAP.md Phase 84 Plans block is all-checked with "10/10 plans complete"; closeout commit exists.</done>
</task>

</tasks>

<verification>
- `.planning/phases/84-.../84-10-UAT.md` exists with the documented frontmatter and structure.
- STATE.md and ROADMAP.md reflect Phase 84 completion.
- All 6 ROADMAP Phase 84 "Phase closes when..." criteria (a)-(f) are observably true in the UAT log.
- If UAT was `diagnosed`, a follow-up `/gsd:plan-phase 84 --gaps` will close residual issues; Task 2 is deferred.
</verification>

<success_criteria>
- Operator executes the full Phase 84 upgrade procedure against a real AWS account.
- All Phase 84 must_haves observably true.
- UAT record + STATE/ROADMAP closeout committed.
</success_criteria>

<output>
After completion, create `.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-10-SUMMARY.md`
</output>
