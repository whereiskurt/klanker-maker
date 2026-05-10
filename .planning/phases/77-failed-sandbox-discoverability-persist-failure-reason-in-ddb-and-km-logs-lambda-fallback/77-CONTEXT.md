# Phase 77: failed-sandbox discoverability — Context

**Gathered:** 2026-05-10
**Status:** Ready for planning
**Source:** PRD Express Path (`docs/superpowers/specs/2026-05-10-failed-sandbox-discoverability-design.md`)

<domain>
## Phase Boundary

Make the failure reason for a failed `km create --remote` discoverable to the operator via the existing `km status` and `km logs` commands. Two complementary changes:

1. Persist `failure_reason` + `failed_at` to the sandbox DynamoDB record at create-handler fail time.
2. Surface those fields in `km status` and add a Lambda-log fallback to `km logs` when the per-sandbox log group does not exist.

**In scope:** create-handler failure branch, `km status` rendering, `km logs` fallback, DDB schema additions (additive only), test coverage for all four edges.

**Out of scope (explicit, from PRD):**
- Backfilling existing failed records (L2/L3) — manual `aws logs filter-log-events` is fine.
- Lambda fallback for other handlers (`ttl-handler`, `budget-enforcer`, `email-create-handler`).
- Adding a failure-reason column to `km list` (keeps table narrow).
- Slack-archived-channel auto-recovery (separate decision).

**Triggering incident:** L2 (`learn-465e52e9`) and L3 (`learn-ac6f33d2`) created on 2026-05-10 both failed with archived Slack `#sb-l2` channel; the actionable `Error:` line existed only in `/aws/lambda/km-create-handler` and was unreachable via `km status` / `km logs`.

</domain>

<decisions>
## Implementation Decisions (LOCKED — from PRD)

### DynamoDB schema (`pkg/aws/metadata.go` `SandboxMetadata`)
- Add `FailureReason string` (≤1024 chars, `omitempty`).
- Add `FailedAt *time.Time` (`omitempty`).
- Both fields read back as zero-value on existing records — no migration.

### DDB writer helper (`pkg/aws/sandbox_dynamo.go`)
- New `UpdateSandboxStatusAndReasonDynamo` — single `UpdateItem` updating `status`, `failure_reason`, `failed_at`.
- Mirrors existing `UpdateSandboxStatusDynamo` shape; both stay supported.
- The new helper is used **only** on the failure branch; success path stays on the existing helper.

### create-handler failure branch (`cmd/create-handler/main.go:240-273`)
- Extract reason from `out` (subprocess output):
  1. Scan from bottom; first line starting with `Error:` is the reason. Trim to 1024 chars.
  2. If no `Error:` line: take last 1024 chars of `out`, prefix with `<no error line; tail of subprocess output> `.
- Call `UpdateSandboxStatusAndReasonDynamo(reason, time.Now().UTC())`.
- Same path covers both `failed` and `nocap` status branches.

### Boundary discipline
- **Only** the create-handler (and any future failure-emitting Lambda) writes `FailureReason`/`FailedAt`.
- Read paths (`km status`, `km logs`) are pure consumers; never mutate.

### `km status` rendering (`internal/app/cmd/status.go` `printSandboxStatus`)
- After existing `Status:` line, when `rec.Status` ∈ {`failed`, `nocap`}:
  - `Failure:     <FailureReason>`
  - `Failed At:   <FailedAt formatted as existing timestamps>`
- If `FailureReason` empty (older record, or DDB write itself failed):
  - `Failure:     <unknown — try km logs <id>>`
- Running sandboxes: no `Failure:` line.

### `km status` formatting alignment
- Match existing `Status:` / `Created At:` column alignment exactly.
- `Failed At` timestamp uses the same renderer as `Created At`.

### `km list` — UNCHANGED
- Status column already red-codes `failed`. No reason column.
- Operator flow: `km list` → see red → `km status <id>` for the why.

### `km logs` fallback (existing command)
- Today: `kmaws.GetLogEvents` on `/{prefix}/sandboxes/{id}/audit`. On `ResourceNotFoundException` → raw AWS error.
- Proposed: on `ResourceNotFoundException` → fall back to `FilterLogEvents` on `/aws/lambda/{prefix}-create-handler` with:
  - `filterPattern: '{ $.sandbox_id = "<id>" }'`
  - 24h window
- Output prelude: `── per-sandbox log group not found; falling back to create-handler Lambda ──`
- Print events chronologically: `<timestamp> <JSON .message field>`.
- Both empty → `No create-handler activity found for <id> in the last 24h. Try km status <id> for the persisted failure reason.`
- `--follow`/`-f` in fallback mode → no-op; print one-line note and exit cleanly.

