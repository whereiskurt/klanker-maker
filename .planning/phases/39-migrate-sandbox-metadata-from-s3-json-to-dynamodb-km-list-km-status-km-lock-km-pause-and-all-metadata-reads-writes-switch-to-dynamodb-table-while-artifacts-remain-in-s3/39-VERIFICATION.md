---
phase: 39-migrate-sandbox-metadata-s3-to-dynamodb
verified: 2026-04-01T02:57:56Z
status: human_needed
score: 15/15 must-haves verified
re_verification:
  previous_status: gaps_found
  previous_score: 10/15
  gaps_closed:
    - "ttl-handler Lambda reads/writes/deletes metadata via DynamoDB (bd27e94)"
    - "email-create-handler Lambda reads metadata via DynamoDB (73526f8)"
    - "km create runCreateRemote writes 'starting' metadata to DynamoDB (199be82)"
    - "SANDBOX_TABLE_NAME env var read from environment in both Lambda binaries with km-sandboxes default"
  gaps_remaining: []
  regressions: []
human_verification:
  - test: "km create --remote <profile.yaml> shows sandbox in km list immediately"
    expected: "After the EventBridge event is published, km list shows the sandbox with status 'starting' while the create-handler Lambda provisions it. The sandbox must appear because runCreateRemote wrote 'starting' metadata to DynamoDB at Step 8b."
    why_human: "Requires a real AWS environment with km-sandboxes table provisioned. Automated checks verify the DynamoDB write call exists but cannot confirm the record actually appears in km list against live infrastructure."
  - test: "TTL expiry event extends sandbox metadata in DynamoDB (not S3)"
    expected: "When a ttl-handler extend event fires, km status shows the updated TTL. km list does not show stale TTL from S3."
    why_human: "Requires firing a live EventBridge TTL event and observing DynamoDB record update. Cannot simulate Lambda execution in unit tests."
  - test: "SANDBOX_TABLE_NAME omission from Terraform is functionally harmless"
    expected: "Lambda uses 'km-sandboxes' default when SANDBOX_TABLE_NAME env var is absent. The IAM policy hardcodes the same ARN. No runtime error."
    why_human: "The Terraform environment block for ttl-handler and email-handler omit SANDBOX_TABLE_NAME. Code defaults to 'km-sandboxes'. Verify no table-name mismatch in a deployed environment."
---

# Phase 39: Migrate Sandbox Metadata S3 to DynamoDB Verification Report

**Phase Goal:** All sandbox metadata reads/writes (km list, km status, km lock/unlock, km pause/resume, km create/destroy, and Lambda handlers) switch from S3 JSON blobs to a DynamoDB km-sandboxes table with alias-index GSI, atomic lock/unlock via ConditionExpression, DynamoDB TTL for auto-cleanup, and backward-compat S3 fallback when table does not exist — artifacts remain in S3.
**Verified:** 2026-04-01T02:57:56Z
**Status:** human_needed (all automated checks pass)
**Re-verification:** Yes — after gap closure (3 gaps from initial verification)

## Goal Achievement

### Observable Truths

