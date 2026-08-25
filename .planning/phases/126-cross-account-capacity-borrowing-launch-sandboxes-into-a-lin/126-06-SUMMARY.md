---
phase: 126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin
plan: 06
subsystem: infra
tags: [go, aws, sts, ec2, dynamodb, cross-account, capacity]

requires:
  - phase: 126-01
    provides: "launch_accounts config block + Config.GetLaunchAccount(s) getters, RuntimeSpec.LaunchAccount schema field"
  - phase: 126-02
    provides: "conditional cross-account provider override in the sandbox terragrunt template + service.hcl launch_account/launcher_role_arn/launcher_external_id locals"
  - phase: 126-03
    provides: "pkg/aws.AssumeRoleConfig fail-closed credential helper, SandboxMetadata.LaunchAccount DynamoDB round-trip, account-namespaced capacity store constructor argument"
provides:
  - "cmd.LaunchTarget struct + cmd.ResolveLaunchTarget(ctx, cfg, p, cliOverride, ssmStore) — the single link-resolution helper used by all three call sites"
  - "cmd.checkLaunchAccountEFSGuard — pure guard naming the enrollment flag on a shared-filesystem mismatch"
  - "Local create (runCreate) and remote create (runCreateRemote) both source network placement, region, and the compiler's launch-account locals from the resolved link via a single shared mapping function (applyLaunchAccountNetwork)"
  - "AZ-ranking / capacity gate reads the LINKED account's GPU quota and offerings via a second assumed-role AWS config (buildCapacityAWSConfig), fail-closed — never falls back to home credentials"
  - "Capacity store rows namespaced by the linked account id; home stays unnamespaced (bare key, unchanged)"
  - "cmd.hydrateLaunchAccountVPCID + cmd.resolveLaunchTargetVPCID — resolves the linked VPC id via DescribeSubnets (not carried anywhere in the link record), required for ec2spot to place the ENI in the correct VPC instead of self-provisioning a disconnected one"
  - "km create --launch-account (explicit empty forces home) and km capacity --launch-account (report on a linked account, header always names the account)"
  - "Sandbox DynamoDB row (both the local write and the remote 'starting' row) records LaunchAccount"
affects: [126-07, 126-08, 126-09, 126-10]

tech-stack:
  added: []
  patterns:
    - "Placement/capacity decision logic factored into small, named pure functions (applyLaunchAccountNetwork, resolveLaunchRegion, launchAccountCapacityNamespace, resolveLaunchNATServedAZs, buildCapacityAWSConfig, resolveLaunchTargetVPCID) so a large integration function (runCreate/runCreateRemote, ~3000 lines, blocked from direct testing by this package's AWS-fail-fast TestMain seam) stays table-testable — mirrors the file's own established checkPrivateSubnetGuard/resolveSandboxSubnets/natServedAZs convention"
    - "Structural source-text tests (grep-style, mirroring create_private_subnet_test.go) to pin call-site counts and ordering invariants that can't be exercised end-to-end (e.g. the fatal-return-before-any-client-construction fail-closed guarantee)"

key-files:
  created:
    - internal/app/cmd/launch_account_resolve.go
    - internal/app/cmd/launch_account_resolve_test.go
    - internal/app/cmd/create_launch_account_test.go
  modified:
    - internal/app/cmd/create.go
    - internal/app/cmd/capacity.go
    - internal/app/cmd/clone.go
    - internal/app/cmd/create_override_test.go
    - internal/app/cmd/create_wait_capacity_test.go

