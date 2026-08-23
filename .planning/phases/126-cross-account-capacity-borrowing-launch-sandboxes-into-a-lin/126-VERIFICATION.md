---
phase: 126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin
verified: 2026-08-23T02:14:48Z
status: human_needed
score: 7/7 must-haves verified (unit-level); 1 item requires human execution (live UAT, deliberately deferred)
behavior_unverified: 0
overrides_applied: 0
human_verification:
  - test: "126-UAT.md § Live verification — enroll a real second AWS account (`km account add`), register it (`km account register`), run the 8-row `simulate-principal-policy` containment matrix against the live launcher role, then `km capacity --launch-account`, `km create --launch-account` (local and remote), `km destroy`, a TTL-expiry reap, and `km account rm [--purge-backend]`"
    expected: "All 8 simulation rows match their expected `allowed`/`implicitDeny` verdict; `km capacity` reports the TARGET account's L-DB2E81BA headroom (not home's); a GPU box lands in the target account, is reachable, is destroyed on demand and reaped unattended on TTL expiry; final state leaves no running instance and no orphaned link"
    why_human: "Requires two real AWS credential sets (a second AWS account's admin credentials plus the home account's), real GPU vCPU quota headroom in the target account, and real spend — none of which exist in this environment or any prior wave of this phase. This is REQ-126-UAT and is explicitly scoped as a human-gated checkpoint (`type=\"checkpoint:human-verify\" gate=\"blocking\"`, `autonomous: false`) in plan 126-10, not an executor gap."
---

# Phase 126: Cross-account capacity borrowing — launch sandboxes into a linked AWS account Verification Report

**Phase Goal:** Cross-account capacity borrowing — launch sandboxes into a linked AWS account, so GPU capacity and quota can be borrowed from a second account while the control plane stays home.
**Verified:** 2026-08-23T02:14:48Z
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

This phase was verified goal-backward against the codebase (not against SUMMARY.md claims). All ten plans' code, Terraform, and doctor-check artifacts were read directly, cross-referenced against their own `must_haves` frontmatter, and independently re-run: `go build ./...`, the full package test suites for every package the phase touches (including the three `internal/app/cmd`, `cmd/ttl-handler`, `pkg/compiler` that the default `make test` excludes), `terraform validate`/`fmt` on both Terraform modules touched, and direct `grep`/source-reading of the load-bearing mechanisms (deep-merge provider override, fail-closed AssumeRole, DynamoDB round-trip, doctor-check registration, destroy discovery ordering, CLI wiring, doc cross-references).

