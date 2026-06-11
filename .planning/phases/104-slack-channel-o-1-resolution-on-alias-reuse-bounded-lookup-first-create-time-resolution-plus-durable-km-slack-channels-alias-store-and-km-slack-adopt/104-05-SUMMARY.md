---
phase: 104-slack-channel-o-1-resolution-on-alias-reuse
plan: 05
subsystem: infra
tags: [slack, dynamodb, km-slack-channels, bounded-resolver, uat, docs]

# Dependency graph
requires:
  - phase: 104-01
    provides: bounded resolver + KM_SLACK_RESOLVE_BUDGET / KM_SLACK_MAX_SCAN_PAGES knobs
  - phase: 104-02
    provides: km-slack-channels DDB table (TF module + live unit + init.go entry)
  - phase: 104-03
    provides: SlackChannelStore helper + create-handler IAM + config getter
  - phase: 104-04
    provides: km slack adopt + km doctor table-existence check
provides:
  - "Operator-facing docs: docs/slack-notifications.md Phase 104 section covering incident root cause, bounded resolver, env knobs, DDB table, km slack adopt, observability, deploy sequence"
  - "CLAUDE.md Phase 104 note with all 3 fix layers, deploy sequence, and where-to-look row"
  - "Live large-workspace UAT: incident scenario (reused alias, real corporate Slack) confirmed bounded (~2m27s) with O(1) DDB hit; no 900s wedge"
  - "Provider-lock-drift remediation documented (stray infra/modules/** .terraform.lock.hcl files at mismatched provider versions; remediation recorded as project memory)"
affects: [105, operator-guide, release-notes]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Deploy sequence for DDB+IAM changes: make build (binary) BEFORE km init; full km init --dry-run=false, NOT --sidecars (env-block change)"
    - "Lock-drift remediation: remove stray infra/modules/**/.terraform.lock.hcl + .terraform/ dirs + terragrunt caches before apply when provider versions diverge"

key-files:
  created: []
  modified:
    - docs/slack-notifications.md
    - CLAUDE.md

key-decisions:
  - "Deploy sequence documented as make build BEFORE km init (not make build-lambdas alone); a stale binary silently skips the new module (existing memory project_make_build_precedes_km_init)"
  - "Provider-lock-drift root cause: stray gitignored .terraform.lock.hcl files in infra/modules/** left by past bare terraform validate runs; removed before apply (new project memory candidate)"
  - "Archived-channel fast-fail (cache_hit path, conversations.info classifies as archived) is the correct bounded behavior for reuse-after-destroy with archiveOnDestroy profile; a live-channel cache_hit path was not separately isolated but is implicitly proven by the store write-through in Create #1"
  - "km slack adopt negative cases confirmed fail-closed: format guard (^C[A-Z0-9]+$) fires before any DDB write; bot-not-member check fires before any DDB write"

patterns-established:
  - "Checkpoint UAT pattern: orchestrator runs live deploy + test sequence and reports observed slack_resolve path= values + wall-clock times; executor records results verbatim in SUMMARY"

requirements-completed: [SLACK-CHAN-E2E, SLACK-CHAN-DEPLOY]

# Metrics
duration: ~4h (including orchestrator UAT deploy + test cycle)
completed: 2026-06-10
---

# Phase 104 Plan 05: Docs + Deploy-Surface Audit + Live UAT Summary

**O(1) durable-store Slack channel resolution verified end-to-end on a real large-workspace: reused-alias create bounded at 2m27s (was 900s wedge), slack_resolve path=cache_hit after destroy-recreate, km slack adopt fail-closed on format and bot-membership preconditions**

## Performance

- **Duration:** ~4h (Tasks 1-2 autonomous; Task 3 orchestrator-run live UAT)
- **Started:** 2026-06-10T16:00:00Z (estimated)
- **Completed:** 2026-06-10T20:26:00Z
- **Tasks:** 3 of 3
- **Files modified:** 2 (docs/slack-notifications.md, CLAUDE.md)

