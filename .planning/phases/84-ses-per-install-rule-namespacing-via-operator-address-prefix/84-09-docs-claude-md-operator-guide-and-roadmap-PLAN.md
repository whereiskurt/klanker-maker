---
phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
plan: 09
type: execute
wave: 3
depends_on: [02, 03, 04, 05, 06, 07, 08]
files_modified:
  - CLAUDE.md
  - OPERATOR-GUIDE.md
  - .planning/ROADMAP.md
autonomous: true
requirements:
  - SES-SHARED-RULESET
  - SES-PER-INSTALL-RULES
  - SES-82.1-REMOVAL
  - SES-CONFIGURE-WIRING
  - SES-DOCTOR-ORPHANS

must_haves:
  truths:
    - "CLAUDE.md has a new '### Phase 84: SES per-install rule namespacing via operator address prefix' section that documents the operator address format, the shared rule set, the bootstrap step, and the doctor check"
    - "CLAUDE.md's Email section's KM_OPERATOR_EMAIL description is updated to reflect the prefix-aware derivation"
    - "OPERATOR-GUIDE.md gains a 'Phase 84 upgrade' runbook section that documents: rebuild km, refresh sidecars, run km bootstrap --shared-ses, run km init cutover, re-configure, verify with km doctor — mirrors the rebuild-step pattern from Phase 82-10 / 83-07"
    - "ROADMAP.md Phase 82.1's plan list marks 82.1-03-PLAN.md as superseded by Phase 84 (the historical plan file in .planning/phases/82.1-*/ is NOT modified)"
    - "ROADMAP.md Phase 84 entry's Plans block lists all 10 Phase 84 plans with their slug-based filenames"
  artifacts:
    - path: "CLAUDE.md"
      provides: "Phase 84 section + updated Email-section description"
    - path: "OPERATOR-GUIDE.md"
      provides: "Phase 84 upgrade runbook + shared-SES + cutover sequence"
    - path: ".planning/ROADMAP.md"
      provides: "Phase 82.1-03 marked superseded; Phase 84 Plans list populated"
  key_links:
    - from: "CLAUDE.md Phase 84 section"
      to: "OPERATOR-GUIDE.md Phase 84 upgrade runbook"
      via: "see also reference"
      pattern: "OPERATOR-GUIDE.md.*Phase 84"
---

<objective>
Write the documentation that operators read to understand Phase 84:

