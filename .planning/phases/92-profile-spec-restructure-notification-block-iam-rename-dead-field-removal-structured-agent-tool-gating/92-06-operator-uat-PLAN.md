---
phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating
plan: 6
type: execute
wave: 6
depends_on: [1, 2, 3, 4, 5]
files_modified:
  - .planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-06-UAT.md
autonomous: false
requirements: []
verifies: [VC-2, VC-8, VC-9, VC-10, VC-11]

must_haves:
  truths:
    - "Operator confirms Scenario 0 PASSES first: `scripts/validate-all-profiles.sh` exits 0 against all 20 profiles. This is the phase's hard exit gate."
    - "Operator confirms `km validate` rejects a fixture with stale `identity:` / `agent.maxConcurrentTasks:` / `cli.notifySlackEnabled:` keys (Scenario 2)."
    - "`make build && km init --sidecars` succeeds (Scenario 3) — VC-9."
    - "`km create profiles/learn.v2.yaml --no-bedrock` provisions; SSM into sandbox; `cat /home/sandbox/.claude/settings.json` matches synthesizer output; `cat /home/sandbox/.codex/config.toml` validates (Scenario 4) — VC-10."
    - "Claude Code denies a tool listed in `agent.claude.tools.deny: [WebFetch]` (Scenario 5) — VC-10."
    - "Slack notify-hook fires for idle in a `notification.slack.perSandbox: true` profile (Scenario 6) — VC-10."
    - "Slack inbound `mentionOnly: true` works in shared channel; `false` accepts every message in per-sandbox (Scenario 7) — VC-10."
    - "Per-message `codex:` prefix in `agent.default: claude` profile triggers Codex (Scenario 8, Phase 70 routing intact) — VC-10."
    - "`km doctor` passes (Scenario 9) — VC-8."
    - "Any failure rolls back to a planning loop — orchestrator runs `/gsd:plan-phase --gaps` to generate gap closure plans."
  artifacts:
    - path: ".planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-06-UAT.md"
      provides: "Operator UAT runbook + scenario log + pass/fail recording per scenario."
      min_lines: 80
  key_links:
    - from: "Wave 6 UAT"
      to: "Phase 92 close"
      via: "All 10 scenarios PASS → mark milestone complete; any FAIL → gap closure plan"
      pattern: "PASS|FAIL"
---

<objective>
Final operator-driven verification that Phase 92 lands correctly end-to-end. NOT autonomous — this wave PAUSES for operator (the user) to run live commands against real AWS infrastructure and Slack workspace, and to record PASS/FAIL for each of 10 scenarios.

The UAT is the phase's hard exit gate. Scenario 0 (full-inventory validation) is the FIRST gate — if any of the 20 profiles fail `km validate`, the UAT stops immediately and Wave 5 reopens for gap closure.

Wave dependencies: requires Waves 1-5 all merged and `go test ./...` GREEN. Orchestrator must confirm `go test ./...` PASS before releasing this wave to the operator.

After all 10 scenarios PASS, the operator marks Phase 92 complete; STATE.md updates; the milestone retrospective input is augmented.
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
</execution_context>

<context>
@.planning/STATE.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-CONTEXT.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-RESEARCH.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-VALIDATION.md
@docs/agent-tool-gating.md
@docs/slack-notifications.md
</context>

<tasks>

<task type="auto">
  <name>Task 1: Pre-UAT — run automated gates, build km binary, deploy sidecars</name>
  <files>
    .planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-06-UAT.md
  </files>
  <action>
Before handing control to the operator, run the full automated test suite and deploy the sidecars so subsequent operator scenarios can exercise real infrastructure.

Step 1 — Confirm clean build + tests:

  make build && go test ./... -count=1 && bash scripts/validate-all-profiles.sh

All three must succeed before continuing.

Step 2 — Deploy sidecars (this is the official post-merge runbook step per CONTEXT.md §Post-merge runbook):

  km init --sidecars --dry-run=false

Why `--sidecars`: per RESEARCH.md §4b, `--sidecars` rebuilds binaries and forces a Lambda cold-start, which is sufficient for schema-change propagation. Full `km init` is NOT needed (no Lambda env var changes in this phase — env var names preserved).

