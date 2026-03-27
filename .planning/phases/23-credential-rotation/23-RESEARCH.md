# Phase 23: Credential Rotation — Research

**Researched:** 2026-03-26
**Domain:** Credential lifecycle management, AWS SSM/KMS/CloudWatch, Go crypto stdlib, SSM SendCommand, proxy CA rotation
**Confidence:** HIGH (based on deep codebase analysis of existing patterns from Phases 6, 13, 14, 15; all AWS SDK patterns verified against existing working code in the repo)

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| CRED-01 | `km roll creds` rotates all platform credentials (GitHub App private key, KMS key rotation, proxy CA, Ed25519 signing keys for all running sandboxes) | Phase 13 GitHub token patterns, Phase 14 identity patterns, init.go proxy CA generation, KMS SDK already imported |
| CRED-02 | `km roll creds --sandbox <id>` rotates secrets for a single sandbox (Ed25519 key pair, GitHub token force-refresh, SSM re-encryption) | GenerateSandboxIdentity() in identity.go, HandleTokenRefresh in token.go |
| CRED-03 | `km roll creds --platform` rotates only platform-level secrets (GitHub App, proxy CA, KMS) | ensureProxyCACert() pattern in init.go reusable for regeneration |
| CRED-04 | Audit trail — each rotation logged to CloudWatch with before/after key fingerprints | cloudwatchlogs SDK already direct dependency; slog JSON to stdout pattern from github-token-refresher |
| CRED-05 | Zero-downtime rotation — sandboxes pick up new secrets on next poll cycle; proxy CA requires SSM SendCommand restart | SSM SendCommand SDK exists but is not yet used in Go code; needs new narrow interface |
| CRED-06 | `km doctor --check-rotation` warns if any credential hasn't been rotated in >90 days | doctor.go CheckResult pattern; SSM GetParameter LastModifiedDate is the rotation timestamp source |
</phase_requirements>

---

## Summary

Phase 23 adds `km roll creds` — a top-level Cobra command that drives credential rotation for all secrets the platform manages. The codebase already contains all the building-block primitives: `GenerateSandboxIdentity()` in `pkg/aws/identity.go` generates a new Ed25519 key pair and writes it to SSM; `WriteTokenToSSM()` in `pkg/github/token.go` updates GitHub tokens; `ensureProxyCACert()` in `internal/app/cmd/init.go` generates ECDSA P-256 CA certs; and the doctor's `CheckResult` pattern provides a ready-made structure for the `--check-rotation` check. The KMS SDK (`aws-sdk-go-v2/service/kms`) is already a direct dependency imported in `doctor.go` and `bootstrap.go`.

The one missing AWS API surface is SSM SendCommand — needed to restart the proxy sidecar on running EC2 sandboxes after CA rotation. The `aws-sdk-go-v2/service/ssm` package is already a direct dependency and `*ssm.Client` implements `SendCommand` natively; only a new narrow interface needs to be defined. For ECS sandboxes the equivalent is ECS UpdateService or ECS StopTask (force a task replacement), which triggers a new task that downloads the fresh CA from S3 at startup.

KMS "rotation" in this platform context means triggering AWS managed key rotation on demand (not alias-swap, since there is no use of customer-managed CMK aliases for rotation in the current Terraform). The correct AWS KMS API call is `RotateKeyOnDemand` (AWS SDK v2 KMS client method) or the existing annual `enable_key_rotation = true` Terraform setting. Since the KMS keys already have `enable_key_rotation = true`, the `--platform` rotation for KMS should trigger an on-demand rotation via `kms.RotateKeyOnDemand`.

**Primary recommendation:** Model `km roll creds` as a Cobra command in `internal/app/cmd/roll.go` following the doctor/budget command pattern — injected DI deps struct, independent check/action functions, structured CloudWatch audit logging via `slog` JSON to stdout (same as github-token-refresher Lambda). Use `GetParametersByPath("/sandbox/")` to enumerate running sandbox IDs for bulk rotation.

---

## Standard Stack

