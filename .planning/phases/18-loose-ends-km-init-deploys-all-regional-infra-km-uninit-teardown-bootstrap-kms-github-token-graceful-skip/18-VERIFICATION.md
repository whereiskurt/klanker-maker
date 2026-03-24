---
phase: 18-loose-ends
verified: 2026-03-23T00:00:00Z
status: passed
score: 12/12 must-haves verified
re_verification: false
human_verification:
  - test: "km init live run in us-east-1"
    expected: "All 6 modules applied in order; network outputs.json written; SES/s3-replication/ttl-handler skip with warning when env vars absent"
    why_human: "Requires real AWS credentials and live Terragrunt execution"
  - test: "km uninit --force live run"
    expected: "All 6 modules destroyed in reverse order; skips directories that don't exist; prints count of destroyed modules"
    why_human: "Requires real AWS credentials and live Terragrunt Destroy execution"
  - test: "km create with no GitHub App SSM params"
    expected: "Step 13a prints 'GitHub token skipped (not configured)' — no stack trace"
    why_human: "Requires live AWS SSM with missing parameter to trigger ParameterNotFound path"
  - test: "km doctor against live environment"
    expected: "Lambda check shows CheckOK or CheckWarn for km-ttl-handler; SES shows CheckOK or CheckWarn for sandboxes.{domain}"
    why_human: "Requires real Lambda and SES clients against a deployed environment"
  - test: "km bootstrap --show-prereqs output accuracy"
    expected: "Printed IAM role/trust policy matches actual SSO path and three-statement least-privilege SCP policy"
    why_human: "Output accuracy requires human review against real AWS SSO configuration"
---

# Phase 18: Loose Ends Verification Report

**Phase Goal:** Close all operational gaps discovered during live testing — `km init` deploys all regional infrastructure (not just the VPC), `km uninit` tears it all down, bootstrap creates the KMS platform key, github-token module skips gracefully when unconfigured, and `km configure` populates `state_bucket` automatically.

**Verified:** 2026-03-23
**Status:** passed (with human verification items for live-environment behaviors)
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | km init deploys all 6 regional modules in dependency order | VERIFIED | `regionalModules()` in init.go returns network, dynamodb-budget, dynamodb-identities, ses, s3-replication, ttl-handler in that order; `TestRunInitWithRunnerAllModules` verifies all 6 Apply calls |
| 2 | km init is idempotent — re-running succeeds without destroying existing state | VERIFIED | `TestRunInitIdempotent` passes; Apply is idempotent by Terraform design |
| 3 | km init skips modules with missing env vars with a warning | VERIFIED | `TestRunInitSkipsSESWithoutZoneID` (KM_ROUTE53_ZONE_ID), `TestRunInitSkipsArtifactModulesWithoutBucket` (KM_ARTIFACTS_BUCKET) both pass |
| 4 | km configure prompts for state_bucket and writes it to km-config.yaml | VERIFIED | `StateBucket` field in `platformConfig` struct; `--state-bucket` flag; `TestConfigureStateBucketFlag` and `TestConfigureStateBucketOmittedWhenEmpty` pass |
| 5 | km uninit destroys all 6 regional modules in reverse dependency order | VERIFIED | `RunUninitWithDeps` in uninit.go: ttl-handler, s3-replication, ses, dynamodb-identities, dynamodb-budget, network; `TestUninitDestroyOrder` passes |
| 6 | km uninit refuses to proceed if active sandboxes exist unless --force is passed | VERIFIED | `TestUninitRefusesWithActiveSandboxes`, `TestUninitProceedsWithForce`, `TestUninitActiveSandboxErrorMessage` all pass |
| 7 | km uninit handles missing directories gracefully (skip with warning, continue) | VERIFIED | `TestUninitSkipsMissingModuleDirectory` passes; os.IsNotExist check present in uninit.go |
| 8 | km uninit is registered in root command tree | VERIFIED | `root.AddCommand(NewUninitCmd(cfg))` present in root.go line 37, adjacent to NewInitCmd |
| 9 | ErrGitHubNotConfigured returned on ParameterNotFound; caller prints "skipped (not configured)" | VERIFIED | Sentinel defined in create.go; errors.As with ssmtypes.ParameterNotFound in all 3 GetParameter calls; `errors.Is(tokenErr, ErrGitHubNotConfigured)` in caller; `TestCreateGitHubSkip_CallerPrintsSkipMessage` passes |
| 10 | km doctor checks TTL handler Lambda exists | VERIFIED | `checkLambdaFunction` function in doctor.go; wired into `buildChecks()`; `TestDoctorLambda_OK`, `TestDoctorLambda_NotFound`, `TestDoctorLambda_NilClient` pass |
| 11 | km doctor checks SES domain verified identity | VERIFIED | `checkSESIdentity` function in doctor.go; wired into `buildChecks()`; `TestCheckSESIdentity_OK`, `TestCheckSESIdentity_NotFound`, `TestCheckSESIdentity_NilClient` pass |
| 12 | Bootstrap KMS key creation is testable and tested | VERIFIED | `KMSEnsureAPI` interface and variadic DI in bootstrap.go; `bootstrap_kms_test.go` covers key-already-exists and creates-key paths; both tests pass |

