# Phase 82: Multi-instance resource_prefix isolation — Context

**Gathered:** 2026-05-16
**Status:** Ready for planning
**Source:** PRD Express Path (`docs/superpowers/specs/2026-05-16-multi-instance-resource-prefix-isolation-design.md`)

<domain>
## Phase Boundary

Close the gap between `CLAUDE.md`'s "multiple km installs per AWS account via `resource_prefix`" promise and reality.

**In scope (what this phase delivers):**

1. **Fix the three hard infrastructure blockers** that prevent a second install from running `km init` cleanly:
   - SES receipt rule set name (`infra/modules/ses/v1.0.0/main.tf:62`) is hardcoded `"km-sandbox-email"`.
   - Email-handler IAM policy (`infra/modules/email-handler/v1.0.0/main.tf:75`) hardcodes the S3 state-bucket prefix `tf-km/`.
   - Three ECS modules (`ecs-task/v1.0.0/main.tf:156`, `ecs/v1.0.0/main.tf:126`, `ecs-cluster/v1.0.0/main.tf:132`) hardcode `parameter/km/*` in SSM IAM ARNs.

2. **Close the doctor cross-install destruction risk** by tagging every per-sandbox + per-AMI resource with `km:resource-prefix = ${prefix}` and adding tag-filters to `ListBakedAMIs` + `checkOrphanedEC2`. Resources lacking the tag get `WARN`, never delete.

3. **Ship `km doctor --backfill-tags`** to retrofit the new `km:resource-prefix` tag onto pre-Phase-82 resources.

4. **Close the configure-flow footgun** so `km configure` re-run preserves an existing non-default `resource_prefix` instead of silently re-defaulting to `"km"`.

5. **Convert silent `km-…` fallbacks to prefix-aware or hard failures** in four call sites: `cmd/configui/main.go:225`, `cmd/km-slack-bridge/main.go:175`, `pkg/compiler/userdata.go:3315/3331/3346`.

**Out of scope (explicit non-goals):**

- Region-level isolation (two installs in different regions already work — no changes needed).
- Account-level isolation (separate AWS account is the gold standard — already works).
- VPC sharing between installs (per-sandbox VPCs make this a non-issue).
- Slack app-level isolation (one Slack app per AWS account is fine; bot-token SSM path already prefix-scoped).
- Cross-install migration tooling (no need to convert an existing `km` install to a non-default prefix; the goal is to enable a *second* install alongside the existing `km` one).
- Empirical verification of SES rename behavior in production — that's a pre-merge spike on a throwaway account, NOT a deliverable.

**Phase boundary precedent:** Phase 66 introduced `resource_prefix` and `email_subdomain` as schema fields and threaded them through most surfaces. Phase 82 is the *enforcement* companion: closes the remaining 15% gap (3 infra blockers + 2 doctor holes + 1 configure footgun + 4 silent fallbacks).

</domain>

<decisions>
## Implementation Decisions

### Severity-driven slicing

**Locked:** The fix splits into three rollout waves matching the design spec's bucket structure:
- **Wave 1 — Go-only (no Terraform apply):** Close C1 (configure footgun), F1 (silent fallbacks), the Go side of Bucket 1 (`pkg/aws/ec2_ami.go` adds `km:resource-prefix` tag at bake; doctor filters use the tag; ship `km doctor --backfill-tags`). Builds via `make build`; no `km init` required.
- **Wave 2 — Terraform module additions (`km init --dry-run=true` to review plan):** B1 (SES module + rule-set rename), B2 (email-handler `state_prefix` variable), B3 (ECS modules SSM ARN), plus tag additions to every sandbox-creating module. Backward-compatible defaults so existing `km` install sees zero diff except SES rename + new tags.
- **Wave 3 — Apply Wave 2 (`km init --dry-run=false`):** Tags appear via in-place updates. SES rule set briefly recreates (~10s inbound-mail gap unless Terraform `moved {}` blocks apply cleanly — see open question Q1).

### Tag-based discrimination

**Locked:** Use `km:resource-prefix = ${var.resource_prefix}` as the install-discriminator tag.

**Locked:** Resources lacking the tag get `WARN` in doctor (not `ERROR`, not silent-delete), with remediation pointer to `km doctor --backfill-tags`.

**Locked:** This is the *install* discriminator and is complementary to the *platform-vs-sandbox* discriminator (`km:role-class`) proposed in `docs/superpowers/specs/2026-05-09-doctor-tag-based-platform-discrimination.md` — both tags coexist on the same resources. The two specs' backfill commands SHOULD be unified into a single `km doctor --backfill-tags` if both ship in the same window — see open question Q2.

### SES rule-set rename strategy

**Locked:** SES module gains a `resource_prefix` variable; the literal `"km-sandbox-email"` becomes `"${var.resource_prefix}-sandbox-email"`.

**Open (Q1):** Whether `moved {}` Terraform blocks apply cleanly to `aws_ses_receipt_rule_set` (name-keyed resource — should work, but unverified). If yes: zero-downtime rename via state move. If no: `create_before_destroy` two-step (pre-create new rule set + flip activation). Spike outcome determines plan task structure.