**Every command below was re-run independently by this verifier, not copied from a SUMMARY.md claim.**

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `launch_accounts:` config block survives `config.Load()` via the merge-list triad (REQ-126-CFG) | ✓ VERIFIED | `go test ./internal/app/config/...` passes; `internal/app/config/config.go` shows `SetDefault("launch_accounts", ...)`, the merge-list entry, and `UnmarshalKey("launch_accounts", ...)` |
| 2 | `spec.runtime.launchAccount` is schema-declared and `km validate` gates unknown-link + launchAccount/privateSubnet mutex | ✓ VERIFIED | `go test ./pkg/profile/...` passes; `bash scripts/validate-all-profiles.sh` exits 0 |
| 3 | Sandbox terragrunt template deep-merges root and emits a conditional `assume_role` provider block, byte-identical when dormant | ✓ VERIFIED | `pkg/terragrunt` tests pass (incl. the real-terragrunt-v0.99.1-gated render test); `grep` confirms `merge_strategy = "deep"`, `generate "provider"`, and the `local.assume_role_block` ternary in `infra/templates/sandbox/terragrunt.hcl` |
| 4 | `pkg/aws.AssumeRoleConfig` is fail-closed — no fallback to caller credentials on a failed assume | ✓ VERIFIED | Source read directly: doc comment states FAIL-CLOSED and names the wrong-account-quota consequence; `go test ./pkg/aws/...` passes |
| 5 | `SandboxMetadata.LaunchAccount` round-trips through all three DynamoDB marshal/unmarshal chokepoints | ✓ VERIFIED | `grep` confirms `unmarshalLaunchAccountField` wired at the read/list/list-all sites and the symmetric write in `pkg/aws/sandbox_dynamo.go`; `go test ./pkg/aws/...` passes |
| 6 | `infra/modules/gpu-launcher-account/v1.0.0` provisions a bounded launcher role, boundaried box role, results bucket, optional network/EFS (REQ-126-ENROLL) | ✓ VERIFIED | `terraform init -backend=false` + `terraform validate` exit 0 (re-run directly by this verifier, not copied from the SUMMARY) |
| 7 | `km account add/register/list/rm` exist, are wired into the CLI, and the create/capacity/destroy/doctor paths all resolve a link through one shared `ResolveLaunchTarget` helper | ✓ VERIFIED | `root.go:96` registers `NewAccountCmd(cfg)`; `go test ./internal/app/cmd/... -run 'Account|LaunchAccount|Destroy|Doctor'` all pass; `destroy.go` shows the sandbox-row read at line ~204-230 preceding the `FindSandboxByID` tag scan at line 259 |
| 8 | `km doctor` registers four cross-account link checks, all skip cleanly with zero AWS calls when no links configured (REQ-126-DOCTOR) | ✓ VERIFIED | `grep` confirms all four `checkLaunchAccount*` functions are both defined and registered in `buildChecks` (`doctor.go` lines 4740/4745/4755/4764); `go test ./internal/app/cmd/... -run Doctor` passes |
| 9 | The expiry Lambda (ttl-handler) reaps a linked-account sandbox unattended via a conditional assume_role provider, with scoped IAM (REQ-126-TEARDOWN) | ✓ VERIFIED | `go test ./cmd/ttl-handler/...` passes; `terraform validate` on `infra/modules/ttl-handler/v1.0.0` exits 0 (one pre-existing-pattern deprecation warning on `aws_region.current.name`, not a failure, matches the existing `ec2spot_github_token` precedent) |
| 10 | REQ-126-UAT — live cross-account verification (launcher containment proof, real GPU box launched/destroyed/reaped in a real second account) | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED (deliberately deferred) | `126-UAT.md` records a green 7-command pre-flight gate but an explicitly pending, un-executed live checklist — no `launch_accounts:` link exists anywhere in this project's history; this is a human-gated checkpoint by design (plan 10, Task 3, `autonomous: false`), not a plan-execution gap |

