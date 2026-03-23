# Phase 14: Sandbox Identity & Signed Email — Ed25519 Key Pairs for Inter-Sandbox Trust - Research

**Researched:** 2026-03-22
**Domain:** Cryptographic identity (Ed25519 signing, X25519 encryption), AWS SSM/DynamoDB, email header extension
**Confidence:** HIGH

---

## Summary

Phase 14 introduces per-sandbox cryptographic identity: each sandbox gets an Ed25519 key pair at creation time, stores the private key in SSM (KMS-encrypted), and publishes the public key to a new `km-identities` DynamoDB table. Outbound SES emails are signed with the private key (custom `X-KM-Signature` + `X-KM-Sender-ID` headers), and receiving sandboxes can verify signatures by fetching the sender's public key from DynamoDB. Optional encryption is layered on via Ed25519-to-X25519 key conversion and NaCl box authenticated encryption.

The codebase has well-established patterns for all three AWS services involved: SSM PutParameter/GetParameter (in ConfigUI handlers_secrets.go), DynamoDB single-table (km-budgets pattern), and SES email sending (ses.go). This phase follows those patterns closely. No external signing dependencies are required — `crypto/ed25519` stdlib covers key generation, signing, and verification. For optional encryption, `golang.org/x/crypto` is already a transitive dependency at v0.49.0 and provides the NaCl box package and curve25519 package needed for key exchange. The Ed25519-to-X25519 conversion requires either `filippo.io/edwards25519` (new dependency) or can be avoided by generating separate X25519 key pairs specifically for encryption.

**Primary recommendation:** Generate Ed25519 for signing (stdlib, no deps) and generate a separate, independent X25519 key pair using `golang.org/x/crypto/nacl/box.GenerateKey()` for encryption, storing both in SSM under clearly named paths. This avoids the Ed25519→X25519 conversion complexity and the `filippo.io/edwards25519` dependency, while keeping the signing and encryption use cases cleanly separated.

---

## Standard Stack

### Core (Go stdlib — zero new dependencies for signing)
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `crypto/ed25519` | stdlib (Go 1.13+) | Key generation, signing, verification | No external deps; spec-compliant (RFC 8032); 32-byte keys; fast |
| `crypto/rand` | stdlib | Cryptographically secure random source for key generation | Standard practice; required by ed25519.GenerateKey |
| `encoding/base64` | stdlib | Encode private/public key bytes for SSM storage and DynamoDB | Already used throughout the codebase for similar encoding |

### Supporting (already transitive in go.mod)
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `golang.org/x/crypto/nacl/box` | v0.49.0 (transitive) | NaCl authenticated encryption for optional `email.encryption` | Only when `encryption: optional/required` is set in profile |
| `golang.org/x/crypto/nacl/secretbox` | v0.49.0 (transitive) | Underlying primitive used by box | Indirect; not used directly |
| `golang.org/x/crypto/curve25519` | v0.49.0 (transitive) | X25519 key exchange (wrapped by nacl/box) | Only needed if doing separate X25519 generation |
| `github.com/aws/aws-sdk-go-v2/service/ssm` | v1.68.3 | PutParameter, GetParameter, DeleteParameter for signing key | Already imported in configui and pkg/aws patterns |
| `github.com/aws/aws-sdk-go-v2/service/dynamodb` | v1.57.0 | PutItem, GetItem, DeleteItem on km-identities table | Already used in budget.go — same client |
| `github.com/aws/aws-sdk-go-v2/service/sesv2` | v1.60.1 | SendEmail with custom headers for X-KM-Signature | Already used in ses.go |

**Key insight:** `golang.org/x/crypto` is already in the module graph at v0.49.0 as a transitive dependency. The `nacl/box` package is available without adding any new `require` line in go.mod — only `go get golang.org/x/crypto` as a direct dependency is needed, which is a minor go.mod promotion.

**Installation (promote to direct dependency):**
```bash
go get golang.org/x/crypto@v0.49.0
```

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Generate separate X25519 key pair for encryption | Convert Ed25519→X25519 | Conversion requires `filippo.io/edwards25519` (new external dep); separate keys are simpler, auditable, and independently rotatable |
| `nacl/box` for encryption | `crypto/ecdh` + AES-GCM | `nacl/box` is higher-level, already in module graph, correct for email payload encryption; `ecdh` requires more assembly |
| Custom SSM key path in Go | Terraform-managed SSM param | Private keys must be written at sandbox creation time by `km create` — not at Terraform apply time — because the key must be generated fresh per sandbox in Go |

