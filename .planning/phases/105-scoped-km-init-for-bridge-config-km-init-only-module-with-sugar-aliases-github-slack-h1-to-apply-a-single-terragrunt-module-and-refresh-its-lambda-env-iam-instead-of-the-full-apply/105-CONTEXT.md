# Phase 105: Scoped km init for bridge config - Context

**Gathered:** 2026-06-11
**Status:** Ready for planning
**Source:** Interactive design discussion (operator + Claude, 2026-06-11)

<domain>
## Phase Boundary

**Delivers:** A scoped `km init` path so an operator can push a *config-key edit* in
`km-config.yaml` (the `github.*`, `slack.*`, `h1.*` sections, plus email) into the owning
Lambda's environment block by applying ONE terragrunt module, instead of running the full
~27-module `km init --dry-run=false`.

**Motivation (operator, verbatim intent):** "I basically just want fast iteration right now on
slack/github/h1 changes and the full init is toooo heavy." Today, flipping e.g.
`github.default_router` or editing `h1.programs` requires a full-fleet terragrunt apply that
plans network/DynamoDB/SES/etc. — minutes of work and broad surface to change ~4 strings on
one Lambda.

**Mechanism — Option A (scoped terragrunt apply), chosen over Option B (direct Lambda API poke):**
Filter the existing `regionalModules()` apply loop to the single selected module and still run
`ExportTerragruntEnvVars(cfg)` first, so the module recomputes its `environment { variables }`
block from the yaml-derived `KM_*` env vars. Stays fully inside terraform state ⇒ ZERO drift,
and picks up IAM changes for that module too. Option B (`UpdateFunctionConfiguration` via the
existing private `upsertLambdaEnvVar()` at `init.go:3420`) was rejected because it creates
terraform drift that is only benign if it derives from the identical yaml→KM_* pipeline — an
easy invariant to violate — and adds a parallel deploy path rather than reusing the apply loop.

**NOT in scope:**
- Refreshing a stale Lambda *code zip* (that is still `make build-lambdas` + `km init --lambdas`).
- Provisioning *new* resources / wiring (new DDB table, SQS queue ⇒ still full `km init`).
- Full-fleet `--only` access to all ~27 modules ("too much rope").
- The Slack auto-resume / auto-start idea discussed earlier the same session (separate future phase).
</domain>

<decisions>
## Implementation Decisions

### Flag surface
- New flag `--only <module>` on `km init`, validated against a **curated allowlist** (NOT all
  `regionalModules()`). Unknown / out-of-allowlist value ⇒ error that prints the allowed set.
- Sugar aliases resolving to exact module names:
  - `--github` → `lambda-github-bridge`
  - `--slack`  → `lambda-slack-bridge`
  - `--h1`     → `lambda-h1-bridge`
  - `--email`  → `email-handler`
- Aliases and `--only` are equivalent entry points; `--github` is just sugar for
  `--only lambda-github-bridge`.

### Two-tier allowlist (LOCKED)
- **Tier 1 — cheap (env+IAM, no destroy-class resources, no confirmation):**
  `lambda-github-bridge`, `lambda-slack-bridge`, `lambda-h1-bridge`, `email-handler`.
  All four are Lambdas with an `environment { variables }` block (confirmed: github
  `infra/modules/lambda-github-bridge/v1.1.0/main.tf:272-290`; h1
  `infra/modules/lambda-h1-bridge/v1.0.0/main.tf:297-313`; slack
  `infra/modules/lambda-slack-bridge/v1.0.0/main.tf`; email-handler
  `infra/modules/email-handler/v1.0.0/main.tf:247`) ⇒ identical safety profile.
  Exposed via the `--github`/`--slack`/`--h1`/`--email` aliases. Fast, no prompt.
- **Tier 2 — gated (destroy-class):** `ses`. Reachable ONLY via explicit `--only ses` —
  **NO cheap alias**. Must route through the destroy-class safety gate (the same curated
  trip-block used by `km init --plan` / `RunInitPlanFunc`) as a PRE-APPLY gate: refuse a
  protected destroy/replace without `--i-accept-destroys`. The `ses` module (`ses/v2.0.0/main.tf`)
  manages `aws_ses_domain_identity`, `aws_ses_domain_dkim`, `aws_route53_record`
  (dkim/mx/ses_verification), `aws_ses_receipt_rule`, `aws_s3_bucket_policy`; it is also the
  LAST module in the loop and owns the consolidated bucket policy. This is the "did the dns and
  all that" path — deliberately heavier and explicit, not one-keystroke.
- `--email` maps to `email-handler` ONLY; the SES/DNS layer is the separate `--only ses` path.
- Implement the allowlist as TWO named slices (cheap vs gated) so a future target is a one-line
  add plus a tier choice.

