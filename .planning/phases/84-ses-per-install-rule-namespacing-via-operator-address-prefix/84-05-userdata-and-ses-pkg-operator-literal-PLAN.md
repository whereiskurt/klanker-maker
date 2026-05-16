---
phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
plan: 05
type: execute
wave: 1
depends_on: [01]
files_modified:
  - pkg/compiler/userdata.go
  - pkg/aws/ses.go
autonomous: true
requirements:
  - SES-PREFIX-ADDRESS

must_haves:
  truths:
    - "Sandbox-side `km-send` heredoc in `pkg/compiler/userdata.go` no longer contains a bare `operator@${KM_SANDBOX_DOMAIN}` literal at lines ~1621 and ~1653; both occurrences become `${KM_OPERATOR_EMAIL}` shell-var references"
    - "`KM_OPERATOR_EMAIL` is exported into the sandbox environment EARLIER in userdata (via `/etc/profile.d/...` write or env-file write) so the heredoc's variable reference resolves correctly at km-send invocation time"
    - "`pkg/aws/ses.go:271` notification body interpolates the operator address using the resource prefix; the file no longer contains a bare `operator@` Go-string literal for the operator address"
    - "Test stub W0-09 (`TestUserdata_KmSendOperatorAddressUsesEnvVar`) passes"
    - "Test stub W0-10 (`TestSendCreateNotification_OperatorAddressUsesPrefix`) passes"
  artifacts:
    - path: "pkg/compiler/userdata.go"
      provides: "Updated km-send heredoc with KM_OPERATOR_EMAIL env-var refs + KM_OPERATOR_EMAIL export earlier in userdata"
    - path: "pkg/aws/ses.go"
      provides: "SendCreateNotification (and any peer functions) interpolate operator address from prefix instead of using a literal"
  key_links:
    - from: "pkg/compiler/userdata.go (km-send heredoc)"
      to: "shell environment at sandbox runtime"
      via: "${KM_OPERATOR_EMAIL} expanded from /etc/profile.d/km-notify-env.sh or peer env file"
      pattern: "\\$\\{KM_OPERATOR_EMAIL\\}"
    - from: "pkg/aws/ses.go"
      to: "Operator notification recipient address"
      via: "interpolated address using resource_prefix"
      pattern: "operator-"
---

<objective>
Replace the four hardcoded `operator@` literals in Go production code with prefix-aware derivations (per RESEARCH § Pitfall 6 + CONTEXT.md "Additional Decisions" § Scope additions):

1. `pkg/compiler/userdata.go:1621` — sandbox-side `km-send` heredoc default `--to`
2. `pkg/compiler/userdata.go:1653` — second km-send heredoc occurrence
3. `pkg/aws/ses.go:271` — operator-notification email body

Add a `KM_OPERATOR_EMAIL` export earlier in userdata so the heredoc's shell-var reference resolves at runtime.

(The fourth location, `cmd/email-create-handler/main.go:861`, is handled in Plan 84-06 because it's tightly coupled with the handler's recipient-verification changes.)

Purpose: After this plan, every sandbox provisioned with a fresh `km create` uses `${KM_OPERATOR_EMAIL}` as its km-send default recipient, and the platform's notification emails carry the prefix-aware operator address.

Output:
- 2 files modified
- 2 Wave 0 stubs (W0-09, W0-10) turn GREEN
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-CONTEXT.md
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-RESEARCH.md

@pkg/compiler/userdata.go
@pkg/aws/ses.go
@pkg/compiler/userdata_82_02_test.go
@pkg/aws/ses_test.go

<interfaces>
<!-- Key transformations. Exact line numbers from RESEARCH § Sources. -->

`pkg/compiler/userdata.go` — TWO occurrences to fix:
- Line ~1621: inside a heredoc-emitted bash script (the inlined km-send helper). The bash variable `KM_SANDBOX_DOMAIN` is used as `--to operator@${KM_SANDBOX_DOMAIN}`. Replace with `--to ${KM_OPERATOR_EMAIL}` (shell variable, not Go template — the heredoc is a raw string).
- Line ~1653: a second km-send heredoc with the same pattern. Apply the same replacement.

Earlier in userdata.go, find where `/etc/profile.d/km-notify-env.sh` (or the equivalent env file) is generated. Phase 82 added `KM_RESOURCE_PREFIX`, `KM_EMAIL_SUBDOMAIN` etc. there. ADD a `KM_OPERATOR_EMAIL=<derived>` line, computed at compile time from the platform config (compiler has access to `pc.OperatorEmail` or the per-config knobs). The line must appear in the file BEFORE the km-send heredoc is created (so when the heredoc is later sourced/executed, `${KM_OPERATOR_EMAIL}` is set).

