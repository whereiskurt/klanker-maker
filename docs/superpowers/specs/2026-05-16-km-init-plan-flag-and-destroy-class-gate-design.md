# `km init --plan` flag with destroy-class gate — design note

**Status:** Approved — slotted as Phase 84.2 (after 84.1 completes).
**Date:** 2026-05-16.
**Author:** Brainstormed during Phase 84.1 execution, after Phase 84 UAT diagnosed 8 gaps that all shared one root cause: no operator-visible plan-before-apply step on `km init` or `km bootstrap`.

## Problem

`km init --dry-run=true` is a documentation feature, not a plan feature. `internal/app/cmd/init.go:276` (`runInitDryRun`) prints a static numbered list of steps (DNS, build, upload, "apply regional modules in order") and the regional module ordering with env-var-derived `[skip]` annotations. It never invokes `terragrunt plan`, never reads terraform state, never touches AWS.

This is what caused the Phase 84 incident. Per `84-10-UAT.md`, the operator running the upgrade had no way to see — before apply — that the regional `ses/v1.0.0` → `v2.0.0` cutover would destroy the `aws_ses_domain_identity`, `aws_ses_active_receipt_rule_set`, `aws_route53_record` (MX, DKIM, verification TXT), and effectively black-hole inbound email. The `--dry-run=true` info dump showed `apply ses` in the module list — that's it.

Phase 84.1 closes the upgrade-safety gaps for the SES module specifically (env-var exports, foundation import blocks, timeout, doctor digest check). It does **not** add a generalizable plan-before-apply safety mechanism. The next analogous incident (a different module, a different destructive change) would land with the same blind spot.

This phase fixes that by making `km init --plan` a real `terragrunt plan` per module, with a curated destroy-class gate that exits non-zero if AWS would destroy or replace a resource type known from past incidents to cause unrecoverable data loss.

## Decisions

Settled in brainstorming with the operator on 2026-05-16:

1. **Flag shape.** New `--plan` flag, independent of `--dry-run`. Combinable (`--dry-run=true --plan` shows both info dump and plan). `--plan` itself implies no apply ever runs. Rationale: the current info dump is genuinely useful (it surfaces wrapper-level steps like DNS + build + upload that are not visible in `terragrunt plan`) — replacing its semantics would silently change default behavior.

2. **Scope.** All regional modules, in `runInit` order. No selectable subset. Rationale: a real safety gate has to cover everything the apply would touch; a subset flag invites plan-missing the module that's about to drift.

3. **Safety gate.** Destroy-class gate with curated `ProtectedTypes` allowlist. Any destroy or replace of a protected resource type halts with exit 1. Override: `--i-accept-destroys` (per-invocation, never persisted, does NOT auto-apply — it only clears the `--plan` exit code). Rationale: an all-destroys gate is too noisy (routine lambda zip-hash churn would trip it); no-gate is just `terragrunt plan` with extra steps.

4. **Bootstrap parity.** `km bootstrap --shared-ses` gets a parallel `--plan` flag in the same phase. Rationale: Phase 84 Gaps 2, 3, 6 happened in the bootstrap path too — symmetric coverage is cheap once the plumbing exists.

5. **Output model.** Sequential execution (matches `runInit`). Default: per-module one-line summary (`ses: 2 to add, 0 to change, 1 to destroy ⚠`). `--verbose` streams the full plan text. Destroy-gate trip always prints offending resources in full regardless of verbose. Rationale: terragrunt's own `run --all plan` parallelism is faster but interleaves output, complicates JSON aggregation for the gate, and bursts AWS API calls.

6. **No config file for protected list.** `ProtectedTypes` lives compiled-in. Updates require PR review. Rationale: an operator-side config file would let an organization silently disable the safety; the safety value comes from review pressure.

## Architecture

### Files touched

