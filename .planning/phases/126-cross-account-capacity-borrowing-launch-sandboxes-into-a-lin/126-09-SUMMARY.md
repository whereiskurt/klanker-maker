---
phase: 126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin
plan: 09
subsystem: doctor
tags: [go, doctor, cross-account, sts, s3-bucket-policy, ec2, dynamodb]

requires:
  - phase: 126-01
    provides: "config.LaunchAccountConfig + Config.GetLaunchAccount(s) getters"
  - phase: 126-03
    provides: "pkg/aws.AssumeRoleConfig fail-closed cross-account credential helper + NewAssumeRoleSTSClient seam"
  - phase: 126-07
    provides: "account_register.go's artifactsGrantSid helper + getArtifactsPolicyDocument parser + ArtifactsPolicyS3API"
provides:
  - "cmd.checkLaunchAccountLinks — pure, no-AWS validation of every configured link's structural completeness"
  - "cmd.checkLaunchAccountAssumable — forces real credential resolution against each link's launcher role"
  - "cmd.checkLaunchAccountArtifactsGrant — verifies the read-only artifacts-bucket grant is present and not widened"
  - "cmd.checkLaunchAccountOrphanInstances — surfaces a linked-account EC2 instance with no home DynamoDB row"
  - "DoctorConfigProvider.GetLaunchAccounts() + four new DoctorDeps fields wired into buildChecks/initRealDepsWithExisting"
affects: []

tech-stack:
  added: []
  patterns:
    - "Aggregate multi-link CheckResult (one CheckResult summarizing every configured link) rather than one CheckResult per link, matching checkGitHubPeerBridges/checkGitHubReposResolvable's existing convention"
    - "resolveLaunchAccountAssumedConfig: a single shared (paramErr, assumeErr) helper consumed by both the assumable check and the orphan-instance check, so a missing external-id secret and a broken trust policy are always distinguishable the same way in both checks"

key-files:
  created:
    - internal/app/cmd/doctor_launch_account_test.go
  modified:
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go

key-decisions:
  - "Both plan tasks landed in one commit, not two — they share the same DoctorDeps struct edit, the same resolveLaunchAccountAssumedConfig helper, and the same test file, making a clean per-task commit boundary artificial. Mirrors the 126-07 precedent (which combined its two tasks for the identical reason: shared files/helpers)."
  - "checkLaunchAccountAssumable forces credential resolution via assumedCfg.Credentials.Retrieve(ctx) — pkg/aws.AssumeRoleConfig wraps a lazy, cached credentials provider that never contacts STS until read, so merely constructing the config would let a broken trust policy report healthy (T-126-50)."
  - "A failed external-id SSM read and a failed role-assume are reported as two distinct warning buckets (not a single generic failure), so an operator can immediately tell a missing/rotated secret from a broken trust policy — and the assume step is never attempted after a parameter-read failure."
  - "checkLaunchAccountArtifactsGrant treats a bucket with no policy attached at all as 'every link missing' (via the existing getArtifactsPolicyDocument NoSuchBucketPolicy handling), not as an error — an install that has never registered a link legitimately has no bucket policy yet."
  - "checkLaunchAccountArtifactsGrant flags ANY action beyond the exact pair {s3:GetObject, s3:ListBucket} as a widened grant, rather than checking for specific write verbs — the invariant is 'read-only in that direction', so anything else at all is a finding (T-126-48)."
  - "checkLaunchAccountOrphanInstances filters on the literal tag key \"tag:km:sandbox-id\" (never a prefix-derived key) — km tags every sandbox resource under the fixed km: namespace regardless of resource_prefix, matching the existing checkOrphanedEC2 convention exactly."
  - "An unreachable link (param-read/assume/describe failure) is reported as its own WARN case, distinct from and never collapsed into 'no orphans found' — the whole point of the check is defeated if an unreachable link silently reads as clean (T-126-47 / Test 8)."

requirements-completed: [REQ-126-DOCTOR]