| #  | Truth | Status | Evidence |
|----|-------|--------|----------|
| 1  | ReadSandboxMetadataDynamo returns structured SandboxMetadata from DynamoDB GetItem | VERIFIED | pkg/aws/sandbox_dynamo.go line 251; 13 tests pass |
| 2  | WriteSandboxMetadataDynamo stores ttl_expiry as Number (Unix epoch) for DynamoDB TTL | VERIFIED | sandbox_dynamo.go line 237: AttributeValueMemberN |
| 3  | LockSandboxDynamo uses ConditionExpression for atomic lock — no read-modify-write race | VERIFIED | sandbox_dynamo.go line 396: `attribute_exists(sandbox_id) AND (attribute_not_exists(locked) OR locked = :f)`; ConditionalCheckFailedException handling |
| 4  | UnlockSandboxDynamo uses ConditionExpression for atomic unlock | VERIFIED | sandbox_dynamo.go line 424: `attribute_exists(sandbox_id) AND locked = :t` |
| 5  | ListAllSandboxesByDynamo paginates past 1MB boundary using LastEvaluatedKey | VERIFIED | sandbox_dynamo.go line 341: pagination loop; test with 2-page mock |
| 6  | ResolveSandboxAliasDynamo queries alias-index GSI for O(1) alias resolution | VERIFIED | sandbox_dynamo.go line 356: KeyConditionExpression on alias-index GSI |
| 7  | DeleteSandboxMetadataDynamo removes a sandbox record | VERIFIED | sandbox_dynamo.go line 296; test confirms DeleteItem key |
| 8  | km-sandboxes DynamoDB table module with alias-index GSI and ttl_expiry TTL | VERIFIED | infra/modules/dynamodb-sandboxes/v1.0.0/main.tf: alias-index GSI ALL projection, ttl on ttl_expiry, PAY_PER_REQUEST |
| 9  | IAM permissions on all 3 Lambda roles (ttl-handler, email-handler, create-handler) | VERIFIED | All 3 main.tf have aws_iam_role_policy.dynamodb_sandboxes with GetItem/PutItem/UpdateItem/DeleteItem/Scan/Query on km-sandboxes and alias-index |
| 10 | Config has SandboxTableName field defaulting to km-sandboxes | VERIFIED | internal/app/config/config.go line 82-84, 151, 204, 239 |
| 11 | km list/lock/unlock/destroy/status/pause/resume/stop/extend/budget use DynamoDB | VERIFIED | All CLI commands use DynamoDB primary path with ResourceNotFoundException S3 fallback |
| 12 | km create writes metadata to DynamoDB (all 3 paths: EC2, Docker, Remote) | VERIFIED | EC2 line 536, Docker line 1209, Remote line 1499 — all call WriteSandboxMetadataDynamo |
| 13 | ttl-handler Lambda reads/writes/deletes metadata via DynamoDB | VERIFIED | cmd/ttl-handler/main.go: ReadSandboxMetadataDynamo (line 161), WriteSandboxMetadataDynamo (line 207), DeleteSandboxMetadataDynamo (line 461); dynamodbpkg.NewFromConfig init at line 623 |
| 14 | email-create-handler Lambda reads metadata via DynamoDB | VERIFIED | cmd/email-create-handler/main.go: ReadSandboxMetadataDynamo (line 249); DynamoClient initialized at line 440 |
| 15 | S3 fallback on ResourceNotFoundException for backward compat | VERIFIED | All 11 CLI commands, ttl-handler readMetadataBestEffort (line 318), email-handler pattern — S3 used only when ResourceNotFoundException received |

