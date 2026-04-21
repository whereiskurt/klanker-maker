# Phase 59: Email Sender Allowlist - Research

**Researched:** 2026-04-19
**Domain:** Email sender filtering (operator inbox + sandbox inbound)
**Confidence:** HIGH

## Summary

This phase adds sender allowlist enforcement at two levels: (1) operator inbox via the email-create-handler Lambda, and (2) sandbox inbound via the existing `spec.email.allowedSenders` profile field. The codebase already has the data model and storage for `allowedSenders` in DynamoDB (`km-identities` table) and the `MatchesAllowList()` function in `pkg/aws/identity.go`, but enforcement is currently NOT active anywhere in production -- the only call to `ParseSignedMessage` passes `["*"]` (allow all), and the bash km-recv script has no sender filtering at all.

The operator inbox path is straightforward: the Lambda already extracts the sender email address and has access to the S3 artifacts bucket where `km-config.yaml` is stored (at `toolchain/km-config.yaml`). Adding an allowlist check before safe phrase validation requires loading config from S3 and matching the sender email against patterns.

For sandbox inbound, the key insight is that the bash km-recv script on the sandbox does NOT use the Go library -- it's pure bash/AWS CLI. The allowedSenders patterns are stored in DynamoDB but never queried by km-recv. Enforcement needs to be added to the bash script (query DynamoDB for allowed_senders, match against sender From header or X-KM-Sender-ID).

**Primary recommendation:** Extend `MatchesAllowList()` to handle `@`-containing email patterns, add operator allowlist to Lambda from km-config.yaml via S3, and add bash-side sender filtering to km-recv using DynamoDB-stored allowedSenders.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- New field in `km-config.yaml`: `email.allowedSenders` (list of email patterns)
- Patterns: exact email (`user@domain.com`), domain wildcard (`*@domain.com`)
- Config is uploaded to S3 toolchain during `km init --sidecars` (already happens for km-config.yaml)
- Lambda reads the config from S3 at invocation time (already fetches km-config.yaml)
- Extend existing `spec.email.allowedSenders` field -- no new fields
- Detect email patterns by presence of `@` in the pattern string
- New patterns: `"user@domain.com"` (exact), `"*@domain.com"` (domain wildcard)
- Existing patterns unchanged: `"self"`, `"*"`, `"sb-abc123"`, `"build.*"` (alias wildcard)
- Operator inbox: email-create-handler Lambda -- check sender email against config allowlist BEFORE safe phrase validation. Reject silently if not on list
- Sandbox inbound: extend `MatchesAllowList()` in `pkg/aws/identity.go` to handle `@`-containing patterns

### Claude's Discretion
- Pattern matching implementation details (fnmatch vs regex for domain wildcards)
- Error message format for operator rejections
- Whether to log rejected sender addresses (yes, for audit trail)

### Deferred Ideas (OUT OF SCOPE)
- SES-level sender filtering (not supported by SES receipt rules -- only recipient matching)
- Per-sandbox allowlists for external senders pushed to Lambda (would require Lambda-side profile parsing)
</user_constraints>

## Architecture Patterns

### Current Email Flow

```
Operator Inbox:
  SES -> S3 (mail/create/) -> Lambda trigger -> email-create-handler
  Lambda: parse MIME -> extract sender -> validate KM-AUTH -> dispatch command

Sandbox Inbound:
  SES -> S3 (mail/) -> km-mail-poller (bash, polls S3 every 60s)
  -> downloads to /var/mail/km/new/ (filtered by To address only)
  -> km-recv (bash, reads local files, verifies signatures, displays)
```

### Key Files and Their Roles

| File | Role | Modification Needed |
|------|------|---------------------|
| `cmd/email-create-handler/main.go` | Lambda handler, lines 891-933 `main()`, line 191 safe phrase check | Add allowlist check before line 191 |
| `pkg/aws/identity.go:379-401` | `MatchesAllowList()` function | Add `@`-pattern branch in default case |
| `pkg/aws/mailbox.go:157` | `ParseSignedMessage()` calls `MatchesAllowList` | No change needed (already plumbed) |
| `internal/app/config/config.go` | Config struct + viper loading | Add `EmailAllowedSenders []string` field |
| `internal/app/cmd/configure.go:16-29` | `platformConfig` struct for km-config.yaml | Add `Email` sub-struct with `AllowedSenders` |
| `pkg/compiler/userdata.go:822-1206` | km-recv bash script | Add sender allowlist enforcement |
| `pkg/compiler/userdata.go:520-568` | km-mail-poller bash script | Consider filtering at poller level |
| `infra/modules/email-handler/v1.0.0/main.tf:246` | Lambda env vars | No change if reading from S3 |

