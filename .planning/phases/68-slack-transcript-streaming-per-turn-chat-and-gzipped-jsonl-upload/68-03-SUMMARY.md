---
phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
plan: 03
subsystem: infra
tags: [terraform, dynamodb, slack, transcript-streaming, config, viper]

# Dependency graph
requires:
  - phase: 68-00
    provides: Wave 0 t.Skip stub for TestConfig_GetSlackStreamMessages_* (replaced here with real assertions)
  - phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
    provides: dynamodb-slack-threads/v1.0.0 module pattern + GetSlackThreadsTableName helper pattern (mirrored verbatim with new schema)
  - phase: 66-multi-instance-support-configurable-resource-prefix-and-email-subdomain
    provides: ResourcePrefix field + GetResourcePrefix helper (used to derive table name)
provides:
  - "Terraform module infra/modules/dynamodb-slack-stream-messages/v1.0.0/ — provisions {prefix}-slack-stream-messages DDB table"
  - "Config.SlackStreamMessagesTableName field + Config.GetSlackStreamMessagesTableName() helper (nil-safe, prefix-aware, override-aware)"
  - "viper key slack_stream_messages_table_name with default km-slack-stream-messages, merged from km-config.yaml"
  - "Resolution of RESEARCH Open Question 1: table suffix is -slack-stream-messages (not -km-slack-stream-messages)"
affects:
  - "68-06 (IAM): wires sandbox EC2 role dynamodb:PutItem against this table"
  - "68-10 (provisioning): injects KM_SLACK_STREAM_TABLE env var via km create"
  - "68-11 (doctor): adds slack_transcript_table_exists check"
  - "68 Wave 1 live Terragrunt wiring (out of scope here)"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "DDB module mirror: copy Phase 67 dynamodb-slack-threads/v1.0.0 layout, keep PAY_PER_REQUEST + native TTL + SSE"
    - "Config helper mirror: nil-safe → default-name; explicit-field-wins; otherwise prefix + suffix"

key-files:
  created:
    - infra/modules/dynamodb-slack-stream-messages/v1.0.0/main.tf
    - infra/modules/dynamodb-slack-stream-messages/v1.0.0/variables.tf
    - infra/modules/dynamodb-slack-stream-messages/v1.0.0/outputs.tf
  modified:
    - internal/app/config/config.go
    - internal/app/config/config_stream_table_test.go

key-decisions:
  - "Table name is {prefix}-slack-stream-messages (NOT {prefix}-km-slack-stream-messages); resolves Open Question 1 from CONTEXT.md ambiguity"
  - "Reused Phase 67 dynamodb-slack-threads layout 1:1 except resource name + key schema + Component tag (km-slack-transcript)"
  - "Stuck with PAY_PER_REQUEST billing — transcript streaming traffic is bursty per active turn, on-demand avoids capacity waste"
  - "Added ManagedBy=klankermaker tag (Phase 67 only carried Component tag); cheap correctness improvement for resource discovery"

patterns-established:
  - "DDB native TTL on Number attribute (Unix epoch seconds) — writers must compute now_unix + retention_seconds; doc warning embedded in module to prevent ISO8601 mistakes"
  - "Config helper: nil-receiver branch returns the literal default name (not via GetResourcePrefix) so callers in error paths still get a sensible name without crashing"
  - "Module table_name variable carries default = {prefix-default}-slack-stream-messages so direct apply works in dev; Terragrunt stack overrides with computed prefix"

requirements-completed: []

# Metrics
duration: 3min
completed: 2026-05-03
---

# Phase 68 Plan 03: Slack Stream Messages DDB Module + Config Helper Summary

**New `dynamodb-slack-stream-messages/v1.0.0` Terraform module (channel_id+slack_ts schema, native TTL, SSE) plus Config.GetSlackStreamMessagesTableName helper resolving table-name to `{prefix}-slack-stream-messages` (RESEARCH Open Question 1 resolved).**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-05-03T19:55:55Z
- **Completed:** 2026-05-03T19:58:54Z
- **Tasks:** 2 (1 plain auto, 1 TDD)
- **Files modified:** 5 (3 created, 2 modified)

## Accomplishments

