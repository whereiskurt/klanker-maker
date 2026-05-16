---
phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
plan: 01
type: execute
wave: 0
depends_on: []
files_modified:
  - internal/app/cmd/configure_test.go
  - internal/app/cmd/doctor_test.go
  - internal/app/cmd/doctor_ses_rules_test.go
  - cmd/email-create-handler/main_test.go
  - pkg/compiler/userdata_84_test.go
  - pkg/aws/ses_test.go
  - Makefile
autonomous: true
requirements:
  - SES-PREFIX-ADDRESS
  - SES-CONFIGURE-WIRING
  - SES-HANDLER-LOOKUP
  - SES-DOCTOR-ORPHANS
  - SES-82.1-REMOVAL

must_haves:
  truths:
    - "11 failing test stubs (W0-01..11) exist and compile"
    - "`go test ./...` runs to a known-failing state on each new stub (i.e., each stub references an as-yet-unimplemented production symbol or expectation)"
    - "Makefile gains a `test-no-82.1-leftovers` target that greps for Phase 82.1 leftovers and exits non-zero when matches are present"
    - "Wave 0 plan produces no production code changes — test scaffolds only"
  artifacts:
    - path: "internal/app/cmd/configure_test.go"
      provides: "Three new test functions (DerivesOperatorEmailFromPrefix, BlankOperatorEmail_DerivesFromPrefix, ResetPrefix_ClearsOperatorEmail)"
    - path: "internal/app/cmd/doctor_test.go"
      provides: "Two new test functions (CheckSESRules_AllOwn, CheckSESRules_Orphans)"
    - path: "internal/app/cmd/doctor_ses_rules_test.go"
      provides: "NEW FILE with mockSESReceiptRuleAPI"
    - path: "cmd/email-create-handler/main_test.go"
      provides: "Two new test functions (OperatorAddress_OwnPrefix, OperatorAddress_ForeignPrefix_Drops)"
    - path: "pkg/compiler/userdata_84_test.go"
      provides: "NEW FILE asserting generated userdata references ${KM_OPERATOR_EMAIL}"
    - path: "pkg/aws/ses_test.go"
      provides: "Extended with TestSendCreateNotification_OperatorAddressUsesPrefix"
    - path: "Makefile"
      provides: "test-no-82.1-leftovers grep gate target"
  key_links:
    - from: "internal/app/cmd/doctor_ses_rules_test.go"
      to: "internal/app/cmd/doctor.go (future SESReceiptRuleAPI interface)"
      via: "narrow interface gated by build tag or stub type — test compiles without aws-sdk-go-v2/service/ses dependency"
      pattern: "type mockSESReceiptRuleAPI struct"
---

<objective>
Wave 0 — failing-test infrastructure (Nyquist) for Phase 84. Creates all 11 stubs (W0-01..11) from `84-VALIDATION.md` § Per-Task Verification Map. No production code touched. Each stub references an as-yet-unimplemented production symbol or expectation, so `go test` and `make test-no-82.1-leftovers` are RED at end of this plan.

Purpose: Implementation tasks in Waves 1-2 each map to one or more of these stubs and turn them GREEN. The grep-gate target also runs in CI from this point forward.

Output:
- 6 Go test files modified/created
- 1 Makefile target added
- 0 production source changes
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/ROADMAP.md
@.planning/STATE.md
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-CONTEXT.md
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-RESEARCH.md
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-VALIDATION.md

@internal/app/cmd/configure_test.go
@internal/app/cmd/doctor_test.go
@cmd/email-create-handler/main_test.go
@pkg/aws/ses_test.go
@pkg/compiler/userdata_82_02_test.go
@internal/app/cmd/doctor_slack_inbound_test.go
@Makefile

<interfaces>
<!-- Patterns to follow when writing the stubs. -->

Pattern from `pkg/compiler/userdata_82_02_test.go` for substring assertions on generated userdata:
- Build a fake `Compiler` with minimal config (resource_prefix, email_subdomain, domain).
- Call the userdata generator; assert specific substrings present/absent.

