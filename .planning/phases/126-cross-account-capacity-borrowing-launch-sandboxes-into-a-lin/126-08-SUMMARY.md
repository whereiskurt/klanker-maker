---
phase: 126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin
plan: 08
subsystem: infra
tags: [go, terraform, iam, sts, teardown, cross-account, ttl-handler, lambda]

requires:
  - phase: 126-01
    provides: "launch_accounts config block + Config.GetLaunchAccount(s) getters, RuntimeSpec.LaunchAccount schema field"
  - phase: 126-02
    provides: "conditional cross-account provider override in the sandbox terragrunt template + service.hcl launch_account/launcher_role_arn/launcher_external_id locals"
  - phase: 126-03
    provides: "pkg/aws.AssumeRoleConfig fail-closed credential helper, SandboxMetadata.LaunchAccount DynamoDB round-trip"
  - phase: 126-06
    provides: "cmd.ResolveLaunchTarget — the single link-resolution helper, reused here unchanged"
provides:
  - "cmd.destroyLaunchContext / cmd.destroyDiscoveryOutcome / cmd.resolveDestroyRegionLabel / cmd.destroyStartMessage / cmd.synthesizeDestroyServiceHCL — the account-aware discovery-and-cold-clone chain for `km destroy`"
  - "ttl-handler renderDestroyMainTF (pure) + buildDestroyTerraformInputs (pure) + launchAccountLink + TTLHandler.LaunchAccounts + parseLaunchAccountsEnv — the link-aware provider-rendering chain for the expiry Lambda"
  - "ttl-handler env var KM_LAUNCH_ACCOUNTS and its Lambda-side JSON parser"
  - "infra/modules/ttl-handler/v1.0.0: launch_accounts_json variable + two count-gated aws_iam_role_policy resources (sts:AssumeRole, ssm:GetParameter+kms:Decrypt)"
  - "internal/app/cmd/init.go export of KM_LAUNCH_ACCOUNTS (JSON, gated on len(cfg.LaunchAccounts) > 0)"
affects: [126-09, 126-10]

tech-stack:
  added: []
  patterns:
    - "Sandbox-row-before-tag-scan discovery order: the DynamoDB row (which carries LaunchAccount) is read and consulted BEFORE any account-scoped AWS discovery call, because that call can only ever see the credential-holder's own account — the same ordering constraint now applies in three independent places in this codebase (destroy.go's cold-clone path, ttl-handler's terraformDestroy, and — from plan 06 — the create-time capacity gate)."
    - "Pure rendering/decision functions taking explicit inputs, no handler/AWS-client pointer, so behavior is testable without a Lambda runtime or a live AWS session — extends the create.go convention (applyLaunchAccountNetwork, buildCapacityAWSConfig, etc. from plan 06) to the teardown side (renderDestroyMainTF, buildDestroyTerraformInputs, destroyDiscoveryOutcome, synthesizeDestroyServiceHCL)."
    - "A single JSON config blob is the sole source of truth for both a Lambda's env-var parser and a Terraform module's derived IAM resource list (jsondecode(var.X) → [for k,v in ... : v.field]) — avoids a second comma-joined-list env var with no precedent in this codebase, and avoids splitting the launcher-ARN list from the record it belongs to."

key-files:
  created:
    - internal/app/cmd/destroy_launch_account.go
    - internal/app/cmd/destroy_launch_account_test.go
    - cmd/ttl-handler/main_launch_account_test.go
  modified:
    - internal/app/cmd/destroy.go
    - cmd/ttl-handler/main.go
    - infra/modules/ttl-handler/v1.0.0/main.tf
    - infra/modules/ttl-handler/v1.0.0/variables.tf
    - infra/live/use1/ttl-handler/terragrunt.hcl
    - internal/app/cmd/init.go

