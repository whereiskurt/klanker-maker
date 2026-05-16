---
phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
plan: 07
type: execute
wave: 2
depends_on: [01, 02, 04]
files_modified:
  - internal/app/cmd/bootstrap.go
  - internal/app/cmd/doctor.go
  - internal/app/cmd/init.go
  - internal/app/cmd/doctor_ses_rules_test.go
  - go.mod
  - go.sum
autonomous: true
requirements:
  - SES-SHARED-RULESET
  - SES-DOCTOR-ORPHANS

must_haves:
  truths:
    - "A new `km bootstrap --shared-ses` flag (or new subcommand) exists that: (1) auto-detects whether `sandbox-email-shared` rule set already exists via `aws ses list-receipt-rule-sets`; (2) auto-detects whether the domain identity exists via `aws ses list-identities --identity-type Domain`; (3) sets `KM_REGISTER_SHARED_RULESET` and `KM_REGISTER_DOMAIN_IDENTITY` env vars accordingly; (4) calls `terragrunt apply` against `infra/live/use1/ses-shared-rule-set/`"
    - "The auto-detect logic is unit-testable via a `SESIdentityLister` interface (Phase 80 `OidcProviderLister` pattern) — mock-able in tests"
    - "A new `checkSESRules` function in `doctor.go` consumes the `SESReceiptRuleAPI` interface, lists rules in `sandbox-email-shared`, parses `${prefix}-` from each rule name, and returns OK / WARN with orphan list"
    - "`km doctor` wires `checkSESRules` into its standard run with `localPrefix = cfg.GetResourcePrefix()`"
    - "`go.mod` includes `github.com/aws/aws-sdk-go-v2/service/ses` (classic v1) for production wiring; the test file's build tag is removed and the test file compiles as part of the default build"
    - "Test stubs W0-06, W0-07, W0-08 pass without the build-tag scaffolding"
  artifacts:
    - path: "internal/app/cmd/bootstrap.go"
      provides: "km bootstrap --shared-ses flag/command + auto-detect logic + SESIdentityLister interface"
    - path: "internal/app/cmd/doctor.go"
      provides: "SESReceiptRuleAPI interface + checkSESRules function + wiring into doctor run"
    - path: "internal/app/cmd/doctor_ses_rules_test.go"
      provides: "Build-tag removed; production-ready test"
    - path: "go.mod"
      provides: "aws-sdk-go-v2/service/ses classic v1 dependency"
  key_links:
    - from: "internal/app/cmd/bootstrap.go"
      to: "infra/live/use1/ses-shared-rule-set/ (Plan 84-02 output)"
      via: "terragrunt apply with env-var inputs set"
      pattern: "ses-shared-rule-set"
    - from: "internal/app/cmd/doctor.go"
      to: "shared rule set's rules"
      via: "SES classic v1 DescribeReceiptRuleSet(\"sandbox-email-shared\")"
      pattern: "DescribeReceiptRuleSet"
---

<objective>
Add the new `km bootstrap --shared-ses` capability that operators run BEFORE their first `km init` on a fresh account (or whenever the shared SES state needs to be reconciled). Auto-detection follows Phase 80's `cluster-irsa` precedent (`register_oidc_provider = auto` via `OidcProviderLister` interface).

Add the `km doctor` SES-rules orphan check (RESEARCH § Pattern 3 + Example 4). Wire the new test file by removing its Wave 0 build-tag scaffolding.

Per CONTEXT.md "km bootstrap is a NEW subcommand (or new flag)": choose flag form (`km init --bootstrap-foundation`) OR subcommand form (`km bootstrap --shared-ses`). RESEARCH recommends adding to `km bootstrap` (existing subcommand at `internal/app/cmd/bootstrap.go`). Match Phase 80's pattern: introduce a new flag on the existing `km bootstrap` Cobra command.

Per CONTEXT.md "go.mod: add `github.com/aws/aws-sdk-go-v2/service/ses` (classic v1)": this plan owns the dependency addition.

