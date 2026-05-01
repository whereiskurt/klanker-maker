---
phase: 6-fix-github-app-installation-id-resolutio
plan: 1
type: execute
wave: 1
depends_on: []
files_modified:
  - internal/app/cmd/create.go
  - internal/app/cmd/create_github_test.go
autonomous: true
requirements:
  - GH-FIX-01  # Wildcard-only allowedRepos resolves an unambiguous installation
  - GH-FIX-02  # Multiple installations + wildcard-only surfaces a clear ambiguity error
  - GH-FIX-03  # Zero installations + wildcard-only preserves legacy fallback
  - GH-FIX-04  # Concrete-owner code path unchanged (regression guard)
  - GH-FIX-05  # Caller differentiates ambiguity from "not configured"

must_haves:
  truths:
    - "km create profiles/learn.yaml writes a non-empty token to /sandbox/{id}/github-token in SSM (when exactly one installation exists under /km/config/github/installations/)"
    - "git clone of an org repo from inside the learn.yaml sandbox succeeds with no username/password prompt"
    - "When two or more per-owner installations exist and allowedRepos is wildcard-only, km create prints a loud ⚠ warning naming the candidate owners and the two suggested fixes"
    - "When zero per-owner installations exist and allowedRepos is wildcard-only, the legacy /km/config/github/installation-id key is honored exactly as before"
    - "Concrete owner-prefixed entries (e.g., whereiskurt/foo) still resolve via the per-owner key — no regression"
    - "Existing tests in create_github_test.go all still pass"
  artifacts:
    - path: "internal/app/cmd/create.go"
      provides: "ErrAmbiguousInstallation sentinel + extended resolveInstallationID with GetParametersByPath enumeration + differentiated caller warning"
      contains: "ErrAmbiguousInstallation"
    - path: "internal/app/cmd/create_github_test.go"
      provides: "Five new resolveInstallationID test cases + regression guard"
      contains: "TestResolveInstallationID_WildcardOnly"
  key_links:
    - from: "resolveInstallationID (create.go)"
      to: "ssmClient.GetParametersByPath"
      via: "Path=/km/config/github/installations/, Recursive=false, WithDecryption=true"
      pattern: "GetParametersByPath"
    - from: "create.go:994 caller block"
      to: "ErrAmbiguousInstallation typed error"
      via: "errors.As — branch ⚠ warning vs ⊘ skip"
      pattern: "errors.As.*ErrAmbiguousInstallation"
    - from: "SSMGetPutAPI interface (create.go:60-63)"
      to: "GetParametersByPath method"
      via: "interface extension"
      pattern: "GetParametersByPath"
---

<objective>
Fix GitHub App installation-ID resolution so `learn.yaml` (with `allowedRepos: ["*"]`) successfully provisions a token, allowing `git clone` inside the sandbox without credential prompts.

Purpose: `extractRepoOwner("*")` returns "", causing `resolveInstallationID` to skip per-owner lookup and fall through to the unset legacy key, which returns `ErrGitHubNotConfigured`. The caller silently prints `⊘ GitHub token: skipped`, no token lands in SSM, and the GIT_ASKPASS helper has nothing to feed git. Wildcard-only deployments are currently broken whenever the legacy single-installation key is unset.

Output: Extended `resolveInstallationID` that enumerates `/km/config/github/installations/` when no concrete owner is found in `allowedRepos`; a new `ErrAmbiguousInstallation` sentinel for the multi-installation case; a differentiated caller warning at the create.go:994 site; five new unit tests covering all wildcard branches plus a regression test for the concrete-owner path.
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@CLAUDE.md
@internal/app/cmd/create.go
@internal/app/cmd/create_github_test.go
@pkg/github/token.go
@pkg/compiler/userdata.go
@profiles/learn.yaml

<interfaces>
<!-- Existing interfaces and code the executor needs verbatim. -->

