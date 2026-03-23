# Phase 17: Sandbox Email Mailbox & Access Control — Aliases, Allow-Lists, Self-Mail, S3 Reader - Research

**Researched:** 2026-03-23
**Domain:** DynamoDB GSI, Go net/mail MIME parsing, S3 prefix access, profile schema extension, allow-list pattern matching
**Confidence:** HIGH

---

## Summary

Phase 17 adds three distinct capabilities on top of the completed Phase 4 (SES/S3 storage) and Phase 14 (Ed25519 identity/DynamoDB) foundations:

1. **Sandbox aliases** — a `spec.email.alias` field registers a human-readable dot-notation name (e.g. `research.team-a`) in the `km-identities` DynamoDB table. A new GSI (`alias-index`) on the alias attribute enables efficient `FetchPublicKeyByAlias()` lookups without scanning. The alias is stored as an additional attribute on the existing identity row — no new table needed.

2. **Email allow-lists** — a `spec.email.allowedSenders[]` field carries an array of patterns (`"self"`, sandbox IDs, wildcard alias patterns like `build.*`, or `"*"`). The allow-list is stored as a DynamoDB list attribute on the identity row and evaluated in `pkg/aws/mailbox.go` when a message is parsed: the sender ID in the `X-KM-Sender-ID` header is matched against the patterns. Self-mail is always permitted regardless of the allow-list.

3. **Mailbox reader library** (`pkg/aws/mailbox.go`) — `ListMailboxMessages()` lists S3 objects under `mail/{sandbox-id}/`, `ReadMessage()` retrieves raw MIME bytes, and `ParseSignedMessage()` parses the MIME headers and body using Go stdlib `net/mail`, extracts `X-KM-*` headers, verifies the Ed25519 signature using the existing `VerifyEmailSignature()` from `identity.go`, and enforces the allow-list. The library handles both signed/encrypted and plaintext messages.

No new AWS services are required. The Go stdlib `net/mail` package handles MIME parsing. The DynamoDB GSI addition requires a Terraform module update plus a Terraform state migration step (adding a GSI to an existing table). The allow-list pattern matching is pure Go string operations — no additional libraries needed.

**Primary recommendation:** Add the alias GSI to the existing `dynamodb-identities` Terraform module (v1.1.0), store alias and `allowed_senders` as new attributes on the existing identity row, implement `FetchPublicKeyByAlias()` using a DynamoDB `Query` on the GSI, and implement `pkg/aws/mailbox.go` following the narrow-interface pattern established in `budget.go`, `identity.go`, and `ses.go`. Built-in profile defaults hardened/sealed get `allowedSenders: ["self"]`; open-dev/restricted-dev get `allowedSenders: ["*"]`.

---

## Standard Stack

### Core (zero new external dependencies)
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `net/mail` | stdlib | Parse raw MIME email: headers, body, From/To/Subject | Standard Go MIME parser; handles RFC 5322 message format; already available |
| `mime/multipart` | stdlib | Parse multipart MIME bodies if needed | Paired with net/mail for complex message structures |
| `strings` | stdlib | Allow-list pattern matching (wildcards, prefix checks) | No external glob library needed for `build.*` patterns |
| `github.com/aws/aws-sdk-go-v2/service/s3` | v1.97.1 | ListObjectsV2, GetObject for mailbox reading | Already in go.mod as a direct dependency |
| `github.com/aws/aws-sdk-go-v2/service/dynamodb` | v1.57.0 | Query on alias GSI for FetchPublicKeyByAlias | Already in go.mod; `Query` already used in budget.go |

