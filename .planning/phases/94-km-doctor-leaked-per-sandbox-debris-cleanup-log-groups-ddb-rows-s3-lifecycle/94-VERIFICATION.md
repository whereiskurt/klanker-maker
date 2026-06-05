---
phase: 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle
verified: 2026-06-05T01:52:24Z
status: passed
score: 15/15 must-haves verified
human_verification:
  - test: "Run km doctor --delete-logs --delete-ddb-rows --set-log-retention --set-s3-lifecycle --dry-run=false against the kph install"
    expected: "~271 orphaned log groups reclaimed, orphaned DDB rows removed, retention set on management groups, S3 lifecycle rules installed on artifacts bucket transient prefixes"
    why_human: "DBG-UAT: requires a live AWS account with the kph non-default-prefix install — cannot verify against real AWS programmatically"
---

# Phase 94: km doctor Leaked Per-Sandbox Debris Cleanup Verification Report

**Phase Goal:** Teach `km doctor` to detect, reclaim, and prevent the orphaned per-sandbox debris that teardown leaves behind — CloudWatch log groups (4 families), leaked DynamoDB rows (budgets/identities/slack-threads + status=failed sandboxes rows), and an artifacts S3 bucket with no lifecycle expiry on transient prefixes. Three new checks following the checkStale*/checkOrphaned* contract + cleanup flags + guardrail flags + two config knobs. PLUS a root-cause source fix: finish the resource_prefix migration so per-sandbox log groups are CREATED with the dynamic {resource_prefix} instead of hardcoded km-//km/.

