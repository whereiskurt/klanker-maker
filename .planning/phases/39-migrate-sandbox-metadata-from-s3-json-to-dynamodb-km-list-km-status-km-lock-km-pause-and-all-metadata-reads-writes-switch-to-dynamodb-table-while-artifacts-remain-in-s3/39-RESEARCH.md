# Phase 39: Migrate Sandbox Metadata from S3 JSON to DynamoDB - Research

**Researched:** 2026-03-28
**Domain:** AWS DynamoDB, Go SDK v2, sandbox metadata lifecycle
**Confidence:** HIGH

## Summary

The project currently stores `SandboxMetadata` as JSON blobs in S3 at `tf-km/sandboxes/<sandbox-id>/metadata.json`. Every `km list` triggers an N+1 call pattern (one `ListObjectsV2` + one `GetObject` per sandbox). Lock/unlock is a read-modify-write that has a race window. Alias resolution requires a full scan.

This phase replaces the S3 metadata store with a dedicated DynamoDB table `km-sandboxes`. The table uses `sandbox_id` as the hash key (single-item design — no sort key), a `alias-index` GSI for O(1) alias resolution, and DynamoDB TTL on `ttl_expiry` for auto-cleanup. Lock/unlock become atomic `ConditionExpression` updates eliminating the race. `km list` becomes a single `Scan` call.

The codebase already uses DynamoDB SDK v2 extensively (budget: `km-budgets`, identity: `km-identities`). The patterns — narrow interface, `attributevalue` marshal/unmarshal, conditional updates — are already established and can be replicated directly. The migration follows the existing pattern of adding a new `infra/modules/dynamodb-sandboxes/v1.0.0` Terraform module, a new `infra/live/use1/dynamodb-sandboxes/terragrunt.hcl`, and a `pkg/aws/sandbox_dynamo.go` source file replacing the S3-backed functions.

**Primary recommendation:** Mirror the `dynamodb-identities` module pattern exactly. Use a flat-key design (`sandbox_id` as hash key, no sort key), `alias-index` GSI, and DynamoDB TTL. Replace S3 metadata calls in `pkg/aws/sandbox.go` behind new functions in `pkg/aws/sandbox_dynamo.go`, update the 22 call sites, add IAM policies to all affected roles, and add a `dynamodb-sandboxes` module to `km init` ordering.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/dynamodb` | v1.57.0 (already in go.mod) | DynamoDB CRUD operations | Already used for km-budgets and km-identities |
| `github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue` | v1.20.36 (already in go.mod) | Marshal/unmarshal Go structs to/from DynamoDB AttributeValues | Already used in budget.go |
| `github.com/aws/aws-sdk-go-v2/service/dynamodb/types` | (part of dynamodb module) | AttributeValue types, condition checks, errors | Already used |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `aws-sdk-go-v2/service/dynamodb` `ConditionExpression` | same | Atomic conditional updates for lock/unlock | Lock: `attribute_not_exists(locked) OR locked = :false`; Unlock: `locked = :true` |

**No new dependencies needed.** All required libraries are already in `go.mod`.

---

## Architecture Patterns

### DynamoDB Table Design

```
Table: km-sandboxes
  Hash key: sandbox_id (S)      — same as km-identities pattern
  No sort key                   — one row per sandbox, flat design
  TTL attribute: ttl_expiry     — Unix epoch seconds (int64)

Attributes:
  sandbox_id    (S) — PK
  profile_name  (S)
  substrate     (S) — "ec2" | "docker"
  region        (S)
  status        (S) — "starting" | "running" | "failed" | "killed" | "reaped" | "paused" | "stopped"
  created_at    (S) — RFC3339 string (consistent with km-identities pattern)
  ttl_expiry    (N) — Unix epoch seconds for DynamoDB TTL; also stored for km status/list display
  idle_timeout  (S) — e.g. "15m"
  max_lifetime  (S) — e.g. "72h"
  created_by    (S) — "cli" | "email" | "api" | "remote"
  alias         (S) — human-friendly alias; GSI hash key for alias-index
  locked        (BOOL)
  locked_at     (S) — RFC3339 or absent