| File | Change |
|---|---|
| `internal/app/cmd/init.go` | Add `--plan` and `--i-accept-destroys` flags to `NewInitCmd`. New `runInitPlan(cfg, region, verbose, acceptDestroys)`. Flag validation: `--plan` + `--sidecars`/`--lambdas` rejected as mutually exclusive. |
| `internal/app/cmd/bootstrap.go` | Same two flags on `--shared-ses`. New `runBootstrapSharedSESPlan(cfg, region, verbose, acceptDestroys)`. |
| `pkg/terragrunt/runner.go` | Add `PlanWithOutput(ctx, dir, planFile)` (invokes `terragrunt plan -out=<planFile>`). Add `ShowPlanJSON(ctx, dir, planFile) ([]byte, error)` (invokes `terraform show -json <planFile>`). |
| `pkg/terragrunt/runner_test.go` | Extend mock + tests for the two new methods. |
| `pkg/terragrunt/planreport/report.go` | NEW. `Parse(jsonBytes) (Report, error)` returning `{Module, Adds, Changes, Destroys []ResourceChange, Replaces []ResourceChange, ParseFailed bool}`. |
| `pkg/terragrunt/planreport/protected.go` | NEW. Curated `ProtectedTypes []string` with rationale comments per entry. |
| `pkg/terragrunt/planreport/gate.go` | NEW. `Evaluate(reports []Report, acceptDestroys bool) GateResult` returning `{Blocked bool, Trips []Trip}`. |
| `pkg/terragrunt/planreport/*_test.go` | NEW. Unit tests with captured plan-JSON fixtures. |
| `internal/app/cmd/init_plan_test.go` | NEW. Mock runner, verify flag validation, loop, exit codes, output format. |
| `internal/app/cmd/bootstrap_plan_test.go` | NEW. Same for bootstrap. |

**No changes to** `runInit` apply path (Phase 84.1 owns those edits — keeping the surfaces separate avoids merge churn). **No changes to** `runInitDryRun` (the info-dump stays as documentation).

### Data flow — `km init --plan`

1. Parse flags. Reject `--plan` combined with `--sidecars` or `--lambdas`.
2. Call `ExportTerragruntEnvVars(cfg)` (the Phase 84.1-01 helper — depends on 84.1-01 having shipped).
3. Validate AWS credentials (same path as `runInit`).
4. For each module in `regionalModules(regionDir)` order:
   a. If any `m.envReqs` env var is empty AND not in `configEnv` → skip with `[skip: <var> not set]`. Mirrors `runInitDryRun` behavior. Skipped modules do NOT count toward the gate.
   b. Create temp file via `os.CreateTemp("", "km-plan-*.tfplan")`; defer remove.
   c. Call `runner.PlanWithOutput(ctx, dir, planFile.Name())`. Stdout captured for optional verbose output.
   d. Call `runner.ShowPlanJSON(ctx, dir, planFile.Name())`. JSON bytes returned.
   e. `planreport.Parse(bytes)` → `Report{Module: m.name, ...}`.
   f. Print one-line summary line for the module.
   g. If `--verbose`, stream the captured plan text from step (c).
   h. Append report to `reports`.
5. After loop: `result := planreport.Evaluate(reports, acceptDestroys)`.
   - If `result.Blocked`: print the full protected-resource trip block (see § Gate output), exit 1.
   - Else: print aggregate summary across all modules + footer "Run `km init --dry-run=false` to apply", exit 0.

### Data flow — `km bootstrap --shared-ses --plan`

Same structure, single module (`infra/live/<region>/ses-shared-rule-set/`). One report, same gate.

## Protected resource types

Initial list, with the incident motivating each entry:

```go
// pkg/terragrunt/planreport/protected.go

var ProtectedTypes = []string{
    "aws_ses_domain_identity",         // Phase 84 Gap 3 — destroyed during 82.x→84 cutover, inbound email broken
    "aws_ses_domain_dkim",             // Phase 84 Gap 3 — DKIM keys lost with the identity
    "aws_ses_active_receipt_rule_set", // Phase 84 Gap 6 — active pointer nulled, inbound stopped
    "aws_ses_receipt_rule_set",        // Shared rule set — prevent_destroy in code, but plan-time check catches earlier
    "aws_route53_record",              // MX, DKIM CNAMEs, verification TXT — recovery required manual re-creation
    "aws_s3_bucket",                   // Mailbox + artifacts — data loss
    "aws_s3_bucket_policy",            // Detaching SES write policy silently breaks inbound
    "aws_dynamodb_table",              // Sandbox metadata, lock table — destroy = irrecoverable
    "aws_kms_key",                     // Schedule-deleted with 7-30d window, hard to undo cleanly
}
```

