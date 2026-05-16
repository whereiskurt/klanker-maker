---
phase: 83-km-event-operator-controlled-eventbridge
plan: 07
type: execute
wave: 4
depends_on: [05, 06]
files_modified:
  - CLAUDE.md
  - docs/operator-events.md
  - .planning/STATE.md
  - .planning/ROADMAP.md
autonomous: false
requirements:
  - operator-feature-83
must_haves:
  truths:
    - "CLAUDE.md has a new section '## Operator-controlled EventBridge (Phase 83)' matching the Phase 73/79/80 format: heading, command table, profile/schema reference, one-time setup, important notes block"
    - "docs/operator-events.md exists as the full operator runbook — installation, km event add/list/rm/apply walkthroughs, manifest schema reference, escape hatches (--hcl, --event-pattern), troubleshooting matrix"
    - "Operator UAT passes end-to-end: km init applies operator-event-bus stack; km event add 'every monday at 1pm' doctor --name nightly-doctor --dry-run=false creates a real EventBridge rule on the custom bus targeting the km-runner Lambda; rule visible in AWS console with state ENABLED; km event list shows the new entry; km event rm cleanly destroys the rule + prunes config; idempotent re-runs print existing ARN"
    - "Manual verifications from 83-VALIDATION.md are exercised by the UAT checkpoint and signed off by the operator"
    - "STATE.md entry added marking Phase 83 complete with the locked decisions + UAT outcome"
    - "ROADMAP.md Phase 83 entry updated from 0 plans to 7 plans with all checkboxes checked"
  artifacts:
    - path: "CLAUDE.md"
      provides: "Phase 83 operator-events section in main project guide"
      contains: "Operator-controlled EventBridge"
    - path: "docs/operator-events.md"
      provides: "Full operator runbook for km event"
      contains: "km event add"
  key_links:
    - from: "CLAUDE.md"
      to: "docs/operator-events.md"
      via: "See docs/operator-events.md for the full operator guide."
      pattern: "operator-events.md"
---

<objective>
Phase 83 closeout: documentation + end-to-end operator UAT.

Document the new km event command tree so operators know how to use it, then run a real-AWS UAT against
the operator's klanker-application profile to prove the full lifecycle (add → list → rule fires → rm) works
on actual infrastructure.

This is the last plan in Phase 83. After this plan: km event is shipped, km at recurring is hard-cut,
km doctor warns on drift, the singleton km-runner Lambda is deployed, the custom EventBridge bus is provisioned
with 7-day archive.
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-CONTEXT.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-RESEARCH.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-VALIDATION.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-02-SUMMARY.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-03-SUMMARY.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-04-SUMMARY.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-05-SUMMARY.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-06-SUMMARY.md
@CLAUDE.md
@docs/slack-notifications.md
@docs/vscode.md

<interfaces>
CLAUDE.md style reference — Phase 73 "VS Code Remote-SSH" or Phase 79 "Presence daemon" or Phase 80 "Cross-account k8s integrations" sections. Each has:
- Heading: `## <Feature name> (Phase <N>)`
- One-sentence introduction
- Sub-section: profile/schema field table (if applicable) — skip for Phase 83 since there's no profile change
- Sub-section: Operator state (paths/files)
- Sub-section: One-time setup with commands
- Sub-section: Per-feature workflow (commands)
- Sub-section: Important workflow notes (bullet list)
- Tail: "See docs/<feature>.md for the full operator guide."

docs/operator-events.md format reference — docs/slack-notifications.md or docs/vscode.md:
- Title: # Operator-controlled EventBridge events
- Overview (what + why two-lane model)
- Prerequisites (km init applied, build/km-runner.zip present)
- Installation (make build && km init for fresh setups; make build && km init --lambdas + km init for existing setups)
- Profile field reference: (none — Phase 83 has no profile changes)
- Manifest schema reference (the EventManifest YAML structure with all fields)
- CLI walkthroughs:
  - km event add 'every monday at 1pm' doctor --name nightly-doctor
  - km event add 'in 2 hours' doctor --name spot-doctor (one-off; converted to pinned cron)
  - km event add --event-pattern s3-upload.json --target arn:aws:sns:... --name on-s3-upload
  - km event apply events/nightly-doctor.yaml
  - km event list
  - km event rm nightly-doctor