From internal/app/cmd/create.go:55-63 (sentinel + interface — interface MUST be extended):
```go
var ErrGitHubNotConfigured = errors.New("GitHub App not configured in SSM — run 'km configure github' first")

// SSMGetPutAPI is a narrow interface covering the SSM operations used by
// generateAndStoreGitHubToken. *ssm.Client satisfies this interface.
type SSMGetPutAPI interface {
    GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
    PutParameter(ctx context.Context, params *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
}
```

From internal/app/cmd/create.go:1874-1885 (helper — DO NOT modify):
```go
// extractRepoOwner returns the owner portion of a "owner/repo" string.
// Returns empty string for bare repos (no slash), wildcards ("*"), and empty strings.
func extractRepoOwner(repo string) string {
    if repo == "" || repo == "*" {
        return ""
    }
    parts := strings.SplitN(repo, "/", 2)
    if len(parts) < 2 {
        return ""
    }
    return parts[0]
}
```

From internal/app/cmd/create.go:1894-1935 (function to MODIFY):
```go
func resolveInstallationID(ctx context.Context, ssmClient SSMGetPutAPI, allowedRepos []string) (string, error) {
    // ... extracts firstOwner; if found, tries /km/config/github/installations/{owner};
    //     otherwise falls through to legacy /km/config/github/installation-id
    //     and returns ErrGitHubNotConfigured if both miss.
    // FIX: when no concrete owner found AND legacy key is missing, enumerate
    // /km/config/github/installations/ via GetParametersByPath BEFORE returning
    // ErrGitHubNotConfigured. (Strategy: enumerate first when no concrete owner —
    // see Task 2 action for exact ordering.)
}
```

From internal/app/cmd/create.go:980-1003 (caller — MUST be updated for differentiated warning):
```go
instID, tokenErr := generateAndStoreGitHubToken(ctx, ssmClient, sandboxID, kmsKeyARN, gh.AllowedRepos, nil)
if tokenErr != nil {
    if errors.Is(tokenErr, ErrGitHubNotConfigured) {
        fmt.Printf("  ⊘ GitHub token: skipped (not configured)\n")
    } else {
        log.Warn().Err(tokenErr).Str("sandbox_id", sandboxID).
            Msg("Step 13a: GitHub App token generation failed (non-fatal — sandbox is provisioned)")
    }
}
```

From pkg/github/token.go:119-130 (DO NOT modify — wildcard handling already correct):
```go
// Wildcard "*" means all repos — omit the repositories field so GitHub
// scopes the token to all repos the installation can access.
var shortNames []string
for _, r := range repos {
    if r == "*" {
        shortNames = nil
        break
    }
    shortNames = append(shortNames, repoShortName(r))
}
```

From internal/app/cmd/doctor.go:119-123 (idiomatic SSM enum interface — copy this style):
```go
// SSMReadAPI covers SSM GetParameter and GetParametersByPath.
type SSMReadAPI interface {
    GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
    GetParametersByPath(ctx context.Context, params *ssm.GetParametersByPathInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
}
```

From internal/app/cmd/doctor.go:468-481 (idiomatic GetParametersByPath usage — copy this pattern):
```go
pathOut, err := client.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
    Path: awssdk.String(installationsPathPrefix),
})
if err == nil && len(pathOut.Parameters) > 0 {
    var accounts []string
    for _, p := range pathOut.Parameters {
        name := awssdk.ToString(p.Name)
        parts := strings.Split(name, "/")
        if len(parts) > 0 {
            accounts = append(accounts, parts[len(parts)-1])
        }
    }
    sort.Strings(accounts)
    // ...
}
```

