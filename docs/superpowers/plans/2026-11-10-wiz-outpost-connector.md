# Wiz Outpost Account Connector Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `km init --wiz --dry-run=false` creates Wiz's Outpost connector roles (`WizAccess-Role`, `WizScannerRole-<suffix>`) in the install's account from a `wiz.outpost:` block in `km-config.yaml`, then prints the three values to paste into the Wiz UI.

**Architecture:** A pure Go package `pkg/wizoutpost` owns the config shape, validation, and the mapping to CloudFormation parameters. `km init` exports that mapping as one env var `KM_WIZ_OUTPOST_JSON`. A new Terraform module deploys Wiz's vendored template verbatim through one `aws_cloudformation_stack`. `km doctor`, `km wiz status`, and the EBS orphan sweeps read AWS directly.

**Tech Stack:** Go (cobra, viper, aws-sdk-go-v2: cloudformation, iam), Terraform/Terragrunt (hashicorp/aws `aws_cloudformation_stack`).

**Spec:** `docs/superpowers/specs/2026-11-10-wiz-outpost-connector-design.md`

## Global Constraints

- Template file is Wiz's own, verbatim: `infra/modules/wiz-outpost-connector/v1.0.0/wiz-outpost-connector.json` (already copied in; `WizVersionLastUpdated` default `2025-10-07`). Never hand-edit it; never fetch it from a URL.
- Dormant by default: no `wiz:` block ⇒ `KM_WIZ_OUTPOST_JSON` unset ⇒ module skipped, zero doctor checks, zero AWS calls.
- `isOrg` is always `Disabled`. The three `*RoleMapping` parameters stay at the template default `Outpost Scanning` (not exposed).
- Stack name: `{resource_prefix}-wiz-outpost-connector`.
- Module must NOT declare `required_providers` (providers live in `infra/live/root.hcl`).
- New `regionalModules()` entry ⇒ `make build` before `km init`; `TestRunInitPlan_ModuleOrder` hardcodes the module count and must be bumped.
- Config key `wiz` must be added to the v2→v merge-list in `config.Load()` and decoded with `v.UnmarshalKey("wiz", &cfg.Wiz)`, or the block is silently ignored.
- Doc examples use synthetic values (`111122223333`, `example.com`), never real tenants or account ids.
- Every commit scoped by explicit pathspec (`git add <paths>`), never `git add -A`.

## Review Focus

1. **`--wiz` with no `wiz:` block** — must fail before any terragrunt run with a message naming the missing `wiz.outpost` block (not the opaque "requires KM_WIZ_OUTPOST_JSON"). Test in Task 4.
2. **A partially filled block** (e.g. `external_id` missing, or a non-UUID external id) — `config.Load()` errors naming the key; `km init` never reaches AWS. Test in Task 2.
3. **Wiz temporary scan volumes/snapshots carrying a copied `km:sandbox-id` tag** — never reported as orphans, never deleted by `km doctor --dry-run=false --delete-ebs`. Test in Task 6.
4. **`km uninit` after the `wiz:` block was removed** — the live unit must still evaluate (empty env ⇒ `{}` parameters) so destroy runs. Test in Task 3 (HCL fallback asserted by a Go test that reads the unit).
5. **Template bump that renames a parameter or output km relies on** — fails `go test`, not the apply. Test in Task 1.

---

### Task 1: `pkg/wizoutpost` — settings, validation, CFN parameter mapping, template contract

**Files:**
- Create: `pkg/wizoutpost/wizoutpost.go`
- Create: `pkg/wizoutpost/wizoutpost_test.go`
- (exists) `infra/modules/wiz-outpost-connector/v1.0.0/wiz-outpost-connector.json`

