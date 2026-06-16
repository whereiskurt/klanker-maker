---
phase: 115-generic-github-webhook-event-prompt-router
plan: "06"
subsystem: github-bridge
tags: [github-webhooks, event-router, poller, userdata, docs, uat]
dependency_graph:
  requires:
    - "115-03 (handleEventRoute + KM_GITHUB_EVENTS Lambda wiring)"
    - "115-04 (init.go KM_GITHUB_EVENTS export + config surface)"
    - "115-05 (manifest event union + km doctor GH-EVENT-DOCTOR)"
  provides:
    - "pkg/compiler/userdata.go: KIND-aware poller preamble + Number==0 tolerance"
    - "docs/github-bridge.md: Phase 115 operator guide section"
    - ".planning/phases/115-generic-github-webhook-event-prompt-router/115-UAT.md: live E2E runbook"
  affects:
    - "GH-EVENT-E2E (pending human-verify checkpoint, live UAT required)"
tech_stack:
  added: []
  patterns:
    - "KIND-branched preamble in bash heredoc: issue_comment || empty KIND -> PR-context; else -> [GitHub Event Trigger]"
    - "NUMBER==0 guard on DDB session lookup and writeback (event-rule envelopes have no PR number)"
    - "Golden file manual update: targeted edit of GitHub poller lines without disturbing settings.json blob"
key_files:
  created:
    - path: ".planning/phases/115-generic-github-webhook-event-prompt-router/115-UAT.md"
      purpose: "7-step live E2E operator runbook: deploy, positive repo, negative exclude, dedup, evidence capture"
  modified:
    - path: "pkg/compiler/userdata.go"
      purpose: "km-github-inbound-poller: parse KIND/ACTION, relax NUMBER guard, gate session lookup/writeback on NUMBER!=0, branch preamble on KIND"
    - path: "pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh"
      purpose: "Updated golden in-place to mirror Phase 115 poller changes (learn.v2.yaml has GitHub inbound enabled; settings.json blob + SubagentStop semantic check unchanged)"
    - path: "docs/github-bridge.md"
      purpose: "Added Phase 115 section: config shape, gating model, cooldown, dormant-by-default, deploy surface, known limitation, doctor check, UAT pointer"
key-decisions:
  - "Targeted golden update instead of CAPTURE_PRE92_BASELINE=1 regeneration: the capture tool writes the CURRENT output (with SubagentStop in settings.json) but the Phase92 semantic check asserts SubagentStop is present in GENERATED and then deletes it before comparing to the GOLDEN (which must not have it). Regenerating would break that invariant. Manual targeted edit of the GitHub poller lines is the correct approach."
  - "KIND-defaulting to issue_comment branch: empty KIND (pre-Phase-115 envelope from a bridge that has not yet deployed Plans 03-05) falls into the issue_comment preamble path, preserving backward compatibility for mixed-version fleets."
metrics:
  duration: "~14min"
  completed: "2026-06-16"
  tasks_completed: 2
  tasks_pending: 1
  files_modified: 3
  files_created: 1
requirements-completed: [GH-EVENT-POLLER, GH-EVENT-DOCS]
requirements-pending: [GH-EVENT-E2E]
---

# Phase 115 Plan 06: Poller Surgery + Docs + Live E2E Checkpoint Summary

**Poller tolerance for event envelopes (KIND-branched preamble, Number==0 guards) + Phase 115 operator docs + UAT runbook; live E2E checkpoint handed off to orchestrator**

## Performance

- **Duration:** ~14 min
- **Started:** 2026-06-16T00:20:42Z
- **Completed:** 2026-06-16T00:34:57Z (Tasks 1-2; Task 3 is pending human-verify)
- **Tasks completed:** 2/3 (Task 3 is the live E2E checkpoint)
- **Files modified:** 3
- **Files created:** 1

## Accomplishments

### Task 1: Poller tolerance for event envelopes (userdata.go)

Extended `km-github-inbound-poller` in `pkg/compiler/userdata.go` to handle
event-rule envelopes (`Number=0`, `Kind != issue_comment`):

- **KIND/ACTION parse:** Added `KIND=$(echo "$BODY" | jq -r '.kind // empty')` and
  `ACTION=$(echo "$BODY" | jq -r '.action // empty')` after the existing envelope field
  parses.

- **Relaxed validation guard:** Changed `[ -z "$REPO" ] || [ -z "$NUMBER" ]` to
  `[ -z "$REPO" ]` only. `NUMBER=0` is valid for event-rule envelopes; bash `"0"` is
  already non-empty so the old guard was accidentally lenient, but the WARN message
  said "missing repo/number" which was misleading and the PR-context preamble with
  `#$NUMBER` and `pull/0/head` was wrong.

- **Session-continuity lookup gated on NUMBER != 0:** Each event-rule dispatch is a
  fresh session. Keying on `(repo, 0)` in `km-github-threads` would incorrectly merge
  sessions across different events on the same repo.

- **KIND-branched preamble:**
  - `[ "$KIND" = "issue_comment" ] || [ -z "$KIND" ]` → existing `[GitHub Comment Trigger]`
    preamble (with worktree isolation + `git fetch origin pull/${NUMBER}/head`). Empty KIND
    falls into this branch for backward compatibility with pre-Phase-115 bridge deployments.
  - All other Kinds → new `[GitHub Event Trigger]` preamble (repo + event/action + sender +
    URL + expanded prompt). No `pull/0/head` fetch.

