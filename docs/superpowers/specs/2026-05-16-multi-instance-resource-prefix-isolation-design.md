# Multi-instance `resource_prefix` isolation — design note

**Status:** Approved — implementing in Phase 82 (shipped 2026-05-16).
**Author:** code-inspection audit (no live AWS testing — see § Confidence).
**Date:** 2026-05-16.

## Problem

`CLAUDE.md` and `OPERATOR-GUIDE.md § Multi-instance support` advertise that two parallel installs (e.g. `kph` and `rg`) can coexist in one AWS account by setting different `resource_prefix` values in each install's `km-config.yaml`. A code-inspection audit of every operator command, every Terraform module, every sidecar binary, and the userdata template found this promise is **~85% true and 15% broken**, with three hard infrastructure blockers, two cross-install destruction holes in `km doctor`, and one configure-flow footgun. The broken 15% is bad enough to make the experiment unsafe to attempt today: the second `km init` will fail or silently overwrite the first install's SES rule set, and the first install's `km doctor` run will see the second install's sandboxes as orphans eligible for deletion.

The audit is recorded in this doc so the remediation can be sliced into a single GSD phase with concrete file-touch counts.

## What already works (✅ ~85%)

For context — this is the surface that does NOT need to change:

| Surface | Evidence |
|---|---|
| `km list`, `km destroy`, `km info`, `km email`, `km cluster`, `km otel` | All resolve through `cfg.GetSandboxTableName()`, `cfg.GetSsmPrefix()`, etc.; never scan account-wide. |
| `km at list/cancel` | EventBridge group + schedule names are `${prefix}-at-…`. |
| `km doctor` schedule/Lambda/KMS/IAM/SSM stale checks | All filter `ListSchedules` / `ListFunctions` / SSM `GetParametersByPath` by `prefix + "-"` or `/${prefix}/…`. |
| DynamoDB tables (sandboxes, budgets, identities, schedules, slack-threads, slack-stream-messages, nonces) | `${var.resource_prefix}-*` everywhere. |
| Lambda functions + their IAM roles + their CloudWatch log groups | Same. |
| S3 artifacts/state buckets | `KM_ARTIFACTS_BUCKET` / `tf-${KM_RESOURCE_PREFIX}-state-…` flow from env vars. |
| Per-sandbox SQS FIFO queues (Slack inbound) | `${var.resource_prefix}-slack-inbound-${sandbox_id}.fifo`. |
| EventBridge schedules (TTL, budget, at-jobs) | `${var.resource_prefix}-…`. |
| Operator-policy SSM paths via `km-operator-policy/v1.0.0/` | `/${prefix}/…`. |
| Userdata env-var injection into sandboxes | All `KM_*` env vars are prefix-derived. |
| Sandbox VPC / SG / EBS / EFS | Per-sandbox UUID, no install-level shared name. |

## What is broken — by severity

### 🔴 Hard infrastructure blockers (second `km init` will fail or silently corrupt the first install)

**B1. SES receipt rule set name is a literal `"km-sandbox-email"`.**

- `infra/modules/ses/v1.0.0/main.tf:62` — `rule_set_name = "km-sandbox-email"`
- `infra/modules/ses/v1.0.0/main.tf:66` — `aws_ses_active_receipt_rule_set` references the same literal.
- The module takes no `resource_prefix` variable at all.

SES allows **exactly one active rule set per account per region**. On the second install's `km init`, either (a) Terraform errors when creating an `aws_ses_receipt_rule_set` with a name that already exists, or (b) if the name is differentiated by hand, the `aws_ses_active_receipt_rule_set` activate-call wins/loses non-deterministically and the loser's inbound email + Slack-via-email paths break silently.

**B2. Email-handler IAM policy hardcodes the state-bucket S3 prefix `tf-km/`.**

- `infra/modules/email-handler/v1.0.0/main.tf:75` — `Resource = "arn:aws:s3:::${var.state_bucket}/tf-km/sandboxes/*/metadata.json"`

On an `rg` install whose state-bucket layout is `tf-rg/sandboxes/…`, this IAM policy points at the wrong key prefix. The email-handler Lambda's `s3:GetObject` denies at runtime → inbound mail can't fetch sandbox metadata → routing breaks.

**B3. ECS substrate IAM policies hardcode `parameter/km/*`.**

