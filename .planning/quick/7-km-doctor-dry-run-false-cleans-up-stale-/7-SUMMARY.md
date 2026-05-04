---
phase: quick-7
plan: 7
type: execute
subsystem: km-doctor
tags: [doctor, slack, cleanup, sqs, s3, ssm]
requires:
  - kmaws.SQSClient (Plan 67-08, existing)
  - kmaws.DeleteSlackInboundQueue (Plan 67-08, existing)
provides:
  - kmaws.S3CleanupAPI (new — embeds S3ListAPI + DeleteObjects)
  - SSMDeleterAPI (new — narrow DeleteParameter)
  - DoctorDeps.SlackSSMDeleter (new field)
  - DoctorDeps.SlackTranscriptS3 retyped to S3CleanupAPI
  - listAllKeysUnderPrefix (new helper in doctor_slack_transcript.go)
affects:
  - km doctor --dry-run=false (now also cleans Slack inbound SQS queues + their SSM params + S3 transcript prefixes)
  - km doctor (default dry-run=true) remediation messages now point at --dry-run=false
tech-stack:
  added:
    - aws-sdk-go-v2 service/s3 DeleteObjects (already vendored)
    - aws-sdk-go-v2 service/ssm DeleteParameter (already vendored)
  patterns:
    - Closure-threaded dryRun + cleanup deps (mirrors KMS/IAM/Schedules)
    - Best-effort destructive cleanup with per-resource error isolation
    - ParameterNotFound treated as success (canonical Phase 67 idiom)
key-files:
  created: []
  modified:
    - pkg/aws/sandbox.go
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_slack.go
    - internal/app/cmd/doctor_slack_transcript.go
    - internal/app/cmd/doctor_slack_inbound_test.go
    - internal/app/cmd/doctor_slack_transcript_test.go
decisions:
  - "Gate SSM cleanup on successful queue delete (avoid orphaning param when SQS errors; next doctor run reaps both)"
  - "Per-resource failures isolated; loop never aborts (matches KMS/IAM/Schedules cleanup pattern)"
  - "Empty stale prefix counts as 'deleted' (nothing to do = success)"
  - "TrimPrefix no-op detection: when sbID == queueName, skip SSM step rather than send malformed param name"
  - "Out of scope: third Slack stale check (channels) — explicitly excluded by plan constraints"
metrics:
  duration_seconds: 832
  duration_human: 14min
  tasks_completed: 2
  files_modified: 6
  commits: 2
  completed_date: 2026-05-04
requirements:
  - QUICK-7-INBOUND-CLEANUP
  - QUICK-7-TRANSCRIPT-CLEANUP
---

# Quick-7: km doctor --dry-run=false cleans up stale Slack resources — Summary

Extended km doctor's destructive-cleanup pattern (already present for KMS,
IAM, EventBridge schedules) to two Slack-related checks that previously
only detected and warned. Operators running `km doctor --dry-run=false`
can now reap orphan SQS queues + their SSM parameters and S3 transcript
prefixes left behind by failed `km destroy` runs, without resorting to
manual `aws sqs delete-queue` / `aws s3 rm --recursive` invocations.

## What changed

### New cleanup paths

**1. `checkSlackInboundStaleQueues` — Slack inbound stale queues (Plan 67-08 surface)**

`internal/app/cmd/doctor_slack.go:275` — signature now accepts `dryRun bool`
and `ssmDeleter SSMDeleterAPI`.

When `dryRun=false`, for every orphan SQS queue URL not present in the DDB
sandboxes table:

1. Call `kmaws.DeleteSlackInboundQueue` (best-effort — already swallows
   `QueueDoesNotExist`).
2. Parse the sandbox ID from the queue name (`{prefix}-slack-inbound-{id}.fifo`).
3. Best-effort `ssm.DeleteParameter` on `/sandbox/{id}/slack-inbound-queue-url`.
   `ssmtypes.ParameterNotFound` is treated as success (canonical
   `errors.As` pattern from `destroy_slack.go:191`). Other SSM errors
   are swallowed.
4. SSM cleanup is gated on successful queue delete to avoid orphaning the
   param when SQS errors — next doctor run reaps both.

Final WARN message: `"%d stale inbound queue(s) without DDB record (%d deleted, %d skipped)"`.

**2. `checkSlackTranscriptStaleObjects` — Slack transcript stale objects (Plan 68-11 surface)**