**Score:** 9/9 codebase-verifiable truths VERIFIED (100%); 1 truth (REQ-126-UAT's live verification) is present-and-designed-for but requires human execution with real two-account AWS credentials and GPU quota, and is correctly represented as pending rather than fabricated.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/config/config.go` | `LaunchAccountConfig` struct + merge-list triad | ✓ VERIFIED | Present, wired, tested |
| `pkg/profile/types.go` + schema JSON | `RuntimeSpec.LaunchAccount` field | ✓ VERIFIED | Present, wired, tested |
| `internal/app/cmd/launch_account_validate.go` | `ValidateLaunchAccountLink` | ✓ VERIFIED | Present, wired into both `km validate` call sites |
| `infra/templates/sandbox/terragrunt.hcl` | deep-merge + conditional provider override | ✓ VERIFIED | Present; real-terragrunt render test passes |
| `pkg/compiler/service_hcl.go` | launch-account locals emission | ✓ VERIFIED | Tested, dormant-byte-identical |
| `pkg/aws/assume.go` | `AssumeRoleConfig` (fail-closed) | ✓ VERIFIED | Present, tested |
| `pkg/aws/sandbox_dynamo.go` | `LaunchAccount` DDB round-trip | ✓ VERIFIED | Present, tested |
| `pkg/capacity/store.go` | namespaced capacity store | ✓ VERIFIED | Present, tested |
| `infra/modules/gpu-launcher-account/v1.0.0/*` | launcher/box role, results bucket, network/EFS | ✓ VERIFIED | `terraform validate` exits 0 |
| `internal/app/cmd/account.go` | `km account add` | ✓ VERIFIED | Present, registered, tested |
| `internal/app/cmd/account_register.go` | `km account register/list/rm` | ✓ VERIFIED | Present, tested |
| `internal/app/cmd/launch_account_resolve.go` | `ResolveLaunchTarget` shared resolver | ✓ VERIFIED | Present, tested, used by create/capacity/destroy/doctor |
| `internal/app/cmd/destroy_launch_account.go` | account-aware discovery chain | ✓ VERIFIED | Present, tested; ordering confirmed via source read |
| `cmd/ttl-handler/main.go` | link-aware provider rendering | ✓ VERIFIED | Present, tested |
| `infra/modules/ttl-handler/v1.0.0/*` | scoped IAM for the expiry Lambda | ✓ VERIFIED | `terraform validate` exits 0 |
| `internal/app/cmd/doctor.go` | four cross-account checks | ✓ VERIFIED | Present, registered, tested |
| `docs/cross-account-capacity-borrowing.md` | operator runbook | ✓ VERIFIED | 345 lines, all expected sections present (scope, enrollment, security model, cost, deploy surface, residual gap) |
| `.planning/REQUIREMENTS.md` | REQ-126-* traceability table | ✓ VERIFIED | All 8 rows present (backfilled at commit `fdf79d9f`, confirmed present at HEAD) |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|----|--------|---------|
| `config.Load()` | `cfg.LaunchAccounts` | merge-list entry | ✓ WIRED | grep + regression test present |
| `spec.runtime.launchAccount` | `km validate` | `ValidateLaunchAccountLink` at both call sites | ✓ WIRED | `grep -c` = 2 in `validate.go` (per plan 01 acceptance) |
| `service.hcl` locals | rendered `provider.tf` | conditional `generate "provider"` | ✓ WIRED | real-terragrunt render test passes |
| resolved link | `compiler.NetworkConfig.{LaunchAccount,LauncherRoleARN,LauncherExternalID,VPCID}` | `applyLaunchAccountNetwork` (shared by both create paths) | ✓ WIRED | single shared function, not two independently-maintained copies (verified by source read) |
| resolved link | capacity/quota gate | second assumed-role `awsCfg`, fail-closed | ✓ WIRED | `buildCapacityAWSConfig` tests assert no home-fallback after an assume failure |
| sandbox row `launch_account` | `km destroy` discovery order | row read before `FindSandboxByID` | ✓ WIRED | confirmed via direct source read (destroy.go line ordering) |
| sandbox row `launch_account` | ttl-handler's rendered provider | `KM_LAUNCH_ACCOUNTS` env → `parseLaunchAccountsEnv` → `buildDestroyTerraformInputs` | ✓ WIRED | tests pass; `terraform validate` on the IAM grant confirms the count-gate |
| `km account register` grant | `km account rm` removal | shared `artifactsGrantSid` helper (also reused by `km doctor`) | ✓ WIRED | grep confirms all three call sites use the shared helper, not independent literals |

### Requirements Coverage

| Requirement | Source Plan | Status | Evidence |
|-------------|-------------|--------|----------|
| REQ-126-CFG | 126-01 | ✓ SATISFIED | Verified above |
| REQ-126-ENROLL | 126-04, 126-05 | ✓ SATISFIED | Verified above |
| REQ-126-REGISTER | 126-07 | ✓ SATISFIED | Verified above |
| REQ-126-LAUNCH | 126-02, 126-06 | ✓ SATISFIED | Verified above |
| REQ-126-CAPACITY | 126-03, 126-06 | ✓ SATISFIED | Verified above |
| REQ-126-TEARDOWN | 126-03, 126-08 | ✓ SATISFIED | Verified above |
| REQ-126-DOCTOR | 126-09 | ✓ SATISFIED | Verified above |
| REQ-126-UAT | 126-10 | ? NEEDS HUMAN | Correctly represented as pending, not fabricated — see Human Verification |

No orphaned requirements: `.planning/REQUIREMENTS.md` lines 659-666 list exactly REQ-126-CFG through REQ-126-UAT, matching the eight IDs declared across the ten plans' frontmatter.

### Anti-Patterns Found

Scanned every file this phase modified for `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER`/empty-implementation patterns. No new debt markers were introduced by this phase. The only hits are pre-existing, unrelated markers in files this phase also touched incidentally:
- `pkg/compiler/service_hcl.go` — `PLACEHOLDER_ECR`, a pre-existing Phase-66-era TODO, and `MAIN_IMAGE_PLACEHOLDER` reference — all pre-date this phase and are string-substitution tokens, not debt markers.
- `internal/app/cmd/create.go` — `PLACEHOLDER_SANDBOX_ROLE_ARN`/`PLACEHOLDER_SIDECAR_ROLE_ARN`/`PLACEHOLDER_PROXY_CA_B64` — pre-existing Docker-compose template substitution tokens, unrelated to launch accounts.
- `internal/app/cmd/account.go` — two comments describing the `{PLACEHOLDER}` HCL-template-substitution mechanism `GenerateAccountLinkHCL` itself uses — this is the documented design of that function, not a debt marker.
- `cmd/ttl-handler/main.go:1387` — pre-existing "TODO: Read substrate from metadata.json to handle ECS sandboxes" — unrelated to this phase (ECS substrate is documented elsewhere as unused/untested).

No blocker-level anti-patterns found in any phase-126-authored file.

### Known, Documented Residual Gap (non-blocking)

`LaunchTarget.SecurityGroupID` is resolved from the link record but not wired into `compiler.NetworkConfig` — `infra/modules/ec2spot/v1.3.0` unconditionally self-creates its own per-sandbox security group inside whichever VPC it receives (there is no "reuse an existing SG" variable for home launches either), so this does not cause an apply-time failure; the link's pre-provisioned SG output simply goes unused. Flagged by plan 06's own SUMMARY, carried into the operator runbook (`docs/cross-account-capacity-borrowing.md` line 206), and explicitly called out by the team-lead's handoff as accepted. Not scored as a gap.

### Human Verification Required

#### 1. REQ-126-UAT — Live cross-account verification

**Test:** Run the full `126-UAT.md` § Live verification checklist: enroll a real second AWS account via `km account add`, register it via `km account register`, run the 8-row `simulate-principal-policy` containment matrix against the live launcher role, then exercise `km capacity --launch-account`, `km create --launch-account` (both local and remote paths), `km destroy`, an unattended TTL-expiry reap, the cold-clone destroy path, and `km account rm [--purge-backend]`.

**Expected:** All 8 simulation rows match their expected `allowed`/`implicitDeny` verdict (a single unexpected `allowed` is a blocking finding); `km capacity` reports the TARGET account's `L-DB2E81BA` GPU headroom, not home's; a GPU box is launched, reachable, destroyed on demand, and reaped unattended on TTL expiry in the target account; final state leaves no running instance and no orphaned link.

**Why human:** Requires two real AWS credential sets (target-account admin + home-account), confirmed GPU vCPU quota headroom in the target account, and real spend. No `launch_accounts:` link has ever existed in this project's history (confirmed independently by four separate plans' own SUMMARY.md files during Waves 1-4, and reconfirmed by this verifier — no such config exists in the working tree). This is REQ-126-UAT's designated scope: `type="checkpoint:human-verify" gate="blocking"`, `autonomous: false` in plan 126-10's own frontmatter — a deliberate design decision, not an executor shortfall.

