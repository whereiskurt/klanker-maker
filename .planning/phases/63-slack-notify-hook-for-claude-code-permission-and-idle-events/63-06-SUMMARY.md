---
phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events
plan: "06"
subsystem: infra
tags: [slack, lambda, dynamodb, terraform, terragrunt, ed25519, function-url]

requires:
  - phase: 63-03
    provides: "bridge.Handler + 5 injectable interfaces (PublicKeyFetcher, NonceStore, ChannelOwnershipFetcher, BotTokenFetcher, SlackPoster)"

provides:
  - "cmd/km-slack-bridge Lambda entry point wiring production AWS adapters"
  - "pkg/slack/bridge/aws_adapters.go: 5 production adapter implementations"
  - "infra/modules/lambda-slack-bridge/v1.0.0: Lambda + Function URL Terraform module"
  - "infra/modules/dynamodb-slack-nonces/v1.0.0: DynamoDB nonce table module"
  - "infra/live/use1/lambda-slack-bridge + dynamodb-slack-nonces Terragrunt live configs"
  - "SandboxMetadata.SlackChannelID + SlackPerSandbox fields for Plans 08/09"
  - "km init integration: regionalModules() + buildLambdaZips() include slack bridge"

affects: [63-07, 63-08, 63-09, km-init, sandbox-metadata]

tech-stack:
  added: []
  patterns:
    - "SlackPosterAdapter uses direct HTTP (not pkg/slack.Client) to expose Retry-After headers from 429 responses"
    - "SSMBotTokenFetcher: 15-min in-process cache via sync.Mutex-protected struct with configurable CacheTTL for tests"
    - "identityTableAdapter bridge struct satisfies kmaws.IdentityTableAPI by wrapping DynamoGetPutter"
    - "unmarshalSlackFields: separate helper reads Phase 63 DynamoDB attrs from raw item map after toSandboxMetadata()"
    - "Lambda Function URL first in codebase: auth=NONE + Ed25519/nonce = application-layer auth"

key-files:
  created:
    - cmd/km-slack-bridge/main.go
    - cmd/km-slack-bridge/main_test.go
    - pkg/slack/bridge/aws_adapters.go
    - pkg/slack/bridge/aws_adapters_test.go
    - infra/modules/lambda-slack-bridge/v1.0.0/main.tf
    - infra/modules/lambda-slack-bridge/v1.0.0/variables.tf
    - infra/modules/lambda-slack-bridge/v1.0.0/outputs.tf
    - infra/modules/dynamodb-slack-nonces/v1.0.0/main.tf
    - infra/modules/dynamodb-slack-nonces/v1.0.0/variables.tf
    - infra/modules/dynamodb-slack-nonces/v1.0.0/outputs.tf
    - infra/live/use1/dynamodb-slack-nonces/terragrunt.hcl
    - infra/live/use1/lambda-slack-bridge/terragrunt.hcl
  modified:
    - pkg/aws/metadata.go
    - pkg/aws/sandbox_dynamo.go
    - pkg/aws/sandbox_dynamo_test.go
    - internal/app/cmd/init.go
    - internal/app/cmd/init_test.go

key-decisions:
  - "SlackPosterAdapter does its own thin HTTP (Option B) rather than extending pkg/slack.Client — keeps Client API stable, exposes Retry-After header cleanly for 429→ErrSlackRateLimited"
  - "identityTableAdapter bridge struct satisfies kmaws.IdentityTableAPI wrapping DynamoGetPutter — avoids leaking full *dynamodb.Client into bridge package while reusing FetchPublicKey"
  - "unmarshalSlackFields() separate helper (not in sandboxItemDynamo struct) — avoids schema migration of internal struct; new fields read directly from raw item map"
  - "Lambda Function URL: authorization_type=NONE — Ed25519 signature + nonce provide application-layer auth (no IAM auth needed at HTTP layer)"
  - "replace_triggered_by = [aws_iam_role.slack_bridge] per CLAUDE.md memory — prevents stale KMS grants on IAM role recreation"
  - "DynamoPublicKeyFetcher uses DynamoDB km-identities NOT SSM — RESEARCH.md correction #1 enforced at every layer (adapter, IAM policy, comments)"
  - "Schema additions to SandboxMetadata: operator must run km init --sidecars after this lands so management Lambdas pick up SlackChannelID/SlackPerSandbox schema change (CLAUDE.md memory: project_schema_change_requires_km_init)"

requirements-completed: [SLCK-04]

duration: 7min
completed: 2026-04-30
---

# Phase 63 Plan 06: Production Bridge Wiring Summary

**km-slack-bridge Lambda wired end-to-end: DynamoDB-backed Ed25519 key lookup, conditional nonce store, SSM bot token with 15-min cache, direct-HTTP Slack poster surfacing 429/Retry-After — plus first Lambda Function URL in this codebase**

## Performance