- **DDB module v1.0.0 ready for live wiring:** `infra/modules/dynamodb-slack-stream-messages/v1.0.0/` provisions a PAY_PER_REQUEST table with PK=`channel_id` (S), SK=`slack_ts` (S), native TTL on `ttl_expiry` (N, Unix epoch), SSE enabled, tagged `Component=km-slack-transcript` + `ManagedBy=klankermaker`. `terraform fmt -check` and `terraform validate` both pass.
- **Config helper resolves table name across all three branches:** `Config.GetSlackStreamMessagesTableName()` returns `"km-slack-stream-messages"` on nil receiver, the explicit `SlackStreamMessagesTableName` field if set, otherwise `GetResourcePrefix() + "-slack-stream-messages"`. viper key `slack_stream_messages_table_name` registered in `Load()` defaults + km-config.yaml merge list.
- **Wave 0 stubs promoted:** All four `t.Skip` stubs in `internal/app/config/config_stream_table_test.go` replaced with real assertions; 4/4 PASS; full config suite regression-clean.
- **Open Question 1 resolved with rationale:** Plan elected `{prefix}-slack-stream-messages` over CONTEXT.md's `{prefix}-km-slack-stream-messages` (which would yield the doubled `km-km-...` prefix); mirrors Phase 67's `{prefix}-slack-threads` exactly. Documented in code comments and below in Decision Record so 68-CONTEXT.md / STATE.md can be amended on the next pass.

## Task Commits

Each task was committed atomically:

1. **Task 1: Create dynamodb-slack-stream-messages Terraform module v1.0.0** — `1d3de1b` (feat)
2. **Task 2 RED: Add failing tests for GetSlackStreamMessagesTableName** — `3e0bec6` (test, TDD red)
2. **Task 2 GREEN: Implement field + helper + viper key wiring** — `3623317` (feat, TDD green)

**Plan metadata:** _pending_ (docs: complete plan — final commit at end of summary)

_Note: Task 2 is `tdd="true"`, so it has separate RED + GREEN commits per the TDD execution flow. No REFACTOR commit needed — the GREEN implementation was already minimal._

## Files Created/Modified

- `infra/modules/dynamodb-slack-stream-messages/v1.0.0/main.tf` — DDB table resource with PK=channel_id, SK=slack_ts, TTL on ttl_expiry, SSE enabled, PAY_PER_REQUEST billing, Component+ManagedBy tags
- `infra/modules/dynamodb-slack-stream-messages/v1.0.0/variables.tf` — `table_name` (default `km-slack-stream-messages`) + `tags` (default `{}`)
- `infra/modules/dynamodb-slack-stream-messages/v1.0.0/outputs.tf` — `table_name` and `table_arn` outputs
- `internal/app/config/config.go` — added `SlackStreamMessagesTableName string` field, viper SetDefault registration, km-config.yaml merge key, struct-literal load wiring, and `GetSlackStreamMessagesTableName()` method (nil-safe, override-aware, prefix-derived)
- `internal/app/config/config_stream_table_test.go` — Wave 0 t.Skip stubs replaced with 4 real assertions (DefaultPrefix, CustomPrefix, ExplicitOverride, NilReceiver)

## Decisions Made

### Decision Record — RESEARCH Open Question 1 (table naming)

CONTEXT.md originally specified `{prefix}-km-slack-stream-messages`. With the default `ResourcePrefix="km"`, that pattern would yield `km-km-slack-stream-messages` — clearly a spec ambiguity (the `km-` was carried over from non-prefix-aware language).

**Resolution:** This plan adopts `{prefix}-slack-stream-messages`, mirroring Phase 67's `{prefix}-slack-threads` pattern. With default prefix this yields `km-slack-stream-messages` (clean, no doubled prefix). Documented inline in `GetSlackStreamMessagesTableName()` doc comment so future readers don't reopen the question.

**Action item for next pass:** STATE.md "Decisions" section + 68-CONTEXT.md "Resource naming" section should be amended to reference `{prefix}-slack-stream-messages` exclusively. Plans 06 / 10 / 11 will read the helper, so they'll inherit the correct name regardless.

### Other decisions

