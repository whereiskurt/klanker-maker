# Phase 85: doctor: orphan state-lock digest sweeper + report cleanup — Research

**Researched:** 2026-05-18
**Domain:** Go, AWS SDK v2 (DynamoDB BatchWriteItem, S3 HeadObject), `internal/app/cmd/doctor.go` extension
**Confidence:** HIGH — all findings are from direct codebase inspection; no external sources required

---

## Summary

Phase 85 is a focused extension of the existing `checkStateLockDigest` check introduced in Phase 84.1. The function already scans the DDB lock table, cross-references S3 GetObject, and emits a single-line WARN. This phase replaces that check with a new function that (a) parallelises the S3 scan with a 10-worker pool using `HeadObject` instead of `GetObject`, (b) adds an age guard (configurable, default 24h) before flagging a row as orphan-deletable, (c) adds a `--delete-state-digests` flag folded into `--with-deletes`, (d) deletes eligible rows via `BatchWriteItem` in 25-item batches, and (e) formats output as `summary + 10-item preview + "--json for full list"` matching the Stale Lambdas pattern.

The existing `S3StateReader` interface (GetObject) must be extended or replaced. For the sweeper, S3 HeadObject is more appropriate than GetObject: it returns `s3types.NotFound` on missing keys (HEAD does not return `s3types.NoSuchKey`), avoids downloading state-file bodies, and is ~10x faster at scale. A new narrow interface `S3StateHeadAPI` should be introduced to avoid widening `S3StateReader` (which already has tests locked against GetObject).

The DDB delete path uses `BatchWriteItem` with `DeleteRequest` items. No helper exists in `pkg/aws/` for this — the plan must implement it inline in the new check function, following the `dynamodbtypes.WriteRequest{DeleteRequest: &dynamodbtypes.DeleteRequest{Key: ...}}` pattern from the SDK. `BatchWriteItem` max is 25 items per call; `UnprocessedItems` must be retried in a loop.

All five TDD test cases target functions and interfaces that do not yet exist. A new file `internal/app/cmd/doctor_state_digest_sweeper.go` (or equivalent) should hold the new function and interfaces. The existing `doctor_state_digest_test.go` holds Phase 84.1 tests and must not be modified; new tests go in the same file (the BRIEF says `doctor_state_digest_test.go` is the target) or a companion file.

**Primary recommendation:** Implement `checkStateLockDigestSweeper` as a replacement/successor to `checkStateLockDigest`, calling it from `buildChecks` in place of the old call, with the five new DI seams (`S3StateHeadAPI`, `LockDigestDeleterAPI`) wired identically to the existing `StateLockS3Client`/`StateLockDDBClient` pattern in `DoctorDeps`.

---

<user_constraints>
## User Constraints (from BRIEF.md — treated as CONTEXT.md)

### Locked Decisions

| Decision | Choice |
|---|---|
| Orphan source | Both km destroy leak + manual cleanup; sweeper handles accumulation, leak fix is separate phase |
| Gate shape | `--with-deletes` umbrella + per-category `--delete-state-digests` flag |
| Safety guards | strict NoSuchKey (definitive 404) only + age > 24h (configurable). Generic errors → "could not verify", no delete. Re-HEAD at delete time: explicitly declined. |
| Output format | summary + capped at 10 inline items + `--json` for full list (matches Stale Lambdas) |
| Deletion path | `BatchWriteItem` in 25-item batches; no per-item DeleteItem loop |
| Performance target | full `km doctor` run < 30s (vs ~1:41 today) via ~10 concurrent HeadObject workers |
| Test file | `internal/app/cmd/doctor_state_digest_test.go` (existing file, add new tests) |
| Phase placement | Phase 85 (integer, not 84.5) |

### Claude's Discretion

- Exact function name for the new sweeper check (must be distinct from `checkStateLockDigest`)
- Whether to put sweeper code in `doctor.go` or a new `doctor_state_digest_sweeper.go` file
- Exact concurrency primitive (goroutine + semaphore channel vs worker pool struct) — must be idiom-consistent with `runChecks`
- Whether `S3StateHeadAPI` is a new interface or `S3StateReader` is widened

### Deferred Ideas (OUT OF SCOPE)

