---
phase: 103-hackerone-comment-trigger-bridge
plan: 06
subsystem: operator-cli
tags: [hackerone, cli, ssm, basic-auth, webhook-secret]
requires:
  - "config.H1Config / H1ProgramEntry / H1Target structs (Plan 02)"
  - "SSMWriteAPI + putSSMParam + boolPtr helpers (configure_github.go)"
provides:
  - "km h1 init — mints webhook secret + captures Basic-Auth creds → SSM /{prefix}config/h1/*"
  - "km h1 status — redacted SSM-backed H1 config dump"
  - "RunH1Init / RunH1Status / NewH1Cmd / H1InitOpts (exported, DI-testable)"
affects:
  - "km-h1-bridge Lambda (Plan 04/08) reads webhook-secret + api-username + api-token back from these SSM writes"
tech-stack:
  added: []
  patterns:
    - "Forked github.go CLI shape, dropping the manifest subcommand (HackerOne has no App-install model)"
    - "SSM write seam via SSMWriteAPI interface for mock-driven TDD"
    - "Internal redaction at the status layer — secret + api-token never printed"
key-files:
  created:
    - internal/app/cmd/h1.go
    - internal/app/cmd/h1_test.go
  modified:
    - internal/app/cmd/root.go
decisions:
  - "api-username stored as plain String (not secret); webhook-secret + api-token as SecureString"
  - "No manifest subcommand — HackerOne webhooks are configured per-program in the UI, not via an App manifest"
  - "Interactive prompt fallback for api-username/api-token when flags omitted (mirrors github init input flow)"
metrics:
  duration: 3m
  completed: 2026-06-10
---

# Phase 103 Plan 06: km h1 init/status CLI Summary

`km h1 init` mints a 32-byte hex webhook secret and captures HackerOne customer-API Basic-Auth creds, writing all four params to SSM under `/{prefix}config/h1/{webhook-secret,api-username,api-token,bridge-url}` (secret + token SecureString); `km h1 status` prints the SSM-backed config + `cfg.H1` program routing with the webhook secret and api-token redacted. Forked from `internal/app/cmd/github.go`, dropping the manifest subcommand because HackerOne has no App-install model.

## What Was Built

### Task 1 — `RunH1Init` (commit c27df983, test 887a4c72)
- Mints a 32-byte hex (64-char) webhook secret via `crypto/rand`.
- Writes four SSM params under `/{prefix}config/h1/`:
  - `webhook-secret` — SecureString (HMAC key the bridge reads back; closes footgun #9 "every runtime read needs a write")
  - `api-username` — String (Basic-Auth username, not secret)
  - `api-token` — SecureString (Basic-Auth token)
  - `bridge-url` — String (may be empty before `km init` provides the Function URL)
- Prints the Function URL + minted secret + the HackerOne Webhooks UI paste path (Engagements → Program → Settings → Automation → Webhooks).
- Flags `--api-username` / `--api-token` / `--bridge-url` / `--force`, with interactive prompt fallback when creds are omitted.
- SSM client injected via the existing `SSMWriteAPI` seam for mock-driven tests.

### Task 2 — `RunH1Status` (same commit; tests in 887a4c72)
- Reads the four SSM params + `cfg.H1` (programs, targets, allow, events, commands, bot_handle, default_profile).
- REDACTS the webhook secret and api-token (`[set, redacted]`); prints api-username + bridge-url + program routing in full.
- Dormant-safe: when `h1:` is absent and SSM is empty, prints a clean "not configured (dormant)" message with no error.

### Wiring
- `NewH1Cmd` parent registered in `root.go` with `init` + `status` subcommands only — no manifest.

## Verification

- `go test ./internal/app/cmd -run "TestH1Init|TestH1Status" -count=1` — green (6 test funcs: init writes/random/print-URL/no-manifest, status redaction, status dormant).
- `go build ./...` + `go vet ./internal/app/cmd` — clean.
- `make build` (km v0.4.904) — `km h1 --help` shows `init` + `status`, no `manifest`.

## Deviations from Plan

None — plan executed exactly as written. The implementation for both tasks landed in one GREEN commit because the RED test file covered both `RunH1Init` and `RunH1Status` (the package would not compile until both existed); the TDD RED→GREEN boundary is preserved via the separate failing-test commit (887a4c72).

## Deferred Issues

- Untracked build artifact `km-h1` (12 MB) at the repo root, produced by Plan 05's `cmd/km-h1` build. Only `km` is gitignored, not `km-h1`. Out of this plan's scope (different package); logged to `deferred-items.md` — recommend adding `km-h1` to `.gitignore` in a Plan 05 follow-up or the deploy-wiring plan.

## Self-Check: PASSED

- FOUND: internal/app/cmd/h1.go
- FOUND: internal/app/cmd/h1_test.go
- FOUND: commit 887a4c72 (RED test)
- FOUND: commit c27df983 (GREEN impl)