---

## Architecture Patterns

### New Package: `pkg/aws/identity.go`

Following the `ses.go` and `budget.go` narrow-interface pattern, all cryptographic identity operations go in `pkg/aws/identity.go` with a new `IdentityAPI` interface.

```
pkg/aws/
├── ses.go           # existing — SES email
├── budget.go        # existing — DynamoDB budget
├── identity.go      # NEW — SSM signing key + DynamoDB km-identities
└── identity_test.go # NEW — mock SSMAPI + mock IdentityTableAPI
```

### New Terraform Module: `infra/modules/dynamodb-identities/v1.0.0/`

Follows the `dynamodb-budget` module exactly — same PAY_PER_REQUEST billing, same global table replica pattern, same TTL attribute. Provisioned alongside `km-budgets` in `infra/live/use1/`.

```
infra/modules/dynamodb-identities/v1.0.0/
├── main.tf        # aws_dynamodb_table "identities" — PK: sandbox_id (S), no SK needed
├── variables.tf   # table_name, replica_regions, tags
└── outputs.tf     # table_name, table_arn
infra/live/use1/
├── dynamodb-budget/     # existing
├── dynamodb-identities/ # NEW — same terragrunt.hcl pattern
└── ...
```

### New Profile Schema Fields: `spec.email`

```
pkg/profile/types.go   # EmailSpec struct added to Spec
schemas/               # JSON schema additions for spec.email.*
```

### Profile Schema Extension

```go
// EmailSpec controls email signing and encryption policy for the sandbox.
type EmailSpec struct {
    Signing       string `yaml:"signing"`       // "required" | "optional" | "off"
    VerifyInbound string `yaml:"verifyInbound"` // "required" | "optional" | "off"
    Encryption    string `yaml:"encryption"`    // "required" | "optional" | "off"
}

// In Spec:
Email *EmailSpec `yaml:"email,omitempty"`
```

### DynamoDB Key Design for `km-identities`

The table uses a simple single-attribute primary key (no sort key) — each sandbox has exactly one identity record:

```
PK = sandbox_id  (partition key, string) — e.g. "sb-a1b2c3d4"

Attributes:
  sandbox_id   (S) — same as PK for clarity
  public_key   (S) — base64-encoded Ed25519 public key (32 bytes → 44 chars)
  created_at   (S) — ISO 8601 timestamp
  email_address (S) — e.g. "sb-a1b2c3d4@sandboxes.domain.com"
  // Optional: encryption_public_key (S) — if separate X25519 key pair
  expiresAt    (N) — Unix timestamp for TTL (set at sandbox creation + retention)
```

**Rationale for no sort key:** Budget table uses PK+SK to store multiple row types per sandbox. Identity table has only one row per sandbox — no SK needed. Simpler access pattern.

### SSM Parameter Paths

```
Signing private key:    /sandbox/{sandbox-id}/signing-key
                        Type: SecureString, KMS key: per-sandbox KMS key alias
                        Value: base64-encoded 64-byte Ed25519 private key

Encryption private key: /sandbox/{sandbox-id}/encryption-key  (if encryption enabled)
                        Type: SecureString, same KMS key
                        Value: base64-encoded 32-byte X25519 private key
```

**Why use the existing per-sandbox KMS key:** The `secrets` Terraform module already provisions `alias/km-label-ssm-region-sandboxID` per sandbox. Writing the signing key to SSM using this key alias is consistent with how existing secrets are stored. No new KMS infrastructure needed.

### Email Headers for Signing

```
X-KM-Signature: <base64-encoded Ed25519 signature of email body>
X-KM-Sender-ID: <sandbox-id>
```

The SES v2 SDK `SendEmailInput` does not expose arbitrary headers directly on the `Simple` message type — headers must be passed via `RawMessage` (raw MIME). This is a critical detail: `Simple` message type does not support custom headers. The implementation must construct raw MIME email bytes manually or switch to `SendEmailInput.Content.Raw`.

