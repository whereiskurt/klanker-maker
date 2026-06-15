---
phase: 115-generic-github-webhook-event-prompt-router
plan: "02"
subsystem: github-bridge
tags: [tdd, green, wave-2, github-webhooks, event-router, config]
dependency_graph:
  requires:
    - "115-01 (Wave 0 RED scaffold: event_router_test.go, webhook_handler_phase115_test.go, config_test.go)"
  provides:
    - "pkg/github/bridge/event_router.go (EventRule, EventPayload, MatchEventRule, ExpandEventTemplate — pure functions)"
    - "pkg/github/bridge/payload.go HTMLURL field on RepositoryField"
    - "pkg/github/bridge/webhook_handler.go EventRules field on WebhookHandler"
    - "internal/app/config/config.go GithubEventRule struct + Events field on GithubConfig"
  affects:
    - "Phase 115 Plan 03 (webhook_handler.go handleEventRoute — reads EventRules set here)"
    - "Phase 115 Plans 04-05 (init.go KM_GITHUB_EVENTS export, main.go cold-start parse, doctor check)"
tech_stack:
  added: []
  patterns:
    - "Pure function event router: exact-before-glob two-pass (mirrors resolve.go Resolve pattern)"
    - "strings.NewReplacer template expansion: six named vars, no text/template (mirrors commands.go ExpandTemplate)"
    - "mapstructure+yaml+json triple-tagged config struct (mirrors GithubCommandEntry pattern)"
    - "Single UnmarshalKey atomic decode: no sibling merge-list entry for github.events"
key_files:
  created:
    - path: "pkg/github/bridge/event_router.go"
      purpose: "EventRule, EventPayload, MatchEventRule, ExpandEventTemplate — pure IO-free functions; 162 lines"
  modified:
    - path: "pkg/github/bridge/payload.go"
      purpose: "Added HTMLURL string field to RepositoryField for {{html_url}} template var"
    - path: "pkg/github/bridge/webhook_handler.go"
      purpose: "Added EventRules []EventRule field to WebhookHandler (dormant-by-default; unblocked phase115 test compile)"
    - path: "internal/app/config/config.go"
      purpose: "Added GithubEventRule struct (10 fields, all mapstructure-tagged) + Events []GithubEventRule to GithubConfig"
decisions:
  - "Added WebhookHandler.EventRules in Plan 02 (not Plan 03): the field was required to unblock TestMatchEventRule/TestExpandEventTemplate compile (both tests share package bridge_test with webhook_handler_phase115_test.go which references the field — Rule 3 blocking issue)"
  - "isGlob reused from resolve.go without re-declaration: keeps glob semantics identical to github.repos matching"
  - "excluded() checks exact non-glob entries first (g==repo) in addition to glob entries: consistent with the match pass behavior"
  - "cooldown_seconds uses snake_case in mapstructure/json but camelCase (cooldownSeconds) in yaml tag per CONTEXT.md config shape"
metrics:
  duration: "215s"
  completed_date: "2026-06-15"
  tasks_completed: 2
  tasks_total: 2
  files_created: 1
  files_modified: 3
---

# Phase 115 Plan 02: EventRouter Core Implementation Summary

Pure IO-free event router + template expander + config struct. Turns the Plan 01 RED tests `TestMatchEventRule`, `TestExpandEventTemplate`, and `TestLoadGithubEvents` GREEN.

## What Was Done

### Task 1: EventRouter matcher + template expander (event_router.go)

Created `pkg/github/bridge/event_router.go` with four exported symbols:

- `EventRule` struct — 10 fields with json tags mirroring the km-config.yaml surface (json tags match KM_GITHUB_EVENTS wire format); same shape as `GithubEventRule` in config.go but bridge-local (no mapstructure needed)
- `EventPayload` struct — five string fields: Repo, Action, Sender, DefaultBranch, HTMLURL
- `MatchEventRule(eventType string, payload EventPayload, rules []EventRule) *EventRule` — two-pass exact-before-glob first-match, identical to `Resolve()` in `resolve.go`; reuses `isGlob` from the same package; `excluded()` helper checks both exact and glob patterns in exclude list
- `ExpandEventTemplate(tmpl string, p EventPayload, eventType string) string` — `strings.NewReplacer` with the six vars `{{repo}}`, `{{event}}`, `{{action}}`, `{{sender}}`, `{{default_branch}}`, `{{html_url}}`; no `text/template`

Edited `pkg/github/bridge/payload.go`: added `HTMLURL string \`json:"html_url"\`` to `RepositoryField` (GitHub sends this at `repository.html_url` for repository/created and push events).

Edited `pkg/github/bridge/webhook_handler.go`: added `EventRules []EventRule` field to `WebhookHandler` struct (deviation — see below). Required to unblock package compilation.

### Task 2: GithubEventRule config struct + Events field (config.go)

Edited `internal/app/config/config.go`:

1. Added `GithubEventRule` struct near `GithubCommandEntry` (line ~139). All 10 fields carry mapstructure/yaml/json triple tags per the viper UnmarshalKey requirement. The yaml `cooldownSeconds` tag matches the CONTEXT.md config surface while mapstructure/json use snake_case `cooldown_seconds`.

2. Added `Events []GithubEventRule` field to `GithubConfig` with mapstructure `"events"` tag. Decoded automatically by the existing `v.UnmarshalKey("github", &cfg.Github)` call at config.go:831 via the existing `"github"` merge-list entry at config.go:699 — no new sibling entry added.

## Verification Results

```
go test ./pkg/github/bridge/... -run 'TestMatchEventRule|TestExpandEventTemplate' -timeout 30s -count=1
→ ok  github.com/whereiskurt/klanker-maker/pkg/github/bridge  0.329s  EXIT: 0

go test ./internal/app/config/... -timeout 60s -count=1
→ ok  github.com/whereiskurt/klanker-maker/internal/app/config  0.215s  EXIT: 0

grep -n '"github.events"' internal/app/config/config.go | grep -v '//'
→ (no output — no sibling merge-list entry)

grep -n 'isGlob' pkg/github/bridge/event_router.go
→ lines 82, 112, 158 — called only, not redefined
```

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Added WebhookHandler.EventRules field in Plan 02 (not Plan 03)**
- **Found during:** Task 1 verify (go test ./pkg/github/bridge/... -run TestMatchEventRule)
- **Issue:** `webhook_handler_phase115_test.go` (same package `bridge_test`) references `WebhookHandler.EventRules` in struct literals. When this field is absent, the ENTIRE package fails to compile, blocking `TestMatchEventRule` and `TestExpandEventTemplate` from running.
- **Fix:** Added `EventRules []EventRule` to `WebhookHandler` struct in `webhook_handler.go` with a dormant-by-default doc comment. The field is idle until Plan 03 wires `handleEventRoute`.
- **Files modified:** `pkg/github/bridge/webhook_handler.go`
- **Commit:** a8069708 (included with Task 1)

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| Task 1 | a8069708 | feat(115-02): EventRouter matcher + template expander (event_router.go) |
| Task 2 | 8eac88a5 | feat(115-02): GithubEventRule struct + Events field in GithubConfig (config.go) |

## Self-Check: PASSED