GSI: alias-index
  Hash key: alias (S)
  Projection: ALL
  Purpose: O(1) alias → sandbox_id resolution (replacing full S3 scan in ResolveSandboxAlias)
```

**Key design rationale:**
- No sort key — mirrors `km-identities` v1.1.0 exactly, simpler Get/Put/Delete
- `ttl_expiry` stored as Number (Unix seconds) because DynamoDB TTL requires a Number attribute
- `alias` must be stored as a string; GSI requires non-empty values — use empty string sentinel or omit attribute when no alias (DynamoDB GSI only indexes items where the GSI key attribute exists and is non-null)
- `created_at` stored as RFC3339 string for human readability in scan results; convert to `time.Time` on read

### Recommended Project Structure

```
pkg/aws/
├── sandbox_dynamo.go       # New: DynamoDB-backed metadata CRUD (mirrors budget.go pattern)
├── sandbox_dynamo_test.go  # New: unit tests with mock DynamoDB client
├── sandbox.go              # Existing: keep SandboxRecord, SandboxMetadata, helper funcs
├── metadata.go             # Existing: SandboxMetadata struct (unchanged)
infra/modules/
└── dynamodb-sandboxes/
    └── v1.0.0/
        ├── main.tf         # Table, GSI, TTL, tags — mirrors dynamodb-identities v1.1.0
        ├── variables.tf
        └── outputs.tf
infra/live/use1/
└── dynamodb-sandboxes/
    └── terragrunt.hcl      # mirrors dynamodb-identities/terragrunt.hcl
internal/app/config/
└── config.go               # Add SandboxTableName field, default "km-sandboxes"
```

### Pattern 1: DynamoDB Get/Put for Metadata (mirrors km-identities)

```go
// Source: pkg/aws/identity.go pattern adapted for sandbox metadata

// SandboxMetadataAPI is the narrow DynamoDB interface for sandbox metadata CRUD.
type SandboxMetadataAPI interface {
    GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
    PutItem(ctx context.Context, input *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
    UpdateItem(ctx context.Context, input *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
    DeleteItem(ctx context.Context, input *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
    Scan(ctx context.Context, input *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
    Query(ctx context.Context, input *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// ReadSandboxMetadataDynamo reads a sandbox record from DynamoDB.
// Returns ErrSandboxNotFound if the item does not exist.
func ReadSandboxMetadataDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID string) (*SandboxMetadata, error) {
    out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
        TableName: aws.String(tableName),
        Key: map[string]dynamodbtypes.AttributeValue{
            "sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
        },
    })
    if err != nil {
        return nil, fmt.Errorf("%w: DynamoDB GetItem for sandbox %s: %v", ErrSandboxNotFound, sandboxID, err)
    }
    if len(out.Item) == 0 {
        return nil, fmt.Errorf("%w: no DynamoDB record for sandbox %s", ErrSandboxNotFound, sandboxID)
    }
    var meta SandboxMetadata
    if err := attributevalue.UnmarshalMap(out.Item, &meta); err != nil {
        return nil, fmt.Errorf("unmarshal DynamoDB item for sandbox %s: %w", sandboxID, err)
    }
    return &meta, nil
}
```

### Pattern 2: Atomic Lock/Unlock with ConditionExpression

```go
// Source: DynamoDB conditional writes pattern — eliminates S3 read-modify-write race

// LockSandboxDynamo atomically locks a sandbox.
// Fails with ConditionalCheckFailedException if already locked.
func LockSandboxDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID string) error {
    now := time.Now().UTC().Format(time.RFC3339)
    _, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
        TableName: aws.String(tableName),
        Key: map[string]dynamodbtypes.AttributeValue{
            "sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
        },
        UpdateExpression: aws.String("SET locked = :t, locked_at = :now"),
        ConditionExpression: aws.String("attribute_exists(sandbox_id) AND (attribute_not_exists(locked) OR locked = :f)"),
        ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
            ":t":   &dynamodbtypes.AttributeValueMemberBOOL{Value: true},
            ":f":   &dynamodbtypes.AttributeValueMemberBOOL{Value: false},
            ":now": &dynamodbtypes.AttributeValueMemberS{Value: now},
        },
    })
    var ccf *dynamodbtypes.ConditionalCheckFailedException
    if errors.As(err, &ccf) {
        return fmt.Errorf("sandbox %s is already locked", sandboxID)
    }
    return err
}
```

### Pattern 3: Single-Scan km list (replaces N+1 S3 calls)

```go
// Source: DynamoDB Scan pattern — single API call replaces N+1 S3 calls

