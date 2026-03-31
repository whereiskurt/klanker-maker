---
status: complete
phase: 37-docker-compose-local-substrate
source: 37-01-SUMMARY.md, 37-02-SUMMARY.md, 37-03-SUMMARY.md
started: 2026-03-31T12:38:00Z
updated: 2026-03-31T12:52:00Z
---

## Current Test

[testing complete]

## Tests

### 1. km validate accepts substrate docker
expected: `./km validate` on a profile with `substrate: docker` passes validation without errors.
result: pass

### 2. km create --substrate docker provisions sandbox
expected: `./km create profiles/goose.yaml --substrate docker` creates IAM roles, writes docker-compose.yml to `~/.km/sandboxes/{id}/`, and attempts `docker compose up -d`.
result: issue
reported: "Three bugs found during testing: (1) --substrate flag didn't exist on cobra command, (2) substrate override only set local variable not profile struct so compiler still used ec2 path, (3) compose template used hardcoded local image names instead of ECR URIs. All three fixed during UAT. After fixes: IAM roles created, compose YAML written correctly, docker compose up invoked with ECR image URIs."
severity: major

### 3. IAM policy document is valid
expected: The IAM inline policy attached to `km-docker-{id}-{region}` should be valid JSON with correct IAM actions. No "MalformedPolicyDocument" error during create.
result: issue
reported: "Original code json.Marshal'd the IAMSessionPolicy struct (MaxSessionDuration/AllowedRegions) which is NOT an IAM policy document. AWS returned MalformedPolicyDocument. Fixed by writing a proper IAM policy JSON with SSM and S3 actions scoped to the sandbox."
severity: major

### 4. STS AssumeRole succeeds after role creation
expected: After IAM role creation, STS AssumeRole should succeed (after propagation delay). No "AccessDenied" error on sts:AssumeRole.
result: issue
reported: "Original code had only a fixed 5s sleep before a single AssumeRole attempt — too short for IAM eventual consistency. Fixed with retry loop (6 attempts, 5s between). After fix, AssumeRole succeeds on attempt 3 (10-15s after role creation). Real STS creds visible in compose file."
severity: major

### 5. docker-compose.yml has correct 6-service topology
expected: The written docker-compose.yml has all 6 services: main, km-dns-proxy, km-http-proxy, km-audit-log, km-tracing, km-cred-refresh. Only km-cred-refresh has AWS_ACCESS_KEY_ID.
result: pass

### 6. docker compose up starts containers
expected: `docker compose up -d` successfully starts all 6 containers. `docker ps` shows them running.
result: skipped
reason: Sidecar images (km-dns-proxy, km-http-proxy, km-audit-log, km-tracing) don't exist in ECR yet. Only km-create-handler repo exists. Need `make ecr-repos && make ecr-push` for all sidecar images first.

### 7. km destroy cleans up docker sandbox
expected: `./km destroy {sandbox-id} --remote --yes` detects docker substrate via S3 metadata, runs `docker compose down -v`, deletes IAM roles.
result: skipped
reason: Can't test without running containers from test 6.

### 8. km shell routes to docker exec
expected: `./km shell {sandbox-id}` runs `docker exec -it km-{id}-main /bin/bash`.
result: skipped
reason: Can't test without running containers from test 6.

### 9. km stop routes to docker compose stop
expected: `./km stop {sandbox-id}` detects docker substrate, runs `docker compose stop`.
result: skipped
reason: Can't test without running containers from test 6.

### 10. km pause routes to docker compose pause
expected: `./km pause {sandbox-id}` detects docker substrate, runs `docker compose pause`.
result: skipped
reason: Can't test without running containers from test 6.

## Summary

total: 10
passed: 2
issues: 3
pending: 0
skipped: 5

## Gaps

- truth: "km create --substrate docker provisions a sandbox end-to-end"
  status: failed
  reason: "User reported: Three bugs found: (1) --substrate CLI flag missing, (2) substrate override not propagated to profile struct for compiler, (3) compose template used local image names not ECR URIs. All fixed during UAT."
  severity: major
  test: 2
  root_cause: "--substrate flag never added to Cobra command; substrate override only set local var not resolvedProfile.Spec.Runtime.Substrate; compose template hardcoded km-*:latest instead of using ECR registry from KM_ACCOUNTS_APPLICATION"
  artifacts:
    - path: "internal/app/cmd/create.go"
      issue: "missing --substrate flag, missing profile struct override"
    - path: "pkg/compiler/compose.go"
      issue: "hardcoded local image names"
  missing:
    - "Already fixed during UAT session"
  debug_session: ""

- truth: "IAM inline policy is valid AWS policy JSON"
  status: failed
  reason: "User reported: json.Marshal of IAMSessionPolicy struct produces invalid IAM policy. Fixed with proper policy JSON."
  severity: major
  test: 3
  root_cause: "artifacts.IAMPolicy is an IAMSessionPolicy struct (MaxSessionDuration/AllowedRegions) — not an IAM policy document. json.Marshal produces {MaxSessionDuration:3600,...} which AWS rejects."
  artifacts:
    - path: "internal/app/cmd/create.go"
      issue: "PutRolePolicy called with marshaled IAMSessionPolicy instead of IAM policy JSON"
  missing:
    - "Already fixed during UAT session"
  debug_session: ""

- truth: "STS AssumeRole succeeds after role creation"
  status: failed
  reason: "User reported: Fixed 5s sleep too short for IAM eventual consistency. Fixed with 6-attempt retry loop."
  severity: major
  test: 4
  root_cause: "IAM eventual consistency requires 10-30s after CreateRole before AssumeRole succeeds. Original code had single attempt after 5s fixed delay."
  artifacts:
    - path: "internal/app/cmd/create.go"
      issue: "single AssumeRole attempt after 5s sleep"
  missing:
    - "Already fixed during UAT session"
  debug_session: ""
