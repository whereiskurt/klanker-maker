# Phase 4: Lifecycle Hardening, Artifacts & Email - Research

**Researched:** 2026-03-22
**Domain:** Linux mount namespaces, AWS S3 multipart upload, EC2 IMDS spot interruption, SES send/receive, audit-log redaction decorator
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Filesystem Enforcement**
- EC2: `mount --bind -o ro` in user-data.sh at boot — kernel-native, no extra binaries, uses existing root access in bootstrap script
- ECS: `readonlyRootFilesystem: true` on the main container in the ECS task definition; `writablePaths` from the profile are mounted as tmpfs volumes (or EFS if persistence needed). Enforced by the container runtime.
- Write attempts to read-only paths return EROFS — OS-level error, not application-level. No special interception needed; error surfaces in agent stderr which the audit-log sidecar already captures.
- No dedicated `filesystem_violation` audit event type — EROFS in stderr is sufficient. Keeps the audit-log sidecar simple.

**Artifact Upload**
- Profile schema gets a new `spec.artifacts` section: `paths` (list of glob/dir strings to upload), `maxSizeMB` (per-file size limit), `replicationRegion` (optional secondary AWS region for S3 replication)
- Upload trigger: exit-only — called by the teardown flow (`pkg/lifecycle/teardown.go`) before Terraform destroy. Same trigger for TTL expiry, idle timeout, and `km destroy`.
- Oversized files: skip the file, emit a `skipped_artifact` warning event in the audit log with filename and size, continue uploading remaining files. No hard failure.
- S3 path for artifacts: `s3://km-sandbox-artifacts-ea554771/artifacts/{sandbox-id}/`
- Multi-region replication: S3 bucket replication rule added by the Terraform module when `replicationRegion` is set in the profile. No SDK-level copy logic.

**Spot Interruption Handling**
- Handler lives in user-data.sh as a background poll loop: check `http://169.254.169.254/latest/meta-data/spot/termination-time` every 5 seconds
- On detection: run the same artifact upload script used at normal exit, then allow the instance to terminate naturally (no forced kill)
- Reuses existing upload logic — no fifth sidecar binary
- ECS Fargate spot: handled via ECS task state change event (EventBridge rule watching for `TASK_STOPPING` with `stopCode: SpotInterruption`) → triggers artifact upload Lambda before task is reclaimed

**Secret Redaction**
- Redaction happens at audit-log capture time, before any write to CloudWatch or S3 — nothing sensitive leaves the sandbox in log form
- Secret identification: two layers
  1. SSM parameter values from the profile (fetched at sidecar startup via the sandbox IAM role)
  2. Regex pattern library: AWS access key prefix (`AKIA[A-Z0-9]{16}`), JWT Bearer tokens (`Bearer [A-Za-z0-9\-._~+/]+=*`), hex strings ≥40 chars (`[0-9a-f]{40,}`)
- Replacement: `[REDACTED]` — literal string, grep-friendly, obvious in logs
- Redaction applies to the `detail` field of AuditEvent — not to structural fields like sandbox_id, event_type, timestamp

**Email Architecture**
- Per-sandbox address format: `{sandbox-id}@sandboxes.klankermaker.ai`
- Uses `sandboxes.klankermaker.ai` subdomain — separate MX record from any future human mail on root domain
- Sandbox ID is directly parseable from the address
- Sending from sandboxes: SES API via IAM role — `ses:SendEmail` permission scoped to the sandbox's own address as the `From` address. Agent uses AWS SDK or SES SMTP endpoint (`email-smtp.<region>.amazonaws.com:587`). No relay sidecar.
- Inbound email for cross-sandbox orchestration: SES receipt rule stores to S3 at `km-sandbox-artifacts-ea554771/mail/{sandbox-id}/inbox/`. Agent polls S3 for new objects using `ListObjectsV2` with a prefix. Simple, no Lambda, no SQS per sandbox.
- Operator lifecycle notifications: SES sends email on all four events: TTL expiry/destroyed, idle timeout triggered, spot interruption detected, error/abnormal exit.
- Operator email address: configurable via `KM_OPERATOR_EMAIL` env var

### Claude's Discretion
- Exact regex patterns in the redaction library (beyond the three discussed)
- Artifact upload Go implementation (S3 multipart vs simple PutObject, concurrency)
- SES domain verification and DKIM setup details (Route53 DNS records)
- ECS Fargate spot interruption Lambda implementation details
- How `spec.artifacts` section integrates with profile inheritance (child overrides parent, same semantics as other sections)
- Operator notification email template/formatting