### Pattern 1: Extending MatchesAllowList for Email Patterns

**What:** Add an email-pattern branch to `MatchesAllowList()` triggered by `@` presence in the pattern string.

**Implementation:**
```go
// In the default case of MatchesAllowList, after existing sandbox ID/alias checks:
default:
    // Existing: exact sandbox ID match
    if senderID == p {
        return true
    }
    // NEW: email pattern match (pattern contains @)
    if strings.Contains(p, "@") && senderEmail != "" {
        // Exact email match: "user@domain.com"
        if !strings.Contains(p, "*") {
            if strings.EqualFold(p, senderEmail) {
                return true
            }
        } else {
            // Domain wildcard: "*@domain.com"
            matched, err := path.Match(p, strings.ToLower(senderEmail))
            if err == nil && matched {
                return true
            }
        }
        continue // skip alias check for email patterns
    }
    // Existing: alias wildcard
    if senderAlias != "" {
        matched, err := path.Match(p, senderAlias)
        if err == nil && matched {
            return true
        }
    }
```

**Signature change required:** `MatchesAllowList` currently takes `(patterns []string, senderID, senderAlias, receiverSandboxID string)`. To support email matching, it needs an additional `senderEmail string` parameter. This will require updating all call sites:
- `pkg/aws/mailbox.go:248` -- pass the From header email
- `pkg/aws/identity_test.go` -- update all test calls

**Recommendation on pattern matching:** Use `path.Match` (already used for alias wildcards). It handles `*@domain.com` correctly since `*` matches any sequence of non-separator characters. Case-insensitive via `strings.ToLower`. No need for regex.

### Pattern 2: Lambda Allowlist from S3

**What:** The Lambda reads `km-config.yaml` from S3 at `toolchain/km-config.yaml` (already uploaded by `km init --sidecars`).

**Current state:**
- Lambda does NOT currently read km-config.yaml from S3
- Lambda uses only environment variables (KM_ARTIFACTS_BUCKET, KM_EMAIL_DOMAIN, etc.)
- Lambda already has S3 GetObject permission on the artifacts bucket
- `km-config.yaml` is already uploaded to `s3://<bucket>/toolchain/km-config.yaml` by `uploadCreateHandlerToolchain()`

**Implementation approach:**
```go
// In main() or at start of Handle():
// Fetch km-config.yaml from S3
configObj, err := h.S3Client.GetObject(ctx, &s3.GetObjectInput{
    Bucket: aws.String(h.ArtifactBucket),
    Key:    aws.String("toolchain/km-config.yaml"),
})
// Parse YAML, extract email.allowedSenders
// Match senderEmail against patterns before safe phrase check
```

**Key decision:** Read config every invocation vs cache. Lambda instances are reused, so reading from S3 each time ensures freshness after `km init --sidecars` updates. The S3 read is fast (<50ms) and the Lambda already reads larger objects.

### Pattern 3: Bash km-recv Sender Enforcement

**What:** The bash km-recv script needs sender allowlist enforcement.

**Current enforcement gap:** `allowedSenders` is stored in DynamoDB `km-identities` table under the `allowed_senders` attribute (StringSet), but km-recv never queries it.

**Options:**
1. **Query DynamoDB at read time** -- km-recv already queries DynamoDB for public keys, so adding an `allowed_senders` query is consistent
2. **Pre-seed to environment/file at provisioning** -- the compiler could write allowedSenders to an env var or config file during userdata generation
3. **Filter at km-mail-poller level** -- reject messages before they land in `/var/mail/km/new/`

