---
phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
plan: "08"
subsystem: slack-inbound-observability
tags: [slack, sqs, doctor, status, list, observability, diagnostics]
dependency_graph:
  requires: [67-06]
  provides: [inbound-status-display, inbound-list-column, inbound-doctor-checks]
  affects: [status.go, list.go, doctor.go, doctor_slack.go, sandbox.go, sandbox_dynamo.go]
tech_stack:
  added: []
  patterns:
    - DDB Query with SelectCount for cheap thread counting (no item bodies returned)
    - Lazy-init SQS/DDB clients in printSandboxStatus (mirrors existing EC2 pattern in computeIdleRemaining)
    - Conditional column rendering — 💬 column appears only when at least one sandbox has inbound enabled
    - X-OAuth-Scopes response header parsing for Slack auth.test scope discovery (no SDK dependency)
    - Function-typed DI fields on DoctorDeps (SlackListSandboxesWithInbound, SlackAuthTestScopes) — production wiring uses closures over real AWS clients
key_files:
  created:
    - .planning/phases/67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch/67-08-SUMMARY.md
  modified:
    - internal/app/cmd/status.go
    - internal/app/cmd/list.go
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_slack.go
    - internal/app/cmd/doctor_slack_inbound_test.go
    - internal/app/cmd/create_slack_inbound_test.go
    - pkg/aws/sandbox.go
decisions:
  - "Added SlackChannelID, SlackInboundQueueURL, ActiveThreads to SandboxRecord rather than fetching SandboxMetadata separately in status/list — simpler dataflow, the existing metadataToRecord conversion already had the mapping queued from Plan 67-07"
  - "X-OAuth-Scopes response header (not response body) is the canonical way to read Slack OAuth scopes from auth.test — avoids needing scopes=true query param or scope-specific calls"
  - "Type-assertion against *appConfigAdapter to derive ResourcePrefix rather than extending the DoctorConfigProvider interface — keeps the interface narrow; falls back to 'km' when the type assertion fails"
  - "DoctorDeps function fields (SlackListSandboxesWithInbound, SlackAuthTestScopes) instead of injectable structs — minimal surface area; production wires closures, tests pass inline lambdas"
  - "💬 column hidden entirely (no empty column) when no sandbox has inbound enabled — preserves Phase 63-only operator visual"
  - "All three new doctor checks demote ERROR to WARN in buildChecks (matching Phase 63 pattern) so Slack inbound issues never fail km doctor"
metrics:
  duration: "~48min"
  completed: "2026-05-03"
  tasks: 2
  files_modified: 7
---

# Phase 67 Plan 08: Slack Inbound Operator Diagnostics Summary

**One-liner:** km status shows queue URL/depth/thread count, km list --wide adds 💬 thread column, km doctor adds three checks (queue-exists, stale-queues, app-events-scopes) so operators detect orphaned SQS queues, missing scopes, and unreachable queues before they break inbound.

## What Was Built

### Task 1: km status + km list extensions

**`pkg/aws/sandbox.go`** — `SandboxRecord` gained three fields populated from DDB metadata:
- `SlackChannelID string` — the Slack channel the sandbox posts to
- `SlackInboundQueueURL string` — SQS FIFO URL when notifySlackInboundEnabled was set
- `ActiveThreads int` — count of `km-slack-threads` rows for the sandbox's channel (populated only by `km list --wide`)

**`internal/app/cmd/status.go`** — added a "Slack Inbound" section to `printSandboxStatus`:
- When `rec.SlackInboundQueueURL != ""`: prints `queue:`, `queue depth:` (live `awspkg.QueueDepth`), and `threads:` (DDB Query Count against `km-slack-threads` partitioned on `channel_id`)
- When sandbox has Slack outbound but no inbound: prints `Slack Inbound: disabled`
- SQS and DDB clients are lazy-loaded via `kmaws.LoadAWSConfig` — same pattern as EC2/CloudWatch in `computeIdleRemaining`
- `printSandboxStatus` now takes `ctx context.Context` (single caller updated)

Helper `countActiveThreads(ctx, ddb DDBQueryClient, tableName, channelID string) (int, error)` does a single `Query` with `Select=COUNT` — no item bodies returned.

**`internal/app/cmd/list.go`** — added optional 💬 column in `--wide` mode:
- After EC2 status checks, when `wide` is set, scans records for any `SlackInboundQueueURL != ""` — only then loads thread counts (avoids unnecessary DDB scan)
- For each record with a `SlackChannelID`, calls `countActiveThreads` and stores result in `record.ActiveThreads`
- `printSandboxTable` decides `showThreads` from records — if any sandbox has inbound enabled, the 💬 header + per-row count column appear; otherwise the column is hidden entirely (Phase 63 sandboxes see no visual change)
- Records without `SlackChannelID` show `-` in the column

