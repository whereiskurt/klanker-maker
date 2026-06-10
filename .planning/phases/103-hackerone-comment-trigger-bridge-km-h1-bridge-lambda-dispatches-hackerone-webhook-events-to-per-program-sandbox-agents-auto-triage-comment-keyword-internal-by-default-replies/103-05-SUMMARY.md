---
phase: 103-hackerone-comment-trigger-bridge
plan: 05
subsystem: sandbox-helper
tags: [hackerone, km-h1, basic-auth, internal-by-default, back-channel, tdd]

# Dependency graph
requires:
  - phase: 103-hackerone-comment-trigger-bridge
    plan: 01
    provides: "103-CAPTURE/field-paths.md pinning the activities endpoint, the internal flag, and the OQ2-deferred state endpoint"
provides:
  - "cmd/km-h1 sandbox-side HackerOne customer-API helper: comment | read | state over HTTP Basic Auth"
  - "Internal-by-default reply guard (safety layer 4): comment posts attributes.internal:true unless --reply-to-researcher is explicit"
  - "429/5xx backoff-retry ladder and Basic-Auth creds loaded from env (KM_H1_API_USER/KM_H1_API_TOKEN) or SSM (/{prefix}/config/h1/api-username + api-token)"
affects: [103-07-userdata-poller, 103-10-e2e]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Options-based dispatch(args, stderr, ...option) so httptest tests inject base URL / Basic-Auth creds / stdout sink / zero backoff without os.Args or env"
    - "internal-by-default enforced at BOTH the flag default (true) and the JSON marshalling layer — no code path silently posts external"
    - "LOW-confidence endpoint kept thin + a KM_H1_STATE_ENDPOINT printf-template override so a fast-follow can repoint without a rebuild"

key-files:
  created:
    - cmd/km-h1/main.go
    - cmd/km-h1/main_test.go
  modified: []

key-decisions:
  - "Forked cmd/km-github's testable-inner-entry-point shape but dropped the App-JWT/installation-token loader entirely — HackerOne customer API is plain Basic Auth, so creds are read once (env-then-SSM), no refresher"
  - "comment body is supplied as --body @file (mirrors km-github file-based body handling) per the CLAUDE.md OpenSSL/stdin constraint; a bare value is tolerated as a literal for convenience"
  - "state implements the OQ2 best-effort candidate POST /reports/{id}/state_changes with an in-code uncertainty note + a KM_H1_STATE_ENDPOINT escape hatch, deferring the live-pinned correction to Plan 10 (Wave 6) E2E against the HackerOne Sandbox program"

patterns-established:
  - "Safety-critical defaults are asserted at the JSON-body level in tests (TestCommentInternalDefault decodes attributes.internal), not merely at the flag layer"

requirements-completed: [H1-HELPER-KM-H1, H1-REPLY-INTERNAL-DEFAULT]

# Metrics
duration: 3min
completed: 2026-06-10
---

# Phase 103 Plan 05: km-h1 sandbox-side HackerOne helper Summary

**Built `cmd/km-h1` — the sandbox agent's back-channel to HackerOne: `comment` / `read` / `state` over HTTP Basic Auth, with the safety-critical internal-by-default reply guard enforced at both the flag and JSON-body level.**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-06-10T04:00:43Z
- **Completed:** 2026-06-10T04:03:39Z
- **Tasks:** 2
- **Files created:** 2

## What Was Built

A 3-verb fork of `cmd/km-github` that swaps the GitHub App-JWT/installation-token dance for plain HTTP Basic Auth (the big HackerOne simplification — no per-sandbox token refresher):

- **`comment --report N --body @file [--reply-to-researcher]`** — POST `/reports/{N}/activities` with `{"data":{"type":"activity-comment","attributes":{"message":...,"internal":...}}}`. `internal` defaults **true** at the flag AND in the marshalled JSON; `--reply-to-researcher` is the only path that flips it to `false` (safety layer 4 — researcher-visible replies are explicit at every layer).
- **`read --report N`** — GET `/reports/{N}`, prints the report JSON to stdout for the agent to consume.
- **`state --report N --to <state>`** — POST `/reports/{N}/state_changes` (OQ2 LOW-confidence best-effort candidate; thin + correctable via the `KM_H1_STATE_ENDPOINT` printf-template override pending the Plan 10 live pinning).

Shared infrastructure: a `doWithRetry` helper that sets Basic Auth (`req.SetBasicAuth`), a 429/5xx backoff ladder (1s/2s/4s), and creds resolution preferring `KM_H1_API_USER`/`KM_H1_API_TOKEN` env (poller-exported) then SSM `/{prefix}/config/h1/api-username` + `api-token`.

## Tasks Completed

| Task | Name | Commit | Files |
| ---- | ---- | ------ | ----- |
| 1 | km-h1 comment (internal-default) + read | f78707d3 | cmd/km-h1/main.go, cmd/km-h1/main_test.go |
| 2 | km-h1 state (best-effort per OQ2) + endpoint override | 27d3efb4 | cmd/km-h1/main.go, cmd/km-h1/main_test.go |

## Verification

- `go test ./cmd/km-h1 -count=1` — 10/10 pass:
  - `TestCommentInternalDefault` — asserts `attributes.internal == true` with NO flag, at the decoded-JSON-body level.
  - `TestCommentResearcherVisible` — `--reply-to-researcher` ⇒ `internal == false`.
  - `TestCommentBasicAuth` — `Authorization: Basic` header carries the supplied creds.
  - `TestRead` — GET `/reports/{id}`, prints body.
  - `TestRetry429` — 429-then-200 retried with backoff, succeeds (asserts exactly 2 server calls).
  - `TestState` / `TestStateEndpointOverride` — pinned `POST /reports/{id}/state_changes` + the override escape hatch.
- `go vet ./cmd/km-h1` clean; `go build ./cmd/km-h1` OK.

## Coordination Note

Stayed strictly within this plan's `files_modified` (`cmd/km-h1/main.go` + its test). Did NOT touch `pkg/h1/bridge` (sibling Plans 02/03). No production HackerOne program referenced; the only live target for this phase is the operator's HackerOne Sandbox account (E2E in Plan 10).

## Deviations from Plan

**None for Rules 1-4.** One in-scope hardening within Task 2's mandate ("keep the verb thin so a fast-follow can correct it"): added the `KM_H1_STATE_ENDPOINT` printf-template override so the LOW-confidence OQ2 state endpoint can be repointed at runtime without a rebuild, plus `TestStateEndpointOverride`. This is a correctability affordance for the deferred Plan 10 live-pinning, not a scope expansion.

## Self-Check: PASSED

- cmd/km-h1/main.go — FOUND
- cmd/km-h1/main_test.go — FOUND
- commit f78707d3 — FOUND
- commit 27d3efb4 — FOUND