## Accomplishments

- Wrote the Phase 104 operator-facing section in `docs/slack-notifications.md`: incident root cause (unbounded conversations.list scan wedging the 900s create-handler Lambda), bounded resolver mechanics, `KM_SLACK_RESOLVE_BUDGET` (default 45s) and `KM_SLACK_MAX_SCAN_PAGES` (default 0 = scan OFF / fail-fast), the km-slack-channels DDB table (PK alias, no TTL, read-first + write-through), `notification.slack.channelOverride` as the zero-lookup manual escape, `km slack adopt` runbook (format guard, bot-must-be-member precondition, how to find the Channel ID), `slack_resolve path=` observability line, and deploy sequence (`make build` BEFORE `km init`).
- Added CLAUDE.md Phase 104 note (all 3 fix layers, deploy sequence, no `--sidecars`, no SandboxProfile schema change) and a where-to-look row pointing at `docs/slack-notifications.md § Phase 104`.
- Executed the 12-point deploy-surface self-audit (Task 2): all items present and correctly wired — TF module, live unit, init.go entry, km-operator-policy IAM (count-gated), create-handler static-string plumbing (no dependency block, no env var), config getter + merge-list, SlackChannelStore (no TTL, no ConditionExpression), runtime derivation via cfg getter, km slack adopt registered, km doctor table-existence check present, slack-channels NOT in orphan scan, deploy sequence documented.
- Live large-workspace UAT (Task 3) PASSED on account 052251888500 (us-east-1): all checks confirmed by orchestrator.

## Task Commits

1. **Task 1: Docs — slack-notifications.md section + CLAUDE.md phase note** - `298c3f34` (docs)
2. **Task 2: Deploy-surface self-audit** - (audit-only; no code fix needed; findings recorded in SUMMARY)
3. **Task 3: Live large-workspace UAT** - (operator-run; no repo files modified; PASSED)

## Files Created/Modified

- `docs/slack-notifications.md` — Added 175-line Phase 104 section: incident background, bounded resolver, env knobs, DDB table, channelOverride escape, km slack adopt runbook, observability, deploy sequence, troubleshooting
- `CLAUDE.md` — Added Phase 104 note (13 lines) + where-to-look row

## Decisions Made

- Deploy sequence for this phase: `make build` (binary carries the new module registration) THEN `make build-lambdas` THEN `km init --dry-run=false`. NOT `--sidecars` — the km-slack-channels table + create-handler IAM require a full terragrunt apply. This is a reinforce of the existing `project_make_build_precedes_km_init` memory.
- Archived-channel fast-fail is the correct UAT outcome for the reuse-after-destroy scenario: the dc34 profile sets `archiveOnDestroy: true`, so Create #2 on the same alias correctly hit `path=cache_hit` (DDB row survived destroy), validated via `conversations.info` (classified: archived, NOT channel_not_found), and failed fast with actionable operator guidance. This proves the O(1) store read and the bounded classifier without triggering a workspace scan.
- Provider-lock-drift remediation required before apply: stray gitignored `.terraform.lock.hcl` files in 9 `infra/modules/**` dirs (5 at aws 6.49.0, 1 at 6.47.0) left by past bare `terraform validate` runs were copied into the terragrunt cache and conflicted with root.hcl's exact 6.46.0 pin. Fix: remove stray module-source locks + `.terraform/` dirs + terragrunt caches, then re-init. This is a new project-memory vector not covered by existing lock-drift notes (existing memories name operator Mac plugin_cache_dir; this names infra/modules/** as the source).

## Deviations from Plan

### Lock-drift remediation during UAT deploy

