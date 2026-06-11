# Phase 105: Scoped km init for bridge config - Research

**Researched:** 2026-06-11
**Domain:** `internal/app/cmd/init.go` — Cobra flag surface, `regionalModules()` loop, `RunInitWithRunner`, `RunInitPlanWithRunner`, destroy-class gate
**Confidence:** HIGH — all findings are from direct source inspection of the live codebase

## Summary

Phase 105 is an operator-CLI change to `internal/app/cmd/init.go`. No Lambda code, no TF modules, no SandboxProfile schema, no new AWS resources. The work is: add two new flags (`--only` and four sugar aliases), add routing/guard logic inside `NewInitCmd()`'s dispatch block, add a new `runInitScoped()` function that filters the existing `RunInitWithRunner` loop to one module, and add a tier-2 pre-apply destroy-class gate path for `--only ses`.

Every anchor cited in CONTEXT.md was verified against the current code. The actual line numbers deviate slightly from the CONTEXT.md estimates because the file has grown, but the structural positions are accurate. All five tier-1/2 modules exist in `regionalModules()` with the exact names stated, and their terragrunt.hcl dependency blocks resolve from S3 backend state on a live install — no local `outputs.json` files are needed for scoped apply.

**Primary recommendation:** Implement `runInitScoped()` as a tight filter of the existing `RunInitWithRunner` loop (filter by `mod.name == selectedModule`), with a two-slice curated allowlist (`scopedCheapAllowlist`, `scopedGatedAllowlist`) and tier-2 wiring that calls `planModule()` + `planreport.Evaluate()` before `runner.Apply()`.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- New flag `--only <module>` validated against a curated allowlist (NOT all `regionalModules()`). Unknown/out-of-allowlist value errors that prints the allowed set.
- Sugar aliases: `--github` → `lambda-github-bridge`, `--slack` → `lambda-slack-bridge`, `--h1` → `lambda-h1-bridge`, `--email` → `email-handler`.
- Two-tier allowlist: Tier 1 cheap (no confirmation) = `lambda-github-bridge`, `lambda-slack-bridge`, `lambda-h1-bridge`, `email-handler`; Tier 2 gated (destroy-class) = `ses`, only via explicit `--only ses`, no cheap alias.
- Tier-2 must route through the destroy-class safety gate as a pre-apply gate (not standalone plan): refuse protected destroy/replace without `--i-accept-destroys`.
- `--only` / aliases are mutually exclusive with `--sidecars`, `--lambdas`, and `--plan`.
- Scoped path still runs `ExportTerragruntEnvVars(cfg)` and still honors `--dry-run` (default true).
- `runInitScoped()` reuses the existing module loop filtered to one module dir.
- CLI-only, operator-side change. Deploy = `make build` only.
- Implement allowlist as two named slices so a future target is a one-line add plus a tier choice.

### Claude's Discretion
- Exact Cobra flag wiring, help text, and error-message wording.
- Internal naming of the two allowlist slices / resolver function.
- Whether aliases set `--only` internally or are parsed into the same resolved-module variable.
- Test structure (mirror existing init_test.go patterns with mockRunner).
- Dry-run output format for the scoped path (should clearly name the single module + tier).

### Deferred Ideas (OUT OF SCOPE)
- Slack auto-resume / auto-start when @-mentioned on a paused/stopped sandbox.
- Additional `--only` targets beyond the 5 allowlisted modules.
- Option B direct-API env poke as a sub-second alternative.
</user_constraints>

<phase_requirements>
## Phase Requirements

No pre-assigned IDs. Based on ROADMAP.md hints and CONTEXT.md design, the following IDs are proposed for planner use:

| ID | Description | Research Support |
|----|-------------|-----------------|
| INIT-SCOPED-FLAG | `--only <module>` flag on `km init`, validated against two-tier curated allowlist; unknown value errors with allowed set printed | Anchor: `NewInitCmd()` at `init.go:569`; allowlist as two named slices in same file |
| INIT-SCOPED-ALIASES | Sugar aliases `--github`/`--slack`/`--h1`/`--email` each resolving to the exact module name; aliases and `--only` are equivalent entry points | Confirmed module names in `regionalModules()` at `init.go:205-372` |
| INIT-SCOPED-GUARD | Mutual-exclusion guard: `--only`/aliases vs `--sidecars`/`--lambdas`/`--plan`; tier-2 `--only ses` routes through destroy-class gate as pre-apply | Routing block at `init.go:583-601`; gate at `RunInitPlanWithRunner` `init.go:2072`; `planModule` at `init.go:2177` |
| INIT-SCOPED-IMPL | `runInitScoped()` — `ExportTerragruntEnvVars` + single-module filter of `RunInitWithRunner` loop; honors `--dry-run`; ses path does plan-then-gate-then-apply | `RunInitWithRunner` at `init.go:1794`; `ExportTerragruntEnvVars` at `init.go:1071`; `planModule` + `planreport.Evaluate` reused |
| INIT-SCOPED-TESTS | Unit tests: allowlist resolution + rejection, mutual-exclusion guard, tier-2 gate invocation, dry-run output | `mockRunner` in `init_test.go`; `mockPlanRunner` in `init_plan_test.go`; `RegionalModules()` exported for test inspection |
| INIT-SCOPED-DOCS | Update CLAUDE.md CLI section, `klanker:init` skill, `OPERATOR-GUIDE.md`, and per-bridge docs with the new scoped shortcut; clarify boundary (env+IAM only, not code zip) | Doc surfaces enumerated in research; klanker:init SKILL.md "Fast-path variants" section is the primary doc update target |
</phase_requirements>

---

## Standard Stack

### Core — nothing new; all existing packages