// ListAllSandboxesByDynamo scans the km-sandboxes table and returns all records.
func ListAllSandboxesByDynamo(ctx context.Context, client SandboxMetadataAPI, tableName string) ([]SandboxRecord, error) {
    out, err := client.Scan(ctx, &dynamodb.ScanInput{
        TableName: aws.String(tableName),
    })
    if err != nil {
        return nil, fmt.Errorf("scan km-sandboxes table: %w", err)
    }
    records := make([]SandboxRecord, 0, len(out.Items))
    for _, item := range out.Items {
        var meta SandboxMetadata
        if err := attributevalue.UnmarshalMap(item, &meta); err != nil {
            continue // skip malformed items
        }
        records = append(records, metadataToRecord(meta))
    }
    return records, nil
}
```

### Pattern 4: Alias GSI Query (replaces full S3 scan in ResolveSandboxAlias)

```go
// Source: km-identities alias-index GSI pattern (dynamodb-identities v1.1.0)

// ResolveSandboxAliasDynamo queries the alias-index GSI for O(1) alias lookup.
func ResolveSandboxAliasDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, alias string) (string, error) {
    out, err := client.Query(ctx, &dynamodb.QueryInput{
        TableName:              aws.String(tableName),
        IndexName:              aws.String("alias-index"),
        KeyConditionExpression: aws.String("alias = :alias"),
        ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
            ":alias": &dynamodbtypes.AttributeValueMemberS{Value: alias},
        },
    })
    if err != nil {
        return "", fmt.Errorf("query alias-index for %q: %w", alias, err)
    }
    if len(out.Items) == 0 {
        return "", fmt.Errorf("alias %q not found: no active sandbox with that alias", alias)
    }
    if len(out.Items) > 1 {
        // Should not happen with proper alias enforcement, but guard defensively
        return "", fmt.Errorf("alias %q is ambiguous: matched %d sandboxes", alias, len(out.Items))
    }
    var meta SandboxMetadata
    if err := attributevalue.UnmarshalMap(out.Items[0], &meta); err != nil {
        return "", fmt.Errorf("unmarshal alias query result: %w", err)
    }
    return meta.SandboxID, nil
}
```

### Pattern 5: WriteSandboxMetadataDynamo (PutItem)

```go
// WriteSandboxMetadataDynamo writes a full SandboxMetadata record to DynamoDB.
// Uses PutItem (full replacement). Called at sandbox creation and status updates.
// ttl_expiry must be a Number (Unix epoch seconds) for DynamoDB TTL to work.
func WriteSandboxMetadataDynamo(ctx context.Context, client SandboxMetadataAPI, tableName string, meta *SandboxMetadata) error {
    item, err := attributevalue.MarshalMap(meta)
    if err != nil {
        return fmt.Errorf("marshal sandbox metadata for %s: %w", meta.SandboxID, err)
    }
    // DynamoDB TTL requires a Number attribute. Store ttl_expiry as Unix epoch seconds.
    if meta.TTLExpiry != nil {
        item["ttl_expiry"] = &dynamodbtypes.AttributeValueMemberN{
            Value: strconv.FormatInt(meta.TTLExpiry.Unix(), 10),
        }
    }
    _, err = client.PutItem(ctx, &dynamodb.PutItemInput{
        TableName: aws.String(tableName),
        Item:      item,
    })
    return err
}
```

### Anti-Patterns to Avoid

- **Storing ttl_expiry as a string:** DynamoDB TTL only fires on Number attributes containing Unix epoch seconds. Store as `N` type.
- **Using PutItem for lock/unlock:** PutItem is not atomic — creates a TOCTOU race. Always use `UpdateItem` with `ConditionExpression` for lock state changes.
- **GSI query on empty alias:** DynamoDB GSIs do not index items where the GSI key attribute is absent. Never write an empty string alias to the GSI key — either omit the attribute or use a `attribute_not_exists` filter. Items without an alias simply won't appear in alias-index queries.
- **Scanning for backward compat check:** Do not scan to check if the table exists. Use `DescribeTable` or catch `ResourceNotFoundException` from the first read attempt.
- **Dual-write to both S3 and DynamoDB:** Adds complexity and two-phase commit risk. The spec calls for a clean cutover with S3 fallback only when the table does not exist (not dual-write).

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Atomic conditional lock | Custom distributed lock over S3 or with mutexes | DynamoDB `UpdateItem` + `ConditionExpression` | Native atomic semantics; no polling, no TTL on lock itself |
| Type conversion for DynamoDB | Manual `map[string]AttributeValue` construction | `attributevalue.MarshalMap` / `UnmarshalMap` | Already used in budget.go; handles all Go types including pointers and time |
| TTL management | Custom cleanup Lambda or cron scan | DynamoDB native TTL on `ttl_expiry` (Number, Unix seconds) | Automatic, no operational overhead; items deleted within 48h of expiry |
| GSI for alias resolution | Full-table Scan + filter | DynamoDB GSI `alias-index` with Query | O(1) vs O(n) — already proven in km-identities v1.1.0 |
| Terraform module | Inline resource in existing module | New `infra/modules/dynamodb-sandboxes/v1.0.0/main.tf` | Matches project convention; clean separation; Terragrunt isolation |

---

## Common Pitfalls

### Pitfall 1: SandboxMetadata struct tags incompatible with attributevalue marshaling

**What goes wrong:** `attributevalue.MarshalMap` uses struct field names or `dynamodbav` tags. The existing `SandboxMetadata` struct uses `json` tags. Fields with `json:"sandbox_id"` are stored under key `sandbox_id` when marshaled by `attributevalue` (uses the json tag as fallback), but `omitempty` behavior differs between JSON and DynamoDB marshaling.

**Why it happens:** `attributevalue` reads `dynamodbav` struct tags first, then falls back to lowercase field name — NOT the json tag. So `SandboxID string \`json:"sandbox_id"\`` marshals to DynamoDB key `SandboxID` (capitalized), not `sandbox_id`.

