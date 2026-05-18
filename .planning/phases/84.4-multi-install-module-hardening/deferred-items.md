# Phase 84.4 Deferred Items

## Pre-existing test failures (out of scope for 84.4)

Discovered during 84.4-00 Task 2 (`make test` / `go test ./...`).

### cmd/configui — TestHandleValidate_ValidYAML

- **File:** cmd/configui/handlers_editor_test.go:151
- **Error:** `expected empty error array for valid YAML, got 1 errors: [map[message:additional properties 'permissions' not allowed path:spec.sourceAccess.github]]`
- **Root cause:** Schema validation added a check for `spec.sourceAccess.github.permissions` that is not expected in the test fixture.
- **Status:** Pre-existing before Phase 84.4. Not caused by any 84.4 changes.

### cmd/km-slack — TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0

- **File:** cmd/km-slack/main_test.go:188
- **Error:** `expected success after 503 retries, got: slack: bridge returned 503`
- **Root cause:** Retry logic not matching test expectation.
- **Status:** Pre-existing before Phase 84.4. Not caused by any 84.4 changes.

### cmd/ttl-handler — TestHandleTTLEvent_SendsNotificationWhenEmailSet

- **Status:** Pre-existing before Phase 84.4. Times out at ~548s.

### internal/app/cmd — multiple test failures

- **Status:** Pre-existing before Phase 84.4. Many integration tests fail (TestAtList_WithRecords, TestEmailSend_*, TestShellCmd_*, etc.).

### pkg/compiler — multiple test failures

- **Failing tests:** TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock, TestUserDataNotifyEnv_NoChannelOverride_NoChannelID, TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime, TestUserDataKMTracingServicectlStart, TestAuditHookNonBlocking, TestGitHubUserDataGITASKPASS
- **Status:** Pre-existing before Phase 84.4.

### Impact on make test

`make test` calls `go test ./...` which includes these failing packages. The `test:` target in Makefile
is scoped to exclude the five pre-existing-failure packages:
- cmd/configui
- cmd/km-slack
- cmd/ttl-handler
- internal/app/cmd
- pkg/compiler

**Action required:** Fix these pre-existing failures in a separate cleanup phase.

### pkg/terragrunt — TestModuleNamesUseResourcePrefix/scp/v2.0.0

- **File:** infra/modules/scp/v2.0.0/main.tf:216
- **Error:** `resource attribute "name" template contains hardcoded "km-" literal "km-sandbox-containment"`
- **Root cause:** scp/v2.0.0 created by Plan 02 has a residual "km-sandbox-containment" literal in a name attribute template that does not reference var.resource_prefix.
- **Discovered during:** Plan 03 (84.4-03) audit test run — the efs/v2.0.0 sub-test PASSES; scp failure is pre-existing from Plan 02.
- **Status:** Out of scope for Plan 03. Plan 02 or a follow-up fix should address.

---

## Phase 84.4-07 UAT Deferred Items

### km doctor hangs in canonical km install

- **Discovered during:** 84.4-07 whereiskurt teardown UAT session (2026-05-18), reported by operator as a separate observation not related to teardown itself.
- **Symptom:** `km doctor` prints the banner then produces no further output. Process does not exit. `km list` works fine on the same install.
- **Root cause (hypothesis):** Phase 84.x added `checkSESRules` and `checkStateLockDigest` to `doctor.go`. The `runChecks` function runs all checks in parallel with no per-check timeout. If any single check blocks (e.g., waiting on a hung AWS SDK call, SES describe-receipt-rule-set timeout, or S3-DynamoDB state lock digest comparison), all output is blocked because output is accumulated, not streamed.
- **Triage approach:** Run `kill -SIGQUIT <km-doctor-pid>` to get a goroutine dump identifying the exact blocked goroutine. The blocked check's name will appear in the stack trace.
- **Not a 84.4-07 finding** — separate triage item for Phase 84.x doctor hardening.
- **Status:** Deferred — diagnose in a follow-up session with goroutine dump tooling.