From internal/app/cmd/create_github_test.go:21-49 (existing mock — MUST be extended with GetParametersByPath):
```go
type mockSSMGetPut struct {
    getResults map[string]mockSSMResult
}
func (m *mockSSMGetPut) GetParameter(...) (...) { ... }
func (m *mockSSMGetPut) PutParameter(...) (...) { ... }
// Add: GetParametersByPath method backed by a new field, e.g.:
//   pathResults map[string][]ssmtypes.Parameter  // keyed by Path input
```
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: RED — Add failing tests for wildcard-only installation enumeration + ambiguity warning</name>
  <files>internal/app/cmd/create_github_test.go</files>
  <behavior>
    Five new tests + one mock extension. All MUST fail before implementation lands.

    Mock extension (in create_github_test.go):
    - Add field `pathResults map[string][]ssmtypes.Parameter` to `mockSSMGetPut` (keyed by full Path string, e.g. `/km/config/github/installations/`).
    - Add method `GetParametersByPath(ctx, params, ...) (*ssm.GetParametersByPathOutput, error)` that returns `pathResults[*params.Path]` wrapped in `&ssm.GetParametersByPathOutput{Parameters: ...}`. Return empty slice (not nil) when key absent. Use `awssdk.String(name)` when constructing parameter Names.

    Test cases (all in create_github_test.go, named exactly as below):

    1. `TestResolveInstallationID_WildcardOnly_SingleInstallation_ReturnsIt`
       - `allowedRepos: ["*"]`, no legacy key configured (ParameterNotFound), pathResults has exactly one entry: Name=`/km/config/github/installations/whereiskurt`, Value=`555555`.
       - Expect: returns "555555", nil.

    2. `TestResolveInstallationID_WildcardOnly_MultipleInstallations_ReturnsAmbiguous`
       - `allowedRepos: ["*"]`, pathResults has TWO entries: `/km/config/github/installations/orgA` (val "111") and `/km/config/github/installations/orgB` (val "222"). Legacy key may or may not be set — should not be consulted before enumeration.
       - Expect: returns ("", err) where `errors.Is(err, ErrAmbiguousInstallation)` is true (sentinel) OR `errors.As` recovers a typed error exposing a `Candidates() []string` method (or exported `Candidates` slice field) containing both "orgA" and "orgB" sorted.
       - Expect: error message contains both candidate names AND mentions the legacy key fix and the owner-prefixed `allowedRepos` fix.

    3. `TestResolveInstallationID_WildcardOnly_NoInstallations_LegacySet_ReturnsLegacy`
       - `allowedRepos: ["*"]`, pathResults empty for installations prefix, legacy key value="legacy-99".
       - Expect: returns "legacy-99", nil.

    4. `TestResolveInstallationID_WildcardOnly_NoInstallations_NoLegacy_ReturnsNotConfigured`
       - `allowedRepos: ["*"]`, pathResults empty, legacy ParameterNotFound.
       - Expect: `errors.Is(err, ErrGitHubNotConfigured)` is true.

    5. `TestResolveInstallationID_ConcreteOwner_StillUsesPerOwnerKey_RegressionGuard`
       - `allowedRepos: ["whereiskurt/foo"]`, per-owner key `/km/config/github/installations/whereiskurt` value="333". pathResults need not be set (must NOT be consulted on this code path).
       - Expect: returns "333", nil. **Must NOT call GetParametersByPath** — assert by leaving `pathResults` nil; if implementation calls it, the mock should panic OR a separate counter field on the mock (e.g., `pathCallCount int`) MUST remain 0.

    Caller-warning source-level test:

    6. Extend `TestCreateGitHubSkip_CallerPrintsSkipMessage` (or add `TestCreateGitHubCaller_DifferentiatesAmbiguity`) to assert create.go contains:
       - `ErrAmbiguousInstallation` (the new sentinel/type name appears in source)
       - `errors.As(tokenErr, ` (using errors.As, not just errors.Is — typed-error branch)
       - `⚠` (loud warning glyph) appears near a branch handling the ambiguity case
       - The string "set " or "add " referencing the two suggested fixes (legacy key OR owner-prefixed entry)
  </behavior>
  <action>
    1. Open `internal/app/cmd/create_github_test.go`.
    2. Extend `mockSSMGetPut` with the `pathResults` field and the `GetParametersByPath` method described in the behavior block. Mirror the pattern in `internal/app/cmd/doctor_test.go:116-127` for return shape.
    3. Add the five new `TestResolveInstallationID_*` test functions exactly as specified, each using a freshly constructed `mockSSMGetPut` populated only with what the case needs.
    4. Extend the caller source-level test (or add a new one) to assert the differentiated ambiguity warning markers in create.go.
    5. Run: `go test ./internal/app/cmd/ -run 'TestResolveInstallationID_WildcardOnly|TestResolveInstallationID_ConcreteOwner_StillUsesPerOwnerKey|TestCreateGitHubCaller_DifferentiatesAmbiguity' -v 2>&1 | tail -40`.
    6. Verify ALL FIVE new resolveInstallationID tests fail (compile error from missing `ErrAmbiguousInstallation` and `GetParametersByPath` on the interface is acceptable — that IS the red signal). The caller source-level test MUST also fail (`⚠` and `errors.As(tokenErr,` not yet present).
    7. Commit: `test(6): add failing tests for wildcard-only installation enumeration` (atomic RED commit per operator hint).

    Do NOT add `ErrAmbiguousInstallation` to create.go in this task. Do NOT extend `SSMGetPutAPI` in this task. Do NOT modify `resolveInstallationID` in this task. The compile failure on the new mock method or missing sentinel is the desired RED state — if the package fails to compile, that's still the red signal as long as it is caused by the new test code referencing yet-to-exist symbols.
  </action>
  <verify>
    <automated>go test ./internal/app/cmd/ -run 'TestResolveInstallationID|TestCreateGitHubCaller_DifferentiatesAmbiguity' -v 2>&1 | grep -E '(FAIL|PASS|undefined|cannot use)' | head -20</automated>
  </verify>
  <done>
    - `create_github_test.go` contains the five new test functions with exact names listed in `<behavior>`.
    - `mockSSMGetPut` has `pathResults` field and `GetParametersByPath` method.
    - All five new resolveInstallationID tests fail (or the package fails to compile because `ErrAmbiguousInstallation` is referenced but not yet defined — equivalent RED).
    - The seven existing tests in create_github_test.go (TestCreateGitHubSkip_*, TestExtractRepoOwner, TestResolveInstallationID_PerAccountFound, etc.) are unchanged in intent — only the mock struct is extended in a backward-compatible way.
    - Single test commit landed.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: GREEN — Extend SSMGetPutAPI, add ErrAmbiguousInstallation, enumerate installations in resolveInstallationID</name>
  <files>internal/app/cmd/create.go</files>
  <behavior>
    Implementation that turns all five new tests + the caller source test from Task 1 GREEN, while keeping all existing tests passing.

    1. Extend `SSMGetPutAPI` (create.go:60-63) with `GetParametersByPath`. Method signature must match `*ssm.Client` exactly:
       ```go
       GetParametersByPath(ctx context.Context, params *ssm.GetParametersByPathInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
       ```
       This is additive; `*ssm.Client` already satisfies it.

    2. Add a new sentinel/typed error near `ErrGitHubNotConfigured` (create.go:55-56). Recommended typed error so callers can recover the candidate list:
       ```go
       // ErrAmbiguousInstallation is returned by resolveInstallationID when allowedRepos
       // contains only wildcards and multiple per-owner installation parameters exist
       // under /km/config/github/installations/. The Candidates field lists owner names
       // (the trailing path segment of each parameter, sorted).
       type ErrAmbiguousInstallation struct {
           Candidates []string
       }
       func (e *ErrAmbiguousInstallation) Error() string {
           return fmt.Sprintf(
               "ambiguous GitHub App installation: found %d candidates (%s); "+
                   "either set /km/config/github/installation-id to disambiguate, "+
                   "or add an owner-prefixed entry like \"%s/*\" to spec.sourceAccess.github.allowedRepos",
               len(e.Candidates), strings.Join(e.Candidates, ", "), e.Candidates[0])
       }
       ```
       Pointer receiver on `Error()`; callers use `errors.As(err, &target)` where `var target *ErrAmbiguousInstallation`.

    3. Modify `resolveInstallationID` (create.go:1894-1935). New ordering when no concrete owner is extracted from allowedRepos (firstOwner == ""):
       - Step A: Call `ssmClient.GetParametersByPath` with `Path = aws.String("/km/config/github/installations/")`, `Recursive = aws.Bool(false)`, `WithDecryption = aws.Bool(true)`.
       - Step B: On nil error AND `len(pathOut.Parameters) == 1`: return `*pathOut.Parameters[0].Value, nil`.
       - Step C: On nil error AND `len(pathOut.Parameters) >= 2`: extract owner names (last path segment of each parameter Name, e.g. `/km/config/github/installations/orgA` → `orgA`), sort, return `&ErrAmbiguousInstallation{Candidates: owners}`. Use `strings.TrimPrefix` then `strings.Split` or last-segment extraction matching doctor.go:474-480.
       - Step D: On nil error AND zero parameters: fall through to existing legacy lookup logic exactly as before.
       - Step E: On non-nil error from GetParametersByPath: log/wrap and fall through to legacy lookup (do not block on enumeration failure — preserve graceful degradation; an `AccessDenied` here should NOT mask a working legacy key).

       The concrete-owner code path (firstOwner != "") MUST remain untouched — wrap the new enumeration logic in an `else` branch or guard so concrete-owner lookups never call `GetParametersByPath`. This preserves the regression test from Task 1 case 5.

       Preserve existing comment block above the function; update it to document the new wildcard branch.

    4. Run all tests in the package and confirm GREEN.

    5. Commit: `feat(6): resolve wildcard-only installation IDs via SSM enumeration`.
  </behavior>
  <action>
    1. Open `internal/app/cmd/create.go`.
    2. Add `GetParametersByPath` to the `SSMGetPutAPI` interface (lines 60-63). Match the exact signature on `*ssm.Client`.
    3. Add `ErrAmbiguousInstallation` typed error immediately below `ErrGitHubNotConfigured` (around line 56). Use a pointer receiver on `Error()`. Ensure imports include `strings` and `fmt` (already present in this file).
    4. Refactor `resolveInstallationID` (lines 1894-1935) per the behavior block. Keep `firstOwner != ""` path identical. Add the wildcard-enumeration branch when `firstOwner == ""`.
    5. Verify `aws` and `ssmtypes` are already imported (they are — both used elsewhere in the file). No new imports expected aside from what's already there.
    6. Run: `go build ./...` to confirm compile.
    7. Run: `go test ./internal/app/cmd/ -run 'TestResolveInstallationID|TestCreateGitHubSkip|TestExtractRepoOwner|TestGenerateAndStoreGitHubToken' -v 2>&1 | tail -60`.
    8. Confirm: all five new tests PASS; all seven pre-existing tests still PASS; no others regress.
    9. Run the broader suite to check for unintended breakage: `go test ./internal/app/cmd/ 2>&1 | tail -20`.
    10. Commit: `feat(6): resolve wildcard-only installation IDs via SSM enumeration`.

    Do NOT modify `pkg/github/token.go` (already handles wildcard at exchange-time correctly).
    Do NOT modify the caller block at create.go:980-1003 in this task — that is Task 3.
  </action>
  <verify>
    <automated>go build ./... && go test ./internal/app/cmd/ -run 'TestResolveInstallationID|TestCreateGitHubSkip|TestExtractRepoOwner|TestGenerateAndStoreGitHubToken' -v 2>&1 | tail -30</automated>
  </verify>
  <done>
    - `SSMGetPutAPI` has `GetParametersByPath` method; `*ssm.Client` still satisfies it (proved by package compile).
    - `ErrAmbiguousInstallation` typed error defined with `Candidates []string` and a pointer-receiver `Error()` method that mentions both fix suggestions.
    - `resolveInstallationID` enumerates installations via `GetParametersByPath` only when no concrete owner is extracted; returns the single ID when exactly one exists, returns `&ErrAmbiguousInstallation{...}` when ≥2 exist, falls through to legacy when zero exist; concrete-owner path unchanged.
    - All five new tests + all pre-existing tests in the package PASS.
    - Caller source test from Task 1 may still fail (the `⚠` warning at create.go:994 is added in Task 3) — acceptable.
  </done>