### Core (all already in go.mod as direct dependencies)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `aws-sdk-go-v2/service/ssm` | v1.68.3 | PutParameter, GetParameter, GetParametersByPath, SendCommand | Already direct dep; all SSM operations |
| `aws-sdk-go-v2/service/kms` | v1.50.3 | DescribeKey, RotateKeyOnDemand | Already indirect dep in doctor.go/bootstrap.go; promote to direct |
| `aws-sdk-go-v2/service/cloudwatchlogs` | v1.64.1 | CreateLogGroup, CreateLogStream, PutLogEvents for audit trail | Already direct dep from sidecar/audit-log |
| `aws-sdk-go-v2/service/dynamodb` | v1.57.0 | UpdateItem for refreshing Ed25519 public keys in km-identities table | Already direct dep |
| `aws-sdk-go-v2/service/s3` | v1.97.1 | PutObject for uploading new proxy CA cert to S3 | Already direct dep |
| `crypto/ed25519` + `crypto/rand` | stdlib | Generating new Ed25519 key pairs | Already used in identity.go |
| `crypto/ecdsa` + `crypto/x509` | stdlib | Generating new proxy CA cert+key | Already used in init.go's ensureProxyCACert() |
| `log/slog` | stdlib | Structured JSON audit logging | Already used in github-token-refresher |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `aws-sdk-go-v2/service/ecs` | v1.74.0 | ListTasks, StopTask for ECS sandbox proxy restart | When sandbox substrate is ECS and proxy CA rotated |
| `aws-sdk-go-v2/service/ec2` | v1.296.0 | DescribeInstances for listing running EC2 sandboxes | When enumerating EC2 sandboxes for bulk rotation |
| `encoding/base64` | stdlib | Encode key fingerprints for audit log before/after comparison | Already used throughout identity.go |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| SSM GetParametersByPath for sandbox enumeration | S3 list of `tf-km/sandboxes/` prefixes | S3 list is the existing sandbox enumeration pattern (ListSandboxes in sandbox.go); SSM path enumeration is simpler for rotation but S3 is more authoritative for running status |
| CloudWatch PutLogEvents directly | slog JSON to stdout (Lambda captures to CW automatically) | Direct CW write is needed since `km roll creds` is a CLI command, not a Lambda; must use CW SDK explicitly |
| KMS RotateKeyOnDemand | Create new KMS key + update alias | Alias-swap changes ARN — all SSM SecureString parameters would need re-encryption with new ARN; RotateKeyOnDemand rotates key material in-place, same ARN, same alias, zero consumer impact |

**Installation (promote kms to direct dependency):**
```bash
go get github.com/aws/aws-sdk-go-v2/service/kms
```

---

## Architecture Patterns

### Recommended Project Structure

```
internal/app/cmd/
├── roll.go               # "km roll creds" Cobra command + RunE + DI struct
└── roll_test.go          # unit tests with mock AWS clients

pkg/aws/
├── rotation.go           # RotateProxyCACert(), RotateSandboxIdentity(),
│                         # UpdateIdentityPublicKey(), WriteRotationAudit()
└── rotation_test.go      # unit tests (mock SSMAPI, DynamoDB, S3, CW)
```

The `pkg/aws/rotation.go` package-level function approach matches `identity.go`, `ses.go`, and `budget.go`. The Cobra command in `roll.go` wires together the orchestration and flag parsing, following the `doctor.go` DI-deps pattern.

### Pattern 1: Roll Command DI Struct (follow doctor.go)

**What:** `RollDeps` struct holds injected AWS clients. Nil fields skip the corresponding rotation step.

**Example:**
```go
// Source: existing doctor.go DoctorDeps pattern
type RollDeps struct {
    SSMClient    RollSSMAPI      // PutParameter, GetParameter, GetParametersByPath, SendCommand
    KMSClient    RollKMSAPI      // DescribeKey, RotateKeyOnDemand
    S3Client     RollS3API       // PutObject (proxy CA upload)
    DynamoClient RollDynamoAPI   // UpdateItem (km-identities table)
    CWClient     RollCWAPI       // CreateLogGroup, CreateLogStream, PutLogEvents
    ECSClient    RollECSAPI      // ListTasks, StopTask (ECS proxy restart)
    EC2Client    RollEC2API      // DescribeInstances (EC2 sandbox enumeration)
}
```