Pattern from `internal/app/cmd/doctor_slack_inbound_test.go` for narrow-interface mocks:
```go
type mockSESReceiptRuleAPI struct {
  describeFn func(ctx context.Context, in *ses.DescribeReceiptRuleSetInput, optFns ...func(*ses.Options)) (*ses.DescribeReceiptRuleSetOutput, error)
}
func (m *mockSESReceiptRuleAPI) DescribeReceiptRuleSet(ctx context.Context, in *ses.DescribeReceiptRuleSetInput, optFns ...func(*ses.Options)) (*ses.DescribeReceiptRuleSetOutput, error) {
  return m.describeFn(ctx, in, optFns...)
}
```

Pattern for grep gate (Makefile):
```makefile
.PHONY: test-no-82.1-leftovers
test-no-82.1-leftovers:
	@! grep -rn "KM_SES_ACTIVATE_RULESET\|activate_rule_set" infra/ internal/ pkg/ cmd/ OPERATOR-GUIDE.md CLAUDE.md \
		|| (echo "Phase 82.1 leftovers found"; exit 1)
```
</interfaces>
</context>

<tasks>

<task type="auto">
  <name>Task 1a: Configure + doctor test stubs (W0-01..03, W0-06..07) — internal/app/cmd package</name>
  <files>internal/app/cmd/configure_test.go, internal/app/cmd/doctor_test.go</files>
  <action>
Add failing test stubs to two existing test files in the `internal/app/cmd` package. Same-package edits keep this task atomic.

**REVISION FROM ITERATION 0 (Minor 11 split):** Originally part of a 4-file Task 1; split here for tighter atomicity within a single package.

**`internal/app/cmd/configure_test.go`** — append three test functions (W0-01, W0-02, W0-03):
  - `TestConfigure_DerivesOperatorEmailFromPrefix` — call the function/helper that derives operator email (e.g., a new `deriveOperatorEmail(prefix, emailSubdomain, domain string) string`); assert `operator-kph@sandboxes.example.com` when prefix=`kph`, subdomain=`sandboxes`, domain=`example.com`. If the helper doesn't exist yet, the test must reference its expected exported name so the compile fails until task 84-04-01 lands.
  - `TestConfigure_BlankOperatorEmail_DerivesFromPrefix` — set up a `platformConfig` with `OperatorEmail=""`, `ResourcePrefix="rg"`, `EmailSubdomain="sandboxes"`, `Domain="example.com"`; drive `runConfigure` (or its helper) in a way that exercises the derivation; assert resulting `pc.OperatorEmail == "operator-rg@sandboxes.example.com"`.
  - `TestConfigure_ResetPrefix_ClearsOperatorEmail` — start from a config with `OperatorEmail="operator-kph@..."` and `ResourcePrefix="kph"`; run configure with `--reset-prefix` semantics (test exercises the reset path); assert `pc.OperatorEmail == ""` after reset (so the next configure re-derives from the new default prefix).

**Field-name verification (per plan-checker iteration 1):** The `platformConfig` struct fields are `ResourcePrefix`, `EmailSubdomain`, `Domain`, `OperatorEmail` (configure.go lines 22-33). NOT `ParentDomain`. Use the actual names.

**`internal/app/cmd/doctor_test.go`** — append two test functions (W0-06, W0-07):
  - `TestCheckSESRules_AllOwn` — construct a `mockSESReceiptRuleAPI` (defined in Task 2 below) that returns rules `kph-operator-inbound` and `kph-sandbox-catchall`; call `checkSESRules(ctx, mock, "kph")`; assert `CheckResult{Status: StatusOK}` with message mentioning `2 rules` and `kph`.
  - `TestCheckSESRules_Orphans` — mock returns `kph-operator-inbound`, `kph-sandbox-catchall`, `xx-operator-inbound`; call `checkSESRules(ctx, mock, "kph")`; assert `Status: StatusWarn` and that orphan list contains `xx-operator-inbound`.

For each new test function: `t.Skip()` is NOT acceptable — the assertion must be the real one (RED at this point).

Do NOT modify any production code in this task.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go vet ./internal/app/cmd/... 2>&1 | head -30 && go test ./internal/app/cmd/ -run 'TestConfigure_DerivesOperatorEmailFromPrefix|TestConfigure_BlankOperatorEmail_DerivesFromPrefix|TestConfigure_ResetPrefix_ClearsOperatorEmail|TestCheckSESRules_AllOwn|TestCheckSESRules_Orphans' -count=1 2>&1 | tail -20</automated>
  </verify>
  <done>Both files updated. `go vet` highlights the missing production symbols. `go test` reports RED for each of the 5 test names.</done>
