# Failed-sandbox discoverability — design note

**Status:** Proposal — pending operator sign-off, then `/gsd:add-phase`.
**Author:** triage of L2/L3 create failures, 2026-05-10.
**Date:** 2026-05-10.

## Problem

When `km create --remote` fails inside the create-handler Lambda, the
operator has no first-class path to the failure reason.

Today's incident: two sandboxes (`learn-465e52e9` alias L2,
`learn-ac6f33d2` alias L3) showed `failed` in `km list`. Both
discovery paths a reasonable operator would try are dead ends:

- `km status <id>` prints the metadata (sandbox id, profile, substrate,
  region, created-at) and stops at `Status: failed`. No reason field.
  See `internal/app/cmd/status.go:347-382` — every section after
  `Created At` is gated on `rec.Status == "running"` or on resource ARNs
  the failed sandbox never received.
- `km logs <id>` errors out with `ResourceNotFoundException: The
  specified log group does not exist`, because the per-sandbox log
  group `/{prefix}/sandboxes/{id}/` is created by EC2 userdata that
  never ran.

The actual failure reason existed the whole time, in
`/aws/lambda/km-create-handler` CloudWatch logs:

```
{"level":"error","sandbox_id":"learn-465e52e9",
 "output":"Error: provision Slack channel: channel #sb-l2
  (C0B1PR7JQU9) is archived; pick a different --alias or unarchive…"}
```

The create-handler already has the subprocess output in hand
(`cmd/create-handler/main.go:240-273`) and already calls
`UpdateSandboxStatusDynamo` to flip status to `failed`/`nocap`. It just
doesn't persist the reason or surface a way to fetch it later.

## Proposed approach

Two complementary changes, both small and orthogonal.

### 1. Persist `failure_reason` in DynamoDB at fail time

- Add fields to `pkg/aws/metadata.go` `SandboxMetadata`:
  - `FailureReason string` — the extracted error message, ≤1024 chars.
  - `FailedAt *time.Time` — wall-clock timestamp of the failure.
- New helper `UpdateSandboxStatusAndReasonDynamo` in
  `pkg/aws/sandbox_dynamo.go` that updates `status`, `failure_reason`,
  and `failed_at` in a single `UpdateItem` call. Mirrors the existing
  `UpdateSandboxStatusDynamo` shape; both stay supported (the new helper
  is used only on the failure branch).