- Escape hatches: --hcl <file>, --event-pattern <file>, multi-target via --hcl
- Troubleshooting matrix (table: symptom → likely cause → fix)
- Security model (custom bus + IAM scope; km-runner gets full operator policy)
- Cost notes (archive ~$0.10/GB/mo; km-runner Lambda is per-invocation pennies)
- Comparison table: km at vs km event (when to use each)

UAT script — verbatim against operator's klanker-application AWS profile:
1. make build && make build-km-runner (rebuild after Phase 83 work)
2. km init (apply the new operator-event-bus regional module; expect "Apply complete" output)
3. Verify: aws lambda get-function --function-name km-km-runner --region us-east-1 returns ARN; aws events list-event-buses --region us-east-1 includes km-operator-events
4. km event --dry-run=true add 'every monday at 1pm' doctor --name uat-nightly-doctor --target-km 'doctor --all-regions'
5. Expect: "(dry-run) terragrunt plan complete" — no state mutation
6. km event --dry-run=false add 'every monday at 1pm' doctor --name uat-nightly-doctor --target-km 'doctor --all-regions'
7. Expect: terragrunt apply success + "Event 'uat-nightly-doctor' provisioned: arn:aws:events:..."
8. km event list — expect tabwriter table with uat-nightly-doctor row
9. AWS console: navigate to EventBridge → Buses → km-operator-events → Rules; verify uat-nightly-doctor is ENABLED, target = km-km-runner Lambda
10. Trigger manually: aws events put-events --entries 'Source=manual,DetailType=test,Detail="{}"' --event-bus-name km-operator-events; check CloudWatch /aws/lambda/km-km-runner for the invocation log (km running doctor --all-regions inside the Lambda)
11. km doctor — expect operator_event_rules_healthy: OK
12. Manually delete the rule via console: aws events delete-rule --name km-event-uat-nightly-doctor --event-bus-name km-operator-events
13. km doctor — expect operator_event_rules_healthy: WARN with uat-nightly-doctor in the message
14. km event rm uat-nightly-doctor (cleanup) — expect "Event 'uat-nightly-doctor' destroyed"; km event list shows no entries
15. Regression tests:
   - km at --cron 'cron(...)' destroy <sb> → "unknown flag: --cron"
   - km at 'every monday' destroy <sb> → "recurring expressions not supported; use km event"
   - km at '5pm tomorrow' destroy <sb> → success (regression preserved)

Track UAT outcomes in 83-UAT.md (operator fills in PASS/FAIL/notes per step).
</interfaces>
</context>

<tasks>

