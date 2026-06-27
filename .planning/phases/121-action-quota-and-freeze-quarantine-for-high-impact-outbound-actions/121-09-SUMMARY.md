---
phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions
plan: "09"
subsystem: quota-alerter
tags: [lambda, dynamodb-streams, ses, slack, idempotency, event-source-mapping]
dependency_graph:
  requires: [121-08]
  provides: [km-quota-alerter Lambda, event_source_mapping, 4-point deploy registration]
  affects: [infra/live/use1/lambda-quota-alerter, pkg/quota, init.go]
tech_stack:
  added: [aws_lambda_event_source_mapping, SES operator email, DDB conditional alert_sent write]
  patterns: [injected interfaces for Lambda testability, TDD RED/GREEN, attribute_not_exists idempotency guard]
key_files:
  created:
    - cmd/km-quota-alerter/alerter.go
    - infra/modules/lambda-quota-alerter/v1.0.0/main.tf
    - infra/modules/lambda-quota-alerter/v1.0.0/variables.tf
    - infra/live/use1/lambda-quota-alerter/terragrunt.hcl
  modified:
    - cmd/km-quota-alerter/main.go
    - cmd/km-quota-alerter/alerter_test.go
    - internal/app/cmd/init.go
    - internal/app/cmd/init_test.go
decisions:
  - "attribute_not_exists(alert_sent) conditional write after SES send — not before — so ConditionalCheckFailed is a harmless race (other instance sent it), not a lost alert"
  - "proxyOriginActions map drives channel-notice path (github_*/email_send); bridge-origin actions (slack_post/h1_comment) handle their own in-thread notice"
  - "httpSlackPoster inlined in main.go (lean binary); no pkg/slack import in Lambda"
  - "starting_position=LATEST on event_source_mapping — only new breaches processed, not historical backfill"
  - "maximum_retry_attempts=3 on stream mapping — bounded retries; bisect_batch_on_function_error=true isolates poison records"
metrics:
  duration: "384s"
  completed_date: "2026-06-27"
  tasks_completed: 3
  files_changed: 8
---

# Phase 121 Plan 09: km-quota-alerter Lambda Summary

One-liner: DDB-Stream-triggered alerter Lambda with SES operator email + idempotent alert_sent conditional write + proxy-origin Slack channel notice, registered across all four deploy-surface points (TF module + live unit + `regionalModules()` + `lambdaBuilds()`).

## What Was Built

### Task 1: Alerter Lambda code (TDD — ALR-01)

`cmd/km-quota-alerter/alerter.go` implements the testable core handler with injected interfaces:

- `sesAPI` — SES v2 `SendEmail`
- `ddbAPI` — DynamoDB `UpdateItem` (alert_sent conditional write) + `GetItem` (sandbox channel lookup)
- `slackPoster` — channel-level user notice for proxy-origin trips

**First-breach detection logic:**
1. Skip non-MODIFY records
2. Skip if `breached_at` absent from NewImage (no breach)
3. Skip if `breached_at` already in OldImage (not a new breach — re-increment)
4. Skip if `alert_sent` already in NewImage (already alerted)
5. Send SES operator email
6. For proxy-origin actions (`github_pr`, `github_comment`, `github_review`, `email_send`): resolve `slack_channel_id` from `km-sandboxes` GetItem and post enforce-aware channel notice
7. Conditional write `alert_sent = :ts` with `attribute_not_exists(alert_sent)` — swallow `ConditionalCheckFailedException` (another instance beat us, exactly one alert sent)

`cmd/km-quota-alerter/main.go` wires AWS SDK clients (`dynamodb.Client`, `sesv2.Client`, `ssm.Client`) and registers `lambda.Start(a.Handle)`.

**7 test sub-cases (ALR-01) GREEN:**
- first_breach_sends_alert_and_sets_alert_sent
- second_breach_record_alert_sent_already_set
- conditional_check_failed_is_swallowed
- non_new_breach_is_skipped
- insert_and_remove_records_skipped
- proxy_origin_posts_channel_notice
- proxy_origin_no_channel_skips_slack