Step 3 — Initialize the UAT log file `.planning/phases/92-.../92-06-UAT.md` with the empty scenario table (Task 2 walks operator through filling it in). Use the template below — touch the file so Task 2 has a starting point.

Both gates must PASS before the operator runs Scenarios 4-9 (the live-AWS scenarios).
  </action>
  <verify>
    <automated>make build &amp;&amp; go test ./... -count=1 &amp;&amp; bash scripts/validate-all-profiles.sh &amp;&amp; km init --sidecars --dry-run=false</automated>
    Expected: all four commands succeed. VC-9 (Scenario 3), VC-11 (Scenario 0).
  </verify>
  <done>
    Build + tests + validation script + sidecar deploy all GREEN; 92-06-UAT.md initialized; operator can begin live scenarios.
  </done>
</task>

<task type="checkpoint:human-verify" gate="blocking">
  <name>Task 2: Operator UAT — record results in 92-06-UAT.md (10 scenarios)</name>
  <files>
    .planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-06-UAT.md
  </files>
  <action>
    See <how-to-verify> below — operator walks 10 scenarios in order, recording PASS/FAIL into 92-06-UAT.md.
  </action>
  <verify>
    Operator marks each scenario PASS (manual) and commits 92-06-UAT.md when all 10 are GREEN. VC-2, VC-8, VC-9, VC-10, VC-11.
  </verify>
  <done>
    All 10 scenarios PASS; 92-06-UAT.md committed; orchestrator received resume signal phase-92-uat-passed.
  </done>
  <what-built>
    Waves 1-5 + Task 1 of this plan have produced:
    - New spec.iam: block (Wave 1), new spec.notification: block (Waves 2-3), new spec.runtime.vscode.enabled (Waves 2-3), new spec.agent: block with Claude + Codex sub-blocks (Wave 4), synthesizers (Wave 5).
    - 20 profile YAMLs migrated; scripts/validate-all-profiles.sh exits 0.
    - km binary built locally; sidecars deployed via km init --sidecars.
  </what-built>
  <how-to-verify>
The operator walks through 10 scenarios in order, recording PASS/FAIL into `.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-06-UAT.md`.

UAT LOG TEMPLATE (operator fills the table + per-scenario sub-sections):

```
# Phase 92 — Operator UAT Log

**Started:** YYYY-MM-DD
**Operator:** {operator name}
**km version:** {output of `km --version`}

## Scenario Pass/Fail

| # | Scenario | Status | Notes |
|---|---|---|---|
| 0 | Full-inventory validation: scripts/validate-all-profiles.sh exits 0 against 20 profiles. | ⬜ pending | — |
| 1 | km validate profiles/learn.v2.yaml and all other inventory files — pass (covered by 0). | ⬜ pending | — |
| 2 | km validate rejects a stale-key fixture (identity:, agent.maxConcurrentTasks, cli.notifySlackEnabled). | ⬜ pending | — |
| 3 | make build && km init --sidecars succeeds. | ⬜ pending | — |
| 4 | km create profiles/learn.v2.yaml --no-bedrock provisions; SSM-shell in; cat settings.json + config.toml. | ⬜ pending | — |
| 5 | Claude Code refuses a denied tool (agent.claude.tools.deny: [WebFetch]). | ⬜ pending | — |
| 6 | Slack notify-hook fires for an idle event in notification.slack.perSandbox: true profile. | ⬜ pending | — |
| 7 | Slack inbound mentionOnly: true / false works per channel mode. | ⬜ pending | — |
| 8 | Per-message codex: prefix in agent.default: claude profile triggers Codex (Phase 70 routing). | ⬜ pending | — |
| 9 | km doctor passes. | ⬜ pending | — |
```

PER-SCENARIO INSTRUCTIONS:

**Scenario 0 — Full-inventory validation (HARD EXIT GATE)**

  bash scripts/validate-all-profiles.sh

Expected: exits 0; prints "validate-all-profiles: all 20 profiles valid". If any profile fails: STOP UAT. Open gap closure plan via `/gsd:plan-phase 92 --gaps`. Operator records exit code + profile failures (if any).

**Scenario 1 — Spot-check individual profiles**

