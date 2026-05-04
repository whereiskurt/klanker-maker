---
phase: quick-7
plan: 7
type: execute
wave: 1
depends_on: []
files_modified:
  - internal/app/cmd/doctor.go
  - internal/app/cmd/doctor_slack.go
  - internal/app/cmd/doctor_slack_transcript.go
  - internal/app/cmd/doctor_slack_inbound_test.go
  - internal/app/cmd/doctor_slack_transcript_test.go
  - pkg/aws/sandbox.go
autonomous: true
requirements:
  - QUICK-7-INBOUND-CLEANUP
  - QUICK-7-TRANSCRIPT-CLEANUP

must_haves:
  truths:
    - "km doctor --dry-run=true (the default) leaves stale Slack SQS queues and S3 transcript prefixes untouched and reports them as WARN with operator-readable counts"
    - "km doctor --dry-run=false deletes orphan SQS queues matching {prefix}-slack-inbound-*.fifo when no DDB row exists for them"
    - "km doctor --dry-run=false also deletes the corresponding /sandbox/{id}/slack-inbound-queue-url SSM parameter, treating ParameterNotFound as success"
    - "km doctor --dry-run=false recursively deletes objects under transcripts/{sandbox_id}/ prefixes whose sandbox no longer exists in DDB"
    - "Per-resource failures during cleanup do not abort the loop; final WARN message reports counts (deleted/skipped/total)"
    - "Existing tests in doctor_slack_inbound_test.go and doctor_slack_transcript_test.go still pass; new tests cover the dry-run=false branches"
    - "go build ./... and make build succeed; go test ./internal/app/cmd/... passes"
  artifacts:
    - path: "internal/app/cmd/doctor_slack.go"
      provides: "checkSlackInboundStaleQueues with dryRun + ssmClient parameters; performs DeleteQueue + DeleteParameter when dryRun=false"
    - path: "internal/app/cmd/doctor_slack_transcript.go"
      provides: "checkSlackTranscriptStaleObjects with dryRun parameter; performs paginated ListObjectsV2 + batched DeleteObjects per stale prefix when dryRun=false"
    - path: "pkg/aws/sandbox.go"
      provides: "S3CleanupAPI interface (or extension to S3ListAPI) exposing DeleteObjects"
    - path: "internal/app/cmd/doctor.go"
      provides: "buildChecks threads dryRun + SSM client into both stale Slack checks; DoctorDeps gains a SlackSSMDeleter field if needed"
    - path: "internal/app/cmd/doctor_slack_inbound_test.go"
      provides: "extended tests covering dry-run=true (no destructive calls), dry-run=false (DeleteQueue + DeleteParameter), partial failures, ParameterNotFound treated as success"
    - path: "internal/app/cmd/doctor_slack_transcript_test.go"
      provides: "extended tests covering dry-run=true (no destructive calls), dry-run=false happy path, multi-page pagination, partial failures"
  key_links:
    - from: "internal/app/cmd/doctor.go (buildChecks at the slack inbound stale-queues block, ~line 2256)"
      to: "checkSlackInboundStaleQueues"
      via: "closure passes deps.DryRun and an SSM DeleteParameter client"
      pattern: "checkSlackInboundStaleQueues\\(ctx,.*dryRun"
    - from: "internal/app/cmd/doctor.go (buildChecks at the transcript stale-objects block, ~line 2294)"
      to: "checkSlackTranscriptStaleObjects"
      via: "closure passes deps.DryRun"
      pattern: "checkSlackTranscriptStaleObjects\\(ctx,.*dryRun"
    - from: "checkSlackInboundStaleQueues"
      to: "kmaws.DeleteSlackInboundQueue"
      via: "best-effort per-orphan call inside the dryRun=false branch"
      pattern: "DeleteSlackInboundQueue\\(ctx"
    - from: "checkSlackTranscriptStaleObjects"
      to: "S3CleanupAPI.DeleteObjects"
      via: "batched (max 1000 keys) per stale prefix inside the dryRun=false branch"
      pattern: "DeleteObjects\\("
---

<objective>
Extend `km doctor`'s existing destructive-cleanup pattern (already present for KMS, IAM, EventBridge) to two Slack-related checks that today only detect-and-warn:
1. `checkSlackInboundStaleQueues` — orphan SQS queues + their SSM parameters.
2. `checkSlackTranscriptStaleObjects` — orphan S3 transcript prefixes.

Purpose: an operator running `km doctor --dry-run=false` should be able to clean up stale Slack resources from failed `km destroy` runs without resorting to manual `aws sqs delete-queue` / `aws s3 rm --recursive` commands. This closes the last detection-only gaps in the doctor cleanup story and matches the muscle memory operators already have from KMS/IAM/EventBridge cleanup.