- **PAY_PER_REQUEST billing retained** (matches Phase 67 dynamodb-slack-threads). Per-turn writes are bursty — there's no steady baseline to justify provisioned capacity.
- **`ManagedBy=klankermaker` tag added** beyond Phase 67's tag set. Cheap correctness improvement: makes the table easy to find in cross-account resource discovery.
- **`point_in_time_recovery.enabled = false`** kept (matches Phase 67). Stream messages are recoverable from the gzipped JSONL transcript in S3; PITR adds cost without recovery benefit.

## Deviations from Plan

None - plan executed exactly as written.

The plan called out one in-scope formatting cascade ahead of time: adding `SlackStreamMessagesTableName` (the longest field name in the struct literal) shifts the column alignment for adjacent fields. `gofmt -w` re-aligned the literal block to the new longest column; this is the canonical Go formatter doing its job, not a deviation. Only files that the plan listed as `files_modified` were touched.

## Issues Encountered

- **`terraform init` left `.terraform/` and `.terraform.lock.hcl` artifacts** in the new module directory after `terraform validate`. Both paths are already covered by the repo `.gitignore` (lines for `.terraform/` and `.terraform.lock.hcl`), so neither was staged. Verified with `git check-ignore`. The Phase 67 `dynamodb-slack-threads/v1.0.0/` module has the same residual artifacts from its plan execution, so this is consistent with project convention.
- **Pre-existing gofmt drift in `config.go`** (e.g. `DNSParentAccountID    string` had trailing tab spacing not produced by gofmt). Adding the new field forced gofmt to re-evaluate alignment for the whole struct literal block — running `gofmt -w` cleaned both my changes and the pre-existing drift in adjacent regions. The drift was within the same struct-literal block touched by my change, so qualifies as in-scope per the scope-boundary rule (cascade from new field). Tests still pass; no behavior change.

## User Setup Required

None — no external service configuration required for this plan.

**Future operator action (per CONTEXT.md "Deploy convention", deferred to a later plan):** After 68 Wave 1 wires the live Terragrunt stack to this module, operators must run `make build && km init --sidecars && km init` to provision the `{prefix}-slack-stream-messages` table. Plan 06 wires sandbox EC2 IAM `dynamodb:PutItem` against this table; until both Plan 03 (table) and Plan 06 (IAM) ship and `km init` runs, sandbox-side `km-slack record-mapping` writes will fail. Plan 11 adds the doctor check that surfaces this gap.

## Next Phase Readiness

- ✅ Plan 06 can read `Config.GetSlackStreamMessagesTableName()` to populate IAM resource ARN templates
- ✅ Plan 10 can inject `KM_SLACK_STREAM_TABLE=$(cfg.GetSlackStreamMessagesTableName())` into the per-sandbox userdata env file
- ✅ Plan 11 doctor check has a stable name to query for `DescribeTable`
- ⚠️ STATE.md "Decisions" section + 68-CONTEXT.md "Resource naming" should reference the resolved suffix `{prefix}-slack-stream-messages` on the next pass (action recorded above; not blocking)

## Self-Check: PASSED

Verified post-creation:

- ✅ `infra/modules/dynamodb-slack-stream-messages/v1.0.0/main.tf` — FOUND
- ✅ `infra/modules/dynamodb-slack-stream-messages/v1.0.0/variables.tf` — FOUND
- ✅ `infra/modules/dynamodb-slack-stream-messages/v1.0.0/outputs.tf` — FOUND
- ✅ `internal/app/config/config.go` modified (SlackStreamMessagesTableName field + GetSlackStreamMessagesTableName method present)
- ✅ `internal/app/config/config_stream_table_test.go` modified (4 real assertions, no t.Skip)
- ✅ Commit `1d3de1b` (Task 1: feat) — FOUND
- ✅ Commit `3e0bec6` (Task 2 RED: test) — FOUND
- ✅ Commit `3623317` (Task 2 GREEN: feat) — FOUND
- ✅ `go build ./...` clean
- ✅ `go test ./internal/app/config/... -count=1` clean (full suite)
- ✅ `terraform fmt -check` clean
- ✅ `terraform validate` succeeds

---
*Phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload*
*Plan: 03*
*Completed: 2026-05-03*
