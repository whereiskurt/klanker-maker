# Phase 77: Failed-Sandbox Discoverability — Research

**Researched:** 2026-05-10
**Domain:** DynamoDB schema extension, CloudWatch Logs FilterLogEvents, CLI rendering (status/logs)
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- Add `FailureReason string` (≤1024 chars, `omitempty`) and `FailedAt *time.Time` (`omitempty`) to `SandboxMetadata`.
- New helper `UpdateSandboxStatusAndReasonDynamo` — single `UpdateItem` updating `status`, `failure_reason`, `failed_at`. Mirrors `UpdateSandboxStatusDynamo`. Both stay supported. New helper used only on failure branch.
- Reason extraction from `out`: scan bottom-up for first `Error:` line (trim to 1024); no `Error:` line → last 1024 chars with `<no error line; tail of subprocess output> ` prefix.
- `UpdateSandboxStatusAndReasonDynamo` called with `failed`/`nocap` + reason + `time.Now().UTC()`. Same path covers both branches.
- `km status`: after `Status:` line, when status ∈ {`failed`, `nocap`}: print `Failure:` and `Failed At:` lines matching existing `Created At:` alignment. If `FailureReason` empty → `<unknown — try km logs <id>>`.
- `km status` formatting: match existing `Status:` / `Created At:` column alignment exactly. `Failed At` uses same renderer as `Created At`.
- `km list` unchanged.
- `km logs` fallback: on `ResourceNotFoundException` → `FilterLogEvents` on `/aws/lambda/{prefix}-create-handler` with `filterPattern: '{ $.sandbox_id = "<id>" }'`, 24h window. Prelude line. Chronological print of `message` field. Both empty → friendly hint. `--follow` in fallback → no-op with note, exit cleanly.
- Lambda log-group name: `/aws/lambda/<prefix>-create-handler` where `<prefix>` = `KM_RESOURCE_PREFIX` (default `km`).
- No infra churn. No new IAM. No `km init --sidecars`.
- Test strategy: four test files as specified in CONTEXT.md § Test strategy.

### Claude's Discretion

- Exact ordering of frontmatter / wave assignment for plans.
- Internal helper signatures inside `cmd/create-handler/main.go` for reason extraction (inline or `extractFailureReason(out string) string` helper).
- Time renderer choice for `Failed At` (must match existing `Created At` style — same one).
- Mock/stub strategy in tests (DDB client interface is already in `pkg/aws`).
- Whether Lambda-fallback CloudWatch client is a new helper in `pkg/aws` or inline in `internal/app/cmd/logs.go` (prefer `pkg/aws` for consistency).

### Deferred Ideas (OUT OF SCOPE)

- L2/L3 backfill of existing failed records.
- Lambda-log fallback for `ttl-handler`, `budget-enforcer`, `email-create-handler`.
- Failure-reason column in `km list` table.
- Slack-archived-channel auto-recovery.
</user_constraints>

---

## Summary

Phase 77 makes failed-sandbox failure reasons discoverable via two complementary changes: (1) persisting `failure_reason` + `failed_at` into DynamoDB at create-handler fail time, and (2) adding a Lambda-log fallback to `km logs` when the per-sandbox log group never existed.

The scope is intentionally surgical: three command edits plus one helper, touching nine files total. No infrastructure changes. The existing `SandboxMetadataAPI` interface in `pkg/aws/sandbox_dynamo.go` already covers everything the new helper needs — `UpdateItem` is already in the interface. The existing `CWLogsAPI` interface in `pkg/aws/cloudwatch.go` does NOT currently include `FilterLogEvents`, so that method must be added to the interface and a new helper written.

**Primary recommendation:** Add `FilterLogEvents` to `CWLogsAPI`, write `FilterCreateHandlerLogs` in `pkg/aws/cloudwatch.go`, and wire the fallback entirely through that helper in `internal/app/cmd/logs.go`. This keeps `logs.go` thin, follows the established pattern, and makes the mock surface reusable.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/dynamodb` | already in go.mod | DynamoDB UpdateItem for new helper | All DDB ops use this SDK |
| `github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs` | already in go.mod | FilterLogEvents for Lambda fallback | All CW ops use this SDK |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types` | already in go.mod | `ResourceNotFoundException` type assertion | Lambda fallback detection |