### Task 2: Three new km doctor checks

**`internal/app/cmd/doctor_slack.go`** — three new check functions, each following the existing Plan 63-09 signature:

#### 1. `checkSlackInboundQueueExists`

Iterates all `km-sandboxes` rows with non-empty `slack_inbound_queue_url`, calls `awspkg.QueueDepth` against each. If any queue is unreachable (`GetQueueAttributes` returns `QueueDoesNotExist` or any error), the check returns FAIL listing the sandbox IDs and queue URLs.

**Failure looks like:** `inbound queue missing or unreachable for: sb-abc123 (https://sqs.../km-slack-inbound-sb-abc123.fifo)`

**How to fix:** the queue was deleted manually (or SQS quota issue). Run `km destroy <sandbox-id> --remote --yes` to clean up the DDB record, then `km create` to reprovision.

#### 2. `checkSlackInboundStaleQueues`

Calls `sqsClient.ListQueues` with prefix `{resource_prefix}-slack-inbound-`, then cross-references against the DDB inbound row set. Any queue URL not in DDB is an orphan from a failed `km destroy` (queue deleted from list before DDB update, or DDB row deleted but queue cleanup failed). Returns WARN per stale queue with full URL listing.

**Failure looks like:** `2 stale inbound queue(s) without DDB record: https://sqs.../km-slack-inbound-orphan1.fifo, https://sqs.../km-slack-inbound-orphan2.fifo`

**How to fix:** run `aws sqs delete-queue --queue-url <url>` for each stale queue listed.

#### 3. `checkSlackAppEventsScopes`

Calls Slack's `auth.test` HTTP endpoint and parses the `X-OAuth-Scopes` response header (a comma-separated scope list). Requires both `channels:history` and `groups:history` for inbound event handling. Missing scopes = silent inbound failure (Slack accepts the App but never sends events).

**Failure looks like:** `Slack App missing required scopes for inbound: channels:history, groups:history`

**How to fix:** add scopes via Slack App config → OAuth & Permissions → Bot Token Scopes, then run `km slack rotate-token` to refresh the SSM-cached token.

### Production wiring (`initRealDepsWithExisting`)

`DoctorDeps` gained four new fields:
- `SlackInboundSQS kmaws.SQSClient` — from `sqs.NewFromConfig(awsCfg)`
- `SlackListSandboxesWithInbound func(ctx) ([]inboundRow, error)` — closure over `dynamodb.NewFromConfig(awsCfg)` calling new `listSandboxesWithInboundImpl` helper
- `SlackAuthTestScopes func(ctx) ([]string, error)` — closure that fetches `/km/slack/bot-token` from SSM and calls `fetchSlackBotScopes` (HTTP POST to `slack.com/api/auth.test`, parses `X-OAuth-Scopes` header)
- `SlackResourcePrefix string` — derived via `cfg.(*appConfigAdapter)` type assertion → `cfg.GetResourcePrefix()`, defaults to `"km"`

All three checks demote ERROR to WARN in `buildChecks` (mirrors Phase 63 pattern) — Slack issues never fail `km doctor`.

### Test coverage

**`doctor_slack_inbound_test.go`** — 13 tests replacing 3 Wave 0 stubs:

| Function | Test | Asserts |
| --- | --- | --- |
| checkSlackInboundQueueExists | _AllHealthy | OK, message includes count |
| | _OneMissing | FAIL when getAttrsErr is set |
| | _NoSandboxes | OK, "no sandboxes" message |
| | _NilDeps | SKIPPED |
| checkSlackInboundStaleQueues | _FoundOrphans | WARN, orphan URL surfaced |
| | _AllAccountedFor | OK, count message |
| | _NoQueues | OK, "no inbound queues" |
| | _NilDeps | SKIPPED |
| checkSlackAppEventsScopes | _HasAllScopes | OK |
| | _MissingScope | FAIL, both scope names + remediation |
| | _PartialScopes | FAIL, only missing scope listed |
| | _AuthTestError | WARN |
| | _NilDeps | SKIPPED |

**`create_slack_inbound_test.go`** — `fakeSQS` extended with two new fields (`getAttrsErr`, `listResult`) and the corresponding wiring in `GetQueueAttributes`/`ListQueues`. No regressions — existing Plan 67-06 tests still pass (each leaves the new fields at zero value).

## Verification

```bash
go build ./...                                                              # clean
go test ./internal/app/cmd/... -run "TestDoctor_Slack" -count=1             # 13/13 PASS
go test ./internal/app/cmd/... -run "TestDoctor" -count=1                   # all doctor tests PASS
go test ./pkg/... -count=1                                                  # all pkg tests PASS
make build                                                                  # km v0.2.464 builds clean
```

