---
phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
plan: 06
type: execute
wave: 1
depends_on: [01]
files_modified:
  - cmd/email-create-handler/main.go
autonomous: true
requirements:
  - SES-HANDLER-LOOKUP
  - SES-PREFIX-ADDRESS

must_haves:
  truths:
    - "The email-create-handler Lambda accepts emails to `operator-${ownPrefix}@${domain}` AND silently drops emails to `operator-${foreignPrefix}@${domain}` with an INFO-level stderr log line"
    - "The outbound-reply `From` literal at `cmd/email-create-handler/main.go:861` interpolates the derived operator address (no bare `operator@` literal remains in this file)"
    - "Recipient verification happens BEFORE the existing allowlist + safe-phrase checks (so foreign emails are dropped cheaply, not analyzed)"
    - "Test stubs W0-04 (`TestHandle_OperatorAddress_OwnPrefix`) and W0-05 (`TestHandle_OperatorAddress_ForeignPrefix_Drops`) pass"
  artifacts:
    - path: "cmd/email-create-handler/main.go"
      provides: "Recipient verification gate at Handle entry + outbound reply From interpolation"
  key_links:
    - from: "cmd/email-create-handler/main.go (Handle method)"
      to: "MIME message To: header"
      via: "mail.ParseAddress or AddressList of msg.Header.Get(\"To\")"
      pattern: "Header.Get\\(\"To\"\\)|AddressList\\(\"To\"\\)"
    - from: "cmd/email-create-handler/main.go (outbound reply)"
      to: "Derived operator address for the From line"
      via: "fmt.Sprintf with resource prefix"
      pattern: "operator-"
---

<objective>
Add recipient verification to the email-create-handler Lambda: accept emails addressed to this install's `operator-${prefix}@${domain}` AND silently drop emails addressed to other prefixes (RESEARCH § Pattern 2 + CONTEXT.md "email-handler Lambda resolution").

Also replace the outbound-reply `From` literal at line 861 (RESEARCH § Anti-Patterns + CONTEXT.md "Additional Decisions" § Scope additions item 3).

Purpose: When the foundation rule set is shared and BOTH installs' catchall rules fire on every sandbox-domain email, the Lambda must defensively verify that the inbound mail is for this install's operator. Foreign mail (a sender misroutes to the wrong install) is dropped cleanly with an INFO log line.

Output:
- 1 file modified: `cmd/email-create-handler/main.go`
- 2 Wave 0 stubs (W0-04, W0-05) turn GREEN
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-CONTEXT.md
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-RESEARCH.md

@cmd/email-create-handler/main.go
@cmd/email-create-handler/main_test.go

<interfaces>
<!-- Surface changes to the Handle pipeline. -->

Existing Handle entry path (RESEARCH § Pattern 2 + Code Examples):
- The Lambda already reads `KM_RESOURCE_PREFIX` via `resourcePrefix()` helper at lines 113-118.
- The Lambda already reads a domain — confirm exact env var (likely `KM_EMAIL_DOMAIN` or constructed from `KM_EMAIL_SUBDOMAIN` + `KM_DOMAIN`).
- The Handle method (~line 230+) extracts the sender via `mail.ParseAddress(msg.Header.Get("From"))`. We add an analogous extraction for `To:`.

New gate code (insert BEFORE the existing allowlist/safe-phrase checks):

```go
// Phase 84: verify the recipient belongs to THIS install. The foundation
// rule set is shared across installs in this account; a misrouted email
// to another install's operator address would land in our S3 prefix if
// the rule were misconfigured. Drop it silently with an INFO log.
toAddrs, _ := msg.Header.AddressList("To")
deliveredTo := ""
for _, a := range toAddrs {
    if a != nil {
        deliveredTo = strings.ToLower(a.Address)
        break
    }
}
expectedAddr := strings.ToLower(fmt.Sprintf("operator-%s@%s",
    resourcePrefix(), h.Domain))
if deliveredTo != expectedAddr {
    fmt.Fprintf(os.Stderr, "[operator-email] silently dropping email to %s (expected %s)\n",
        deliveredTo, expectedAddr)
    return nil
}
// existing safe-phrase + allowlist + sandbox-create flow follows
```

Outbound-reply From at line ~861:
- Before: likely `from := fmt.Sprintf("operator@%s", h.Domain)` or similar.
- After: `from := fmt.Sprintf("operator-%s@%s", resourcePrefix(), h.Domain)`.