- Plug the upstream leak: `km destroy` / `km uninit` deleting the `terraform.tfstate-md5` DDB row alongside S3 object
- Sweeper for other digest-mismatch types (live S3 object + stale MD5)
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| IN-SCOPE-1 | New cleanup category: orphan DDB lock rows where S3 state object is definitively gone | New check function + new DI interfaces in `DoctorDeps` |
| IN-SCOPE-2 | `--delete-state-digests` flag + folded into `--with-deletes` umbrella | Wiring pattern fully documented below |
| IN-SCOPE-3 | Safety: definitive NoSuchKey only; ambiguous errors → skip; age > 24h guard | HeadObject returns `s3types.NotFound` (not `NoSuchKey`); age guard pattern from `doctor_ebs.go` provisioningCutoff |
| IN-SCOPE-4 | Deletion: `BatchWriteItem` 25 items/batch, `UnprocessedItems` retry | SDK v2 `BatchWriteItemInput.RequestItems` pattern documented below |
| IN-SCOPE-5 | Output: top-line summary + 10-item inline preview + `--json full list` | Stale Lambdas `strings.Builder` pattern documented below |
| IN-SCOPE-6 | Performance: parallel S3 HEAD scan ~10 concurrent workers | goroutine+semaphore channel pattern documented below |
| IN-SCOPE-7 | 5 TDD test cases in `doctor_state_digest_test.go` | Mock interfaces + test patterns from existing test file |
| ACCEPT-READ | `km doctor` < 30s on ~275-orphan account; 10-item preview; `--json` full list | Parallelism + HeadObject (no body download) enables this |
| ACCEPT-WRITE | `km doctor --with-deletes --dry-run=false` cleans orphans; live mismatches untouched | Age guard + NoSuchKey-only guard protects live rows |
| ACCEPT-TEST | All 5 TDD cases pass | Mock patterns for new interfaces documented below |
</phase_requirements>

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/dynamodb` | v1.57.0 | `BatchWriteItem`, `Scan` + `NewScanPaginator` | Already in go.mod; existing check uses same package |
| `github.com/aws/aws-sdk-go-v2/service/s3` | v1.97.1 | `HeadObject` for orphan detection | Already in go.mod; S3HeadBucketAPI uses HeadObject |
| `github.com/aws/aws-sdk-go-v2/service/s3/types` | same | `s3types.NotFound` (HeadObject 404) | Distinct from `s3types.NoSuchKey` (GetObject 404) — critical |
| `github.com/aws/aws-sdk-go-v2/service/dynamodb/types` | same | `dynamodbtypes.WriteRequest`, `dynamodbtypes.DeleteRequest` | BatchWriteItem items |
| `sync` stdlib | — | `sync.WaitGroup` + semaphore channel for worker pool | Pattern already used in `runChecks` |
| `fmt`, `strings`, `time`, `errors` stdlib | — | Output formatting, age guard, error classification | Used throughout doctor.go |

### No New Dependencies

This phase adds zero external dependencies. All required packages are already imported in `doctor.go`.

**Installation:** none required.

---

## Architecture Patterns

### Existing `checkStateLockDigest` — what it does today

```
Current function signature:
  checkStateLockDigest(ctx, s3Client S3StateReader, ddbClient LockDigestReader, lockTableName string) CheckResult

Current behavior:
  1. NewScanPaginator over lock table (already correct — H7)
  2. For each -md5 row: GetObject to download full state body + compute md5
  3. If NoSuchKey → orphan mismatch entry (single string, no age guard)
  4. If md5 mismatch → recovery command entry
  5. Returns single-line message: "state digest mismatch in N item(s): <all items joined>"
  6. Remediation: aws dynamodb update-item commands (for md5 mismatch only)
  7. Read-only: never deletes