**Score:** 15/15 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/aws/sandbox_dynamo.go` | DynamoDB CRUD functions | VERIFIED | 460+ lines, 8 exported functions + SandboxMetadataAPI interface |
| `pkg/aws/sandbox_dynamo_test.go` | Unit tests with mock (min 200 lines) | VERIFIED | 482 lines, 13 test functions |
| `infra/modules/dynamodb-sandboxes/v1.0.0/main.tf` | DynamoDB table with GSI and TTL | VERIFIED | sandbox_id PK, alias-index GSI ALL, ttl_expiry TTL |
| `infra/live/use1/dynamodb-sandboxes/terragrunt.hcl` | Terragrunt live config | VERIFIED | Sources v1.0.0, table_name = km-sandboxes |
| `internal/app/config/config.go` | SandboxTableName config field | VERIFIED | Default km-sandboxes, viper-wired |
| `internal/app/cmd/create.go` | DynamoDB write on all 3 substrate paths | VERIFIED | EC2 line 536, Docker line 1209, Remote line 1499 |
| `internal/app/cmd/list.go` | DynamoDB scan | VERIFIED | ListAllSandboxesByDynamo + ResourceNotFoundException S3 fallback |
| `internal/app/cmd/lock.go` | Atomic DynamoDB lock | VERIFIED | LockSandboxDynamo primary path; S3 read-modify-write in named fallback function runLockS3Fallback |
| `cmd/ttl-handler/main.go` | Lambda uses DynamoDB for metadata | VERIFIED | DynamoClient + SandboxTableName fields; ReadSandboxMetadataDynamo, WriteSandboxMetadataDynamo, DeleteSandboxMetadataDynamo all wired |
| `cmd/email-create-handler/main.go` | Lambda uses DynamoDB for read | VERIFIED | handleStatus uses ReadSandboxMetadataDynamo (line 249); DynamoClient initialized |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `internal/app/cmd/create.go` | `pkg/aws/sandbox_dynamo.go` | WriteSandboxMetadataDynamo | VERIFIED | EC2 line 536, Docker line 1209, Remote line 1499 — all 3 paths wired |
| `internal/app/cmd/list.go` | `pkg/aws/sandbox_dynamo.go` | ListAllSandboxesByDynamo | VERIFIED | line 135 |
| `internal/app/cmd/lock.go` | `pkg/aws/sandbox_dynamo.go` | LockSandboxDynamo | VERIFIED | line 75 primary; runLockS3Fallback only on ResourceNotFoundException |
| `cmd/ttl-handler/main.go` | `pkg/aws/sandbox_dynamo.go` | ReadSandboxMetadataDynamo | VERIFIED | line 161 (handleExtend); line 318 (readMetadataBestEffort) |
| `cmd/ttl-handler/main.go` | `pkg/aws/sandbox_dynamo.go` | WriteSandboxMetadataDynamo | VERIFIED | line 207 (handleExtend) |
| `cmd/ttl-handler/main.go` | `pkg/aws/sandbox_dynamo.go` | DeleteSandboxMetadataDynamo | VERIFIED | line 461 (terraformDestroy) |
| `cmd/email-create-handler/main.go` | `pkg/aws/sandbox_dynamo.go` | ReadSandboxMetadataDynamo | VERIFIED | line 249 (handleStatus) |
| `infra/modules/ttl-handler/v1.0.0/main.tf` | Lambda env vars | SANDBOX_TABLE_NAME | NOTE | Env var NOT explicitly set; Lambda defaults to "km-sandboxes" when absent (line 617-620). IAM policy hardcodes same ARN. Functionally harmless but undocumented. |
| `infra/modules/email-handler/v1.0.0/main.tf` | Lambda env vars | SANDBOX_TABLE_NAME | NOTE | Same as ttl-handler: absent from env block, code defaults to "km-sandboxes". |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| META-DYNAMO-01 | 39-01 | ReadSandboxMetadataDynamo | SATISFIED | sandbox_dynamo.go line 251, 13 tests pass |
| META-DYNAMO-02 | 39-01 | WriteSandboxMetadataDynamo with Number TTL | SATISFIED | sandbox_dynamo.go line 237 AttributeValueMemberN |
| META-DYNAMO-03 | 39-01 | LockSandboxDynamo atomic ConditionExpression | SATISFIED | sandbox_dynamo.go line 396 |
| META-DYNAMO-04 | 39-01 | UnlockSandboxDynamo atomic ConditionExpression | SATISFIED | sandbox_dynamo.go line 424 |
| META-DYNAMO-05 | 39-01 | ListAllSandboxesByDynamo with pagination | SATISFIED | sandbox_dynamo.go line 341 |
| META-DYNAMO-06 | 39-01 | ResolveSandboxAliasDynamo GSI query | SATISFIED | sandbox_dynamo.go line 352 |
| META-DYNAMO-IAM | 39-02 | IAM permissions on all 3 Lambda roles | SATISFIED | All 3 main.tf have aws_iam_role_policy.dynamodb_sandboxes with full CRUD + Scan/Query |
| META-DYNAMO-INFRA | 39-02 | DynamoDB table module with correct schema | SATISFIED | dynamodb-sandboxes/v1.0.0/main.tf |
| META-DYNAMO-CONFIG | 39-02 | SandboxTableName config field | SATISFIED | config.go line 82 |
| META-DYNAMO-SWITCHOVER-CLI | 39-03 | All CLI commands use DynamoDB | SATISFIED | All 11 CLI files confirmed: create (all 3 paths), list, status, lock, unlock, destroy, stop, pause, resume, extend, budget |
| META-DYNAMO-SWITCHOVER-LAMBDA | 39-03 | Lambda handlers use DynamoDB | SATISFIED | ttl-handler: 3/3 metadata ops use DynamoDB. email-handler: 1/1 metadata op uses DynamoDB. |
| META-DYNAMO-BACKWARD-COMPAT | 39-03 | S3 fallback on ResourceNotFoundException | SATISFIED | All CLI commands and both Lambda handlers use DynamoDB primary path with S3 fallback gated on ResourceNotFoundException |

**Note on requirement IDs:** META-DYNAMO-* requirement IDs appear in ROADMAP.md and plan frontmatter but are NOT present in .planning/REQUIREMENTS.md Traceability table. This is an administrative gap introduced in Phase 39 — requirements are defined in the plans and ROADMAP, but the central registry is not updated. Not a functional gap.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `cmd/ttl-handler/main.go` | 367 | `// TODO: Read substrate from metadata.json to handle ECS sandboxes.` | INFO | Pre-existing TODO, unrelated to this phase. No functional impact. |
| `infra/modules/ttl-handler/v1.0.0/main.tf` | 259 | SANDBOX_TABLE_NAME absent from environment block | INFO | Lambda code defaults to "km-sandboxes" when env var is absent. IAM hardcodes same ARN. Not a blocker, but table name is undocumented in Terraform config. |
| `infra/modules/email-handler/v1.0.0/main.tf` | 199 | SANDBOX_TABLE_NAME absent from environment block | INFO | Same as ttl-handler: functional but undocumented. |