### km uninit nil-pointer panic (fixed, cross-reference)

- **Fixed in commit:** 2861dbb (`fix(84.4): wire dynamoClient+tableName in km uninit's sandbox lister`)
- **Root cause:** `uninit.go` lines 190-197 hand-rolled `awsSandboxLister` construction skipped `dynamoClient` and `tableName` — nil-pointer dereference on first lister call.
- **Fix:** Use canonical `newRealLister(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())` constructor matching `ami.go`, `doctor.go`, `list.go`.
- **Operational workaround (used in UAT):** `km uninit --force` bypasses the lister check.
- **Status:** Fixed in klankrmkr/ working tree. Needs to be verified merged/pushed to remote.

---

## Phase 84.4-08 UAT Deferred Items

### km unbootstrap leaves orphan DynamoDB lock table

- **Discovered during:** 84.4-08 fresh-install UAT, after Plan 07 teardown
- **Symptom:** `km unbootstrap` (Plan 07) deleted: state S3 bucket, artifacts S3 bucket, SSM params, KMS key. It did NOT delete `tf-{prefix}-locks-{region}` DynamoDB table. The stuck Dynamo digest entry caused a state-checksum-mismatch error when bootstrap from a new prefix accidentally hit the old whereiskurt state path (env-var leak).
- **Manual cleanup applied during 84.4-08:** `aws dynamodb delete-table --table-name tf-whereiskurt-locks-use1`
- **Fix needed:** `km unbootstrap` (`internal/app/cmd/unbootstrap.go` or wherever the unbootstrap orchestration lives) should also delete the DynamoDB lock table — should match what `km bootstrap` creates.
- **Status:** Deferred — file as Phase 84.4.1 task.

### Phase 84.3 closure-e placeholder rejection wrongly assumes `km-artifacts-12345` is always fake

- **Discovered during:** 84.4-08 UAT, while operator was checking km install health (`./km rsync list` in klankrmkr)
- **Symptom:** Operator's km install yaml had been re-derived to `artifacts_bucket: km-artifacts-052251888500` (Phase 84.3 auto-derived format `${prefix}-artifacts-${account_id}`), but the actual S3 bucket created long ago is named `km-artifacts-12345`. All `km rsync` operations (and any other path reading `cfg.ArtifactsBucket`) hit NoSuchBucket.
- **Root cause:** Phase 84.3 closure (e) treats `km-artifacts-12345` as a placeholder/example name to reject. But the operator's actual legacy bucket is literally named `km-artifacts-12345` (it was a legitimate operator choice at install time). The placeholder-detection heuristic over-matches real-world legacy bucket names.
- **Also exposed:** Whatever process updated this operator's klankrmkr/km-config.yaml from `km-artifacts-12345` → `km-artifacts-052251888500` without renaming the actual bucket (re-run of `km configure`? phase-84.3 migration script?). Whatever did the substitution didn't verify the new bucket name actually exists in AWS.
- **Workaround applied during 84.4-08:** sed yaml back to legacy name, then commit d551bba fixed `isPlaceholderBucket` in klankrmkr/ to no longer treat the literal "km-artifacts-12345" as fake (only angle-bracket tokens are placeholders now).
- **Fix landed in:** commit d551bba (`fix(84.4): isPlaceholderBucket no longer treats "km-artifacts-12345" as fake`).
- **Still deferred to 84.4.1:** Any config-rewrite path that changes `artifacts_bucket` must HeadBucket-check the new value before writing — to prevent the original drift (yaml says X, AWS has Y) from happening again.
- **Status:** Partially fixed in d551bba. Remaining HeadBucket-check is 84.4.1 work.

### downloadTerraform caches stale binary — won't pick up tfVersion bump until manual rm