### Email-handler state-prefix variable

**Locked:** Add `variable "state_prefix"` to `infra/modules/email-handler/v1.0.0/` (default `"tf-km"` for backward compat). Caller passes `state_prefix = "tf-${var.resource_prefix}"` from `infra/live/use1/email-handler/terragrunt.hcl`.

### ECS modules prefix variable

**Locked:** Add `variable "resource_prefix"` to all three ECS modules. Plumb from `infra/templates/sandbox/terragrunt.hcl`. Three literals (`ecs-task:156`, `ecs:126`, `ecs-cluster:132`) become `parameter/${var.resource_prefix}/*`.

### Configure-flow protection

**Locked:** `internal/app/cmd/configure.go:140, 178, 185` — when an existing `km-config.yaml` is found with a non-default `resource_prefix`, **preserve** it. Changing it requires an explicit `--reset-prefix` flag (or interactive prompt that requires typing the existing prefix to confirm). Fresh `km configure` (no existing config) still defaults to `"km"`.

**Locked:** Add a unit test in `internal/app/cmd/configure_test.go` covering: existing `rg` config + re-run without `--reset-prefix` → preserves `rg`.

### Silent fallback hardening

**Locked:** For each of the four `km-…` fallbacks:
- `cmd/configui/main.go:225` — log + `os.Exit(1)` when `KM_BUDGET_TABLE` unset (configui runs only as a sidecar Lambda — missing env = configuration bug).
- `cmd/km-slack-bridge/main.go:175` — same treatment for `KM_SLACK_THREADS_TABLE`.
- `pkg/compiler/userdata.go:3315, 3331, 3346` — replace literals with calls to existing prefix-aware helpers (`cfg.GetSlackThreadsTableName()`, `cfg.GetSlackStreamMessagesTableName()`). These are Go-side template-generation paths; the helpers already exist in `internal/app/config/config.go`.

### Rollout pattern alignment

**Locked:** Follow the established sidecar-rollout cadence: `make build && km init --sidecars && km init --dry-run=false`. Matches Phase 63 / 67 / 68 / 73 / 79 / 80 deploy pattern. Existing sandboxes do NOT get retroactive tagging — `km doctor --backfill-tags` handles infra-level tags only; sandbox-instance tags ride on the next `km destroy && km create`.

### Confidence stance

**Locked:** Code-inspection-only confidence is high enough to ship Wave 1 + Wave 2 plan creation without a live AWS experiment. The one piece worth empirically verifying before flipping prod is **SES rule-set rename behavior** (Q1) — a pre-merge spike on a throwaway AWS account.

### Documentation updates

**Locked:** Update three files when planning lands:
- `CLAUDE.md` § Multi-instance support — describe new prerequisites (run `--backfill-tags` once after upgrade) and link to spec.
- `OPERATOR-GUIDE.md` — same.
- The spec itself (`docs/superpowers/specs/2026-05-16-multi-instance-resource-prefix-isolation-design.md`) — mark `Status:` from `Proposal` to `Approved — implementing in Phase 82` once plans are verified.

### Claude's Discretion

These are details the spec doesn't lock down — the planner can choose:

- **Exact PLAN.md decomposition.** Spec suggests 3 waves; planner may split Wave 1 into separate plans per concern (configure fix, fallback hardening, AMI tag + doctor filter, backfill-tags command) or bundle them. Aim for ~15-20 file-touch budget per the spec's estimate.
- **`--backfill-tags` UX shape.** Subcommand under `km doctor`, separate `km tags backfill`, or hidden helper? Spec recommends `km doctor --backfill-tags` for cohesion with the 2026-05-09 platform-discrimination spec; planner can confirm.
- **Test scaffolding.** `configure_test.go`, `doctor_test.go`, `ec2_ami_test.go` are named in the spec. Planner decides exact test cases per file beyond the one spec-named case (configure re-run preserves prefix).
- **Whether to fold open-question resolutions into this phase or follow-ups.** Q3 (`multi_install_collision` doctor check), Q4 (Phase 80 cluster-role name prefix), and Q5 (state-bucket layout verification) are optional. Recommended: Q4 is a one-line fix worth folding in; Q3 + Q5 are deferrable.

</decisions>

<specifics>
## Specific Ideas

### File-level inventory (from spec § File-touch estimate)

**Terraform modules touched:**
- `infra/modules/ses/v1.0.0/main.tf` (+ `variables.tf`) — B1 fix.
- `infra/modules/email-handler/v1.0.0/main.tf` (+ `variables.tf`) — B2 fix.
- `infra/modules/ecs-task/v1.0.0/main.tf` (+ `variables.tf`) — B3 fix.
- `infra/modules/ecs/v1.0.0/main.tf` (+ `variables.tf`) — B3 fix.
- `infra/modules/ecs-cluster/v1.0.0/main.tf` (+ `variables.tf`) — B3 fix.
- Sandbox-creating modules (tag additions): `infra/modules/ec2spot/v1.0.0/main.tf`, plus ECS substrate equivalents. Roughly ~6 modules need `Tags = { "km:resource-prefix" = var.resource_prefix }` plumbed.

