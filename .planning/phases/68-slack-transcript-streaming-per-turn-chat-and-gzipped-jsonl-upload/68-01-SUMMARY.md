---
phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
plan: 01
subsystem: profile-validation
tags: [slack, transcript, profile-schema, validation, json-schema, configui]

# Dependency graph
requires:
  - phase: 68-00
    provides: Wave-0 stub _test.go file (validate_slack_transcript_test.go) seeded with 5 t.Skip placeholders
  - phase: 67
    provides: notifySlackInboundEnabled validation rule precedent (mirrored here)
  - phase: 63
    provides: notifySlackEnabled / notifySlackPerSandbox / notifySlackChannelOverride profile fields
provides:
  - notifySlackTranscriptEnabled profile field on CLISpec (bool, default false)
  - JSON Schema entry under spec.cli with prerequisite documentation
  - Three hard validation rules (ST1/ST2/ST3) mirroring Phase 67 inbound semantics
  - 5 PASSing validation tests (promoted from Wave-0 stubs)
affects: [68-02, 68-03, 68-04, 68-05, 68-06, 68-07, 68-08, 68-09, 68-10, 68-11, 68-12]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Phase 67 mirror: same prerequisite chain (slack=true, perSandbox=true, no override) for any feature requiring audience-containment"
    - "Audience-containment rationale: per-sandbox channel guarantees a known operator-curated invitee list"

key-files:
  created:
    - .planning/phases/68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload/68-01-SUMMARY.md
  modified:
    - pkg/profile/types.go
    - pkg/profile/validate.go
    - pkg/profile/validate_slack_transcript_test.go
    - pkg/profile/schemas/sandbox_profile.schema.json

key-decisions:
  - "Field type bool (not *bool): default false is the no-opt-in semantic; validation rejects misconfiguration rather than treating unset as a third state — mirrors Phase 67 NotifySlackInboundEnabled"
  - "Schema location: pkg/profile/schemas/sandbox_profile.schema.json (not pkg/profile/profile.schema.json as plan stated) — actual codebase location used by ValidateSchema"
  - "Field positioned after NotifySlackInboundEnabled (logical sibling, both Phase 67/68 bidirectional Slack toggles) rather than strict alphabetical — matches the existing semantic-grouping convention in CLISpec"
  - "Three rules emit IsWarning: false (hard errors) — same as Phase 67 inbound rules, because misconfiguration would cause silent failure (transcripts going to unintended audience or no-op streaming)"

patterns-established:
  - "Phase-67-mirror pattern: any new bidirectional/operator-curated Slack feature gates on the same triple (notifySlackEnabled + notifySlackPerSandbox + !channelOverride)"
  - "Test fixture pattern: minimalXProfile + containsXError helpers per feature (avoids redeclaring boolPtr across sibling _test.go files)"

requirements-completed: []  # Plan 68-01 has empty requirements: [] in frontmatter (spec-driven phase)

# Metrics
duration: 4min
completed: 2026-05-03
---

# Phase 68 Plan 01: notifySlackTranscriptEnabled profile field + validation Summary

**Profile field NotifySlackTranscriptEnabled on CLISpec with three hard validation rules (slack-enabled, per-sandbox, no-channel-override) mirroring Phase 67 inbound semantics, JSON Schema entry for ConfigUI, and 5 stub tests promoted to PASS.**

## Performance