coverage:
  - id: D1
    description: "checkLaunchAccountLinks validates launcher ARN, region, subnet/AZ presence and length parity, external-id param path, box role ARN, and results bucket, plus a separate single-subnet warning; skips with zero AWS calls when no links are configured"
    requirement: "REQ-126-DOCTOR"
    verification:
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountLinks_NoLinks_Skipped"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountLinks_WellFormed_OK"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountLinks_MissingFields_Warn"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountLinks_SubnetAZLengthMismatch_Warn"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountLinks_SingleSubnet_Warn"
        status: pass
    human_judgment: false
  - id: D2
    description: "checkLaunchAccountAssumable forces real credential resolution per link, reports success naming the link and resolved account, and distinguishes a failed external-id read from a failed assume — never OK on any failure"
    requirement: "REQ-126-DOCTOR"
    verification:
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountAssumable_NoLinks_SkippedNoAWSCalls"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountAssumable_Success_OKNamesLinkAndAccount"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountAssumable_ForcesCredentialResolution"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountAssumable_FailedAssume_NeverOK"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountAssumable_FailedExternalIDRead_DistinctFromFailedAssume"
        status: pass
    human_judgment: false
  - id: D3
    description: "checkLaunchAccountArtifactsGrant reads the home artifacts bucket policy once, derives each link's Sid via the shared artifactsGrantSid helper, and warns distinctly on a missing statement vs. a statement widened beyond s3:GetObject/s3:ListBucket"
    requirement: "REQ-126-DOCTOR"
    verification:
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountArtifactsGrant_NoLinks_SkippedNoAWSCalls"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountArtifactsGrant_Present_OK"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountArtifactsGrant_Missing_Warn"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountArtifactsGrant_WriteAction_WarnNotOK"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountArtifactsGrant_UsesSharedSidHelper"
        status: pass
    human_judgment: false
  - id: D4
    description: "checkLaunchAccountOrphanInstances filters linked-account instances on the literal km:sandbox-id tag, compares against the home sandbox inventory, and reports an unreachable link distinctly from a clean 'no orphans' result"
    requirement: "REQ-126-DOCTOR"
    verification:
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountOrphanInstances_NoLinks_SkippedNoAWSCalls"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountOrphanInstances_UnmatchedInstance_Warn"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountOrphanInstances_MatchedInstance_OK"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountOrphanInstances_FiltersOnFixedTagKey"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountOrphanInstances_UnreachableLink_WarnNotCleanResult"
        status: pass
    human_judgment: false
  - id: D5
    description: "km doctor runs end to end on a link-free configuration and shows all four new checks as SKIPPED (buildChecks registration proof, not just direct function calls)"
    requirement: "REQ-126-DOCTOR"
    verification:
      - kind: unit
        ref: "internal/app/cmd/doctor_launch_account_test.go#TestDoctorLaunchAccountChecks_EndToEnd_NoLinksAllSkipped"
        status: pass
    human_judgment: false

duration: ~50min
completed: 2026-08-22
status: complete
---

# Phase 126 Plan 09: km doctor cross-account link checks Summary

**Four new read-only `km doctor` checks — link well-formed, launcher assumable (with forced credential resolution), artifacts grant present and unwidened, and orphaned linked-account instances — all skip silently with zero AWS calls when no `launch_accounts` links are configured.**

## Performance

- **Duration:** ~50 min
- **Tasks:** 2 completed (landed in one commit — see Decisions Made)
- **Files modified:** 2 modified (`doctor.go`, `doctor_test.go`), 1 created (`doctor_launch_account_test.go`)

## Accomplishments

- `checkLaunchAccountLinks` — pure, no-AWS structural validation of every configured link: launcher ARN, region, non-empty subnet list, subnet/AZ length parity (the AZ-failover sweep pairs them by index), external-id parameter path, box role ARN, results bucket, plus a separate warning when a link has exactly one subnet (the sweep collapses to a single attempt).
- `checkLaunchAccountAssumable` — reads each link's external-id SSM SecureString, assumes the launcher role via the Plan 03 `pkg/aws.AssumeRoleConfig` seam, then **forces real credential resolution** with `assumedCfg.Credentials.Retrieve(ctx)` — `AssumeRoleConfig` wraps a lazy, cached provider that never contacts STS until read, so skipping this call would let a broken trust policy report healthy. A failed external-id parameter read is reported in a distinct bucket from a failed assume, and the assume step is skipped entirely after a parameter-read failure.
- `checkLaunchAccountArtifactsGrant` — reads the home artifacts bucket policy exactly once, derives each link's expected statement Sid through the shared `artifactsGrantSid` helper from `account_register.go` (Plan 07) rather than an inline literal, and warns on both a missing statement and a statement carrying any action beyond `s3:GetObject`/`s3:ListBucket`.
- `checkLaunchAccountOrphanInstances` — assumes each link's launcher role, describes non-terminated EC2 instances filtered on the fixed literal tag key `tag:km:sandbox-id` (never prefix-derived), and flags any instance whose sandbox id has no corresponding row in the home account's sandbox inventory. A link that can't be reached is reported as "could not check", never silently folded into "no orphans found".
- All four checks registered in `buildChecks` following the existing config-getter → skip-when-unconfigured → downgrade-error-to-warning convention, and wired with real AWS clients in `initRealDepsWithExisting`.