---

## Architecture Patterns

### Pattern 1: DynamoDB UpdateItem for new helper

The existing `UpdateSandboxStatusDynamo` (line 587 of `sandbox_dynamo.go`) is the exact model to mirror:

```go
// Source: pkg/aws/sandbox_dynamo.go:587
func UpdateSandboxStatusDynamo(...) error {
    _, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
        TableName: ...,
        Key: map[string]dynamodbtypes.AttributeValue{
            "sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
        },
        UpdateExpression: awssdk.String("SET #s = :status"),
        ExpressionAttributeNames: map[string]string{"#s": "status"},
        ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
            ":status": &dynamodbtypes.AttributeValueMemberS{Value: status},
        },
    })
    ...
}
```

The new helper extends the expression to set three attributes:

```go
// Proposed UpdateSandboxStatusAndReasonDynamo
UpdateExpression: "SET #s = :status, failure_reason = :reason, failed_at = :ts"
ExpressionAttributeNames: map[string]string{"#s": "status"}
ExpressionAttributeValues: {
    ":status": S(status),
    ":reason": S(reason),              // ≤1024 chars already trimmed by caller
    ":ts":     S(failedAt.UTC().Format(time.RFC3339)),  // matches created_at convention
}
```

Note: `failed_at` is stored as RFC3339 String (same as `created_at`, `locked_at`, `expires_at` — the project uses String for all timestamps except the native TTL epoch attribute).

### Pattern 2: SandboxMetadata field additions

Current `SandboxMetadata` in `pkg/aws/metadata.go` uses `json` tags and `omitempty`. The two new fields follow the same pattern:

```go
// Add after SlackInboundQueueURL (line 49 of metadata.go)
// Phase 77 — failure discoverability.
FailureReason string     `json:"failure_reason,omitempty"`
FailedAt      *time.Time `json:"failed_at,omitempty"`
```

The `sandboxItemDynamo` internal struct in `sandbox_dynamo.go` does NOT need these fields — the new helper bypasses full marshal/unmarshal with a targeted `UpdateItem`. However, `unmarshalSandboxItem` / `toSandboxMetadata` / `unmarshalSlackFields` pattern shows that a new `unmarshalFailureFields` function is the right way to populate `FailureReason` and `FailedAt` when reading back (used by `ReadSandboxMetadataDynamo` and `ListAllSandboxesByDynamo`).

Alternatively: add `failure_reason` and `failed_at` to `sandboxItemDynamo` (similar to `ExpiresAt string`), update `unmarshalSandboxItem`, `toSandboxMetadata`, and `marshalSandboxItem`. This is more work but keeps the marshalling logic consolidated. Either approach is at planner discretion — the standalone `unmarshalFailureFields` pattern mirrors `unmarshalSlackFields` and is less invasive.

### Pattern 3: reason extraction in create-handler/main.go

The failure branch (lines 240-274 of `cmd/create-handler/main.go`) currently:

1. Classifies `failStatus` (`failed` vs `nocap`) by scanning `outStr` for capacity-error strings.
2. Calls `awspkg.UpdateSandboxStatusDynamo(ctx, h.DynamoClient, h.TableName, event.SandboxID, failStatus)`.

The new path:

1. Same `failStatus` classification (unchanged).
2. Extract reason from `outStr` using bottom-up scan:
   - Lines scan: `strings.Split` on `\n`, iterate in reverse, find first with `strings.HasPrefix(line, "Error:")`.
   - Trim to 1024 chars if longer.
   - No `Error:` line: take `outStr`, trim to 1024, prefix with `<no error line; tail of subprocess output> `.
3. Replace call to `UpdateSandboxStatusDynamo` with `UpdateSandboxStatusAndReasonDynamo(ctx, h.DynamoClient, h.TableName, event.SandboxID, failStatus, reason, time.Now().UTC())`.

The planner should decide whether to extract a `extractFailureReason(out string) string` helper (testable in isolation) or inline. The standalone helper is strongly preferred for clean test coverage of the extraction logic.

### Pattern 4: km status rendering