### Routing / guards
- `--only` (and its aliases) are mutually exclusive with `--sidecars`, `--lambdas`, and `--plan`
  (the guard belongs in the flag-dispatch block, currently `init.go:583-601`).
- The scoped path still runs `ExportTerragruntEnvVars(cfg)` (so env block recomputes from yaml)
  and still honors `--dry-run` (default true ⇒ show what would apply; `--dry-run=false` applies).
- New `runInitScoped()` reuses the existing module loop (`RunInitWithRunner`, `init.go:1794-1999`)
  filtered to the one selected module dir. On a live install the upstream `outputs.json` already
  exist so the module's `dependency` blocks resolve.

### Correctness / known wrinkles (address at plan + verify time)
- **Consolidated bucket policy:** a scoped `ses`-alone apply recomputes the consolidated S3
  bucket policy from dependency outputs; verify it does not propose a spurious change when applied
  in isolation on a live install (outputs.json freshness).
- **Drift invariant (why Option A):** because the scoped apply recomputes from the same
  yaml→KM_* pipeline as a full apply, a later full `km init` is a no-op — no surprise revert.
- **Pre-apply gate wiring:** the destroy-class gate (`RunInitPlanFunc` / curated trip-block) is
  currently a plan-only feature; tier-2 must invoke it as a *pre-apply* gate. Confirm exact wiring.

### Footprint / deploy
- CLI-only, operator-side change. No SandboxProfile schema change, no new TF resource.
- Deploy of THIS feature = `make build` the km binary (operator's local binary; per
  `feedback_rebuild_km` always `make build`, not bare `go build`).
- Existing config keys already flow to env via the documented pipeline
  (`km-config.yaml → ExportTerragruntEnvVars (init.go:1071-1360) → KM_GITHUB_*/KM_H1_*/KM_SLACK_*
  → terragrunt.hcl get_env() → Lambda module var → env block`).

### Claude's Discretion
- Exact Cobra flag wiring, help text, and error-message wording.
- Internal naming of the two allowlist slices / resolver function.
- Whether aliases set `--only` internally or are parsed into the same resolved-module variable.
- Test structure (the existing `lambdaConfigUpdater` interface and module-loop runner are already
  mockable — mirror existing init_test.go patterns).
- Dry-run output format for the scoped path (should clearly name the single module + tier).
</decisions>

<specifics>
## Specific Ideas / Concrete References

- Module list + ordering: `regionalModules()` at `internal/app/cmd/init.go:205-372`.
- Flag definitions: `NewInitCmd()` at `init.go:569-627`.
- Flag dispatch / routing: `init.go:583-601` (where `--plan` / `--sidecars` / `--lambdas` route).
- Partial path precedent: `runInitPartial()` at `init.go:984-1051` (what `--sidecars`/`--lambdas` do).
- Module apply loop: `RunInitWithRunner()` at `init.go:1794-1999`.
- Env-var export: `ExportTerragruntEnvVars()` at `init.go:1071-1360`
  (github lines ~1230-1292, h1 ~1294-1359, slack ~1168-1228).
- Direct-API precedent (Option B, NOT chosen but reuse-aware): `upsertLambdaEnvVar()` at
  `init.go:3420`; `ForceCreateHandlerColdStartWith()` (`:3400`), `ForceSlackBridgeColdStartWith()`
  (`:3446`).
- Destroy-class gate: `RunInitPlanFunc` (the `km init --plan` curated trip-block) — reuse for tier-2.
- Bridge env blocks: github `lambda-github-bridge/v1.1.0/main.tf:272-290`; h1
  `lambda-h1-bridge/v1.0.0/main.tf:297-313`; email-handler `email-handler/v1.0.0/main.tf:247`;
  ses module `ses/v2.0.0/main.tf`.

### Acceptance / UAT anchor
A `github.default_router` flip (false→true) lands on the live `lambda-github-bridge` env block via
`km init --github --dry-run=false` in seconds, and a subsequent full `km init` plans the same env
block as a no-op (proving no drift).
</specifics>

<deferred>
## Deferred Ideas

- **Slack auto-resume / auto-start** when @-mentioned on a paused/stopped sandbox (discussed
  same session; the GitHub bridge already has `EC2Resumer.StartSandbox` + `SetStatusRunning` as a
  port template). Separate future phase; the tier-1 scoped IAM apply here would later carry its
  `ec2:StartInstances` permission change.
- **Additional `--only` targets** beyond the 5 allowlisted modules — one-line add + tier choice when wanted.
- **Option B direct-API env poke** as a sub-second alternative for the tier-1 case — not built;
  the existing `km slack rotate-token` already covers the one latency-critical case.
</deferred>

---

*Phase: 105-scoped-km-init-for-bridge-config*
*Context gathered: 2026-06-11 via interactive design discussion*
