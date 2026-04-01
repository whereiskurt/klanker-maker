---
phase: 39-migrate-sandbox-metadata-s3-to-dynamodb
verified: 2026-04-01T00:28:48Z
status: gaps_found
score: 10/12 must-haves verified
gaps:
  - truth: "ttl-handler Lambda reads/writes/deletes metadata via DynamoDB"
    status: failed
    reason: "cmd/ttl-handler/main.go still calls awspkg.ReadSandboxMetadata (S3), PutObject for metadata.json, and awspkg.DeleteSandboxMetadata (S3). No DynamoDB client is initialized. SANDBOX_TABLE_NAME env var is not wired in infra/modules/ttl-handler/v1.0.0/main.tf."
    artifacts:
      - path: "cmd/ttl-handler/main.go"
        issue: "Uses ReadSandboxMetadata (S3), PutObject metadata.json, DeleteSandboxMetadata (S3) — all S3 paths. No ReadSandboxMetadataDynamo/WriteSandboxMetadataDynamo/DeleteSandboxMetadataDynamo calls."
      - path: "infra/modules/ttl-handler/v1.0.0/main.tf"
        issue: "SANDBOX_TABLE_NAME env var not added to Lambda environment block. IAM permissions exist but Lambda code never uses DynamoDB."
    missing:
      - "Replace ReadSandboxMetadata with ReadSandboxMetadataDynamo + S3 fallback in handleExtend and handleDestroy"
      - "Replace PutObject(metadata.json) with WriteSandboxMetadataDynamo in handleExtend"
      - "Replace DeleteSandboxMetadata with DeleteSandboxMetadataDynamo in handleDestroy"
      - "Add dynamodb.NewFromConfig(awsCfg) client initialization at Lambda startup"
      - "Add SANDBOX_TABLE_NAME env var to ttl-handler Lambda environment in infra/modules/ttl-handler/v1.0.0/main.tf"
      - "Add sandbox_table_name variable to infra/modules/ttl-handler/v1.0.0/variables.tf"

  - truth: "email-create-handler Lambda reads metadata via DynamoDB"
    status: failed
    reason: "cmd/email-create-handler/main.go handleStatus uses direct S3 GetObject on tf-km/sandboxes/{id}/metadata.json. No DynamoDB import or client initialization present."
    artifacts:
      - path: "cmd/email-create-handler/main.go"
        issue: "handleStatus (line ~246) reads metadata via h.S3Client.GetObject on metadata.json key — S3 path, not DynamoDB."
      - path: "infra/modules/email-handler/v1.0.0/main.tf"
        issue: "SANDBOX_TABLE_NAME env var not in Lambda environment block."
    missing:
      - "Replace S3 GetObject metadata read in handleStatus with ReadSandboxMetadataDynamo + S3 fallback"
      - "Add dynamodb.NewFromConfig(awsCfg) client initialization at Lambda startup"
      - "Add SANDBOX_TABLE_NAME env var to email-handler Lambda environment in infra/modules/email-handler/v1.0.0/main.tf"
      - "Add sandbox_table_name variable to infra/modules/email-handler/v1.0.0/variables.tf"

  - truth: "km create writes metadata to DynamoDB (all 3 paths: EC2, Docker, Remote)"
    status: failed
    reason: "runCreateRemote (the --remote path) at line ~1480 writes metadata via s3Client.PutObject to tf-km/sandboxes/{id}/metadata.json. EC2 path (line 537) and Docker path (line 1191) correctly use WriteSandboxMetadataDynamo."
    artifacts:
      - path: "internal/app/cmd/create.go"
        issue: "runCreateRemote function writes 'starting' metadata to S3 (PutObject to metadata.json) instead of DynamoDB. EC2 and Docker paths are correct."
    missing:
      - "Replace s3Client.PutObject metadata.json write in runCreateRemote (Step 8b) with WriteSandboxMetadataDynamo"
      - "Add DynamoDB client initialization and tableName lookup in runCreateRemote"
      - "Add S3 fallback on ResourceNotFoundException for backward compat in runCreateRemote"
---

# Phase 39: Migrate Sandbox Metadata S3 to DynamoDB Verification Report