### Pattern 2: Sandbox Enumeration for Bulk Rotation

**What:** Use `pkg/aws.ListSandboxes()` (existing pattern in `sandbox.go`) to get all running sandboxes. Filter to running status before rotating identity keys.

**Example:**
```go
// Source: internal/app/cmd/doctor.go checkSandboxSummary pattern
sandboxes, err := lister.ListSandboxes(ctx, false)
for _, s := range sandboxes {
    if s.Status == "running" {
        if err := RotateSandboxIdentity(ctx, ssmClient, dynClient, s.SandboxID, kmsKeyID, identityTableName); err != nil {
            // Log error, continue (non-fatal per-sandbox failure)
        }
    }
}
```

### Pattern 3: Ed25519 Key Rotation (update-in-place)

**What:** Generate new key pair, write private key to SSM (overwrite=true), update DynamoDB public key (PutItem replaces entire row), log fingerprints.

**Example:**
```go
// Source: pkg/aws/identity.go GenerateSandboxIdentity + PublishIdentity
// Step 1: Capture old fingerprint for audit log
oldRecord, _ := FetchPublicKey(ctx, dynClient, tableName, sandboxID)

// Step 2: Generate new key pair, store in SSM (Overwrite=true)
newPub, err := GenerateSandboxIdentity(ctx, ssmClient, sandboxID, kmsKeyID)

// Step 3: Update DynamoDB — use PutItem (replaces row, preserves alias/allowedSenders)
// NOTE: rotation must NOT use ConditionExpression attribute_not_exists — row already exists
_, err = dynClient.PutItem(ctx, &dynamodb.PutItemInput{
    TableName: aws.String(tableName),
    Item:      buildIdentityItem(sandboxID, newPub, ...),
    // No condition — intentional overwrite
})

// Step 4: Audit log
writeRotationAudit(ctx, cwClient, logGroup, "ed25519_rotation", sandboxID, oldFingerprint, newFingerprint)
```

**Critical:** `PublishIdentity()` uses `attribute_not_exists(sandbox_id)` condition — NOT suitable for rotation. Rotation must call `PutItem` without that condition.

### Pattern 4: GitHub App Private Key Rotation

**What:** Operator generates a new private key in the GitHub App settings UI, provides the new PEM path. The command reads the new PEM, validates it can generate a JWT, writes to SSM (`/km/config/github/private-key`), and logs fingerprint change.

**Note:** There is no GitHub API to programmatically regenerate a private key — the operator must do this in the GitHub UI. `km roll creds --platform` for GitHub should accept `--github-private-key-file` flag, validate the PEM, write to SSM, then verify by generating a JWT.

### Pattern 5: Proxy CA Rotation

**What:** Generate new ECDSA P-256 CA cert+key, upload both to S3 (`s3://bucket/sidecars/km-proxy-ca.crt` and `km-proxy-ca.key`), then trigger proxy restart on running sandboxes.

**Example (key generation):**
```go
// Source: internal/app/cmd/init.go ensureProxyCACert() — same code, force=true path
key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
// ... create x509 cert, PEM encode, upload to S3
```

**EC2 sandbox proxy restart via SSM SendCommand:**
```go
// Source: AWS SDK docs — no existing use in codebase
// aws-sdk-go-v2/service/ssm SendCommand
input := &ssm.SendCommandInput{
    InstanceIds:  []string{instanceID},
    DocumentName: aws.String("AWS-RunShellScript"),
    Parameters: map[string][]string{
        "commands": {
            "aws s3 cp s3://${bucket}/sidecars/km-proxy-ca.crt /usr/local/share/ca-certificates/km-proxy-ca.crt",
            "update-ca-certificates",
            "systemctl restart km-http-proxy",
        },
    },
}
```