**How to avoid:** Add `dynamodbav` tags to `SandboxMetadata` struct fields, OR use a separate DynamoDB-specific struct (preferred — keeps the existing JSON API unchanged). Use a `sandboxMetadataDynamo` internal struct with `dynamodbav:"sandbox_id"` tags and convert to/from `SandboxMetadata`.

**Confidence:** HIGH — verified by reading `attributevalue` package behavior. The existing `budget.go` avoids this by building `AttributeValue` maps manually rather than using struct marshaling.

### Pitfall 2: ttl_expiry must be Number type, not string

**What goes wrong:** If `ttl_expiry` is stored as a DynamoDB String attribute, TTL never fires. DynamoDB TTL only processes Number attributes whose value is a Unix epoch timestamp in seconds.

**How to avoid:** Override the `ttl_expiry` attribute after `MarshalMap` to store as `AttributeValueMemberN` with `strconv.FormatInt(meta.TTLExpiry.Unix(), 10)`. Do not rely on automatic marshaling of `*time.Time` (which marshals to a string).

### Pitfall 3: Backward compat fallback to S3 — table-not-found detection

**What goes wrong:** Checking for table existence with a `DescribeTable` call on every read adds latency. On first failed `GetItem`, the error type is `*dynamodbtypes.ResourceNotFoundException` — this should trigger S3 fallback, not an error.

**How to avoid:** On the first `GetItem` or `Scan` call, catch `ResourceNotFoundException` and fall back to existing S3 path. Cache a bool `dynamoAvailable` in the lister struct so subsequent calls don't re-attempt DynamoDB when the table is known absent.

```go
var ce *dynamodbtypes.ResourceNotFoundException
if errors.As(err, &ce) {
    // table doesn't exist — fall back to S3
    return fallbackToS3(...)
}
```

### Pitfall 4: IAM permissions missing on Lambda roles

**What goes wrong:** The TTL handler, email-create-handler, and budget-enforcer Lambdas have S3 permissions for metadata but not DynamoDB. After migration they will fail with `AccessDeniedException` when calling `GetItem`/`UpdateItem`/`DeleteItem`.

