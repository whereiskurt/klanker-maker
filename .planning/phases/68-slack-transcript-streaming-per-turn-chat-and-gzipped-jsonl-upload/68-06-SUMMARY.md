---
phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
plan: 06
subsystem: infra
tags: [terraform, iam, ec2, lambda, slack, s3, dynamodb]

# Dependency graph
requires:
  - phase: 67
    provides: ec2spot_slack_inbound_sqs IAM pattern; lambda-slack-bridge module shape; resource_prefix variable
  - phase: 68-00
    provides: Wave-0 stub seeding for Phase 68 testdata
  - phase: 68-03
    provides: dynamodb-slack-stream-messages module + Config.GetSlackStreamMessagesTableName helper
provides:
  - "ec2spot module gains var.artifacts_bucket + var.slack_stream_messages_table_name (both default \"\")"
  - "ec2spot module gains aws_iam_role_policy.ec2spot_slack_transcript_s3 (PutObject on transcripts/{sandbox_id}/*)"
  - "ec2spot module gains aws_iam_role_policy.ec2spot_slack_transcript_ddb (PutItem on stream-messages table)"
  - "lambda-slack-bridge module gains var.artifacts_bucket + aws_iam_role_policy.slack_bridge_transcript_s3_read (GetObject + HeadObject on transcripts/*)"
  - "Compiler emits artifacts_bucket + slack_stream_messages_table_name into ec2spot terragrunt module_inputs"
  - "userDataParams.SlackStreamMessagesTableName field for downstream template consumers"