</task>

<task type="auto">
  <name>Task 3: Differentiate the silent skip — loud ⚠ warning for ErrAmbiguousInstallation at create.go:994</name>
  <files>internal/app/cmd/create.go</files>
  <action>
    Update the caller block at create.go:993-1003 (the `if tokenErr != nil` arm of the GitHub-token step) to add a third branch that uses `errors.As` to recover an `*ErrAmbiguousInstallation` and prints a loud warning:

    ```go
    if tokenErr != nil {
        var ambig *ErrAmbiguousInstallation
        switch {
        case errors.Is(tokenErr, ErrGitHubNotConfigured):
            fmt.Printf("  ⊘ GitHub token: skipped (not configured)\n")
        case errors.As(tokenErr, &ambig):
            fmt.Fprintf(os.Stderr, "  ⚠ GitHub token: skipped — ambiguous installation\n")
            fmt.Fprintf(os.Stderr, "    Candidates: %s\n", strings.Join(ambig.Candidates, ", "))
            fmt.Fprintf(os.Stderr, "    Fix: either (a) set /km/config/github/installation-id in SSM to pick one,\n")
            fmt.Fprintf(os.Stderr, "         or  (b) add an owner-prefixed entry like %q to spec.sourceAccess.github.allowedRepos\n",
                ambig.Candidates[0]+"/*")
            log.Warn().Strs("candidates", ambig.Candidates).Str("sandbox_id", sandboxID).
                Msg("Step 13a: GitHub App installation ambiguous — token not generated (non-fatal)")
        default:
            log.Warn().Err(tokenErr).Str("sandbox_id", sandboxID).
                Msg("Step 13a: GitHub App token generation failed (non-fatal — sandbox is provisioned)")
        }
    }
    ```

    Order matters: `errors.Is(ErrGitHubNotConfigured)` MUST be checked before `errors.As(*ErrAmbiguousInstallation)` so that an unconfigured deployment still gets the quiet `⊘` line (the new code path returns `ErrGitHubNotConfigured` for the zero-installations + no-legacy case, see Task 2 Step D).

    Verify imports: `errors`, `os`, `strings`, `fmt`, and `github.com/rs/zerolog/log` are already in scope at this site.

    Steps:
    1. Open `internal/app/cmd/create.go`, locate the caller block (search for `errors.Is(tokenErr, ErrGitHubNotConfigured)`).
    2. Replace the simple if/else with the three-branch switch above.
    3. Run: `go build ./...`
    4. Run the package test suite: `go test ./internal/app/cmd/ 2>&1 | tail -20`. Confirm the caller source-level test from Task 1 now PASSES, and nothing else regresses.
    5. Optional sanity check: `grep -n '⚠ GitHub token: skipped — ambiguous installation' internal/app/cmd/create.go` — must return one match.
    6. Commit: `feat(6): differentiate ambiguous-installation warning from silent skip`.

    Do NOT alter the success path at create.go:1000-1003 (`resolvedInstallationID = instID` + `✓ GitHub token stored in SSM` line).
    Do NOT change the upstream `if resolvedProfile.Spec.SourceAccess.GitHub != nil ...` guard at create.go:985.
  </action>
  <verify>
    <automated>go build ./... && go test ./internal/app/cmd/ 2>&1 | tail -20</automated>
  </verify>
  <done>
    - create.go:994 area contains a three-branch error handler (Is(ErrGitHubNotConfigured) → ⊘ skip; As(*ErrAmbiguousInstallation) → ⚠ warn with candidates + two fix suggestions; default → existing log.Warn).
    - All tests in `internal/app/cmd/` pass — including the caller source-level test from Task 1 that asserts presence of `⚠`, `errors.As(tokenErr,`, and the two fix-suggestion strings.
    - Single commit landed.
    - `go build ./...` succeeds.
  </done>
