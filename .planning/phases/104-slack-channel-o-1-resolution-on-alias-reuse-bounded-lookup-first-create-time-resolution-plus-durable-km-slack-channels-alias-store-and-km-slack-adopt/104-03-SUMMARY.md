---
phase: 104-slack-channel-o-1-resolution-on-alias-reuse
plan: "03"
subsystem: slack-channel-store
tags: [dynamodb, slack, iam, config, tdd]
dependency_graph:
  requires: [104-01, 104-02]
  provides: [slack-channel-store-iam, config-getter, ddb-helper, create-wiring]
  affects: [km-create, create-handler-lambda, km-slack-channels-table]
tech_stack:
  added: [pkg/aws/slack_channels.go, SlackChannelGetPutAPI, SlackChannelStore]
  patterns: [DDB GetItem/PutItem narrow interface, count-gated IAM policy, static-string TF input]
key_files:
  created:
    - pkg/aws/slack_channels.go
    - pkg/aws/slack_channels_test.go
  modified:
    - infra/modules/km-operator-policy/v1.0.0/variables.tf
    - infra/modules/km-operator-policy/v1.0.0/main.tf
    - infra/modules/create-handler/v1.0.0/variables.tf
    - infra/modules/create-handler/v1.0.0/main.tf
    - infra/live/use1/create-handler/terragrunt.hcl
    - internal/app/config/config.go
    - internal/app/config/config_test.go
    - internal/app/cmd/create.go
decisions:
  - "No dependency block in live unit (CORRECTION #1): IAM grant needs only table name string, not ARN output; dependency block would break km init --plan on fresh installs"
  - "No TF env var for table name (CORRECTION #2): cfg.GetSlackChannelsTableName() derived at runtime, mirrors km-slack-threads pattern"
  - "Merge-list entry mandatory (CORRECTION #3): slack_channels_table_name without merge-list entry is silently ignored by viper (project_config_key_merge_list footgun)"
  - "No ConditionExpression in UpsertByAlias: alias→channel_id is a stable mapping, always-overwrite is correct for write-through upsert"
  - "No TTL on channel mappings: durable across destroy/recreate cycles is the entire purpose"
metrics:
  duration: 775s
  completed: "2026-06-10"
  tasks_completed: 3
  files_changed: 9
requirements: [SLACK-CHAN-STORE, SLACK-CHAN-DEPLOY]
---

# Phase 104 Plan 03: Slack Channel Store IAM, Config Getter, DDB Helper, and Create Wiring Summary

**One-liner:** End-to-end DDB channel store wiring — IAM grant (count-gated GetItem/PutItem/DescribeTable), `GetSlackChannelsTableName()` config getter with mandatory merge-list entry, `pkg/aws.SlackChannelStore` helper, and `km create` wired to build + pass the real store so the O(1) alias→channel_id path is live.

## What Was Built

### Task 1: create-handler IAM Grant + Static-String Plumbing