### Multi-instance support
- Lambda group name: `/aws/lambda/<prefix>-create-handler` where `<prefix>` is `KM_RESOURCE_PREFIX` (default `km`).
- Honors the existing `resource_prefix` knob (CLAUDE.md § Multi-instance).

### No infrastructure churn
- No `km init --sidecars` required — change is data-plane and command-side only.
- No new IAM permissions: create-handler already writes DDB; `km logs` operator role already reads CloudWatch.

### Test strategy (LOCKED from PRD § Test coverage)
- `cmd/create-handler/main_test.go`:
  - Reason-extraction picks last `Error:` line, trims to 1024.
  - `UpdateSandboxStatusAndReasonDynamo` called with `failed`/`nocap` + non-empty reason + non-zero timestamp.
  - No-`Error:` fallback prefixes with explanatory marker.
- `pkg/aws/sandbox_dynamo_test.go`:
  - Round-trip test for new helper (write → read → fields populated).
- `internal/app/cmd/status_test.go`:
  - Failed sandbox with reason → `Failure:` line printed.
  - Failed sandbox without reason → `<unknown>` hint printed.
  - Running sandbox → no `Failure:` line.
- `internal/app/cmd/logs_test.go`:
  - Per-sandbox group present → unchanged behavior.
  - Per-sandbox group missing + Lambda group has matching events → fallback prints with prelude.
  - Both empty → friendly hint message.
  - `--follow` in fallback → exits cleanly with note.

### Claude's Discretion
The PRD locks the *what*. The following implementation details are at planner discretion provided they honor the locked decisions:
- Exact ordering of frontmatter / wave assignment for plans.
- Internal helper signatures inside `cmd/create-handler/main.go` for reason extraction (e.g. extract a `extractFailureReason(out string) string` helper or inline).
- Time renderer choice for `Failed At` (must match existing `Created At` style — pick the same one).
- Mock/stub strategy in tests (DDB client interface is already in `pkg/aws`).
- Whether the Lambda-fallback CloudWatch client is a new helper in `pkg/aws` or inline in `internal/app/cmd/logs.go` (prefer `pkg/aws` for consistency, but planner can decide based on the existing module surface).

</decisions>

<specifics>
## Specific Ideas (from PRD)

### Concrete failure example (drives the reason-extraction test fixture)
```
{"level":"error","sandbox_id":"learn-465e52e9",
 "output":"Error: provision Slack channel: channel #sb-l2
  (C0B1PR7JQU9) is archived; pick a different --alias or unarchive…"}
```
The reason extraction must reduce this to:
```
Error: provision Slack channel: channel #sb-l2 (C0B1PR7JQU9) is archived; pick a different --alias or unarchive…
```

### Expected `km status` output for a failed sandbox
```
Status:      failed
Failure:     provision Slack channel: channel #sb-l2 (C0B1PR7JQU9) is archived
Failed At:   2026-05-10 11:05:44 AM EDT
```

### File touch list (planner verifies, but design note targets these)
- `pkg/aws/metadata.go` — add fields
- `pkg/aws/sandbox_dynamo.go` — new helper
- `pkg/aws/sandbox_dynamo_test.go` — round-trip test
- `cmd/create-handler/main.go` — failure branch extraction + write
- `cmd/create-handler/main_test.go` — extraction + write tests
- `internal/app/cmd/status.go` — render `Failure:`/`Failed At:` lines
- `internal/app/cmd/status_test.go` — render tests
- `internal/app/cmd/logs.go` — Lambda fallback
- `internal/app/cmd/logs_test.go` — fallback tests

</specifics>

<deferred>
## Deferred Ideas

From PRD § Out of scope:
- L2/L3 backfill of existing failed records.
- Lambda-log fallback for `ttl-handler`, `budget-enforcer`, `email-create-handler`.
- Failure-reason column in `km list` table.
- Slack-archived-channel auto-recovery (separate proposal).

From PRD § Open questions (resolved by PRD):
- 1024-char reason cap is intentional; longer dumps live in `km logs` Lambda fallback.
- `failed_at` and `created_at` both kept (different semantics).
- Lambda log-group retention may shrink the 24h fallback window — persisted `failure_reason` covers this; fallback is supplementary.

</deferred>

---

*Phase: 77-failed-sandbox-discoverability-persist-failure-reason-in-ddb-and-km-logs-lambda-fallback*
*Context gathered: 2026-05-10 via PRD Express Path*
*PRD source: `docs/superpowers/specs/2026-05-10-failed-sandbox-discoverability-design.md` (committed at ff5045e)*