1. CLAUDE.md — new `### Phase 84` section + Email-section update (KM_OPERATOR_EMAIL now prefix-aware)
2. OPERATOR-GUIDE.md — new "Phase 84 upgrade" runbook section (mirroring Phase 82's runbook pattern)
3. ROADMAP.md — mark Phase 82.1-03 superseded; populate the Phase 84 Plans block

Plan 84-08 already removed the Phase 82.1 doc surface; this plan adds the Phase 84 replacement.

Output:
- 3 files modified
- 0 code changes
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/ROADMAP.md
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-CONTEXT.md
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-RESEARCH.md

@CLAUDE.md
@OPERATOR-GUIDE.md

<interfaces>
Content templates (the executor will copy these into the destination files).

CLAUDE.md additions:

(A) New section to insert WHERE the Phase 82.1 section was deleted by Plan 84-08 (between the Phase 82 section and `## CLI`). Heading depth `###`. Content:

The section title is: "Phase 84: SES per-install rule namespacing via operator address prefix"

It documents:
- Operator address format: `operator-{resource_prefix}@{email_subdomain}.{parent_domain}` (e.g. `operator-km@sandboxes.example.com` for the default install).
- Shared rule set name: `sandbox-email-shared`, owned by `infra/modules/ses-shared-rule-set/v1.0.0/`, has `lifecycle.prevent_destroy = true`.
- Per-install rules: `{prefix}-operator-inbound` and `{prefix}-sandbox-catchall`, both pointing at `sandbox-email-shared` by string name.
- Bootstrap: `km bootstrap --shared-ses` provisions the foundation; idempotent via auto-detect (Phase 80 cluster-irsa pattern).
- Doctor check: `km doctor` reports `✓ SES rules healthy` or `⚠ orphan SES rules: <list>`.

Then a fenced bash code block titled "Phase 84 upgrade procedure (one-time, existing installs)" with:
```
make build
km init --sidecars
km bootstrap --shared-ses
km init --dry-run=true
km init --dry-run=false
km configure
km doctor
```

End with: "See `OPERATOR-GUIDE.md` § Phase 84 upgrade for the detailed runbook and two-install coexistence scenario."

(B) Update the "Key environment variables" table row for `KM_OPERATOR_EMAIL` in the Email section. New cell text: "Operator inbox address (operator-{resource_prefix}@{email_subdomain}.{parent_domain}; derived by `km configure` from Phase 84 onward)".

OPERATOR-GUIDE.md additions — new section "Phase 84 upgrade — shared SES rule set + per-install rules" at the same heading depth as the deleted Phase 82.1 SES section. Includes:

1. Prerequisites bullet list (km build with Phase 84 contents; AWS credentials with foundation + regional scope; for second-install: primary operator has already run `km bootstrap --shared-ses`).

2. Step-by-step block "Single install or primary install in a multi-install account":
```
make build
km init --sidecars
km bootstrap --shared-ses --dry-run=true
km bootstrap --shared-ses --dry-run=false
km init --dry-run=true
km init --dry-run=false
km configure
km doctor
```

3. Step-by-step block "Second install in an existing account" with same shape but explaining auto-detect → no-op apply; also explains that `km doctor` will report orphans for the sibling install's rules — this is EXPECTED.

4. Rollback note: Phase 84 hard-removes Phase 82.1; rollback requires checking out a pre-Phase-84 commit. No in-place rollback flag.

5. Validation block — three commands:
- `aws ses describe-active-receipt-rule-set --query 'Name'` → expect `sandbox-email-shared`
- `aws ses describe-receipt-rule-set --rule-set-name sandbox-email-shared --query 'Rules[?starts_with(Name, ...)].Name'` (filtered by `$KM_RESOURCE_PREFIX`)
- `km doctor | grep "SES rules"` → expect green-check line

ROADMAP.md edits:

(A) In the Phase 82.1 entry's Plans list, update the 82.1-03 line. Change:
- from: `- [x] 82.1-03-PLAN.md — SES activate_rule_set opt-in variable: count-gate aws_ses_active_receipt_rule_set + operator checkpoint (Wave 2, Terraform + docs)`
- to: `- [x] 82.1-03-PLAN.md — SES activate_rule_set opt-in variable: count-gate aws_ses_active_receipt_rule_set + operator checkpoint (Wave 2, Terraform + docs) — **SUPERSEDED by Phase 84**`

Do NOT delete the line. The historical 82.1-03 plan file in `.planning/phases/82.1-multi-instance-polish-.../` stays untouched.

(B) In the Phase 84 entry, populate the Plans block with the actual 10 plan filenames (after the existing **Plans:** line — which should read `**Plans:** 10 plans`):

The 10 plan list entries are:
- 84-01-wave0-test-scaffolds-PLAN.md
- 84-02-foundation-ses-shared-rule-set-module-PLAN.md
- 84-03-regional-ses-v2-module-PLAN.md
- 84-04-km-configure-operator-email-derivation-PLAN.md
- 84-05-userdata-and-ses-pkg-operator-literal-PLAN.md
- 84-06-email-handler-recipient-verification-PLAN.md
- 84-07-km-bootstrap-shared-ses-and-doctor-check-PLAN.md
- 84-08-phase-82.1-hard-removal-and-grep-gate-PLAN.md
- 84-09-docs-claude-md-operator-guide-and-roadmap-PLAN.md
- 84-10-operator-uat-checkpoint-PLAN.md

Each entry follows the pattern `- [ ] <filename> — <brief objective>` (the brief objective is sourced from each plan's `<objective>` block).

(C) In REQUIREMENTS.md's traceability table (at the bottom of the file), append rows for Phase 84's 7 requirement IDs marked `Planned`:
- SES-PREFIX-ADDRESS → Phase 84 → Planned
- SES-SHARED-RULESET → Phase 84 → Planned
- SES-PER-INSTALL-RULES → Phase 84 → Planned
- SES-82.1-REMOVAL → Phase 84 → Planned
- SES-CONFIGURE-WIRING → Phase 84 → Planned
- SES-HANDLER-LOOKUP → Phase 84 → Planned
- SES-DOCTOR-ORPHANS → Phase 84 → Planned

(Note: REQUIREMENTS.md is the canonical traceability table; ROADMAP.md just lists requirement IDs in each phase entry.)
</interfaces>
</context>

<tasks>

<task type="auto">
  <name>Task 1: Add Phase 84 section to CLAUDE.md + update KM_OPERATOR_EMAIL description in Email section</name>
  <files>CLAUDE.md</files>
  <action>
1. Read CLAUDE.md around the Phase 82 section to find where Plan 84-08 left a gap (between Phase 82 and `## CLI`).

2. Insert the new "### Phase 84: SES per-install rule namespacing via operator address prefix" section using the content described in the `<interfaces>` block. Preserve heading depth (`###`), surrounding blank lines, and fenced-code-block conventions used elsewhere in CLAUDE.md.

3. Locate the "Key environment variables" table inside the Email section. Update the `KM_OPERATOR_EMAIL` row's description cell to the new prefix-aware text per the `<interfaces>` block.

4. Visual scan: confirm the surrounding markdown structure is intact — table formatting unchanged, Phase 82 section unchanged, `## CLI` unchanged.

5. Confirm rendering: open CLAUDE.md in your editor's markdown preview; confirm no broken backticks, no orphaned headings.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && grep -q "Phase 84: SES per-install rule namespacing" CLAUDE.md && echo "Phase 84 section present" && grep -q "operator inbox address" CLAUDE.md -i && echo "KM_OPERATOR_EMAIL description updated"</automated>
  </verify>
  <done>CLAUDE.md has the new Phase 84 section; KM_OPERATOR_EMAIL row reflects the prefix-aware derivation; Phase 82 section unchanged; no broken markdown.</done>
</task>

<task type="auto">
  <name>Task 2: Add Phase 84 upgrade runbook to OPERATOR-GUIDE.md</name>
  <files>OPERATOR-GUIDE.md</files>
  <action>
1. Read OPERATOR-GUIDE.md around the location where Plan 84-08 deleted the Phase 82.1 SES handoff section (formerly lines 646-677).

2. Insert the new "Phase 84 upgrade — shared SES rule set + per-install rules" section. Use the content described in the `<interfaces>` block (Prerequisites + Single-install runbook + Second-install runbook + Rollback note + Validation commands).

3. Match the surrounding heading depth — if the deleted section was an `##` heading, the new section is `##`; if `###`, `###`. Confirm by reading the file structure.

4. Preserve OPERATOR-GUIDE.md's overall structure: do not reorganize unrelated sections; do not delete other content.

5. Confirm rendering: verify that the bash code blocks have proper triple-backtick syntax and that the prerequisite bullet list renders cleanly.

6. Cross-reference: the new section should end with a "See also: CLAUDE.md § Phase 84" pointer, mirroring how the deleted Phase 82.1 section cross-referenced CLAUDE.md.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && grep -q "Phase 84 upgrade" OPERATOR-GUIDE.md && echo "Phase 84 runbook section present" && grep -q "km bootstrap --shared-ses" OPERATOR-GUIDE.md && echo "bootstrap step documented" && grep -q "sandbox-email-shared" OPERATOR-GUIDE.md && echo "rule set name documented"</automated>
  </verify>
  <done>OPERATOR-GUIDE.md has the new Phase 84 upgrade section with bootstrap step, two-install scenario, rollback note, and validation commands.</done>
</task>

<task type="auto">
  <name>Task 3: Mark Phase 82.1-03 superseded + populate Phase 84 Plans list in ROADMAP.md + add traceability rows to REQUIREMENTS.md</name>
  <files>.planning/ROADMAP.md, .planning/REQUIREMENTS.md</files>
  <action>
1. Open `.planning/ROADMAP.md`.

2. Locate the Phase 82.1 entry's Plans list (~line 1786-1788). Update the `82.1-03-PLAN.md` bullet to append `— **SUPERSEDED by Phase 84**` per the `<interfaces>` block. Do NOT delete the line; do NOT modify the historical plan file in `.planning/phases/82.1-*/`.

3. Locate the Phase 84 entry (~line 1806 onward). Below the existing `**Requirements:**` and `**Depends on:**` lines, ADD:
   ```
   **Plans:** 10 plans

   Plans:
   - [ ] 84-01-wave0-test-scaffolds-PLAN.md — Wave 0 Nyquist failing-test infrastructure (11 stubs + Makefile grep gate)
   - [ ] 84-02-foundation-ses-shared-rule-set-module-PLAN.md — New foundation Terraform module + live wiring (rule set + active + domain identity + DKIM + MX + verification)
   - [ ] 84-03-regional-ses-v2-module-PLAN.md — New regional ses/v2.0.0 module owning prefix-named rules only + live dir cutover
   - [ ] 84-04-km-configure-operator-email-derivation-PLAN.md — deriveOperatorEmail helper + runConfigure integration + --reset-prefix clears stored email
   - [ ] 84-05-userdata-and-ses-pkg-operator-literal-PLAN.md — Replace operator@ literals in pkg/compiler/userdata.go + pkg/aws/ses.go; export KM_OPERATOR_EMAIL in userdata env files
   - [ ] 84-06-email-handler-recipient-verification-PLAN.md — Email-create-handler verifies operator-${prefix}@; silent drops foreign; updates outbound From
   - [ ] 84-07-km-bootstrap-shared-ses-and-doctor-check-PLAN.md — km bootstrap --shared-ses (auto-detect via SESIdentityLister) + km doctor checkSESRules + go.mod aws-sdk-go-v2/service/ses
   - [ ] 84-08-phase-82.1-hard-removal-and-grep-gate-PLAN.md — Delete activate_rule_set from infra + Phase 82.1 sections from CLAUDE.md + OPERATOR-GUIDE.md; wire grep gate into make test
   - [ ] 84-09-docs-claude-md-operator-guide-and-roadmap-PLAN.md — CLAUDE.md Phase 84 section + OPERATOR-GUIDE.md upgrade runbook + ROADMAP closeout (this plan)
   - [ ] 84-10-operator-uat-checkpoint-PLAN.md — Operator UAT against real AWS (NOT autonomous)
   ```

4. Open `.planning/REQUIREMENTS.md`. Navigate to the Traceability table at the bottom (around line 235+). Append 7 rows in the existing `| ID | Phase | Status |` format:
   ```
   | SES-PREFIX-ADDRESS | Phase 84 | Planned |
   | SES-SHARED-RULESET | Phase 84 | Planned |
   | SES-PER-INSTALL-RULES | Phase 84 | Planned |
   | SES-82.1-REMOVAL | Phase 84 | Planned |
   | SES-CONFIGURE-WIRING | Phase 84 | Planned |
   | SES-HANDLER-LOOKUP | Phase 84 | Planned |
   | SES-DOCTOR-ORPHANS | Phase 84 | Planned |
   ```

5. Update the REQUIREMENTS.md "Coverage" section at the bottom (`- v1 requirements: ...`) if the counts include Phase 84 IDs. The IDs are synthetic and may not be tracked in the v1 totals — leave totals alone unless there's an existing pattern for synthetic IDs.

6. Verify both files render correctly as markdown (no broken tables).
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && grep -q "SUPERSEDED by Phase 84" .planning/ROADMAP.md && echo "82.1-03 marked superseded" && grep -c "84-..-.*PLAN.md" .planning/ROADMAP.md | awk '$1 >= 10 {print "10 Phase 84 plans listed"}' && grep -q "SES-PREFIX-ADDRESS | Phase 84" .planning/REQUIREMENTS.md && echo "traceability updated"</automated>
  </verify>
  <done>ROADMAP.md has the 82.1-03 supersede note, the Phase 84 Plans block with all 10 filenames, and the Plans count line. REQUIREMENTS.md traceability table has 7 new Phase 84 rows.</done>
</task>

</tasks>

<verification>
- CLAUDE.md has the Phase 84 section + updated KM_OPERATOR_EMAIL description.
- OPERATOR-GUIDE.md has the Phase 84 upgrade runbook.
- ROADMAP.md marks 82.1-03 as superseded and lists all 10 Phase 84 plans.
- REQUIREMENTS.md traceability has 7 new Phase 84 rows.
- No broken markdown.
</verification>

<success_criteria>
- All operator-facing docs reflect the Phase 84 design.
- The historical Phase 82.1 plan file in `.planning/phases/82.1-*/` is UNTOUCHED.
- ROADMAP + REQUIREMENTS traceability is consistent with the plan structure.
</success_criteria>

<output>
After completion, create `.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-09-SUMMARY.md`
</output>