Output:
- 3 source files modified + 1 file's build tag removed
- 1 new Go dependency
- 3 Wave 0 stubs (W0-06, W0-07, W0-08) turn GREEN
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-CONTEXT.md
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-RESEARCH.md
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-VALIDATION.md

@internal/app/cmd/bootstrap.go
@internal/app/cmd/cluster.go
@internal/app/cmd/doctor.go
@internal/app/cmd/doctor_ses_rules_test.go

<interfaces>
<!-- Two new interfaces + their wiring. -->

`SESIdentityLister` (in bootstrap.go) — narrow interface, Phase 80 precedent:

```go
type SESIdentityLister interface {
    ListReceiptRuleSets(ctx context.Context, in *ses.ListReceiptRuleSetsInput, optFns ...func(*ses.Options)) (*ses.ListReceiptRuleSetsOutput, error)
    ListIdentities(ctx context.Context, in *sesv2.ListEmailIdentitiesInput, optFns ...func(*sesv2.Options)) (*sesv2.ListEmailIdentitiesOutput, error)
}
// (Or: split into SESRuleSetLister + SESIdentityLister if cleaner. Both readonly.)

// Returns: (registerSharedRuleSet bool, registerDomainIdentity bool, err error)
func detectSharedSESState(ctx context.Context, lister SESIdentityLister, ruleSetName, domain string) (bool, bool, error)
```

**Field-name verification (per plan-checker iteration 1):** Use `cfg.Domain` (NOT `cfg.ParentDomain`) and `cfg.EmailSubdomain` — see `internal/app/cmd/configure.go` lines 22-33. Executor should re-grep before coding.

`SESReceiptRuleAPI` (in doctor.go) — narrow interface, RESEARCH § Pattern 3:

```go
type SESReceiptRuleAPI interface {
    DescribeReceiptRuleSet(ctx context.Context, params *ses.DescribeReceiptRuleSetInput, optFns ...func(*ses.Options)) (*ses.DescribeReceiptRuleSetOutput, error)
}

func checkSESRules(ctx context.Context, client SESReceiptRuleAPI, localPrefix string) CheckResult
```

