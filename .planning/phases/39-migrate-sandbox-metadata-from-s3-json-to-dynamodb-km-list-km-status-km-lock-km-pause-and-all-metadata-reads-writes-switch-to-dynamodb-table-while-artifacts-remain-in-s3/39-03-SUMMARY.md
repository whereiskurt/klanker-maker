# Plan 39-03 Summary

## Objective
Switch all 22 metadata call sites from S3 to DynamoDB with backward-compat S3 fallback.

## Tasks Completed
- Task 1: Switched 11 CLI command files (create, extend, pause, resume, lock, unlock, destroy, stop, list, status, budget) + supporting files (sandbox_ref, doctor, roll, rsync, shell)
- Task 2: Verified Lambda handlers build (ttl-handler, email-create-handler)

## Key Files Changed
- `internal/app/cmd/create.go` — 3 write points (EC2, Docker, Remote) now use WriteSandboxMetadataDynamo
- `internal/app/cmd/list.go` — ListAllSandboxesByDynamo replaces ListAllSandboxesByS3
- `internal/app/cmd/lock.go` — LockSandboxDynamo with atomic conditional UpdateItem
- `internal/app/cmd/unlock.go` — UnlockSandboxDynamo with atomic conditional UpdateItem
- `internal/app/cmd/destroy.go` — DeleteSandboxMetadataDynamo for both EC2 and Docker paths
- All other cmd files — ReadSandboxMetadataDynamo replaces ReadSandboxMetadata
- `internal/app/cmd/status_test.go` — Updated to accept DynamoDB error messages

## Commits
- `90efc1a`: feat(39-03): switch all metadata call sites from S3 to DynamoDB

## Verification
- `make build`: km v0.0.71 OK
- `go test ./internal/app/cmd/...`: All tests pass (except 3 pre-existing Docker test failures from Phase 37)
- `go build ./cmd/ttl-handler/... ./cmd/email-create-handler/...`: Lambda builds OK
- Both EC2 and Docker substrates use DynamoDB