This list grows by PR. Each addition should reference the incident report (UAT log, postmortem) that motivated it.

## Gate behavior

### Algorithm

```
trips := []Trip{}
for _, r := range reports {
    if r.ParseFailed {
        trips = append(trips, Trip{Module: r.Module, Reason: "plan JSON parse failed — conservative trip"})
        continue
    }
    for _, ch := range append(r.Destroys, r.Replaces...) {
        if slices.Contains(ProtectedTypes, ch.Type) {
            trips = append(trips, Trip{Module: r.Module, Type: ch.Type, Address: ch.Address, Action: ch.Action})
        }
    }
}
if len(trips) > 0 && !acceptDestroys {
    return GateResult{Blocked: true, Trips: trips}
}
return GateResult{Blocked: false, Trips: trips}  // trips still listed for visibility even on override
```

### Trip output

```
✗ km init --plan would destroy 3 protected resources:

  ses-shared-rule-set:
    - aws_ses_domain_identity.sandboxes      [DESTROY]
    - aws_ses_domain_dkim.sandboxes          [DESTROY]
    - aws_route53_record.dkim[0]             [DESTROY]

These resource types are on the protected list because past incidents
caused unrecoverable data loss (see .planning/phases/84-.../84-10-UAT.md).

To proceed anyway, re-run with --i-accept-destroys (you must understand
why each resource is destroying — terragrunt apply will not ask again).

Exit 1.
```

### Replace vs destroy

Both count as trips. A `Replace` (destroy + create) of `aws_kms_key` is just as data-lossy as a pure destroy. JSON action codes in the `terraform show -json` schema: `["delete"]` = destroy, `["delete","create"]` or `["create","delete"]` = replace.

### Override semantics

`--i-accept-destroys`:
- Per-invocation flag only; never persisted to disk or env.
- Clears the `--plan` exit code from 1 to 0 when the only failures are protected trips.
- Still prints the full trip list (visibility).
- Does NOT auto-apply. The operator must separately run `--dry-run=false`.

## Error handling

| Failure | Behavior |
|---|---|
| `terragrunt plan` exits non-zero (auth fail, missing env var, syntax error) | Stop loop. Print module name + captured stderr. Exit non-zero with module-specific message. Do not continue to other modules. |
| `terraform show -json` fails on a successful plan | Log warning, mark report `ParseFailed: true`, continue loop. Gate treats parse-failed modules as **trips** (conservative — cannot prove safety). |
| Context cancelled (Ctrl-C) | Propagate via `context.Canceled` (Phase 84.1-02 makes this clean). Cleanup temp plan files. Exit 130 (SIGINT convention). |
| Temp file create fails | Fall back to ephemeral inline plan (no JSON parse, conservative-trip) and log warning. Rare. |
| Module's `envReqs` unsatisfied | Skip with `[skip: <var> not set]`. Matches `runInitDryRun`. Skipped modules do not count toward gate. |
| AWS API throttling during plan | Surfaces as `terragrunt plan` non-zero — same as row 1. |
| Plan succeeds with no changes | Report as `0 to add, 0 to change, 0 to destroy ✓`. Counts as clean. |

## Testing

### Unit

1. `pkg/terragrunt/planreport/report_test.go` — table-driven over JSON fixtures:
   - `testdata/ses-clean.json` — no changes
   - `testdata/ses-rule-add.json` — 2 rules added, 0 destroyed (clean case)
   - `testdata/ses-82to84-destroy.json` — Phase 84 trip scenario (hand-crafted to schema if no real capture available)
   - `testdata/lambda-replace.json` — zip hash → replace
   - `testdata/malformed.json` — parse-fail path