**Live terragrunt files:**
- `infra/live/use1/ses/terragrunt.hcl` (or wherever SES is instantiated — verify path during planning).
- `infra/live/use1/email-handler/terragrunt.hcl` — pass `state_prefix`.
- `infra/templates/sandbox/terragrunt.hcl` — pass `resource_prefix` to ECS modules.

**Go code:**
- `pkg/aws/ec2_ami.go` — add `km:resource-prefix` tag at bake (`CreateImage` + `CreateTags`); add tag filter to `ListBakedAMIs` (line 184) once filter is configurable.
- `internal/app/cmd/doctor.go` — tag filter in `checkOrphanedEC2` (line 1679).
- `internal/app/cmd/configure.go` — preserve-on-re-run logic (lines 140, 178, 185).
- `cmd/configui/main.go` — hard-fail at line 225.
- `cmd/km-slack-bridge/main.go` — hard-fail at line 175.
- `pkg/compiler/userdata.go` — replace literals at lines 3315, 3331, 3346 with helper calls.
- **New file:** `internal/app/cmd/doctor_backfill_tags.go` — implements `km doctor --backfill-tags`.

**Tests:**
- `internal/app/cmd/configure_test.go` — re-run-preserves-prefix case.
- `internal/app/cmd/doctor_test.go` — tag-filter cases for cross-install isolation (foreign AMIs/instances NOT flagged as orphans).
- `pkg/aws/ec2_ami_test.go` — bake-time tag assertion.

**Docs:**
- `CLAUDE.md` § Multi-instance support — new prerequisites + `--backfill-tags` step.
- `OPERATOR-GUIDE.md` — same.
- The spec file — flip status to Approved.

### Validation hooks

- After Wave 1 lands: `make build && go test ./...` — unit tests pass for configure preserve, fallback hard-fails, bake-tag assertion.
- After Wave 2 lands: `km init --dry-run=true` against the current `km` install shows zero resource recreations except SES rule-set rename + tag additions (in-place).
- After Wave 3 lands: `aws ses describe-active-receipt-rule-set` returns the prefix-renamed rule set; `km doctor --backfill-tags` is idempotent on second run; AWS Resource Groups Tagging API confirms `km:resource-prefix=km` on existing infra.
- Manual: spin up a second AWS profile pointing at a different working directory, `km configure` with `resource_prefix: rg`, `km bootstrap && km init` succeeds without resource-name collision. (Optional spike — out of scope for phase deliverable.)

### Related work in flight

- `docs/superpowers/specs/2026-05-09-doctor-tag-based-platform-discrimination.md` — platform-vs-sandbox tag (`km:role-class`). If both ship in the same window, unify the `--backfill-tags` command.
- Phase 66 — original `resource_prefix` introduction. Phase 82 is the enforcement companion.
- Phase 80 — cluster-irsa naming (`km-cluster-<name>`) is a one-line prefix fix candidate for inclusion (Q4).

</specifics>

<deferred>
## Deferred Ideas

The spec's § Open questions section enumerates five items. Resolutions:

- **Q1: SES `moved {}` block behavior.** NOT deferred — must be resolved during planning (or as a Wave 2 prerequisite spike). Determines whether rename is zero-downtime or has a sub-10-second inbound-mail gap. Recommend a one-time `terragrunt plan` on a throwaway account before merging Wave 2.
- **Q2: `--backfill-tags` unification with the 2026-05-09 platform-discrimination spec.** Treat as a planning-time decision. If the 2026-05-09 work is also in flight, unify. If not, this phase ships `--backfill-tags` independently and the future phase extends it.
- **Q3: Optional positive `multi_install_collision` doctor check (INFO-level "foreign install detected").** **Deferred to a follow-up phase.** Nice-to-have, not blocking the multi-install promise.
- **Q4: Phase 80 cluster-role name prefixing (`km-cluster-<name>` → `${prefix}-cluster-<name>`).** **Recommended to fold into this phase** — one-line module change, high collision-risk if a `kph` and `rg` operator both run `km cluster add --name dev-use1-0`. Planner can confirm or defer based on file-touch budget.
- **Q5: State-bucket layout verification (`tf-${prefix}-state-${region}` already templated for new installs per `infra/live/site.hcl:43`).** **Verification only, not a code change.** Add a planning task to confirm the template holds in practice during Wave 2 plan-review.

**Out of scope for Phase 82 entirely (deferred to future phases):**
- Cross-install migration tooling (re-prefixing an existing install).
- Per-install Slack workspace (current model is one bot per AWS account; multi-install can share one Slack app).
- Cross-install observability dashboard.

</deferred>

---

*Phase: 82-multi-instance-resource-prefix-isolation*
*Context gathered: 2026-05-16 via PRD Express Path*