Already covered by Scenario 0; spot-check by running:

  ./km validate profiles/learn.v2.yaml
  ./km validate pkg/profile/builtins/sealed.yaml
  ./km validate pkg/profile/builtins/hardened.yaml
  ./km validate pkg/profile/builtins/open-dev.yaml
  ./km validate pkg/profile/builtins/restricted-dev.yaml

Expected: all "valid" with no warnings.

**Scenario 2 — Rejection of stale-key fixtures**

Create a throwaway fixture and run km validate against each — each should FAIL with a clear path-named error.

Stale 1 (legacy identity:):

  cat > /tmp/stale-identity.yaml << 'EOF'
  apiVersion: klankermaker.example.com/v1
  kind: SandboxProfile
  metadata: {name: stale-identity}
  spec:
    identity: {roleSessionDuration: "1h", allowedRegions: [us-east-1]}
    runtime: {substrate: ec2, ami: amazon-linux-2023, instanceType: t3.small}
  EOF
  ./km validate /tmp/stale-identity.yaml

Expected: error referencing spec.identity or additionalProperties rejection.

Stale 2 (dead agent block): add `  agent: {maxConcurrentTasks: 1, taskTimeout: "30m"}` to a copy with iam: fixed. Expected: error referencing spec.agent.maxConcurrentTasks (Wave 4's agent block doesn't have that field) OR additionalProperties rejection on the spec.agent object.

Stale 3 (legacy notify field): add `  cli: {notifySlackEnabled: true}` to a copy. Expected: error referencing spec.cli.notifySlackEnabled (property no longer exists in CLISpec schema).

Operator records: each rejection's error text.

**Scenario 3 — Sidecar deploy**

Already run in Task 1 (pre-UAT). Confirm:

  km doctor 2>&1 | grep -i sidecar

Operator records: doctor output for sidecar/Lambda state.

**Scenario 4 — Real create + SSM inspection** (live AWS — operator must have AWS credentials)

  ./km create profiles/learn.v2.yaml --no-bedrock
  # Wait for green status
  ./km list
  SANDBOX_ID={the sandbox-id from list}
  ./km shell $SANDBOX_ID
  # Inside SSM shell:
  cat /home/sandbox/.claude/settings.json
  cat /home/sandbox/.codex/config.toml
  exit
  ./km destroy $SANDBOX_ID --remote --yes

Expected:
- settings.json contains permissions.allow (canonical) NOT legacy autoApprove.
- settings.json contains trustedDirectories.
- settings.json contains a hooks block from mergeNotifyHookIntoSettings.
- config.toml contains [features] hooks = true, [[hooks.PermissionRequest]], [[hooks.Stop]], and the asymmetry NOTE comment.

Operator records: PASS/FAIL + paste of the two cat outputs.

**Scenario 5 — Denied tool refusal** (live AWS + Claude Code access)

Create a profile copy with deny populated:

  cp profiles/learn.v2.yaml /tmp/learn.v2.webfetch-deny.yaml
  # Edit /tmp/learn.v2.webfetch-deny.yaml: set spec.agent.claude.tools.deny: [WebFetch]
  ./km create /tmp/learn.v2.webfetch-deny.yaml --no-bedrock
  SANDBOX_ID={the new sandbox-id}
  ./km agent run $SANDBOX_ID --prompt "Use the WebFetch tool to fetch https://example.com. Tell me what you got." --wait
  ./km destroy $SANDBOX_ID --remote --yes

Expected: Claude refuses to use WebFetch ("I can't use WebFetch" or equivalent). The agent's output.json shows no successful WebFetch invocation.

Operator records: PASS/FAIL + paste of agent response.

**Scenario 6 — Slack notify-hook on idle** (live AWS + live Slack workspace + Phase 91 bridge deployed)

  ./km create profiles/learn.v2.yaml --no-bedrock
  SANDBOX_ID={the new sandbox-id}
  ./km agent run $SANDBOX_ID --prompt "Sleep for 6 minutes (write a python loop with time.sleep) and then exit." --wait=false
  # Wait ~6 minutes for idle event
  # Check Slack channel #sb-{sandbox-id-suffix} for a posted "idle" message
  ./km destroy $SANDBOX_ID --remote --yes