key-decisions:
  - "ResolveLaunchTarget's signature gained a 5th parameter (ssmStore SSMParamStore) beyond the plan's literal (ctx, cfg, p, cliOverride) — the literal 4-arg signature carries no AWS session and cannot actually read a parameter-store secret. Same class of deviation as 126-03's AssumeRoleConfig/NewAssumeRoleSTSClient seam."
  - "cliOverride is *string, not string — nil means 'flag not supplied, defer to the profile'; a non-nil empty string means 'explicitly forced home'. Cobra's Flags().Changed(\"launch-account\") distinguishes the two at the km create/km capacity call sites."
  - "The link-to-network mapping (subnets, AZs, VPCID, the three service.hcl locals) is one function, applyLaunchAccountNetwork, called from both runCreate and runCreateRemote — never two independently-maintained copies (this is exactly the drift the plan's own T-126-31 threat entry warns about)."
  - "Found and fixed during this plan (not specified by it): the launch_accounts link record carries subnet ids but no VPC id anywhere (not in config.LaunchAccountConfig, not in any gpu-launcher-account module output). Left as-is, a linked launch would have hit infra/modules/ec2spot/v1.3.0's create_vpc = vpc_id == \"\" branch, self-provisioned a brand-new disconnected VPC, and then tried to place the ENI into a subnet from the link's REAL (different) VPC — a hard AWS apply-time failure (\"subnet belongs to a different network\"), directly breaking must_haves.truths #1. Fixed by resolving the VPC id from the link's first subnet via one DescribeSubnets call against the assumed-role config (hydrateLaunchAccountVPCID), before any artifact is compiled."

requirements-completed: [REQ-126-LAUNCH, REQ-126-CAPACITY]

coverage:
  - id: D1
    description: "ResolveLaunchTarget resolves the effective link (profile-primary, CLI-override precedence, explicit-empty-override forces home), is dormant (zero config lookup, zero SSM read) when no link is named, and fails fast on every inconsistent link record"
    requirement: "REQ-126-LAUNCH"
    verification:
      - kind: unit
        ref: "internal/app/cmd/launch_account_resolve_test.go#TestResolveLaunchTarget_Dormant"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/launch_account_resolve_test.go#TestResolveLaunchTarget_CLIOverrideWins"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/launch_account_resolve_test.go#TestResolveLaunchTarget_CLIOverrideForcesHome"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/launch_account_resolve_test.go#TestResolveLaunchTarget_UnknownLink"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/launch_account_resolve_test.go#TestResolveLaunchTarget_PopulatesTarget"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/launch_account_resolve_test.go#TestResolveLaunchTarget_ParamStoreReadFailureIsFatal"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/launch_account_resolve_test.go#TestResolveLaunchTarget_SubnetAZLengthMismatch"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/launch_account_resolve_test.go#TestCheckLaunchAccountEFSGuard"
        status: pass
    human_judgment: false
  - id: D2
    description: "Both the local and remote create paths source subnets/AZs/region/VPC id from the resolved link (applyLaunchAccountNetwork + resolveLaunchRegion + hydrateLaunchAccountVPCID), and skip the home network-outputs/NAT-guard path entirely when a link is in effect"
    requirement: "REQ-126-LAUNCH"
    verification:
      - kind: unit
        ref: "internal/app/cmd/create_launch_account_test.go#TestApplyLaunchAccountNetwork_PopulatesFromTarget"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/create_launch_account_test.go#TestApplyLaunchAccountNetwork_DormantFieldsStayEmpty"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/create_launch_account_test.go#TestResolveLaunchRegion"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/create_launch_account_test.go#TestHydrateLaunchAccountVPCID_AssumeFailurePropagates"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/create_launch_account_test.go#TestCreateGo_BothNetworkResolutionSitesReferenceLaunchTarget"
        status: pass
      - kind: other
        ref: "km create --help lists --launch-account (verified against the built binary)"
        status: pass
    human_judgment: false
  - id: D3
    description: "The capacity/quota gate reads the linked account's headroom via a second assumed-role AWS config, and a failed assume-role aborts the create/report rather than silently checking home — the single most dangerous silent-wrong-answer in this phase (T-126-29)"
    requirement: "REQ-126-CAPACITY"
    verification:
      - kind: unit
        ref: "internal/app/cmd/create_launch_account_test.go#TestBuildCapacityAWSConfig_AssumeFailureIsFatalNoFallback"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/create_launch_account_test.go#TestBuildCapacityAWSConfig_HomeUnchanged"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/create_launch_account_test.go#TestCreateGo_AssumeFailureAbortsBeforeHomeFallback"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/create_launch_account_test.go#TestCapacityGo_AssumeFailureAbortsBeforeAnyClient"
        status: pass
      - kind: other
        ref: "km capacity --help lists --launch-account; report header names the account (verified against the built binary + TestLaunchAccountReportLabel)"
        status: pass
    human_judgment: false
  - id: D4
    description: "The capacity store namespaces its rows by the linked account id; home launches keep the pre-Phase-126 bare key; the DynamoDB client for capacity reads/writes always stays on the home config in both create paths and the capacity command"
    requirement: "REQ-126-CAPACITY"
    verification:
      - kind: unit
        ref: "internal/app/cmd/create_launch_account_test.go#TestLaunchAccountCapacityNamespace"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/create_launch_account_test.go#TestCreateGo_SingleNewDynamoCapacityStoreCallSite"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/create_launch_account_test.go#TestCapacityGo_SingleNewDynamoCapacityStoreCallSite"
        status: pass
    human_judgment: false
  - id: D5
    description: "The sandbox's DynamoDB row (both the local write and the remote 'starting' row) records which account holds the instance"
    requirement: "REQ-126-LAUNCH"
    verification:
      - kind: unit
        ref: "internal/app/cmd/create_launch_account_test.go#TestSandboxMetadata_LaunchAccountFieldWired"
        status: pass
    human_judgment: false
  - id: D6
    description: "The NAT-served-zone filter is always nil for a linked launch, regardless of the profile's privateSubnet bool or the home network's NAT gateways — a filter derived from a NAT-less network would drop every zone"
    requirement: "REQ-126-CAPACITY"
    verification:
      - kind: unit
        ref: "internal/app/cmd/create_launch_account_test.go#TestResolveLaunchNATServedAZs"
        status: pass
    human_judgment: false
  - id: D7
    description: "Live cross-account create/capacity/teardown against a real registered launch_accounts link — the launcher role's actual containment, the resolved VPC id, and the capacity gate all exercised against real AWS"
    human_judgment: true
    rationale: "No launch_accounts link exists yet in this environment (plans 04/05's enrollment module and km account add command are landed but have never been run against real AWS credentials in a second account) — plan 10's live UAT is the designated place for this. This plan's own scope is Go-side wiring, verified by unit tests and structural source checks against the AWS-fail-fast TestMain seam that blocks any live-credential test in this package."
    verification: []