| Package / Symbol | Location | Purpose in This Phase |
|---------|---------|--------------|
| `cobra.Command` | `github.com/spf13/cobra` | Cobra flag definitions in `NewInitCmd()` |
| `InitRunner` interface | `internal/app/cmd/init.go:51-57` | Already has `Apply`, `Output`, `Reconfigure`, `PlanWithOutput`, `ShowPlanJSON` — sufficient for scoped apply + tier-2 gate |
| `regionalModules()` | `init.go:205-372` | Returns ordered `[]regionalModule`; filter to one entry for scoped path |
| `RunInitWithRunner` | `init.go:1794-1999` | Full apply loop — new `runInitScoped()` is a thin wrapper that filters this |
| `ExportTerragruntEnvVars` | `init.go:1071-1360` | MUST be called before any scoped apply (same contract as `runInit`) |
| `planModule` | `init.go:2177-2235` | Runs PlanWithOutput + ShowPlanJSON + planreport.Parse for a single module; reuse directly for tier-2 gate |
| `planreport.Evaluate` | `pkg/terragrunt/planreport` | The curated destroy-class gate (`Blocked`, `Trips`, `acceptDestroys`) — reuse for tier-2 |
| `RunInitPlanFunc` | `init.go:99` | Package-level `var` for the `--plan` path; tier-2 does NOT call this whole function — it calls `planModule` + `planreport.Evaluate` directly for just the one module |
| `mockRunner` | `internal/app/cmd/init_test.go:22` | Already satisfies full `InitRunner`; reuse in new unit tests |
| `mockPlanRunner` | `internal/app/cmd/init_plan_test.go:42` | Embeds `mockRunner`, adds `PlanWithOutput`/`ShowPlanJSON`; reuse for tier-2 gate tests |
| `ModuleTimeoutFunc` | `init.go:196` | Per-module timeout lookup; `lambda-*` and `ses` already get 5-min bound |

**Installation:** no new dependencies. This phase touches only `internal/app/cmd/init.go` (and its test files).

---

## Architecture Patterns

### Recommended Structure

```
internal/app/cmd/init.go
├── scopedCheapAllowlist  []string  (package-level var)
├── scopedGatedAllowlist  []string  (package-level var)
├── NewInitCmd()          — add --only + 4 alias flags; add routing guard + runInitScoped dispatch
└── runInitScoped()       — new function: ExportTerragruntEnvVars + single-module apply (+ pre-apply gate for tier-2)

internal/app/cmd/init_scoped_test.go   (new test file, mirrors init_test.go / init_plan_test.go)
```

### Pattern 1: Two-slice curated allowlist

The simplest implementation that satisfies the one-line-add-plus-tier-choice requirement:

```go
// scopedCheapAllowlist: Tier 1 — env+IAM, no destroy-class resources, no confirmation.
// Sugar aliases --github/--slack/--h1/--email also resolve to these names.
var scopedCheapAllowlist = []string{
    "lambda-github-bridge",
    "lambda-slack-bridge",
    "lambda-h1-bridge",
    "email-handler",
}

// scopedGatedAllowlist: Tier 2 — reachable only via explicit --only <module>.
// Every entry here routes through the destroy-class safety gate before apply.
var scopedGatedAllowlist = []string{
    "ses",
}
```

