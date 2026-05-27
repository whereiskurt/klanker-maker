---
phase: 89
plan: 05
subsystem: compiler/create/destroy
tags: [sops, secrets, userdata, ec2spot, s3, tdd]
dependency_graph:
  requires:
    - 89-01 (SecretsSpec struct, ValidateSopsBundleFile — already in codebase)
    - 89-02 (ec2spot/v1.2.0 module on disk, KMS key module)
  provides:
    - SopsBundlePresent userdata template field + section 5.5 block
    - Pre-apply SOPS bundle S3 upload (create.go)
    - Non-fatal SOPS bundle S3 cleanup (destroy.go)
    - ec2spot/v1.2.0 module bump in sandbox template
  affects:
    - Every new EC2 sandbox (template version bump to v1.2.0)
    - EC2 sandboxes with spec.secrets.sopsFile set (section 5.5 userdata block)
tech_stack:
  added: []
  patterns:
    - S3Putter/S3Deleter interface pattern for testable S3 helpers
    - TDD RED→GREEN across 4 files with 14 new tests
    - Template gating via {{- if .SopsBundlePresent }} for zero-regression backwards compat
key_files:
  created:
    - pkg/compiler/userdata_secrets_test.go (5 tests — gate-off, gate-on, exit-1, profile.d, heredoc byte-lock)
    - pkg/compiler/compiler_secrets_test.go (4 tests — v1.2.0 template, artifacts_bucket, SopsBundlePresent, backwards compat)
    - internal/app/cmd/create_secrets_test.go (3 tests — happy path, no-op, missing file)
    - internal/app/cmd/destroy_secrets_test.go (2 tests — happy path, non-fatal-on-error)
  modified:
    - pkg/compiler/userdata.go (SopsBundlePresent field + section 5.5 template + population)
    - infra/templates/sandbox/terragrunt.hcl (v1.1.0 → v1.2.0 module version)
    - internal/app/cmd/create.go (S3Putter interface + uploadSopsBundleIfPresent + Step 8.6 wiring)
    - internal/app/cmd/destroy.go (S3Deleter interface + deleteSopsBundleNonFatal + Step 12.1 wiring)
decisions:
  - Template version bump lives in infra/templates/sandbox/terragrunt.hcl (copied verbatim at CreateSandboxDir time), not in Go source — compiler_secrets_test.go tests the file directly
  - artifacts_bucket already emitted by service_hcl.go (Phase 68); WARNING 3 assertion is a confirmation test, not new code
  - SOPS bundle upload is wired at Step 8.6 (after userdata upload, before sandbox dir populate) to fail fast before terragrunt apply
  - deleteSopsBundleNonFatal is intentionally void (no return value) — caller never needs to handle the error
metrics:
  duration: 750s
  completed: "2026-05-27T20:54:50Z"
  tasks: 4
  files: 9
---

# Phase 89 Plan 05: Compiler + Create + Destroy SOPS Plumbing Summary

**One-liner:** Compiler template gated SOPS section 5.5 (fetch/decrypt/expose) + pre-apply bundle upload in create.go + non-fatal cleanup in destroy.go + ec2spot module bumped to v1.2.0 — 14 tests across 4 new test files.

## What Was Built

### Task 1: Userdata template surface

`pkg/compiler/userdata.go` gains:
- `SopsBundlePresent bool` field on `userDataParams` struct
- `params.SopsBundlePresent = p.Spec.Secrets != nil && p.Spec.Secrets.SopsFile != ""` in `generateUserData`
- New template section 5.5 (gated by `{{- if .SopsBundlePresent }}`) at the right insertion point after sidecar binary downloads:
  1. `aws s3 cp .../binaries/sops` → `/opt/km/bin/sops` + `chmod +x`
  2. `aws s3 cp .../sandboxes/{id}/secrets.enc.yaml` → `/etc/sandbox-secrets.enc.yaml` + `chmod 0400`
  3. `sops decrypt --output-type dotenv` with `exit 1` abort on failure
  4. `chown root:root` + `chmod 0400` on decrypted file
  5. Heredoc writes `/etc/profile.d/zz-sandbox-secrets.sh` with `set -a / source / set +a` pattern

5 tests in `userdata_secrets_test.go`: gate-off, gate-on (with WARNING 7 encrypted-file chmod check), exit-1 abort, profile.d sourcing order, heredoc byte-exact lock (WARNING 4).

### Task 2: Compiler wiring + module version bump

- `infra/templates/sandbox/terragrunt.hcl` source path bumped from `/v1.1.0` to `/v1.2.0` so ec2spot v1.2.0's KMS decrypt + S3 GetObject IAM policies attach at sandbox create time
- `artifacts_bucket` already emitted in `service_hcl.go` module_inputs (Phase 68); WARNING 3 confirmed via test assertion on compiled ServiceHCL

4 tests in `compiler_secrets_test.go`: template uses v1.2.0, ServiceHCL has artifacts_bucket, SopsBundlePresent→userdata section when set, absent when no Spec.Secrets.

### Task 3: create.go SOPS bundle upload