Critical: the env-file line uses systemd format (`KM_OPERATOR_EMAIL=value`, no `export` prefix) per MEMORY note "systemd EnvironmentFile gotcha" — if the same value is also needed in `/etc/profile.d/*.sh`, write a shell-format export there too (parallel structure that Phase 67 uses for `KM_SLACK_*`).

**Field-name verification (per plan-checker iteration 1):** The `platformConfig` struct field is `Domain` (NOT `ParentDomain`); see `internal/app/cmd/configure.go` lines 22-33. Executor should re-grep before coding to confirm no concurrent refactor.

`pkg/aws/ses.go` — line ~271:
- The function (likely `SendCreateNotification` per RESEARCH) currently has a literal like `fmt.Sprintf("From: operator@%s", domain)` or `to := "operator@" + domain`.
- Replace with `to := fmt.Sprintf("operator-%s@%s", resourcePrefix, emailDomain)` (the function's signature already takes `resourcePrefix` per Phase 82-B2, or accepts a `*platformConfig` — confirm by reading the function).
- If the function does NOT currently accept `resourcePrefix`, either:
  - (preferred) Update the function signature to accept `resourcePrefix` and update all call sites to pass it (search with `grep -rn "SendCreateNotification(" .`)
  - (fallback) Read `os.Getenv("KM_RESOURCE_PREFIX")` inside the function as a temporary measure — flag as tech debt in the SUMMARY.
- Reuse the `deriveOperatorEmail` helper from Plan 84-04 if it's exported package-level visible (it isn't if it lives in `internal/app/cmd/configure.go`). Acceptable to duplicate the 1-line `fmt.Sprintf` here, OR move `deriveOperatorEmail` into `pkg/aws` or a shared package — planner's discretion. Default: duplicate the format string; the canonical name is documented in CONTEXT.md.

Test contract (must pass after this plan):
- `TestUserdata_KmSendOperatorAddressUsesEnvVar` (W0-09): generated userdata contains `${KM_OPERATOR_EMAIL}` in both km-send heredoc blocks AND contains `KM_OPERATOR_EMAIL=...` in the env-file section, AND does NOT contain `operator@${KM_SANDBOX_DOMAIN}` in either heredoc.
- `TestSendCreateNotification_OperatorAddressUsesPrefix` (W0-10): driving `SendCreateNotification` with `resource_prefix="kph"` produces a body string containing `operator-kph@sandboxes.example.com` and NO bare `operator@`.
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Replace operator@ literals in userdata.go (lines 1621 + 1653) and add KM_OPERATOR_EMAIL env export</name>
  <files>pkg/compiler/userdata.go</files>
  <behavior>
    - The two km-send heredoc blocks in the generated userdata contain `--to ${KM_OPERATOR_EMAIL}` (or an equivalent shell-var reference) instead of `--to operator@${KM_SANDBOX_DOMAIN}`.
    - The env-file generation block (the same block that emits `KM_RESOURCE_PREFIX`, `KM_EMAIL_SUBDOMAIN`, etc.) ALSO emits `KM_OPERATOR_EMAIL=<derived>` — derived at compile time from the platform config.
    - The systemd-format env file is updated; if a shell-format `/etc/profile.d/*.sh` parallel exists, that's updated too.
    - The compile-time derivation uses the same formula as Plan 84-04's `deriveOperatorEmail`: `operator-${resource_prefix}@${email_subdomain}.${domain}`.
    - Test W0-09 (`TestUserdata_KmSendOperatorAddressUsesEnvVar`) passes.
  </behavior>
  <action>
1. Open `pkg/compiler/userdata.go`. Locate the two heredoc occurrences (around lines 1621 and 1653) — `grep -n "operator@" pkg/compiler/userdata.go` will pinpoint them.

2. Replace both occurrences:
   - Before: `--to operator@${KM_SANDBOX_DOMAIN}` (or whatever the exact literal is — read first)
   - After: `--to ${KM_OPERATOR_EMAIL}` (preserve the surrounding bash syntax)