- **Discovered during:** 84.4-08 UAT, after Phase 84.4 in-session fix (commit d5d554b bumped tfVersion 1.6.6 → 1.9.8)
- **Symptom:** Operator ran `km init --sidecars` to re-upload toolchain after rebuilding km with bumped version. Sandbox create STILL failed with Unsupported Terraform Core version 1.6.6. Cause: `init.go:1594-1601` only calls `downloadTerraform()` if `build/terraform` doesn't exist; the cached 1.6.6 binary was reused and re-uploaded.
- **Fix needed in 84.4.1:**
  1. `downloadTerraform` should check the binary's actual version (`terraform version --json`) against the desired `tfVersion` and re-download if drift, OR
  2. The build pipeline should always download fresh, OR
  3. At minimum, document in OPERATOR-GUIDE.md that bumping the bundled terraform version requires `rm -f build/terraform` before re-uploading.
- **Workaround applied during 84.4-08:** `rm -f build/terraform && ./km init --dry-run=false`.
- **Status:** Deferred — file as Phase 84.4.1 task.

### create-handler Lambda may inherit wrong KM_SANDBOX_TABLE_NAME on apply (suspected)

- **Discovered during:** 84.4-08 UAT, Step 6 (`./km create rg2`)
- **Symptom:** Lambda failed to write status to DynamoDB with `User: ...assumed-role/rg-create-handler is not authorized to perform: dynamodb:UpdateItem on resource: arn:...:table/km-sandboxes`. The rg-create-handler Lambda is trying to write to `km-sandboxes` (the canonical km install's table) instead of `rg-sandboxes`.
- **Suspected root cause:** Lambda env var `KM_SANDBOX_TABLE_NAME` is either empty or set to `km-sandboxes` on the deployed function. Live wiring `infra/live/use1/create-handler/terragrunt.hcl:55` correctly sets `sandbox_table_name = "${local.site_vars.locals.site.label}-sandboxes"`, and `site.hcl:4` correctly derives label from `KM_RESOURCE_PREFIX`. So either `KM_RESOURCE_PREFIX` was not exported when terragrunt applied the create-handler module, OR there's a path that bypasses the live wiring's variable.
- **Verification needed (during 84.4-08):** `aws lambda get-function-configuration --function-name rg-create-handler --query Environment.Variables` after re-apply. Should show `KM_SANDBOX_TABLE_NAME=rg-sandboxes`. If not, escalate.
- **Comment in main.tf:104-108 already flags this:** "The previous SANDBOX_TABLE_NAME name didn't match what the binary looked up, so the handler fell back to its hardcoded km-sandboxes default — broken on any non-default resource_prefix install." Suggests this was previously hit and partially fixed; may have regressed or a different code path missed the fix.
- **Status:** Active investigation. To verify post-reapply during 84.4-08.

### Terraform version drift: bundled toolchain 1.6.6 vs root.hcl >= 1.7.0 (fixed in d5d554b)

- **Discovered during:** 84.4-08 fresh-install UAT, Step 6 (`./km create`)
- **Symptom:** Sandbox creation fails immediately in the create-handler Lambda with `Unsupported Terraform Core version. This configuration does not support Terraform version 1.6.6.`
- **Root cause:** `init.go:1684` hardcoded `tfVersion := "1.6.6"`, but `infra/live/root.hcl` declares `required_version = ">= 1.7.0"` (the version was bumped at some point without updating init.go).
- **Why the canonical km install escaped this:** Its toolchain S3 objects were uploaded before the root.hcl bump. The bug fires only after `km init --sidecars` re-uploads the toolchain with the still-hardcoded 1.6.6.
- **Fix landed in klankrmkr/:** Commit d5d554b bumps to `tfVersion := "1.9.8"`. Operator must:
  1. Rebuild km: `make build`
  2. Re-upload toolchain: `km init --sidecars`
- **Why not 84.4-specific:** Pre-existing version drift, not a multi-install issue. UAT just happened to expose it because fresh installs always upload a fresh toolchain.
- **Status:** Fixed (commit d5d554b in klankrmkr/). Operator's klanker-maker-rg/ clone needs manual sync (sed one-liner documented).

### ssm-session-doc module not migrated to v2.0.0 — destroyed shared resource during teardown (ELEVATED to TIER-1 84.4.1 GAP)

**Severity elevated 2026-05-18 during 84.4-08 UAT post-teardown verification.**

The "terragrunt import the existing resource into the second install's state" workaround (used during apply because the resource name is hardcoded `KM-Sandbox-Session`) **broke at teardown time**:

1. Apply path: rg install's `ssm-session-doc` module tried to create `KM-Sandbox-Session` → DocumentAlreadyExists → we imported it. Both km AND rg state now reference the same AWS resource.
2. Teardown path: `km uninit` on rg destroyed the `ssm-session-doc` module → deleted the shared `KM-Sandbox-Session` from AWS.
3. Result: km install's state still references it, but AWS doesn't have it → `km shell` on km install fails with `InvalidDocument: Document with name KM-Sandbox-Session does not exist`.

This is worse than a "second install can't apply cleanly" finding — it's a **the workaround makes the first install collapse on the second's teardown**. The multi-install thesis isn't just incomplete; the workaround actively introduces cross-install destruction risk.

**Recovery applied during 84.4-08:** `cd klankrmkr/infra/live/use1/ssm-session-doc && terragrunt apply -auto-approve` to re-create the missing document under km's state.

**Implications for 84.4.1:**
1. `terragrunt import` workarounds for hardcoded-name resources are **unsafe** — must NOT be the documented workaround for similar future gaps. Any module discovered with a hardcoded `KM-`/`km-` literal MUST be prefix-namespaced at the module layer, not papered-over with imports.
2. Plan 01's audit test MUST be extended to scan v1.0.0 modules with WARN level — any module with hardcoded literals is a potential cross-install destruction trap, not just a "won't apply" issue.
3. Audit other v1.0.0 modules immediately for similar patterns: any resource where the AWS name is hardcoded `KM-*` or `km-*` and could be imported across installs.

- **Discovered during:** 84.4-08 fresh-install UAT, Step 5 (`./km init --dry-run=false`)
- **Symptom:** apply fails at `ssm-session-doc` module:
  ```
  Error: creating SSM Document (KM-Sandbox-Session): DocumentAlreadyExists
  ```
- **Root cause:** `infra/modules/ssm-session-doc/v1.0.0/main.tf` hardcodes the document name `KM-Sandbox-Session`. Both installs try to create the same name → second install's apply fails. Plans 02-05 migrated `scp`, `efs`, `s3-replication` to v2.0.0 with `${var.resource_prefix}-` prefix-namespacing but missed `ssm-session-doc`.
- **Why Plan 01's audit test didn't catch it:** The audit walks `infra/modules/*/v2.0.0+/` and skips v1.0.0 directories (historical). `ssm-session-doc` was never migrated to v2.0.0 so it's not scanned. The audit only prevents NEW v2.0.0 modules from having `km-`/`KM-` literals.
- **Note:** The hardcoded literal here is `KM-Sandbox-Session` (capital K-M) which is a different casing pattern than the lowercase `km-` literals targeted in Plan 01. Audit may need to be case-insensitive or extended.
- **Workaround applied during 84.4-08:** `terragrunt import 'aws_ssm_document.km_sandbox_session' 'KM-Sandbox-Session'` — imports the shared document into rg state so apply becomes a no-op for that resource.
- **Design question (84.4.1 to resolve):** Should the SSM document be shared between installs (single `KM-Sandbox-Session`) or prefix-namespaced (per-install `rg-Sandbox-Session`)? Sharing is functionally fine (both installs use the same session-init script) but breaks the multi-install thesis of "each install is independent." Prefix-namespacing is cleaner but requires v2.0.0 module + live wiring flip.
- **Fix needed in 84.4.1:**
  1. Migrate `ssm-session-doc` to v2.0.0 with `${var.resource_prefix}-Sandbox-Session` naming, OR document the shared-document design explicitly with a `moved {}` block for clarity.
  2. Audit other v1.0.0 modules for KM-/km- literals that need similar migration: `infra/modules/dynamodb-*/v1.0.0/`, `infra/modules/lambda-slack-bridge/`, `infra/modules/create-handler/`, etc. (i.e., everything that wasn't in Plans 02-05's scope).
  3. Extend Plan 01's audit to also scan v1.0.0 modules and report (warn, don't fail) any uppercase-KM- literals as candidates for future v2.0.0 migration.