The `printSandboxStatus` function in `internal/app/cmd/status.go` (line 347) currently renders:

```go
fmt.Fprintf(out, "Status:      %s\n", statusDisplay)
fmt.Fprintf(out, "Created At:  %s\n", rec.CreatedAt.Local().Format("2006-01-02 3:04:05 PM MST"))
```

Column alignment uses 13-char labels with two trailing spaces: `"Status:      "`, `"Created At:  "`. The `Failed At:   ` label (10 chars + colon + spaces) should align to the same column. Looking at the pattern:

- `"Status:      "` — 13 chars before value (6 + 7 spaces)
- `"Created At:  "` — 13 chars before value (10 + 3 spaces... wait: "Created At:" = 11 chars, "  " = 2 → 13 total)
- `"Failed At:   "` — "Failed At:" = 10 chars, "   " = 3 → 13 total — correct match

Proposed insertion after the `Status:` `Fprintf` (before the `Created At:` line or after, since `Failed At` is logically paired with status):

```go
// After the Status: line:
if rec.Status == "failed" || rec.Status == "nocap" {
    if rec.FailureReason != "" {
        fmt.Fprintf(out, "Failure:     %s\n", rec.FailureReason)
        if rec.FailedAt != nil {
            fmt.Fprintf(out, "Failed At:   %s\n", rec.FailedAt.Local().Format("2006-01-02 3:04:05 PM MST"))
        }
    } else {
        fmt.Fprintf(out, "Failure:     <unknown — try km logs %s>\n", rec.SandboxID)
    }
}
```

Timestamp format `"2006-01-02 3:04:05 PM MST"` is copied verbatim from line 363 of `status.go` (the `Created At:` renderer).

### Pattern 5: km logs Lambda fallback

Current `runLogs` in `internal/app/cmd/logs.go` (line 49):

```go
err = kmaws.TailLogs(ctx, cwClient, logGroup, stream, follow, cmd.OutOrStdout())
if err != nil && !errors.Is(err, context.Canceled) {
    return fmt.Errorf("tail logs for sandbox %s: %w", sandboxID, err)
}
```

`TailLogs` calls `GetLogEvents`, which wraps errors. A `ResourceNotFoundException` from `GetLogEvents` propagates as a wrapped error. The fallback must unwrap and type-assert it:

```go
var notFound *cloudwatchlogstypes.ResourceNotFoundException
if errors.As(err, &notFound) {
    // fall back to Lambda log group
}
```

The fallback uses `FilterLogEvents` (a different CloudWatch Logs API call than `GetLogEvents`). This method is NOT currently in `CWLogsAPI`. The planner must add it.

**Adding FilterLogEvents to CWLogsAPI:**

```go
// In pkg/aws/cloudwatch.go CWLogsAPI interface — add:
FilterLogEvents(ctx context.Context, params *cloudwatchlogs.FilterLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error)
```

**New helper `FilterCreateHandlerLogs` in `pkg/aws/cloudwatch.go`:**

```go
func FilterCreateHandlerLogs(ctx context.Context, client CWLogsAPI, prefix, sandboxID string) ([]LogEvent, error) {
    logGroup := "/aws/lambda/" + prefix + "-create-handler"
    now := time.Now().UTC()
    startTime := now.Add(-24 * time.Hour)
    filterPattern := `{ $.sandbox_id = "` + sandboxID + `" }`

    out, err := client.FilterLogEvents(ctx, &cloudwatchlogs.FilterLogEventsInput{
        LogGroupName:  aws.String(logGroup),
        FilterPattern: aws.String(filterPattern),
        StartTime:     aws.Int64(startTime.UnixMilli()),
        EndTime:       aws.Int64(now.UnixMilli()),
    })
    if err != nil {
        return nil, err
    }
    result := make([]LogEvent, 0, len(out.Events))
    for _, ev := range out.Events {
        var ts int64
        if ev.Timestamp != nil { ts = *ev.Timestamp }
        var msg string
        if ev.Message != nil { msg = *ev.Message }
        result = append(result, LogEvent{Timestamp: ts, Message: msg})
    }
    return result, nil
}
```

**Updated `runLogs` fallback logic:**