Expected: Slack post appears in the per-sandbox channel within ~6 minutes; subject "[$SANDBOX_ID] idle"; body from the Phase 62 hook.

Operator records: PASS/FAIL + Slack message screenshot or link.

**Scenario 7 — Slack inbound mentionOnly per channel** (live AWS + live Slack workspace)

Case A — shared channel, mentionOnly: true (default for shared mode per Phase 91):

  ./km create profiles/learn.v2.polite.yaml --no-bedrock
  # In Slack #km-notifications:
  #   Post "hello sandbox" (NO @-mention) → expect NO dispatch
  #   Post "@klanker hello sandbox" (with @-mention) → expect dispatch
  ./km agent list $SANDBOX_ID
  ./km destroy $SANDBOX_ID --remote --yes

Case B — per-sandbox channel, mentionOnly: false (chatty mode):

  ./km create profiles/learn.v2.chatty.yaml --no-bedrock
  # In Slack #sb-{id}: post "hi" (no mention) → expect dispatch
  ./km agent list $SANDBOX_ID
  ./km destroy $SANDBOX_ID --remote --yes

Expected: A blocks bare post + accepts mention; B accepts bare post.

Operator records: PASS/FAIL for each sub-case.

**Scenario 8 — Per-message codex: prefix routing** (live AWS + live Slack workspace + Claude-default profile)

  ./km create profiles/learn.v2.yaml --no-bedrock  # agent.default: claude
  SANDBOX_ID={new id}
  # In Slack #sb-{id}: post "codex: list current directory"
  # Expect: 8-step clean-handoff per Phase 70 (new top-level message, Codex agent answers in new thread)
  ./km agent list $SANDBOX_ID --queue   # check the dispatch routed to Codex
  ./km destroy $SANDBOX_ID --remote --yes

Expected: Codex agent (not Claude) answers; thread is a fresh top-level Slack message per Phase 70's clean-handoff sequence.

Operator records: PASS/FAIL + Slack screenshot.

**Scenario 9 — km doctor**

  ./km doctor --all-regions

Expected: every check shows ✓ or ⚠ (warnings acceptable for ORPHAN-class issues unrelated to Phase 92). No ✗ errors.

Operator records: full doctor output.

SIGN-OFF section — operator marks Phase 92 complete when all 10 scenarios PASS:

  - [ ] Scenario 0  — Full-inventory validation
  - [ ] Scenario 1  — Spot-check individual profiles
  - [ ] Scenario 2  — Rejection of stale-key fixtures
  - [ ] Scenario 3  — Sidecar deploy
  - [ ] Scenario 4  — Real create + SSM inspection
  - [ ] Scenario 5  — Denied tool refusal
  - [ ] Scenario 6  — Slack notify-hook on idle
  - [ ] Scenario 7  — Slack inbound mentionOnly
  - [ ] Scenario 8  — Per-message codex prefix routing
  - [ ] Scenario 9  — km doctor passes

  **Closed:** YYYY-MM-DD
  **Final state:** PASS / FAIL
  </how-to-verify>
  <resume-signal>Type `phase-92-uat-passed` with paste of the closing summary, OR describe what failed and which scenario(s).</resume-signal>
</task>

</tasks>

<verification>
- Scenario 0 PASS — `scripts/validate-all-profiles.sh` exits 0 (VC-11).
- Scenarios 1-9 all PASS as recorded in 92-06-UAT.md (VC-2, VC-8, VC-9, VC-10).
- 92-06-UAT.md committed to git.
- STATE.md updated to reflect Phase 92 complete after UAT close.
</verification>

<success_criteria>
- Operator confirms all 10 UAT scenarios PASS.
- 92-06-UAT.md is committed with date + operator name + km version + per-scenario outputs.
- Any FAIL triggers `/gsd:plan-phase 92 --gaps` flow.
</success_criteria>

<output>
After UAT close, create `.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-06-SUMMARY.md` capturing:
- Total UAT duration.
- Sandbox lifecycle counts (create/destroy).
- Slack scenario notes (any flakiness — Phase 91 / Phase 67 live infra behavior).
- Codex Scenario 8 outcome — Phase 70 routing preserved.
- Final recommendation: mark Phase 92 milestone status complete in ROADMAP.md.
- Cost recap (compute spend during UAT).
</output>