duration: 24min
completed: 2026-08-22
status: complete
---

# Phase 126 Plan 06: Cross-account create-flow wiring — link resolution, network placement, capacity gate, VPC id fix Summary

**A profile naming a `launch_accounts` link now launches into the linked AWS account through one shared resolution helper used by local create, remote create, and `km capacity`: subnets/AZs/region/VPC id come from the link, the GPU quota gate reads the linked account's headroom via a second assumed-role AWS config that fails closed on any assume error, and the capacity store namespaces its rows by account — with a genuine apply-time VPC-mismatch bug found and fixed along the way.**

## Performance

- **Duration:** 24 min (first commit 20:22:10 → last commit 20:45:42, 2026-08-22)
- **Tasks:** 3 planned tasks + 1 Rule-1 bug fix, all committed
- **Files modified:** 8 (3 created, 5 modified)

## Accomplishments

- `cmd.ResolveLaunchTarget` — the single link-resolution helper (link lookup, external-id read with decryption, and five fail-fast consistency checks) used by `runCreate`, `runCreateRemote`, and `runCapacity`. Fully dormant (zero config lookup, zero SSM read) when no link is in effect.
- `cmd.checkLaunchAccountEFSGuard` — pure guard mirroring `checkPrivateSubnetGuard`'s shape, naming `km account add --provision-efs` when a linked profile requests the shared filesystem against a link with no EFS.
- `runCreate`'s network-resolution block and AZ-ranking block, and `runCreateRemote`'s (previously easy-to-miss) second network-resolution block, both source placement from the resolved link via one shared mapping function (`applyLaunchAccountNetwork`) rather than two independently-maintained copies.
- The GPU quota/offerings gate builds a second AWS config via `AssumeRoleConfig` (plan 03) when a link is in effect, and aborts the create/report on any assume failure — verified there is no code path that continues with the home config after that failure.
- The capacity store is namespaced by the linked account id (`launchAccountCapacityNamespace`); home stays unnamespaced. The DynamoDB client itself always stays on the home config in both create paths and `km capacity` — only the namespace argument changes.
- **Found and fixed a genuine bug**: the link record carries subnet ids but no VPC id anywhere in the schema or any enrollment-module output. Without resolving it, `infra/modules/ec2spot/v1.3.0` would have self-provisioned a brand-new, disconnected VPC (its `create_vpc = vpc_id == ""` branch) and then tried to place the sandbox's ENI into a subnet from the link's REAL, different VPC — a hard AWS apply-time failure. Fixed with `hydrateLaunchAccountVPCID`, a one-time `DescribeSubnets` call against the assumed-role config, called right after `ResolveLaunchTarget` in both create paths.
- `km create --launch-account` (explicit empty forces home) and `km capacity --launch-account` (report header always names the account: `"home"` or `"{link} ({accountID})"`, so a linked report can never be misread as a home one).
- The sandbox's DynamoDB row — both the local `WriteSandboxMetadataDynamo` call and the remote path's "starting" row — records `LaunchAccount`.

