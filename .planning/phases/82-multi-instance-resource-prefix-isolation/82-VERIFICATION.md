---
phase: 82-multi-instance-resource-prefix-isolation
verified: 2026-05-16T12:00:00Z
updated: 2026-05-16T12:30:00Z
status: resolved
score: 10/10 must-haves verified (after Phase 82.1 gap closure)
gaps:
  - truth: "km configure re-run preserves resource_prefix in all invocation modes"
    status: resolved
    reason: "Preserve logic fires only when --output-dir is explicitly passed (line 145: `if !resetPrefix && outputDir != \"\"`). Bare `km configure` (no --output-dir) skips the read-existing-config block. The test TestConfigureRerunPreservesResourcePrefix exercises only the --output-dir path. CLAUDE.md claims 'preserving is never overwritten' without this qualifier."
    resolved_by: "Phase 82.1-01 (commit 061fdc2): extended preserve guard to use findRepoRoot() fallback when outputDir is empty; TestConfigureBarePath_PreservesResourcePrefix + TestConfigureBarePath_FreshInstallDefaultsToKm added"
    artifacts:
      - path: "internal/app/cmd/configure.go"
        issue: "Preserve guard at line 145 is `outputDir != \"\"` — silent no-op when --output-dir absent (the default usage)"
    missing:
      - "Either extend the preserve guard to also attempt findRepoRoot() as a fallback read path, or qualify the CLAUDE.md/OPERATOR-GUIDE.md documentation to note the --output-dir requirement"
  - truth: "All four silent km-* literal fallbacks are replaced"
    status: resolved
    reason: "Three userdata.go sites are fixed. pkg/compiler/service_hcl.go:784 still has literal `\"km-slack-stream-messages\"` fallback — this was explicitly deferred in Plan 82-02 and logged to deferred-items.md, but the deferred-items file exists in the phase directory rather than in a future-phase plan."
    resolved_by: "Phase 82.1-02 (commit 42f7f25): service_hcl.go:784 literal replaced with KM_RESOURCE_PREFIX-derived value; TestEC2ServiceHCL_StreamTableUsesResourcePrefix added"
    artifacts:
      - path: "pkg/compiler/service_hcl.go"
        issue: "Line 784: `streamTable = \"km-slack-stream-messages\"` literal not replaced with prefix-aware derivation"
    missing:
      - "Replace `streamTable = \"km-slack-stream-messages\"` at service_hcl.go:784 with `resourcePrefix + \"-slack-stream-messages\"` (same pattern as the three sites fixed in userdata.go)"
human_verification:
  - test: "km init --dry-run=true against a second install"
    expected: "Zero 'must be replaced' lines; tag-only additions as in-place updates; SES rule set evaluates to {prefix}-sandbox-email"
    why_human: "Requires a second km-config.yaml with a distinct resource_prefix (e.g. 'km2') and live AWS credentials — cannot verify statically"
  - test: "km doctor --backfill-tags --dry-run=true on the production install"
    expected: "Reports Tagged: 0, SkippedAlreadyTagged: N (post-apply idempotency)"
    why_human: "Requires live AWS session with correct AWS_DEFAULT_REGION + AWS_PROFILE env vars; cannot verify from code"
  - test: "Bare `km configure` re-run (no --output-dir) against a directory containing km-config.yaml with resource_prefix: rg"
    expected: "Preserves resource_prefix: rg (or clearly fails if the partial-implementation gap is a blocker)"
    why_human: "Tests in codebase only exercise --output-dir path; the default interactive/non-interactive path without --output-dir is untested"
---

# Phase 82: Multi-instance resource-prefix isolation — Verification Report

**Phase Goal:** Close the gap between CLAUDE.md's 'multiple km installs per AWS account via resource_prefix' promise and reality — fix 3 hard Terraform blockers (SES rule-set, email-handler S3 IAM, ECS SSM ARN), 1 configure-flow footgun, 4 silent km-* fallbacks, add the km:resource-prefix install-discriminator tag at bake-time + via terraform + via a one-time `km doctor --backfill-tags` retro-sweep, and tag-filter doctor's cross-install destruction surfaces.