<task type="auto">
  <name>Task 1: Write docs/operator-events.md as the full operator runbook</name>
  <files>docs/operator-events.md</files>
  <behavior>
    - Title: # Operator-controlled EventBridge events (Phase 83)
    - Sections in order:
      1. Overview — two-lane model (km at vs km event), why custom bus, why singleton km-runner, archive default ON
      2. Prerequisites — Phase 80 (cluster) is independent; need klanker-application credentials; AWS region with km init already bootstrapped
      3. Installation — `make build && make build-km-runner && km init` (fresh) OR `make build && make build-km-runner && km init` (existing — picks up new operator-event-bus regional module)
      4. Manifest schema reference — full YAML example with annotations for every field (name, schedule, event_pattern, target, input, dlq, description, archive)
      5. CLI walkthroughs — examples per the <interfaces> list, each showing input + expected output
      6. Escape hatches — --hcl example, --event-pattern example
      7. Multi-target — Phase 83 v1 limitation: --target accepts list but uses first; for true fanout use --hcl
      8. Troubleshooting matrix:
         | Symptom | Likely cause | Fix |
         |---------|--------------|-----|
         | "operator-event-bus stack not found" | km init not run since Phase 83 deployed | `km init` to apply the regional bootstrap |
         | "Invalid schedule expression" with at(...) | at()→cron conversion missed | Should not happen; report bug |
         | "AccessDenied: events:PutEvents" | km-operator-policy not updated | terragrunt apply on km-operator-policy/v1.0.0 |
         | km event rm: "stack not found" | already destroyed | safe; idempotent — entry pruned from config |
         | km doctor: operator_event_rules_healthy WARN | rule manually deleted via console | re-create with `km event add` or remove via `km event rm` |
      9. Security model — custom bus isolation, IAM scope (km-runner has full operator policy by design, see CONTEXT.md), archive permissions
      10. Cost notes — archive ~$0.10/GB/mo, km-runner Lambda free tier ample for monthly-rate workloads
      11. Comparison: km at vs km event — table with columns "use case / mechanism / persistence / cleanup"

    AVOID:
    - Documenting deferred features (type:code target, km event replay CLI) as if they exist — note them as "deferred to v2"
    - Real AWS account IDs in examples — use 123456789012 placeholder (per memory feedback_generic_doc_examples)
    - Naming real tenants — use "example.com" / "Corporate"

    REFERENCES:
    - docs/slack-notifications.md for format/length reference (~300-500 lines is normal)
    - docs/vscode.md for the workflow-walkthrough cadence
    - Plan 83-05 SUMMARY for the final flag list on `km event add`
    - Plan 83-02 SUMMARY for the EventManifest schema fields
  </behavior>
  <action>
    1. Read docs/slack-notifications.md fully for format reference (Phase 73 had a similar artifact at docs/vscode.md).
    2. Read all five SUMMARY files (83-02..83-06) to get the canonical CLI flag list, schema fields, error wording, doctor check name.
    3. Draft docs/operator-events.md with all 11 sections per <behavior>.
    4. Use example manifests from pkg/events/testdata/ in the schema-reference section.
    5. Markdown-lint the result: no broken links, all code blocks fenced with language hints, tables have aligned columns.

    AVOID:
    - Bash one-liner code blocks longer than ~5 lines without comments — break into numbered steps
    - Real customer names, tenant IDs, or production resource ARNs
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && test -f docs/operator-events.md && test $(wc -l < docs/operator-events.md) -gt 100 && grep -c "km event" docs/operator-events.md</automated>
  </verify>
  <done>
    docs/operator-events.md exists, contains at least 100 lines, references "km event" at least 10 times. All 11 sections present. No real customer names or production ARNs.
  </done>
</task>