2. `pkg/terragrunt/planreport/gate_test.go` — synthetic `Report` slices, parameterized over `acceptDestroys`. Cases: empty input, all-clean, single protected trip, multi-module trip, override clears block but preserves trip list, parse-fail → trip.

3. `internal/app/cmd/init_plan_test.go` — mock the runner interface. Verify:
   - `--plan` + `--sidecars` rejected
   - Module loop order matches `regionalModules()`
   - Skip behavior matches `runInitDryRun`
   - Verbose vs summary output
   - Exit code 0 clean / 1 trip / 1 plan-error / 0 trip-with-override
   - `--i-accept-destroys` preserves trip output

4. `internal/app/cmd/bootstrap_plan_test.go` — same shape, single module.

5. `pkg/terragrunt/runner_test.go` — extend mock to cover `PlanWithOutput` and `ShowPlanJSON`. Verify `-out=` and `-json` flags reach `buildCommand`.

### Operator UAT (manual — not autonomous)

Three scenarios, run against real AWS:

1. **Clean re-apply against already-Phase-84 account** — `km init --plan` exits 0 with all-zero counts. This is the operator's `sandboxes.klankermaker.ai` account today; serves as the "happy path" smoke test.

2. **Synthetic destroy scenario** — temporarily edit a regional module to drop a protected resource (e.g. delete the catchall rule from `infra/modules/ses/v2.0.0/main.tf`). Confirm gate trips with the correct address. Restore the module.

3. **Override flow** — repeat scenario 2 with `--i-accept-destroys`. Confirm gate prints trip list but exits 0. Confirm no apply happens.

4. **Bootstrap parity** — `km bootstrap --shared-ses --plan` runs cleanly on a bootstrapped account. Reapply the same synthetic destroy on the foundation module and confirm trip.

## Non-goals

- **Selectable per-module planning.** Decided against in scope question 2.
- **Tri-state `--dry-run`.** Decided against in scope question 1.
- **`km plan` as a top-level command.** Decided against in approach selection.
- **Operator-side config file for the protected list.** Decided against in scope question 6.
- **Auto-apply after `--i-accept-destroys`.** Explicitly two-step by design — the operator runs `--dry-run=false` separately.
- **Cross-module dependency analysis.** Each module is planned in isolation. Terragrunt dependency edges (mock-outputs, etc.) are handled by terragrunt itself.
- **Periodic plan via `km doctor`.** Decided against in scope question 4 (option C). Could be a follow-up phase if drift detection becomes operationally valuable.

## Open questions / follow-ups

1. **JSON schema version pinning.** The `terraform show -json` output is schema-versioned. We should pin to a minimum schema version and assert on parse. Open: which version is shipping with the terraform pinned by current terragrunt? Resolve during plan-phase.

2. **Per-module timeout coordination with Phase 84.1-02.** Phase 84.1-02 adds context timeouts to `runner.Apply`. The same wrapper should apply to `PlanWithOutput`. Decide whether to thread a `--timeout` flag through `--plan` or inherit defaults.

3. **`km init --plan` and `km doctor` together.** Should `km doctor` mention "consider running `km init --plan` to check for unexpected drift" in its output? Probably yes, low-cost addition — defer to plan-phase.

## Dependencies

- **Phase 84.1-01** (ExportTerragruntEnvVars helper) — must ship before this phase can call the unified env-var helper.
- **Phase 84.1-02** (runner timeouts + heartbeat) — desirable but not strictly blocking; if Phase 84.2 starts before 84.1-02 lands, `PlanWithOutput` can take its own `context.WithTimeout` locally.

## Confidence

Medium-high. The plumbing is well-understood: `runner.Plan` already exists at `pkg/terragrunt/runner.go:104`; `terraform show -json` is a stable documented interface; the gate package is pure logic with no external dependencies. The dominant risk is the JSON fixture work (capturing realistic real-world plan output for the test suite without a Phase-82.x AWS account to reproduce the original incident plan). Mitigation: hand-craft fixtures to the documented schema and validate against a single real plan capture during operator UAT.