**Verified:** 2026-06-05T01:52:24Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| #  | Truth                                                                                                                                         | Status     | Evidence                                                                                                                                      |
|----|----------------------------------------------------------------------------------------------------------------------------------------------|------------|-----------------------------------------------------------------------------------------------------------------------------------------------|
| 1  | Three new mocked-API interfaces (CWLogsCleanupAPI, DDBScanDeleteAPI, S3LifecycleAPI) exist and real AWS clients satisfy them                 | VERIFIED   | doctor.go lines 177-209; real client assignments at initRealDepsWithExisting lines 3584-3586; go build ./... clean                            |
| 2  | doctor_log_retention_days and doctor_s3_expire_days load from km-config.yaml (merge-list wired) and clamp to <=0→30                         | VERIFIED   | config.go: struct fields 181/187, SetDefault 313-314, merge-list 389-390, GetInt 458-459, clamps 516-521; TestDoctorRetentionAndExpireDays passes |
| 3  | --delete-logs / --delete-ddb-rows / --set-log-retention / --set-s3-lifecycle flags exist on km doctor and thread into DoctorDeps via runDoctor | VERIFIED | doctor.go lines 2398-2401 (var declarations), 2490-2502 (flag registration), 2545-2548 (deps wiring in runDoctor), 2466 (fan-out call)         |
| 4  | --with-deletes fans out to --delete-logs and --delete-ddb-rows (NOT to the guardrail flags)                                                  | VERIFIED   | doctor.go lines 2461-2464: fan-out sets deleteLogs+deleteDDBRows; setLogRetention/setS3Lifecycle excluded; TestDoctorFlags_WithDeletesImpliesNewFlags passes |
| 5  | checkStaleLogGroups enumerates all four per-sandbox log-group families and WARNs on orphans                                                  | VERIFIED   | doctor_log_groups.go perSandboxFamilies() covers budget-enforcer, github-token-refresher, /km/sandboxes/, /km/sidecars/ (lines 62-83); 16 tests pass |
| 6  | Each family matched under BOTH legacy km-//km/ AND new {prefix}-//{prefix}/ prefix, deduped by log-group name                               | VERIFIED   | doctor_log_groups.go lines 62-83: pairs built per family; dedup via seenFilters map (lines 158-166); TestDoctor_StaleLogGroups_BothNames and _KmPrefix pass |
| 7  | --set-log-retention sets retentionInDays on management + sandbox groups lacking it; idempotent no-op when already set                        | VERIFIED   | applyLogRetention() in doctor_log_groups.go lines 299-342; RetentionInDays!=nil skip (line 324); TestDoctor_StaleLogGroups_RetentionAlreadySet and _SetRetention pass |
| 8  | DescribeLogGroups pagination handled; DynamoDB Scan LastEvaluatedKey pagination handled                                                      | VERIFIED   | describeAllLogGroupsForFilter() NextToken loop (lines 109-130); all four scan*Table helpers use LastEvaluatedKey loop; _Pagination tests pass  |
| 9  | checkStaleLogGroups registered in buildChecks                                                                                                | VERIFIED   | doctor.go line 2965: closure appended after SSM parameters check                                                                             |
| 10 | checkOrphanedDDBRows scans 4 tables, preserves BUDGET#ai# rows, status=failed/nocap guard (starting excluded), slack-threads non-key sandbox_id | VERIFIED | doctor_ddb_rows.go: strings.HasPrefix sk "BUDGET#ai#" (line 194); sandboxDeletableStatuses map (line 308-311); sandbox_id non-key in slack scan (line 281); 13 tests including _PreserveAI, _StartingSkipped, _SlackThreadNoSandboxID pass |
| 11 | checkOrphanedDDBRows registered in buildChecks                                                                                               | VERIFIED   | doctor.go line 2989: closure appended after checkStaleLogGroups block                                                                        |
| 12 | checkS3LifecyclePolicy detects missing expiry on transient prefixes, merge-preserving --set-s3-lifecycle, build-artifact prefixes never touched | VERIFIED | doctor_artifacts.go: transientPrefixes var (line 236); merge-and-put pattern; 7 tests pass including _SetMergesPreservesExisting and _Idempotent |
| 13 | checkS3LifecyclePolicy registered in buildChecks                                                                                             | VERIFIED   | doctor.go line 3209: closure appended beside checkOrphanedArtifacts                                                                         |
| 14 | DBG-SRCFIX: TF modules (budget-enforcer, github-token, create-handler) use ${var.resource_prefix} for log-group names/ARNs                  | VERIFIED   | budget-enforcer/main.tf line 260; github-token/main.tf line 159; create-handler/main.tf lines 41-42 use /${var.resource_prefix}/sandboxes/*  |
| 15 | DBG-SRCFIX: userdata.go + service_hcl.go use dynamic ResourcePrefix; byte-identity golden tests prove km-install is unchanged               | VERIFIED   | userdata.go line 1197: /{{ .ResourcePrefix }}/sandboxes/; service_hcl.go lines 355/361; TestUserdataKmPrefixByteIdentity and TestServiceHCLKmPrefixByteIdentity pass |

**Score:** 15/15 truths verified

### Required Artifacts

| Artifact                                               | Expected                                                                        | Status     | Details                                                                                  |
|--------------------------------------------------------|---------------------------------------------------------------------------------|------------|------------------------------------------------------------------------------------------|
| `internal/app/cmd/doctor_log_groups.go`                | checkStaleLogGroups, applyLogRetention, four families + legacy/prefixed names   | VERIFIED   | 343 lines; all functions present; logGroupFilterEntry named type; perSandboxFamilies deduped |
| `internal/app/cmd/doctor_log_groups_test.go`           | 16 table-driven tests                                                           | VERIFIED   | 16 TestDoctor_StaleLogGroups_* functions present                                         |
| `internal/app/cmd/doctor_ddb_rows.go`                  | checkOrphanedDDBRows, attrStr, ddbDeleteOp, four scan helpers                   | VERIFIED   | 384 lines; all functions present; BUDGET#ai# and status guards verified                  |
| `internal/app/cmd/doctor_ddb_rows_test.go`             | 13 table-driven tests including AI-preservation and status-guard                | VERIFIED   | 13 TestDoctor_OrphanedDDBRows_* functions present                                        |
| `internal/app/cmd/doctor_artifacts.go`                 | checkS3LifecyclePolicy, transientPrefixes, lifecycleRulePrefix                  | VERIFIED   | All three present; build-artifact prefixes explicitly absent from transientPrefixes       |
| `internal/app/cmd/doctor_artifacts_test.go`            | 7 TestDoctor_S3LifecyclePolicy_* tests                                          | VERIFIED   | All 7 present                                                                            |
| `internal/app/cmd/doctor.go`                           | Three interfaces, DoctorDeps fields, flags, runDoctor threading, buildChecks    | VERIFIED   | CWLogsCleanupAPI/DDBScanDeleteAPI/S3LifecycleAPI at lines 177-209; DoctorDeps at 481-496; flags at 2490-2502; all three checks in buildChecks |
| `internal/app/config/config.go`                        | Five-touchpoint pattern for both new config knobs                               | VERIFIED   | All five touchpoints confirmed for DoctorLogRetentionDays and DoctorS3ExpireDays         |
| `internal/app/cmd/doctor_test.go`                      | mockCWLogsCleanup, mockDDBScanDelete, mockS3Lifecycle fakes                     | VERIFIED   | All three fakes at lines 2247-2318; TestDoctorFlags_WithDeletesImpliesNewFlags present   |
| `infra/modules/budget-enforcer/v1.0.0/main.tf`         | ${var.resource_prefix} in log-group name                                        | VERIFIED   | Line 260: /aws/lambda/${var.resource_prefix}-budget-enforcer-${var.sandbox_id}           |
| `infra/modules/github-token/v1.0.0/main.tf`            | ${var.resource_prefix} in log-group name                                        | VERIFIED   | Line 159: /aws/lambda/${var.resource_prefix}-github-token-refresher-${var.sandbox_id}    |
| `infra/modules/create-handler/v1.0.0/main.tf`          | /${var.resource_prefix}/sandboxes/* in IAM ARNs                                 | VERIFIED   | Lines 41-42: ARNs use /${var.resource_prefix}/sandboxes/*                                |
| `pkg/compiler/userdata.go`                             | /{{ .ResourcePrefix }}/sandboxes/ (dynamic)                                     | VERIFIED   | Line 1197: CW_LOG_GROUP=/{{ .ResourcePrefix }}/sandboxes/{{ .SandboxID }}/               |
| `pkg/compiler/service_hcl.go`                          | /{{ .ResourcePrefix }}/ for ECS CW_LOG_GROUP + awslogs-group                   | VERIFIED   | Lines 355/361 use {{ .ResourcePrefix }}; ecsHCLParams.ResourcePrefix struct field at 536 |
| `pkg/compiler/userdata_94_05_prefix_test.go`           | TestUserdataKmPrefixByteIdentity golden test                                    | VERIFIED   | File present; test passes                                                                |
| `pkg/compiler/service_hcl_94_05_prefix_test.go`        | TestServiceHCLKmPrefixByteIdentity + TestServiceHCLKmPrefixContainsDynamicPaths| VERIFIED   | File present; tests pass                                                                 |
| `pkg/compiler/testdata/service_hcl_km_prefix_94_05.golden.hcl` | ECS km-prefix baseline golden                                          | VERIFIED   | File present (5784 bytes)                                                                |

### Key Link Verification

| From                                      | To                                              | Via                                                          | Status   | Details                                                                                 |
|-------------------------------------------|-------------------------------------------------|--------------------------------------------------------------|----------|-----------------------------------------------------------------------------------------|
| buildChecks in doctor.go                  | checkStaleLogGroups                             | closure with deps.CWLogsCleanupClient + cfg.GetResourcePrefix | WIRED    | doctor.go line 2965; all dep fields captured in closure locals above                    |
| buildChecks in doctor.go                  | checkOrphanedDDBRows                            | closure with deps.DDBScanDeleteClient + four table names      | WIRED    | doctor.go line 2989; deps wired in buildChecks closure at line 2973+                    |
| buildChecks in doctor.go                  | checkS3LifecyclePolicy                          | closure with deps.S3LifecycleClient + cfg.GetDoctorS3ExpireDays | WIRED  | doctor.go line 3209; s3Lifecycle + s3ExpireDays captured above                          |
| config.Load merge-list                    | Config.DoctorLogRetentionDays / DoctorS3ExpireDays | merge-list entries + GetInt + clamp                       | WIRED    | config.go lines 389-390 (merge-list), 458-459 (GetInt), 516-521 (clamps)                |
| NewDoctorCmdWithDeps flag block           | runDoctor → deps.DeleteLogs/DeleteDDBRows/SetLogRetention/SetS3Lifecycle | four new bool params                             | WIRED    | doctor.go lines 2463-2464 (fan-out), 2466 (runDoctor call), 2545-2548 (deps wiring)    |
| initRealDepsWithExisting                  | CWLogsCleanupClient / DDBScanDeleteClient / S3LifecycleClient | NewFromConfig(awsCfg) assignments                | WIRED    | doctor.go lines 3584-3586                                                               |
| perSandboxFamilies(prefix)               | both legacy km-//km/ AND {prefix}-//{prefix}/ filters | deduplication via seenFilters map                        | WIRED    | doctor_log_groups.go lines 62-83 (both pairs built), 158-166 (dedup)                    |

### Requirements Coverage

| Requirement   | Source Plan | Description                                                                                                          | Status          | Evidence                                                                         |
|---------------|-------------|----------------------------------------------------------------------------------------------------------------------|-----------------|----------------------------------------------------------------------------------|
| DBG-INFRA     | 94-01       | Three mocked-API interfaces + DoctorDeps client/bool fields + initRealDepsWithExisting wiring                       | SATISFIED       | Interfaces at doctor.go 177-209; fields 481-496; real wiring 3584-3586           |
| DBG-CFG       | 94-01       | doctor_log_retention_days / doctor_s3_expire_days via five-touchpoint pattern incl. merge-list                       | SATISFIED       | All five touchpoints verified in config.go; accessors in doctor.go 280-281       |
| DBG-FLAGS     | 94-01       | Four flags threaded through runDoctor; --with-deletes fans out to delete flags only                                  | SATISFIED       | Flags 2490-2502; fan-out 2461-2464; runDoctor signature 2512; TestDoctorFlags passes |
| DBG-LOGS      | 94-02       | checkStaleLogGroups enumerates all four log-group families, orphan→WARN, --delete-logs reclaims                      | SATISFIED       | All four families in perSandboxFamilies(); 16 tests including OrphanDetected, DeleteFlag |
| DBG-LOGS-PREFIX | 94-02     | Lambda families matched on BOTH literal km- AND dynamic {resource_prefix} (both name shapes)                         | SATISFIED       | perSandboxFamilies builds both pairs per family; _KmPrefix and _BothNames tests verify |
| DBG-LOGS-RET  | 94-02       | --set-log-retention idempotently sets retentionInDays; management groups never deleted                               | SATISFIED       | applyLogRetention() lines 299-342; RetentionInDays!=nil skip; _ManagementGroupsNeverDeleted test |
| DBG-DDB       | 94-03       | checkOrphanedDDBRows scans four tables, orphan→WARN, --delete-ddb-rows reclaims                                     | SATISFIED       | Four scan helpers in doctor_ddb_rows.go; checkOrphanedDDBRows registered in buildChecks |
| DBG-DDB-AI    | 94-03       | BUDGET#ai# rows preserved unconditionally                                                                            | SATISFIED       | strings.HasPrefix(sk, "BUDGET#ai#") guard line 194; _PreserveAI test             |
| DBG-DDB-GUARD | 94-03       | sandboxes rows purged only for status∈{failed,nocap}; starting and others skipped                                   | SATISFIED       | sandboxDeletableStatuses map lines 308-311; _StartingSkipped test                |
| DBG-DDB-SLACK | 94-03       | slack-threads orphans via non-key sandbox_id; rows missing it skipped                                               | SATISFIED       | scanSlackThreadsTable() checks sandbox_id == "" skip (line 281); _SlackThreadNoSandboxID test |
| DBG-S3        | 94-04       | checkS3LifecyclePolicy WARNs when no expiry rule covers transient prefixes                                          | SATISFIED       | transientPrefixes var; coverage loop lines 305-315; _Missing test                |
| DBG-S3-SET    | 94-04       | --set-s3-lifecycle installs transient-only expiry rules, preserves existing, never touches build-artifact prefixes  | SATISFIED       | Merge-and-put logic; toolchain/sidecars/rsync absent from transientPrefixes; _SetMergesPreservesExisting test |
| DBG-PAGE      | 94-02/03    | Full pagination on DescribeLogGroups (NextToken) and DynamoDB Scan (LastEvaluatedKey)                               | SATISFIED       | describeAllLogGroupsForFilter NextToken loop; all scan*Table LastEvaluatedKey loop; _Pagination tests |
| DBG-MULTI     | 94-02       | Multi-install isolation via active-set diff; sibling-install sandbox IDs excluded                                   | SATISFIED       | Active-set diff in checkStaleLogGroups (step 4-5); _IgnorePrefix test documents behavior |
| DBG-SRCFIX    | 94-05       | log groups CREATED with dynamic {resource_prefix} in TF + compiler; byte-identical on km; check matches both names  | SATISFIED       | Three TF modules migrated; userdata.go + service_hcl.go migrated; two golden tests pass |
| DBG-UAT       | N/A         | Live reclamation against kph install (manual; requires real AWS account)                                            | MANUAL-ONLY     | Cannot verify programmatically — see Human Verification Required section          |

### Anti-Patterns Found

No anti-patterns found. Reviewed all new/modified files:

- `doctor_log_groups.go`: No TODOs, no stubs, no empty returns. Full implementation.
- `doctor_ddb_rows.go`: No TODOs, no stubs, no placeholder returns. Full implementation.
- `doctor_artifacts.go` additions: No TODOs, no stubs. Build-artifact prefixes explicitly excluded by design comment.
- `doctor.go` additions: Guardrail exclusion from --with-deletes is intentional and documented inline (Pitfall 6 comment).
- TF modules: terraform fmt -check clean (94-05 summary confirms).
- Config knobs: No silent ignoring; merge-list confirmed.

### Human Verification Required

#### 1. Live AWS Reclamation (DBG-UAT)

**Test:** On the kph install, run `km doctor --delete-logs --delete-ddb-rows --set-log-retention --set-s3-lifecycle --dry-run=false` (after `make build-lambdas && km init --dry-run=false` to deploy the TF prefix fix)
**Expected:** ~271 orphaned log groups deleted, orphaned DDB rows reclaimed, retention set on management groups lacking it, transient-prefix expiry rules installed on artifacts bucket
**Why human:** Requires real AWS credentials and a live kph-prefix non-default install. Cannot mock live CloudWatch/DynamoDB/S3 at scale.

### Gaps Summary

No gaps. All 15 observable truths verified, all 15 requirement IDs accounted for (14 satisfied by code + tests, 1 DBG-UAT designated manual-only per REQUIREMENTS.md), all key links wired, all commits verified in git history, all targeted test suites green.

---

_Verified: 2026-06-05T01:52:24Z_
_Verifier: Claude (gsd-verifier)_