- **Session writeback gated on NUMBER != 0:** The DDB `update-item` writing
  `agent_session_id`/`agent_type` and the Phase 106 resume-hint comment are also skipped
  for event-rule envelopes (no PR thread to persist; no originating comment to reply to).

**Golden update:** The Phase92/KmPrefix byte-identity tests compare against
`testdata/userdata_learn_v2_pre92_baseline.golden.sh`. Since `profiles/learn.v2.yaml`
has `notification.github.inbound.enabled: true`, the golden captures the GitHub poller
bash. My Phase 115 changes added new lines, breaking the byte-identity assertion. The
golden was updated with targeted edits to the GitHub poller sections only; the
settings.json blob and SubagentStop semantic equivalence check remain unchanged. Both
tests pass GREEN after the update.

**Verification:** `go build ./pkg/compiler/...` clean; `go test ./pkg/compiler/... -timeout 600s -count=1` GREEN (all 5.4s, including TestUserdataLearnV2Phase92ByteIdentity, TestUserdataKmPrefixByteIdentity, TestUserdataH1ByteIdentity, and all GitHub compiler tests).

### Task 2: docs/github-bridge.md Phase 115 section + 115-UAT.md

Added `§ Phase 115 — Generic event→prompt router` to `docs/github-bridge.md` and
created the live E2E runbook at `115-UAT.md`.

The doc section covers:
- What the router does and its design constraints (no actor allowlist, first-match, etc.)
- Full `github.events:` config YAML block with all field descriptions and a table
- Six template vars (`{{repo}}`, `{{event}}`, `{{action}}`, `{{sender}}`,
  `{{default_branch}}`, `{{html_url}}`) with descriptions
- Gating model: on/actions/match/exclude, no-match → 200 drop
- Cooldown: opt-in per-(event,repo,action), `gh-event-cooldown:` nonce prefix
- Dormant-by-default invariant and Lambda log indicators
- Sandbox-side poller changes summary (GH-EVENT-POLLER)
- Known limitation: no cross-event session continuity
- km doctor check reference
- Deploy surface (verbatim): `make build-lambdas` + `km init --github` (NOT `--sidecars`);
  manifest regen + App re-install for new event subscriptions; cold-created sandboxes get
  the new poller free; long-lived alias sandboxes need `km destroy && km create`

The 115-UAT.md provides a 7-step live operator runbook:
1. Add github.events: rule to km-config.yaml
2. Deploy (make build-lambdas + km init --github)
3. Subscribe App to repository event (km github manifest + re-install)
4. Confirm env reached Lambda (CloudWatch log check)
5. POSITIVE: create throwaway repo → verify bridge logs, km list, on-box preamble, agent output
6. NEGATIVE: create excluded repo → verify no dispatch, no sandbox
7. DEDUP/COOLDOWN: redeliver webhook → verify no second sandbox
Plus a sign-off table and cleanup commands.

## Task Commits

1. **Task 1: Poller surgery** - `5df586e4` (feat)
2. **Task 2: Docs + UAT runbook** - `76a873ae` (feat)

## Checkpoint: Task 3 Pending (GH-EVENT-E2E)

Task 3 is a `checkpoint:human-verify` gate. The autonomous tasks (Tasks 1-2) are
committed. The live E2E requires:

- A real GitHub org with the km App installed
- A configured `github.events:` rule pointing to a real profile
- `make build-lambdas` + `km init --github` (or full apply) deployed

**What to verify:**
1. Deploy Plans 02-05 + Plan 06 Tasks 1-2 (all merged as of this SUMMARY)
2. Run 115-UAT.md Steps 1-8
3. Confirm positive repo cold-creates with `[GitHub Event Trigger]` preamble
4. Confirm excluded repo produces no dispatch
5. Fill in 115-UAT.md sign-off table and set `status: verified`

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Phase92 golden broken by my GitHub poller changes**
- **Found during:** Task 1 verification
- **Issue:** `TestUserdataLearnV2Phase92ByteIdentity` and `TestUserdataKmPrefixByteIdentity`
  both compare against `testdata/userdata_learn_v2_pre92_baseline.golden.sh`. Since
  `learn.v2.yaml` has GitHub inbound enabled, the golden contains the GitHub poller bash.
  My Phase 115 changes added new lines (KIND/ACTION parse + if/else/fi preamble branch),
  breaking the byte-identity assertion.
- **Fix:** Targeted manual edits to the golden file, mirroring the exact changes made in
  `userdata.go`. The settings.json blob and SubagentStop semantic check were NOT touched.
  Full `CAPTURE_PRE92_BASELINE=1` regeneration was NOT used (it would capture the
  CURRENT settings.json with SubagentStop, breaking the assertion that detects SubagentStop
  was added post-baseline).
- **Files modified:** `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh`
- **Commit:** `5df586e4`

## Self-Check: PASSED

All key files found; all commits present:
- `userdata.go` - FOUND
- `docs/github-bridge.md` - FOUND
- `115-UAT.md` - FOUND
- `115-06-SUMMARY.md` - FOUND
- Commit `5df586e4` (Task 1: poller surgery) - FOUND
- Commit `76a873ae` (Task 2: docs + UAT runbook) - FOUND

`go build ./pkg/compiler/...` clean; `go test ./pkg/compiler/... -timeout 600s -count=1` GREEN.