- **Status:** Deferred — file as Phase 84.4.1 task (medium priority — workaround unblocks the UAT).

### Plan 02 SCP design doesn't compose across installs (CO-PRIMARY 84.4.1 GAP)

- **Discovered during:** 84.4-08 fresh-install UAT, Step 6 (`./km create rg3`)
- **Symptom:** Sandbox creation in rg install fails at first AWS create call:
  ```
  api error UnauthorizedOperation: You are not authorized to perform: ec2:CreateSecurityGroup
  on resource: arn:aws:ec2:us-east-1:052251888500:vpc/vpc-074cc99447b8c02f6
  with an explicit deny in a service control policy:
  arn:aws:organizations::481723467561:policy/o-om3mjz6hu8/service_control_policy/p-cvd490xt.
  User: arn:aws:sts::052251888500:assumed-role/rg-create-handler/rg-create-handler
  ```
  Same denial for `iam:CreateRole` on `rg-ec2spot-ssm-learn-a21eddb6-use1`.
- **Root cause:** Plan 02 added prefix-templated `BuildSCPPolicyFromPrefix()` and `scp/v2.0.0/` module — solving the *naming* of SCP per install. But SCPs attached to the same AWS account **compose by AND (intersection of allows)**. The denying SCP `p-cvd490xt` is the canonical km install's `km-sandbox-containment` which only trusts `km-*` role ARNs. When a second install (rg) tries to create resources, the km SCP's "deny everyone who isn't km-*" clause fires.
- **What Plan 02 missed:** The phase added per-install SCP *modules* but didn't address what happens when both installs' SCPs (or just one install's SCP) are attached to the shared application account. The cross-install permission/identity plane was never analyzed.
- **Similarity to Plan 06 gap:** This is the **same shape** as the Plan 06 SES auto-import failure — 84.4 fixed module-level resource naming (proven working in the init apply) but left cross-install identity/permission interactions broken.
- **Possible fix designs (84.4.1 to choose):**
  1. **Shared SCP with union of trusted roles:** Build a single `km-sandbox-containment` SCP whose `ArnNotLike` allowlist enumerates ALL installed-prefix create-handler ARNs (`km-*`, `rg-*`, etc.). Requires either an external registry of "which prefixes exist" OR pattern-matching `*-create-handler` style.
  2. **Per-install SCPs attached to separate OUs:** Move each install's application account into its own OU and attach the install's SCP only to its OU. AWS Organizations model permits this but requires operator-level OU structure work.
  3. **Drop SCP entirely for non-canonical installs:** Second install assumes the canonical install's SCP applies and bypasses its own SCP module. Couples installs to the canonical one.
  4. **Pattern-based allow in SCP:** Use `aws:PrincipalArn` like `arn:aws:iam::*:role/*-create-handler` in the allowlist to trust any install's create-handler by suffix. Loosest but simplest.
- **Workaround for 84.4-08 UAT:** Manually edit the deployed SCP `p-cvd490xt` body to ADD `arn:aws:iam::052251888500:role/rg-create-handler` to the trusted-role allowlist. This is destructive of the design but unblocks the operator. Documented in rg-uat-log.md.
- **Status:** Deferred — file as **co-primary** Phase 84.4.1 task alongside Plan 06 gap.

### Plan 06 auto-import does NOT fire on second-install-shared-domain scenario (PRIMARY 84.4.1 GAP)

- **Discovered during:** 84.4-08 fresh-install UAT, Step 3 (`./km bootstrap --shared-ses --dry-run=false`)
- **Symptom:** Bootstrap output: `Shared SES domain identity: creating`. Terraform then attempted to create the 3 DKIM CNAMEs at `*._domainkey.sandboxes.klankermaker.ai`. Route53 rejected all 3 with `InvalidChangeBatch: Tried to create resource record set ... but it already exists` — they were already created by the canonical km install.
- **Root cause:** `bootstrap.go:595` gates `autoImportFoundationSESRecords` on `!registerID`. `detectSharedSESState` (`bootstrap.go:550`) returned `registerID = true` for the rg install even though the SES domain identity for `sandboxes.klankermaker.ai` already exists in AWS (owned by km install). Either:
  1. `detectSharedSESState` doesn't probe AWS for the existing domain identity at all (uses only state-file presence?), OR
  2. It probes but cannot find the existing identity for the cfg-derived email domain, OR
  3. The whole gate is conceptually wrong — DKIM record auto-import should be independent of `registerID` (records exist in Route53 ⇒ import them; the SES domain identity ownership is a separate question).
- **Operator-visible impact:** This is the headline 84.4 thesis violation. The phase was *expressly designed* to make second installs on a shared SES domain frictionless. Today it requires manual `terragrunt import` for each DKIM/MX/TXT/SES-identity record, which is exactly what Plan 06 was supposed to eliminate.
- **Plan 06's unit tests:** Passed at plan-execution time. The unit test mocks made the auto-import path appear correct; the integration scenario was never end-to-end exercised against a live shared-domain account until 84.4-08.
- **Workaround applied during 84.4-08:** Manual `terragrunt import` calls for each DKIM CNAME (documented in rg-uat-log.md / SUMMARY).
- **Fix needed in 84.4.1:**
  1. Make `detectSharedSESState` actually call SES `GetEmailIdentity` (or list identities) against the target email domain — return `registerID = false` if AWS already has it.
  2. OR — preferred: separate the DKIM-record-import gate from the `registerID` gate. Import any Route53 record whose name matches the expected DKIM/MX/TXT pattern, regardless of `registerID`. This is more robust against partial-state scenarios.
  3. Add an integration test that actually exercises the second-install-shared-domain path with a real Route53 hosted zone (LocalStack or moto). The current unit-test-only coverage missed this entirely.
- **Status:** Deferred — file as **the primary** Phase 84.4.1 task. This is the load-bearing 84.4 finding.

### km init --plan fails cryptically when Lambda zips not built (fresh-clone DX) + Makefile drift

- **Discovered during:** 84.4-08 fresh-install UAT, Step 4 (`./km init --plan`)
- **Symptom 1:** `create-handler` plan fails with `filebase64sha256: open .../build/create-handler.zip: no such file or directory`. Operator must know to build zips first.
- **Symptom 2:** `make build-lambdas` builds only 4 of the 6 required Lambda zips. Missing: `create-handler.zip` and `km-slack-bridge.zip`. The Makefile target is **drifted** from `internal/app/cmd/init.go:buildLambdaZips()` which builds all 6 (lines 1583-1591). Workaround: run `./km init --lambdas` instead of `make build-lambdas`.
- **Why fresh clones hit this:** `build/` is gitignored. The Lambda terragrunt modules reference `../../../../build/<name>.zip` paths that must exist at plan time.
- **Fix needed in 84.4.1:**
  1. Sync Makefile `build-lambdas` target with `init.go:buildLambdaZips()`'s 6-zip list, OR delete the Makefile target and tell operators to use `./km init --lambdas`.
  2. `km init --plan` should check for required Lambda zips and either auto-build them or fail with a clear error pointing to `./km init --lambdas`.
- **Status:** Deferred — file as Phase 84.4.1 task. Workaround documented in rg-uat-log.md.

### km bootstrap --shared-ses missing region.hcl prereq on fresh installs

- **Discovered during:** 84.4-08 fresh-install UAT, Step 3 (`./km bootstrap --shared-ses --dry-run=false`)
- **Symptom:** Bootstrap fails at terragrunt init for `infra/live/use1/ses-shared-rule-set/`:
  ```
  Error in function call ... Call to function "read_terragrunt_config" failed:
  You attempted to run terragrunt in a folder that does not contain a terragrunt.hcl file.
  Path: "../region.hcl".
  ```
  The live wiring at `infra/live/use1/ses-shared-rule-set/terragrunt.hcl` line 14 reads `${get_terragrunt_dir()}/../region.hcl`, but `region.hcl` is gitignored (`infra/live/*/region.hcl`) and only written by `km init` (`internal/app/cmd/init.go:929` and `init.go:1271`). On a truly fresh clone, `km bootstrap --shared-ses` fires before `km init`, hitting this dependency.
- **Why canonical km install never hit it:** klankrmkr has `region.hcl` from a long-ago `km init`. The dependency is invisible until a fresh clone.
- **Fix needed:** Either:
  1. `km bootstrap --shared-ses` (and `km bootstrap --all`) should write `region.hcl` as a prereq before invoking terragrunt — extract the write logic from `init.go` into a shared helper.
  2. OR remove the `../region.hcl` dependency from `ses-shared-rule-set/terragrunt.hcl` — derive region from `KM_REGION` env var or site.hcl `local.region` (which exists and is exported by ExportTerragruntEnvVars).
- **Workaround applied during 84.4-08:** Manual write of region.hcl with `region_label = "use1"; region_full = "us-east-1"`.
- **Status:** Deferred — file as Phase 84.4.1 task.

### km configure state_bucket prompt missing default + HeadBucket retry UX (Phase 84.3 closure (a) regression)

- **Discovered during:** 84.4-08 fresh-install UAT (operator running `km configure` for prefix `rg`)
- **Symptom:** Configure prompted `S3 state bucket for sandbox metadata (used by km list/status):` with NO default in brackets. Other prompts in the same configure flow correctly show `[default]:` (e.g., `Resource prefix ... [rg]:`, `Operator email ... [operator-rg@...]:`, `Maximum concurrent ... [10]:`). Operator entered a custom value `tf-rg-state-use1-0123456` (treated suffix as placeholder example). Bootstrap then created `tf-rg-state-use1` (computed by site.hcl `tf-${KM_RESOURCE_PREFIX}-state-${region}`), leaving km-config.yaml's `state_bucket:` value out of sync with the real backend bucket.
- **Impact:** km wrapper paths that read `cfg.StateBucket` directly (km list S3 fallback, km doctor's state-bucket check, km uninit's safety pre-check) hit 404 on the bogus name. Terragrunt itself is fine — it uses site.hcl's computed name.
- **Fix needed:** `km configure` should:
  1. Compute the default `tf-${resource_prefix}-state-${region}` from earlier prompt answers and show it as `[tf-rg-state-use1]:`.
  2. HeadBucket-check the default before prompting (Phase 84.3 closure (a) intent).
  3. If globally taken, offer `[Y/edit/abort]` retry UX.
  4. If accepted, write the SAME computed name to km-config.yaml so wrapper paths and terragrunt agree.
- **Workaround applied during 84.4-08:** `sed -i '' 's|tf-rg-state-use1-0123456|tf-rg-state-use1|' km-config.yaml`
- **Status:** Deferred — file as Phase 84.4.1 task.
