---
phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
plan: 04
type: execute
wave: 1
depends_on: [01]
files_modified:
  - internal/app/cmd/configure.go
autonomous: true
requirements:
  - SES-CONFIGURE-WIRING
  - SES-PREFIX-ADDRESS

must_haves:
  truths:
    - "`km configure` derives `operator_email` as `operator-${resource_prefix}@${email_subdomain}.${domain}` when the user accepts the default (no manual override)"
    - "`km configure --reset-prefix` also clears the stored `operator_email` so the next configure re-derives from the new default prefix"
    - "An exported helper `deriveOperatorEmail(prefix, emailSubdomain, domain string) string` exists in `configure.go` (or a peer file) so tests and other commands can reuse the derivation"
    - "When the operator manually enters a non-default operator email at the prompt, the manual value is preserved on re-run (preserve-on-rerun semantics from Phase 82.1)"
    - "Test stubs W0-01, W0-02, W0-03 from Plan 84-01 now PASS"
  artifacts:
    - path: "internal/app/cmd/configure.go"
      provides: "deriveOperatorEmail helper + integration into runConfigure flow + --reset-prefix clearing logic"
  key_links:
    - from: "internal/app/cmd/configure.go"
      to: "km-config.yaml's operator_email field"
      via: "platformConfig.OperatorEmail write path"
      pattern: "pc.OperatorEmail = "
    - from: "internal/app/cmd/configure.go"
      to: "Test stubs in internal/app/cmd/configure_test.go"
      via: "function name deriveOperatorEmail (or its tested integration point)"
      pattern: "func deriveOperatorEmail"
---

<objective>
Update `km configure` to derive `KM_OPERATOR_EMAIL` from `resource_prefix + email_subdomain + domain` (CONTEXT.md locked decision). Preserve operator-typed overrides on re-run. Clear stored operator email when `--reset-prefix` is passed so the next configure re-derives from the new default.

Per RESEARCH § Pitfall 5: stale operator_email after re-configure is a real risk; `--reset-prefix` must clear it.

Purpose: After this plan, `km bootstrap` (Plan 84-07) reads `pc.OperatorEmail` knowing it's a single source of truth derived from the prefix. The sandbox-side `${KM_OPERATOR_EMAIL}` env var (set by `km create` → userdata) traces back to this derivation. Test stubs W0-01..03 from Wave 0 turn GREEN.

Output:
- 1 file modified: `internal/app/cmd/configure.go`
- 3 Wave 0 stubs turn GREEN
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-CONTEXT.md
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-RESEARCH.md
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-VALIDATION.md

@internal/app/cmd/configure.go
@internal/app/cmd/configure_test.go

<interfaces>
<!-- Surface this plan exposes to the rest of Phase 84. -->

```go
// internal/app/cmd/configure.go (planned additions)

// deriveOperatorEmail returns the canonical operator inbox address for a given
// install. The address shape is locked by Phase 84 CONTEXT.md:
//
//   operator-${resource_prefix}@${email_subdomain}.${domain}
//
// Empty inputs return "" — callers should fall back to whatever they had before.
func deriveOperatorEmail(resourcePrefix, emailSubdomain, domain string) string

// In runConfigure, after the resource_prefix / email_subdomain / domain
// values have been confirmed (and before the operator_email prompt), compute
// the derived value and use it as the prompt default. When the user accepts
// the default, pc.OperatorEmail is set to the derived value.
//
// When --reset-prefix is passed, pc.OperatorEmail is cleared BEFORE the prompt
// so the derivation is freshly computed from the new (default) prefix.
```