- `km-operator-policy/variables.tf`: added `slack_channels_table_name` variable (default `""`)
- `km-operator-policy/main.tf`: added `aws_iam_role_policy.dynamodb_slack_channels` — `count = var.slack_channels_table_name != "" ? 1 : 0`, grants `GetItem`, `PutItem`, `DescribeTable` on the table ARN. Mirrors the `dynamodb_slack_threads` resource pattern exactly.
- `create-handler/variables.tf`: added `slack_channels_table_name` variable (default `""`)
- `create-handler/main.tf`: added `slack_channels_table_name = var.slack_channels_table_name` pass-through in the `km_operator_policy` module block
- `infra/live/use1/create-handler/terragrunt.hcl`: added `slack_channels_table_name = "${local.site_vars.locals.site.label}-slack-channels"` as a **static string** (no `dependency "slack_channels"` block per CORRECTION #1)

Both modules `terraform validate` clean.

### Task 2: Config Getter + Merge-List + SlackChannelStore DDB Helper (TDD)

**Config (RED → GREEN):**
- `config.go`: added `SlackChannelsTableName string` struct field
- `config.go`: added `v.SetDefault("slack_channels_table_name", "")` at the SetDefault block
- `config.go`: added `"slack_channels_table_name"` to the v2→v merge-list (CORRECTION #3 — without this, the km-config.yaml key is silently ignored)
- `config.go`: populated `SlackChannelsTableName: v.GetString("slack_channels_table_name")` in the `cfg = &Config{...}` block
- `config.go`: added `GetSlackChannelsTableName()` getter — nil receiver → `"km-slack-channels"`, explicit override wins, else `GetResourcePrefix() + "-slack-channels"`
- `config_test.go`: added `TestGetSlackChannelsTableName` with 4 sub-tests: nil receiver, explicit override, prefix-derived, yaml merge-list round-trip

**SlackChannelStore (RED → GREEN):**
- `pkg/aws/slack_channels.go`: created `SlackChannelGetPutAPI` interface (GetItem + PutItem only), `SlackChannelStore{Client, TableName}`, `GetByAlias` (returns `""` on miss), `UpsertByAlias` (always-overwrite PutItem, no ConditionExpression, no TTL, sets `updated_at` RFC3339)
- `pkg/aws/slack_channels_test.go`: created `fakeChannelDDB` (map-backed), `TestSlackChannelStore_UpsertThenGet`, `TestSlackChannelStore_GetMiss` — both pass

### Task 3: Wire DDB Store into km create Resolution

- `internal/app/cmd/create.go`: in the Slack-enabled create path, after building the SSM store and Slack client, now builds:
  ```go
  ddbClientForSlack := dynamodbpkg.NewFromConfig(awsCfg)
  channelStore := &awspkg.SlackChannelStore{
      Client:    ddbClientForSlack,
      TableName: cfg.GetSlackChannelsTableName(),
  }
  ```
  and passes `channelStore` into `resolveSlackChannel(...)` (replacing the `nil` placeholder from plan 104-01)

The O(1) DDB read-first path + write-through on fresh create is now live end-to-end. The migrate-on-touch back-fill for pre-104 SSM-cached channels is also activated (plan 104-01's logic fires because `store` is no longer nil).

## Deviations from Plan

None — plan executed exactly as written. All 4 corrections honored:
- CORRECTION #1: no dependency block in live unit
- CORRECTION #2: no `KM_SLACK_CHANNELS_TABLE` env var added
- CORRECTION #3: merge-list entry present at `internal/app/config/config.go`
- CORRECTION #4 (orphan exclusion): out of scope for this plan (addressed in 104-04)

## Pre-existing Test Failure (Out of Scope)

`TestGoSourceNamesUseResourcePrefix` in `pkg/hygiene` fails due to hardcoded `km-` strings in `doctor_artifacts.go` and `doctor_log_groups.go`. This failure pre-dates this plan (confirmed by stash test). Logged to deferred-items — not touched by this plan's changes.

## Verification Results

```
terraform validate km-operator-policy/v1.0.0 → Success
terraform validate create-handler/v1.0.0     → Success
TestGetSlackChannelsTableName (4 cases)      → PASS
TestSlackChannelStore_UpsertThenGet          → PASS
TestSlackChannelStore_GetMiss                → PASS
make build                                   → km v0.4.917
go test ./internal/app/config/ ./pkg/aws/ ./internal/app/cmd/ ./pkg/slack/ → PASS
```

## Self-Check: PASSED

Files created/modified:
- `pkg/aws/slack_channels.go` — exists (SlackChannelStore)
- `pkg/aws/slack_channels_test.go` — exists (2 tests)
- `internal/app/config/config.go` — GetSlackChannelsTableName present
- `infra/live/use1/create-handler/terragrunt.hcl` — static string input, no dependency block
- `infra/modules/km-operator-policy/v1.0.0/main.tf` — dynamodb_slack_channels resource present

Commits:
- `f6a5b272` — feat(infra): create-handler IAM + static-string plumbing for km-slack-channels
- `d7681120` — feat(config+aws): GetSlackChannelsTableName getter (+merge-list) + SlackChannelStore DDB helper
- `9d41950e` — feat(slack): wire SlackChannelStore into km create resolution (P2)