- **Duration:** ~4 min
- **Started:** 2026-05-03T19:56:02Z
- **Completed:** 2026-05-03T19:59:52Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments
- New `NotifySlackTranscriptEnabled bool` field added to `CLISpec` in `pkg/profile/types.go` with `yaml:"notifySlackTranscriptEnabled,omitempty" json:"notifySlackTranscriptEnabled,omitempty"` tags
- JSON Schema entry under `spec.cli.notifySlackTranscriptEnabled` (type=boolean, default=false) with description documenting prerequisites (`notifySlackEnabled` + `notifySlackPerSandbox` required, incompatible with `notifySlackChannelOverride`) — surfaces the field for ConfigUI
- Three hard validation rules added to `ValidateSemantic` in `pkg/profile/validate.go` (ST1: requires-slack-enabled, ST2: requires-per-sandbox, ST3: incompatible-with-channel-override), mirroring Phase 67 inbound rules SI1/SI2/SI3
- Wave-0 stubs in `pkg/profile/validate_slack_transcript_test.go` replaced with 5 real assertions — all PASS, no SKIPs
- Phase 63 + Phase 67 Slack validation tests still PASS (no regression)

## Task Commits

1. **Task 1: Add NotifySlackTranscriptEnabled field + JSON Schema entry** — `7496367` (feat)
2. **Task 2 (RED): Replace Wave-0 stubs with real assertions** — committed via parallel `78955b8` (Plan 68-02 swept the staged file; content unchanged from intended RED authored here — see Issues Encountered)
3. **Task 2 (GREEN): Add three notifySlackTranscriptEnabled validation rules** — `a58576e` (feat)

_TDD: RED authored locally, accidentally landed in 78955b8 via parallel agent's `git add` sweep, GREEN landed in a58576e._

## Files Created/Modified
- `pkg/profile/types.go` — added `NotifySlackTranscriptEnabled bool` field on `CLISpec` with documentation
- `pkg/profile/validate.go` — added three Phase 68 transcript validation rules in the existing Phase 63 Slack rule block
- `pkg/profile/validate_slack_transcript_test.go` — replaced 5 t.Skip stubs with real assertions using new `boolPtrTranscript`/`minimalTranscriptProfile`/`containsTranscriptError` helpers (mirrors `validate_slack_inbound_test.go`)
- `pkg/profile/schemas/sandbox_profile.schema.json` — added `notifySlackTranscriptEnabled` schema entry under spec.cli.properties

## Decisions Made

- **Field type bool, not \*bool:** Default false (no opt-in) is the explicit semantic. Following Phase 67 `NotifySlackInboundEnabled` precedent — the unset-vs-false distinction isn't needed because validation rejects misconfiguration rather than treating "unset" as a third state.
- **Position next to NotifySlackInboundEnabled:** Both are Phase 67/68 bidirectional/streaming Slack toggles with identical prerequisite chains. Existing CLISpec ordering is semantic-grouping, not strict alphabetical, so this matches convention.
- **Schema entry next to notifySlackInboundEnabled in JSON schema:** Same logical-sibling rationale.
- **Hard errors (IsWarning: false) for all three rules:** Same as Phase 67 inbound. A misconfigured transcript flag would either silently no-op (no streaming despite operator setting it) or send transcripts to an unintended audience (channel override) — both worse than fail-fast validation.
- **Schema path correction:** Plan referenced `pkg/profile/profile.schema.json` but the actual codebase location is `pkg/profile/schemas/sandbox_profile.schema.json`. Honored the real location used by `ValidateSchema`. Verification via `jq` confirms valid JSON and `go test` exercises the live schema.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Schema file path correction**
- **Found during:** Task 1 (JSON Schema entry add)
- **Issue:** Plan instructed editing `pkg/profile/profile.schema.json`, which does not exist. Actual schema location is `pkg/profile/schemas/sandbox_profile.schema.json`.
- **Fix:** Located real schema via `find pkg/profile -name '*.schema.json'`, edited that file. The plan's stated `verify automated` grep would have failed against the wrong path; correct path now contains the entry.
- **Files modified:** `pkg/profile/schemas/sandbox_profile.schema.json`
- **Verification:** `grep -c notifySlackTranscriptEnabled pkg/profile/schemas/sandbox_profile.schema.json` returns 1; `jq` valid; build clean.
- **Committed in:** `7496367` (Task 1)

---

**Total deviations:** 1 auto-fixed (1 blocking — file path correction)
**Impact on plan:** Documentation-level discrepancy in plan; implementation correct. No scope creep.