**ECS sandbox proxy restart (force task replacement):**
For ECS, there is no `systemctl`. The proxy CA is fetched at task startup from S3 (per the userdata.go ECS startup pattern). The rotation approach is: update the S3 cert, then optionally force a new task deployment. Since ECS tasks are ephemeral, the new CA will be picked up on next task start. For "zero-downtime" this means the old task continues with the old CA until its natural replacement. If immediate restart is needed, `ecs.StopTask` on the running tasks forces replacement.

### Pattern 6: SSM Re-encryption

**What:** When `--sandbox <id>` is used, re-encrypt all sandbox SSM parameters under `/sandbox/<id>/` with the current KMS key. This is a read + re-write operation.

```go
// List all parameters for sandbox
out, err := ssmClient.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
    Path:           aws.String("/sandbox/" + sandboxID + "/"),
    WithDecryption: aws.Bool(true),
    Recursive:      aws.Bool(true),
})
// Re-write each as SecureString with current KMS key ARN
```

### Pattern 7: Rotation Age Check (km doctor --check-rotation)

**What:** SSM `GetParameter` returns `LastModifiedDate` timestamp for each parameter. Compare `time.Since(LastModifiedDate)` against 90-day threshold. Check both platform params (`/km/config/github/*`) and a sample of sandbox params.

```go
// Source: SSM GetParameter response — LastModifiedDate is in ssmtypes.Parameter
out, _ := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
    Name: aws.String("/km/config/github/private-key"),
})
age := time.Since(aws.ToTime(out.Parameter.LastModifiedDate))
if age > 90*24*time.Hour {
    return CheckResult{Status: CheckWarn, Message: fmt.Sprintf("GitHub private key not rotated in %d days", int(age.Hours()/24))}
}
```

### Anti-Patterns to Avoid

- **Using PublishIdentity() for rotation:** It uses `attribute_not_exists` condition — will silently no-op if row exists. Rotation must use unconditional PutItem.
- **Blocking the entire rotation on one sandbox failure:** Per-sandbox rotation errors must be logged and continued (non-fatal). Global rotation should report a summary of failures, not abort.
- **Deleting then recreating SSM parameters:** Use `Overwrite: true` in `PutParameter`. Delete-create creates a race window.
- **Trying to rotate KMS key by creating a new CMK and swapping alias:** RotateKeyOnDemand rotates the key material in-place, preserving the same ARN. Alias-swap would require re-encrypting all SecureString parameters.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Ed25519 key generation | Custom key generation logic | `crypto/ed25519.GenerateKey()` already in identity.go | One-liner; stdlib; already battle-tested in Phase 14 |
| Proxy CA generation | New cert library | `crypto/ecdsa` + `crypto/x509` already in init.go's `ensureProxyCACert()` | Exact code already exists; extract into pkg/aws/rotation.go |
| Sandbox enumeration | Tag-based EC2 query | `pkg/aws.ListSandboxes()` (sandbox.go) | Already handles S3-based listing + metadata join |
| Rotation age check | Custom timestamp storage | SSM `Parameter.LastModifiedDate` | AWS tracks this natively; no additional metadata needed |
| CloudWatch log group creation | Manual CW setup | `cloudwatchlogs.CreateLogGroup` with `ResourceAlreadyExistsException` swallowing | Same pattern as audit-log sidecar |

---

## Common Pitfalls

### Pitfall 1: PublishIdentity ConditionExpression Blocks Rotation

**What goes wrong:** Calling `PublishIdentity()` for key rotation silently succeeds (returns nil) but does NOT update DynamoDB because the condition `attribute_not_exists(sandbox_id)` is already met.
**Why it happens:** Phase 14 designed `PublishIdentity` for idempotent initial creation.
**How to avoid:** Write a new `UpdateIdentityPublicKey()` function that uses unconditional `PutItem` (or `UpdateItem`). Never reuse `PublishIdentity` for rotation.
**Warning signs:** After calling rotation, `FetchPublicKey()` returns the old public key.

### Pitfall 2: KMS RotateKeyOnDemand Not Available in All Regions/Tiers

