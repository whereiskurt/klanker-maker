---
phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events
plan: "05"
subsystem: infra
tags: [slack, ed25519, ssm, sidecar, go-binary, sandbox]

# Dependency graph
requires:
  - phase: 63-02
    provides: pkg/slack (BuildEnvelope, SignEnvelope, PostToBridge, MaxBodyBytes, BridgeBackoff)
provides:
  - cmd/km-slack binary — sandbox-side Slack notify client (post subcommand, SSM key load, Ed25519 sign, bridge POST, 40KB cap)
  - Makefile build-sidecars and sidecars targets updated for km-slack
  - internal/app/cmd/init.go buildAndUploadSidecars extended for km-slack
  - pkg/compiler/userdata.go sidecar download block includes km-slack
affects:
  - 63-04 (hook script calls /opt/km/bin/km-slack post when KM_NOTIFY_SLACK_ENABLED=1)
  - 63-06 (Lambda + Terraform creates the bridge URL km-slack posts to)
  - 63-08 (km create populates KM_SLACK_CHANNEL_ID and KM_SLACK_BRIDGE_URL in /etc/profile.d/km-notify-env.sh)
  - 63-10 (E2E verification end-to-end, operator guide update)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - runWith() inner function accepts injected key for testability — SSM bypass in unit tests (matches km-send pattern, same approach as pkg/aws identity tests)
    - TDD RED-GREEN: test file written first with undefined symbols, implementation written to pass
    - Three-site pipeline synchronization: Makefile + init.go + userdata.go must all agree on binary name and S3 key

key-files:
  created:
    - cmd/km-slack/main.go
    - cmd/km-slack/main_test.go
  modified:
    - Makefile
    - internal/app/cmd/init.go
    - pkg/compiler/userdata.go

key-decisions:
  - "runWith() inner function accepts (ctx, priv, sandboxID, bridgeURL, ...) so tests inject ephemeral Ed25519 keys and stub bridge URL — no SSM stub required"
  - "GOARCH=amd64 for km-slack matching existing EC2 sidecars (RESEARCH.md Pitfall 7) — Lambdas use arm64, EC2 sidecars use amd64"
  - "Signing key load: SSM /sandbox/{id}/signing-key, WithDecryption=true, base64-decoded, first 32 bytes as Ed25519 seed (same pattern as km-send)"
  - "Makefile build-sidecars uses -trimpath -ldflags '-s -w' for km-slack (stripped + reproducible) matching production sidecar convention"
  - "Three-site sync: Makefile sidecars target uploads s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-slack; init.go sidecarBuild list includes km-slack; userdata.go downloads /opt/km/bin/km-slack — all three must stay in sync"

patterns-established:
  - "New sandbox-side Go binary in cmd/ directory (first in codebase; km-send is bash)"
  - "Sidecar pipeline wiring pattern: any new sandbox-side binary added to Makefile + init.go + userdata.go as a trio"

requirements-completed: [SLCK-03]

# Metrics
duration: 162s
completed: 2026-04-30
---

# Phase 63 Plan 05: km-slack Binary and Sidecar Pipeline Wiring Summary

**Ed25519-signed sandbox-side Slack client (cmd/km-slack) wired into all three pipeline sites: Makefile cross-compile/upload, init.go buildAndUploadSidecars, and userdata.go EC2 bootstrap download**

## Performance

- **Duration:** 162s (~3 min)
- **Started:** 2026-04-30T01:24:38Z
- **Completed:** 2026-04-30T01:27:20Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments

- Built cmd/km-slack/main.go — single binary with `post` subcommand; reads KM_SANDBOX_ID, KM_SLACK_BRIDGE_URL, AWS_REGION from env; loads Ed25519 key from SSM /sandbox/{id}/signing-key; signs via pkg/slack.SignEnvelope; POSTs via pkg/slack.PostToBridge with retry and 40KB body cap
- 8 tests pass (happy path, oversized body exits before HTTP, missing env vars, stdin rejection, 401 fail-fast, 503+503+200 retry, signature verify roundtrip at stub server)
- Wired into all three pipeline sites: Makefile produces build/km-slack for linux/amd64; init.go uploads it to S3 on `km init --sidecars`; userdata.go downloads /opt/km/bin/km-slack on new sandbox provisioning

## Task Commits

Each task was committed atomically:

1. **Task 1: cmd/km-slack/main.go + main_test.go (TDD)** - `496b5d2` (feat)
2. **Task 2: Makefile + init.go + userdata.go pipeline wiring** - `cc3ef2f` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified

- `cmd/km-slack/main.go` — km-slack binary entry point; `main()` validates flags and invokes `run()`; `run()` loads env and SSM key then calls `runWith()`; `runWith()` is the testable inner function
- `cmd/km-slack/main_test.go` — 8 tests using httptest stub bridge and ephemeral ed25519 key; TestMain shrinks BridgeBackoff to ms-scale
- `Makefile` — build-sidecars target adds km-slack cross-compile step; sidecars target adds S3 upload for sidecars/km-slack
- `internal/app/cmd/init.go` — buildAndUploadSidecars sidecarBuild slice gains `{name: "km-slack", srcDir: "cmd/km-slack"}`
- `pkg/compiler/userdata.go` — sidecar download block adds `aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-slack" /opt/km/bin/km-slack`

## Decisions Made

- `runWith()` takes `(ctx, priv, sandboxID, bridgeURL, ...)` directly so tests inject ephemeral keys without localstack/SSM stubs — same testability pattern as pkg/aws identity tests
- GOARCH=amd64 locked for km-slack per RESEARCH.md Pitfall 7; Lambdas use arm64, EC2 sidecars use amd64; mismatch causes exec format error on EC2
- km-slack is the first sandbox-side Go binary in the codebase (km-send is bash) — establishes a new class of sidecar binary
- Stdin (`--body -`) rejected at main() before runWith() per CLAUDE.md / OpenSSL 3.5+ signing constraint

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

**Operator MUST run `km init --sidecars` once after this plan ships** to upload km-slack to S3 before any new sandbox using `notifySlackEnabled: true` can deliver Slack messages. Existing sandboxes do not get km-slack retroactively (per project memory: schema change → km init --sidecars). Manual deploy via SSM RunCommand is needed for existing sandboxes if required.

## Next Phase Readiness

- cmd/km-slack/main.go provides `/opt/km/bin/km-slack post` — this is what the Phase 63 hook script (Plan 04) calls when `KM_NOTIFY_SLACK_ENABLED=1`
- Next dependencies:
  - 63-06 (Lambda + Terraform): creates the bridge URL endpoint that km-slack posts to
  - 63-08 (km create): populates KM_SLACK_CHANNEL_ID and KM_SLACK_BRIDGE_URL in /etc/profile.d/km-notify-env.sh post-launch
  - 63-10 (E2E): end-to-end verification; operator guide update documenting `km init --sidecars` requirement

---
*Phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events*
*Completed: 2026-04-30*