- In `cmd/create-handler/main.go` failure branch, extract the reason:
  1. Scan `out` lines from the bottom; the first line starting with
     `Error:` (km's error format) is the reason. Trim to 1024 chars.
  2. If no `Error:` line found, take the last 1024 chars of `out`,
     prefix with `<no error line; tail of subprocess output> `.
  3. Call `UpdateSandboxStatusAndReasonDynamo` with the reason and
     `time.Now().UTC()`.
- Same path covers `nocap` (the existing branch already classifies
  capacity errors separately — same reason field applies).

Boundary discipline: only the create-handler (and any future failure-
emitting Lambda) writes the field. Read paths are pure consumers and
never mutate.

### 2. Display the reason in `km status`

In `internal/app/cmd/status.go` `printSandboxStatus`, after the
existing `Status:` line, when `rec.Status` is `failed` or `nocap`:

```
Status:      failed
Failure:     provision Slack channel: channel #sb-l2 (C0B1PR7JQU9) is archived
Failed At:   2026-05-10 11:05:44 AM EDT
```

If `FailureReason` is empty (older records, or the DDB write itself
failed) print `Failure:     <unknown — try km logs <id>>` so the
operator knows the next move.

`km list` is unchanged — its status column is already red-coded for
`failed`; adding a reason column would bloat the table. The operator
flow is `km list` → see red → `km status <id>` for the reason.

### 3. Extend `km logs` with a Lambda-log fallback

In the existing `km logs <id>` command:

- Today: calls `kmaws.GetLogEvents` on
  `/{prefix}/sandboxes/{id}/audit`. On `ResourceNotFoundException` it
  surfaces a raw AWS error.
- Proposed: on `ResourceNotFoundException`, fall back to
  `FilterLogEvents` on `/aws/lambda/{prefix}-create-handler` with
  `filterPattern: '{ $.sandbox_id = "<id>" }'` and a 24h window.
- Output prelude:
  `── per-sandbox log group not found; falling back to create-handler Lambda ──`
- Print events chronologically (timestamp + JSON `message` field).
- If the fallback is also empty:
  `No create-handler activity found for <id> in the last 24h. Try km status <id> for the persisted failure reason.`
- `--follow`/`-f` is a no-op in fallback mode (the failure has already
  occurred). Print a one-line note and exit cleanly.

Lambda log-group name follows the existing `resource_prefix` knob;
construct as `/aws/lambda/<prefix>-create-handler` (e.g.
`/aws/lambda/km-create-handler` for the default install,
`/aws/lambda/kph-create-handler` for a `KM_RESOURCE_PREFIX=kph`
install) so multi-instance installs work.

## Why this is the right shape

- **Cheap at read time.** The reason lives in the DDB record that
  `km status` already fetches; no extra AWS call for the common path.
- **One write, many reads.** The create-handler is already calling
  DynamoDB at fail time — adding two attributes to that call is free.
- **Fallback covers the gap when persistence fails.** If the DDB write
  itself fails (network, throttling, IAM regression), the operator
  still has `km logs <id>` → Lambda group as a deeper inspection path.
- **No schema migration.** Both new fields are `omitempty`; existing
  records read back as zero-value and `km status` prints the
  `<unknown>` hint.

## Test coverage

- `cmd/create-handler/main_test.go`: extend the failing-subprocess test
  to assert (a) the reason-extraction picks the last `Error:` line and
  trims to 1024, (b) `UpdateSandboxStatusAndReasonDynamo` is called with
  `failed`/`nocap` + non-empty reason + non-zero timestamp, (c) the
  no-`Error:` fallback prefixes with the explanatory marker.
- `pkg/aws/sandbox_dynamo_test.go`: round-trip test for the new helper
  (write → read → fields populated).
- `internal/app/cmd/status_test.go`: failed sandbox with reason →
  `Failure:` line printed; failed sandbox without reason → `<unknown>`
  hint printed; running sandbox → no `Failure:` line.
- `internal/app/cmd/logs_test.go`: per-sandbox group present →
  unchanged behavior; per-sandbox group missing + Lambda group has
  matching events → fallback prints with prelude; both empty → friendly
  hint message; `--follow` in fallback → exits cleanly with note.

## Out of scope

- **Backfilling L2/L3.** The two existing failed records won't get a
  reason. Manual `aws logs filter-log-events` (the same query the new
  fallback runs) is fine for one-off cleanup.
- **Lambda fallback for other handlers.** `ttl-handler`,
  `budget-enforcer`, `email-create-handler` follow the same pattern,
  but failures there are rare and out of the create-failure scope.
  Add when needed.
- **Failure-reason in `km list`.** Keeps the table narrow; operators
  drill into `km status` for the why.
- **Slack-archived-channel auto-recovery.** The actual L2/L3 root cause
  (archived `#sb-{alias}` channels from prior destroys) is its own
  decision: surface-only via this spec, auto-unarchive via a separate
  proposal if desired.

## Open questions

1. **Reason length cap.** 1024 chars is generous for one error line but
   small for a tail-of-output. If the no-`Error:` fallback truncates a
   stack trace that the operator needs, they can still go to
   `km logs <id>` (Lambda fallback) for the full stream. Keeping the
   DDB attribute small avoids item-size pressure.
2. **`failed_at` vs `created_at`.** Both timestamps are useful — keep
   them separate. `created_at` stays the provision-attempt start;
   `failed_at` is when the create-handler gave up. `km status` shows
   both.
3. **Lambda log-group retention.** Defaults vary; if the install has
   a 1-day retention on the Lambda group, the 24h fallback window may
   miss older failures. The persisted `failure_reason` covers this
   case — the fallback is supplementary.

## Decision needed

Operator approval to land this as a single GSD phase. Scope is one
package change (`pkg/aws/metadata.go` + `sandbox_dynamo.go`), three
command edits (`create-handler/main.go`, `cmd/status.go`,
`cmd/logs.go`), and four test files. No `km init --sidecars` required
— the change is data-plane and command-side only.