key-decisions:
  - "The launcher-role-ARN list the ttl-handler IAM policy needs is DERIVED in Terraform from the same launch_accounts_json blob (via jsondecode + a for-expression local) rather than threaded as a second, separately-exported list variable. The plan's action text says 'supplied as a list variable derived from the same configured links, defaulting to an empty list' — a Terraform local IS a list variable satisfying that literally, and this codebase has no existing precedent for a comma-joined-list-in-env-var pattern (every prior JSON-blob-in-Lambda-env case, KM_GITHUB_EVENTS/KM_GITHUB_PEER_BRIDGES/KM_SLACK_PEER_BRIDGES, is a single value). Single source of truth beats parity risk between two independently-exported values."
  - "The Lambda environment map entry (KM_LAUNCH_ACCOUNTS = var.launch_accounts_json) is NOT itself count-gated — it is always present with an empty-string default, matching the exact precedent set by KM_GITHUB_PEER_BRIDGES / KM_GITHUB_EVENTS in this same module family. Only the two new aws_iam_role_policy resources are gated (count = length(local.launch_account_launcher_role_arns) > 0 ? 1 : 0) — that count expression is the actual mechanism that keeps a dormant install's IAM surface byte-identical."
  - "config.LaunchAccountConfig (internal/app/config/config.go) was NOT touched — it has no `json` tags (json.Marshal would emit bare Go field names) but that file is outside this plan's files_modified. init.go instead defines a small inline payload type with the snake_case json tags the Lambda-side launchAccountLink struct expects, mirroring the config → wire-format translation githubEventsPayload already does for a different reason (envelope shape, not tag naming) — same technique, different root cause."
  - "A DynamoDB metadata-read failure in either path (destroy.go's earlyMeta, ttl-handler's terraformDestroy) falls through to the unmodified home-account path rather than failing the destroy — deliberately the SAME fail-open choice on both sides of the account boundary, so a transient metadata outage never blocks a home-account teardown. This differs from the unknown-link-name / external-id-read-failure cases, which are hard errors on both sides (T-126-42): 'we don't know if this sandbox is linked' defaults to the pre-Phase-126 safe behavior; 'we know it's linked but can't resolve the link' does not silently downgrade to a home-account destroy that would fail against the wrong account."
  - "No new test in cmd/ttl-handler/main_launch_account_test.go constructs a *TTLHandler or calls terraformDestroy/HandleTTLEvent, so the 'every new test must set the teardown-function seam' guard (which exists to stop a nil-TeardownFunc test from reaching live instance metadata via HandleTTLEvent) does not apply to any of them — documented explicitly in the test file's header comment. This mirrors the pre-existing suite's own convention: terraformDestroy has never been unit-tested directly anywhere in this codebase (it execs a bundled terraform binary against a /tmp scratch dir removed by defer on return), so all six of Task 2's behaviors are instead covered against the two pure functions extracted for exactly that purpose (renderDestroyMainTF, buildDestroyTerraformInputs)."

requirements-completed: [REQ-126-TEARDOWN]