- `infra/modules/ecs-task/v1.0.0/main.tf:156`
- `infra/modules/ecs/v1.0.0/main.tf:126`
- `infra/modules/ecs-cluster/v1.0.0/main.tf:132`

All three: `Resource = "arn:aws:ssm:…:parameter/km/*"` literal. Only matters if the install uses the ECS substrate (default is EC2-spot), but for ECS users on a non-default prefix the sandbox can't read its own SSM parameters.

### 🟠 Cross-install destruction risk in `km doctor`

**D1. Sandbox / AMI tags are not prefix-scoped.**

Across the codebase every per-sandbox resource is tagged with `km:sandbox-id`, `km:profile`, etc. — never `km:resource-prefix` or `km:install`. Concrete consequences:

- `pkg/aws/ec2_ami.go:184` — `ListBakedAMIs` filters `tag:km:sandbox-id=*`, returning AMIs from **both** installs.
- `internal/app/cmd/doctor.go:1679` — orphaned-EC2 check filters `tag:km:sandbox-id=*`, then declares any instance whose ID isn't in **this install's** prefix-scoped DDB to be orphaned.
- `internal/app/cmd/doctor.go:2740` has a *comment* referring to a future `km:resource-prefix` tag — never implemented.

A `kph` operator running `km doctor` sees every `rg` sandbox + AMI as orphaned/stale. The stale-AMI cleanup, orphaned-EBS cleanup, and orphaned-instance cleanup paths are precisely what the user is trying to avoid having ever fire across installs.

This is also the dual of the problem solved by [2026-05-09-doctor-tag-based-platform-discrimination.md](2026-05-09-doctor-tag-based-platform-discrimination.md): that doc fixed *platform* discrimination via `km:role-class = platform | sandbox`; this doc adds the *install* discriminator via `km:resource-prefix = ${prefix}`.

### 🟡 Configure-flow footgun

**C1. `km configure` silently re-defaults `resource_prefix` to `"km"`.**

- `internal/app/cmd/configure.go:140, 178, 185` — three paths each unconditionally `resourcePrefix = "km"` when the operator doesn't explicitly pass `--resource-prefix` (or the env var, or the interactive prompt input is empty).

An `rg` operator who re-runs `km configure` for any reason (e.g. rotating `operator_email`) and accepts the default at the prompt will rewrite `km-config.yaml` with `resource_prefix: km`, and the install starts pointing at the wrong install's tables on the next CLI invocation.

### 🟡 Silent-misroute fallbacks (defense-in-depth that's broken)

**F1. Four `km-…` literal fallbacks in sidecar binaries and userdata template.**

These fire only when the env var is missing. If env injection is correct they never fire — but if a future regression skips an env, the install silently cross-routes to the wrong install's DDB rows instead of erroring out:

| File | Line | Fallback |
|---|---|---|
| `cmd/configui/main.go` | 225 | `"km-budgets"` |
| `cmd/km-slack-bridge/main.go` | 175 | `"km-slack-threads"` |
| `pkg/compiler/userdata.go` | 3315, 3331 | `"km-slack-threads"` |
| `pkg/compiler/userdata.go` | 3346 | `"km-slack-stream-messages"` |

Should be either prefix-aware (call `cfg.GetSlackThreadsTableName()` etc. where a config is available) or hard-fail (log + `os.Exit(1)`) where it isn't.

## Proposed approach

The fixes split cleanly into four buckets. Each is independently shippable; the cross-install destruction risk (D1) is the most important to land first because it removes the largest blast-radius failure mode.

### Bucket 1 — Tag every install-owned resource with `km:resource-prefix` (fixes D1)

- Add `Tags = { "km:resource-prefix" = var.resource_prefix }` to every sandbox-creating module: `infra/modules/ec2spot/v1.0.0/main.tf`, the ECS modules, `infra/modules/ecs-spot-handler/v1.0.0/main.tf`, anywhere `aws_security_group`/`aws_iam_role`/`aws_instance` is created for a sandbox.
- Add the same tag in `pkg/aws/ec2_ami.go` (bake-path `CreateImage` + `CreateTags`).
- Doctor: add a tag filter to `ListBakedAMIs` (`pkg/aws/ec2_ami.go:184`) and `checkOrphanedEC2` (`internal/app/cmd/doctor.go:1679`): `tag:km:resource-prefix = ${cfg.GetResourcePrefix()}`. Resources lacking the tag get `WARN` ("untagged — run `km doctor --backfill-tags`"), never delete.
- One-shot `km doctor --backfill-tags` (already a pattern proposed for the platform-discrimination work; reuse the scaffolding) tags existing resources whose sandbox-id matches this install's DDB.
- Parallels the platform/sandbox tag work in [2026-05-09-doctor-tag-based-platform-discrimination.md](2026-05-09-doctor-tag-based-platform-discrimination.md): if both ship in the same window, fold the backfill into a single command.