**Verified:** 2026-05-16
**Status:** human_needed (9/10 must-haves verified; 2 partial gaps noted; 3 items require live AWS confirmation)
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | C1: `km configure` re-run preserves resource_prefix | PARTIAL | `--output-dir` path: verified (configure.go:145-160, TestConfigureRerunPreservesResourcePrefix passes). Bare `km configure` (no --output-dir): NOT covered — guard is `outputDir != ""` so the read-existing-config block is skipped |
| 2 | C1: `--reset-prefix` flag exists and resets to `"km"` | VERIFIED | configure.go:97-98 flag declaration; TestConfigureResetPrefixFlag passes |
| 3 | F1: configui hard-fails on missing KM_BUDGET_TABLE | VERIFIED | configui/main.go:224-231 `resolveBudgetTable` calls `exit(1)` when env unset; test passes |
| 4 | F1: km-slack-bridge hard-fails on missing KM_SLACK_THREADS_TABLE | VERIFIED | km-slack-bridge/main.go:406-412 `resolveThreadsTable` calls `exit(1)`; test passes |
| 5 | F1: userdata.go uses prefix-aware table names (3 sites) | VERIFIED | userdata.go:3328-3331, 3344-3347, 3359-3362 use `resourcePrefix + "-slack-threads"` / `"-slack-stream-messages"` derived from KM_RESOURCE_PREFIX; compiler tests pass |
| 6 | F1: service_hcl.go uses prefix-aware table name (4th site) | FAILED | service_hcl.go:784 still `streamTable = "km-slack-stream-messages"` literal — explicitly deferred in deferred-items.md but not completed |
| 7 | B1: SES rule-set name parameterized via resource_prefix | VERIFIED | ses/v1.0.0/main.tf:62 `rule_set_name = "${var.resource_prefix}-sandbox-email"`; live ses/terragrunt.hcl:46 wired to KM_RESOURCE_PREFIX |
| 8 | B2: email-handler S3 IAM uses state_prefix variable | VERIFIED | email-handler/v1.0.0/variables.tf:64-67 `variable "state_prefix"` default `"tf-km"`; main.tf:76 uses `${var.state_prefix}`; live terragrunt.hcl:46 passes `state_prefix = "tf-${get_env(\"KM_RESOURCE_PREFIX\",\"km\")}"` |
| 9 | B3: ECS modules use `${var.km_label}/*` in SSM ARNs | VERIFIED | ecs-task:157, ecs:130, ecs-cluster:135 all use `parameter/${var.km_label}/*`; km_label defined in each module's variables.tf; sandbox terragrunt.hcl:56-58 wires `km_label = local.site_vars.locals.site.label` |
| 10 | Tag schema: KMBakeTags emits km:resource-prefix at bake time | VERIFIED | ec2_ami.go:90 `{Key: "km:resource-prefix", Value: awssdk.String(resourcePrefix)}`; TestKMBakeTags passes |
| 11 | Tag schema: ListBakedAMIs filters by km:resource-prefix | VERIFIED | ec2_ami.go:195-200 adds `tag:km:resource-prefix` filter when resourcePrefix non-empty; TestListBakedAMIs_PrefixFilter passes |
| 12 | Tag schema: Terraform modules emit km:resource-prefix on all sandbox-creating resources | VERIFIED | ec2spot, ecs, ecs-task, ecs-cluster, email-handler all emit `"km:resource-prefix" = var.resource_prefix`; SES rule_set tag removed (AWS provider limitation, documented) |
| 13 | Backfill: `km doctor --backfill-tags` exists with cross-install DDB safety guard | VERIFIED | doctor_backfill_tags.go exists; safety guard at lines 123-130 skips resources where existing prefix != currentPrefix; doctor.go:2252 wires `--backfill-tags` flag |
| 14 | Doctor: `checkOrphanedEC2` skips foreign-prefix instances | VERIFIED | doctor.go:1764-1767 `if hasResourcePrefix && instPrefix != currentPrefix { continue }`; TestCheckOrphanedEC2_SkipsForeignPrefix passes |
| 15 | Docs: CLAUDE.md and OPERATOR-GUIDE.md updated | VERIFIED | CLAUDE.md lines 11-37 Phase 82 subsection present; OPERATOR-GUIDE.md lines 621-644 Phase 82 isolation guarantees present |

