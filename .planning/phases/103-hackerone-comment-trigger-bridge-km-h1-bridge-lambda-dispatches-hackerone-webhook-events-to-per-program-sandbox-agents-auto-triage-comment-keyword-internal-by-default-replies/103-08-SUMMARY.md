---
phase: 103-hackerone-comment-trigger-bridge
plan: 08
subsystem: deploy-wiring
tags: [hackerone, init, regional-modules, lambda-zip, sidecar, dynamodb, sqs, dlq, ssm, terragrunt, deploy-surface]

# Dependency graph
requires:
  - phase: 103-hackerone-comment-trigger-bridge
    plan: 07
    provides: "lambda-h1-bridge TF module v1.0.0 + live unit (deps on dynamodb-sandboxes, dynamodb-slack-nonces, dynamodb-h1-threads via mock until this plan); km-h1-bridge main.go reading KM_H1_PROGRAMS / KM_H1_DEFAULT_PROFILE / KM_H1_BOT_HANDLE / SSM /config/h1/commands"
  - phase: 103-hackerone-comment-trigger-bridge
    plan: 02
    provides: "config.H1Config / H1ProgramEntry / H1Target / H1CommandEntry structs + v2→v merge-list entry (UnmarshalKey \"h1\")"
provides:
  - "dynamodb-h1-threads/v1.0.0 module (PK=report_id S, SK=target S, TTL ttl_expiry N, PAY_PER_REQUEST, agent_type schema-on-write) + infra/live/use1/dynamodb-h1-threads live unit — makes the lambda-h1-bridge live-unit h1_threads dependency concrete (was mock in Plan 07)"
  - "init.go regionalModules(): dynamodb-h1-threads (before bridge) + lambda-h1-bridge (after lambda-github-bridge + shared deps, before ses)"
  - "init.go lambdaBuilds() km-h1-bridge + sidecarBuilds() km-h1 (artifact lockstep); Makefile build-lambdas builds km-h1-bridge.zip"
  - "ExportTerragruntEnvVars: KM_H1_PROGRAMS (JSON envelope) + KM_H1_DEFAULT_PROFILE + KM_H1_BOT_HANDLE (env-wins drift WARN)"
  - "PublishH1CommandsToSSM: merged install-wide CommandSet (base64) at /{prefix}/config/h1/commands; PreStageH1Profiles to h1-profiles/{slug}/.km-profile.yaml"
  - "pkg/aws/sqs.go: H1InboundQueueName / CreateH1InboundQueue / DeleteH1InboundQueue / H1InboundDLQName (1800s VisibilityTimeout + DLQ RedrivePolicy maxReceiveCount=3)"
  - "create_h1_inbound.go: provisionH1InboundQueue + rollbackH1InboundQueue (poison-wedge protection)"
affects: [103-09-sandbox-poller, 103-10-uat]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "H1 inbound FIFO uses a 1800s VisibilityTimeout (h1InboundQueueAttrs), NOT the 30s of the Slack/GitHub inbound queues — a HackerOne triage turn runs far longer than a Slack reply; too-short timeout re-delivers mid-turn → duplicate-review loops (Phase 97 failure mode). Poison-wedge DLQ + RedrivePolicy(maxReceiveCount=3) shared with the Slack/GitHub pattern (project_inbound_poller_fifo_poison_wedge)."
    - "H1 commands are declared per-program (h1.programs[].commands) but the bridge consumes ONE install-wide map — PublishH1CommandsToSSM MERGES every program's commands (last-program-wins + WARN on name collision) into the GitHub-identical base64 CommandSet envelope {commands, default_command}."

key-files:
  created:
    - infra/modules/dynamodb-h1-threads/v1.0.0/main.tf
    - infra/modules/dynamodb-h1-threads/v1.0.0/variables.tf
    - infra/modules/dynamodb-h1-threads/v1.0.0/outputs.tf
    - infra/live/use1/dynamodb-h1-threads/terragrunt.hcl
    - internal/app/cmd/create_h1_inbound.go
  modified:
    - internal/app/cmd/init.go
    - internal/app/cmd/init_test.go
    - pkg/aws/sqs.go
    - Makefile

key-decisions:
  - "report_id is type S (String), NOT N — HackerOne report IDs are opaque identifiers carried as JSON strings in the webhook/customer-API payloads; keeping S avoids any N-coercion risk. Differs from dynamodb-github-threads where number is N (PR numbers are genuine integers)."
  - "Sort key is `target` (the fanout alias / h1-{handle}), so one report fanned to N targets occupies N distinct (report_id, target) rows — N targets never collide on a single report (Phase 103 multi-target dispatch)."
  - "PublishH1CommandsToSSM merges per-program commands into one install-wide map because the bridge (Plan 07) reads a single install-wide WebhookHandler.Commands + DefaultCommand — last-program-wins with a WARN on a duplicate command name; default_command is the first non-empty per-program DefaultCommand."
  - "notificationH1Inbound is a forward-compat stub returning nil (dormant) — the notification.h1.inbound schema field is owned by Plan 09 (it ships the km-h1-inbound-poller userdata + the dormancy golden). Plan 09 repoints the stub at p.Spec.Notification.H1.Inbound AND wires the create.go call site, alongside the gate field it owns. Keeps Plan 08 build-green in isolation."
  - "km-h1 sidecar follows the km-github precedent (built/uploaded via sidecarBuilds() in init.go, NOT the Makefile sidecars target); only the lambda zip (km-h1-bridge) is added to Makefile build-lambdas, matching km-github-bridge exactly."