Resolution helper (at Claude's discretion on naming):

```go
func resolveScopedModule(only, github, slack, h1, email bool, onlyVal string) (string, error) {
    // alias → canonical name
    // validate against combined allowlist
    // return "" + error for unknown/out-of-allowlist
}
```

### Pattern 2: Flag dispatch routing block (CURRENT at init.go:583-601)

Current block:

```go
if plan && (sidecarsOnly || lambdasOnly) {
    return fmt.Errorf("--plan cannot be combined with --sidecars or --lambdas")
}
if plan {
    return RunInitPlanFunc(cfg, awsProfile, region, verbose, acceptDestroys)
}
if sidecarsOnly || lambdasOnly {
    return runInitPartial(cfg, awsProfile, region, verbose, sidecarsOnly, lambdasOnly)
}
if dryRun {
    return runInitDryRun(cfg, region)
}
return runInit(cfg, awsProfile, region, verbose)
```

New block adds a guard at the top and a new branch before `dryRun`:

```go
// Phase 105: resolve --only / sugar aliases
scopedModule, scopedErr := resolveScopedModule(onlyVal, githubFlag, slackFlag, h1Flag, emailFlag)
if scopedErr != nil { return scopedErr }

// Mutual exclusion: --only / aliases vs --plan / --sidecars / --lambdas
if scopedModule != "" && (plan || sidecarsOnly || lambdasOnly) {
    return fmt.Errorf("--only/--github/--slack/--h1/--email cannot be combined with --plan, --sidecars, or --lambdas")
}
if plan && (sidecarsOnly || lambdasOnly) {
    return fmt.Errorf("--plan cannot be combined with --sidecars or --lambdas")
}
if plan {
    return RunInitPlanFunc(cfg, awsProfile, region, verbose, acceptDestroys)
}
if scopedModule != "" {
    return runInitScoped(cfg, awsProfile, region, verbose, scopedModule, acceptDestroys)
}
if sidecarsOnly || lambdasOnly {
    return runInitPartial(cfg, awsProfile, region, verbose, sidecarsOnly, lambdasOnly)
}
if dryRun {
    return runInitDryRun(cfg, region)
}
return runInit(cfg, awsProfile, region, verbose)
```

### Pattern 3: runInitScoped() structure

Follows the `runInitPlan` → `RunInitPlanWithRunner` split pattern for testability:

```go
func runInitScoped(cfg *config.Config, awsProfile, region string, verbose bool, module string, acceptDestroys bool) error {
    ctx := context.Background()
    awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
    if err != nil { ... }
    if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil { ... }

    ExportTerragruntEnvVars(cfg)           // MUST run first — same contract as runInit
    EnsureSlackBotUserIDFromSSM(ctx, cfg, awsCfg)  // needed for slack module

    repoRoot := findRepoRoot()
    runner := terragrunt.NewRunner(awsProfile, repoRoot)
    runner.Verbose = verbose

    return RunInitScopedWithRunner(runner, repoRoot, region, module, acceptDestroys)
}
```

The testable core `RunInitScopedWithRunner`:

```go
func RunInitScopedWithRunner(runner InitRunner, repoRoot, region, module string, acceptDestroys bool) error {
    ctx := context.Background()
    regionLabel := compiler.RegionLabel(region)
    regionDir := filepath.Join(repoRoot, "infra", "live", regionLabel)

    if err := ensureRegionHCL(regionDir, regionLabel, region); err != nil { return err }

    // Find the one module in the ordered list
    modules := regionalModules(regionDir)
    var target *regionalModule
    for i := range modules {
        if modules[i].name == module { target = &modules[i]; break }
    }
    if target == nil {
        return fmt.Errorf("module %q not found in regional module list", module)
    }

    // Tier check: if module is in gated allowlist, run plan → gate → apply
    if isScopedGated(module) {
        // Pre-apply destroy-class gate (plan only, do not apply if tripped)
        report, planErr := planModule(ctx, runner, *target, false)
        if planErr != nil { return fmt.Errorf("planning %s: %w", module, planErr) }
        result := planreport.Evaluate([]planreport.Report{report}, acceptDestroys)
        if result.Blocked {
            printTripBlock(fmt.Sprintf("km init --only %s", module), result.Trips)
            return fmt.Errorf("destroy-class gate tripped (re-run with --i-accept-destroys to override)")
        }
        if len(result.Trips) > 0 {
            printTripBlock(fmt.Sprintf("km init --only %s", module), result.Trips)
            fmt.Println("  (override active via --i-accept-destroys)")
        }
        // SES preflight (same as RunInitWithRunner ses branch)
        if module == "ses" && InitSESPreflight != nil {
            if err := InitSESPreflight(ctx); err != nil { return err }
            // Also run Reconfigure for ses (same rationale as RunInitWithRunner:1860)
            rcfgCtx, cancel := context.WithTimeout(ctx, reconfigureTimeout)
            err := runner.Reconfigure(rcfgCtx, target.dir)
            cancel()
            if err != nil { return fmt.Errorf("reconfiguring ses backend: %w", err) }
        }
    }

    // envReqs check (same as RunInitWithRunner)
    for _, envVar := range target.envReqs {
        if os.Getenv(envVar) == "" {
            return fmt.Errorf("module %s requires %s (not set)", module, envVar)
        }
    }

    // Tier + module banner
    tier := "tier-1"
    if isScopedGated(module) { tier = "tier-2 (gated)" }
    printBanner(fmt.Sprintf("km init --only %s", module), fmt.Sprintf("%s (%s) [%s]", region, regionLabel, tier))

    // Apply with per-module timeout
    fmt.Printf("  Applying %s...", module)
    timeout := ModuleTimeoutFunc(module)
    applyCtx, cancel := context.WithTimeout(ctx, timeout)
    err := runner.Apply(applyCtx, target.dir)
    cancel()
    if err != nil {
        fmt.Println()
        if errors.Is(err, context.DeadlineExceeded) {
            return fmt.Errorf("module %s wedged after %s: %w", module, timeout, err)
        }
        return fmt.Errorf("applying %s: %w", module, err)
    }
    fmt.Println(" done")

    // email-handler post-apply: capture ARN (same as RunInitWithRunner:1943)
    if module == "email-handler" {
        outputMap, outErr := runner.Output(ctx, target.dir)
        if outErr == nil {
            if arnVal, ok := outputMap["lambda_function_arn"]; ok {
                arn := fmt.Sprintf("%v", extractValue(arnVal))
                os.Setenv("KM_EMAIL_HANDLER_ARN", arn)
                fmt.Printf("  Email handler ARN: %s\n", arn)
            }
        }
    }

    fmt.Println()
    fmt.Printf("Scoped init complete for %s.\n", module)
    fmt.Printf("Note: this refreshed env + IAM only. For a stale code zip: make build-lambdas && km init --lambdas\n")
    fmt.Printf("Note: for new resources/tables/queues: km init --dry-run=false\n")
    return nil
}
```

### Anti-Patterns to Avoid

- **Calling `RunInitPlanFunc` for tier-2 gate:** `RunInitPlanFunc` runs a plan loop over ALL modules and never applies. Tier-2 needs to gate on plan for just ONE module, then apply. Call `planModule()` + `planreport.Evaluate()` directly — do not call `RunInitPlanFunc`.
- **Skipping `ExportTerragruntEnvVars` in the scoped path:** The entire point of Option A is that terragrunt recomputes the env block from `KM_*` vars. Without `ExportTerragruntEnvVars`, the env vars are unset and `get_env()` in the `terragrunt.hcl` returns empty strings.
- **Skipping the ses preflight / Reconfigure:** `RunInitWithRunner` has special handling for the `ses` module at lines 1845-1870 (preflight + Reconfigure). The scoped path for `ses` must replicate both.
- **Applying with `dryRun=true` being the default:** `runInitScoped` must respect the `--dry-run` flag. When `dryRun=true` (the default), print what would apply and return without calling `runner.Apply`. This mirrors `runInitDryRun`.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Single-module apply with timeout | Custom timer + exec | `ModuleTimeoutFunc(name)` + `context.WithTimeout` | Already handles wedged-terragrunt scenario; already tested |
| Plan output parsing + destroy classification | Custom regex on plan text | `planModule()` + `planreport.Evaluate()` | Existing, tested, handles `ParseFailed` conservative trip correctly |
| Module existence check | Direct `os.Stat` in `runInitScoped` | Reuse the `os.IsNotExist` pattern from `RunInitWithRunner:1821` | Consistent skip behavior |
| env-var skip check | Re-implement | Iterate `target.envReqs` exactly as `RunInitWithRunner:1828-1838` does | Consistent behavior; envReqs for the 5 allowlisted modules are all already defined |
| SES backend reconfigure | Skip it for scoped apply | Replicate `RunInitWithRunner:1851-1870` for the `ses` module | Reconfigure is required for ses because its module source has changed from v1→v2; omitting it silently wedges |
| post-apply output capture | Skip for scoped apply | Include for `email-handler` (ARN capture) | `RunInitWithRunner:1943` captures `KM_EMAIL_HANDLER_ARN`; a scoped email-handler apply should do the same for consistency |

---

## Common Pitfalls

### Pitfall 1: Terragrunt dependency blocks on a live install

**What goes wrong:** Developer sees `dependency "sandboxes"` in `lambda-github-bridge/terragrunt.hcl` and worries that a scoped apply will try to re-apply upstream modules.

**Why it's not a problem on a live install:** Terragrunt dependency blocks read from the S3 backend state of the upstream module, not from local `outputs.json` files. On a live install every upstream module's state is in S3. The `mock_outputs_allowed_terraform_commands = [..., "apply"]` is only used when the upstream has _never_ been applied (no S3 state) — on a live install the real state is used.

**The `upstreamOutputs` field is different:** Only the `efs` module uses `upstreamOutputs` (a Go-level probe that checks for local `outputs.json`). None of the 5 allowlisted modules have `upstreamOutputs` populated, so the `upstreamOutputsExist()` check at `init.go:1781` does not apply to them.

**Confirmed:** `lambda-github-bridge`, `lambda-slack-bridge`, `lambda-h1-bridge`, `email-handler`, and `ses` all have `upstreamOutputs: nil` in `regionalModules()`.

**How to avoid:** On a fresh install with no upstream state, a scoped apply of any of these modules will use mock outputs (from `mock_outputs_allowed_terraform_commands = [..., "apply"]`). The operator should run a full `km init --dry-run=false` before using scoped apply on a new install.

### Pitfall 2: ses consolidated bucket policy on scoped-alone apply

**What goes wrong:** `ses/v2.0.0/main.tf:133` owns `aws_s3_bucket_policy.mail` — the consolidated S3 bucket policy. This resource is computed from `var.artifact_bucket_name` and `var.resource_prefix`, both supplied by `get_env()` in `ses/terragrunt.hcl`. No dependency block — it reads these from env vars, not from upstream outputs.

**Why it should be safe:** `ExportTerragruntEnvVars` sets `KM_ARTIFACTS_BUCKET` and `KM_RESOURCE_PREFIX` before the scoped apply. The policy document is derived from these two values. On a stable install with no env drift, the plan should show no change to `aws_s3_bucket_policy.mail`.

**Potential issue:** If `KM_ARTIFACTS_BUCKET` or `KM_RESOURCE_PREFIX` has drifted since the last full apply (e.g., env var exported with a different value), the scoped `ses` apply COULD propose an unexpected bucket policy change. This is why tier-2 has the destroy-class gate and dry-run default.

**How to avoid:** The tier-2 `--only ses` path runs the pre-apply plan + destroy-class gate. The gate catches `aws_s3_bucket_policy` changes of `destroy`/`replace` type. The executor UAT step should verify: `km init --only ses` (dry-run=true, default) shows no unexpected policy changes on a live install with clean env.

**ses has NO terragrunt dependency blocks** (confirmed: `cat infra/live/use1/ses/terragrunt.hcl` — only `get_env()` calls, no `dependency` blocks). This makes scoped ses apply simpler than the bridge modules.

### Pitfall 3: EnsureSlackBotUserIDFromSSM omission

**What goes wrong:** `runInit` calls `EnsureSlackBotUserIDFromSSM` after `ExportTerragruntEnvVars` so that `KM_SLACK_BOT_USER_ID` is populated in the Lambda env when `lambda-slack-bridge` is applied. If `runInitScoped` omits this call for the `--slack` path, the next scoped apply will write an empty `KM_SLACK_BOT_USER_ID` into the Lambda env, breaking the mention-only feature (Phase 91).

**How to avoid:** `runInitScoped` (the production wrapper) should call `EnsureSlackBotUserIDFromSSM` unconditionally (it's a no-op when not needed and non-fatal on error). Alternatively, gate it on `module == "lambda-slack-bridge"`. Recommended: call it unconditionally to mirror `runInit` exactly.

### Pitfall 4: email-handler ARN not captured after scoped apply

**What goes wrong:** `RunInitWithRunner` at line 1943 captures `KM_EMAIL_HANDLER_ARN` from `email-handler` outputs and sets it as an env var (SES module reads it via `get_env("KM_CREATE_HANDLER_ARN")`... actually confirmed: the ses module does NOT read `KM_EMAIL_HANDLER_ARN` — ses reads `KM_ARTIFACTS_BUCKET` and `KM_RESOURCE_PREFIX` only). The ARN capture is for the in-process loop (if ses applies in the same run after email-handler). In a scoped apply of `email-handler` alone, ses does not apply afterward, so the ARN capture is informational.

**Verdict:** Include the ARN `fmt.Printf` for operator visibility, but the absence of ARN capture does not break anything in a scoped email-handler apply. The ARN is already persisted in TF state.

### Pitfall 5: --dry-run default with scoped path

**What goes wrong:** The `dryRun` flag defaults to `true` in `NewInitCmd`. If `runInitScoped` always applies without checking `dryRun`, it will apply on `km init --github` without any `--dry-run=false` override.

**How to avoid:** Check `dryRun` before calling `runner.Apply`. When `dryRun=true`, print a summary of what would be applied and return nil. The CONTEXT.md Decision confirms: "The scoped path still honors `--dry-run` (default true ⇒ show what would apply; `--dry-run=false` applies)."

---

## Code Examples

Verified line anchors for the planner:

### `regionalModule` struct (init.go:149-154)
```go
// Source: init.go:149
type regionalModule struct {
    name            string
    dir             string
    envReqs         []string // environment variables required to apply this module
    upstreamOutputs []string // upstream module names whose outputs.json must exist before planning
}
```

### Exact module names and envReqs for the 5 allowlisted modules (init.go:264-370)

| Module name | dir (relative) | envReqs | upstreamOutputs |
|---|---|---|---|
| `email-handler` | `regionDir/email-handler` | `["KM_ARTIFACTS_BUCKET"]` | nil |
| `lambda-slack-bridge` | `regionDir/lambda-slack-bridge` | `["KM_ARTIFACTS_BUCKET"]` | nil |
| `lambda-github-bridge` | `regionDir/lambda-github-bridge` | `["KM_ARTIFACTS_BUCKET"]` | nil |
| `lambda-h1-bridge` | `regionDir/lambda-h1-bridge` | `["KM_ARTIFACTS_BUCKET"]` | nil |
| `ses` | `regionDir/ses` | `["KM_ROUTE53_ZONE_ID"]` | nil |

For scoped apply: `ses` requires `KM_ROUTE53_ZONE_ID` (set by `ExportTerragruntEnvVars`); the four tier-1 modules require `KM_ARTIFACTS_BUCKET` (also set by `ExportTerragruntEnvVars`). If either is unset, `runInitScoped` should return a clear error.

### defaultModuleTimeout for allowlisted modules (init.go:183-190)

```go
// Source: init.go:182
case "ses", "ttl-handler", "create-handler", "email-handler", "lambda-slack-bridge", "lambda-github-bridge", "lambda-h1-bridge":
    return 5 * time.Minute
```

All 5 allowlisted modules already have a 5-minute timeout. No changes to `defaultModuleTimeout`.

### NewInitCmd dispatch block — CURRENT (init.go:583-601)

```go
// Source: init.go:583
RunE: func(cmd *cobra.Command, args []string) error {
    if awsProfile == "" {
        awsProfile = "klanker-application"
    }
    if plan && (sidecarsOnly || lambdasOnly) {
        return fmt.Errorf("--plan cannot be combined with --sidecars or --lambdas")
    }
    if plan {
        return RunInitPlanFunc(cfg, awsProfile, region, verbose, acceptDestroys)
    }
    if sidecarsOnly || lambdasOnly {
        return runInitPartial(cfg, awsProfile, region, verbose, sidecarsOnly, lambdasOnly)
    }
    if dryRun {
        return runInitDryRun(cfg, region)
    }
    return runInit(cfg, awsProfile, region, verbose)
},
```

The Phase 105 guard and dispatch must be inserted BEFORE the `plan` check (to catch alias+plan mutual exclusion) and the new `runInitScoped` branch AFTER the `plan` branch and BEFORE `sidecarsOnly || lambdasOnly`.

### planModule signature (init.go:2177)

```go
// Source: init.go:2177
func planModule(ctx context.Context, runner InitRunner, m regionalModule, verbose bool) (planreport.Report, error)
```

Called directly from `RunInitPlanWithRunner` for each module. Tier-2 reuses this exact call for the single ses module before applying.

### RunInitPlanFunc — package-level var (init.go:99)

```go
// Source: init.go:99
var RunInitPlanFunc = runInitPlan
```

`RunInitPlanFunc` is the full `--plan` path (plans ALL modules, never applies). Tier-2 does NOT call `RunInitPlanFunc`. It calls `planModule` + `planreport.Evaluate` directly for just the one module, then calls `runner.Apply`.

### printTripBlock and planreport.Evaluate (init.go:2131-2145)

```go
// Source: init.go:2131 (inside RunInitPlanWithRunner)
result := planreport.Evaluate(reports, acceptDestroys)
if result.Blocked {
    printTripBlock("km init --plan", result.Trips)
    return fmt.Errorf("destroy-class gate tripped (re-run with --i-accept-destroys to override)")
}
if len(result.Trips) > 0 {
    printTripBlock("km init --plan", result.Trips)
    fmt.Println("  (override active via --i-accept-destroys — exit 0; no apply will run)")
}
```

Tier-2 replicates this pattern with invoker string `"km init --only ses"`. The `--i-accept-destroys` flag is already present in `NewInitCmd` (it's used by `--plan`); the scoped path can reuse the same `acceptDestroys` variable.

### ExportTerragruntEnvVars coverage for the 5 modules (init.go:1071-1360)

| Module | Relevant KM_* vars set by ExportTerragruntEnvVars |
|---|---|
| `lambda-github-bridge` | `KM_GITHUB_REPOS`, `KM_GITHUB_PEER_BRIDGES`, `KM_GITHUB_DEFAULT_ROUTER`, `KM_ARTIFACTS_BUCKET`, `KM_RESOURCE_PREFIX` |
| `lambda-slack-bridge` | `KM_SLACK_MENTION_ONLY`, `KM_SLACK_REACT_ALWAYS`, `KM_SLACK_PEER_BRIDGES`, `KM_SLACK_DEFAULT_ROUTER`, `KM_ARTIFACTS_BUCKET`, `KM_RESOURCE_PREFIX` |
| `lambda-h1-bridge` | `KM_H1_PROGRAMS`, `KM_H1_DEFAULT_PROFILE`, `KM_H1_BOT_HANDLE`, `KM_ARTIFACTS_BUCKET`, `KM_RESOURCE_PREFIX` |
| `email-handler` | `KM_ARTIFACTS_BUCKET`, `KM_RESOURCE_PREFIX`, `KM_EMAIL_SUBDOMAIN`, `KM_DOMAIN` |
| `ses` | `KM_ROUTE53_ZONE_ID`, `KM_ARTIFACTS_BUCKET`, `KM_RESOURCE_PREFIX`, `KM_EMAIL_SUBDOMAIN` |

All are exported by `ExportTerragruntEnvVars` before any terragrunt invocation.

### lambdaBuilds list (for context — NOT used by Option A)

```go
// Source: init.go:2470
func lambdaBuilds() []lambdaBuild
```

Option A (scoped terragrunt apply) does NOT rebuild the Lambda zip. The zip path `${local.repo_root}/build/km-github-bridge.zip` must already exist. The boundary note in the output message ("For a stale code zip: make build-lambdas && km init --lambdas") covers this.

---

## Dependency Resolution on Live Install — Detail

On a live install, terragrunt `dependency` blocks resolve from the S3 Terraform state of the upstream module. The `mock_outputs` entries in the `dependency` blocks exist only as a fallback when no S3 state exists (fresh install). Because all upstream modules (dynamodb-sandboxes, dynamodb-slack-nonces, dynamodb-github-threads, etc.) have been applied in the full `km init`, their S3 state is present and the dependency blocks resolve to real ARNs/table names.

Therefore: a scoped apply of any of the 5 allowlisted modules on a live install will use real dependency outputs from S3 state. No local `outputs.json` files are needed (unlike the `efs` / `network` modules, which use `upstreamOutputs` for local file probing).

**Implication for `RunInitScopedWithRunner`:** The `upstreamOutputsExist()` check that `RunInitPlanWithRunner` uses at line 2116 is not needed for the scoped apply path (it is a plan-mode skip guard for modules with `upstreamOutputs` — none of the 5 allowlisted modules have this field set).

---

## ses Consolidated Bucket Policy — Wrinkle Analysis

`ses/v2.0.0/main.tf:133` defines `aws_s3_bucket_policy.mail`. This resource:

1. Is computed from `var.artifact_bucket_name` (= `get_env("KM_ARTIFACTS_BUCKET", "")`) and `var.resource_prefix` (= `get_env("KM_RESOURCE_PREFIX", "km")`).
2. Has no dependency blocks — it does not read from any upstream module's state.
3. Is deterministic: same `KM_ARTIFACTS_BUCKET` + `KM_RESOURCE_PREFIX` → identical policy JSON.

**Spurious diff risk:** LOW on a clean install (same yaml values → same env vars → same policy). HIGH if the operator's env has drifted (e.g., `KM_ARTIFACTS_BUCKET` set to a different value in the shell). The drift WARN in `ExportTerragruntEnvVars` surfaces this to the operator before apply.

**The tier-2 destroy-class gate catches the dangerous case:** An `aws_s3_bucket_policy` change that is a `destroy` or `replace` would trip the gate. A pure `update` to the policy document (e.g., adding a statement) would NOT trip the gate — it would apply. This is the correct behavior.

**Executor verification required (UAT):** On a live install with stable config, `km init --only ses` (dry-run=true, the default) should show the `ses` plan as "No changes" or "No-op" for the bucket policy. This is the no-drift invariant check.

---

## Testing Surface

### Existing test infrastructure (verified)

- **`mockRunner`** at `init_test.go:22-73`: satisfies full `InitRunner`; `applied []string` records Apply calls; `failOn string` triggers failure; `applyBlocks bool` exercises timeout. This mock is SUFFICIENT for tier-1 scoped apply tests.
- **`mockPlanRunner`** at `init_plan_test.go:42-72`: embeds `mockRunner`, adds `planJSON map[string][]byte` for per-module plan output. Sufficient for tier-2 gate tests.
- **`cmd.RegionalModules(dir)`** (exported): tests can call this to inspect module names/order without importing internal fields.
- **`cmd.RunInitWithRunner(runner, repoRoot, region)`** (exported): test seam for the full loop; `RunInitScopedWithRunner` should be exported in the same pattern.
- **`cmd.RunInitPlanFunc`** (package-level var): can be overridden in tests to verify that the scoped tier-2 path does NOT call it.
- **`cmd.BuildLambdaZipsFunc`** (package-level var at `init.go:106`): can be overridden to skip the zip build in scoped tests.

### What can be tested without real AWS/terragrunt

| Test scenario | Test type | Approach |
|---|---|---|
| Allowlist resolution: `--github` → `lambda-github-bridge` | Unit | Call `resolveScopedModule(...)` directly or via `cmd.Execute()` with `--github --dry-run=true` |
| Allowlist rejection: `--only unknown-module` errors with allowed set | Unit | `cmd.Execute()` → assert error contains module names |
| Mutual-exclusion guard: `--github --plan` errors | Unit | `cmd.Execute()` → assert error |
| Mutual-exclusion guard: `--github --sidecars` errors | Unit | `cmd.Execute()` → assert error |
| Tier-1 scoped apply routes to `RunInitScopedWithRunner` | Unit | Override `RunInitScopedWithRunner` var (if exported as var) or use cobra `Execute()` with mocked runner |
| Tier-2: plan is called before apply for `ses` | Unit | `mockPlanRunner` + `RunInitScopedWithRunner(runner, ...)` — assert `planned` has `ses`, then `applied` has `ses` |
| Tier-2: gate blocked → apply NOT called | Unit | `mockPlanRunner.planJSON["ses"] = destroyPlanJSON` → assert `applied` is empty |
| Tier-2: gate overridden with `acceptDestroys=true` → apply IS called | Unit | Same as above but with `acceptDestroys=true` → assert `applied` has `ses` |
| Scoped apply calls `ExportTerragruntEnvVars` | Unit | `t.Setenv(...)` + verify env vars are set before apply |
| `--dry-run=true` (default) does not call runner.Apply | Unit | `mockRunner` — assert `applied` is empty |
| `--dry-run=false` calls runner.Apply | Unit | `mockRunner` — assert `applied` has the one module |
| Module not found → error | Unit | `RunInitScopedWithRunner` with a module dir not created |

### What needs live UAT

| Scenario | Why manual |
|---|---|
| `km init --github --dry-run=false` on live install → `lambda-github-bridge` env block updated | Requires real AWS + real terragrunt |
| Full `km init --dry-run=false` after scoped apply → no-op (no drift) | Requires real AWS; verifies the drift invariant |
| `km init --only ses` (dry-run=true) on live install → no unexpected policy changes | Requires real AWS; verifies the bucket policy wrinkle |
| `km init --only ses --dry-run=false` on live install → ses applies cleanly | Requires real AWS |

---

## Deploy Surface for Phase 105

**Deploy = `make build` only.** No Lambda code changes, no TF module changes, no SandboxProfile schema changes.

Confirmed: this is an operator-side CLI change. The `km` binary is local to the operator's workstation. No `km init --sidecars`, no `km init --dry-run=false` required to deploy this feature itself.

**Doc surfaces that must update** (planner should include a docs task):

| Doc surface | What changes |
|---|---|
| `CLAUDE.md` CLI section | Add `km init --github`, `km init --slack`, `km init --h1`, `km init --email`, `km init --only <module>` to the CLI command table |
| `skills/init/SKILL.md` | "Fast-path variants" section (lines 59-67): add `km init --github/--slack/--h1/--email` as the new fast path for config-key edits; clarify boundary vs `--lambdas` (code zip) vs `--sidecars` (binary) vs full `km init` (new resources) |
| `OPERATOR-GUIDE.md` | Init section: add scoped apply guidance; boundary note (env+IAM only, not code zip, not new resources) |
| `docs/github-bridge.md` § Deploy sections | Lines like "Deploy = make build-lambdas + km init --dry-run=false (NOT --sidecars)" now have a faster alternative: `km init --github --dry-run=false` for config-key edits only |
| `docs/slack-notifications.md` § Deploy sections | Same pattern — "km init --slack --dry-run=false" for config-key edits |
| `docs/h1-bridge.md` § Deploy section | "km init --h1 --dry-run=false" for config-key edits |
| `docs/operational-gotchas.md` | Consider adding a note that `--github/--slack/--h1` do NOT rebuild the Lambda zip (must still `make build-lambdas` + full `km init` for code changes) |

---

## State of the Art

| Old approach | Current approach | Phase added | Impact |
|---|---|---|---|
| Full `km init --dry-run=false` for any config change | `km init --github/--slack/--h1/--email` for env-block-only config edits | Phase 105 | Minutes → seconds for bridge config iteration |
| Direct `UpdateFunctionConfiguration` (Option B, rejected) | Scoped terragrunt apply (Option A) | Phase 105 design | Zero TF drift; picks up IAM changes; reuses apply loop |
| `km init --plan` for destroy-class preview | Pre-apply gate in tier-2 (`--only ses`) | Phase 105 | Same curated gate, wired as pre-apply guard not standalone plan |

**Not changed:** `--sidecars` (binary/sidecar rebuild), `--lambdas` (code zip rebuild), full `--dry-run=false` (all modules + new resources). Phase 105 adds a new lane between `--lambdas` (too shallow: no env block update) and full apply (too broad: all 27 modules).

---

## Open Questions

1. **`runInitScoped` as exported var or exported function?**
   - What we know: `RunInitWithRunner` and `RunInitPlanWithRunner` are exported functions (not vars). `RunInitPlanFunc` is an exported var for the top-level entry point.
   - What's unclear: Should `RunInitScopedWithRunner` be exported as a var (testable seam) or just an exported function (sufficient since mockRunner satisfies InitRunner directly)?
   - Recommendation: Export as a plain function (`RunInitScopedWithRunner`) following the `RunInitWithRunner` pattern. No var needed — `cmd_test` can call it directly with `mockRunner`. Keep the production entry-point `runInitScoped` unexported.

2. **h1 command SSM publishing in the scoped path**
   - What we know: `runInit` at line 759 calls `PublishH1CommandsToSSM` when `len(cfg.H1.Programs) > 0`. This publishes the h1 commands to SSM before the Lambda module applies (the bridge reads from SSM at cold start).
   - What's unclear: Should `runInitScoped` for `--h1` also call `PublishH1CommandsToSSM`?
   - Recommendation: YES — the scoped h1 path should mirror `runInit` and call `PublishH1CommandsToSSM` before applying `lambda-h1-bridge`. Same for `PublishGitHubCommandsToSSM` for `--github`. These are cheap SSM writes and the Lambda cold start reads from SSM. Omitting them means a `km init --h1 --dry-run=false` after editing `h1.programs` would update the env block but not the SSM commands param.

3. **Dry-run output format for scoped path**
   - What we know: `runInitDryRun` prints a numbered step list. For scoped, we need something simpler.
   - Recommendation: When `dryRun=true`, print a one-liner: "Would apply: {module} [{tier}] ({env vars that would change})". Let the planner decide the exact format.

---

## Validation Architecture

> `workflow.nyquist_validation` is `true` in `.planning/config.json` — this section is included.

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` package (stdlib) |
| Config file | none (standard `go test ./...`) |
| Quick run command | `go test ./internal/app/cmd/ -run TestInitScoped -v` |
| Full suite command | `go test ./internal/app/cmd/ -timeout 120s` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| INIT-SCOPED-FLAG | `--only lambda-github-bridge` resolves to that module | unit | `go test ./internal/app/cmd/ -run TestScopedModuleResolution -v` | Wave 0 |
| INIT-SCOPED-FLAG | `--only unknown-module` errors and prints allowed set | unit | `go test ./internal/app/cmd/ -run TestScopedModuleRejection -v` | Wave 0 |
| INIT-SCOPED-ALIASES | `--github` resolves to `lambda-github-bridge` | unit | `go test ./internal/app/cmd/ -run TestScopedAliases -v` | Wave 0 |
| INIT-SCOPED-ALIASES | All 4 aliases map to expected module names | unit | `go test ./internal/app/cmd/ -run TestScopedAliases -v` | Wave 0 |
| INIT-SCOPED-GUARD | `--github --plan` returns error | unit | `go test ./internal/app/cmd/ -run TestScopedMutualExclusion -v` | Wave 0 |
| INIT-SCOPED-GUARD | `--github --sidecars` returns error | unit | `go test ./internal/app/cmd/ -run TestScopedMutualExclusion -v` | Wave 0 |
| INIT-SCOPED-GUARD | `--only ses` routes through plan gate before apply | unit | `go test ./internal/app/cmd/ -run TestScopedTier2Gate -v` | Wave 0 |
| INIT-SCOPED-GUARD | Tier-2 gate blocked: apply not called | unit | `go test ./internal/app/cmd/ -run TestScopedTier2GateBlocked -v` | Wave 0 |
| INIT-SCOPED-IMPL | `--dry-run=true` does not call runner.Apply | unit | `go test ./internal/app/cmd/ -run TestScopedDryRun -v` | Wave 0 |
| INIT-SCOPED-IMPL | `--dry-run=false` calls runner.Apply exactly once for the target module | unit | `go test ./internal/app/cmd/ -run TestScopedApply -v` | Wave 0 |
| INIT-SCOPED-IMPL | ExportTerragruntEnvVars called before apply (env vars set) | unit | `go test ./internal/app/cmd/ -run TestScopedEnvVarsExported -v` | Wave 0 |
| INIT-SCOPED-IMPL | ses preflight + Reconfigure called for `--only ses` | unit | `go test ./internal/app/cmd/ -run TestScopedSesPreflight -v` | Wave 0 |
| INIT-SCOPED-IMPL | No-drift invariant: scoped apply on stable install = no-op on full plan | manual UAT | `km init --github --dry-run=false` then `km init --plan` → no new trips | N/A |
| INIT-SCOPED-DOCS | Updated `klanker:init` SKILL.md has --github/--slack/--h1/--email | manual review | grep skill | Existing |

### Sampling Rate

- **Per task commit:** `go test ./internal/app/cmd/ -run TestInitScoped -v -timeout 30s`
- **Per wave merge:** `go test ./internal/app/cmd/ -timeout 120s`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `internal/app/cmd/init_scoped_test.go` — covers INIT-SCOPED-FLAG, INIT-SCOPED-ALIASES, INIT-SCOPED-GUARD, INIT-SCOPED-IMPL tests listed above

*(No new framework install needed — standard Go test infrastructure already in place.)*

---

## Sources

### Primary (HIGH confidence — direct source inspection)

- `internal/app/cmd/init.go` lines 1-3460 — `regionalModules()`, `NewInitCmd()`, `RunInitWithRunner()`, `RunInitPlanWithRunner()`, `ExportTerragruntEnvVars()`, `planModule()`, `runInitPartial()`, `runInitPlan()`, `lambdaConfigUpdater`, `ModuleTimeoutFunc`, `BuildLambdaZipsFunc`
- `internal/app/cmd/init_test.go` — `mockRunner` definition, `TestRunInitWithRunnerAllModules`, `TestRegionalModulesIncludesSSMDoc`
- `internal/app/cmd/init_plan_test.go` — `mockPlanRunner` definition
- `infra/live/use1/lambda-github-bridge/terragrunt.hcl` — dependency blocks (dynamodb-sandboxes, dynamodb-slack-nonces, dynamodb-github-threads), `get_env` calls, `mock_outputs_allowed_terraform_commands`
- `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` — dependency blocks, env-block inputs
- `infra/live/use1/lambda-h1-bridge/terragrunt.hcl` — dependency blocks
- `infra/live/use1/email-handler/terragrunt.hcl` — no dependency blocks, pure `get_env`
- `infra/live/use1/ses/terragrunt.hcl` — no dependency blocks, pure `get_env`
- `infra/modules/ses/v2.0.0/main.tf:64-136` — consolidated bucket policy, `aws_s3_bucket_policy.mail`
- `infra/modules/email-handler/v1.0.0/main.tf:247-260` — Lambda `environment { variables }` block confirmed
- `.planning/config.json` — `workflow.nyquist_validation: true`
- `skills/init/SKILL.md` — doc surface to update (Fast-path variants section)

### Secondary (MEDIUM confidence)

- `.planning/STATE.md` line 1669 — detailed rationale and decision history for Phase 105 (verbatim operator/Claude design session notes)
- `.planning/ROADMAP.md` lines 2403-2421 — Phase 105 goal, two-tier allowlist, requirements stub

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — no new libraries; everything is existing init.go patterns
- Architecture: HIGH — all function signatures and patterns verified from live source
- Pitfalls: HIGH — dependency resolution behavior confirmed from terragrunt.hcl inspection; ses policy confirmed from tf module source
- Test surface: HIGH — mockRunner/mockPlanRunner patterns verified from live test files

**Research date:** 2026-06-11
**Valid until:** 2026-07-11 (stable — no fast-moving external deps; init.go is in-tree)