### Deferred Ideas (OUT OF SCOPE)
None — discussion stayed within phase scope
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| OBSV-04 | Filesystem policy enforces writable and read-only paths | EC2: bind mount pattern; ECS: readonlyRootFilesystem + tmpfs volumes. Both in locked decisions. |
| OBSV-05 | Artifacts upload to S3 on sandbox exit with configurable size limits | `pkg/aws/artifacts.go` (new), hook in `pkg/lifecycle/teardown.go` before Terraform destroy. `ArtifactsSpec` added to profile schema. |
| OBSV-06 | S3 artifact storage supports multi-region replication | Terraform S3 replication rule in existing artifact bucket module; triggered by `replicationRegion` in profile. |
| OBSV-07 | Secret patterns are redacted from audit logs before storage | `RedactingDestination` decorator in `sidecars/audit-log/auditlog.go`; wraps existing CloudWatch/S3 destinations. |
| PROV-13 | Sandbox handles spot interruption gracefully — uploads artifacts to S3 before termination when possible | EC2: poll loop in user-data.sh template; ECS: EventBridge → Lambda. |
| MAIL-01 | SES is configured globally with Route53 domain verification | Terraform module for SES domain identity + DKIM DNS records in `infra/modules/ses/`. |
| MAIL-02 | Each sandbox agent gets its own email address | `ses:CreateEmailIdentity` at `km create` time; address format `{sandbox-id}@sandboxes.klankermaker.ai`. |
| MAIL-03 | Agents inside sandboxes can send email via SES | IAM policy on sandbox role: `ses:SendEmail` scoped to `From` address. Exposed via env var `KM_EMAIL_ADDRESS`. |
| MAIL-04 | Operator receives email notifications for sandbox lifecycle events | `pkg/aws/ses.go` (new) called from teardown/scheduler paths; uses `ses:SendEmail` with `KM_OPERATOR_EMAIL`. |
| MAIL-05 | Cross-account agent orchestration is possible via email | SES receipt rule → S3 `mail/{sandbox-id}/inbox/`; agent polls with `ListObjectsV2`. |
</phase_requirements>

---

## Summary

Phase 4 spans four distinct engineering tracks: (1) OS-level filesystem enforcement through bind mounts and ECS container isolation primitives, (2) artifact preservation on sandbox exit and on spot interruption via S3 upload hooks, (3) secret scrubbing in the audit-log pipeline via a decorator pattern, and (4) a full email layer built on SES with per-sandbox identities, inbound S3 routing, and operator notifications. Each track has clear integration points in the existing codebase that were already designed with these extensions in mind.

The existing codebase is well-prepared: `FilesystemPolicy` struct is in `types.go`, `Destination` interface in `auditlog.go` was explicitly designed for a wrapping decorator, `ExecuteTeardown` in `teardown.go` accepts injected callbacks where an artifact upload step slots in, and `pkg/aws/` already holds S3, CloudWatch, and scheduler helpers using the narrow-interface pattern. The SES work is net-new (no existing SES helpers) but follows the same pattern as `scheduler.go` and `mlflow.go`.

The ECS Fargate spot interruption path requires a Lambda function and an EventBridge rule — both are Terraform resources (no new Go binary). The EC2 spot path is bash in the user-data template. The S3 multi-region replication is purely a Terraform configuration in the artifact bucket module, not Go code.

**Primary recommendation:** Implement in five logical groups matching the five feature areas, starting with the profile schema extension (`ArtifactsSpec`) since every other track either reads from it (artifact upload, filesystem enforcement) or follows a parallel pattern (email, redaction).

---

## Standard Stack

### Core (already in go.mod)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/s3` | v1.97.1 | S3 PutObject, ListObjectsV2 for artifacts and inbox polling | Already in go.mod; multipart upload is in same package |
| `github.com/aws/aws-sdk-go-v2/service/sesv2` | needs adding | Send email, create email identities, configure receipt rules | AWS SDK v2 standard; sesv2 (v2 API) preferred over ses (v1 API) |
| `github.com/rs/zerolog` | v1.33.0 | Structured logging throughout — redaction logs warnings | Already in use everywhere |
| `regexp` | stdlib | Regex compilation for secret redaction pattern library | No external library needed; patterns are fixed at sidecar startup |