### Bucket 2 — Make the three blocker modules prefix-aware (fixes B1, B2, B3)

**B1 — SES module:**

- Add `variable "resource_prefix"` to `infra/modules/ses/v1.0.0/variables.tf`.
- Replace the literal in `main.tf:62` with `rule_set_name = "${var.resource_prefix}-sandbox-email"`.
- Plumb the variable through `infra/live/use1/ses/terragrunt.hcl` (or wherever the SES module is instantiated).
- One-shot migration on existing `km` installs: Terraform will want to recreate the rule set under the new name. `aws_ses_active_receipt_rule_set` will flip atomically. There is a brief window (seconds) during which neither rule set is active and inbound mail is rejected — schedule the apply during a quiet window or pre-create the new rule set + flip activation as a two-step.

**B2 — Email-handler module:**

- Add `variable "state_prefix"` to `infra/modules/email-handler/v1.0.0/variables.tf` (default `"tf-km"` for backward compat, set explicitly by callers).
- Replace `main.tf:75` literal with `"arn:aws:s3:::${var.state_bucket}/${var.state_prefix}/sandboxes/*/metadata.json"`.
- Pass `state_prefix = "tf-${var.resource_prefix}"` from the live config in `infra/live/use1/email-handler/terragrunt.hcl`.

**B3 — ECS modules:**

- Add `variable "resource_prefix"` to each of `infra/modules/ecs-task/v1.0.0/`, `infra/modules/ecs/v1.0.0/`, `infra/modules/ecs-cluster/v1.0.0/`.
- Replace the three `parameter/km/*` literals with `parameter/${var.resource_prefix}/*`.
- Plumb the variable from the sandbox terragrunt template (`infra/templates/sandbox/terragrunt.hcl`) where ECS-substrate sandboxes are instantiated.

### Bucket 3 — Close the configure-flow footgun (fixes C1)

- Replace the three `resourcePrefix = "km"` re-defaults in `internal/app/cmd/configure.go:140, 178, 185` with: if an existing `km-config.yaml` is found, **preserve** its `resource_prefix` and **require** an explicit `--reset-prefix` flag (or interactive confirmation by typing the existing prefix) to change it.
- A fresh `km configure` on a directory with no existing config still defaults to `"km"` — that's the new-user path and matches the documented default.
- Add a unit test in `internal/app/cmd/configure_test.go` that covers: existing `rg` config + re-run without `--reset-prefix` → preserves `rg`.

### Bucket 4 — Convert silent fallbacks to hard failures (fixes F1)

- `cmd/configui/main.go:225` — when `KM_BUDGET_TABLE` is unset, log error and exit 1 (configui only runs as a sidecar Lambda that always has the env injected; missing env = configuration bug).
- `cmd/km-slack-bridge/main.go:175` — same treatment for `KM_SLACK_THREADS_TABLE`.
- `pkg/compiler/userdata.go:3315, 3331, 3346` — these are template-generation paths in the operator's Go code, not runtime in the sandbox. Replace the `"km-slack-threads"` / `"km-slack-stream-messages"` literals with calls to `cfg.GetSlackThreadsTableName()` / `cfg.GetSlackStreamMessagesTableName()` (already prefix-aware helpers; see `internal/app/config/config.go`).

## Rollout / migration playbook

The four buckets ship as one phase (small enough — see file-touch estimate below) with three internal waves. The user has approval to stage these however they want; this is one suggestion.

**Wave 1 — pure Go-side changes (no Terraform apply needed):**
- Bucket 3 (configure footgun)
- Bucket 4 (fallback hard-fails)
- Bucket 1 *Go-side only* (`pkg/aws/ec2_ami.go` adds tag at bake, doctor filters use tag, `--backfill-tags` command).

`make build`. No `km init` needed. The doctor backfill is the first manual step the operator runs.