<task type="auto">
  <name>Task 2: Add Phase 83 section to CLAUDE.md following Phase 73/79/80 format</name>
  <files>CLAUDE.md</files>
  <behavior>
    - Locate the latest phase section in CLAUDE.md (likely Phase 80 "## Cross-account k8s integrations").
    - Insert a new section AFTER Phase 80 (preserves chronological order): `## Operator-controlled EventBridge (Phase 83)`
    - Section structure (matching Phase 80):
      - One-paragraph introduction explaining the two-lane model and why this exists alongside km at
      - Sub-heading: ### What it does — paragraph describing the flow (NL → cron → terragrunt apply → EventBridge rule → km-runner Lambda → CloudWatch logs)
      - Sub-heading: ### CLI table
        ```
        | Subcommand | Purpose |
        |---|---|
        | `km event add '<when>' <cmd> --name <slug>` | Add a durable, declarative event rule |
        | `km event apply events/<name>.yaml` | Apply a hand-edited manifest |
        | `km event list` | List configured rules |
        | `km event rm <name>` | Destroy a rule + prune config |
        ```
      - Sub-heading: ### km-config.yaml schema — show the events: key with one example entry
      - Sub-heading: ### One-time setup
        ```bash
        make build
        make build-km-runner      # builds the km-runner Lambda zip (km binary baked in)
        km init                   # applies operator-event-bus regional module
        ```
      - Sub-heading: ### Per-rule workflow
        ```bash
        km event add 'every monday at 1pm' doctor --name nightly-doctor --dry-run=false
        km event list
        km event rm nightly-doctor
        ```
      - Sub-heading: ### Important workflow notes (bullet list)
        - `make build-km-runner` is required after Phase 83 ships so km init has a Lambda zip to deploy.
        - `km at --cron` was removed — recurring expressions belong on `km event`. `km at` is now one-off-only.
        - Custom bus `${prefix}-operator-events` is isolated from the default bus. Cross-bus event routing requires --hcl escape hatch.
        - Archive enabled by default (7-day retention) on the bus; --no-archive currently disables logging of the rule in CONFIG only (per-rule archive opt-out not enforced at module level in v1 — document this honestly per Plan 83-05 SUMMARY).
        - km doctor `operator_event_rules_healthy` check WARNs (not ERROR) on missing/disabled rules — matches Slack inbound pattern.
      - Tail: `See docs/operator-events.md for the full operator guide including manifest schema, escape hatches, and troubleshooting matrix.`

    AVOID:
    - Mentioning deferred features (type:code, replay CLI) as if shipping
    - Duplicating long content already in docs/operator-events.md — CLAUDE.md is a quick reference, the docs file is the encyclopedia
    - Real account IDs

    REFERENCES:
    - CLAUDE.md "## Cross-account k8s integrations (Phase 80)" section — verbatim format reference
    - Plan 83-05 SUMMARY for final flag list
  </behavior>
  <action>
    1. Read CLAUDE.md to find the Phase 80 section ("## Cross-account k8s integrations (Phase 80)") — note its structure end-to-end.
    2. Insert the new "## Operator-controlled EventBridge (Phase 83)" section immediately after Phase 80, before the next section.
    3. Match Phase 80's exact heading depth and code block formatting.
    4. Test: open CLAUDE.md, verify Phase 80 section is unchanged, Phase 83 section follows.

    AVOID: Inserting in the wrong location — Phase 83 must come after Phase 80, not after Phase 67 (sections are loosely chronological).
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && grep -c "Operator-controlled EventBridge" CLAUDE.md && grep -c "km event" CLAUDE.md</automated>
  </verify>
  <done>
    CLAUDE.md contains the new "## Operator-controlled EventBridge (Phase 83)" section. References to "km event" appear at least 4 times in CLAUDE.md. The section has all 6 sub-headings per <behavior>.
  </done>
</task>