### Recommended Project Structure (additions)
```
pkg/aws/
├── identity.go         # GenerateIdentity, PublishIdentity, FetchPublicKey,
│                       # SignEmailBody, VerifyEmailSignature, CleanupIdentity
└── identity_test.go    # table-driven unit tests with mock interfaces

infra/modules/
└── dynamodb-identities/v1.0.0/

infra/live/use1/
└── dynamodb-identities/
    └── terragrunt.hcl
```

---

## Key Technical Patterns

### Pattern 1: Key Generation and SSM Storage

```go
// Source: crypto/ed25519 stdlib
import (
    "crypto/ed25519"
    "crypto/rand"
    "encoding/base64"
    "context"

    awssdk "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/ssm"
    ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func GenerateSandboxSigningKey(ctx context.Context, client SSMAPI, sandboxID, kmsKeyID string) (ed25519.PublicKey, error) {
    pub, priv, err := ed25519.GenerateKey(rand.Reader)
    if err != nil {
        return nil, fmt.Errorf("generate ed25519 key: %w", err)
    }

    privB64 := base64.StdEncoding.EncodeToString(priv)
    paramPath := fmt.Sprintf("/sandbox/%s/signing-key", sandboxID)

    _, err = client.PutParameter(ctx, &ssm.PutParameterInput{
        Name:      awssdk.String(paramPath),
        Value:     awssdk.String(privB64),
        Type:      ssmtypes.ParameterTypeSecureString,
        KeyId:     awssdk.String(kmsKeyID),
        Overwrite: awssdk.Bool(false), // fail if already exists — idempotency guard
    })
    if err != nil {
        return nil, fmt.Errorf("store signing key in SSM for sandbox %s: %w", sandboxID, err)
    }
    return pub, nil
}
```

### Pattern 2: Publishing Public Key to DynamoDB

```go
// Source: DynamoDB attributevalue pattern from budget.go
func PublishIdentity(ctx context.Context, client IdentityTableAPI, tableName, sandboxID, emailAddress string, pubKey ed25519.PublicKey) error {
    pk := base64.StdEncoding.EncodeToString(pubKey)
    now := time.Now().UTC().Format(time.RFC3339)

    _, err := client.PutItem(ctx, &dynamodb.PutItemInput{
        TableName: awssdk.String(tableName),
        Item: map[string]dynamodbtypes.AttributeValue{
            "sandbox_id":    &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
            "public_key":    &dynamodbtypes.AttributeValueMemberS{Value: pk},
            "created_at":    &dynamodbtypes.AttributeValueMemberS{Value: now},
            "email_address": &dynamodbtypes.AttributeValueMemberS{Value: emailAddress},
        },
        ConditionExpression: awssdk.String("attribute_not_exists(sandbox_id)"), // idempotency
    })
    return err
}
```

### Pattern 3: Signing Email Body with Ed25519

```go
// Source: crypto/ed25519 stdlib
func SignEmailBody(privKeyB64, body string) (string, error) {
    privKeyBytes, err := base64.StdEncoding.DecodeString(privKeyB64)
    if err != nil {
        return "", fmt.Errorf("decode signing key: %w", err)
    }
    priv := ed25519.PrivateKey(privKeyBytes)
    sig := ed25519.Sign(priv, []byte(body))
    return base64.StdEncoding.EncodeToString(sig), nil
}
```

### Pattern 4: Verifying Inbound Signature

```go
// Source: crypto/ed25519 stdlib
func VerifyEmailSignature(pubKeyB64, body, sigB64 string) error {
    pubKeyBytes, _ := base64.StdEncoding.DecodeString(pubKeyB64)
    sigBytes, _ := base64.StdEncoding.DecodeString(sigB64)
    if !ed25519.Verify(ed25519.PublicKey(pubKeyBytes), []byte(body), sigBytes) {
        return fmt.Errorf("signature verification failed")
    }
    return nil
}
```

### Pattern 5: SES Custom Headers — MUST Use Raw MIME

**CRITICAL:** SES v2 `Simple` message type does NOT support custom headers. To attach `X-KM-Signature` and `X-KM-Sender-ID`, the email must be sent as raw MIME bytes using `Content.Raw`:

```go
// Construct raw MIME email manually
rawMIME := fmt.Sprintf(
    "From: %s\r\nTo: %s\r\nSubject: %s\r\nX-KM-Sender-ID: %s\r\nX-KM-Signature: %s\r\n\r\n%s",
    from, to, subject, sandboxID, sigB64, body,
)

_, err = sesClient.SendEmail(ctx, &sesv2.SendEmailInput{
    FromEmailAddress: awssdk.String(from),
    Destination: &sesv2types.Destination{ToAddresses: []string{to}},
    Content: &sesv2types.EmailContent{
        Raw: &sesv2types.RawMessage{
            Data: []byte(rawMIME),
        },
    },
})
```

### Pattern 6: NaCl Box Encryption (for `email.encryption`)

```go
// Source: golang.org/x/crypto/nacl/box (v0.49.0 — already transitive)
import (
    "crypto/rand"
    "golang.org/x/crypto/nacl/box"
)

// EncryptForRecipient encrypts message for recipient's X25519 public key.
// recipientPubKey32 is a [32]byte from DynamoDB (separate X25519 key).
func EncryptForRecipient(recipientPubKey32 *[32]byte, plaintext []byte) ([]byte, error) {
    // SealAnonymous: sender ephemeral key generated internally, no sender identity leak
    return box.SealAnonymous(nil, plaintext, recipientPubKey32, rand.Reader)
}
```

### Anti-Patterns to Avoid

- **Reusing Ed25519 keys for NaCl box:** Ed25519 and X25519 are different curve representations. Direct byte reuse (without proper conversion via `filippo.io/edwards25519`) produces cryptographically invalid keys. Use separate X25519 key pairs.
- **Putting private key in DynamoDB:** Private key MUST stay in SSM SecureString. Only public key goes in DynamoDB.
- **Using `Simple` message type for signed email:** SES v2 `Simple` does not expose custom headers. Must use `Raw` message.
- **Signing the entire MIME envelope:** Sign only the email body (plaintext content). Headers vary in transit (MX relay adds Received headers). Sign body only, same as DKIM body canonicalization model.
- **Fatal errors for identity operations in km create:** Follow the established non-fatal pattern (log.Warn + continue). Sandbox must provision even if identity key storage fails.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Ed25519 key generation | Custom PRNG-based key construction | `crypto/ed25519.GenerateKey(rand.Reader)` | Timing attacks in custom code; stdlib is constant-time |
| Ed25519 signing | Manual SHA-512 + scalar mult | `ed25519.Sign(priv, message)` | Subtle spec compliance required; stdlib tested against RFC 8032 |
| Signature verification | Manual curve math | `ed25519.Verify(pub, message, sig)` | Edge cases in point validation; stdlib handles all |
| Authenticated encryption | XOR + HMAC | `nacl/box.SealAnonymous` | Nonce management, MAC verification, key derivation all handled |
| KMS key ID lookup | Hardcoding key alias | Read from compiler output or config | Key alias is sandbox-scoped, formatted at compile time |
| Raw MIME construction | String concat for headers | Follow RFC 5322 header format with CRLF | Header injection via non-CRLF line endings is a security issue |

---

## Common Pitfalls

### Pitfall 1: SES `Simple` vs `Raw` Message Type for Custom Headers

**What goes wrong:** Developer uses `sesv2types.Message.Simple` (which is what ses.go currently uses for `SendLifecycleNotification`) — custom `X-KM-Signature` headers are silently dropped. Emails are sent without the signature header.

**Why it happens:** SES v2 SDK's `Simple` type abstracts the MIME format and only exposes Subject, Body.Text, Body.Html. No extension point for arbitrary headers.

**How to avoid:** Sign emails use `Content.Raw` with raw MIME bytes. The existing `SendLifecycleNotification` (unsigned) keeps using `Simple`. The new `SendSignedEmail` function always uses `Raw`.

**Warning signs:** Headers not appearing in received email when inspecting raw message source.

### Pitfall 2: Ed25519 Private Key Format — Seed vs Full Key

**What goes wrong:** Ed25519 private key in Go's stdlib is 64 bytes (32-byte seed + 32-byte public key). Storing and restoring only 32 bytes (the seed) loses the embedded public key and requires `ed25519.NewKeyFromSeed()` reconstruction. Confusion between seed and full key causes signature failures.

**How to avoid:** Store the full 64-byte private key (`[]byte(priv)`) as base64. Restore with `ed25519.PrivateKey(decoded)`. Document clearly: `len(privKey) == 64`.