**Score:** 12/12 truths verified

---

## Required Artifacts

### Plan 18-01 Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/cmd/init.go` | Expanded km init applying all 6 regional modules | VERIFIED | `regionalModules()` returns 6 modules; `RunInitWithRunner` exported; env var checks and skip-with-warning logic present |
| `internal/app/cmd/init_test.go` | Unit tests for expanded init | VERIFIED | 7 tests including `TestRunInitWithRunnerAllModules`, `TestRunInitSkipsMissingDirectory`, `TestRunInitSkipsSESWithoutZoneID`, `TestRunInitSkipsArtifactModulesWithoutBucket`, `TestRunInitStopsOnApplyError`, `TestRunInitIdempotent` |
| `internal/app/cmd/configure.go` | state_bucket prompt in configure wizard | VERIFIED | `StateBucket string \`yaml:"state_bucket,omitempty"\`` in `platformConfig`; `--state-bucket` flag; prompt in interactive wizard |
| `internal/app/cmd/configure_test.go` | Test for state_bucket in configure | VERIFIED | `TestConfigureStateBucketFlag`, `TestConfigureStateBucketOmittedWhenEmpty` pass |

### Plan 18-02 Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/cmd/uninit.go` | km uninit command implementation | VERIFIED | `UninitRunner` interface, `RunUninitWithDeps`, `NewUninitCmd` all present; active-sandbox guard, reverse-order destroy loop |
| `internal/app/cmd/uninit_test.go` | Unit tests for uninit | VERIFIED | 11 tests covering destroy order, force flag, active sandbox guard, missing directories, error continuation |
| `internal/app/cmd/help/uninit.txt` | Help text for km uninit | VERIFIED | Exists; documents 6 modules in reverse order, --force behavior, and examples |
| `internal/app/cmd/root.go` | uninit registered in command tree | VERIFIED | `root.AddCommand(NewUninitCmd(cfg))` at line 37 |

### Plan 18-03 Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/cmd/create.go` | ErrGitHubNotConfigured sentinel and graceful skip | VERIFIED | Sentinel defined; `SSMGetPutAPI` interface; ParameterNotFound detection in all 3 SSM GetParameter calls; caller uses `errors.Is` |
| `internal/app/cmd/create_github_test.go` | Tests for github-token graceful skip | VERIFIED | 5 tests: AppClientIDNotFound, InstallationIDNotFound, AccessDeniedIsNotSkipped, CallerPrintsSkipMessage, NonFatalPreserved — all pass |