Output: updated `doctor_slack.go`, `doctor_slack_transcript.go`, `doctor.go` plumbing, narrow S3CleanupAPI extension in `pkg/aws/sandbox.go`, extended unit tests in both existing slack test files. `make build` produces a working `./km` binary; `go test ./internal/app/cmd/...` passes.
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@CLAUDE.md
@.planning/STATE.md

# Project conventions (load-bearing)
- After ANY edit under `cmd/`, `internal/`, or `pkg/`, run `make build` (NOT bare `go build`). The Makefile applies ldflags for `km --version`. Memory: `feedback_rebuild_km`.
- Slack code is in flight (Phase 68 active per project memory `project_phase_68_in_flight`). Treat the Phase 67/68 doctor checks as stable surface — extend, don't refactor.
- Tests use closure-based deps + lightweight in-package fakes. The existing `fakeSQS` in `create_slack_inbound_test.go` already implements `kmaws.SQSClient` with `deleteCalled`/`deleteURL`/`deleteErr` fields — reuse it. The existing `fakeS3List` in `doctor_slack_transcript_test.go` only covers `ListObjectsV2`/`GetObject` — extend or wrap with a `DeleteObjects` method.

# Files to modify
@internal/app/cmd/doctor_slack.go
@internal/app/cmd/doctor_slack_transcript.go
@internal/app/cmd/doctor.go
@pkg/aws/sandbox.go
@internal/app/cmd/doctor_slack_inbound_test.go
@internal/app/cmd/doctor_slack_transcript_test.go

# Reference files (read-only context)
@pkg/aws/sqs.go
@internal/app/cmd/destroy_slack_inbound.go

<interfaces>
<!-- Key types and contracts the executor needs. Embedded so no exploration is required. -->

From pkg/aws/sqs.go:
```go
// SQSClient already covers DeleteQueue + ListQueues — reuse it.
type SQSClient interface {
    CreateQueue(ctx context.Context, in *sqs.CreateQueueInput, opts ...func(*sqs.Options)) (*sqs.CreateQueueOutput, error)
    DeleteQueue(ctx context.Context, in *sqs.DeleteQueueInput, opts ...func(*sqs.Options)) (*sqs.DeleteQueueOutput, error)
    GetQueueAttributes(ctx context.Context, in *sqs.GetQueueAttributesInput, opts ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error)
    ListQueues(ctx context.Context, in *sqs.ListQueuesInput, opts ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error)
}

// Best-effort helper — already swallows QueueDoesNotExist. Use this instead of calling DeleteQueue directly.
func DeleteSlackInboundQueue(ctx context.Context, c SQSClient, queueURL string) error
```

