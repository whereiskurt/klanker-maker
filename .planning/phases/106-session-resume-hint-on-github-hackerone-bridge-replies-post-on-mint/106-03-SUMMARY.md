---
phase: 106-session-resume-hint-on-github-hackerone-bridge-replies-post-on-mint
plan: "03"
subsystem: compiler-userdata
tags: [tdd, green-tests, h1-inbound, resume-hint, phase-106, post-on-mint, internal-only]
dependency_graph:
  requires: [RESUME-HINT-GITHUB, RESUME-HINT-TESTS]
  provides: [RESUME-HINT-H1, RESUME-HINT-FORMAT, RESUME-HINT-MINT, RESUME-HINT-SLACK-EXCLUDED]
  affects: [pkg/compiler/userdata.go]
tech_stack:
  added: []
  patterns: [TDD-GREEN, post-on-mint, best-effort-guard, heredoc-injection, internal-only-safety]
key_files:
  created: []
  modified:
    - pkg/compiler/userdata.go
decisions:
  - "Mirrored GitHub block structure exactly from Plan 02: same mint guard pattern, same agent-branch select, same printf format (plain text, no backticks — Go raw string literal constraint from Plan 02 deviation applies here too)"
  - "Placement: inside the existing if [ -n '$NEW_H1_SESSION' ] block, after the DDB update-item write-back and Session updated echo — matching the plan's line_number_refresh anchor precisely"
  - "Internal-only safety lock: km-h1 comment bare form (no --reply-to-researcher) — km-h1 defaults internal:true; the hint is never visible to the external HackerOne researcher"
metrics:
  duration: 120s
  completed: "2026-06-12T03:30:00Z"
  tasks_completed: 2
  files_modified: 1
---

# Phase 106 Plan 03: H1 inbound poller post-on-mint internal resume-hint block

H1 inbound poller now posts an internal-only collapsed `<details>` resume-hint comment after every session mint — one per newly-minted session id, gated by `NEW_H1_SESSION != ${H1_SESSION:-}`. The hint is INTERNAL by default (no `--reply-to-researcher`) and never visible to the external HackerOne researcher.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Insert H1 post-on-mint internal resume-hint block | c918518f | pkg/compiler/userdata.go |
| 2 | Prove H1 dormancy + Slack-exclusion + full compiler suite green | c918518f | (verification only, no new files) |

## What Was Built

**Task 1:** Inserted the Phase 106 resume-hint block inside the existing `if [ -n "$NEW_H1_SESSION" ]` mint guard in the `H1INBOUND` heredoc of `pkg/compiler/userdata.go`, immediately after the `aws dynamodb update-item` write-back and "Session updated" echo. The block:

- Is guarded by `[ -n "$NEW_H1_SESSION" ] && [ "$NEW_H1_SESSION" != "${H1_SESSION:-}" ]` — fires only on first turn or Gap-E re-mint
- Selects the agent-correct resume command: `codex exec resume $NEW_H1_SESSION` or `claude --resume $NEW_H1_SESSION` based on `EFFECTIVE_AGENT`
- Builds a collapsed `<details>` fold referencing `$SANDBOX_ID` and the `/workspace` run-from directory
- Posts via `/opt/km/bin/km-h1 comment --report "$REPORT_ID" --body "$HINT_BODY" || true` — internal-only, best-effort, never blocks SQS ack
- Does NOT pass `--reply-to-researcher` — km-h1 defaults `internal:true`; the hint stays on the internal/team comment track

**Task 2:** Full `go test ./pkg/compiler/ -count=1 -timeout 300s` suite GREEN (EXIT=0). All dormancy and byte-identity guards pass: `TestUserdataH1ByteIdentity` (H1-free profile — H1INBOUND block is inside `{{- if .H1InboundEnabled }}` conditional), `TestUserdataKmPrefixByteIdentity`, and `TestUserdata_SlackPollerUnaffectedByGitHubInbound`. Slack poller is byte-identical (untouched). GitHub resume-hint block from Plan 02 unaffected.

## Verification Results

| Check | Result |
|-------|--------|
| `TestUserdataH1EnabledRendersPoller` | GREEN |
| `TestUserdataH1ByteIdentity` (dormancy golden, H1-free profile) | GREEN |
| `TestUserdataKmPrefixByteIdentity` | GREEN |
| `TestUserdata_SlackPollerUnaffectedByGitHubInbound` | GREEN |
| All `TestUserdata_GitHubInbound*` | GREEN |
| Full `go test ./pkg/compiler/` | GREEN (EXIT=0) |
| No `--reply-to-researcher` on hint call | CONFIRMED |
| `go vet ./pkg/compiler/` | CLEAN (implicit in go test) |

## Deviations from Plan

None — plan executed exactly as written. The Go raw string literal / no-backtick constraint identified in Plan 02 was anticipated (same file, same constraint) and the printf format uses plain text from the outset.

## Self-Check: PASSED

- `pkg/compiler/userdata.go` — FOUND
- Commit c918518f — FOUND
- `TestUserdataH1EnabledRendersPoller` GREEN
- `TestUserdataH1ByteIdentity` GREEN (dormancy invariant preserved)
- `TestUserdataKmPrefixByteIdentity` GREEN
- Full `go test ./pkg/compiler/` GREEN (EXIT=0)
- No `--reply-to-researcher` in inserted block — CONFIRMED
