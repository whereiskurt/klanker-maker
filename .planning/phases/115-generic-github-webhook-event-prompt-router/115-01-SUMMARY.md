---
phase: 115-generic-github-webhook-event-prompt-router
plan: "01"
subsystem: github-bridge
tags: [tdd, red-scaffold, wave-0, github-webhooks, event-router]
dependency_graph:
  requires: []
  provides:
    - "pkg/github/bridge/event_router_test.go (RED: MatchEventRule + ExpandEventTemplate)"
    - "pkg/github/bridge/webhook_handler_phase115_test.go (RED: event-route gating + dispatch + cooldown)"
    - "internal/app/config/config_test.go additions (RED: GithubEventRule load round-trip)"
    - "internal/app/cmd/github_test.go additions (RED: manifest event-union)"
    - "internal/app/cmd/doctor_test.go additions (RED: checkGitHubEventsValid)"
  affects:
    - "Phase 115 Plans 02-05 (test targets for production implementation)"
tech_stack:
  added: []
  patterns:
    - "TDD Wave 0 RED scaffold: table-driven tests referencing not-yet-implemented symbols"
    - "bridge_test package reuse: mock types from handle_test.go shared across phase files"
    - "package cmd (same-package) test: unexported checkGitHubEventsValid callable from doctor_test.go"
key_files:
  created:
    - path: "pkg/github/bridge/event_router_test.go"
      purpose: "RED tests for MatchEventRule (9 cases) + ExpandEventTemplate (6 cases)"
    - path: "pkg/github/bridge/webhook_handler_phase115_test.go"
      purpose: "RED tests for event-route gating (5 cases), dispatch (no-alias cold/alias-warm/no-match/issue_comment unchanged/agent field), cooldown (GUID dedup/window suppression/zero-cooldown)"
  modified:
    - path: "internal/app/config/config_test.go"
      purpose: "Added TestLoadGithubEvents (populated) + TestLoadGithubEvents_Absent (dormancy sentinel)"
    - path: "internal/app/cmd/github_test.go"
      purpose: "Added TestRunGitHubManifest_EventUnion: event union + metadata:read permission"
    - path: "internal/app/cmd/doctor_test.go"
      purpose: "Added TestCheckGitHubEventsValid: SKIP/WARN/OK cases for event rule validation"
decisions:
  - "TestHandleEventRoute_Cooldown uses separate handler instances for the cooldown==0 case (cleanest isolation without refactoring mockPublisher to count calls)"
  - "cfg.Github.Events assigned directly in github_test.go (package cmd_test) — config.GithubEventRule type referenced to make compile-fail precise"
  - "doctor_test.go TestCheckGitHubEventsValid stays in package cmd (not cmd_test) to access unexported checkGitHubEventsValid, mirroring doctor_github_commands_test.go"
metrics:
  duration: "286s"
  completed_date: "2026-06-15"
  tasks_completed: 3
  tasks_total: 3
  files_created: 2
  files_modified: 3
---

# Phase 115 Plan 01: Wave 0 RED Test Scaffold Summary

Wave 0 RED scaffold for Phase 115 generic GitHub webhook event router. Five test file additions (two new, three additions) that pin the expected behavior of every unit-testable Phase 115 requirement before any production code exists. All five fail to compile citing missing production symbols — genuine RED.

## What Was Done

### Task 1: event_router_test.go (GH-EVENT-ROUTER + GH-EVENT-TEMPLATE)

Created `pkg/github/bridge/event_router_test.go` with two test functions:

- `TestMatchEventRule` — 12 table-driven subtests covering: empty rules, wrong eventType, exact-before-glob (both orders), first-glob-wins, match/no-match on glob `myorg/*`, empty actions vs non-empty actions + action in/not in list, exclude glob suppresses otherwise-matching rule, non-excluded repo still matches
- `TestExpandEventTemplate` — 6 subtests: all six vars replaced, unknown var verbatim, empty template, repeated var, eventType param, no vars verbatim

Compile-fails on: `bridge.EventPayload`, `bridge.EventRule`, `bridge.MatchEventRule`, `bridge.ExpandEventTemplate`

### Task 2: webhook_handler_phase115_test.go (GH-EVENT-GATING + GH-EVENT-DISPATCH + GH-EVENT-COOLDOWN)

Created `pkg/github/bridge/webhook_handler_phase115_test.go` with:

- `TestHandleEventRoute_Dispatch` — 5 subtests: empty EventRules drops non-issue_comment with 200; matched rule + no alias cold-creates (Number==0, Kind=="repository", expanded Body); matched rule + alias enqueues SQS (warm); no match → 200 no dispatch; issue_comment path unchanged with EventRules present; rule.Agent propagated to envelope
- `TestHandleEventRoute_Cooldown` — 3 subtests: GUID dedup fires before routing (same GUID = no second dispatch); cooldown>0 suppresses second delivery within window; cooldown==0 allows both deliveries

Compile-fails on: `bridge.EventRule`, `WebhookHandler.EventRules` field

### Task 3: Config, manifest, doctor additions (GH-EVENT-CONFIG + GH-EVENT-MANIFEST + GH-EVENT-DOCTOR)

Three file additions:

1. `config_test.go` — `TestLoadGithubEvents` + `TestLoadGithubEvents_Absent`: yaml round-trip for `github.events:` block; proves existing `UnmarshalKey("github", ...)` picks up Events automatically (no separate merge-list entry needed). Compile-fails on `cfg.Github.Events`.

2. `github_test.go` — `TestRunGitHubManifest_EventUnion`: `cfg.Github.Events` with `on: repository` rule → manifest `default_events` contains both `"issue_comment"` AND `"repository"`; `default_permissions["metadata"]=="read"`. Compile-fails on `cfg.Github.Events` + `config.GithubEventRule`.

3. `doctor_test.go` — `TestCheckGitHubEventsValid`: 5 subtests (empty→SKIP, malformed match glob→WARN, malformed exclude glob→WARN, missing profile→WARN, valid rule→OK). Compile-fails on `checkGitHubEventsValid` + `appcfg.GithubEventRule`.

## Verification Results

All test targets confirm RED for the right reasons:

```
go test ./pkg/github/bridge/... -run 'TestMatchEventRule|TestExpandEventTemplate|TestHandleEventRoute' -timeout 30s
→ FAIL [build failed]: undefined: bridge.EventPayload, bridge.EventRule, WebhookHandler.EventRules

go test ./internal/app/config/... ./internal/app/cmd/... -run 'TestLoadGithubEvents|TestRunGitHubManifest|TestCheckGitHubEventsValid' -timeout 60s
→ FAIL [build failed]: cfg.Github.Events undefined, checkGitHubEventsValid undefined, appcfg.GithubEventRule undefined
```

## Deviations from Plan

None — plan executed exactly as written.

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| Task 1 | 5d0e90dd | test(115-01): RED scaffold for MatchEventRule + ExpandEventTemplate |
| Task 2 | 3e2e2d53 | test(115-01): RED scaffold for event-route gating, dispatch, cooldown |
| Task 3 | b6ec45f3 | test(115-01): RED scaffolds for config load, manifest union, doctor check |

## Self-Check: PASSED

Files exist:
- pkg/github/bridge/event_router_test.go: FOUND
- pkg/github/bridge/webhook_handler_phase115_test.go: FOUND
- internal/app/config/config_test.go (modified): FOUND
- internal/app/cmd/github_test.go (modified): FOUND
- internal/app/cmd/doctor_test.go (modified): FOUND

Commits exist: 5d0e90dd, 3e2e2d53, b6ec45f3 all in git log.