3. Locate the env-file generation block. Search for `KM_RESOURCE_PREFIX=` or `KM_EMAIL_SUBDOMAIN=` to find it. Add a new line writing `KM_OPERATOR_EMAIL=<derived value>` where `<derived value>` is computed from the same platform-config fields the surrounding lines already use. Use `fmt.Sprintf("operator-%s@%s.%s", resourcePrefix, emailSubdomain, domain)` (or call the helper from Plan 84-04 if it's package-visible to `pkg/compiler`).

4. If userdata.go has BOTH a systemd-format env file and a shell-format `/etc/profile.d/*.sh` (per MEMORY note on the systemd EnvironmentFile gotcha — Phase 67 established this pattern), update BOTH.

5. Run `gofmt -w pkg/compiler/userdata.go`. Run `go vet ./pkg/compiler/...`. Confirm test W0-09 passes.

If `pkg/compiler` doesn't currently take `domain` as a separate field (it may be merged into `domain`), inspect the existing config struct passed to the userdata generator and use the right combination of fields to produce the canonical `operator-${prefix}@${subdomain}.${parent}` shape.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go test ./pkg/compiler/ -run TestUserdata_KmSendOperatorAddressUsesEnvVar -count=1 2>&1 | tail -10 && ! grep -n "operator@\\${KM_SANDBOX_DOMAIN}" pkg/compiler/userdata.go && echo "no bare operator@ literal remains"</automated>
  </verify>
  <done>W0-09 passes. Both heredoc occurrences use `${KM_OPERATOR_EMAIL}`. Env-file generation includes `KM_OPERATOR_EMAIL=...`. `go vet` clean. No regression in other userdata tests.</done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Replace operator@ literal in pkg/aws/ses.go (line 271)</name>
  <files>pkg/aws/ses.go</files>
  <behavior>
    - `SendCreateNotification` (or the function holding the line-271 literal) produces a notification body whose operator address is `operator-${resource_prefix}@${email_domain}` instead of `operator@${domain}`.
    - The function either accepts `resourcePrefix` as a parameter or reads it from a *platformConfig argument that's already in scope.
    - All callers updated to pass the new parameter (if a signature change is needed).
    - Test W0-10 (`TestSendCreateNotification_OperatorAddressUsesPrefix`) passes.
  </behavior>
  <action>
1. Open `pkg/aws/ses.go`. Locate line ~271 — `grep -n "operator@" pkg/aws/ses.go`.

2. Examine the function signature. If it accepts a `*platformConfig` or similar struct that already carries `ResourcePrefix` and `EmailSubdomain`/`Domain`, derive the operator address inline:
   ```go
   operatorAddr := fmt.Sprintf("operator-%s@%s.%s", pc.ResourcePrefix, pc.EmailSubdomain, pc.Domain)
   ```
   (Use the same field names that exist on the struct; confirm by reading.)

3. If the function does NOT have access to those fields, update its signature to accept `resourcePrefix string` (or `pc *platformConfig`). Update all call sites (`grep -rn "SendCreateNotification(" .`). If there are many call sites, this signals a larger refactor — keep the change minimal: prefer adding a new explicit parameter at the end of the existing signature.

4. Replace ALL operator address literals in the function body — both the email's `To:` header AND any body text that mentions the address.

5. Run `gofmt -w pkg/aws/ses.go`. Run `go vet ./pkg/aws/...` and `go vet ./...` to catch any callers broken by signature changes. Run the test.

6. After both Task 1 and Task 2 land, run `make build` to confirm the km binary still links cleanly.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go test ./pkg/aws/ -run TestSendCreateNotification_OperatorAddressUsesPrefix -count=1 2>&1 | tail -10 && go build ./... 2>&1 | tail -5</automated>
  </verify>
  <done>W0-10 passes. `go build ./...` clean. No bare `operator@` literal in `pkg/aws/ses.go`. All call sites updated.</done>
</task>

</tasks>

<verification>
- `grep -n "operator@" pkg/compiler/userdata.go pkg/aws/ses.go` returns only acceptable cases (e.g., in comments documenting the legacy address; or zero matches).
- W0-09 and W0-10 both pass.
- `go build ./...` succeeds.
- No regression in other compiler or pkg/aws tests.
</verification>

<success_criteria>
- All Go-side operator address literals (except `cmd/email-create-handler/main.go:861` — handled by Plan 84-06) are prefix-aware.
- Sandbox userdata exports `KM_OPERATOR_EMAIL` and the heredoc-emitted km-send references it.
- Two Wave 0 stubs turn GREEN.
</success_criteria>

<output>
After completion, create `.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-05-SUMMARY.md`
</output>