Caveats from existing code (read configure.go to confirm exact spelling):
- `platformConfig` field for resource prefix may be `ResourcePrefix` (Phase 82's preserve-on-rerun field) — match exactly.
- `EmailSubdomain` and `Domain` / `Domain` field names — match exactly.
- The `--reset-prefix` flag handling lives in Phase 82.1 — read that block carefully to extend it.
- The prompt helper (`promptWithDefault`, `promptString`, etc.) — reuse the same helper to keep UX consistent.

**Field-name verification (per plan-checker iteration 1):** Field names verified against `internal/app/cmd/configure.go` as of phase planning — executor should re-grep before coding to confirm no concurrent refactor. The actual field names on `platformConfig` are `ResourcePrefix`, `EmailSubdomain`, `Domain`, `OperatorEmail` (lines 22-33).

Test contract (must pass after this plan):
- `TestConfigure_DerivesOperatorEmailFromPrefix` — direct call to `deriveOperatorEmail("kph", "sandboxes", "example.com")` → `"operator-kph@sandboxes.example.com"`.
- `TestConfigure_BlankOperatorEmail_DerivesFromPrefix` — drive runConfigure (or its inner helper) with blank operator email + known prefix/subdomain/domain → resulting `pc.OperatorEmail` equals the derived value.
- `TestConfigure_ResetPrefix_ClearsOperatorEmail` — drive runConfigure with `resetPrefix=true` and a pre-existing config (`operator_email="operator-kph@..."`, `resource_prefix="kph"`) → after run, `pc.OperatorEmail == ""` (or the new-derived value, depending on what the rest of the run flow does; the test in 84-01 specifies the expected behavior).
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Add deriveOperatorEmail helper + integrate into runConfigure</name>
  <files>internal/app/cmd/configure.go</files>
  <behavior>
    - `deriveOperatorEmail("kph", "sandboxes", "example.com")` returns `"operator-kph@sandboxes.example.com"`.
    - `deriveOperatorEmail("", "sandboxes", "example.com")` returns `""` (any blank input → blank output, callers handle).
    - `deriveOperatorEmail("km", "sandboxes", "example.com")` returns `"operator-km@sandboxes.example.com"` — default prefix is `km`, NOT a special case (per CONTEXT.md "Default prefix").
    - In `runConfigure`, when `pc.OperatorEmail == ""` and the three inputs are populated, the prompt's default value is the derived address; bare Enter accepts it.
    - In `runConfigure`, when `pc.OperatorEmail != ""` (operator override from a prior run), the existing value is preserved and shown as the prompt default.
  </behavior>
  <action>
1. Open `internal/app/cmd/configure.go`. Locate the existing `OperatorEmail` prompt path (RESEARCH line 338) and the `--reset-prefix` handling (Phase 82.1).

2. Add the helper function near the other small helpers (top of file, after imports):

```go
// deriveOperatorEmail returns the canonical operator inbox for an install.
// Phase 84: operator-${resource_prefix}@${email_subdomain}.${domain}.
// Returns "" when any input is empty — callers handle the fallback.
func deriveOperatorEmail(resourcePrefix, emailSubdomain, domain string) string {
    if resourcePrefix == "" || emailSubdomain == "" || domain == "" {
        return ""
    }
    return fmt.Sprintf("operator-%s@%s.%s", resourcePrefix, emailSubdomain, domain)
}
```

3. In `runConfigure`, BEFORE the existing operator-email prompt:
   - Compute `derivedDefault := deriveOperatorEmail(pc.ResourcePrefix, pc.EmailSubdomain, pc.Domain)` (use the actual field names from configure.go).
   - If `pc.OperatorEmail == ""` (fresh install or post-reset), use `derivedDefault` as the prompt's default value.
   - If `pc.OperatorEmail != ""`, preserve the existing value as the prompt default (per Phase 82.1 preserve-on-rerun).

4. Use the existing prompt helper (e.g., `promptWithDefault` if that's the codebase's name; otherwise the equivalent — match the existing pattern around line 338).

5. In the `--reset-prefix` handling block (Phase 82.1), ADD a line that clears `pc.OperatorEmail`:

```go
if resetPrefix {
    pc.ResourcePrefix = ""        // existing behavior from Phase 82.1
    pc.OperatorEmail = ""         // NEW (Phase 84): force re-derivation from new default prefix
    // ... continue with existing reset flow
}
```

6. The non-interactive path (when stdin is not a TTY): if `pc.OperatorEmail == ""` AND `derivedDefault != ""`, set `pc.OperatorEmail = derivedDefault` without prompting. This is the same behavior the interactive prompt would produce when the operator presses Enter to accept the default.

7. Run `gofmt -w internal/app/cmd/configure.go`. Run `go vet ./internal/app/cmd/...`. Run `make build` (per MEMORY note `feedback_rebuild_km`).
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go test ./internal/app/cmd/ -run 'TestConfigure_DerivesOperatorEmailFromPrefix|TestConfigure_BlankOperatorEmail_DerivesFromPrefix|TestConfigure_ResetPrefix_ClearsOperatorEmail' -count=1 2>&1 | tail -15</automated>
  </verify>
  <done>All three test stubs (W0-01, W0-02, W0-03) pass. `make build` produces an updated km binary. `go vet ./...` clean for `internal/app/cmd/`.</done>
</task>

</tasks>

<verification>
- W0-01, W0-02, W0-03 stubs from Plan 84-01 turn GREEN.
- `deriveOperatorEmail` is exported (lowercase exported within package — package-level visibility is enough; the test is in the same package).
- `--reset-prefix` clears `operator_email`.
- Default install (`prefix=km`) yields `operator-km@sandboxes.<parent>`, NOT `operator@`.
</verification>

<success_criteria>
- All three Wave 0 configure-related stubs pass.
- No regression: existing `TestConfigure_*` tests (Phase 82, 82.1) still pass — `go test ./internal/app/cmd/ -run TestConfigure -count=1`.
- `km configure` UX preserved (prompt-with-default, override capability).
</success_criteria>

<output>
After completion, create `.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-04-SUMMARY.md`
</output>