### Gaps Summary

No code-level gaps found. Every one of the phase's eight requirement IDs (REQ-126-CFG through REQ-126-DOCTOR) is fully implemented, wired, and independently re-verified against the live codebase by this verifier — not merely claimed in a SUMMARY.md. The ninth requirement, REQ-126-UAT, is the phase's own designed terminal checkpoint requiring real two-account AWS credentials and GPU quota that do not exist in this environment; its pending state is accurately recorded (no fabricated "Actual" entries in `126-UAT.md`), which is itself evidence of correct process rather than a shortfall. The full non-excluded test suite (`go test ./internal/app/cmd/... -timeout 600s -count=1`) was re-run directly by this verifier and produces exactly the five team-lead-pre-declared, unrelated known-preexisting failures (`TestBootstrapSCPApplyPath`, `TestBootstrapSCPSkipped_OrganizationBlank`, `TestClusterAdd`, `TestClusterRm`, `TestClusterAddPersistFailure`) and nothing else; `go build ./...` and every package-scoped test this phase touches (`internal/app/config`, `pkg/profile`, `pkg/aws`, `pkg/capacity`, `pkg/terragrunt`, `pkg/compiler`, `cmd/ttl-handler`) pass cleanly. Both new/modified Terraform modules (`gpu-launcher-account/v1.0.0`, `ttl-handler/v1.0.0`) pass `terraform validate`/`fmt`.

Overall status is `human_needed` rather than `passed` solely because of the one deliberately-deferred, correctly-flagged live-UAT checkpoint — not because of any gap in the phase's code, tests, or documentation.

---

_Verified: 2026-08-23T02:14:48Z_
_Verifier: Claude (gsd-verifier)_