### Supporting (already in go.mod)

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/aws/aws-sdk-go-v2/service/lambda` | needs adding | Invoke artifact upload Lambda for ECS Fargate spot path | ECS substrate spot interruption only |
| `github.com/aws/aws-sdk-go-v2/service/eventbridge` | needs adding | Register EventBridge rule for ECS Fargate spot interruption | ECS substrate only; Terraform handles rule creation, Go may query |
| `path/filepath` | stdlib | Glob expansion for artifact paths in `spec.artifacts.paths` | Glob pattern matching for artifact discovery |
| `io/fs` | stdlib | Walk filesystem directories for artifact collection | Phase 4 artifact collector |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `sesv2` | `ses` (v1 SDK) | sesv2 is the current API; ses v1 is legacy. sesv2 required for newer features like `CreateEmailIdentity`. Use sesv2. |
| Simple S3 PutObject for uploads | S3 multipart upload | Multipart needed for files > 5 GB (S3 limit). For sandbox artifacts, files are typically small. Use simple PutObject with a 100 MB threshold: PutObject below threshold, multipart above. Keeps code simple for common case. |
| Polling IMDS from Go | Polling IMDS from bash in user-data | The spot poll loop runs inside the sandbox as a background process with no access to Go tooling. Bash polling is the correct approach for EC2. |

**Installation — new SDK packages to add:**

```bash
go get github.com/aws/aws-sdk-go-v2/service/sesv2
go get github.com/aws/aws-sdk-go-v2/service/lambda
```

---

## Architecture Patterns

### Recommended Project Structure (new files this phase)

```
pkg/
├── profile/
│   └── types.go              # Add ArtifactsSpec struct + Artifacts field to Spec
├── aws/
│   ├── artifacts.go          # S3 artifact uploader (UploadArtifacts function)
│   ├── artifacts_test.go
│   ├── ses.go                # SES helpers: CreateIdentity, SendNotification, ProvisionInboxRule
│   └── ses_test.go
├── lifecycle/
│   └── teardown.go           # Add ArtifactUploader callback to TeardownCallbacks
sidecars/
└── audit-log/
    ├── auditlog.go           # Add RedactingDestination type
    └── audit_log_test.go     # Add redaction tests
pkg/compiler/
├── userdata.go               # Add bind mount block + spot poll loop sections
└── service_hcl.go            # Add readonlyRootFilesystem + tmpfs volumes + SES IAM grant
infra/modules/
└── ses/
    └── v1.0.0/
        ├── main.tf           # SES domain identity, DKIM, receipt rule set
        ├── variables.tf
        └── outputs.tf
internal/app/cmd/
└── create.go                 # Add SES identity provisioning, output email address
```

### Pattern 1: RedactingDestination Decorator

**What:** A `Destination` implementation that wraps any other `Destination`. On `Write()`, it scans each JSON-marshaled `detail` map value for secret patterns and replaces matches with `[REDACTED]` before forwarding to the inner destination.

**When to use:** Instantiated at audit-log sidecar startup when `REDACT_SECRETS=true` (or always in production). Transparent to callers — they still call `Process(ctx, r, dest)`.

**Example:**

```go
// Source: design from 04-CONTEXT.md; pattern from existing auditlog.go Destination interface

type RedactingDestination struct {
    inner    Destination
    patterns []*regexp.Regexp
    literals []string // SSM secret values fetched at startup
}

func NewRedactingDestination(inner Destination, literals []string) *RedactingDestination {
    return &RedactingDestination{
        inner:    inner,
        patterns: compileDefaultPatterns(),
        literals: literals,
    }
}

func (d *RedactingDestination) Write(ctx context.Context, event AuditEvent) error {
    event.Detail = redactMap(event.Detail, d.patterns, d.literals)
    return d.inner.Write(ctx, event)
}

func (d *RedactingDestination) Flush(ctx context.Context) error {
    return d.inner.Flush(ctx)
}
```

**Key insight for redaction:** The `detail` field is `map[string]interface{}` — values can be nested maps or string slices. Redaction must walk the map recursively, converting values to string for pattern matching and back. Non-string values (numbers, booleans) pass through without modification.

### Pattern 2: ArtifactUploader Callback in TeardownCallbacks

**What:** Extend `TeardownCallbacks` with an `UploadArtifacts` function field. `ExecuteTeardown` calls it before dispatching to `Destroy` or `Stop`. The upload is best-effort: failures log an error but do not block teardown.

**When to use:** Called for all teardown policies where artifact upload is configured in the profile. The `retain` policy still triggers upload (data preservation), but no destroy follows.

**Example:**

```go
// Extends pkg/lifecycle/teardown.go
type TeardownCallbacks struct {
    Destroy         func(ctx context.Context, sandboxID string) error
    Stop            func(ctx context.Context, sandboxID string) error
    UploadArtifacts func(ctx context.Context, sandboxID string) error // nil = skip
}
```

### Pattern 3: ArtifactsSpec in Profile Schema

**What:** New top-level section in `Spec` (alongside `Policy`, `Agent`, etc.):

```go
// In pkg/profile/types.go
type ArtifactsSpec struct {
    Paths             []string `yaml:"paths"`                       // glob/dir patterns
    MaxSizeMB         int      `yaml:"maxSizeMB"`                   // per-file limit; 0 = unlimited
    ReplicationRegion string   `yaml:"replicationRegion,omitempty"` // secondary AWS region
}
```

Added to `Spec` as `Artifacts ArtifactsSpec`. Inheritance semantics: child completely replaces parent's `artifacts` section (same as all other sections — no additive merge per Phase 1 decisions).

### Pattern 4: SES Identity Provisioning at km create

**What:** After `terragrunt apply` succeeds, `km create` calls `pkg/aws/ses.go`'s `ProvisionSandboxEmail(ctx, sesClient, sandboxID, domain)`. This calls `sesv2.CreateEmailIdentity` for the per-sandbox address. The address is output to the operator.

**When to use:** Only when `spec.agent` section is present or when email is enabled (Phase 4 provisioning always creates the address — it costs nothing if unused).

**Example:**

```go
// pkg/aws/ses.go
type SESV2API interface {
    CreateEmailIdentity(ctx context.Context, input *sesv2.CreateEmailIdentityInput, ...) (*sesv2.CreateEmailIdentityOutput, error)
    SendEmail(ctx context.Context, input *sesv2.SendEmailInput, ...) (*sesv2.SendEmailOutput, error)
}