<task type="checkpoint:human-verify" gate="blocking">
  <name>Task 3: Operator UAT — end-to-end km event lifecycle against real AWS</name>
  <files>none</files>
  <action>
    Operator-driven test — Claude does NOT execute the bash commands in <how-to-verify>. The 12-step UAT requires live AWS credentials, real terragrunt apply against the operator's klanker-application account, and human inspection of CloudWatch logs + EventBridge console. Capture stdout from each step and record outcomes in .planning/phases/83-km-event-operator-controlled-eventbridge/83-UAT.md (PASS/FAIL/notes).

    On approval: proceed to Task 4 (closeout).
    On failure: halt, document which step failed; file a gap-closure plan via /gsd:plan-phase 83 --gaps if the bug is in plan-83-XX code.
  </action>
  <verify>
    <automated>echo "Manual UAT — see <how-to-verify> for the 12-step lifecycle test against real AWS; operator records results in 83-UAT.md"</automated>
  </verify>
  <done>
    Operator has executed all 12 UAT steps and recorded outcomes in 83-UAT.md. All PASS, or any FAILs filed as a gap-closure plan against Phase 83.
  </done>
  <what-built>
    Phase 83 ships in 4 waves:
    - Wave 0 (Plan 83-01): Test scaffolds — all SKIP at this point
    - Wave 1 (Plans 83-02, 83-03): pkg/events + EventConfig + km-operator-policy update + 2 new Terraform modules
    - Wave 2 (Plans 83-04, 83-05): km-runner Lambda + km event CLI
    - Wave 3 (Plan 83-06): km at hard cut + km init folding + km doctor check

    By the time this task runs, the operator workstation has:
    - A fresh km binary with km event command tree
    - build/km-runner.zip ready
    - km-operator-policy widened to include the custom bus
    - infra/modules/operator-event-bus/v1.0.0/ and operator-event-rule/v1.0.0/ exist
    - init.go updated to apply operator-event-bus

    UAT proves the WHOLE chain works against real AWS infrastructure in the klanker-application account.
  </what-built>
  <how-to-verify>
    Operator MUST:

    1. **Rebuild and bootstrap** (5 minutes):
       ```bash
       cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02
       make build
       make build-km-runner
       km init   # applies the new operator-event-bus regional module (us-east-1 by default)
       ```
       EXPECT: terragrunt apply completes; no errors. The output should mention `operator-event-bus` in the applied modules list.

    2. **Verify infrastructure is up** (1 minute):
       ```bash
       aws --profile klanker-application lambda get-function --function-name km-km-runner --region us-east-1 --query 'Configuration.[FunctionName,Runtime,Architectures]'
       aws --profile klanker-application events list-event-buses --region us-east-1 | grep -E "(km-operator-events|name)"
       aws --profile klanker-application events list-archives --region us-east-1 | grep -E "(km-operator-events-archive|retention)"
       ```
       EXPECT: Function exists with provided.al2023 + arm64; custom bus listed; archive listed with retention_days=7.

    3. **km event add dry-run** (no state mutation):
       ```bash
       km event add 'every monday at 1pm' doctor --name uat-nightly-doctor --target-km 'doctor --all-regions' --dry-run=true
       ```
       EXPECT: terragrunt plan output, ends with "(dry-run) terragrunt plan complete — re-run with --dry-run=false to apply". No errors. km-config.yaml unchanged.

    4. **km event add real apply**:
       ```bash
       km event add 'every monday at 1pm' doctor --name uat-nightly-doctor --target-km 'doctor --all-regions' --dry-run=false
       ```
       EXPECT: terragrunt apply succeeds; prints "Event 'uat-nightly-doctor' provisioned: arn:aws:events:...". km-config.yaml now contains an events: list with the new entry.

    5. **km event list**:
       ```bash
       km event list
       ```
       EXPECT: tabwriter table with one row: NAME=uat-nightly-doctor, TYPE=schedule, EXPRESSION=cron(0 13 ? * 2 *), TARGET TYPE=km, TARGETS=arn:aws:lambda:...

    6. **Verify in AWS console**:
       Navigate to EventBridge → Buses → km-operator-events → Rules
       EXPECT: Rule "km-event-uat-nightly-doctor" exists, state=ENABLED, target=km-km-runner Lambda.

    7. **Trigger manually** (to test the Lambda exec path):
       ```bash
       aws --profile klanker-application events put-events \
         --entries 'Source=manual,DetailType=test,Detail={"command":"doctor --all-regions"},EventBusName=km-operator-events' \
         --region us-east-1
       sleep 15
       aws --profile klanker-application logs tail /aws/lambda/km-km-runner --since 30s --region us-east-1
       ```
       EXPECT: CloudWatch shows km-runner invoked, output contains the doctor command output (sandbox count, doctor check results, etc.).

       NOTE: This manual put-events skips the rule's scheduling — it tests that the bus + km-runner work. The actual rule firing at 1pm Monday is harder to test in real-time; verify via rule history instead if you can wait.

    8. **km doctor — healthy state**:
       ```bash
       km doctor 2>&1 | grep operator_event_rules_healthy
       ```
       EXPECT: "operator_event_rules_healthy: OK"

    9. **Drift test — manually break the rule**:
       ```bash
       aws --profile klanker-application events delete-rule \
         --name km-event-uat-nightly-doctor \
         --event-bus-name km-operator-events \
         --region us-east-1
       km doctor 2>&1 | grep operator_event_rules_healthy
       ```
       EXPECT: "operator_event_rules_healthy: WARN — 1 rule(s) not ENABLED or missing: uat-nightly-doctor"

    10. **km event rm cleanup**:
        ```bash
        # First re-create the rule so rm has something to destroy (since we deleted it manually in step 9)
        km event add 'every monday at 1pm' doctor --name uat-nightly-doctor --target-km 'doctor --all-regions' --dry-run=false 2>&1 | head -5
        # Idempotency check — should print existing ARN
        km event add 'every monday at 1pm' doctor --name uat-nightly-doctor --target-km 'doctor --all-regions' --dry-run=false 2>&1 | grep "already registered"
        # Actual remove
        km event rm uat-nightly-doctor --dry-run=false
        km event list
        ```
        EXPECT: First "already registered" print confirms idempotency. rm prints "Event 'uat-nightly-doctor' destroyed". list shows no rows. km-config.yaml events: list is empty or absent.

    11. **Regression — km at hard cut**:
        ```bash
        km at --cron 'cron(0 9 * * ? *)' destroy fake-sb-id 2>&1 | grep "unknown flag"
        km at 'every monday at 1pm' destroy fake-sb-id 2>&1 | grep -i "recurring expressions not supported"
        km at '5pm tomorrow' kill fake-sb-id 2>&1 | head -5    # this MAY error on the sandbox-id resolution but should NOT fail at the parse step
        ```
        EXPECT: First two commands return non-zero with the expected error strings. Third command parses successfully (any failure is about the fake sandbox-id, not the time expression).

    12. **Final check — full doctor**:
        ```bash
        km doctor 2>&1 | tail -30
        ```
        EXPECT: No new ERROR-level checks introduced by Phase 83. operator_event_rules_healthy: OK (rules empty after cleanup). No regressions in other checks.

    Record outcomes in `.planning/phases/83-km-event-operator-controlled-eventbridge/83-UAT.md` per step (PASS/FAIL/notes).
  </how-to-verify>
  <resume-signal>
    Reply with one of:
    - "UAT approved" — all 12 steps PASS; proceed to closeout
    - "UAT issue: <description>" — bug needs fixing; planner may need to file a gap-closure plan via `/gsd:plan-phase 83 --gaps`
  </resume-signal>