**Score:** 9/10 must-haves verified (C1 partial: works with --output-dir only; F1 partial: service_hcl.go:784 deferred)

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/cmd/configure.go` | Preserve logic + --reset-prefix flag | PARTIAL | Flag and logic exist; guard condition `outputDir != ""` limits coverage to explicit --output-dir path only |
| `internal/app/cmd/configure_test.go` | Tests for preserve + reset | VERIFIED | TestConfigureRerunPreservesResourcePrefix and TestConfigureResetPrefixFlag both pass |
| `cmd/configui/main.go` | Hard-fail on missing KM_BUDGET_TABLE | VERIFIED | resolveBudgetTable exits 1 when unset |
| `cmd/km-slack-bridge/main.go` | Hard-fail on missing KM_SLACK_THREADS_TABLE | VERIFIED | resolveThreadsTable exits 1 when unset |
| `pkg/compiler/userdata.go` | Prefix-aware fallbacks (3 sites) | VERIFIED | Lines 3328-3362 use resourcePrefix variable |
| `pkg/compiler/service_hcl.go` | Prefix-aware fallback (4th site, line 784) | STUB | Still `"km-slack-stream-messages"` literal — deferred to follow-up |
| `pkg/aws/ec2_ami.go` | KMBakeTags emits km:resource-prefix; ListBakedAMIs filters by it | VERIFIED | Lines 90 and 195-200 |
| `internal/app/cmd/doctor_backfill_tags.go` | Backfill command with cross-install guard | VERIFIED | Full implementation with DDB cross-reference |
| `internal/app/cmd/doctor.go` | checkOrphanedEC2 tag-filter | VERIFIED | Lines 1754-1772 |
| `infra/modules/ses/v1.0.0/main.tf` + `variables.tf` | B1: parameterized rule_set_name | VERIFIED | `${var.resource_prefix}-sandbox-email` |
| `infra/modules/email-handler/v1.0.0/main.tf` + `variables.tf` | B2: state_prefix variable | VERIFIED | `state_prefix` variable with default `"tf-km"` |
| `infra/modules/ecs-task/v1.0.0/`, `ecs/v1.0.0/`, `ecs-cluster/v1.0.0/` | B3: `${var.km_label}/*` SSM ARNs | VERIFIED | All three modules use `parameter/${var.km_label}/*` |
| `CLAUDE.md` | Phase 82 subsection | VERIFIED | Lines 11-37 |
| `OPERATOR-GUIDE.md` | Phase 82 isolation guarantees | VERIFIED | Lines 621-644 |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `configure.go:145` | existing km-config.yaml | `filepath.Join(outputDir, "km-config.yaml")` | PARTIAL | Only fires when outputDir != "" — default bare invocation does not read existing config |
| `km-slack-bridge/main.go:210` | resolveThreadsTable | `wireEventsHandler()` in main() | WIRED | os.Exit(1) path confirmed; init() split documented |
| `configui/main.go:108` | resolveBudgetTable | `budgetTableName()` | WIRED | Hard-fail confirmed |
| `ec2_ami.go:83` | CreateImage tags | `KMBakeTags()` called from ami bake path | WIRED | Function signature verified |
| `doctor_backfill_tags.go` | doctor.go --backfill-tags flag | `runBackfillTags()` at doctor.go:2202 | WIRED | Flag registered at doctor.go:2252 |
| `infra/live/use1/ses/terragrunt.hcl:46` | ses module `var.resource_prefix` | `get_env("KM_RESOURCE_PREFIX", "km")` | WIRED | Live file confirmed |
| `infra/live/use1/email-handler/terragrunt.hcl:46` | email-handler `var.state_prefix` | `"tf-${get_env("KM_RESOURCE_PREFIX","km")}"` | WIRED | Live file confirmed |
| `infra/templates/sandbox/terragrunt.hcl:56-58` | ECS modules `var.km_label` | `local.site_vars.locals.site.label` | WIRED | Template confirmed |

---

### Requirements Coverage

No formal REQUIREMENTS.md IDs — operator-driven phase. CONTEXT.md `<decisions>` block enumerates the deliverables (C1, F1, B1, B2, B3, tag schema, backfill, doctor filter). Coverage assessed above in Observable Truths.

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/app/cmd/configure.go` | 145 | Preserve guard `outputDir != ""` skips the most common invocation | Warning | `km configure` without `--output-dir` silently resets prefix — contradicts CLAUDE.md doc claim |
| `pkg/compiler/service_hcl.go` | 784 | `streamTable = "km-slack-stream-messages"` literal | Warning | Non-default installs get wrong stream-table name in Docker/service HCL generation |

Neither is a blocker for the primary EC2 path, but both are exploitable in the scenarios Phase 82 was designed to fix.

---

### Human Verification Required

#### 1. Bare `km configure` Preserve Behavior

**Test:** In a temp directory containing a `km-config.yaml` with `resource_prefix: rg`, run `km configure --non-interactive --domain ... --terraform-account ... --application-account ... --sso-start-url ... --sso-region ... --region ...` (no `--output-dir`).
**Expected:** The resulting km-config.yaml should have `resource_prefix: rg`. If it has `resource_prefix: km` the footgun is still open for the default workflow.
**Why human:** No test exercises this code path. The current test (TestConfigureRerunPreservesResourcePrefix) always passes `--output-dir` explicitly.

#### 2. km init --dry-run=true against a second install

**Test:** Create a second km-config.yaml in a scratch directory with `resource_prefix: km2`; export `KM_RESOURCE_PREFIX=km2`; run `km init --dry-run=true` from that directory.
**Expected:** Terraform plan shows zero resource replacements (`must be replaced` count = 0); tag-only in-place updates; SES rule set evaluates to `km2-sandbox-email`.
**Why human:** Requires live AWS credentials and a second distinct prefix config — cannot be verified statically.

#### 3. km doctor --backfill-tags idempotency on the production install

**Test:** Run `AWS_DEFAULT_REGION=us-east-1 AWS_PROFILE=klanker-application km doctor --backfill-tags --dry-run=true` after the Wave 3 apply.
**Expected:** Reports `Tagged: 0, SkippedAlreadyTagged: N` where N = number of pre-Phase-82 resources previously backfilled.
**Why human:** Requires live AWS session; the 82-10 SUMMARY claims this was verified (second run gave Tagged: 0), but cannot be re-confirmed statically.

---

### Gaps Summary

Two partial gaps were found (status: human_needed rather than gaps_found because neither blocks the primary EC2 multi-instance scenario, both have documented deferred status, and the human-verification items are the actionable next steps):

**Gap 1 — C1 Partial (configure footgun, default invocation path):**
The preserve-on-re-run logic in `configure.go:145` is conditional on `outputDir != ""`. When an operator runs bare `km configure` (the most common workflow), `outputDir` is empty and the existing `km-config.yaml` is never read. The `--output-dir` path works correctly and is covered by tests. CLAUDE.md's claim "Re-running `km configure` preserves the existing `resource_prefix`" is true only for the `--output-dir` path. Plan 82-01 SUMMARY explicitly documented this as a deliberate scope limitation ("same guard would apply there if needed in a future iteration"), so this is a known deferred item, not a regression.

**Gap 2 — F1 Partial (service_hcl.go:784 literal):**
`pkg/compiler/service_hcl.go:784` retains `streamTable = "km-slack-stream-messages"` as a literal. This was found during Plan 82-02 execution, logged to `deferred-items.md`, and explicitly de-scoped as "Medium priority" because it affects Docker/service HCL generation (not the primary EC2 userdata path). The three userdata.go sites are fixed. The deferred item does not have an assigned follow-up plan yet.

**All automated tests pass (go test ./... green on relevant packages).**

---

_Verified: 2026-05-16_
_Verifier: Claude (gsd-verifier)_
