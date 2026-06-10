---
phase: 103-hackerone-comment-trigger-bridge
plan: 07
subsystem: infra
tags: [hackerone, lambda, function-url, terraform, iam, hmac, base64, port]

# Dependency graph
requires:
  - phase: 103-hackerone-comment-trigger-bridge
    plan: 04
    provides: "WebhookHandler.Handle() flow + all real AWS adapters (DynamoH1NonceStore, DynamoAliasResolver(+WithStatus), DynamoH1ThreadStore, DynamoSandboxStatusWriter, H1SQSAdapter, EventBridgeAdapter, EC2Resumer, SSMSecretFetcher, SSMCommandsFetcher, H1APICommenter), ProgramEntry/Target/CommandEntry, VerifyH1Signature"
  - phase: 103-hackerone-comment-trigger-bridge
    plan: 06
    provides: "cmd/km-h1 sandbox helper (consumes the dispatched H1Envelope from the inbound queue)"
provides:
  - "cmd/km-h1-bridge/main.go — Lambda Function URL entrypoint: base64-decode-then-HMAC (Pitfall 1), lowercase headers, KM_H1_PROGRAMS list-of-objects parse, SSM cold-start reads of /{prefix}/config/h1/* (webhook secret + Basic-Auth creds + command set), real-adapter wiring of pkg/h1/bridge.WebhookHandler, 200-on-internal-error (Pitfall 2)"
  - "infra/modules/lambda-h1-bridge/v1.0.0 — Lambda + Function URL (auth NONE) + CloudWatch log group + IAM grants matched 1:1 to the runtime AWS calls (nonces PutItem, sandboxes Query(alias-index)/GetItem/UpdateItem, h1-threads RW gated, h1-inbound-*.fifo SendMessage, EventBridge PutEvents, EC2 Describe+StartInstances, SSM read /config/h1/* + kms:Decrypt)"
  - "infra/live/use1/lambda-h1-bridge/terragrunt.hcl — live unit sourcing v1.0.0 with dependencies on dynamodb-sandboxes, dynamodb-slack-nonces (shared), dynamodb-h1-threads (Plan 08), each with 'show' in mock_outputs_allowed_terraform_commands"
affects: [103-08-init-wiring, 103-10-uat]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Lambda Function URL base64-decode-then-HMAC: decode the body in handle() BEFORE constructing WebhookRequest so VerifyH1Signature keys on the DECODED bytes (Pitfall 1); malformed base64 -> 400, every other internal error -> 200 (Pitfall 2, no fresh-GUID redelivery)"

key-files:
  created:
    - cmd/km-h1-bridge/main.go
    - cmd/km-h1-bridge/main_test.go
    - infra/modules/lambda-h1-bridge/v1.0.0/main.tf
    - infra/modules/lambda-h1-bridge/v1.0.0/variables.tf
    - infra/modules/lambda-h1-bridge/v1.0.0/outputs.tf
    - infra/live/use1/lambda-h1-bridge/terragrunt.hcl
  modified: []

key-decisions:
  - "Forked cmd/km-github-bridge/main.go but DROPPED the GitHub App machinery entirely: no readAppCredentials, no JWT installation reactor, no relayer, no orphan router. HackerOne's customer API is HTTP Basic Auth (no App-install model) and each program webhook points at one install's Function URL (federation out of scope)."
  - "The bridge holds the HackerOne Basic-Auth identity (api_username/api_token from SSM) ONLY for the loop-guard (drop its own internal ack) and the synchronous INTERNAL 'on it' comment; researcher-visible replies come from cmd/km-h1 in the sandbox, never this Lambda. On SSM cred-fetch failure the bridge degrades (no loop-guard, no internal ack) but still dispatches — preserving the 200-on-internal-error contract."
  - "h1-threads IAM grant is gated on a non-empty h1_threads_table_arn (count=...) so the module applies cleanly before Plan 08 provisions the table; the live unit feeds the ARN via mock_outputs today (dynamodb-h1-threads unit lands in Plan 08)."
  - "Function URL auth NONE (app-layer HMAC + nonce); CORS + the public lambda:InvokeFunctionUrl resource policy ported verbatim from the github module. replace_triggered_by on the IAM role preserved to avoid stale KMS grants."

patterns-established:
  - "IAM<->runtime cross-check comment block at the top of main.tf enumerating every runtime AWS call against its grant (an init_test guards its presence — ported from the github module)."

requirements-completed: [H1-BRIDGE-HMAC, H1-DISPATCH-3WAY, H1-DEPLOY-WIRING]

# Metrics
duration: 6min
completed: 2026-06-10
---

# Phase 103 Plan 07: km-h1-bridge Lambda entry + Terraform module Summary