</task>

<task type="auto">
  <name>Task 4: Closeout — STATE.md + ROADMAP.md + final commit</name>
  <files>.planning/STATE.md, .planning/ROADMAP.md</files>
  <behavior>
    - STATE.md updates:
      - Update `current_focus` line to: `Current focus: Phase 83 — km event operator-controlled EventBridge COMPLETE`
      - Update `progress` block: increment completed_plans by 7 (one per Phase 83 plan)
      - Append lessons learned to the "Lessons" / "Hooks" section at the bottom (existing pattern: `- [Phase 83]: <one-liner key insight>`). Examples:
        - "[Phase 83]: EventBridge Rules do not support at() — pkg/events.AtExprToCronForRules converts to pinned cron at km event add time"
        - "[Phase 83]: km-operator-policy EventBridgePutEvents was additively widened to include the custom bus — no moved {} block needed since we changed a Resource list, not a resource address"
        - "[Phase 83]: km-runner Lambda bakes the km binary directly into the zip (no S3 toolchain download like create-handler) — simpler cold start"
        - "[Phase 83]: --cron flag stripped from km at; recurring NL inputs now error with pointer at km event — no deprecation period"
        - "[Phase 83-XX]: Any specific gotchas from each plan's SUMMARY"
      - Append performance metrics row: `Phase 83-km-event-operator-controlled-eventbridge: 7 plans, <total-duration>` (operator can fill in duration)
    - ROADMAP.md updates:
      - Find Phase 83 entry. Update Plans count: `**Plans:** 7 plans`
      - Replace `- [ ] TBD (run /gsd:plan-phase 83 to break down)` placeholder with the seven checkbox lines:
        ```
        - [x] 83-01-wave0-test-scaffolds-PLAN.md — Wave 0 test infrastructure for Nyquist sampling (4 test files + mockEventRunner + fixtures)
        - [x] 83-02-foundations-pkg-events-and-config-PLAN.md — pkg/events package + EventConfig in config.go + km-operator-policy widened to custom bus
        - [x] 83-03-terraform-modules-bus-and-rule-PLAN.md — Two new Terraform modules: operator-event-bus (singleton bus + archive + km-runner Lambda) and operator-event-rule (per-rule resources)
        - [x] 83-04-km-runner-lambda-binary-and-makefile-PLAN.md — cmd/km-runner Lambda binary + Makefile build-km-runner target
        - [x] 83-05-km-event-cli-PLAN.md — internal/app/cmd/event.go CLI tree (add/list/rm/apply) modeled on Phase 80 km cluster add
        - [x] 83-06-cleanup-km-init-doctor-PLAN.md — km at --cron strip + recurring NL reject + operator-event-bus in km init regionalModules + km doctor operator_event_rules_healthy check
        - [x] 83-07-docs-and-uat-PLAN.md — docs/operator-events.md + CLAUDE.md Phase 83 section + end-to-end operator UAT closeout
        ```
      - Check the Phase 83 box if there's a top-level "completed phases" indicator
    - Commit all Phase 83 artifacts with a single closeout commit (separate from per-plan commits already shipped).

    AVOID:
    - Updating STATE.md current_plan beyond what already shipped (per-plan commits handle their own counter)
    - Adding unverified lessons — only document what the SUMMARY files document
    - Renaming any plan file — use the exact filenames committed in earlier waves

    REFERENCES:
    - .planning/STATE.md — Phase 80 / Phase 79 closeout entries for format reference
    - .planning/ROADMAP.md — Phase 80 entry shows the closeout format
  </behavior>
  <action>
    1. Read all 7 Phase 83 SUMMARY files to pull canonical lessons.
    2. Update STATE.md: change current_focus, increment completed_plans, append the lessons.
    3. Update ROADMAP.md: replace Phase 83's placeholder line with the 7 checkbox list (all checked).
    4. Run `gsd-tools commit`:
       ```bash
       node $HOME/.claude/get-shit-done/bin/gsd-tools.cjs commit "docs(83): close out Phase 83 — km event command shipped" --files .planning/STATE.md .planning/ROADMAP.md
       ```
    5. Verify: `git log -1 --oneline` shows the closeout commit.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02 && grep -c "Phase 83 — km event" .planning/STATE.md && grep -c "83-01-wave0-test-scaffolds-PLAN.md" .planning/ROADMAP.md && git log -1 --oneline 2>&1 | grep -i "phase 82\|82:"</automated>
  </verify>
  <done>
    STATE.md current_focus updated to Phase 83 complete. ROADMAP.md Phase 83 entry has 7 checked plan boxes. A closeout git commit exists.
  </done>