### Plan 18-04 Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/cmd/doctor.go` | Extended doctor checks for Lambda and SES | VERIFIED | `LambdaGetFunctionAPI`, `SESGetEmailIdentityAPI` interfaces; `checkLambdaFunction`, `checkSESIdentity` functions; wired into `buildChecks()`; clients initialized in `initRealDeps()` |
| `internal/app/cmd/doctor_test.go` | Tests for new doctor checks | VERIFIED | `TestDoctorLambda_OK/NotFound/NilClient`, `TestCheckSESIdentity_OK/NotFound/NilClient`, `TestBuildChecks_IncludesLambdaAndSES` — all pass |
| `internal/app/cmd/bootstrap_kms_test.go` | Bootstrap KMS test coverage | VERIFIED | `TestEnsureKMSPlatformKey_KeyAlreadyExists`, `TestEnsureKMSPlatformKey_CreatesKey` — both pass |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `internal/app/cmd/init.go` | `pkg/terragrunt` | `runner.Apply(ctx, mod.dir)` for each regional module | WIRED | `runner.Apply` called in loop over `regionalModules()`; line ~167 |
| `internal/app/cmd/configure.go` | `km-config.yaml` | `platformConfig` struct with `StateBucket` field | WIRED | `StateBucket string \`yaml:"state_bucket,omitempty"\`` present; written via yaml.Marshal |
| `internal/app/cmd/uninit.go` | `pkg/terragrunt` | `runner.Destroy(ctx, mod.dir)` for each module in reverse | WIRED | `runner.Destroy` called in loop over reversed module list; line ~150 |
| `internal/app/cmd/uninit.go` | `pkg/aws` | `SandboxLister.ListSandboxes` for active sandbox check | WIRED | `lister.ListSandboxes(ctx, false)` called when `lister != nil && !force`; filters by `r.Region == region && r.Status == "running"` |
| `internal/app/cmd/root.go` | `internal/app/cmd/uninit.go` | `root.AddCommand(NewUninitCmd(cfg))` | WIRED | Present at line 37 in root.go |
| `internal/app/cmd/create.go` | `ssm/types.ParameterNotFound` | `errors.As(err, &notFound)` detection | WIRED | Applied to all 3 SSM GetParameter calls (app-client-id, private-key, installation-id) |
| `internal/app/cmd/doctor.go` | `aws-sdk-go-v2/service/lambda` | `LambdaGetFunctionAPI` interface, `GetFunction` | WIRED | Interface defined; `checkLambdaFunction` calls `GetFunction`; `deps.LambdaClient = lambda.NewFromConfig(awsCfg)` in `initRealDeps()` |
| `internal/app/cmd/doctor.go` | `aws-sdk-go-v2/service/sesv2` | `SESGetEmailIdentityAPI` interface, `GetEmailIdentity` | WIRED | Interface defined; `checkSESIdentity` calls `GetEmailIdentity`; `deps.SESClient = sesv2.NewFromConfig(awsCfg)` in `initRealDeps()` |

---

## Requirements Coverage

The LE-* requirement IDs (LE-01 through LE-12) referenced in plan frontmatter are **phase-internal tracking IDs** that do not appear in `.planning/REQUIREMENTS.md`. REQUIREMENTS.md covers v1 requirements (SCHM-*, PROV-*, NETW-*, OBSV-*, MAIL-*, INFR-*, CFUI-*, CONF-*, BUDG-*). Phase 18 closes operational gaps discovered during live testing — these are sub-requirements derived from ROADMAP.md phase 18's goal description, not tracked in the canonical REQUIREMENTS.md.

**No orphaned REQUIREMENTS.md IDs exist for Phase 18** — the REQUIREMENTS.md traceability table does not map any IDs to Phase 18.

The 12 phase-internal LE items and their coverage:

| LE ID | Description | Source Plan | Status |
|-------|-------------|-------------|--------|
| LE-01 | km init all 6 regional modules | 18-01 | SATISFIED — `regionalModules()` returns 6 modules; all tests pass |
| LE-02 | km init idempotent | 18-01 | SATISFIED — `TestRunInitIdempotent` passes |
| LE-03 | km uninit reverse teardown | 18-02 | SATISFIED — reverse-order destroy loop; `TestUninitDestroyOrder` passes |
| LE-04 | km uninit sandbox guard | 18-02 | SATISFIED — active-sandbox check; 4 related tests pass |
| LE-05 | km bootstrap KMS key | 18-04 | SATISFIED — `KMSEnsureAPI` DI interface; 2 KMS tests pass |
| LE-06 | km bootstrap --show-prereqs | 18-04 | SATISFIED — `--show-prereqs` flag and `runShowPrereqs` already existed; verified present in bootstrap.go |
| LE-07 | github-token graceful skip | 18-03 | SATISFIED — `ErrGitHubNotConfigured` sentinel; 5 tests pass |
| LE-08 | km configure state_bucket | 18-01 | SATISFIED — `StateBucket` in `platformConfig`; 2 configure tests pass |
| LE-09 | km create warnings labeled "skipped" | 18-03 | SATISFIED — `errors.Is(tokenErr, ErrGitHubNotConfigured)` → "skipped (not configured)" print path confirmed |
| LE-10 | stale directory cleanup | 18-04 | SATISFIED — no stale `infra/live/network/` at top level; only `infra/live/use1/network/` exists (correct) |
| LE-11 | root.hcl verified | 18-04 | SATISFIED — `infra/live/root.hcl` exists and is the canonical include target; `site.hcl` is a locals-only file intentionally read by root.hcl (not stale) |
| LE-12 | km doctor checks all infra | 18-04 | SATISFIED — `checkLambdaFunction` and `checkSESIdentity` added; `TestBuildChecks_IncludesLambdaAndSES` confirms both appear in check list |

