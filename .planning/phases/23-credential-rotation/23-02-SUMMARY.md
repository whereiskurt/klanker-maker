---
phase: 23-credential-rotation
plan: "02"
subsystem: internal/app/cmd
tags: [rotation, cobra, cli, tdd, ssm, kms, ecs, ec2, cloudwatch, di-deps]
dependency_graph:
  requires:
    - pkg/aws/rotation.go (RotateSandboxIdentity, RotateProxyCACert, ReEncryptSSMParameters, WriteRotationAudit)
    - pkg/aws/identity.go (IdentitySSMAPI, IdentityTableAPI, RotationSSMAPI)
    - pkg/aws/sandbox.go (SandboxRecord)
    - pkg/github/token.go (GenerateGitHubAppJWT)
    - internal/app/cmd/list.go (SandboxLister interface)
  provides:
    - internal/app/cmd/roll.go (NewRollCmd, NewRollCmdWithDeps, RollDeps)
  affects:
    - internal/app/cmd/root.go (km root command registers NewRollCmd)
tech_stack:
  added: []
  patterns:
    - TDD (RED-GREEN with 9 failing tests before implementation)
    - DI deps pattern (RollDeps following DoctorDeps)
    - Narrow interfaces (RollSSMAPI embeds RotationSSMAPI + adds SendCommand)
    - Fire-and-forget proxy restart (SSM SendCommand / ECS StopTask)
key_files:
  created:
    - internal/app/cmd/roll.go
    - internal/app/cmd/roll_test.go
  modified:
    - internal/app/cmd/root.go
decisions:
  - RollSSMAPI embeds kmaws.RotationSSMAPI and adds SendCommand so the rotation library functions work directly without interface conversion
  - Per-sandbox failures are collected in a failures slice and non-fatal; platform failures abort with error
  - Platform-mode lister has err field in test to verify it is NOT called (lister returns error if invoked)
  - restartProxiesForSandboxes accepts nil sandboxes and re-enumerates in platform-only mode, passes pre-enumerated list in all-mode to avoid double listing
  - KMS key alias/km-platform used as keyID (KMS API accepts alias names directly)
  - rollSSMAdapter wraps *ssm.Client as compile-time satisfier for RollSSMAPI (no runtime overhead)
metrics:
  duration: 412s
  completed_date: "2026-03-27"
  tasks_completed: 2
  files_created: 2
  files_modified: 1
---

# Phase 23 Plan 02: km roll creds Command Summary

Operator-facing `km roll creds` Cobra command wiring the rotation library from Plan 01 with mode orchestration (all/sandbox/platform), EC2 SSM SendCommand + ECS StopTask proxy restart, and per-sandbox failure resilience.

## What Was Built

`internal/app/cmd/roll.go` provides the `km roll` command with the `creds` subcommand:

- **RollDeps struct** — DI pattern from doctor.go: SSMClient, KMSClient, S3Client, DynamoClient, CWClient, ECSClient, EC2Client, Lister. All nil fields auto-initialize real AWS clients via `initRealRollDeps`.
- **Narrow interfaces** — RollSSMAPI (embeds RotationSSMAPI + adds SendCommand), RollKMSAPI (DescribeKey + RotateKeyOnDemand), RollECSAPI (ListTasks + StopTask), RollEC2API (DescribeInstances).
- **All mode** (no flags, CRED-01): `rotatePlatform` (proxy CA + KMS) → `ListSandboxes` → per-sandbox `rotateSandbox` → `restartProxiesForSandboxes`. Platform errors fatal; per-sandbox errors non-fatal with summary.
- **Sandbox mode** (`--sandbox <id>`, CRED-02): `RotateSandboxIdentity` + `ReEncryptSSMParameters` only; no platform rotation; no lister call.
- **Platform mode** (`--platform`, CRED-03): `RotateProxyCACert` + `RotateKeyOnDemand` (KMS describe-first) + optional GitHub App key; no sandbox enumeration.
- **GitHub PEM validation**: reads file, decodes PEM block, validates via `GenerateGitHubAppJWT`, writes to SSM `/km/config/github/private-key` (Overwrite=true). Skipped with info message if `--github-private-key-file` not provided.
- **Proxy restart** (`restartProxiesForSandboxes`, CRED-05): EC2 substrates get SSM SendCommand with `AWS-RunShellScript` + systemctl restart; ECS substrates get StopTask if `--force-restart`, otherwise eventual-consistency info log. Fire-and-forget (no wait for completion).
- **Audit events**: `WriteRotationAudit` called after every step (proxy CA, KMS, sandbox identity, SSM re-encrypt).
- **Summary output**: human-readable `N credentials rotated, M failures` or `--json` structured output.

## Tests

`internal/app/cmd/roll_test.go` — 9 tests with mock AWS clients (660 lines):

| Test | Verifies |
|------|----------|
| TestRollCreds_AllMode | S3 PutObject x2, KMS RotateKeyOnDemand x1, DynamoDB x2, CW events |
| TestRollCreds_SandboxMode | DynamoDB PutItem, no S3 calls, no KMS, CW events |
| TestRollCreds_PlatformMode | S3 x2, KMS x1, no DynamoDB, CW events; lister error if called |
| TestRollCreds_PlatformMode_SkipsGitHubKeyWhenNotProvided | No SSM write to /km/config/github/private-key |
| TestRollCreds_PerSandboxFailureIsNonFatal | 3 sandboxes, 1 failing; no error returned; failure in output |
| TestRollCreds_EC2ProxyRestart | SSM SendCommand issued for EC2 substrate sandbox |
| TestRollCreds_ECSProxyRestart_ForceRestart | ECS StopTask called with --force-restart |
| TestRollCreds_AuditEventsWritten | At least 3 CW events for all-mode run |
| TestRollCreds_HelpShowsAllFlags | All 5 flags registered on creds subcommand |

## Deviations from Plan

None — plan executed exactly as written.

## Verification

```
go test ./internal/app/cmd/ -run TestRoll -v -count=1
# PASS: 9/9 tests

go build ./cmd/km/ && ./km roll creds --help
# Shows all flags: --sandbox, --platform, --github-private-key-file, --force-restart, --json

go vet ./internal/app/cmd/
# clean (no output)
```

## Self-Check: PASSED