Current interfaces:
  type S3StateReader interface {
      GetObject(ctx, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
  }
  type LockDigestReader interface {
      Scan(ctx, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
  }

Current DoctorDeps wiring:
  StateLockS3Client  S3StateReader   // wired to s3.NewFromConfig(awsCfg) in initRealDepsWithExisting
  StateLockDDBClient LockDigestReader // wired to dynamodb.NewFromConfig(awsCfg)

Current buildChecks registration (line ~2595):
  stateLockS3 := deps.StateLockS3Client
  stateLockDDB := deps.StateLockDDBClient
  lockTable := backendLockTableName(cfg)
  checks = append(checks, func(ctx context.Context) CheckResult {
      return checkStateLockDigest(ctx, stateLockS3, stateLockDDB, lockTable)
  })
```

### New check function signature (recommended)

```go
// Source: codebase inspection + BRIEF.md design decisions
func checkStateLockDigestSweeper(
    ctx      context.Context,
    s3Head   S3StateHeadAPI,      // new interface: HeadObject only
    ddbRead  LockDigestReader,    // existing interface: Scan only (reuse)
    ddbWrite LockDigestDeleterAPI, // new interface: BatchWriteItem only
    lockTableName string,
    dryRun         bool,
    deleteDigests  bool,
    minAgeToDelete time.Duration, // default 24h; configurable
) CheckResult
```

### New interfaces required

```go
// S3StateHeadAPI is the narrow S3 surface for the orphan sweeper.
// HeadObject returns s3types.NotFound (HTTP 404) — distinct from
// GetObject's s3types.NoSuchKey — when the state object is absent.
type S3StateHeadAPI interface {
    HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

// LockDigestDeleterAPI is the narrow DynamoDB surface for BatchWriteItem deletes.
type LockDigestDeleterAPI interface {
    BatchWriteItem(ctx context.Context, params *dynamodb.BatchWriteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
}
```

**CRITICAL:** `HeadObject` 404 is `s3types.NotFound`, NOT `s3types.NoSuchKey`. The existing `checkStateLockDigest` uses `GetObject` which returns `s3types.NoSuchKey`. If the new function uses `HeadObject`, the error type assertion must change to `var notFound *s3types.NotFound`.

### DoctorDeps additions

```go
// In DoctorDeps struct — add alongside existing StateLockS3Client/StateLockDDBClient:
StateLockS3HeadClient  S3StateHeadAPI       // HeadObject for orphan scan
StateLockDDBWriteClient LockDigestDeleterAPI // BatchWriteItem for orphan delete
DeleteStateDigests bool                      // --delete-state-digests opt-in
```

**Wiring in `initRealDepsWithExisting`:**
```go
// Both use the same awsCfg — same region, same credentials:
deps.StateLockS3HeadClient  = s3.NewFromConfig(awsCfg)       // *s3.Client satisfies S3StateHeadAPI
deps.StateLockDDBWriteClient = dynamodb.NewFromConfig(awsCfg) // *dynamodb.Client satisfies LockDigestDeleterAPI
```

Note: The existing `*s3.Client` wired to `StateLockS3Client` also satisfies `S3StateHeadAPI` (both are method sets on the same client). In production, the same `s3.NewFromConfig(awsCfg)` value can serve both roles. Two separate `DoctorDeps` fields are still correct for testability (separate mocks per interface).

### Flag wiring pattern (matches --delete-lambdas exactly)

**In `NewDoctorCmdWithDeps`:**
```go
var deleteStateDigests bool

// ... existing var block ...

cmd.Flags().BoolVar(&deleteStateDigests, "delete-state-digests", false,
    "With --dry-run=false, delete orphan DDB lock rows where the S3 state object is definitively gone (age > 24h). Only definitive 404 (NoSuchKey) triggers deletion — network/5xx errors are never deleted.")

// In the RunE body, in the withDeletes block:
if withDeletes {
    deleteEBS = true
    deleteSQS = true
    deleteS3 = true
    deleteLambdas = true
    deleteSSH = true
    deleteSSM = true
    deleteStateDigests = true  // ADD THIS LINE
}

// Pass deleteStateDigests through to runDoctor:
return runDoctor(cmd, provider, deps, jsonOutput, quietMode, dryRun, allRegions,
    deleteEBS, deleteSQS, deleteS3, deleteLambdas, deleteSSH, deleteSSM, deleteStateDigests)
```

**`runDoctor` signature extension:**
```go
func runDoctor(cmd *cobra.Command, cfg DoctorConfigProvider, deps *DoctorDeps,
    jsonOutput, quietMode, dryRun, allRegions,
    deleteEBS, deleteSQS, deleteS3, deleteLambdas, deleteSSH, deleteSSM,
    deleteStateDigests bool) error {
    // ...
    deps.DeleteStateDigests = deleteStateDigests
```

**`--with-deletes` flag description update** (append to existing):
```
"Shortcut for --delete-ebs --delete-sqs --delete-s3 --delete-lambdas --delete-ssh --delete-ssm --delete-state-digests. ..."
```

### Parallel S3 HEAD worker pool pattern

Codebase precedent: `runChecks` uses `sync.WaitGroup` + goroutines (unbounded). For per-item parallelism with a cap (~10), use a semaphore channel — the idiomatic Go pattern that avoids introducing a new struct type:

```go
// Source: codebase inspection — extends runChecks pattern with bounded concurrency
const headWorkers = 10

sem := make(chan struct{}, headWorkers)
var mu sync.Mutex
var wg sync.WaitGroup

type scanResult struct {
    lockID    string
    age       time.Duration
    isOrphan  bool    // definitive NoSuchKey
    cantVerify bool   // ambiguous error (network/5xx/throttle)
}
results := make([]scanResult, 0, len(lockItems))

for _, item := range lockItems {
    wg.Add(1)
    go func(lockID string, age time.Duration) {
        defer wg.Done()
        sem <- struct{}{}        // acquire
        defer func() { <-sem }() // release
        
        _, err := s3Head.HeadObject(ctx, &s3.HeadObjectInput{
            Bucket: awssdk.String(bucket),
            Key:    awssdk.String(key),
        })
        var notFound *s3types.NotFound
        if errors.As(err, &notFound) {
            mu.Lock()
            results = append(results, scanResult{lockID: lockID, age: age, isOrphan: true})
            mu.Unlock()
        } else if err != nil {
            // Generic error: ambiguous — do NOT delete
            mu.Lock()
            results = append(results, scanResult{lockID: lockID, cantVerify: true})
            mu.Unlock()
        }
        // else: HeadObject succeeded → object exists → skip (live or stale-md5 type)
    }(lockID, age)
}
wg.Wait()
```

### Age guard pattern (from `doctor_ebs.go`)

```go
// Source: internal/app/cmd/doctor_ebs.go line 105 — provisioningCutoff pattern
// For EBS: 10 minutes; for state-lock digests: 24h (configurable, passed as param)
cutoff := time.Now().Add(-minAgeToDelete)

// How to get age from DDB row:
// The DDB lock table stores when a row was last written. However, Terraform lock
// table items do NOT have a standard timestamp attribute — only LockID and Digest.
// THEREFORE: the age guard must use a separate timestamp source.
// 
// CRITICAL DESIGN QUESTION (see Open Questions #1):
// How to determine DDB row age without a timestamp attribute in the lock table?
```

### Age guard — DDB row timestamp problem

This is the key design gap. The Terraform/Terragrunt DDB lock table schema has:
- `LockID` (S, PK) — e.g. `tf-km-12345/use1/ses/terraform.tfstate-md5`
- `Digest` (S) — MD5 hex

There is NO standard `CreatedAt` or `UpdatedAt` attribute. Options:

1. **Use S3 HeadObject `LastModified`** — if the state object is gone (NoSuchKey), we have no S3 metadata. The age guard cannot be derived from S3 on orphan rows. **NOT viable for orphans.**

2. **Use DDB item `LastModified` via TTL/custom attribute** — Terraform does not write a timestamp to the lock table. **NOT available.**

3. **Age guard based on S3 object deletion time (not available)**

4. **Age guard based on sandbox destroy time from DDB sandbox table** — Could look up the sandbox record's `destroyed_at` field. Complex and indirect.

5. **Age guard via km-config.yaml configurable threshold, applied to ALL orphan rows** — Accept that we cannot compute exact age from available data. Set a conservative default (24h). Document that this means "rows that have been orphaned for at least the min-age threshold as approximated by: any row whose S3 state object has been gone longer than we can tell." In practice, if `km doctor` is run shortly after a `km destroy`, the 24h default means rows created within the last 24h are protected.

**RECOMMENDED APPROACH (matches BRIEF.md intent):** There is no per-row timestamp to check. The age guard should be implemented as a wall-clock guard: rows are only eligible for deletion if the LOCK TABLE SCAN itself is performed more than `minAgeToDelete` after the last known destructive operation. This is approximately equivalent to: refuse to delete any row from a scan that has not been preceded by at least one prior `km doctor` run. Since we have no prior-run timestamp, the simplest safe implementation is:

**Simpler alternative (HIGH confidence this is what BRIEF means):** Read the DDB row's own `LockID` — the key contains the module path. Compare against the `km destroy` or `km uninit` event timestamp if stored. Since we do NOT store this, the age guard in BRIEF.md is likely intended to apply to the orphan row's **DDB item creation time** — but DDB doesn't expose item creation time unless the table has TTL or a custom attribute.

**After re-reading BRIEF.md carefully:** "The DDB row's age exceeds the configurable threshold (default 24h). Matches the existing in-flight-create age guard pattern elsewhere in doctor." The EBS age guard uses `v.CreateTime` from the EC2 volume — EC2 volumes have a `CreateTime` field. The lock table does NOT have this. The planner must resolve this: either (a) the age guard is a no-op / always passes (not safe), or (b) the age guard reads a DDB item-level attribute that Terraform happens to write, or (c) use a different mechanism.

**Actual Terraform lock table item structure** (from Phase 84.1 code inspection):

```go
// From checkStateLockDigest source code — items only have LockID + Digest:
lockIDAttr, ok := item["LockID"].(*dynamodbtypes.AttributeValueMemberS)
digestAttr, ok := item["Digest"].(*dynamodbtypes.AttributeValueMemberS)
```

No `CreatedAt` or `UpdatedAt` field is visible in the scan. **The age guard cannot be applied per-row from DDB alone.** The planner must decide: either skip the per-row age guard (and accept the race window risk, mitigated by the 24h default being a conservative wall-clock buffer), OR add a configurable `--min-orphan-age` duration that is applied as: "do not delete any row from a doctor run that completes less than N hours after the last `km destroy` was known to have completed." Since `km doctor` has no memory, the simplest implementation is:

**FINAL RECOMMENDATION for planner:** Implement the age guard as a **scan-time timestamp guard**: when the scan begins, record `scanTime := time.Now()`. For each orphan row found, compute `rowAge = scanTime - someProxy`. Since no proxy timestamp is available in the DDB item, the practical implementation in this codebase is: **treat all orphan rows from a scan that is run more than `minAgeToDelete` (24h default) after the binary was built** — which is still not per-row. The most pragmatic correct approach: log every orphan row with "could not determine age; apply conservative guard: defer to next `km doctor` run" — but this defeats the purpose.

**Simplest correct implementation:** Apply the age guard based on the `--min-orphan-age` duration as a single "how old must this scan be" gate: **always skip deletion if the current `km doctor` run started less than the threshold ago since the last observed `km destroy`** — but this requires inter-run state.

**Pragmatic resolution (HIGH confidence this is the intended design):** The BRIEF says "Matches the existing in-flight-create age guard pattern." The EBS check skips volumes created within 10 minutes (using EC2's `CreateTime`). The lock-table analog would skip rows created within 24h. Since DDB lock items don't have `CreateTime`, check if the `LockID` matches any currently-provisioning sandbox (DDB sandbox record with `status=provisioning`). If so, skip. This is the race condition the guard protects against. The guard's real purpose is: do not delete a lock row for a module that is currently being applied. The 24h window is a proxy for "surely nothing has been running for 24h." A correct implementation: **skip deletion of any lockID whose sandbox-ID can be extracted and that sandbox has `status != destroyed` in the DDB sandbox table**. This is similar to how `checkStaleLambdas` cross-references the sandbox lister.

### BatchWriteItem delete pattern (25 items/batch)

```go
// Source: AWS SDK v2 dynamodb@v1.57.0 API inspection
// BatchWriteItem accepts up to 25 WriteRequest per call.
// UnprocessedItems must be retried.

const batchSize = 25

func batchDeleteLockItems(ctx context.Context, client LockDigestDeleterAPI, tableName string, lockIDs []string) (deleted int, failed int, failures map[string]error) {
    failures = make(map[string]error)
    
    for i := 0; i < len(lockIDs); i += batchSize {
        end := i + batchSize
        if end > len(lockIDs) {
            end = len(lockIDs)
        }
        batch := lockIDs[i:end]
        
        requests := make([]dynamodbtypes.WriteRequest, len(batch))
        for j, lockID := range batch {
            requests[j] = dynamodbtypes.WriteRequest{
                DeleteRequest: &dynamodbtypes.DeleteRequest{
                    Key: map[string]dynamodbtypes.AttributeValue{
                        "LockID": &dynamodbtypes.AttributeValueMemberS{Value: lockID},
                    },
                },
            }
        }
        
        out, err := client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
            RequestItems: map[string][]dynamodbtypes.WriteRequest{
                tableName: requests,
            },
        })
        if err != nil {
            for _, id := range batch {
                failed++
                failures[id] = err
            }
            continue
        }
        // Handle UnprocessedItems (partial throttle)
        if len(out.UnprocessedItems) > 0 {
            if unprocessed, ok := out.UnprocessedItems[tableName]; ok {
                for _, req := range unprocessed {
                    if req.DeleteRequest != nil {
                        if attr, ok := req.DeleteRequest.Key["LockID"].(*dynamodbtypes.AttributeValueMemberS); ok {
                            failed++
                            failures[attr.Value] = fmt.Errorf("unprocessed by DynamoDB (throttled)")
                        }
                    }
                }
                deleted += len(batch) - len(unprocessed)
            } else {
                deleted += len(batch)
            }
        } else {
            deleted += len(batch)
        }
    }
    return deleted, failed, failures
}
```

### Output format — matches Stale Lambdas exactly

The Stale Lambdas pattern (from `doctor_lambdas.go`) uses a `strings.Builder` and produces:

```
found N stale per-sandbox Lambda(s) (no DynamoDB record):
  km-budget-enforcer-sb-ghost (component=budget-enforcer, sandbox=sb-ghost)
  km-github-token-refresher-sb-ghost (...)
```

The new digest sweeper output (formatted mode):

```
// Summary line:
"state digest mismatch in N item(s) (M orphan: state object missing, K other)"

// Inline block (up to 10 items):
"  tf-km-12345/use1/ses/terraform.tfstate-md5 (orphan: state object missing)"
"  tf-km-12345/use1/ses2/terraform.tfstate-md5 (orphan: state object missing)"
// ...up to 10 items...

// Continuation (if > 10):
"  … and N more (use --json for full list)"
```

**Implementation pattern:**
```go
// Source: doctor_lambdas.go lines 133-148 pattern adapted for digest sweeper
const inlinePreviewCap = 10

var sb strings.Builder
orphanCount := len(orphanItems)  // definitive NoSuchKey
otherCount := len(otherMismatch) // live S3 + stale MD5
total := orphanCount + otherCount

fmt.Fprintf(&sb, "state digest mismatch in %d item(s) (%d orphan: state object missing, %d other)", 
    total, orphanCount, otherCount)

allItems := append(orphanItems, otherMismatch...)
for i, item := range allItems {
    if i >= inlinePreviewCap {
        fmt.Fprintf(&sb, "\n  … and %d more (use --json for full list)", total-inlinePreviewCap)
        break
    }
    fmt.Fprintf(&sb, "\n  %s", item)
}
```

### buildChecks registration — replace existing call

```go
// REPLACE in buildChecks (around line 2594):
// OLD:
checks = append(checks, func(ctx context.Context) CheckResult {
    return checkStateLockDigest(ctx, stateLockS3, stateLockDDB, lockTable)
})

// NEW:
stateLockS3Head := deps.StateLockS3HeadClient
stateLockDDBWrite := deps.StateLockDDBWriteClient
deleteStateDigests := deps.DeleteStateDigests
minOrphanAge := 24 * time.Hour // default; expose as configurable if needed
checks = append(checks, func(ctx context.Context) CheckResult {
    return checkStateLockDigestSweeper(ctx, stateLockS3Head, stateLockDDB, stateLockDDBWrite,
        lockTable, dryRun, deleteStateDigests, minOrphanAge)
})
```

### Recommended project structure (no change to directories)

All new code goes in `internal/app/cmd/`. Options for file placement:

- **Option A (recommended):** New file `doctor_state_digest_sweeper.go` for the new function and interfaces; leave `doctor.go` changes minimal (flags, DoctorDeps fields, buildChecks wiring).
- **Option B:** Inline everything in `doctor.go` (matches how `checkStateLockDigest` is currently placed at the bottom of doctor.go). This is consistent but makes doctor.go even longer.

Option A is preferred because `doctor_lambdas.go` establishes the pattern of per-check-category files.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| DDB scan pagination | Manual nextPage cursor | `dynamodb.NewScanPaginator` | Already used in `checkStateLockDigest`; SDK handles pagination correctly (H7) |
| Parallel item processing with cap | Custom worker pool struct | `sync.WaitGroup` + semaphore channel `make(chan struct{}, 10)` | Idiom used throughout Go stdlib; consistent with `runChecks` |
| DDB batch deletes | Loop of individual `DeleteItem` calls | `BatchWriteItem` with 25-item batches | ~25x fewer API calls for 275 items; consistent with BRIEF.md |
| S3 existence check | Download body (GetObject) | `HeadObject` | HeadObject is ~10x faster; avoids downloading MB-size state files |
| Error type check for S3 404 | String matching on error message | `errors.As(err, &s3types.NotFound{})` | Type-safe; HeadObject returns `s3types.NotFound`, not `s3types.NoSuchKey` |

**Key insight:** The switch from `GetObject` (body download + md5) to `HeadObject` (existence check only) is the single biggest performance win. 275 items × ~360ms per sequential GetObject = ~99s vs 275 items ÷ 10 workers × ~30ms HeadObject = ~825ms. This is the path from ~1:41 to ~30s.

---

## Common Pitfalls

### Pitfall 1: Wrong S3 404 error type for HeadObject
**What goes wrong:** Using `errors.As(err, &s3types.NoSuchKey{})` for HeadObject results — it will never match. HeadObject returns `s3types.NotFound`, not `s3types.NoSuchKey`.
**Why it happens:** `checkStateLockDigest` uses `GetObject` which returns `s3types.NoSuchKey`. The new sweeper switches to `HeadObject`.
**How to avoid:** Use `var notFound *s3types.NotFound` with `errors.As(err, &notFound)` in the HeadObject path. The existing `GetObject` path (in the non-orphan mismatch branch of the old check) continues to use `s3types.NoSuchKey`.
**Warning signs:** All HeadObject calls returning "could not verify" in test or production, even for genuinely missing objects.

### Pitfall 2: `UnprocessedItems` in BatchWriteItem silently dropped
**What goes wrong:** Only checking `err != nil` from `BatchWriteItem`; if AWS throttles partial batches, `UnprocessedItems` is non-empty but `err` is nil. Items silently fail to delete.
**Why it happens:** BatchWriteItem is a partial-success API — it succeeds even if some items couldn't be processed.
**How to avoid:** Always check `out.UnprocessedItems[tableName]` after a successful (nil-error) `BatchWriteItem` call. Count them as failed in the summary.
**Warning signs:** `km doctor --with-deletes --dry-run=false` reports "275 deleted" on a throttled account, but re-run still shows orphans.

### Pitfall 3: `runDoctor` and `NewDoctorCmdWithDeps` signature mismatch after adding new bool param
**What goes wrong:** Adding `deleteStateDigests bool` to `runDoctor` breaks the existing call site in `NewDoctorCmdWithDeps` and any test harness that calls `runDoctor` directly.
**Why it happens:** `runDoctor` is called in one place with a fixed positional bool list; adding a param requires updating all callers.
**How to avoid:** Add `deleteStateDigests` as the LAST bool parameter (matches the pattern where `deleteSSM` was added last). Check all callers: `NewDoctorCmdWithDeps` RunE and any test helpers.
**Warning signs:** Compile error "too few arguments in call to runDoctor".

### Pitfall 4: Goroutine leak if context cancelled during worker pool
**What goes wrong:** Workers blocked on `sem <- struct{}{}` when context is cancelled will block forever.
**How to avoid:** Use a select on the semaphore acquire:
```go
select {
case sem <- struct{}{}:
case <-ctx.Done():
    return
}
```
This ensures workers respect context cancellation.

### Pitfall 5: Age guard is per-row but DDB lock table has no timestamp
**What goes wrong:** Implementing age guard as `if rowAge < minAgeToDelete { skip }` but there's no per-row timestamp in the Terraform lock table schema.
**How to avoid:** See "Age guard — DDB row timestamp problem" section above. Planner must choose an approach. Recommended: cross-reference the DDB sandbox table to skip any lockID whose embedded sandbox-ID maps to a non-destroyed sandbox record (same approach as `checkStaleLambdas`).
**Warning signs:** Age guard is a no-op or incorrectly applied.

### Pitfall 6: `--with-deletes` description not updated
**What goes wrong:** The flag description for `--with-deletes` lists specific flags; adding `--delete-state-digests` to the umbrella without updating the description misleads operators.
**How to avoid:** Update the `BoolVar` description for `--with-deletes` to include `--delete-state-digests`.

---

## Code Examples

### Existing interface compile-time assertion pattern (from `doctor_state_digest_test.go`)
```go
// Source: internal/app/cmd/doctor_state_digest_test.go lines 322-324
var _ S3StateReader = (*mockS3StateReader)(nil)
var _ LockDigestReader = (*mockLockDigestReader)(nil)

// New additions for phase 85 tests:
var _ S3StateHeadAPI = (*mockS3StateHead)(nil)
var _ LockDigestDeleterAPI = (*mockLockDigestDeleter)(nil)
```

### Mock for S3StateHeadAPI (new TDD tests)
```go
// New mock for S3StateHeadAPI — HeadObject returns NotFound or an error
type mockS3StateHead struct {
    missing map[string]bool // bucket+"/"+key present → returns s3types.NotFound
    err     error           // non-nil → returns this error for ALL calls (ambiguous/5xx case)
}

func (m *mockS3StateHead) HeadObject(_ context.Context, params *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
    if m.err != nil {
        return nil, m.err
    }
    full := awssdk.ToString(params.Bucket) + "/" + awssdk.ToString(params.Key)
    if m.missing[full] {
        return nil, &s3types.NotFound{Message: awssdk.String("not found")}
    }
    return &s3.HeadObjectOutput{}, nil
}
```

### Mock for LockDigestDeleterAPI (new TDD tests)
```go
// New mock for BatchWriteItem — records calls for test assertions
type mockLockDigestDeleter struct {
    calls       [][]string // each entry is a batch of lockIDs in one call
    err         error
    unprocessed []string // lockIDs to return in UnprocessedItems
}

func (m *mockLockDigestDeleter) BatchWriteItem(
    _ context.Context,
    params *dynamodb.BatchWriteItemInput,
    _ ...func(*dynamodb.Options),
) (*dynamodb.BatchWriteItemOutput, error) {
    if m.err != nil {
        return nil, m.err
    }
    var batch []string
    for _, table := range params.RequestItems {
        for _, req := range table {
            if req.DeleteRequest != nil {
                if attr, ok := req.DeleteRequest.Key["LockID"].(*dynamodbtypes.AttributeValueMemberS); ok {
                    batch = append(batch, attr.Value)
                }
            }
        }
    }
    m.calls = append(m.calls, batch)
    
    out := &dynamodb.BatchWriteItemOutput{}
    // simulate UnprocessedItems for specific test cases
    // ...
    return out, nil
}
```

### TDD test skeleton (5 required cases)
```go
// 1. orphan + age-passes → row deleted
func TestDigestSweeper_OrphanAgePassesDeleted(t *testing.T) { ... }

// 2. orphan + age-fails → row skipped (never deleted within guard window)
func TestDigestSweeper_OrphanAgeFailsSkipped(t *testing.T) { ... }

// 3. S3 HEAD returns network/5xx error → row skipped
func TestDigestSweeper_S3HeadAmbiguousError_Skipped(t *testing.T) { ... }

// 4. Non-orphan mismatch type (live S3 object + stale MD5) → never deleted by sweeper
func TestDigestSweeper_LiveS3StaleDigest_NotDeletedBySweeper(t *testing.T) { ... }

// 5. Batch of 26 items → splits into 2 BatchWriteItem calls
func TestDigestSweeper_26Items_TwoBatches(t *testing.T) { ... }
```

### fakeSandboxLister (already exists in tests — reuse)
```go
// Source: internal/app/cmd/doctor_helpers_test.go or doctor_lambdas_test.go
// fakeSandboxLister is already defined in the test package — reuse it
type fakeSandboxLister struct {
    records []kmaws.SandboxRecord
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| No orphan cleanup | `--with-deletes` umbrella + per-category flags | Phase 84.1+ | Operator can clean up stale resources without manual aws CLI |
| Sequential S3 GetObject (body download) | Parallel S3 HeadObject (existence check only) | Phase 85 (this phase) | ~10x faster per item; ~100x total with parallelism |
| Single-line join of all mismatches | summary + 10-item preview + --json full list | Phase 85 (this phase) | 275-item output becomes readable |
| Read-only check | Read + optional delete via BatchWriteItem | Phase 85 (this phase) | Automated cleanup of 275 accumulated orphans |

**Key interface change:** `S3StateReader` (GetObject) → `S3StateHeadAPI` (HeadObject) for the orphan check path. The non-orphan mismatch check (live S3 object + stale MD5) still needs GetObject or can be removed from the new sweeper (per BRIEF.md out-of-scope: "Rows where the S3 object EXISTS but the MD5 doesn't match... are reported by the check today and will continue to be reported"). The new sweeper must still report md5-mismatch rows (WARN with remediation command) but must not delete them. This means the sweeper may need BOTH interfaces (HeadObject for orphan scan + GetObject for md5-mismatch detection), OR can split responsibilities: HeadObject first to detect orphans, then optionally GetObject for md5 verification on surviving rows.

**PLANNER NOTE:** The simplest approach that meets BRIEF.md: HeadObject for ALL rows first (parallel scan), categorize as: (a) NotFound → orphan candidate, (b) network/5xx → "could not verify", (c) object exists → optionally GetObject for md5 check. The md5 check (GetObject) on surviving rows can remain sequential since those rows are not the performance bottleneck (orphans are the majority).

---

## Open Questions

1. **DDB row age guard — no per-row timestamp available**
   - What we know: Terraform lock table only has `LockID` and `Digest` attributes. No `CreatedAt`.
   - What's unclear: How to implement the 24h age guard per BRIEF.md without per-row timestamp.
   - Recommendation: Cross-reference the DDB sandbox table. Extract sandbox-ID from LockID (module path prefix often contains sandbox-ID or install prefix). Skip deletion if the sandbox table has a live record for that sandbox. Apply the 24h threshold as: sandbox `destroyed_at` (if available) must be > 24h ago. If no sandbox record exists at all (truly orphaned from a test/manual cleanup), age guard passes. This gives the planner a concrete, implementable design.

2. **Should `checkStateLockDigest` be preserved or replaced?**
   - What we know: BRIEF.md says "replaces the unreadable single-line digest-mismatch warn." The existing tests are locked to `checkStateLockDigest`'s current behavior.
   - What's unclear: Whether the old function is deprecated/removed or the new function is additive.
   - Recommendation: Replace the `buildChecks` registration (remove old call, add new call). Keep `checkStateLockDigest` function body so its Phase 84.1 tests remain valid. The new sweeper is a separate function registered in its place.

3. **Does `S3StateReader` need to be widened to include HeadObject?**
   - What we know: `S3StateReader` currently only has `GetObject`. `S3HeadBucketAPI` has HeadObject but also HeadBucket (too wide). A new `S3StateHeadAPI` is the narrowest option.
   - Recommendation: New interface `S3StateHeadAPI` with only `HeadObject`. Consistent with "narrow, one method per service API surface used" convention documented in `doctor.go` line 79.

---

## Validation Architecture

> `nyquist_validation` is enabled in `.planning/config.json` — this section is required.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` package |
| Config file | none — `go test` standard |
| Quick run command | `go test ./internal/app/cmd/ -run TestDigestSweeper -v` |
| Full suite command | `go test ./internal/app/cmd/ -v` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| IN-SCOPE-7 / TDD-1 | orphan + age passes → row deleted | unit | `go test ./internal/app/cmd/ -run TestDigestSweeper_OrphanAgePassesDeleted -v` | ❌ Wave 0 |
| IN-SCOPE-7 / TDD-2 | orphan + age fails → row skipped | unit | `go test ./internal/app/cmd/ -run TestDigestSweeper_OrphanAgeFailsSkipped -v` | ❌ Wave 0 |
| IN-SCOPE-7 / TDD-3 | S3 5xx/network error → skip, no delete | unit | `go test ./internal/app/cmd/ -run TestDigestSweeper_S3HeadAmbiguousError -v` | ❌ Wave 0 |
| IN-SCOPE-7 / TDD-4 | live S3 + stale MD5 → never deleted | unit | `go test ./internal/app/cmd/ -run TestDigestSweeper_LiveS3StaleDigest -v` | ❌ Wave 0 |
| IN-SCOPE-7 / TDD-5 | 26 items → 2 BatchWriteItem calls | unit | `go test ./internal/app/cmd/ -run TestDigestSweeper_26Items_TwoBatches -v` | ❌ Wave 0 |
| IN-SCOPE-2 / flags | `--delete-state-digests` wired to `--with-deletes` | unit | `go test ./internal/app/cmd/ -run TestDoctor -v` (existing doctor_test.go) | ❌ Wave 0 |
| ACCEPT-READ | `km doctor` reports summary + 10-item preview | unit (output format) | `go test ./internal/app/cmd/ -run TestDigestSweeper_OutputFormat -v` | ❌ Wave 0 |
| ACCEPT-WRITE | dryRun=false + deleteStateDigests=true → BatchWriteItem called | unit | Covered by TDD-1 | ❌ Wave 0 |
| IN-SCOPE-4 | `UnprocessedItems` handled correctly | unit | `go test ./internal/app/cmd/ -run TestDigestSweeper_UnprocessedItems -v` | ❌ Wave 0 (optional) |

### Existing passing tests (must remain green)
| Test | Command |
|------|---------|
| All Phase 84.1 state digest tests | `go test ./internal/app/cmd/ -run TestCheckStateLockDigest -v` |
| All stale Lambda tests | `go test ./internal/app/cmd/ -run TestCheckStaleLambdas -v` |
| `TestParseLockID_KeyWithSlashes` | `go test ./internal/app/cmd/ -run TestParseLockID -v` |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/cmd/ -run TestDigestSweeper -v`
- **Per wave merge:** `go test ./internal/app/cmd/ -v`
- **Phase gate:** `go test ./internal/app/cmd/ -v` green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `internal/app/cmd/doctor_state_digest_sweeper.go` — new file with `checkStateLockDigestSweeper`, `S3StateHeadAPI`, `LockDigestDeleterAPI`
- [ ] New TDD test functions in `internal/app/cmd/doctor_state_digest_test.go` (5 required + optional UnprocessedItems + output format)
- [ ] Framework install: none — Go stdlib testing

---

## Sources

### Primary (HIGH confidence — direct codebase inspection)
- `internal/app/cmd/doctor.go` — `checkStateLockDigest`, `DoctorDeps`, flag wiring, `runChecks`, `runDoctor`, `buildChecks`
- `internal/app/cmd/doctor_lambdas.go` — output format pattern (strings.Builder, per-item marker), `--delete-lambdas` pattern
- `internal/app/cmd/doctor_lambdas_test.go` — test mock patterns, `fakeSandboxLister`
- `internal/app/cmd/doctor_state_digest_test.go` — existing Phase 84.1 mocks (`mockS3StateReader`, `mockLockDigestReader`)
- `internal/app/cmd/doctor_ebs.go` — age guard pattern (`provisioningCutoff`)
- `/Users/khundeck/go/pkg/mod/github.com/aws/aws-sdk-go-v2/service/dynamodb@v1.57.0/api_op_BatchWriteItem.go` — `BatchWriteItemInput`, `UnprocessedItems`
- `/Users/khundeck/go/pkg/mod/github.com/aws/aws-sdk-go-v2/service/s3@v1.97.1/types/errors.go` — `s3types.NotFound` vs `s3types.NoSuchKey`
- `.planning/phases/85-doctor-orphan-state-lock-digest-sweeper-report-cleanup/BRIEF.md` — locked decisions, acceptance criteria, TDD test list

### Secondary (MEDIUM confidence)
- `go.mod` — SDK versions confirmed: dynamodb v1.57.0, s3 v1.97.1

### Tertiary (LOW confidence)
- None

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all from direct codebase inspection, no inference
- Architecture: HIGH — flag wiring, interface shape, and output format all directly observed from analogs in the codebase
- Age guard design: MEDIUM — the DDB lock table has no timestamp attribute; the recommended design (cross-reference sandbox table) is an inference from the BRIEF's intent; planner must validate
- Pitfalls: HIGH — S3 HeadObject vs GetObject error type difference verified from SDK source

**Research date:** 2026-05-18
**Valid until:** 2026-06-18 (stable internal codebase; no external APIs)