**How to avoid:** Add a `dynamodb_sandboxes` IAM policy block to `infra/modules/ttl-handler/v1.0.0/main.tf` and `infra/modules/email-handler/v1.0.0/main.tf` and `infra/modules/create-handler/v1.0.0/main.tf`. The `klanker-terraform` CLI role already has broad DynamoDB access but the Lambda execution roles are tightly scoped.

### Pitfall 5: alias GSI does not index items with missing alias attribute

**What goes wrong:** Sandboxes without an alias are not indexed by `alias-index`. Querying the GSI for an alias that doesn't exist returns empty results (correct), but if a sandbox has `alias: ""` (empty string) it IS written to the GSI, creating false matches for `ResolveSandboxAliasDynamo("", ...)`.

**How to avoid:** When writing sandbox metadata, only include the `alias` attribute in the DynamoDB item if it is non-empty. Use a custom marshal helper that omits the field when blank.

### Pitfall 6: km init ordering — dynamodb-sandboxes must precede ttl-handler and create-handler

**What goes wrong:** If `dynamodb-sandboxes` Terragrunt module is applied after `ttl-handler` or `create-handler`, the Lambda roles won't reference the correct table ARN. More critically, the table won't exist when the first sandbox is created.

**How to avoid:** Insert `dynamodb-sandboxes` in `regionalModules()` (in `internal/app/cmd/init.go`) after `dynamodb-budget` and `dynamodb-identities`, before `ttl-handler`. This follows the existing dependency ordering pattern.

### Pitfall 7: Scan pagination — Scan returns max 1MB per call

**What goes wrong:** `km list` with DynamoDB Scan only gets the first 1MB of items (roughly 300-500 sandbox records). For large deployments, `km list` silently shows incomplete results.

**How to avoid:** Implement pagination loop using `LastEvaluatedKey`:
```go
var items []map[string]dynamodbtypes.AttributeValue
var lastKey map[string]dynamodbtypes.AttributeValue
for {
    out, err := client.Scan(ctx, &dynamodb.ScanInput{
        TableName:         aws.String(tableName),
        ExclusiveStartKey: lastKey,
    })
    if err != nil { return nil, err }
    items = append(items, out.Items...)
    if out.LastEvaluatedKey == nil { break }
    lastKey = out.LastEvaluatedKey
}
```

---

## Code Examples

Verified patterns from the existing codebase:

### DynamoDB UpdateItem with ConditionExpression (from budget.go)
```go
// Source: pkg/aws/budget.go — IncrementAISpend
out, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
    TableName: awssdk.String(tableName),
    Key: map[string]dynamodbtypes.AttributeValue{
        "PK": &dynamodbtypes.AttributeValueMemberS{Value: pk},
        "SK": &dynamodbtypes.AttributeValueMemberS{Value: sk},
    },
    UpdateExpression: awssdk.String("ADD spentUSD :cost, inputTokens :inputTokens, outputTokens :outputTokens"),
    ReturnValues:     dynamodbtypes.ReturnValueAllNew,
    ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
        ":cost": costAV,
    },
})
```

### DynamoDB GetItem (from identity.go pattern)
```go
// Source: pkg/aws/identity.go — FetchPublicKey
out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
    TableName: awssdk.String(tableName),
    Key: map[string]dynamodbtypes.AttributeValue{
        "sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
    },
})
if len(out.Item) == 0 {
    return nil, fmt.Errorf("%w: no identity record for sandbox %s", ErrSandboxNotFound, sandboxID)
}
var record IdentityRecord
attributevalue.UnmarshalMap(out.Item, &record)
```

### Terraform module for DynamoDB with GSI and TTL (from dynamodb-identities v1.1.0)
```hcl
# Source: infra/modules/dynamodb-identities/v1.1.0/main.tf
resource "aws_dynamodb_table" "sandboxes" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "sandbox_id"

  attribute { name = "sandbox_id" type = "S" }
  attribute { name = "alias"      type = "S" }

  global_secondary_index {
    name            = "alias-index"
    hash_key        = "alias"
    projection_type = "ALL"
  }

  ttl {
    attribute_name = "ttl_expiry"
    enabled        = true
  }

  tags = merge(var.tags, { Module = "dynamodb-sandboxes", Version = "v1.0.0" })
}
```