</task>

<task type="auto">
  <name>Task 1b: email-handler + pkg/aws test stubs (W0-04..05, W0-10) — cross-package</name>
  <files>cmd/email-create-handler/main_test.go, pkg/aws/ses_test.go</files>
  <action>
Add failing test stubs to two test files in different packages. Small surface — 3 stubs total.

**REVISION FROM ITERATION 0 (Minor 11 split):** Originally part of a 4-file Task 1; split here for tighter atomicity.

**`cmd/email-create-handler/main_test.go`** — append two test functions (W0-04, W0-05):
  - `TestHandle_OperatorAddress_OwnPrefix` — set `KM_RESOURCE_PREFIX=kph` and a domain env (`KM_EMAIL_DOMAIN=sandboxes.example.com` or whatever the handler reads); build a raw MIME message with `To: operator-kph@sandboxes.example.com`; invoke the Handle path; assert NO silent drop, the existing allowlist/safe-phrase pipeline proceeds (use a sentinel — e.g., expect a specific later-stage error or call into a mock).
  - `TestHandle_OperatorAddress_ForeignPrefix_Drops` — same setup but `To: operator-rg@sandboxes.example.com`; assert the Handle returns `nil` (silent drop) AND writes the expected `[operator-email] silently dropping ...` line to a captured stderr.

If these tests require additional helper scaffolding (captured-stderr helper, fake S3-event builder), add it here as unexported test helpers.

**`pkg/aws/ses_test.go`** — append one test function (W0-10):
  - `TestSendCreateNotification_OperatorAddressUsesPrefix` — drive `SendCreateNotification` (or whichever function holds the literal at line 271); pass `resource_prefix="kph"`, `email_subdomain="sandboxes"`, `domain="example.com"`; assert the body string contains `operator-kph@sandboxes.example.com` and does NOT contain bare `operator@`.

**Field-name verification (per plan-checker iteration 1):** If the test drives a function that takes a `*platformConfig`, use `Domain` (not `ParentDomain`). See `internal/app/cmd/configure.go` lines 22-33.

For each new test function: `t.Skip()` is NOT acceptable. Do NOT modify any production code.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go vet ./cmd/email-create-handler/... ./pkg/aws/... 2>&1 | head -20 && go test ./cmd/email-create-handler/ -run 'TestHandle_OperatorAddress_OwnPrefix|TestHandle_OperatorAddress_ForeignPrefix_Drops' -count=1 2>&1 | tail -10 && go test ./pkg/aws/ -run TestSendCreateNotification_OperatorAddressUsesPrefix -count=1 2>&1 | tail -10</automated>
  </verify>
  <done>Both files updated. `go vet` highlights the missing production symbols. `go test` reports RED for each of the 3 test names.</done>
</task>


<task type="auto">
  <name>Task 2: New test files W0-08 (doctor_ses_rules_test.go) + W0-09 (userdata_84_test.go)</name>
  <files>internal/app/cmd/doctor_ses_rules_test.go, pkg/compiler/userdata_84_test.go</files>
  <action>
Create two NEW test files.

**`internal/app/cmd/doctor_ses_rules_test.go`** — Pattern from `doctor_slack_inbound_test.go`:
  - Package `cmd`.
  - Define `mockSESReceiptRuleAPI` struct implementing the `SESReceiptRuleAPI` interface that task 84-07 will introduce in `doctor.go`. Method: `DescribeReceiptRuleSet(ctx context.Context, params *ses.DescribeReceiptRuleSetInput, optFns ...func(*ses.Options)) (*ses.DescribeReceiptRuleSetOutput, error)`.
  - To avoid pulling the classic `aws-sdk-go-v2/service/ses` import before 84-07 lands, gate this file with a Go build tag `//go:build phase84_doctor` (planner has chosen this approach to keep Wave 0 dependency-free; task 84-07 will remove the tag once go.mod includes the SDK). Document the build tag in a top-of-file comment.
  - Provide one consolidated `TestCheckSESRules` umbrella test that wires the mock and exercises both happy/orphan paths — this is W0-08.