---

## Anti-Patterns Found

No blockers or warnings found. Reviewed key files modified in this phase:

| File | Pattern Checked | Finding |
|------|----------------|---------|
| `internal/app/cmd/init.go` | Empty returns, TODO, stub handlers | None found — substantive implementation |
| `internal/app/cmd/uninit.go` | Empty returns, stub handlers | None found — full implementation with real logic |
| `internal/app/cmd/configure.go` | Placeholder struct fields | None found — `StateBucket` fully wired |
| `internal/app/cmd/create.go` | Stub error handling | None found — `ErrGitHubNotConfigured` properly detected and handled |
| `internal/app/cmd/doctor.go` | Stub check functions | None found — `checkLambdaFunction` and `checkSESIdentity` have real AWS API calls |
| `internal/app/cmd/bootstrap_kms_test.go` | Trivial or placeholder tests | None found — tests assert on actual output content |

---

## Test Suite Status

Full test suite: **all 18 packages pass** (0 failures, 0 compilation errors).

Specific Phase 18 test coverage:

- Init tests: `TestInitAllModulesOrder`, `TestRunInitWithRunnerAllModules`, `TestRunInitSkipsMissingDirectory`, `TestRunInitSkipsSESWithoutZoneID`, `TestRunInitSkipsArtifactModulesWithoutBucket`, `TestRunInitStopsOnApplyError`, `TestRunInitIdempotent` — 7/7 pass
- Configure tests: `TestConfigureStateBucketFlag`, `TestConfigureStateBucketOmittedWhenEmpty` — 2/2 pass
- Uninit tests: 11/11 pass
- GitHub skip tests: 5/5 pass
- Doctor Lambda/SES tests: 7/7 pass
- Bootstrap KMS tests: 2/2 pass

---

## Human Verification Required

### 1. km init live run

**Test:** Run `km init --region us-east-1` with real AWS credentials (klanker-application profile) but without `KM_ROUTE53_ZONE_ID` and `KM_ARTIFACTS_BUCKET` set.
**Expected:** network, dynamodb-budget, dynamodb-identities are applied; ses, s3-replication, ttl-handler each print "[skip] — <env var> not set"; command exits 0.
**Why human:** Requires live Terragrunt execution and real AWS credentials.

### 2. km uninit live run

**Test:** Run `km uninit --force --region us-east-1` with real AWS credentials against a region where regional infra is deployed.
**Expected:** Modules destroyed in reverse order (ttl-handler first, network last); each module prints "Destroying <name>..."; exits with "N module(s) destroyed".
**Why human:** Requires live Terragrunt Destroy execution and real AWS state.

### 3. km create with no GitHub SSM params

**Test:** Run `km create` against a profile with `sourceAccess.github` configured, in an environment where `/github-app/app-client-id` SSM parameter does not exist.
**Expected:** Step 13a output is "Step 13a: GitHub token skipped (not configured)" — no stack trace, no zerolog error dump.
**Why human:** Requires real AWS SSM environment with the ParameterNotFound condition to actually occur.

### 4. km doctor against live environment

**Test:** Run `km doctor` against an initialized region (km init completed).
**Expected:** Lambda check shows "km-ttl-handler" as CheckOK or CheckWarn (not an error/panic); SES check shows domain identity status.
**Why human:** Requires real Lambda and SES clients against a live AWS account.

### 5. km bootstrap --show-prereqs accuracy

**Test:** Run `km bootstrap --show-prereqs` and compare the printed IAM role/trust policy against the actual SSO role path and SCP policy in the management account.
**Expected:** Three-statement least-privilege policy, correct SSO role path, SCP enable step all match current implementation.
**Why human:** Output accuracy requires human review against the actual AWS SSO configuration for this deployment.

---

## Gaps Summary

No gaps. All 12 observable truths are verified at all three levels (exists, substantive, wired). The full test suite passes (18 packages, 0 failures). The binary builds successfully.

Five items are flagged for human verification because they require live AWS environments, real Terragrunt execution, or live SSM parameters to exercise the runtime paths — these cannot be verified programmatically from the codebase alone.

---

_Verified: 2026-03-23_
_Verifier: Claude (gsd-verifier)_