affects: [68-08, 68-09, 68-10]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Per-sandbox S3 prefix scoping (transcripts/{sandbox_id}/*) — prevents cross-sandbox writes"
    - "Cross-sandbox bridge read with application-layer prefix enforcement (handler.go envelope validation is the security boundary; IAM grant is intentionally broad)"
    - "Empty-string default for new module variables — gates the policy via count = ... > 0 ? 1 : 0 so disabled-feature sandboxes skip the resource entirely"

key-files:
  created: []
  modified:
    - infra/modules/ec2spot/v1.0.0/variables.tf
    - infra/modules/ec2spot/v1.0.0/main.tf
    - infra/modules/lambda-slack-bridge/v1.0.0/variables.tf
    - infra/modules/lambda-slack-bridge/v1.0.0/main.tf
    - pkg/compiler/userdata.go
    - pkg/compiler/service_hcl.go

key-decisions:
  - "ec2spot transcript IAM policies are gated on var.artifacts_bucket != \"\" (S3) and var.slack_stream_messages_table_name != \"\" (DDB) so existing callers compile unchanged. Plan 10 wires real values via km create."
  - "Bridge S3 read policy is broad (transcripts/*) — application-layer per-sandbox prefix enforcement in handler.go is the security boundary, matching the pattern documented in RESEARCH Pitfall 4."
  - "SlackStreamMessagesTableName follows the Phase 67 SlackThreadsTableName precedent: env var (KM_SLACK_STREAM_TABLE) with sane default; not pulled from Config in the compiler. Plan 10 propagates Config.GetSlackStreamMessagesTableName() into the env var via km create."
  - "service_hcl.go (not userdata.go) was the correct location for ec2spot terragrunt module wiring — the plan frontmatter listed only userdata.go but the IAM policies in Tasks 1-2 are inert without service_hcl.go template emission. Edited both as deviation Rule 3."

patterns-established:
  - "Phase 68 transcript IAM scoping: sandbox-side narrow (sandbox_id-prefix), bridge-side broad (cross-sandbox transcripts/*), with application-layer prefix check on bridge"
  - "ec2spot variable + count-gated policy pairing: opt-in via non-empty variable, omit-on-default-empty"

requirements-completed: []  # plan frontmatter requirements: [] — none

# Metrics
duration: 8 min
completed: 2026-05-03
---

# Phase 68 Plan 06: Transcript IAM Grants Summary

**Provisioning-only plan delivering the four IAM grants Plans 08/09/10 will consume: sandbox-side S3 PutObject + DDB PutItem (transcript upload + stream-messages map), and bridge-side S3 GetObject/HeadObject (cross-sandbox transcript fetch with application-layer prefix enforcement).**

## Performance

- **Duration:** 8 min
- **Started:** 2026-05-03T20:04:56Z
- **Completed:** 2026-05-03T20:13:21Z
- **Tasks:** 4
- **Files modified:** 6

## Accomplishments

- Two new ec2spot Terraform variables (`artifacts_bucket`, `slack_stream_messages_table_name`) with empty-string defaults — additive, no callers need updating immediately.
- Two new ec2spot IAM policies (`ec2spot_slack_transcript_s3` for sandbox-scoped PutObject, `ec2spot_slack_transcript_ddb` for stream-messages PutItem) gated on the new variables being non-empty.
- New lambda-slack-bridge variable + IAM policy granting cross-sandbox `s3:GetObject` + `s3:HeadObject` on the `transcripts/*` prefix; application-layer prefix enforcement in handler.go is the security boundary.
- Compiler now emits both new variables into the per-sandbox terragrunt `module_inputs` block (via `service_hcl.go`'s `ec2ServiceHCLTemplate`), pulling values from `KM_ARTIFACTS_BUCKET` (NetworkConfig) and `KM_SLACK_STREAM_TABLE` env (mirroring the Phase 67 SlackThreadsTableName precedent).

## Task Commits

Each task was committed atomically:

1. **Task 1: Add ec2spot variables** — `91ea6b1` (feat)
2. **Task 2: Add ec2spot IAM policies (S3 PutObject + DDB PutItem)** — `b1dadc8` (feat)
3. **Task 3: Add lambda-slack-bridge S3 transcript read policy** — committed via `64afe02` (sibling executor commit; see deviation #2 below)
4. **Task 4: Wire artifacts_bucket + slack_stream_messages_table_name through compiler** — `ee73ac2` (feat)

**Plan metadata commit:** to follow this SUMMARY (separate `docs(68-06)` commit).

## Files Created/Modified

- `infra/modules/ec2spot/v1.0.0/variables.tf` — added `artifacts_bucket` + `slack_stream_messages_table_name` variables (default `""`).
- `infra/modules/ec2spot/v1.0.0/main.tf` — added `aws_iam_role_policy.ec2spot_slack_transcript_s3` and `aws_iam_role_policy.ec2spot_slack_transcript_ddb`, both gated on `local.total_ec2spot_count > 0` AND the corresponding variable being non-empty.
- `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` — added `artifacts_bucket` variable.
- `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` — added `aws_iam_role_policy.slack_bridge_transcript_s3_read` (count-gated on `var.artifacts_bucket != ""`); `replace_triggered_by` on the Lambda function intentionally not modified (RESEARCH Pitfall 4).
- `pkg/compiler/userdata.go` — added `userDataParams.SlackStreamMessagesTableName` field, populated unconditionally from `KM_SLACK_STREAM_TABLE` env (default `km-slack-stream-messages`).
- `pkg/compiler/service_hcl.go` — added `ec2HCLParams.ArtifactsBucket` + `ec2HCLParams.SlackStreamMessagesTableName` fields, emitted into `ec2ServiceHCLTemplate`'s `module_inputs` block; populated in `generateEC2ServiceHCL` from `NetworkConfig.ArtifactsBucket` (with env fallback) and `KM_SLACK_STREAM_TABLE` env.

## Decisions Made

1. **Variable defaults are empty strings, not omitted.** Lets `count = var.X != "" ? 1 : 0` cleanly skip the resource for callers that haven't yet wired the new inputs (Plan 10 ships that wiring). No breaking change for in-flight terragrunt plans.

2. **Sandbox-side narrow / bridge-side broad IAM scoping.** Mirrors RESEARCH Open Question 4 resolution: the bridge enforces per-sandbox `transcripts/{sandbox_id}/*` prefix via `handler.go` envelope validation BEFORE any GetObject call. The IAM grant exists to satisfy AWS, not as the security boundary. This pattern (explicit application-layer check + broader IAM) is documented in the new policy comment for future maintainers.

3. **Naming: `${var.resource_prefix}-${var.sandbox_id}-slack-transcript-{s3,ddb}`** — matches the Phase 67 `${var.resource_prefix}-${var.sandbox_id}-slack-inbound` precedent. Rejected the plan's suggested `${var.km_label}` form because Phase 67 standardized on `resource_prefix` (multi-instance aware).

4. **`SlackStreamMessagesTableName` reads `KM_SLACK_STREAM_TABLE` env var, NOT `Config.GetSlackStreamMessagesTableName()`.** The plan's action description suggested calling the helper, but `generateUserData`/`generateEC2ServiceHCL` don't have a `*Config` injected. Following the Phase 67 SlackThreadsTableName precedent (env var pulled from operator-side config and re-injected into compiler env) keeps the wiring consistent. Plan 10 (km create env injection) is responsible for setting the env var from `Config.GetSlackStreamMessagesTableName()`.

5. **RESEARCH Open Question 3 resolved.** `artifacts_bucket` was NOT a pre-existing variable on `ec2spot` (confirmed via grep on the unmodified module). Adding it in this plan is correct; lambda-slack-bridge also needed the same variable added (it had artifacts under `nonces_table_arn` etc. but no artifacts_bucket).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Plan listed only `pkg/compiler/userdata.go` but ec2spot terragrunt wiring lives in `pkg/compiler/service_hcl.go`**
- **Found during:** Task 4 (compiler wiring).
- **Issue:** Plan frontmatter `files_modified` listed only `pkg/compiler/userdata.go`, and Task 4's action text said "Identify the template location where ec2spot inputs are built. Add the two new fields to that template." But `userdata.go` is the user-data shell-script template, NOT the terragrunt service.hcl template. The ec2spot module call lives in `service_hcl.go`'s `ec2ServiceHCLTemplate` (around line 75), with parameters in `ec2HCLParams` (line 424). Without editing `service_hcl.go`, the IAM policies added in Tasks 1-2 would be inert: `var.artifacts_bucket` and `var.slack_stream_messages_table_name` would always be the empty-string defaults.
- **Fix:** Edited `service_hcl.go` to (a) add `ArtifactsBucket` + `SlackStreamMessagesTableName` to `ec2HCLParams`, (b) add the corresponding `module_inputs` lines to `ec2ServiceHCLTemplate`, (c) populate the fields in `generateEC2ServiceHCL` from `NetworkConfig.ArtifactsBucket` and `KM_SLACK_STREAM_TABLE` env. Also added the field to `userDataParams` in `userdata.go` (per the plan's literal verify check) so downstream user-data template consumers can use it later.
- **Files modified:** `pkg/compiler/service_hcl.go` (in addition to plan's `pkg/compiler/userdata.go`).
- **Verification:** Plan-spec verify passes (`go build ./... && grep -q "SlackStreamMessagesTableName" pkg/compiler/userdata.go`); manually inspected `module_inputs` template emits both new fields.
- **Committed in:** `ee73ac2` (Task 4 commit).

**2. [Concurrency artifact] My Task 2 + Task 3 commits inadvertently swept in unrelated files staged by sibling executors on the shared branch.**
- **Found during:** Task 2 commit (`b1dadc8`) and Task 3 staging.
- **Issue:** The branch `gsd/phase-67-slack-inbound` is being concurrently driven by multiple executor agents (Plans 68-04, 68-05, 68-06 ran in parallel per the Wave 2 dispatch). When I ran `git add infra/modules/ec2spot/v1.0.0/main.tf && git commit`, three additional files were already index-staged by the 68-04/68-05 executors and were swept into `b1dadc8`: `internal/app/cmd/agent.go` and `internal/app/cmd/agent_transcript_test.go` (Plan 68-04 follow-on) plus the documented partial. Then before I could explicitly stage Task 3, the 68-04 docs commit (`64afe02` by a sibling) swept up my staged `infra/modules/lambda-slack-bridge/v1.0.0/{main,variables}.tf` Task 3 changes; my standalone commit attempt then found nothing to commit and exited 1.
- **Fix:** None — the work IS committed and present in HEAD; the only damage is that commits 64afe02 and b1dadc8 carry mixed plan attribution. All Plan 68-06 changes are verifiable in the tree (`grep` checks all pass) and reachable from HEAD.
- **Files affected:** `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` + `variables.tf` (Task 3 work) committed in `64afe02` instead of a dedicated Task 3 commit; `internal/app/cmd/agent.go` + `agent_transcript_test.go` (Plan 68-04 work) committed in `b1dadc8` instead of their own 68-04 commit.
- **Verification:** Re-ran all four plan-spec greps post-HEAD-advance; all pass. `go build ./...` clean.
- **Note for future executors on shared branches:** run `git status --short` before each commit to detect concurrent staging, and stage by explicit path only. (I did stage by path; the issue was that unrelated files were already pre-staged in the shared index.)

---

**Total deviations:** 1 auto-fixed (Rule 3 - Blocking) + 1 concurrency artifact (no functional impact).
**Impact on plan:** All success criteria met. The concurrency artifact only affects commit attribution, not deliverables. Sibling executors should be aware that the shared index can sweep their staged work into another agent's commit; this is a workflow artifact of parallel-executor wave dispatch.

## Issues Encountered

- Two pre-existing test failures in `pkg/compiler/userdata_notify_test.go` (`TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`, `TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime`) confirmed unchanged by Plan 68-06's changes (verified via `git stash && go test … && git stash pop` diff). Both are already documented in `deferred-items.md` — out of scope per scope-boundary rule. Appended a Plan 68-06 confirmation note to deferred-items.md.

## User Setup Required

None — provisioning-only plan; all inputs are operator-side config the system already manages. Plan 10 will surface any operator action needed (likely none beyond Plan 03's existing `km init`-driven DDB provisioning).

## Next Phase Readiness

Plans 08, 09, and 10 can now consume these grants:

- **Plan 08 (bridge S3 read):** `slack_bridge_transcript_s3_read` policy live; bridge handler can `s3:GetObject`/`s3:HeadObject` on `transcripts/*` once the bridge module is re-applied with the new `artifacts_bucket` input wired into `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` (NOT done in this plan — it's a live-Terragrunt change Plan 08 will own).
- **Plan 09 (hook script):** Sandbox can now `s3:PutObject` on `transcripts/{sandbox_id}/*` and `dynamodb:PutItem` on the stream-messages table whenever the new ec2spot variables are non-empty.
- **Plan 10 (km create env injection):** Inject `KM_ARTIFACTS_BUCKET` (already present) + `KM_SLACK_STREAM_TABLE` (NEW — set from `Config.GetSlackStreamMessagesTableName()`) into the sandbox env via `km create` so the compiler picks up Phase 66 multi-instance prefix overrides at sandbox-creation time.

**Operator follow-up required (Plan 08 territory, not this plan):** `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` needs `artifacts_bucket = get_env("KM_ARTIFACTS_BUCKET", "")` (or equivalent) added to the `inputs = {…}` block so the new bridge S3 read policy actually receives a non-empty bucket name and the policy is created. Without this, the policy will be conditionally omitted (count = 0) and the bridge will get 403 on transcript fetches.

## Self-Check

- `infra/modules/ec2spot/v1.0.0/variables.tf` exists with new variables: PASS
- `infra/modules/ec2spot/v1.0.0/main.tf` contains `ec2spot_slack_transcript_s3` + `ec2spot_slack_transcript_ddb`: PASS
- `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` contains `slack_bridge_transcript_s3_read`: PASS
- `pkg/compiler/userdata.go` contains `SlackStreamMessagesTableName`: PASS
- `pkg/compiler/service_hcl.go` contains `SlackStreamMessagesTableName` + `ArtifactsBucket` (struct field) + `artifacts_bucket` (template line): PASS
- Commits `91ea6b1` + `b1dadc8` + `ee73ac2` all reachable from HEAD: PASS
- `go build ./...` clean: PASS
- `terraform fmt -check` on all three .tf files: PASS

## Self-Check: PASSED

---
*Phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload*
*Completed: 2026-05-03*