## Task Commits

Both tasks landed in a single commit (see Decisions Made below for why):

1. **Tasks 1 & 2: four cross-account link doctor checks** - `4f739e3a` (feat)

**Plan metadata:** _pending — see State Updates below_

## Files Created/Modified

- `internal/app/cmd/doctor.go` — `DoctorConfigProvider.GetLaunchAccounts()` + `appConfigAdapter` impl; `LaunchAccountAssumeRoleFunc` / `LaunchAccountEC2ClientFunc` types; four new `DoctorDeps` fields (`LaunchAccountSSMClient`, `LaunchAccountAssumeRole`, `LaunchAccountEC2ClientFactory`, `LaunchAccountArtifactsPolicyClient`); `sortedLaunchAccountLinkNames`, `resolveLaunchAccountAssumedConfig`, `checkLaunchAccountLinks`, `checkLaunchAccountAssumable`, `launchAccountGrantActionStrings`, `launchAccountGrantIsReadOnly`, `checkLaunchAccountArtifactsGrant`, `checkLaunchAccountOrphanInstances`; four registrations in `buildChecks`; wiring block in `initRealDepsWithExisting`.
- `internal/app/cmd/doctor_test.go` — `GetLaunchAccounts()` added to the `testConfig`/`testDoctorConfig` stubs so they keep satisfying the widened `DoctorConfigProvider` interface (`doctorStaleAMIConfig` inherits it via struct embedding, needed no change).
- `internal/app/cmd/doctor_launch_account_test.go` — new: 21 tests, plus shared call-counting stubs (`countingSSMReadStub`, `countingAssumeRoleStub`, `countingArtifactsPolicyStub` with panic-on-write/delete, `recordingEC2Stub`) and one end-to-end `buildChecks`+`runChecks` test.

## Decisions Made

See `key-decisions` in the frontmatter above — duplicated here for scanning convenience:

1. Both tasks landed in one commit — they share the same `DoctorDeps` struct edit, the same `resolveLaunchAccountAssumedConfig` helper, and the same test file; a clean per-task commit split would have been artificial. Mirrors the 126-07 precedent, which combined its two tasks for the identical shared-file/shared-helper reason.
2. `checkLaunchAccountAssumable` forces credential resolution via `Credentials.Retrieve(ctx)` rather than trusting config construction alone (T-126-50).
3. Failed external-id read and failed assume are two distinct warning buckets, and the assume step is never attempted after a parameter-read failure.
4. A bucket with no policy attached at all is "every link missing", not an error — a fresh install legitimately has no policy yet.
5. Any action beyond the exact `{s3:GetObject, s3:ListBucket}` pair is treated as a widened grant — the invariant is "read-only", so a positive allowlist of two actions is safer than a negative check for specific write verbs.
6. The orphan-instance tag filter is the literal `"tag:km:sandbox-id"`, matching the existing `checkOrphanedEC2` convention exactly (never prefix-derived).
7. An unreachable link is its own distinct WARN case — collapsing it into "no orphans found" would defeat the entire point of the check (T-126-47).

## Verification