</task>

</tasks>

<verification>
After all three tasks land:

1. **Compile + full package test:**
   ```bash
   go build ./... && go test ./internal/app/cmd/ -v 2>&1 | tail -50
   ```
   All tests PASS, build succeeds.

2. **Targeted assertion that all RED tests are now GREEN:**
   ```bash
   go test ./internal/app/cmd/ -run 'TestResolveInstallationID|TestCreateGitHubSkip|TestCreateGitHubCaller' -v 2>&1 | grep -E '^(=== RUN|--- (PASS|FAIL))'
   ```
   No `--- FAIL` lines.

3. **Source-level inspection for required markers:**
   ```bash
   grep -n 'ErrAmbiguousInstallation\|GetParametersByPath\|⚠ GitHub token' internal/app/cmd/create.go
   ```
   Should show: type def + Error() method, interface method, GetParametersByPath call inside resolveInstallationID, and the ⚠ warning line in the caller.

4. **Verify pkg/github/token.go was NOT modified:**
   ```bash
   git diff --stat pkg/github/token.go
   ```
   Should be empty.

5. **Verify profiles/learn.yaml was NOT modified:**
   ```bash
   git diff --stat profiles/learn.yaml
   ```
   Should be empty.
</verification>

<success_criteria>
**Code-level (executor verifiable):**
- `go build ./...` succeeds.
- `go test ./internal/app/cmd/...` all pass.
- `internal/app/cmd/create.go` contains: extended `SSMGetPutAPI` with `GetParametersByPath`; `ErrAmbiguousInstallation` typed error with `Candidates []string` and pointer-receiver `Error()`; `resolveInstallationID` enumerates `/km/config/github/installations/` for wildcard-only inputs with single/multi/zero/legacy/concrete branches; caller at create.go:994 prints `⊘` for not-configured, `⚠` with candidate list and two fix suggestions for ambiguous, default `log.Warn` for everything else.
- `internal/app/cmd/create_github_test.go` contains the five new `TestResolveInstallationID_WildcardOnly_*` and `TestResolveInstallationID_ConcreteOwner_StillUsesPerOwnerKey_RegressionGuard` tests, plus the extended caller source-level test.
- `pkg/github/token.go` and `profiles/learn.yaml` are unchanged (`git diff --stat` confirms zero lines).
- 2-3 atomic commits per operator hint: (a) RED test commit, (b) GREEN implementation commit, (c) caller-warning commit. A single tight squash is also acceptable.

