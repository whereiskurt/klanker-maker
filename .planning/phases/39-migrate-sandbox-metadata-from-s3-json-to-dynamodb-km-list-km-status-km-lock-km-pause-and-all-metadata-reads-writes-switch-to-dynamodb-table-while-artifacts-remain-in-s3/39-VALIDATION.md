---
phase: 39
slug: migrate-sandbox-metadata-s3-to-dynamodb
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-31
---

# Phase 39 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — standard Go testing |
| **Quick run command** | `go test ./pkg/aws/... ./internal/app/cmd/... -run "DynamoDB\|Dynamo\|MetadataDynamo" -count=1` |
| **Full suite command** | `go test ./pkg/aws/... ./internal/app/cmd/... -count=1` |
| **Estimated runtime** | ~10 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick command
- **After every plan wave:** Run full suite command
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 10 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 39-01-01 | 01 | 1 | dynamo-crud | unit | `go test ./pkg/aws/... -run "DynamoDB" -count=1` | ❌ W0 | ⬜ pending |
| 39-01-02 | 01 | 1 | dynamo-list | unit | `go test ./pkg/aws/... -run "DynamoList" -count=1` | ❌ W0 | ⬜ pending |
| 39-01-03 | 01 | 1 | dynamo-lock | unit | `go test ./pkg/aws/... -run "DynamoLock" -count=1` | ❌ W0 | ⬜ pending |
| 39-02-01 | 02 | 2 | terraform-table | integration | `terraform fmt -check` | ✅ | ⬜ pending |
| 39-02-02 | 02 | 2 | iam-perms | integration | `terraform validate` | ✅ | ⬜ pending |
| 39-03-01 | 03 | 3 | cli-switchover | unit | `go test ./internal/app/cmd/... -count=1` | ✅ | ⬜ pending |
| 39-03-02 | 03 | 3 | lambda-switchover | unit | `go build ./cmd/ttl-handler/... ./cmd/email-create-handler/...` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/aws/sandbox_dynamo.go` — DynamoDB CRUD functions
- [ ] `pkg/aws/sandbox_dynamo_test.go` — unit tests with fake DynamoDB client

*Existing Go test infrastructure covers all phase requirements.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| DynamoDB table created by km init | terraform-table | Requires real AWS | Run `km init`, verify table in AWS console |
| km list reads from DynamoDB | cli-switchover | Requires running sandboxes | Create sandbox, `km list`, verify output |
| Lock is atomic (no race) | dynamo-lock | Requires concurrent writes | Two terminals: `km lock` simultaneously |
| Backward compat: S3 fallback | fallback | Requires env without table | Unset table name, verify S3 path works |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 10s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