**Phase Goal:** All sandbox metadata reads/writes (km list, km status, km lock/unlock, km pause/resume, km create/destroy, and Lambda handlers) switch from S3 JSON blobs to a DynamoDB km-sandboxes table with alias-index GSI, atomic lock/unlock via ConditionExpression, DynamoDB TTL for auto-cleanup, and backward-compat S3 fallback when table does not exist — artifacts remain in S3.
**Verified:** 2026-04-01T00:28:48Z
**Status:** gaps_found
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| #  | Truth | Status | Evidence |
|----|-------|--------|----------|
| 1  | ReadSandboxMetadataDynamo returns structured SandboxMetadata from DynamoDB GetItem | VERIFIED | pkg/aws/sandbox_dynamo.go line 251; 13 tests pass |
| 2  | WriteSandboxMetadataDynamo stores ttl_expiry as Number (Unix epoch) for DynamoDB TTL | VERIFIED | sandbox_dynamo.go line 237: AttributeValueMemberN; test coverage confirmed |
| 3  | LockSandboxDynamo uses ConditionExpression for atomic lock — no read-modify-write race | VERIFIED | sandbox_dynamo.go line 396: `attribute_exists(sandbox_id) AND (attribute_not_exists(locked) OR locked = :f)`; errors.As for ConditionalCheckFailedException |
| 4  | UnlockSandboxDynamo uses ConditionExpression for atomic unlock | VERIFIED | sandbox_dynamo.go line 424: `attribute_exists(sandbox_id) AND locked = :t` |
| 5  | ListAllSandboxesByDynamo paginates past 1MB boundary using LastEvaluatedKey | VERIFIED | sandbox_dynamo.go line 341: pagination loop; test with 2-page mock |
| 6  | ResolveSandboxAliasDynamo queries alias-index GSI for O(1) alias resolution | VERIFIED | sandbox_dynamo.go line 356: KeyConditionExpression on alias-index GSI |
| 7  | DeleteSandboxMetadataDynamo removes a sandbox record | VERIFIED | sandbox_dynamo.go line 296; test confirms DeleteItem key |
| 8  | km-sandboxes DynamoDB table module with alias-index GSI and ttl_expiry TTL | VERIFIED | infra/modules/dynamodb-sandboxes/v1.0.0/main.tf: alias-index GSI ALL projection, ttl on ttl_expiry, PAY_PER_REQUEST |
| 9  | IAM permissions on all 3 Lambda roles (ttl-handler, email-handler, create-handler) | VERIFIED | All 3 main.tf have aws_iam_role_policy.dynamodb_sandboxes with GetItem/PutItem/UpdateItem/DeleteItem/Scan/Query on km-sandboxes and alias-index |
| 10 | Config has SandboxTableName field defaulting to km-sandboxes | VERIFIED | internal/app/config/config.go line 82-84, 151, 204, 239 |
| 11 | km list/lock/unlock/destroy/status/pause/resume/stop/extend/budget use DynamoDB | VERIFIED | All CLI commands confirmed using DynamoDB functions with ResourceNotFoundException S3 fallback |
| 12 | km create writes metadata to DynamoDB (all 3 paths: EC2, Docker, Remote) | FAILED | EC2 (line 537) and Docker (line 1191) use WriteSandboxMetadataDynamo. Remote (runCreateRemote ~line 1480) still writes PutObject to tf-km/sandboxes/{id}/metadata.json |
| 13 | ttl-handler Lambda reads/writes/deletes metadata via DynamoDB | FAILED | cmd/ttl-handler/main.go line 162: ReadSandboxMetadata (S3); line 209: PutObject metadata.json; line 479: DeleteSandboxMetadata (S3). No DynamoDB calls present. |
| 14 | email-create-handler Lambda reads metadata via DynamoDB | FAILED | cmd/email-create-handler/main.go handleStatus ~line 251: S3 GetObject on metadata.json key. No DynamoDB call. |
| 15 | SANDBOX_TABLE_NAME env var wired into Lambda Terraform modules | FAILED | Neither infra/modules/ttl-handler/v1.0.0/main.tf nor infra/modules/email-handler/v1.0.0/main.tf have SANDBOX_TABLE_NAME in Lambda environment block. |