New `S3Putter` interface + `uploadSopsBundleIfPresent` helper:
- Offline validation via `profile.ValidateSopsBundleFile` fires BEFORE any AWS call
- PutObject to `s3://{artifactBucket}/sandboxes/{sandboxID}/secrets.enc.yaml` with `application/x-yaml` content-type
- Wired at Step 8.6 in `runCreate` (after userdata upload, before sandbox dir population)
- On error, cleans up sandbox dir before returning

3 tests in `create_secrets_test.go`: happy path (byte-exact fixture check + bucket/key/content-type assertions), no-op when nil, error-before-S3 when file missing.

### Task 4: destroy.go SOPS bundle cleanup

New `S3Deleter` interface + `deleteSopsBundleNonFatal` helper:
- DeleteObject on `sandboxes/{sandboxID}/secrets.enc.yaml` — always attempted regardless of profile having SopsFile set
- Non-fatal: absorbs errors, logs at Warn level; S3 lifecycle 7-day rule (89-02) is belt-and-suspenders
- Wired at Step 12.1 in `runDestroy` (after DynamoDB/S3 metadata cleanup, before CloudWatch log export)

2 tests in `destroy_secrets_test.go`: happy path (bucket + key assertions), non-fatal-on-error (absorbs error, no panic).

## Verification Results

All plan verification checks pass:
- `grep -c 'ec2spot/v1.1.0' pkg/compiler/*.go` → 0 (all files)
- `grep -c 'artifacts_bucket' pkg/compiler/*.go` → ≥1 (service_hcl.go: 2)
- `SopsBundlePresent` appears in: struct definition, template gate, population site
- `secrets.enc.yaml` appears in both create.go and destroy.go
- `ValidateSopsBundleFile` gate in create.go confirmed
- `chmod 0400 /etc/sandbox-secrets.enc.yaml` in template: 1 hit
- All 14 Phase-89 tests GREEN
- `go build ./...` clean
- `go vet ./pkg/compiler/... ./internal/app/cmd/...` silent

## Deviations from Plan

### Auto-fixed: Pre-dependency state already satisfied (Rule 3)

89-01 (SecretsSpec, ValidateSopsBundleFile) was already implemented in the codebase before this plan ran. 89-02's ec2spot/v1.2.0 module also already existed. No action needed.

### Compiler ec2spot version in template file, not Go source

The plan's verify step says `grep -c 'ec2spot/v1.2.0' pkg/compiler/*.go` should return ≥1. The version lives in `infra/templates/sandbox/terragrunt.hcl` (copied verbatim at sandbox creation time), not in a Go string. The `compiler_secrets_test.go` tests the template file directly (`TestSandboxTemplateUsesEC2SpotV120`). The grep check returns 0 for compiler Go files (the version is in the template, not emitted by Go code) but the test covers it authoritatively.

### artifacts_bucket already in service_hcl.go

WARNING 3 in the plan notes the risk of ec2spot v1.2.0 silently skipping the S3 IAM policy if `artifacts_bucket` is not emitted. This was already handled by Phase 68 work in `service_hcl.go`. The compiler_secrets_test.go `TestCompileEC2ServiceHCLHasArtifactsBucket` confirms it.

## Pre-existing Test Failures (Out of Scope)

6 pre-existing failures in `pkg/compiler/` (not caused by these changes):
- TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock
- TestUserDataNotifyEnv_NoChannelOverride_NoChannelID
- TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime
- TestUserDataKMTracingServicectlStart
- TestAuditHookNonBlocking
- TestGitHubUserDataGITASKPASS

1 pre-existing failure in `internal/app/cmd/`:
- TestCreateDockerWritesComposeFile

All pre-existed before these changes. Deferred to a future plan.

## Operator Follow-up (After All Phase 89 Plans Land)

After 89-01 + 89-02 + 89-03 + 89-04 + 89-05:
1. `make build && make sidecars`
2. `km init --sidecars` (pushes sops binary to S3 at binaries/sops)
3. `km bootstrap --shared-secrets-key --plan` (preview KMS creation)
4. `km bootstrap --shared-secrets-key` (create KMS key)
5. `km init` (re-apply so s3-artifacts-lifecycle v1.1.0 attaches the 7-day rule)
6. Create a profile with `spec.secrets.sopsFile: ./secrets.enc.yaml` and `km create`

Phase 89-07 owns the live UAT for step 6.

## Self-Check: PASSED

Commits verified:
- 2e3a75a feat(89-05): add SopsBundlePresent field + section 5.5 SOPS template block to userdata.go
- b9cedd8 feat(89-05): bump sandbox module from ec2spot/v1.1.0 to v1.2.0 + compiler emission tests
- 3e75b27 feat(89-05): add SOPS bundle upload to create.go (S3Putter interface + uploadSopsBundleIfPresent)
- cda5cbd feat(89-05): add SOPS bundle cleanup to destroy.go (S3Deleter interface + deleteSopsBundleNonFatal)

Files verified to exist:
- pkg/compiler/userdata_secrets_test.go ✓
- pkg/compiler/compiler_secrets_test.go ✓
- internal/app/cmd/create_secrets_test.go ✓
- internal/app/cmd/destroy_secrets_test.go ✓