### Supporting (already in go.mod)
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue` | v1.20.36 | Marshal/unmarshal DynamoDB expression values | Used in budget.go Query; same pattern for GSI query |
| `golang.org/x/crypto/nacl/box` | v0.49.0 (indirect) | Decrypt NaCl-encrypted body in ParseSignedMessage | Only when `X-KM-Encrypted: true` header present |
| `encoding/base64` | stdlib | Decode signature/key bytes from MIME headers | Matches identity.go encoding convention |

**Installation:** No new dependencies. All needed packages are in go.mod already.

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `net/mail` for MIME parsing | External library (e.g. `jhillyerd/enmime`) | `net/mail` is sufficient for the single-part messages produced by `buildRawMIME()` in identity.go; external library only needed if processing arbitrary MIME multipart |
| DynamoDB GSI on alias | Separate alias→sandbox_id DynamoDB table | GSI on existing table is simpler, cheaper, avoids two-table consistency problems; DynamoDB billing is per-request so a sparse GSI has no cost waste |
| Wildcard matching with `path.Match` | Custom prefix match with `strings.HasSuffix` | `path.Match` handles `build.*` correctly; simpler than regex; no allocation |

---

## Architecture Patterns

### Recommended Project Structure Changes

```
pkg/aws/
├── identity.go         # existing — extend: alias store/fetch, allow-list enforcement
├── identity_test.go    # existing — extend: TestFetchPublicKeyByAlias, TestAllowListMatch
├── mailbox.go          # NEW — ListMailboxMessages, ReadMessage, ParseSignedMessage
└── mailbox_test.go     # NEW — mock S3API tests

infra/modules/dynamodb-identities/
├── v1.0.0/             # existing — do not modify
└── v1.1.0/             # NEW — adds alias GSI, allowed_senders attribute (schema-compatible)
    ├── main.tf
    ├── variables.tf
    └── outputs.tf

infra/live/use1/dynamodb-identities/
└── terragrunt.hcl      # update source to v1.1.0

pkg/profile/
├── types.go            # extend EmailSpec: Alias string, AllowedSenders []string
└── schemas/sandbox_profile.schema.json  # add alias, allowedSenders to spec.email

profiles/
├── open-dev.yaml       # add allowedSenders: ["*"]
├── restricted-dev.yaml # add allowedSenders: ["*"]
├── hardened.yaml       # add allowedSenders: ["self"]
└── sealed.yaml         # add allowedSenders: ["self"]

internal/app/cmd/
└── status.go           # extend printSandboxStatus: alias, allowedSenders summary
```

### Pattern 1: Alias Storage in Existing DynamoDB Row

The alias is stored as an additional attribute `alias` on the existing `km-identities` row keyed by `sandbox_id`. No new table. The GSI projects all attributes (`ALL` projection) so `FetchPublicKeyByAlias()` returns a full `IdentityRecord` including `sandbox_id`, `public_key`, `email_address`, etc.

The DynamoDB module is bumped to v1.1.0 (additive, backward-compatible change — existing rows without alias attribute simply don't appear in GSI results).

```terraform
# infra/modules/dynamodb-identities/v1.1.0/main.tf (additions)
attribute {
  name = "alias"
  type = "S"
}