**Recommendation:** Option 2 (pre-seed) is simplest and most reliable. Write an `ALLOWED_SENDERS` environment variable into the km-mail-poller systemd unit file (like `ALLOWED_SUFFIXES` is done for DNS). Then in km-recv, read from environment or a well-known file. This avoids DynamoDB queries on every km-recv invocation.

**Alternatively**, the km-mail-poller could enforce at download time by checking the X-KM-Sender-ID header against the allowedSenders patterns before writing to `/var/mail/km/new/`. This is more appropriate since it prevents unwanted messages from reaching the inbox at all.

### Anti-Patterns to Avoid
- **Don't add email patterns to Lambda env vars** -- the list could be long and env vars have size limits. Read from km-config.yaml in S3.
- **Don't skip case-insensitive comparison** -- email addresses are case-insensitive per RFC 5321.
- **Don't confuse From header with X-KM-Sender-ID** -- sandbox-to-sandbox uses X-KM-Sender-ID (a sandbox ID). External emails only have From header. The `@` detection distinguishes these.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Email pattern matching | Custom regex parser | `path.Match` (stdlib) | Already used for alias wildcards, handles `*@domain.com` |
| YAML config parsing | Custom parser | `gopkg.in/yaml.v3` (already in deps) | Standard, already used everywhere |
| Email address extraction | Manual string parsing | `net/mail.ParseAddress` (stdlib) | Already used in `extractEmail()` in the Lambda |

## Common Pitfalls

### Pitfall 1: Case sensitivity in email matching
**What goes wrong:** `User@Domain.Com` doesn't match `user@domain.com`
**Why it happens:** String comparison is case-sensitive by default
**How to avoid:** Always `strings.ToLower()` both pattern and sender email before comparison
**Warning signs:** Tests pass with lowercase but fail with mixed case

### Pitfall 2: From header format variations
**What goes wrong:** From header can be `"John Doe <john@example.com>"` or bare `john@example.com`
**Why it happens:** RFC 5322 allows multiple formats
**How to avoid:** Use `mail.ParseAddress()` or existing `extractEmail()` to normalize
**Warning signs:** Pattern matches bare email but not display-name format

### Pitfall 3: Empty allowlist behavior
**What goes wrong:** Empty `email.allowedSenders` in km-config.yaml blocks all emails or allows all
**How to avoid:** Define clear semantics -- empty/missing list means "no filtering" (allow all). This matches the existing MatchesAllowList behavior (returns false for empty, but self-mail bypass exists)
**Recommendation:** For operator inbox, empty/missing list = allow all (backward compatible). For sandbox, existing behavior: empty list = reject all (except self-mail bypass in ParseSignedMessage).

### Pitfall 4: Breaking existing sandbox ID/alias patterns
**What goes wrong:** Adding `@` detection inadvertently changes behavior for existing patterns
**How to avoid:** The `@` check is a SEPARATE branch -- only triggered when pattern contains `@`. Existing patterns (self, *, sandbox IDs, alias wildcards) never contain `@`.
**Warning signs:** Existing MatchesAllowList tests fail

### Pitfall 5: Lambda config freshness
**What goes wrong:** Lambda caches km-config.yaml across invocations, operator updates allowlist but Lambda uses stale config
**How to avoid:** Read config from S3 on every invocation (not cached in Lambda global state). S3 reads are fast enough for the email handler's latency budget.

## Code Examples

### MatchesAllowList with email pattern support
```go
// Source: pkg/aws/identity.go (proposed modification)
func MatchesAllowList(patterns []string, senderID, senderAlias, receiverSandboxID, senderEmail string) bool {
    for _, p := range patterns {
        switch p {
        case "*":
            return true
        case "self":
            if senderID == receiverSandboxID {
                return true
            }
        default:
            if senderID != "" && senderID == p {
                return true
            }
            // Email pattern: contains @ → match against sender's From address
            if strings.Contains(p, "@") && senderEmail != "" {
                lowerEmail := strings.ToLower(senderEmail)
                lowerPattern := strings.ToLower(p)
                if !strings.Contains(p, "*") {
                    if lowerEmail == lowerPattern {
                        return true
                    }
                } else {
                    if matched, err := path.Match(lowerPattern, lowerEmail); err == nil && matched {
                        return true
                    }
                }
                continue
            }
            if senderAlias != "" {
                matched, err := path.Match(p, senderAlias)
                if err == nil && matched {
                    return true
                }
            }
        }
    }
    return false
}
```