### Pitfall 3: DynamoDB `PutItem` vs `UpdateItem` for Identity Records

**What goes wrong:** Using `UpdateItem` (budget pattern) for identity creation — budget uses `ADD` expressions for atomic increment of numeric spend. Identity uses `PutItem` with `attribute_not_exists` condition — identity is written once at creation, never incremented.

**How to avoid:** Use `PutItem` + `ConditionExpression: attribute_not_exists(sandbox_id)` for initial write. Use `PutItem` without condition (overwrite=true) for any rotation scenario.

### Pitfall 4: Inbound Verification with Lambda/TTL Teardown Race

**What goes wrong:** TTL handler destroys sandbox including `km-identities` row. A message in-flight is verified AFTER the row is deleted — verification fails with "key not found" which looks like a malicious unsigned message.

**How to avoid:** Add retention window — keep `km-identities` row alive for TTL + 24h grace period using DynamoDB TTL (`expiresAt` attribute). `km destroy` deletes the row immediately for hard destroys; TTL handler sets `expiresAt` to current + 24h instead of immediate delete.

### Pitfall 5: X25519 Key vs Ed25519 Key Confusion in DynamoDB

**What goes wrong:** DynamoDB `km-identities` table stores `public_key` (Ed25519 for signing) and optionally `encryption_public_key` (X25519 for encryption). Code fetches `public_key` and passes it to `nacl/box.Seal` — silently produces garbage ciphertext because Ed25519 and X25519 byte representations are different elliptic curves.

**How to avoid:** Store separate attributes with clearly different names (`public_key` for Ed25519 signing, `encryption_public_key` for X25519 encryption). Never mix them. Generate two independent key pairs.

### Pitfall 6: SSM `Overwrite: false` on km create retry

**What goes wrong:** `km create` fails after generating the key and storing in SSM but before DynamoDB publish. Operator retries `km create` — SSM PutParameter with `Overwrite: false` returns `ParameterAlreadyExists` error.

**How to avoid:** On retry path, detect `ParameterAlreadyExists` and fetch the existing key instead of failing. OR: Include `sandboxID` in the Terragrunt flow which is idempotent. The safer approach: generate key, store in SSM with `Overwrite: true` (last-writer-wins is acceptable for key rotation on retry), then publish to DynamoDB with `ConditionExpression`.

---

## Code Examples

### Ed25519 Key Generation — Full Round-Trip

```go
// Source: crypto/ed25519 stdlib documentation
package main

import (
    "crypto/ed25519"
    "crypto/rand"
    "encoding/base64"
    "fmt"
)

func main() {
    // Generate
    pub, priv, err := ed25519.GenerateKey(rand.Reader)
    // pub: 32 bytes (PublicKey)
    // priv: 64 bytes (PrivateKey = seed[0:32] + pub[0:32])

    // Store in SSM as base64
    privB64 := base64.StdEncoding.EncodeToString(priv)
    pubB64 := base64.StdEncoding.EncodeToString(pub)

    // Sign
    message := []byte("email body content")
    sig := ed25519.Sign(priv, message)
    sigB64 := base64.StdEncoding.EncodeToString(sig)

    // Verify (using only pubB64 from DynamoDB)
    pubBytes, _ := base64.StdEncoding.DecodeString(pubB64)
    sigBytes, _ := base64.StdEncoding.DecodeString(sigB64)
    ok := ed25519.Verify(ed25519.PublicKey(pubBytes), message, sigBytes)
    fmt.Println(ok) // true
}
```

### SSM PutParameter Pattern (from handlers_secrets.go)

```go
// Source: cmd/configui/handlers_secrets.go — established project pattern
_, err = ssmClient.PutParameter(ctx, &ssm.PutParameterInput{
    Name:      awssdk.String("/sandbox/sb-abc123/signing-key"),
    Value:     awssdk.String(privB64),
    Type:      ssmtypes.ParameterTypeSecureString,
    KeyId:     awssdk.String("alias/km-label-ssm-use1-sb-abc123"),
    Overwrite: awssdk.Bool(true),
})
```

### DynamoDB PutItem Pattern (adapted from budget.go)