Pre-existing test failures unrelated to Plan 67-08 (`TestAtList_WithRecords`, `TestListCmd_EmptyStateBucketError`, `TestStatusCmd_EmptyStateBucketError`) are out of scope and not affected by these changes.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Build broken before Task 2 because SandboxRecord didn't have Slack fields**
- **Found during:** Task 1 setup
- **Issue:** `pkg/aws/sandbox_dynamo.go:137-138` (committed in Plan 67-07) referenced `SlackChannelID` and `SlackInboundQueueURL` on `SandboxRecord`, but the struct didn't have those fields. The repo wouldn't build.
- **Fix:** added the three fields (`SlackChannelID`, `SlackInboundQueueURL`, `ActiveThreads`) to `SandboxRecord` in `pkg/aws/sandbox.go` — both unblocks the build AND is explicitly required by Task 1.
- **Files modified:** `pkg/aws/sandbox.go`
- **Commit:** `ab3cf93`

### Plan-driven design adjustments

- **Plan suggested:** add a `doctorDeps` struct (lowercase) with `SQS`, `Cfg`, `ListSandboxesWithInbound`, etc., as positional struct param on each check
- **What was built:** kept the existing `DoctorDeps` (uppercase) struct from Plan 63-09 and added the four new fields directly. Each check function takes the specific deps it needs as positional args, mirroring `checkSlackTokenValidity` / `checkStaleSlackChannels`.
- **Reason:** the existing pattern is already consistent across 25+ doctor checks; introducing a parallel struct would split the convention and complicate `buildChecks`.

- **Plan suggested:** doctor.go `listSandboxesWithInboundImpl` uses a raw `Scan` with `FilterExpression: attribute_exists(...) AND <> :empty`
- **What was built:** uses the existing `kmaws.ListAllSandboxMetadataDynamo` + Go-side filter
- **Reason:** that helper already pulls `SlackInboundQueueURL` (added in Plan 67-07) and is well-tested. The Go-side filter is O(n) but n is bounded by total sandbox count (typically <100); avoiding the filter expression keeps the helper trivial.

- **Plan suggested:** `SlackAuthTestScopes` calls auth.test with `?scopes=true` or parses scopes from response body
- **What was built:** parses `X-OAuth-Scopes` response header
- **Reason:** Slack's documented behavior is that ALL responses include this header for OAuth-authenticated calls; no special query param or body parsing needed. Simpler and lighter (no JSON decode).

## Files Modified Summary

| File | Lines added | Purpose |
| --- | --- | --- |
| pkg/aws/sandbox.go | +12 | SlackChannelID, SlackInboundQueueURL, ActiveThreads on SandboxRecord |
| internal/app/cmd/status.go | +57 | Slack Inbound section + countActiveThreads helper + DDBQueryClient interface |
| internal/app/cmd/list.go | +49 | Thread count loading + conditional 💬 column |
| internal/app/cmd/doctor.go | +52 | Four new DoctorDeps fields + buildChecks registrations + production wiring |
| internal/app/cmd/doctor_slack.go | +247 | Three check functions + listSandboxesWithInboundImpl + fetchSlackBotScopes |
| internal/app/cmd/doctor_slack_inbound_test.go | +200 | 13 new tests replacing 3 Wave 0 stubs |
| internal/app/cmd/create_slack_inbound_test.go | +9 | fakeSQS getAttrsErr/listResult fields |

## Self-Check: PASSED

**Files exist:**
- FOUND: /Users/khundeck/working/klankrmkr/internal/app/cmd/status.go (Slack Inbound section, countActiveThreads, DDBQueryClient interface)
- FOUND: /Users/khundeck/working/klankrmkr/internal/app/cmd/list.go (--wide 💬 column conditional)
- FOUND: /Users/khundeck/working/klankrmkr/internal/app/cmd/doctor.go (3 check registrations + production deps wiring)
- FOUND: /Users/khundeck/working/klankrmkr/internal/app/cmd/doctor_slack.go (3 new check functions + 2 helpers)
- FOUND: /Users/khundeck/working/klankrmkr/internal/app/cmd/doctor_slack_inbound_test.go (13 tests)
- FOUND: /Users/khundeck/working/klankrmkr/pkg/aws/sandbox.go (SandboxRecord new fields)

**Commits:**
- FOUND: ab3cf93 (feat(67-08): extend km status + km list with Slack inbound stats)
- FOUND: 4294be6 (feat(67-08): add three Slack inbound doctor checks + tests)
