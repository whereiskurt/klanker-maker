---
phase: 103-hackerone-comment-trigger-bridge
plan: 03
subsystem: h1-bridge
tags: [hackerone, payload-parse, hmac, command-parser, agent-verbs, reply-to-researcher, tdd, port]

# Dependency graph
requires:
  - phase: 103-hackerone-comment-trigger-bridge
    plan: 01
    provides: "103-CAPTURE/field-paths.md pinned JSON paths + two synthetic webhook bodies"
  - phase: 102-github-bridge-agent-verbs
    provides: "pkg/github/bridge payload.go + commands.go (the verbatim port sources)"
provides:
  - "pkg/h1/bridge/payload.go — H1WebhookPayload (data.activity + data.report), wrapper-tolerant report resolve, field accessors, H1Envelope, VerifyH1Signature"
  - "pkg/h1/bridge/commands.go — ParseCommands/RunCommandPass/StripCode/ExtractArgs, /claude /codex agent verbs, /reply_to_researcher reserved token + intent flag, ExpandTemplate + ExpandTemplateFields (report-field refs)"
  - "CommandEntry shared type (owned by commands.go; resolve.go references it)"
affects: [103-04-webhook-handler, 103-07-userdata-poller, 103-08-lambda-main]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Wrapper-tolerant JSON:API parse: report resolved from data.report OR data.report.data, preferring the deepest object carrying relationships.program (synthetic-fallback directive)"
    - "Safety-by-zero-value: H1Envelope.ReplyToResearcher defaults false = internal; the gate lives in Plan 04, parse layer only carries intent"
    - "Reserved-token interception before template-command lookup (/help /claude /codex /reply_to_researcher) — verbatim port + 1 H1 addition"

key-files:
  created:
    - pkg/h1/bridge/payload.go
    - pkg/h1/bridge/payload_test.go
    - pkg/h1/bridge/commands.go
    - pkg/h1/bridge/commands_test.go
    - pkg/h1/bridge/testdata/report_created.json
    - pkg/h1/bridge/testdata/report_comment_created.json
  modified: []

key-decisions:
  - "Ported VerifyH1Signature byte-identical from VerifyGitHubSignature (header-name swap only); rawBody contract = already-base64-DECODED bytes, with TestVerifyH1Signature_Base64 proving the decode-first requirement (Pitfall 1)"
  - "Parser made wrapper-tolerant (single-data + JSON:API double-data) per field-paths.md synthetic-fallback directive; missing program handle = empty string (hard resolve-miss), never a panic"
  - "/reply_to_researcher is parse-only here — sets ParseResult.ReplyToResearcher and is always stripped from prompt text; the visibility+allowlist gate is deferred to Plan 04 (internal-by-default safety invariant)"
  - "CommandEntry ownership settled on commands.go after a live cross-plan flip-flop with 103-02 (their commits 17217e86 then f2b47871 deferred the decl to commands.go); single-owner state is build-green and stable"

requirements-completed: [H1-BRIDGE-HMAC, H1-COMMAND-PARSE, H1-AGENT-VERB, H1-EVENT-PROMPT-MAP]

# Metrics
duration: 9min
completed: 2026-06-10
---

# Phase 103 Plan 03: HackerOne payload parse + command/agent-verb engine Summary

**Built the genuinely-new HackerOne data layer — wrapper-tolerant `data.activity`+`data.report` payload parse (struct tags pinned from the Wave-0 capture), the header-swapped base64-aware HMAC verifier, the `H1Envelope` (internal-by-default safety zero value), and the GitHub-ported command/agent-verb engine extended with the `/reply_to_researcher` reserved token and report-field template expansion.**

## Performance

- **Duration:** ~9 min (inflated by a live cross-plan symbol race; see Deviations)
- **Tasks:** 2 (both TDD: RED → GREEN)
- **Files created:** 6 (2 impl, 2 test, 2 testdata fixtures)
- **Test result:** `go test ./pkg/h1/bridge` green; both plan verification commands pass

## Accomplishments

### Task 1 — payload.go + VerifyH1Signature
- `H1WebhookPayload` parses the HackerOne `data.activity` + `data.report` envelope using the EXACT struct tags pinned in `103-CAPTURE/field-paths.md`: program handle (`data.report.relationships.program.data.attributes.handle`), report id/title/state, actor username, the safety-critical `internal` flag, and the comment `message`.
- **Wrapper-tolerant resolve** (`resolveReport`): accepts BOTH `data.report.{...}` and the JSON:API double-data `data.report.data.{...}` nesting, preferring the deepest object carrying `relationships.program` — per the synthetic-fallback parse-tolerance directive. A missing program handle yields an empty `ProgramHandle()` (hard resolve-miss for the caller to drop), never a panic.
- Field accessors `ProgramHandle()/ReportID()/Title()/State()/ActorUsername()/CommentBody()/Internal()/ActivityID()/ActivityType()` form the handler/template consumption surface.
- `H1Envelope` carries `source="hackerone"`, program, report_id, kind, actor, body, agent, and `ReplyToResearcher bool` whose **zero value (false) = internal** — the safety invariant.
- `VerifyH1Signature` ported verbatim from `VerifyGitHubSignature` (only the doc/header name swapped to `X-H1-Signature`). `TestVerifyH1Signature_Base64` documents the decode contract: the HMAC is over the base64-DECODED body, and feeding still-encoded bytes must fail.