```go
err = kmaws.TailLogs(ctx, cwClient, logGroup, stream, follow, cmd.OutOrStdout())
if err != nil && !errors.Is(err, context.Canceled) {
    var notFound *cloudwatchlogstypes.ResourceNotFoundException
    if !errors.As(err, &notFound) {
        return fmt.Errorf("tail logs for sandbox %s: %w", sandboxID, err)
    }
    // Fallback: per-sandbox group absent, try Lambda create-handler group
    return runLogsLambdaFallback(cmd, cfg, cwClient, sandboxID, follow)
}
```

```go
func runLogsLambdaFallback(cmd *cobra.Command, cfg *config.Config, cwClient kmaws.CWLogsAPI, sandboxID string, follow bool) error {
    out := cmd.OutOrStdout()
    fmt.Fprintf(out, "── per-sandbox log group not found; falling back to create-handler Lambda ──\n")

    if follow {
        fmt.Fprintf(out, "--follow is not supported in fallback mode (failure is terminal); use km status %s for the persisted reason.\n", sandboxID)
        return nil
    }

    ctx := cmd.Context()
    if ctx == nil { ctx = context.Background() }

    events, err := kmaws.FilterCreateHandlerLogs(ctx, cwClient, cfg.GetResourcePrefix(), sandboxID)
    if err != nil {
        return fmt.Errorf("filter create-handler logs for %s: %w", sandboxID, err)
    }

    if len(events) == 0 {
        fmt.Fprintf(out, "No create-handler activity found for %s in the last 24h. Try km status %s for the persisted failure reason.\n", sandboxID, sandboxID)
        return nil
    }

    for _, ev := range events {
        ts := time.UnixMilli(ev.Timestamp).UTC().Format(time.RFC3339)
        fmt.Fprintf(out, "%s %s\n", ts, ev.Message)
    }
    return nil
}
```

### Pattern 6: SandboxRecord propagation

`printSandboxStatus` receives `*kmaws.SandboxRecord`, not `*SandboxMetadata`. The `FailureReason` and `FailedAt` fields must be added to `SandboxRecord` AND populated in `metadataToRecord` AND `unmarshalSandboxItem`/`toSandboxMetadata`.

Fields to add to `SandboxRecord` in `pkg/aws/sandbox.go`:

```go
// Phase 77 — failure discoverability.
FailureReason string     `json:"failure_reason,omitempty"`
FailedAt      *time.Time `json:"failed_at,omitempty"`
```

Update `metadataToRecord` in `sandbox_dynamo.go` to copy the fields.

### Anti-Patterns to Avoid

- **Using `PutItem` (full replace) for the failure write:** The create-handler failure branch happens after the initial `WriteSandboxMetadataDynamo` (PutItem) that set up the record. A second PutItem would silently drop Slack fields, locked state, etc. Always use `UpdateItem` with targeted attribute expressions on the failure path.
- **Adding `failure_reason` to the TTL/stop record-preservation path:** The failure write is a targeted `UpdateItem` and cannot accidentally affect TTL behavior. Do not add these fields to `WriteSandboxMetadataDynamo` (which uses PutItem).
- **Not aliasing `failure_reason` in UpdateExpression:** `failure_reason` is not a reserved word in DynamoDB, so no `ExpressionAttributeNames` alias is required (unlike `status` which uses `#s`). Verify before submitting.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| CloudWatch Logs filter query | Custom log parser | `FilterLogEvents` SDK call | Pagination, time-range, filter patterns handled by SDK |
| DynamoDB attribute-level update | Read-modify-write via GetItem + PutItem | `UpdateItem` with expression | Avoids race conditions, no field clobbering |
| Time formatting | Custom formatter | `"2006-01-02 3:04:05 PM MST"` (same as line 363 status.go) | Matches existing UI exactly |

---

## Common Pitfalls

### Pitfall 1: CWLogsAPI interface missing FilterLogEvents

**What goes wrong:** `FilterLogEvents` is a method on `*cloudwatchlogs.Client` but is NOT in the current `CWLogsAPI` interface (confirmed by reading `pkg/aws/cloudwatch.go` lines 21-29). Adding the fallback in `logs.go` that calls `kmaws.FilterCreateHandlerLogs` will fail to compile if the interface and mock are not updated.