## Task Commits

Each task was committed atomically:

1. **Task 1: ResolveLaunchTarget — link lookup, external-id read, fail-fast guards** - `92a092c9` (test)
2. **Task 2: local create path — network from the link, second assumed-role capacity gate, namespaced store, sandbox row** - `2d1c6c62` (feat)
3. **Task 3: remote create path and `km capacity --launch-account`** - `27171d54` (feat)
4. **Fix: resolve the linked account's VPC id (Rule 1 — apply-time bug)** - `0316cfbe` (fix)

_No TDD RED/GREEN split — tests were written alongside each task's implementation and run together; every acceptance criterion (including the source-text structural checks) was verified before commit._

## Files Created/Modified

- `internal/app/cmd/launch_account_resolve.go` - `LaunchTarget` struct, `ResolveLaunchTarget`, `checkLaunchAccountEFSGuard`
- `internal/app/cmd/launch_account_resolve_test.go` - 9 unit tests for the resolver + guard
- `internal/app/cmd/create.go` - launch-target resolution wired into both `runCreate` and `runCreateRemote`; `applyLaunchAccountNetwork`, `resolveLaunchRegion`, `launchAccountCapacityNamespace`, `resolveLaunchNATServedAZs`, `buildCapacityAWSConfig`, `resolveLaunchTargetVPCID`, `hydrateLaunchAccountVPCID`; `--launch-account` flag; sandbox metadata `LaunchAccount` field on both create paths
- `internal/app/cmd/capacity.go` - `--launch-account` flag on `km capacity`, `buildCapacityAWSConfig`/`launchAccountCapacityNamespace` reuse, `launchAccountReportLabel`, report header now names the account
- `internal/app/cmd/clone.go` - both `runCreate`/`runCreateRemote` call sites updated for the new parameter (clone does not itself support `--launch-account`; passes `nil`)
- `internal/app/cmd/create_launch_account_test.go` - unit + structural tests for Task 2/3 + the VPC-id fix
- `internal/app/cmd/create_override_test.go` - updated the pre-existing literal `runCreateRemote` signature check for the new parameter
- `internal/app/cmd/create_wait_capacity_test.go` - updated the pre-existing literal `runCreate(...)` call-site source check for the new parameter

## Decisions Made