### Task 2 — commands.go (parse + agent verbs + /reply_to_researcher + ExpandTemplate)
- `StripCode / ParseCommands / RunCommandPass / ExtractArgs / EffectiveDefault / ResolveCommandRouting / CommandAllowed / buildHelpReply` forked near-verbatim from the GitHub bridge (repo→program rename).
- `<=1`-distinct-command MultiError rule preserved; `/claude` `/codex` agent verbs with dedup + conflict; `/help` reserved built-in with the agents listing + current-thread-agent line.
- **`/reply_to_researcher` reserved token added**: parsing it sets `ParseResult.ReplyToResearcher` (parse-only intent), and it is always stripped from the extracted prompt text. It never appears in `Known` (reserved interception before the command-map lookup).
- **`ReportFields` + `ExpandTemplateFields`**: pre-expand the fixed `{{report_id}}/{{title}}/{{state}}/{{program}}` refs from handler-supplied values, then run the standard `{{args}}` fill via `ExpandTemplate`. Unknown `{{x}}` placeholders are preserved verbatim.

## Task Commits

1. **Task 1 RED** (payload tests + fixtures) — `1136c69c` (test)
2. **Task 1 GREEN** (payload.go) — `1e20f493` (feat)
3. **Task 2 RED** (commands tests) — `9854116e` (test)
4. **Task 2 GREEN** (commands.go) — `57f149c7` (feat)

## Files Created/Modified
- `pkg/h1/bridge/payload.go` — H1WebhookPayload, wrapper-tolerant resolve, accessors, H1Envelope, VerifyH1Signature (280 lines)
- `pkg/h1/bridge/payload_test.go` — parse (both fixtures + double-data tolerance + missing-handle), HMAC verify, base64 contract (251 lines)
- `pkg/h1/bridge/commands.go` — command/agent-verb engine, /reply_to_researcher, ExpandTemplateFields, CommandEntry (490 lines)
- `pkg/h1/bridge/commands_test.go` — ParseCommands/AgentVerb/ReservedTokens/ExpandTemplate/RunCommandPass smoke (395 lines)
- `pkg/h1/bridge/testdata/{report_created,report_comment_created}.json` — captured fixtures copied for in-package test consumption

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Cross-plan `CommandEntry` symbol race with parallel Plan 103-02**
- **Found during:** Task 2 (first build of commands.go)
- **Issue:** Plan 103-02 (running in parallel) also lands `pkg/h1/bridge/resolve.go`, whose `ProgramEntry.Commands` references the shared `CommandEntry` type. During execution the declaration flip-flopped between `resolve.go` and `commands.go` (observed in their committed history: `17217e86` "own CommandEntry in resolve.go" then `f2b47871` "defer CommandEntry decl to commands.go"), repeatedly breaking the package build with `CommandEntry redeclared` / `undefined: CommandEntry`.
- **Fix:** Settled ownership in `commands.go` (the command-parsing domain, matching the GitHub port and 103-02's final deferring comment). Polled the shared file until a single-owner, build-green state held stably (16s window), then committed. No edits made to `resolve.go` (103-02's file) — the resolution is purely via where my own file declares the type.
- **Files modified:** `pkg/h1/bridge/commands.go` (mine only)
- **Commit:** `57f149c7`
- **Note:** Final state is consistent — `resolve.go` carries 0 declarations + a "declared in commands.go" coordination comment; `commands.go` owns the single `CommandEntry` with the full Description/Alias/Profile/Allow/Prompt field set both plans need. Build + full package test green.

## Coordination Notes
- Stayed strictly within this plan's `files_modified` (payload + command parser). Did NOT touch config structs (103-02), `cmd/km-h1` (103-05), `webhook_handler.go` (103-04), or `resolve.go`/`interfaces.go` (103-02).
- No production HackerOne program is referenced; fixtures use the synthetic `km-sandbox` handle.

## Issues Encountered
- The parallel-plan symbol race (above) was the only friction; resolved without crossing file boundaries.

## Next Phase Readiness
- Plan 04 (webhook_handler): `ParsePayload`, `VerifyH1Signature`, `H1Envelope`, and `RunCommandPass`/`ParseResult.ReplyToResearcher` are ready to wire. The reply-visibility gate (Command-present AND program allowlist) consumes `ReplyToResearcher` there.
- Plan 10 (E2E): re-pin every DOCS-SHAPED row (envelope wrapper placement, header casing) against the real HackerOne Sandbox webhook delivery and tighten `resolveReport` if the live wrapper differs — the parser already fails safe on surprise.

## Self-Check: PASSED

All 6 created files exist on disk; all 4 task commits (`1136c69c`, `1e20f493`, `9854116e`, `57f149c7`) present in git history. `go test ./pkg/h1/bridge` green; both plan verification commands pass.

---
*Phase: 103-hackerone-comment-trigger-bridge*
*Completed: 2026-06-10*