patterns-established:
  - "TestRegionalModulesIncludesH1Bridge asserts both membership AND the full ordering chain (threads<bridge, github-bridge<bridge, shared-deps<bridge, bridge<ses) — the focused guard for this plan; the comprehensive cross-list membership guards are Plan 10."

requirements-completed: [H1-DEPLOY-WIRING, H1-DISPATCH-3WAY, H1-THREAD-CONTINUITY]

# Metrics
duration: 19min
completed: 2026-06-10
---

# Phase 103 Plan 08: H1 bridge deploy wiring Summary

**The highest-risk deploy-surface plan, closed against six memory footguns: lambda-h1-bridge + dynamodb-h1-threads are now in regionalModules() (correctly ordered), km-h1-bridge is in lambdaBuilds()/Makefile and km-h1 in sidecarBuilds(), KM_H1_* env vars + the merged H1 CommandSet flow to terragrunt/SSM, and the new {prefix}-h1-inbound-*.fifo queues carry the shared DLQ + RedrivePolicy with a 1800s visibility timeout — so a correct bridge actually deploys instead of being silently skipped.**

## Performance

- **Duration:** ~19 min
- **Started:** 2026-06-10T04:37:34Z
- **Completed:** 2026-06-10T04:56:06Z
- **Tasks:** 2 (both auto)
- **Files created:** 5 (4 Terraform/HCL, 1 Go); **modified:** 4

## Accomplishments

### Task 1 — dynamodb-h1-threads module + live unit; create_h1_inbound + SQS DLQ helper (`c1591922`)
- `infra/modules/dynamodb-h1-threads/v1.0.0/{main,variables,outputs}.tf`: forked dynamodb-github-threads. PK=`report_id` (S), SK=`target` (S), TTL `ttl_expiry` (N), PAY_PER_REQUEST, SSE on, no `required_providers`. `agent_type` is schema-on-write (no attribute declaration → no TF migration), mirroring the GitHub hangover.
- `infra/live/use1/dynamodb-h1-threads/terragrunt.hcl`: sources v1.0.0, `{prefix}-h1-threads`. This makes the Plan-07 lambda-h1-bridge live-unit `h1_threads` dependency concrete (it resolved via `mock_outputs` until now).
- `pkg/aws/sqs.go`: added `H1InboundDLQName`, `H1InboundQueueName`, `CreateH1InboundQueue`, `DeleteH1InboundQueue`, and a dedicated `h1InboundQueueAttrs` builder. The H1 attrs override `VisibilityTimeout` to **1800s** (`h1InboundVisibilityTimeout`) — deliberately NOT the 30s of the Slack/GitHub inbound queues — so a long triage turn is not re-delivered mid-flight (Phase 97 dup-review loops). RedrivePolicy(maxReceiveCount=3) attached when `dlqARN` is non-empty (dormancy when empty).
- `internal/app/cmd/create_h1_inbound.go`: forked create_github_inbound.go → `provisionH1InboundQueue` (DLQArn threaded; persists `h1_inbound_queue_url` to km-sandboxes; publishes `/{prefix}/sandbox/{id}/h1-inbound-queue-url` to SSM) + `rollbackH1InboundQueue`. `notificationH1Inbound` is a forward-compat stub (Plan 09 wiring point).