### Task 2: TF module with event_source_mapping + IAM

`infra/modules/lambda-quota-alerter/v1.0.0/main.tf` — first `aws_lambda_event_source_mapping` in the codebase (Risk 3 from plan):

```hcl
resource "aws_lambda_event_source_mapping" "quota_stream" {
  event_source_arn               = var.quota_stream_arn
  function_name                  = aws_lambda_function.quota_alerter.arn
  starting_position              = "LATEST"
  batch_size                     = 100
  bisect_batch_on_function_error = true
  maximum_retry_attempts         = 3
}
```

IAM grants: CloudWatch Logs + DDB stream read (`GetRecords`/`GetShardIterator`/`DescribeStream`/`ListStreams`) + DDB `UpdateItem` on quota table + DDB `GetItem` on sandboxes (gated on `sandboxes_table_arn != ""`) + SES `SendEmail`/`SendRawEmail` + SSM `GetParameter` for bot token (gated on `bot_token_path != ""`) + KMS `Decrypt` (gated on `kms_key_arn != ""`).

`terraform fmt -check` passes.

### Task 3: Four-point deploy registration (INIT-02)

All four points required by `project_new_lambda_needs_live_unit_and_init_list` and `project_km_init_skips_existing_lambda_zips`:

1. **TF module** — `infra/modules/lambda-quota-alerter/v1.0.0/`
2. **Live unit** — `infra/live/use1/lambda-quota-alerter/terragrunt.hcl` with `dependency "quota_table"` (provides `stream_arn`) + `dependency "sandboxes"` + `mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]` (footgun guard from `project_terragrunt_show_needs_mocks`)
3. **`regionalModules()`** — entry added AFTER `dynamodb-action-quota` (stream dependency), BEFORE `check-runner-role`
4. **`lambdaBuilds()`** — `{name: "km-quota-alerter", srcDir: "cmd/km-quota-alerter"}` added

**Tests GREEN:**
- `TestRunInitPlan_ModuleOrder`: len(mods)==26 (was already pinned in plan 01), network first, ses last
- `TestQuotaAlerterBuildListMembership` (INIT-02): `km-quota-alerter` in `lambdaBuilds()`

## Verification Results

| Check | Result |
|-------|--------|
| `go test ./cmd/km-quota-alerter/... -run TestIdempotentAlert -count=1` | PASS (7 sub-tests) |
| `go test ./internal/app/cmd/... -run TestQuotaAlerterBuildListMembership\|TestRunInitPlan_ModuleOrder -count=1` | PASS (count=26) |
| `grep -q 'aws_lambda_event_source_mapping' infra/modules/lambda-quota-alerter/v1.0.0/main.tf` | FOUND |
| `terraform fmt -check infra/modules/lambda-quota-alerter/v1.0.0/` | PASS |

## Deviations from Plan

None — plan executed exactly as written.

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| 1 | 7741465e | feat(121-09): implement km-quota-alerter Lambda handler (ALR-01) |
| 2 | c574d0d7 | feat(121-09): add lambda-quota-alerter TF module with event_source_mapping + IAM |
| 3 | 1efb87b1 | feat(121-09): register lambda-quota-alerter across all four deploy-surface points (INIT-02) |

## Deploy Notes

This is a new Lambda + infrastructure. Full deploy sequence:

1. `make build` (binary carries the new `regionalModules()` entry — **before** `km init`)
2. `make build-lambdas` (builds `km-quota-alerter.zip`)
3. `km init --dry-run=false` (new Lambda + event_source_mapping + IAM — NOT `--sidecars`)

No sandbox recreate needed (alerter reads from the DDB stream; existing sandboxes' quota writes will trigger it automatically after deploy).

## Self-Check: PASSED

All created files verified on disk. All three commits verified in git log.
