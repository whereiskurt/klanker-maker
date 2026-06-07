---
phase: 97-github-comment-trigger-mvp
plan: 04
subsystem: github-bridge-lambda
tags: [github, lambda, bridge, webhook, hmac, sqs, eventbridge, terraform]
dependency_graph:
  requires: [97-01, 97-02, 97-03]
  provides: [km-github-bridge Lambda, lambda-github-bridge TF module, pkg/github/bridge]
  affects: [infra/modules/lambda-github-bridge, cmd/km-github-bridge, pkg/github/bridge]
tech_stack:
  added: [pkg/github/bridge, cmd/km-github-bridge, infra/modules/lambda-github-bridge/v1.0.0]
  patterns: [HMAC-SHA256 verify, exact-before-glob resolution, 11-step Handle ordering, 200-on-error, synchronous reaction, JSON env var serialization]
key_files:
  created:
    - pkg/github/bridge/payload.go
    - pkg/github/bridge/resolve.go
    - pkg/github/bridge/resolve_test.go
    - pkg/github/bridge/interfaces.go
    - pkg/github/bridge/webhook_handler.go
    - pkg/github/bridge/webhook_handler_test.go
    - pkg/github/bridge/aws_adapters.go
    - pkg/github/bridge/handle_test.go
    - cmd/km-github-bridge/main.go
    - infra/modules/lambda-github-bridge/v1.0.0/main.tf
    - infra/modules/lambda-github-bridge/v1.0.0/variables.tf
    - infra/modules/lambda-github-bridge/v1.0.0/outputs.tf
  modified:
    - Makefile
decisions:
  - "Exact-before-glob resolution in Resolve(): exact matches are collected in pass 1, globs in pass 2; this ensures order-independence for exact entries regardless of YAML declaration order"
  - "InstallationReactor mints App JWT + installation token per-invocation (no token cache); acceptable because Handle() is one comment at a time and the JWT mint is <1ms"
  - "EventBridgeAdapter serializes sandboxCreateDetail locally (mirrors pkg/aws.SandboxCreateDetail) to avoid import cycle between bridge and pkg/aws; shape stays byte-identical"
  - "KM_GITHUB_REPOS is a JSON string (not multiple env vars) per Pitfall 2: list-of-objects cannot be scalar env vars; shape {repos:[...], default_profile} matches Lambda TF module variable"
  - "200-on-internal-error invariant strictly enforced throughout Handle(); 5xx would cause GitHub to redeliver with a NEW X-GitHub-Delivery GUID bypassing dedup"
  - "Reaction is posted SYNCHRONOUSLY (Pitfall 3): Lambda runtime freezes when Handle() returns; goroutine reaction would time out during freeze"
metrics:
  duration: "605s (10m 5s)"
  completed_date: "2026-06-06"
  tasks_completed: 3
  files_created: 12
  files_modified: 1
---

# Phase 97 Plan 04: km-github-bridge Lambda Summary

**One-liner:** GitHub App inbound bridge Lambda with HMAC-SHA256 verify, exact-before-glob repo resolution, warm-enqueue/cold-SandboxCreate dispatch, and synchronous 👀 ACK — full twin of the Slack bridge.

## What Was Built

### Task 1: Payload structs + pure Resolve() + VerifyGitHubSignature()

`pkg/github/bridge/payload.go`: `IssueCommentPayload` with the locked field mapping (action, issue.{number,pull_request}, comment.{id,body,html_url,user.{login,type}}, installation.id, repository.{full_name,default_branch}). `GitHubEnvelope` struct with all dispatch fields (source, repo, number, kind, comment_id, html_url, sender, body, install_id, default_branch).

`pkg/github/bridge/resolve.go`: Pure `Resolve(fullName string, entries []RepoEntry, defaultProfile string) (alias, profile string, allow []string, matched bool)` implementing exact-before-glob, first-match-wins resolution in two passes (exact first, then glob). Alias defaults to `gh-{owner}-{repo}`. Profile falls back to `defaultProfile`. Also `ContainsMention()` (case-insensitive), `ExtractMentionBody()`, and `OwnerFromFullName()`/`RepoFromFullName()`.

`pkg/github/bridge/interfaces.go`: Narrow mockable interfaces — `SecretFetcher`, `BotLoginFetcher`, `DeliveryNonceStore`, `SandboxAliasResolver`, `EventBridgePublisher`, `SQSSender`, `GitHubReactor`.

`pkg/github/bridge/webhook_handler.go`: `WebhookHandler.Handle()` with the 11-step ordering from RESEARCH Pattern 2 and `VerifyGitHubSignature()` (constant-time HMAC-SHA256, sha256= prefix check).

### Task 2: Full Handle() branch coverage + AWS adapters