**Score:** 10/15 truths verified (mapped to 10/12 plan must-haves)

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/aws/sandbox_dynamo.go` | DynamoDB CRUD functions | VERIFIED | 460 lines, 8 exported functions + SandboxMetadataAPI interface |
| `pkg/aws/sandbox_dynamo_test.go` | Unit tests with mock (min 200 lines) | VERIFIED | 482 lines, 13 test functions, all pass |
| `infra/modules/dynamodb-sandboxes/v1.0.0/main.tf` | DynamoDB table with GSI and TTL | VERIFIED | sandbox_id PK, alias-index GSI ALL, ttl_expiry TTL |
| `infra/live/use1/dynamodb-sandboxes/terragrunt.hcl` | Terragrunt live config | VERIFIED | Sources v1.0.0, table_name = km-sandboxes |
| `internal/app/config/config.go` | SandboxTableName config field | VERIFIED | Default km-sandboxes, viper-wired |
| `internal/app/cmd/create.go` | DynamoDB write on all 3 substrate paths | PARTIAL | EC2 and Docker paths VERIFIED; Remote (runCreateRemote) still writes S3 |
| `internal/app/cmd/list.go` | DynamoDB scan | VERIFIED | ListAllSandboxesByDynamo + ResourceNotFoundException S3 fallback |
| `internal/app/cmd/lock.go` | Atomic DynamoDB lock | VERIFIED | LockSandboxDynamo + ResourceNotFoundException S3 fallback |
| `cmd/ttl-handler/main.go` | Lambda uses DynamoDB for metadata | FAILED | ReadSandboxMetadata + PutObject + DeleteSandboxMetadata — all S3 paths |
| `cmd/email-create-handler/main.go` | Lambda uses DynamoDB for read | FAILED | handleStatus reads via S3 GetObject on metadata.json |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `internal/app/cmd/create.go` | `pkg/aws/sandbox_dynamo.go` | WriteSandboxMetadataDynamo | PARTIAL | EC2 line 537, Docker line 1191 — WIRED. runCreateRemote — NOT WIRED |
| `internal/app/cmd/list.go` | `pkg/aws/sandbox_dynamo.go` | ListAllSandboxesByDynamo | VERIFIED | line 135 |
| `internal/app/cmd/lock.go` | `pkg/aws/sandbox_dynamo.go` | LockSandboxDynamo | VERIFIED | line 75 |
| `cmd/ttl-handler/main.go` | `pkg/aws/sandbox_dynamo.go` | ReadSandboxMetadataDynamo | NOT WIRED | File still uses S3 functions exclusively |
| `infra/modules/ttl-handler/v1.0.0/main.tf` | Lambda env vars | SANDBOX_TABLE_NAME | NOT WIRED | Env block does not contain SANDBOX_TABLE_NAME |
| `infra/modules/email-handler/v1.0.0/main.tf` | Lambda env vars | SANDBOX_TABLE_NAME | NOT WIRED | Env block does not contain SANDBOX_TABLE_NAME |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| META-DYNAMO-01 | 39-01 | ReadSandboxMetadataDynamo | SATISFIED | sandbox_dynamo.go line 251, 13 tests pass |
| META-DYNAMO-02 | 39-01 | WriteSandboxMetadataDynamo with Number TTL | SATISFIED | sandbox_dynamo.go line 237 AttributeValueMemberN |
| META-DYNAMO-03 | 39-01 | LockSandboxDynamo atomic ConditionExpression | SATISFIED | sandbox_dynamo.go line 396 |
| META-DYNAMO-04 | 39-01 | UnlockSandboxDynamo atomic ConditionExpression | SATISFIED | sandbox_dynamo.go line 424 |
| META-DYNAMO-05 | 39-01 | ListAllSandboxesByDynamo with pagination | SATISFIED | sandbox_dynamo.go line 341 |
| META-DYNAMO-06 | 39-01 | ResolveSandboxAliasDynamo GSI query | SATISFIED | sandbox_dynamo.go line 352 |
| META-DYNAMO-IAM | 39-02 | IAM permissions on all 3 Lambda roles | SATISFIED | All 3 main.tf have aws_iam_role_policy.dynamodb_sandboxes |
| META-DYNAMO-INFRA | 39-02 | DynamoDB table module with correct schema | SATISFIED | dynamodb-sandboxes/v1.0.0/main.tf |
| META-DYNAMO-CONFIG | 39-02 | SandboxTableName config field | SATISFIED | config.go line 82 |
| META-DYNAMO-SWITCHOVER-CLI | 39-03 | All CLI commands use DynamoDB | PARTIAL | 10 of 11 CLI files correct; create.go Remote path still uses S3 |
| META-DYNAMO-SWITCHOVER-LAMBDA | 39-03 | Lambda handlers use DynamoDB | BLOCKED | ttl-handler: 0/3 metadata ops use DynamoDB. email-handler: 0/1 metadata op uses DynamoDB. |
| META-DYNAMO-BACKWARD-COMPAT | 39-03 | S3 fallback on ResourceNotFoundException | PARTIAL | CLI commands have fallback. Lambda handlers have no DynamoDB calls, so backward-compat is moot — they never tried DynamoDB. |

**Note on requirement IDs:** META-DYNAMO-* requirement IDs appear in ROADMAP.md and plan frontmatter but are NOT present in .planning/REQUIREMENTS.md Traceability table. These were introduced in Phase 39 without being added to REQUIREMENTS.md. This is an administrative gap — the requirements are defined in the plans and ROADMAP, but the central registry is not updated.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `cmd/ttl-handler/main.go` | 162 | `awspkg.ReadSandboxMetadata(ctx, metaS3Client, ...)` | BLOCKER | Lambda reads sandbox metadata from S3 instead of DynamoDB — bypasses the migration entirely |
| `cmd/ttl-handler/main.go` | 209 | `h.S3Client.PutObject(ctx, &s3.PutObjectInput{Key: "…/metadata.json"})` | BLOCKER | Lambda writes updated TTL metadata to S3 instead of DynamoDB |
| `cmd/ttl-handler/main.go` | 479 | `awspkg.DeleteSandboxMetadata(ctx, h.S3Client, ...)` | BLOCKER | Lambda deletes metadata from S3 instead of DynamoDB |
| `cmd/email-create-handler/main.go` | ~251 | `h.S3Client.GetObject(ctx, &s3.GetObjectInput{Key: "…/metadata.json"})` | BLOCKER | Lambda reads sandbox metadata from S3 instead of DynamoDB |
| `internal/app/cmd/create.go` | ~1480 | `s3Client.PutObject(ctx, &s3.PutObjectInput{Key: "…/metadata.json"})` | BLOCKER | runCreateRemote writes "starting" metadata to S3 instead of DynamoDB — km list will not show remotely-created sandboxes unless they are also in DynamoDB |
| `cmd/ttl-handler/main.go` | 385 | `// TODO: Read substrate from metadata.json to handle ECS sandboxes.` | WARNING | Pre-existing TODO, unrelated to this phase |