- `go build ./...` — exits 0.
- `go vet ./internal/app/cmd/...` — clean.
- `gofmt -l internal/app/cmd/doctor.go internal/app/cmd/doctor_test.go internal/app/cmd/doctor_launch_account_test.go` — clean after formatting.
- `go test ./internal/app/cmd/... -run 'DoctorLaunchAccount' -timeout 600s -count=1` — **PASS**, 21/21 subtests (process exit code 0, checked directly).
- `go test ./internal/app/cmd/... -timeout 600s -count=1` — the only failures are the five pre-existing known ones the team lead named (`TestBootstrapSCPApplyPath`, `TestBootstrapSCPSkipped_OrganizationBlank`, `TestClusterAdd`, `TestClusterRm`, `TestClusterAddPersistFailure`) plus three additional `Configure*` interactive-wizard tests (`TestConfigureWizardWritesResourcePrefixAndEmailSubdomain`, `TestConfigureWizardDefaultsApply`, `TestConfigureInteractivePromptsUseNewNames`) that hit the *same* root cause documented in this repo's memory — `internal/app/cmd`'s `TestMain` fast-fails `LoadAWSConfig`, and `Configure` (like `Bootstrap`/`Cluster`) still needs to resolve real AWS SSO credentials for part of its flow. Confirmed unrelated to this plan's diff: `grep -rln DoctorConfigProvider internal/app/cmd/*.go` returns only `doctor*.go` files — nothing in `configure.go`/`bootstrap.go`/`cluster.go` references the interface this plan touched. `TestRunAgentAuthClaude_TeesAndCleans` failed once under full-suite load and passed on every other run (individually and in two subsequent full-suite runs) — flaky/load-sensitive, not a regression.
- `make test` (excludes `internal/app/cmd` by design) — **PASS**, all packages including `pkg/hygiene` (confirms no hardcoded `km-` literal was introduced — the fixed `"tag:km:sandbox-id"` literal is the existing project convention, matching `checkOrphanedEC2` exactly, not a resource-prefix violation).
- `grep -n "checkLaunchAccountLinks\|checkLaunchAccountAssumable\|checkLaunchAccountArtifactsGrant\|checkLaunchAccountOrphanInstances" internal/app/cmd/doctor.go` — every occurrence is a definition, a registration in `buildChecks`, or a doc comment; **none appears inside any deletion/sweep function** in this file.
- `grep -n "artifactsGrantSid" internal/app/cmd/doctor.go` — the grant check calls the shared helper (`sid := artifactsGrantSid(prefix, n)`), not an inline literal.
- The exact tag-filter literal asserted by `TestDoctorLaunchAccountOrphanInstances_FiltersOnFixedTagKey` is `"tag:km:sandbox-id"`.
- `TestDoctorLaunchAccountChecks_EndToEnd_NoLinksAllSkipped` output (the four required SKIPPED lines, `km doctor` run end-to-end via `buildChecks`+`runChecks` against a link-free config):
  ```
  SKIPPED: Launch Account Links — no launch_accounts configured
  SKIPPED: Launch Account Assumable — no launch_accounts configured
  SKIPPED: Launch Account Artifacts Grant — no launch_accounts configured
  SKIPPED: Launch Account Orphan Instances — no launch_accounts configured
  ```

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking issue] Widened `DoctorConfigProvider` interface broke existing test stubs**
- **Found during:** Task 1, first `go vet ./internal/app/cmd/...` after adding `GetLaunchAccounts()` to the interface.
- **Issue:** `testConfig` and `testDoctorConfig` in `doctor_test.go` (pre-existing, not part of this plan's file list) no longer satisfied `DoctorConfigProvider` once the interface grew a new method — a compile-time blocker, not a logic bug.
- **Fix:** Added a one-line `GetLaunchAccounts() map[string]appcfg.LaunchAccountConfig { return nil }` to both stubs. `doctorStaleAMIConfig` needed no change — it embeds `testDoctorConfig` and inherits the method.
- **Files modified:** `internal/app/cmd/doctor_test.go`.
- **Commit:** `4f739e3a` (folded into the single implementation commit).

### Process note (not a behavioral deviation)

The plan scopes both tasks to the same two production files, and Task 2's `checkLaunchAccountOrphanInstances` directly reuses Task 1's `resolveLaunchAccountAssumedConfig` helper. Given that shared-file, shared-helper coupling — and following the 126-07 plan's precedent for the identical situation — both tasks were implemented, verified, and committed together rather than split into two commits with an artificial intermediate state. Every acceptance criterion for both tasks is independently satisfied and independently test-covered (see `coverage` above), so nothing about correctness or test isolation was compromised by the combined commit.

## Known Stubs

None — every code path is fully wired. No hardcoded empty returns, no placeholder UI, no TODO markers.

## Threat Flags

None beyond the plan's own `<threat_model>` (T-126-47 through T-126-51), all of which are mitigated and test-covered as described above (see Accomplishments and Decisions Made). No new network endpoint, auth path, or schema change was introduced beyond what the plan specified — this plan only reads existing cross-account surface (launcher role assume, artifacts bucket policy, linked-account EC2 instances) that Plans 01/02/03/05/07/08 already created.

## Self-Check: PASSED

- `internal/app/cmd/doctor_launch_account_test.go` — FOUND
- `internal/app/cmd/doctor.go` (modified) — FOUND, contains all four new check functions
- `internal/app/cmd/doctor_test.go` (modified) — FOUND, contains `GetLaunchAccounts()` stubs
- Commit `4f739e3a` — FOUND in `git log --oneline`