func ProvisionSandboxEmail(ctx context.Context, client SESV2API, sandboxID, domain string) (string, error) {
    address := sandboxID + "@" + domain
    _, err := client.CreateEmailIdentity(ctx, &sesv2.CreateEmailIdentityInput{
        EmailIdentity: aws.String(address),
    })
    // ...
    return address, err
}
```

### Pattern 5: EC2 Spot Poll Loop in user-data.sh

**What:** Background bash loop that checks IMDS `spot/termination-time` every 5 seconds. Uses IMDSv2 token (already obtained at boot). On detection, calls the artifact upload script synchronously before allowing natural termination.

**Placement in userdata.go template:** After sidecar startup (section 5), before the `SANDBOX_READY` signal (section 7). The loop runs with `&` (background) so boot completes normally.

**Critical detail:** IMDSv2 token has a TTL (currently set to 60 seconds in the template). The poll loop must refresh the token every iteration, or use a TTL longer than the poll interval. Recommended: set token TTL to 21600 (6 hours) at boot, store in `IMDS_TOKEN` env var, refresh token in the loop when close to expiry.

```bash
# Section 6: Spot interruption poll loop (background)
(
  while true; do
    SPOT_ACTION=$(curl -sf \
      -H "X-aws-ec2-metadata-token: $IMDS_TOKEN" \
      "http://169.254.169.254/latest/meta-data/spot/termination-time" 2>/dev/null || echo "")
    if [ -n "$SPOT_ACTION" ]; then
      echo "[km-spot] Spot interruption detected at $SPOT_ACTION — uploading artifacts"
      /opt/km/bin/km-upload-artifacts || true
      echo "[km-spot] Artifact upload complete — allowing natural termination"
      break
    fi
    sleep 5
  done
) &
```

### Anti-Patterns to Avoid

- **Redacting structural fields:** Only redact `detail` map values. Never touch `sandbox_id`, `event_type`, `timestamp`, `source` — these are needed for log querying and correlation.
- **Blocking teardown on artifact upload failure:** Upload is best-effort. Always call `Destroy` even if upload returns an error. Log the error; don't propagate it to block cleanup.
- **Using SES v1 API (`service/ses`):** The v1 API is legacy. Use `service/sesv2` for all SES operations including `CreateEmailIdentity`, `SendEmail`, and receipt rule configuration.
- **Writing S3 replication logic in Go:** S3 replication is configured in Terraform (bucket replication rules), not in Go. The Go uploader calls `PutObject` once to the primary bucket; Terraform handles replication automatically.
- **Compiling regex patterns on every log line:** Compile patterns once at `RedactingDestination` construction and cache the `[]*regexp.Regexp` slice. The `regexp.MustCompile` result is safe for concurrent use.
- **Using ECS `readonlyRootFilesystem` without tmpfs for /tmp:** Containers crash without writable `/tmp`. The `WritablePaths` from the profile must always include `/tmp` as a minimum, or the compiler must inject it automatically if not present.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| S3 multi-region replication | SDK-level cross-region copy in Go | Terraform S3 replication rule (`aws_s3_bucket_replication_configuration`) | Replication handles eventual consistency, retries, versioning, delete markers — not a simple copy |
| DKIM key generation for SES | Custom RSA key generation | `aws_ses_domain_dkim` Terraform resource or `sesv2.CreateEmailIdentity` (SES manages DKIM automatically when using SES-managed keys) | SES manages key rotation and DNS record provisioning |
| Email address routing per sandbox | Custom SMTP server or Lambda per inbox | SES receipt rule with S3 action — route on `{sandbox-id}@domain` prefix match | SES receipt rules have wildcard matching on recipient; single rule set handles all sandboxes |
| Regex for secret redaction | Custom secret detection | The three locked patterns + stdlib `regexp` | AWS keys have a well-defined format; over-engineering detection creates false positives |
| Artifact path glob expansion | Custom glob walker | stdlib `path/filepath.Glob` + `filepath.Walk` | Handles symlinks, permissions errors, nested directories correctly |

**Key insight:** The Terraform layer is the right place for infrastructure-level configurations (replication, DKIM, receipt rules). Go code handles runtime behavior (upload, send, redact). Don't push infrastructure config into Go SDK calls.

---

## Common Pitfalls

### Pitfall 1: EC2 Bind Mount Ordering — Mounts After Agent Starts

**What goes wrong:** If the sandbox agent process starts before the bind mounts are applied in user-data.sh (e.g., if the agent is launched by a systemd service that starts before the bind mount block executes), the agent sees the original writable filesystem.

**Why it happens:** user-data.sh runs sequentially, but systemd unit files created in section 5 can start services in the background. If the agent's systemd service has `After=network.target` only, it may start before user-data finishes.

**How to avoid:** Apply bind mounts in user-data.sh before any `systemctl start` calls. The bind mount block should be section 2.5 (after IMDSv2 check, before sidecar install). The `SANDBOX_READY` signal at the end acts as a gate — nothing launches the agent until user-data completes.

**Warning signs:** Agent can write to paths listed in `readOnlyPaths`; EROFS errors never appear in audit logs.

### Pitfall 2: ECS tmpfs Volume Mount Path Conflicts

**What goes wrong:** The ECS task definition adds tmpfs volumes for each path in `writablePaths`. If two writable paths share a common prefix (e.g., `/var/run` and `/var/run/docker.sock`), or if a writable path overlaps with a sidecar's working directory, the tmpfs mount can shadow the sidecar path.

**Why it happens:** Fargate tmpfs volumes are mounted at the container level. An overly broad `writablePaths` entry (e.g., `/var`) shadows all subdirectories including sidecar-required paths.

**How to avoid:** The compiler should validate `writablePaths` entries: warn if any entry is a prefix of another, reject if an entry overlaps with `/proc`, `/sys`, `/dev`, or known sidecar paths. Always include `/tmp` in the minimal writable set.

**Warning signs:** Sidecar containers (dns-proxy, http-proxy) fail to start; container logs show "read-only file system" from sidecar processes.

### Pitfall 3: IMDS Token Expiry in Spot Poll Loop

**What goes wrong:** The EC2 user-data template currently sets `X-aws-ec2-metadata-token-ttl-seconds: 60`. The spot poll loop runs every 5 seconds indefinitely. After 60 seconds, the token expires and all IMDS calls in the poll loop return 401, causing the loop to silently stop detecting interruptions.

**Why it happens:** IMDSv2 tokens are time-limited. The existing template fetches a token at boot with a 60-second TTL and doesn't refresh it.

**How to avoid:** Change the boot-time token TTL to 21600 (6 hours) — the maximum allowed. The poll loop should also have its own refresh logic: re-fetch the token every N iterations (or every 30 minutes) to handle very long-lived sandboxes.

**Warning signs:** Spot interruption test (manually calling IMDS endpoint) shows no response; artifacts not uploaded after spot termination.

### Pitfall 4: SES Sandbox Mode — Email Not Delivered

**What goes wrong:** New AWS accounts start with SES in "sandbox mode." In sandbox mode, you can only send email to verified addresses and from verified addresses. Operator notifications to unverified email addresses silently fail or bounce.

**Why it happens:** SES sandbox mode is the default for new accounts and requires a support request to exit.

**How to avoid:** The Terraform `ses` module should check if the account is out of sandbox mode (no Terraform resource for this — it's a manual support request). Document this as a pre-requisite in the km init checklist. `km create` should warn if `KM_OPERATOR_EMAIL` is set but SES is still in sandbox mode (detectable via `sesv2.GetAccount` API — check `SendingEnabled` and `SendQuota`).

**Warning signs:** `km create` completes but operator never receives TTL/expiry notifications; no bounce notifications visible either.

### Pitfall 5: SES Receipt Rule — No Default Rule Set

**What goes wrong:** SES receipt rules require an active rule set. AWS accounts do not have a default active rule set. If the `ses` Terraform module creates rules but doesn't also create and activate a rule set, inbound email is silently dropped.

**Why it happens:** SES requires exactly one "active" rule set per region. `aws_ses_active_receipt_rule_set` must be applied as a separate resource.

**How to avoid:** The `infra/modules/ses/` Terraform module must create `aws_ses_receipt_rule_set` and `aws_ses_active_receipt_rule_set` as part of global SES setup (MAIL-01). Per-sandbox receipt rules reference the global rule set.

**Warning signs:** Agent sends email to another sandbox address; `ListObjectsV2` on the inbox prefix returns empty; no objects arrive in `mail/{sandbox-id}/inbox/`.

### Pitfall 6: Secret Redaction — JSON Encoding of detail Values

**What goes wrong:** The `AuditEvent.Detail` field is `map[string]interface{}`. When a secret appears as a nested map value (e.g., `detail["env"]["MY_TOKEN"] = "secret"`), shallow string replacement on the top-level map values misses it.

**Why it happens:** The audit log `detail` is a free-form JSON object. Sidecars (dns-proxy, http-proxy) may log environment context, command arguments, or HTTP headers that contain secrets at any nesting depth.

**How to avoid:** The `redactMap` function must recursively walk values. For strings, apply patterns. For `map[string]interface{}`, recurse. For `[]interface{}`, iterate and recurse. For other types (numbers, booleans), pass through unchanged.

**Warning signs:** `grep AKIA /var/log/km-audit.log` returns matches after redaction was applied; SSM parameter value appears in CloudWatch log events.

---

## Code Examples

Verified patterns from the existing codebase and AWS SDK v2:

### RedactingDestination — Wraps Any Destination

```go
// sidecars/audit-log/auditlog.go — follows established Destination interface pattern