### Config struct addition
```go
// Source: internal/app/config/config.go (proposed addition)
// EmailAllowedSenders is the operator inbox sender allowlist.
// Patterns: exact email ("user@domain.com"), domain wildcard ("*@domain.com").
// When empty, all senders are permitted (backward compatible).
EmailAllowedSenders []string
```

### km-config.yaml format
```yaml
# Operator inbox sender allowlist
email:
  allowedSenders:
    - "admin@example.com"
    - "*@company.com"
```

### Lambda allowlist check (before safe phrase)
```go
// Source: cmd/email-create-handler/main.go (proposed addition in Handle())
// Step 4.5: Sender allowlist check (before safe phrase validation)
if len(h.AllowedSenders) > 0 {
    senderAllowed := false
    lowerSender := strings.ToLower(senderEmail)
    for _, pattern := range h.AllowedSenders {
        lowerPattern := strings.ToLower(pattern)
        if !strings.Contains(pattern, "*") {
            if lowerSender == lowerPattern {
                senderAllowed = true
                break
            }
        } else {
            if matched, _ := path.Match(lowerPattern, lowerSender); matched {
                senderAllowed = true
                break
            }
        }
    }
    if !senderAllowed {
        fmt.Fprintf(os.Stderr, "[operator-email] sender %s not in allowlist, dropping\n", senderEmail)
        return nil // silent rejection
    }
}
```

