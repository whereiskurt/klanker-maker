# Phase 59: Email sender allowlist for operator inbox and sandbox inbound - Context

**Gathered:** 2026-04-21
**Status:** Ready for planning

<domain>
## Phase Boundary

Add email sender allowlist enforcement at two levels:
1. **Operator inbox** — only approved sender email addresses can reach operator@sandboxes.klankermaker.ai (enforced by email-create-handler Lambda, alongside existing safe phrase)
2. **Sandbox inbound** — extend existing `spec.email.allowedSenders` to support external email address patterns (not just sandbox IDs/aliases)

</domain>

<decisions>
## Implementation Decisions

### Operator allowlist configuration
- New field in `km-config.yaml`: `email.allowedSenders` (list of email patterns)
- Patterns: exact email (`user@domain.com`), domain wildcard (`*@domain.com`)
- Config is uploaded to S3 toolchain during `km init --sidecars` (already happens for km-config.yaml)
- Lambda reads the config from S3 at invocation time (already fetches km-config.yaml)

### Sandbox external email patterns
- Extend existing `spec.email.allowedSenders` field — no new fields
- Detect email patterns by presence of `@` in the pattern string
- New patterns: `"user@domain.com"` (exact), `"*@domain.com"` (domain wildcard)
- Existing patterns unchanged: `"self"`, `"*"`, `"sb-abc123"`, `"build.*"` (alias wildcard)

### Enforcement points
- **Operator inbox**: email-create-handler Lambda — check sender email against config allowlist BEFORE safe phrase validation. Reject silently if not on list (same as current safe phrase rejection behavior)
- **Sandbox inbound**: extend `MatchesAllowList()` in `pkg/aws/identity.go` to handle `@`-containing patterns. Already called by `ParseSignedMessage()` in km-recv

### Claude's Discretion
- Pattern matching implementation details (fnmatch vs regex for domain wildcards)
- Error message format for operator rejections
- Whether to log rejected sender addresses (yes, for audit trail)

</decisions>

<specifics>
## Specific Ideas

- The `MatchesAllowList()` function at `pkg/aws/identity.go:379-401` already handles sandbox ID and alias patterns. Add a branch: if pattern contains `@`, treat as email pattern match against the sender's From address
- For operator inbox, the email-create-handler Lambda at `cmd/email-create-handler/main.go` already extracts the From header. Add allowlist check before `extractKMAuth()`
- Config struct at `internal/app/config/config.go` needs new field: `EmailAllowedSenders []string`
- km-config.yaml already gets uploaded to S3 via `km init --sidecars` at `uploadCreateHandlerToolchain()`

</specifics>

<deferred>
## Deferred Ideas

- SES-level sender filtering (not supported by SES receipt rules — only recipient matching)
- Per-sandbox allowlists for external senders pushed to Lambda (would require Lambda-side profile parsing)

</deferred>

---

*Phase: 59-email-sender-allowlist-for-operator-inbox-and-sandbox-inbound*
*Context gathered: 2026-04-21 via brainstorming discussion*