**What goes wrong:** `RotateKeyOnDemand` was added in 2023 for symmetric CMKs. The platform KMS keys use `ENCRYPT_DECRYPT` purpose with `SYMMETRIC_DEFAULT` spec — this is supported. Asymmetric keys do NOT support on-demand rotation.
**Why it happens:** Confusion between symmetric and asymmetric key rotation APIs.
**How to avoid:** The secrets module and github-token module both use `key_usage = "ENCRYPT_DECRYPT"` (symmetric default) — RotateKeyOnDemand is valid. Verify with `DescribeKey` before calling.
**Warning signs:** SDK error `UnsupportedOperationException: OnDemandRotationNotSupported`.

### Pitfall 3: SSM SendCommand Returns Before Command Completes

**What goes wrong:** `SendCommand` returns a `CommandId` immediately — it does NOT wait for the shell script to finish on the EC2 instance.
**Why it happens:** SSM Run Command is async.
**How to avoid:** Either poll `GetCommandInvocation` until status is `Success/Failed`, or (simpler) fire-and-forget with a log message that restart was initiated. Since proxy restart is non-critical path for zero-downtime (old proxy keeps working until next poll), fire-and-forget is acceptable for v1.
**Warning signs:** Rotation appears to succeed but proxies don't restart because the command is still pending.

### Pitfall 4: GitHub Private Key Cannot Be Programmatically Regenerated

**What goes wrong:** Assuming the GitHub API supports generating new private keys for an existing App.
**Why it happens:** GitHub's REST API does not have an endpoint to generate new private keys programmatically.
**How to avoid:** `km roll creds --platform` for GitHub must require `--github-private-key-file` flag pointing to a new PEM file the operator has already downloaded from the GitHub App settings UI. The command validates the PEM, verifies it can generate a working JWT, then writes to SSM.
**Warning signs:** Attempting `POST /app/keys` to regenerate returns 404 or permission errors.

### Pitfall 5: ECS Proxy CA Rotation Requires Task Restart, Not Graceful Reload

**What goes wrong:** Assuming `systemctl reload km-http-proxy` picks up the new CA cert without restart.
**Why it happens:** On EC2, proxy processes can reload config; on ECS tasks, the CA cert is loaded at container startup from S3 — there is no live reload path.
**How to avoid:** For ECS, rotation is eventually consistent: new CA cert uploaded to S3; next task deployment (from spot interruption, scale-in/out, or explicit `StopTask`) picks it up. If immediate rotation is required, use `ecs.StopTask` to force task replacement.
**Warning signs:** ECS containers continue using old CA after rotation — this is expected behavior, not a bug.

### Pitfall 6: CloudWatch Log Group May Not Exist

**What goes wrong:** `PutLogEvents` fails with `ResourceNotFoundException` if the log group `/km/credential-rotation` doesn't exist.
**Why it happens:** Unlike Lambda which auto-creates log groups, CLI commands must create them explicitly.
**How to avoid:** Create log group with `cloudwatchlogs.CreateLogGroup` at rotation start; swallow `ResourceAlreadyExistsException` (same pattern as audit-log sidecar).

---

## Code Examples

### Narrow Interface for SSM SendCommand

```go
// Source: aws-sdk-go-v2/service/ssm API — new narrow interface following doctor.go pattern
// This interface does NOT yet exist in the codebase; must be created in roll.go

type RollSSMAPI interface {
    PutParameter(ctx context.Context, params *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
    GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
    GetParametersByPath(ctx context.Context, params *ssm.GetParametersByPathInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
    SendCommand(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
    DeleteParameter(ctx context.Context, params *ssm.DeleteParameterInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error)
}
// *ssm.Client satisfies this interface directly.
```

### KMS RotateKeyOnDemand Interface

```go
// Source: aws-sdk-go-v2/service/kms — following bootstrap.go KMSEnsureAPI pattern
type RollKMSAPI interface {
    DescribeKey(ctx context.Context, params *kms.DescribeKeyInput, optFns ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
    RotateKeyOnDemand(ctx context.Context, params *kms.RotateKeyOnDemandInput, optFns ...func(*kms.Options)) (*kms.RotateKeyOnDemandOutput, error)
}
```