## Issues Encountered

**Parallel-execution interleaving (informational, not a problem):**
A concurrent agent executing Plan 68-02 (`78955b8 test(68-02): promote Wave-0 stubs to ActionUpload canonical-JSON tests`) staged unrelated `pkg/slack/payload*` files and inadvertently swept my staged `pkg/profile/validate_slack_transcript_test.go` into its commit (likely via `git add .` or similar). Net effect:
- The intended Task-2-RED commit ("test(68-01): replace Wave-0 stubs with real assertions") was never created as a standalone commit
- The test file content reached HEAD via `78955b8` with my exact RED authoring intact (verified by re-reading the file at HEAD)
- My subsequent Task-2-GREEN commit (`a58576e`) added the validation rules and the existing test file at HEAD then immediately passed
- All five `TestValidate_SlackTranscript_*` tests PASS at the final HEAD

The parallel agent's commit message attribution is incorrect for the transcript test content (it claims 68-02 but the test file belongs to 68-01); flagging here so the historical commit log is interpreted with this context. No corrective action taken — content is in HEAD, tests pass, rewriting history mid-execution would risk losing the parallel agent's legitimate work.

**Pre-existing modified `pkg/slack/payload.go`:**
At plan start, `pkg/slack/payload.go` had uncommitted Phase-68-aligned modifications (ActionUpload constant, BuildEnvelopeUpload errors — Plan 68-05/68-08 territory). It got swept into my Task 1 commit (`7496367`) despite explicit-path `git add` (likely already in the index when the session started). Content is forward-additive and used by later plans, so leaving it in 7496367 is harmless; rewriting history to extract it would be more disruptive than the misattribution. Flagged for awareness.

## User Setup Required

None — profile field addition is self-contained. Operators MUST run after the broader Phase 68 lands:

```bash
make build               # rebuild km with new validation (per feedback_rebuild_km.md)
km init --sidecars       # refresh management Lambda toolchain so REMOTE km validate honors the new field
km init                  # apply any infra updates
```

Without `km init --sidecars`, remote `km create` calls (which run inside the management Lambda) will reject the new field as unknown. Existing sandboxes do NOT pick up `notifySlackTranscriptEnabled` retroactively — operators must `km destroy` + `km create` to bake new env vars into a fresh user-data script.

## Next Phase Readiness

- Profile field, schema, and validation are wired. Subsequent plans (68-02..68-12) can now consume `cli.NotifySlackTranscriptEnabled` as a runtime guard.
- Plans needing `cli.NotifySlackTranscriptEnabled` runtime read: 68-04 (compiler env injection), 68-09 (PostToolUse hook gating), 68-10 (Stop hook upload gating), 68-11 (km create branching), 68-12 (km doctor checks)
- No blockers identified.

## Self-Check: PASSED

Verified at HEAD (`a58576e`):
- FOUND: `pkg/profile/types.go` — contains `NotifySlackTranscriptEnabled` (2 hits: declaration + comment)
- FOUND: `pkg/profile/validate.go` — contains `notifySlackTranscriptEnabled` (3 hits: three rule paths)
- FOUND: `pkg/profile/schemas/sandbox_profile.schema.json` — contains `notifySlackTranscriptEnabled` (1 hit: schema entry)
- FOUND: `pkg/profile/validate_slack_transcript_test.go` — 5 real test functions (no t.Skip)
- FOUND commit `7496367`: feat(68-01): add NotifySlackTranscriptEnabled profile field + JSON schema
- FOUND commit `a58576e`: feat(68-01): add three notifySlackTranscriptEnabled validation rules
- Test status: 5 PASS, 0 FAIL, 0 SKIP for `TestValidate_SlackTranscript_*`
- Build: `go build ./...` clean
- Schema: `jq . pkg/profile/schemas/sandbox_profile.schema.json` valid

---
*Phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload*
*Plan: 01*
*Completed: 2026-05-03*