**Interfaces:**
- Produces:
  ```go
  type Scanning struct {
      Data, EKS, Lightsail, Serverless, CloudCost, RDSLogs,
      S3AWSLogs, S3KMSDecrypt, TerraformBuckets *bool
  } // yaml/mapstructure keys: data, eks, lightsail, serverless, cloud_cost,
    // rds_logs, s3_aws_logs, s3_kms_decrypt, terraform_buckets
  type Settings struct {
      TrustRoleARN          string   // trust_role_arn
      OrchestratorAccountID string   // orchestrator_account_id
      ExternalID            string   // external_id
      RoleSuffix            string   // role_suffix
      AccessRoleName        string   // access_role_name (default "WizAccess-Role")
      Scanning              Scanning // scanning
  }
  const TemplateRelPath = "infra/modules/wiz-outpost-connector/v1.0.0/wiz-outpost-connector.json"
  const MaxInlineTemplateBytes = 51200
  func (s Settings) Validate() error
  func (s Settings) Parameters() map[string]string
  func (s Settings) AccessRoleNameOrDefault() string
  func (s Settings) ScannerRoleName() string            // "WizScannerRole-" + RoleSuffix
  func StackName(prefix string) string                  // prefix + "-wiz-outpost-connector"
  func ExportJSON(s Settings) (string, error)            // {"parameters":{...}} compact, keys sorted
  var RequiredParameters []string                        // every key Parameters() emits + "WizVersionLastUpdated"
  var RequiredOutputs = []string{"WizAccessRoleARNID", "WizScannerRoleARNID", "ExternalID"}
  ```

- [ ] **Step 1: Write the failing tests**