type RedactingDestination struct {
    inner    Destination
    patterns []*regexp.Regexp
    literals []string
}

func compileDefaultPatterns() []*regexp.Regexp {
    raw := []string{
        `AKIA[A-Z0-9]{16}`,           // AWS access key ID
        `Bearer [A-Za-z0-9\-._~+/]+=*`, // JWT Bearer token
        `[0-9a-f]{40,}`,               // hex strings >=40 chars (git SHAs, API tokens)
    }
    compiled := make([]*regexp.Regexp, len(raw))
    for i, p := range raw {
        compiled[i] = regexp.MustCompile(p)
    }
    return compiled
}

func redactString(s string, patterns []*regexp.Regexp, literals []string) string {
    for _, lit := range literals {
        if lit != "" {
            s = strings.ReplaceAll(s, lit, "[REDACTED]")
        }
    }
    for _, re := range patterns {
        s = re.ReplaceAllString(s, "[REDACTED]")
    }
    return s
}
```

### S3 Artifact Uploader — Simple PutObject with Size Check

```go
// pkg/aws/artifacts.go — follows narrow-interface pattern from sandbox.go

type S3PutAPI interface {
    PutObject(ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

type ArtifactSkippedEvent struct {
    Path   string
    SizeMB float64
    Reason string
}

func UploadArtifacts(ctx context.Context, client S3PutAPI, bucket, sandboxID string, paths []string, maxSizeMB int) ([]ArtifactSkippedEvent, error) {
    // Expand globs, walk directories, upload each file
    // Skip files > maxSizeMB, return ArtifactSkippedEvent for each
    // S3 key: artifacts/{sandboxID}/{relative-path}
}
```

### ECS Task Definition — readonlyRootFilesystem with tmpfs

```hcl
# In ECS container definition (service_hcl.go template)
# readonlyRootFilesystem enforces OBSV-04 for ECS substrate
{
  name                 = "main"
  image                = "..."
  readonlyRootFilesystem = true
  mountPoints = [
    { sourceVolume = "tmpfs-tmp", containerPath = "/tmp", readOnly = false },
    # Additional writablePaths from profile injected here
  ]
}
volumes = [
  { name = "tmpfs-tmp", dockerVolumeConfiguration = null }
]
# For Fargate, tmpfs is declared in the task definition's ephemeralStorage or
# via the volume type. Fargate does NOT support Linux tmpfs options directly;
# instead, use task-level ephemeralStorage or add empty volumes with bind mounts.
```

**IMPORTANT FARGATE CAVEAT (HIGH confidence, verified via AWS docs):** Fargate does not support `tmpfs` in the same way EC2 ECS does. On Fargate, the `readonlyRootFilesystem: true` flag + ephemeral writable directories must use the `dockerVolumeConfiguration` with `scope: task` (anonymous volumes), or the writable paths must be declared as named volumes in the task definition that map to the container. The simple `tmpfs` mount type in `linuxParameters.tmpfs` is not supported on Fargate. Use named volumes with `scope: task` and `autoprovision: true` instead.

### SES Email Identity Provisioning

```go
// pkg/aws/ses.go
import sesv2 "github.com/aws/aws-sdk-go-v2/service/sesv2"

func ProvisionSandboxEmail(ctx context.Context, client SESV2API, sandboxID, domain string) (string, error) {
    address := sandboxID + "@" + domain
    _, err := client.CreateEmailIdentity(ctx, &sesv2.CreateEmailIdentityInput{
        EmailIdentity: aws.String(address),
    })
    if err != nil {
        return "", fmt.Errorf("create SES email identity %s: %w", address, err)
    }
    return address, nil
}
```

### SES Operator Notification

```go
func SendLifecycleNotification(ctx context.Context, client SESV2API, operatorEmail, sandboxID, event, fromAddr string) error {
    _, err := client.SendEmail(ctx, &sesv2.SendEmailInput{
        FromEmailAddress: aws.String(fromAddr), // e.g. notifications@sandboxes.klankermaker.ai
        Destination: &sesv2types.Destination{
            ToAddresses: []string{operatorEmail},
        },
        Content: &sesv2types.EmailContent{
            Simple: &sesv2types.Message{
                Subject: &sesv2types.Content{Data: aws.String("km sandbox " + event + ": " + sandboxID)},
                Body: &sesv2types.Body{
                    Text: &sesv2types.Content{Data: aws.String("Sandbox " + sandboxID + " lifecycle event: " + event)},
                },
            },
        },
    })
    return err
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| SES v1 API (`service/ses`) | SES v2 API (`service/sesv2`) | AWS SDK v2 launch | `CreateEmailIdentity` only exists in v2; v1 uses `VerifyEmailIdentity` which is deprecated for new accounts |
| ECS tmpfs via `linuxParameters.tmpfs` | Named volumes with `scope: task` for Fargate writable paths | Fargate always | Fargate never supported `linuxParameters.tmpfs`; this is a common documentation confusion |
| Polling SQS for inbound email delivery | SES receipt rule → S3 → `ListObjectsV2` poll | Simpler architecture decision | SQS adds per-sandbox queue provisioning complexity; S3 polling is stateless and simpler |

**Deprecated/outdated:**
- `ses.VerifyEmailIdentity`: replaced by `sesv2.CreateEmailIdentity`. Do not use the v1 API.
- EC2 instance metadata v1 (IMDSv1 token-less requests): already blocked in the ec2spot module (`http_tokens = "required"`). The bash spot poll loop must use IMDSv2 token header.

---

## Open Questions

1. **ECS Fargate Spot — Lambda artifact upload implementation scope**
   - What we know: EventBridge rule triggers Lambda on `TASK_STOPPING` with `stopCode: SpotInterruption`. Lambda must have IAM permission to read from the ECS task's S3 artifact paths.
   - What's unclear: The Lambda needs to know the sandbox's artifact paths (from the profile). This requires either: (a) storing artifact config in DynamoDB/SSM at provision time, or (b) tagging the ECS task with serialized artifact config (limited to 256 chars per tag value).
   - Recommendation: Store `artifacts` config in SSM at `km create` time (`/km/sandboxes/{sandbox-id}/artifacts-config`). Lambda reads this SSM key. Simple, fits existing SSM patterns.

2. **SES `sandboxes.klankermaker.ai` subdomain DKIM verification**
   - What we know: Domain `klankermaker.ai` and Route53 hosted zone exist (Phase 1). SES requires DNS records for domain verification and DKIM.
   - What's unclear: Whether the subdomain `sandboxes.klankermaker.ai` requires its own NS delegation or can use the parent hosted zone's DKIM records. Subdomain addresses can be verified at the parent domain level in SES.
   - Recommendation: Verify the subdomain `sandboxes.klankermaker.ai` in SES using the parent Route53 hosted zone. Add CNAME records for DKIM tokens (`xxx._domainkey.sandboxes.klankermaker.ai`). No separate hosted zone needed for the subdomain.

3. **Artifact upload from within EC2 sandbox — IAM permissions**
   - What we know: The sandbox IAM role currently allows SSM, CloudWatch, and S3 state bucket access (from Phase 2/3 Terraform modules).
   - What's unclear: Whether `s3:PutObject` to `km-sandbox-artifacts-ea554771/artifacts/{sandbox-id}/` is in the existing sandbox IAM policy.
   - Recommendation: Add `s3:PutObject` scoped to `arn:aws:s3:::km-sandbox-artifacts-ea554771/artifacts/${sandbox_id}/*` in the ec2spot module's IAM role policy and the ECS task role policy.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing stdlib (no external framework) |
| Config file | none — `go test ./...` from repo root |
| Quick run command | `go test ./sidecars/audit-log/... ./pkg/aws/... ./pkg/profile/... -count=1` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| OBSV-04 | FilesystemPolicy fields read by compiler and written to userdata/service_hcl templates | unit | `go test ./pkg/compiler/... -run TestFilesystem -count=1` | ❌ Wave 0 |
| OBSV-05 | UploadArtifacts skips oversized files, emits skipped events, returns remaining | unit | `go test ./pkg/aws/... -run TestUploadArtifacts -count=1` | ❌ Wave 0 |
| OBSV-05 | ArtifactsSpec parsed from profile YAML | unit | `go test ./pkg/profile/... -run TestArtifacts -count=1` | ❌ Wave 0 |
| OBSV-06 | ReplicationRegion in profile triggers replication config in service_hcl | unit | `go test ./pkg/compiler/... -run TestReplication -count=1` | ❌ Wave 0 |
| OBSV-07 | RedactingDestination replaces AWS key pattern with [REDACTED] | unit | `go test ./sidecars/audit-log/... -run TestRedact -count=1` | ❌ Wave 0 |
| OBSV-07 | RedactingDestination replaces SSM literal value with [REDACTED] | unit | `go test ./sidecars/audit-log/... -run TestRedactLiteral -count=1` | ❌ Wave 0 |
| OBSV-07 | RedactingDestination does not redact structural fields (sandbox_id, event_type) | unit | `go test ./sidecars/audit-log/... -run TestRedactStructural -count=1` | ❌ Wave 0 |
| PROV-13 | Spot poll loop bash included in EC2 user-data when spec.runtime.spot=true | unit | `go test ./pkg/compiler/... -run TestSpotPollLoop -count=1` | ❌ Wave 0 |
| MAIL-01 | SES domain identity Terraform module creates domain identity + DKIM records | manual (Terraform plan inspection) | manual-only — no Go code | ❌ Wave 0 (Terraform) |
| MAIL-02 | ProvisionSandboxEmail calls CreateEmailIdentity with correct address | unit | `go test ./pkg/aws/... -run TestProvisionSandboxEmail -count=1` | ❌ Wave 0 |
| MAIL-03 | SES IAM policy includes ses:SendEmail scoped to sandbox's From address | unit | `go test ./pkg/compiler/... -run TestSESIAM -count=1` | ❌ Wave 0 |
| MAIL-04 | SendLifecycleNotification sends email with sandbox ID in subject | unit | `go test ./pkg/aws/... -run TestSendLifecycle -count=1` | ❌ Wave 0 |
| MAIL-05 | SES receipt rule routes inbox mail to correct S3 prefix | manual (AWS console/CLI verify S3 receipt) | manual-only — SES delivery is end-to-end | manual-only |

### Sampling Rate

- **Per task commit:** `go test ./sidecars/audit-log/... ./pkg/aws/... ./pkg/profile/... -count=1`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `sidecars/audit-log/redact_test.go` — covers OBSV-07 redaction tests (new file; add to existing `auditlog_test` package)
- [ ] `pkg/aws/artifacts_test.go` — covers OBSV-05 upload logic
- [ ] `pkg/aws/ses_test.go` — covers MAIL-02, MAIL-04
- [ ] `pkg/compiler/filesystem_test.go` — covers OBSV-04 compiler output
- [ ] `infra/modules/ses/v1.0.0/` — Terraform module (no Go test; plan inspection only)

---

## Sources

### Primary (HIGH confidence)
- AWS EC2 documentation — Instance Metadata Service v2 token lifetime, `spot/termination-time` endpoint format (2-minute warning window)
- AWS ECS documentation — `readonlyRootFilesystem` in task definition container definitions, Fargate volume types (named volumes only, no `linuxParameters.tmpfs`)
- AWS SES documentation — `sesv2.CreateEmailIdentity`, `sesv2.SendEmail`, SES receipt rule S3 actions, sandbox mode limitations
- Codebase `sidecars/audit-log/auditlog.go` — `Destination` interface pattern, existing `AuditEvent` struct
- Codebase `pkg/lifecycle/teardown.go` — `TeardownCallbacks` struct, `ExecuteTeardown` function
- Codebase `pkg/aws/spot.go` — existing `TerminateSpotInstance`, `GetSpotInstanceID`
- Codebase `pkg/profile/types.go` — existing `FilesystemPolicy` struct, `Spec` struct for `ArtifactsSpec` placement
- Codebase `pkg/compiler/userdata.go` — EC2 user-data template structure, IMDSv2 token pattern
- Codebase `pkg/compiler/service_hcl.go` — ECS task definition template, container definition pattern
- Codebase `go.mod` — confirms `aws-sdk-go-v2/service/s3 v1.97.1` present; `sesv2` not yet added

### Secondary (MEDIUM confidence)
- AWS SDK Go v2 sesv2 package — `CreateEmailIdentity`, `SendEmail` type signatures (verified against SDK structure in go module cache)
- AWS Fargate documentation — ephemeral storage limits (20 GB default, up to 200 GB), volume scope behavior

### Tertiary (LOW confidence — flag for validation)
- SES sandbox mode exit process — requires AWS support ticket; timing varies per account. Mark as pre-requisite for MAIL-04 operator notifications.

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries already in go.mod or well-known AWS SDK v2 packages
- Architecture: HIGH — all integration points are identified in existing code; patterns follow established codebase conventions
- Pitfalls: HIGH for EC2/bind-mounts and SES sandbox mode (well-documented AWS behavior); MEDIUM for ECS tmpfs Fargate caveat (Fargate docs are ambiguous, recommend testing)
- Validation: HIGH — test framework and patterns established in Phase 3

**Research date:** 2026-03-22
**Valid until:** 2026-04-22 (30 days — AWS SES and ECS Fargate APIs are stable)