`internal/app/cmd/doctor_slack_transcript.go:156` — signature now accepts
`dryRun bool` and `s3Client` retyped to `kmaws.S3CleanupAPI`.

When `dryRun=false`, for every stale `transcripts/{sandbox_id}/` prefix
whose sandbox is no longer in DDB:

1. Paginate `ListObjectsV2` (no delimiter) via the new
   `listAllKeysUnderPrefix` helper to collect every key.
2. Batch `DeleteObjects` in groups of 1000 (S3 API limit), `Quiet=true`.
3. Per-prefix failures isolated — loop continues to the next prefix.
4. Empty prefix counts as cleaned (nothing to do = success).

Final WARN message: `"%d stale transcript prefix(es) (%d deleted, %d skipped, %d objects total)"`.

### New interfaces

**`pkg/aws/sandbox.go:58` — `S3CleanupAPI`**

```go
// S3CleanupAPI is the narrow interface for destructive S3 operations used
// by km doctor cleanup. Embeds S3ListAPI so callers can pass a single value
// to checks that both list and delete. The real *s3.Client satisfies this
// interface directly.
type S3CleanupAPI interface {
    S3ListAPI
    DeleteObjects(ctx context.Context, input *s3.DeleteObjectsInput, opts ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}
```

**`internal/app/cmd/doctor.go:128` — `SSMDeleterAPI`**

```go
// SSMDeleterAPI covers SSM DeleteParameter for stale-resource cleanup
// (Plan quick-7). The real *ssm.Client satisfies this interface directly.
type SSMDeleterAPI interface {
    DeleteParameter(ctx context.Context, params *ssm.DeleteParameterInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error)
}
```

### DoctorDeps additions

`internal/app/cmd/doctor.go:268-280`:

- New field: `SlackSSMDeleter SSMDeleterAPI` — used by the inbound stale
  queue check; nil = skip SSM cleanup.
- Retyped: `SlackTranscriptS3 kmaws.S3CleanupAPI` (was `S3ListAPI`).

Both are wired in `initRealDepsWithExisting` at lines 2469-2491:

- `deps.SlackSSMDeleter = ssmClientForSlack` — reuses the existing
  `*ssm.Client` constructed at line 2453 for the Plan 63-09 Slack SSM
  store (no new AWS client allocation).
- `deps.SlackTranscriptS3 = s3.NewFromConfig(awsCfg)` — unchanged at the
  call site; the real `*s3.Client` already satisfies the new
  `S3CleanupAPI`.

### `runDoctor` post-cleanup summary

`internal/app/cmd/doctor.go:1986` — `cleanupChecks` slice extended from
`["Stale KMS Keys", "Stale IAM Roles", "Stale Schedules"]` to also
include `"Slack inbound stale queues"` and `"Slack transcript stale
objects"` so the post-cleanup summary footer surfaces them after a
`--dry-run=false` run.

### Operator-facing change

Both checks' `Remediation` text now points operators at
`--dry-run=false` instead of giving manual aws-cli commands:

| Check | Old remediation | New remediation |
| --- | --- | --- |
| Slack inbound stale queues | `Run: aws sqs delete-queue --queue-url <url> for each stale queue listed above` | `Re-run with --dry-run=false to delete the orphan queues + their SSM parameters` |
| Slack transcript stale objects | `These prefixes belong to destroyed sandboxes. Cleanup is optional; remove with: aws s3 rm s3://<bucket>/<prefix> --recursive` | `Re-run with --dry-run=false to delete the orphan transcript objects` |

## Test additions

Two test files extended (no new files created):

**`internal/app/cmd/doctor_slack_inbound_test.go`** — new
`fakeSSMDeleter` helper + 4 new cases:

- `TestDoctor_SlackInboundStaleQueues_DryRunTrue_NoDestructiveCalls` —
  asserts zero DeleteQueue + zero SSM calls when dryRun=true.
- `TestDoctor_SlackInboundStaleQueues_DryRunFalse_HappyPath` — 2 orphans,
  2 DeleteQueue calls, 2 SSM DeleteParameter calls with the correct
  `/sandbox/sb-xN/slack-inbound-queue-url` names; message contains
  `(2 deleted, 0 skipped)`.
- `TestDoctor_SlackInboundStaleQueues_DryRunFalse_PartialFailure` — every
  DeleteQueue returns AccessDenied; message contains
  `(0 deleted, 2 skipped)` and zero SSM calls (gated on successful queue
  delete).