**Wave 2 — Terraform module additions, NO state changes yet:**
- Bucket 1 Terraform side (add the `km:resource-prefix` tag to every sandbox-creating module, default to `var.resource_prefix`).
- Bucket 2 (all three blocker modules become prefix-aware, with backward-compatible defaults so existing `km` installs see no plan diff except the SES rule-set rename).

`km init --sidecars && km init --dry-run=true` — review plan. Should be: zero diff for existing `km` install except SES rule-set rename + new tags appearing on resources.

**Wave 3 — apply Wave 2:**

`km init --dry-run=false`. Tags appear on existing infra (in-place updates, no resource recreation). SES rule set briefly recreates (~10s inbound-mail gap). Operators can now run a *second* install in the same account by pointing a separate working directory at a `km-config.yaml` with a different `resource_prefix` and re-running `km configure` + `km bootstrap` + `km init`.

## Open questions

1. **SES rule-set rename — is there a way to avoid the inbound-mail outage window?** The cleanest path is `aws_ses_active_receipt_rule_set` with `create_before_destroy` + a manual flip step. Worth checking whether Terraform 1.x's `moved {}` block applies to SES (it might — SES rule sets are name-keyed, so a `moved` block from `aws_ses_receipt_rule_set.km_sandbox` to `aws_ses_receipt_rule_set.kph_sandbox` should be a state-move, not a recreate). If it works, the outage window goes to zero.
2. **`--backfill-tags` scope.** Does this share scaffolding with the analogous proposal in [2026-05-09-doctor-tag-based-platform-discrimination.md](2026-05-09-doctor-tag-based-platform-discrimination.md)? If yes, that doc's command applies `km:role-class` and this doc's adds `km:resource-prefix`. Unify into one `km doctor --backfill-tags` command that does both?
3. **Should `km doctor` *positively assert* prefix-isolation?** A new check `multi_install_collision` that scans for any AWS resource with a `km:`-namespace tag whose `km:resource-prefix` doesn't match the local install — and reports it as INFO ("foreign install detected: prefix=rg, count=12"). Not a WARN — coexistence is the new feature. But surfacing it would let operators *see* the other install exists without having to look at AWS console.
4. **Cluster-irsa (Phase 80) cross-install behavior.** OIDC providers are account-global; the audit shows Phase 80 already handles this correctly (auto-detect + reuse). But what about the per-cluster IAM role names (`km-cluster-<name>`)? Two installs running `km cluster add --name dev-use1-0` would collide. Worth one-line-fixing in this phase: prefix the role name with `${var.resource_prefix}-cluster-<name>`.
5. **State-bucket layout.** The current `tf-km-state-use1` bucket layout is regional, not per-install. Confirm `tf-${prefix}-state-${region}` (already templated for new installs per `infra/live/site.hcl:43`) holds in practice — should be fine, but worth verifying once during plan review.

## File-touch estimate

If approved as a single phase, the patch surface is:

- **Terraform modules:** 4 (ses, email-handler, ecs-task, ecs, ecs-cluster) + tag additions across ~6 sandbox-creating modules.
- **Live terragrunt:** 2–3 (`infra/live/use1/ses/`, `…/email-handler/`, sandbox template).
- **Go code:** `pkg/aws/ec2_ami.go` (tag at bake + ListBakedAMIs filter), `internal/app/cmd/doctor.go` (orphan-EC2 filter), `internal/app/cmd/configure.go` (preserve-on-re-run), 4 hardcode replacements in `cmd/configui/main.go`, `cmd/km-slack-bridge/main.go`, `pkg/compiler/userdata.go`. New file: `internal/app/cmd/doctor_backfill_tags.go`.
- **Tests:** `configure_test.go` for the preserve-on-re-run case; `doctor_test.go` for the tag-filter cases; `ec2_ami_test.go` for the bake-time tag.
- **Docs:** Update `CLAUDE.md § Multi-instance support` and `OPERATOR-GUIDE.md` to describe the new prerequisites + the `--backfill-tags` flow.

Roughly 15–20 file touches. Comparable in size to Phase 80 (cluster-irsa).

## Confidence