```go
// Source: adapted from pkg/aws/budget.go SetBudgetLimits pattern
_, err = dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
    TableName: awssdk.String("km-identities"),
    Item: map[string]dynamodbtypes.AttributeValue{
        "sandbox_id":    &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
        "public_key":    &dynamodbtypes.AttributeValueMemberS{Value: pubKeyB64},
        "created_at":    &dynamodbtypes.AttributeValueMemberS{Value: now},
        "email_address": &dynamodbtypes.AttributeValueMemberS{Value: emailAddr},
    },
})
```

### km destroy cleanup pattern (from ses.go `CleanupSandboxEmail`)

```go
// Source: pkg/aws/ses.go CleanupSandboxEmail — swallow not-found, idempotent
func CleanupSandboxIdentity(ctx context.Context, ssmClient SSMAPI, dynClient IdentityTableAPI, tableName, sandboxID string) error {
    // 1. Delete SSM parameter
    _, err := ssmClient.DeleteParameter(ctx, &ssm.DeleteParameterInput{
        Name: awssdk.String("/sandbox/" + sandboxID + "/signing-key"),
    })
    if err != nil {
        var pnf *ssmtypes.ParameterNotFound
        if !errors.As(err, &pnf) {
            return fmt.Errorf("delete signing key for sandbox %s: %w", sandboxID, err)
        }
        // Not found — idempotent, continue
    }

    // 2. Delete DynamoDB identity row
    _, err = dynClient.DeleteItem(ctx, &dynamodb.DeleteItemInput{
        TableName: awssdk.String(tableName),
        Key: map[string]dynamodbtypes.AttributeValue{
            "sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
        },
    })
    return err // DeleteItem on non-existent item is a no-op in DynamoDB — no error
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| DKIM for email trust | Ed25519 custom headers | N/A (new feature) | Custom header approach is simpler for internal mesh; no DNS TXT records needed |
| Separate Secrets Manager for keys | SSM Parameter Store SecureString | Established in Phase 2 | SSM already integrated; per-sandbox KMS key already exists |
| Ed25519→X25519 key conversion | Separate X25519 key pair | Phase 14 decision | Avoids `filippo.io/edwards25519` dep; cleaner separation of concerns |

**Deprecated/outdated for this phase:**
- Do not use AWS KMS `Sign/Verify` API for email signing — expensive, adds latency, requires KMS API call per email, and KMS asymmetric keys don't support Ed25519 (only RSA and ECDSA). SSM + in-process `crypto/ed25519` is the right model.
- Do not use DKIM for this signing mechanism — DKIM requires DNS TXT record infrastructure and is designed for anti-spam, not inter-sandbox authentication.

---

## Integration Points in Existing Commands

### `km create` (internal/app/cmd/create.go)

New identity provisioning steps follow the existing non-fatal pattern established for budget (Step 12b). Add as Step 14 (or renumber):

1. Generate Ed25519 key pair (`crypto/ed25519.GenerateKey`)
2. Write private key to SSM at `/sandbox/{sandboxID}/signing-key` (SecureString, KMS key alias from compiler)
3. Write public key to DynamoDB `km-identities` table
4. If `email.encryption: optional|required`, generate X25519 key pair and write to SSM + DynamoDB

All identity steps: non-fatal. Sandbox is provisioned even if SSM/DynamoDB writes fail.

### `km destroy` (internal/app/cmd/destroy.go)

Add cleanup steps following `CleanupSandboxEmail` pattern:
- `CleanupSandboxIdentity` — deletes `/sandbox/{sandboxID}/signing-key` from SSM (idempotent: ignore ParameterNotFound)
- Delete `km-identities` DynamoDB row (idempotent: DynamoDB DeleteItem is no-op for missing key)

### `km status` (internal/app/cmd/status.go)

Add `IdentityFetcher` interface (parallel to `BudgetFetcher`). `FetchIdentity` reads from `km-identities` DynamoDB. `printSandboxStatus` adds new section:

```
Identity:
  Public Key:      <first 16 chars of base64>...
  Signing:         required
  Verify Inbound:  required
  Encryption:      optional