### Bash km-recv sender filtering
```bash
# In km-recv process_messages(), after parse_headers:
# Check sender against allowed list (from env or config file)
if [ -n "$KM_ALLOWED_SENDERS" ]; then
  sender_check="${HDR_SENDER_ID:-}"
  sender_email="${HDR_FROM}"
  allowed=false
  IFS=: read -ra PATTERNS <<< "$KM_ALLOWED_SENDERS"
  for p in "${PATTERNS[@]}"; do
    case "$p" in
      "*") allowed=true; break ;;
      "self")
        [ "$sender_check" = "$SANDBOX_ID" ] && allowed=true && break ;;
      *@*)
        # Email pattern match
        lower_email=$(echo "$sender_email" | tr '[:upper:]' '[:lower:]' | grep -oP '[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}')
        lower_pattern=$(echo "$p" | tr '[:upper:]' '[:lower:]')
        if [[ "$lower_pattern" == *"*"* ]]; then
          # Wildcard: *@domain.com
          domain="${lower_pattern#*@}"
          [[ "$lower_email" == *"@$domain" ]] && allowed=true && break
        else
          [ "$lower_email" = "$lower_pattern" ] && allowed=true && break
        fi
        ;;
      *)
        # Sandbox ID or alias pattern
        [ "$sender_check" = "$p" ] && allowed=true && break ;;
    esac
  done
  if ! $allowed; then
    # Skip this message silently (or move to skipped/)
    if $MARK_READ; then
      mkdir -p "$MAIL_DIR/skipped"
      mv "$msg_file" "$MAIL_DIR/skipped/$(basename "$msg_file")"
    fi
    continue
  fi
fi
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| No sender filtering | Safe phrase only (operator inbox) | Current | Anyone with the safe phrase can send |
| No sandbox sender filtering | allowedSenders field exists but not enforced | Current | Profile field is documentation-only |

**Current gaps:**
- Operator inbox relies solely on KM-AUTH safe phrase -- no sender identity check
- Sandbox `allowedSenders` stored in DynamoDB but never queried by km-recv bash script
- `MatchesAllowList()` only handles sandbox IDs and aliases, not email addresses

## Open Questions

1. **Should km-mail-poller filter at download time or km-recv at read time?**
   - What we know: km-mail-poller downloads to `/var/mail/km/new/`, km-recv reads from there
   - Recommendation: Filter at km-mail-poller level (prevent messages from landing). Also enforce in km-recv for belt-and-suspenders.

2. **How to pass allowedSenders to the sandbox bash environment?**
   - Option A: Environment variable in systemd unit (like ALLOWED_SUFFIXES for DNS): `Environment=KM_ALLOWED_SENDERS=self:*@company.com:sb-partner`
   - Option B: Write to a config file at `/opt/km/etc/allowed-senders`
   - Recommendation: Option A (env var, colon-separated), consistent with existing patterns in userdata.go

3. **Backward compatibility when email.allowedSenders is not set in km-config.yaml?**
   - Recommendation: Missing/empty = allow all senders (no filtering). This is backward compatible.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) |
| Config file | None needed |
| Quick run command | `go test ./pkg/aws/ -run TestMatchesAllowList -count=1` |
| Full suite command | `go test ./pkg/aws/ ./cmd/email-create-handler/ ./internal/app/config/ -count=1` |

### Phase Requirements -> Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| AL-01 | MatchesAllowList handles exact email pattern | unit | `go test ./pkg/aws/ -run TestMatchesAllowList_EmailExact -x` | No (Wave 0) |
| AL-02 | MatchesAllowList handles domain wildcard pattern | unit | `go test ./pkg/aws/ -run TestMatchesAllowList_EmailDomainWildcard -x` | No (Wave 0) |
| AL-03 | MatchesAllowList email matching is case-insensitive | unit | `go test ./pkg/aws/ -run TestMatchesAllowList_EmailCaseInsensitive -x` | No (Wave 0) |
| AL-04 | MatchesAllowList existing patterns still work (regression) | unit | `go test ./pkg/aws/ -run TestMatchesAllowList -x` | Yes |
| AL-05 | Lambda rejects sender not on operator allowlist | unit | `go test ./cmd/email-create-handler/ -run TestHandle_SenderNotAllowed -x` | No (Wave 0) |
| AL-06 | Lambda allows sender on operator allowlist | unit | `go test ./cmd/email-create-handler/ -run TestHandle_SenderAllowed -x` | No (Wave 0) |
| AL-07 | Lambda allows all when allowlist is empty | unit | `go test ./cmd/email-create-handler/ -run TestHandle_EmptyAllowlist -x` | No (Wave 0) |
| AL-08 | Config loads email.allowedSenders from km-config.yaml | unit | `go test ./internal/app/config/ -run TestEmailAllowedSenders -x` | No (Wave 0) |

### Sampling Rate
- **Per task commit:** `go test ./pkg/aws/ -run TestMatchesAllowList -count=1`
- **Per wave merge:** `go test ./pkg/aws/ ./cmd/email-create-handler/ ./internal/app/config/ -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/aws/identity_test.go` -- add TestMatchesAllowList_Email* tests (extend existing file)
- [ ] `cmd/email-create-handler/main_test.go` -- add TestHandle_SenderNotAllowed/Allowed tests (extend existing file)
- [ ] `internal/app/config/config_test.go` -- add TestEmailAllowedSenders (may need to create)

## Sources

### Primary (HIGH confidence)
- `pkg/aws/identity.go:379-401` -- MatchesAllowList current implementation
- `pkg/aws/mailbox.go:157-297` -- ParseSignedMessage with allowedSenders plumbing
- `cmd/email-create-handler/main.go:136-265` -- Lambda Handle() flow
- `cmd/email-create-handler/main.go:891-933` -- Lambda main() config loading
- `internal/app/config/config.go:17-129` -- Config struct and viper loading
- `pkg/compiler/userdata.go:520-568` -- km-mail-poller bash script
- `pkg/compiler/userdata.go:822-1206` -- km-recv bash script
- `pkg/profile/types.go:80-83` -- EmailSpec.AllowedSenders field
- `infra/modules/email-handler/v1.0.0/main.tf:246-257` -- Lambda env vars
- `internal/app/cmd/init.go:1335-1336` -- km-config.yaml upload to S3

### Secondary (MEDIUM confidence)
- `internal/app/cmd/configure.go:16-29` -- platformConfig struct (may need email sub-struct)

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH -- all code paths examined, no external libraries needed
- Architecture: HIGH -- clear modification points identified, existing patterns to follow
- Pitfalls: HIGH -- edge cases well understood from existing code review

**Research date:** 2026-04-19
**Valid until:** 2026-05-19 (stable codebase, no external dependency changes)