**Operator-gated (post-execution, NOT executor's responsibility):**
The operator (whereiskurt) runs the manual end-to-end gate before declaring the fix done. Listed here for reference only — executor must NOT attempt these:
- `make build` (per project memory `feedback_rebuild_km` — always Make, not bare `go build`)
- `km create profiles/learn.yaml` — should print `✓ GitHub token stored in SSM` (not `⊘ skipped`)
- `km shell <sandbox-id>` then `git clone https://github.com/whereiskurt/defcon.run.34 /tmp/test` — must succeed with no username prompt
- `km destroy --remote --yes <sandbox-id>` (per project memory `feedback_destroy_flags`)
</success_criteria>

<output>
After completion, create `.planning/quick/6-fix-github-app-installation-id-resolutio/6-SUMMARY.md` documenting:
- Root cause (one paragraph: `extractRepoOwner("*")` returns "", legacy key unset, silent skip)
- Fix shape (interface extension + new typed error + enumeration branch + differentiated caller warning)
- Files changed: `internal/app/cmd/create.go`, `internal/app/cmd/create_github_test.go`
- Files NOT changed (deliberately): `pkg/github/token.go` (already handles wildcard at exchange time), `profiles/learn.yaml` (already correct for the post-fix world)
- Commit list (2-3 hashes)
- Operator gate status: PENDING (manual e2e is operator's responsibility — leave as PENDING in summary; operator updates after running the gate)
</output>