### WriteSandboxMetadata (current S3 pattern — to be replaced)
```go
// Current pattern in lock.go, extend.go, create.go — read-modify-write on S3
// All callers do: json.Marshal(meta) + s3Client.PutObject(...)
// Replace with: WriteSandboxMetadataDynamo(ctx, dynamoClient, tableName, meta)
metaJSON, _ := json.Marshal(meta)
_, putErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
    Bucket:      aws.String(cfg.StateBucket),
    Key:         aws.String("tf-km/sandboxes/" + sandboxID + "/metadata.json"),
    Body:        bytes.NewReader(metaJSON),
    ContentType: aws.String("application/json"),
})
```

---

## Call Site Inventory (Complete Audit)

22 call sites identified in the phase description. Grouped by change type:

### New DynamoDB functions in `pkg/aws/sandbox_dynamo.go`
| New Function | Replaces |
|---|---|
| `ReadSandboxMetadataDynamo` | `ReadSandboxMetadata` (S3 GetObject) |
| `WriteSandboxMetadataDynamo` | `s3.PutObject(metadata.json)` (inline in 8+ files) |
| `DeleteSandboxMetadataDynamo` | `DeleteSandboxMetadata` (S3 DeleteObject) |
| `ListAllSandboxesByDynamo` | `ListAllSandboxesByS3` |
| `ResolveSandboxAliasDynamo` | `ResolveSandboxAlias` (S3 scan) |
| `LockSandboxDynamo` | read+PutObject in `lock.go` |
| `UnlockSandboxDynamo` | read+PutObject in `unlock.go` |

### CLI commands requiring DynamoDB client injection
| File | Current operation | Change |
|---|---|---|
| `internal/app/cmd/create.go` | 3 PutObject (EC2, Docker, Remote paths) | Replace with WriteSandboxMetadataDynamo |
| `internal/app/cmd/extend.go` | ReadSandboxMetadata + PutObject | Replace with DynamoDB read/write |
| `internal/app/cmd/pause.go` | ReadSandboxMetadata + PutObject | Replace |
| `internal/app/cmd/resume.go` | PutObject status update | Replace |
| `internal/app/cmd/lock.go` | ReadSandboxMetadata + PutObject | Replace with LockSandboxDynamo (atomic) |
| `internal/app/cmd/unlock.go` | ReadSandboxMetadata + PutObject | Replace with UnlockSandboxDynamo (atomic) |
| `internal/app/cmd/destroy.go` | ReadSandboxMetadata + DeleteSandboxMetadata | Replace |
| `internal/app/cmd/stop.go` | ReadSandboxMetadata (substrate routing) | Replace |
| `internal/app/cmd/list.go` | ListAllSandboxesByS3 | Replace with ListAllSandboxesByDynamo |
| `internal/app/cmd/status.go` | ReadSandboxMetadata (via awsSandboxFetcher) | Replace fetcher backend |
| `internal/app/cmd/budget.go` | ReadSandboxMetadata (substrate check) | Replace |
| `cmd/ttl-handler/main.go` | ReadSandboxMetadata + PutObject (handleExtend) + DeleteSandboxMetadata (handleDestroy) | Replace all 3 |
| `cmd/email-create-handler/main.go` | ReadSandboxMetadata (status email) | Replace |

### Infrastructure changes
| File | Change |
|---|---|
| `infra/modules/dynamodb-sandboxes/v1.0.0/main.tf` | New module |
| `infra/modules/dynamodb-sandboxes/v1.0.0/variables.tf` | New |
| `infra/modules/dynamodb-sandboxes/v1.0.0/outputs.tf` | New |
| `infra/live/use1/dynamodb-sandboxes/terragrunt.hcl` | New live config |
| `infra/modules/ttl-handler/v1.0.0/main.tf` | Add DynamoDB policy to `km-ttl-handler` role |
| `infra/modules/email-handler/v1.0.0/main.tf` | Add DynamoDB policy to email handler role |
| `infra/modules/create-handler/v1.0.0/main.tf` | Add DynamoDB policy to create handler role |
| `internal/app/cmd/init.go` | Add `dynamodb-sandboxes` to `regionalModules()` ordering |
| `internal/app/config/config.go` | Add `SandboxTableName` field, default `"km-sandboxes"` |