```go
func TestParameters_DefaultsMirrorTemplate(t *testing.T) {
    s := Settings{TrustRoleARN: "arn:aws:iam::111122223333:role/wiz-dc", OrchestratorAccountID: "444455556666",
        ExternalID: "abcdef12-3456-7890-abcd-ef1234567890", RoleSuffix: "km"}
    p := s.Parameters()
    want := map[string]string{
        "ExternalId": "abcdef12-3456-7890-abcd-ef1234567890", "RoleARN": "arn:aws:iam::111122223333:role/wiz-dc",
        "OrchestratorAccountID": "444455556666", "RoleSuffix": "km", "WizRoleName": "WizAccess-Role",
        "isOrg": "Disabled", "CloudCostScanning": "Enabled", "DataScanning": "Disabled", "EKSScanning": "Disabled",
        "LightsailScanning": "Disabled", "RDSLogsScanning": "Enabled", "S3AWSLogsScanning": "Enabled",
        "S3KMSDecrypt": "Enabled", "ServerlessScanning": "Enabled", "TerraformBucketScanning": "Enabled",
    }
    // assert reflect.DeepEqual(p, want)
}
func TestParameters_ToggleOverrides(t *testing.T)        // EKS=&true → "EKSScanning":"Enabled"; CloudCost=&false → "Disabled"
func TestValidate_RejectsEachMissingOrMalformedField(t *testing.T)
    // table: empty TrustRoleARN; "not-an-arn"; OrchestratorAccountID "1234" and "11112222333a";
    // ExternalID "" and "not-a-uuid"; RoleSuffix "". Each error message contains the yaml key
    // ("wiz.outpost.trust_role_arn", ... ). Valid settings → nil.
func TestTemplateContract_ParametersAndOutputsExist(t *testing.T)
    // read ../../ + TemplateRelPath; every RequiredParameters key ∈ template.Parameters;
    // every RequiredOutputs key ∈ template.Outputs; every Enabled/Disabled value Parameters()
    // emits ∈ that parameter's AllowedValues; ExternalId sample matches the template AllowedPattern.
func TestTemplateContract_MinifiedUnderInlineLimit(t *testing.T)
    // json.Compact(file) length < MaxInlineTemplateBytes (currently 33,543)
func TestExportJSON_Stable(t *testing.T)                   // two calls byte-identical; starts with `{"parameters":{`
func TestStackAndRoleNames(t *testing.T)                   // StackName("km")=="km-wiz-outpost-connector"; ScannerRoleName()=="WizScannerRole-km"
```

- [ ] **Step 2: Run** `go test ./pkg/wizoutpost/` — Expected: FAIL (package/functions undefined).
- [ ] **Step 3: Implement** in `pkg/wizoutpost/wizoutpost.go`. Validation regexes: role ARN `^arn:aws(-us-gov|-cn)?:iam::\d{12}:role/.+$`, account `^\d{12}$`, external id `^\S{8}-\S{4}-\S{4}-\S{4}-\S{12}$` (the template's own pattern). Nil `*bool` ⇒ template default. `json.Marshal` of a map already sorts keys.
- [ ] **Step 4: Run** `go test ./pkg/wizoutpost/` — Expected: PASS.
- [ ] **Step 5: Commit** `git add pkg/wizoutpost infra/modules/wiz-outpost-connector/v1.0.0/wiz-outpost-connector.json docs/superpowers/specs/2026-11-10-wiz-outpost-connector-design.md docs/superpowers/plans/2026-11-10-wiz-outpost-connector.md && git commit -m "feat(wiz): pkg/wizoutpost settings, CFN parameter mapping, vendored Outpost template"`

### Task 2: `wiz:` config block

**Files:**
- Modify: `internal/app/config/config.go` (Config struct near `Webhooks WebhooksConfig` ~line 1302; merge-list near `"webhooks",` ~line 1558; `UnmarshalKey` near ~line 1837)
- Create: `internal/app/config/config_wiz_test.go`

**Interfaces:**
- Consumes: `wizoutpost.Settings`, `Settings.Validate()`.
- Produces: `type WizConfig struct { Outpost *wizoutpost.Settings \`mapstructure:"outpost" yaml:"outpost,omitempty"\` }`; field `Config.Wiz WizConfig \`mapstructure:"wiz" yaml:"wiz,omitempty"\``.

- [ ] **Step 1: Write failing tests** (pattern: `config_webhooks_test.go` — write a temp km-config.yaml, point the loader at it, call `Load()`):
  - `TestLoad_WizOutpostBlockDecoded` — full block → `cfg.Wiz.Outpost.RoleSuffix == "km"`, `*cfg.Wiz.Outpost.Scanning.EKS == true` when set.
  - `TestLoad_NoWizBlock_NilOutpost` — `cfg.Wiz.Outpost == nil`.
  - `TestLoad_WizOutpostInvalid_Errors` — `external_id: "nope"` → `Load()` error containing `wiz.outpost.external_id`.
  - `TestLoad_WizIsInMergeList` — guard in the style of the existing `slack.allow` merge-list regression test.
- [ ] **Step 2: Run** `go test ./internal/app/config/ -run Wiz` — Expected: FAIL.
- [ ] **Step 3: Implement**: struct + merge-list entry + `UnmarshalKey("wiz", &cfg.Wiz)`; after decode, if `cfg.Wiz.Outpost != nil` return `cfg.Wiz.Outpost.Validate()` wrapped. Add a `CRITICAL: "wiz" must be in the v2→v merge-list` comment like the siblings.
- [ ] **Step 4: Run** `go test ./internal/app/config/` — Expected: PASS.
- [ ] **Step 5: Commit** `git add internal/app/config/config.go internal/app/config/config_wiz_test.go && git commit -m "feat(wiz): wiz.outpost block in km-config.yaml"`

### Task 3: Terraform module + live unit + env export + module list

**Files:**
- Create: `infra/modules/wiz-outpost-connector/v1.0.0/{main.tf,variables.tf,outputs.tf}`
- Create: `infra/live/use1/wiz-outpost-connector/terragrunt.hcl`
- Modify: `internal/app/cmd/init.go` — `regionalModules()` (append an entry at the END of the list; no dependencies), `ExportTerragruntEnvVars` (~line 2633, next to `KM_WEBHOOK_SOURCES`)
- Modify: `internal/app/cmd/init_plan_test.go:441` (`TestRunInitPlan_ModuleOrder` count +1)
- Create: `internal/app/cmd/init_wiz_test.go`

**Interfaces:**
- Consumes: `wizoutpost.ExportJSON`, `wizoutpost.StackName`.
- Produces: module name `"wiz-outpost-connector"` with `envReqs: []string{"KM_WIZ_OUTPOST_JSON"}`; module outputs `access_role_arn`, `scanner_role_arn`, `external_id`, `template_version`, `stack_name`.

- [ ] **Step 1: Write failing tests** in `init_wiz_test.go`:
  - `TestExportTerragruntEnvVars_WizOutpost` — cfg with `Wiz.Outpost` set ⇒ `os.Getenv("KM_WIZ_OUTPOST_JSON") == wizoutpost.ExportJSON(...)`; cfg without ⇒ unset (use `t.Setenv("KM_WIZ_OUTPOST_JSON", "")` then unset).
  - `TestExportTerragruntEnvVars_WizOutpostEnvDriftWarns` — env set to a different value ⇒ stderr contains `KM_WIZ_OUTPOST_JSON` and the env value wins (mirror the webhook drift test).
  - `TestRegionalModules_WizOutpostEntry` — `RegionalModules(dir)` contains `wiz-outpost-connector` with `EnvReqs == []string{"KM_WIZ_OUTPOST_JSON"}`, and it is the last entry.
  - `TestWizLiveUnit_ToleratesEmptyEnv` — read `infra/live/use1/wiz-outpost-connector/terragrunt.hcl`; assert it contains `get_env("KM_WIZ_OUTPOST_JSON", "{}")` and a `try(` around the `parameters` decode (so `km uninit` with the block removed still evaluates), and sources `infra/modules/wiz-outpost-connector/v1.0.0`.
  - `TestWizModule_NoRequiredProviders` — `main.tf`/`variables.tf` contain no `required_providers`.
- [ ] **Step 2: Run** `cd internal/app/cmd && go test . -run 'Wiz|ModuleOrder'` — Expected: FAIL.
- [ ] **Step 3: Implement**:
  - `main.tf`: one `aws_cloudformation_stack "this"`: `name = var.stack_name`, `template_body = jsonencode(jsondecode(file("${path.module}/wiz-outpost-connector.json")))`, `parameters = var.parameters`, `capabilities = ["CAPABILITY_NAMED_IAM"]`, `tags = { "km:managed" = "true", "km:component" = "wiz-outpost-connector" }`.
  - `variables.tf`: `stack_name` (string), `parameters` (map(string)).
  - `outputs.tf`: the five outputs, from `aws_cloudformation_stack.this.outputs["WizAccessRoleARNID"]` / `["WizScannerRoleARNID"]` / `["ExternalID"]`, `template_version = aws_cloudformation_stack.this.parameters["WizVersionLastUpdated"]`, `stack_name`.
  - Live unit: copy the `remote_state`/`include`/`locals` header of `infra/live/use1/lambda-webhook-bridge/terragrunt.hcl` (state key `.../wiz-outpost-connector/terraform.tfstate`); `inputs = { stack_name = "${local.site_vars.locals.site.label}-wiz-outpost-connector", parameters = try(jsondecode(get_env("KM_WIZ_OUTPOST_JSON", "{}")).parameters, {}) }`. Confirm `site.label` is the resource prefix by reading `site.hcl`; use whatever key `lambda-webhook-bridge` passes as `resource_prefix`.
  - Export: `if cfg.Wiz.Outpost != nil` → `ExportJSON`, same env-wins drift WARN shape as `KM_WEBHOOK_SOURCES`.
- [ ] **Step 4: Run** `cd internal/app/cmd && go test . -run 'Wiz|ModuleOrder|RegionalModules'` — PASS. Then `cd infra/live/use1/wiz-outpost-connector && terragrunt hclfmt --check` and `terraform -chdir=../../../modules/wiz-outpost-connector/v1.0.0 fmt -check` — no diff. Sweep: `find infra/modules/wiz-outpost-connector -name '.terraform*'` prints nothing.
- [ ] **Step 5: Commit** module, live unit, init.go, both test files.

### Task 4: `km init --wiz` scoped alias + paste-out

**Files:**
- Modify: `internal/app/cmd/init.go` — `scopedCheapAllowlist` (~line 150), `ResolveScopedModule` (~line 186), flag registration (~line 1517/1590), `RunInitScopedWithRunner` post-apply (~line 380), `RunInitWithRunner` post-apply loop (same hook for full init), `runInitScoped` pre-check
- Modify: `internal/app/cmd/init_scoped_test.go`, `internal/app/cmd/init_webhook_test.go` (call sites gain one bool)
- Modify: `internal/app/cmd/help/init.txt` (if present; else the cobra `Long`)
- Test: `internal/app/cmd/init_wiz_test.go`

**Interfaces:**
- Consumes: module outputs from Task 3; `runner.Output(ctx, dir) (map[string]any, error)`; `extractValue`.
- Produces: `func ResolveScopedModule(onlyVal string, github, slack, h1, email, webhooks, wiz bool) (string, error)`; `func printWizPasteOut(w io.Writer, accessARN, scannerARN, externalID string)`.

- [ ] **Step 1: Write failing tests**:
  - `TestResolveScopedModule_Wiz` — `ResolveScopedModule("", false,false,false,false,false,true)` → `"wiz-outpost-connector", nil`; `--wiz` + `--github` → error mentioning `--wiz`.
  - `TestPrintWizPasteOut` — output exactly:
    ```
    Wiz Outpost connector — paste into the Wiz UI:
      Access role ARN   arn:aws:iam::111122223333:role/WizAccess-Role
      Scanner role ARN  arn:aws:iam::111122223333:role/WizScannerRole-km
      External ID       abcdef12-3456-7890-abcd-ef1234567890
    ```
  - `TestRunInitScoped_WizPrintsPasteOutAfterApply` — mockRunner whose `Output` returns the three outputs; `RunInitScopedWithRunner(..., "wiz-outpost-connector", false, false)` stdout contains `paste into the Wiz UI`. Dry-run ⇒ no paste-out, `Would apply: wiz-outpost-connector`.
  - `TestRunInitScoped_WizWithoutBlock_ErrorsClearly` — the `runInitScoped` pre-check: cfg with `Wiz.Outpost == nil` and module `wiz-outpost-connector` ⇒ error `--wiz requires a wiz.outpost block in km-config.yaml`. Factor the check as `func requireWizBlock(cfg *config.Config, module string) error` so it is testable without AWS.
- [ ] **Step 2: Run** `cd internal/app/cmd && go test . -run 'Wiz|ResolveScoped'` — FAIL.
- [ ] **Step 3: Implement** the above; update every existing `ResolveScopedModule` call site and the error string `at most one of --only/--github/--slack/--h1/--email/--webhooks/--wiz may be set`. Flag help: `Scoped apply of wiz-outpost-connector (sugar for --only wiz-outpost-connector)`. Call `requireWizBlock` in `runInitScoped` before `LoadAWSConfig`.
- [ ] **Step 4: Run** `cd internal/app/cmd && go test . -run 'Wiz|ResolveScoped|Scoped|Webhook'` — PASS.
- [ ] **Step 5: Commit** init.go, help text, the three test files.

### Task 5: `km wiz status`

**Files:**
- Create: `internal/app/cmd/wiz.go`, `internal/app/cmd/wiz_test.go`
- Modify: `internal/app/cmd/root.go` (`root.AddCommand(NewWizCmd(cfg))`)

**Interfaces:**
- Consumes: `wizoutpost.StackName`, `printWizPasteOut` (Task 4).
- Produces: `type WizStackAPI interface { DescribeStacks(ctx, *cloudformation.DescribeStacksInput, ...func(*cloudformation.Options)) (*cloudformation.DescribeStacksOutput, error) }`; `func runWizStatus(ctx context.Context, w io.Writer, api WizStackAPI, prefix string, configured bool) error`; `func NewWizCmd(cfg *config.Config) *cobra.Command`.

- [ ] **Step 1: Write failing tests** with a fake `WizStackAPI`:
  - `TestWizStatus_NotConfigured_NoStack` — `configured=false`, stack absent ⇒ prints `Wiz Outpost connector: not configured (no wiz.outpost block in km-config.yaml)`.
  - `TestWizStatus_NotConfigured_StackExists` — `configured=false`, stack present ⇒ prints `Wiz Outpost stack exists but wiz: is not configured — remove the connector in Wiz, then km uninit or delete the stack` plus the paste-out values (operator-invoked, so the one DescribeStacks call is fine).
  - `TestWizStatus_StackMissing` — DescribeStacks returns a "does not exist" ValidationError ⇒ prints `stack <prefix>-wiz-outpost-connector not found — run km init --wiz --dry-run=false`, returns nil.
  - `TestWizStatus_Healthy` — stack `CREATE_COMPLETE` with the three outputs + parameter `WizVersionLastUpdated=2025-10-07` ⇒ output contains `Status: CREATE_COMPLETE`, `Template: 2025-10-07`, and the paste-out block.
- [ ] **Step 2: Run** `cd internal/app/cmd && go test . -run WizStatus` — FAIL.
- [ ] **Step 3: Implement**; `NewWizCmd` has one subcommand `status`; the real client is `cloudformation.NewFromConfig(awsCfg)` built with `awspkg.LoadAWSConfig`.
- [ ] **Step 4: Run** — PASS. `go build ./...` clean.
- [ ] **Step 5: Commit** wiz.go, wiz_test.go, root.go.

### Task 6: `km doctor` — Wiz connector group + Wiz-safe orphan sweeps

**Files:**
- Create: `internal/app/cmd/doctor_wiz.go`, `internal/app/cmd/doctor_wiz_test.go`
- Modify: `internal/app/cmd/doctor.go` (config interface ~line 312 + adapter ~line 395: `GetWizOutpost() *wizoutpost.Settings`; register checks next to the Phase 127 webhook block ~line 4983)
- Modify: `internal/app/cmd/doctor_ebs.go` (three loops: ~line 115 volumes, ~line 285 untagged volumes, ~line 405 snapshots)
- Modify: `internal/app/cmd/doctor_ebs_test.go`

**Interfaces:**
- Consumes: `WizStackAPI` (Task 5), `wizoutpost.Settings`, `wizoutpost.StackName`, `ScannerRoleName`, `AccessRoleNameOrDefault`, template default version (read `WizVersionLastUpdated` default from the vendored template via a small exported `wizoutpost.TemplateVersion(repoRoot string) (string, error)` — add to Task 1's package here, with a test).
- Produces: `type WizRoleAPI interface { GetRole(ctx, *iam.GetRoleInput, ...func(*iam.Options)) (*iam.GetRoleOutput, error) }`; `func checkWizOutpostStack(ctx, api WizStackAPI, prefix, wantVersion string) CheckResult`; `func checkWizOutpostTrust(ctx, api WizRoleAPI, s wizoutpost.Settings) CheckResult`; `func isWizScanResource(tags []ec2types.Tag) bool`.

- [ ] **Step 1: Write failing tests**:
  - `TestIsWizScanResource` — tag `wiz` = `auto-gen-volume` / `auto-gen-snapshot` / `shareable-resource` ⇒ true; `wiz=other`, no `wiz` tag ⇒ false.
  - `TestCheckOrphanedEBSVolumes_SkipsWizScanVolume` — detached volume with `km:sandbox-id=sb-gone` AND `wiz=auto-gen-volume`, `dryRun=false, deleteEBS=true` ⇒ fake records zero `DeleteVolume` calls and the volume is not in the message. Same shape for `checkUntaggedAvailableVolumes` and `checkOrphanedSnapshots` (`TestCheckOrphanedSnapshots_SkipsWizScanSnapshot`).
  - `TestCheckWizOutpostStack` — table: missing ⇒ Warn with remediation `km init --wiz --dry-run=false`; `UPDATE_ROLLBACK_COMPLETE` ⇒ Warn; complete but deployed `WizVersionLastUpdated` ≠ want ⇒ Warn naming both versions; complete + match ⇒ OK.
  - `TestCheckWizOutpostTrust` — fake `GetRole` returns URL-encoded trust docs (IAM returns `AssumeRolePolicyDocument` URL-encoded; decode with `url.QueryUnescape`). Access role principal must equal `TrustRoleARN`; scanner role principal must equal `arn:aws:iam::<OrchestratorAccountID>:role/WizOrchestratorNodePoolRole`; both `Condition.StringEquals["sts:ExternalId"]` must equal `ExternalID`. Mismatch on any ⇒ Warn naming the role and the field; all match ⇒ OK.
  - `TestDoctor_WizDormant_NoChecks` — with `GetWizOutpost()==nil`, the registered check list contains no `Wiz` names.
- [ ] **Step 2: Run** `cd internal/app/cmd && go test . -run 'Wiz|Orphaned|Untagged'` — FAIL.
- [ ] **Step 3: Implement**. Add `if isWizScanResource(v.Tags) { continue }` as the first line in each of the three loops. Register `checkWizOutpostStack` + `checkWizOutpostTrust` only when `GetWizOutpost() != nil`; a dormant install registers nothing and makes no AWS call. The "stack left behind after the block was removed" case is reported by `km wiz status` (Task 5), not doctor.
- [ ] **Step 4: Run** `cd internal/app/cmd && go test . -run 'Wiz|Orphaned|Untagged|Doctor'` — PASS (pre-existing local Bootstrap/Cluster/Configure failures excepted; confirm they fail identically without this change).
- [ ] **Step 5: Commit** doctor_wiz.go, doctor_wiz_test.go, doctor.go, doctor_ebs.go, doctor_ebs_test.go, and the `TemplateVersion` addition.

### Task 7: Docs

**Files:**
- Create: `docs/wiz-outpost-connector.md`
- Modify: `docs/wiz-sensor.md` § 7 table (add row: *Wiz reads the account* / `wiz.outpost:` / this doc); `CLAUDE.md` "Where to look" table (one row); `CLAUDE.md` CLI list (`km wiz status`, `km init --wiz`)

- [ ] **Step 1: Write `docs/wiz-outpost-connector.md`** with sections: What it is (fourth Wiz direction); Prerequisites (orchestrator already running; where in the Wiz UI to find the tenant trust role ARN — "User Settings → Tenant" per the template's own parameter description — the orchestrator account id, and the external id the connector form generates); the `wiz.outpost:` block with every key and default (synthetic values); Apply (`make build` → `km init --wiz --dry-run` → `--dry-run=false`); Paste into the Wiz UI (the three values; `km wiz status` reprints); What `km doctor` checks; Template bump procedure (export the current Outpost connector template from the Wiz tenant or the Terraform the other accounts use; create `infra/modules/wiz-outpost-connector/v1.1.0/` as a copy with the new JSON; repoint the live unit; `go test ./pkg/wizoutpost/` — the contract test names any renamed parameter/output; `km init --wiz --dry-run`; apply; confirm the connector stays healthy in Wiz); Removing it (remove the connector in Wiz first, then `km uninit` or delete the stack; removing the yaml block alone destroys nothing); Troubleshooting (stack `ROLLBACK_COMPLETE` ⇒ read stack events, usually a role name already taken — `WizAccess-Role` created by hand earlier; doctor trust mismatch ⇒ re-apply; disk scans failing ⇒ check for an SCP on the account, `km doctor` "SCP content"); Deploy surface (no Lambda, sidecar, userdata, profile schema, or recreate).
- [ ] **Step 2: Verify** `grep -nE '[0-9]{12}' docs/wiz-outpost-connector.md` shows only `111122223333`/`444455556666`.
- [ ] **Step 3: Commit** the four docs.

### Task 8: Whole-branch verification

- [ ] **Step 1:** `go build ./... && go vet ./pkg/wizoutpost/ ./internal/app/config/` — clean.
- [ ] **Step 2:** `go test ./pkg/... ./internal/app/config/...` and `cd internal/app/cmd && go test . -count=1` — capture each command's own exit code; only the known pre-existing local failures may remain, and they must fail identically on `origin/main` (check in a throwaway worktree, never `git stash`).
- [ ] **Step 3:** `make build` (VERSION bump is expected), then `km init --wiz --dry-run` against a config WITHOUT a `wiz:` block ⇒ the clear `requires a wiz.outpost block` error; WITH a block ⇒ `Would apply: wiz-outpost-connector [tier-1]`.
- [ ] **Step 4:** Live (operator, needs SSO): `km init --wiz --dry-run=false` → paste-out printed → `km wiz status` → `km doctor` Wiz group green → paste into Wiz UI → connector healthy. Record results in the doc's UAT section.
