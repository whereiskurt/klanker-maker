---
title: Phase 70 follow-up — km destroy should clean up km-identities DDB row
area: km-cli
created: 2026-05-24
origin: Phase 70 SC-4 UAT 2026-05-24 (signing-key blocker root cause)
---

### Problem
During Phase 70 UAT we discovered that destroyed sandboxes leave **stale rows in `km-identities`** with their old alias + pubkey. When a new sandbox is created with the SAME alias (e.g., `learncodex` reused after destroying the AMI-baked one), the bridge's alias-based identity lookup hits the stale dead row → returns wrong pubkey → all signed requests from the new sandbox fail with `401 bad_signature`.

The workaround during UAT was manually deleting the stale row:
```bash
aws dynamodb delete-item --table-name km-identities --key '{"sandbox_id":{"S":"<destroyed-sandbox-id>"}}'
```

This should be part of the `km destroy` flow.

### Affected lookup path
`pkg/aws/identity.go:FetchPublicKey` is keyed by `sandbox_id` so it's fine. The trouble is when the bridge or another caller looks up by **alias**:
- `pkg/aws/identity.go:FetchPublicKeyByAlias` returns the FIRST match — if a destroyed sandbox left a row with the same alias, that wins.

### Fix
In `internal/app/cmd/destroy.go`, after a successful sandbox teardown, delete the corresponding `km-identities` row:

```go
identityClient := dynamodb.NewFromConfig(awsCfg)
_, _ = identityClient.DeleteItem(ctx, &dynamodb.DeleteItemInput{
    TableName: aws.String(cfg.IdentityTableName),
    Key: map[string]types.AttributeValue{
        "sandbox_id": &types.AttributeValueMemberS{Value: sandboxID},
    },
})
```

Add to the existing destroy cleanup (alongside KMS key, SQS queue, S3 prefix, etc.). Make it best-effort (`_, _ = ...`) so it doesn't fail the destroy on race conditions.

### Tests
- Add a destroy test that asserts km-identities GetItem returns empty after destroy.
- Doctor check: a separate validation that no orphan km-identities rows exist for destroyed sandbox IDs (look up sandbox_id in km-sandboxes; if not found, the row is orphan).

### Verification
After fix: create + destroy + recreate a sandbox with the same alias should NOT produce 401 bad_signature; the new sandbox's signed posts should succeed on the first attempt.

### Files
- `internal/app/cmd/destroy.go` (add identity cleanup step)
- `internal/app/cmd/destroy_test.go` (assertion)
- Optionally `internal/app/cmd/doctor.go` (new check for orphan identities)

### Resolution (2026-05-24)
The local-destroy path in `internal/app/cmd/destroy.go` already called `awspkg.CleanupSandboxIdentity` (line 442 EC2, line 697 Docker). The bug was that **`km destroy` defaults to `--remote=true`** and dispatches to the TTL handler Lambda (`cmd/ttl-handler/main.go`), which had no identity cleanup at all — so the alias-reuse 401 was an artifact of remote destroys.

Fix landed in `cmd/ttl-handler/main.go`:
- New `cleanupSandboxIdentity` + `cleanupSandboxIdentityWith` helpers wrapping `awspkg.CleanupSandboxIdentity`.
- Called from `terraformDestroy` (after DDB metadata delete) and from `sdkOnlyTeardown` (after metadata delete). Best-effort / non-fatal — never blocks destroy.
- New `IdentityTable` field on `TTLHandler` + `identitiesTable()` env-var helper (`KM_IDENTITIES_TABLE`, fallback `<prefix>-identities`).

Terraform plumbing in `infra/modules/ttl-handler/v1.0.0`:
- `KM_IDENTITIES_TABLE` env var on the Lambda.
- New `identities_table_name` variable (default `km-identities`).
- New IAM policy `${prefix}-ttl-handler-identity-cleanup` granting `dynamodb:DeleteItem` on the identities table and `ssm:DeleteParameter` on `/${prefix}/sandbox/*/{signing-key,encryption-key,safe-phrase}`.
- Live wiring in `infra/live/use1/ttl-handler/terragrunt.hcl`.

Tests in `cmd/ttl-handler/identity_cleanup_test.go` cover: the three SSM params + DDB row are deleted with the right keys; nil clients skip silently; env-var fallback uses `<resource_prefix>-identities`.

**Deploy:** `km init --sidecars` to rebuild the TTL Lambda zip + `km init` (or a targeted `terragrunt apply` on `live/use1/ttl-handler`) to push the new IAM policy and `KM_IDENTITIES_TABLE` env var.

Doctor orphan-row check deferred (not blocking).