This audit is **code-inspection only**. No second AWS account was provisioned to verify failures live. Confidence is high for B1, B2, B3, C1, F1 because they're deterministic from the source (the literal strings are in the patches above). D1 is high because the tag-filter call sites are explicit in `pkg/aws/ec2_ami.go` and `internal/app/cmd/doctor.go`. The one piece worth verifying empirically before merging the fix is **the SES rule-set rename's behavior** (does `moved {}` apply? what's the actual outage window?) — recommend a one-time dry-run on a throwaway account before flipping the prod install.

## Out of scope for this design note

- Region-level isolation (two installs in *different* regions in the same account work today — no changes needed).
- Account-level isolation (already works trivially — separate AWS account is the gold standard).
- VPC sharing between installs (per-sandbox VPCs make this a non-issue).
- Slack-app-level isolation (one Slack app per AWS account is fine; the bot-token SSM path is already `/${prefix}/slack/bot-token`, so two installs can have separate tokens).

## Decision needed

Operator approval to scope this as a single GSD phase (probable name: `multi-instance-resource-prefix-isolation-fix-ses-email-handler-ecs-prefix-leaks-and-add-km-resource-prefix-tag-for-doctor-cross-install-safety`) via `/gsd:add-phase`. As written this is **one phase, three waves, ~20 file touches**, with one mandatory operator soak (Wave 2 plan review before Wave 3 apply) and one optional empirical verification (SES rename behavior on a throwaway account).

## Implementation outcome

Phase 82 shipped on 2026-05-16. All 10 plans executed across three waves.

### Plans shipped

| Plan | Name | Key change |
|------|------|-----------|
| 82-01 | configure preserve-on-rerun | `km configure` no longer overwrites `resource_prefix`; `--reset-prefix` flag added |
| 82-02 | userdata KM_RESOURCE_PREFIX injection | `KM_RESOURCE_PREFIX` injected into sandbox env at `km create` time |
| 82-03 | ec2/ami tag-at-bake and ListBakedAMIs filter | `km:resource-prefix` applied at AMI bake; `km ami list` and doctor orphan check filter by prefix |
| 82-04 | doctor cross-install safety | `km doctor` orphan-EC2 and stale-AMI checks guard against cross-install false positives |
| 82-05 | doctor --backfill-tags | New `km doctor --backfill-tags` command for one-time retro-tag sweep |
| 82-06 | SES module resource_prefix variable | B1 resolved: rule-set name is `${resource_prefix}-sandbox-email` |
| 82-07 | email-handler state_prefix variable | B2 resolved: IAM ARNs and S3 paths interpolate prefix |
| 82-08 | ECS modules km_label variable | B3 resolved: SSM parameter ARN interpolates prefix in ecs-task, ecs, ecs-cluster |
| 82-09 | km:resource-prefix tag emission | `km:resource-prefix` tag added to every tag map across 6 Terraform modules |
| 82-10 | Operational deployment + docs | Wave 3 applied; backfill run; CLAUDE.md + OPERATOR-GUIDE.md updated |

### Deviations

**SES tags bug (Rule 1 — Bug):** Plan 82-09 mistakenly added a `tags = {}` block to `aws_ses_receipt_rule_set.km_sandbox`. The AWS provider does not support `tags` on this resource type. Discovered during `terraform validate` at plan 82-10 Task 2. Fixed at commit `ec6b4cd` (removed the block). No operator action required.

### Verification results (2026-05-16)

- `km init --dry-run=false`: all 16 modules applied cleanly; zero `must be replaced` lines.
- SES active rule set post-apply: `km-sandbox-email` (unchanged — existing prefix `km` evaluates to same string).
- `km doctor --backfill-tags --dry-run=false` first run: `Tagged: 4`, `Errored: 0`.
- Second run: `Tagged: 0`, `SkippedAlreadyTagged: 4` — idempotent.
- Resource Groups Tagging API post-backfill: 6 resources tagged `km:resource-prefix=km`.
- `km-slack-bridge` Lambda env vars: `KM_RESOURCE_PREFIX=km` present alongside all other expected vars.

### Note on SES rule-set rename

The RESEARCH.md concern ("what's the actual outage window for the SES rename?") did not materialize in practice. The existing `km` install evaluates `${var.resource_prefix}-sandbox-email` to `km-sandbox-email` — identical to the previous hard-coded literal. Terraform's plan showed a zero-diff on the SES resources. The rename only produces a diff for a genuinely *new* prefix (e.g. `km2`), which is the expected behavior.