**How to avoid:** Add `FilterLogEvents` to `CWLogsAPI`. Add a stub implementation to `mockCWLogsAPI` in `cloudwatch_test.go` (and in `logs_test.go`'s local mock if it uses one). This is a NEW method that must be added — not merely wired up.

**Warning signs:** Compile error `mockCWLogsAPI does not implement CWLogsAPI` after adding the interface method.

### Pitfall 2: SandboxRecord does not carry FailureReason/FailedAt by default

**What goes wrong:** `printSandboxStatus` works on `*SandboxRecord`, not `*SandboxMetadata`. If `FailureReason`/`FailedAt` are added only to `SandboxMetadata` without propagating to `SandboxRecord`, `km status` will always show the `<unknown>` hint even for newly-failed sandboxes.

**How to avoid:** Add fields to BOTH `SandboxMetadata` AND `SandboxRecord`. Update `metadataToRecord` to copy them. Update `unmarshalSandboxItem`/`toSandboxMetadata` (or `unmarshalFailureFields`) to read them from DynamoDB on the read path.

### Pitfall 3: sandboxItemDynamo internal struct missing failure fields

**What goes wrong:** `ReadSandboxMetadataDynamo` goes through `unmarshalSandboxItem` → `toSandboxMetadata` → `unmarshalSlackFields`. If `failure_reason` and `failed_at` are only written by `UpdateItem` but never read back by the unmarshal path, `km status` will show `<unknown>` for sandboxes that DO have a failure reason persisted.

**How to avoid:** Either add `FailureReason string` and `FailedAt string` to `sandboxItemDynamo` and handle them in `unmarshalSandboxItem`/`toSandboxMetadata`, OR add an `unmarshalFailureFields` function (mirroring `unmarshalSlackFields`) and call it in `ReadSandboxMetadataDynamo` and `ListAllSandboxesByDynamo`. The `unmarshalSlackFields` pattern (lines 237-258) is the established convention for post-conversion field injection.

### Pitfall 4: DynamoDB reserved words in UpdateExpression

**What goes wrong:** `status` is a reserved word (requires `#s` alias). `failure_reason` and `failed_at` are NOT DynamoDB reserved words (verified), so they can be used directly in the expression. Mixing up which names need aliases causes `ValidationException`.

**How to avoid:** Keep `#s` alias for `status` in the new helper. Use `failure_reason` and `failed_at` literal in the expression. See the existing `UpdateSandboxStatusDynamo` for the alias pattern.

### Pitfall 5: TailLogs ResourceNotFoundException wrapping

**What goes wrong:** `TailLogs` calls `GetLogEvents`, which returns `fmt.Errorf("get log events from %q/%q: %w", logGroup, logStream, err)`. The `ResourceNotFoundException` is wrapped. `errors.As` unwraps correctly, but `errors.Is` would not. The fallback detection MUST use `errors.As`.

**How to avoid:** Use `errors.As(err, &notFound)` for the ResourceNotFoundException type assertion in `runLogs`. See the existing pattern at `pkg/aws/cloudwatch.go:74-77` (DeleteSandboxLogGroup uses the same pattern).

### Pitfall 6: failed_at RFC3339 vs pointer nil on older records

**What goes wrong:** `ReadSandboxMetadataDynamo` reads back `failed_at` as a string and must parse it to `*time.Time`. If the field is absent (older records), the pointer remains nil. `printSandboxStatus` must check `rec.FailedAt != nil` before formatting.

**How to avoid:** Use `*time.Time` (pointer) for `FailedAt` in both `SandboxMetadata` and `SandboxRecord`. The parsing pattern is identical to `LockedAt` (lines 106-113 of `sandbox_dynamo.go`).

---

## Code Examples

### Existing UpdateSandboxStatusDynamo (the model to mirror)

```go
// Source: pkg/aws/sandbox_dynamo.go:587
func UpdateSandboxStatusDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID, status string) error {
    _, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
        TableName: awssdk.String(tableName),
        Key: map[string]dynamodbtypes.AttributeValue{
            "sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
        },
        UpdateExpression: awssdk.String("SET #s = :status"),
        ExpressionAttributeNames: map[string]string{
            "#s": "status",
        },
        ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
            ":status": &dynamodbtypes.AttributeValueMemberS{Value: status},
        },
    })
    if err != nil {
        return fmt.Errorf("update status for sandbox %s to %q: %w", sandboxID, status, err)
    }
    return nil
}
```

### Existing LockedAt unmarshal pattern (the model for FailedAt parsing)

```go
// Source: pkg/aws/sandbox_dynamo.go:106-113
if d.LockedAt != "" {
    lockedAt, err := time.Parse(time.RFC3339, d.LockedAt)
    if err == nil {
        meta.LockedAt = &lockedAt
    }
}
```

### Existing unmarshalSlackFields pattern (the model for unmarshalFailureFields)

```go
// Source: pkg/aws/sandbox_dynamo.go:237
func unmarshalSlackFields(item map[string]dynamodbtypes.AttributeValue, meta *SandboxMetadata) {
    if v, ok := item["slack_channel_id"]; ok {
        if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
            meta.SlackChannelID = sv.Value
        }
    }
    // ...
}
```

### Existing ResourceNotFoundException pattern (the model for logs.go fallback detection)

```go
// Source: pkg/aws/cloudwatch.go:74-77
var notFound *types.ResourceNotFoundException
if errors.As(err, &notFound) {
    return nil
}
```

### Existing Created At timestamp format

```go
// Source: internal/app/cmd/status.go:363
fmt.Fprintf(out, "Created At:  %s\n", rec.CreatedAt.Local().Format("2006-01-02 3:04:05 PM MST"))
```

### Existing nocap classification (create-handler/main.go:244-253)

```go
// Source: cmd/create-handler/main.go:244
failStatus := "failed"
outStr := string(out)
if strings.Contains(outStr, "InsufficientInstanceCapacity") ||
    strings.Contains(outStr, "MaxSpotInstanceCountExceeded") ||
    strings.Contains(outStr, "SpotMaxPriceTooLow") ||
    strings.Contains(outStr, "no Spot capacity") {
    failStatus = "nocap"
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|---|---|---|---|
| `km status` stops at `Status: failed` | `km status` shows `Failure:` + `Failed At:` | Phase 77 | Operator gets actionable info without AWS console |
| `km logs` returns raw ResourceNotFoundException | `km logs` falls back to Lambda log group | Phase 77 | `km logs` always returns something useful for failed sandboxes |
| `failure_reason` absent from DDB | `failure_reason` + `failed_at` persisted at fail time | Phase 77 | Survives Lambda log group retention expiry |

---

## Open Questions

1. **FilterLogEvents pagination**
   - What we know: `FilterLogEventsOutput` has `NextToken` for pagination; for a 24h window on a single sandbox_id, results are typically small.
   - What's unclear: Should the helper paginate or cap at one page?
   - Recommendation: For a 24h window filtered by `sandbox_id`, one page (default 10,000 events) is sufficient. Add pagination if needed later. Document the limitation.

2. **DynamoDB reserved word check for `failure_reason`**
   - What we know: `status` IS reserved (uses `#s`); `locked` is NOT reserved (used literally). `failure_reason` and `failed_at` are compound names with underscores — not in the published DynamoDB reserved words list.
   - Recommendation: Use them literally in the expression. If the first test run returns `ValidationException: Invalid expression`, add aliases. LOW risk.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go standard `testing` package |
| Config file | none (no separate test config) |
| Quick run command | `go test ./cmd/create-handler/... ./pkg/aws/... ./internal/app/cmd/... -run TestFailure -count=1` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map

| Behavior | Test Type | Test File | Test Function Pattern | Mock Surface |
|----------|-----------|-----------|----------------------|-------------|
| DDB write: `UpdateSandboxStatusAndReasonDynamo` called on failure branch with correct status/reason/timestamp | unit | `cmd/create-handler/main_test.go` | `TestCreateHandler_FailurePath_WritesFailureReason` | `mockSandboxMetadataAPI` (via `h.DynamoClient`) |
| DDB write: `nocap` status also gets failure reason | unit | `cmd/create-handler/main_test.go` | `TestCreateHandler_NocapPath_WritesFailureReason` | `mockSandboxMetadataAPI` |
| Reason extraction: last `Error:` line wins, trimmed to 1024 | unit | `cmd/create-handler/main_test.go` | `TestExtractFailureReason_LastErrorLine` | none (pure function) |
| Reason extraction: no `Error:` line → tail prefix | unit | `cmd/create-handler/main_test.go` | `TestExtractFailureReason_NoErrorLine_TailFallback` | none (pure function) |
| DDB round-trip: write `UpdateSandboxStatusAndReasonDynamo` → read back → fields populated | unit | `pkg/aws/sandbox_dynamo_test.go` | `TestUpdateSandboxStatusAndReasonDynamo_RoundTrip` | `mockSandboxMetadataAPI` (UpdateItem stores, GetItem returns pre-built item) |
| `km status` failed with reason → `Failure:` line printed | unit | `internal/app/cmd/status_test.go` | `TestStatusCmd_FailedWithReason` | `fakeFetcher` (returns `SandboxRecord{Status:"failed", FailureReason:"..."}`) |
| `km status` failed without reason → `<unknown>` hint | unit | `internal/app/cmd/status_test.go` | `TestStatusCmd_FailedNoReason` | `fakeFetcher` (returns `SandboxRecord{Status:"failed", FailureReason:""}`) |
| `km status` running → no `Failure:` line | unit | `internal/app/cmd/status_test.go` | `TestStatusCmd_Running_NoFailureLine` | `fakeFetcher` (returns `SandboxRecord{Status:"running"}`) |
| `km logs` per-sandbox group present → unchanged behavior | unit | `internal/app/cmd/logs_test.go` | `TestLogsCmd_PerSandboxGroupPresent` | mock `CWLogsAPI` returning events |
| `km logs` per-sandbox group missing + Lambda has events → fallback with prelude | unit | `internal/app/cmd/logs_test.go` | `TestLogsCmd_FallbackWithEvents` | mock `CWLogsAPI`: `GetLogEvents` returns `ResourceNotFoundException`, `FilterLogEvents` returns events |
| `km logs` both empty → friendly hint | unit | `internal/app/cmd/logs_test.go` | `TestLogsCmd_FallbackBothEmpty` | mock `CWLogsAPI`: `GetLogEvents` returns `ResourceNotFoundException`, `FilterLogEvents` returns empty |
| `km logs --follow` in fallback → exits cleanly with note | unit | `internal/app/cmd/logs_test.go` | `TestLogsCmd_FallbackFollow_NoOp` | mock `CWLogsAPI`: `GetLogEvents` returns `ResourceNotFoundException` |

### Observable Assertions

**DDB write edge:**
- `mockSandboxMetadataAPI.updateItemInput` is non-nil after handler invocation.
- `updateItemInput.UpdateExpression` contains `failure_reason` and `failed_at`.
- The `:reason` expression attribute value equals the expected extracted reason string.
- The `:ts` expression attribute value is a non-empty RFC3339 string.

**Reason extraction edge:**
- Given input with multiple lines including `"Error: something bad"` at a non-last position and a second `"Error: actual root cause"` near the bottom, the extracted reason == `"Error: actual root cause"` (last Error: line wins — scan from bottom, take first match).
- Given input with no `Error:` line, extracted reason has `<no error line; tail of subprocess output> ` prefix.
- Given reason > 1024 chars, `len(extracted) <= 1024`.

**`km status` rendering edge:**
- `out` contains `"Failure:"` when status is `failed` or `nocap`.
- `out` contains the literal reason string from `SandboxRecord.FailureReason`.
- `out` contains `"Failed At:"` when `FailedAt` is non-nil.
- `out` does NOT contain `"Failure:"` when status is `running`.
- `out` contains `"<unknown"` when `FailureReason` is empty and status is `failed`.

**`km logs` fallback edge:**
- `out` contains `"per-sandbox log group not found; falling back"` prelude when `GetLogEvents` returns `ResourceNotFoundException`.
- `out` contains the event message text when `FilterLogEvents` returns events.
- `out` contains `"No create-handler activity found"` when `FilterLogEvents` returns empty.
- `out` contains `"--follow is not supported"` note and exits nil when `follow=true` and per-sandbox group missing.

### Sampling Rate
- **Per task commit:** `go test ./pkg/aws/... ./internal/app/cmd/... ./cmd/create-handler/... -count=1`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `cmd/create-handler/main_test.go` — `TestCreateHandler_FailurePath_WritesFailureReason`, `TestCreateHandler_NocapPath_WritesFailureReason`, `TestExtractFailureReason_*` — requires `mockSandboxMetadataAPI` added to test file (currently only `mockS3GetAPI` and `mockSESAPI` exist in `main_test.go`; the existing `mockSandboxMetadataAPI` lives in `pkg/aws/sandbox_dynamo_test.go` and is package-private)
- [ ] `internal/app/cmd/logs_test.go` — currently only has a trivial string-construction test; needs a real mock `CWLogsAPI` (including `FilterLogEvents`) and injected client path in `runLogs`
- [ ] `pkg/aws/cloudwatch.go` `CWLogsAPI` interface — add `FilterLogEvents` method before any test can mock it
- [ ] `internal/app/cmd/logs.go` — `runLogs` currently constructs `cloudwatchlogs.NewFromConfig` directly; needs DI seam (e.g. `NewLogsCmdWithClient` pattern mirroring `NewStatusCmdWithFetcher`) for testability

---

## Sources

### Primary (HIGH confidence)
- `pkg/aws/sandbox_dynamo.go` (read directly) — `UpdateSandboxStatusDynamo` shape, `sandboxItemDynamo`, `unmarshalSlackFields` pattern, `SandboxMetadataAPI` interface
- `pkg/aws/metadata.go` (read directly) — `SandboxMetadata` current fields, Phase 67 Slack additions
- `pkg/aws/sandbox.go` (read directly) — `SandboxRecord` current fields
- `pkg/aws/cloudwatch.go` (read directly) — `CWLogsAPI` interface (confirmed `FilterLogEvents` absent), `TailLogs`, `GetLogEvents`, `ResourceNotFoundException` pattern
- `internal/app/cmd/logs.go` (read directly) — current `runLogs` impl, `TailLogs` call site, `cfg.GetResourcePrefix()` usage
- `internal/app/cmd/status.go:347-382` (read directly) — `printSandboxStatus`, timestamp format `"2006-01-02 3:04:05 PM MST"`, column alignment
- `cmd/create-handler/main.go:240-274` (read directly) — current failure branch, `failStatus` classification, `UpdateSandboxStatusDynamo` call site, `DynamoAPI`/`CreateHandler` fields
- `pkg/aws/sandbox_dynamo_test.go` (read directly) — `mockSandboxMetadataAPI` shape, test patterns
- `pkg/aws/cloudwatch_test.go` (read directly) — `mockCWLogsAPI` shape (confirmed `FilterLogEvents` stub absent)
- `cmd/create-handler/main_test.go` (read directly) — existing test patterns, `mockS3GetAPI`, `mockSESAPI`; no DDB mock present
- `internal/app/cmd/status_test.go` (read directly) — `fakeFetcher`, `runStatusCmd`, existing status test patterns
- `internal/app/cmd/logs_test.go` (read directly) — only trivial string test; no AWS client mock present
- `infra/modules/create-handler/v1.0.0/main.tf` (read directly) — confirmed Lambda function name `${var.resource_prefix}-create-handler`

### Secondary (MEDIUM confidence)
- DynamoDB reserved words list (training knowledge, cross-checked with project's existing use of `locked`, `status`, `alias` without aliasing issues) — `failure_reason` and `failed_at` are not reserved

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries already in go.mod; confirmed by reading source
- Architecture: HIGH — all patterns read directly from source files; no ambiguity
- Pitfalls: HIGH — each pitfall identified from direct code inspection, not guesswork
- Test mock surfaces: HIGH — both `mockSandboxMetadataAPI` and `mockCWLogsAPI` read directly; gaps confirmed

**Research date:** 2026-05-10
**Valid until:** 2026-06-10 (stable codebase; no fast-moving external dependencies)