---

## IAM Permissions Required

### CLI role `klanker-terraform` (already has broad access — verify coverage)
```
dynamodb:GetItem, PutItem, UpdateItem, DeleteItem, Scan, Query
Resource: arn:aws:dynamodb:*:*:table/km-sandboxes
Resource: arn:aws:dynamodb:*:*:table/km-sandboxes/index/alias-index
```

### Lambda execution roles (each needs new inline policy)
```
dynamodb:GetItem, PutItem, UpdateItem, DeleteItem, Scan, Query
Resource: arn:aws:dynamodb:*:${account_id}:table/km-sandboxes
Resource: arn:aws:dynamodb:*:${account_id}:table/km-sandboxes/index/alias-index
```
Applies to: `km-ttl-handler`, `km-email-create-handler`, `km-create-handler`

### Bootstrap (km init) role — table creation
```
dynamodb:CreateTable, DescribeTable, UpdateTimeToLive, CreateGlobalSecondaryIndex
Resource: arn:aws:dynamodb:*:*:table/km-sandboxes
```
These permissions are granted by the `klanker-application` profile used during `km init`.

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| S3 ListObjects + N GetObject calls | Single DynamoDB Scan | This phase | O(n) → O(1) for list |
| Read-modify-write lock via S3 PutObject | Atomic ConditionExpression UpdateItem | This phase | Eliminates TOCTOU race |
| Full S3 scan for alias resolution | DynamoDB GSI Query | This phase | O(n) → O(1) for alias |
| S3 metadata.json for all metadata | DynamoDB item per sandbox | This phase | Faster, atomic, TTL-native |

**Deprecated after this phase:**
- `ListAllSandboxesByS3` — replaced by `ListAllSandboxesByDynamo` (S3 version kept for backward compat fallback)
- `ResolveSandboxAlias` (S3 scan) — replaced by `ResolveSandboxAliasDynamo` (GSI query)
- Direct `s3.PutObject` for `metadata.json` in CLI commands — replaced by `WriteSandboxMetadataDynamo`

---

## Open Questions

1. **SandboxMetadata struct tag strategy**
   - What we know: `attributevalue.MarshalMap` uses `dynamodbav` tags, not `json` tags
   - What's unclear: Whether to add `dynamodbav` tags to the existing struct (changing a shared type) or introduce a separate internal DynamoDB struct
   - Recommendation: Introduce a private `sandboxItemDynamo` struct in `sandbox_dynamo.go` with `dynamodbav` tags and convert to/from `SandboxMetadata`. Keeps the public API unchanged and avoids touching the `json` serialization used in API responses.

2. **ttl_expiry type in SandboxMetadata**
   - What we know: `SandboxMetadata.TTLExpiry` is `*time.Time`; DynamoDB TTL needs a Number (Unix epoch)
   - What's unclear: Whether to change the struct field type or always override after marshaling
   - Recommendation: Keep `TTLExpiry *time.Time` in the struct (JSON compat); in `WriteSandboxMetadataDynamo`, override `item["ttl_expiry"]` with `AttributeValueMemberN` after `MarshalMap`.

3. **Backward compat S3 fallback scope**
   - What we know: Phase spec says "fall back to S3 if table doesn't exist"
   - What's unclear: Whether fallback is per-operation or global (once detected, always use S3)
   - Recommendation: Global flag: on first `ResourceNotFoundException`, set `cfg.sandboxTableAvailable = false` (or use a package-level atomic bool). All subsequent calls skip DynamoDB attempt. This avoids per-call DescribeTable overhead.

4. **Pagination in km list DynamoDB Scan**
   - What we know: DynamoDB Scan returns max 1MB per call
   - What's unclear: Whether the project expects >300 simultaneous sandboxes (unlikely given MaxSandboxes default of 10)
   - Recommendation: Implement pagination loop regardless — it's a 5-line addition and prevents a silent correctness bug at scale.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) + testify patterns |