- `ResolveLaunchTarget` gained a 5th `ssmStore SSMParamStore` parameter beyond the plan's literal 4-arg signature — the literal signature has no AWS session and cannot actually perform the external-id read. Same class of deviation as 126-03's `AssumeRoleConfig`/`NewAssumeRoleSTSClient` seam, and documented for the same reason: testability without a live AWS session.
- `cliOverride` is `*string` rather than `string`, so `km create --launch-account=""` (explicit empty, forces home) is distinguishable from the flag being absent entirely (defer to the profile) — resolved via Cobra's `Flags().Changed("launch-account")` at both `km create` and (implicitly, since there is no profile to defer to) `km capacity`'s call sites.
- The link-to-network field mapping is exactly one function (`applyLaunchAccountNetwork`), shared by both create paths — this is precisely the drift the plan's own T-126-31 threat entry warns about (an unwired remote path silently launching into the home account with a link-shaped profile).
- Placement/capacity decision logic (region resolution, capacity-store namespace, NAT filter, the assumed-role config builder, the VPC-id resolver) is factored into small, named, pure/narrow-interface functions rather than left inline in the ~3000-line `runCreate`/`runCreateRemote` functions — this file's TestMain seam fails `LoadAWSConfig` fast for every test, so an integration-style test of the full create flow is not possible; the established convention in this file (`checkPrivateSubnetGuard`, `resolveSandboxSubnets`, `natServedAZs`) is to extract the decision logic into something table-testable, which this plan follows.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] The linked account's VPC id is not resolved anywhere, and ec2spot would self-provision a disconnected VPC**
- **Found during:** Post-Task-3 verification, while re-reading `infra/modules/ec2spot/v1.3.0/main.tf` to confirm the plan's must_haves.truths claim ("...the sandbox's subnets, security group and region come from the link record...") was actually achievable end-to-end, not just at the Go-struct level.
- **Issue:** `config.LaunchAccountConfig` (plan 01) and every `gpu-launcher-account` module output (plan 04) carry `subnet_ids`/`availability_zones`/`security_group_id` but **no VPC id**. `compiler.NetworkConfig.VPCID` was therefore left at its zero value for a linked launch. `infra/modules/ec2spot/v1.3.0/main.tf`'s `create_vpc = var.vpc_id == ""` branch self-provisions a brand-new VPC/subnet/IGW whenever `vpc_id` is empty — but `effective_subnets = length(var.sandbox_subnets) > 0 ? var.sandbox_subnets : aws_subnet.sandbox[*].id` (line 98) would still use the link's REAL subnet ids (since we do pass `sandbox_subnets`), which belong to a DIFFERENT vpc than the one the module just created its security group in (`aws_security_group.ec2spot`'s `vpc_id = local.effective_vpc_id`, line 203). This is a hard AWS API rejection at `terraform apply` ("subnet belongs to a different network"), not a silent misconfiguration — every linked GPU create would have failed at apply time.
- **Fix:** Added `LaunchTarget.VPCID`, resolved via one `DescribeSubnets` call against the link's first subnet (all of a link's subnets are provisioned together by one `km account add` run, so the first subnet's VPC is authoritative for all of them) using the launcher-role-assumed config — `resolveLaunchTargetVPCID` (pure logic behind a narrow `ec2SubnetDescriber` interface) + `hydrateLaunchAccountVPCID` (the wiring: builds the assumed config via `buildCapacityAWSConfig`, so it shares that function's fail-closed contract exactly — an assume failure aborts the create, VPCID is never set from a half-resolved lookup). Called once, right after `ResolveLaunchTarget`, in both `runCreate` and `runCreateRemote`. `applyLaunchAccountNetwork` now also sets `network.VPCID = target.VPCID`.
- **Files modified:** `internal/app/cmd/launch_account_resolve.go` (new `VPCID` field + doc comment), `internal/app/cmd/create.go` (new functions + two call sites), `internal/app/cmd/create_launch_account_test.go` (6 new tests)
- **Verification:** `TestResolveLaunchTargetVPCID_Success/_DescribeFailureIsFatal/_NoSubnetsIsFatal`, `TestHydrateLaunchAccountVPCID_HomeIsNoop/_AssumeFailurePropagates`, `TestApplyLaunchAccountNetwork_PopulatesFromTarget` (now asserts VPCID too) — all pass. `go build ./...` clean.
- **Committed in:** `0316cfbe` (fix)

