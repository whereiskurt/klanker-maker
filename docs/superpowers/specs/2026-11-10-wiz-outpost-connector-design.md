# Wiz Outpost account connector — design

Date: 2026-11-10. Status: draft for approval.

## Intent

The operator already runs Wiz with an **Outpost** deployment: a Wiz
Orchestrator account (elsewhere, not managed by km) scans several AWS accounts
that are already connected to it. The account a km install lives in is not one
of them. This feature makes that account a connected Outpost account —
inventory, posture (CSPM), and agentless disk scanning — driven entirely from
`km-config.yaml`:

```bash
km init --wiz --dry-run=false     # or a full km init --dry-run=false
# km prints three values → operator pastes them into the Wiz UI connector form
```

This is the fourth Wiz integration and points in a fourth direction. The
existing three (see `docs/wiz-sensor.md` § 7) are: km pulls from Wiz
(`checks.triggers`), Wiz pushes to km (`webhooks:`), a sandbox reports to Wiz
(the runtime sensor). This one is **Wiz reads the account**. It shares no code
or config with the others.

## Goals

- Create, in the install's application account, exactly the IAM footprint of
  Wiz's **Outpost cloud connector** CloudFormation template:
  - `WizAccess-Role` (name configurable) — read-only (`ViewOnlyAccess` +
    `SecurityAudit` + Wiz's inline read policies), trusted by the Wiz tenant's
    data-center role ARN, `sts:ExternalId`-gated.
  - `WizScannerRole-<suffix>` — snapshot / volume / KMS work for disk scanning
    (plus serverless and Terraform-bucket scanning), trusted by
    `arn:aws:iam::<orchestrator>:role/WizOrchestratorNodePoolRole`, same
    external id.
  - The policies attached to them, per the template's toggles.
- Configure it from a new `wiz.outpost:` block in `km-config.yaml`; apply with
  a scoped `km init --wiz` (tier-1 sugar, like `--github`) or a full `km init`.
- After apply, print the values the Wiz UI connector form needs: access role
  ARN, scanner role ARN, external id. Re-printable later.
- `km doctor` reports the connector's health.
- Dormant by default: no `wiz:` block ⇒ nothing applied, no AWS calls,
  byte-identical behaviour for every other install.

## Non-goals

- Deploying or managing the Outpost orchestrator, or any account other than
  this install's application account.
- Linked capacity accounts (Phase 126) — not connected.
- Registering the connector in Wiz through the Wiz API. The operator pastes the
  three values into the Wiz UI. (Possible later step: use the Wiz API
  credentials km already holds for `checks.triggers` to read an existing
  connector's settings and pre-fill orchestrator id / trust role ARN. Out of
  scope now.)
- The non-Outpost (SaaS-scanning) template. Outpost only.
- Any SCP change. The target account carries no km SCP (confirmed by the
  operator: that account never had the SCP applied). (If a future install
  that does carry the SCP enables this, `DenyInfraAndStorage` denies
  `ec2:CreateSnapshot`/`CopySnapshot`/`DeleteSnapshot` to the scanner role and
  disk scanning fails inside Wiz with no km-side symptom. See "Builder notes".)
- Multi-install sharing. The operator confirmed one install in this account.

## Anti-goals (fails even if it "works")

- Hand-porting Wiz's ~600 actions into HCL. It drifts from Wiz silently; the
  template must be Wiz's own file, verbatim.
- Fetching the template from a Wiz URL at apply time. A Wiz-side change would
  alter scanning permissions in this account with no reviewable diff.
- `km doctor` (or anything in km) deleting a Wiz temporary scan volume or
  snapshot.
- A missing or half-filled `wiz:` block touching AWS at all.

## Decided by the operator

- Outpost mode; the orchestrator is already running elsewhere and other
  accounts already use it.
- Connector registration is manual, in the Wiz UI.
- Entry point: `km init --wiz --dry-run=false`, roles derived from
  `km-config.yaml`.
- The external id lives in `km-config.yaml` in plain text (Wiz treats it as a
  confused-deputy nonce, not a secret).
- One km install in this account.
- No capacity accounts.

## Calls I made (operator can override)

- **Vendored, pinned template.** `infra/modules/wiz-outpost-connector/v1.0.0/`
  carries Wiz's Outpost connector template JSON as a file, deployed through one
  `aws_cloudformation_stack` (`capabilities = ["CAPABILITY_NAMED_IAM"]`). The
  operator's other accounts reportedly use Terraform that applies the CF stack
  the same way, so this account looks like the others in Wiz. A template bump
  is a new file (immutable module version, per repo convention) and a
  reviewable diff. The starting template is Wiz's current Outpost connector
  template. **v1.0.0 ships the publicly available copy** (GitLab
  `aws-wiz-role` module, `wiz_template/wiz-aws-outpost-org.json`,
  `WizVersionLastUpdated: 2025-10-07`) — decided by the operator. The operator
  may later supply the current file from their Wiz tenant; swapping it in is a
  new immutable module version (`v1.1.0`) following the template-bump
  procedure in `docs/wiz-outpost-connector.md` (where to export it from Wiz,
  drop it in, run the contract test, plan, apply, confirm the connector stays
  healthy). That procedure is a required section of the doc.
- **Scan toggles mirror the template's defaults**: disk scanning is always on
  (the template has no disk on/off parameter; `WizScannerPolicy` is always
  attached to the scanner role), so there is no `disk` key; cloud cost, RDS logs, S3 `AWSLogs/*`, S3 KMS decrypt, serverless,
  Terraform-bucket scanning on; data, EKS, Lightsail scanning off. Each is a
  boolean in `km-config.yaml`, mapped to the template's `Enabled`/`Disabled`
  parameter strings.
- **`isOrg` is always `Disabled`**: single account, no StackSet.
- **Config shape** (one merge-list entry, `wiz`, decoded with `UnmarshalKey`):

  ```yaml
  wiz:
    outpost:
      trust_role_arn: arn:aws:iam::<wiz-dc-account>:role/<from Wiz tenant settings>
      orchestrator_account_id: "111122223333"
      external_id: "<from the Wiz connector form>"
      role_suffix: "km"                 # → WizScannerRole-km
      access_role_name: WizAccess-Role  # optional, template default
      scanning:                         # all optional; defaults as above
        data: false
        eks: false
        lightsail: false
        serverless: true
        cloud_cost: true
        rds_logs: true
        s3_aws_logs: true
        s3_kms_decrypt: true
        terraform_buckets: true
  ```

  Validation at load: `trust_role_arn` is a role ARN, `orchestrator_account_id`
  is 12 digits, `external_id` matches the template's own pattern
  `\S{8}-\S{4}-\S{4}-\S{4}-\S{12}` (UUID-shaped), `role_suffix` non-empty. Partial block ⇒ `km init` errors
  naming the missing key, before any terragrunt run.
- **Plumbing**: `km init` exports `KM_WIZ_OUTPOST_JSON` (one canonical JSON
  object) only when the block is present; the live unit
  `infra/live/<region>/wiz-outpost-connector/terragrunt.hcl` reads it with
  `get_env`. Env-vs-yaml drift WARN, as for `KM_WEBHOOK_SOURCES`. Module
  dormancy: the regional module entry is skipped entirely (no plan, no state)
  when the block is absent — not a `count = 0` resource — so a fresh install
  never creates the state key.
- **Scoped init**: `--wiz` is sugar for `--only wiz-outpost-connector`, added to
  the tier-1 allowlist (IAM-only, no destroy-class risk on create). New entry
  in `regionalModules()` — so `make build` must precede `km init`, as with
  every new module.
- **Paste-out**: after a successful apply (scoped or full), km prints

  ```
  Wiz Outpost connector — paste into the Wiz UI:
    Access role ARN   arn:aws:iam::<acct>:role/WizAccess-Role
    Scanner role ARN  arn:aws:iam::<acct>:role/WizScannerRole-km
    External ID       <external_id>
  ```

  from the stack outputs (`WizAccessRoleARNID`, `WizScannerRoleARNID`,
  `ExternalID`). A small read-only `km wiz status` reprints the same lines
  (stack status, template version, the three values).
- **`km doctor` group "Wiz Outpost connector"** (skipped with zero AWS calls
  when unconfigured): stack exists and is `*_COMPLETE`; the deployed
  `WizVersionLastUpdated` parameter equals the vendored template's; both roles
  exist and their trust principals match config (tenant role ARN,
  `WizOrchestratorNodePoolRole` in the configured orchestrator account, the
  external id).
- **Orphan sweeps never touch Wiz resources.** `checkOrphanedEBSVolumes`,
  `checkUntaggedAvailableVolumes` and `checkOrphanedSnapshots` skip any volume
  or snapshot carrying tag `wiz` with value `auto-gen-volume`,
  `auto-gen-snapshot` or `shareable-resource`. This is unconditional (no config
  dependency): a Wiz scan copying the source volume's `km:sandbox-id` tag onto
  its temporary volume must not make that volume deletable by
  `km doctor --delete-ebs`. A test pins it.
- **Teardown**: `km uninit` destroys the stack (single install owns it). It is
  not in the destroy-class protected list; the operator removes the connector
  in Wiz first (doc note).
- **Docs**: new `docs/wiz-outpost-connector.md` (setup: where each value comes
  from in the Wiz UI, the paste step, template-bump procedure, troubleshooting,
  deploy surface); a row in `docs/wiz-sensor.md` § 7's table; a CLAUDE.md
  "Where to look" row.
- **Deploy surface**: `make build` → `km init --wiz --dry-run=false` (or full
  `km init --dry-run=false`). No Lambda, sidecar, userdata, profile schema, or
  sandbox recreate.

- **Template size.** CloudFormation's inline `TemplateBody` limit is 51,200
  bytes. The reference Outpost template is 59,380 bytes pretty-printed and
  33,543 minified, so the module passes
  `template_body = jsonencode(jsondecode(file("…/wiz-outpost-connector.json")))`
  (minified). A Go test asserts the minified size stays under 51,200; if a
  future template exceeds it, the module stages it as an `aws_s3_object` in the
  artifacts bucket and switches to `template_url`. Not built until needed.
- **Template contract.** The design assumes the template defines the parameters
  `ExternalId`, `RoleARN`, `OrchestratorAccountID`, `RoleSuffix`,
  `WizRoleName`, `isOrg`, `WizVersionLastUpdated`, the scan toggles, and the
  outputs `WizAccessRoleARNID` / `WizScannerRoleARNID` / `ExternalID` — as in
  the reference copy. A Go test parses the vendored file and fails if any of
  these is missing, so a template bump that renames one breaks the build, not
  the apply. A config toggle with no matching template parameter is dropped
  from the config shape.
- **One region.** IAM role names are global, so the live unit exists only
  under `infra/live/use1/` (the install's home region and the only committed
  region directory). A `km init` for any other region hits the existing
  "directory not found → skip" path and never creates a second stack.
- **Removing the `wiz:` block does not destroy anything.** `km init` skips the
  module; `km wiz status` reports "Wiz Outpost stack exists but `wiz:` is not
  configured — remove the connector in Wiz, then `km uninit` or delete the
  stack" (not `km doctor`, which stays at zero AWS calls when unconfigured). `km uninit` destroys the stack whenever its state exists, and must
  not require `KM_WIZ_OUTPOST_JSON` to be set (the live unit tolerates an empty
  value on `destroy`). A config edit is never a destructive path.
- **Doctor reads IAM, not the stack.** The trust check calls `iam:GetRole` on
  both roles and compares the trust policy's principal and `sts:ExternalId`
  condition to `km-config.yaml`. This catches a hand-edited trust policy as
  well as a yaml edit that was never applied.
- **SCP guard.** If `{prefix}-sandbox-containment` is attached to the
  application account (read through `{prefix}-org-admin`, as the existing
  "SCP content" doctor check does), the Wiz group WARNs that the scanner and
  access roles are denied snapshot actions, other-region reads and
  organizations discovery. No SCP exemption is built.

## Left to the builder

- Exact Go type names, where the config struct lives, test layout.
- How the template's `Enabled`/`Disabled` parameters are assembled in HCL vs Go
  (prefer HCL `jsondecode` of the env JSON → `parameters` map).
- Whether `km wiz status` is its own command file or a subcommand group ready
  for later verbs.

## Builder notes / known edges

- The apply runs under the operator's application-account SSO role
  (`klanker-terraform`); it needs `cloudformation:*` on the stack and IAM
  create/attach for `WizAccess-Role` / `WizScannerRole-*`. Verify against
  `infra/modules/km-operator-policy` if the operator role is not admin.
- If the km SCP is ever present in a connected account, `km doctor` should
  WARN that the scanner role is denied snapshot actions; the narrow fix is a
  Wiz-specific exemption on `ec2:CreateSnapshot`/`CopySnapshot`/
  `ModifySnapshotAttribute` and tag-conditioned `DeleteSnapshot`, NOT
  `scp.trusted_roles` (that also exempts instance mutation and the region
  lock). Not built now; a doctor WARN is cheap and is in scope.
- Wiz's scanner will report access errors in other regions if the account is
  region-restricted elsewhere; expected, not a km bug.
- Short-lived sandboxes usually finish within one scan cycle; the value is
  coverage of the platform's own resources (roles, Lambdas, buckets, tables)
  plus long-lived sandboxes, not per-sandbox disk findings on ephemeral boxes.

## Verification (live)

1. `km init --wiz --dry-run` on an install with no `wiz:` block: no module
   listed, no AWS call.
2. With the block: plan shows one `aws_cloudformation_stack` create; apply;
   the three values print; both roles exist with the expected trust policies.
3. Paste into the Wiz UI; connector reaches healthy; account inventory appears.
4. A disk scan of a long-lived sandbox completes; Wiz's temp volume/snapshot
   appear tagged `wiz=auto-gen-*`; `km doctor` does not list them as orphans.
5. `km doctor` group green; edit `external_id` in yaml without applying →
   doctor flags the trust mismatch.
6. `km uninit` removes the stack.