**The km-h1-bridge Lambda made real: a Function URL entrypoint that base64-decodes the body before HMAC-verifying it (Pitfall 1), wires the Plan-04 bridge package to live AWS adapters, returns 200 on every internal error (Pitfall 2), plus a forked lambda-h1-bridge/v1.0.0 Terraform module whose IAM grants are matched 1:1 to the runtime calls — App/JWT/relayer/router stripped, h1-threads + h1-inbound + SSM /config/h1/* grants added.**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-06-10T04:27:19Z
- **Completed:** 2026-06-10T04:32:49Z
- **Tasks:** 2 (both auto)
- **Files created:** 6 (2 Go, 4 Terraform)

## Accomplishments
- `cmd/km-h1-bridge/main.go`: forked the GitHub bridge entry, dropped all GitHub App machinery (App creds, JWT reactor, relayer, orphan router), and wired the eight Plan-04 H1 adapters into `pkg/h1/bridge.WebhookHandler`. Reads the webhook secret, Basic-Auth creds, and command set from SSM `/{prefix}/config/h1/*` at cold start; parses the `KM_H1_PROGRAMS` list-of-objects env. Pitfall 1 (decode-then-verify) and Pitfall 2 (200-on-internal-error) ported verbatim.
- `cmd/km-h1-bridge/main_test.go`: 6 tests covering decode-then-verify reaching `Handle` + dispatching, bad-signature 401 (decode does NOT bypass verify), internal-error→200, malformed-base64→400, and header lowercasing.
- `infra/modules/lambda-h1-bridge/v1.0.0/{main,variables,outputs}.tf`: Function URL auth NONE; IAM grants enumerated against the runtime calls (nonces PutItem, sandboxes Query(alias-index)/GetItem/UpdateItem, h1-threads RW gated, h1-inbound-*.fifo SendMessage, EventBridge PutEvents, EC2 Describe+StartInstances scoped to `km:resource-prefix`, SSM read `/config/h1/*` + `kms:Decrypt`). No `required_providers` (root.hcl owns it).
- `infra/live/use1/lambda-h1-bridge/terragrunt.hcl`: sources v1.0.0; declares deps on `dynamodb-sandboxes`, `dynamodb-slack-nonces` (shared), and `dynamodb-h1-threads` (Plan 08), each with `show` in `mock_outputs_allowed_terraform_commands`.

## Task Commits

1. **Task 1: cmd/km-h1-bridge/main.go — Lambda entry + base64/header normalization** — `459b0972` (feat)
2. **Task 2: lambda-h1-bridge TF module v1.0.0 + live unit** — `045b3833` (feat)

**Plan metadata:** _(this commit)_

## Files Created/Modified
- `cmd/km-h1-bridge/main.go` — Lambda Function URL handler: cold-start adapter wiring, SSM reads, base64-decode-then-HMAC, 200-on-internal-error.
- `cmd/km-h1-bridge/main_test.go` — entry-point tests (decode-then-verify, bad-sig 401, internal-error 200, bad-base64 400, header lowercase).
- `infra/modules/lambda-h1-bridge/v1.0.0/main.tf` — IAM role + 9 policies + Lambda + Function URL (auth NONE) + log group + public invoke permission.
- `infra/modules/lambda-h1-bridge/v1.0.0/variables.tf` — module inputs (KM_H1_* env, SSM paths, table ARNs, gated h1_threads_table_arn).
- `infra/modules/lambda-h1-bridge/v1.0.0/outputs.tf` — function_name / function_arn / function_url / lambda_role_arn.
- `infra/live/use1/lambda-h1-bridge/terragrunt.hcl` — live unit wiring the module with dependency outputs.

## Decisions Made
- Dropped GitHub App machinery wholesale (Basic Auth replaces App-JWT; per-program webhook URLs replace one-App-many-installs relay).
- Bridge Basic-Auth creds serve only the loop-guard + internal ACK; degrade-not-fail on missing SSM creds.
- h1-threads IAM grant is `count`-gated on a non-empty ARN so the module is apply-clean before Plan 08 ships the table.

## Deviations from Plan

None - plan executed exactly as written.

The plan's two-task decomposition (Lambda entry + tests, then TF module + live unit) was followed verbatim. The only mechanical adjustment was running `terraform fmt` to align the env-block `=` columns (whitespace only; no semantic change).

**Total deviations:** 0.
**Impact on plan:** None — both verification commands (`go test ./cmd/km-h1-bridge && go build`, `terraform validate`) pass as specified.

## Issues Encountered
None. `terraform validate` emits only the codebase-wide `data.aws_region.current.name` deprecation warning (identical to the github module it forks); not in scope to change here.

## User Setup Required
None - no external service configuration required at this plan. (Operator-side `km h1 init` to mint the webhook secret + cache Basic-Auth creds, and the HackerOne Sandbox program webhook URL paste, are Plan 08 / Wave 6.)

## Next Phase Readiness
- **Plan 08 (init.go wiring):** Add `lambda-h1-bridge` to `regionalModules()` and a `cmd/km-h1-bridge` entry to `lambdaBuilds()`; export the `KM_H1_*` env block; provision the `dynamodb-h1-threads` table module + live unit; harden the `{prefix}-h1-inbound-*.fifo` queues with the shared DLQ. Per memory `project_make_build_precedes_km_init`, `make build` the km binary BEFORE `km init` once the `regionalModules()` entry lands. The h1-threads live-unit dependency referenced here resolves via mock today; Plan 08 makes it concrete.
- **Wave 6 (UAT):** the operator's HackerOne Sandbox account is the only live target; a real webhook delivery re-pins the envelope headers + state endpoint.
- No blockers. Build green, module validates, both task commits present.

## Self-Check: PASSED

All 6 created files exist on disk; both task commits (`459b0972`, `045b3833`) present in git history. `go test ./cmd/km-h1-bridge -count=1` green + `go build ./cmd/km-h1-bridge` clean; `terraform validate` passes in the module dir (only the codebase-wide region-name deprecation warning).

---
*Phase: 103-hackerone-comment-trigger-bridge*
*Completed: 2026-06-10*