From pkg/aws/sandbox.go (CURRENT shape — extend, don't replace):
```go
type S3ListAPI interface {
    ListObjectsV2(ctx context.Context, input *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
    GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}
```

From pkg/aws/identity.go (canonical SSM DeleteParameter signature — what the SSM dep needs):
```go
DeleteParameter(ctx context.Context, input *ssm.DeleteParameterInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error)
```

From internal/app/cmd/doctor.go (current DoctorDeps Slack fields, ~line 257-275):
```go
// Plan 67-08 — already present
SlackInboundSQS               kmaws.SQSClient
SlackListSandboxesWithInbound func(ctx context.Context) ([]inboundRow, error)
SlackResourcePrefix           string

// Plan 68-11 — already present
SlackTranscriptS3   kmaws.S3ListAPI
SlackListSandboxIDs func(ctx context.Context) ([]string, error)

// Already present from earlier checks — covers GetParameter only:
SSMReadClient SSMReadAPI

// CURRENT call sites in buildChecks (~line 2256, 2294):
checks = append(checks, func(ctx context.Context) CheckResult {
    return checkSlackInboundStaleQueues(ctx, listInbound, inboundSQS, resourcePrefix)
})
// ...
checks = append(checks, func(ctx context.Context) CheckResult {
    r := checkSlackTranscriptStaleObjects(ctx, transcriptS3, transcriptBucket, listSandboxIDs)
    if r.Status == CheckError { r.Status = CheckWarn }
    return r
})
```

From internal/app/cmd/doctor_slack.go (CURRENT signature — line 272):
```go
func checkSlackInboundStaleQueues(
    ctx context.Context,
    listInbound func(context.Context) ([]inboundRow, error),
    sqsClient kmaws.SQSClient,
    resourcePrefix string,
) CheckResult
```

From internal/app/cmd/doctor_slack_transcript.go (CURRENT signature — line 155):
```go
func checkSlackTranscriptStaleObjects(
    ctx context.Context,
    s3Client kmaws.S3ListAPI,
    bucket string,
    listSandboxIDs func(context.Context) ([]string, error),
) CheckResult
```

From internal/app/cmd/doctor.go (~line 1857 — flag is already wired):
```go
cmd.Flags().BoolVar(&dryRun, "dry-run", true,
    "Show stale resources without deleting (use --dry-run=false to clean up)")
// deps.DryRun is set in runDoctor at line 1889; checkStaleKMSKeys etc. already consume it.
```

From internal/app/cmd/destroy_slack_inbound.go:129 (canonical SSM param name — DO NOT change format):
```go
paramName := "/sandbox/" + deps.SandboxID + "/slack-inbound-queue-url"
```

From internal/app/cmd/destroy_slack.go:191 (canonical ParameterNotFound treatment):
```go
var notFound *ssmtypes.ParameterNotFound
// errors.As(err, &notFound) → treat as success
```

From internal/app/cmd/doctor.go:1980-2002 — runDoctor already prints a "Cleanup summary:" block when `!dryRun` and a check name appears in `cleanupChecks` AND its message contains "deleted". Extend the slice with the two new check names.
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Plumbing — narrow interfaces + DoctorDeps additions + buildChecks rewiring</name>
  <files>
    pkg/aws/sandbox.go,
    internal/app/cmd/doctor.go
  </files>
  <behavior>
    - `pkg/aws/sandbox.go` exposes a new narrow interface `S3CleanupAPI` (or equivalent) that adds exactly one method:
      `DeleteObjects(ctx context.Context, input *s3.DeleteObjectsInput, opts ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)`
      Either embed `S3ListAPI` and add `DeleteObjects`, or define `S3CleanupAPI` as a sibling and have callers accept whichever is narrowest. The real `*s3.Client` MUST satisfy whatever new interface is introduced.
    - `internal/app/cmd/doctor.go` `DoctorDeps` gains:
        * `SlackSSMDeleter SSMDeleterAPI` (or reuse a wider existing one — pick the path with the smallest blast radius). The interface must expose only `DeleteParameter`.
        * If `SlackTranscriptS3` is retyped to the new `S3CleanupAPI`, `initRealDepsWithExisting` must still wire the real `*s3.Client` (it already implements `DeleteObjects`).
    - `initRealDepsWithExisting` (~line 2469-2491) wires both new deps from existing `awsCfg` (no new AWS API calls; just additional `NewFromConfig` use of the same SSM/S3 clients already constructed). Specifically:
        * `deps.SlackSSMDeleter = ssmClientForSlack` (the existing one already created at line 2453) — or new `ssm.NewFromConfig(awsCfg)` if the type assertion fails.
        * `deps.SlackTranscriptS3` keeps its assignment (the real client already has DeleteObjects).
    - `buildChecks` (~line 2247-2300) call sites updated to thread `deps.DryRun` and the new SSM dep into `checkSlackInboundStaleQueues`, and `deps.DryRun` into `checkSlackTranscriptStaleObjects`. Closure captures done in the same style as the existing `dryRun := deps.DryRun` at line 2160.
    - `runDoctor`'s post-cleanup summary `cleanupChecks` slice (line 1982) gains the two new check names (`"Slack inbound stale queues"`, `"Slack transcript stale objects"`) so operators see the same cleanup-summary footer for them.
    - This task does NOT change cleanup logic in the check functions yet — it only changes signatures + threads new params through. Check functions accept the new parameters but ignore the dryRun branch (existing detect-only behaviour preserved). This makes the build green so Task 2 has a clean editing surface.
    - Test: `go build ./...` succeeds. Existing `doctor_slack_inbound_test.go` and `doctor_slack_transcript_test.go` calls to the two functions are updated (compile fix only — pass `false` and `nil` for the new args). All existing tests still pass.
  </behavior>
  <action>
    1. Edit `pkg/aws/sandbox.go`: add the new narrow interface near `S3ListAPI` (line 51-56). Preferred shape (smallest blast radius):
       ```go
       // S3CleanupAPI is the narrow interface for destructive S3 operations used
       // by km doctor cleanup. The real *s3.Client satisfies it directly.
       type S3CleanupAPI interface {
           S3ListAPI
           DeleteObjects(ctx context.Context, input *s3.DeleteObjectsInput, opts ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
       }
       ```
       Verify imports already include `"github.com/aws/aws-sdk-go-v2/service/s3"` (they do).

    2. Edit `internal/app/cmd/doctor.go`:
       a. Add a narrow SSM deleter interface near `SSMReadAPI` (~line 122):
          ```go
          // SSMDeleterAPI covers SSM DeleteParameter for stale-resource cleanup.
          type SSMDeleterAPI interface {
              DeleteParameter(ctx context.Context, params *ssm.DeleteParameterInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error)
          }
          ```
       b. In `DoctorDeps` (~line 268-275), promote `SlackTranscriptS3` to `kmaws.S3CleanupAPI` and add `SlackSSMDeleter SSMDeleterAPI` near the inbound deps block (around line 266).
       c. In `initRealDepsWithExisting`, wire the new deps. Around line 2469 add:
          ```go
          deps.SlackSSMDeleter = ssmClientForSlack
          ```
          (The `ssmClientForSlack` variable on line 2453 already exists — confirm via Read first; reuse it. The real `*ssm.Client` satisfies both `SSMParamStore` requirements via the productionSSMParamStore wrapper AND the new `SSMDeleterAPI` directly.)
       d. In `buildChecks`, update both call sites:
          ```go
          dryRun := deps.DryRun  // already declared above for KMS/IAM checks; reuse — do NOT redeclare
          slackSSMDeleter := deps.SlackSSMDeleter
          checks = append(checks, func(ctx context.Context) CheckResult {
              return checkSlackInboundStaleQueues(ctx, listInbound, inboundSQS, resourcePrefix, dryRun, slackSSMDeleter)
          })
          // ... and ...
          checks = append(checks, func(ctx context.Context) CheckResult {
              r := checkSlackTranscriptStaleObjects(ctx, transcriptS3, transcriptBucket, listSandboxIDs, dryRun)
              if r.Status == CheckError { r.Status = CheckWarn }
              return r
          })
          ```
       e. In `runDoctor` (~line 1982), extend the `cleanupChecks` slice:
          ```go
          cleanupChecks := []string{"Stale KMS Keys", "Stale IAM Roles", "Stale Schedules",
              "Slack inbound stale queues", "Slack transcript stale objects"}
          ```

    3. Edit `internal/app/cmd/doctor_slack.go` `checkSlackInboundStaleQueues` signature (line 272-277) to accept the two new parameters:
       ```go
       func checkSlackInboundStaleQueues(
           ctx context.Context,
           listInbound func(context.Context) ([]inboundRow, error),
           sqsClient kmaws.SQSClient,
           resourcePrefix string,
           dryRun bool,
           ssmDeleter SSMDeleterAPI,  // may be nil — treat nil as "skip SSM cleanup"
       ) CheckResult
       ```
       For Task 1, the new params are accepted but unused (Task 2 fills the body). Add a `_ = dryRun; _ = ssmDeleter` blank assignment to suppress unused warnings during the transition, OR leave the signature change and add only enough body wiring that Task 2 can fill in. Preferred: keep the body unchanged for Task 1 and silence linter via `_ = dryRun; _ = ssmDeleter` immediately after the resourcePrefix default block.

    4. Edit `internal/app/cmd/doctor_slack_transcript.go` `checkSlackTranscriptStaleObjects` signature (line 155-160) to accept `dryRun bool` and retype `s3Client` to `kmaws.S3CleanupAPI`:
       ```go
       func checkSlackTranscriptStaleObjects(
           ctx context.Context,
           s3Client kmaws.S3CleanupAPI,
           bucket string,
           listSandboxIDs func(context.Context) ([]string, error),
           dryRun bool,
       ) CheckResult
       ```
       Same Task 1 discipline: accept `dryRun` but ignore it (`_ = dryRun`). Body unchanged.

    5. Update test compile fixes (signature drift only — no new behaviour):
       - `doctor_slack_inbound_test.go` ~lines 101, 125, 141, 152: append `, false, nil` to every existing `checkSlackInboundStaleQueues(...)` call.
       - `doctor_slack_transcript_test.go` ~lines 188, 219, 235, 249, 260, 270: append `, false` to every existing `checkSlackTranscriptStaleObjects(...)` call.
       - The existing `fakeS3List` in `doctor_slack_transcript_test.go` (line 34) currently implements `S3ListAPI`. After retyping to `S3CleanupAPI`, it must also implement `DeleteObjects`. Add a stub method that records calls (mirror the `fakeSQS` pattern: `deleteObjectsCalls int; deleteObjectsKeys []string; deleteObjectsErr error`). Task 2 will exercise it.

    Why this split: Task 1 is pure mechanical plumbing and yields a green build. Task 2 then ONLY edits the two check function bodies + adds tests for the new branches — no signature churn, no DoctorDeps risk.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr &amp;&amp; go build ./... &amp;&amp; go test ./internal/app/cmd/... -run 'TestDoctor_SlackInboundStaleQueues|TestDoctor_SlackTranscriptStaleObjects' -count=1</automated>
  </verify>
  <done>
    - `go build ./...` returns 0
    - All existing tests in `doctor_slack_inbound_test.go` and `doctor_slack_transcript_test.go` still pass (no behavioural change yet, just signature drift)
    - `pkg/aws/sandbox.go` exports `S3CleanupAPI` and the real `*s3.Client` satisfies it (verified by `go vet`)
    - `DoctorDeps.SlackSSMDeleter` exists and is wired in `initRealDepsWithExisting`
    - `buildChecks` threads `dryRun` into both check call sites
    - `runDoctor`'s `cleanupChecks` slice contains both new check names
    - `make build` succeeds (binary built with ldflags)
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Cleanup logic + new test coverage for the dryRun=false branches</name>
  <files>
    internal/app/cmd/doctor_slack.go,
    internal/app/cmd/doctor_slack_transcript.go,
    internal/app/cmd/doctor_slack_inbound_test.go,
    internal/app/cmd/doctor_slack_transcript_test.go
  </files>
  <behavior>
    Behaviour for `checkSlackInboundStaleQueues` when `dryRun=false`:
    - For each orphan queue URL in `stale`:
      1. Call `kmaws.DeleteSlackInboundQueue(ctx, sqsClient, qURL)`. On error, increment `skipped` and continue (do NOT abort the loop).
      2. Parse the sandbox ID from the queue name segment: split `qURL` on `/`, take last element, strip the `{resourcePrefix}-slack-inbound-` prefix and `.fifo` suffix. If parsing yields empty, skip the SSM step but still count the queue as deleted.
      3. If `ssmDeleter != nil` AND a sandbox ID was successfully parsed, call `ssmDeleter.DeleteParameter(ctx, &ssm.DeleteParameterInput{Name: aws.String("/sandbox/" + sbID + "/slack-inbound-queue-url")})`. On `ssmtypes.ParameterNotFound` (use `errors.As`), treat as success. On any other error, log to no-op (swallow — the queue is already gone, the SSM param is at worst orphaned for the next doctor run to find).
      4. Increment `deleted` on successful queue delete.
    - Final status: WARN with message: `fmt.Sprintf("%d stale inbound queue(s) without DDB record (%d deleted, %d skipped)", len(stale), deleted, skipped)`.
    - When `dryRun=true`, behaviour is unchanged: WARN with the existing `Remediation` hint message — but UPDATE the remediation hint to point at `--dry-run=false` instead of the manual `aws sqs delete-queue` command (mirroring the KMS/IAM/Schedules pattern). Existing test assertions on the message check for the substring "orphan" (line 105) and "all 2 ... accounted for" (line 129) — make sure the dryRun=true message keeps a substring containing the orphan queue URL.

    Behaviour for `checkSlackTranscriptStaleObjects` when `dryRun=false`:
    - For each stale prefix `p` in `stale`:
      1. Paginate `s3:ListObjectsV2` with `Prefix=p` (NO delimiter — we want every key under the prefix). Collect all `s3types.Object` keys.
      2. Batch into groups of 1000 (S3 DeleteObjects API limit). For each batch, call `s3Client.DeleteObjects` with an `*s3.DeleteObjectsInput` containing `Bucket`, `Delete: &s3types.Delete{Objects: <batch>}`. Use `Quiet: true` to keep the response small.
      3. On any error in pagination or deletion, increment `skipped` for the prefix and continue with the next prefix.
      4. Track `objectsDeleted` total across all prefixes.
    - Final status: WARN with message: `fmt.Sprintf("%d stale transcript prefix(es) (%d deleted, %d skipped, %d objects total)", len(stale), deleted, skipped, objectsDeleted)`.
    - When `dryRun=true`, behaviour is unchanged: WARN with stale prefix names. Update the remediation hint to point at `--dry-run=false` (mirror Task 1's pattern).

    New tests (extend existing files; do NOT create new test files):

    `doctor_slack_inbound_test.go` — add 4 cases:
      * `TestDoctor_SlackInboundStaleQueues_DryRunTrue_NoDestructiveCalls`: orphan present, `dryRun=false`-args are `false, nil`. Assert `fakeSQS.deleteCalled == 0` AND result Status=WARN AND result Message contains the orphan URL substring.
      * `TestDoctor_SlackInboundStaleQueues_DryRunFalse_HappyPath`: 2 orphans, working SQS client, working SSM client. Assert `fakeSQS.deleteCalled == 2` AND SSM mock saw 2 DeleteParameter calls with names matching `/sandbox/{sb-id}/slack-inbound-queue-url` AND result Message contains "(2 deleted, 0 skipped)".
      * `TestDoctor_SlackInboundStaleQueues_DryRunFalse_PartialFailure`: 2 orphans, fakeSQS.deleteErr set so DeleteQueue returns "AccessDenied" for both. Result Message contains "(0 deleted, 2 skipped)" AND no SSM calls were made for the failed queues (deletion of SSM is best-effort but we said: only call SSM after successful queue delete — re-check the behaviour spec; if you decide otherwise, document inline and ensure the test matches).
      * `TestDoctor_SlackInboundStaleQueues_DryRunFalse_SSMParameterNotFound`: 1 orphan. fakeSQS deletes successfully. SSM mock returns `&ssmtypes.ParameterNotFound{}`. Result Status=WARN with `(1 deleted, 0 skipped)` — i.e. ParameterNotFound is treated as success.

    For the SSM mock, define a small in-test fake at top of file (after `fakeSQS` reference) — name it `fakeSSMDeleter` — exposing `deleteCalls []string` and `deleteErr error`. Implement only `DeleteParameter`. Make it satisfy the new `SSMDeleterAPI` interface.

    `doctor_slack_transcript_test.go` — add 4 cases:
      * `TestDoctor_SlackTranscriptStaleObjects_DryRunTrue_NoDestructiveCalls`: stale prefix present (sb-c). Args `dryRun=false`. Assert `fakeS3List.deleteObjectsCalls == 0` AND result Status=WARN AND result Message contains "transcripts/sb-c/".
      * `TestDoctor_SlackTranscriptStaleObjects_DryRunFalse_HappyPath`: 1 stale prefix with 3 objects (extend `fakeS3List` so a second `ListObjectsV2` call without delimiter under `transcripts/sb-c/` returns 3 keys). Assert `fakeS3List.deleteObjectsCalls == 1` AND captured `deleteObjectsKeys` length == 3 AND Message contains "(1 deleted, 0 skipped, 3 objects total)".
      * `TestDoctor_SlackTranscriptStaleObjects_DryRunFalse_PartialFailure`: 2 stale prefixes; the second prefix's DeleteObjects errors. Result Message contains "(1 deleted, 1 skipped, ...".
      * `TestDoctor_SlackTranscriptStaleObjects_DryRunFalse_MultiPage`: 1 stale prefix with 1500 objects across 2 pages of ListObjectsV2. Assert `deleteObjectsCalls == 2` (two batches: 1000 + 500) AND total objects deleted == 1500.

    For the multi-page test, the `fakeS3List.pages` slice already supports sequential pages — model the per-prefix list calls by stuffing ListObjectsV2 outputs in order: first the top-level common-prefix list, then page 1 of the prefix contents (1000 keys, IsTruncated=true), then page 2 (500 keys, IsTruncated=false). Adjust the fake's call counter so it advances per call.
  </behavior>
  <action>
    1. Edit `internal/app/cmd/doctor_slack.go` — replace the body of `checkSlackInboundStaleQueues` to implement the dryRun=false branch. The existing detect-only logic (build orphan list) stays; insert a new conditional block AFTER `stale` is populated and BEFORE the existing `if len(stale) > 0` return:

       ```go
       if len(stale) == 0 {
           return CheckResult{
               Name:    name,
               Status:  CheckOK,
               Message: fmt.Sprintf("all %d inbound queue(s) accounted for in DDB", len(listOut.QueueUrls)),
           }
       }

       if dryRun {
           return CheckResult{
               Name:        name,
               Status:      CheckWarn,
               Message:     fmt.Sprintf("%d stale inbound queue(s) without DDB record: %s", len(stale), strings.Join(stale, ", ")),
               Remediation: "Re-run with --dry-run=false to delete the orphan queues + their SSM parameters",
           }
       }

       // Destructive cleanup path.
       prefixSegment := resourcePrefix + "-slack-inbound-"
       deleted, skipped := 0, 0
       for _, qURL := range stale {
           if delErr := kmaws.DeleteSlackInboundQueue(ctx, sqsClient, qURL); delErr != nil {
               skipped++
               continue
           }
           deleted++
           // Best-effort SSM cleanup — derive sandbox ID from the queue URL tail.
           if ssmDeleter == nil {
               continue
           }
           lastSlash := strings.LastIndex(qURL, "/")
           if lastSlash < 0 {
               continue
           }
           queueName := qURL[lastSlash+1:]
           sbID := strings.TrimSuffix(strings.TrimPrefix(queueName, prefixSegment), ".fifo")
           if sbID == "" || sbID == queueName {
               continue
           }
           paramName := "/sandbox/" + sbID + "/slack-inbound-queue-url"
           _, ssmErr := ssmDeleter.DeleteParameter(ctx, &ssm.DeleteParameterInput{Name: awssdk.String(paramName)})
           if ssmErr != nil {
               var notFound *ssmtypes.ParameterNotFound
               if errors.As(ssmErr, &notFound) {
                   // Already gone — treat as success. No state change.
                   continue
               }
               // Other SSM errors are swallowed: queue is already deleted, param can be reaped on next doctor run.
           }
       }
       return CheckResult{
           Name:    name,
           Status:  CheckWarn,
           Message: fmt.Sprintf("%d stale inbound queue(s) without DDB record (%d deleted, %d skipped)", len(stale), deleted, skipped),
       }
       ```

       Add to imports at top of file: `"errors"`, `"github.com/aws/aws-sdk-go-v2/service/ssm"`, `ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"`. Remove the `_ = dryRun; _ = ssmDeleter` line added in Task 1.

    2. Edit `internal/app/cmd/doctor_slack_transcript.go` — replace the body of `checkSlackTranscriptStaleObjects` to implement the dryRun=false branch. The existing detect-only logic (compute `stale`) stays; insert a new conditional block AFTER `stale` is populated:

       ```go
       if len(stale) == 0 {
           return CheckResult{
               Name:    name,
               Status:  CheckOK,
               Message: fmt.Sprintf("%d transcript prefix(es); none stale", len(prefixes)),
           }
       }

       if dryRun {
           return CheckResult{
               Name:        name,
               Status:      CheckWarn,
               Message:     fmt.Sprintf("%d stale transcript prefix(es): %s", len(stale), strings.Join(stale, ", ")),
               Remediation: "Re-run with --dry-run=false to delete the orphan transcript objects",
           }
       }

       // Destructive cleanup path.
       deleted, skipped, objectsDeleted := 0, 0, 0
       for _, p := range stale {
           keys, listErr := listAllKeysUnderPrefix(ctx, s3Client, bucket, p)
           if listErr != nil {
               skipped++
               continue
           }
           if len(keys) == 0 {
               deleted++ // empty prefix — nothing to do, count as cleaned
               continue
           }
           prefixOK := true
           for batchStart := 0; batchStart < len(keys); batchStart += 1000 {
               end := batchStart + 1000
               if end > len(keys) { end = len(keys) }
               objs := make([]s3types.ObjectIdentifier, 0, end-batchStart)
               for _, k := range keys[batchStart:end] {
                   objs = append(objs, s3types.ObjectIdentifier{Key: awssdk.String(k)})
               }
               _, delErr := s3Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
                   Bucket: awssdk.String(bucket),
                   Delete: &s3types.Delete{Objects: objs, Quiet: awssdk.Bool(true)},
               })
               if delErr != nil { prefixOK = false; break }
               objectsDeleted += end - batchStart
           }
           if prefixOK { deleted++ } else { skipped++ }
       }
       return CheckResult{
           Name:    name,
           Status:  CheckWarn,
           Message: fmt.Sprintf("%d stale transcript prefix(es) (%d deleted, %d skipped, %d objects total)", len(stale), deleted, skipped, objectsDeleted),
       }
       ```

       Add a small helper at the bottom of the file:
       ```go
       // listAllKeysUnderPrefix paginates ListObjectsV2 (no delimiter) and returns every object key.
       func listAllKeysUnderPrefix(ctx context.Context, c kmaws.S3CleanupAPI, bucket, prefix string) ([]string, error) {
           var keys []string
           var token *string
           for {
               out, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
                   Bucket: awssdk.String(bucket),
                   Prefix: awssdk.String(prefix),
                   ContinuationToken: token,
               })
               if err != nil { return nil, err }
               for _, obj := range out.Contents {
                   if obj.Key != nil { keys = append(keys, *obj.Key) }
               }
               if out.IsTruncated == nil || !*out.IsTruncated { break }
               token = out.NextContinuationToken
           }
           return keys, nil
       }
       ```

       Add to imports at top of file: `s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"`. Remove the `_ = dryRun` line from Task 1.

    3. Extend `fakeS3List` in `doctor_slack_transcript_test.go` (~line 34):
       - Add fields: `deleteObjectsCalls int`, `deleteObjectsKeys []string`, `deleteObjectsErr error` (or `deleteObjectsErrs []error` for multi-call control).
       - Add a method:
         ```go
         func (f *fakeS3List) DeleteObjects(_ context.Context, in *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
             f.deleteObjectsCalls++
             if in != nil && in.Delete != nil {
                 for _, o := range in.Delete.Objects {
                     if o.Key != nil { f.deleteObjectsKeys = append(f.deleteObjectsKeys, *o.Key) }
                 }
             }
             if f.deleteObjectsErr != nil { return nil, f.deleteObjectsErr }
             return &s3.DeleteObjectsOutput{}, nil
         }
         ```

    4. Add a `fakeSSMDeleter` near top of `doctor_slack_inbound_test.go` (after the imports, before existing tests):
       ```go
       type fakeSSMDeleter struct {
           deleteCalls []string
           deleteErr   error
       }
       func (f *fakeSSMDeleter) DeleteParameter(_ context.Context, in *ssm.DeleteParameterInput, _ ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
           if in != nil && in.Name != nil { f.deleteCalls = append(f.deleteCalls, *in.Name) }
           if f.deleteErr != nil { return nil, f.deleteErr }
           return &ssm.DeleteParameterOutput{}, nil
       }
       ```
       Add necessary imports (`"github.com/aws/aws-sdk-go-v2/service/ssm"`, `ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"`).

    5. Add the 4 new tests in each file as described under "behaviour" above. Reuse the existing fakeSQS field `deleteCalled` to count DeleteQueue invocations. For partial-failure tests, set `fakeSQS.deleteErr` to a non-nil error.

    6. Update the existing `TestDoctor_SlackInboundStaleQueues_FoundOrphans` (~line 89) to also assert the new dry-run remediation message contains "--dry-run=false" (since Task 2 changed the remediation text).

    7. Update the existing `TestDoctor_SlackTranscriptStaleObjects` (~line 173) likewise: assert remediation contains "--dry-run=false".

    8. After every edit, run `make build` then `go test ./internal/app/cmd/...` from the repo root.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr &amp;&amp; make build &amp;&amp; go test ./internal/app/cmd/... -count=1</automated>
  </verify>
  <done>
    - `make build` succeeds; `./km --version` runs (smoke check that ldflags applied)
    - `go test ./internal/app/cmd/...` passes — both old and new test cases
    - `checkSlackInboundStaleQueues` deletes orphan SQS queues + best-effort SSM params when `dryRun=false`; ParameterNotFound is treated as success
    - `checkSlackTranscriptStaleObjects` paginates + batched-deletes objects under each stale prefix when `dryRun=false`
    - Per-resource failures do not abort either loop; final WARN message reports counts in the documented format
    - dry-run=true behaviour preserved (existing tests still green); remediation messages now point operators at `--dry-run=false` instead of manual aws-cli commands
    - `go vet ./...` clean
  </done>
</task>

</tasks>

<verification>
After both tasks complete:

```bash
cd /Users/khundeck/working/klankrmkr
make build                              # produces ./km with ldflags
./km --version                          # smoke check
go test ./internal/app/cmd/... -count=1 # all cmd tests, including extended slack tests
go vet ./...                            # no new warnings
```

Manual sanity (no AWS calls — operator can confirm the help text):
```bash
./km doctor --help | grep dry-run       # flag still documented
```

Optional, requires AWS creds (operator decides):
```bash
./km doctor                              # dry-run=true (default) — orphans appear as WARN with --dry-run=false hint
./km doctor --dry-run=false              # cleanup actually fires; rerun should show 0 stale
```
</verification>

<success_criteria>
- `km doctor` (default `--dry-run=true`) detects orphan SQS queues and orphan S3 transcript prefixes and reports them as WARN. Remediation hint points at `--dry-run=false`, mirroring KMS/IAM/Schedules.
- `km doctor --dry-run=false` deletes orphan SQS queues, best-effort deletes their SSM `/sandbox/{id}/slack-inbound-queue-url` parameters (treating `ParameterNotFound` as success), and recursively deletes objects under stale `transcripts/{sandbox_id}/` S3 prefixes.
- Per-resource failures are isolated — the loop continues, and the final WARN message reports `(N deleted, M skipped[, K objects total])`.
- The post-cleanup summary block in `runDoctor` (already wired for KMS/IAM/Schedules) now also surfaces the two new check results.
- All existing unit tests still pass; new tests exercise dry-run=true (no destructive calls), dry-run=false happy path, partial failures, ParameterNotFound, and multi-page S3 prefixes.
- `make build` produces a working `./km` binary; `go test ./internal/app/cmd/...` and `go vet ./...` pass cleanly.
- Out-of-scope: stale Slack channel archival (third stale Slack check) is explicitly NOT touched.
</success_criteria>

<output>
After completion, create `.planning/quick/7-km-doctor-dry-run-false-cleans-up-stale-/7-SUMMARY.md` documenting:
- The two new cleanup paths (with file:line refs)
- The new `S3CleanupAPI` interface and `SSMDeleterAPI` interface (where they live, what they wrap)
- New `DoctorDeps.SlackSSMDeleter` field and where it's wired in `initRealDepsWithExisting`
- Test additions: 4 new cases per file, reusing existing fakes
- Operator-facing change: WARN messages now say "Re-run with --dry-run=false" instead of giving manual aws-cli commands
</output>
