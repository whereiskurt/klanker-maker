---
phase: 82
slug: multi-instance-resource-prefix-isolation
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-16
---

# Phase 82 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib); integration tests via `buildKM()` binary runner |
| **Config file** | None — `go test ./...` |
| **Quick run command** | `go test ./internal/app/cmd/... ./pkg/aws/... ./pkg/compiler/... -count=1` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~45 seconds quick (Go unit cohort); ~3 minutes full (with `buildKM` integration cases) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/... ./pkg/aws/... ./pkg/compiler/... -count=1`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green AND `make build` must succeed (ldflags-versioned km binary).
- **Max feedback latency:** ~60 seconds (per-task cohort)

---

## Per-Task Verification Map

> Task IDs land when PLAN.md files are written. Rows below seed expected verification by phase outcome — planner maps each task back to one of these rows.

| Behavior | Test Type | Automated Command | File Exists | Status |
|---------|-----------|-------------------|-------------|--------|
| `km configure` re-run preserves non-default `resource_prefix` | Integration (buildKM binary runner) | `go test ./internal/app/cmd/... -run TestConfigureRerunPreservesResourcePrefix -count=1` | ❌ W0 | ⬜ pending |
| `km configure --reset-prefix` re-defaults to `"km"` | Integration | `go test ./internal/app/cmd/... -run TestConfigureResetPrefixFlag -count=1` | ❌ W0 | ⬜ pending |
| `KMBakeTags` includes `km:resource-prefix` tag | Unit | `go test ./pkg/aws/... -run TestKMBakeTags_IncludesAllRequiredKeys -count=1` | ✅ (update existing) | ⬜ pending |
| `ListBakedAMIs` applies prefix filter when prefix provided | Unit | `go test ./pkg/aws/... -run TestListBakedAMIs_PrefixFilter -count=1` | ❌ W0 | ⬜ pending |
| `checkOrphanedEC2` skips instances with foreign `km:resource-prefix` tag | Unit | `go test ./internal/app/cmd/... -run TestCheckOrphanedEC2_SkipsForeignPrefix -count=1` | ❌ W0 | ⬜ pending |
| `checkOrphanedEC2` WARNs (does NOT delete) on untagged instances | Unit | `go test ./internal/app/cmd/... -run TestCheckOrphanedEC2_WarnsUntagged -count=1` | ❌ W0 | ⬜ pending |
| `userdata.go` Compile path uses `cfg.GetSlackThreadsTableName()` (not `"km-slack-threads"` literal) | Unit | `go test ./pkg/compiler/... -run TestCompile_SlackInboundTableName -count=1` | ❌ W0 | ⬜ pending |
| `userdata.go` Compile path uses `cfg.GetSlackStreamMessagesTableName()` (not `"km-slack-stream-messages"` literal) | Unit | `go test ./pkg/compiler/... -run TestCompile_SlackStreamTableName -count=1` | ❌ W0 | ⬜ pending |
| `configui` exits non-zero when `KM_BUDGET_TABLE` is unset | Integration | `go test ./cmd/configui/... -run TestMain_RequiresBudgetTable -count=1` OR shell smoke (planner picks) | ❌ W0 | ⬜ pending |
| `km-slack-bridge` exits non-zero when `KM_SLACK_THREADS_TABLE` is unset | Integration | `go test ./cmd/km-slack-bridge/... -run TestMain_RequiresThreadsTable -count=1` | ❌ W0 | ⬜ pending |
| `km doctor --backfill-tags` cross-install safety: only tags resources matching this install's DDB | Unit | `go test ./internal/app/cmd/... -run TestBackfillTags_CrossInstallGuard -count=1` | ❌ W0 | ⬜ pending |
| `km doctor --backfill-tags` is idempotent (second run = no-op) | Unit | `go test ./internal/app/cmd/... -run TestBackfillTags_Idempotent -count=1` | ❌ W0 | ⬜ pending |
| Wave 2 plan shows zero recreations for existing `km` install | Manual | `km init --dry-run=true` — review output | Manual only | ⬜ pending |
| Wave 3 SES rule-set evaluates correctly post-apply | Manual | `aws ses describe-active-receipt-rule-set` | Manual only | ⬜ pending |
| Wave 3 sandbox resources carry `km:resource-prefix=km` tag | Manual | AWS Resource Groups Tagging API query | Manual only | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Tests that must exist (or be stubbed) before downstream tasks can be sampled:

- [ ] `internal/app/cmd/configure_test.go` — add `TestConfigureRerunPreservesResourcePrefix`, `TestConfigureResetPrefixFlag`
- [ ] `pkg/aws/ec2_ami_test.go` — update existing `TestKMBakeTags_IncludesAllRequiredKeys` for the new `km:resource-prefix` key; add `TestListBakedAMIs_PrefixFilter`
- [ ] `internal/app/cmd/doctor_test.go` — add `TestCheckOrphanedEC2_SkipsForeignPrefix`, `TestCheckOrphanedEC2_WarnsUntagged`
- [ ] `pkg/compiler/userdata_test.go` (or equivalent) — add `TestCompile_SlackInboundTableName`, `TestCompile_SlackStreamTableName`
- [ ] New: `internal/app/cmd/doctor_backfill_tags_test.go` — add `TestBackfillTags_CrossInstallGuard`, `TestBackfillTags_Idempotent`
- [ ] `cmd/configui/main_test.go` and `cmd/km-slack-bridge/main_test.go` if hard-fail behavior is unit-testable (planner decides; may degrade to shell smoke)

No new test framework installs — Go stdlib `testing` already in use across `internal/app/cmd/`, `pkg/aws/`, and `cmd/`.

---

## Manual-Only Verifications

| Behavior | Why Manual | Test Instructions |
|----------|------------|-------------------|
| Wave 2 `terragrunt plan` for existing `km` install is clean | Terragrunt-output inspection — not unit-testable | Run `km init --dry-run=true` against the `klanker-application` profile. Confirm zero `must be replaced` lines; only in-place updates allowed (`~`) on resource tags. Recreate-needed = abort + escalate to planner. |
| Wave 3 SES active rule set is named `"km-sandbox-email"` post-apply | Live AWS API check | `aws ses describe-active-receipt-rule-set --profile klanker-application --region us-east-1` — confirm `Name == "km-sandbox-email"`. Variable-evaluation correctness (not a Go test). |
| Wave 3 sandbox + AMI resources carry `km:resource-prefix=km` | Cross-service tag query | `aws resourcegroupstaggingapi get-resources --tag-filters Key=km:resource-prefix,Values=km --profile klanker-application --region us-east-1 | jq '.ResourceTagMappingList | length'` — should equal sandbox + AMI count from `km list` + `km ami list`. |
| `km doctor --backfill-tags` idempotent on second run | AWS-API observable, not unit-testable beyond mock | Run twice in sequence; second run reports `tagged: 0` (or equivalent zero-work output). |
| Optional: throwaway-account second-install spike (`resource_prefix: rg`) | Pre-merge confidence check for SES, ECS, email-handler | On a non-production AWS account: `km configure --resource-prefix rg && km bootstrap && km init --dry-run=false`. Confirm no resource-name collision with a parallel `km` install in the same account. Spec § Confidence recommends this for SES specifically. |

---

## Validation Sign-Off

- [ ] All tasks have automated verify OR a Wave 0 dependency (or are explicitly marked Manual-Only)
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all `❌ W0` references in the per-task map
- [ ] No watch-mode flags (Go test runs are one-shot)
- [ ] Feedback latency < 60 seconds for the per-task cohort
- [ ] `nyquist_compliant: true` set in frontmatter after planner maps tasks → rows

**Approval:** pending