Domain resolution caveat: The handler reads its domain from an env var. If today it's reading a value that DOES include the email subdomain (e.g., `KM_EMAIL_DOMAIN=sandboxes.example.com`), use it as-is. If it's reading just `KM_DOMAIN=example.com` and constructing the domain elsewhere, use the same construction at the verification point. Read the existing code carefully.

Multiple `To:` recipients: if the email has multiple To-addresses (rare but possible, e.g., CC parsed as To by SES), check if ANY of them match the expected address. The example above takes only the first — refine to a loop if needed.

Test contract (must pass after this plan):
- W0-04 `TestHandle_OperatorAddress_OwnPrefix`: env `KM_RESOURCE_PREFIX=kph`, MIME `To: operator-kph@sandboxes.example.com` → existing pipeline runs (the test asserts the silent-drop path is NOT taken).
- W0-05 `TestHandle_OperatorAddress_ForeignPrefix_Drops`: env `KM_RESOURCE_PREFIX=kph`, MIME `To: operator-rg@sandboxes.example.com` → `Handle` returns `nil`, stderr contains `silently dropping`.
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Add recipient verification gate at Handle entry + replace outbound reply From literal</name>
  <files>cmd/email-create-handler/main.go</files>
  <behavior>
    - `Handle` extracts the To: address from the MIME message and compares (case-insensitively) against `operator-${resourcePrefix()}@${h.Domain}`.
    - On match: existing pipeline (allowlist, safe-phrase, sandbox-create) runs unchanged.
    - On mismatch: writes `[operator-email] silently dropping email to <toAddr> (expected <expected>)\n` to stderr, returns nil.
    - The outbound-reply From line (line ~861) uses the same derived address — no bare `operator@%s` literal remains in main.go.
    - W0-04 and W0-05 stubs pass.
  </behavior>
  <action>
1. Open `cmd/email-create-handler/main.go`. Locate the `Handle` method (around line 230 per RESEARCH).

2. Find the existing sender-extraction code (typically `From:` parsing). Immediately AFTER that block and BEFORE the safe-phrase / allowlist checks, insert the recipient verification block from the `<interfaces>` example.

3. Confirm `h.Domain` (or equivalent struct field) exists and holds the email domain. If it's named differently or sourced from a method call, adjust accordingly. If no such field exists, read it from `os.Getenv("KM_EMAIL_DOMAIN")` (or whichever env var the Lambda configuration uses today — read main.go's env-var section to confirm).

4. Use case-insensitive comparison (`strings.ToLower`) — email addresses are case-insensitive in the local part by most modern conventions, but SES delivery preserves the casing from the recipient header.

5. Locate the line ~861 `From:` literal — `grep -n "operator@" cmd/email-create-handler/main.go`. Replace with the derived address using `resourcePrefix()` and the same domain source as the verification gate.

6. Search for any OTHER `operator@` literals in `cmd/email-create-handler/` (`grep -rn "operator@" cmd/email-create-handler/`). Replace all production occurrences (test files are separate). The expected outcome: `grep -n "operator@" cmd/email-create-handler/main.go` returns ZERO matches in production code paths.

7. Run `gofmt -w cmd/email-create-handler/main.go`. Run `go vet ./cmd/email-create-handler/...`. Run the Wave 0 tests.

8. If the Lambda has a build step (e.g., `make build-email-handler` per CLAUDE.md "Important workflow notes"), ensure the build still succeeds.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go test ./cmd/email-create-handler/ -run 'TestHandle_OperatorAddress_OwnPrefix|TestHandle_OperatorAddress_ForeignPrefix_Drops' -count=1 2>&1 | tail -15 && ! grep -n "operator@" cmd/email-create-handler/main.go || echo "WARN: residual operator@ literal — verify it is a comment or doc reference"</automated>
  </verify>
  <done>W0-04 and W0-05 pass. No bare `operator@` literal in main.go production code. `go vet` clean. Lambda build (if applicable) succeeds.</done>
</task>

</tasks>

<verification>
- W0-04 and W0-05 stubs from Plan 84-01 turn GREEN.
- The Handle pipeline drops foreign-prefix emails BEFORE running the allowlist/safe-phrase checks (cheap dispatch).
- The outbound reply From uses the prefix-aware address.
- No regression in existing email-handler tests — `go test ./cmd/email-create-handler/ -count=1`.
</verification>

<success_criteria>
- All email-handler-related Wave 0 stubs pass.
- The Lambda is structurally ready for the shared-rule-set deployment: cross-install email contamination is bounded (read-only — the foreign install's mail-handler will silently drop our messages too).
</success_criteria>

<output>
After completion, create `.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-06-SUMMARY.md`
</output>