`km bootstrap --shared-ses` flag wiring (in bootstrap.go's existing Cobra setup):
- Add `bootstrapCmd.Flags().Bool("shared-ses", false, "Provision the account-shared SES rule set + domain identity (Phase 84)")`.
- In the run function: if `--shared-ses` set, call `detectSharedSESState`, set env vars (`KM_REGISTER_SHARED_RULESET`, `KM_REGISTER_DOMAIN_IDENTITY`, `KM_EMAIL_SUBDOMAIN`, `KM_PARENT_DOMAIN`, `KM_HOSTED_ZONE_ID`), run `terragrunt apply --auto-approve` against `infra/live/use1/ses-shared-rule-set/`.
- Honor existing `--dry-run` flag (terragrunt plan vs apply).
- Honor existing `ExportConfigEnvVars` requirement (MEMORY note: `terragrunt env export required`).
- Log step-level summaries per OPER-01.

Phase 80 reference (`internal/app/cmd/cluster.go`): mirror the structure of `runClusterAdd`'s OIDC-provider auto-detect flow, including:
- `--register-shared-rule-set auto|true|false` flag override
- Verbose mode flag for diagnostics
- "Creating" vs "Reusing existing" log lines based on the detection result

go.mod addition:
```bash
go get github.com/aws/aws-sdk-go-v2/service/ses
```
This is the classic v1 SES SDK module (distinct from existing `sesv2`). Required by both bootstrap.go's `SESIdentityLister.ListReceiptRuleSets` and doctor.go's `SESReceiptRuleAPI.DescribeReceiptRuleSet`.

`doctor_ses_rules_test.go`:
- Remove the `//go:build phase84_doctor` build tag added in Plan 84-01.
- The test now compiles against the real interface in `doctor.go`.
- Update mock to match the actual interface signature.

`doctor.go` wiring (where existing checks are registered):
- Add `checkSESRules` to the standard doctor run, similar to how `checkSESIdentity` is wired.
- The function reads `cfg.GetResourcePrefix()` to determine `localPrefix`.
- WARN on orphans, OK on all-own (matches CONTEXT.md decision).
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Add aws-sdk-go-v2/service/ses dependency + SESReceiptRuleAPI interface + checkSESRules function</name>
  <files>internal/app/cmd/doctor.go, internal/app/cmd/doctor_ses_rules_test.go, go.mod, go.sum</files>
  <behavior>
    - `go get github.com/aws/aws-sdk-go-v2/service/ses` succeeds and appears in go.mod.
    - `doctor.go` declares `SESReceiptRuleAPI` (narrow interface) and `checkSESRules(ctx, client, localPrefix) CheckResult`.
    - `checkSESRules` returns `Status: StatusOK` when all rules' `${prefix}-` matches `localPrefix`; returns `Status: StatusWarn` with comma-joined orphan names when at least one rule's prefix mismatches.
    - The new function is registered in `doctor`'s standard check list with `localPrefix = cfg.GetResourcePrefix()`.
    - `doctor_ses_rules_test.go` no longer has a build tag; the test compiles in the default build.
    - Test stubs W0-06, W0-07, W0-08 pass.
  </behavior>
  <action>
1. Run `go get github.com/aws/aws-sdk-go-v2/service/ses` from repo root. Confirm go.mod has the new line.

2. Open `internal/app/cmd/doctor.go`. Near the existing `checkSESIdentity` function (~line 881-927 per RESEARCH), add:

```go
// SESReceiptRuleAPI covers SES classic-v1 DescribeReceiptRuleSet for the orphan-rule check (Phase 84).
type SESReceiptRuleAPI interface {
    DescribeReceiptRuleSet(ctx context.Context, params *ses.DescribeReceiptRuleSetInput, optFns ...func(*ses.Options)) (*ses.DescribeReceiptRuleSetOutput, error)
}

func checkSESRules(ctx context.Context, client SESReceiptRuleAPI, localPrefix string) CheckResult {
    if client == nil {
        return CheckResult{Name: "SES rules", Status: StatusSkipped, Message: "SES classic SDK client unavailable"}
    }
    out, err := client.DescribeReceiptRuleSet(ctx, &ses.DescribeReceiptRuleSetInput{
        RuleSetName: aws.String("sandbox-email-shared"),
    })
    if err != nil {
        return CheckResult{
            Name: "SES rules",
            Status: StatusError,
            Message: fmt.Sprintf("DescribeReceiptRuleSet: %v", err),
            Remediation: "Verify the shared rule set exists; run `km bootstrap --shared-ses`",
        }
    }
    orphans := []string{}
    own := 0
    for _, r := range out.Rules {
        n := aws.ToString(r.Name)
        idx := strings.Index(n, "-")
        if idx < 0 {
            orphans = append(orphans, n)
            continue
        }
        prefix := n[:idx]
        if prefix == localPrefix {
            own++
        } else {
            orphans = append(orphans, n)
        }
    }
    if len(orphans) > 0 {
        return CheckResult{
            Name: "SES rules",
            Status: StatusWarn,
            Message: fmt.Sprintf("orphan SES rules: %s", strings.Join(orphans, ", ")),
            Remediation: "Other installs' rules — if no install owns them, delete via `aws ses delete-receipt-rule --rule-set-name sandbox-email-shared --rule-name <name>`",
        }
    }
    return CheckResult{Name: "SES rules", Status: StatusOK, Message: fmt.Sprintf("✓ SES rules healthy (%d rules for prefix %q)", own, localPrefix)}
}
```

3. Add `import "github.com/aws/aws-sdk-go-v2/service/ses"` and `"github.com/aws/aws-sdk-go-v2/service/ses/types"` if needed (the input/output types live in the classic SDK module). Confirm `aws.String` / `aws.ToString` come from the existing `github.com/aws/aws-sdk-go-v2/aws` import.

4. Wire `checkSESRules` into the doctor's standard check pipeline. Find where `checkSESIdentity` is invoked and add a sibling invocation. Construct the classic SES v1 client from the existing AWS config the doctor builds at the top of its run (look for `awsConfig.LoadDefaultConfig`).

5. Open `internal/app/cmd/doctor_ses_rules_test.go`. Remove the `//go:build phase84_doctor` build-tag line at the top of the file. Adjust the mock's type signatures to match the production `SESReceiptRuleAPI`.

6. Run `gofmt -w` on edited files. Run `go vet ./internal/app/cmd/...`. Run W0-06, W0-07, W0-08.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go mod tidy && grep -q "aws-sdk-go-v2/service/ses " go.mod && echo "ses sdk in go.mod" && go test ./internal/app/cmd/ -run 'TestCheckSESRules' -count=1 2>&1 | tail -15</automated>
  </verify>
  <done>SES classic SDK in go.mod. `checkSESRules` exists and is wired into doctor's standard run. W0-06, W0-07, W0-08 all pass. `go vet` clean.</done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Add km bootstrap --shared-ses flag with auto-detect logic</name>
  <files>internal/app/cmd/bootstrap.go</files>
  <behavior>
    - `km bootstrap --shared-ses` runs the auto-detect: calls `ses.ListReceiptRuleSets`, sets `KM_REGISTER_SHARED_RULESET=false` if `sandbox-email-shared` is present (otherwise `true`); calls `sesv2.ListEmailIdentities` (or classic SES `ListIdentities`), sets `KM_REGISTER_DOMAIN_IDENTITY=false` if the target domain identity already exists (otherwise `true`).
    - The command then invokes terragrunt against `infra/live/use1/ses-shared-rule-set/` with `apply --auto-approve` (or `plan` when `--dry-run`).
    - `ExportConfigEnvVars(cfg)` is called before terragrunt invocation (MEMORY note `terragrunt env export required`).
    - A `detectSharedSESState` helper exists with a `SESIdentityLister` interface for unit testing.
    - At least one unit test exercises the auto-detect logic with a mock `SESIdentityLister`: when the rule set already exists, `registerSharedRuleSet` returns `false`; when the domain identity does not exist, `registerDomainIdentity` returns `true`.
  </behavior>
  <action>
1. Open `internal/app/cmd/bootstrap.go`. Find the existing Cobra command definition for `km bootstrap`.

2. Add a new flag:
   ```go
   bootstrapCmd.Flags().Bool("shared-ses", false, "Provision the account-shared SES rule set + domain identity (Phase 84)")
   ```

3. In the run function, detect the new flag. When set, run the SHARED-SES workflow (in addition to or alongside the existing bootstrap steps — confirm via README/docs whether `--shared-ses` is the only mode or augments the default):

```go
sharedSES, _ := cmd.Flags().GetBool("shared-ses")
if sharedSES {
    if err := exportConfigEnvVars(cfg); err != nil { return err }

    sesClient := ses.NewFromConfig(awsCfg)
    sesv2Client := sesv2.NewFromConfig(awsCfg)
    domain := fmt.Sprintf("%s.%s", cfg.EmailSubdomain, cfg.Domain)
    registerRS, registerID, err := detectSharedSESState(ctx, &realSESLister{ses: sesClient, sesv2: sesv2Client}, "sandbox-email-shared", domain)
    if err != nil { return err }

    fmt.Fprintf(os.Stderr, "Shared SES rule set: %s\n", ternary(registerRS, "creating", "reusing existing"))
    fmt.Fprintf(os.Stderr, "Shared SES domain identity: %s\n", ternary(registerID, "creating", "reusing existing"))

    os.Setenv("KM_REGISTER_SHARED_RULESET", strconv.FormatBool(registerRS))
    os.Setenv("KM_REGISTER_DOMAIN_IDENTITY", strconv.FormatBool(registerID))
    // KM_EMAIL_SUBDOMAIN, KM_PARENT_DOMAIN, KM_HOSTED_ZONE_ID are set by ExportConfigEnvVars.

    return runTerragruntApply(ctx, "infra/live/use1/ses-shared-rule-set", dryRun, verbose)
}
```

4. Define `detectSharedSESState` and `SESIdentityLister` near the top of bootstrap.go (or in a new helper file `internal/app/cmd/bootstrap_ses.go` if bootstrap.go is already large):

```go
type SESIdentityLister interface {
    ListReceiptRuleSets(ctx context.Context, in *ses.ListReceiptRuleSetsInput, optFns ...func(*ses.Options)) (*ses.ListReceiptRuleSetsOutput, error)
    ListEmailIdentities(ctx context.Context, in *sesv2.ListEmailIdentitiesInput, optFns ...func(*sesv2.Options)) (*sesv2.ListEmailIdentitiesOutput, error)
}

type realSESLister struct {
    ses   *ses.Client
    sesv2 *sesv2.Client
}
func (r *realSESLister) ListReceiptRuleSets(ctx context.Context, in *ses.ListReceiptRuleSetsInput, optFns ...func(*ses.Options)) (*ses.ListReceiptRuleSetsOutput, error) {
    return r.ses.ListReceiptRuleSets(ctx, in, optFns...)
}
func (r *realSESLister) ListEmailIdentities(ctx context.Context, in *sesv2.ListEmailIdentitiesInput, optFns ...func(*sesv2.Options)) (*sesv2.ListEmailIdentitiesOutput, error) {
    return r.sesv2.ListEmailIdentities(ctx, in, optFns...)
}

func detectSharedSESState(ctx context.Context, lister SESIdentityLister, ruleSetName, emailDomain string) (registerRS, registerID bool, err error) {
    // Default: create both.
    registerRS, registerID = true, true

    rsOut, err := lister.ListReceiptRuleSets(ctx, &ses.ListReceiptRuleSetsInput{})
    if err != nil {
        return registerRS, registerID, fmt.Errorf("ListReceiptRuleSets: %w", err)
    }
    for _, rs := range rsOut.RuleSets {
        if aws.ToString(rs.Name) == ruleSetName {
            registerRS = false
            break
        }
    }

    idOut, err := lister.ListEmailIdentities(ctx, &sesv2.ListEmailIdentitiesInput{})
    if err != nil {
        return registerRS, registerID, fmt.Errorf("ListEmailIdentities: %w", err)
    }
    for _, id := range idOut.EmailIdentities {
        if id.IdentityType == sesv2types.IdentityTypeDomain && aws.ToString(id.IdentityName) == emailDomain {
            registerID = false
            break
        }
    }
    return registerRS, registerID, nil
}
```

5. Add a small unit test for `detectSharedSESState` in `bootstrap_test.go` (or a new file `bootstrap_ses_test.go`):
   - Mock `SESIdentityLister` returning both items present → expect `(false, false, nil)`.
   - Mock returning rule set but not domain identity → expect `(false, true, nil)`.
   - Mock returning nothing → expect `(true, true, nil)`.

6. Run `gofmt -w`. Run `go vet`. Run `go build ./...`. Run `make build` (per `feedback_rebuild_km`).
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go test ./internal/app/cmd/ -run TestDetectSharedSESState -count=1 2>&1 | tail -10 && go build ./... 2>&1 | tail -5 && ./km bootstrap --help 2>&1 | grep -q "shared-ses" && echo "flag is registered"</automated>
  </verify>
  <done>`km bootstrap --shared-ses` flag exists and shows in help. `detectSharedSESState` unit-testable and tested with at least 3 scenarios. `go build` clean. `make build` produces a km binary that exposes the flag.</done>
</task>


<task type="auto" tdd="true">
  <name>Task 3: Add regional ses-module preflight check (Major 7 — drift gate)</name>
  <files>internal/app/cmd/init.go</files>
  <behavior>
    - Before `km init` runs `terragrunt apply` against `infra/live/use1/ses/` (the regional v2.0.0 module), it calls `ses.ListReceiptRuleSets` to verify the shared rule set `sandbox-email-shared` exists.
    - When NOT present: emit a clear error to stderr (`"Foundation SES rule set 'sandbox-email-shared' not found. Run 'km bootstrap --shared-ses' first on a fresh account."`) and exit non-zero BEFORE invoking terragrunt.
    - When present: continue with the existing `km init` flow unchanged.
    - The preflight reuses the `SESIdentityLister` interface from Task 2 — no new interface, no duplicated SDK plumbing.
    - At least one unit test covers the "rule set missing → error" branch using a mock lister.
  </behavior>
  <action>
1. Open `internal/app/cmd/init.go`. Locate where the regional `ses` module's terragrunt apply is invoked (it will be in the `regionalModules` ordering — SES is the last entry per Phase 82-B1).

2. Immediately before the SES-module apply step, add a preflight check:

```go
// Phase 84: Foundation rule set must exist before regional rules can attach.
// The regional v2.0.0 ses module references `sandbox-email-shared` as a string
// constant (no Terraform data source for SES rule sets exists in the AWS
// provider). If the operator hasn't run `km bootstrap --shared-ses` yet, the
// regional terragrunt apply will fail mid-flight with `RuleSetDoesNotExist`.
// Fail fast with a clear actionable message instead.
sesClient := ses.NewFromConfig(awsCfg)
lister := &realSESLister{ses: sesClient, sesv2: sesv2.NewFromConfig(awsCfg)}
domain := fmt.Sprintf("%s.%s", cfg.EmailSubdomain, cfg.Domain)
registerRS, _, err := detectSharedSESState(ctx, lister, "sandbox-email-shared", domain)
if err != nil {
    return fmt.Errorf("ses preflight: %w", err)
}
if registerRS {
    // registerRS=true means the rule set is NOT present.
    return fmt.Errorf("Foundation SES rule set 'sandbox-email-shared' not found. Run 'km bootstrap --shared-ses' first on a fresh account.")
}
```

3. The preflight only runs for the SES module step — not for other regional modules. Wrap it conditionally so it doesn't fire on every `km init` step.

4. Add a unit test exercising the missing-rule-set branch with the mock `SESIdentityLister` from Task 2's test file:
   - Mock returns empty `RuleSets`.
   - Expect: the preflight returns the actionable error string.
   - Expect: terragrunt is NOT invoked.

5. Run `gofmt -w`. Run `go vet`. Run `go build ./...`. Run `make build`.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go test ./internal/app/cmd/ -run 'TestInitSESPreflight|TestKmInit.*Preflight' -count=1 2>&1 | tail -10 && go build ./... 2>&1 | tail -3</automated>
  </verify>
  <done>`km init` fails fast with the actionable error when the shared rule set is missing. Preflight unit-tested. No regression for the happy path (shared rule set present → preflight passes, terragrunt apply proceeds).</done>
</task>

</tasks>

<verification>
- W0-06, W0-07, W0-08 stubs from Plan 84-01 turn GREEN (build tag removed; test compiles in default build).
- `km bootstrap --shared-ses --help` shows the flag.
- `detectSharedSESState` has at least 3 unit tests for the (create, reuse), (reuse, create), (create, create) scenarios.
- go.mod includes `aws-sdk-go-v2/service/ses` (classic v1).
- `go build ./...` and `make build` succeed.
</verification>

<success_criteria>
- Plan 84-02's foundation module now has a code-side driver in `km bootstrap --shared-ses`.
- `km doctor` reports `✓ SES rules healthy` or `⚠ orphan SES rules: ...` per CONTEXT.md.
- All three doctor-related Wave 0 stubs pass.
</success_criteria>

<output>
After completion, create `.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-07-SUMMARY.md`
</output>