coverage:
  - id: D1
    description: "km destroy discovers a linked-account sandbox from its DynamoDB row before the home-account tag scan runs, treats that scan's not-found as expected (not fatal) only for a linked sandbox, and preserves the exact pre-Phase-126 hard failure for a home-account not-found"
    requirement: "REQ-126-TEARDOWN"
    verification:
      - kind: unit
        ref: "internal/app/cmd/destroy_launch_account_test.go#TestDestroyDiscoveryOutcome_LinkedNotFoundIsNonFatal"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/destroy_launch_account_test.go#TestDestroyDiscoveryOutcome_HomeNotFoundStillFatal"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/destroy_launch_account_test.go#TestDestroyDiscoveryOutcome_OtherErrorStillFatalEvenWhenLinked"
        status: pass
      - kind: other
        ref: "grep confirms the sandbox-row read (destroy.go:212) precedes the FindSandboxByID tag-discovery call (destroy.go:259)"
        status: pass
    human_judgment: false
  - id: D2
    description: "the cold-clone synthesized service.hcl carries the three launch-account locals only for a linked sandbox, sources its region label from the link record, and is byte-identical to the pre-Phase-126 literal for a home sandbox"
    requirement: "REQ-126-TEARDOWN"
    verification:
      - kind: unit
        ref: "internal/app/cmd/destroy_launch_account_test.go#TestSynthesizeDestroyServiceHCL_HomeByteIdentical"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/destroy_launch_account_test.go#TestSynthesizeDestroyServiceHCL_LinkedCarriesLaunchAccountLocals"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/destroy_launch_account_test.go#TestResolveDestroyRegionLabel_LinkedUsesLinkRegion"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/destroy_launch_account_test.go#TestResolveDestroyRegionLabel_HomeFallsBackToUse1"
        status: pass
    human_judgment: false
  - id: D3
    description: "destroyLaunchContext resolves the sandbox row's launch account into a LaunchTarget (reusing ResolveLaunchTarget), is dormant (zero SSM read) for a nil/empty-launch-account row, and a metadata-read failure falls through to the unmodified home path rather than blocking the destroy"
    requirement: "REQ-126-TEARDOWN"
    verification:
      - kind: unit
        ref: "internal/app/cmd/destroy_launch_account_test.go#TestDestroyLaunchContext_NilMetaIsDormant"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/destroy_launch_account_test.go#TestDestroyLaunchContext_EmptyLaunchAccountIsDormant"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/destroy_launch_account_test.go#TestDestroyLaunchContext_LinkedResolvesTarget"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/destroy_launch_account_test.go#TestDestroyLaunchContext_UnknownLinkIsFatal"
        status: pass
    human_judgment: false
  - id: D4
    description: "the expiry Lambda's rendered main.tf carries a conditional assume_role provider block (launcher ARN + external id) and uses the link's region for both the provider and the state key's region label, for a linked sandbox — and is byte-identical to the pre-Phase-126 literal for a home sandbox"
    requirement: "REQ-126-TEARDOWN"
    verification:
      - kind: unit
        ref: "cmd/ttl-handler/main_launch_account_test.go#TestRenderDestroyMainTF_HomeByteIdentical"
        status: pass
      - kind: unit
        ref: "cmd/ttl-handler/main_launch_account_test.go#TestRenderDestroyMainTF_LinkedCarriesAssumeRoleBlock"
        status: pass
      - kind: unit
        ref: "cmd/ttl-handler/main_launch_account_test.go#TestRenderDestroyMainTF_HomeHasNoAssumeRole"
        status: pass
      - kind: unit
        ref: "cmd/ttl-handler/main_launch_account_test.go#TestBuildDestroyTerraformInputs_LinkedUsesLinkRegion"
        status: pass
      - kind: unit
        ref: "cmd/ttl-handler/main_launch_account_test.go#TestBuildDestroyTerraformInputs_HomeDormant"
        status: pass
    human_judgment: false
  - id: D5
    description: "an unknown link name or a failed external-id read aborts the ttl-handler teardown with a clear, logged error rather than rendering a home-account provider that would report success while the linked instance kept running"
    requirement: "REQ-126-TEARDOWN"
    verification:
      - kind: unit
        ref: "cmd/ttl-handler/main_launch_account_test.go#TestBuildDestroyTerraformInputs_UnknownLinkAborts"
        status: pass
      - kind: unit
        ref: "cmd/ttl-handler/main_launch_account_test.go#TestBuildDestroyTerraformInputs_ExternalIDReadFailureAborts"
        status: pass
      - kind: unit
        ref: "cmd/ttl-handler/main_launch_account_test.go#TestBuildDestroyTerraformInputs_NilLinksMapIsSafe"
        status: pass
    human_judgment: false
  - id: D6
    description: "the rendered module label equals the resource prefix rather than a hardcoded 'km' literal"
    requirement: "REQ-126-TEARDOWN"
    verification:
      - kind: unit
        ref: "cmd/ttl-handler/main_launch_account_test.go#TestRenderDestroyMainTF_ModuleLabelIsResourcePrefix"
        status: pass
    human_judgment: false
  - id: D7
    description: "the expiry Lambda's IAM role gains sts:AssumeRole (scoped to specific launcher ARNs, never a wildcard) and ssm:GetParameter+kms:Decrypt (scoped to the launch-accounts parameter path prefix), both emitted only when at least one link is configured; terraform validate and fmt both pass"
    requirement: "REQ-126-TEARDOWN"
    verification:
      - kind: other
        ref: "terraform validate (exit 0) + terraform fmt -check -recursive (exit 0) in infra/modules/ttl-handler/v1.0.0"
        status: pass
      - kind: other
        ref: "grep confirms the gating expression: count = length(local.launch_account_launcher_role_arns) > 0 ? 1 : 0 on both new aws_iam_role_policy resources"
        status: pass
    human_judgment: true
    rationale: "No launch_accounts link exists in this environment (same gap 126-06/126-07 flagged) — the actual cross-account AssumeRole call, SSM read, and terraform apply of these new IAM statements against a real linked account are unverified here. Plan 10's live UAT is the designated place; this plan's own scope is Go/Terraform wiring, verified by unit tests, terraform validate/fmt, and a full `make test` pass."

metrics:
  duration: "~55min"
  completed: 2026-08-22
status: complete
---

# Phase 126 Plan 08: Cross-account teardown — km destroy, the expiry Lambda, and the IAM to let it assume the launcher Summary

