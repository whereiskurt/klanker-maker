---
phase: 102
plan: 04
subsystem: github-bridge
tags: [doctor, km-doctor, help-reply, agent-verbs, docs, operator-ux, tdd]
dependency_graph:
  requires: [102-01, 102-02]
  provides: [doctor-reserved-shadow-claude-codex, help-reply-agent-listing, phase-102-docs]
  affects: [internal/app/cmd/doctor.go, pkg/github/bridge/commands.go, pkg/github/bridge/webhook_handler.go, docs/github-bridge.md, CLAUDE.md]
tech_stack:
  added: []
  patterns: [tdd-red-green, table-driven-tests, pure-function-cascade]
key_files:
  created:
    - internal/app/cmd/doctor_github_commands_test.go (TestDoctorGitHubCommandsAgentVerbShadow added)
    - pkg/github/bridge/commands_test.go (TestBuildHelpReplyAgentListing added)
  modified:
    - internal/app/cmd/doctor.go (reserved-shadow loop extended to claude/codex)
    - pkg/github/bridge/commands.go (buildHelpReply + RunCommandPass gain currentAgentType param)
    - pkg/github/bridge/webhook_handler.go (captures agentType from LookupSandbox; passes to RunCommandPass)
    - docs/github-bridge.md (Phase 102 section added)
    - CLAUDE.md (Phase 102 bullet + Where-to-look row added)
decisions:
  - buildHelpReply and RunCommandPass extended with currentAgentType param (not a new type) — single-point cascade avoids wrapper layers
  - threadCurrentAgentType captured at the existing LookupSandbox call site (line 303); no second DDB read
  - /help agent listing prepended before the commands block so users see agents first
  - Reserved-shadow loop over slice (not 3 ifs) — forward-extensible if more verbs added
metrics:
  duration: 429s
  completed_date: "2026-06-08"
  tasks_completed: 3
  files_modified: 7
---

# Phase 102 Plan 04: UX Guardrails and Operator Docs Summary

km doctor now WARNs when `github.commands` shadows the reserved `/claude` or `/codex` agent verbs; the `/help` built-in reply advertises both verbs and the thread's current agent type; `docs/github-bridge.md` gains a complete Phase 102 operator runbook and `CLAUDE.md` gains the Phase 102 bullet.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | doctor reserved-shadow for claude/codex (TDD) | db41b37a | doctor.go, doctor_github_commands_test.go |
| 2 | /help reply lists agents + current thread agent (TDD) | 50d081eb | commands.go, webhook_handler.go, commands_test.go |
| 3 | Operator docs — github-bridge.md § Phase 102 + CLAUDE.md bullet | 7ce81b7c | docs/github-bridge.md, CLAUDE.md |

## What Was Built

### Task 1: doctor reserved-shadow (TDD)

`checkGitHubCommandsValid` in `doctor.go` previously checked only `"help"` for reserved-name shadowing. Replaced the single `if` with a loop over `[]string{"help","claude","codex"}`, emitting one `WARN` per shadowed name. Updated the `Remediation` string to mention all three. Added `TestDoctorGitHubCommandsAgentVerbShadow` with three table cases: `claude` → WARN, `codex` → WARN, clean map → OK.

### Task 2: /help reply agent listing (TDD)

`buildHelpReply` gained a `currentAgentType string` parameter. When non-empty it appends "**Current thread agent:** `<type>`" after the Available agents block. `RunCommandPass` gained the same parameter and passes it through. `webhook_handler.go` now captures the third return value of `LookupSandbox` (`agentType`) into `threadCurrentAgentType` and threads it to `RunCommandPass`. Added `TestBuildHelpReplyAgentListing` with 5 table cases covering: agent listing present, codex current agent shown, claude current agent shown, fresh-thread no current-agent line, `/help + /codex` help wins.

### Task 3: Operator docs

`docs/github-bridge.md` gained `## Phase 102 — Agent verbs (/claude, /codex)` covering: syntax, parsing rules, per-thread persistence + precedence table, Codex precondition, reserved tokens + km doctor WARN, /help extension, back-compat, deploy surface, and a 7-item E2E UAT checklist.

`CLAUDE.md` gained the Phase 102 bullet in the phase-history block (mirroring Phase 100/101 style) and a "Where to look" row pointing to `docs/github-bridge.md § Phase 102`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] pkg/compiler/userdata.go build error from Plan 102-03**

- **Found during:** Task 1 RED execution (first test run)
- **Issue:** Plan 102-03 (parallel execution) had already committed `userdata.go` with a backtick-escaped message (`\`/codex\``) inside a raw string literal. The file was already fixed by the time I read it (Plan 102-03 self-corrected mid-flight). The build was clean when I re-checked.
- **Fix:** No action required — the file was already corrected; confirmed clean build before proceeding.
- **Files modified:** none (pre-fixed)

## Self-Check: PASSED

- FOUND: internal/app/cmd/doctor.go
- FOUND: pkg/github/bridge/commands.go
- FOUND: docs/github-bridge.md
- FOUND: CLAUDE.md
- FOUND: commit db41b37a (Task 1)
- FOUND: commit 50d081eb (Task 2)
- FOUND: commit 7ce81b7c (Task 3)