global_secondary_index {
  name            = "alias-index"
  hash_key        = "alias"
  projection_type = "ALL"
}
```

### Pattern 2: FetchPublicKeyByAlias — GSI Query

Follows the exact Query pattern from `budget.go` (already uses `dynamodb.QueryInput` with expression attribute values). The `IdentityTableAPI` interface needs `Query` added — or a new `IdentityQueryAPI` interface can be defined to keep concerns narrow.

```go
// Source: mirrors budget.go Query pattern (lines 158-165)
out, err := client.Query(ctx, &dynamodb.QueryInput{
    TableName:              aws.String(tableName),
    IndexName:              aws.String("alias-index"),
    KeyConditionExpression: aws.String("alias = :alias"),
    ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
        ":alias": &dynamodbtypes.AttributeValueMemberS{Value: alias},
    },
    Limit: aws.Int32(1), // alias is unique; first result is the only result
})
```

Returns `(nil, nil)` if no result (alias not registered) — consistent with `FetchPublicKey()` semantics.

### Pattern 3: Allow-List Enforcement

Allow-list patterns are stored in DynamoDB as a `StringSet` attribute (`SS` type) on the identity row. At mailbox read time, `matchesAllowList()` is called with the sender's sandbox ID and alias.

```go
// matchesAllowList returns true if senderID/senderAlias is permitted by patterns.
// "self" matches when senderID == receiverSandboxID.
// "*" matches everything.
// Exact sandbox ID match: senderID == pattern.
// Wildcard alias: path.Match(pattern, senderAlias) — handles "build.*".
func matchesAllowList(patterns []string, senderID, senderAlias, receiverSandboxID string) bool {
    for _, p := range patterns {
        switch p {
        case "*":
            return true
        case "self":
            if senderID == receiverSandboxID {
                return true
            }
        default:
            if senderID == p {
                return true
            }
            if senderAlias != "" {
                if ok, _ := path.Match(p, senderAlias); ok {
                    return true
                }
            }
        }
    }
    return false
}
```

Self-mail is always permitted: if `X-KM-Sender-ID` matches the receiving sandbox ID, the message is accepted before checking the allow-list.

### Pattern 4: Mailbox Reader — pkg/aws/mailbox.go

Follows the narrow-interface pattern from `ses.go`, `identity.go`, and `budget.go`. New interface `MailboxS3API` wraps only what mailbox operations need.

```go
// MailboxS3API is the minimal S3 interface for mailbox operations.
// Implemented by *s3.Client.
type MailboxS3API interface {
    ListObjectsV2(ctx context.Context, input *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
    GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// MailboxMessage is a parsed inbound email.
type MailboxMessage struct {
    MessageID   string    // S3 object key (SES-assigned message ID)
    S3Key       string    // full S3 object key: mail/{sandbox-id}/{message-id}
    From        string
    To          string
    Subject     string
    Body        string    // decrypted plaintext body
    SenderID    string    // X-KM-Sender-ID header value
    SignatureOK bool      // true if Ed25519 signature verified successfully
    Encrypted   bool      // true if X-KM-Encrypted: true was present
    Plaintext   bool      // true if no X-KM-Signature header (legacy/external)
}

// ListMailboxMessages returns S3 keys for all messages under mail/{sandboxID}/.
func ListMailboxMessages(ctx context.Context, client MailboxS3API, bucket, sandboxID string) ([]string, error)

// ReadMessage retrieves raw MIME bytes for a single message.
func ReadMessage(ctx context.Context, client MailboxS3API, bucket, s3Key string) ([]byte, error)

// ParseSignedMessage parses raw MIME bytes, verifies signature, decrypts body if needed.
// Returns MailboxMessage; signature failure or allow-list violation returns error.
func ParseSignedMessage(rawMIME []byte, receiverSandboxID string, identityClient IdentityTableAPI, tableName string, allowedSenders []string) (*MailboxMessage, error)
```

S3 prefix for a sandbox's inbox: `mail/{sandbox-id}/` — derived from the existing SES receipt rule which stores all inbound email at `mail/` prefix. SES appends the message ID as the object key suffix. The per-sandbox sub-prefix `mail/{sandbox-id}/` does NOT currently exist in the SES receipt rule — SES stores everything flat at `mail/{message-id}`. This is an important gap: the current rule stores all sandbox emails in the same `mail/` prefix, not per-sandbox.

**Gap: SES receipt rule S3 prefix is flat (`mail/`), not per-sandbox (`mail/{sandbox-id}/`)**

The current receipt rule stores all emails at `mail/` prefix (see `infra/modules/ses/v1.0.0/main.tf` line 80). The `To:` address header contains the sandbox ID (`{sandbox-id}@{domain}`), but SES does not automatically route to per-sandbox prefixes unless separate receipt rules are defined per recipient.

**Resolution options:**
1. Keep the flat `mail/` prefix and filter by `To:` header in `ListMailboxMessages()` — scan all objects, read each, filter by recipient. Expensive at scale but correct for v1.
2. Add a prefix routing Lambda action before the S3 action that copies to `mail/{sandbox-id}/{message-id}`. More complexity, separate Lambda.
3. Accept the flat prefix and document that `ListMailboxMessages()` requires scanning `mail/` and filtering by `To:` header after reading each object's headers.

**Recommended for planning:** Option 1 — flat `mail/` prefix with `To:` header filtering. The IAM policy already scopes sandbox access to `mail/{sandbox-id}/*` (see `pkg/compiler/service_hcl.go` line 259), which means sandboxes cannot read each other's email at the S3 level. However, `ListMailboxMessages()` running from the operator context (not inside the sandbox) would need the flat prefix. A routing Lambda (option 2) is a natural v2 improvement.

**Alternative clean resolution:** Update the SES receipt rule to use a per-message-routing Lambda that copies to `mail/{to-sandbox-id}/{message-id}`. This keeps the per-sandbox S3 prefix clean and aligns with the IAM policy already written. This is architecturally cleaner.

The planner should make this decision. Both options are documented here.

### Pattern 5: PublishIdentity Extension

`PublishIdentity()` in `identity.go` needs two new parameters: `alias string` and `allowedSenders []string`. Following the existing "omit empty" pattern, alias is only written to DynamoDB when non-empty. `allowedSenders` is written as a `SS` (StringSet) when len > 0.

```go
// Additional attributes in PublishIdentity
if alias != "" {
    item["alias"] = &dynamodbtypes.AttributeValueMemberS{Value: alias}
}
if len(allowedSenders) > 0 {
    item["allowed_senders"] = &dynamodbtypes.AttributeValueMemberSS{Value: allowedSenders}
}
```

`FetchPublicKey()` needs to read these new attributes into `IdentityRecord` (two new fields: `Alias string` and `AllowedSenders []string`).

### Pattern 6: km status Display Extension

`printSandboxStatus()` in `status.go` gains two new lines in the Identity section:

```
Identity:
  Public Key:       abc123...
  Signing:          required
  Verify Inbound:   required
  Encryption:       off
  Alias:            research.team-a      ← new
  Allowed Senders:  self                 ← new (comma-joined)
```

When alias is empty string, the Alias line is omitted. When `AllowedSenders` is empty, shows "not configured".

### Anti-Patterns to Avoid

- **Don't modify v1.0.0 Terraform module** — create v1.1.0 with the GSI addition. Modifying v1.0.0 would break the state reference used in the existing live terragrunt.hcl.
- **Don't add Query to IdentityTableAPI** — the existing interface covers PutItem/GetItem/DeleteItem for the primary key. Create a separate `IdentityQueryAPI` interface with Query only, injected into `FetchPublicKeyByAlias()`. This keeps mock surface areas small in tests.
- **Don't perform allow-list enforcement inside SendSignedEmail** — allow-list is an inbound receive-time concept, not send-time. The sending sandbox has no knowledge of the receiver's allow-list at send time.
- **Don't use DynamoDB Scan for alias lookup** — always use the GSI Query. A full table scan would be O(n) over all sandboxes.
- **Don't block on allow-list violations in ParseSignedMessage** — return a structured error type so callers can distinguish "sender not on allow-list" from "signature invalid" from "S3 read error".

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| MIME email parsing | Custom byte scanner for headers | `net/mail.ReadMessage()` | RFC 5322 edge cases (folded headers, encoded words); stdlib is correct |
| Wildcard alias pattern matching | Regex or custom glob | `path.Match()` from stdlib | `build.*` → `path.Match("build.*", "build.team-a")` = true; correct, zero deps |
| DynamoDB marshaling of expression values | Manual `AttributeValueMemberS` construction | `attributevalue.Marshal()` from aws-sdk-go-v2/feature/dynamodb/attributevalue | Already used in budget.go; handles type inference |
| StringSet encoding for allowed_senders | JSON array stored as string | Native DynamoDB `SS` type (`AttributeValueMemberSS`) | Native set type enables future set operations (e.g. `CONTAINS` condition) |

**Key insight:** The `net/mail` package was designed for exactly this: parsing RFC 5322 messages. The messages produced by `buildRawMIME()` in `identity.go` are simple single-part text messages — `net/mail.ReadMessage()` handles them with two lines of code.

---

## Common Pitfalls

### Pitfall 1: GSI Addition Requires Table Recreation in Older Terraform Provider Versions
**What goes wrong:** Adding a GSI to an existing `aws_dynamodb_table` resource sometimes forces `terraform plan` to show `forces replacement` depending on how the attribute blocks are ordered in the resource.
**Why it happens:** The DynamoDB Terraform provider handles GSI additions as in-place updates (no table recreation) for tables created with `billing_mode = PAY_PER_REQUEST`. However, adding new `attribute` blocks for GSI keys requires careful ordering.
**How to avoid:** Add the new `attribute { name = "alias" type = "S" }` block alongside the existing `sandbox_id` attribute block. Run `terragrunt plan` before apply to confirm in-place update (not replacement). DynamoDB GSI creation on an existing table is an async AWS operation that takes ~30-60 seconds.
**Warning signs:** Plan output shows `-/+ destroy and then create` instead of `~ update in-place`.

### Pitfall 2: SES S3 Receipt Rule Stores Flat Key (Not Per-Sandbox Prefix)
**What goes wrong:** Code assumes emails are at `mail/{sandbox-id}/{message-id}` but they are actually at `mail/{message-id}`.
**Why it happens:** The current SES receipt rule (`infra/modules/ses/v1.0.0/main.tf`) uses `object_key_prefix = "mail/"` with no per-recipient routing. SES appends a UUID as the object key.
**How to avoid:** The planner must decide: (a) accept flat prefix and filter by `To:` header after listing, or (b) add a per-recipient routing Lambda/prefix. Document the chosen approach in the plan. The existing IAM policy `mail/{sandbox-id}/*` in service_hcl.go was written anticipating per-sandbox prefixes — it will need updating if flat prefix is chosen.
**Warning signs:** `ListMailboxMessages()` returns emails intended for other sandboxes.

### Pitfall 3: PublishIdentity Signature Change Breaks Existing Callers
**What goes wrong:** Adding alias and allowedSenders parameters to `PublishIdentity()` breaks the call site in `internal/app/cmd/create.go`.
**Why it happens:** Go is not variadic-default-friendly.
**How to avoid:** Either (a) add parameters to the function signature and update the single call site in `create.go`, or (b) create a new `PublishIdentityV2()` with the extended signature. Option (a) is preferred — there is only one call site.

### Pitfall 4: Alias Dot-Notation Validation
**What goes wrong:** Aliases like `../etc` or `sandbox123` (no dot) slip through without validation.
**Why it happens:** No validation on the `spec.email.alias` field.
**How to avoid:** Add a JSON schema pattern constraint on `alias`: `"pattern": "^[a-z][a-z0-9-]*\\.[a-z][a-z0-9-]*$"` — enforces dot-notation with lowercase alphanumeric segments. Validate in `km validate` via the schema.

### Pitfall 5: Allow-List Pattern with Wildcard Not Matching Lowercase
**What goes wrong:** `build.*` matches `build.team-a` but not `Build.team-a` — alias comparison is case-sensitive.
**Why it happens:** DynamoDB stores alias as written; `path.Match` is case-sensitive.
**How to avoid:** Normalize aliases to lowercase in schema validation and before DynamoDB writes. Document that all aliases must be lowercase (enforced by JSON schema pattern).

### Pitfall 6: net/mail Header Encoding (RFC 2047 Encoded Words)
**What goes wrong:** Subject header with non-ASCII characters appears as `=?UTF-8?Q?...?=` instead of decoded text.
**Why it happens:** `net/mail` does not automatically decode RFC 2047 encoded words.
**How to avoid:** Use `mime.WordDecoder{}.DecodeHeader(msg.Header.Get("Subject"))` for display purposes. For `X-KM-*` headers (which are base64 ASCII values), no decoding needed — they are not subject to encoded-word encoding.

---

## Code Examples

### List and Read Mailbox Messages (flat prefix — Option A)
```go
// Source: mirrors S3ListAPI pattern from sandbox.go + artifacts.go
func ListMailboxMessages(ctx context.Context, client MailboxS3API, bucket, sandboxID string) ([]string, error) {
    prefix := "mail/"
    out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
        Bucket: aws.String(bucket),
        Prefix: aws.String(prefix),
    })
    if err != nil {
        return nil, fmt.Errorf("list mailbox for %s: %w", sandboxID, err)
    }
    // Filter by reading To: header — or accept all and let ParseSignedMessage filter.
    // For v1: return all keys; caller filters by To: after ParseSignedMessage.
    keys := make([]string, 0, len(out.Contents))
    for _, obj := range out.Contents {
        if obj.Key != nil {
            keys = append(keys, *obj.Key)
        }
    }
    return keys, nil
}
```

### ParseSignedMessage (core logic)
```go
// Source: net/mail stdlib + existing VerifyEmailSignature from identity.go
func ParseSignedMessage(rawMIME []byte, receiverSandboxID string, allowedSenders []string) (*MailboxMessage, error) {
    msg, err := mail.ReadMessage(bytes.NewReader(rawMIME))
    if err != nil {
        return nil, fmt.Errorf("parse MIME: %w", err)
    }

    senderID := msg.Header.Get("X-KM-Sender-ID")
    sigB64   := msg.Header.Get("X-KM-Signature")
    encrypted := msg.Header.Get("X-KM-Encrypted") == "true"

    bodyBytes, err := io.ReadAll(msg.Body)
    if err != nil {
        return nil, fmt.Errorf("read body: %w", err)
    }
    body := string(bodyBytes)

    // Self-mail is always permitted
    if senderID == receiverSandboxID {
        // proceed
    } else if !matchesAllowList(allowedSenders, senderID, "", receiverSandboxID) {
        return nil, fmt.Errorf("sender %s not on allow-list for %s", senderID, receiverSandboxID)
    }

    // Verify signature if present
    signatureOK := false
    if sigB64 != "" {
        // caller must supply sender's pubKey for verification
        // (fetched via FetchPublicKey or FetchPublicKeyByAlias)
        // ParseSignedMessage takes pubKeyB64 as a parameter for the verification step
        if err := VerifyEmailSignature(pubKeyB64, body, sigB64); err == nil {
            signatureOK = true
        }
    }

    return &MailboxMessage{
        From:        msg.Header.Get("From"),
        To:          msg.Header.Get("To"),
        Subject:     msg.Header.Get("Subject"),
        Body:        body,
        SenderID:    senderID,
        SignatureOK: signatureOK,
        Encrypted:   encrypted,
        Plaintext:   sigB64 == "",
    }, nil
}
```

### FetchPublicKeyByAlias — GSI Query
```go
// Source: mirrors budget.go Query pattern (lines 158-165) + identity.go FetchPublicKey
func FetchPublicKeyByAlias(ctx context.Context, client IdentityQueryAPI, tableName, alias string) (*IdentityRecord, error) {
    aliasAV, err := attributevalue.Marshal(alias)
    if err != nil {
        return nil, fmt.Errorf("marshal alias: %w", err)
    }
    out, err := client.Query(ctx, &dynamodb.QueryInput{
        TableName:              aws.String(tableName),
        IndexName:              aws.String("alias-index"),
        KeyConditionExpression: aws.String("alias = :alias"),
        ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
            ":alias": aliasAV,
        },
        Limit: aws.Int32(1),
    })
    if err != nil {
        return nil, fmt.Errorf("query alias %s: %w", alias, err)
    }
    if len(out.Items) == 0 {
        return nil, nil // not found — consistent with FetchPublicKey semantics
    }
    return parseIdentityRecord(out.Items[0])
}
```

### EmailSpec Extension (types.go)
```go
// EmailSpec defines email signing, inbound verification, encryption, alias, and allow-list.
type EmailSpec struct {
    Signing        string   `yaml:"signing"`
    VerifyInbound  string   `yaml:"verifyInbound"`
    Encryption     string   `yaml:"encryption"`
    Alias          string   `yaml:"alias,omitempty"`          // NEW Phase 17
    AllowedSenders []string `yaml:"allowedSenders,omitempty"` // NEW Phase 17
}
```

### JSON Schema Addition
```json
"alias": {
  "type": "string",
  "description": "Human-friendly dot-notation name (e.g. 'research.team-a') registered in km-identities",
  "pattern": "^[a-z][a-z0-9-]*\\.[a-z][a-z0-9-]*$"
},
"allowedSenders": {
  "type": "array",
  "description": "Patterns controlling which sandboxes may send to this sandbox. 'self', sandbox IDs, alias wildcards (build.*), or '*'",
  "items": {
    "type": "string"
  }
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Single flat `mail/` S3 prefix for all sandboxes | Still flat (Phase 4 design) | Phase 17 must address | ListMailboxMessages needs To: header filtering or routing Lambda |
| No human-readable sandbox names | Sandbox alias in DynamoDB GSI | Phase 17 new | FetchPublicKeyByAlias enables alias→identity resolution |
| No inbound email control | Allow-list patterns in identity row | Phase 17 new | Receiving sandboxes enforce sender restrictions |
| km status shows 3 identity policy fields | Extended with alias + allowedSenders summary | Phase 17 new | Operators see full email config at a glance |

---

## Open Questions

1. **S3 Mailbox Prefix: Flat vs. Per-Sandbox**
   - What we know: Current SES receipt rule stores all email at `mail/{message-id}` (flat). IAM in service_hcl.go already scopes sandbox access to `mail/{sandbox-id}/*` (anticipating per-sandbox prefix).
   - What's unclear: Whether the plan should update the SES receipt rule with a routing Lambda (Option B) or accept the flat prefix and filter by `To:` header (Option A).
   - Recommendation: The planner should choose. Option A is simpler (no new Lambda); Option B aligns with existing IAM policies. This is a locked decision for the plan.

2. **`IdentityTableAPI` Interface Extension vs. New Interface**
   - What we know: The existing `IdentityTableAPI` covers PutItem/GetItem/DeleteItem. `FetchPublicKeyByAlias()` needs Query.
   - What's unclear: Whether to add `Query` to `IdentityTableAPI` (simpler, one interface) or create `IdentityQueryAPI` (narrower, follows existing DI pattern where budget.go has a separate `BudgetAPI`).
   - Recommendation: Create `IdentityQueryAPI` with only `Query` — consistent with the existing narrow-interface pattern where each operation set has its own interface.

3. **ParseSignedMessage Signature for Sender Key Lookup**
   - What we know: ParseSignedMessage needs the sender's public key to verify the Ed25519 signature. The sender's key must be fetched from DynamoDB using `FetchPublicKey(senderID)` or `FetchPublicKeyByAlias(senderAlias)`.
   - What's unclear: Whether ParseSignedMessage should accept a pre-fetched pubKeyB64 (simpler, caller does the lookup) or accept an IdentityTableAPI client and do the lookup itself (more self-contained).
   - Recommendation: Accept `pubKeyB64 string` as a parameter — keep ParseSignedMessage a pure function that doesn't touch DynamoDB. The caller fetches the key. This makes the function trivially testable without mocks.

4. **Alias Uniqueness Enforcement**
   - What we know: DynamoDB GSI does not enforce uniqueness — two sandboxes could register the same alias.
   - What's unclear: Whether to add a conditional write (`attribute_not_exists(alias)`) to prevent duplicate alias registration.
   - Recommendation: Yes — use `ConditionExpression: attribute_not_exists(alias)` in the alias write path (separate from the `attribute_not_exists(sandbox_id)` condition on initial PublishIdentity). This requires either a separate `UpdateItem` call for alias registration or a more complex condition expression on PutItem.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) — `go test ./...` |
| Config file | none (no external config) |
| Quick run command | `go test ./pkg/aws/... -run TestMailbox -v` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map
| Behavior | Test Type | Automated Command | File Exists? |
|----------|-----------|-------------------|-------------|
| FetchPublicKeyByAlias returns IdentityRecord for known alias | unit | `go test ./pkg/aws/... -run TestFetchPublicKeyByAlias -v` | No — Wave 0 |
| FetchPublicKeyByAlias returns nil for unknown alias | unit | `go test ./pkg/aws/... -run TestFetchPublicKeyByAlias_NotFound -v` | No — Wave 0 |
| matchesAllowList permits self-mail | unit | `go test ./pkg/aws/... -run TestMatchesAllowList -v` | No — Wave 0 |
| matchesAllowList permits wildcard `*` | unit | `go test ./pkg/aws/... -run TestMatchesAllowList -v` | No — Wave 0 |
| matchesAllowList enforces `build.*` pattern | unit | `go test ./pkg/aws/... -run TestMatchesAllowList -v` | No — Wave 0 |
| matchesAllowList rejects unlisted sender | unit | `go test ./pkg/aws/... -run TestMatchesAllowList_Reject -v` | No — Wave 0 |
| ListMailboxMessages returns S3 keys | unit | `go test ./pkg/aws/... -run TestListMailboxMessages -v` | No — Wave 0 |
| ReadMessage returns raw bytes | unit | `go test ./pkg/aws/... -run TestReadMessage -v` | No — Wave 0 |
| ParseSignedMessage parses MIME, verifies signature | unit | `go test ./pkg/aws/... -run TestParseSignedMessage -v` | No — Wave 0 |
| ParseSignedMessage accepts plaintext (no X-KM-Signature) | unit | `go test ./pkg/aws/... -run TestParseSignedMessage_Plaintext -v` | No — Wave 0 |
| ParseSignedMessage rejects denied sender | unit | `go test ./pkg/aws/... -run TestParseSignedMessage_AllowListDenied -v` | No — Wave 0 |
| km status shows Alias and Allowed Senders | unit | `go test ./internal/app/cmd/... -run TestStatus_EmailAlias -v` | No — Wave 0 |
| PublishIdentity stores alias and allowed_senders | unit | `go test ./pkg/aws/... -run TestPublishIdentity_WithAlias -v` | No — Wave 0 |
| EmailSpec parses alias and allowedSenders from YAML | unit | `go test ./pkg/profile/... -run TestParse_EmailAlias -v` | No — Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/aws/... ./pkg/profile/... -v`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/aws/mailbox_test.go` — covers ListMailboxMessages, ReadMessage, ParseSignedMessage
- [ ] `pkg/aws/identity_test.go` extensions — TestFetchPublicKeyByAlias, TestMatchesAllowList, TestPublishIdentity_WithAlias

*(All existing test files remain valid; only additions needed)*

---

## Sources

### Primary (HIGH confidence)
- Codebase: `pkg/aws/identity.go` — current IdentityRecord, PublishIdentity, FetchPublicKey, VerifyEmailSignature signatures
- Codebase: `pkg/aws/budget.go` — Query pattern with attributevalue.Marshal; DynamoDB narrow interface
- Codebase: `pkg/aws/ses.go` — SESV2API narrow interface pattern; email address format
- Codebase: `infra/modules/ses/v1.0.0/main.tf` — receipt rule S3 prefix `mail/` confirmed flat
- Codebase: `infra/modules/dynamodb-identities/v1.0.0/main.tf` — table schema, no existing GSI
- Codebase: `infra/live/use1/dynamodb-identities/terragrunt.hcl` — v1.0.0 source reference, table_name = "km-identities"
- Codebase: `pkg/profile/types.go` — EmailSpec (current fields), Spec (Email *EmailSpec pointer), Parse()
- Codebase: `pkg/profile/schemas/sandbox_profile.schema.json` — existing email enum values
- Codebase: `pkg/compiler/service_hcl.go:259` — existing IAM `mail/{sandbox-id}/*` prefix
- Codebase: `internal/app/cmd/status.go` — printSandboxStatus, IdentityFetcher, DI pattern for fetchers
- Go stdlib documentation — `net/mail.ReadMessage()`, `path.Match()` semantics
- AWS SDK Go v2 — DynamoDB `QueryInput.IndexName` for GSI queries

### Secondary (MEDIUM confidence)
- DynamoDB GSI in-place creation confirmed non-destructive for PAY_PER_REQUEST tables (standard AWS behavior, consistent with Terraform provider documentation)
- `path.Match` semantics for `build.*` pattern verified against Go stdlib spec

### Tertiary (LOW confidence — needs validation in plan)
- Alias uniqueness via `attribute_not_exists(alias)` conditional expression — conceptually correct but requires testing with actual DynamoDB client behavior for attribute-level conditional writes
- SES receipt rule per-recipient routing Lambda — described as an option but not prototyped

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all packages are in go.mod, all patterns are established in the codebase
- Architecture: HIGH — directly extends existing DynamoDB/S3/interface patterns; no new AWS services
- Pitfalls: HIGH — S3 prefix gap is confirmed from code inspection; GSI behavior is well-documented AWS
- Open questions: MEDIUM — alias uniqueness and ParseSignedMessage parameter design are implementation choices, both paths are safe

**Research date:** 2026-03-23
**Valid until:** 2026-04-23 (stable domain — AWS SDK v2 and DynamoDB behavior are stable)