</task>

</tasks>

<verification>
- docs/operator-events.md exists, >100 lines, references km event ≥10 times
- CLAUDE.md has new "## Operator-controlled EventBridge (Phase 83)" section
- Operator UAT all 12 steps PASS (manual sign-off)
- STATE.md current_focus = "Phase 83 ... COMPLETE"
- ROADMAP.md Phase 83 has 7 checked plan boxes
- Final closeout commit exists in git log
</verification>

<success_criteria>
- Documentation is operator-ready (CLAUDE.md quick reference + docs/operator-events.md full guide)
- End-to-end UAT proves the full lifecycle works on real AWS
- STATE.md + ROADMAP.md reflect Phase 83 closeout with all lessons captured
- Phase 83 ships
</success_criteria>

<output>
After completion, create `.planning/phases/83-km-event-operator-controlled-eventbridge/83-07-SUMMARY.md` documenting:
- UAT outcomes per step (PASS/FAIL/notes) — pulled from 83-UAT.md
- Any deviations from the original CONTEXT.md scope discovered during UAT (e.g., a flag wording change, an unexpected edge case)
- Final cost estimate for the operator-event-bus archive (real AWS line-item, e.g., "0.0001 USD for the first 7 days with 0 events flowing through")
- Performance metrics: total Phase 83 duration (hours from 83-01 start to 83-07 close), total plan count (7), total test count (≥20 unit + 1 e2e + 12 UAT steps)
</output>
</content>