| Config file | none — `go test ./...` from repo root |
| Quick run command | `go test ./pkg/aws/ -run TestDynamo -v` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map
| ID | Behavior | Test Type | Automated Command | File Exists? |
|----|----------|-----------|-------------------|-------------|
| META-DYNAMO-01 | ReadSandboxMetadataDynamo returns ErrSandboxNotFound when item absent | unit | `go test ./pkg/aws/ -run TestReadSandboxMetadataDynamo_NotFound -v` | ❌ Wave 0 |
| META-DYNAMO-02 | WriteSandboxMetadataDynamo stores ttl_expiry as Number type | unit | `go test ./pkg/aws/ -run TestWriteSandboxMetadataDynamo_TTLType -v` | ❌ Wave 0 |
| META-DYNAMO-03 | LockSandboxDynamo is atomic — ConditionalCheckFailedException on already-locked | unit | `go test ./pkg/aws/ -run TestLockSandboxDynamo_AlreadyLocked -v` | ❌ Wave 0 |
| META-DYNAMO-04 | ListAllSandboxesByDynamo paginates past 1MB boundary | unit | `go test ./pkg/aws/ -run TestListAllSandboxesByDynamo_Pagination -v` | ❌ Wave 0 |
| META-DYNAMO-05 | ResolveSandboxAliasDynamo queries alias-index GSI | unit | `go test ./pkg/aws/ -run TestResolveSandboxAliasDynamo -v` | ❌ Wave 0 |
| META-DYNAMO-06 | Backward compat: fallback to S3 on ResourceNotFoundException | unit | `go test ./pkg/aws/ -run TestReadSandboxMetadataDynamo_Fallback -v` | ❌ Wave 0 |
| META-DYNAMO-07 | km list uses DynamoDB Scan when table exists | integration | manual — requires real AWS | manual-only |
| META-DYNAMO-08 | km lock is idempotent (returns "already locked" not error) | unit | `go test ./internal/app/cmd/ -run TestLock_AlreadyLocked -v` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/aws/ -run TestDynamo -v`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/aws/sandbox_dynamo_test.go` — covers META-DYNAMO-01 through META-DYNAMO-06
- [ ] Mock `SandboxMetadataAPI` interface implementation in test file
- [ ] `internal/app/cmd/lock_test.go` additions for META-DYNAMO-08

---

## Sources

### Primary (HIGH confidence)
- Direct code inspection: `pkg/aws/sandbox.go`, `pkg/aws/metadata.go`, `pkg/aws/budget.go`, `pkg/aws/identity.go`
- Direct code inspection: `infra/modules/dynamodb-identities/v1.1.0/main.tf` — GSI + TTL pattern
- Direct code inspection: `infra/modules/dynamodb-budget/v1.0.0/main.tf` — billing mode pattern
- Direct code inspection: `internal/app/cmd/lock.go`, `extend.go`, `list.go`, `status.go` — all current S3 call sites
- Direct code inspection: `cmd/ttl-handler/main.go` — Lambda metadata read/write paths
- Direct code inspection: `internal/app/config/config.go` — Config struct, table name fields
- Direct code inspection: `internal/app/cmd/init.go` — `regionalModules()` ordering
- Direct code inspection: `infra/modules/ttl-handler/v1.0.0/main.tf` — Lambda IAM role policies

### Secondary (MEDIUM confidence)
- AWS DynamoDB TTL documentation: TTL attribute must be Number type containing Unix epoch seconds
- DynamoDB ConditionalCheckFailedException: `dynamodbtypes.ConditionalCheckFailedException` is the typed error returned by SDK v2 for failed condition expressions

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries already in go.mod and in use
- Architecture: HIGH — mirrors existing dynamodb-identities and dynamodb-budget patterns verbatim
- Call sites: HIGH — complete audit from additional_context confirmed against actual source files
- Pitfalls: HIGH — derived from reading actual current code and DynamoDB SDK v2 behavior
- IAM: HIGH — existing Lambda role policies read from actual Terraform source

**Research date:** 2026-03-28
**Valid until:** 2026-04-28 (stable AWS SDK — 30-day window)