- **Duration:** 7 min
- **Started:** 2026-04-30T01:25:03Z
- **Completed:** 2026-04-30T01:32:31Z
- **Tasks:** 2
- **Files modified:** 15 (7 created new, 5 modified existing + 3 new infra)

## Accomplishments

- Built all 5 production AWS adapters with tests covering happy path + one error path each
- Created first Lambda Function URL in the codebase with auth=NONE + application-layer Ed25519/nonce auth
- Extended SandboxMetadata with SlackChannelID and SlackPerSandbox for Plans 08/09
- Integrated km-slack-bridge into km init (regionalModules + buildLambdaZips)

## Task Commits

1. **Task 1: Lambda entry point + 5 adapters + metadata fields** - `248962a` (feat)
2. **Task 2: Terraform modules + live Terragrunt + init integration** - `0323a28` (feat)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/cmd/km-slack-bridge/main.go` — Lambda entry point wiring 5 production adapters
- `/Users/khundeck/working/klankrmkr/cmd/km-slack-bridge/main_test.go` — Lambda handler tests with stub adapters
- `/Users/khundeck/working/klankrmkr/pkg/slack/bridge/aws_adapters.go` — 5 production adapters (DynamoPublicKeyFetcher, DynamoNonceStore, DynamoChannelOwnershipFetcher, SSMBotTokenFetcher, SlackPosterAdapter)
- `/Users/khundeck/working/klankrmkr/pkg/slack/bridge/aws_adapters_test.go` — adapter tests (happy + error paths, 429→ErrSlackRateLimited, cache expiry)
- `/Users/khundeck/working/klankrmkr/pkg/aws/metadata.go` — SandboxMetadata gains SlackChannelID + SlackPerSandbox
- `/Users/khundeck/working/klankrmkr/pkg/aws/sandbox_dynamo.go` — marshalSandboxItem + unmarshalSlackFields for new fields
- `/Users/khundeck/working/klankrmkr/pkg/aws/sandbox_dynamo_test.go` — round-trip tests for Slack fields
- `/Users/khundeck/working/klankrmkr/infra/modules/lambda-slack-bridge/v1.0.0/main.tf` — Lambda + Function URL + IAM + replace_triggered_by
- `/Users/khundeck/working/klankrmkr/infra/modules/dynamodb-slack-nonces/v1.0.0/main.tf` — PAY_PER_REQUEST + TTL on ttl_expiry
- `/Users/khundeck/working/klankrmkr/infra/live/use1/lambda-slack-bridge/terragrunt.hcl` — live wiring with dependency on identities/sandboxes/nonces tables
- `/Users/khundeck/working/klankrmkr/infra/live/use1/dynamodb-slack-nonces/terragrunt.hcl` — live nonce table
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/init.go` — regionalModules() + buildLambdaZips() include slack bridge
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/init_test.go` — TestRegionalModulesIncludesSlackBridge

## Decisions Made

- **SlackPosterAdapter: Option B (direct HTTP)** — adapter does its own thin HTTP calls (not via pkg/slack.Client) so it can read Retry-After headers from Slack 429 responses without adding a back-channel API to the existing Client
- **identityTableAdapter bridge struct** — wraps DynamoGetPutter to satisfy kmaws.IdentityTableAPI so FetchPublicKey can be reused without importing *dynamodb.Client into bridge package
- **unmarshalSlackFields() separate helper** — reads Phase 63 DynamoDB attrs from raw item map after toSandboxMetadata() rather than adding fields to sandboxItemDynamo struct, avoiding struct schema migration
- **First Lambda Function URL**: authorization_type=NONE — application-layer Ed25519/nonce is the auth mechanism; no IAM needed at HTTP ingress
- **replace_triggered_by per CLAUDE.md memory** — prevents stale aws/lambda KMS grants when IAM role is recreated

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

- stub public key fetcher in main_test.go initially returned `[]byte` instead of `ed25519.PublicKey` — auto-fixed during GREEN phase (type alias; Rule 1 bug fix inline)

## User Setup Required

Schema additions to SandboxMetadata (SlackChannelID, SlackPerSandbox) require operator to run `km init --sidecars` after this plan lands so management Lambdas pick up the updated toolchain before remote creates can write/read the new fields. (CLAUDE.md memory: project_schema_change_requires_km_init)

## Next Phase Readiness

- Bridge Lambda fully wired: Go entry point + production AWS adapters + Terraform module + live Terragrunt + km init integration
- 63-07 (`km slack init`) reads the `function_url` Terraform output and stores it at SSM `/km/slack/bridge-url`
- Plans 08 (km create) and 09 (km destroy) can read `SandboxMetadata.SlackChannelID` and `SlackPerSandbox`
- Operator running `km init` after this plan ships will: build km-slack-bridge.zip, deploy dynamodb-slack-nonces and lambda-slack-bridge to use1, and produce the Function URL output

---
*Phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events*
*Completed: 2026-04-30*