- **Found during:** Task 3 (live UAT deploy — `km init --dry-run=false`)
- **Issue:** Stray gitignored `.terraform.lock.hcl` files in 9 `infra/modules/**` directories (versions 6.49.0 / 6.47.0) were copied into the terragrunt cache and conflicted with root.hcl's pinned aws provider 6.46.0. This caused the apply to fail on provider-checksum validation.
- **Fix:** Orchestrator removed all stray module-source locks, `.terraform/` dirs, and terragrunt caches; re-ran init which applied cleanly.
- **Impact:** No code change; remediation is a one-time operator action. Documented in Decisions Made for future reference.
- **Rule:** Rule 3 (blocking issue during task execution) — handled automatically by orchestrator.

---

**Total deviations:** 1 (lock-drift, blocking — auto-remediated by orchestrator)
**Impact on plan:** No scope creep. Remediation was infrastructure hygiene, not a code fix.

## UAT Results (Task 3)

**Environment:** account 052251888500, us-east-1, AWS_PROFILE=klanker-application

### Deploy

- Provider-lock-drift remediation applied (see Deviations).
- All units applied: dynamodb-slack-channels, create-handler (with km-slack-channels IAM grant), h1/bridge units (previously wedged).
- Toolchain km binary (carrying bounded resolver) uploaded to S3 toolchain/km; create-handler Lambda cold-started.
- `km doctor`: "✓ Slack Channels Table (km-slack-channels) exists". DDB describe-table: ACTIVE, hash_key=alias, PAY_PER_REQUEST, SSE ENABLED, no TTL.

### Create #1 — fresh alias (uat-104, profiles/dc34.yaml)

- Completed in **2m27s** (not the 900s wedge).
- Slack channel `C0B9VLEJMEG` resolved + created + published to SSM.
- DDB write-through verified: row `{alias:uat-104, channel_id:C0B9VLEJMEG, updated_at}` present.

### Store persistence after destroy

- After `km destroy`, the DDB row survived (no TTL) — channel_id still `C0B9VLEJMEG`.

### Create #2 — reused alias (uat-104, incident scenario)

- Bounded lookup-first resolver read DDB store O(1) (no unbounded conversations.list scan).
- `conversations.info` classified the channel as **archived** (dc34 profile sets `archiveOnDestroy: true`).
- Correctly did NOT invalidate the mapping (archived ≠ channel_not_found).
- **Failed fast in seconds** with actionable guidance: "channel … is archived; pick a different --alias or unarchive it via conversations.unarchive".
- No wedge. `slack_resolve path=cache_hit` (O(1) DDB hit) → bounded archived-channel classifier.

### km slack adopt negatives (plan 104-04 coverage)

- `km slack adopt github-bot not-an-id` — rejected with `^C[A-Z0-9]+$` format error.
- Lowercase channel ID — rejected with same format guard.
- Well-formed ID for a channel where bot is not a member — rejected via `conversations.info` precondition check, zero DDB rows written. Fail-closed confirmed.

### Cleanup

- Both test sandboxes destroyed. uat-104 test store row deleted. No residue.

### Verdict

**PASSED.** The incident fix is verified end-to-end on the real workspace. O(1) durable-store resolution on alias reuse with no unbounded scan; bounded fast-fail with operator guidance instead of the 900s create-handler wedge.

## Issues Encountered

None beyond the lock-drift remediation documented in Deviations.

## User Setup Required

None — no external service configuration required. The km-slack-channels DDB table was provisioned via `km init --dry-run=false` during UAT.

## Next Phase Readiness

Phase 104 is complete. All 5 plans executed and verified:
- Plan 01: bounded resolver + env knobs
- Plan 02: km-slack-channels DDB table (TF module + live unit + init.go)
- Plan 03: SlackChannelStore helper + create-handler IAM + config getter
- Plan 04: km slack adopt + km doctor check
- Plan 05: docs + deploy-surface audit + live UAT (this plan)

Phase 103 (HackerOne bridge) continues from plan 103-02 onward.

---
*Phase: 104-slack-channel-o-1-resolution-on-alias-reuse*
*Completed: 2026-06-10*