### Key Fingerprint for Audit Log

```go
// Compute a compact fingerprint from an Ed25519 public key for before/after audit comparison.
// Source: encoding/base64 stdlib, consistent with identity.go representation
func ed25519Fingerprint(pub ed25519.PublicKey) string {
    // SHA-256 hash of the public key bytes, first 8 bytes as hex — short unique identifier
    h := sha256.Sum256(pub)
    return fmt.Sprintf("sha256:%x", h[:8])
}
```

### CloudWatch Rotation Audit Event

```go
// Source: github-token-refresher slog JSON to stdout pattern adapted for direct CW write
// Each rotation event written as a structured JSON log entry.
type RotationAuditEvent struct {
    Event       string `json:"event"`        // "ed25519_rotation", "proxy_ca_rotation", etc.
    SandboxID   string `json:"sandbox_id,omitempty"`
    KeyType     string `json:"key_type"`
    BeforeFP    string `json:"before_fingerprint"`
    AfterFP     string `json:"after_fingerprint"`
    Timestamp   string `json:"timestamp"`
    Success     bool   `json:"success"`
    Error       string `json:"error,omitempty"`
}
```

### doctor --check-rotation Check Structure

```go
// Source: doctor.go CheckResult pattern — add new check function following same pattern
func checkCredentialRotationAge(ctx context.Context, ssmClient SSMReadAPI, thresholdDays int) CheckResult {
    params := []string{
        "/km/config/github/private-key",
        "/km/config/github/app-client-id",
    }
    var stale []string
    for _, p := range params {
        out, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(p)})
        if err != nil { continue }  // Missing params handled by checkGitHubConfig
        if out.Parameter.LastModifiedDate != nil {
            age := time.Since(aws.ToTime(out.Parameter.LastModifiedDate))
            if age > time.Duration(thresholdDays)*24*time.Hour {
                stale = append(stale, fmt.Sprintf("%s (%dd)", p, int(age.Hours()/24)))
            }
        }
    }
    if len(stale) > 0 {
        return CheckResult{Status: CheckWarn, Message: "stale credentials: " + strings.Join(stale, ", "),
            Remediation: fmt.Sprintf("Run 'km roll creds --platform' to rotate platform credentials")}
    }
    return CheckResult{Status: CheckOK, Message: fmt.Sprintf("all platform credentials rotated within %d days", thresholdDays)}
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Manual SSM parameter update | `km roll creds` one-command rotation | Phase 23 | Operators no longer need to remember SSM paths or AWS console |
| Annual KMS rotation only | `RotateKeyOnDemand` on-demand trigger | AWS 2023 | Can rotate KMS material any time, not just annually |
| Static proxy CA (5-year cert) | Rotatable CA via `km roll creds --platform` | Phase 23 | Allows timely response to CA compromise events |

**Deprecated/outdated:**
- `ensureProxyCACert()` in init.go: Currently idempotent (skip if already in S3). The rotation version must be force-regenerate — extract shared cert generation logic into `pkg/aws/rotation.go` rather than calling init's function directly.

---

## Open Questions

1. **GitHub App private key rotation workflow**
   - What we know: GitHub has no API to programmatically generate new private keys
   - What's unclear: Should `km roll creds --platform` require the operator to provide the new PEM file, or should it guide them to do so interactively?
   - Recommendation: Accept `--github-private-key-file` flag for non-interactive use; if omitted in interactive mode, print step-by-step instructions and prompt for the file path.

2. **CloudWatch log group for rotation audit**
   - What we know: Rotation events need a named log group; existing code uses `/km/sandbox/{id}/audit` for per-sandbox logs
   - What's unclear: Should rotation audit go to a dedicated `/km/credential-rotation` log group or to the existing platform log group?
   - Recommendation: Use `/km/credential-rotation` as a dedicated group — rotation is a platform operation, not sandbox-scoped.

3. **ECS proxy restart scope**
   - What we know: ECS task containers load CA from S3 at startup; there is no live reload
   - What's unclear: Should `km roll creds --platform` actively stop running ECS tasks to force CA pickup, or document that ECS tasks will pick up new CA on next natural restart?
   - Recommendation: Document eventually-consistent behavior; add `--force-restart` flag to `km roll creds` that triggers `ecs.StopTask` for immediate rotation when needed.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing + testify (existing in repo) |
| Config file | none (standard `go test ./...`) |
| Quick run command | `go test ./internal/app/cmd/ -run TestRoll -v` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements to Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| CRED-01 | `km roll creds` invokes all platform + sandbox rotation steps | unit | `go test ./internal/app/cmd/ -run TestRollCredsAll -v` | No — Wave 0 |
| CRED-02 | `km roll creds --sandbox <id>` rotates Ed25519 key + GitHub token + SSM re-encrypt for one sandbox | unit | `go test ./internal/app/cmd/ -run TestRollCredsSandbox -v` | No — Wave 0 |
| CRED-03 | `km roll creds --platform` rotates GitHub App key + proxy CA + KMS only | unit | `go test ./internal/app/cmd/ -run TestRollCredsplatform -v` | No — Wave 0 |
| CRED-04 | Rotation events written to CloudWatch with before/after fingerprints | unit | `go test ./pkg/aws/ -run TestWriteRotationAudit -v` | No — Wave 0 |
| CRED-05 | SSM SendCommand issued for EC2 sandbox proxy restart; ECS StopTask called for ECS sandboxes | unit (mock clients) | `go test ./internal/app/cmd/ -run TestRollCredsRestart -v` | No — Wave 0 |
| CRED-06 | `km doctor --check-rotation` returns WARN when LastModifiedDate > 90 days | unit | `go test ./internal/app/cmd/ -run TestCheckCredentialRotationAge -v` | No — Wave 0 |

### Sampling Rate

- **Per task commit:** `go test ./internal/app/cmd/ -run TestRoll -v && go test ./pkg/aws/ -run TestRotation -v`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `internal/app/cmd/roll_test.go` — covers CRED-01, CRED-02, CRED-03, CRED-05
- [ ] `pkg/aws/rotation_test.go` — covers CRED-04 (audit), CRED-02 (identity update)
- [ ] `internal/app/cmd/doctor_test.go` additions — covers CRED-06 (check-rotation case)

---

## Sources

### Primary (HIGH confidence — codebase verified)

- `/Users/khundeck/working/klankrmkr/pkg/aws/identity.go` — Ed25519 key generation, SSM storage, DynamoDB publishing patterns
- `/Users/khundeck/working/klankrmkr/pkg/github/token.go` — GitHub JWT generation, token refresh, SSM write patterns
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/init.go` (lines 572-651) — `ensureProxyCACert()` ECDSA P-256 generation and S3 upload
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/doctor.go` — `CheckResult` pattern, DI deps struct, `runChecks` parallel execution
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/bootstrap.go` — `KMSEnsureAPI` narrow interface pattern for KMS
- `/Users/khundeck/working/klankrmkr/go.mod` — dependency versions confirmed

### Secondary (MEDIUM confidence — AWS SDK v2 API surface)

- AWS SDK v2 KMS `RotateKeyOnDemand` — available for symmetric CMKs since 2023; confirmed supported for `ENCRYPT_DECRYPT` purpose keys
- AWS SSM `SendCommand` with `AWS-RunShellScript` document — standard pattern for EC2 remote command execution; available in `*ssm.Client`
- SSM `Parameter.LastModifiedDate` field — populated on every `GetParameter` response; accurate rotation age source

### Tertiary (LOW confidence — verify during implementation)

- ECS `StopTask` behavior for proxy restart — expected to trigger task replacement but actual behavior depends on service desired count and capacity provider settings

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries are existing direct dependencies; no new packages except kms promotion from indirect
- Architecture: HIGH — all patterns replicated from existing working code in the repo
- Pitfalls: HIGH — derived from direct reading of existing code (PublishIdentity condition, init.go proxy CA pattern, SendCommand async behavior)

**Research date:** 2026-03-26
**Valid until:** 2026-06-26 (stable AWS SDK surface; 90 days)