```

### `pkg/aws/ses.go`

Add `SendSignedEmail` function alongside existing `SendLifecycleNotification`. Uses `Content.Raw` (raw MIME) instead of `Content.Simple`. Reads private key from SSM, signs body, constructs raw MIME with custom headers.

### `pkg/profile/types.go`

Add `EmailSpec` struct and `Email *EmailSpec` field on `Spec`. The `Email` field is optional (pointer) — nil means all policies default to `off`. Schema validation: enum values `required|optional|off`.

### Built-in Profile Defaults

| Profile | `email.signing` | `email.verifyInbound` | `email.encryption` |
|---------|----------------|----------------------|-------------------|
| hardened | `required` | `required` | `off` |
| sealed | `required` | `required` | `off` |
| open-dev | `optional` | `optional` | `off` |
| restricted-dev | `optional` | `optional` | `off` |

---

## Infrastructure: New DynamoDB Module

### `infra/modules/dynamodb-identities/v1.0.0/main.tf`

Near-copy of `dynamodb-budget/v1.0.0/main.tf`. Key differences:
- Table name variable: `var.table_name` (default `km-identities`)
- Hash key: `sandbox_id` (string, no sort key) — unlike km-budgets which has PK+SK
- No DynamoDB Streams required (no Lambda trigger needed for identities)
- TTL attribute: `expiresAt` (same as budgets for automatic row cleanup after sandbox destroy)
- No global table replicas by default (public keys are rarely read cross-region; single-region is sufficient for v1)

### `infra/live/use1/dynamodb-identities/terragrunt.hcl`

Copy of `use1/dynamodb-budget/terragrunt.hcl` pattern, referencing new module, table_name = "km-identities".

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` package |
| Config file | none (no pytest.ini/jest.config) |
| Quick run command | `go test ./pkg/aws/... -run TestIdentity -v` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map

| Behavior | Test Type | Automated Command | File Exists? |
|----------|-----------|-------------------|-------------|
| GenerateSandboxSigningKey: key stored in SSM with correct path | unit | `go test ./pkg/aws/... -run TestIdentity_GenerateSigningKey` | Wave 0 |
| PublishIdentity: public key written to DynamoDB | unit | `go test ./pkg/aws/... -run TestIdentity_Publish` | Wave 0 |
| SignEmailBody: Ed25519 signature over body bytes | unit | `go test ./pkg/aws/... -run TestIdentity_SignEmailBody` | Wave 0 |
| VerifyEmailSignature: valid sig returns nil | unit | `go test ./pkg/aws/... -run TestIdentity_VerifySignature` | Wave 0 |
| VerifyEmailSignature: tampered body returns error | unit | `go test ./pkg/aws/... -run TestIdentity_VerifySignature_Tampered` | Wave 0 |
| CleanupSandboxIdentity: SSM delete idempotent (ParameterNotFound) | unit | `go test ./pkg/aws/... -run TestIdentity_Cleanup` | Wave 0 |
| SendSignedEmail: raw MIME contains X-KM-Signature header | unit | `go test ./pkg/aws/... -run TestIdentity_SendSigned` | Wave 0 |
| profile.EmailSpec: schema accepts required/optional/off | unit | `go test ./pkg/profile/... -run TestEmailSpec` | Wave 0 |
| km create: identity provisioned non-fatally | unit | `go test ./internal/app/cmd/... -run TestCreate_IdentityNonFatal` | Wave 0 |
| km status: identity section printed | unit | `go test ./internal/app/cmd/... -run TestStatus_IdentitySection` | Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/aws/... -run TestIdentity -v`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/aws/identity.go` — main implementation (GenerateSigningKey, PublishIdentity, FetchPublicKey, SignEmailBody, VerifyEmailSignature, CleanupSandboxIdentity, SendSignedEmail)
- [ ] `pkg/aws/identity_test.go` — unit tests with mock SSMAPI + mock IdentityTableAPI + mock SESV2API
- [ ] `infra/modules/dynamodb-identities/v1.0.0/main.tf` — DynamoDB table (no sort key, with TTL)
- [ ] `infra/modules/dynamodb-identities/v1.0.0/variables.tf`
- [ ] `infra/modules/dynamodb-identities/v1.0.0/outputs.tf`
- [ ] `infra/live/use1/dynamodb-identities/terragrunt.hcl`
- [ ] `pkg/profile/types.go` — EmailSpec struct added to Spec

---

## Open Questions