### Human Verification Required

None — all gaps are programmatically verifiable.

### Gaps Summary

Three related gaps all stem from the same root cause: **Plan 03 Task 2 (Lambda handler switchover) was not completed.** The SUMMARY.md for Plan 03 claims "Lambda builds OK" and "Both EC2 and Docker substrates use DynamoDB" but does not specifically state Lambda handlers were switched. The actual code confirms they were not.

**Gap 1 — ttl-handler Lambda (META-DYNAMO-SWITCHOVER-LAMBDA):** The ttl-handler is the most critical Lambda — it handles TTL expiry events and sandbox destruction triggered by budget enforcement. It makes 3 metadata calls, all still using S3. The IAM policy was added (Phase 39-02) but the Lambda code was never updated. The SANDBOX_TABLE_NAME env var is also not wired through Terraform.

**Gap 2 — email-create-handler Lambda (META-DYNAMO-SWITCHOVER-LAMBDA):** The handleStatus function reads sandbox metadata from S3 via direct GetObject. SANDBOX_TABLE_NAME env var missing from Terraform.

**Gap 3 — km create --remote path (META-DYNAMO-SWITCHOVER-CLI partial):** The `runCreateRemote` function writes "starting" metadata to S3 at Step 8b. This means sandboxes created via `km create --remote` will not appear in `km list` (which now reads from DynamoDB) until the create-handler Lambda writes metadata after provisioning. The user experience is broken: remote-created sandboxes are invisible to `km list` during provisioning.

These 3 gaps are in the same plan (39-03 Task 2) and can be fixed together in a single gap-closure plan.

---

_Verified: 2026-04-01T00:28:48Z_
_Verifier: Claude (gsd-verifier)_