**Residual, deliberately not fixed — SecurityGroupID is carried on `LaunchTarget` but not wired into `compiler.NetworkConfig`.** With the VPC-id fix above, this is now a soft gap rather than a functional break: `infra/modules/ec2spot/v1.3.0` **always** self-creates its own per-sandbox security group inside whatever `vpc_id` it receives (`aws_security_group.ec2spot`, unconditional — there is no "reuse an existing SG" input variable at all, for home launches either). So a linked launch will correctly create a fresh SG inside the link's real VPC (now that VPCID resolves correctly) — the link's pre-provisioned `SecurityGroupID` output from `gpu-launcher-account` simply goes unused, exactly as a home launch's shared VPC has no shared SG either. No apply-time failure results. Left unaddressed because doing anything with it (adding a "reuse SG" ec2spot module variable, or removing the enrollment module's SG provisioning) is a Terraform-module-level change outside this plan's declared `files_modified`, and no later plan (07–10) in this phase addresses it either.

---

**Total deviations:** 1 auto-fixed (Rule 1 — apply-time bug), 1 residual gap flagged for a future plan/operator awareness.
**Impact on plan:** The VPC-id fix was necessary for this plan's own must_haves.truths #1 to hold true end-to-end (not just at the Go-struct level) — without it, every linked GPU create would fail at `terraform apply`. No scope creep: the fix is contained to the same three files Task 2/3 already touched, uses the same assumed-role primitive (`buildCapacityAWSConfig`) those tasks introduced, and is fully covered by new unit tests. The residual SecurityGroupID gap is documented, not silently dropped, and does not block correctness.

## Issues Encountered

None beyond the deviation documented above.

## User Setup Required

None — no external service configuration required. This plan is pure Go/AWS-SDK code; it introduces no new Terraform resource, DynamoDB table, or IAM grant (all of those landed in plans 03/04). Live verification against a real registered link is plan 10's scope.

## Next Phase Readiness

- Plan 07 (`km account register`/`list`/`rm`) can now assume a fully-wired create path exists to actually launch into a registered link — nothing in Task 2/3's scope blocks it.
- Plan 08 (cross-account teardown) can rely on `SandboxMetadata.LaunchAccount` being reliably populated by both create paths (local write and remote "starting" row) — this was the exact key-link plan 08's own frontmatter calls out as load-bearing.
- Plan 09 (`km doctor` checks) can build its four checks on top of the same `ResolveLaunchTarget`/`buildCapacityAWSConfig` primitives without re-deriving the link-resolution or assumed-role logic.
- Plan 10's live UAT is the first point at which any of this plan's Go wiring will run against a real second AWS account and a real registered link — no synthetic/mocked substitute was available in this environment (no `launch_accounts:` entry configured, no cross-account credentials). The VPC-id fix found in this plan is exactly the class of bug live UAT exists to catch, and was caught here instead by tracing the actual Terraform module rather than stopping at the Go-struct level — flagging this so plan 10's live UAT gives real weight to the VPC/subnet/SG placement step specifically, not just the launcher-role IAM containment the plan's `must_haves.truths` already emphasizes.
- The residual SecurityGroupID-unused gap (see Deviations) is worth a one-line mention in plan 10's operator runbook so an operator doesn't go looking for their pre-provisioned SG being reused and wonder why it isn't.

---
*Phase: 126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin*
*Completed: 2026-08-22*

## Self-Check: PASSED

- `internal/app/cmd/launch_account_resolve.go` — FOUND
- `internal/app/cmd/launch_account_resolve_test.go` — FOUND
- `internal/app/cmd/create_launch_account_test.go` — FOUND
- This SUMMARY.md — FOUND
- Commit `92a092c9` (Task 1) — FOUND in `git log --oneline --all`
- Commit `2d1c6c62` (Task 2) — FOUND in `git log --oneline --all`
- Commit `27171d54` (Task 3) — FOUND in `git log --oneline --all`
- Commit `0316cfbe` (VPC-id fix) — FOUND in `git log --oneline --all`