No blockers or warnings remain. All three original blocker anti-patterns (ReadSandboxMetadata/PutObject/DeleteSandboxMetadata in Lambda hot paths) have been eliminated.

### S3 Fallback Pattern Verification

All remaining `metadata.json` / `ReadSandboxMetadata` / `PutObject` references outside `pkg/aws/` are in properly-named S3 fallback functions or `ResourceNotFoundException` error handlers:

- `lock.go` — `runLockS3Fallback()` named function, entered only on ResourceNotFoundException
- `unlock.go` — `runUnlockS3Fallback()` named function, entered only on ResourceNotFoundException
- `extend.go` — inside `errors.As(writeErr, &rnf)` ResourceNotFoundException block
- `resume.go` — inside `errors.As(statusErr, &rnf)` ResourceNotFoundException block
- `stop.go` — inside `errors.As(metaErr, &rnf)` ResourceNotFoundException block
- `pause.go` — inside `errors.As(metaErr, &rnf)` ResourceNotFoundException block
- `destroy.go` — two fallback sites, both inside ResourceNotFoundException blocks
- `status.go` — inside `errors.As(err, &rnf)` ResourceNotFoundException block
- `budget.go` — inside `errors.As(err, &rnf)` ResourceNotFoundException block

No primary-path `metadata.json` writes or reads remain in any CLI command or Lambda handler.

### Human Verification Required

#### 1. Remote create shows in km list during provisioning

**Test:** Run `km create --remote <profile.yaml>` against a real AWS environment with km-sandboxes table provisioned. Before the create-handler Lambda completes provisioning, run `km list`.
**Expected:** The sandbox appears in `km list` with status `starting` immediately after the EventBridge event is published, because `runCreateRemote` now writes "starting" metadata to DynamoDB at Step 8b.
**Why human:** Requires live AWS environment with EventBridge, DynamoDB, and Lambda deployed. Cannot simulate Lambda event chain in unit tests.

#### 2. TTL extend updates DynamoDB record

**Test:** Trigger a TTL extend EventBridge event for a sandbox in a live environment. Check `km status <sandbox-id>` before and after.
**Expected:** `km status` shows the updated TTL expiry. The DynamoDB record — not an S3 file — is the source of truth.
**Why human:** Requires live EventBridge TTL event execution. Lambda environment cannot be simulated without deployed infrastructure.

#### 3. SANDBOX_TABLE_NAME default is sufficient in deployed Lambda

**Test:** Deploy ttl-handler and email-handler Lambda via Terraform. Confirm `SANDBOX_TABLE_NAME` env var is absent from the Lambda configuration in the AWS console. Trigger a metadata read/write and confirm no errors.
**Expected:** Lambda uses default `km-sandboxes` and successfully reads/writes DynamoDB. No "table not found" errors.
**Why human:** Confirms the env var omission from Terraform does not cause a table-name mismatch in deployed infrastructure.

### Gaps Summary

All three gaps from the initial verification have been closed:

**Gap 1 — ttl-handler Lambda (CLOSED, commit bd27e94):** `cmd/ttl-handler/main.go` now initializes a DynamoDB client at startup, reads `SANDBOX_TABLE_NAME` from env (default `km-sandboxes`), and uses `ReadSandboxMetadataDynamo` / `WriteSandboxMetadataDynamo` / `DeleteSandboxMetadataDynamo` in all three metadata call sites. The `readMetadataBestEffort` helper also reads from DynamoDB.

**Gap 2 — email-create-handler Lambda (CLOSED, commit 73526f8):** `cmd/email-create-handler/main.go` `handleStatus` now uses `ReadSandboxMetadataDynamo` instead of S3 GetObject. DynamoDB client initialized at Lambda startup.

**Gap 3 — km create --remote path (CLOSED, commit 199be82):** `runCreateRemote` Step 8b now calls `WriteSandboxMetadataDynamo` with a `dynamodbpkg.NewFromConfig(awsCfg)` client and `cfg.SandboxTableName` default. Sandboxes created via `km create --remote` will appear in `km list` immediately after the EventBridge event is published.

**Remaining note:** Neither `infra/modules/ttl-handler/v1.0.0/main.tf` nor `infra/modules/email-handler/v1.0.0/main.tf` explicitly set `SANDBOX_TABLE_NAME` in the Lambda environment block. This is an INFO-level hygiene gap — the Lambda code defaults to `km-sandboxes` and the IAM policies hardcode the same table name, so the omission does not cause a functional failure. The `create-handler` Terraform module similarly omits the env var.

---

_Verified: 2026-04-01T02:57:56Z_
_Verifier: Claude (gsd-verifier)_