### Task 2 — init.go wiring + Makefile (`48bfd1e8`)
- `regionalModules()`: `dynamodb-h1-threads` inserted **before** `lambda-h1-bridge`; `lambda-h1-bridge` inserted **after** `lambda-github-bridge` (and after `dynamodb-sandboxes` / `dynamodb-slack-nonces`), **before** `ses`. `defaultModuleTimeout` adds lambda-h1-bridge to the 5-min lambda group.
- `lambdaBuilds()`: `{name:"km-h1-bridge", srcDir:"cmd/km-h1-bridge"}`. `sidecarBuilds()`: `{name:"km-h1", srcDir:"cmd/km-h1"}`.
- `ExportTerragruntEnvVars`: exports `KM_H1_PROGRAMS` (JSON `{programs, default_profile, bot_handle}` envelope matching the bridge's `programsConfig`), plus scalar `KM_H1_DEFAULT_PROFILE` / `KM_H1_BOT_HANDLE` for the terragrunt-default path — all with the env-wins drift-WARN pattern. Gated on `len(cfg.H1.Programs) > 0` (dormant otherwise).
- `PublishH1CommandsToSSM` (+ wired into `runInit`): merges every program's commands into one base64 CommandSet at `/{prefix}/config/h1/commands` (the format the Plan-07 `SSMCommandsFetcher` decodes), with @file prompt inlining and drift WARN.
- `PreStageH1Profiles` + `h1ProgramConfigsFromCfg`: H1 analog of `PreStageGitHubProfiles`, staging `h1-profiles/{slug}/.km-profile.yaml` (exported test-seam, same un-wired-in-runInit posture as the GitHub helper).
- `Makefile build-lambdas`: builds `km-h1-bridge.zip` (artifact lockstep with lambdaBuilds()).
- `init_test.go`: `TestRegionalModulesIncludesH1Bridge` (membership + full ordering chain) and `TestH1BridgeBuildListMembership` (lambdaBuilds/sidecarBuilds membership).

## Deploy note (memory project_make_build_precedes_km_init)

This plan adds a `regionalModules()` entry, so the operator MUST **`make build` the km binary BEFORE `km init`** — a stale km silently skips dynamodb-h1-threads + lambda-h1-bridge and bakes mock dependency ARNs (account `000000000000`) into dependents. `KM_H1_*` are env-block changes ⇒ deploy with **`km init --dry-run=false`**, NOT `--sidecars` (`--sidecars` rebuilds zips + cold-starts but does not update the Lambda env block). Full deploy sequence: `make build && make build-lambdas && km init --dry-run=false` (the Plan-10 runbook covers the live HackerOne Sandbox UAT). No production HackerOne program is a target — only the operator's HackerOne Sandbox account.

## Deviations from Plan

### Auto-resolved scope/ordering decisions (no user input needed)

**1. [Rule 3 - Dependency ordering] notification.h1.inbound gate field deferred to Plan 09**
- **Found during:** Task 1.
- **Issue:** The plan's Task 1 forks create_github_inbound.go (which gates on `notificationGitHubInbound(profile).Enabled`), but the `notification.h1.inbound` profile schema field is owned by **Plan 09** (Wave 5) — it does not exist yet.
- **Resolution:** `provisionH1InboundQueue` / `rollbackH1InboundQueue` are fully implemented; the gate accessor `notificationH1Inbound` is a documented forward-compat stub returning nil (dormant). Plan 09 repoints it at `p.Spec.Notification.H1.Inbound` and wires the create.go call site alongside the gate field it births. This keeps Plan 08 build-green in isolation and respects the plan's file boundary (create.go is NOT in this plan's files_modified).
- **Files:** internal/app/cmd/create_h1_inbound.go.
- **Commit:** c1591922.

**2. [Scope] Makefile adds only the lambda zip, not a km-h1 sidecar step**
- The km-github sidecar is built/uploaded exclusively via init.go `sidecarBuilds()`, NOT the Makefile `sidecars` target. km-h1 follows that exact precedent (added to `sidecarBuilds()`); only `km-h1-bridge.zip` is added to Makefile `build-lambdas`, matching km-github-bridge 1:1. The plan's "Makefile: add km-h1-bridge + km-h1" is honored as "the lambda zip in the Makefile; the sidecar via the same init.go path km-github uses."

**Total deviations:** 2 (both auto-resolved, zero architectural).
**Impact on plan:** None — both verification commands (`go build ./... && make build`; `terraform validate` for dynamodb-h1-threads) pass as specified.

## Issues Encountered / Deferred Issues

- `go test ./internal/app/cmd/` reports ONE failure: `TestUnlockCmd_RequiresStateBucket`. This is a **pre-existing, environmental** failure — the test exercises a real DynamoDB UpdateItem and fails on an expired AWS SSO token (`InvalidGrantException` / `refresh SSO token failed`), NOT on any code path touched by this plan (it is in unlock.go, not init/sqs/h1). Out of scope per the deviation scope boundary; logged here, not fixed. `pkg/aws/...` and all H1/init/prestage/commands tests pass.

## Self-Check: PASSED

- Created files all exist on disk (5: 3 module .tf, 1 live .hcl, create_h1_inbound.go).
- Commits `c1591922` + `48bfd1e8` present in git history.
- `go build ./...` clean; `make build` succeeds (km v0.4.905 — picks up the new regionalModules entry); `terraform validate` green for dynamodb-h1-threads.
- H1 entries confirmed in regionalModules() (dynamodb-h1-threads + lambda-h1-bridge), lambdaBuilds() (km-h1-bridge), sidecarBuilds() (km-h1); km-h1-bridge cross-compiles for linux/arm64; `TestRegionalModulesIncludesH1Bridge` + `TestH1BridgeBuildListMembership` pass.

---
*Phase: 103-hackerone-comment-trigger-bridge*
*Completed: 2026-06-10*