**Both teardown paths — the operator's `km destroy` (including its cold-clone fallback for a fresh-clone destroy) and the unattended expiry Lambda — are now account-aware: they consult the sandbox's DynamoDB row before any account-scoped discovery call, render a conditional cross-account `assume_role` provider block only for a linked sandbox, and a home-account sandbox takes the byte-identical Phase 125 path on both sides, asserted directly against embedded pre-change literals.**

## Performance

- **Duration:** ~55 min
- **Tasks:** 3 completed
- **Files modified:** 6 modified, 3 created

## Accomplishments

- **`km destroy`'s cold-clone gap closed (Pattern 5's full three-step fix chain, not just the literal C3 fix).** The early sandbox-row read in `runDestroy` is hoisted out of its block scope; `destroyLaunchContext` resolves the row's launch account (reusing `ResolveLaunchTarget` from plan 06, the same shape `km capacity --launch-account` already uses) BEFORE the home-account `FindSandboxByID` tag scan runs. `destroyDiscoveryOutcome` treats that scan's not-found as expected — not fatal — exactly when a launch account is in effect (the home-account tagging API can never see a resource in a different account), while preserving the original hard failure and error message for a plain sandbox. `resolveDestroyRegionLabel` sources the cold-clone directory's region from the link record instead of the (necessarily empty) tag inspection, and `synthesizeDestroyServiceHCL` extends the minimal `service.hcl` with the three launch-account locals (`launch_account`, `launcher_role_arn`, `launcher_external_id`) only when a link is in effect — byte-identical to the pre-change literal otherwise (asserted directly, not structurally).
- **The expiry Lambda renders a link-aware provider using the link's own region, not its own instance-wide fields.** `terraformDestroy` now reads the sandbox's DynamoDB row for its `launch_account` (the handler's `Region`/`RegionLabel`/`StatePrefix` fields are cold-start env vars, shared across every event the Lambda ever processes — they cannot carry a per-sandbox linked region). The rendering itself was extracted into a pure function, `renderDestroyMainTF(destroyTerraformInputs) string` — no handler pointer, no AWS call — fed by `buildDestroyTerraformInputs`, which resolves region/region-label/state-key/assume-role inputs from the sandbox's launch account name, the parsed `KM_LAUNCH_ACCOUNTS` link map, and an injected external-id reader function. An unknown link or a failed external-id read is a hard, logged error — never a silent fall-through to a home-account provider that would report success while the linked instance kept billing (T-126-42).
- **Second, independent fix landed in the same pass**: the module label baked into the Lambda's rendered `main.tf` (`km_label = "km"`) is now `resourcePrefix()` instead of a hardcoded literal — the prefix bug the design spec flagged as newly reachable on the cross-account path, a no-op on any default-prefix install.
- **Terraform grants added, narrowly and dormant by default.** `infra/modules/ttl-handler/v1.0.0` gains one additive JSON-string variable (`launch_accounts_json`, edited in place, no version bump — the lambda-github-bridge precedent) surfaced as `KM_LAUNCH_ACCOUNTS`, plus two `count`-gated `aws_iam_role_policy` resources: `sts:AssumeRole` scoped to the specific launcher role ARNs (derived from `launch_accounts_json` via a Terraform `jsondecode` local — no second list variable), and `ssm:GetParameter` + `kms:Decrypt` scoped to the `/{prefix}/launch-accounts/*` parameter path (the KMS grant mirrors `ec2spot`'s existing `ec2spot_github_token` `kms:ViaService` pattern, since these SecureStrings use the default `alias/aws/ssm` key). Confirmed first: the role held NO pre-existing SSM `GetParameter`/`kms:Decrypt` grant to extend — this is a genuinely new statement, not a redundant one. `internal/app/cmd/init.go` exports `KM_LAUNCH_ACCOUNTS` as JSON only when at least one link is configured, mirroring the `KM_GITHUB_EVENTS` export block including its env-wins drift WARN.

## Task Commits

Each task was committed atomically:

1. **Task 1: km destroy — read the sandbox row before the account-scoped tag scan, carry the launch account into the synthesized configuration** - `86b49c42` (feat)
2. **Task 2: expiry Lambda — link-aware provider, link region, prefix-correct module label** - `99b0d811` (feat)
3. **Task 3: Terraform and init wiring so the Lambda can actually assume the launcher** - `c3d2711b` (feat)

_No TDD RED/GREEN split — tests were written alongside each task's implementation and run together; every acceptance criterion (byte-identity assertions, dormancy/gating checks, the sandbox-row-before-tag-scan line-number check) was verified before commit._

## Files Created/Modified

- `internal/app/cmd/destroy.go` - hoisted `earlyMeta`; new launch-context resolution + account-aware discovery/region/synthesis wiring in `runDestroy`
- `internal/app/cmd/destroy_launch_account.go` - `destroyLaunchContext`, `destroyDiscoveryOutcome`, `destroyStartMessage`, `resolveDestroyRegionLabel`, `synthesizeDestroyServiceHCL` (new file — pure helpers factored out of `destroy.go` for direct unit testing)
- `internal/app/cmd/destroy_launch_account_test.go` - 16 unit tests covering all 6 of Task 1's behaviors
- `cmd/ttl-handler/main.go` - `launchAccountLink`, `destroyTerraformInputs`, `renderDestroyMainTF`, `ttlExternalIDReader`, `buildDestroyTerraformInputs`, `readSSMParameter`, `parseLaunchAccountsEnv`; `TTLHandler.LaunchAccounts` field; `terraformDestroy` rewired to read the sandbox row and call the new pure functions; cold-start parsing wired into `main()`
- `cmd/ttl-handler/main_launch_account_test.go` - 15 unit tests covering all 6 of Task 2's behaviors, against the pure functions directly
- `infra/modules/ttl-handler/v1.0.0/main.tf` - `data "aws_region" "current"`, `locals.launch_accounts`/`launch_account_launcher_role_arns`, two new `aws_iam_role_policy` resources, `KM_LAUNCH_ACCOUNTS` environment entry
- `infra/modules/ttl-handler/v1.0.0/variables.tf` - `launch_accounts_json` variable
- `infra/live/use1/ttl-handler/terragrunt.hcl` - `launch_accounts_json = get_env("KM_LAUNCH_ACCOUNTS", "")`
- `internal/app/cmd/init.go` - `KM_LAUNCH_ACCOUNTS` JSON export block, gated on `len(cfg.LaunchAccounts) > 0`, mirroring the `KM_GITHUB_EVENTS` block

## Decisions Made

See `key-decisions` in the frontmatter for the full reasoning on: deriving the launcher-ARN list from `launch_accounts_json` in Terraform rather than a second list variable; the env-var-always-present-but-gated-IAM-statements split; why `config.LaunchAccountConfig` itself was left untouched; the deliberate fail-open-on-metadata-read-failure vs fail-closed-on-unknown-link-or-failed-secret-read asymmetry; and why no new ttl-handler test sets the `TeardownFunc` seam (none of them reach `HandleTTLEvent`/`terraformDestroy`).

## Deviations from Plan

### Auto-fixed / clarified (not bugs — scope interpretation, documented per Rule 2/organizational judgment)

**1. New file `internal/app/cmd/destroy_launch_account.go`, not listed in the plan's `files_modified`.** The plan's Task 1 `<files>` lists only `destroy.go` and `destroy_launch_account_test.go`. The five new pure helper functions (`destroyLaunchContext`, `destroyDiscoveryOutcome`, `destroyStartMessage`, `resolveDestroyRegionLabel`, `synthesizeDestroyServiceHCL`) were placed in a new file rather than inlined into the already-1069-line `destroy.go`, matching the pattern plan 06 used for `launch_account_resolve.go` (a dedicated file for a dedicated concern, not a monolithic edit). No behavior change; purely an organizational choice made to keep `destroy.go`'s diff reviewable.

**2. `resolveDestroyTerraformInputs`'s launcher-ARN list is a Terraform `local`, not a second exported Terraform `variable`.** The plan's Task 3 action text says the ARN list should be "supplied as a list variable derived from the same configured links, defaulting to an empty list." A Terraform `local` computed via `jsondecode(var.launch_accounts_json)` IS a list variable in that sense (derived from the same links, defaults to `[]`) — it was not threaded as a second root-level `variable` block with its own env-var export, because (a) the plan's own "Artifacts this phase produces" section lists exactly one new Terraform variable (`launch_accounts_json`), and (b) this codebase has no existing precedent for a comma-joined-list-in-Lambda-env-var pattern to follow, while `jsondecode` of the same JSON blob the Lambda already parses is both simpler and eliminates a parity-drift risk between two independently-exported values. Documented in `key-decisions`.

**3. No new ttl-handler test sets the `TeardownFunc` seam.** Task 2's acceptance criteria calls for grepping the new test file to confirm every new test sets it. None of the 15 new tests construct a `*TTLHandler` or invoke `HandleTTLEvent`/`terraformDestroy` — they exercise `renderDestroyMainTF`/`buildDestroyTerraformInputs`/`parseLaunchAccountsEnv` directly, per the plan's own instruction to "extract the configuration rendering into a small pure function... so all six behaviors can be tested without a Lambda runtime." Since no test takes the path the `TeardownFunc` seam guards against, the guard is vacuously satisfied — documented explicitly in the new test file's header comment rather than silently omitted. This also matches the pre-existing suite's own convention: `terraformDestroy` has never been called directly by any test in this codebase (it execs a bundled terraform binary against a `/tmp` scratch directory removed by `defer` on return, making direct unit testing impractical regardless of this phase).

**Total deviations:** 0 Rule-1/2/3 bug fixes. 3 organizational/scope-interpretation notes, all reasoned from either the plan's own artifact list, this codebase's established precedents, or the pre-existing test suite's own conventions — none change observable behavior for a home-account sandbox.

## Issues Encountered

None. The AWS-fail-fast `TestMain` seam in `internal/app/cmd` (documented in the team-lead's `known_preexisting_failures`) produced the same 5 expected failures (`TestBootstrapSCPApplyPath`, `TestBootstrapSCPSkipped_OrganizationBlank`, `TestClusterAdd`, `TestClusterRm`, `TestClusterAddPersistFailure`) both before and after this plan's changes — confirmed via `go test ./internal/app/cmd/... -timeout 600s -count=1` producing an identical failure set, and `go test ./internal/app/cmd/... -run 'Destroy' -timeout 600s -count=1` (which excludes them entirely) exiting 0.

## User Setup Required

None for this plan's own scope — pure Go + Terraform-module code, no AWS calls made during development or testing. **Deploy surface for when this phase ships:** `make build-lambdas` (rebuilds the ttl-handler Lambda zip with the new terraform-destroy rendering logic) followed by `km init --dry-run=false` (the new `launch_accounts_json` variable is an env-block change, and the two new `aws_iam_role_policy` resources are an IAM change — both require a full terragrunt apply; `--sidecars` updates neither). No sandbox recreate is needed — this plan only touches the teardown path, not sandbox creation.

## Next Phase Readiness

- Plan 09 (`km doctor` checks) can build cross-account-teardown-readiness checks against the same `ResolveLaunchTarget`/link-lookup primitives this plan reused, and can check for the presence of the two new IAM policy resources via the module's `count` gate.
- Plan 10's live UAT is the first point at which any of this plan's teardown wiring runs against a real linked account and a real registered link — no synthetic/mocked substitute was available in this environment (same gap 126-06/126-07/126-08 all flag: no `launch_accounts:` link exists in this environment). Specifically worth exercising live: (a) `km destroy` on a linked sandbox from a fresh clone (the cold-clone path this plan's Task 1 targets), (b) an expired-TTL teardown of a linked sandbox via the real EventBridge schedule → ttl-handler path, and (c) confirming the `kms:ViaService` KMS grant actually authorizes decrypt against the real `alias/aws/ssm` key in the target account (this pattern is copied from `ec2spot_github_token`'s existing, presumably-proven precedent, but has not been proven for THIS specific external-id parameter).
- The `must_haves.truths` and `key_links` from this plan's own frontmatter are all satisfied at the Go/Terraform-wiring level: the `launch_account` DynamoDB attribute is now consulted by both teardown paths before any account-scoped call; `km-config.yaml launch_accounts` → `KM_LAUNCH_ACCOUNTS` → the Lambda's launcher-ARN/region lookup chain is complete end-to-end in code.

---
*Phase: 126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin*
*Completed: 2026-08-22*

## Self-Check: PASSED

- `internal/app/cmd/destroy_launch_account.go` — FOUND
- `internal/app/cmd/destroy_launch_account_test.go` — FOUND
- `cmd/ttl-handler/main_launch_account_test.go` — FOUND
- This SUMMARY.md — FOUND
- Commit `86b49c42` (Task 1) — FOUND in `git log --oneline --all`
- Commit `99b0d811` (Task 2) — FOUND in `git log --oneline --all`
- Commit `c3d2711b` (Task 3) — FOUND in `git log --oneline --all`