**`pkg/compiler/userdata_84_test.go`** — Pattern from `pkg/compiler/userdata_82_02_test.go`:
  - Package `compiler`.
  - One test function `TestUserdata_KmSendOperatorAddressUsesEnvVar`:
    - Construct a minimal `Compiler` / userdata-generation input matching the existing userdata test pattern (use the smallest fixture that exercises the heredoc emitted at userdata.go line 1621 and 1653).
    - Call the userdata generator.
    - Assert: the generated userdata contains `${KM_OPERATOR_EMAIL}` (env-var reference, the Phase 84 target) AND does NOT contain `operator@${KM_SANDBOX_DOMAIN}` or any other bare `operator@` literal in those two heredoc blocks.
    - Acceptable to also assert the env-var is exported earlier in userdata (e.g., from `/etc/profile.d/...`).

Both files are RED at end of this task — the production symbols/strings they expect don't exist yet.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go test -tags phase84_doctor ./internal/app/cmd/ -run TestCheckSESRules -count=1 2>&1 | tail -10 && go test ./pkg/compiler/ -run TestUserdata_KmSendOperatorAddressUsesEnvVar -count=1 2>&1 | tail -10</automated>
  </verify>
  <done>Both files exist, compile (with build tag where applicable), and RUN to RED — assertions fail because production code hasn't shipped.</done>
</task>

<task type="auto">
  <name>Task 3: Makefile grep gate W0-11 (Phase 82.1 leftover scanner — initial RED state)</name>
  <files>Makefile</files>
  <action>
Add the W0-11 grep gate to the Makefile. W0-04 and W0-05 are now covered by Task 1b (split per plan-checker iteration 1, Minor 11).

**`Makefile`** — append a new target near the existing test targets:
```makefile
.PHONY: test-no-82.1-leftovers
test-no-82.1-leftovers:
	@! grep -rn "KM_SES_ACTIVATE_RULESET\|activate_rule_set" infra/ internal/ pkg/ cmd/ OPERATOR-GUIDE.md CLAUDE.md \
		|| (echo "Phase 82.1 leftovers found — see Phase 84"; exit 1)
```

Note: this target's grep is intentionally UN-scoped at Wave 0 — it WILL match `infra/modules/ses/v1.0.0/` and `.terragrunt-cache/` content. That's intentional: Wave 0's job is to land a RED gate. Plan 84-08 Task 3 updates the grep to add `--exclude-dir='v1.0.0' --exclude-dir='.terragrunt-cache'` filters AFTER the OPERATOR-GUIDE.md deletions land, so the gate turns GREEN.

This target is RED at the end of Wave 0 because Phase 82.1 leftovers are still present:
- `infra/modules/ses/v1.0.0/` (historical — stays untouched per CONTEXT.md lock)
- `infra/live/use1/ses/terragrunt.hcl:47` (Plan 84-03 cleans this)
- `OPERATOR-GUIDE.md` lines 646-677 (Plan 84-08 deletes this)

If a `test` umbrella target exists, do NOT yet add `test-no-82.1-leftovers` to its deps — that wiring happens in Plan 84-08 alongside the deletions, to avoid breaking the existing CI green status during Wave 1.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && make test-no-82.1-leftovers 2>&1 | tail -5; echo "exit=$?"</automated>
  </verify>
  <done>Makefile target exists. `make test-no-82.1-leftovers` exits non-zero with "Phase 82.1 leftovers found" message (RED — correct for Wave 0).</done>
</task>

</tasks>

<verification>
- All 11 W0 stubs (W0-01..W0-11 per 84-VALIDATION.md) exist.
- `go test ./...` produces failing/error output for the new test names (RED).
- `make test-no-82.1-leftovers` exits non-zero (RED).
- No production source files modified — `git diff --stat -- '*.go' ':!*_test.go'` is empty.
</verification>

<success_criteria>
- All Wave 0 artifacts on disk.
- 0 production code changes.
- Wave 0 RED state confirmed across all 11 stubs + Makefile gate.
- Subsequent Wave 1+ implementation tasks can reference these stubs in `<automated>` blocks.
</success_criteria>

<output>
After completion, create `.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-01-SUMMARY.md`
</output>