`pkg/github/bridge/aws_adapters.go`: Production AWS adapters wired into `WebhookHandler`:
- `SSMSecretFetcher`: 15-min cached SSM fetch for webhook signing secret.
- `SSMBotLoginFetcher`: 15-min cached SSM fetch for bot login name.
- `DynamoGitHubNonceStore`: conditional PutItem on nonces table; returns `(replayed=true, nil)` on `ConditionalCheckFailedException`.
- `DynamoAliasResolver`: alias-index GSI Query for warm path + GetItem for `github_inbound_queue_url`.
- `GitHubSQSAdapter`: `sqs.SendMessage` to github-inbound FIFO with MessageGroupId + MessageDeduplicationId.
- `EventBridgeAdapter`: `events:PutEvents` with `SandboxCreate` detail carrying alias + profile + GitHubEnvelope JSON.
- `InstallationReactor`: App JWT (GenerateGitHubAppJWT) → installation token (ExchangeForInstallationToken) → POST `eyes` reaction; treats 201/200/422 as idempotent success.

`pkg/github/bridge/handle_test.go`: Table-driven tests for all 11 Handle() branches: 401 bad sig, 200 on secret-fetch error, action-not-created drop, bot/self loop drop, no-PR drop, no-mention drop, unauthorized-sender SILENT drop (reactor NOT called — key invariant), replayed GUID drop, warm enqueue+react, cold SandboxCreate+react, SQS error→200, reactor error→200, nil reactor no-panic.

### Task 3: Lambda entrypoint + TF module

`cmd/km-github-bridge/main.go`: Lambda entrypoint cloned from `cmd/km-slack-bridge/main.go`. Reads `KM_GITHUB_REPOS` JSON at cold-start (list-of-objects Pitfall 2 fix), wires all AWS adapters, normalizes base64-encoded bodies and lowercases headers. Dormant when `KM_GITHUB_REPOS` is unset (zero repo entries = all repos silent-drop).

`infra/modules/lambda-github-bridge/v1.0.0/`: Terraform module mirroring `lambda-slack-bridge`:
- Lambda function (timeout 60s, arm64, provided.al2023).
- Lambda Function URL (authorization_type=NONE, application-layer HMAC auth).
- IAM: SSM GetParameter on `/{prefix}/config/github/*` + KMS Decrypt; EventBridge PutEvents on default bus; DDB Query on `alias-index` + GetItem on base table; SQS SendMessage/GetQueueUrl on `{prefix}-github-inbound-*.fifo`; DDB PutItem on nonces table.
- No `required_providers` (root.hcl owns providers per CLAUDE.md Pitfall 7).
- `function_url` output for `km github init` to record.

`Makefile`: Added `km-github-bridge` to `build-lambdas` target (linux/arm64 bootstrap zip).

## Key Decisions

1. **Exact-before-glob in two passes**: `Resolve()` makes two passes — collect exact matches first, globs second. This ensures an exact entry declared *after* a glob still wins, making the config order-independent for exact matches.

2. **No token cache in InstallationReactor**: App JWTs are cheap to mint (<1ms); installation tokens are ~1h valid. Per-invocation mint avoids a stale-token cache bug at the cost of 2 SSM reads (already cached) + 2 HTTP calls. Acceptable for the ~10s ack window.

3. **EventBridgeAdapter uses local struct**: `sandboxCreateDetail` in `aws_adapters.go` mirrors `pkg/aws.SandboxCreateDetail` without importing it, preventing an import cycle. The JSON shape is byte-identical.

4. **SILENT drop for unauthorized sender**: When `sender.login` is not in the allowlist, `Handle()` returns 200 immediately WITHOUT calling the Reactor. The bot is invisible to unauthorized users. This is enforced by a dedicated test case asserting `reactor.called == false`.

5. **200-on-error invariant**: Every internal error path (secret fetch, nonce store, SQS, EventBridge, Reactor) logs and returns 200. GitHub redelivers 5xx with a NEW X-GitHub-Delivery GUID that bypasses our dedup — the Slack bridge lesson applied identically.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Missing build target] Added km-github-bridge to Makefile build-lambdas**
- **Found during:** Task 3 verification
- **Issue:** The plan's verification step requires `make build-lambdas` to produce the bridge zip, but the Makefile target didn't include `cmd/km-github-bridge`.
- **Fix:** Added two lines to `build-lambdas` target in Makefile (cross-compile + zip).
- **Files modified:** Makefile
- **Commit:** 60df5821

**2. [Rule 1 - Bug] Typed-nil interface guard for Reactor in tests**
- **Found during:** Task 2 TDD GREEN phase
- **Issue:** `buildHandler(..., nil)` passed a typed-nil `*mockReactor` which is a non-nil Go interface, causing panic in `Handle()`. The `if h.Reactor != nil` check correctly guards against nil interface values, but the test passed a typed nil.
- **Fix:** Changed `buildHandler` signature to accept `bridge.GitHubReactor` interface directly; nil test passes `bridge.GitHubReactor(nil)` as an explicit nil interface.
- **Files modified:** pkg/github/bridge/handle_test.go
- **Commit:** 6bdea66d

## Self-Check: PASSED

All 12 created files verified present on disk.
All 3 task commits verified in git log (a4433517, 6bdea66d, 60df5821).
`go test ./pkg/github/bridge/ -count=1` — PASS (all tests green).
`go build ./cmd/km-github-bridge/` — OK.
`go vet ./pkg/github/bridge/ ./cmd/km-github-bridge/` — OK.
`grep -L required_providers infra/modules/lambda-github-bridge/v1.0.0/main.tf` — confirmed absent.