1. **KMS key alias at signing key write time**
   - What we know: KMS alias is `alias/km-label-ssm-region-sandboxID`. Alias is created by the `secrets` Terraform module during `km create` → `terragrunt apply`.
   - What's unclear: Is the KMS alias available before terragrunt apply completes? Signing key generation happens in Go code after terragrunt apply (Step 13+ pattern). The alias should exist by then.
   - Recommendation: Write signing key to SSM AFTER successful `runner.Apply(ctx, sandboxDir)` — same ordering as existing SSM writes in the create flow. If no KMS alias configured, fall back to default `aws/ssm` managed key.

2. **X-KM-Signature covers body only vs headers+body**
   - What we know: Signing only the body (not headers) is simpler and avoids relay header mutation issues (Received, DKIM-Signature headers added by MX servers).
   - What's unclear: For inbound verification, does SES Lambda receive the raw email before or after MX relay header injection?
   - Recommendation: Sign body only. Document this explicitly in code comments. SES receipt rules deliver raw message with all headers — verifier extracts body by splitting on `\r\n\r\n`.

3. **IdentityTableName configuration**
   - What we know: `BudgetTableName` defaults to `km-budgets` in config.go `SetDefault`. Pattern is clean.
   - Recommendation: Add `IdentityTableName` to `internal/app/config/config.go` with default `km-identities`. Same pattern as BudgetTableName.

4. **Encryption key pair storage in DynamoDB**
   - What we know: Phase description says `encryption_public_key` lives in `km-identities`. If separate X25519 key pair is used, we need an additional SSM path `/sandbox/{id}/encryption-key` and DynamoDB attribute `encryption_public_key`.
   - Recommendation: Add `encryption_public_key` as optional attribute on `km-identities` row. Only written when profile `email.encryption` is `optional` or `required`. Fetch during encryption: if attribute missing → encrypt or reject based on sender's `email.encryption` policy.

---

## Sources

### Primary (HIGH confidence)
- `crypto/ed25519` stdlib docs (`go doc crypto/ed25519`) — key generation, signing, verification functions verified locally
- `golang.org/x/crypto/nacl/box` v0.36.0 source at `/Users/khundeck/go/pkg/mod/golang.org/x/crypto@v0.36.0/nacl/box/box.go` — verified SealAnonymous, Open, GenerateKey signatures
- Project codebase: `pkg/aws/ses.go`, `pkg/aws/budget.go`, `cmd/configui/handlers_secrets.go`, `internal/app/cmd/create.go`, `infra/modules/dynamodb-budget/v1.0.0/main.tf`, `infra/modules/secrets/v1.0.0/main.tf` — all read directly for pattern extraction
- `go list -m all` in project directory — confirmed `golang.org/x/crypto v0.49.0` already in module graph

### Secondary (MEDIUM confidence)
- [ECDH encryption using ed25519 keys — hodo.dev](https://hodo.dev/posts/post-48-ecdh-over-ed25519/) — Ed25519→X25519 conversion algorithm (SHA-512 seed + BytesMontgomery); decision made to use separate X25519 key pairs instead to avoid external dep
- [AWS SSM PutParameter API docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_PutParameter.html) — SecureString type, KeyId parameter, Overwrite behavior
- [Go proposal: add montgomery/edwards key conversion](https://github.com/golang/go/issues/20504) — confirms `filippo.io/edwards25519` is the standard for conversion; also confirms it's NOT in stdlib

### Tertiary (LOW confidence — flag for validation)
- SES v2 Raw message for custom headers: verified from `SendEmailInput` struct SDK source and general MIME email knowledge. MUST be tested against real SES in staging — custom header stripping behavior varies by SES configuration.

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — stdlib ed25519 and x/crypto confirmed locally; AWS SDK patterns verified in codebase
- Architecture patterns: HIGH — DynamoDB, SSM, SES patterns directly extracted from existing code
- Pitfalls: MEDIUM-HIGH — SES Raw vs Simple header behavior needs staging validation; Ed25519 key format (64 vs 32 bytes) is stdlib-verified
- Ed25519→X25519 conversion: HIGH (reason to avoid) — confirmed `filippo.io/edwards25519` not in module graph; separate key pair approach is cleaner

**Research date:** 2026-03-22
**Valid until:** 2026-09-22 (stable APIs — ed25519 stdlib, AWS SDK v2, DynamoDB)