- `TestDoctor_SlackInboundStaleQueues_DryRunFalse_SSMParameterNotFound` —
  SSM returns `&ssmtypes.ParameterNotFound{}`; treated as success
  `(1 deleted, 0 skipped)`.

**`internal/app/cmd/doctor_slack_transcript_test.go`** — `fakeS3List`
extended with `DeleteObjects` (so it satisfies `S3CleanupAPI`) + 4 new
cases:

- `TestDoctor_SlackTranscriptStaleObjects_DryRunTrue_NoDestructiveCalls` —
  asserts zero DeleteObjects calls when dryRun=true.
- `TestDoctor_SlackTranscriptStaleObjects_DryRunFalse_HappyPath` — 1
  stale prefix with 3 objects, 1 DeleteObjects batch carrying 3 keys,
  message contains `(1 deleted, 0 skipped, 3 objects total)`.
- `TestDoctor_SlackTranscriptStaleObjects_DryRunFalse_PartialFailure` — 2
  stale prefixes; per-call deleteObjectsErrs (success then AccessDenied)
  → message contains `(1 deleted, 1 skipped,`.
- `TestDoctor_SlackTranscriptStaleObjects_DryRunFalse_MultiPage` — 1
  stale prefix with 1500 objects across 2 ListObjectsV2 pages → 2
  DeleteObjects batches (1000 + 500), 1500 total keys captured by the
  fake.

Two existing tests also updated to assert the new `--dry-run=false`
remediation text.

## Verification

```
go build ./...                                                                # 0
go test ./internal/app/cmd/... -run 'TestDoctor_SlackInboundStaleQueues|TestDoctor_SlackTranscriptStaleObjects' -count=1
ok  github.com/whereiskurt/klankrmkr/internal/app/cmd  0.942s
make build                                                                     # ./km v0.2.494 (ca02760)
./km --version                                                                 # km version v0.2.494 (ca02760)
```

All 18 inbound-stale + transcript-stale tests pass (10 pre-existing + 8
new). Full `go test ./internal/app/cmd/...` is green except for one
pre-existing baseline failure (`TestUnlockCmd_RequiresStateBucket`) that
fails identically before and after this plan (verified via `git stash`).

## Deviations from Plan

None. The plan was executed exactly as written. The `_ = dryRun;
_ = ssmDeleter` trick suggested for Task 1 was used as documented and
removed in Task 2.

One minor cleanup mid-execution: my first Edit to
`doctor_slack_transcript.go` left an orphaned tail (the original
"Status: CheckOK" return tail referencing `prefixes` after I
restructured the function). Detected immediately by build failure (well,
I caught it before running build), removed in a follow-up edit. No
commit pollution.

## Out-of-scope items (logged in `deferred-items.md`)

- `TestUnlockCmd_RequiresStateBucket` baseline failure (unrelated).
- `go vet` warning in `sidecars/http-proxy/httpproxy/transparent.go`
  (unrelated; predates this work).
- `km-config.example.yaml` working-tree drift (auto-regenerated by some
  side-effect; not staged in any quick-7 commit).

## Self-Check: PASSED

Verified each claim:

- File `pkg/aws/sandbox.go` modified (S3CleanupAPI added at line 58): FOUND.
- File `internal/app/cmd/doctor.go` modified (SSMDeleterAPI + SlackSSMDeleter): FOUND.
- File `internal/app/cmd/doctor_slack.go` modified (cleanup body): FOUND.
- File `internal/app/cmd/doctor_slack_transcript.go` modified (cleanup body + helper): FOUND.
- File `internal/app/cmd/doctor_slack_inbound_test.go` modified (4 new tests + fakeSSMDeleter): FOUND.
- File `internal/app/cmd/doctor_slack_transcript_test.go` modified (4 new tests + DeleteObjects fake): FOUND.
- File `.planning/quick/7-km-doctor-dry-run-false-cleans-up-stale-/deferred-items.md` created: FOUND.
- Commit `ca02760` (Task 1: refactor plumbing): FOUND in git log.
- Commit `afa03a4` (Task 2: cleanup logic + tests): FOUND in git log.
- `make build` succeeds with ldflags applied: VERIFIED (km v0.2.494).
- `go test ./internal/app/cmd/... -run 'TestDoctor_Slack...'` green: VERIFIED (18/18 pass).
